package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// RecordingClaude writes an executable script that mimics a `claude` that finishes at once and writes what it was
// started with to outFile, one key=value per line: its arguments (args), its working directory (cwd), the path
// of its --mcp-config file (cfg) and, when there is one, that file's mode (mode), its directory's mode (dirmode)
// and its content (body).
func RecordingClaude(t testing.TB, outFile string) string {
	t.Helper()
	script := fmt.Sprintf(`#!/bin/sh
cat > /dev/null
cfg=""
prev=""
for a in "$@"; do
  if [ "$prev" = "--mcp-config" ]; then cfg="$a"; fi
  prev="$a"
done
{
  echo "args=$*"
  echo "cwd=$(pwd -P)"
  echo "cfg=$cfg"
  if [ -n "$cfg" ]; then
    echo "mode=$(stat -f %%Lp "$cfg" 2>/dev/null || stat -c %%a "$cfg")"
    echo "dirmode=$(stat -f %%Lp "$(dirname "$cfg")" 2>/dev/null || stat -c %%a "$(dirname "$cfg")")"
    echo "body=$(cat "$cfg")"
  fi
} > %q
echo '{"type":"result","subtype":"success","result":"done","session_id":"s-rec","total_cost_usd":0}'
`, outFile)
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write recording claude: %v", err)
	}
	return path
}
