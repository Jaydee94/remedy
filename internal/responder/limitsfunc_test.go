package responder_test

import (
	"context"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

func TestTheLimitsAreAskedForAtEveryAutomaticStart(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	limits := store.DiagnosisLimits{Cooldown: time.Minute, MaxPerIncident: 3, MaxPerDay: 0} // switched off
	e.r.LimitsFunc = func() store.DiagnosisLimits { return limits }
	in := e.incident("pr:20", "web", sha, "failure")

	e.r.AutoStart(ctx)
	if got := e.get(in); got.State != store.IncOpen || len(e.src.callList()) != 0 {
		t.Fatalf("switched off: %+v, %d GitHub calls", got, len(e.src.callList()))
	}
	limits.MaxPerDay = 5 // changed in the Setup page
	e.r.AutoStart(ctx)
	if got := e.get(in); got.Diagnoses != 1 {
		t.Fatalf("after the daily limit was raised the incident must be diagnosed: %+v", got)
	}
}
