package runner

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestTheClockExpiresOnceItsBudgetIsUsedUp(t *testing.T) {
	var fired atomic.Int32
	c := newRunClock(60*time.Millisecond, func() { fired.Add(1) })
	defer c.Stop()

	time.Sleep(20 * time.Millisecond)
	if c.Expired() || fired.Load() != 0 {
		t.Fatal("the clock expired early")
	}
	time.Sleep(200 * time.Millisecond)
	if !c.Expired() || fired.Load() != 1 {
		t.Fatalf("expired = %v, fired %d times, want true and once", c.Expired(), fired.Load())
	}
}

func TestTheClockStandsStillWhilePausedAndKeepsWhatIsLeft(t *testing.T) {
	var fired atomic.Int32
	c := newRunClock(200*time.Millisecond, func() { fired.Add(1) })
	defer c.Stop()

	time.Sleep(80 * time.Millisecond)
	c.Pause()
	c.Pause() // idempotent

	time.Sleep(400 * time.Millisecond) // twice the budget
	if c.Expired() || fired.Load() != 0 {
		t.Fatal("the clock ran while it was paused")
	}

	c.Resume()
	c.Resume() // idempotent: no second timer
	time.Sleep(60 * time.Millisecond)
	if c.Expired() {
		t.Fatal("the clock forgot the time before the pause: it expired after 140 ms of a 200 ms budget")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !c.Expired() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !c.Expired() || fired.Load() != 1 {
		t.Fatalf("expired = %v, fired %d times, want true and once", c.Expired(), fired.Load())
	}
}

func TestAStoppedClockNeverExpires(t *testing.T) {
	var fired atomic.Int32
	c := newRunClock(40*time.Millisecond, func() { fired.Add(1) })
	c.Stop()
	c.Resume()
	time.Sleep(150 * time.Millisecond)
	if c.Expired() || fired.Load() != 0 {
		t.Fatal("a stopped clock expired")
	}
}
