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
	bin := c.Binary
	if bin == "" {
		bin = "claude"
	}
	cmd := exec.CommandContext(ctx, bin, loginCheckArgs...)
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
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if notLoggedIn.Match(out.Bytes()) {
			return LoginMissing, ""
		}
		return LoginUnknown, fmt.Sprintf("the login check exited with code %d", exit.ExitCode())
	}
	// The binary is missing or cannot be executed. The underlying error may carry a path: the reason stays a fixed text.
	return LoginUnknown, "the login check could not start"
}

// Version is the first line of `claude --version`, shortened to what a version line can hold, or "" when the CLI cannot
// say. It is shown to the maintainer as text.
func (c Claude) Version(ctx context.Context, parentEnv []string) string {
	bin := c.Binary
	if bin == "" {
		bin = "claude"
	}
	cmd := exec.CommandContext(ctx, bin, "--version")
	cmd.Env = FilterEnv(parentEnv)
	cmd.Dir = os.TempDir()
	cmd.WaitDelay = 2 * time.Second
	var out capBuffer
	cmd.Stdout = &out
	if cmd.Run() != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(out.String()), "\n")
	line = strings.TrimSpace(line)
	if len(line) > 64 || strings.IndexFunc(line, func(r rune) bool { return r < ' ' || r > '~' }) >= 0 {
		return ""
	}
	return line
}
