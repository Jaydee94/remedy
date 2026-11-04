package webhook_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/webhook"
)

func TestTheBucketAllowsTheBurstAndThenTheRate(t *testing.T) {
	l := webhook.NewLimits()
	at := now
	for i := range 120 { // burst: twice the rate
		if ok, _ := l.Allow(1, 60, at); !ok {
			t.Fatalf("request %d of the burst was refused", i+1)
		}
	}
	ok, retry := l.Allow(1, 60, at)
	if ok || retry < time.Second || retry > 2*time.Second {
		t.Fatalf("the 121st request: ok %v, retry after %v, want a refusal and about one second", ok, retry)
	}
	if ok, _ := l.Allow(1, 60, at.Add(time.Second)); !ok {
		t.Error("a second later one request is allowed again (60 a minute is one a second)")
	}
	if ok, _ := l.Allow(1, 60, at.Add(time.Second)); ok {
		t.Error("only one request came free in that second")
	}
}

func TestRetryAfterRoundsUpToWholeSeconds(t *testing.T) {
	l := webhook.NewLimits()
	for range 2 { // rate 1 a minute, burst 2
		l.Allow(1, 1, now)
	}
	ok, retry := l.Allow(1, 1, now)
	if ok || retry != time.Minute {
		t.Fatalf("ok %v, retry %v, want a refusal and a minute", ok, retry)
	}
}

func TestEndpointsDoNotShareABucket(t *testing.T) {
	l := webhook.NewLimits()
	for range 200 {
		l.Allow(1, 60, now)
	}
	if ok, _ := l.Allow(2, 60, now); !ok {
		t.Error("a flood on one endpoint used up the bucket of another")
	}
}

func TestAChangedRateTakesEffect(t *testing.T) {
	l := webhook.NewLimits()
	for range 200 {
		l.Allow(1, 60, now)
	}
	if ok, _ := l.Allow(1, 6000, now.Add(time.Minute)); !ok {
		t.Error("a raised rate did not give the endpoint its tokens")
	}
}

func TestNewIncidentsAreCountedPerHour(t *testing.T) {
	l := webhook.NewLimits()
	for i := range 30 {
		if !l.AllowNew(1, 30, now.Add(time.Duration(i)*time.Second)) {
			t.Fatalf("new incident %d refused", i+1)
		}
	}
	if l.AllowNew(1, 30, now.Add(time.Minute)) {
		t.Error("the 31st new incident within the hour was allowed")
	}
	if !l.AllowNew(2, 30, now.Add(time.Minute)) {
		t.Error("another endpoint was limited by the first")
	}
	if !l.AllowNew(1, 30, now.Add(time.Hour+time.Minute)) {
		t.Error("the hour has passed and the endpoint is still limited")
	}
	// A refusal does not count against the hour.
	for range 100 {
		l.AllowNew(3, 1, now)
	}
	if !l.AllowNew(3, 1, now.Add(time.Hour+time.Second)) {
		t.Error("refused attempts extended the window")
	}
}

func TestOnlyEightRequestsAreInFlight(t *testing.T) {
	l := webhook.NewLimits()
	var releases []func()
	for i := range webhook.MaxInFlight {
		release, ok := l.Acquire()
		if !ok {
			t.Fatalf("slot %d refused", i+1)
		}
		releases = append(releases, release)
	}
	if _, ok := l.Acquire(); ok {
		t.Fatal("a ninth request was admitted")
	}
	releases[0]()
	releases[0]() // a second release of the same slot must not free another
	release, ok := l.Acquire()
	if !ok {
		t.Fatal("a freed slot was not reusable")
	}
	if _, ok := l.Acquire(); ok {
		t.Fatal("releasing twice freed two slots")
	}
	release()
}

func TestTheCountersKeepALastErrorOfBoundedLength(t *testing.T) {
	l := webhook.NewLimits()
	l.Record(1, true, "", now)
	l.Record(1, false, "status: has the wrong type", now.Add(time.Second))
	l.Record(1, false, strings.Repeat("e\n", 500), now.Add(2*time.Second))
	s := l.Stats(1)
	if s.Accepted != 1 || s.Rejected != 2 || len([]rune(s.LastError)) > 300 || strings.Contains(s.LastError, "\n") || !s.LastAt.Equal(now.Add(2*time.Second)) {
		t.Fatalf("stats = %+v", s)
	}
	if got := l.Stats(99); got != (webhook.Stats{}) {
		t.Errorf("stats of an unknown endpoint = %+v", got)
	}
}

func TestStateOfIdleEndpointsIsForgotten(t *testing.T) {
	l := webhook.NewLimits()
	for id := int64(1); id <= 50; id++ {
		l.Allow(id, 60, now)
		l.AllowNew(id, 30, now)
	}
	l.Allow(999, 60, now.Add(2*time.Hour))
	if n := l.Tracked(); n > 1 {
		t.Errorf("%d endpoints are still tracked two hours later", n)
	}
}
