package sshhost

import (
	"context"
	"errors"
	"time"

	"golang.org/x/crypto/ssh"
)

// ErrShell means the account's shell did not print the canary back: it does not treat quoted words like a POSIX shell, so
// the quoting that keeps a word a word cannot be trusted. Nothing but the canary was sent.
var ErrShell = errors.New("the account's shell does not treat quoted words like a POSIX shell")

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
// stream. A command that does not end in time is stopped with ErrTimeout.
func (c *Client) RunSplit(ctx context.Context, cmd string, limit int, timeout time.Duration) (ExecResult, error) {
	sess, err := c.c.NewSession()
	if err != nil {
		return ExecResult{}, ErrUnreachable
	}
	defer sess.Close()
	out, errw := &capWriter{max: limit}, &capWriter{max: limit}
	sess.Stdout, sess.Stderr = out, errw
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		res := ExecResult{}
		res.Stdout, res.StdoutCut = out.result()
		res.Stderr, res.StderrCut = errw.result()
		var ee *ssh.ExitError
		switch {
		case errors.As(err, &ee):
			res.ExitCode = ee.ExitStatus()
		case err != nil:
			return res, ErrUnreachable
		}
		return res, nil
	case <-timer.C:
		return ExecResult{}, ErrTimeout
	case <-ctx.Done():
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
	if o.Limit <= 0 {
		o.Limit = defaultExecLimit
	}
	if o.Timeout <= 0 {
		o.Timeout = defaultExecTimeout
	}
	defer s.lock(hostID)()
	cl, err := Dial(ctx, t, cred, pinLine)
	if err != nil {
		return ExecResult{}, err
	}
	defer cl.Close()
	canary, err := cl.RunSplit(ctx, o.CanaryCommand, canaryLimit, canaryTimeout)
	if err != nil {
		return ExecResult{}, err
	}
	if canary.ExitCode != 0 || canary.Stdout != o.CanaryWant {
		return ExecResult{}, ErrShell
	}
	return cl.RunSplit(ctx, cmd, o.Limit, o.Timeout)
}
