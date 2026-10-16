package gatekeeper_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/rand"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"

	_ "modernc.org/sqlite" // the driver of giveGatewayAccess
)

// slowTool is a mutating tool whose Run takes a while.
func slowTool(delay time.Duration) gatekeeper.Tool {
	return gatekeeper.Tool{
		Name:        "slow_change",
		Description: "Takes its time.",
		Mutating:    true,
		Schema:      json.RawMessage(`{"type":"object","additionalProperties":false}`),
		Decode: func(raw json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{}`), gatekeeper.DecodeArgs(raw, &struct{}{})
		},
		Run: func(ctx context.Context, _ gatekeeper.Call) (string, error) {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
			}
			return "slow done", nil
		},
	}
}

// automaticEnv is an env whose run is an automatic responder run of an alert incident, with an approval timeout from timeout().
// The incident is returned.
func automaticEnv(t *testing.T, timeout func() time.Duration, extra ...gatekeeper.Tool) (*env, store.Incident) {
	t.Helper()
	e := newEnvWith(t, func(st *store.Store) []gatekeeper.Tool {
		return append(append(gatekeeper.IncidentTools(st), gatekeeper.NoteTool(st)), extra...)
	}, func(c *gatekeeper.Config) {
		c.ProgressInterval = 20 * time.Millisecond
		c.Grace = testGrace
		c.ApprovalTimeout = timeout
	})
	ctx := context.Background()
	if err := e.st.FinishRun(ctx, e.run.ID, run.Outcome{ExitCode: 0}); err != nil { // the env's own tool run makes room for a responder run
		t.Fatal(err)
	}
	in, err := e.st.OpenIncident(ctx, store.NewIncident{Source: store.SourceAlertmanager, Key: "A/1", Title: "t", Conclusion: "firing", AutoDiagnose: true},
		store.NewActivity{Kind: store.KindIncidentOpened, Summary: "opened"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.StartDiagnosis(ctx, store.StartParams{IncidentID: in.ID, Provider: "claude", Prompt: "p", Automatic: true, Limits: store.DefaultLimits(), Now: time.Now()},
		store.NewActivity{Kind: store.KindDiagnosisStarted, Summary: "started"}); err != nil {
		t.Fatal(err)
	}
	claimed, err := e.st.ClaimNext(ctx)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimNext = %+v, %v", claimed, err)
	}
	e.run = *claimed
	giveGatewayAccess(t, e)
	if e.token, err = e.st.MintRunToken(ctx, claimed.ID); err != nil {
		t.Fatal(err)
	}
	return e, in
}

// giveGatewayAccess sets mcp = 1 on the env's run. A responder run does not have gatekeeper access in the store yet (a later task of the
// plan adds StartParams.Tools); until then the test changes the row itself, through a second connection that is closed again.
func giveGatewayAccess(t *testing.T, e *env) {
	t.Helper()
	db, err := sql.Open("sqlite", e.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE runs SET mcp = 1 WHERE id = ?`, e.run.ID); err != nil {
		t.Fatal(err)
	}
}

// atomicDuration is a timeout that a test changes while the gatekeeper reads it.
type atomicDuration struct{ v atomic.Int64 }

func (a *atomicDuration) set(d time.Duration) { a.v.Store(int64(d)) }
func (a *atomicDuration) get() time.Duration  { return time.Duration(a.v.Load()) }

func ms(n int) func() time.Duration {
	return func() time.Duration { return time.Duration(n) * time.Millisecond }
}

// callsOf returns the tool calls of the env's run, oldest first.
func callsOf(t *testing.T, e *env) []store.ToolCall {
	t.Helper()
	calls, err := e.st.ListToolCalls(context.Background(), e.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return calls
}

func expiredEntries(t *testing.T, e *env) int {
	return count(activityKinds(t, e.st), store.KindApprovalExpired)
}

func TestAnApprovalOfAnAutomaticRunExpiresAndTheAgentIsTold(t *testing.T) {
	e, in := automaticEnv(t, ms(150))
	ctx := context.Background()
	s := e.open(t, "toolu_1", "incident_add_note", noteArgs(in.ID, "a note"))

	start := time.Now()
	text, isErr := s.result()
	if !isErr || !strings.Contains(text, "no decision within") {
		t.Fatalf("result = %q (error %v), want the expiry error", text, isErr)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("the answer took %v", took)
	}
	calls := callsOf(t, e)
	if len(calls) != 1 || calls[0].Status != store.CallAbandoned || calls[0].Decision != store.DecisionAbandoned || calls[0].ExpiresAt == nil {
		t.Fatalf("calls = %+v, want one abandoned call with a deadline", calls)
	}
	if left, err := e.st.ListApprovals(ctx, store.ApprovalFilter{PendingOnly: true}); err != nil || len(left) != 0 {
		t.Fatalf("pending approvals = %+v, %v", left, err)
	}
	if n := expiredEntries(t, e); n != 1 {
		t.Fatalf("activity %v: want one %s", activityKinds(t, e.st), store.KindApprovalExpired)
	}
	if n := notesOf(t, e, in.ID); len(n) != 0 {
		t.Fatalf("the note exists although the approval expired: %+v", n)
	}
	if got, err := e.st.GetRun(ctx, e.run.ID); err != nil || got.Status != run.Running {
		t.Fatalf("run = %+v, %v; want it still running", got, err)
	}
}

func TestAfterTheExpiryTheRunGoesOn(t *testing.T) {
	e, in := automaticEnv(t, ms(100))
	first := e.open(t, "toolu_1", "incident_add_note", noteArgs(in.ID, "one"))
	if text, isErr := first.result(); !isErr || !strings.Contains(text, "no decision within") {
		t.Fatalf("first = %q (error %v)", text, isErr)
	}

	// A read tool still works.
	out := e.call(t, "toolu_2", "echo", map[string]any{"text": "still here"})
	if text, isErr := resultText(t, out); isErr || text != "still here" {
		t.Fatalf("echo = %q (error %v)", text, isErr)
	}

	// A second mutating call gets a deadline of its own and expires too.
	second := e.open(t, "toolu_3", "incident_add_note", noteArgs(in.ID, "two"))
	if text, isErr := second.result(); !isErr || !strings.Contains(text, "no decision within") {
		t.Fatalf("second = %q (error %v)", text, isErr)
	}
	if n := expiredEntries(t, e); n != 2 {
		t.Fatalf("activity %v: want two %s", activityKinds(t, e.st), store.KindApprovalExpired)
	}
	if n := notesOf(t, e, in.ID); len(n) != 0 {
		t.Fatalf("notes = %+v", n)
	}
}

func TestARunThatAClickStartedNeverExpires(t *testing.T) {
	e := newEnvWith(t, func(st *store.Store) []gatekeeper.Tool {
		return append(gatekeeper.IncidentTools(st), gatekeeper.NoteTool(st))
	}, func(c *gatekeeper.Config) {
		c.ProgressInterval = 20 * time.Millisecond
		c.Grace = testGrace
		c.ApprovalTimeout = ms(100)
	})
	in := seedIncident(t, e.st, "pr:7", "go")
	s := e.open(t, "toolu_1", "incident_add_note", noteArgs(in.ID, "late but welcome"))
	call := e.pending(t)
	if call.ExpiresAt != nil {
		t.Fatalf("a run started by a click has a deadline: %+v", call)
	}

	time.Sleep(600 * time.Millisecond)
	again := e.pending(t)
	if again.ID != call.ID || again.Status != store.CallWaiting {
		t.Fatalf("after 600 ms the call is %+v, want it still waiting", again)
	}
	if _, err := e.g.Decide(context.Background(), call.ID, true, "go on"); err != nil {
		t.Fatal(err)
	}
	if text, isErr := s.result(); isErr || text != "note added" {
		t.Fatalf("result = %q (error %v)", text, isErr)
	}
	if n := expiredEntries(t, e); n != 0 {
		t.Fatalf("an %s entry exists", store.KindApprovalExpired)
	}
}

func TestWithoutATimeoutAnAutomaticRunWaits(t *testing.T) {
	for name, timeout := range map[string]func() time.Duration{"nil": nil, "zero": ms(0)} {
		t.Run(name, func(t *testing.T) {
			e, in := automaticEnv(t, timeout)
			e.open(t, "toolu_1", "incident_add_note", noteArgs(in.ID, "a note"))
			call := e.pending(t)
			if call.ExpiresAt != nil {
				t.Fatalf("the call has a deadline: %+v", call)
			}
			time.Sleep(400 * time.Millisecond)
			again := e.pending(t)
			if again.ID != call.ID || again.Status != store.CallWaiting {
				t.Fatalf("after 400 ms the call is %+v, want it still waiting", again)
			}
			if n := expiredEntries(t, e); n != 0 {
				t.Fatalf("an %s entry exists", store.KindApprovalExpired)
			}
		})
	}
}

func TestTheTimeoutIsReadWhenTheCallStartsWaiting(t *testing.T) {
	var timeout atomicDuration
	timeout.set(150 * time.Millisecond)
	e, in := automaticEnv(t, timeout.get)
	ctx := context.Background()

	first := e.open(t, "toolu_1", "incident_add_note", noteArgs(in.ID, "one"))
	timeout.set(time.Hour)
	second := e.open(t, "toolu_2", "incident_add_note", noteArgs(in.ID, "two"))

	if text, isErr := first.result(); !isErr || !strings.Contains(text, "no decision within") {
		t.Fatalf("first = %q (error %v)", text, isErr)
	}
	calls := callsOf(t, e)
	if len(calls) != 2 {
		t.Fatalf("calls = %+v", calls)
	}
	got, err := e.st.GetToolCall(ctx, calls[1].ID)
	if err != nil || got.ExpiresAt == nil {
		t.Fatalf("second call = %+v, %v", got, err)
	}
	if left := time.Until(*got.ExpiresAt); left < 59*time.Minute || left > 61*time.Minute {
		t.Fatalf("the second deadline is %v away, want about an hour", left)
	}
	if got.Status != store.CallWaiting {
		t.Fatalf("the second call is %q, want waiting", got.Status)
	}
	_ = second
}

func TestAnApprovalBeforeTheDeadlineExecutes(t *testing.T) {
	e, in := automaticEnv(t, func() time.Duration { return 2 * time.Second })
	s := e.open(t, "toolu_1", "incident_add_note", noteArgs(in.ID, "in time"))
	call := e.pending(t)
	if call.ExpiresAt == nil {
		t.Fatalf("the call has no deadline: %+v", call)
	}
	if _, err := e.g.Decide(context.Background(), call.ID, true, "go on"); err != nil {
		t.Fatal(err)
	}
	if text, isErr := s.result(); isErr || text != "note added" {
		t.Fatalf("result = %q (error %v)", text, isErr)
	}
	got, _ := e.st.GetToolCall(context.Background(), call.ID)
	if got.Status != store.CallSucceeded || got.Decision != store.DecisionApproved {
		t.Fatalf("call = %+v", got)
	}
	if n := expiredEntries(t, e); n != 0 {
		t.Fatalf("an %s entry exists", store.KindApprovalExpired)
	}
	if n := notesOf(t, e, in.ID); len(n) != 1 {
		t.Fatalf("notes = %+v", n)
	}
}

func TestAnApprovedCallIsNeverExpiredEvenIfItsDeadlinePassesBeforeItRuns(t *testing.T) {
	e, _ := automaticEnv(t, ms(100), slowTool(240*time.Millisecond))
	s := e.open(t, "toolu_1", "slow_change", map[string]any{})
	call := e.pending(t)
	time.Sleep(60 * time.Millisecond)
	if _, err := e.g.Decide(context.Background(), call.ID, true, "go on"); err != nil {
		t.Fatalf("Decide: %v (the test needs the decision to arrive before the deadline)", err)
	}
	// The deadline passes at 100 ms, while the tool still runs until about 300 ms.
	text, isErr := s.result()
	if isErr || text != "slow done" {
		t.Fatalf("result = %q (error %v)", text, isErr)
	}
	got, _ := e.st.GetToolCall(context.Background(), call.ID)
	if got.Status != store.CallSucceeded || got.Decision != store.DecisionApproved {
		t.Fatalf("call = %+v", got)
	}
	if n := expiredEntries(t, e); n != 0 {
		t.Fatalf("an %s entry exists", store.KindApprovalExpired)
	}
}

// The decision and the deadline race; whoever comes first wins and the other changes nothing.
func TestADecisionAndTheDeadlineHaveExactlyOneWinner(t *testing.T) {
	e, in := automaticEnv(t, ms(30))
	ctx := context.Background()
	approved, expired := 0, 0
	for round := 0; round < 25; round++ {
		useID := "toolu_round_" + string(rune('a'+round))
		s := e.open(t, useID, "incident_add_note", noteArgs(in.ID, "round "+useID))
		var call store.ToolCall
		for _, c := range callsOf(t, e) {
			if c.ToolUseID == useID {
				call = c
			}
		}
		if call.ID == 0 {
			t.Fatalf("round %d: no call row for %s", round, useID)
		}
		delay := time.Duration(rand.Intn(61)) * time.Millisecond
		decided := make(chan error, 1)
		go func() {
			time.Sleep(delay)
			_, err := e.g.Decide(ctx, call.ID, true, "go on")
			decided <- err
		}()

		text, isErr := s.result()
		err := <-decided
		if err != nil && !errors.Is(err, store.ErrNotPending) && !errors.Is(err, gatekeeper.ErrNoWaiter) {
			t.Fatalf("round %d: Decide = %v", round, err)
		}
		var got store.ToolCall
		eventually(t, "a terminal call", func() bool {
			got, _ = e.st.GetToolCall(ctx, call.ID)
			return got.Status == store.CallSucceeded || got.Status == store.CallAbandoned
		})
		switch {
		case got.Status == store.CallSucceeded && got.Decision == store.DecisionApproved:
			if isErr || text != "note added" {
				t.Fatalf("round %d: approved but the agent got %q (error %v)", round, text, isErr)
			}
			approved++
		case got.Status == store.CallAbandoned && got.Decision == store.DecisionAbandoned:
			if !isErr || !strings.Contains(text, "no decision within") {
				t.Fatalf("round %d: expired but the agent got %q (error %v)", round, text, isErr)
			}
			if err == nil {
				t.Fatalf("round %d: the decision was accepted and the call expired", round)
			}
			expired++
		default:
			t.Fatalf("round %d: call ends as %q/%q", round, got.Status, got.Decision)
		}
		if n := expiredEntries(t, e); n != expired {
			t.Fatalf("round %d: %d %s entries, want %d", round, n, store.KindApprovalExpired, expired)
		}
		if n := len(notesOf(t, e, in.ID)); n != approved {
			t.Fatalf("round %d: %d notes, want %d: an expired approval must run nothing", round, n, approved)
		}
	}
	t.Logf("approved %d, expired %d", approved, expired)
}

func TestAReplayOfAnExpiredCallGetsTheSameAnswer(t *testing.T) {
	e, in := automaticEnv(t, ms(100))
	first := e.open(t, "toolu_1", "incident_add_note", noteArgs(in.ID, "one"))
	want, isErr := first.result()
	if !isErr || !strings.Contains(want, "no decision within") {
		t.Fatalf("first = %q (error %v)", want, isErr)
	}
	// The CLI sends the call again; the stored answer comes back, with no second approval.
	replay := e.call(t, "toolu_1", "incident_add_note", noteArgs(in.ID, "one"))
	if text, isErr := resultText(t, replay); !isErr || text != want {
		t.Fatalf("replay = %q (error %v), want %q", text, isErr, want)
	}
	if left, err := e.st.ListApprovals(context.Background(), store.ApprovalFilter{PendingOnly: true}); err != nil || len(left) != 0 {
		t.Fatalf("pending approvals = %+v, %v", left, err)
	}
	if n := expiredEntries(t, e); n != 1 {
		t.Fatalf("want one %s entry, got %d", store.KindApprovalExpired, n)
	}
}
