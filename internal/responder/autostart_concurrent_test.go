package responder_test

import (
	"context"
	"sync"
	"testing"

	"github.com/Jaydee94/remedy/internal/responder"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
)

// Two loops call AutoStart (the GitHub poller's hook and the signal runner's): together they still start exactly one run.
func TestConcurrentAutoStartsStartExactlyOneRun(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.incident("pr:1", "web", sha, "failure")
	e.incident("pr:2", "web", sha, "failure")
	e.incident("pr:3", "web", sha, "failure")

	// The fixture's NewSource records the tokens in an unguarded slice; the production one only builds a client.
	e.r.NewSource = func(secret.Value) responder.Source { return e.src }

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.r.AutoStart(ctx)
		}()
	}
	wg.Wait()

	diagnosing := 0
	list, err := e.st.ListIncidents(ctx, store.IncidentFilter{State: "all", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range list {
		if in.State == store.IncDiagnosing {
			diagnosing++
		}
	}
	if diagnosing != 1 {
		t.Fatalf("%d incidents are being diagnosed, want 1", diagnosing)
	}
}
