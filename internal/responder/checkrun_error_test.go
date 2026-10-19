package responder_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
)

// Only a missing incident means "GitHub's run". Any other error of the lookup (here: a closed store) says nothing about the source, so
// the outcome is not judged by the wrong shape: it stays as reported, and Complete, which reads the source again, is the authority.
func TestCheckRunDoesNotTakeALookupErrorForAGitHubIncident(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alert := e.openAlert("A/1", "Pod web-1 crashes", 0, true)
	ar, err := e.r.Start(ctx, alert.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.Close(); err != nil {
		t.Fatal(err)
	}
	outage := run.Outcome{ExitCode: 0, Output: json.RawMessage(goodOutage)}
	if got := e.r.CheckRun(ctx, ar, outage); got.FailureReason != "" || got.ExitCode != 0 {
		t.Fatalf("the outage answer of an alert's run, with a failing lookup = %+v", got)
	}
}
