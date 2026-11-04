package webhook

import (
	"math"
	"sync"
	"time"

	"github.com/Jaydee94/remedy/internal/signals"
)

// MaxInFlight is how many requests the listener works on at once; the next one is answered 503 before it touches anything.
const MaxInFlight = 8

const (
	maxErrorLen = 300
	forgetAfter = time.Hour
)

// Stats are the in-memory counters of an endpoint, shown on the Setup page. A restart forgets them.
type Stats struct {
	Accepted, Rejected int
	LastError          string
	LastAt             time.Time
}

type bucket struct {
	tokens float64
	at     time.Time
}

type endpointState struct {
	bucket bucket
	newAt  []time.Time // when the new incidents of the last hour were opened
	stats  Stats
	seen   time.Time
}

// Limits holds the limits of every endpoint in memory. None of them is per IP: behind a reverse proxy every request has the
// proxy's address, so a limit per address would let one sender lock out all of them.
type Limits struct {
	mu       sync.Mutex
	st       map[int64]*endpointState
	inflight int
	swept    time.Time
}

// NewLimits returns empty limits.
func NewLimits() *Limits { return &Limits{st: map[int64]*endpointState{}} }

func (l *Limits) state(id int64, now time.Time) *endpointState {
	l.sweep(now)
	s := l.st[id]
	if s == nil {
		s = &endpointState{}
		l.st[id] = s
	}
	s.seen = now
	return s
}

// sweep forgets endpoints not seen for an hour, at most once a minute, so that the map cannot grow without bound.
func (l *Limits) sweep(now time.Time) {
	if now.Sub(l.swept) < time.Minute {
		return
	}
	l.swept = now
	for id, s := range l.st {
		if now.Sub(s.seen) > forgetAfter {
			delete(l.st, id)
		}
	}
}

// Tracked is how many endpoints the limits hold state for.
func (l *Limits) Tracked() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.st)
}

// Allow takes one request from the bucket of the endpoint: ratePerMinute tokens a minute, a burst of twice that. When it is empty
// it says how long to wait, in whole seconds and at least one.
func (l *Limits) Allow(id int64, ratePerMinute int, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.state(id, now)
	capacity := float64(2 * ratePerMinute)
	perSecond := float64(ratePerMinute) / 60
	b := &s.bucket
	if b.at.IsZero() {
		b.tokens = capacity
	} else if d := now.Sub(b.at).Seconds(); d > 0 {
		b.tokens = math.Min(capacity, b.tokens+d*perSecond)
	}
	b.tokens = math.Min(b.tokens, capacity) // the rate was lowered
	b.at = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := math.Ceil((1 - b.tokens) / perSecond)
	return false, time.Duration(math.Max(1, wait)) * time.Second
}

// AllowNew counts one new incident of the endpoint against its limit per hour. A refusal is not counted.
func (l *Limits) AllowNew(id int64, perHour int, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.state(id, now)
	keep := s.newAt[:0]
	for _, at := range s.newAt {
		if now.Sub(at) < time.Hour {
			keep = append(keep, at)
		}
	}
	s.newAt = keep
	if len(s.newAt) >= perHour {
		return false
	}
	s.newAt = append(s.newAt, now)
	return true
}

// Acquire takes one of the MaxInFlight slots. The release function frees it once, however often it is called.
func (l *Limits) Acquire() (release func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inflight >= MaxInFlight {
		return nil, false
	}
	l.inflight++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			l.inflight--
			l.mu.Unlock()
		})
	}, true
}

// Record counts a request of the endpoint. errText is the reason of a rejection (a field and a reason, never a value); it is
// kept on one line and cut.
func (l *Limits) Record(id int64, accepted bool, errText string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.state(id, now)
	if accepted {
		s.stats.Accepted++
	} else {
		s.stats.Rejected++
		s.stats.LastError, _ = signals.Cut(signals.OneLine(errText), maxErrorLen)
	}
	s.stats.LastAt = now
}

// Stats returns the counters of an endpoint (the zero value for one that was not seen).
func (l *Limits) Stats(id int64) Stats {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s := l.st[id]; s != nil {
		return s.stats
	}
	return Stats{}
}
