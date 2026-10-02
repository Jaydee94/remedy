package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

func seedRepo(t *testing.T, s *store.Store) store.Repo {
	t.Helper()
	ctx := context.Background()
	if err := s.SaveConnection(ctx, connection("octo")); err != nil {
		t.Fatal(err)
	}
	r, err := s.AddRepo(ctx, store.ConnectionID, "octo/hello", "main")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func failing(repoID int64, ref, check, sha string) store.NewIncident {
	return store.NewIncident{
		RepoID: repoID, Ref: ref, RefURL: "https://github.com/octo/hello/pull/7", CheckName: check,
		Conclusion: "failure", HeadSHA: sha, CheckURL: "https://github.com/octo/hello/runs/1",
	}
}

func entry(kind string, repoID int64) store.NewActivity {
	return store.NewActivity{Kind: kind, RepoID: repoID, Summary: kind + " summary"}
}

func TestOpenIncidentStoresItAndLogsTheActivity(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)

	in, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))
	if err != nil {
		t.Fatal(err)
	}
	if in.State != store.IncOpen || in.Occurrences != 1 || in.RepoName != "octo/hello" || in.RepoID != repo.ID ||
		in.Ref != "pr:7" || in.CheckName != "go" || in.HeadSHA != "aaa" || in.Conclusion != "failure" ||
		in.RefURL == "" || in.CheckURL == "" || in.ResolvedAt != nil || in.FirstSeen.IsZero() || !in.FirstSeen.Equal(in.LastSeen) {
		t.Fatalf("incident = %+v", in)
	}

	log, err := s.ListActivity(ctx, store.ActivityQuery{IncidentID: in.ID})
	if err != nil || len(log) != 1 {
		t.Fatalf("activity = %+v, err = %v", log, err)
	}
	a := log[0]
	if a.Kind != store.KindIncidentOpened || a.IncidentID != in.ID || a.RepoID != repo.ID ||
		a.Summary != "incident_opened summary" || string(a.Data) != "{}" || a.At.IsZero() {
		t.Fatalf("activity entry = %+v", a)
	}
}

func TestOnlyOneActiveIncidentPerKey(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)

	first, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "bbb"), entry(store.KindIncidentOpened, repo.ID)); !errors.Is(err, store.ErrExists) {
		t.Fatalf("second incident for the same key: error = %v, want ErrExists", err)
	}
	if all, _ := s.ListActivity(ctx, store.ActivityQuery{}); len(all) != 1 {
		t.Fatalf("the refused incident left %d activity entries, want 1", len(all))
	}
	// Another check name, another ref: both are different keys.
	if _, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "web", "aaa"), entry(store.KindIncidentOpened, repo.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenIncident(ctx, failing(repo.ID, "pr:8", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID)); err != nil {
		t.Fatal(err)
	}

	// A resolved incident no longer blocks the key.
	if err := s.ResolveIncident(ctx, first.ID, "green", entry(store.KindIncidentResolved, repo.ID)); err != nil {
		t.Fatal(err)
	}
	second, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "ccc"), entry(store.KindIncidentOpened, repo.ID))
	if err != nil || second.ID == first.ID {
		t.Fatalf("reopening after resolve: %+v, %v", second, err)
	}
	found, err := s.FindActiveIncident(ctx, repo.ID, "pr:7", "go")
	if err != nil || found.ID != second.ID {
		t.Fatalf("FindActiveIncident = %+v, %v, want the new incident %d", found, err, second.ID)
	}
}

func TestRecordRecurrence(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in, _ := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))

	if err := s.RecordRecurrence(ctx, in.ID, "timed_out", "bbb", "https://example.test/2", entry(store.KindIncidentRecurred, repo.ID)); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetIncident(ctx, in.ID)
	if got.Occurrences != 2 || got.HeadSHA != "bbb" || got.Conclusion != "timed_out" || got.CheckURL != "https://example.test/2" ||
		got.State != store.IncOpen || !got.FirstSeen.Equal(in.FirstSeen) || got.LastSeen.Before(in.LastSeen) {
		t.Fatalf("incident after the recurrence = %+v", got)
	}
	log, _ := s.ListActivity(ctx, store.ActivityQuery{IncidentID: in.ID})
	if len(log) != 2 || log[0].Kind != store.KindIncidentRecurred || log[1].Kind != store.KindIncidentOpened {
		t.Fatalf("activity = %+v, want recurred then opened (newest first)", log)
	}

	_ = s.ResolveIncident(ctx, in.ID, "green", entry(store.KindIncidentResolved, repo.ID))
	if err := s.RecordRecurrence(ctx, in.ID, "failure", "ccc", "", entry(store.KindIncidentRecurred, repo.ID)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("recurrence on a resolved incident: error = %v, want ErrNotFound", err)
	}
}

func TestTouchIncidentOnlyMovesLastSeen(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in, _ := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))

	if err := s.TouchIncident(ctx, in.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetIncident(ctx, in.ID)
	if got.LastSeen.Before(in.LastSeen) || got.Occurrences != 1 || got.HeadSHA != "aaa" {
		t.Fatalf("incident after touch = %+v", got)
	}
	if log, _ := s.ListActivity(ctx, store.ActivityQuery{IncidentID: in.ID}); len(log) != 1 {
		t.Fatalf("touch wrote an activity entry: %+v", log)
	}
	if err := s.TouchIncident(ctx, 999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("touch of an unknown incident: error = %v", err)
	}
}

func TestResolveIncident(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in, _ := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))

	if err := s.ResolveIncident(ctx, in.ID, "green", entry(store.KindIncidentResolved, repo.ID)); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetIncident(ctx, in.ID)
	if got.State != store.IncResolved || got.ResolvedReason != "green" || got.ResolvedAt == nil {
		t.Fatalf("incident after resolve = %+v", got)
	}
	if _, err := s.FindActiveIncident(ctx, repo.ID, "pr:7", "go"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a resolved incident is still active: %v", err)
	}
	if err := s.ResolveIncident(ctx, in.ID, "green", entry(store.KindIncidentResolved, repo.ID)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second resolve: error = %v, want ErrNotFound", err)
	}
	if log, _ := s.ListActivity(ctx, store.ActivityQuery{IncidentID: in.ID}); len(log) != 2 || log[0].Kind != store.KindIncidentResolved {
		t.Fatalf("activity = %+v", log)
	}
}

func TestIgnoreIncident(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in, _ := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))

	if err := s.IgnoreIncident(ctx, in.ID, entry(store.KindIncidentIgnored, repo.ID)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetIncident(ctx, in.ID); got.State != store.IncIgnored {
		t.Fatalf("state = %q, want ignored", got.State)
	}
	// An ignored incident still holds its key, so that a new failure does not open a second one.
	if _, err := s.FindActiveIncident(ctx, repo.ID, "pr:7", "go"); err != nil {
		t.Fatalf("FindActiveIncident of an ignored incident: %v", err)
	}
	if err := s.IgnoreIncident(ctx, in.ID, entry(store.KindIncidentIgnored, repo.ID)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ignoring twice: error = %v, want ErrNotFound", err)
	}

	other, _ := s.OpenIncident(ctx, failing(repo.ID, "pr:8", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))
	_ = s.ResolveIncident(ctx, other.ID, "green", entry(store.KindIncidentResolved, repo.ID))
	if err := s.IgnoreIncident(ctx, other.ID, entry(store.KindIncidentIgnored, repo.ID)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ignoring a resolved incident: error = %v, want ErrNotFound", err)
	}
}

func TestListIncidentsFiltersAndOrders(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	other, err := s.AddRepo(ctx, store.ConnectionID, "octo/other", "main")
	if err != nil {
		t.Fatal(err)
	}
	open := func(repoID int64, ref, check string) store.Incident {
		in, err := s.OpenIncident(ctx, failing(repoID, ref, check, "aaa"), entry(store.KindIncidentOpened, repoID))
		if err != nil {
			t.Fatal(err)
		}
		return in
	}
	a := open(repo.ID, "pr:7", "go")
	b := open(repo.ID, "pr:8", "go")
	c := open(repo.ID, "pr:9", "web")
	d := open(other.ID, "pr:1", "go")
	_ = s.ResolveIncident(ctx, b.ID, "green", entry(store.KindIncidentResolved, repo.ID))
	_ = s.IgnoreIncident(ctx, c.ID, entry(store.KindIncidentIgnored, repo.ID))

	ids := func(f store.IncidentFilter) []int64 {
		list, err := s.ListIncidents(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		out := []int64{}
		for _, in := range list {
			out = append(out, in.ID)
		}
		return out
	}
	same := func(got, want []int64) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	cases := []struct {
		name   string
		filter store.IncidentFilter
		want   []int64
	}{
		{"all, newest first", store.IncidentFilter{}, []int64{d.ID, c.ID, b.ID, a.ID}},
		{"all, spelled out", store.IncidentFilter{State: "all"}, []int64{d.ID, c.ID, b.ID, a.ID}},
		{"active", store.IncidentFilter{State: "active"}, []int64{d.ID, a.ID}},
		{"resolved", store.IncidentFilter{State: "resolved"}, []int64{b.ID}},
		{"ignored", store.IncidentFilter{State: "ignored"}, []int64{c.ID}},
		{"one repo", store.IncidentFilter{RepoID: repo.ID}, []int64{c.ID, b.ID, a.ID}},
		{"one repo, active", store.IncidentFilter{RepoID: repo.ID, State: "active"}, []int64{a.ID}},
		{"limit", store.IncidentFilter{Limit: 2}, []int64{d.ID, c.ID}},
	}
	for _, tc := range cases {
		if got := ids(tc.filter); !same(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}

	active, err := s.ListActiveIncidents(ctx, repo.ID)
	if err != nil || len(active) != 2 {
		t.Fatalf("ListActiveIncidents = %+v, %v; want the open and the ignored incident of the repo", active, err)
	}
	if _, err := s.GetIncident(ctx, 9999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetIncident(unknown) error = %v", err)
	}
}

func TestListActivityIsNewestFirstAndLimited(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	for _, kind := range []string{"a", "b", "c", "d", "e"} {
		if err := s.AddActivity(ctx, store.NewActivity{Kind: kind, Summary: kind}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListActivity(ctx, store.ActivityQuery{Limit: 3})
	if err != nil || len(got) != 3 || got[0].Kind != "e" || got[1].Kind != "d" || got[2].Kind != "c" {
		t.Fatalf("activity = %+v, err = %v", got, err)
	}
	if got[0].RepoID != 0 || got[0].IncidentID != 0 || got[0].RunID != "" {
		t.Fatalf("an entry without links has links: %+v", got[0])
	}
}

func TestAddActivityKeepsValidDataAndRejectsInvalidData(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	if err := s.AddActivity(ctx, store.NewActivity{Kind: "k", Summary: "s", Data: []byte(`{"a":1}`)}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddActivity(ctx, store.NewActivity{Kind: "k", Summary: "s", Data: []byte(`{bad`)}); err == nil {
		t.Fatal("expected an error for data that is not JSON")
	}
	got, _ := s.ListActivity(ctx, store.ActivityQuery{})
	if len(got) != 1 || string(got[0].Data) != `{"a":1}` {
		t.Fatalf("activity = %+v", got)
	}
}

func TestDeletingARepoKeepsItsHistory(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in, _ := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))

	if err := s.DeleteRepo(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetIncident(ctx, in.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the incident of a removed repo is still there: %v", err)
	}
	log, _ := s.ListActivity(ctx, store.ActivityQuery{})
	if len(log) != 1 || log[0].IncidentID != 0 || log[0].RepoID != 0 || log[0].Summary == "" {
		t.Fatalf("history after removing the repo = %+v", log)
	}
}

func TestMarkRepoPolled(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	if err := s.MarkRepoPolled(ctx, repo.ID, at, "boom"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetRepo(ctx, repo.ID)
	if got.LastPolledAt == nil || !got.LastPolledAt.Equal(at) || got.LastError != "boom" {
		t.Fatalf("repo = %+v", got)
	}
	if err := s.MarkRepoPolled(ctx, repo.ID, at.Add(time.Minute), ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetRepo(ctx, repo.ID); got.LastError != "" {
		t.Fatalf("last error = %q, want it cleared", got.LastError)
	}
	if err := s.MarkRepoPolled(ctx, 9999, at, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown repo: error = %v", err)
	}
}
