package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

func resolveAlert(t *testing.T, s *store.Store, in store.Incident) {
	t.Helper()
	if err := s.ResolveIncident(context.Background(), in.ID, "cleared", store.NewActivity{Kind: store.KindIncidentResolved, Summary: "resolved"}); err != nil {
		t.Fatal(err)
	}
}

func reopenActivity() store.NewActivity {
	return store.NewActivity{Kind: store.KindIncidentReopened, Summary: "Incident reopened: HighLoad fired again (2nd time)"}
}

func reopenFields() store.Reopen {
	return store.Reopen{
		Title: "HighLoad on nas", Severity: "critical", AutoDiagnose: true, HostID: 0,
		Details: json.RawMessage(`{"labels":{"alertname":"HighLoad","instance":"nas"}}`),
	}
}

// markDiagnosed stores a diagnosis on the row the way a finished diagnosis leaves it.
func markDiagnosed(t *testing.T, path string, id int64) {
	t.Helper()
	sqlOn(t, path, `UPDATE incidents SET diagnosis = '{"summary":"s"}', diagnosed_at = '2026-10-04T12:00:00.000000000Z',
		diagnoses = 2, last_diagnosis_at = '2026-10-04T12:00:00.000000000Z', run_id = NULL WHERE id = ?`, id)
}

func TestReopenBringsBackADiagnosedIncidentWithItsDiagnosis(t *testing.T) {
	s, path := openStoreAt(t)
	ctx := context.Background()
	h, err := s.CreateHost(ctx, hostFields("nas"))
	if err != nil {
		t.Fatal(err)
	}
	in := openAlert(t, s, "HighLoad/abc", true)
	markDiagnosed(t, path, in.ID)
	resolveAlert(t, s, in)

	r := reopenFields()
	r.HostID = h.ID
	if err := s.ReopenIncident(ctx, in.ID, r, reopenActivity()); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetIncident(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != store.IncDiagnosed || got.Occurrences != 2 || got.ResolvedAt != nil || got.ResolvedReason != "" {
		t.Errorf("state %q, occurrences %d, resolved %v/%q", got.State, got.Occurrences, got.ResolvedAt, got.ResolvedReason)
	}
	if string(got.Diagnosis) != `{"summary":"s"}` || got.DiagnosedAt == nil || got.Diagnoses != 2 || got.LastDiagnosisAt == nil {
		t.Errorf("the diagnosis and its counters must stay: %+v", got)
	}
	if got.Title != "HighLoad on nas" || got.Severity != "critical" || got.HostID != h.ID || got.HostName != "nas" ||
		string(got.Details) != `{"labels":{"alertname":"HighLoad","instance":"nas"}}` {
		t.Errorf("the fields were not refreshed: %+v", got)
	}
	if got.AutoDiagnose {
		t.Error("a diagnosed incident must not be diagnosed on its own after a reopen")
	}
	if !got.LastSeen.After(in.LastSeen) && !got.LastSeen.Equal(in.LastSeen) {
		t.Errorf("last_seen = %v, was %v", got.LastSeen, in.LastSeen)
	}
	// It counts as the active incident of its key again.
	if a, err := s.FindActiveIncidentByKey(ctx, store.SourceAlertmanager, "HighLoad/abc"); err != nil || a.ID != in.ID {
		t.Errorf("FindActiveIncidentByKey = %+v, %v", a, err)
	}
	log, err := s.ListActivity(ctx, store.ActivityQuery{IncidentID: in.ID})
	if err != nil || len(log) == 0 || log[0].Kind != store.KindIncidentReopened || log[0].IncidentID != in.ID {
		t.Errorf("newest activity = %+v, %v, want incident_reopened of incident %d", log, err, in.ID)
	}
}

func TestReopenOfAnUndiagnosedIncidentIsOpenAndFollowsTheAutoFlag(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	in := openAlert(t, s, "HighLoad/abc", true)
	resolveAlert(t, s, in)

	if err := s.ReopenIncident(ctx, in.ID, reopenFields(), reopenActivity()); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetIncident(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != store.IncOpen || !got.AutoDiagnose || got.Occurrences != 2 {
		t.Errorf("state %q, auto %v, occurrences %d, want open, true, 2", got.State, got.AutoDiagnose, got.Occurrences)
	}

	// And false when the new observation says so.
	resolveAlert(t, s, got)
	r := reopenFields()
	r.AutoDiagnose = false
	if err := s.ReopenIncident(ctx, in.ID, r, reopenActivity()); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetIncident(ctx, in.ID); got.AutoDiagnose || got.Occurrences != 3 {
		t.Errorf("auto %v, occurrences %d, want false, 3", got.AutoDiagnose, got.Occurrences)
	}
}

func TestReopenOfADiagnosedIncidentNeverTurnsAutoDiagnoseOn(t *testing.T) {
	s, path := openStoreAt(t)
	ctx := context.Background()
	in := openAlert(t, s, "HighLoad/abc", true)
	markDiagnosed(t, path, in.ID)
	resolveAlert(t, s, in)
	r := reopenFields()
	r.AutoDiagnose = true
	if err := s.ReopenIncident(ctx, in.ID, r, reopenActivity()); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetIncident(ctx, in.ID); got.AutoDiagnose {
		t.Error("auto_diagnose is true for a diagnosed incident")
	}
}

func TestAnIgnoredIncidentStaysIgnoredWhenItResolvesAndReturns(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	in := openAlert(t, s, "HighLoad/abc", true)
	if err := s.IgnoreIncident(ctx, in.ID, store.NewActivity{Kind: store.KindIncidentIgnored, Summary: "ignored"}); err != nil {
		t.Fatal(err)
	}
	resolveAlert(t, s, in)

	if err := s.ReopenIncident(ctx, in.ID, reopenFields(), reopenActivity()); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetIncident(ctx, in.ID)
	if err != nil || got.State != store.IncIgnored || got.ResolvedAt != nil {
		t.Fatalf("%+v, %v, want ignored and not resolved", got, err)
	}

	// The flag was cleared by the reopen: resolving it again as ignored sets it, un-ignoring first does not.
	if err := s.UnignoreIncident(ctx, in.ID, store.NewActivity{Kind: store.KindIncidentUnignored, Summary: "u"}); err != nil {
		t.Fatal(err)
	}
	resolveAlert(t, s, got)
	if err := s.ReopenIncident(ctx, in.ID, reopenFields(), reopenActivity()); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetIncident(ctx, in.ID); got.State != store.IncOpen {
		t.Errorf("state = %q after an un-ignore, a resolve and a reopen, want open", got.State)
	}
}

func TestReopenOfAnIncidentWithAQueuedRunIsDiagnosing(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	in := openAlert(t, s, "HighLoad/abc", true)
	if _, err := startAlert(s, in, true, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	resolveAlert(t, s, in)
	if err := s.ReopenIncident(ctx, in.ID, reopenFields(), reopenActivity()); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetIncident(ctx, in.ID); got.State != store.IncDiagnosing {
		t.Errorf("state = %q, want diagnosing", got.State)
	}
}

func TestReopenRefusesWhenAnotherIncidentOfTheKeyIsActive(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	old := openAlert(t, s, "HighLoad/abc", true)
	resolveAlert(t, s, old)
	fresh := openAlert(t, s, "HighLoad/abc", true)

	if err := s.ReopenIncident(ctx, old.ID, reopenFields(), reopenActivity()); !errors.Is(err, store.ErrExists) {
		t.Fatalf("error = %v, want ErrExists", err)
	}
	// Nothing moved, and nothing was logged for the old row.
	got, _ := s.GetIncident(ctx, old.ID)
	if got.State != store.IncResolved || got.Occurrences != 1 {
		t.Errorf("the old row changed: %+v", got)
	}
	if log, _ := s.ListActivity(ctx, store.ActivityQuery{IncidentID: old.ID}); len(log) != 2 {
		t.Errorf("activity of the old row = %d entries, want 2 (opened, resolved)", len(log))
	}
	if a, err := s.FindActiveIncidentByKey(ctx, store.SourceAlertmanager, "HighLoad/abc"); err != nil || a.ID != fresh.ID {
		t.Errorf("active = %+v, %v", a, err)
	}
}

func TestReopenOfAnIncidentThatIsNotResolvedIsNotFound(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	in := openAlert(t, s, "HighLoad/abc", true)
	if err := s.ReopenIncident(ctx, in.ID, reopenFields(), reopenActivity()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
	if err := s.ReopenIncident(ctx, in.ID+99, reopenFields(), reopenActivity()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound for an unknown id", err)
	}
	if got, _ := s.GetIncident(ctx, in.ID); got.Occurrences != 1 {
		t.Errorf("occurrences = %d", got.Occurrences)
	}
}

func TestFindLatestResolvedByKeyReturnsTheNewestOfTwo(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	if _, err := s.FindLatestResolvedByKey(ctx, store.SourceAlertmanager, "HighLoad/abc"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
	first := openAlert(t, s, "HighLoad/abc", true)
	resolveAlert(t, s, first)
	second := openAlert(t, s, "HighLoad/abc", true)
	// An active row is not a candidate.
	if got, err := s.FindLatestResolvedByKey(ctx, store.SourceAlertmanager, "HighLoad/abc"); err != nil || got.ID != first.ID {
		t.Fatalf("with one resolved and one active: %+v, %v", got, err)
	}
	resolveAlert(t, s, second)
	got, err := s.FindLatestResolvedByKey(ctx, store.SourceAlertmanager, "HighLoad/abc")
	if err != nil || got.ID != second.ID {
		t.Fatalf("%+v, %v, want incident %d", got, err, second.ID)
	}
	if _, err := s.FindLatestResolvedByKey(ctx, store.SourceArgoCD, "HighLoad/abc"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another source: error = %v, want ErrNotFound", err)
	}
}

func TestFiringsAreCountedFromOpenedAndReopenedEntriesOfTheLastDay(t *testing.T) {
	s, path := openStoreAt(t)
	ctx := context.Background()
	in := openAlert(t, s, "HighLoad/abc", true) // incident_opened, now
	ts := func(d time.Duration) string { return time.Now().Add(d).UTC().Format("2006-01-02T15:04:05.000000000Z") }
	add := func(kind, at string) {
		sqlOn(t, path, `INSERT INTO activity (at, kind, incident_id, summary) VALUES (?, ?, ?, 'x')`, at, kind, in.ID)
	}
	add(store.KindIncidentReopened, ts(-3*time.Hour))
	add(store.KindIncidentReopened, ts(-25*time.Hour))
	add(store.KindIncidentOpened, ts(-26*time.Hour))
	add(store.KindIncidentResolved, ts(-time.Minute)) // not a firing

	got, err := s.GetIncident(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Firings24h != 2 {
		t.Errorf("Firings24h = %d, want 2 (the opened one now, the reopened one 3 h ago)", got.Firings24h)
	}
	if got.LastFiredAt == nil || time.Since(*got.LastFiredAt) > time.Minute {
		t.Errorf("LastFiredAt = %v, want the opened entry from now", got.LastFiredAt)
	}

	// Every query that returns incidents fills the fields.
	list, err := s.ListIncidents(ctx, store.IncidentFilter{State: "all"})
	if err != nil || len(list) != 1 || list[0].Firings24h != 2 || list[0].LastFiredAt == nil {
		t.Errorf("ListIncidents = %+v, %v", list, err)
	}
	un, err := s.ListUnresolvedBySource(ctx, store.SourceAlertmanager)
	if err != nil || len(un) != 1 || un[0].Firings24h != 2 {
		t.Errorf("ListUnresolvedBySource = %+v, %v", un, err)
	}
	act, err := s.FindActiveIncidentByKey(ctx, store.SourceAlertmanager, "HighLoad/abc")
	if err != nil || act.Firings24h != 2 {
		t.Errorf("FindActiveIncidentByKey = %+v, %v", act, err)
	}
}

func TestLastFiredAtIsTheNewestEntryAndNilWithoutOne(t *testing.T) {
	s, path := openStoreAt(t)
	ctx := context.Background()
	in := openAlert(t, s, "HighLoad/abc", true)
	sqlOn(t, path, `DELETE FROM activity WHERE incident_id = ?`, in.ID)
	got, err := s.GetIncident(ctx, in.ID)
	if err != nil || got.Firings24h != 0 || got.LastFiredAt != nil {
		t.Fatalf("without entries: %+v, %v", got, err)
	}
	sqlOn(t, path, `INSERT INTO activity (at, kind, incident_id, summary) VALUES ('2026-10-01T10:00:00.000000000Z', 'incident_opened', ?, 'x')`, in.ID)
	sqlOn(t, path, `INSERT INTO activity (at, kind, incident_id, summary) VALUES ('2026-10-03T10:00:00.000000000Z', 'incident_reopened', ?, 'x')`, in.ID)
	got, _ = s.GetIncident(ctx, in.ID)
	want := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	if got.Firings24h != 0 || got.LastFiredAt == nil || !got.LastFiredAt.Equal(want) {
		t.Errorf("Firings24h %d, LastFiredAt %v, want 0 and %v", got.Firings24h, got.LastFiredAt, want)
	}
}
