package runner_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/runner"
)

const backupPrefix = ".claude.json.backup."

func writeFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func TestPruneKeepsTheFiveNewestByNameNotMtime(t *testing.T) {
	dir := t.TempDir()
	numbers := []string{
		"1791355592189", "1791358412198", "1791358412199", "1791358412200",
		"1791358412201", "1791358412202", "1791358412203",
	}
	base := time.Now()
	for i, n := range numbers {
		writeFile(t, dir, backupPrefix+n)
		// The lowest number gets the newest mtime: sorting by mtime would keep the wrong files.
		mt := base.Add(-time.Duration(i) * time.Hour)
		if err := os.Chtimes(filepath.Join(dir, backupPrefix+n), mt, mt); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := runner.PruneBackups(dir, 5)
	if err != nil {
		t.Fatalf("PruneBackups: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	var want []string
	for _, n := range numbers[2:] {
		want = append(want, backupPrefix+n)
	}
	got := names(t, dir)
	if len(got) != len(want) {
		t.Fatalf("left %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("left %v, want %v", got, want)
		}
	}
}

func TestPruneRemovesOnlyMatchingNames(t *testing.T) {
	dir := t.TempDir()
	keepNames := []string{
		".claude.json",
		".claude.json.backup.123",
		".claude.json.backup.1234567890123x",
		"x.claude.json.backup.1234567890123",
		".claude.json.backup.12345678901234",
	}
	for _, n := range keepNames {
		writeFile(t, dir, n)
	}
	// A directory named like a match stays, even though it is the oldest.
	dirMatch := backupPrefix + "1000000000000"
	if err := os.Mkdir(filepath.Join(dir, dirMatch), 0o700); err != nil {
		t.Fatal(err)
	}
	keepNames = append(keepNames, dirMatch)
	// Three real matches, keep one: two go.
	for _, n := range []string{"1791358412198", "1791358412199", "1791358412200"} {
		writeFile(t, dir, backupPrefix+n)
	}

	removed, err := runner.PruneBackups(dir, 1)
	if err != nil {
		t.Fatalf("PruneBackups: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	want := append([]string{}, keepNames...)
	want = append(want, backupPrefix+"1791358412200")
	sort.Strings(want)
	got := names(t, dir)
	if len(got) != len(want) {
		t.Fatalf("left %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("left %v, want %v", got, want)
		}
	}
}

func TestPruneNeverReadsAFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can open a file of mode 0000")
	}
	dir := t.TempDir()
	for _, n := range []string{"1791358412198", "1791358412199", "1791358412200", "1791358412201"} {
		writeFile(t, dir, backupPrefix+n)
	}
	writeFile(t, dir, ".claude.json")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.Chmod(filepath.Join(dir, e.Name()), 0o000); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := runner.PruneBackups(dir, 2)
	if err != nil {
		t.Fatalf("PruneBackups: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	got := names(t, dir)
	want := []string{".claude.json", backupPrefix + "1791358412200", backupPrefix + "1791358412201"}
	if len(got) != len(want) {
		t.Fatalf("left %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("left %v, want %v", got, want)
		}
	}
}

func TestPruneOfAMissingDirectory(t *testing.T) {
	removed, err := runner.PruneBackups(filepath.Join(t.TempDir(), "backups"), 5)
	if err != nil || removed != 0 {
		t.Fatalf("PruneBackups = (%d, %v), want (0, nil)", removed, err)
	}
}
