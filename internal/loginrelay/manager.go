package loginrelay

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"sync"
	"time"
)

// The time rules and limits of the spec (section 6).
const (
	sessionTTL      = 10 * time.Minute // any non-terminal state, from the start
	startingWait    = 30 * time.Second // starting without a URL: no_url
	submitWait      = 60 * time.Second // submitting without a result: timeout
	runnerGrace     = 45 * time.Second // a poll this recent: the runner is online
	runnerLostAfter = 60 * time.Second // no poll and no report: runner_lost
	keepTerminal    = 10 * time.Minute // the last terminal session stays readable
	maxAttempts     = 3
	startLimit      = 5
	startWindow     = 10 * time.Minute
	codeLimit       = 10 // code submissions per session
	tickEvery       = 5 * time.Second
	defaultPollWait = 25 * time.Second
)

// EventKind is what happened to a session, for the activity feed.
type EventKind string

const (
	EventStarted   EventKind = "started"
	EventSucceeded EventKind = "succeeded"
	EventFailed    EventKind = "failed"
	EventCancelled EventKind = "cancelled"
)

// Event is a state a session reached. Reason is set only for EventFailed
// (expired is a failure with the reason expired). It never holds material.
type Event struct {
	Kind   EventKind
	Reason Reason
}

// Config wires a Manager. Every field is optional.
type Config struct {
	Now      func() time.Time // default time.Now
	NewID    func() string    // default 128 random bits in hex
	PollWait time.Duration    // how long a poll is held, default 25 s
	// RunActive reports whether a run is running; nil means none is.
	RunActive func(context.Context) (bool, error)
	// OnEvent receives the events in order, outside the manager's lock. It
	// must not call the Manager.
	OnEvent func(Event)
	Log     *slog.Logger
}

// View is what the admin API shows of the session. URL is set only while
// awaiting_code; it is a copy the caller owns and may Zero. The zero State
// never occurs: without a session the view is StateIdle.
type View struct {
	ID        string
	State     State
	Attempt   int
	URL       Material
	Reason    Reason
	ExpiresAt time.Time
}

// session is the one login session. It holds the URL and the pending code only
// in Material holders and nothing that formats as plain text.
type session struct {
	id      string
	state   State
	attempt int
	url     Material
	reason  Reason
	boot    string // the runner process that took the session's first command
	codes   int    // code submissions that named this session
	seq     int64  // the last command number of this session
	queue   []Command

	startedAt time.Time
	stateAt   time.Time // when the current state was entered
	seenAt    time.Time // the runner's last poll or report
	endedAt   time.Time
}

func (s *session) terminal() bool {
	switch s.state {
	case StateSucceeded, StateFailed, StateCancelled, StateExpired:
		return true
	}
	return false
}

func (s *session) enqueue(a Action, code Material) {
	s.seq++
	s.queue = append(s.queue, Command{Seq: s.seq, Session: s.id, Action: a, Code: code})
}

// ack drops the commands the runner has acted on and zeroes their codes.
func (s *session) ack(n int64) {
	i := 0
	for i < len(s.queue) && s.queue[i].Seq <= n {
		s.queue[i].Code.Zero()
		i++
	}
	if i > 0 {
		s.queue = append([]Command(nil), s.queue[i:]...)
	}
}

// wipe zeroes the URL and every queued code and empties the queue.
func (s *session) wipe() {
	s.url.Zero()
	s.url = Material{}
	for _, c := range s.queue {
		c.Code.Zero()
	}
	s.queue = nil
}

// Manager holds the login session: its states, timers, rate limits, the
// command queue with acks and the claim gate. It is memory only.
type Manager struct {
	cfg Config

	// gate is the claim gate: a claim holds the read side, Start the write
	// side around its RunActive check.
	gate sync.RWMutex
	// deliver keeps events in order while they are delivered outside mu.
	deliver sync.Mutex

	mu       sync.Mutex
	sess     *session
	lastPoll time.Time
	starts   []time.Time
	wake     chan struct{} // closed and replaced on every change
	events   []Event       // collected under mu, delivered by unlock
}

// New builds a Manager with the defaults filled in.
func New(cfg Config) *Manager {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NewID == nil {
		cfg.NewID = randomID
	}
	if cfg.PollWait <= 0 {
		cfg.PollWait = defaultPollWait
	}
	if cfg.OnEvent == nil {
		cfg.OnEvent = func(Event) {}
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	return &Manager{cfg: cfg, wake: make(chan struct{})}
}

func randomID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // never fails (crypto/rand)
	return hex.EncodeToString(b[:])
}

// clone copies a holder's content into a new holder, so a caller can read it
// while the manager zeroes its own.
func clone(m Material) Material {
	if m.Len() == 0 {
		return Material{}
	}
	b := bytes.Clone(m.Bytes())
	return Material{get: func() []byte { return b }}
}

// lock takes mu and applies the time rules.
func (m *Manager) lock() time.Time {
	m.mu.Lock()
	now := m.cfg.Now()
	m.settle(now)
	return now
}

// unlock releases mu and delivers the events collected under it, in order.
func (m *Manager) unlock() {
	ev := m.events
	m.events = nil
	if len(ev) == 0 {
		m.mu.Unlock()
		return
	}
	m.deliver.Lock()
	m.mu.Unlock()
	defer m.deliver.Unlock()
	for _, e := range ev {
		m.cfg.OnEvent(e)
	}
}

// changed wakes every waiting poll.
func (m *Manager) changed() {
	close(m.wake)
	m.wake = make(chan struct{})
}

// logState writes the one log line of the relay: fixed fields, no id, no
// material, no error from outside.
func (m *Manager) logState(s *session) {
	m.cfg.Log.Info("login session", "state", string(s.state), "attempt", s.attempt, "reason", string(s.reason))
}

func (m *Manager) enter(s *session, st State, now time.Time) {
	s.state, s.stateAt = st, now
	m.logState(s)
	m.changed()
}

// end puts the session in a terminal state. cancel queues a cancel for the
// runner, for every end the runner did not report itself.
func (m *Manager) end(s *session, st State, reason Reason, at time.Time, cancel bool) {
	s.state, s.reason, s.endedAt = st, reason, at
	s.wipe()
	if cancel {
		s.enqueue(ActionCancel, Material{})
	}
	switch st {
	case StateSucceeded:
		m.events = append(m.events, Event{Kind: EventSucceeded})
	case StateCancelled:
		m.events = append(m.events, Event{Kind: EventCancelled})
	default:
		m.events = append(m.events, Event{Kind: EventFailed, Reason: reason})
	}
	m.logState(s)
	m.changed()
}

// drop forgets the session; a runner that still names it gets a cancel 0.
func (m *Manager) drop() {
	m.sess.wipe()
	m.sess = nil
	m.changed()
}

// settle applies the time rules at now: the earliest deadline that has passed
// ends the session (on a tie, expiry wins over a state's timer, which wins
// over runner_lost), and a terminal session is forgotten after keepTerminal.
func (m *Manager) settle(now time.Time) {
	s := m.sess
	if s == nil {
		return
	}
	if !s.terminal() {
		at, st, reason := s.startedAt.Add(sessionTTL), StateExpired, ReasonExpired
		var d time.Time
		var r Reason
		switch s.state {
		case StateStarting:
			d, r = s.stateAt.Add(startingWait), ReasonNoURL
		case StateSubmitting:
			d, r = s.stateAt.Add(submitWait), ReasonTimeout
		}
		if !d.IsZero() && d.Before(at) {
			at, st, reason = d, StateFailed, r
		}
		if lost := s.seenAt.Add(runnerLostAfter); lost.Before(at) {
			at, st, reason = lost, StateFailed, ReasonRunnerLost
		}
		if now.Before(at) {
			return
		}
		m.end(s, st, reason, at, true)
	}
	if !now.Before(s.endedAt.Add(keepTerminal)) {
		m.drop()
	}
}

// Start opens a session. It refuses with *RefusedError (already_active,
// replace_required, runner_offline, run_active) or *RateLimitedError, and
// returns the RunActive error as is. It holds the write side of the claim
// gate around the RunActive check, so a run is either claimed before the
// session opens or not until it ends.
func (m *Manager) Start(ctx context.Context, replace, loggedIn bool) (View, error) {
	m.gate.Lock()
	defer m.gate.Unlock()

	err := m.refusal(m.lock(), replace, loggedIn)
	m.unlock()
	if err != nil {
		return View{}, err
	}
	if m.cfg.RunActive != nil {
		active, err := m.cfg.RunActive(ctx)
		if err != nil {
			return View{}, err
		}
		if active {
			return View{}, &RefusedError{Reason: RefusalRunActive}
		}
	}

	now := m.lock()
	defer m.unlock()
	if err := m.refusal(now, replace, loggedIn); err != nil {
		return View{}, err
	}
	if m.sess != nil {
		m.sess.wipe()
	}
	s := &session{id: m.cfg.NewID(), state: StateStarting, attempt: 1, startedAt: now, stateAt: now, seenAt: now}
	s.enqueue(ActionStart, Material{})
	m.sess = s
	m.starts = append(m.starts, now)
	m.events = append(m.events, Event{Kind: EventStarted})
	m.logState(s)
	m.changed()
	return m.view(), nil
}

// refusal is the check before a start, under mu.
func (m *Manager) refusal(now time.Time, replace, loggedIn bool) error {
	if m.sess != nil && !m.sess.terminal() {
		return &RefusedError{Reason: RefusalAlreadyActive}
	}
	if loggedIn && !replace {
		return &RefusedError{Reason: RefusalReplaceRequired}
	}
	if m.lastPoll.IsZero() || now.Sub(m.lastPoll) > runnerGrace {
		return &RefusedError{Reason: RefusalRunnerOffline}
	}
	kept := m.starts[:0]
	for _, t := range m.starts {
		if now.Sub(t) < startWindow {
			kept = append(kept, t)
		}
	}
	m.starts = kept
	if len(m.starts) >= startLimit {
		return &RateLimitedError{RetryAfter: m.starts[0].Add(startWindow).Sub(now)}
	}
	return nil
}

// SubmitCode queues the code for the runner. The checks run in this order:
// the session id (ErrWrongState), the rate (*RateLimitedError, every call
// that names the session counts), the state (ErrWrongState). The manager
// keeps code and zeroes it when the runner acknowledges it or the session
// ends.
func (m *Manager) SubmitCode(id string, code Material) error {
	now := m.lock()
	defer m.unlock()
	s := m.sess
	if s == nil || id != s.id {
		return ErrWrongState
	}
	if s.codes >= codeLimit {
		retry := s.startedAt.Add(sessionTTL).Sub(now)
		if retry < time.Second {
			retry = time.Second
		}
		return &RateLimitedError{RetryAfter: retry}
	}
	s.codes++
	if s.state != StateAwaitingCode {
		return ErrWrongState
	}
	if code.Len() == 0 {
		return ErrBadCode
	}
	s.url.Zero()
	s.url = Material{}
	s.enqueue(ActionCode, code)
	m.enter(s, StateSubmitting, now)
	return nil
}

// Cancel ends the open session and queues a cancel for the runner.
func (m *Manager) Cancel(id string) error {
	now := m.lock()
	defer m.unlock()
	s := m.sess
	if s == nil || id != s.id || s.terminal() {
		return ErrNoSession
	}
	m.end(s, StateCancelled, ReasonCancelled, now, true)
	return nil
}

// View is the session as the admin API shows it.
func (m *Manager) View() View {
	m.lock()
	defer m.unlock()
	return m.view()
}

func (m *Manager) view() View {
	s := m.sess
	if s == nil {
		return View{State: StateIdle}
	}
	v := View{ID: s.id, State: s.state, Attempt: s.attempt, Reason: s.reason, ExpiresAt: s.startedAt.Add(sessionTTL)}
	if s.state == StateAwaitingCode {
		v.URL = clone(s.url)
	}
	return v
}

// Poll is the runner's long poll. It returns the command for the runner, or
// nil after PollWait with nothing to do. A returned code is a copy the caller
// owns and should Zero once written; the manager zeroes its own when the
// runner acknowledges it.
func (m *Manager) Poll(ctx context.Context, req PollRequest) (*Command, error) {
	timer := time.NewTimer(m.cfg.PollWait)
	defer timer.Stop()
	for {
		now := m.lock()
		c := m.poll(req, now)
		wake := m.wake
		m.unlock()
		if c != nil {
			return c, nil
		}
		select {
		case <-wake:
		case <-timer.C:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// poll is one look at the queue for a poll, under mu. Commands of a session go
// only to a poll that names it, except the start, which also goes to a runner
// that holds no session.
func (m *Manager) poll(req PollRequest, now time.Time) *Command {
	m.lastPoll = now
	s := m.sess
	if s != nil && !s.terminal() && s.boot != "" && req.Boot != s.boot {
		m.end(s, StateFailed, ReasonRunnerRestarted, now, true)
	}
	if s == nil || (req.Session != "" && req.Session != s.id) {
		if req.Session != "" {
			return &Command{Session: req.Session, Action: ActionCancel}
		}
		return nil
	}
	if req.Session == s.id {
		s.ack(req.Ack)
	}
	if s.terminal() {
		if len(s.queue) > 0 && req.Session == s.id {
			return copyCommand(s.queue[0])
		}
		if req.Session == s.id {
			return &Command{Session: s.id, Action: ActionCancel}
		}
		return nil
	}
	s.seenAt = now
	if len(s.queue) > 0 && (req.Session == s.id || s.queue[0].Action == ActionStart) {
		if s.boot == "" {
			s.boot = req.Boot
		}
		return copyCommand(s.queue[0])
	}
	return nil
}

func copyCommand(c Command) *Command {
	c.Code = clone(c.Code)
	return &c
}

// Report applies the runner's report. An unknown session is ErrNoSession; a
// terminal session, another attempt or a state that does not follow is
// ErrWrongState and changes nothing. A sign-in address that ValidateURL
// refuses, or none, fails the session bad_url: nothing of it is kept.
func (m *Manager) Report(r Report) error {
	now := m.lock()
	defer m.unlock()
	s := m.sess
	if s == nil || r.Session != s.id {
		return ErrNoSession
	}
	if s.terminal() || r.Attempt != s.attempt {
		return ErrWrongState
	}
	s.seenAt = now
	switch State(r.State) {
	case StateAwaitingCode:
		if s.state != StateStarting {
			return ErrWrongState
		}
		if ValidateURL(r.URL) != nil {
			m.end(s, StateFailed, ReasonBadURL, now, true)
			return nil
		}
		s.url = NewMaterial(r.URL)
		m.enter(s, StateAwaitingCode, now)
	case StateSucceeded:
		if s.state != StateSubmitting {
			return ErrWrongState
		}
		m.end(s, StateSucceeded, "", now, false)
	case StateFailed:
		reason := r.Reason
		if !reason.FromRunner() {
			reason = ReasonProcessExited
		}
		if reason != ReasonInvalidCode {
			m.end(s, StateFailed, reason, now, false)
			return nil
		}
		if s.state != StateSubmitting {
			return ErrWrongState
		}
		if s.attempt >= maxAttempts {
			// The runner may still hold a child that waits for a code.
			m.end(s, StateFailed, ReasonInvalidCode, now, true)
			return nil
		}
		s.attempt++
		s.url.Zero()
		s.url = Material{}
		s.enqueue(ActionStart, Material{})
		m.enter(s, StateStarting, now)
	default:
		return ErrWrongState
	}
	return nil
}

// ClaimGate is taken by the claim handler around each ClaimNext. open is
// false while a session is open; otherwise the read side of the gate is held
// until release (idempotent) is called.
func (m *Manager) ClaimGate() (release func(), open bool) {
	m.gate.RLock()
	m.lock()
	open = m.sess == nil || m.sess.terminal()
	m.unlock()
	if !open {
		m.gate.RUnlock()
		return func() {}, false
	}
	var once sync.Once
	return func() { once.Do(m.gate.RUnlock) }, true
}

// Tick applies the time rules, so a timer's event is delivered even when no
// browser and no runner calls.
func (m *Manager) Tick() {
	m.lock()
	m.unlock()
}

// Run calls Tick every 5 s until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Tick()
		}
	}
}
