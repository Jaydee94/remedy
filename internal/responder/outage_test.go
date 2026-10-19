package responder_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/diagnosis"
	"github.com/Jaydee94/remedy/internal/responder"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

const goodOutage = `{"summary":"The pod web keeps crashing.","cause":"It runs out of memory: the limit is 128Mi.","confidence":"high","category":"resource_pressure","affected":["deployment/demo/web"],"proposed_action":"Raise the memory limit of deployment demo/web to 256Mi.","evidence":"cluster_describe showed OOMKilled."}`

const alertDetails = `{"labels":{"alertname":"A","namespace":"demo","pod":"web-1"},"annotations":{"summary":"s"},"startsAt":"2026-10-16T09:00:00Z","generatorURL":"http://p/g"}`

// openAlert opens an alert incident with details, a host and the choice of automatic diagnosis.
func (e *env) openAlert(key, title string, hostID int64, auto bool) store.Incident {
	e.t.Helper()
	in, err := e.st.OpenIncident(context.Background(), store.NewIncident{
		Source: store.SourceAlertmanager, Key: key, Title: title, Severity: "critical", AutoDiagnose: auto,
		Conclusion: "firing", Details: json.RawMessage(alertDetails), HostID: hostID,
	}, store.NewActivity{Kind: store.KindIncidentOpened, Summary: "opened"})
	if err != nil {
		e.t.Fatal(err)
	}
	return in
}

func (e *env) host(name string, enabled bool) store.Host {
	e.t.Helper()
	h, err := e.st.CreateHost(context.Background(), store.HostFields{Name: name, Address: "10.0.0.5", Port: 22, Account: "remedy", Enabled: enabled})
	if err != nil {
		e.t.Fatal(err)
	}
	return h
}

// setLastSeen rewrites last_seen through a second connection, as the age of an alert is not something the store lets a test set.
func (e *env) setLastSeen(in store.Incident, at time.Time) {
	e.t.Helper()
	db, err := sql.Open("sqlite", "file:"+e.path)
	if err != nil {
		e.t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE incidents SET last_seen = ? WHERE id = ?`, at.UTC().Format("2006-01-02T15:04:05.000000000Z"), in.ID); err != nil {
		e.t.Fatal(err)
	}
}

func TestAnAlertIncidentIsDiagnosedWithToolsTheClusterAndItsHost(t *testing.T) {
	e := newEnv(t)
	e.r.ClusterEnabled = func() bool { return true }
	h := e.host("nas", true)
	in := e.openAlert("A/1", "Pod web-1 crashes", h.ID, true)

	r, err := e.r.Start(context.Background(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !r.MCP || !r.Cluster || r.HostCount != 1 || r.Role != run.RoleResponder || r.HeadSHA != "" {
		t.Fatalf("run = %+v", r)
	}
	if !strings.Contains(r.Prompt, "Cluster tools: available") || !strings.Contains(r.Prompt, "Host tools: available") {
		t.Fatalf("the prompt does not say which tools there are:\n%s", r.Prompt)
	}
	if !strings.Contains(r.Prompt, "alert_title") || !strings.Contains(r.Prompt, "Pod web-1 crashes") {
		t.Fatalf("the title is not in an alert_title block:\n%s", r.Prompt)
	}
	if calls := e.src.callList(); len(calls) != 0 || len(e.tokens) != 0 {
		t.Fatalf("GitHub was used for an alert: %v (tokens %d)", calls, len(e.tokens))
	}
	if got := e.get(in); got.State != store.IncDiagnosing || got.RunID != r.ID {
		t.Fatalf("incident = %+v", got)
	}
}

func TestAnAlertDoesNotNeedAGitHubConnection(t *testing.T) {
	e := newEnv(t)
	if err := e.st.DeleteConnection(context.Background()); err != nil {
		t.Fatal(err)
	}
	in := e.openAlert("A/1", "Pod web-1 crashes", 0, true)
	if _, err := e.r.Start(context.Background(), in.ID); err != nil {
		t.Fatalf("Start without a GitHub connection = %v", err)
	}
}

func TestWithoutTheClusterOrAHostTheRunHasOnlyTheIncidentTools(t *testing.T) {
	e := newEnv(t)
	in := e.openAlert("A/1", "Pod web-1 crashes", 0, true)

	r, err := e.r.Start(context.Background(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !r.MCP || r.Cluster || r.HostCount != 0 {
		t.Fatalf("run = %+v", r)
	}
	if !strings.Contains(r.Prompt, "Cluster tools: not available") || !strings.Contains(r.Prompt, "Host tools: not available") {
		t.Fatalf("the prompt does not say that there are no more tools:\n%s", r.Prompt)
	}
}

// A host that was disabled after the alert was mapped to it gives the run none of its tools, and the prompt says so.
func TestADisabledHostGivesTheRunNoHostTools(t *testing.T) {
	e := newEnv(t)
	h := e.host("nas", false)
	in := e.openAlert("A/1", "Pod web-1 crashes", h.ID, true)

	r, err := e.r.Start(context.Background(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.HostCount != 0 || !strings.Contains(r.Prompt, "Host tools: not available") {
		t.Fatalf("run = %+v\n%s", r, r.Prompt)
	}
}

func TestAManualStartIgnoresTheLimitsAndAnAutomaticOneKeepsThem(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	in := e.openAlert("A/1", "Pod web-1 crashes", 0, false)

	e.r.AutoStart(ctx)
	if got := e.get(in); got.State != store.IncOpen {
		t.Fatalf("AutoStart picked an alert that is not automatic: %+v", got)
	}
	r, err := e.r.Start(ctx, in.ID)
	if err != nil || r.Automatic {
		t.Fatalf("Start = %+v, %v", r, err)
	}
}

func TestAutoStartPicksAFreshAlertAndSkipsAStaleOne(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	old := e.openAlert("A/old", "Old alert", 0, true)
	fresh := e.openAlert("A/fresh", "Fresh alert", 0, true)
	e.setLastSeen(old, e.now.Add(-2*time.Hour))
	e.setLastSeen(fresh, e.now.Add(-time.Minute))

	e.r.AutoStart(ctx)
	if got := e.get(fresh); got.State != store.IncDiagnosing {
		t.Fatalf("the fresh alert = %+v", got)
	}
	if got := e.get(old); got.State != store.IncOpen {
		t.Fatalf("the stale alert = %+v", got)
	}
}

func TestTheFreshnessOfAnAlertIsTheSettingOfTheResponder(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	old := e.openAlert("A/old", "Old alert", 0, true)
	fresh := e.openAlert("A/fresh", "Fresh alert", 0, true)
	e.setLastSeen(old, e.now.Add(-2*time.Hour))
	e.setLastSeen(fresh, e.now.Add(-time.Minute))
	e.r.AlertFresh = func() time.Duration { return 24 * time.Hour }

	e.r.AutoStart(ctx)
	if got := e.get(old); got.State != store.IncDiagnosing {
		t.Fatalf("the older alert = %+v, want it first", got)
	}
}

func TestAnArgoCDIncidentIsStillNotSupported(t *testing.T) {
	e := newEnv(t)
	in := e.incidentOf(store.SourceArgoCD)
	if _, err := e.r.Start(context.Background(), in.ID); !errors.Is(err, responder.ErrSourceNotSupported) {
		t.Fatalf("Start = %v, want ErrSourceNotSupported", err)
	}
}

func TestTheAnswerForAnAlertIsCheckedAsAnOutageAndStored(t *testing.T) {
	ok := run.Outcome{Output: json.RawMessage(goodOutage)}
	if got := responder.CheckOutcomeFor(store.SourceAlertmanager, ok); got.FailureReason != "" {
		t.Fatalf("a valid outage answer = %+v", got)
	}
	gh := run.Outcome{Output: json.RawMessage(goodDiagnosis)}
	if got := responder.CheckOutcomeFor(store.SourceAlertmanager, gh); got.FailureReason != run.ReasonInvalidOutput {
		t.Fatalf("a GitHub-shaped answer for an alert = %+v", got)
	}
	if got := responder.CheckOutcomeFor(store.SourceGitHub, ok); got.FailureReason != run.ReasonInvalidOutput {
		t.Fatalf("an outage answer for a GitHub incident = %+v", got)
	}
	if got := responder.CheckOutcomeFor(store.SourceGitHub, gh); got.FailureReason != "" {
		t.Fatalf("a valid GitHub answer = %+v", got)
	}
	if got := responder.CheckOutcome(gh); got.FailureReason != "" {
		t.Fatalf("CheckOutcome = %+v", got)
	}

	e := newEnv(t)
	in := e.openAlert("A/1", "Pod web-1 crashes", 0, true)
	r, err := e.r.Start(context.Background(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.finish(r, run.Outcome{ExitCode: 0, Output: json.RawMessage(goodOutage)})

	got := e.get(in)
	if got.State != store.IncDiagnosed {
		t.Fatalf("incident = %+v", got)
	}
	want, err := diagnosis.ParseOutage([]byte(goodOutage))
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Diagnosis) != string(want.JSON()) {
		t.Fatalf("diagnosis = %s, want %s", got.Diagnosis, want.JSON())
	}
	log, _ := e.st.ListActivity(context.Background(), store.ActivityQuery{IncidentID: in.ID})
	if len(log) == 0 || log[0].Summary != "Diagnosis of Pod web-1 crashes finished: The pod web keeps crashing." {
		t.Fatalf("activity = %+v", log)
	}
}

func TestAnInvalidOutageAnswerFailsTheDiagnosisAndReopensTheIncident(t *testing.T) {
	e := newEnv(t)
	in := e.openAlert("A/1", "Pod web-1 crashes", 0, true)
	r, err := e.r.Start(context.Background(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.finish(r, run.Outcome{ExitCode: 0, Output: json.RawMessage(goodDiagnosis)}) // the shape of a GitHub incident

	got := e.get(in)
	if got.State != store.IncOpen || len(got.Diagnosis) != 0 {
		t.Fatalf("incident = %+v", got)
	}
	log, _ := e.st.ListActivity(context.Background(), store.ActivityQuery{IncidentID: in.ID})
	if len(log) == 0 || log[0].Summary != "Diagnosis of Pod web-1 crashes failed: the answer was not a valid diagnosis" {
		t.Fatalf("activity = %+v", log)
	}
}

func TestCheckRunLooksUpTheSourceOfTheRunsIncident(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alert := e.openAlert("A/1", "Pod web-1 crashes", 0, true)
	ar, err := e.r.Start(ctx, alert.ID)
	if err != nil {
		t.Fatal(err)
	}
	// finish the alert's run so that the GitHub incident can start
	e.finish(ar, run.Outcome{ExitCode: 0, Output: json.RawMessage(goodOutage)})
	gh := e.incident("pr:20", "web", sha, "failure")
	gr, err := e.r.Start(ctx, gh.ID)
	if err != nil {
		t.Fatal(err)
	}

	outage := run.Outcome{Output: json.RawMessage(goodOutage)}
	if got := e.r.CheckRun(ctx, ar, outage); got.FailureReason != "" {
		t.Fatalf("the outage answer of an alert's run = %+v", got)
	}
	if got := e.r.CheckRun(ctx, gr, outage); got.FailureReason != run.ReasonInvalidOutput {
		t.Fatalf("the outage answer of a GitHub run = %+v", got)
	}
	none := gr
	none.IncidentID = nil
	if got := e.r.CheckRun(ctx, none, outage); got.FailureReason != run.ReasonInvalidOutput {
		t.Fatalf("a run without an incident must be judged as GitHub's: %+v", got)
	}
	if got := e.r.CheckRun(ctx, none, run.Outcome{Output: json.RawMessage(goodDiagnosis)}); got.FailureReason != "" {
		t.Fatalf("a GitHub answer of a run without an incident = %+v", got)
	}
}
