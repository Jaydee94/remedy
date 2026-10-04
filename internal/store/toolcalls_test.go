package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

func newCall(runID, useID, tool, kind string) store.NewToolCall {
	return store.NewToolCall{RunID: runID, ToolUseID: useID, Tool: tool, Kind: kind, Arguments: json.RawMessage(`{}`)}
}

func activityKinds(t *testing.T, s *store.Store) []string {
	t.Helper()
	log, err := s.ListActivity(context.Background(), store.ActivityQuery{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]string, 0, len(log))
	for _, a := range log {
		kinds = append(kinds, a.Kind)
	}
	return kinds
}

func count(kinds []string, kind string) int {
	n := 0
	for _, k := range kinds {
		if k == kind {
			n++
		}
	}
	return n
}

// waitingCall records a mutating call of a running tool run and returns it.
func waitingCall(t *testing.T, s *store.Store, r run.Run, useID string) store.ToolCall {
	t.Helper()
	c, existed, err := s.BeginToolCall(context.Background(), newCall(r.ID, useID, "incident_add_note", store.CallKindMutating))
	if err != nil || existed {
		t.Fatalf("BeginToolCall = %+v, existed %v, err %v", c, existed, err)
	}
	return c
}

func TestBeginToolCallRecordsReadAndMutatingCalls(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))
	if err != nil {
		t.Fatal(err)
	}
	r := claimedToolRun(t, s)

	read, existed, err := s.BeginToolCall(ctx, newCall(r.ID, "toolu_1", "incident_list", store.CallKindRead))
	if err != nil || existed {
		t.Fatalf("read call: %+v, existed %v, err %v", read, existed, err)
	}
	if read.Status != store.CallRunning || read.Decision != "" || read.Kind != store.CallKindRead || read.ID == 0 || read.CreatedAt.IsZero() {
		t.Fatalf("read call = %+v", read)
	}
	if count(activityKinds(t, s), store.KindApprovalRequested) != 0 {
		t.Fatal("a read call asked for an approval")
	}

	n := newCall(r.ID, "toolu_2", "incident_add_note", store.CallKindMutating)
	n.IncidentID = in.ID
	n.Arguments = json.RawMessage(`{"id":1,"note":"hello"}`)
	mut, existed, err := s.BeginToolCall(ctx, n)
	if err != nil || existed {
		t.Fatalf("mutating call: %+v, existed %v, err %v", mut, existed, err)
	}
	if mut.Status != store.CallWaiting || mut.Decision != store.DecisionPending || mut.IncidentID != in.ID || string(mut.Arguments) != `{"id":1,"note":"hello"}` {
		t.Fatalf("mutating call = %+v", mut)
	}
	log, _ := s.ListActivity(ctx, store.ActivityQuery{IncidentID: in.ID})
	if len(log) != 2 || log[0].Kind != store.KindApprovalRequested || log[0].RunID != r.ID || log[0].RepoName != "octo/hello" {
		t.Fatalf("activity = %+v, want the approval request with the run and the repository", log)
	}

	// An incident that does not exist is not linked, and the call is still recorded.
	n = newCall(r.ID, "toolu_3", "incident_add_note", store.CallKindMutating)
	n.IncidentID = 9999
	if c, _, err := s.BeginToolCall(ctx, n); err != nil || c.IncidentID != 0 {
		t.Fatalf("call for a missing incident = %+v, %v", c, err)
	}
}

func TestBeginToolCallFindsTheFirstOfARepeat(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r := claimedToolRun(t, s)
	first := waitingCall(t, s, r, "toolu_1")

	again, existed, err := s.BeginToolCall(ctx, newCall(r.ID, "toolu_1", "other_tool", store.CallKindRead))
	if err != nil || !existed || again.ID != first.ID || again.Tool != "incident_add_note" || again.Status != store.CallWaiting {
		t.Fatalf("repeat = %+v, existed %v, err %v, want the first call", again, existed, err)
	}
	if n := count(activityKinds(t, s), store.KindApprovalRequested); n != 1 {
		t.Fatalf("%d approval requests, want 1", n)
	}

	// The repeat is answered even after the run has ended; a new call is not.
	if err := s.FinishRun(ctx, r.ID, run.Outcome{Result: "ok"}); err != nil {
		t.Fatal(err)
	}
	if again, existed, err := s.BeginToolCall(ctx, newCall(r.ID, "toolu_1", "x", store.CallKindRead)); err != nil || !existed || again.ID != first.ID {
		t.Fatalf("repeat after the end = %+v, %v, %v", again, existed, err)
	}
	if _, _, err := s.BeginToolCall(ctx, newCall(r.ID, "toolu_new", "x", store.CallKindRead)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("new call of an ended run: %v, want ErrNotFound", err)
	}
}

func TestBeginToolCallNeedsARunningRun(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	queued, _ := s.CreateToolRun(ctx, "claude", "x")
	if _, _, err := s.BeginToolCall(ctx, newCall(queued.ID, "t", "x", store.CallKindRead)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("queued run: %v, want ErrNotFound", err)
	}
	if _, _, err := s.BeginToolCall(ctx, newCall("no-such-run", "t", "x", store.CallKindRead)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown run: %v, want ErrNotFound", err)
	}
}

func TestToolCallLimits(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r := claimedToolRun(t, s)

	for i := 0; i < store.MaxPendingPerRun; i++ {
		waitingCall(t, s, r, "pending_"+strconv.Itoa(i))
	}
	if _, _, err := s.BeginToolCall(ctx, newCall(r.ID, "pending_over", "incident_add_note", store.CallKindMutating)); !errors.Is(err, store.ErrCallLimit) {
		t.Fatalf("sixth waiting call: %v, want ErrCallLimit", err)
	}
	// A read call is not held up by waiting ones.
	if _, _, err := s.BeginToolCall(ctx, newCall(r.ID, "read_0", "incident_list", store.CallKindRead)); err != nil {
		t.Fatalf("read call beside waiting ones: %v", err)
	}
	// Deciding one makes room.
	first, _ := s.ListToolCalls(ctx, r.ID)
	if _, err := s.DecideApproval(ctx, first[0].ID, false, "no"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.BeginToolCall(ctx, newCall(r.ID, "pending_again", "incident_add_note", store.CallKindMutating)); err != nil {
		t.Fatalf("waiting call after a decision: %v", err)
	}

	// 100 calls in all.
	have := len(first) + 1
	for i := have; i < store.MaxCallsPerRun; i++ {
		if _, _, err := s.BeginToolCall(ctx, newCall(r.ID, "bulk_"+strconv.Itoa(i), "incident_list", store.CallKindRead)); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if _, _, err := s.BeginToolCall(ctx, newCall(r.ID, "bulk_over", "incident_list", store.CallKindRead)); !errors.Is(err, store.ErrCallLimit) {
		t.Fatalf("call %d: %v, want ErrCallLimit", store.MaxCallsPerRun+1, err)
	}
}

func TestFinishToolCall(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r := claimedToolRun(t, s)
	read, _, _ := s.BeginToolCall(ctx, newCall(r.ID, "t1", "incident_list", store.CallKindRead))

	if err := s.FinishToolCall(ctx, read.ID, store.CallSucceeded, "the result", ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetToolCall(ctx, read.ID)
	if err != nil || got.Status != store.CallSucceeded || got.Result != "the result" || got.FinishedAt == nil {
		t.Fatalf("call = %+v, %v", got, err)
	}
	if err := s.FinishToolCall(ctx, read.ID, store.CallFailed, "", "again"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a finished call was finished again: %v", err)
	}

	failed, _, _ := s.BeginToolCall(ctx, newCall(r.ID, "t2", "incident_get", store.CallKindRead))
	if err := s.FinishToolCall(ctx, failed.ID, store.CallFailed, "", "no such incident"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetToolCall(ctx, failed.ID); got.Status != store.CallFailed || got.Error != "no such incident" {
		t.Fatalf("failed call = %+v", got)
	}

	if err := s.FinishToolCall(ctx, failed.ID, store.CallDenied, "", ""); err == nil {
		t.Fatal("a call was finished with a status that is not an outcome")
	}
	waiting := waitingCall(t, s, r, "t3")
	if err := s.FinishToolCall(ctx, waiting.ID, store.CallSucceeded, "x", ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a waiting call was finished without a decision: %v", err)
	}
	if _, err := s.GetToolCall(ctx, 9999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown call: %v", err)
	}
}

func TestDecideApproval(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r := claimedToolRun(t, s)

	approved := waitingCall(t, s, r, "a")
	got, err := s.DecideApproval(ctx, approved.ID, true, "looks right")
	if err != nil || got.Decision != store.DecisionApproved || got.Status != store.CallWaiting || got.DecisionReason != "looks right" || got.DecidedAt == nil {
		t.Fatalf("approved = %+v, %v (an approval only records the decision; the execution comes later)", got, err)
	}
	if _, err := s.DecideApproval(ctx, approved.ID, true, ""); !errors.Is(err, store.ErrNotPending) {
		t.Fatalf("a second decision: %v, want ErrNotPending", err)
	}
	if _, err := s.DecideApproval(ctx, approved.ID, false, ""); !errors.Is(err, store.ErrNotPending) {
		t.Fatalf("a decision that reverses an approval: %v, want ErrNotPending", err)
	}

	denied := waitingCall(t, s, r, "d")
	got, err = s.DecideApproval(ctx, denied.ID, false, "not now")
	if err != nil || got.Decision != store.DecisionDenied || got.Status != store.CallDenied || got.Error != "denied: not now" || got.FinishedAt == nil {
		t.Fatalf("denied = %+v, %v", got, err)
	}
	bare := waitingCall(t, s, r, "d2")
	if got, _ := s.DecideApproval(ctx, bare.ID, false, ""); got.Error != "denied" {
		t.Fatalf("a denial without a reason says %q, want \"denied\"", got.Error)
	}

	read, _, _ := s.BeginToolCall(ctx, newCall(r.ID, "r", "incident_list", store.CallKindRead))
	if _, err := s.DecideApproval(ctx, read.ID, true, ""); !errors.Is(err, store.ErrNotPending) {
		t.Fatalf("deciding a read call: %v, want ErrNotPending", err)
	}
	if _, err := s.DecideApproval(ctx, 9999, true, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown call: %v, want ErrNotFound", err)
	}

	kinds := activityKinds(t, s)
	if count(kinds, store.KindApprovalDecided) != 3 {
		t.Fatalf("%d decision entries, want 3: %v", count(kinds, store.KindApprovalDecided), kinds)
	}

	ended := waitingCall(t, s, r, "e")
	if err := s.FinishRun(ctx, r.ID, run.Outcome{Result: "ok"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecideApproval(ctx, ended.ID, true, ""); !errors.Is(err, store.ErrNotPending) {
		t.Fatalf("a decision for an ended run: %v, want ErrNotPending", err)
	}
}

func TestBeginExecutionLetsExactlyOneCallerRunAnApprovedCall(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r := claimedToolRun(t, s)

	c := waitingCall(t, s, r, "a")
	if _, won, err := s.BeginExecution(ctx, c.ID); err != nil || won {
		t.Fatalf("an undecided call may run: won %v, err %v", won, err)
	}
	if _, err := s.DecideApproval(ctx, c.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	got, won, err := s.BeginExecution(ctx, c.ID)
	if err != nil || !won || got.Status != store.CallRunning || got.Decision != store.DecisionApproved {
		t.Fatalf("first caller: %+v, won %v, err %v", got, won, err)
	}
	if _, won, err := s.BeginExecution(ctx, c.ID); err != nil || won {
		t.Fatalf("second caller won the same call: %v, %v", won, err)
	}
	if err := s.FinishToolCall(ctx, c.ID, store.CallSucceeded, "done", ""); err != nil {
		t.Fatalf("the winner cannot finish the call: %v", err)
	}

	denied := waitingCall(t, s, r, "d")
	_, _ = s.DecideApproval(ctx, denied.ID, false, "")
	if _, won, _ := s.BeginExecution(ctx, denied.ID); won {
		t.Fatal("a denied call may run")
	}

	cancelled := waitingCall(t, s, r, "c")
	_, _ = s.DecideApproval(ctx, cancelled.ID, true, "")
	if _, err := s.CancelRun(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, won, _ := s.BeginExecution(ctx, cancelled.ID); won {
		t.Fatal("an approved call of a cancelled run may run")
	}
}

func TestAbandonCall(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r := claimedToolRun(t, s)

	pending := waitingCall(t, s, r, "p")
	changed, err := s.AbandonCall(ctx, pending.ID)
	if err != nil || !changed {
		t.Fatalf("AbandonCall = %v, %v", changed, err)
	}
	got, _ := s.GetToolCall(ctx, pending.ID)
	if got.Status != store.CallAbandoned || got.Decision != store.DecisionAbandoned || got.FinishedAt == nil || got.Error == "" {
		t.Fatalf("abandoned call = %+v", got)
	}
	if _, err := s.DecideApproval(ctx, pending.ID, true, ""); !errors.Is(err, store.ErrNotPending) {
		t.Fatalf("an abandoned call was decided: %v", err)
	}
	if changed, _ := s.AbandonCall(ctx, pending.ID); changed {
		t.Fatal("a call was abandoned twice")
	}

	// Approved, but its agent went away before it ran: the log keeps what was decided.
	approved := waitingCall(t, s, r, "a")
	_, _ = s.DecideApproval(ctx, approved.ID, true, "")
	if changed, _ := s.AbandonCall(ctx, approved.ID); !changed {
		t.Fatal("an approved call that never ran was not abandoned")
	}
	if got, _ := s.GetToolCall(ctx, approved.ID); got.Status != store.CallAbandoned || got.Decision != store.DecisionApproved {
		t.Fatalf("approved but abandoned call = %+v, want status abandoned and decision approved", got)
	}

	done, _, _ := s.BeginToolCall(ctx, newCall(r.ID, "r", "incident_list", store.CallKindRead))
	_ = s.FinishToolCall(ctx, done.ID, store.CallSucceeded, "x", "")
	if changed, _ := s.AbandonCall(ctx, done.ID); changed {
		t.Fatal("a finished call was abandoned")
	}
	if n := count(activityKinds(t, s), store.KindApprovalAbandoned); n != 2 {
		t.Fatalf("%d abandon entries, want 2", n)
	}
}

func TestEndingARunAbandonsItsWaitingCalls(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r := claimedToolRun(t, s)
	c := waitingCall(t, s, r, "w")

	if err := s.FinishRun(ctx, r.ID, run.Outcome{Result: "ok"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetToolCall(ctx, c.ID)
	if got.Status != store.CallAbandoned || got.Decision != store.DecisionAbandoned {
		t.Fatalf("call of a finished run = %+v", got)
	}
	if n := count(activityKinds(t, s), store.KindApprovalAbandoned); n != 1 {
		t.Fatalf("%d abandon entries, want 1", n)
	}
}

func TestAbandonAllWaitingClosesWhatARestartLeftBehind(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r := claimedToolRun(t, s)
	a := waitingCall(t, s, r, "a")
	b := waitingCall(t, s, r, "b")

	n, err := s.AbandonAllWaiting(ctx)
	if err != nil || n != 2 {
		t.Fatalf("AbandonAllWaiting = %d, %v, want 2", n, err)
	}
	for _, id := range []int64{a.ID, b.ID} {
		if got, _ := s.GetToolCall(ctx, id); got.Status != store.CallAbandoned {
			t.Fatalf("call %d = %+v", id, got)
		}
	}
	if n, _ := s.AbandonAllWaiting(ctx); n != 0 {
		t.Fatalf("a second sweep abandoned %d", n)
	}
}

func TestListToolCallsAndApprovals(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r := claimedToolRun(t, s)
	read, _, _ := s.BeginToolCall(ctx, newCall(r.ID, "r", "incident_list", store.CallKindRead))
	first := waitingCall(t, s, r, "m1")
	second := waitingCall(t, s, r, "m2")
	_, _ = s.DecideApproval(ctx, first.ID, false, "")

	calls, err := s.ListToolCalls(ctx, r.ID)
	if err != nil || len(calls) != 3 || calls[0].ID != read.ID || calls[2].ID != second.ID {
		t.Fatalf("ListToolCalls = %+v, %v, want the three calls oldest first", calls, err)
	}
	all, err := s.ListApprovals(ctx, store.ApprovalFilter{})
	if err != nil || len(all) != 2 || all[0].ID != second.ID || all[1].ID != first.ID {
		t.Fatalf("all approvals = %+v, %v, want the two mutating calls newest first", all, err)
	}
	pending, _ := s.ListApprovals(ctx, store.ApprovalFilter{PendingOnly: true})
	if len(pending) != 1 || pending[0].ID != second.ID {
		t.Fatalf("pending approvals = %+v", pending)
	}
	if id, err := s.PendingApprovalForRun(ctx, r.ID); err != nil || id != second.ID {
		t.Fatalf("PendingApprovalForRun = %d, %v, want %d", id, err, second.ID)
	}
	_, _ = s.DecideApproval(ctx, second.ID, true, "")
	if id, _ := s.PendingApprovalForRun(ctx, r.ID); id != 0 {
		t.Fatalf("PendingApprovalForRun = %d after the decision, want 0", id)
	}
}

func TestAddNote(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in, _ := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))
	r := claimedToolRun(t, s)

	if err := s.AddNote(ctx, in.ID, r.ID, "the lock file is stale"); err != nil {
		t.Fatal(err)
	}
	notes, err := s.ListNotes(ctx, in.ID)
	if err != nil || len(notes) != 1 || notes[0].Note != "the lock file is stale" || notes[0].RunID != r.ID || notes[0].CreatedAt.IsZero() {
		t.Fatalf("notes = %+v, %v", notes, err)
	}
	log, _ := s.ListActivity(ctx, store.ActivityQuery{IncidentID: in.ID, Limit: 1})
	if len(log) != 1 || log[0].Kind != store.KindNoteAdded || log[0].RunID != r.ID || log[0].RepoName != "octo/hello" {
		t.Fatalf("activity = %+v", log)
	}
	if err := s.AddNote(ctx, 9999, r.ID, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a note for a missing incident: %v, want ErrNotFound", err)
	}
	if notes, _ := s.ListNotes(ctx, 9999); len(notes) != 0 {
		t.Fatalf("notes of a missing incident = %+v", notes)
	}
}

func TestCancelRun(t *testing.T) {
	s, ctx := openStore(t), context.Background()

	queued, _ := s.CreateRun(ctx, "claude", "plain")
	got, err := s.CancelRun(ctx, queued.ID)
	if err != nil || got.Status != run.Failed || got.FailureReason != run.ReasonCancelled || got.FinishedAt == nil || !got.CancelRequested {
		t.Fatalf("cancelled queued run = %+v, %v", got, err)
	}
	if _, err := s.CancelRun(ctx, queued.ID); !errors.Is(err, store.ErrNotCancellable) {
		t.Fatalf("a finished run was cancelled: %v", err)
	}
	if _, err := s.CancelRun(ctx, "no-such-run"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown run: %v", err)
	}

	// A running run without gatekeeper access cannot hear a cancel.
	plain, _ := s.CreateRun(ctx, "claude", "plain2")
	_, _ = s.ClaimNext(ctx)
	if _, err := s.CancelRun(ctx, plain.ID); !errors.Is(err, store.ErrNotCancellable) {
		t.Fatalf("a running run without tools: %v, want ErrNotCancellable", err)
	}
	_ = s.FinishRun(ctx, plain.ID, run.Outcome{})
}

func TestCancelRunOfAWaitingToolRun(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r := claimedToolRun(t, s)
	token, _ := s.MintRunToken(ctx, r.ID)
	c := waitingCall(t, s, r, "w")

	got, err := s.CancelRun(ctx, r.ID)
	if err != nil || got.Status != run.Running || !got.CancelRequested {
		t.Fatalf("cancelled running run = %+v, %v, want it still running with the request recorded", got, err)
	}
	if call, _ := s.GetToolCall(ctx, c.ID); call.Status != store.CallAbandoned {
		t.Fatalf("the waiting call = %+v, want it abandoned", call)
	}
	if _, err := s.RunForToken(ctx, token); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a cancelled run's token still works: %v", err)
	}
	if _, _, err := s.BeginToolCall(ctx, newCall(r.ID, "late", "x", store.CallKindRead)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a cancelled run made a new call: %v", err)
	}
	if _, err := s.CancelRun(ctx, r.ID); err != nil {
		t.Fatalf("a second cancel failed: %v", err)
	}
	if n := count(activityKinds(t, s), store.KindRunCancelled); n != 1 {
		t.Fatalf("%d cancel entries, want 1", n)
	}

	// The runner then reports the end, and the run is failed as cancelled.
	if err := s.FinishRun(ctx, r.ID, run.Outcome{ExitCode: -1, FailureReason: run.ReasonCancelled}); err != nil {
		t.Fatal(err)
	}
	if fin, _ := s.GetRun(ctx, r.ID); fin.Status != run.Failed || fin.FailureReason != run.ReasonCancelled {
		t.Fatalf("finished run = %+v", fin)
	}
}

func TestAResponderRunCannotBeCancelledThisWay(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in, _ := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))
	started, err := s.StartDiagnosis(ctx, store.StartParams{IncidentID: in.ID, Provider: "claude", Prompt: "p", HeadSHA: "aaa", Now: time.Now()},
		entry(store.KindDiagnosisStarted, repo.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelRun(ctx, started.ID); !errors.Is(err, store.ErrNotCancellable) {
		t.Fatalf("a responder run was cancelled: %v, want ErrNotCancellable", err)
	}
}
