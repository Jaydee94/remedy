package hostexec

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Kind is what a command is.
type Kind int

const (
	// Invalid means the words themselves are not acceptable (a control character, too long).
	Invalid Kind = iota
	// Read means the table describes the command exactly: it runs without an approval.
	Read
	// Change means anything else that the never-list allows: it runs only after an approval.
	Change
	// Never means the never-list refuses the command, also when it would be approved.
	Never
)

// Verdict is the classification of one command.
type Verdict struct {
	Kind Kind
	// Reason is a short sentence: why a command is Never or Invalid, or, for a Change, the nearest miss of the table
	// ("the container x does not match ...") so that a refusal of host_read can explain itself.
	Reason string
	// Form is the form of the table that matched (Read only).
	Form string
	// Sudo says whether the command runs with sudo -n (a docker read).
	Sudo bool
}

// Rules are what a host allows besides the fixed table: the container pattern and the units of its onboarding.
type Rules struct {
	DockerPattern string
	Units         []string
}

var (
	containerRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)
	// patternRE is the same expression as sshhost's dockerRE, which the onboarding checks a pattern with (defence in depth).
	patternRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}\*?$`)
	// hexRE matches a name that docker could read as an ID or an ID prefix.
	hexRE = regexp.MustCompile(`^[0-9A-Fa-f]+$`)
	// unitRE is the same check as sshhost's, which decides which units the sudoers snippet may name; a test pins both.
	unitRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]{0,100}\.(service|timer|socket)$`)
	numberRE = regexp.MustCompile(`^[0-9]{1,4}$`)
)

// ValidUnit says whether a systemd unit name is acceptable.
func ValidUnit(u string) bool { return unitRE.MatchString(u) }

// ContainerAllowed says whether a container name matches the host's pattern as a whole: a name that ends in one star is a
// prefix, any other pattern must match exactly. An empty pattern allows nothing, and so does a name made only of hex
// digits: docker reads such a name as an ID prefix too, so it could name a container outside the pattern.
func ContainerAllowed(name, pattern string) bool {
	if pattern == "" || !patternRE.MatchString(pattern) || !containerRE.MatchString(name) || hexRE.MatchString(name) {
		return false
	}
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
		return strings.HasPrefix(name, prefix)
	}
	return name == pattern
}

type slotKind int

const (
	litSlot slotKind = iota
	containerSlot
	unitSlot
	numberSlot
)

type slot struct {
	kind slotKind
	text string
}

type form struct {
	text        string
	slots       []slot
	sudo        bool
	needsDocker bool
}

func mustForm(text string, docker bool) form {
	f := form{text: text, sudo: docker, needsDocker: docker}
	for _, w := range strings.Fields(text) {
		switch w {
		case "{NAME}":
			f.slots = append(f.slots, slot{kind: containerSlot})
		case "{UNIT}":
			f.slots = append(f.slots, slot{kind: unitSlot})
		case "{N}":
			f.slots = append(f.slots, slot{kind: numberSlot})
		default:
			f.slots = append(f.slots, slot{kind: litSlot, text: w})
		}
	}
	return f
}

// readForms is the table of reads. It is code: versioned, tested, and nothing outside it can add to it.
var readForms = []form{
	mustForm("docker ps -a", true),
	mustForm("docker logs --tail {N} {NAME}", true),
	mustForm("docker inspect {NAME}", true),
	mustForm("systemctl status {UNIT} --no-pager", false),
	mustForm("systemctl is-active {UNIT}", false),
	mustForm("systemctl list-units --failed --no-pager", false),
	mustForm("journalctl -u {UNIT} -n {N} --no-pager", false),
	mustForm("df -h", false),
	mustForm("free -m", false),
	mustForm("uptime", false),
	mustForm("uname -a", false),
	mustForm("ps -eo pid,user,pcpu,pmem,etime,comm", false),
	mustForm("ss -tln", false),
}

// ReadForms lists the forms of the table as text, for messages and tests.
func ReadForms() []string {
	out := make([]string, len(readForms))
	for i, f := range readForms {
		out[i] = f.text
	}
	return out
}

// match compares the words with a form exactly. When every literal word matches and only a slot does not, near says why
// (it is the nearest miss, used to explain a refusal).
func (f form) match(argv []string, r Rules) (ok bool, near string) {
	if len(argv) != len(f.slots) {
		return false, ""
	}
	ok = true
	for i, s := range f.slots {
		w := argv[i]
		switch s.kind {
		case litSlot:
			if w != s.text {
				return false, ""
			}
		case containerSlot:
			if !ContainerAllowed(w, r.DockerPattern) {
				ok = false
				if near == "" {
					if r.DockerPattern == "" {
						near = "this host has no container rules, so docker commands are not possible"
					} else {
						near = fmt.Sprintf("the container %q does not match the host's pattern %q", w, r.DockerPattern)
					}
				}
			}
		case unitSlot:
			if !ValidUnit(w) {
				ok = false
				if near == "" {
					near = fmt.Sprintf("%q is not a unit name such as node_exporter.service", w)
				}
			}
		case numberSlot:
			n, err := strconv.Atoi(w)
			if !numberRE.MatchString(w) || err != nil || n < 1 || n > 2000 {
				ok = false
				if near == "" {
					near = fmt.Sprintf("%q is not a whole number from 1 to 2000", w)
				}
			}
		}
	}
	if ok && f.needsDocker && r.DockerPattern == "" {
		return false, "this host has no container rules, so docker commands are not possible"
	}
	return ok, near
}

// Classify says what a command is on a host with these rules. The order matters: invalid words first, then the never-list
// (so that nothing on it can ever match a form), then the table.
func Classify(argv []string, r Rules) Verdict {
	if err := ValidateArgv(argv); err != nil {
		return Verdict{Kind: Invalid, Reason: err.Error()}
	}
	if why := NeverReason(argv); why != "" {
		return Verdict{Kind: Never, Reason: why}
	}
	nearest := ""
	for _, f := range readForms {
		ok, near := f.match(argv, r)
		if ok {
			return Verdict{Kind: Read, Form: f.text, Sudo: f.sudo}
		}
		if near != "" && nearest == "" {
			nearest = near
		}
	}
	return Verdict{Kind: Change, Reason: nearest}
}

var changeVerbs = []string{"start", "stop", "restart"}

// ChangeCheck looks at the two shapes of a change that a host's rules can judge before an approval is asked for:
// `docker start|stop|restart NAME` (the name must match the container pattern) and `systemctl start|stop|restart UNIT`
// (the unit must be one the host lists, with its suffix). The sudoers rule takes more words than the pattern checks, so a
// docker or systemctl start|stop|restart with more than one name (or with a flag) is refused: one container or unit at a
// time. A change of any other shape passes: the sudoers file decides.
func ChangeCheck(argv []string, r Rules) error {
	if len(argv) < 2 || !slices.Contains(changeVerbs, argv[1]) {
		return nil
	}
	// The program may be an absolute path (the stock sudoers rule names /usr/bin/docker): sudo resolves the same rule.
	prog := path.Base(argv[0])
	if (prog == "docker" || prog == "systemctl") && len(argv) > 3 {
		return ArgError("one container (or unit) at a time")
	}
	if len(argv) != 3 {
		return nil
	}
	switch prog {
	case "docker":
		if r.DockerPattern == "" {
			return ArgError("this host has no container rules, so docker commands are not possible")
		}
		if !ContainerAllowed(argv[2], r.DockerPattern) {
			return ArgError(fmt.Sprintf("the container %q does not match the host's pattern %q", argv[2], r.DockerPattern))
		}
	case "systemctl":
		if !slices.Contains(r.Units, argv[2]) {
			if len(r.Units) == 0 {
				return ArgError("this host lists no units, so systemctl start, stop and restart are not possible")
			}
			return ArgError(fmt.Sprintf("the unit %q is not one this host lists (write it with its suffix); the units are: %s", argv[2], strings.Join(r.Units, ", ")))
		}
	}
	return nil
}
