package incident_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/store"
)

func TestUnignore(t *testing.T) {
	v := newEnv(t)
	ctx := context.Background()
	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "aaa", "failure"))
	in := v.incidents(t, "all")[0]
	if _, err := v.e.Ignore(ctx, in.ID); err != nil {
		t.Fatal(err)
	}

	got, err := v.e.Unignore(ctx, in.ID)
	if err != nil || got.State != store.IncOpen {
		t.Fatalf("Unignore = %+v, %v", got, err)
	}
	if s := v.summaries(t); s[len(s)-1] != "Stopped ignoring the incident for go on PR #7 in octo/hello" {
		t.Fatalf("summaries = %q", s)
	}
	if _, err := v.e.Unignore(ctx, in.ID); !errors.Is(err, incident.ErrNotIgnored) {
		t.Fatalf("un-ignoring twice: error = %v, want ErrNotIgnored", err)
	}
	if _, err := v.e.Unignore(ctx, 9999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("un-ignoring an unknown incident: error = %v, want ErrNotFound", err)
	}
}

func TestUnignoreAnIncidentThatTurnedGreen(t *testing.T) {
	v := newEnv(t)
	ctx := context.Background()
	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "aaa", "failure"))
	in := v.incidents(t, "all")[0]
	if _, err := v.e.Ignore(ctx, in.ID); err != nil {
		t.Fatal(err)
	}
	v.observe(t, v.obs(incident.Green, "pr:7", "go", "bbb", "success"))

	if _, err := v.e.Unignore(ctx, in.ID); !errors.Is(err, incident.ErrNotIgnored) {
		t.Fatalf("un-ignoring a resolved incident: error = %v, want ErrNotIgnored", err)
	}
}

func TestUnignoreNamesAnAlert(t *testing.T) {
	v := newEnv(t)
	ctx := context.Background()
	v.observe(t, alert(incident.Bad, "HighLatency/api", "HighLatency api"))
	in := v.incidents(t, "all")[0]
	if _, err := v.e.Ignore(ctx, in.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := v.e.Unignore(ctx, in.ID); err != nil {
		t.Fatal(err)
	}
	if s := v.summaries(t); s[len(s)-1] != "Stopped ignoring the incident for alert HighLatency api" {
		t.Fatalf("summaries = %q", s)
	}
}

// Un-ignore races with itself and with the check turning green. Whatever the order, the incident ends resolved, at most
// one un-ignore is recorded, and no caller sees an error other than "not ignored".
func TestUnignoreRacesAreSafe(t *testing.T) {
	v := newEnv(t)
	ctx := context.Background()
	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "aaa", "failure"))
	in := v.incidents(t, "all")[0]
	if _, err := v.e.Ignore(ctx, in.ID); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 3)
	wg.Add(3)
	go func() { defer wg.Done(); _, errs[0] = v.e.Unignore(ctx, in.ID) }()
	go func() { defer wg.Done(); _, errs[1] = v.e.Unignore(ctx, in.ID) }()
	go func() {
		defer wg.Done()
		errs[2] = v.e.Observe(ctx, v.obs(incident.Green, "pr:7", "go", "bbb", "success"))
	}()
	wg.Wait()

	for i, err := range errs[:2] {
		if err != nil && !errors.Is(err, incident.ErrNotIgnored) {
			t.Errorf("Unignore #%d: error = %v, want nil or ErrNotIgnored", i, err)
		}
	}
	if errs[2] != nil {
		t.Errorf("Observe: %v", errs[2])
	}
	if got, _ := v.st.GetIncident(ctx, in.ID); got.State != store.IncResolved {
		t.Errorf("state = %q, want resolved", got.State)
	}
	var unignored, resolved int
	for _, k := range v.kinds(t) {
		switch k {
		case store.KindIncidentUnignored:
			unignored++
		case store.KindIncidentResolved:
			resolved++
		}
	}
	if unignored > 1 || resolved != 1 {
		t.Errorf("unignored = %d, resolved = %d, want at most 1 and exactly 1", unignored, resolved)
	}
}
