package store_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

// oldDatabase builds a database the way version 007 left it, with incidents and everything that points at them:
// it applies the migration files below 008 from disk, records them, and fills the tables with raw SQL. The names of
// the checks are the awkward ones: characters that JSON and shell quoting treat specially, a separator-like bar and a
// non-ASCII letter.
func oldDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v007.db")
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	raw.SetMaxOpenConns(1)

	files, err := filepath.Glob("migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no migration files: %v", err)
	}
	sort.Strings(files)
	if _, err := raw.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		name := filepath.Base(f)
		if name >= "008" {
			continue
		}
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(body)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, name); err != nil {
			t.Fatal(err)
		}
	}

	const at = "2026-10-04T12:00:00.000000000Z"
	seed := []string{
		`INSERT INTO github_connections (id, token_ciphertext, token_hint, login, status, checked_at) VALUES (1, x'00', '…abcd', 'octo', 'ok', '` + at + `')`,
		`INSERT INTO repos (id, connection_id, full_name, default_branch, created_at) VALUES (1, 1, 'octo/hello', 'main', '` + at + `')`,
		`INSERT INTO repos (id, connection_id, full_name, default_branch, created_at) VALUES (2, 1, 'octo/other', 'main', '` + at + `')`,
		`INSERT INTO runs (id, provider, prompt, status, created_at, role) VALUES ('run-1', 'claude', 'diagnose', 'succeeded', '` + at + `', 'responder')`,
		// Incident 1: open, diagnosed once, a check name with an ampersand.
		`INSERT INTO incidents (id, repo_id, ref, ref_url, check_name, state, conclusion, head_sha, check_url, occurrences, diagnoses, first_seen, last_seen,
		   diagnosis, run_id, diagnosed_sha)
		 VALUES (1, 1, 'pr:7', 'https://github.com/octo/hello/pull/7', 'build & test', 'open', 'failure', 'aaa', 'https://github.com/octo/hello/runs/1', 3, 2,
		   '` + at + `', '` + at + `', '{"summary":"s"}', 'run-1', 'aaa')`,
		// Incident 2: resolved, timed out.
		`INSERT INTO incidents (id, repo_id, ref, check_name, state, conclusion, head_sha, first_seen, last_seen, resolved_at, resolved_reason)
		 VALUES (2, 1, 'branch:main', 'go', 'resolved', 'timed_out', 'bbb', '` + at + `', '` + at + `', '` + at + `', 'green')`,
		// Incident 3: ignored, cancelled (not diagnosed automatically), a name with a bar, a quote and a non-ASCII letter.
		`INSERT INTO incidents (id, repo_id, ref, check_name, state, conclusion, head_sha, first_seen, last_seen)
		 VALUES (3, 2, 'pr:9', 'lint | ü "quoted"', 'ignored', 'cancelled', 'ccc', '` + at + `', '` + at + `')`,
		// Incident 4 existed and is gone: the sequence of ids must not go back below it.
		`INSERT INTO incidents (id, repo_id, ref, check_name, state, conclusion, head_sha, first_seen, last_seen)
		 VALUES (4, 1, 'pr:11', 'gone', 'open', 'failure', 'ddd', '` + at + `', '` + at + `')`,
		`DELETE FROM incidents WHERE id = 4`,
		`INSERT INTO activity (at, kind, repo_id, incident_id, summary) VALUES ('` + at + `', 'incident_opened', 1, 1, 'opened one')`,
		`INSERT INTO activity (at, kind, repo_id, incident_id, summary) VALUES ('` + at + `', 'incident_opened', 2, 3, 'opened three')`,
		`INSERT INTO incident_notes (incident_id, run_id, note, created_at) VALUES (1, 'run-1', 'first note', '` + at + `')`,
		`INSERT INTO incident_notes (incident_id, run_id, note, created_at) VALUES (1, NULL, 'second note', '` + at + `')`,
		`INSERT INTO incident_notes (incident_id, run_id, note, created_at) VALUES (3, NULL, 'note on three', '` + at + `')`,
		`INSERT INTO tool_calls (run_id, incident_id, tool_use_id, tool, kind, status, created_at) VALUES ('run-1', 1, 'toolu_1', 'incident_get', 'read', 'succeeded', '` + at + `')`,
		`UPDATE runs SET incident_id = 1 WHERE id = 'run-1'`,
	}
	for _, q := range seed {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	return path
}

func TestMigration008KeepsEveryIncidentAndEverythingThatPointsAtIt(t *testing.T) {
	path := oldDatabase(t)
	ctx := context.Background()
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open on a 007 database: %v", err)
	}
	defer s.Close()

	list, err := s.ListIncidents(ctx, store.IncidentFilter{State: "all"})
	if err != nil || len(list) != 3 {
		t.Fatalf("ListIncidents = %d incidents, %v, want 3", len(list), err)
	}
	byID := map[int64]store.Incident{}
	for _, in := range list {
		byID[in.ID] = in
	}
	one, two, three := byID[1], byID[2], byID[3]
	if one.RepoName != "octo/hello" || one.Ref != "pr:7" || one.CheckName != "build & test" || one.State != store.IncOpen ||
		one.Conclusion != "failure" || one.HeadSHA != "aaa" || one.Occurrences != 3 || one.Diagnoses != 2 ||
		string(one.Diagnosis) != `{"summary":"s"}` || one.DiagnosedSHA != "aaa" || one.RunID != "run-1" || one.RefURL == "" || one.CheckURL == "" {
		t.Errorf("incident 1 changed: %+v", one)
	}
	if two.State != store.IncResolved || two.ResolvedReason != "green" || two.ResolvedAt == nil || two.Conclusion != "timed_out" {
		t.Errorf("incident 2 changed: %+v", two)
	}
	if three.State != store.IncIgnored || three.CheckName != `lint | ü "quoted"` || three.RepoName != "octo/other" {
		t.Errorf("incident 3 changed: %+v", three)
	}

	// What the migration adds: every old row is a GitHub incident, titled by its check, with no severity, and it is
	// diagnosed automatically exactly for the conclusions that were.
	for id, want := range map[int64]bool{1: true, 2: true, 3: false} {
		in := byID[id]
		if in.Source != store.SourceGitHub || in.Title != in.CheckName || in.Severity != "none" || string(in.Details) != "{}" || in.AutoDiagnose != want {
			t.Errorf("incident %d: source %q, title %q, severity %q, details %s, auto %v (want %v)",
				id, in.Source, in.Title, in.Severity, in.Details, in.AutoDiagnose, want)
		}
	}

	// The key the migration computed in SQL is the key the code computes, also for the awkward names: the next cycle of
	// the poller must find the incident that is open, not open a second one.
	if got, err := s.FindActiveIncident(ctx, 1, "pr:7", "build & test"); err != nil || got.ID != 1 {
		t.Errorf("FindActiveIncident(build & test) = %+v, %v, want incident 1", got, err)
	}
	if got, err := s.FindActiveIncident(ctx, 2, "pr:9", `lint | ü "quoted"`); err != nil || got.ID != 3 {
		t.Errorf("FindActiveIncident(lint | ü) = %+v, %v, want incident 3", got, err)
	}
	if _, err := s.FindActiveIncident(ctx, 1, "branch:main", "go"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("FindActiveIncident of the resolved incident 2 = %v, want ErrNotFound", err)
	}

	// Everything that pointed at an incident still does. A rebuild that drops the table with foreign keys on would
	// have deleted the notes and cleared the other links.
	if notes, err := s.ListNotes(ctx, 1); err != nil || len(notes) != 2 {
		t.Errorf("notes of incident 1 = %d, %v, want 2", len(notes), err)
	}
	if notes, err := s.ListNotes(ctx, 3); err != nil || len(notes) != 1 {
		t.Errorf("notes of incident 3 = %d, %v, want 1", len(notes), err)
	}
	if log, err := s.ListActivity(ctx, store.ActivityQuery{IncidentID: 1}); err != nil || len(log) != 1 || log[0].Summary != "opened one" {
		t.Errorf("activity of incident 1 = %+v, %v", log, err)
	}
	if calls, err := s.ListToolCalls(ctx, "run-1"); err != nil || len(calls) != 1 || calls[0].IncidentID != 1 {
		t.Errorf("tool calls of run-1 = %+v, %v", calls, err)
	}
	if r, err := s.GetRun(ctx, "run-1"); err != nil || r.IncidentID == nil || *r.IncidentID != 1 {
		t.Errorf("run-1 = %+v, %v, want incident 1", r, err)
	}

	// The sequence goes on above the incident that was deleted, so an id is never used twice.
	fresh, err := s.OpenIncident(ctx, store.NewIncident{RepoID: 1, Ref: "pr:12", CheckName: "new", Conclusion: "failure", HeadSHA: "eee"},
		store.NewActivity{Kind: store.KindIncidentOpened, RepoID: 1, Summary: "opened"})
	if err != nil || fresh.ID != 5 {
		t.Fatalf("a new incident = %+v, %v, want id 5", fresh, err)
	}

	// Foreign keys are on again after the migration: removing a repository takes its incidents and their notes with it
	// and leaves the other repository alone.
	if err := s.DeleteRepo(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetIncident(ctx, 3); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("incident 3 after the removal of its repository: %v, want ErrNotFound", err)
	}
	if notes, _ := s.ListNotes(ctx, 3); len(notes) != 0 {
		t.Errorf("the notes of incident 3 outlived it: %+v", notes)
	}
	if _, err := s.GetIncident(ctx, 1); err != nil {
		t.Errorf("incident 1 after the removal of the other repository: %v", err)
	}
}

func TestMigration008LetsAResolvedKeyReopenAndKeepsASourceApart(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)

	first, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "bbb"), entry(store.KindIncidentOpened, repo.ID)); !errors.Is(err, store.ErrExists) {
		t.Fatalf("a second active incident for one key: %v, want ErrExists", err)
	}
	if err := s.ResolveIncident(ctx, first.ID, "green", entry(store.KindIncidentResolved, repo.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "ccc"), entry(store.KindIncidentOpened, repo.ID)); err != nil {
		t.Fatalf("a resolved key must reopen: %v", err)
	}

	// The same key in another source is another incident.
	alert := store.NewIncident{Source: store.SourceAlertmanager, Key: "KubePodCrashLooping/abc", Title: "KubePodCrashLooping demo/web",
		Severity: "critical", AutoDiagnose: true, Conclusion: "firing", Details: []byte(`{"labels":{"namespace":"demo"}}`)}
	a, err := s.OpenIncident(ctx, alert, store.NewActivity{Kind: store.KindIncidentOpened, Summary: "opened"})
	if err != nil {
		t.Fatalf("OpenIncident of an alert: %v", err)
	}
	if a.Source != store.SourceAlertmanager || a.RepoID != 0 || a.RepoName != "" || a.Title != alert.Title || a.Severity != "critical" ||
		!a.AutoDiagnose || a.Conclusion != "firing" || string(a.Details) != `{"labels":{"namespace":"demo"}}` {
		t.Errorf("alert incident = %+v", a)
	}
	if _, err := s.OpenIncident(ctx, alert, store.NewActivity{Kind: store.KindIncidentOpened, Summary: "again"}); !errors.Is(err, store.ErrExists) {
		t.Errorf("the same alert twice: %v, want ErrExists", err)
	}
	other := alert
	other.Source = store.SourceArgoCD
	if _, err := s.OpenIncident(ctx, other, store.NewActivity{Kind: store.KindIncidentOpened, Summary: "argo"}); err != nil {
		t.Errorf("the same key in another source: %v", err)
	}
	found, err := s.FindActiveIncidentByKey(ctx, store.SourceAlertmanager, alert.Key)
	if err != nil || found.ID != a.ID {
		t.Errorf("FindActiveIncidentByKey = %+v, %v, want %d", found, err, a.ID)
	}
}

func TestOpenIncidentRefusesWhatCannotBeStored(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	act := store.NewActivity{Kind: store.KindIncidentOpened, Summary: "opened"}
	for name, n := range map[string]store.NewIncident{
		"an alert without a key":     {Source: store.SourceAlertmanager, Title: "t", Conclusion: "firing"},
		"an alert without a title":   {Source: store.SourceAlertmanager, Key: "k", Conclusion: "firing"},
		"a source nobody knows":      {Source: "loki", Key: "k", Title: "t", Conclusion: "firing"},
		"a severity nobody knows":    {Source: store.SourceAlertmanager, Key: "k", Title: "t", Severity: "urgent", Conclusion: "firing"},
		"details that are no JSON":   {Source: store.SourceAlertmanager, Key: "k", Title: "t", Conclusion: "firing", Details: []byte(`{`)},
		"a GitHub incident, no repo": {Ref: "pr:1", CheckName: "go", Conclusion: "failure", HeadSHA: "a"},
		"an alert with a repo":       {Source: store.SourceAlertmanager, RepoID: 1, Key: "k", Title: "t", Conclusion: "firing"},
	} {
		if _, err := s.OpenIncident(ctx, n, act); err == nil || errors.Is(err, store.ErrExists) {
			t.Errorf("%s: err = %v, want a refusal", name, err)
		}
	}
	if list, _ := s.ListIncidents(ctx, store.IncidentFilter{State: "all"}); len(list) != 0 {
		t.Errorf("a refused incident was stored: %+v", list)
	}
	if log, _ := s.ListActivity(ctx, store.ActivityQuery{}); len(log) != 0 {
		t.Errorf("a refused incident left activity: %+v", log)
	}
}

func TestListIncidentsFiltersBySource(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	if _, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenIncident(ctx, store.NewIncident{Source: store.SourceArgoCD, Key: "guestbook", Title: "guestbook is Degraded", Conclusion: "degraded"},
		store.NewActivity{Kind: store.KindIncidentOpened, Summary: "argo"}); err != nil {
		t.Fatal(err)
	}
	for source, want := range map[string]int{"": 2, store.SourceGitHub: 1, store.SourceArgoCD: 1, store.SourceAlertmanager: 0} {
		list, err := s.ListIncidents(ctx, store.IncidentFilter{State: "all", Source: source})
		if err != nil || len(list) != want {
			t.Errorf("source %q: %d incidents, %v, want %d", source, len(list), err, want)
		}
	}
	// A repository filter still means the incidents of that repository.
	if list, _ := s.ListIncidents(ctx, store.IncidentFilter{State: "all", RepoID: repo.ID}); len(list) != 1 || list[0].Source != store.SourceGitHub {
		t.Errorf("repo filter = %+v", list)
	}
}

func TestGitHubKeyTellsFieldsApart(t *testing.T) {
	keys := map[string]bool{}
	for _, k := range []string{
		store.GitHubKey(1, "branch:a", "b|c"), store.GitHubKey(1, "branch:a|b", "c"), store.GitHubKey(11, "branch:a", "bc"),
		store.GitHubKey(1, "pr:7", "go"), store.GitHubKey(2, "pr:7", "go"), store.GitHubKey(1, "pr:7", "go "),
	} {
		if keys[k] {
			t.Errorf("two different incidents share the key %q", strings.ReplaceAll(k, "\x1f", "<US>"))
		}
		keys[k] = true
	}
}
