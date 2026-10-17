package responder_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/responder"
	"github.com/Jaydee94/remedy/internal/store"
)

// alertIncident is an incident of another source: no repository, no commit.
func (e *env) alertIncident() store.Incident { return e.incidentOf(store.SourceAlertmanager) }

func (e *env) incidentOf(source string) store.Incident {
	e.t.Helper()
	in, err := e.st.OpenIncident(context.Background(), store.NewIncident{
		Source: source, Key: "KubePodCrashLooping/abc", Title: "KubePodCrashLooping demo/web", Severity: "critical",
		AutoDiagnose: true, Conclusion: "firing",
	}, store.NewActivity{Kind: store.KindIncidentOpened, Summary: "opened"})
	if err != nil {
		e.t.Fatal(err)
	}
	return in
}

func TestAnIncidentOfAnUnsupportedSourceIsNotDiagnosed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	in := e.incidentOf(store.SourceArgoCD)

	_, err := e.r.Start(ctx, in.ID)
	if !errors.Is(err, responder.ErrSourceNotSupported) {
		t.Fatalf("Start of an Argo CD incident = %v, want ErrSourceNotSupported", err)
	}
	if busy, _ := e.st.HasActiveRun(ctx); busy {
		t.Fatal("a run was created for an incident the responder cannot handle")
	}
	if calls := e.src.callList(); len(calls) != 0 || len(e.tokens) != 0 {
		t.Fatalf("GitHub was asked for an incident of another source: %v (tokens used: %d)", calls, len(e.tokens))
	}
	if got := e.get(in); got.State != store.IncOpen {
		t.Fatalf("incident = %+v", got)
	}
}

// The automatic start leaves an incident of a source it does not handle alone and still starts the next one.
func TestTheAutomaticStartLeavesAnIncidentOfAnUnsupportedSourceAlone(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alert := e.incidentOf(store.SourceArgoCD) // the oldest, so it would be first
	red := e.incident("pr:20", "web", sha, "failure")

	e.r.AutoStart(ctx)

	if got := e.get(alert); got.State != store.IncOpen {
		t.Fatalf("the Argo CD incident = %+v", got)
	}
	if got := e.get(red); got.State != store.IncDiagnosing {
		t.Fatalf("the failing check = %+v, want it diagnosing", got)
	}
}

func TestTheJobLogOfAnIncidentOfAnotherSourceIsNotAskedFromGitHub(t *testing.T) {
	e := newEnv(t)
	in := e.alertIncident()

	log, note, err := e.r.JobLog(context.Background(), in.ID)
	if err != nil || log != "" || !strings.Contains(note, "does not come from GitHub") {
		t.Fatalf("JobLog = %q, %q, %v", log, note, err)
	}
	if calls := e.src.callList(); len(calls) != 0 {
		t.Fatalf("GitHub was asked: %v", calls)
	}
}
