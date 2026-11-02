package deploy_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// installArgs renders values and returns the arguments of the runner's install-cli init container.
func installArgs(t *testing.T, values map[string]any) []string {
	t.Helper()
	sts := mustRender(t, values).find("StatefulSet", "remedy-runner")
	if sts == nil {
		t.Fatal("no runner StatefulSet rendered")
	}
	raw, _ := container(t, podSpec(t, sts), "install-cli")["args"].([]any)
	args := make([]string, 0, len(raw))
	for _, a := range raw {
		args = append(args, strings.TrimSpace(toString(a)))
	}
	return args
}

// argValue returns the value of the first argument that starts with prefix, and whether there was one.
func argValue(args []string, prefix string) (string, bool) {
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			return strings.TrimPrefix(a, prefix), true
		}
	}
	return "", false
}

func TestTheChartShipsAPinnedCLI(t *testing.T) {
	values := baseValues()
	delete(values["runner"].(map[string]any), "cli")
	args := installArgs(t, values)

	version, _ := argValue(args, "--version=")
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(version) {
		t.Errorf("the default --version = %q, want a pinned version such as 2.1.316", version)
	}
	if url, _ := argValue(args, "--url-template="); !strings.HasPrefix(url, "https://") {
		t.Errorf("the default --url-template = %q, want an https URL", url)
	}
	sha := regexp.MustCompile(`^(amd64|arm64)=[a-z0-9-]+@([0-9a-f]{64})$`)
	seen := map[string]string{}
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "--platform="); ok {
			m := sha.FindStringSubmatch(v)
			if m == nil {
				t.Errorf("--platform=%s is not <arch>=<name>@<64 hex characters>", v)
				continue
			}
			seen[m[1]] = m[2]
		}
	}
	if len(seen) != 2 || seen["amd64"] == seen["arm64"] {
		t.Errorf("platforms = %v, want amd64 and arm64 with different checksums", seen)
	}
}

func TestAnOperatorsPinWinsOverTheDefault(t *testing.T) {
	args := installArgs(t, baseValues()) // baseValues pins 2.1.288 with checksums of 64 a's
	if v, _ := argValue(args, "--version="); v != "2.1.288" {
		t.Errorf("--version = %q, want the operator's 2.1.288", v)
	}
	if v, _ := argValue(args, "--platform=amd64="); v != "linux-x64@"+strings.Repeat("a", 64) {
		t.Errorf("--platform=amd64 = %q, want the operator's checksum", v)
	}
}

// scripts/cli-checksums.sh rewrites the pin in place and refuses a file that has anything but exactly one line of each
// of these shapes (its re_version, re_url, re_amd64 and re_arm64). A second lookalike line in values.yaml would make the
// next Renovate bump fail loudly; this test makes it fail earlier. The shapes here are stricter than the script's: the
// lines must also be filled in, because the pin is the chart's default.
func TestThePinInValuesHasOneLineOfEachShape(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(chartDir, "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for name, re := range map[string]string{
		"version":     `(?m)^ *version: *"[0-9][0-9.]*"`,
		"urlTemplate": `(?m)^ *urlTemplate: *"https://[^"]*"`,
		"amd64":       `(?m)^ *amd64: *\{name: *"[^"]*", *sha256: *"[0-9a-f]{64}"`,
		"arm64":       `(?m)^ *arm64: *\{name: *"[^"]*", *sha256: *"[0-9a-f]{64}"`,
	} {
		if n := len(regexp.MustCompile(re).FindAll(b, -1)); n != 1 {
			t.Errorf("values.yaml has %d lines of the shape %q (%s), want exactly 1", n, name, re)
		}
	}
}
