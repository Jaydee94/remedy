package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// FakeClaudeStructured writes an executable script that mimics a `claude` that answers with structured
// output: its result event carries output (one line of JSON) as structured_output. Before that it
// reports its arguments and the files in its working directory in a probe event.
func FakeClaudeStructured(t testing.TB, output string) string {
	t.Helper()
	script := fmt.Sprintf(`#!/bin/sh
cat > /dev/null
args=$(printf '%%s ' "$@" | sed 's/\\/\\\\/g; s/"/\\"/g')
files=$(ls -A | tr '\n' ' ')
echo '{"type":"system","subtype":"init","session_id":"s-2"}'
echo "{\"type\":\"probe\",\"args\":\"$args\",\"files\":\"$files\"}"
cat <<'EOF'
{"type":"result","subtype":"success","result":"ok","session_id":"s-2","total_cost_usd":0.01,"structured_output":%s}
EOF
`, output)
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write structured fake claude: %v", err)
	}
	return path
}
