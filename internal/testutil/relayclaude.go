package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// RelayOptions plants what FakeClaudeRelay's script does.
//
// Mode is one of:
//
//	valid-exits    accepts only the planted Code: it creates the login marker, prints a success line and exits 0
//	valid-stays    accepts only the planted Code: it creates the marker, prints the success line and waits for a signal
//	invalid-stdout rejects every line, on stdout
//	invalid-stderr rejects every line, on stderr
//	no-url         prints no "visit:" line
//	bad-url        prints a URL on another host (claude.com.evil.test) unless URL is set
//	exit-early     prints everything, then exits 1 without reading a line
//	lingering      like valid-exits, but leaves a background sleeper (in the process group) that holds the output
//	ignores-term   like valid-stays, but traps TERM, so only KILL ends it
//
// URL and Code have defaults. LoggedIn creates the login marker before the first call. Flap makes `auth status --text`
// exit 3 with unrelated text while an `auth login` of the fake runs and the marker is absent. Noisy prints the URL and the
// code on stderr right after the URL line, as material that a careless reader of the output would pick up.
type RelayOptions struct {
	Mode, URL, Code string
	LoggedIn        bool
	Flap            bool
	Noisy           bool
}

// Relay is a fake `claude` for the login relay. Dir holds its state: url, code, loggedin (the marker), pids, lines, flap,
// noisy.
type Relay struct{ Path, Dir string }

const (
	relayDefaultURL    = "https://claude.com/cai/oauth/authorize?code=true&client_id=fake&response_type=code&code_challenge=fake&state=fake"
	relayDefaultBadURL = "https://claude.com.evil.test/cai/oauth/authorize?code=true&client_id=fake&response_type=code&code_challenge=fake&state=fake"
	relayDefaultCode   = "fake-code-0123456789abcdef"
)

// FakeClaudeRelay writes the fake CLI and its state into t.TempDir(). The script is POSIX sh; the paths are the only
// thing formatted into it, every value is read from a file at run time.
func FakeClaudeRelay(t testing.TB, o RelayOptions) *Relay {
	t.Helper()
	switch o.Mode {
	case "valid-exits", "valid-stays", "invalid-stdout", "invalid-stderr", "no-url", "bad-url", "exit-early", "lingering", "ignores-term":
	default:
		t.Fatalf("unknown relay mode %q", o.Mode)
	}
	if o.URL == "" {
		o.URL = relayDefaultURL
		if o.Mode == "bad-url" {
			o.URL = relayDefaultBadURL
		}
	}
	if o.Code == "" {
		o.Code = relayDefaultCode
	}
	dir := t.TempDir()
	files := map[string]string{"url": o.URL, "code": o.Code, "mode": o.Mode}
	for name, v := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(v), 0o600); err != nil {
			t.Fatalf("write fake claude state: %v", err)
		}
	}
	if o.LoggedIn {
		touch(t, filepath.Join(dir, "loggedin"))
	}
	if o.Flap {
		touch(t, filepath.Join(dir, "flap"))
	}
	if o.Noisy {
		touch(t, filepath.Join(dir, "noisy"))
	}
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte(fmt.Sprintf(relayScript, dir)), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	return &Relay{Path: path, Dir: dir}
}

func touch(t testing.TB, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// SetURL changes the URL that the next `auth login` prints; a running one keeps the URL it read.
func (r *Relay) SetURL(t testing.TB, url string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.Dir, "url"), []byte(url), 0o600); err != nil {
		t.Fatalf("write fake claude url: %v", err)
	}
}

// LoggedIn says whether the login marker exists.
func (r *Relay) LoggedIn() bool {
	_, err := os.Stat(filepath.Join(r.Dir, "loggedin"))
	return err == nil
}

// PIDs are the pids the fake recorded, in order: each `auth login` appends its own, a lingering one then its sleeper's.
func (r *Relay) PIDs() []int {
	b, _ := os.ReadFile(filepath.Join(r.Dir, "pids"))
	var pids []int
	for _, f := range strings.Fields(string(b)) {
		if pid, err := strconv.Atoi(f); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// Alive are the recorded pids that `kill -0` still reaches.
func (r *Relay) Alive() []int {
	var alive []int
	for _, pid := range r.PIDs() {
		if syscall.Kill(pid, 0) == nil {
			alive = append(alive, pid)
		}
	}
	return alive
}

// LinesRead is the number of lines the fake has read from its stdin.
func (r *Relay) LinesRead() int {
	b, _ := os.ReadFile(filepath.Join(r.Dir, "lines"))
	return strings.Count(string(b), "\n")
}

// relayScript has one %s: the state directory. Only constructs that dash, bash and busybox sh share.
const relayScript = `#!/bin/sh
D='%s'
if [ "$1" = "--version" ]; then
  echo "2.1.288 (Claude Code)"
  exit 0
fi
if [ "$1" = "auth" ] && [ "$2" = "status" ]; then
  if [ -f "$D/loggedin" ]; then
    echo "Login method: Claude Max Account"
    exit 0
  fi
  if [ -f "$D/flap" ] && [ -f "$D/pids" ]; then
    read -r first < "$D/pids"
    if kill -0 "$first" 2>/dev/null; then
      echo "unrelated status failure" >&2
      exit 3
    fi
  fi
  echo "Not logged in. Run claude auth login to authenticate." >&2
  exit 1
fi
if [ "$1" != "auth" ] || [ "$2" != "login" ]; then
  echo "unexpected arguments" >&2
  exit 2
fi

read -r MODE < "$D/mode"
read -r URL < "$D/url"
read -r CODE < "$D/code"
echo $$ >> "$D/pids"
if [ "$MODE" = "lingering" ]; then
  sleep 30 &
  echo $! >> "$D/pids"
fi
if [ "$MODE" = "ignores-term" ]; then
  trap : TERM
fi

printf 'Opening browser to sign in'
printf '\342\200\246\n'
if [ "$MODE" != "no-url" ]; then
  printf '%%s\n' "If the browser didn't open, visit: $URL"
  if [ -f "$D/noisy" ]; then
    printf '%%s\n' "$URL" >&2
    printf '%%s\n' "$CODE" >&2
  fi
fi
printf 'Paste code here if prompted > '
if [ "$MODE" = "exit-early" ]; then
  exit 1
fi

INVALID="Invalid code. Please make sure the full code was copied."
while read -r line; do
  echo x >> "$D/lines"
  case "$MODE" in
    invalid-stdout)
      echo "$INVALID"
      ;;
    invalid-stderr)
      echo "$INVALID" >&2
      ;;
    *)
      if [ "$line" = "$CODE" ]; then
        : > "$D/loggedin"
        echo "Login successful. Logged in as someone@example.invalid"
        case "$MODE" in
          valid-stays|ignores-term)
            while :; do sleep 1; done
            ;;
          *)
            exit 0
            ;;
        esac
      else
        echo "$INVALID"
      fi
      ;;
  esac
done
exit 1
`
