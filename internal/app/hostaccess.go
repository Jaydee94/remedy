package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/hostexec"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/sshhost"
	"github.com/Jaydee94/remedy/internal/store"
)

// HostAccess implements gatekeeper.HostAccess. It is the only place where the host tools meet a credential: it opens the
// sealed login, holds the pinned key and talks to the host through sshhost. The gatekeeper package sees none of it.
type HostAccess struct {
	Store *store.Store
	Key   secret.Key
	SSH   *sshhost.Service
	// CommandTimeout bounds the command itself (not the dial or the canary). Zero means 30 seconds.
	CommandTimeout time.Duration
	// Log receives one debug line for a failed command: the host and the category of the failure, never the text of the
	// error. Nil means slog.Default().
	Log *slog.Logger
}

func (a *HostAccess) log() *slog.Logger {
	if a.Log != nil {
		return a.Log
	}
	return slog.Default()
}

// failureCategory names the kind of a failed command for the log. The set is closed: nothing the library or the host said,
// no address, no account.
func failureCategory(err error) string {
	switch {
	case errors.Is(err, sshhost.ErrTimeout):
		return "timeout"
	case errors.Is(err, sshhost.ErrLostAfterStart):
		return "lost_after_start"
	case errors.Is(err, sshhost.ErrHostKeyChanged):
		return "hostkey"
	case errors.Is(err, sshhost.ErrAuth):
		return "auth"
	case errors.Is(err, sshhost.ErrRefused):
		return "refused"
	case errors.Is(err, sshhost.ErrShell):
		return "shell"
	case errors.Is(err, sshhost.ErrNoPin), errors.Is(err, sshhost.ErrBadCredential):
		return "not_set_up"
	}
	return "unreachable"
}

const defaultHostCommandTimeout = 30 * time.Second

// usable says whether a host can be given a command now: enabled, with a login and a pinned key.
func usable(e store.HostEntry) bool {
	return e.Host.Enabled && e.Credential != nil && e.Host.PinnedKey != ""
}

// Hosts returns the usable hosts of a run. A host that was deleted, disabled, or lost its login or its pin is not in it.
// Nothing about how to reach a host (address, account, login, key) is in the answer.
func (a *HostAccess) Hosts(ctx context.Context, runID string) ([]gatekeeper.HostInfo, error) {
	entries, err := a.Store.RunHosts(ctx, runID)
	if err != nil {
		return nil, err
	}
	var out []gatekeeper.HostInfo
	for _, e := range entries {
		if !usable(e) {
			continue
		}
		info := gatekeeper.HostInfo{
			Name: e.Host.Name, Notes: e.Host.Notes, DockerPattern: e.Host.DockerPattern, Units: e.Host.SystemdUnits, ChangedAt: e.Host.UpdatedAt,
		}
		if e.Host.Report != "" {
			var rep sshhost.Report
			if json.Unmarshal([]byte(e.Host.Report), &rep) == nil && rep.Sudo == sshhost.SudoNone {
				info.SudoNone = true
			}
		}
		out = append(out, info)
	}
	return out, nil
}

// Exec runs the argv on a host of the run. A failure of the connection becomes a fixed sentence for the agent: nothing the
// library or the host said, no address, no account. A command that did not end in time, or whose connection broke after
// it started, has an unknown outcome and answers gatekeeper.ErrHostTimeout.
func (a *HostAccess) Exec(ctx context.Context, runID, host string, sudo bool, argv []string) (hostexec.Result, error) {
	entries, err := a.Store.RunHosts(ctx, runID)
	if err != nil {
		return hostexec.Result{}, err
	}
	e, err := pickHost(entries, host)
	if err != nil {
		return hostexec.Result{}, err
	}
	raw, err := a.Key.Open(e.Credential.Ciphertext, store.HostCredentialAAD(e.Host.ID))
	if errors.Is(err, secret.ErrOpen) {
		return hostexec.Result{}, gatekeeper.ArgumentError("the stored login of the host cannot be decrypted; the maintainer has to enter it again")
	}
	if err != nil {
		return hostexec.Result{}, err
	}
	cred, err := sshhost.ParseCredential(raw)
	if err != nil {
		return hostexec.Result{}, gatekeeper.ArgumentError("the stored login of the host cannot be used; the maintainer has to enter it again")
	}
	timeout := a.CommandTimeout
	if timeout <= 0 {
		timeout = defaultHostCommandTimeout
	}
	target := sshhost.Target{Address: e.Host.Address, Port: e.Host.Port, Account: e.Host.Account}
	res, err := a.SSH.Exec(ctx, e.Host.ID, target, cred, e.Host.PinnedKey, hostexec.CommandLine(argv, sudo), sshhost.ExecOptions{
		Limit: hostexec.StreamLimit, Timeout: timeout,
		CanaryCommand: hostexec.CanaryCommand(), CanaryWant: hostexec.CanaryWant(),
	})
	if err != nil {
		a.log().Debug("a command on a host failed", "host", e.Host.Name, "category", failureCategory(err))
	}
	if errors.Is(err, sshhost.ErrTimeout) || errors.Is(err, sshhost.ErrLostAfterStart) {
		// The host tools tell a timed-out change apart from a failed one: its outcome is unknown.
		return hostexec.Result{}, gatekeeper.ErrHostTimeout
	}
	if err != nil {
		return hostexec.Result{}, gatekeeper.ArgumentError(hostFailure(err))
	}
	return hostexec.Result{Stdout: res.Stdout, Stderr: res.Stderr, ExitCode: res.ExitCode, StdoutCut: res.StdoutCut, StderrCut: res.StderrCut}, nil
}

// pickHost finds the usable host of a run that a name means: the exact name first, and a name that differs only in case when
// exactly one host does. Names are unique without regard to case in the database, so more than one is not expected; if it
// happens, the name is refused rather than guessed.
func pickHost(entries []store.HostEntry, name string) (*store.HostEntry, error) {
	var folded *store.HostEntry
	matches := 0
	for i := range entries {
		if !usable(entries[i]) {
			continue
		}
		if entries[i].Host.Name == name {
			return &entries[i], nil
		}
		if strings.EqualFold(entries[i].Host.Name, name) {
			folded = &entries[i]
			matches++
		}
	}
	switch matches {
	case 0:
		return nil, gatekeeper.ArgumentError(fmt.Sprintf("there is no host %q available to you; host_list shows the hosts you may use", name))
	case 1:
		return folded, nil
	}
	return nil, gatekeeper.ArgumentError(fmt.Sprintf("the host name %q is ambiguous: more than one host has it apart from its case; use the exact name from host_list", name))
}

// hostFailure is the sentence the agent gets for a failed connection. The set is closed.
func hostFailure(err error) string {
	switch {
	case errors.Is(err, sshhost.ErrHostKeyChanged):
		return "the host shows another key than the pinned one, so nothing was sent; the maintainer has to look at it"
	case errors.Is(err, sshhost.ErrAuth):
		return "the host rejected the login"
	case errors.Is(err, sshhost.ErrRefused):
		return "the host refused the connection"
	case errors.Is(err, sshhost.ErrShell):
		return "the account's shell is not supported: it does not treat quoted words like a POSIX shell, or a startup file of the account (.bashrc, profile) prints something, so nothing was run"
	case errors.Is(err, sshhost.ErrNoPin), errors.Is(err, sshhost.ErrBadCredential):
		return "the host is not set up for use: the maintainer has to check its login and its pinned key"
	}
	return "the host cannot be reached"
}
