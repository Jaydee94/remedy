package snapshot_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/snapshot"
)

type entry struct {
	name string
	typ  byte
	body string
	link string
	mode int64
}

func file(name, body string) entry {
	return entry{name: name, typ: tar.TypeReg, body: body, mode: 0o644}
}

func dir(name string) entry {
	return entry{name: name, typ: tar.TypeDir, mode: 0o755}
}

func symlink(name, target string) entry {
	return entry{name: name, typ: tar.TypeSymlink, link: target, mode: 0o777}
}

// archive builds a gzipped tar with the names exactly as given, so that hostile names are possible.
func archive(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: e.mode, Linkname: e.link}
		if e.typ == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if e.typ == tar.TypeXGlobalHeader {
			h.PAXRecords = map[string]string{"comment": "913da1edbf28ced7b324b5b99ab3c6c61241acee"}
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			if _, err := io.WriteString(tw, e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// names lists the entries of a gzipped tar.
func names(t *testing.T, gz []byte) []string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	var out []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, h.Name)
	}
}

// tree lists every path below dir (files, directories and symlinks), relative and sorted.
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel != "." {
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func TestIsSecret(t *testing.T) {
	for name, want := range map[string]bool{
		".env": true, ".env.local": true, "app/.env.production": true, "keys/server.pem": true, "CERT.PEM": true,
		"tls.key": true, "id_rsa": true, "home/.ssh/id_rsa.pub": true, "ID_RSA": true,
		"main.go": false, "README.md": false, "environment.go": false, "keyboard.go": false, "key": false, "docs/pem.md": false,
	} {
		if got := snapshot.IsSecret(name); got != want {
			t.Errorf("IsSecret(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestFilterKeepsTheTreeAndDropsSecretsAndOddEntries(t *testing.T) {
	src := archive(t,
		entry{name: "pax_global_header", typ: tar.TypeXGlobalHeader},
		dir("Octo-hello-913da1e/"),
		file("Octo-hello-913da1e/main.go", "package main\n"),
		file("Octo-hello-913da1e/README.md", "# hello\n"),
		dir("Octo-hello-913da1e/keys/"),
		file("Octo-hello-913da1e/.env", "TOKEN=x"),
		file("Octo-hello-913da1e/.env.local", "TOKEN=y"),
		file("Octo-hello-913da1e/keys/server.pem", "-----BEGIN"),
		file("Octo-hello-913da1e/keys/id_rsa", "key"),
		file("Octo-hello-913da1e/tls.key", "key"),
		entry{name: "Octo-hello-913da1e/hardlink", typ: tar.TypeLink, link: "Octo-hello-913da1e/main.go"},
		entry{name: "Octo-hello-913da1e/pipe", typ: tar.TypeFifo},
		symlink("Octo-hello-913da1e/docs", "README.md"),
	)
	var out bytes.Buffer
	if err := snapshot.Filter(&out, bytes.NewReader(src), snapshot.Limits{}); err != nil {
		t.Fatal(err)
	}
	got := names(t, out.Bytes())
	want := []string{"Octo-hello-913da1e/", "Octo-hello-913da1e/main.go", "Octo-hello-913da1e/README.md", "Octo-hello-913da1e/keys/", "Octo-hello-913da1e/docs"}
	if !slices.Equal(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
}

func TestFilterEnforcesTheLimits(t *testing.T) {
	big := archive(t, dir("r/"), file("r/a", strings.Repeat("x", 11)))
	if err := snapshot.Filter(io.Discard, bytes.NewReader(big), snapshot.Limits{MaxBytes: 10}); !errors.Is(err, snapshot.ErrTooLarge) {
		t.Errorf("11 bytes with a limit of 10: error = %v", err)
	}
	many := archive(t, dir("r/"), file("r/a", ""), file("r/b", ""), file("r/c", ""))
	if err := snapshot.Filter(io.Discard, bytes.NewReader(many), snapshot.Limits{MaxFiles: 3}); !errors.Is(err, snapshot.ErrTooLarge) {
		t.Errorf("4 entries with a limit of 3: error = %v", err)
	}
	if err := snapshot.Filter(io.Discard, bytes.NewReader(many), snapshot.Limits{MaxFiles: 4}); err != nil {
		t.Errorf("4 entries with a limit of 4: error = %v", err)
	}
}

func TestFilterRejectsUnsafeNamesAndGarbage(t *testing.T) {
	for _, name := range []string{"r/../../etc/passwd", "/etc/passwd"} {
		src := archive(t, file(name, "x"))
		if err := snapshot.Filter(io.Discard, bytes.NewReader(src), snapshot.Limits{}); !errors.Is(err, snapshot.ErrUnsafe) {
			t.Errorf("%q: error = %v, want ErrUnsafe", name, err)
		}
	}
	if err := snapshot.Filter(io.Discard, strings.NewReader("not a gzip stream"), snapshot.Limits{}); err == nil {
		t.Error("garbage was accepted")
	}
	good := archive(t, dir("r/"), file("r/a", strings.Repeat("x", 5000)))
	if err := snapshot.Filter(io.Discard, bytes.NewReader(good[:len(good)/2]), snapshot.Limits{}); err == nil {
		t.Error("a truncated archive was accepted")
	}
}

func TestUnpackStripsTheTopLevelDirectoryAndKeepsTheExecutableBit(t *testing.T) {
	d := t.TempDir()
	src := archive(t,
		entry{name: "pax_global_header", typ: tar.TypeXGlobalHeader},
		dir("Octo-hello-913da1e/"),
		dir("Octo-hello-913da1e/cmd/"),
		file("Octo-hello-913da1e/cmd/main.go", "package main\n"),
		entry{name: "Octo-hello-913da1e/build.sh", typ: tar.TypeReg, body: "#!/bin/sh\n", mode: 0o755},
		file("Octo-hello-913da1e/README.md", "# hello\n"),
	)
	res, err := snapshot.Unpack(d, bytes.NewReader(src), snapshot.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := tree(t, d), []string{"README.md", "build.sh", "cmd", "cmd/main.go"}; !slices.Equal(got, want) {
		t.Fatalf("tree = %v, want %v", got, want)
	}
	b, _ := os.ReadFile(filepath.Join(d, "cmd", "main.go"))
	if string(b) != "package main\n" {
		t.Errorf("content = %q", b)
	}
	if info, _ := os.Stat(filepath.Join(d, "build.sh")); info.Mode()&0o100 == 0 {
		t.Error("the executable bit was lost")
	}
	if info, _ := os.Stat(filepath.Join(d, "README.md")); info.Mode().Perm() != 0o600 {
		t.Errorf("a plain file has mode %v, want 0600", info.Mode().Perm())
	}
	if res.Files != 3 || res.Bytes != int64(len("package main\n")+len("#!/bin/sh\n")+len("# hello\n")) || len(res.Skipped) != 0 {
		t.Errorf("result = %+v", res)
	}
}

func TestUnpackRefusesPathTraversalAndWritesNothingOutside(t *testing.T) {
	parent := t.TempDir()
	d := filepath.Join(parent, "work")
	if err := os.Mkdir(d, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"r/../evil.txt", "r/a/../../evil.txt", "../evil.txt", "/tmp/evil.txt"} {
		src := archive(t, dir("r/"), file(name, "pwned"))
		if _, err := snapshot.Unpack(d, bytes.NewReader(src), snapshot.Limits{}); !errors.Is(err, snapshot.ErrUnsafe) {
			t.Errorf("%q: error = %v, want ErrUnsafe", name, err)
		}
	}
	if got := tree(t, parent); !slices.Equal(got, []string{"work"}) {
		t.Fatalf("something was written outside the workspace: %v", got)
	}
}

func TestUnpackExtractsOnlySymlinksThatCannotLeadOut(t *testing.T) {
	d := t.TempDir()
	src := archive(t,
		dir("r/"),
		dir("r/sub/"),
		file("r/sub/file.txt", "inside"),
		symlink("r/ok", "sub/file.txt"),
		symlink("r/here", "."),
		symlink("r/abs", "/etc/passwd"),
		symlink("r/up", "../outside"),
		symlink("r/sneaky", "here/.."),
		symlink("r/empty", ""),
	)
	res, err := snapshot.Unpack(d, bytes.NewReader(src), snapshot.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Readlink(filepath.Join(d, "ok")); got != "sub/file.txt" {
		t.Errorf("the harmless link is %q", got)
	}
	for _, name := range []string{"abs", "up", "sneaky", "empty"} {
		if _, err := os.Lstat(filepath.Join(d, name)); err == nil {
			t.Errorf("the symlink %q was extracted", name)
		}
	}
	slices.Sort(res.Skipped)
	if want := []string{"abs", "empty", "sneaky", "up"}; !slices.Equal(res.Skipped, want) {
		t.Errorf("skipped = %v, want %v", res.Skipped, want)
	}
}

// A symlink that points to "." and a second one through it must not reach the parent directory.
func TestUnpackCannotEscapeThroughChainedSymlinks(t *testing.T) {
	parent := t.TempDir()
	d := filepath.Join(parent, "work")
	if err := os.Mkdir(d, 0o700); err != nil {
		t.Fatal(err)
	}
	src := archive(t,
		dir("r/"),
		symlink("r/d1", "."),
		symlink("r/l1", "d1/.."),
		file("r/d1/inside.txt", "ok"),
	)
	if _, err := snapshot.Unpack(d, bytes.NewReader(src), snapshot.Limits{}); err != nil {
		t.Fatal(err)
	}
	if got := tree(t, parent); !slices.Equal(got, []string{"work", "work/d1", "work/inside.txt"}) {
		t.Fatalf("tree = %v: nothing may exist outside the workspace and l1 must be missing", got)
	}
}

// Unpack only creates harmless symlinks itself, but the directory it is given might not be empty. A symlink
// that is already there and leads out must not be written through.
func TestUnpackNeverWritesThroughASymlinkThatAlreadyLeadsOut(t *testing.T) {
	parent := t.TempDir()
	outside := filepath.Join(parent, "outside")
	d := filepath.Join(parent, "work")
	for _, p := range []string{outside, d} {
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(d, "link")); err != nil {
		t.Fatal(err)
	}
	src := archive(t, dir("r/"), file("r/link/pwned.txt", "pwned"))
	if _, err := snapshot.Unpack(d, bytes.NewReader(src), snapshot.Limits{}); !errors.Is(err, snapshot.ErrUnsafe) {
		t.Fatalf("error = %v, want ErrUnsafe", err)
	}
	if got := tree(t, outside); len(got) != 0 {
		t.Fatalf("something was written outside the workspace: %v", got)
	}
}

func TestUnpackEnforcesTheLimits(t *testing.T) {
	src := archive(t, dir("r/"), file("r/a", strings.Repeat("x", 11)))
	if _, err := snapshot.Unpack(t.TempDir(), bytes.NewReader(src), snapshot.Limits{MaxBytes: 10}); !errors.Is(err, snapshot.ErrTooLarge) {
		t.Errorf("size: error = %v", err)
	}
	many := archive(t, dir("r/"), file("r/a", ""), file("r/b", ""))
	if _, err := snapshot.Unpack(t.TempDir(), bytes.NewReader(many), snapshot.Limits{MaxFiles: 2}); !errors.Is(err, snapshot.ErrTooLarge) {
		t.Errorf("count: error = %v", err)
	}
}

func TestUnpackLeavesSecretFilesOutEvenIfTheSenderDidNot(t *testing.T) {
	d := t.TempDir()
	src := archive(t, dir("r/"), file("r/main.go", "x"), file("r/.env", "TOKEN=x"), file("r/deploy/id_rsa", "key"))
	if _, err := snapshot.Unpack(d, bytes.NewReader(src), snapshot.Limits{}); err != nil {
		t.Fatal(err)
	}
	if got := tree(t, d); !slices.Equal(got, []string{"main.go"}) {
		t.Fatalf("tree = %v", got)
	}
}

func TestUnpackRefusesADuplicateFileAndABrokenStream(t *testing.T) {
	dup := archive(t, dir("r/"), file("r/a", "one"), file("r/a", "two"))
	if _, err := snapshot.Unpack(t.TempDir(), bytes.NewReader(dup), snapshot.Limits{}); err == nil {
		t.Error("a file that appears twice was accepted")
	}
	if _, err := snapshot.Unpack(t.TempDir(), strings.NewReader("garbage"), snapshot.Limits{}); err == nil {
		t.Error("garbage was accepted")
	}
	good := archive(t, dir("r/"), file("r/a", strings.Repeat("x", 5000)))
	if _, err := snapshot.Unpack(t.TempDir(), bytes.NewReader(good[:len(good)/2]), snapshot.Limits{}); err == nil {
		t.Error("a truncated archive was accepted")
	}
	if _, err := snapshot.Unpack(filepath.Join(t.TempDir(), "missing"), bytes.NewReader(good), snapshot.Limits{}); err == nil {
		t.Error("a missing directory was accepted")
	}
}

func TestFilterThenUnpackGivesTheSameTreeWithoutSecrets(t *testing.T) {
	src := archive(t,
		entry{name: "pax_global_header", typ: tar.TypeXGlobalHeader},
		dir("o-r-abc1234/"), dir("o-r-abc1234/a/"), dir("o-r-abc1234/a/b/"),
		file("o-r-abc1234/a/b/c.txt", "deep"), file("o-r-abc1234/a/.env", "x"), file("o-r-abc1234/top.txt", "top"),
	)
	var filtered bytes.Buffer
	if err := snapshot.Filter(&filtered, bytes.NewReader(src), snapshot.Limits{}); err != nil {
		t.Fatal(err)
	}
	d := t.TempDir()
	if _, err := snapshot.Unpack(d, &filtered, snapshot.Limits{}); err != nil {
		t.Fatal(err)
	}
	if got, want := tree(t, d), []string{"a", "a/b", "a/b/c.txt", "top.txt"}; !slices.Equal(got, want) {
		t.Fatalf("tree = %v, want %v", got, want)
	}
}
