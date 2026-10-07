package kube

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseNamespaces(t *testing.T) {
	got, err := ParseNamespaces(" demo, other ,,demo,kube-test ")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"demo", "other", "kube-test"}; !slices.Equal(got, want) {
		t.Fatalf("namespaces = %v, want %v", got, want)
	}
	if got, err := ParseNamespaces(""); err != nil || len(got) != 0 {
		t.Fatalf("empty list = %v, %v", got, err)
	}
	for _, bad := range []string{"Demo", "demo*", "-demo", "de mo", "demo/other", strings.Repeat("a", 64)} {
		if _, err := ParseNamespaces(bad); err == nil {
			t.Errorf("ParseNamespaces(%q) accepted a bad namespace", bad)
		}
	}
}

func TestWhatIsEnabled(t *testing.T) {
	cases := []struct {
		name        string
		c           Config
		read, write bool
	}{
		{"nothing", Config{}, false, false},
		{"read only", Config{ReadTokenFile: "r"}, true, false},
		{"write token without namespaces", Config{ReadTokenFile: "r", WriteTokenFile: "w"}, true, false},
		{"namespaces without a write token", Config{ReadTokenFile: "r", WriteNamespaces: []string{"demo"}}, true, false},
		{"all", Config{ReadTokenFile: "r", WriteTokenFile: "w", WriteNamespaces: []string{"demo"}}, true, true},
	}
	for _, tc := range cases {
		if tc.c.ReadEnabled() != tc.read || tc.c.WriteEnabled() != tc.write {
			t.Errorf("%s: read=%v write=%v, want %v %v", tc.name, tc.c.ReadEnabled(), tc.c.WriteEnabled(), tc.read, tc.write)
		}
	}
	c := Config{WriteNamespaces: []string{"demo"}}
	if !c.NamespaceAllowed("demo") || c.NamespaceAllowed("other") || c.NamespaceAllowed("") {
		t.Fatal("NamespaceAllowed is not exactly the list")
	}
}

func TestValidate(t *testing.T) {
	read := writeFile(t, "read", "read-token\n")
	write := writeFile(t, "write", "write-token\n")
	empty := writeFile(t, "empty", "  \n")
	notPEM := writeFile(t, "ca.crt", "this is not a certificate")

	ok := Config{API: "https://k8s.example:6443", ReadTokenFile: read, WriteTokenFile: write, WriteNamespaces: []string{"demo"}, ArgoNamespace: "argocd"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a good configuration is refused: %v", err)
	}
	if err := (Config{}).Validate(); err != nil {
		t.Fatalf("no cluster at all must be fine: %v", err)
	}

	bad := map[string]Config{
		"a write token without a read token": {API: ok.API, WriteTokenFile: write, WriteNamespaces: []string{"demo"}},
		"namespaces without a write token":   {API: ok.API, ReadTokenFile: read, WriteNamespaces: []string{"demo"}},
		"an API that is not a URL":           {API: "k8s.example", ReadTokenFile: read},
		"an API with another scheme":         {API: "ftp://k8s.example", ReadTokenFile: read},
		"a token file that is missing":       {API: ok.API, ReadTokenFile: filepath.Join(t.TempDir(), "nope")},
		"a token file without a token":       {API: ok.API, ReadTokenFile: empty},
		"a CA file that is not PEM":          {API: ok.API, ReadTokenFile: read, CAFile: notPEM},
		"a CA file that is missing":          {API: ok.API, ReadTokenFile: read, CAFile: filepath.Join(t.TempDir(), "nope")},
		"a bad namespace in the allowlist":   {API: ok.API, ReadTokenFile: read, WriteTokenFile: write, WriteNamespaces: []string{"De mo"}},
		"a bad Argo CD namespace":            {API: ok.API, ReadTokenFile: read, ArgoNamespace: "Argo CD"},
	}
	for name, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestValidateNeverShowsATokenInItsError(t *testing.T) {
	// A token file whose content is not a token problem: the error names the file, never the content.
	path := writeFile(t, "tok", "s3cr3t-token-value")
	err := Config{API: "ftp://x", ReadTokenFile: path}.Validate()
	if err == nil || strings.Contains(err.Error(), "s3cr3t-token-value") {
		t.Fatalf("error = %v", err)
	}
}

func TestWarnings(t *testing.T) {
	read := writeFile(t, "read", "r")
	write := writeFile(t, "write", "w")
	if w := (Config{ReadTokenFile: read, WriteTokenFile: write}).Warnings(); len(w) != 1 || !strings.Contains(w[0], "REMEDY_K8S_WRITE_NAMESPACES") {
		t.Fatalf("warnings = %v", w)
	}
	if w := (Config{ReadTokenFile: read, WriteTokenFile: write, WriteNamespaces: []string{"demo"}}).Warnings(); len(w) != 0 {
		t.Fatalf("warnings = %v", w)
	}
	if w := (Config{ReadTokenFile: read}).Warnings(); len(w) != 0 {
		t.Fatalf("warnings = %v", w)
	}
}

func TestValidateAcceptsAWriteTokenThatIsNotThereYet(t *testing.T) {
	read := writeFile(t, "read", "read-token\n")
	api := "https://k8s.example:6443"
	for name, file := range map[string]string{
		"missing": filepath.Join(t.TempDir(), "not-yet"),
		"empty":   writeFile(t, "empty", "\n"),
	} {
		c := Config{API: api, ReadTokenFile: read, WriteTokenFile: file, WriteNamespaces: []string{"demo"}}
		if err := c.Validate(); err != nil {
			t.Errorf("a %s write token file must be accepted at start-up, got %v", name, err)
		}
	}

	// What is there but wrong stays a mistake: two lines, or a path that is a directory.
	for name, file := range map[string]string{
		"two lines": writeFile(t, "two", "a\nb\n"),
		"directory": t.TempDir(),
	} {
		c := Config{API: api, ReadTokenFile: read, WriteTokenFile: file, WriteNamespaces: []string{"demo"}}
		if err := c.Validate(); err == nil {
			t.Errorf("a write token file that is a %s must still be refused", name)
		}
	}

	// The read token is as strict as before.
	c := Config{API: api, ReadTokenFile: filepath.Join(t.TempDir(), "not-yet")}
	if err := c.Validate(); err == nil {
		t.Error("a missing read token file must still be refused")
	}
}

func TestWarningsSayWhenTheWriteTokenIsNotThereYet(t *testing.T) {
	read := writeFile(t, "read", "r")
	base := Config{ReadTokenFile: read, WriteNamespaces: []string{"demo"}}
	for name, file := range map[string]string{
		"missing": filepath.Join(t.TempDir(), "not-yet"),
		"empty":   writeFile(t, "empty", " \n"),
	} {
		c := base
		c.WriteTokenFile = file
		w := c.Warnings()
		if len(w) != 1 || !strings.Contains(w[0], "REMEDY_K8S_WRITE_TOKEN_FILE") || !strings.Contains(w[0], "fail") {
			t.Errorf("%s: warnings = %v, want one that says actions fail until the file holds a token", name, w)
		}
	}
	c := base
	c.WriteTokenFile = writeFile(t, "write", "write-token\n")
	if w := c.Warnings(); len(w) != 0 {
		t.Errorf("a write token that is there: warnings = %v, want none", w)
	}
}
