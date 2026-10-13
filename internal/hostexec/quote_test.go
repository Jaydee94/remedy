package hostexec_test

import (
	"bytes"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Jaydee94/remedy/internal/hostexec"
)

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{
		"a":     `'a'`,
		"":      `''`,
		"it's":  `'it'\''s'`,
		"$(id)": `'$(id)'`,
		"a b":   `'a b'`,
		"`x`":   "'`x`'",
		`\`:     `'\'`,
	} {
		if got := hostexec.Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestCommandLine(t *testing.T) {
	argv := []string{"docker", "logs", "--tail", "50", "remedy-drill"}
	if got, want := hostexec.CommandLine(argv, false), `'docker' 'logs' '--tail' '50' 'remedy-drill'`; got != want {
		t.Errorf("CommandLine = %s, want %s", got, want)
	}
	if got, want := hostexec.CommandLine(argv, true), `sudo -n -- 'docker' 'logs' '--tail' '50' 'remedy-drill'`; got != want {
		t.Errorf("CommandLine with sudo = %s, want %s", got, want)
	}
}

func TestDisplayQuotesOnlyWhatNeedsIt(t *testing.T) {
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"docker", "restart", "remedy-drill"}, `docker restart remedy-drill`},
		{[]string{"systemctl", "restart", "node_exporter.service"}, `systemctl restart node_exporter.service`},
		{[]string{"echo", "a b", "it's", ""}, `echo 'a b' 'it'\''s' ''`},
		{[]string{"ps", "-eo", "pid,user,comm"}, `ps -eo pid,user,comm`},
		{[]string{"x", "$(id)", "a;b"}, `x '$(id)' 'a;b'`},
	} {
		if got := hostexec.Display(c.argv); got != c.want {
			t.Errorf("Display(%q) = %s, want %s", c.argv, got, c.want)
		}
	}
}

// corpus is what the quoting must carry through a real shell unchanged.
var corpus = [][]string{
	{"plain"},
	{"a b", "c'd", `e"f`},
	{"$HOME", "$(id)", "`id`", "${x}", "$1", "$$"},
	{`a\b`, `\`, `\\`, `\'`, `\n`},
	{"*", "?", "[a-z]", "~", "~root", "{a,b}", "#", "!", "a;b", "a&&b", "a|b", "a>b", "a<b", "&", "(", ")"},
	{"-n", "--", "-e", "="},
	{"ünïcode ✓", "日本語"},
	{hostexec.CanaryWant()},
	{"a" + strings.Repeat("'", 5) + "b"},
	{strings.Repeat("x", 1000)},
	{"", "after an empty word"},
}

// shells are the POSIX shells this machine has.
func shells(t testing.TB) []string {
	t.Helper()
	var out []string
	for _, name := range []string{"sh", "bash", "dash"} {
		if p, err := exec.LookPath(name); err == nil {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		t.Skip("no POSIX shell on this machine")
	}
	return out
}

// readBack quotes the words, lets the shell print them NUL separated and returns what it printed.
func readBack(t testing.TB, shell string, words []string) []string {
	t.Helper()
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = hostexec.Quote(w)
	}
	line := `printf '%s\0' ` + strings.Join(quoted, " ")
	out, err := exec.Command(shell, "-c", line).Output()
	if err != nil {
		t.Fatalf("%s -c %q: %v", shell, line, err)
	}
	parts := bytes.Split(out, []byte{0})
	parts = parts[:len(parts)-1] // what follows the last NUL
	got := make([]string, len(parts))
	for i, p := range parts {
		got[i] = string(p)
	}
	return got
}

func TestTheShellReadsWhatWasQuoted(t *testing.T) {
	for _, shell := range shells(t) {
		for _, words := range corpus {
			if got := readBack(t, shell, words); !reflect.DeepEqual(got, words) {
				t.Errorf("%s: quoted %q, the shell read %q", shell, words, got)
			}
		}
	}
}

func TestTheCanaryRoundTripsThroughARealShell(t *testing.T) {
	for _, shell := range shells(t) {
		out, err := exec.Command(shell, "-c", hostexec.CanaryCommand()).Output()
		if err != nil || string(out) != hostexec.CanaryWant() {
			t.Errorf("%s: canary printed %q, %v; want %q", shell, out, err, hostexec.CanaryWant())
		}
	}
}

func FuzzQuoteRoundTrip(f *testing.F) {
	for _, seed := range []string{"a", "it's", "$(id)", "a b", `\`, "`", `"`, "*", "~", "--", "a\tb"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if s == "" || strings.ContainsRune(s, 0) || !utf8.ValidString(s) {
			t.Skip()
		}
		sh, err := exec.LookPath("sh")
		if err != nil {
			t.Skip("no sh")
		}
		got := readBack(t, sh, []string{s})
		if len(got) != 1 || got[0] != s {
			t.Fatalf("quoted %q, the shell read %q", s, got)
		}
	})
}
