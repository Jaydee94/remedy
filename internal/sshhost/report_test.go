package sshhost_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/sshhost"
)

const limitedOut = `Matching Defaults entries for remedy on nas:
    env_reset, mail_badpass, secure_path=/usr/local/sbin\:/usr/local/bin

User remedy may run the following commands on nas:
    (root) NOPASSWD: /usr/bin/docker ps -a
    (root) NOPASSWD: /usr/bin/docker logs --tail * remedy-*
`

func TestParseSudoList(t *testing.T) {
	cases := []struct {
		name       string
		out        string
		ok         bool
		level      string
		understood bool
		rules      int
	}{
		{"exact rules", limitedOut, true, sshhost.SudoLimited, true, 2},
		{"everything without a password", "User a may run the following commands on h:\n    (ALL : ALL) NOPASSWD: ALL\n", true, sshhost.SudoUnrestricted, true, 1},
		{"everything, short form", "User a may run the following commands on h:\n    (ALL) NOPASSWD: ALL\n", true, sshhost.SudoUnrestricted, true, 1},
		{"ALL after other commands", "User a may run the following commands on h:\n    (root) NOPASSWD: /bin/ls, /bin/cat, ALL\n", true, sshhost.SudoUnrestricted, true, 1},
		{"ALL behind a tag", "User a may run the following commands on h:\n    (ALL) NOPASSWD: SETENV: ALL\n", true, sshhost.SudoUnrestricted, true, 1},
		{"ALL that needs a password", "User a may run the following commands on h:\n    (ALL) ALL\n    (root) NOPASSWD: /usr/bin/true\n", true, sshhost.SudoLimited, true, 2},
		{"ALL after PASSWD resets the tag", "User a may run the following commands on h:\n    (ALL) NOPASSWD: /bin/ls, PASSWD: ALL\n", true, sshhost.SudoLimited, true, 1},
		{"password required", "sudo: a password is required\n", false, sshhost.SudoNone, true, 0},
		{"not allowed", "Sorry, user a may not run sudo on h.\n", false, sshhost.SudoNone, true, 0},
		{"sudo is not installed", "sh: 1: sudo: not found\n", false, sshhost.SudoNone, true, 0},
		{"an output nobody expected", "Some other sudo says: hello\n", true, sshhost.SudoLimited, false, 0},
		{"empty output that succeeded", "", true, sshhost.SudoLimited, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sshhost.ParseSudoList(c.out, c.ok)
			if got.Level != c.level || got.Understood != c.understood || len(got.Rules) != c.rules {
				t.Fatalf("ParseSudoList = %+v, want level %s, understood %v, %d rules", got, c.level, c.understood, c.rules)
			}
		})
	}
}

func TestParseSudoListBoundsTheRules(t *testing.T) {
	var b strings.Builder
	b.WriteString("User a may run the following commands on h:\n")
	for i := 0; i < 60; i++ {
		b.WriteString("    (root) NOPASSWD: /usr/bin/" + strings.Repeat("x", 300) + "\n")
	}
	got := sshhost.ParseSudoList(b.String(), true)
	if len(got.Rules) != 40 {
		t.Fatalf("kept %d rules, want 40", len(got.Rules))
	}
	for _, r := range got.Rules {
		if len([]rune(r)) > 200 {
			t.Fatalf("a rule has %d characters", len([]rune(r)))
		}
	}
}

func TestBuildReport(t *testing.T) {
	at := time.Date(2026, 10, 9, 18, 30, 0, 0, time.UTC)
	got := sshhost.BuildReport(at, sshhost.Outcome{
		Account: "remedy\n", GroupsOut: "remedy users docker\n", System: "Linux 6.8.0\n", SudoOut: limitedOut, SudoOK: true,
	})
	want := sshhost.Report{
		At: at, Account: "remedy", Groups: []string{"remedy", "users", "docker"}, System: "Linux 6.8.0",
		Sudo: sshhost.SudoLimited, SudoRules: []string{"(root) NOPASSWD: /usr/bin/docker ps -a", "(root) NOPASSWD: /usr/bin/docker logs --tail * remedy-*"},
		Warnings: []string{"The account is in the group docker, which is the same as root."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("report = %+v\nwant     %+v", got, want)
	}
}

func TestBuildReportWarnings(t *testing.T) {
	root := sshhost.BuildReport(time.Now(), sshhost.Outcome{Account: "root\n", GroupsOut: "root\n", SudoOK: false})
	if root.Sudo != sshhost.SudoNone || len(root.Warnings) != 1 || root.Warnings[0] != "The account is root." {
		t.Fatalf("root report = %+v", root)
	}
	all := sshhost.BuildReport(time.Now(), sshhost.Outcome{
		Account: "jaydee\n", GroupsOut: "jaydee sudo\n", SudoOut: "User jaydee may run the following commands on h:\n    (ALL : ALL) NOPASSWD: ALL\n", SudoOK: true,
	})
	if all.Sudo != sshhost.SudoUnrestricted || len(all.Warnings) != 1 || all.Warnings[0] != "sudo allows every command without a password." {
		t.Fatalf("unrestricted report = %+v", all)
	}
	odd := sshhost.BuildReport(time.Now(), sshhost.Outcome{Account: "a\n", GroupsOut: "a\n", SudoOut: "gibberish\n", SudoOK: true})
	if odd.Sudo != sshhost.SudoLimited || len(odd.Warnings) != 1 || odd.Warnings[0] != "I could not read the sudo rules; check them yourself." {
		t.Fatalf("unreadable report = %+v", odd)
	}
}

func TestBuildReportCleansWhatTheHostSays(t *testing.T) {
	got := sshhost.BuildReport(time.Now(), sshhost.Outcome{
		Account: "re\x1b[31mmedy\n", GroupsOut: strings.Repeat("g", 200) + " ok\n", System: strings.Repeat("L", 300), SudoOK: false,
	})
	if strings.ContainsRune(got.Account, 0x1b) {
		t.Fatalf("account keeps a control character: %q", got.Account)
	}
	if len([]rune(got.Groups[0])) > 64 || len([]rune(got.System)) > 100 {
		t.Fatalf("not bounded: group %d, system %d", len([]rune(got.Groups[0])), len([]rune(got.System)))
	}
}
