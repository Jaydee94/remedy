package sshhost

import (
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	SudoNone         = "none"
	SudoLimited      = "limited"
	SudoUnrestricted = "unrestricted"

	maxRules    = 40
	maxRuleLen  = 200
	maxGroupLen = 64
	maxSysLen   = 100
	maxNameLen  = 32
)

// Report is what a connection test learned about the account. Everything in it came from the host and is shown as text.
type Report struct {
	At        time.Time `json:"at"`
	Account   string    `json:"account"`
	Groups    []string  `json:"groups"`
	System    string    `json:"system"`
	Sudo      string    `json:"sudo"`
	SudoRules []string  `json:"sudoRules"`
	Warnings  []string  `json:"warnings"`
}

// Outcome is the raw answer of the four fixed test commands.
type Outcome struct {
	Account   string // id -un
	GroupsOut string // id -Gn
	System    string // uname -sr
	SudoOut   string // sudo -n -l
	SudoOK    bool   // sudo -n -l exited 0
	SudoCut   bool   // the output of sudo -n -l was longer than we keep, so the listing is incomplete
}

// SudoCheck is the reading of the output of sudo -n -l.
type SudoCheck struct {
	Level      string
	Rules      []string
	Understood bool
}

var (
	tagRE     = regexp.MustCompile(`^([A-Z][A-Z_]*):\s*`)
	runasRE   = regexp.MustCompile(`^\([^)]*\)\s*`)
	listingRE = regexp.MustCompile(`(?i)may run the following commands`)
)

// ParseSudoList reads the output of `sudo -n -l`. It is tolerant: when it cannot tell, it says `limited` and Understood false,
// and it never says `none` for an output it did not understand. A failed command is `none`.
func ParseSudoList(out string, ok bool) SudoCheck {
	if !ok {
		return SudoCheck{Level: SudoNone, Understood: true}
	}
	lines := strings.Split(out, "\n")
	start := -1
	for i, l := range lines {
		if listingRE.MatchString(l) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return SudoCheck{Level: SudoLimited, Understood: false}
	}
	check := SudoCheck{Level: SudoLimited, Understood: true}
	for _, l := range lines[start:] {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if len(check.Rules) < maxRules {
			check.Rules = append(check.Rules, truncate(clean(l), maxRuleLen))
		}
		if allowsAllWithoutPassword(l) {
			check.Level = SudoUnrestricted
		}
	}
	if len(check.Rules) == 0 {
		check.Understood = false
	}
	return check
}

// allowsAllWithoutPassword reads one rule line such as "(ALL : ALL) NOPASSWD: ALL": after the run-as list come comma separated
// commands, each optionally behind tags; NOPASSWD: holds until PASSWD: resets it.
func allowsAllWithoutPassword(line string) bool {
	rest := runasRE.ReplaceAllString(line, "")
	if rest == line {
		return false // not a rule line
	}
	nopasswd := false
	for _, entry := range strings.Split(rest, ",") {
		entry = strings.TrimSpace(entry)
		for {
			m := tagRE.FindStringSubmatch(entry)
			if m == nil {
				break
			}
			switch m[1] {
			case "NOPASSWD":
				nopasswd = true
			case "PASSWD":
				nopasswd = false
			}
			entry = entry[len(m[0]):]
		}
		if entry == "ALL" && nopasswd {
			return true
		}
	}
	return false
}

// BuildReport turns the answers of the test commands into the report, with its warnings.
func BuildReport(at time.Time, o Outcome) Report {
	r := Report{At: at.UTC(), Account: truncate(clean(strings.TrimSpace(o.Account)), maxNameLen), System: truncate(clean(strings.TrimSpace(o.System)), maxSysLen)}
	r.Groups = []string{}
	for _, g := range strings.Fields(o.GroupsOut) {
		r.Groups = append(r.Groups, truncate(clean(g), maxGroupLen))
	}
	sudo := ParseSudoList(o.SudoOut, o.SudoOK)
	if o.SudoCut {
		sudo.Understood = false // a cut listing is never read as complete; the level stays what was seen (never none)
	}
	r.Sudo = sudo.Level
	r.SudoRules = sudo.Rules
	if r.SudoRules == nil {
		r.SudoRules = []string{}
	}
	r.Warnings = []string{}
	if r.Account == "root" {
		r.Warnings = append(r.Warnings, "The account is root.")
	}
	for _, g := range r.Groups {
		if g == "docker" {
			r.Warnings = append(r.Warnings, "The account is in the group docker, which is the same as root.")
			break
		}
	}
	if sudo.Level == SudoUnrestricted {
		r.Warnings = append(r.Warnings, "sudo allows every command without a password.")
	}
	if !sudo.Understood && o.SudoOK {
		r.Warnings = append(r.Warnings, "I could not read the sudo rules; check them yourself.")
	}
	return r
}

// clean drops control characters, so that nothing a host prints can move a cursor or colour a page.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
