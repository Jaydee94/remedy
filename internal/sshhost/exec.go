package sshhost

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/ssh"
)

// ErrShell means the account's shell did not print the canary back: it does not treat quoted words like a POSIX shell, so
// the quoting that keeps a word a word cannot be trusted. Nothing but the canary was sent.
var ErrShell = errors.New("the account's shell does not treat quoted words like a POSIX shell")

// ErrLostAfterStart means the command was started on the host and the connection then broke before its exit status
// arrived: the command may have run, may still run, or may have failed, so the outcome is unknown. It is not a dial
// failure (ErrUnreachable) and not a timeout (ErrTimeout).
var ErrLostAfterStart = errors.New("the connection broke after the command started")

const (
	defaultExecLimit   = 24 << 10
	defaultExecTimeout = 30 * time.Second
	canaryLimit        = 4096
	canaryTimeout      = 10 * time.Second
)

// ExecResult is the answer of a command with its two streams apart.
type ExecResult struct {
	Stdout, Stderr       string
	ExitCode             int
	StdoutCut, StderrCut bool
}

// RunSplit runs one command in its own session (no pty, no environment, no input) and keeps at most limit bytes of each
// stream. A limit or timeout of zero or less means 24 KB and 30 seconds. A command that does not end in time (or when ctx
// ends) is asked to stop with SIGTERM (best effort: a server may ignore it) before the session is closed, and the call
// answers ErrTimeout.
func (c *Client) RunSplit(ctx context.Context, cmd string, limit int, timeout time.Duration) (ExecResult, error) {
	if limit <= 0 {
		limit = defaultExecLimit
	}
	if timeout <= 0 {
		timeout = defaultExecTimeout
	}
	sess, err := c.c.NewSession()
	if err != nil {
		return ExecResult{}, ErrUnreachable
	}
	defer sess.Close()
	out, errw := &capWriter{max: limit}, &capWriter{max: limit}
	sess.Stdout, sess.Stderr = out, errw
	// Start and Wait apart, so that a failure to start (nothing ran) differs from a connection that breaks while the
	// command runs (unknown outcome).
	type ended struct {
		err error
		// started is false only when the server answered the exec request with "no": the command did not run. Any other
		// failure of Start (the connection broke while the answer was awaited) may have come after the server started it.
		started bool
	}
	rejected := fmt.Sprintf("ssh: command %v failed", cmd) // what the library says for an exec request that was answered "no"
	done := make(chan ended, 1)
	go func() {
		if err := sess.Start(cmd); err != nil {
			done <- ended{err: err, started: err.Error() != rejected}
			return
		}
		done <- ended{err: sess.Wait(), started: true}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case e := <-done:
		res := ExecResult{}
		res.Stdout, res.StdoutCut = out.result()
		res.Stderr, res.StderrCut = errw.result()
		var ee *ssh.ExitError
		switch {
		case errors.As(e.err, &ee):
			res.ExitCode = ee.ExitStatus()
		case e.err != nil && e.started:
			return res, ErrLostAfterStart
		case e.err != nil:
			return res, ErrUnreachable
		}
		return res, nil
	case <-timer.C:
		_ = sess.Signal(ssh.SIGTERM)
		return ExecResult{}, ErrTimeout
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGTERM)
		return ExecResult{}, ErrTimeout
	}
}

// ExecOptions say how a command is run. Zero Limit and Timeout mean 24 KB per stream and 30 seconds.
type ExecOptions struct {
	Limit   int
	Timeout time.Duration
	// CanaryCommand is run first, in the same connection; its stdout must be exactly CanaryWant and it must exit 0.
	CanaryCommand string
	CanaryWant    string
}

// Exec connects against the pinned key, proves with the canary that the account's shell reads quoted words like a POSIX
// shell, runs the command line and closes the connection. Only one connection per host is open at a time.
func (s *Service) Exec(ctx context.Context, hostID int64, t Target, cred Credential, pinLine, cmd string, o ExecOptions) (ExecResult, error) {
	// Without a canary every shell would pass (an empty command prints nothing and exits 0): refuse before any login.
	if o.CanaryCommand == "" || o.CanaryWant == "" {
		return ExecResult{}, ErrShell
	}
	defer s.lock(hostID)()
	// ErrTimeout means that the command was sent and did not end: its outcome is unknown. A timeout of the dial or of the
	// canary happens before that, so it is a host that cannot be reached.
	cl, err := Dial(ctx, t, cred, pinLine)
	if errors.Is(err, ErrTimeout) {
		return ExecResult{}, ErrUnreachable
	}
	if err != nil {
		return ExecResult{}, err
	}
	defer cl.Close()
	canary, err := cl.RunSplit(ctx, o.CanaryCommand, canaryLimit, canaryTimeout)
	if errors.Is(err, ErrLostAfterStart) || errors.Is(err, ErrTimeout) {
		// Only the canary had started: nothing that changes a host has run yet.
		return ExecResult{}, ErrUnreachable
	}
	if err != nil {
		return ExecResult{}, err
	}
	if canary.ExitCode != 0 || canary.Stdout != o.CanaryWant {
		return ExecResult{}, ErrShell
	}
	return cl.RunSplit(ctx, cmd, o.Limit, o.Timeout)
}
