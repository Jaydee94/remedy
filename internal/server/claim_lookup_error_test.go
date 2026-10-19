package server_test

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
)

// An error of the incident lookup that is not "not found" must not turn an alert's run into a GitHub run (the wrong schema and a
// snapshot download that cannot work). The claim answers 500 and the run, which is claimed already, is failed like a run whose token
// could not be created, so that it does not sit in "running" until the reaper comes.
func TestAClaimWhoseIncidentLookupFailsAnswers500AndFailsTheRun(t *testing.T) {
	e := newRespEnv(t, goodKey(t))
	in := e.alert(t)
	if code, body := e.admin(t, http.MethodPost, "/api/incidents/"+strconv.FormatInt(in.ID, 10)+"/diagnose", ""); code != http.StatusAccepted {
		t.Fatalf("diagnose = %d %s", code, body)
	}
	// A timestamp that cannot be read makes the lookup of the incident fail, while the run is still claimable.
	db, err := sql.Open("sqlite", e.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`UPDATE incidents SET first_seen = 'not a time' WHERE id = ?`, in.ID); err != nil {
		t.Fatal(err)
	}

	resp, raw := e.runner(t, http.MethodPost, "/runner/v1/claim", "", runnerToken)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("claim = %d %s, want 500", resp.StatusCode, raw)
	}
	runs, err := e.st.ListRuns(context.Background(), 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	if runs[0].Status != run.Failed {
		t.Fatalf("run = %+v, want it failed, not left running", runs[0])
	}
}
