package metrics

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Jaydee94/remedy/internal/signals"
)

// SourceName is the name of the metrics store in GET /api/signals.
const SourceName = "metrics"

// minProbeInterval is the floor of the probe's pace: a store is not asked more often than this, however short the Alertmanager's
// poll interval is.
const minProbeInterval = time.Minute

// maxProbeError bounds the error text kept in the state.
const maxProbeError = 300

// defaultTick is the longest Run sleeps before it looks at the switch and the interval again, so that a new address counts soon.
const defaultTick = 5 * time.Second

// Pinger asks the store whether it answers. *Client is one.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Prober asks the metrics store now and then whether it answers, so that the Setup page can say so. It reads nothing else and
// changes nothing; it runs without an Alertmanager and without GitHub. The state is held in memory: a restart forgets it.
type Prober struct {
	// Client is nil when the client could not be set up; the source is then off and never asked.
	Client Pinger
	// On says whether an address is set.
	On func() bool
	// Interval is the pace, read at every look; it is never shorter than a minute.
	Interval func() time.Duration
	Log      *slog.Logger
	Now      func() time.Time // default time.Now

	tick time.Duration // default defaultTick; a test shortens it

	mu    sync.Mutex
	state signals.State // zero until the first ask
}

func (p *Prober) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Prober) enabled() bool { return p.Client != nil && p.On != nil && p.On() }

func (p *Prober) interval() time.Duration {
	d := minProbeInterval
	if p.Interval != nil {
		d = p.Interval()
	}
	return max(d, minProbeInterval)
}

// States returns the state of the one source, as a list so that it can join the signal runner's.
func (p *Prober) States() []signals.State {
	if !p.enabled() {
		return []signals.State{{Name: SourceName, State: signals.StateOff}}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state.State == signals.StateOK || p.state.State == signals.StateError {
		return []signals.State{p.state}
	}
	return []signals.State{{Name: SourceName, State: signals.StatePending}}
}

func (p *Prober) set(s signals.State) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state = s
}

// Run asks at once and then whenever the interval has passed since the last ask, until ctx ends. While no address is set it asks
// nothing and forgets the last answer, so a new address is asked at the next look.
func (p *Prober) Run(ctx context.Context) {
	tick := p.tick
	if tick <= 0 {
		tick = defaultTick
	}
	var last time.Time
	for {
		switch {
		case !p.enabled():
			p.set(signals.State{})
			last = time.Time{}
		case last.IsZero() || time.Since(last) >= p.interval():
			p.probeOnce(ctx)
			last = time.Now()
		}
		t := time.NewTimer(min(tick, p.interval()))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// probeOnce asks the store once, if it is set up, and keeps the answer.
func (p *Prober) probeOnce(ctx context.Context) {
	if !p.enabled() {
		p.set(signals.State{})
		return
	}
	err := p.Client.Ping(ctx)
	if ctx.Err() != nil {
		return // the shutdown is not the store's fault
	}
	if err == nil {
		p.set(signals.State{Name: SourceName, State: signals.StateOK, CheckedAt: p.now()})
		return
	}
	msg := err.Error()
	if r := []rune(msg); len(r) > maxProbeError {
		msg = string(r[:maxProbeError]) + "..."
	}
	if p.Log != nil {
		p.Log.Warn("the metrics store did not answer", "err", msg)
	}
	p.set(signals.State{Name: SourceName, State: signals.StateError, CheckedAt: p.now(), Error: msg})
}
