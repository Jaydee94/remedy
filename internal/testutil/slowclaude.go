package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// SlowClaude writes an executable script that mimics a `claude` that hangs: it prints one event and
// then sleeps far longer than any test waits, so that a run timeout has something to stop. It uses
// exec, so that killing the script kills the sleep and closes its output.
func SlowClaude(t testing.TB) string {
	t.Helper()
	script := `#!/bin/sh
cat > /dev/null
echo '{"type":"system","subtype":"init","session_id":"s-slow"}'
exec sleep 60
`
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write slow claude: %v", err)
	}
	return path
}
