package webhook

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/store"
)

// DefaultResolveAfter is the silence limit of an incident whose endpoint was deleted.
const DefaultResolveAfter = 24 * time.Hour

// maxTick bounds the pause between two sweeps: a silence limit is at least five minutes, so a minute is fine.
const maxTick = time.Minute

// Sweeper resolves the webhook incidents whose sender fell silent. There is no resolution by absence in push: a sender that
// repeats while it fires keeps an incident alive, and one that stops is not taken to have fixed anything, so the reason is
// "expired" and the thread says "not reported".
type Sweeper struct {
	Store    *store.Store
	Interval func() time.Duration
	// AfterSweep runs after every sweep. The app passes the responder's automatic start: a webhook incident that was opened
	// for automatic diagnosis must be started even when neither GitHub nor an Alertmanager is configured.
	AfterSweep func(ctx context.Context)
	Now        func() time.Time
	Log        *slog.Logger
	// Tick replaces the timer in tests.
	Tick chan time.Time
}

func (s *Sweeper) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Sweeper) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Run sweeps until ctx ends.
func (s *Sweeper) Run(ctx context.Context) {
	for {
		pause := maxTick
		if s.Interval != nil {
			pause = min(s.Interval(), maxTick)
		}
		var (
			tick  <-chan time.Time
			timer *time.Timer
		)
		if s.Tick != nil {
			tick = s.Tick
		} else {
			timer = time.NewTimer(pause)
			tick = timer.C
		}
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-tick:
		}
		if _, err := s.SweepOnce(ctx); err != nil && ctx.Err() == nil {
			s.log().Warn("webhook: the sweep failed", "err", err)
		}
		if s.AfterSweep != nil && ctx.Err() == nil {
			s.AfterSweep(ctx)
		}
	}
}

// SweepOnce resolves what is overdue and says how many incidents it resolved. An incident that is being diagnosed is left until
// its run has ended.
func (s *Sweeper) SweepOnce(ctx context.Context) (int, error) {
	list, err := s.Store.ListUnresolvedBySource(ctx, store.SourceWebhook)
	if err != nil || len(list) == 0 {
		return 0, err
	}
	endpoints, err := s.Store.ListWebhooks(ctx)
	if err != nil {
		return 0, err
	}
	after := make(map[string]time.Duration, len(endpoints))
	for _, e := range endpoints {
		after[strings.ToLower(e.Name)] = e.ResolveAfter
	}
	now, n := s.now(), 0
	for _, in := range list {
		if in.State == store.IncDiagnosing {
			continue
		}
		name, _ := store.WebhookKeyParts(in.Key)
		limit, ok := after[strings.ToLower(name)]
		if !ok {
			limit = DefaultResolveAfter
		}
		if limit == 0 || now.Sub(in.LastSeen) <= limit {
			continue
		}
		err := s.Store.ResolveIncident(ctx, in.ID, incident.ReasonExpired, store.NewActivity{
			Kind: store.KindIncidentResolved, Summary: incident.ExpiredText(in, limit),
		})
		if err == store.ErrNotFound {
			continue // resolved by someone else in the meantime
		}
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
