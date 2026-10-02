package incident_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/store"
)

type env struct {
	e    *incident.Engine
	st   *store.Store
	repo store.Repo
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.SaveConnection(ctx, store.Connection{
		TokenCiphertext: []byte{1}, TokenHint: "1234", Login: "octo", Status: store.ConnOK,
	}); err != nil {
		t.Fatal(err)
	}
	repo, err := st.AddRepo(ctx, store.ConnectionID, "octo/hello", "main")
	if err != nil {
		t.Fatal(err)
	}
	return &env{e: &incident.Engine{Store: st}, st: st, repo: repo}
}

func (v *env) obs(class incident.Class, ref, check, sha, conclusion string) incident.Observation {
	return incident.Observation{
		RepoID: v.repo.ID, RepoName: v.repo.FullName, Ref: ref, RefURL: "https://github.com/octo/hello/pull/7",
		CheckName: check, Class: class, Conclusion: conclusion, HeadSHA: sha, URL: "https://github.com/octo/hello/runs/1",
	}
}

func (v *env) observe(t *testing.T, o incident.Observation) {
	t.Helper()
	if err := v.e.Observe(context.Background(), o); err != nil {
		t.Fatalf("Observe(%+v): %v", o, err)
	}
}

func (v *env) incidents(t *testing.T, state string) []store.Incident {
	t.Helper()
	list, err := v.st.ListIncidents(context.Background(), store.IncidentFilter{State: state})
	if err != nil {
		t.Fatal(err)
	}
	return list
}

// kinds returns the kinds of all activity entries, oldest first.
func (v *env) kinds(t *testing.T) []string {
	t.Helper()
	log, err := v.st.ListActivity(context.Background(), store.ActivityQuery{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for i := len(log) - 1; i >= 0; i-- {
		out = append(out, log[i].Kind)
	}
	return out
}

func (v *env) summaries(t *testing.T) []string {
	t.Helper()
	log, _ := v.st.ListActivity(context.Background(), store.ActivityQuery{Limit: 1000})
	var out []string
	for i := len(log) - 1; i >= 0; i-- {
		out = append(out, log[i].Summary)
	}
	return out
}

func TestClassify(t *testing.T) {
	cases := []struct {
		status, conclusion string
		want               incident.Class
	}{
		{"completed", "success", incident.Green},
		{"completed", "neutral", incident.Green},
		{"completed", "skipped", incident.Green},
		{"completed", "failure", incident.Bad},
		{"completed", "timed_out", incident.Bad},
		{"completed", "startup_failure", incident.Bad},
		{"completed", "cancelled", incident.Bad},
		{"completed", "action_required", incident.Bad},
		{"queued", "", incident.Pending},
		{"in_progress", "", incident.Pending},
		{"waiting", "", incident.Pending},
		{"completed", "stale", incident.Pending},
		{"completed", "", incident.Pending},
		{"completed", "something_new", incident.Pending},
	}
	for _, tc := range cases {
		if got := incident.Classify(tc.status, tc.conclusion); got != tc.want {
			t.Errorf("Classify(%q, %q) = %v, want %v", tc.status, tc.conclusion, got, tc.want)
		}
	}
	if !(incident.Green < incident.Pending && incident.Pending < incident.Bad) {
		t.Error("the classes must be ordered by severity")
	}
}

func TestABadResultOpensAnIncident(t *testing.T) {
	v := newEnv(t)
	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "aaaaaaaaaa", "failure"))

	list := v.incidents(t, "all")
	if len(list) != 1 {
		t.Fatalf("incidents = %+v", list)
	}
	in := list[0]
	if in.State != store.IncOpen || in.Occurrences != 1 || in.Ref != "pr:7" || in.CheckName != "go" ||
		in.HeadSHA != "aaaaaaaaaa" || in.Conclusion != "failure" || in.RepoName != "octo/hello" ||
		in.CheckURL != "https://github.com/octo/hello/runs/1" || in.RefURL != "https://github.com/octo/hello/pull/7" {
		t.Fatalf("incident = %+v", in)
	}
	if got := v.summaries(t); !slices.Equal(got, []string{"go failed on PR #7 in octo/hello"}) {
		t.Fatalf("summaries = %q", got)
	}
}

func TestEveryBadConclusionIsWordedForTheLog(t *testing.T) {
	v := newEnv(t)
	for i, c := range []struct{ conclusion, summary string }{
		{"failure", "go failed on branch main in octo/hello"},
		{"timed_out", "web timed out on branch main in octo/hello"},
		{"startup_failure", "lint failed to start on branch main in octo/hello"},
		{"cancelled", "docs was cancelled on branch main in octo/hello"},
		{"action_required", "deploy needs action on branch main in octo/hello"},
	} {
		check := []string{"go", "web", "lint", "docs", "deploy"}[i]
		v.observe(t, v.obs(incident.Bad, "branch:main", check, "abc", c.conclusion))
		got := v.summaries(t)
		if got[len(got)-1] != c.summary {
			t.Errorf("%s: summary = %q, want %q", c.conclusion, got[len(got)-1], c.summary)
		}
	}
}

func TestTheSameCommitChangesNothingButLastSeen(t *testing.T) {
	v := newEnv(t)
	o := v.obs(incident.Bad, "pr:7", "go", "aaaaaaaaaa", "failure")
	v.observe(t, o)
	before := v.incidents(t, "all")[0]

	v.observe(t, o)
	v.observe(t, o)

	after := v.incidents(t, "all")
	if len(after) != 1 || after[0].Occurrences != 1 || after[0].HeadSHA != "aaaaaaaaaa" || after[0].LastSeen.Before(before.LastSeen) {
		t.Fatalf("incident after identical observations = %+v", after)
	}
	if got := v.kinds(t); !slices.Equal(got, []string{store.KindIncidentOpened}) {
		t.Fatalf("activity = %v, want only the opening", got)
	}
}

func TestANewCommitIsARecurrence(t *testing.T) {
	v := newEnv(t)
	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "aaaaaaaaaa", "failure"))
	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "bbbbbbbbbb", "timed_out"))

	list := v.incidents(t, "all")
	if len(list) != 1 || list[0].Occurrences != 2 || list[0].HeadSHA != "bbbbbbbbbb" || list[0].Conclusion != "timed_out" {
		t.Fatalf("incidents = %+v", list)
	}
	if got := v.kinds(t); !slices.Equal(got, []string{store.KindIncidentOpened, store.KindIncidentRecurred}) {
		t.Fatalf("activity = %v", got)
	}
	if got := v.summaries(t); got[1] != "go timed out again on PR #7 in octo/hello (commit bbbbbbb)" {
		t.Fatalf("recurrence summary = %q", got[1])
	}
}

func TestPendingChecksChangeNothing(t *testing.T) {
	v := newEnv(t)
	v.observe(t, v.obs(incident.Pending, "pr:7", "go", "aaa", ""))
	if got := v.incidents(t, "all"); len(got) != 0 {
		t.Fatalf("a pending check opened an incident: %+v", got)
	}

	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "aaa", "failure"))
	v.observe(t, v.obs(incident.Pending, "pr:7", "go", "bbb", "")) // a re-run is in progress
	list := v.incidents(t, "all")
	if len(list) != 1 || list[0].State != store.IncOpen || list[0].Occurrences != 1 || list[0].HeadSHA != "aaa" {
		t.Fatalf("incident after a pending observation = %+v", list)
	}
	if got := v.kinds(t); len(got) != 1 {
		t.Fatalf("activity = %v", got)
	}
}

func TestGreenResolvesAndAnotherFailureOpensANewIncident(t *testing.T) {
	v := newEnv(t)
	v.observe(t, v.obs(incident.Green, "pr:7", "go", "aaa", "success"))
	if got := v.incidents(t, "all"); len(got) != 0 || len(v.kinds(t)) != 0 {
		t.Fatalf("green without an incident did something: %+v", got)
	}

	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "aaa", "failure"))
	first := v.incidents(t, "all")[0]
	v.observe(t, v.obs(incident.Green, "pr:7", "go", "bbb", "success"))

	resolved := v.incidents(t, "resolved")
	if len(resolved) != 1 || resolved[0].ID != first.ID || resolved[0].ResolvedReason != incident.ReasonGreen || resolved[0].ResolvedAt == nil {
		t.Fatalf("resolved = %+v", resolved)
	}
	if got := v.summaries(t); got[len(got)-1] != "go is green again on PR #7 in octo/hello" {
		t.Fatalf("summaries = %q", got)
	}

	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "ccc", "failure"))
	all := v.incidents(t, "all")
	if len(all) != 2 || all[0].ID == first.ID || all[0].State != store.IncOpen || all[0].Occurrences != 1 {
		t.Fatalf("a failure after the resolution must open a new incident: %+v", all)
	}
}

func TestAnIgnoredIncidentStaysIgnoredUntilGreen(t *testing.T) {
	v := newEnv(t)
	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "aaa", "failure"))
	in := v.incidents(t, "all")[0]
	if _, err := v.e.Ignore(context.Background(), in.ID); err != nil {
		t.Fatal(err)
	}

	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "bbb", "failure"))
	list := v.incidents(t, "all")
	if len(list) != 1 || list[0].State != store.IncIgnored || list[0].Occurrences != 1 || list[0].HeadSHA != "aaa" {
		t.Fatalf("an ignored incident changed: %+v", list)
	}

	v.observe(t, v.obs(incident.Green, "pr:7", "go", "ccc", "success"))
	if got := v.incidents(t, "resolved"); len(got) != 1 {
		t.Fatalf("green must resolve an ignored incident: %+v", v.incidents(t, "all"))
	}
}

func TestIncidentsAreKeyedByRepoRefAndCheck(t *testing.T) {
	v := newEnv(t)
	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "aaa", "failure"))
	v.observe(t, v.obs(incident.Bad, "pr:7", "web", "aaa", "failure"))
	v.observe(t, v.obs(incident.Bad, "pr:8", "go", "aaa", "failure"))
	v.observe(t, v.obs(incident.Bad, "branch:main", "go", "aaa", "failure"))
	if got := v.incidents(t, "all"); len(got) != 4 {
		t.Fatalf("got %d incidents, want 4", len(got))
	}

	v.observe(t, v.obs(incident.Green, "pr:7", "go", "bbb", "success"))
	if got := v.incidents(t, "active"); len(got) != 3 {
		t.Fatalf("resolving one key must leave the other three: %+v", got)
	}
}

func TestResolveClosedPRs(t *testing.T) {
	v := newEnv(t)
	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "aaa", "failure"))
	v.observe(t, v.obs(incident.Bad, "pr:7", "web", "aaa", "failure"))
	v.observe(t, v.obs(incident.Bad, "pr:8", "go", "aaa", "failure"))
	v.observe(t, v.obs(incident.Bad, "branch:main", "go", "aaa", "failure"))

	if err := v.e.ResolveClosedPRs(context.Background(), v.repo.ID, map[int]bool{8: true}); err != nil {
		t.Fatal(err)
	}

	resolved := v.incidents(t, "resolved")
	if len(resolved) != 2 {
		t.Fatalf("resolved = %+v, want the two incidents of PR 7", resolved)
	}
	for _, in := range resolved {
		if in.Ref != "pr:7" || in.ResolvedReason != incident.ReasonPRClosed {
			t.Errorf("resolved incident = %+v", in)
		}
	}
	if got := v.incidents(t, "active"); len(got) != 2 {
		t.Fatalf("active = %+v, want pr:8 and the branch", got)
	}
	if got := v.summaries(t); got[len(got)-1] != "PR #7 in octo/hello was closed or merged; the incident for web is resolved" &&
		got[len(got)-2] != "PR #7 in octo/hello was closed or merged; the incident for web is resolved" {
		t.Fatalf("summaries = %q", got)
	}

	// Running it again changes nothing.
	if err := v.e.ResolveClosedPRs(context.Background(), v.repo.ID, map[int]bool{8: true}); err != nil {
		t.Fatal(err)
	}
	if got := v.kinds(t); len(got) != 6 {
		t.Fatalf("activity = %v, want 4 openings and 2 resolutions", got)
	}
}

func TestIgnore(t *testing.T) {
	v := newEnv(t)
	ctx := context.Background()
	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "aaa", "failure"))
	in := v.incidents(t, "all")[0]

	got, err := v.e.Ignore(ctx, in.ID)
	if err != nil || got.State != store.IncIgnored {
		t.Fatalf("Ignore = %+v, %v", got, err)
	}
	if s := v.summaries(t); s[len(s)-1] != "Ignored the incident for go on PR #7 in octo/hello" {
		t.Fatalf("summaries = %q", s)
	}
	if _, err := v.e.Ignore(ctx, in.ID); !errors.Is(err, incident.ErrNotActive) {
		t.Fatalf("ignoring twice: error = %v, want ErrNotActive", err)
	}
	if _, err := v.e.Ignore(ctx, 9999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ignoring an unknown incident: error = %v, want ErrNotFound", err)
	}

	v.observe(t, v.obs(incident.Green, "pr:7", "go", "bbb", "success"))
	if _, err := v.e.Ignore(ctx, in.ID); !errors.Is(err, incident.ErrNotActive) {
		t.Fatalf("ignoring a resolved incident: error = %v, want ErrNotActive", err)
	}
}
