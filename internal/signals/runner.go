package signals

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/store"
)

// MaxNewPerCycle bounds how many incidents one source may open or reopen in one cycle; the rest is picked up in the next, which
// protects the list and the daily limit against an alert storm (an Alertmanager that restarts delivers every alert again). A key
// whose incident is resolved has no active incident, so it is counted like a new one.
const MaxNewPerCycle = 50

// The states of a source.
const (
	StateOff     = "off"     // no address is set
	StateOK      = "ok"      // the last attempt worked
	StateError   = "error"   // the last attempt failed
	StatePending = "pending" // set up, not asked yet
)

// maxErrorLen bounds the error text kept in the state.
const maxErrorLen = 300

// State is what the runner knows about a source. It is held in memory: a restart forgets it and the next cycle fills it in.
type State struct {
	Name      string
	State     string
	CheckedAt time.Time
	Active    int // how many signals the last good answer held
	Error     string
}

// Runner asks every source once per interval and gives what it finds to the incident engine.
type Runner struct {
	Sources  []Source
	Engine   *incident.Engine
	Hosts    func(ctx context.Context) ([]HostRules, error)
	Interval func() time.Duration
	Log      *slog.Logger
	Now      func() time.Time // default time.Now

	// AfterCycle, when set, runs at the end of every cycle in which at least one source was switched on, whatever the fetch
	// returned: an incident that an earlier cycle opened may still be due for its automatic diagnosis although this fetch
	// failed. It never runs when every source is off, and not when ctx has ended. It is how an alert gets diagnosed without
	// a GitHub connection, because the GitHub poller's hook does not run then. It may run beside the poller's hook; the
	// responder's limits are enforced inside one store transaction.
	AfterCycle func(ctx context.Context)

	mu     sync.Mutex
	states map[string]State
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) interval() time.Duration {
	d := time.Minute
	if r.Interval != nil {
		d = r.Interval()
	}
	if d <= 0 {
		d = time.Minute
	}
	return d
}

// pollTick is the longest Run sleeps before it looks at the interval again, so that a new interval counts soon.
const pollTick = 5 * time.Second

func pause(interval time.Duration) time.Duration { return min(interval, pollTick) }

// Run runs a cycle at once and then whenever the interval has passed since the last, until ctx ends. It works without a GitHub
// connection: it does not depend on the GitHub poller.
func (r *Runner) Run(ctx context.Context) {
	r.RunOnce(ctx)
	last := time.Now()
	for {
		t := time.NewTimer(pause(r.interval()))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		if time.Since(last) >= r.interval() {
			r.RunOnce(ctx)
			last = time.Now()
		}
	}
}

// RunOnce asks every source that is set up once.
func (r *Runner) RunOnce(ctx context.Context) {
	ran := false
	for _, src := range r.Sources {
		if ctx.Err() != nil {
			return
		}
		if r.runSource(ctx, src) {
			ran = true
		}
	}
	if ran && r.AfterCycle != nil && ctx.Err() == nil {
		r.AfterCycle(ctx)
	}
}

func (r *Runner) set(s State) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.states == nil {
		r.states = map[string]State{}
	}
	r.states[s.Name] = s
}

func (r *Runner) fail(name string, err error) {
	msg := err.Error()
	if len([]rune(msg)) > maxErrorLen {
		msg = string([]rune(msg)[:maxErrorLen]) + "..."
	}
	r.Log.Warn("a signal source could not be read; nothing was resolved", "source", name, "err", msg)
	r.set(State{Name: name, State: StateError, CheckedAt: r.now(), Error: msg})
}

// States returns the state of every source, in the order of Sources.
func (r *Runner) States() []State {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]State, 0, len(r.Sources))
	for _, src := range r.Sources {
		switch s, ok := r.states[src.Name()]; {
		case !src.Enabled():
			out = append(out, State{Name: src.Name(), State: StateOff})
		case ok && s.State != StateOff:
			out = append(out, s)
		default:
			out = append(out, State{Name: src.Name(), State: StatePending})
		}
	}
	return out
}

// hostRules reads the hosts once per cycle. When it cannot, the cycle fails: without the rules every incident
// touched in it would lose its host.
func (r *Runner) hostRules(ctx context.Context) ([]compiledHost, error) {
	hosts, err := r.Hosts(ctx)
	if err != nil {
		return nil, fmt.Errorf("the hosts could not be read: %w", err)
	}
	ok, skipped := compile(hosts)
	if len(skipped) > 0 {
		sort.Strings(skipped)
		r.Log.Warn("the alert rules of these hosts cannot be read and are skipped", "hosts", skipped)
	}
	return ok, nil
}

// runSource runs one source and says whether it was switched on.
func (r *Runner) runSource(ctx context.Context, src Source) bool {
	name := src.Name()
	if !src.Enabled() {
		r.set(State{Name: name, State: StateOff})
		return false
	}
	r.observe(ctx, src)
	return true
}

func (r *Runner) observe(ctx context.Context, src Source) {
	name := src.Name()
	sigs, err := src.Fetch(ctx)
	if err != nil {
		r.fail(name, err)
		return
	}
	hosts, err := r.hostRules(ctx)
	if err != nil {
		r.fail(name, err)
		return
	}
	present := make(map[string]bool, len(sigs))
	opened, deferred := 0, 0
	for _, sg := range sigs {
		present[sg.Key] = true
		if opened >= MaxNewPerCycle {
			if _, err := r.Engine.Store.FindActiveIncidentByKey(ctx, name, sg.Key); errors.Is(err, store.ErrNotFound) {
				deferred++
				continue
			} else if err != nil {
				r.fail(name, err)
				return
			}
		}
		host, also := mapHost(hosts, sg.Labels)
		wasNew, err := r.Engine.ObserveSignal(ctx, incident.Observation{
			Source: name, Key: sg.Key, Title: sg.Title, Severity: sg.Severity, AutoDiagnose: sg.Auto,
			Details: withAlso(sg.Details, also), Class: incident.Bad, Conclusion: "firing", URL: sg.URL, HostID: host,
		})
		if err != nil {
			r.fail(name, err)
			return
		}
		if wasNew {
			opened++
		}
	}
	if deferred > 0 {
		r.Log.Warn("more new incidents than one cycle opens; the rest follows in the next cycle", "source", name, "deferred", deferred)
	}
	if _, err := r.Engine.ResolveAbsent(ctx, name, present); err != nil {
		r.fail(name, err)
		return
	}
	r.set(State{Name: name, State: StateOK, CheckedAt: r.now(), Active: len(sigs)})
}
