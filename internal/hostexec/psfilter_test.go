package hostexec_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/hostexec"
)

func TestPsFilter(t *testing.T) {
	cases := []struct {
		pattern string
		want    string
		ok      bool
	}{
		{"remedy-*", "name=^remedy-", true},
		{"remedy-drill", "name=^remedy-drill$", true},
		// A dot stays a dot of the regular expression, which is looser than the pattern says. The spec accepts that; the
		// executed filter is pinned here so that a change is a decision.
		{"my.app-*", "name=^my.app-", true},
		{"my.app", "name=^my.app$", true},
		{"", "", false},
		{"*", "", false},
		{"a b", "", false},
		{"remedy-*x", "", false},
		{"-x", "", false},
		{"a,b", "", false},
	}
	for _, c := range cases {
		got, ok := hostexec.PsFilter(c.pattern)
		if got != c.want || ok != c.ok {
			t.Errorf("PsFilter(%q) = %q, %v; want %q, %v", c.pattern, got, ok, c.want, c.ok)
		}
	}
}

func TestExpand(t *testing.T) {
	ps := []string{"docker", "ps", "-a"}
	read := hostexec.Classify(ps, rules)
	if read.Kind != hostexec.Read {
		t.Fatalf("Classify(docker ps -a) = %+v, want a read", read)
	}

	got, ok := hostexec.Expand(read, ps, rules)
	if want := []string{"docker", "ps", "-a", "--filter", "name=^remedy-"}; !ok || !slices.Equal(got, want) {
		t.Errorf("Expand of docker ps -a = %q, %v; want %q", got, ok, want)
	}
	if !slices.Equal(ps, []string{"docker", "ps", "-a"}) {
		t.Errorf("Expand changed the caller's argv: %q", ps)
	}

	exact := hostexec.Rules{DockerPattern: "remedy-drill"}
	got, ok = hostexec.Expand(read, ps, exact)
	if want := []string{"docker", "ps", "-a", "--filter", "name=^remedy-drill$"}; !ok || !slices.Equal(got, want) {
		t.Errorf("Expand with an exact pattern = %q, %v; want %q", got, ok, want)
	}

	// A pattern the filter cannot express must never fall back to the plain command.
	for _, p := range []string{"", "*", "a b"} {
		if got, ok := hostexec.Expand(read, ps, hostexec.Rules{DockerPattern: p}); ok || got != nil {
			t.Errorf("Expand with the pattern %q = %q, %v; want nil, false", p, got, ok)
		}
	}

	for _, argv := range [][]string{
		{"docker", "logs", "--tail", "50", "remedy-a"},
		{"df", "-h"},
	} {
		v := hostexec.Classify(argv, rules)
		if v.Kind != hostexec.Read {
			t.Fatalf("Classify(%q) = %+v, want a read", argv, v)
		}
		if got, ok := hostexec.Expand(v, argv, rules); !ok || !slices.Equal(got, argv) {
			t.Errorf("Expand(%q) = %q, %v; want the argv as given", argv, got, ok)
		}
	}

	// Only a read of that form is expanded: a change is not a read of the table, whatever its words.
	if got, ok := hostexec.Expand(hostexec.Verdict{Kind: hostexec.Change}, ps, rules); !ok || !slices.Equal(got, ps) {
		t.Errorf("Expand of a change = %q, %v; want the argv as given", got, ok)
	}
}

func TestFormatResultNote(t *testing.T) {
	plain := hostexec.FormatResult("nas", hostexec.Result{Stdout: "x\n", ExitCode: 0})
	if strings.Contains(plain, "note:") {
		t.Errorf("a result with no note has one:\n%s", plain)
	}
	noted := hostexec.FormatResult("nas", hostexec.Result{Stdout: "x\n", ExitCode: 0, Note: "only these"})
	exit, note, out := strings.Index(noted, "exit code: 0"), strings.Index(noted, "note: only these\n"), strings.Index(noted, "stdout:")
	if exit < 0 || note < exit || out < note {
		t.Errorf("the note is not between the exit code and stdout:\n%s", noted)
	}
	if strings.Replace(noted, "note: only these\n", "", 1) != plain {
		t.Errorf("a note changes more than its own line:\n%s\nvs\n%s", noted, plain)
	}
}
