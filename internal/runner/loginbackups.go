package runner

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
)

// backupName matches the file the CLI's login leaves in its backups directory: one per attempt, named by the
// millisecond it started.
var backupName = regexp.MustCompile(`^\.claude\.json\.backup\.([0-9]{13})$`)

// PruneBackups keeps the keep newest login config backups in dir, by the number in the NAME (never the mtime), and
// removes the older ones. It lists dir with os.ReadDir and removes by name: it never opens, reads or copies any file,
// and it touches no other entry (a directory, a link or any name that does not match exactly stays). A missing dir is
// (0, nil). The first removal error is returned after all removals were tried.
func PruneBackups(dir string, keep int) (removed int, err error) {
	if keep < 0 {
		return 0, errors.New("negative number of backups to keep")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	type backup struct {
		name string
		n    int64
	}
	var found []backup
	for _, e := range entries {
		m := backupName.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		n, perr := strconv.ParseInt(m[1], 10, 64)
		if perr != nil {
			continue
		}
		found = append(found, backup{name: e.Name(), n: n})
	}
	if len(found) <= keep {
		return 0, nil
	}
	sort.Slice(found, func(i, j int) bool { return found[i].n > found[j].n })
	var first error
	for _, b := range found[keep:] {
		if rerr := os.Remove(filepath.Join(dir, b.name)); rerr != nil {
			if first == nil && !errors.Is(rerr, fs.ErrNotExist) {
				first = rerr
			}
			continue
		}
		removed++
	}
	return removed, first
}
