package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// FakeClaudeLogin writes an executable script that mimics the login commands of `claude` and returns its path.
// `--version` prints a version line. Any other call is the status command, and mode says what it does:
//
//	ok      exits 0 and prints the shape of the real text form, with a line that names an account (the output must
//	        never go anywhere)
//	missing exits 1; called as `auth status --text` it prints the real sentence "Not logged in. Run claude auth login
//	        to authenticate." to stderr, called any other way it prints the JSON form (`"loggedIn": false`, no such
//	        phrase), as the real CLI does (spike S2)
//	broken  exits 3 and prints something nobody expects
//	hang    sleeps far longer than any test waits
//	killed  kills itself with SIGKILL, so it has no exit code
//	lingering leaves a `sleep 5` in the background that holds the output pipes open, prints nothing and exits 0 (for
//	        `--version` it prints the version line first); the sleeper ends by itself
func FakeClaudeLogin(t testing.TB, mode string) string {
	t.Helper()
	var status, pre string
	switch mode {
	case "ok":
		status = `echo "Login method: Claude Max Account"; echo "Email: someone@example.invalid"; exit 0`
	case "missing":
		status = `if [ "$*" = "auth status --text" ]; then echo "Not logged in. Run claude auth login to authenticate." >&2; else echo '{"loggedIn": false, "authMethod": "none"}'; fi; exit 1`
	case "broken":
		status = `echo "unexpected failure of the status command" >&2; exit 3`
	case "hang":
		status = `exec sleep 60`
	case "killed":
		status = `kill -9 $$`
	case "lingering":
		pre = `sleep 5 &`
		status = `exit 0`
	default:
		t.Fatalf("unknown mode %q", mode)
	}
	script := fmt.Sprintf(`#!/bin/sh
%s
if [ "$1" = "--version" ]; then
  echo "2.1.288 (Claude Code)"
  exit 0
fi
%s
`, pre, status)
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	return path
}
