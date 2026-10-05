package runner_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/runner"
)

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	t.Fatalf("lstat %s: %v", path, err)
	return false
}

func TestSweepRemovesWhatARunLeftBehindAndNothingElse(t *testing.T) {
	root := t.TempDir()
	id := run.NewID()

	workspace := filepath.Join(root, id+"-123456")
	configDir := filepath.Join(root, id+"-mcp-654321")
	for _, dir := range []string{workspace, configDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("left behind"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "mcp.json"), []byte(`{"token":"dead"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// None of these is a directory of a run, so none may go: the root can be a directory the operator shares.
	keep := []string{
		filepath.Join(root, "notes"),                   // a directory with another name
		filepath.Join(root, "shared-"+id),              // the ID is not at the start
		filepath.Join(root, id[:31]+"-123456"),         // one character short of an ID
		filepath.Join(root, "ABCDEF"+id[6:]+"-123456"), // upper case is not what NewID makes
		filepath.Join(root, id),                        // an ID without the suffix MkdirTemp adds
	}
	for _, dir := range keep {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(root, id+"-a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	link := filepath.Join(root, id+"-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	removed, err := runner.SweepWorkspaces(root)
	if err != nil {
		t.Fatalf("SweepWorkspaces: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2 (the workspace and the config directory)", removed)
	}
	if exists(t, workspace) || exists(t, configDir) {
		t.Error("the directories of the dead run are still there")
	}
	for _, p := range append(keep, file, link, outside) {
		if !exists(t, p) {
			t.Errorf("%s was removed, it is not a directory of a run", p)
		}
	}
}

func TestSweepOfAnEmptyOrMissingRootIsNotAnError(t *testing.T) {
	if n, err := runner.SweepWorkspaces(t.TempDir()); err != nil || n != 0 {
		t.Errorf("empty root: removed %d, err %v", n, err)
	}
	if n, err := runner.SweepWorkspaces(filepath.Join(t.TempDir(), "missing")); err != nil || n != 0 {
		t.Errorf("missing root: removed %d, err %v", n, err)
	}
}
