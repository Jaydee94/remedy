package incident_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/store"
)

// alert is the observation of a firing alert: no repository, no commit.
func alert(class incident.Class, key, title string) incident.Observation {
	return incident.Observation{
		Source: store.SourceAlertmanager, Key: key, Title: title, Severity: "critical", AutoDiagnose: true, Class: class, Conclusion: "firing",
		Details: json.RawMessage(`{"labels":{"namespace":"demo"}}`), URL: "http://alertmanager.example/#/alerts",
	}
}

func (v *env) history(t *testing.T, incidentID int64) []string {
	t.Helper()
	log, err := v.st.ListActivity(context.Background(), store.ActivityQuery{IncidentID: incidentID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for i := len(log) - 1; i >= 0; i-- { // oldest first
		out = append(out, log[i].Kind+": "+log[i].Summary)
	}
	return out
}

func TestAnAlertOpensAnIncidentWithoutARepositoryAndSeeingItAgainChangesNothing(t *testing.T) {
	v := newEnv(t)
	o := alert(incident.Bad, "KubePodCrashLooping/abc", "KubePodCrashLooping demo/web")
	v.observe(t, o)

	list := v.incidents(t, "active")
	if len(list) != 1 {
		t.Fatalf("%d active incidents, want 1", len(list))
	}
	in := list[0]
	if in.Source != store.SourceAlertmanager || in.Key != o.Key || in.Title != o.Title || in.Severity != "critical" || !in.AutoDiagnose ||
		in.RepoID != 0 || in.State != store.IncOpen || in.Conclusion != "firing" || in.Occurrences != 1 || string(in.Details) != `{"labels":{"namespace":"demo"}}` {
		t.Fatalf("incident = %+v", in)
	}
	want := []string{"incident_opened: Incident opened: alert KubePodCrashLooping demo/web"}
	if got := v.history(t, in.ID); !slices.Equal(got, want) {
		t.Fatalf("history = %q, want %q", got, want)
	}

	// The machine-readable part names the source and the signal, not a check.
	log, _ := v.st.ListActivity(context.Background(), store.ActivityQuery{IncidentID: in.ID})
	var data map[string]string
	if err := json.Unmarshal(log[0].Data, &data); err != nil || data["source"] != "alertmanager" || data["key"] != o.Key || data["title"] != o.Title ||
		data["severity"] != "critical" || data["conclusion"] != "firing" || data["checkName"] != "" {
		t.Fatalf("payload = %s (%v)", log[0].Data, err)
	}

	// The same signal in the next cycle: nothing but last_seen changes, and nothing is logged.
	v.observe(t, o)
	v.observe(t, o)
	again := v.incidents(t, "active")
	if len(again) != 1 || again[0].ID != in.ID || again[0].Occurrences != 1 || again[0].State != store.IncOpen {
		t.Fatalf("after two more cycles: %+v", again)
	}
	if got := v.history(t, in.ID); len(got) != 1 {
		t.Fatalf("history after two more cycles = %q, want the opening only", got)
	}
}

func TestAnAlertThatStopsResolvesItsIncidentAndAFreshOneOpensAnother(t *testing.T) {
	v := newEnv(t)
	firing := alert(incident.Bad, "a/1", "HighLatency api")
	v.observe(t, firing)
	first := v.incidents(t, "active")[0]

	v.observe(t, alert(incident.Green, "a/1", "HighLatency api"))
	if got := v.incidents(t, "active"); len(got) != 0 {
		t.Fatalf("still active: %+v", got)
	}
	resolved, err := v.st.GetIncident(context.Background(), first.ID)
	if err != nil || resolved.State != store.IncResolved || resolved.ResolvedReason != incident.ReasonCleared || resolved.ResolvedAt == nil {
		t.Fatalf("resolved = %+v, %v", resolved, err)
	}
	want := []string{
		"incident_opened: Incident opened: alert HighLatency api",
		"incident_resolved: Incident resolved: alert HighLatency api is no longer reported",
	}
	if got := v.history(t, first.ID); !slices.Equal(got, want) {
		t.Fatalf("history = %q, want %q", got, want)
	}

	// A signal that is not there and was never there opens nothing, and a resolved key reopens as a new incident.
	v.observe(t, alert(incident.Green, "never/1", "never fired"))
	v.observe(t, firing)
	second := v.incidents(t, "active")
	if len(second) != 1 || second[0].ID == first.ID {
		t.Fatalf("after the alert fired again: %+v (the first incident was %d)", second, first.ID)
	}
	if all := v.incidents(t, "all"); len(all) != 2 {
		t.Fatalf("%d incidents in all, want 2", len(all))
	}
}

func TestAPendingSignalDoesNothingAndAnIgnoredIncidentStaysIgnored(t *testing.T) {
	v := newEnv(t)
	v.observe(t, alert(incident.Pending, "p/1", "pending"))
	if all := v.incidents(t, "all"); len(all) != 0 {
		t.Fatalf("a pending signal opened an incident: %+v", all)
	}

	v.observe(t, alert(incident.Bad, "i/1", "NodeDown n1"))
	in := v.incidents(t, "active")[0]
	ignored, err := v.e.Ignore(context.Background(), in.ID)
	if err != nil || ignored.State != store.IncIgnored {
		t.Fatalf("Ignore = %+v, %v", ignored, err)
	}
	v.observe(t, alert(incident.Bad, "i/1", "NodeDown n1"))
	got, _ := v.st.GetIncident(context.Background(), in.ID)
	if got.State != store.IncIgnored {
		t.Fatalf("a signal that is still there reopened the ignored incident: %+v", got)
	}
	want := []string{
		"incident_opened: Incident opened: alert NodeDown n1",
		"incident_ignored: Ignored the incident for alert NodeDown n1",
	}
	if h := v.history(t, in.ID); !slices.Equal(h, want) {
		t.Fatalf("history = %q, want %q", h, want)
	}
}

func TestTheSameKeyInTwoSourcesIsTwoIncidents(t *testing.T) {
	v := newEnv(t)
	a := alert(incident.Bad, "guestbook", "guestbook as an alert")
	g := a
	g.Source, g.Title = store.SourceArgoCD, "Argo CD guestbook is Degraded"
	v.observe(t, a)
	v.observe(t, g)
	list := v.incidents(t, "active")
	if len(list) != 2 {
		t.Fatalf("%d incidents, want 2", len(list))
	}
	v.observe(t, alert(incident.Green, "guestbook", "guestbook as an alert"))
	left := v.incidents(t, "active")
	if len(left) != 1 || left[0].Source != store.SourceArgoCD {
		t.Fatalf("after the alert stopped: %+v, want only the Argo CD incident", left)
	}
	if left[0].Title != "Argo CD guestbook is Degraded" {
		t.Fatalf("title = %q", left[0].Title)
	}
	if got := v.history(t, left[0].ID); len(got) != 1 || got[0] != "incident_opened: Incident opened: Argo CD application Argo CD guestbook is Degraded" {
		t.Fatalf("history = %q", got)
	}
}

func TestAGitHubObservationStillNeedsNoSourceOrKey(t *testing.T) {
	v := newEnv(t)
	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "aaa", "failure"))
	in := v.incidents(t, "active")[0]
	if in.Source != store.SourceGitHub || in.Key != store.GitHubKey(v.repo.ID, "pr:7", "go") || in.Title != "go" || !in.AutoDiagnose || in.RepoID != v.repo.ID {
		t.Fatalf("incident = %+v", in)
	}
	// A new failing commit is a recurrence of the same incident, and a cancelled one switches the responder off for it.
	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "bbb", "cancelled"))
	again := v.incidents(t, "active")
	if len(again) != 1 || again[0].Occurrences != 2 || again[0].AutoDiagnose || again[0].Conclusion != "cancelled" {
		t.Fatalf("after the recurrence: %+v", again)
	}
	want := []string{
		"incident_opened: go failed on PR #7 in octo/hello",
		"incident_recurred: go was cancelled again on PR #7 in octo/hello (commit bbb)",
	}
	if got := v.history(t, in.ID); !slices.Equal(got, want) {
		t.Fatalf("history = %q, want %q", got, want)
	}
}

func TestAnObservationOfAnotherSourceWithoutAKeyIsRefused(t *testing.T) {
	v := newEnv(t)
	o := alert(incident.Bad, "", "no key")
	if err := v.e.Observe(context.Background(), o); err == nil {
		t.Fatal("Observe accepted an alert without a key")
	}
	// A signal that is gone is refused as well: silently doing nothing would hide a source that forgot its keys.
	if err := v.e.Observe(context.Background(), alert(incident.Green, "", "no key")); err == nil {
		t.Fatal("Observe accepted a cleared alert without a key")
	}
	if all := v.incidents(t, "all"); len(all) != 0 {
		t.Fatalf("an incident without a key was stored: %+v", all)
	}
}
