// Package testutil holds helpers shared by tests in several packages.
package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// FakeClaude writes an executable script that mimics `claude -p --output-format stream-json`
// and returns its path. It echoes the prompt it read from stdin and whether ANTHROPIC_API_KEY
// reached the process, writes one line to stderr, and exits with exitCode.
func FakeClaude(t testing.TB, exitCode int) string {
	t.Helper()
	script := fmt.Sprintf(`#!/bin/sh
prompt=$(cat)
echo '{"type":"system","subtype":"init","session_id":"s-1"}'
echo "{\"type\":\"probe\",\"prompt\":\"$prompt\",\"api_key\":\"${ANTHROPIC_API_KEY:-unset}\"}"
echo 'not json'
echo 'warn: something' >&2
echo '{"type":"result","subtype":"success","result":"done","session_id":"s-1","total_cost_usd":0.0123}'
exit %d
`, exitCode)
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	return path
}
