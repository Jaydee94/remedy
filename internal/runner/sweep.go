package runner

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// SweepWorkspaces removes what runs of an earlier runner left in the workspace root and reports how many directories
// it removed. A run normally cleans up after itself, but a runner that is killed (SIGKILL, power loss) cannot: the
// workspace stays, and so does the directory with the MCP config, whose file holds a run token. The token is
// worthless once the control plane has failed the run, yet it should not lie around.
//
// Only the runner at the start calls it, when no run of its own can exist. It removes real directories whose name
// begins with a run ID (32 lower-case hex characters, run.NewID) and a dash, which is what both os.MkdirTemp calls
// of the loop make, and nothing else: the root may be a directory the operator shares, and a symlink is never
// followed or removed. A second runner on the same root is not supported, it would lose its runs here.
func SweepWorkspaces(root string) (int, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	var errs []error
	for _, e := range entries {
		if !e.Type().IsDir() || !startsWithRunID(e.Name()) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			errs = append(errs, err)
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}

// startsWithRunID reports whether name is 32 lower-case hex characters followed by a dash and more.
func startsWithRunID(name string) bool {
	const idLen = 32
	if len(name) <= idLen+1 || name[idLen] != '-' {
		return false
	}
	for _, c := range name[:idLen] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
