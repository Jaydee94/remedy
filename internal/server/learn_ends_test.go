package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/internal/testutil"
)

// learnFailures counts the learn_failed entries of a run.
func (e *learnEnv) learnFailures(t *testing.T, runID string) int {
	t.Helper()
	log, err := e.st.ListActivity(context.Background(), store.ActivityQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, a := range log {
		if a.Kind == store.KindLearnFailed && a.RunID == runID {
			n++
		}
	}
	return n
}

// A learning run whose token cannot be created at the claim ends failed, and its host learns of it like of any other failed run.
func TestALearningRunWhoseTokenCannotBeMintedFailsWithOneLearnFailedEntry(t *testing.T) {
	e := newLearnEnv(t)
	h := e.readyHost(t, "nas")
	_, started := e.learn(t, h.ID)
	var s struct {
		RunID string `json:"runId"`
	}
	_ = json.Unmarshal([]byte(started), &s)

	db := testutil.RawDB(t, e.dsn)
	if _, err := db.Exec(`CREATE FUNCTION no_tokens_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'no tokens'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER no_tokens BEFORE INSERT ON run_tokens FOR EACH ROW EXECUTE FUNCTION no_tokens_fn()`); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodPost, e.ts.URL+"/runner/v1/claim", nil)
	req.Header.Set("Authorization", "Bearer "+runnerToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("claim = %d, want 500", resp.StatusCode)
	}
	got, err := e.st.GetRun(context.Background(), s.RunID)
	if err != nil || got.Status != run.Failed {
		t.Fatalf("run = %+v, %v, want failed", got, err)
	}
	if n := e.learnFailures(t, s.RunID); n != 1 {
		t.Errorf("learn_failed entries = %d, want 1", n)
	}
}

// A learning run cancelled before it started tells its host so, once, however often the cancel is sent.
func TestAQueuedLearningRunThatIsCancelledHasOneLearnFailedEntry(t *testing.T) {
	e := newLearnEnv(t)
	h := e.readyHost(t, "nas")
	_, started := e.learn(t, h.ID)
	var s struct {
		RunID string `json:"runId"`
	}
	_ = json.Unmarshal([]byte(started), &s)
	if code, body := e.do(t, http.MethodPost, "/api/runs/"+s.RunID+"/cancel", ""); code != http.StatusNoContent {
		t.Fatalf("cancel = %d %s", code, body)
	}
	got, _ := e.st.GetRun(context.Background(), s.RunID)
	if got.Status != run.Failed || got.FailureReason != run.ReasonCancelled {
		t.Fatalf("run = %+v, want failed with cancelled", got)
	}
	if n := e.learnFailures(t, s.RunID); n != 1 {
		t.Fatalf("learn_failed entries = %d, want 1", n)
	}
	log, _ := e.st.ListActivity(context.Background(), store.ActivityQuery{Limit: 100})
	for _, a := range log {
		if a.Kind == store.KindLearnFailed && a.RunID == s.RunID && !strings.Contains(a.Summary, "the run was cancelled") {
			t.Errorf("summary = %q, want it to say the run was cancelled", a.Summary)
		}
	}
	// A second cancel is refused (the run has ended) and adds nothing.
	_, _ = e.do(t, http.MethodPost, "/api/runs/"+s.RunID+"/cancel", "")
	if n := e.learnFailures(t, s.RunID); n != 1 {
		t.Errorf("learn_failed entries after a second cancel = %d, want 1", n)
	}
}
