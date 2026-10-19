package incident_test

import (
	"database/sql"
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
func (v *env) diagnosedAndResolved(t *testing.T, o incident.Observation, at time.Time) {
	t.Helper()
	v.observeSignal(t, o)
	v.setDiagnosedAt(t, at)
	gone := o
	gone.Class = incident.Green
	v.observeSignal(t, gone)
	if in := v.byKey(t, o.Source, o.Key); in.State != store.IncResolved {
		t.Fatalf("incident = %+v, want resolved", in)
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

func TestAReFiredAlertThatWasDiagnosedRecentlyIsNotDiagnosedAgainOnItsOwn(t *testing.T) {
	v := newEnv(t)
	o := alert(incident.Bad, "A/1", "HighLatency api")
	v.diagnosedAndResolved(t, o, time.Now().Add(-10*time.Minute))

	if !v.observeSignal(t, o) {
		t.Fatal("the re-fired alert did not open an incident")
	}
	in := v.activeByKey(t, store.SourceAlertmanager, "A/1")
	if in.AutoDiagnose {
		t.Fatalf("incident = %+v, want AutoDiagnose false", in)
	}
	var text string
	for _, line := range v.history(t, in.ID) {
		if strings.HasPrefix(line, store.KindIncidentOpened+": ") {
			text = line
		}
	}
	if !strings.Contains(text, "diagnosed recently") || !strings.Contains(text, "do not diagnose it again on my own") {
		t.Fatalf("incident_opened text = %q, want the reason", text)
	}
	if !strings.Contains(text, "Incident opened: alert HighLatency api") {
		t.Fatalf("incident_opened text = %q, lost the usual text", text)
	}
}

func TestAReFiredAlertWhoseDiagnosisIsOlderThanTheQuietTimeIsDiagnosedAgain(t *testing.T) {
	v := newEnv(t)
	o := alert(incident.Bad, "A/1", "HighLatency api")
	v.diagnosedAndResolved(t, o, time.Now().Add(-incident.ReopenQuiet-time.Minute))

	v.observeSignal(t, o)
	in := v.activeByKey(t, store.SourceAlertmanager, "A/1")
	if !in.AutoDiagnose {
		t.Fatalf("incident = %+v, want AutoDiagnose true", in)
	}
	for _, line := range v.history(t, in.ID) {
		if strings.Contains(line, "diagnosed recently") {
			t.Fatalf("history has the reason although nothing was suppressed: %q", line)
		}
	}
}

func TestAReFiredAlertThatWasNeverDiagnosedStaysAutomatic(t *testing.T) {
	v := newEnv(t)
	o := alert(incident.Bad, "A/1", "HighLatency api")
	v.observeSignal(t, o)
	gone := o
	gone.Class = incident.Green
	v.observeSignal(t, gone)

	v.observeSignal(t, o)
	if in := v.activeByKey(t, store.SourceAlertmanager, "A/1"); !in.AutoDiagnose {
		t.Fatalf("incident = %+v, want AutoDiagnose true", in)
	}
}

func TestAnObservationThatIsNotAutomaticStaysNotAutomatic(t *testing.T) {
	v := newEnv(t)
	o := alert(incident.Bad, "A/1", "HighLatency api")
	o.AutoDiagnose = false
	v.diagnosedAndResolved(t, o, time.Now().Add(-time.Minute))

	v.observeSignal(t, o)
	in := v.activeByKey(t, store.SourceAlertmanager, "A/1")
	if in.AutoDiagnose {
		t.Fatalf("incident = %+v, want AutoDiagnose false", in)
	}
	for _, line := range v.history(t, in.ID) {
		if strings.Contains(line, "diagnosed recently") {
			t.Fatalf("history has the reason for an observation that was not automatic anyway: %q", line)
		}
	}
}

func TestAGitHubIncidentThatRecursAfterADiagnosisIsNotAffected(t *testing.T) {
	v := newEnv(t)
	bad := v.obs(incident.Bad, "pr:7", "build", "abc", "failure")
	bad.AutoDiagnose = true // so that the source check, not the missing flag, is what keeps the rule away from GitHub
	v.observeSignal(t, bad)
	v.setDiagnosedAt(t, time.Now().Add(-time.Minute))
	green := v.obs(incident.Green, "pr:7", "build", "abc", "success")
	v.observeSignal(t, green)

	v.observeSignal(t, bad)
	list := v.incidents(t, "active")
	if len(list) != 1 || !list[0].AutoDiagnose {
		t.Fatalf("incidents = %+v, want one with AutoDiagnose true", list)
	}
	for _, line := range v.history(t, list[0].ID) {
		if strings.Contains(line, "diagnosed recently") {
			t.Fatalf("history has the reason for a GitHub incident: %q", line)
		}
	}
}
