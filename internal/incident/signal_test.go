package incident_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/store"
)

func (v *env) observeSignal(t *testing.T, o incident.Observation) bool {
	t.Helper()
	opened, err := v.e.ObserveSignal(context.Background(), o)
	if err != nil {
		t.Fatalf("ObserveSignal(%+v): %v", o, err)
	}
	return opened
}

func (v *env) byKey(t *testing.T, source, key string) store.Incident {
	t.Helper()
	list, err := v.st.ListUnresolvedBySource(context.Background(), source)
	if err == nil {
		for _, in := range list {
			if in.Key == key {
				return in
			}
		}
	}
	all, err := v.st.ListIncidents(context.Background(), store.IncidentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range all {
		if in.Source == source && in.Key == key {
			return in
		}
	}
	t.Fatalf("no incident %s %s", source, key)
	return store.Incident{}
}

func TestAnAlertOpensAnIncidentOnceAndTouchesItAfterwards(t *testing.T) {
	v := newEnv(t)
	o := incident.Observation{
		Source: store.SourceAlertmanager, Key: "A/1", Title: "t", Severity: "critical", AutoDiagnose: true,
		Details: json.RawMessage(`{"labels":{"a":"b"}}`), Class: incident.Bad, Conclusion: "firing",
	}
	if !v.observeSignal(t, o) {
		t.Fatal("the first observation did not report an opening")
	}
	in := v.byKey(t, store.SourceAlertmanager, "A/1")
	if !in.AutoDiagnose || in.Severity != "critical" || string(in.Details) != `{"labels":{"a":"b"}}` {
		t.Fatalf("incident = %+v", in)
	}
	if v.observeSignal(t, o) {
		t.Fatal("the second observation reported an opening")
	}
	list := v.incidents(t, "active")
	if len(list) != 1 || list[0].Occurrences != 1 {
		t.Fatalf("incidents = %+v", list)
	}
	opened := 0
	for _, k := range v.kinds(t) {
		if k == store.KindIncidentOpened {
			opened++
		}
	}
	if opened != 1 {
		t.Fatalf("%d incident_opened entries, want 1", opened)
	}
}

func TestATouchUpdatesTheHostAndTheDetailsButNotTheAutoDecision(t *testing.T) {
	v := newEnv(t)
	o := incident.Observation{
		Source: store.SourceAlertmanager, Key: "A/1", Title: "t", Severity: "critical", AutoDiagnose: true,
		Details: json.RawMessage(`{"labels":{"a":"b"}}`), Class: incident.Bad, Conclusion: "firing",
	}
	v.observeSignal(t, o)

	h, err := v.st.CreateHost(context.Background(), store.HostFields{Name: "nas", Address: "10.0.0.5", Port: 22, Account: "root", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	o.HostID, o.AutoDiagnose, o.Details = h.ID, false, json.RawMessage(`{"labels":{"a":"c"}}`)
	if v.observeSignal(t, o) {
		t.Fatal("a touch reported an opening")
	}
	in := v.byKey(t, store.SourceAlertmanager, "A/1")
	if in.HostID != h.ID || in.HostName != "nas" || string(in.Details) != `{"labels":{"a":"c"}}` || !in.AutoDiagnose {
		t.Fatalf("incident = %+v", in)
	}

	o.HostID = 0
	v.observeSignal(t, o)
	if in := v.byKey(t, store.SourceAlertmanager, "A/1"); in.HostID != 0 || in.HostName != "" {
		t.Fatalf("host not cleared: %+v", in)
	}
}

func TestAnIgnoredIncidentStaysIgnoredWhenItsAlertKeepsFiring(t *testing.T) {
	v := newEnv(t)
	o := alert(incident.Bad, "A/1", "t")
	v.observeSignal(t, o)
	in := v.byKey(t, store.SourceAlertmanager, "A/1")
	if _, err := v.e.Ignore(context.Background(), in.ID); err != nil {
		t.Fatal(err)
	}
	if v.observeSignal(t, o) {
		t.Fatal("an ignored incident reported an opening")
	}
	if got := v.byKey(t, store.SourceAlertmanager, "A/1"); got.State != store.IncIgnored {
		t.Fatalf("state = %s, want ignored", got.State)
	}
}

func TestResolveAbsentResolvesOnlyWhatIsGoneOfThatSource(t *testing.T) {
	v := newEnv(t)
	for _, k := range []string{"A/1", "A/2", "A/3"} {
		v.observeSignal(t, alert(incident.Bad, k, "t "+k))
	}
	a3 := v.byKey(t, store.SourceAlertmanager, "A/3")
	if _, err := v.e.Ignore(context.Background(), a3.ID); err != nil {
		t.Fatal(err)
	}
	argo := incident.Observation{Source: store.SourceArgoCD, Key: "X", Title: "x", Class: incident.Bad, Conclusion: "degraded"}
	v.observeSignal(t, argo)
	v.observe(t, v.obs(incident.Bad, "pr:7", "build", "abc", "failure"))

	n, err := v.e.ResolveAbsent(context.Background(), store.SourceAlertmanager, map[string]bool{"A/1": true})
	if err != nil || n != 2 {
		t.Fatalf("ResolveAbsent = %d, %v; want 2", n, err)
	}
	for _, k := range []string{"A/2", "A/3"} {
		in := v.byKey(t, store.SourceAlertmanager, k)
		if in.State != store.IncResolved || in.ResolvedReason != incident.ReasonCleared {
			t.Fatalf("%s = %s (%s), want resolved/cleared", k, in.State, in.ResolvedReason)
		}
	}
	if in := v.byKey(t, store.SourceAlertmanager, "A/1"); in.State != store.IncOpen {
		t.Fatalf("A/1 = %s, want open", in.State)
	}
	if in := v.byKey(t, store.SourceArgoCD, "X"); in.State != store.IncOpen {
		t.Fatalf("argocd = %s, want open", in.State)
	}
	if got := len(v.incidents(t, "active")); got != 3 { // A/1, X, the GitHub incident
		t.Fatalf("%d active incidents, want 3", got)
	}
	n, err = v.e.ResolveAbsent(context.Background(), store.SourceAlertmanager, map[string]bool{"A/1": true})
	if err != nil || n != 0 {
		t.Fatalf("second ResolveAbsent = %d, %v; want 0", n, err)
	}
}

func TestResolveAbsentWithNothingPresentResolvesEveryIncidentOfTheSource(t *testing.T) {
	v := newEnv(t)
	v.observeSignal(t, alert(incident.Bad, "A/1", "one"))
	v.observeSignal(t, alert(incident.Bad, "A/2", "two"))
	n, err := v.e.ResolveAbsent(context.Background(), store.SourceAlertmanager, map[string]bool{})
	if err != nil || n != 2 {
		t.Fatalf("ResolveAbsent = %d, %v; want 2", n, err)
	}
	if got := len(v.incidents(t, "active")); got != 0 {
		t.Fatalf("%d active incidents, want 0", got)
	}
	resolved := 0
	for _, k := range v.kinds(t) {
		if k == store.KindIncidentResolved {
			resolved++
		}
	}
	if resolved != 2 {
		t.Fatalf("%d incident_resolved entries, want 2", resolved)
	}
}

func TestObserveStillWorksForGitHub(t *testing.T) {
	v := newEnv(t)
	o := v.obs(incident.Bad, "pr:7", "build", "abc", "failure")
	v.observe(t, o)
	v.observe(t, o)
	list := v.incidents(t, "active")
	if len(list) != 1 || list[0].Occurrences != 1 || list[0].Source != store.SourceGitHub {
		t.Fatalf("incidents = %+v", list)
	}
	opened, err := v.e.ObserveSignal(context.Background(), v.obs(incident.Bad, "pr:8", "build", "abc", "failure"))
	if err != nil || !opened {
		t.Fatalf("ObserveSignal on a new GitHub check = %v, %v; want true", opened, err)
	}
}
