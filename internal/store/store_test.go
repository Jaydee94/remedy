package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCreateAndGetRun(t *testing.T) {
	s, ctx := openStore(t), context.Background()

	created, err := s.CreateRun(ctx, "claude", "say pong")
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if created.Status != run.Queued || created.ID == "" {
		t.Fatalf("unexpected run: %+v", created)
	}

	got, err := s.GetRun(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.Prompt != "say pong" || got.Provider != "claude" {
		t.Fatalf("unexpected run: %+v", got)
	}

	if _, err := s.GetRun(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetRun(missing) error = %v, want ErrNotFound", err)
	}
}

func TestListRunsNewestFirst(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	first, _ := s.CreateRun(ctx, "claude", "one")
	second, _ := s.CreateRun(ctx, "claude", "two")

	runs, err := s.ListRuns(ctx, 10)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 2 || runs[0].ID != second.ID || runs[1].ID != first.ID {
		t.Fatalf("unexpected order: %+v", runs)
	}
}

func TestClaimNextIsExclusiveAndOldestFirst(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	first, _ := s.CreateRun(ctx, "claude", "one")
	_, _ = s.CreateRun(ctx, "claude", "two")

	claimed, err := s.ClaimNext(ctx)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimNext = %v, %v", claimed, err)
	}
	if claimed.ID != first.ID || claimed.Status != run.Running || claimed.StartedAt == nil {
		t.Fatalf("unexpected claim: %+v", claimed)
	}

	second, err := s.ClaimNext(ctx)
	if err != nil || second == nil || second.ID == first.ID {
		t.Fatalf("second claim = %v, %v", second, err)
	}

	none, err := s.ClaimNext(ctx)
	if err != nil || none != nil {
		t.Fatalf("third claim = %v, %v, want nil", none, err)
	}
}

func TestEventsAreSequencedPerRun(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r, _ := s.CreateRun(ctx, "claude", "x")

	for i, kind := range []string{"system", "assistant", "result"} {
		ev, err := s.AppendEvent(ctx, r.ID, kind, json.RawMessage(`{"n":1}`))
		if err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
		if ev.Seq != i+1 {
			t.Fatalf("seq = %d, want %d", ev.Seq, i+1)
		}
	}

	after, err := s.Events(ctx, r.ID, 1)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(after) != 2 || after[0].Seq != 2 || after[0].Kind != "assistant" {
		t.Fatalf("unexpected events: %+v", after)
	}

	if _, err := s.AppendEvent(ctx, r.ID, "bad", json.RawMessage(`not json`)); err == nil {
		t.Fatal("AppendEvent accepted invalid JSON")
	}
}

func TestFinishRun(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r, _ := s.CreateRun(ctx, "claude", "x")

	if err := s.FinishRun(ctx, r.ID, run.Outcome{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("FinishRun on queued run error = %v, want ErrNotFound", err)
	}

	if _, err := s.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	out := run.Outcome{ExitCode: 0, Result: "pong", SessionID: "s-1", CostUSD: 0.01}
	if err := s.FinishRun(ctx, r.ID, out); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	got, _ := s.GetRun(ctx, r.ID)
	if got.Status != run.Succeeded || got.Result != "pong" || got.SessionID != "s-1" ||
		got.ExitCode == nil || *got.ExitCode != 0 || got.FinishedAt == nil {
		t.Fatalf("unexpected run: %+v", got)
	}

	r2, _ := s.CreateRun(ctx, "claude", "y")
	_, _ = s.ClaimNext(ctx)
	_ = s.FinishRun(ctx, r2.ID, run.Outcome{ExitCode: 3})
	got2, _ := s.GetRun(ctx, r2.ID)
	if got2.Status != run.Failed {
		t.Fatalf("status = %s, want failed", got2.Status)
	}
}
