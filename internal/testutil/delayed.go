package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// FakeClaudeAfter writes an executable script that mimics a `claude` that works for a while and then finishes
// successfully with the result "done". seconds is what `sleep` takes, for example "0.5".
func FakeClaudeAfter(t testing.TB, seconds string) string {
	t.Helper()
	script := fmt.Sprintf(`#!/bin/sh
cat > /dev/null
echo '{"type":"system","subtype":"init","session_id":"s-late"}'
sleep %s
echo '{"type":"result","subtype":"success","result":"done","session_id":"s-late","total_cost_usd":0}'
`, seconds)
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write delayed fake claude: %v", err)
	}
	return path
}
