package responder_test

import (
	"context"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

// An older GitHub incident that cannot be diagnosed because GitHub is unusable must not keep a fresh alert waiting: Remedy works with
// Alertmanager alone, and the GitHub incident of a repository stays after the connection is gone or its token is bad.
func TestAnUnusableGitHubDoesNotBlockAFreshAlert(t *testing.T) {
	// The stored token cannot be opened with the second key: source() answers ErrNoConnection for every GitHub incident.
	e := newEnvWithKeys(t, testKey(t, 1), testKey(t, 2))
	ctx := context.Background()
	gh := e.incident("pr:1", "web", sha, "failure") // older, sorts first
	e.incident("pr:2", "web", sha, "failure")
	alert := e.openAlert("A/1", "Pod web-1 crashes", 0, true)
	e.setLastSeen(alert, e.now.Add(-time.Minute))

	e.r.AutoStart(ctx)

	if got := e.get(alert); got.State != store.IncDiagnosing || got.Diagnoses != 1 {
		t.Fatalf("the fresh alert = %+v, want it diagnosing", got)
	}
	if got := e.get(gh); got.State != store.IncOpen || got.Diagnoses != 0 {
		t.Fatalf("the GitHub incident = %+v, want it untouched", got)
	}
}
