package gatekeeper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/Jaydee94/remedy/internal/hostexec"
	"github.com/Jaydee94/remedy/internal/store"
)

// HostInfo is what the host tools know about one host of a run. It carries no address and no credential.
type HostInfo struct {
	Name          string
	Notes         string // the maintainer's text
	DockerPattern string
	Units         []string
	// SudoNone says that the last connection test found that sudo cannot run without a password for the account.
	SudoNone bool
	// ChangedAt is when the host row last changed, including its pinned key and its login. A change after an approval was
	// asked for makes the approval stale.
	ChangedAt time.Time
}

// ErrHostTimeout is what HostAccess.Exec returns (it may be wrapped) when the host did not answer in time. For a read it
// is a plain failure. For a change it means that the outcome is unknown: the command was sent, and the signal that asks it
// to stop is best effort, so it may have run, wholly or in part.
var ErrHostTimeout = errors.New("the host did not answer in time")

// HostAccess is what the host tools need. Its implementation opens the sealed login and talks to the host; this package
// never sees a credential.
type HostAccess interface {
	// Hosts returns the hosts of a run that can be used now: enabled, with a login and a pinned key. A host that was deleted
	// or lost one of those is not in it.
	Hosts(ctx context.Context, runID string) ([]HostInfo, error)
	// Exec runs the argv on a host of the run, with sudo -n when sudo is set. A timeout is ErrHostTimeout. It returns an
	// ArgumentError with a fixed sentence for any other failure of the connection; any other error is the control plane's
	// and is not shown to the agent.
	Exec(ctx context.Context, runID, host string, sudo bool, argv []string) (hostexec.Result, error)
}

const (
	hostWaits       = " This changes the host, so the maintainer has to approve the call first, word for word; the call waits until they decide."
	inspectNotShown = "[docker inspect printed something that could not be filtered, so it is not shown]"
	readTimedOut    = "the host did not answer in time"
	// changeTimedOut says what is known: nothing. It does not suggest to repeat the command.
	changeTimedOut = "the host did not answer in time. The command was sent, so the change may or may not have happened, wholly or in part. " +
		"Do not repeat it before you have looked at the host with host_read, and tell the maintainer what you know."
)

var hostNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$`)

type hostCall struct {
	Host string   `json:"host"`
	Argv []string `json:"argv"`
}

const hostCallProps = `"host":{"type":"string","description":"The name of one of your hosts (host_list shows them)."},` +
	`"argv":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":64,"description":"The program and its arguments as separate words. There is no shell: no pipes, no redirects, no quoting."}`

// HostTools returns the tools of the host group: host_list, host_read and host_change. They are offered only to runs that
// were started with hosts.
func HostTools(a HostAccess) []Tool {
	h := &hostTools{a: a}
	return []Tool{h.list(), h.read(), h.change()}
}

type hostTools struct{ a HostAccess }

// host finds one of the run's usable hosts by name, without regard to case.
func (h *hostTools) host(ctx context.Context, runID, name string) (HostInfo, error) {
	infos, err := h.a.Hosts(ctx, runID)
	if err != nil {
		return HostInfo{}, err
	}
	for _, i := range infos {
		if strings.EqualFold(i.Name, name) {
			return i, nil
		}
	}
	return HostInfo{}, ArgumentError(fmt.Sprintf("there is no host %q available to you; host_list shows the hosts you may use", name))
}

func (i HostInfo) rules() hostexec.Rules {
	return hostexec.Rules{DockerPattern: i.DockerPattern, Units: i.Units}
}

// exec runs the words and turns a timeout into what the agent may be told. The words of a change that timed out have an
// unknown outcome. Every other error is passed on: an ArgumentError is shown, any other is "the tool failed".
func (h *hostTools) exec(ctx context.Context, runID string, info HostInfo, sudo bool, argv []string, change bool) (hostexec.Result, error) {
	res, err := h.a.Exec(ctx, runID, info.Name, sudo, argv)
	switch {
	case errors.Is(err, ErrHostTimeout) && change:
		return hostexec.Result{}, ArgumentError(changeTimedOut)
	case errors.Is(err, ErrHostTimeout):
		return hostexec.Result{}, ArgumentError(readTimedOut)
	}
	return res, err
}

// visible checks that the words show as what they are: Display does not escape a right-to-left override, a zero-width
// character, a no-break space or a line separator, and none of them is a control character. What the maintainer reads on the
// approval card has to be the command that runs, so a change that has one is refused.
func visible(argv []string) error {
	for i, w := range argv {
		for _, r := range w {
			if !unicode.IsPrint(r) && r != ' ' {
				return ArgumentError(fmt.Sprintf("word %d has a character that is invisible or not printable (a zero-width or right-to-left mark, a no-break space, a line separator, a private-use character); the maintainer reads the command as text, so such characters are refused", i+1))
			}
		}
	}
	return nil
}

// checkChange is everything about the words of a change that needs no host. It is applied when the call is made and again
// before the approved call runs.
func checkChange(argv []string) error {
	if err := hostexec.ValidateArgv(argv); err != nil {
		return ArgumentError(err.Error())
	}
	if err := visible(argv); err != nil {
		return err
	}
	if err := hostexec.ValidateProgram(argv); err != nil {
		return ArgumentError(err.Error())
	}
	if why := hostexec.NeverReason(argv); why != "" {
		return ArgumentError("Remedy never runs this command, not even after an approval: " + why)
	}
	return nil
}

// decodeHostCall is the Decode of host_read and host_change. For a change it also applies the rules that need no host.
func decodeHostCall(raw json.RawMessage, change bool) (json.RawMessage, error) {
	var v hostCall
	if err := DecodeArgs(raw, &v); err != nil {
		return nil, err
	}
	if !hostNameRE.MatchString(v.Host) {
		return nil, ArgumentError("host must be the name of one of your hosts; host_list shows them")
	}
	if change {
		if err := checkChange(v.Argv); err != nil {
			return nil, err
		}
	} else if err := hostexec.ValidateArgv(v.Argv); err != nil {
		return nil, ArgumentError(err.Error())
	}
	return json.Marshal(v)
}

// hostText is the text of a result for the model: valid UTF-8 (a host prints bytes). The caller's sanitize redacts it and
// limits its size.
func hostText(s string) string { return strings.ToValidUTF8(s, "�") }

func (h *hostTools) list() Tool {
	return Tool{
		Name:        "host_list",
		Description: "Lists the hosts you may use, with what is possible on each: the container names and systemd units that changes may touch, and whether sudo works. Look at it first.",
		Group:       GroupHost,
		Schema:      schema(``),
		Decode: func(raw json.RawMessage) (json.RawMessage, error) {
			var v struct{}
			if err := DecodeArgs(raw, &v); err != nil {
				return nil, err
			}
			return json.RawMessage("{}"), nil
		},
		Run: func(ctx context.Context, c Call) (string, error) {
			infos, err := h.a.Hosts(ctx, c.RunID)
			if err != nil {
				return "", err
			}
			if len(infos) == 0 {
				return "You have no usable host: none of them is enabled with a login and a pinned key.", nil
			}
			var b strings.Builder
			b.WriteString("The hosts you may use. The notes are the maintainer's text.\n")
			for _, i := range infos {
				fmt.Fprintf(&b, "\n%s\n", i.Name)
				if i.Notes != "" {
					fmt.Fprintf(&b, "  notes: %s\n", clip(i.Notes, 300))
				}
				containers := "none: docker commands are not possible"
				if i.DockerPattern != "" {
					containers = i.DockerPattern
				}
				fmt.Fprintf(&b, "  containers: %s\n", containers)
				units := "none"
				if len(i.Units) > 0 {
					units = strings.Join(i.Units, ", ")
				}
				fmt.Fprintf(&b, "  units: %s\n", units)
				if i.SudoNone {
					b.WriteString("  sudo: does not work for this account, so host_change cannot run here\n")
				}
			}
			return hostText(b.String()), nil
		},
	}
}

func (h *hostTools) read() Tool {
	return Tool{
		Name: "host_read",
		Description: "Runs one command on a host and returns its output, without asking. Only these commands run: " +
			strings.Join(hostexec.ReadForms(), "; ") + ". A name in capitals is a slot: NAME a container of the host, UNIT a systemd unit, N a number from 1 to 2000. " +
			"argv is the program and its arguments as separate words. Anything else is refused: use host_change for it.",
		Group:  GroupHost,
		Schema: schema(hostCallProps, "host", "argv"),
		Decode: func(raw json.RawMessage) (json.RawMessage, error) { return decodeHostCall(raw, false) },
		Run: func(ctx context.Context, c Call) (string, error) {
			var v hostCall
			if err := json.Unmarshal(c.Args, &v); err != nil {
				return "", err
			}
			info, err := h.host(ctx, c.RunID, v.Host)
			if err != nil {
				return "", err
			}
			verdict := hostexec.Classify(v.Argv, info.rules())
			switch verdict.Kind {
			case hostexec.Read:
			case hostexec.Never:
				return "", ArgumentError("this command is never run on a host: " + verdict.Reason)
			case hostexec.Invalid:
				return "", ArgumentError(verdict.Reason)
			default:
				msg := "host_read runs only these commands: " + strings.Join(hostexec.ReadForms(), "; ") + "."
				if verdict.Reason != "" {
					msg += " " + verdict.Reason + "."
				}
				return "", ArgumentError(msg + " For anything else use host_change, which waits for the maintainer's approval.")
			}
			res, err := h.exec(ctx, c.RunID, info, verdict.Sudo, v.Argv, false)
			if err != nil {
				return "", err
			}
			if hostexec.IsInspect(v.Argv) && res.Stdout != "" {
				// What cannot be filtered is never shown as it is: the environment may hold secrets.
				if filtered, ok := hostexec.FilterInspect(res.Stdout); ok && !res.StdoutCut {
					res.Stdout = filtered
				} else {
					res.Stdout = inspectNotShown
					res.StdoutCut = false
				}
			}
			return hostText(hostexec.FormatResult(info.Name, res)), nil
		},
	}
}

func (h *hostTools) change() Tool {
	return Tool{
		Name: "host_change",
		Description: "Runs one command on a host as root through sudo -n and returns its output. argv is the program and its arguments as separate words; there is no shell. " +
			"For the containers and units of a host, host_list says what the host allows. A few commands are never run, also when approved (shells, disk tools, access files, power, firewalls)." + hostWaits,
		Mutating:     true,
		Group:        GroupHost,
		ActivityKind: store.KindHostAction,
		Schema:       schema(hostCallProps, "host", "argv"),
		Decode:       func(raw json.RawMessage) (json.RawMessage, error) { return decodeHostCall(raw, true) },
		CheckRun: func(ctx context.Context, runID string, args json.RawMessage) error {
			var v hostCall
			if err := json.Unmarshal(args, &v); err != nil {
				return err
			}
			info, err := h.host(ctx, runID, v.Host)
			if err != nil {
				return err
			}
			if info.SudoNone {
				return ArgumentError("sudo cannot run without a password for the account on " + info.Name + ", so a change cannot run; the maintainer has to fix the sudoers file and test the connection again")
			}
			if err := hostexec.ChangeCheck(v.Argv, info.rules()); err != nil {
				return ArgumentError(err.Error())
			}
			return nil
		},
		Activity: func(args json.RawMessage) string {
			var v hostCall
			_ = json.Unmarshal(args, &v)
			return fmt.Sprintf("Ran %s on %s", hostexec.Display(v.Argv), v.Host)
		},
		Run: func(ctx context.Context, c Call) (string, error) {
			var v hostCall
			if err := json.Unmarshal(c.Args, &v); err != nil {
				return "", err
			}
			// Checked again on the stored arguments: a call that reached the database some other way is still not run, and
			// the words are the ones the maintainer read.
			if err := checkChange(v.Argv); err != nil {
				return "", err
			}
			info, err := h.host(ctx, c.RunID, v.Host)
			if err != nil {
				return "", err
			}
			if info.ChangedAt.After(c.RequestedAt) {
				return "", ArgumentError("the host " + info.Name + " changed after the approval was asked for (its address, its key or its login), so nothing was run; ask again if the command is still wanted")
			}
			if err := hostexec.ChangeCheck(v.Argv, info.rules()); err != nil {
				return "", ArgumentError(err.Error())
			}
			res, err := h.exec(ctx, c.RunID, info, true, v.Argv, true)
			if err != nil {
				return "", err
			}
			return hostText(hostexec.FormatResult(info.Name, res)), nil
		},
	}
}
