package loginrelay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const plantedCode = "code-PLANTED-4711x"

// fakeClock is a time the test moves by hand.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

// recorder keeps the events the manager delivers.
type recorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *recorder) add(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) all() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...)
}

type harness struct {
	m     *Manager
	clock *fakeClock
	rec   *recorder
	log   *bytes.Buffer
}

type harnessOpt func(*Config)

// newHarness builds a manager on a fake clock with a short poll wait and a
// JSON log at debug level; the runner (boot b1) has polled once, so it is
// online.
func newHarness(t *testing.T, opts ...harnessOpt) *harness {
	t.Helper()
	h := newBareHarness(t, opts...)
	h.poll(t, "", 0)
	return h
}

// newBareHarness is newHarness without the first poll.
func newBareHarness(t *testing.T, opts ...harnessOpt) *harness {
	t.Helper()
	h := &harness{clock: newFakeClock(), rec: &recorder{}, log: &bytes.Buffer{}}
	var n atomic.Int64
	cfg := Config{
		Now:      h.clock.Now,
		NewID:    func() string { return fmt.Sprintf("s%d", n.Add(1)) },
		PollWait: 5 * time.Millisecond,
		OnEvent:  h.rec.add,
		Log:      slog.New(slog.NewJSONHandler(&syncWriter{w: h.log}, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	for _, o := range opts {
		o(&cfg)
	}
	h.m = New(cfg)
	return h
}

type syncWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

func (h *harness) poll(t *testing.T, session string, ack int64) *Command {
	t.Helper()
	return h.pollBoot(t, "b1", session, ack)
}

func (h *harness) pollBoot(t *testing.T, boot, session string, ack int64) *Command {
	t.Helper()
	c, err := h.m.Poll(context.Background(), PollRequest{Boot: boot, Session: session, Ack: ack})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	return c
}

func (h *harness) start(t *testing.T) string {
	t.Helper()
	v, err := h.m.Start(context.Background(), false, false)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return v.ID
}

func (h *harness) report(t *testing.T, r Report) {
	t.Helper()
	if err := h.m.Report(r); err != nil {
		t.Fatalf("Report(%v): %v", r, err)
	}
}

func (h *harness) state(t *testing.T, want State, reason Reason) {
	t.Helper()
	v := h.m.View()
	if v.State != want || v.Reason != reason {
		t.Fatalf("view = %s/%s, want %s/%s", v.State, v.Reason, want, reason)
	}
}

func wantCommand(t *testing.T, c *Command, action Action, seq int64, session string) {
	t.Helper()
	if c == nil {
		t.Fatalf("no command, want %s seq %d", action, seq)
	}
	if c.Action != action || c.Seq != seq || c.Session != session {
		t.Fatalf("command = %s seq %d session %q, want %s seq %d session %q", c.Action, c.Seq, c.Session, action, seq, session)
	}
}

// driveToAwaiting starts a session, lets the runner take the start command and
// report the valid sign-in address.
func driveToAwaiting(t *testing.T, h *harness) string {
	t.Helper()
	id := h.start(t)
	wantCommand(t, h.poll(t, "", 0), ActionStart, 1, id)
	h.report(t, Report{Session: id, State: string(StateAwaitingCode), Attempt: 1, URL: validURL})
	return id
}

func TestTheHappyPath(t *testing.T) {
	h := newHarness(t)
	v, err := h.m.Start(context.Background(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != StateStarting || v.Attempt != 1 || v.ID == "" {
		t.Fatalf("start view = %+v", v)
	}
	id := v.ID
	wantCommand(t, h.poll(t, "", 0), ActionStart, 1, id)
	h.report(t, Report{Session: id, State: string(StateAwaitingCode), Attempt: 1, URL: validURL})
	v = h.m.View()
	if v.State != StateAwaitingCode || string(v.URL.Bytes()) != validURL {
		t.Fatalf("awaiting view = %s, url matches %v", v.State, string(v.URL.Bytes()) == validURL)
	}
	if err := h.m.SubmitCode(id, NewMaterial(plantedCode)); err != nil {
		t.Fatal(err)
	}
	v = h.m.View()
	if v.State != StateSubmitting || v.URL.Len() != 0 {
		t.Fatalf("submitting view = %s, url len %d", v.State, v.URL.Len())
	}
	c := h.poll(t, id, 1)
	wantCommand(t, c, ActionCode, 2, id)
	if string(c.Code.Bytes()) != plantedCode {
		t.Fatal("the code command does not carry the planted code")
	}
	h.report(t, Report{Session: id, State: string(StateSucceeded), Attempt: 1})
	v = h.m.View()
	if v.State != StateSucceeded || v.URL.Len() != 0 {
		t.Fatalf("final view = %s, url len %d", v.State, v.URL.Len())
	}
	want := []Event{{Kind: EventStarted}, {Kind: EventSucceeded}}
	if got := h.rec.all(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestIllegalMoves(t *testing.T) {
	code := func() Material { return NewMaterial(plantedCode) }
	cases := []struct {
		name  string
		setup func(t *testing.T, h *harness) string
		act   func(h *harness, id string) error
		check func(error) bool
	}{
		{"submit in idle", func(*testing.T, *harness) string { return "s1" },
			func(h *harness, id string) error { return h.m.SubmitCode(id, code()) },
			func(err error) bool { return errors.Is(err, ErrWrongState) }},
		{"submit in starting", func(t *testing.T, h *harness) string { return h.start(t) },
			func(h *harness, id string) error { return h.m.SubmitCode(id, code()) },
			func(err error) bool { return errors.Is(err, ErrWrongState) }},
		{"submit in submitting", func(t *testing.T, h *harness) string {
			id := driveToAwaiting(t, h)
			if err := h.m.SubmitCode(id, code()); err != nil {
				t.Fatal(err)
			}
			return id
		},
			func(h *harness, id string) error { return h.m.SubmitCode(id, code()) },
			func(err error) bool { return errors.Is(err, ErrWrongState) }},
		{"submit with another id", driveToAwaiting,
			func(h *harness, _ string) error { return h.m.SubmitCode("other", code()) },
			func(err error) bool { return errors.Is(err, ErrWrongState) }},
		{"start while open", driveToAwaiting,
			func(h *harness, _ string) error { _, err := h.m.Start(context.Background(), false, false); return err },
			func(err error) bool {
				var r *RefusedError
				return errors.As(err, &r) && r.Reason == RefusalAlreadyActive
			}},
		{"cancel in idle", func(*testing.T, *harness) string { return "s1" },
			func(h *harness, id string) error { return h.m.Cancel(id) },
			func(err error) bool { return errors.Is(err, ErrNoSession) }},
		{"cancel with a stale id", func(t *testing.T, h *harness) string { return h.start(t) },
			func(h *harness, _ string) error { return h.m.Cancel("stale") },
			func(err error) bool { return errors.Is(err, ErrNoSession) }},
		{"report succeeded in awaiting_code", driveToAwaiting,
			func(h *harness, id string) error {
				return h.m.Report(Report{Session: id, State: string(StateSucceeded), Attempt: 1})
			},
			func(err error) bool { return errors.Is(err, ErrWrongState) }},
		{"report awaiting_code in submitting", func(t *testing.T, h *harness) string {
			id := driveToAwaiting(t, h)
			if err := h.m.SubmitCode(id, code()); err != nil {
				t.Fatal(err)
			}
			return id
		},
			func(h *harness, id string) error {
				return h.m.Report(Report{Session: id, State: string(StateAwaitingCode), Attempt: 1, URL: validURL})
			},
			func(err error) bool { return errors.Is(err, ErrWrongState) }},
		{"report of attempt 2 in attempt 1", func(t *testing.T, h *harness) string {
			id := h.start(t)
			h.poll(t, "", 0)
			return id
		},
			func(h *harness, id string) error {
				return h.m.Report(Report{Session: id, State: string(StateAwaitingCode), Attempt: 2, URL: validURL})
			},
			func(err error) bool { return errors.Is(err, ErrWrongState) }},
		{"report for an unknown session", driveToAwaiting,
			func(h *harness, _ string) error {
				return h.m.Report(Report{Session: "other", State: string(StateFailed), Attempt: 1, Reason: ReasonTimeout})
			},
			func(err error) bool { return errors.Is(err, ErrNoSession) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			id := tc.setup(t, h)
			before := h.m.View()
			err := tc.act(h, id)
			if err == nil || !tc.check(err) {
				t.Fatalf("err = %v", err)
			}
			after := h.m.View()
			if after.State != before.State || after.Attempt != before.Attempt || after.ID != before.ID {
				t.Fatalf("state changed: %s/%d -> %s/%d", before.State, before.Attempt, after.State, after.Attempt)
			}
		})
	}
}

// keepAlive polls as the runner every 50 s until the clock reaches until.
func keepAlive(t *testing.T, h *harness, t0 time.Time, from, until time.Duration, session string, ack int64) {
	t.Helper()
	for at := from + 50*time.Second; at < until; at += 50 * time.Second {
		h.clock.Set(t0.Add(at))
		h.poll(t, session, ack)
	}
}

func TestTimers(t *testing.T) {
	t.Run("starting fails no_url at 30 s, by Tick alone", func(t *testing.T) {
		h := newHarness(t)
		t0 := h.clock.Now()
		h.start(t)
		h.poll(t, "", 0)
		h.clock.Set(t0.Add(29900 * time.Millisecond))
		h.m.Tick()
		if len(h.rec.all()) != 1 {
			t.Fatalf("events before 30 s = %v", h.rec.all())
		}
		h.state(t, StateStarting, "")
		h.clock.Set(t0.Add(30 * time.Second))
		h.m.Tick()
		if got := h.rec.all(); len(got) != 2 || got[1] != (Event{Kind: EventFailed, Reason: ReasonNoURL}) {
			t.Fatalf("events = %v", got)
		}
		h.state(t, StateFailed, ReasonNoURL)
	})
	t.Run("submitting fails timeout at 60 s", func(t *testing.T) {
		h := newHarness(t)
		id := driveToAwaiting(t, h)
		t0 := h.clock.Now()
		if err := h.m.SubmitCode(id, NewMaterial(plantedCode)); err != nil {
			t.Fatal(err)
		}
		h.poll(t, id, 1)
		h.clock.Set(t0.Add(59900 * time.Millisecond))
		h.m.Tick()
		if len(h.rec.all()) != 1 {
			t.Fatalf("events before 60 s = %v", h.rec.all())
		}
		h.state(t, StateSubmitting, "")
		h.clock.Set(t0.Add(60 * time.Second))
		h.m.Tick()
		if got := h.rec.all(); len(got) != 2 || got[1] != (Event{Kind: EventFailed, Reason: ReasonTimeout}) {
			t.Fatalf("events = %v", got)
		}
		h.state(t, StateFailed, ReasonTimeout)
	})

	// Every non-terminal state expires 10 minutes after the start. Each case
	// reaches its state shortly before, with the runner alive.
	reach := map[State]func(t *testing.T, h *harness, t0 time.Time, id string){
		StateAwaitingCode: func(t *testing.T, h *harness, t0 time.Time, id string) {
			keepAlive(t, h, t0, 0, 9*time.Minute+40*time.Second, id, 1)
		},
		StateSubmitting: func(t *testing.T, h *harness, t0 time.Time, id string) {
			keepAlive(t, h, t0, 0, 9*time.Minute+20*time.Second, id, 1)
			h.clock.Set(t0.Add(9*time.Minute + 30*time.Second))
			if err := h.m.SubmitCode(id, NewMaterial(plantedCode)); err != nil {
				t.Fatal(err)
			}
			h.poll(t, id, 1)
		},
		StateStarting: func(t *testing.T, h *harness, t0 time.Time, id string) {
			keepAlive(t, h, t0, 0, 9*time.Minute+20*time.Second, id, 1)
			h.clock.Set(t0.Add(9*time.Minute + 35*time.Second))
			if err := h.m.SubmitCode(id, NewMaterial(plantedCode)); err != nil {
				t.Fatal(err)
			}
			h.poll(t, id, 1)
			h.report(t, Report{Session: id, State: string(StateFailed), Attempt: 1, Reason: ReasonInvalidCode})
		},
	}
	for _, st := range []State{StateStarting, StateAwaitingCode, StateSubmitting} {
		t.Run(string(st)+" expires at 10 minutes", func(t *testing.T) {
			h := newHarness(t)
			t0 := h.clock.Now()
			id := driveToAwaiting(t, h)
			reach[st](t, h, t0, id)
			h.clock.Set(t0.Add(10*time.Minute - 100*time.Millisecond))
			h.m.Tick()
			if len(h.rec.all()) != 1 {
				t.Fatalf("events before 10 minutes = %v", h.rec.all())
			}
			if v := h.m.View(); v.State != st {
				t.Fatalf("state = %s, want %s", v.State, st)
			}
			h.clock.Set(t0.Add(10 * time.Minute))
			h.m.Tick()
			if got := h.rec.all(); len(got) != 2 || got[1] != (Event{Kind: EventFailed, Reason: ReasonExpired}) {
				t.Fatalf("events = %v", got)
			}
			h.state(t, StateExpired, ReasonExpired)
			if v := h.m.View(); v.URL.Len() != 0 {
				t.Fatal("an expired view shows a URL")
			}
		})
	}

	t.Run("a terminal view stays 10 minutes, then idle", func(t *testing.T) {
		h := newHarness(t)
		t0 := h.clock.Now()
		h.start(t)
		h.poll(t, "", 0)
		h.clock.Set(t0.Add(30 * time.Second))
		h.m.Tick()
		h.clock.Set(t0.Add(30*time.Second + 10*time.Minute - 100*time.Millisecond))
		h.state(t, StateFailed, ReasonNoURL)
		h.clock.Set(t0.Add(30*time.Second + 10*time.Minute))
		v := h.m.View()
		if v.State != StateIdle || v.ID != "" {
			t.Fatalf("view = %+v, want idle", v)
		}
	})
}

func TestThreeAttempts(t *testing.T) {
	h := newHarness(t)
	id := driveToAwaiting(t, h)
	ack := int64(1)
	for attempt := 1; attempt <= 3; attempt++ {
		if err := h.m.SubmitCode(id, NewMaterial(plantedCode)); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		c := h.poll(t, id, ack)
		wantCommand(t, c, ActionCode, ack+1, id)
		ack = c.Seq
		h.report(t, Report{Session: id, State: string(StateFailed), Attempt: attempt, Reason: ReasonInvalidCode})
		v := h.m.View()
		if attempt == 3 {
			if v.State != StateFailed || v.Reason != ReasonInvalidCode {
				t.Fatalf("after attempt 3: %s/%s", v.State, v.Reason)
			}
			break
		}
		if v.State != StateStarting || v.Attempt != attempt+1 || v.URL.Len() != 0 {
			t.Fatalf("after attempt %d: %s attempt %d url len %d", attempt, v.State, v.Attempt, v.URL.Len())
		}
		c = h.poll(t, id, ack)
		wantCommand(t, c, ActionStart, ack+1, id)
		ack = c.Seq
		h.report(t, Report{Session: id, State: string(StateAwaitingCode), Attempt: attempt + 1, URL: validURL})
	}
	want := []Event{{Kind: EventStarted}, {Kind: EventFailed, Reason: ReasonInvalidCode}}
	if got := h.rec.all(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	wantCommand(t, h.poll(t, id, ack), ActionCancel, ack+1, id)
}

func TestACodeStaysPendingUntilAckedAndIsZeroedThen(t *testing.T) {
	h := newHarness(t)
	id := driveToAwaiting(t, h)
	if err := h.m.SubmitCode(id, NewMaterial(plantedCode)); err != nil {
		t.Fatal(err)
	}
	var pending Material
	for _, c := range h.m.sess.queue {
		if c.Action == ActionCode {
			pending = c.Code
		}
	}
	if string(pending.Bytes()) != plantedCode {
		t.Fatal("no pending code command with the planted code")
	}
	for i := range 2 {
		c := h.poll(t, id, 1)
		wantCommand(t, c, ActionCode, 2, id)
		if string(c.Code.Bytes()) != plantedCode {
			t.Fatalf("poll %d: the code differs", i+1)
		}
	}
	if c := h.poll(t, id, 2); c != nil {
		t.Fatalf("after the ack: %v", c)
	}
	if pending.Len() != len(plantedCode) || !bytes.Equal(pending.Bytes(), make([]byte, len(plantedCode))) {
		t.Fatal("the pending code was not zeroed after the ack")
	}
	for _, c := range h.m.sess.queue {
		if c.Action == ActionCode {
			t.Fatal("the acked code is still queued")
		}
	}
}

func TestRunnerLost(t *testing.T) {
	t.Run("silent 60 s", func(t *testing.T) {
		h := newHarness(t)
		t0 := h.clock.Now()
		id := driveToAwaiting(t, h)
		h.clock.Set(t0.Add(59900 * time.Millisecond))
		h.m.Tick()
		h.state(t, StateAwaitingCode, "")
		h.clock.Set(t0.Add(60 * time.Second))
		h.m.Tick()
		if got := h.rec.all(); len(got) != 2 || got[1] != (Event{Kind: EventFailed, Reason: ReasonRunnerLost}) {
			t.Fatalf("events = %v", got)
		}
		h.state(t, StateFailed, ReasonRunnerLost)
		wantCommand(t, h.poll(t, id, 1), ActionCancel, 2, id)
	})
	t.Run("a poll at 59 s keeps it alive", func(t *testing.T) {
		h := newHarness(t)
		t0 := h.clock.Now()
		id := driveToAwaiting(t, h)
		h.clock.Set(t0.Add(59 * time.Second))
		h.poll(t, id, 1)
		h.clock.Set(t0.Add(118 * time.Second))
		h.m.Tick()
		h.state(t, StateAwaitingCode, "")
		h.clock.Set(t0.Add(119 * time.Second))
		h.m.Tick()
		h.state(t, StateFailed, ReasonRunnerLost)
	})
	t.Run("a report keeps it alive", func(t *testing.T) {
		h := newHarness(t)
		t0 := h.clock.Now()
		id := h.start(t)
		h.poll(t, "", 0)
		h.clock.Set(t0.Add(29 * time.Second))
		h.report(t, Report{Session: id, State: string(StateAwaitingCode), Attempt: 1, URL: validURL})
		h.clock.Set(t0.Add(88 * time.Second))
		h.m.Tick()
		h.state(t, StateAwaitingCode, "")
		h.clock.Set(t0.Add(89 * time.Second))
		h.m.Tick()
		h.state(t, StateFailed, ReasonRunnerLost)
	})
}

func TestRunnerRestarted(t *testing.T) {
	t.Run("a new boot naming the session", func(t *testing.T) {
		h := newHarness(t)
		id := driveToAwaiting(t, h)
		c := h.pollBoot(t, "b2", id, 1)
		h.state(t, StateFailed, ReasonRunnerRestarted)
		wantCommand(t, c, ActionCancel, 2, id)
		if got := h.rec.all(); len(got) != 2 || got[1] != (Event{Kind: EventFailed, Reason: ReasonRunnerRestarted}) {
			t.Fatalf("events = %v", got)
		}
	})
	t.Run("a new boot with no session", func(t *testing.T) {
		h := newHarness(t)
		driveToAwaiting(t, h)
		if c := h.pollBoot(t, "b2", "", 0); c != nil {
			t.Fatalf("a runner holding nothing got %v", c)
		}
		h.state(t, StateFailed, ReasonRunnerRestarted)
		var queued bool
		for _, c := range h.m.sess.queue {
			queued = queued || c.Action == ActionCancel
		}
		if !queued {
			t.Fatal("no cancel queued")
		}
	})
}

func TestAnUnknownSessionGetsCancelSeq0(t *testing.T) {
	h := newHarness(t)
	wantCommand(t, h.poll(t, "old", 0), ActionCancel, 0, "old")

	id := driveToAwaiting(t, h)
	h.report(t, Report{Session: id, State: string(StateFailed), Attempt: 1, Reason: ReasonProcessExited})
	wantCommand(t, h.poll(t, id, 1), ActionCancel, 0, id)
}

func TestRunnerOffline(t *testing.T) {
	refused := func(err error) bool {
		var r *RefusedError
		return errors.As(err, &r) && r.Reason == RefusalRunnerOffline
	}
	h := newBareHarness(t)
	if _, err := h.m.Start(context.Background(), false, false); !refused(err) {
		t.Fatalf("no poll yet: err = %v", err)
	}
	h.poll(t, "", 0)
	h.clock.Advance(44 * time.Second)
	if _, err := h.m.Start(context.Background(), false, false); err != nil {
		t.Fatalf("poll 44 s ago: %v", err)
	}

	h = newBareHarness(t)
	h.poll(t, "", 0)
	h.clock.Advance(46 * time.Second)
	if _, err := h.m.Start(context.Background(), false, false); !refused(err) {
		t.Fatalf("poll 46 s ago: err = %v", err)
	}
	if v := h.m.View(); v.State != StateIdle {
		t.Fatalf("a refused start left %s", v.State)
	}
}

func TestRunActive(t *testing.T) {
	h := newHarness(t, func(c *Config) {
		c.RunActive = func(context.Context) (bool, error) { return true, nil }
	})
	_, err := h.m.Start(context.Background(), false, false)
	var r *RefusedError
	if !errors.As(err, &r) || r.Reason != RefusalRunActive {
		t.Fatalf("err = %v", err)
	}
	if v := h.m.View(); v.State != StateIdle {
		t.Fatalf("state = %s", v.State)
	}

	boom := errors.New("store down")
	h = newHarness(t, func(c *Config) {
		c.RunActive = func(context.Context) (bool, error) { return false, boom }
	})
	if _, err := h.m.Start(context.Background(), false, false); err != boom {
		t.Fatalf("err = %v, want the RunActive error as is", err)
	}
	if v := h.m.View(); v.State != StateIdle {
		t.Fatalf("state = %s", v.State)
	}
	if len(h.rec.all()) != 0 {
		t.Fatalf("events = %v", h.rec.all())
	}
}

func TestReplaceRequired(t *testing.T) {
	h := newHarness(t)
	_, err := h.m.Start(context.Background(), false, true)
	var r *RefusedError
	if !errors.As(err, &r) || r.Reason != RefusalReplaceRequired {
		t.Fatalf("err = %v", err)
	}
	if _, err := h.m.Start(context.Background(), true, true); err != nil {
		t.Fatalf("with replace: %v", err)
	}
}

func TestAHostileReportedURLFailsTheSessionAndShowsNothing(t *testing.T) {
	const host = "claude.com.evil.test"
	for name, url := range map[string]string{
		"another host": "https://" + host + "/cai/oauth/authorize?code_challenge=ch-1&state=st-1",
		"no url":       "",
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			id := h.start(t)
			h.poll(t, "", 0)
			if err := h.m.Report(Report{Session: id, State: string(StateAwaitingCode), Attempt: 1, URL: url}); err != nil {
				t.Fatalf("Report: %v", err)
			}
			v := h.m.View()
			if v.State != StateFailed || v.Reason != ReasonBadURL || v.URL.Len() != 0 {
				t.Fatalf("view = %s/%s url len %d", v.State, v.Reason, v.URL.Len())
			}
			wantCommand(t, h.poll(t, id, 1), ActionCancel, 2, id)
			if strings.Contains(h.log.String(), "evil") {
				t.Fatalf("the log names the host: %s", h.log.String())
			}
			if ev := fmt.Sprintf("%+v", h.rec.all()); strings.Contains(ev, "evil") {
				t.Fatalf("an event names the host: %s", ev)
			}
			if got := h.rec.all(); got[len(got)-1] != (Event{Kind: EventFailed, Reason: ReasonBadURL}) {
				t.Fatalf("events = %v", got)
			}
		})
	}
}

func TestRateLimits(t *testing.T) {
	t.Run("five starts per 10 minutes", func(t *testing.T) {
		h := newHarness(t)
		t0 := h.clock.Now()
		for i := range 5 {
			h.clock.Set(t0.Add(time.Duration(i) * time.Minute))
			h.poll(t, "", 0)
			id := h.start(t)
			if err := h.m.Cancel(id); err != nil {
				t.Fatal(err)
			}
		}
		h.clock.Set(t0.Add(5 * time.Minute))
		h.poll(t, "", 0)
		_, err := h.m.Start(context.Background(), false, false)
		var rl *RateLimitedError
		if !errors.As(err, &rl) || rl.RetryAfter <= 0 || rl.RetryAfter > 10*time.Minute {
			t.Fatalf("sixth start: err = %v", err)
		}
		if rl.RetryAfter != 5*time.Minute {
			t.Fatalf("RetryAfter = %s, want 5m", rl.RetryAfter)
		}
		h.clock.Set(t0.Add(10 * time.Minute))
		h.poll(t, "", 0)
		if _, err := h.m.Start(context.Background(), false, false); err != nil {
			t.Fatalf("after the window slid: %v", err)
		}
	})
	t.Run("ten code submissions per session", func(t *testing.T) {
		h := newHarness(t)
		id := h.start(t)
		for i := range 10 {
			if err := h.m.SubmitCode(id, NewMaterial(plantedCode)); !errors.Is(err, ErrWrongState) {
				t.Fatalf("submission %d in starting: %v", i+1, err)
			}
		}
		// Another id is refused for its id and does not count.
		if err := h.m.SubmitCode("other", NewMaterial(plantedCode)); !errors.Is(err, ErrWrongState) {
			t.Fatalf("other id: %v", err)
		}
		h.poll(t, "", 0)
		h.report(t, Report{Session: id, State: string(StateAwaitingCode), Attempt: 1, URL: validURL})
		err := h.m.SubmitCode(id, NewMaterial(plantedCode))
		var rl *RateLimitedError
		if !errors.As(err, &rl) || rl.RetryAfter <= 0 || rl.RetryAfter > 10*time.Minute {
			t.Fatalf("11th submission: err = %v", err)
		}
		h.state(t, StateAwaitingCode, "")
	})
}

func TestPollWakesOnStart(t *testing.T) {
	m := New(Config{PollWait: 2 * time.Second})
	got := make(chan *Command, 1)
	go func() {
		c, _ := m.Poll(context.Background(), PollRequest{Boot: "b1"})
		got <- c
	}()
	// The poll's arrival makes the runner online.
	deadline := time.Now().Add(time.Second)
	var v View
	var err error
	for {
		v, err = m.Start(context.Background(), false, false)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	began := time.Now()
	select {
	case c := <-got:
		wantCommand(t, c, ActionStart, 1, v.ID)
		if d := time.Since(began); d > 500*time.Millisecond {
			t.Fatalf("the poll took %s after the start", d)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("the poll did not wake within 500 ms")
	}

	began = time.Now()
	c, err := m.Poll(context.Background(), PollRequest{Boot: "b1", Session: v.ID, Ack: 1})
	d := time.Since(began)
	if err != nil || c != nil {
		t.Fatalf("idle poll = %v, %v", c, err)
	}
	if d < 1900*time.Millisecond || d > 3*time.Second {
		t.Fatalf("idle poll returned after %s, want about 2 s", d)
	}
}

func TestClaimGate(t *testing.T) {
	h := newHarness(t)
	release, open := h.m.ClaimGate()
	if !open {
		t.Fatal("the gate is shut while idle")
	}
	release()

	release, open = h.m.ClaimGate()
	if !open {
		t.Fatal("the gate is shut while idle")
	}
	started := make(chan string, 1)
	go func() {
		v, err := h.m.Start(context.Background(), false, false)
		if err != nil {
			t.Errorf("Start: %v", err)
		}
		started <- v.ID
	}()
	select {
	case <-started:
		t.Fatal("Start did not wait for the held claim")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	var id string
	select {
	case id = <-started:
	case <-time.After(time.Second):
		t.Fatal("Start did not proceed after the release")
	}

	release, open = h.m.ClaimGate()
	if open {
		t.Fatal("the gate is open while starting")
	}
	release() // a no-op, but callable

	if err := h.m.Cancel(id); err != nil {
		t.Fatal(err)
	}
	release, open = h.m.ClaimGate()
	if !open {
		t.Fatal("the gate is shut after a terminal state")
	}
	release()
	release() // twice is harmless
}

func TestNoMaterialInAnyViewOrLog(t *testing.T) {
	const marker = "st-PLANTED-77q"
	url := "https://claude.com/cai/oauth/authorize?code_challenge=ch-1&state=" + marker
	if ValidateURL(url) != nil {
		t.Fatal("the planted URL is not valid")
	}
	h := newHarness(t)
	var outputs []string
	look := func() {
		v := h.m.View()
		j, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, fmt.Sprintf("%+v", v), fmt.Sprintf("%#v", v), fmt.Sprintf("%v", v), string(j))
		if h.m.sess != nil {
			outputs = append(outputs, fmt.Sprintf("%+v", *h.m.sess), fmt.Sprintf("%#v", *h.m.sess))
		}
	}
	keep := func(c *Command) {
		if c != nil {
			outputs = append(outputs, fmt.Sprintf("%+v", *c), fmt.Sprintf("%#v", c))
		}
	}

	// A full session.
	id := h.start(t)
	look()
	keep(h.poll(t, "", 0))
	h.report(t, Report{Session: id, State: string(StateAwaitingCode), Attempt: 1, URL: url})
	look()
	if err := h.m.SubmitCode(id, NewMaterial(plantedCode)); err != nil {
		t.Fatal(err)
	}
	look()
	keep(h.poll(t, id, 1))
	look()
	h.report(t, Report{Session: id, State: string(StateSucceeded), Attempt: 1})
	look()

	// A session whose code is refused three times.
	h.poll(t, "", 0)
	id = h.start(t)
	ack := int64(0)
	for attempt := 1; attempt <= 3; attempt++ {
		holds := id
		if attempt == 1 {
			holds = ""
		}
		c := h.poll(t, holds, ack)
		keep(c)
		ack = c.Seq
		h.report(t, Report{Session: id, State: string(StateAwaitingCode), Attempt: attempt, URL: url})
		look()
		if err := h.m.SubmitCode(id, NewMaterial(plantedCode)); err != nil {
			t.Fatal(err)
		}
		look()
		c = h.poll(t, id, ack)
		keep(c)
		ack = c.Seq
		h.report(t, Report{Session: id, State: string(StateFailed), Attempt: attempt, Reason: ReasonInvalidCode})
		look()
	}
	h.state(t, StateFailed, ReasonInvalidCode)
	outputs = append(outputs, fmt.Sprintf("%+v", h.rec.all()))

	logs := h.log.String()
	if !strings.Contains(logs, "login session") {
		t.Fatalf("no state line in the log: %s", logs)
	}
	for _, secret := range []string{plantedCode, url, marker} {
		for _, form := range leakForms(secret) {
			if strings.Contains(logs, form) {
				t.Fatalf("the log leaks %q: %s", form, logs)
			}
			for _, o := range outputs {
				if strings.Contains(o, form) {
					t.Fatalf("an output leaks %q: %s", form, o)
				}
			}
		}
	}
}
