package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

func TestFinishToolCallWithActivityLogsTheActionAsItRecordsTheResult(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r := claimedToolRun(t, s)
	call, _, _ := s.BeginToolCall(ctx, newCall(r.ID, "t1", "cluster_rollout_restart", store.CallKindRead))

	if err := s.FinishToolCallWithActivity(ctx, call.ID, "restarted deployment demo/web", "Restarted deployment demo/web"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetToolCall(ctx, call.ID)
	if err != nil || got.Status != store.CallSucceeded || got.Result != "restarted deployment demo/web" || got.FinishedAt == nil {
		t.Fatalf("call = %+v, %v", got, err)
	}
	log, err := s.ListActivity(ctx, store.ActivityQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var found []store.Activity
	for _, a := range log {
		if a.Kind == store.KindClusterAction {
			found = append(found, a)
		}
	}
	if len(found) != 1 || found[0].Summary != "Restarted deployment demo/web" || found[0].RunID != r.ID {
		t.Fatalf("activity = %+v", log)
	}
}

func TestAnActionThatCannotBeFinishedLeavesNoActivity(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r := claimedToolRun(t, s)
	call, _, _ := s.BeginToolCall(ctx, newCall(r.ID, "t1", "cluster_delete_pod", store.CallKindRead))
	if err := s.FinishToolCall(ctx, call.ID, store.CallFailed, "", "gone"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishToolCallWithActivity(ctx, call.ID, "deleted", "Deleted pod demo/x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a call that was finished already was finished again: %v", err)
	}
	if n := count(activityKinds(t, s), store.KindClusterAction); n != 0 {
		t.Fatalf("%d cluster actions were logged for a call that did not succeed", n)
	}
}
