package metrics

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/signals"
)

type fakePinger struct {
	mu    sync.Mutex
	err   error
	calls int
}

func (f *fakePinger) Ping(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.err
}

func (f *fakePinger) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func probe(p Pinger, on bool) *Prober {
	return &Prober{
		Client:   p,
		On:       func() bool { return on },
		Interval: func() time.Duration { return time.Minute },
		Log:      slog.New(slog.DiscardHandler),
		Now:      func() time.Time { return time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC) },
	}
}

func only(t *testing.T, p *Prober) signals.State {
	t.Helper()
	got := p.States()
	if len(got) != 1 || got[0].Name != "metrics" {
		t.Fatalf("States() = %+v, want one entry named metrics", got)
	}
	return got[0]
}

func TestProbeIsOffWhenNoAddressIsSet(t *testing.T) {
	f := &fakePinger{}
	p := probe(f, false)
	p.probeOnce(context.Background())
	if st := only(t, p); st.State != signals.StateOff {
		t.Fatalf("state = %q, want off", st.State)
	}
	if f.count() != 0 {
		t.Fatalf("an unset source was asked %d times", f.count())
	}
}

func TestProbeIsOffWithoutAClient(t *testing.T) {
	p := probe(nil, true)
	p.Client = nil
	p.probeOnce(context.Background())
	if st := only(t, p); st.State != signals.StateOff {
		t.Fatalf("state = %q, want off", st.State)
	}
}

func TestProbeIsPendingBeforeTheFirstAsk(t *testing.T) {
	p := probe(&fakePinger{}, true)
	if st := only(t, p); st.State != signals.StatePending {
		t.Fatalf("state = %q, want pending", st.State)
	}
}

func TestProbeIsOKAfterAGoodPing(t *testing.T) {
	f := &fakePinger{}
	p := probe(f, true)
	p.probeOnce(context.Background())
	st := only(t, p)
	if st.State != signals.StateOK || st.Error != "" || st.CheckedAt.IsZero() {
		t.Fatalf("state = %+v, want ok with a time and no error", st)
	}
}

func TestProbeKeepsAClippedErrorAfterAFailedPing(t *testing.T) {
	f := &fakePinger{err: errors.New("metrics: " + strings.Repeat("x", 1000))}
	p := probe(f, true)
	p.probeOnce(context.Background())
	st := only(t, p)
	if st.State != signals.StateError || st.CheckedAt.IsZero() {
		t.Fatalf("state = %+v, want error with a time", st)
	}
	if n := len([]rune(st.Error)); n > 303 || n < 100 {
		t.Fatalf("error length = %d, want clipped to about 300", n)
	}
	// A later good ping clears the error.
	f.mu.Lock()
	f.err = nil
	f.mu.Unlock()
	p.probeOnce(context.Background())
	if st := only(t, p); st.State != signals.StateOK || st.Error != "" {
		t.Fatalf("state = %+v, want ok again", st)
	}
}

func TestProbeSwitchedOffForgetsTheLastAnswer(t *testing.T) {
	f := &fakePinger{}
	on := true
	p := probe(f, true)
	p.On = func() bool { return on }
	p.probeOnce(context.Background())
	on = false
	if st := only(t, p); st.State != signals.StateOff {
		t.Fatalf("state = %q, want off once the address is gone", st.State)
	}
	on = true
	p.probeOnce(context.Background())
	if st := only(t, p); st.State != signals.StateOK {
		t.Fatalf("state = %q, want ok", st.State)
	}
}

func TestProbeIntervalIsFlooredAtOneMinute(t *testing.T) {
	p := probe(&fakePinger{}, true)
	for _, c := range []struct{ in, want time.Duration }{
		{10 * time.Second, time.Minute},
		{0, time.Minute},
		{-1, time.Minute},
		{time.Minute, time.Minute},
		{5 * time.Minute, 5 * time.Minute},
	} {
		p.Interval = func() time.Duration { return c.in }
		if got := p.interval(); got != c.want {
			t.Errorf("interval(%v) = %v, want %v", c.in, got, c.want)
		}
	}
	p.Interval = nil
	if got := p.interval(); got != time.Minute {
		t.Errorf("interval(nil) = %v, want a minute", got)
	}
}

func TestProbeRunAsksAtOnceThenNotBeforeTheInterval(t *testing.T) {
	f := &fakePinger{}
	p := probe(f, true)
	p.Interval = func() time.Duration { return time.Millisecond } // floored to a minute
	p.tick = time.Millisecond
	p.Now = nil
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	time.Sleep(60 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop with the context")
	}
	if n := f.count(); n != 1 {
		t.Fatalf("pinged %d times in 60ms with a one minute floor, want 1", n)
	}
}

func TestProbeRunAsksAgainWhenTheSourceIsSwitchedOn(t *testing.T) {
	f := &fakePinger{}
	var mu sync.Mutex
	on := false
	p := probe(f, false)
	p.On = func() bool { mu.Lock(); defer mu.Unlock(); return on }
	p.tick = time.Millisecond
	p.Now = nil
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	time.Sleep(20 * time.Millisecond)
	if f.count() != 0 {
		t.Fatal("asked while off")
	}
	mu.Lock()
	on = true
	mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for f.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if f.count() != 1 {
		t.Fatalf("pinged %d times after the switch, want 1", f.count())
	}
}
