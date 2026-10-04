package gatekeeper_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

const testGrace = 200 * time.Millisecond

// approvalEnv has the note tool, a progress notification every 20 ms and a short grace period.
func approvalEnv(t *testing.T) (*env, store.Incident) {
	t.Helper()
	e := newEnvWith(t, func(st *store.Store) []gatekeeper.Tool {
		return append(gatekeeper.IncidentTools(st), gatekeeper.NoteTool(st))
	}, func(c *gatekeeper.Config) {
		c.ProgressInterval = 20 * time.Millisecond
		c.Grace = testGrace
	})
	return e, seedIncident(t, e.st, "pr:7", "go")
}

// stream is an open tools/call whose answer is an event stream.
type stream struct {
	t      *testing.T
	br     *bufio.Reader
	body   io.Closer
	cancel context.CancelFunc
}

// open sends a tools/call and returns once the response headers are there.
func (e *env) open(t *testing.T, useID, tool string, args any) *stream {
	t.Helper()
	e.next++
	params, _ := json.Marshal(map[string]any{
		"name": tool, "arguments": args,
		"_meta": map[string]any{"claudecode/toolUseId": useID, "progressToken": 7},
	})
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":%s}`, e.next, params)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, e.ts.URL, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		b, _ := io.ReadAll(resp.Body)
		cancel()
		t.Fatalf("tools/call = %d %q %s, want an event stream", resp.StatusCode, resp.Header.Get("Content-Type"), b)
	}
	s := &stream{t: t, br: bufio.NewReader(resp.Body), body: resp.Body, cancel: cancel}
	t.Cleanup(s.close)
	return s
}

func (s *stream) close() {
	s.cancel()
	_ = s.body.Close()
}

// next returns the next message of the stream.
func (s *stream) next() map[string]any {
	s.t.Helper()
	var data string
	for {
		line, err := s.br.ReadString('\n')
		if err != nil {
			s.t.Fatalf("the stream ended before the next message: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		case line == "" && data != "":
			var m map[string]any
			if err := json.Unmarshal([]byte(data), &m); err != nil {
				s.t.Fatalf("data %q: %v", data, err)
			}
			return m
		}
	}
}

// progress reads messages until a progress notification and returns its params.
func (s *stream) progress() map[string]any {
	s.t.Helper()
	for {
		m := s.next()
		if m["method"] == "notifications/progress" {
			return m["params"].(map[string]any)
		}
		s.t.Fatalf("expected a progress notification, got %v", m)
	}
}

// result reads messages until the answer to the request (skipping progress notifications).
func (s *stream) result() (string, bool) {
	s.t.Helper()
	for {
		m := s.next()
		if m["method"] == "notifications/progress" {
			continue
		}
		return resultText(s.t, m)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// pending waits for the pending approval of the run and returns it.
func (e *env) pending(t *testing.T) store.ToolCall {
	t.Helper()
	var got store.ToolCall
	eventually(t, "a pending approval", func() bool {
		list, err := e.st.ListApprovals(context.Background(), store.ApprovalFilter{PendingOnly: true})
		if err == nil && len(list) > 0 {
			got = list[0]
			return true
		}
		return false
	})
	return got
}

func noteArgs(incident int64, note string) map[string]any {
	return map[string]any{"id": incident, "note": note}
}

func notesOf(t *testing.T, e *env, id int64) []store.Note {
	t.Helper()
	notes, err := e.st.ListNotes(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return notes
}

func TestAMutatingCallWaitsForTheApprovalAndRunsOnce(t *testing.T) {
	e, in := approvalEnv(t)
	ctx := context.Background()
	s := e.open(t, "toolu_1", "incident_add_note", noteArgs(in.ID, "the lock file is stale"))

	call := e.pending(t)
	if call.Tool != "incident_add_note" || call.Kind != store.CallKindMutating || call.IncidentID != in.ID ||
		call.Status != store.CallWaiting || string(call.Arguments) != fmt.Sprintf(`{"id":%d,"note":"the lock file is stale"}`, in.ID) {
		t.Fatalf("pending call = %+v", call)
	}

	// While it waits, the stream carries progress that grows and names the request's token.
	first, second := s.progress(), s.progress()
	if first["progressToken"] != float64(7) || second["progress"].(float64) <= first["progress"].(float64) || first["message"] == "" {
		t.Fatalf("progress = %v, %v", first, second)
	}
	if n := notesOf(t, e, in.ID); len(n) != 0 {
		t.Fatalf("the note exists before the approval: %+v", n)
	}

	decided, err := e.g.Decide(ctx, call.ID, true, "looks right")
	if err != nil || decided.Decision != store.DecisionApproved {
		t.Fatalf("Decide = %+v, %v", decided, err)
	}
	text, isErr := s.result()
	if isErr || text != "note added" {
		t.Fatalf("result = %q (error %v)", text, isErr)
	}

	notes := notesOf(t, e, in.ID)
	if len(notes) != 1 || notes[0].Note != "the lock file is stale" || notes[0].RunID != e.run.ID {
		t.Fatalf("notes = %+v", notes)
	}
	done, _ := e.st.GetToolCall(ctx, call.ID)
	if done.Status != store.CallSucceeded || done.Result != "note added" || done.Decision != store.DecisionApproved || done.DecisionReason != "looks right" {
		t.Fatalf("audit row = %+v", done)
	}
	kinds := activityKinds(t, e.st)
	for _, want := range []string{store.KindApprovalRequested, store.KindApprovalDecided, store.KindNoteAdded} {
		if count(kinds, want) != 1 {
			t.Errorf("activity %v: want one %s", kinds, want)
		}
	}
}

func activityKinds(t *testing.T, st *store.Store) []string {
	t.Helper()
	log, err := st.ListActivity(context.Background(), store.ActivityQuery{Limit: 200})
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

func TestADeniedCallTellsTheAgentWhyAndChangesNothing(t *testing.T) {
	e, in := approvalEnv(t)
	s := e.open(t, "toolu_1", "incident_add_note", noteArgs(in.ID, "x"))
	call := e.pending(t)

	if _, err := e.g.Decide(context.Background(), call.ID, false, "not now"); err != nil {
		t.Fatal(err)
	}
	text, isErr := s.result()
	if !isErr || text != "denied: not now" {
		t.Fatalf("result = %q (error %v)", text, isErr)
	}
	if n := notesOf(t, e, in.ID); len(n) != 0 {
		t.Fatalf("a denied call changed something: %+v", n)
	}
	if got, _ := e.st.GetToolCall(context.Background(), call.ID); got.Status != store.CallDenied || got.Decision != store.DecisionDenied {
		t.Fatalf("audit row = %+v", got)
	}
}

func TestAReplayJoinsTheWaitAndTheToolRunsOnce(t *testing.T) {
	e, in := approvalEnv(t)
	a := e.open(t, "toolu_1", "incident_add_note", noteArgs(in.ID, "once"))
	call := e.pending(t)
	b := e.open(t, "toolu_1", "incident_add_note", noteArgs(in.ID, "once"))
	a.progress()
	b.progress()

	if list, _ := e.st.ListApprovals(context.Background(), store.ApprovalFilter{}); len(list) != 1 {
		t.Fatalf("%d approvals for one call that was sent twice, want 1", len(list))
	}
	if _, err := e.g.Decide(context.Background(), call.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]*stream{"the original": a, "the replay": b} {
		if text, isErr := s.result(); isErr || text != "note added" {
			t.Errorf("%s: result = %q (error %v)", name, text, isErr)
		}
	}
	if n := notesOf(t, e, in.ID); len(n) != 1 {
		t.Fatalf("%d notes after a replayed call, want 1", len(n))
	}
}

func TestAReplayAfterTheOriginalWentAwayKeepsTheApprovalPending(t *testing.T) {
	e, in := approvalEnv(t)
	a := e.open(t, "toolu_1", "incident_add_note", noteArgs(in.ID, "kept"))
	call := e.pending(t)
	a.progress()
	// The CLI is stopped with SIGTERM: its connection closes and, at once, it replays the call.
	a.close()
	b := e.open(t, "toolu_1", "incident_add_note", noteArgs(in.ID, "kept"))
	b.progress()

	time.Sleep(2 * testGrace) // longer than the grace period: the replay is what keeps the call alive
	if got, _ := e.st.GetToolCall(context.Background(), call.ID); got.Status != store.CallWaiting || got.Decision != store.DecisionPending {
		t.Fatalf("call = %+v, want it still waiting", got)
	}
	if _, err := e.g.Decide(context.Background(), call.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	if text, isErr := b.result(); isErr || text != "note added" {
		t.Fatalf("result = %q (error %v)", text, isErr)
	}
}

func TestADecisionNeedsAnAgentThatWaitsAndAnUnwatchedCallIsAbandoned(t *testing.T) {
	e, in := approvalEnv(t)
	ctx := context.Background()
	s := e.open(t, "toolu_1", "incident_add_note", noteArgs(in.ID, "x"))
	call := e.pending(t)
	s.progress()

	if !e.g.Waiting(call.ID) {
		t.Fatal("Waiting is false while the agent waits")
	}
	s.close() // the agent goes away
	eventually(t, "the server to notice that nobody waits", func() bool { return !e.g.Waiting(call.ID) })
	if _, err := e.g.Decide(ctx, call.ID, true, ""); !errors.Is(err, gatekeeper.ErrNoWaiter) {
		t.Fatalf("a decision without a waiter: %v, want ErrNoWaiter", err)
	}
	if got, _ := e.st.GetToolCall(ctx, call.ID); got.Decision != store.DecisionPending {
		t.Fatalf("a refused decision changed the call: %+v", got)
	}

	eventually(t, "the call to be abandoned after the grace period", func() bool {
		got, _ := e.st.GetToolCall(ctx, call.ID)
		return got.Status == store.CallAbandoned
	})
	got, _ := e.st.GetToolCall(ctx, call.ID)
	if got.Decision != store.DecisionAbandoned || count(activityKinds(t, e.st), store.KindApprovalAbandoned) != 1 {
		t.Fatalf("abandoned call = %+v", got)
	}
	if _, err := e.g.Decide(ctx, call.ID, true, ""); err == nil {
		t.Fatal("an abandoned call was decided")
	}
	if n := notesOf(t, e, in.ID); len(n) != 0 {
		t.Fatalf("an abandoned call changed something: %+v", n)
	}
	if _, err := e.g.Decide(ctx, 9999, true, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown call: %v, want ErrNotFound", err)
	}
}

func TestWhenTheRunEndsTheWaitEnds(t *testing.T) {
	for name, end := range map[string]func(e *env) error{
		"finished":  func(e *env) error { return e.st.FinishRun(context.Background(), e.run.ID, run.Outcome{Result: "ok"}) },
		"cancelled": func(e *env) error { _, err := e.st.CancelRun(context.Background(), e.run.ID); return err },
	} {
		t.Run(name, func(t *testing.T) {
			e, in := approvalEnv(t)
			s := e.open(t, "toolu_1", "incident_add_note", noteArgs(in.ID, "x"))
			call := e.pending(t)
			if err := end(e); err != nil {
				t.Fatal(err)
			}
			text, isErr := s.result()
			if !isErr || !strings.Contains(text, "abandoned") {
				t.Fatalf("result = %q (error %v), want the abandonment", text, isErr)
			}
			if got, _ := e.st.GetToolCall(context.Background(), call.ID); got.Status != store.CallAbandoned {
				t.Fatalf("call = %+v", got)
			}
			if n := notesOf(t, e, in.ID); len(n) != 0 {
				t.Fatalf("a call of an ended run changed something: %+v", n)
			}
		})
	}
}

func TestACallThatIsRefusedNeverAsksForAnApproval(t *testing.T) {
	e, in := approvalEnv(t)
	for name, args := range map[string]any{
		"an empty note":                 noteArgs(in.ID, ""),
		"a blank note":                  noteArgs(in.ID, "  \n "),
		"a note that is long":           noteArgs(in.ID, strings.Repeat("x", 2001)),
		"an incident that is not there": noteArgs(9999, "x"),
		"an unknown member":             map[string]any{"id": in.ID, "note": "x", "extra": true},
		"no id":                         map[string]any{"note": "x"},
	} {
		text, isErr := resultText(t, e.call(t, "bad_"+strings.ReplaceAll(name, " ", "_"), "incident_add_note", args))
		if !isErr {
			t.Errorf("%s was accepted: %q", name, text)
		}
	}
	if list, _ := e.st.ListApprovals(context.Background(), store.ApprovalFilter{}); len(list) != 0 {
		t.Fatalf("refused calls asked for %d approvals", len(list))
	}
	if count(activityKinds(t, e.st), store.KindApprovalRequested) != 0 {
		t.Fatal("a refused call wrote an approval request")
	}
	if text, _ := resultText(t, e.call(t, "bad_missing", "incident_add_note", noteArgs(9999, "x"))); !strings.Contains(text, "no incident 9999") {
		t.Fatalf("the refusal for a missing incident says %q", text)
	}
}

func TestARunMayHaveOnlyFiveCallsWaiting(t *testing.T) {
	e, in := approvalEnv(t)
	for i := 0; i < store.MaxPendingPerRun; i++ {
		e.open(t, fmt.Sprintf("toolu_%d", i), "incident_add_note", noteArgs(in.ID, "x"))
	}
	eventually(t, "five approvals", func() bool {
		list, _ := e.st.ListApprovals(context.Background(), store.ApprovalFilter{PendingOnly: true})
		return len(list) == store.MaxPendingPerRun
	})
	text, isErr := resultText(t, e.call(t, "toolu_over", "incident_add_note", noteArgs(in.ID, "x")))
	if !isErr || !strings.Contains(text, "at most") {
		t.Fatalf("the sixth call = %q (error %v), want the limit", text, isErr)
	}
	// Reads are not held up.
	if _, isErr := resultText(t, e.call(t, "toolu_read", "incident_list", map[string]any{})); isErr {
		t.Fatal("a read call was refused beside waiting ones")
	}
}

// Replays and decisions racing: whatever happens, the tool runs once and everybody gets the answer.
func TestExactlyOnceUnderRacingReplaysAndDecisions(t *testing.T) {
	e, in := approvalEnv(t)
	const replays = 6
	streams := make([]*stream, replays)
	streams[0] = e.open(t, "toolu_1", "incident_add_note", noteArgs(in.ID, "race"))
	call := e.pending(t)
	for i := 1; i < replays; i++ {
		streams[i] = e.open(t, "toolu_1", "incident_add_note", noteArgs(in.ID, "race"))
	}

	var wg sync.WaitGroup
	decisions := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.g.Decide(context.Background(), call.ID, true, "")
			decisions <- err
		}()
	}
	wg.Wait()
	close(decisions)
	won := 0
	for err := range decisions {
		switch {
		case err == nil:
			won++
		case errors.Is(err, store.ErrNotPending) || errors.Is(err, gatekeeper.ErrNoWaiter):
		default:
			t.Errorf("unexpected decision error: %v", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d of 4 simultaneous decisions won, want exactly 1", won)
	}

	for i, s := range streams {
		if text, isErr := s.result(); isErr || text != "note added" {
			t.Errorf("stream %d: result = %q (error %v)", i, text, isErr)
		}
	}
	if n := notesOf(t, e, in.ID); len(n) != 1 {
		t.Fatalf("%d notes, want exactly 1", len(n))
	}
}

func TestTheNoteToolStoresTheNoteAndSaysSo(t *testing.T) {
	e, in := approvalEnv(t)
	s := e.open(t, "toolu_1", "incident_add_note", noteArgs(in.ID, "multi\nline note with ünïcode"))
	call := e.pending(t)
	if _, err := e.g.Decide(context.Background(), call.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	_, _ = s.result()
	notes := notesOf(t, e, in.ID)
	if len(notes) != 1 || notes[0].Note != "multi\nline note with ünïcode" {
		t.Fatalf("notes = %+v", notes)
	}
	log, _ := e.st.ListActivity(context.Background(), store.ActivityQuery{IncidentID: in.ID, Limit: 1})
	if len(log) != 1 || log[0].Kind != store.KindNoteAdded {
		t.Fatalf("activity = %+v", log)
	}
}
