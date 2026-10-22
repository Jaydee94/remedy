package incident_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/store"
)

// setDiagnosedAt rewrites diagnosed_at of every incident through a second connection with a busy timeout.
func (v *env) setDiagnosedAt(t *testing.T, at time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+v.path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE incidents SET diagnosed_at = ?`, at.UTC().Format("2006-01-02T15:04:05.000000000Z")); err != nil {
		t.Fatal(err)
	}
}

// diagnosedAndResolved opens the alert, marks it diagnosed at the given time and lets it clear.
func (v *env) diagnosedAndResolved(t *testing.T, o incident.Observation, at time.Time) store.Incident {
	t.Helper()
	v.observeSignal(t, o)
	v.setDiagnosedAt(t, at)
	gone := o
	gone.Class = incident.Green
	v.observeSignal(t, gone)
	in := v.byKey(t, o.Source, o.Key)
	if in.State != store.IncResolved {
		t.Fatalf("incident = %+v, want resolved", in)
	}
	return in
}

// fireAndClear lets the alert fire and then go away in a complete answer, as the signals runner does.
func (v *env) fireAndClear(t *testing.T, o incident.Observation) {
	t.Helper()
	v.observeSignal(t, o)
	if _, err := v.e.ResolveAbsent(context.Background(), o.Source, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
}

func count(lines []string, prefix string) int {
	n := 0
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

func TestAReturningAlertReopensItsIncident(t *testing.T) {
	v := newEnv(t)
	o := alert(incident.Bad, "A/1", "HighLatency api")
	v.fireAndClear(t, o)
	first := v.byKey(t, o.Source, o.Key)

	again := o
	again.Title, again.Severity, again.Details = "HighLatency api-2", "warning", json.RawMessage(`{"labels":{"namespace":"prod"}}`)
	if !v.observeSignal(t, again) {
		t.Fatal("ObserveSignal said the reopen is not new; the runner's per-cycle cap would not count it")
	}
	all := v.incidents(t, "all")
	if len(all) != 1 {
		t.Fatalf("%d incidents, want the one reopened: %+v", len(all), all)
	}
	in, err := v.st.GetIncident(context.Background(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if in.State != store.IncOpen || in.Occurrences != 2 || in.ResolvedAt != nil || in.ResolvedReason != "" ||
		in.Title != "HighLatency api-2" || in.Severity != "warning" || string(in.Details) != `{"labels":{"namespace":"prod"}}` {
		t.Fatalf("reopened incident = %+v", in)
	}
	want := []string{
		"incident_opened: Incident opened: alert HighLatency api",
		"incident_resolved: Incident resolved: alert HighLatency api is no longer reported",
		"incident_reopened: Incident reopened: alert HighLatency api-2 fired again (2nd time)",
	}
	if got := v.history(t, in.ID); !slices.Equal(got, want) {
		t.Fatalf("history = %q, want %q", got, want)
	}
}

func TestAnAlertWithoutAResolvedIncidentOpensAsBeforeAndDoesNotReopenAnotherKey(t *testing.T) {
	v := newEnv(t)
	v.fireAndClear(t, alert(incident.Bad, "A/1", "HighLatency api"))
	if !v.observeSignal(t, alert(incident.Bad, "B/2", "NodeDown n1")) {
		t.Fatal("a new alert did not open")
	}
	b := v.byKey(t, store.SourceAlertmanager, "B/2")
	if b.Occurrences != 1 || b.State != store.IncOpen {
		t.Fatalf("new incident = %+v", b)
	}
	if h := v.history(t, b.ID); !slices.Equal(h, []string{"incident_opened: Incident opened: alert NodeDown n1"}) {
		t.Fatalf("history = %q", h)
	}
	if a := v.byKey(t, store.SourceAlertmanager, "A/1"); a.State != store.IncResolved {
		t.Fatalf("the other key's incident = %+v, want it left resolved", a)
	}
}

func TestAReopenedIncidentThatWasDiagnosedIsNotDiagnosedOnItsOwnWhateverTheAge(t *testing.T) {
	for _, age := range []time.Duration{10 * time.Minute, 3 * time.Hour, 30 * 24 * time.Hour} {
		v := newEnv(t)
		o := alert(incident.Bad, "A/1", "HighLatency api")
		v.diagnosedAndResolved(t, o, time.Now().Add(-age))

		v.observeSignal(t, o) // o.AutoDiagnose is true
		in := v.activeByKey(t, store.SourceAlertmanager, "A/1")
		if in.AutoDiagnose || in.Occurrences != 2 {
			t.Fatalf("diagnosed %s ago: incident = %+v, want AutoDiagnose false and occurrences 2", age, in)
		}
	}
}

func TestAReopenedIncidentThatWasNeverDiagnosedFollowsTheObservation(t *testing.T) {
	for _, auto := range []bool{true, false} {
		v := newEnv(t)
		o := alert(incident.Bad, "A/1", "HighLatency api")
		o.AutoDiagnose = !auto // the first firing says the opposite: the reopen takes the new observation's value
		v.fireAndClear(t, o)

		o.AutoDiagnose = auto
		v.observeSignal(t, o)
		if in := v.activeByKey(t, store.SourceAlertmanager, "A/1"); in.AutoDiagnose != auto || in.Occurrences != 2 {
			t.Fatalf("auto %v: incident = %+v", auto, in)
		}
	}
}

func TestAnIgnoredIncidentThatResolvedAndFiresAgainIsReopenedIgnoredAndLeftAlone(t *testing.T) {
	v := newEnv(t)
	o := alert(incident.Bad, "A/1", "HighLatency api")
	v.observeSignal(t, o)
	first := v.byKey(t, o.Source, o.Key)
	if _, err := v.e.Ignore(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := v.e.ResolveAbsent(context.Background(), o.Source, map[string]bool{}); err != nil {
		t.Fatal(err)
	}

	if !v.observeSignal(t, o) {
		t.Fatal("the reopen of an ignored incident was not reported as new")
	}
	in, _ := v.st.GetIncident(context.Background(), first.ID)
	if in.State != store.IncIgnored || in.Occurrences != 2 {
		t.Fatalf("reopened = %+v, want ignored with occurrences 2", in)
	}
	before := v.history(t, in.ID)
	lastSeen := in.LastSeen

	// Later observations while it fires do not touch it.
	if v.observeSignal(t, o) {
		t.Fatal("an observation of an ignored incident was reported as new")
	}
	after, _ := v.st.GetIncident(context.Background(), first.ID)
	if after.State != store.IncIgnored || after.Occurrences != 2 || !after.LastSeen.Equal(lastSeen) {
		t.Fatalf("after another observation = %+v (last seen was %s)", after, lastSeen)
	}
	if h := v.history(t, in.ID); !slices.Equal(h, before) {
		t.Fatalf("history changed: %q, then %q", before, h)
	}
	if n := count(before, store.KindIncidentReopened+": "); n != 1 {
		t.Fatalf("history = %q, want one reopen", before)
	}
}

func TestAFlappingAlertReopensTheSameIncidentEachTime(t *testing.T) {
	v := newEnv(t)
	o := alert(incident.Bad, "A/1", "HighLatency api")
	v.fireAndClear(t, o)
	v.fireAndClear(t, o)
	v.observeSignal(t, o)

	all := v.incidents(t, "all")
	if len(all) != 1 || all[0].Occurrences != 3 || all[0].State != store.IncOpen {
		t.Fatalf("incidents = %+v, want one open with occurrences 3", all)
	}
	want := []string{
		"incident_opened: Incident opened: alert HighLatency api",
		"incident_resolved: Incident resolved: alert HighLatency api is no longer reported",
		"incident_reopened: Incident reopened: alert HighLatency api fired again (2nd time)",
		"incident_resolved: Incident resolved: alert HighLatency api is no longer reported",
		"incident_reopened: Incident reopened: alert HighLatency api fired again (3rd time)",
	}
	if got := v.history(t, all[0].ID); !slices.Equal(got, want) {
		t.Fatalf("history = %q, want %q", got, want)
	}
}

func TestAnArgoCDApplicationWithoutASeverityReopensToo(t *testing.T) {
	v := newEnv(t)
	o := incident.Observation{Source: store.SourceArgoCD, Key: "argocd/web", Title: "web", Class: incident.Bad, Conclusion: "degraded"}
	v.fireAndClear(t, o)
	if !v.observeSignal(t, o) {
		t.Fatal("the reopen was not reported")
	}
	all := v.incidents(t, "all")
	if len(all) != 1 || all[0].Occurrences != 2 || all[0].Severity != "none" {
		t.Fatalf("incidents = %+v", all)
	}
	if h := v.history(t, all[0].ID); h[len(h)-1] != "incident_reopened: Incident reopened: Argo CD application web fired again (2nd time)" {
		t.Fatalf("history = %q", h)
	}
}

func TestAGitHubIncidentIsNeverReopened(t *testing.T) {
	v := newEnv(t)
	bad := v.obs(incident.Bad, "pr:7", "build", "abc", "failure")
	v.observeSignal(t, bad)
	v.setDiagnosedAt(t, time.Now().Add(-time.Minute))
	v.observeSignal(t, v.obs(incident.Green, "pr:7", "build", "abc", "success"))

	v.observeSignal(t, bad)
	all := v.incidents(t, "all")
	if len(all) != 2 {
		t.Fatalf("incidents = %+v, want the resolved one and a new one", all)
	}
	for _, in := range all {
		if in.Occurrences != 1 {
			t.Fatalf("incident = %+v, want occurrences 1", in)
		}
		if n := count(v.history(t, in.ID), store.KindIncidentReopened); n != 0 {
			t.Fatalf("a GitHub incident has a reopen entry: %q", v.history(t, in.ID))
		}
	}
	active := v.incidents(t, "active")
	if len(active) != 1 || !active[0].AutoDiagnose {
		t.Fatalf("active = %+v, want one with AutoDiagnose true", active)
	}
}

func (v *env) activeByKey(t *testing.T, source, key string) store.Incident {
	t.Helper()
	list, err := v.st.ListUnresolvedBySource(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range list {
		if in.Key == key {
			return in
		}
	}
	t.Fatalf("no unresolved incident %s %s", source, key)
	return store.Incident{}
}
