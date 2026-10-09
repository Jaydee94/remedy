package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// LoginState is what a login check concluded.
type LoginState string

const (
	LoginOK      LoginState = "ok"      // the CLI says it is logged in
	LoginMissing LoginState = "missing" // the CLI says it is not
	LoginUnknown LoginState = "unknown" // the check could not tell
)

// loginCheckArgs is the CLI's own command for "am I logged in" (the record of spike S2,
// docs/research/k8s-s2-login-check.md). --text matters: the default output is JSON, which says "loggedIn": false and
// never "Not logged in".
var loginCheckArgs = []string{"auth", "status", "--text"}

// notLoggedIn matches the CLI's sentence for a missing login ("Not logged in. Run claude auth login to authenticate.").
// It is matched against output that is never kept.
var notLoggedIn = regexp.MustCompile(`(?i)not logged in`)

// versionLine is all a version line may look like: digits first, then only characters that cannot spell an e-mail
// address, a path or a sentence. Anything else is not shown ("2.1.288 (Claude Code)" is the real output).
var versionLine = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+[ 0-9A-Za-z().+-]*$`)

// binary is the CLI to start: the configured one, or `claude` from the PATH.
func (c Claude) binary() string {
	if c.Binary == "" {
		return "claude"
	}
	return c.Binary
}

// maxCheckOutput bounds what the check reads of the CLI's output.
const maxCheckOutput = 8 << 10

// capBuffer keeps at most maxCheckOutput bytes and drops the rest, so that a chatty CLI cannot fill memory. It claims to
// have written everything, because a short write would fail the command.
type capBuffer struct{ bytes.Buffer }

func (b *capBuffer) Write(p []byte) (int, error) {
	if room := maxCheckOutput - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// LoginCheck runs the CLI's status command and says whether it is logged in. It starts the unmodified binary with the
// allowlisted environment and looks at the exit code and at the shape of the output; it never opens a file of the login,
// and the output (which may name an account) is not returned, logged or kept. The reason names an exit code or a
// timeout, never output. The caller sets the time limit with ctx.
func (c Claude) LoginCheck(ctx context.Context, parentEnv []string) (LoginState, string) {
	cmd := exec.CommandContext(ctx, c.binary(), loginCheckArgs...)
	cmd.Env = FilterEnv(parentEnv)
	cmd.Dir = os.TempDir()
	cmd.WaitDelay = 2 * time.Second
	var out capBuffer
	cmd.Stdout, cmd.Stderr = &out, &out

	err := cmd.Run()
	switch {
	case err == nil:
		return LoginOK, ""
	case ctx.Err() != nil:
		return LoginUnknown, "the login check timed out"
	}
	// A CLI that exits but leaves a child holding its output makes Run return ErrWaitDelay, with the process's own
	// outcome in ProcessState: that outcome is the answer.
	var exit *exec.ExitError
	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil {
		if cmd.ProcessState.Success() {
			return LoginOK, ""
		}
		return loginExited(cmd.ProcessState.ExitCode(), &out)
	}
	if errors.As(err, &exit) {
		return loginExited(exit.ExitCode(), &out)
	}
	// The binary is missing or cannot be executed. The underlying error may carry a path: the reason stays a fixed text.
	return LoginUnknown, "the login check could not start"
}

// loginExited says what a status command that exited with a failure means. The code is -1 when a signal ended it.
func loginExited(code int, out *capBuffer) (LoginState, string) {
	switch {
	case code < 0:
		return LoginUnknown, "the login check was stopped by a signal"
	case notLoggedIn.Match(out.Bytes()):
		return LoginMissing, ""
	}
	return LoginUnknown, fmt.Sprintf("the login check exited with code %d", code)
}

// Version is the first line of `claude --version`, accepted only if it looks like a version (a number first, no characters that could spell an e-mail address or a path), or "" when the CLI cannot
// say. It is shown to the maintainer as text.
func (c Claude) Version(ctx context.Context, parentEnv []string) string {
	cmd := exec.CommandContext(ctx, c.binary(), "--version")
	cmd.Env = FilterEnv(parentEnv)
	cmd.Dir = os.TempDir()
	cmd.WaitDelay = 2 * time.Second
	var out capBuffer
	cmd.Stdout = &out
	// A successful process that left a child holding its output (ErrWaitDelay) has still said its version.
	if err := cmd.Run(); err != nil && !(errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success()) {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(out.String()), "\n")
	line = strings.TrimSpace(line)
	if len(line) > 64 || !versionLine.MatchString(line) {
		return ""
	}
	return line
}
