package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

// diagnosedAt sets diagnosed_at of an incident through a second connection (the store has no method that sets it
// without a run).
func diagnosedAt(t *testing.T, path string, id int64, at time.Time) {
	t.Helper()
	sqlOn(t, path, `UPDATE incidents SET diagnosed_at = ? WHERE id = ?`, at.UTC().Format("2006-01-02T15:04:05.000000000Z"), id)
}

func resolve(t *testing.T, s *store.Store, id int64) {
	t.Helper()
	if err := s.ResolveIncident(context.Background(), id, "cleared", store.NewActivity{Kind: store.KindIncidentResolved, Summary: "resolved"}); err != nil {
		t.Fatal(err)
	}
}

func TestRecentlyDiagnosedIsTrueForAResolvedIncidentDiagnosedInTheWindow(t *testing.T) {
	s, path := openStoreAt(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

	in := alertIncident(t, s, "A/1", true)
	diagnosedAt(t, path, in.ID, at)
	resolve(t, s, in.ID)

	got, err := s.RecentlyDiagnosed(ctx, store.SourceAlertmanager, "A/1", at.Add(-time.Minute))
	if err != nil || !got {
		t.Fatalf("since before diagnosed_at = %v, %v; want true", got, err)
	}
	got, err = s.RecentlyDiagnosed(ctx, store.SourceAlertmanager, "A/1", at)
	if err != nil || !got {
		t.Fatalf("since equal to diagnosed_at = %v, %v; want true", got, err)
	}
	got, err = s.RecentlyDiagnosed(ctx, store.SourceAlertmanager, "A/1", at.Add(time.Second))
	if err != nil || got {
		t.Fatalf("since after diagnosed_at = %v, %v; want false", got, err)
	}
	got, err = s.RecentlyDiagnosed(ctx, store.SourceAlertmanager, "A/2", at.Add(-time.Minute))
	if err != nil || got {
		t.Fatalf("another key = %v, %v; want false", got, err)
	}
	got, err = s.RecentlyDiagnosed(ctx, store.SourceArgoCD, "A/1", at.Add(-time.Minute))
	if err != nil || got {
		t.Fatalf("another source = %v, %v; want false", got, err)
	}
}

func TestRecentlyDiagnosedIgnoresIncidentsThatAreNotResolvedOrWereNeverDiagnosed(t *testing.T) {
	s, path := openStoreAt(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	since := at.Add(-time.Hour)

	open := alertIncident(t, s, "open", true)
	diagnosedAt(t, path, open.ID, at) // still open

	never := alertIncident(t, s, "never", true)
	resolve(t, s, never.ID) // resolved without a diagnosis

	for _, key := range []string{"open", "never"} {
		got, err := s.RecentlyDiagnosed(ctx, store.SourceAlertmanager, key, since)
		if err != nil || got {
			t.Fatalf("%s = %v, %v; want false", key, got, err)
		}
	}
}
