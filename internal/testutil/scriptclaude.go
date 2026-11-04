package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// ScriptClaude writes an executable shell script with the given body (after a `#!/bin/sh` line) as a fake `claude` and
// returns its path. It reads nothing from stdin by itself: a script that needs the prompt says `cat`.
func ScriptClaude(t testing.TB, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	return path
}
