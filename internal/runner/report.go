package runner

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Jaydee94/remedy/internal/provider"
)

const (
	// DefaultReportInterval is how often the runner tells the control plane its login state. It also keeps the control
	// plane's "last heard from the runner" fresh while a long run is silent.
	DefaultReportInterval = 15 * time.Second
	// DefaultLoginCheckOK and DefaultLoginCheckElse are how often the login is checked while it is fine and while it is
	// not: the page should turn green within a minute of a login.
	DefaultLoginCheckOK   = 10 * time.Minute
	DefaultLoginCheckElse = time.Minute

	// loginCheckTimeout bounds one check: it takes 0.06 to 0.10 s (spike S2), so 10 s only ever ends a hang. A timeout is
	// "unknown", never "not logged in".
	loginCheckTimeout = 10 * time.Second
)

// NextCheck is how long to wait before the next login check.
func NextCheck(state provider.LoginState, okEvery, elseEvery time.Duration) time.Duration {
	if state == provider.LoginOK {
		return okEvery
	}
	return elseEvery
}

// Reporter posts the runner's status to the control plane at once and then every Interval. An answer from the control
// plane means the runner is connected; a failure means it is not.
type Reporter struct {
	Client   *Client
	Status   *Status
	Interval time.Duration // default DefaultReportInterval
	Log      *slog.Logger
}

func (r *Reporter) Run(ctx context.Context) {
	interval := r.Interval
	if interval <= 0 {
		interval = DefaultReportInterval
	}
	// Loop.Run writes the same fact from the claim: the two disagree only when the claim and status endpoints disagree,
	// and then not ready is the right answer.
	for {
		if err := r.Client.ReportStatus(ctx, r.Status.Report()); err != nil {
			if ctx.Err() != nil {
				return
			}
			r.Status.SetConnected(false)
			r.Log.Warn("status report failed", "err", err)
		} else {
			r.Status.SetConnected(true)
		}
		if !sleepCtx(ctx, interval) {
			return
		}
	}
}

// LoginChecker asks the CLI whether it is logged in, at once and then every OKEvery while it is and every ElseEvery
// while it is not, and keeps the answer, and the CLI's version line, in the status. Check is the CLI's own command; its
// output never gets here, only the state and a short reason.
type LoginChecker struct {
	Check     func(context.Context) (provider.LoginState, string)
	Version   func(context.Context) string // optional
	Status    *Status
	OKEvery   time.Duration // default DefaultLoginCheckOK
	ElseEvery time.Duration // default DefaultLoginCheckElse
	Log       *slog.Logger
	// Pause, when set and true, skips the check: a login of the relay is rewriting the CLI's config. It is asked before
	// each check and again after it, and a result obtained while it turned true is discarded.
	Pause func() bool

	kickMu sync.Mutex
	kick   chan struct{}
}

// kicks is the kick channel, made on first use so that Kick is safe before Run.
func (c *LoginChecker) kicks() chan struct{} {
	c.kickMu.Lock()
	defer c.kickMu.Unlock()
	if c.kick == nil {
		c.kick = make(chan struct{}, 1)
	}
	return c.kick
}

// Kick runs the next check now instead of after the wait. It never blocks, and kicks before a check coalesce.
func (c *LoginChecker) Kick() {
	select {
	case c.kicks() <- struct{}{}:
	default:
	}
}

func (c *LoginChecker) paused() bool { return c.Pause != nil && c.Pause() }

func (c *LoginChecker) Run(ctx context.Context) {
	okEvery, elseEvery := c.OKEvery, c.ElseEvery
	if okEvery <= 0 {
		okEvery = DefaultLoginCheckOK
	}
	if elseEvery <= 0 {
		elseEvery = DefaultLoginCheckElse
	}
	kick := c.kicks()
	if c.Version != nil {
		// Read once at start: a CLI upgrade restarts the pod.
		vctx, cancel := context.WithTimeout(ctx, loginCheckTimeout)
		c.Status.SetCLIVersion(c.Version(vctx))
		cancel()
	}
	for {
		wait := elseEvery
		if !c.paused() {
			cctx, cancel := context.WithTimeout(ctx, loginCheckTimeout)
			state, reason := c.Check(cctx)
			cancel()
			if ctx.Err() != nil {
				return
			}
			switch {
			case c.paused():
				c.Log.Info("login check discarded: a login is in progress")
			case state == provider.LoginUnknown:
				c.Status.SetLogin(state, time.Now())
				c.Log.Warn("cannot tell whether the CLI is logged in", "reason", reason)
				wait = NextCheck(state, okEvery, elseEvery)
			default:
				c.Status.SetLogin(state, time.Now())
				c.Log.Info("login check", "login", string(state))
				wait = NextCheck(state, okEvery, elseEvery)
			}
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		case <-kick:
			t.Stop()
		}
	}
}

// sleepCtx waits for d or until ctx ends, and says whether the full wait passed.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
