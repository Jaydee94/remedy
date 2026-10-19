package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

const fresh = 10 * time.Minute

func alertIncident(t *testing.T, s *store.Store, key string, auto bool) store.Incident {
	t.Helper()
	in, err := s.OpenIncident(context.Background(), store.NewIncident{Source: store.SourceAlertmanager, Key: key, Title: "t " + key, Severity: "critical", Conclusion: "firing", AutoDiagnose: auto},
		store.NewActivity{Kind: store.KindIncidentOpened, Summary: "opened"})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// openStoreAt is openStore with the file's path, for a test that must change a row the store has no method for.
func openStoreAt(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

// sqlOn runs one statement on the file through a second connection that is closed again.
func sqlOn(t *testing.T, path, stmt string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(stmt, args...); err != nil {
		t.Fatal(err)
	}
}

func startAlert(s *store.Store, in store.Incident, auto bool, now time.Time, mod func(*store.StartParams)) (run.Run, error) {
	p := store.StartParams{IncidentID: in.ID, Provider: "claude", Prompt: "outage", Automatic: auto, Limits: store.DefaultLimits(), Now: now}
	if mod != nil {
		mod(&p)
	}
	return s.StartDiagnosis(context.Background(), p, store.NewActivity{Kind: store.KindDiagnosisStarted, Summary: "started"})
}

func TestAnOutageRunHasGatewayAccessTheClusterFlagAndTheHostsOfTheIncident(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	h, err := s.CreateHost(ctx, hostFields("nas"))
	if err != nil {
		t.Fatal(err)
	}

	r, err := startAlert(s, alertIncident(t, s, "a/1", true), true, time.Now(), func(p *store.StartParams) {
		p.Tools, p.Cluster, p.HostIDs = true, true, []int64{h.ID}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !r.MCP || !r.Cluster || r.HostCount != 1 || r.Role != run.RoleResponder || !r.Automatic {
		t.Fatalf("run = %+v, want mcp, cluster, one host, responder, automatic", r)
	}
	hosts, err := s.RunHosts(ctx, r.ID)
	if err != nil || len(hosts) != 1 || hosts[0].Host.Name != "nas" {
		t.Fatalf("RunHosts = %+v, %v, want nas", hosts, err)
	}
	finishRun(t, s, r.ID, run.Outcome{ExitCode: 1})
	if err := s.FailDiagnosis(ctx, r.ID, store.NewActivity{Kind: store.KindDiagnosisFailed, Summary: "failed"}); err != nil {
		t.Fatal(err)
	}

	r2, err := startAlert(s, alertIncident(t, s, "a/2", true), false, time.Now(), func(p *store.StartParams) { p.Tools = true })
	if err != nil {
		t.Fatal(err)
	}
	if !r2.MCP || r2.Cluster || r2.HostCount != 0 || r2.Automatic {
		t.Fatalf("run with tools only = %+v, want mcp, no cluster, no hosts, not automatic", r2)
	}
	finishRun(t, s, r2.ID, run.Outcome{ExitCode: 1})
	if err := s.FailDiagnosis(ctx, r2.ID, store.NewActivity{Kind: store.KindDiagnosisFailed, Summary: "failed"}); err != nil {
		t.Fatal(err)
	}

	r3, err := startAlert(s, alertIncident(t, s, "a/3", true), false, time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if r3.MCP || r3.Cluster || r3.HostCount != 0 {
		t.Fatalf("run without options = %+v, want no gatekeeper access", r3)
	}
}

func TestAHostThatNoLongerExistsIsSkipped(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	h, err := s.CreateHost(ctx, hostFields("nas"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := startAlert(s, alertIncident(t, s, "a/1", true), false, time.Now(), func(p *store.StartParams) {
		p.HostIDs = []int64{9999, h.ID, h.ID, 0}
	})
	if err != nil {
		t.Fatal(err)
	}
	hosts, err := s.RunHosts(ctx, r.ID)
	if err != nil || len(hosts) != 1 || hosts[0].Host.Name != "nas" || r.HostCount != 1 || !r.MCP {
		t.Fatalf("hosts = %+v, %v, run = %+v, want nas once, HostCount 1, mcp", hosts, err, r)
	}

	// Only hosts that do not exist: the run has no hosts and, without Tools, no access.
	s2 := openStore(t)
	r2, err := startAlert(s2, alertIncident(t, s2, "a/1", true), false, time.Now(), func(p *store.StartParams) { p.HostIDs = []int64{9999} })
	if err != nil || r2.HostCount != 0 || r2.MCP {
		t.Fatalf("run = %+v, %v, want no hosts and no access", r2, err)
	}
}

func TestACandidateOfAnotherSourceNeedsAutoDiagnoseOpenStateAndFreshness(t *testing.T) {
	s, path := openStoreAt(t)
	ctx := context.Background()

	want := alertIncident(t, s, "fresh-auto", true)
	alertIncident(t, s, "fresh-manual", false)
	old := alertIncident(t, s, "old-auto", true)
	sqlOn(t, path, `UPDATE incidents SET last_seen = strftime('%Y-%m-%dT%H:%M:%S','now','-1 hour') || '.000000000Z' WHERE id = ?`, old.ID)

	diagnosing := alertIncident(t, s, "diagnosing", true)
	sqlOn(t, path, `UPDATE incidents SET state = 'diagnosing' WHERE id = ?`, diagnosing.ID)
	diagnosed := alertIncident(t, s, "diagnosed", true)
	sqlOn(t, path, `UPDATE incidents SET state = 'diagnosed' WHERE id = ?`, diagnosed.ID)
	resolved := alertIncident(t, s, "resolved", true)
	if err := s.ResolveIncident(ctx, resolved.ID, "cleared", entry(store.KindIncidentResolved, 0)); err != nil {
		t.Fatal(err)
	}
	ignored := alertIncident(t, s, "ignored", true)
	if err := s.IgnoreIncident(ctx, ignored.ID, entry(store.KindIncidentIgnored, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenIncident(ctx, store.NewIncident{Source: store.SourceArgoCD, Key: "argo/1", Title: "t", Severity: "critical", Conclusion: "Degraded", AutoDiagnose: true},
		store.NewActivity{Kind: store.KindIncidentOpened, Summary: "opened"}); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListAutoCandidates(ctx, time.Now(), store.DefaultLimits(), fresh)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != want.ID {
		var keys []string
		for _, in := range got {
			keys = append(keys, in.Key)
		}
		t.Fatalf("candidates = %v, want only fresh-auto", keys)
	}
}

func TestTheCapAndTheCooldownApplyToAlertIncidentsToo(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	l := store.DiagnosisLimits{Cooldown: 15 * time.Minute, MaxPerIncident: 2, MaxPerDay: 20}
	in := alertIncident(t, s, "a/1", true)
	now := time.Now()

	r, err := startAlert(s, in, true, now, func(p *store.StartParams) { p.Limits = l })
	if err != nil {
		t.Fatal(err)
	}
	failRun(t, s, r)
	if got, _ := s.ListAutoCandidates(ctx, now.Add(time.Minute), l, fresh); len(got) != 0 {
		t.Fatalf("inside the cooldown there are %d candidates, want 0", len(got))
	}
	after := now.Add(20 * time.Minute)
	if got, _ := s.ListAutoCandidates(ctx, after, l, time.Hour); len(got) != 1 {
		t.Fatalf("after the cooldown there are %d candidates, want 1", len(got))
	}

	r, err = startAlert(s, in, true, after, func(p *store.StartParams) { p.Limits = l })
	if err != nil {
		t.Fatal(err)
	}
	failRun(t, s, r)
	if got, _ := s.ListAutoCandidates(ctx, after.Add(time.Hour), l, 3*time.Hour); len(got) != 0 {
		t.Fatalf("after %d automatic runs there are %d candidates, want 0", l.MaxPerIncident, len(got))
	}
}

func TestADiagnosedAlertIsNotDiagnosedAgainAutomaticallyAndNeverOnANewHead(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	in := alertIncident(t, s, "a/1", true)
	r, err := startAlert(s, in, true, time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	finishRun(t, s, r.ID, run.Outcome{ExitCode: 0})
	if err := s.CompleteDiagnosis(ctx, r.ID, json.RawMessage(`{"summary":"s"}`), store.NewActivity{Kind: store.KindDiagnosisFinished, Summary: "done"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetIncident(ctx, in.ID)
	if got.State != store.IncDiagnosed {
		t.Fatalf("state = %s, want diagnosed", got.State)
	}
	later := time.Now().Add(time.Hour)
	if c, err := s.ListAutoCandidates(ctx, later, store.DefaultLimits(), 2*time.Hour); err != nil || len(c) != 0 {
		t.Fatalf("candidates = %+v, %v, want none", c, err)
	}
	// A recurrence with another head must not make it a candidate either.
	if err := s.RecordRecurrence(ctx, in.ID, "firing", "other-head", "", entry(store.KindIncidentRecurred, 0)); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.ListAutoCandidates(ctx, later, store.DefaultLimits(), 2*time.Hour); len(c) != 0 {
		t.Fatalf("candidates after a changed head = %+v, want none", c)
	}
}

func TestGitHubCandidatesAreUnchanged(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	off, err := s.AddRepo(ctx, store.ConnectionID, "octo/off", "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoEnabled(ctx, off.ID, false); err != nil {
		t.Fatal(err)
	}
	on := open(t, s, repo, "pr:1", "go", "aaa", "failure")
	open(t, s, off, "pr:2", "go", "aaa", "failure")
	alert := alertIncident(t, s, "a/1", true)

	// A tiny freshness window does not affect GitHub incidents: they are not judged by last_seen.
	got, err := s.ListAutoCandidates(ctx, time.Now().Add(24*time.Hour), store.DefaultLimits(), time.Second)
	if err != nil || len(got) != 1 || got[0].ID != on.ID {
		t.Fatalf("candidates = %+v, %v, want only the incident of the enabled repo (not the disabled repo's, not the stale alert %d)", got, err, alert.ID)
	}
	// The alert is not affected by repositories.
	got, err = s.ListAutoCandidates(ctx, time.Now(), store.DefaultLimits(), fresh)
	if err != nil || len(got) != 2 {
		t.Fatalf("candidates = %+v, %v, want the GitHub incident and the alert", got, err)
	}
}

func TestStartDiagnosisOfAnAlertStillObeysTheLimitsAndOneRunAtATime(t *testing.T) {
	s := openStore(t)
	a := alertIncident(t, s, "a/1", true)
	b := alertIncident(t, s, "a/2", true)
	if _, err := startAlert(s, a, true, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := startAlert(s, b, true, time.Now(), nil); !errors.Is(err, store.ErrBusy) {
		t.Fatalf("a second start while a run is active: %v, want ErrBusy", err)
	}

	s2 := openStore(t)
	manual := alertIncident(t, s2, "a/3", false)
	_, err := startAlert(s2, manual, true, time.Now(), nil)
	var limit *store.LimitError
	if !errors.As(err, &limit) {
		t.Fatalf("an automatic start of an incident with auto_diagnose = 0: %v, want a LimitError", err)
	}
}
