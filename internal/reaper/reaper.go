// Package reaper fails runs that stay "running" because their runner died or lost the connection.
package reaper

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

const (
	// DefaultMaxAge is longer than the runner's own timeout (10 minutes), so the reaper only acts
	// when the runner could not report.
	DefaultMaxAge   = 15 * time.Minute
	DefaultInterval = time.Minute
	// DefaultHeartbeatMaxAge is how long the runner of a run with gatekeeper access may stay silent. Such a run is
	// not subject to MaxAge, because waiting for an approval can take hours; its runner sends a heartbeat every
	// ten seconds instead.
	DefaultHeartbeatMaxAge = 2 * time.Minute
)

type Reaper struct {
	Store           *store.Store
	MaxAge          time.Duration // default DefaultMaxAge
	HeartbeatMaxAge time.Duration // default DefaultHeartbeatMaxAge
	Interval        time.Duration // default DefaultInterval
	Log             *slog.Logger
	Now             func() time.Time // default time.Now

	// OnFailed is called with the IDs of the runs a sweep failed, for example to close the diagnosis of a
	// responder run.
	OnFailed func(ctx context.Context, ids []string)
}

// Sweep fails the runs that have been running for longer than MaxAge, and the runs with gatekeeper access whose
// runner has been silent for longer than HeartbeatMaxAge. It returns how many it failed.
func (r *Reaper) Sweep(ctx context.Context) (int, error) {
	maxAge := r.MaxAge
	if maxAge <= 0 {
		maxAge = DefaultMaxAge
	}
	silence := r.HeartbeatMaxAge
	if silence <= 0 {
		silence = DefaultHeartbeatMaxAge
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}

	stale, staleErr := r.Store.FailStaleRuns(ctx, now.Add(-maxAge),
		fmt.Sprintf("The run did not finish within %s and was failed by the control plane.", maxAge))
	for _, id := range stale {
		r.Log.Warn("failed a stale run", "run", id, "after", maxAge)
	}
	lost, lostErr := r.Store.FailLostRuns(ctx, now.Add(-silence),
		fmt.Sprintf("The runner stopped reporting for more than %s and the run was failed by the control plane.", silence))
	for _, id := range lost {
		r.Log.Warn("failed a run whose runner was lost", "run", id, "silent for", silence)
	}

	ids := append(stale, lost...)
	if len(ids) > 0 && r.OnFailed != nil {
		r.OnFailed(ctx, ids)
	}
	return len(ids), errors.Join(staleErr, lostErr)
}

// Run sweeps every Interval until ctx ends.
func (r *Reaper) Run(ctx context.Context) {
	interval := r.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := r.Sweep(ctx); err != nil && ctx.Err() == nil {
			r.Log.Error("reaper sweep failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
