package runner

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// DefaultHeartbeatInterval is how often the runner of a run with gatekeeper access reports to the control plane.
const DefaultHeartbeatInterval = 10 * time.Second

// Beat is the control plane's answer to a heartbeat.
type Beat struct {
	// Waiting means the run waits for the maintainer's decision: its time budget stands still.
	Waiting bool `json:"waiting"`
	// Cancel means the maintainer cancelled the run: the agent is stopped.
	Cancel bool `json:"cancel"`
}

// ErrRunGone means the control plane no longer has the run as running: it was finished, or the reaper failed it.
var ErrRunGone = errors.New("the control plane no longer has this run as running")

// Heartbeat tells the control plane that the runner of a run is alive, and learns whether the run waits for an
// approval and whether it was cancelled.
func (c *Client) Heartbeat(ctx context.Context, runID string) (Beat, error) {
	resp, err := c.do(ctx, requestTimeout, "/runner/v1/runs/"+runID+"/heartbeat", nil)
	if err != nil {
		return Beat{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Beat{}, ErrRunGone
	}
	if err := expect(resp, http.StatusOK); err != nil {
		return Beat{}, err
	}
	var b Beat
	if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
		return Beat{}, err
	}
	return b, nil
}

// runClock is the time limit of a run: a budget of running time. It can stand still, which is what a run does while
// it waits for an approval, and it calls onExpire once when the budget is used up.
type runClock struct {
	mu        sync.Mutex
	remaining time.Duration
	since     time.Time // when the clock last started running; zero while it stands still
	timer     *time.Timer
	expired   bool
	stopped   bool
	onExpire  func()
}

func newRunClock(budget time.Duration, onExpire func()) *runClock {
	c := &runClock{remaining: budget, onExpire: onExpire}
	c.mu.Lock()
	c.startLocked()
	c.mu.Unlock()
	return c
}

func (c *runClock) startLocked() {
	c.since = time.Now()
	c.timer = time.AfterFunc(c.remaining, c.expire)
}

func (c *runClock) expire() {
	c.mu.Lock()
	if c.expired || c.stopped {
		c.mu.Unlock()
		return
	}
	c.expired = true
	c.mu.Unlock()
	c.onExpire()
}

// Pause makes the clock stand still. What is left of the budget is kept.
func (c *runClock) Pause() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.since.IsZero() || c.expired || c.stopped {
		return
	}
	c.timer.Stop()
	c.remaining = max(0, c.remaining-time.Since(c.since))
	c.since = time.Time{}
}

// Resume lets a paused clock run again.
func (c *runClock) Resume() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.since.IsZero() || c.expired || c.stopped {
		return
	}
	c.startLocked()
}

// Stop ends the clock for good: it will not expire any more.
func (c *runClock) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
	if c.timer != nil {
		c.timer.Stop()
	}
}

// Expired reports whether the budget was used up.
func (c *runClock) Expired() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.expired
}

// watch is the heartbeat of a run with gatekeeper access. It reports at once and then every interval until ctx ends.
// While the run waits for an approval the clock stands still. When the run was cancelled, or the control plane no
// longer has it, stop is called and the agent goes. A heartbeat that fails for any other reason is only logged: the
// control plane being unreachable must never stop an agent that works.
func (l *Loop) watch(ctx context.Context, log *slog.Logger, runID string, clock *runClock, stop func()) {
	interval := l.HeartbeatInterval
	if interval <= 0 {
		interval = DefaultHeartbeatInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	failures := 0
	for {
		beat, err := l.Client.Heartbeat(ctx, runID)
		switch {
		case errors.Is(err, ErrRunGone):
			log.Warn("the control plane no longer has this run as running: stopping the agent")
			stop()
			return
		case err != nil:
			if ctx.Err() != nil {
				return
			}
			failures++
			log.Warn("heartbeat failed, the run goes on", "err", err, "failures", failures)
		default:
			failures = 0
			if beat.Waiting {
				clock.Pause()
			} else {
				clock.Resume()
			}
			if beat.Cancel {
				log.Info("the run was cancelled: stopping the agent")
				stop()
				return
			}
		}
		select {
		case <-t.C:
		case <-ctx.Done():
			return
		}
	}
}
