package hostexec_test

import (
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/hostexec"
	"github.com/Jaydee94/remedy/internal/sshhost"
)

var rules = hostexec.Rules{DockerPattern: "remedy-*", Units: []string{"node_exporter.service"}}

func TestEveryFormOfTheTableIsRead(t *testing.T) {
	cases := []struct {
		argv []string
		form string
		sudo bool
	}{
		{[]string{"docker", "ps", "-a"}, "docker ps -a", true},
		{[]string{"docker", "logs", "--tail", "50", "remedy-drill"}, "docker logs --tail {N} {NAME}", true},
		{[]string{"docker", "inspect", "remedy-drill"}, "docker inspect {NAME}", true},
		{[]string{"systemctl", "status", "anything.service", "--no-pager"}, "systemctl status {UNIT} --no-pager", false},
		{[]string{"systemctl", "is-active", "x.timer"}, "systemctl is-active {UNIT}", false},
		{[]string{"systemctl", "list-units", "--failed", "--no-pager"}, "systemctl list-units --failed --no-pager", false},
		{[]string{"journalctl", "-u", "x.service", "-n", "200", "--no-pager"}, "journalctl -u {UNIT} -n {N} --no-pager", false},
		{[]string{"df", "-h"}, "df -h", false},
		{[]string{"free", "-m"}, "free -m", false},
		{[]string{"uptime"}, "uptime", false},
		{[]string{"uname", "-a"}, "uname -a", false},
		{[]string{"ps", "-eo", "pid,user,pcpu,pmem,etime,comm"}, "ps -eo pid,user,pcpu,pmem,etime,comm", false},
		{[]string{"ss", "-tln"}, "ss -tln", false},
	}
	for _, c := range cases {
		v := hostexec.Classify(c.argv, rules)
		if v.Kind != hostexec.Read || v.Form != c.form || v.Sudo != c.sudo {
			t.Errorf("Classify(%q) = %+v, want a read of %q (sudo %v)", c.argv, v, c.form, c.sudo)
		}
	}
	if n := len(hostexec.ReadForms()); n != len(cases) {
		t.Errorf("the table has %d forms and the test covers %d: add the case for the new form", n, len(cases))
	}
}

func TestNothingElseIsRead(t *testing.T) {
	notRead := map[string][]string{
		"an extra flag":                  {"docker", "ps", "-a", "--no-trunc"},
		"docker ps without -a":           {"docker", "ps"},
		"words reordered":                {"docker", "-a", "ps"},
		"df with a path":                 {"df", "-h", "/"},
		"df with other flags":            {"df", "-ah"},
		"uptime with a flag":             {"uptime", "-p"},
		"free with an interval":          {"free", "-m", "-s", "1"},
		"ps aux":                         {"ps", "aux"},
		"ps with the arguments column":   {"ps", "-eo", "pid,args"},
		"ss with the process column":     {"ss", "-tlnp"},
		"journalctl without --no-pager":  {"journalctl", "-u", "x.service", "-n", "200"},
		"journalctl following":           {"journalctl", "-u", "x.service", "-n", "200", "--no-pager", "-f"},
		"journalctl with another output": {"journalctl", "--output=json", "-u", "x.service", "-n", "5", "--no-pager"},
		"journalctl without -n":          {"journalctl", "-u", "x.service", "--no-pager"},
		"systemctl status without pager": {"systemctl", "status", "x.service"},
		"a unit without a suffix":        {"systemctl", "status", "x", "--no-pager"},
		"a unit with a path":             {"systemctl", "status", "../x.service", "--no-pager"},
		"a unit with a colon":            {"systemctl", "status", "x:y.service", "--no-pager"},
		"logs without --tail":            {"docker", "logs", "remedy-a"},
		"logs with a zero tail":          {"docker", "logs", "--tail", "0", "remedy-a"},
		"logs with a huge tail":          {"docker", "logs", "--tail", "2001", "remedy-a"},
		"logs with a text tail":          {"docker", "logs", "--tail", "5x", "remedy-a"},
		"logs of another container":      {"docker", "logs", "--tail", "50", "other"},
		"logs with a follow flag":        {"docker", "logs", "--tail", "50", "-f", "remedy-a"},
		"inspect of another container":   {"docker", "inspect", "other"},
		"inspect of two":                 {"docker", "inspect", "remedy-a", "other"},
		"inspect with a format":          {"docker", "inspect", "--format", "{{.Config.Env}}", "remedy-a"},
		"docker run":                     {"docker", "run", "-v", "/:/h", "alpine"},
		"docker exec":                    {"docker", "exec", "remedy-a", "id"},
		"find -exec":                     {"find", ".", "-exec", "rm", "{}", ";"},
		"a program with a path":          {"/usr/bin/docker", "ps", "-a"},
		"sudo inside":                    {"sudo", "docker", "ps", "-a"},
		"env inside":                     {"env", "docker", "ps", "-a"},
		"a shell":                        {"sh", "-c", "docker ps -a"},
		"a semicolon":                    {"df", "-h;", "rm"},
		"an and":                         {"df", "-h", "&&", "rm"},
		"a pipe":                         {"uptime", "|", "sh"},
		"a command substitution":         {"docker", "ps", "-a", "$(id)"},
		"upper case":                     {"DOCKER", "ps", "-a"},
		"a newline in a word":            {"ps", "-eo", "pid,user,pcpu,pmem,etime,comm\n"},
		"no words":                       {},
		"an environment assignment":      {"FOO=bar", "df", "-h"},
		"an empty word":                  {"df", ""},
		"the table form with a NUL":      {"df", "-h\x00"},
		"a never-listed command":         {"rm", "-rf", "/"},
	}
	for name, argv := range notRead {
		if v := hostexec.Classify(argv, rules); v.Kind == hostexec.Read {
			t.Errorf("%s: Classify(%q) = %+v, must not be a read", name, argv, v)
		}
	}
}

func TestTheKindsAreTheRightOnes(t *testing.T) {
	for _, c := range []struct {
		argv []string
		want hostexec.Kind
	}{
		{[]string{"docker", "restart", "remedy-a"}, hostexec.Change},
		{[]string{"apt", "update"}, hostexec.Change},
		{[]string{"rm", "-rf", "/"}, hostexec.Never},
		{[]string{"docker", "run", "x"}, hostexec.Never},
		{[]string{"ls", "a\nb"}, hostexec.Invalid},
		{nil, hostexec.Invalid},
	} {
		if v := hostexec.Classify(c.argv, rules); v.Kind != c.want {
			t.Errorf("Classify(%q).Kind = %v, want %v (%+v)", c.argv, v.Kind, c.want, v)
		}
	}
	if v := hostexec.Classify([]string{"rm", "-rf", "/"}, rules); v.Reason == "" {
		t.Error("a never-listed command has no reason")
	}
}

func TestDockerFormsNeedAContainerRule(t *testing.T) {
	none := hostexec.Rules{}
	for _, argv := range [][]string{
		{"docker", "ps", "-a"},
		{"docker", "logs", "--tail", "5", "remedy-a"},
		{"docker", "inspect", "remedy-a"},
	} {
		v := hostexec.Classify(argv, none)
		if v.Kind == hostexec.Read || !strings.Contains(v.Reason, "container") {
			t.Errorf("Classify(%q) with no rule = %+v, want a refusal that mentions the container rules", argv, v)
		}
	}
	// A form without docker does not care.
	if v := hostexec.Classify([]string{"df", "-h"}, none); v.Kind != hostexec.Read {
		t.Errorf("df -h = %+v", v)
	}
}

func TestAContainerMatchesTheWholePattern(t *testing.T) {
	for _, c := range []struct {
		name, pattern string
		want          bool
	}{
		{"remedy-drill", "remedy-*", true},
		{"remedy-", "remedy-*", true},
		{"remedy", "remedy-*", false},
		{"other", "remedy-*", false},
		{"remedy-drill", "remedy-drill", true},
		{"remedy-drill2", "remedy-drill", false},
		{"remedy-a b", "remedy-*", false},
		{"remedy-a;b", "remedy-*", false},
		{"remedy-drill", "", false},
		{"-remedy", "-remedy", false},
	} {
		if got := hostexec.ContainerAllowed(c.name, c.pattern); got != c.want {
			t.Errorf("ContainerAllowed(%q, %q) = %v, want %v", c.name, c.pattern, got, c.want)
		}
	}
}

func TestChangeCheck(t *testing.T) {
	ok := [][]string{
		{"docker", "restart", "remedy-drill"},
		{"docker", "start", "remedy-drill"},
		{"docker", "stop", "remedy-drill"},
		{"systemctl", "restart", "node_exporter.service"},
		{"apt", "update"},                     // another shape: the OS decides
		{"docker", "restart", "-t", "5", "x"}, // not a known shape
	}
	for _, argv := range ok {
		if err := hostexec.ChangeCheck(argv, rules); err != nil {
			t.Errorf("ChangeCheck(%q) = %v", argv, err)
		}
	}
	bad := map[string][]string{
		"a container outside the pattern": {"docker", "restart", "other"},
		"no container rule":               {"docker", "stop", "remedy-a"},
		"a unit that is not listed":       {"systemctl", "restart", "unknown.service"},
		"a unit without its suffix":       {"systemctl", "restart", "node_exporter"},
	}
	for name, argv := range bad {
		r := rules
		if name == "no container rule" {
			r = hostexec.Rules{Units: rules.Units}
		}
		if err := hostexec.ChangeCheck(argv, r); err == nil {
			t.Errorf("%s: ChangeCheck(%q) = nil", name, argv)
		}
	}
}

// The unit check here and the one that writes the sudoers file (sshhost) must agree: a unit the snippet refuses must not be read.
func TestTheUnitCheckAgreesWithTheSnippet(t *testing.T) {
	for _, u := range []string{
		"a.service", "node_exporter.service", "x.timer", "x.socket", "getty@tty1.service", "a-b_c.d.service",
		"x", "x.mount", "x:y.service", "../x.service", "-x.service", "a b.service", "a\n.service", ".service", "x.service.", "",
		strings.Repeat("a", 102) + ".service",
	} {
		_, err := sshhost.Snippet(sshhost.SnippetInput{Account: "remedy", SystemdUnits: []string{u}})
		if (err == nil) != hostexec.ValidUnit(u) {
			t.Errorf("unit %q: the snippet accepts it = %v, hostexec.ValidUnit = %v", u, err == nil, hostexec.ValidUnit(u))
		}
	}
}

// The docker forms that run with sudo need lines in the sudoers file that the onboarding snippet writes.
func TestTheDockerFormsHaveSudoersLines(t *testing.T) {
	out, err := sshhost.Snippet(sshhost.SnippetInput{Account: "remedy", DockerPattern: "remedy-*"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"NOPASSWD: /usr/bin/docker ps -a\n",
		"NOPASSWD: /usr/bin/docker logs --tail * remedy-*\n",
		"NOPASSWD: /usr/bin/docker inspect remedy-*\n",
	} {
		if !strings.Contains(out.Sudoers, want) {
			t.Errorf("the snippet lacks %q", want)
		}
	}
}
