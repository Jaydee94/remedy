// Package reaper fails runs that stay "running" because their runner died or lost the connection.
package reaper

import (
	"context"
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
)

type Reaper struct {
	Store    *store.Store
	MaxAge   time.Duration // default DefaultMaxAge
	Interval time.Duration // default DefaultInterval
	Log      *slog.Logger
	Now      func() time.Time // default time.Now

	// OnFailed is called with the IDs of the runs a sweep failed, for example to close the diagnosis of a
	// responder run.
	OnFailed func(ctx context.Context, ids []string)
}

// Sweep fails the runs that have been running for longer than MaxAge and returns how many it failed.
func (r *Reaper) Sweep(ctx context.Context) (int, error) {
	maxAge := r.MaxAge
	if maxAge <= 0 {
		maxAge = DefaultMaxAge
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	ids, err := r.Store.FailStaleRuns(ctx, now.Add(-maxAge),
		fmt.Sprintf("The run did not finish within %s and was failed by the control plane.", maxAge))
	for _, id := range ids {
		r.Log.Warn("failed a stale run", "run", id, "after", maxAge)
	}
	if len(ids) > 0 && r.OnFailed != nil {
		r.OnFailed(ctx, ids)
	}
	return len(ids), err
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
