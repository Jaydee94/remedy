package cliinstall_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/cliinstall"
)

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func tarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// serve starts an https server with the files and returns options that point at it.
func serve(t *testing.T, files map[string][]byte) cliinstall.Options {
	t.Helper()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(ts.Close)
	return cliinstall.Options{
		URLTemplate: ts.URL + "/{version}/{platform}/claude",
		Version:     "1.2.3",
		GOARCH:      "arm64",
		Dest:        t.TempDir(),
		Name:        "claude",
		Archive:     "none",
		HTTP:        ts.Client(),
	}
}

func installed(t *testing.T, dest string) []string {
	t.Helper()
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestInstallDownloadsVerifiesAndInstallsARawBinary(t *testing.T) {
	binary := []byte("#!/bin/sh\necho claude 1.2.3\n")
	o := serve(t, map[string][]byte{"/1.2.3/linux-arm64/claude": binary})
	o.Platforms = map[string]cliinstall.Platform{
		"amd64": {Name: "linux-x64", SHA256: strings.Repeat("0", 64)},
		"arm64": {Name: "linux-arm64", SHA256: sum(binary)},
	}
	path, err := cliinstall.Install(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(o.Dest, "claude") {
		t.Fatalf("path = %q", path)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, binary) {
		t.Fatalf("installed content = %q (%v)", got, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755", info.Mode().Perm())
	}
	if names := installed(t, o.Dest); len(names) != 1 {
		t.Fatalf("the directory holds %v, want only claude", names)
	}
}

func TestInstallPicksThePlatformOfTheArchitecture(t *testing.T) {
	x64, arm := []byte("x64 binary"), []byte("arm binary")
	o := serve(t, map[string][]byte{"/1.2.3/linux-x64/claude": x64, "/1.2.3/linux-arm64/claude": arm})
	o.Platforms = map[string]cliinstall.Platform{
		"amd64": {Name: "linux-x64", SHA256: sum(x64)},
		"arm64": {Name: "linux-arm64", SHA256: sum(arm)},
	}
	o.GOARCH = "amd64"
	path, err := cliinstall.Install(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, x64) {
		t.Fatalf("installed %q on amd64", got)
	}
}

func TestInstallLeavesNothingWhenTheChecksumDiffers(t *testing.T) {
	o := serve(t, map[string][]byte{"/1.2.3/linux-arm64/claude": []byte("tampered")})
	o.Platforms = map[string]cliinstall.Platform{"arm64": {Name: "linux-arm64", SHA256: sum([]byte("the real one"))}}
	_, err := cliinstall.Install(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v, want a checksum error", err)
	}
	if names := installed(t, o.Dest); len(names) != 0 {
		t.Fatalf("the directory holds %v after a failed install, want nothing", names)
	}
}

func TestInstallTakesTheMemberOutOfATarGz(t *testing.T) {
	binary := []byte("the cli")
	archive := tarGz(t, map[string][]byte{"package/README": []byte("read me"), "package/bin/claude": binary})
	o := serve(t, map[string][]byte{"/1.2.3/linux-arm64/claude": archive})
	o.Archive, o.Member = "tar.gz", "claude"
	o.Platforms = map[string]cliinstall.Platform{"arm64": {Name: "linux-arm64", SHA256: sum(archive)}}
	path, err := cliinstall.Install(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, binary) {
		t.Fatalf("installed %q", got)
	}
	if names := installed(t, o.Dest); len(names) != 1 {
		t.Fatalf("the directory holds %v, want only claude", names)
	}
}

func TestInstallFailsWhenTheArchiveHasNoSuchMember(t *testing.T) {
	archive := tarGz(t, map[string][]byte{"package/other": []byte("x")})
	o := serve(t, map[string][]byte{"/1.2.3/linux-arm64/claude": archive})
	o.Archive, o.Member = "tar.gz", "claude"
	o.Platforms = map[string]cliinstall.Platform{"arm64": {Name: "linux-arm64", SHA256: sum(archive)}}
	if _, err := cliinstall.Install(context.Background(), o); err == nil {
		t.Fatal("expected an error")
	}
	if names := installed(t, o.Dest); len(names) != 0 {
		t.Fatalf("the directory holds %v, want nothing", names)
	}
}

func TestInstallRefusesWhatItCannotTrust(t *testing.T) {
	binary := []byte("x")
	good := func() cliinstall.Options {
		o := serve(t, map[string][]byte{"/1.2.3/linux-arm64/claude": binary})
		o.Platforms = map[string]cliinstall.Platform{"arm64": {Name: "linux-arm64", SHA256: sum(binary)}}
		return o
	}
	plain := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(plain.Close)

	cases := map[string]func(*cliinstall.Options){
		"an http URL":             func(o *cliinstall.Options) { o.URLTemplate = plain.URL + "/{version}/{platform}" },
		"a version with a path":   func(o *cliinstall.Options) { o.Version = "../1.2.3" },
		"an empty version":        func(o *cliinstall.Options) { o.Version = "" },
		"an architecture unknown": func(o *cliinstall.Options) { o.GOARCH = "riscv64" },
		"a checksum too short": func(o *cliinstall.Options) {
			o.Platforms["arm64"] = cliinstall.Platform{Name: "linux-arm64", SHA256: "abc"}
		},
		"a name with a slash":     func(o *cliinstall.Options) { o.Name = "../claude" },
		"an unknown archive":      func(o *cliinstall.Options) { o.Archive = "zip" },
		"a tar.gz without member": func(o *cliinstall.Options) { o.Archive, o.Member = "tar.gz", "" },
		"an unknown file":         func(o *cliinstall.Options) { o.Version = "9.9.9" },
	}
	for name, change := range cases {
		o := good()
		change(&o)
		if _, err := cliinstall.Install(context.Background(), o); err == nil {
			t.Errorf("%s: expected an error", name)
		}
		if names := installed(t, o.Dest); len(names) != 0 {
			t.Errorf("%s: the directory holds %v, want nothing", name, names)
		}
	}
}

func TestInstallRefusesARedirectToPlainHTTPEvenWithAnInjectedClient(t *testing.T) {
	binary := []byte("x")
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(binary) }))
	t.Cleanup(plain.Close)
	o := serve(t, nil)
	redirecting := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/claude", http.StatusFound)
	}))
	t.Cleanup(redirecting.Close)
	o.URLTemplate = redirecting.URL + "/{version}/{platform}"
	o.HTTP = redirecting.Client()
	o.Platforms = map[string]cliinstall.Platform{"arm64": {Name: "linux-arm64", SHA256: sum(binary)}}
	if _, err := cliinstall.Install(context.Background(), o); err == nil {
		t.Fatal("expected the redirect to http to be refused")
	}
	if names := installed(t, o.Dest); len(names) != 0 {
		t.Fatalf("the directory holds %v, want nothing", names)
	}
}
