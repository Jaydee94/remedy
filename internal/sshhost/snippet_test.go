package sshhost_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/sshhost"
)

const (
	pat  = `[A-Za-z0-9][A-Za-z0-9_.-]*\*?`
	unit = `[A-Za-z0-9][A-Za-z0-9_.@-]*\.(service|timer|socket)`
)

// grantLine is the only shape of a non-comment line the generator may produce for the account.
func grantLine(account string) *regexp.Regexp {
	a := regexp.QuoteMeta(account)
	return regexp.MustCompile(`^` + a + ` ALL=\(root\) NOPASSWD: (` +
		`/usr/bin/docker ps -a` +
		`|/usr/bin/docker logs --tail \* ` + pat +
		`|/usr/bin/docker inspect ` + pat +
		`|/usr/bin/docker start ` + pat + `, /usr/bin/docker restart ` + pat + `, /usr/bin/docker stop ` + pat +
		`|/usr/bin/systemctl start ` + unit + `, /usr/bin/systemctl restart ` + unit + `, /usr/bin/systemctl stop ` + unit +
		`)$`)
}

func TestSnippetForTheNAS(t *testing.T) {
	out, err := sshhost.Snippet(sshhost.SnippetInput{
		Account:       "remedy",
		AuthorizedKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKExample",
		DockerPattern: "remedy-*",
		SystemdUnits:  []string{"node_exporter.service"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKExample\n",
	} {
		if !strings.Contains(out.AuthorizedKeys, want) {
			t.Errorf("authorized_keys lacks %q:\n%s", want, out.AuthorizedKeys)
		}
	}
	for _, want := range []string{
		"remedy ALL=(root) NOPASSWD: /usr/bin/docker ps -a\n",
		"remedy ALL=(root) NOPASSWD: /usr/bin/docker logs --tail * remedy-*\n",
		"remedy ALL=(root) NOPASSWD: /usr/bin/docker inspect remedy-*\n",
		"remedy ALL=(root) NOPASSWD: /usr/bin/docker start remedy-*, /usr/bin/docker restart remedy-*, /usr/bin/docker stop remedy-*\n",
		"remedy ALL=(root) NOPASSWD: /usr/bin/systemctl start node_exporter.service, /usr/bin/systemctl restart node_exporter.service, /usr/bin/systemctl stop node_exporter.service\n",
	} {
		if !strings.Contains(out.Sudoers, want) {
			t.Errorf("sudoers lacks %q:\n%s", want, out.Sudoers)
		}
	}
	assertOnlyGrants(t, out.Sudoers, "remedy")

	// The docker block warns, in comments, that a star in sudoers also matches spaces.
	dockerStart := strings.Index(out.Sudoers, "/usr/bin/docker ps -a")
	warned := false
	for _, line := range strings.Split(out.Sudoers[:dockerStart], "\n") {
		if strings.HasPrefix(line, "#") && strings.Contains(line, "spaces") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no comment before the docker lines says that * also matches spaces:\n%s", out.Sudoers)
	}
}

func assertOnlyGrants(t *testing.T, sudoers, account string) {
	t.Helper()
	re := grantLine(account)
	for _, line := range strings.Split(sudoers, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !re.MatchString(line) {
			t.Errorf("a line grants more than the table allows: %q", line)
		}
	}
}

func TestSnippetWithoutDockerOrUnitsGrantsNothing(t *testing.T) {
	out, err := sshhost.Snippet(sshhost.SnippetInput{Account: "remedy"})
	if err != nil {
		t.Fatal(err)
	}
	assertOnlyGrants(t, out.Sudoers, "remedy")
	if strings.Contains(out.Sudoers, "NOPASSWD") {
		t.Fatalf("a snippet without docker or units grants something:\n%s", out.Sudoers)
	}
	if out.AuthorizedKeys != "" {
		t.Fatalf("authorized_keys without a key = %q", out.AuthorizedKeys)
	}
}

func TestSnippetRefusesHostileInput(t *testing.T) {
	base := sshhost.SnippetInput{Account: "remedy", DockerPattern: "remedy-*", SystemdUnits: []string{"a.service"}}
	cases := map[string]func(in *sshhost.SnippetInput){
		"newline in pattern":   func(in *sshhost.SnippetInput) { in.DockerPattern = "remedy-*\nALL ALL=(ALL) NOPASSWD: ALL" },
		"comma in pattern":     func(in *sshhost.SnippetInput) { in.DockerPattern = "a,b" },
		"space in pattern":     func(in *sshhost.SnippetInput) { in.DockerPattern = "a b" },
		"ALL as pattern":       func(in *sshhost.SnippetInput) { in.DockerPattern = "ALL" },
		"leading dash":         func(in *sshhost.SnippetInput) { in.DockerPattern = "-rf" },
		"bare star":            func(in *sshhost.SnippetInput) { in.DockerPattern = "*" },
		"two stars":            func(in *sshhost.SnippetInput) { in.DockerPattern = "x**" },
		"star in the middle":   func(in *sshhost.SnippetInput) { in.DockerPattern = "a*b" },
		"shell in pattern":     func(in *sshhost.SnippetInput) { in.DockerPattern = "$(id)" },
		"nul in pattern":       func(in *sshhost.SnippetInput) { in.DockerPattern = "a\x00" },
		"very long pattern":    func(in *sshhost.SnippetInput) { in.DockerPattern = strings.Repeat("a", 200) },
		"newline in unit":      func(in *sshhost.SnippetInput) { in.SystemdUnits = []string{"a.service\nroot ALL=(ALL) ALL"} },
		"comma in unit":        func(in *sshhost.SnippetInput) { in.SystemdUnits = []string{"a.service, /bin/sh"} },
		"unit with a path":     func(in *sshhost.SnippetInput) { in.SystemdUnits = []string{"../x.service"} },
		"unit with a space":    func(in *sshhost.SnippetInput) { in.SystemdUnits = []string{"a b.service"} },
		"leading dash in unit": func(in *sshhost.SnippetInput) { in.SystemdUnits = []string{"-x.service"} },
		"unit of a wrong kind": func(in *sshhost.SnippetInput) { in.SystemdUnits = []string{"a.mount"} },
		"unit without suffix":  func(in *sshhost.SnippetInput) { in.SystemdUnits = []string{"a"} }, // Snippet wants normalised units
		"colon in unit":        func(in *sshhost.SnippetInput) { in.SystemdUnits = []string{"x:y.service"} },
		"backslash in unit":    func(in *sshhost.SnippetInput) { in.SystemdUnits = []string{`a\b.service`} },
		"account with rights":  func(in *sshhost.SnippetInput) { in.Account = "root ALL=(ALL)" },
		"upper case account":   func(in *sshhost.SnippetInput) { in.Account = "Remedy" },
		"empty account":        func(in *sshhost.SnippetInput) { in.Account = "" },
		"newline in account":   func(in *sshhost.SnippetInput) { in.Account = "remedy\nroot" },
		"key with a newline": func(in *sshhost.SnippetInput) {
			in.AuthorizedKey = "ssh-ed25519 AAAA\ncommand=\"/bin/sh\" ssh-rsa BBBB"
		},
		"key with options":   func(in *sshhost.SnippetInput) { in.AuthorizedKey = `command="/bin/sh" ssh-ed25519 AAAA` },
		"key of an odd type": func(in *sshhost.SnippetInput) { in.AuthorizedKey = "ssh-dss AAAA" },
		"too many units": func(in *sshhost.SnippetInput) {
			in.SystemdUnits = nil
			for i := 0; i < 21; i++ {
				in.SystemdUnits = append(in.SystemdUnits, "u"+strings.Repeat("a", i)+".service")
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := base
			in.SystemdUnits = append([]string(nil), base.SystemdUnits...)
			mutate(&in)
			if out, err := sshhost.Snippet(in); err == nil {
				t.Fatalf("expected an error, got a snippet:\n%s\n%s", out.AuthorizedKeys, out.Sudoers)
			}
		})
	}
}
