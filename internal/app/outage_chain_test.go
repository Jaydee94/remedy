package app_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/alertmanager/alertmanagertest"
	"github.com/Jaydee94/remedy/internal/diagnosis"
	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/runner"
	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/internal/testutil"
)

const goodOutage = `{"summary":"NodeDown: the NAS does not answer.","cause":"The host ugreen-nas is unreachable; the exporter reports no scrape.","confidence":"medium","category":"host_problem","affected":["host/ugreen-nas"],"proposed_action":"Check the power and the network of the NAS.","evidence":"incident_get showed the alert and no cluster object belongs to it."}`

// waitDiagnosed polls the store until the incident is diagnosed.
func waitDiagnosed(t *testing.T, st *store.Store, id int64) store.Incident {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		in, err := st.GetIncident(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if in.State == store.IncDiagnosed {
			return in
		}
		time.Sleep(50 * time.Millisecond)
	}
	in, _ := st.GetIncident(context.Background(), id)
	t.Fatalf("timed out waiting for the diagnosis; incident = %+v", in)
	return in
}

// The whole chain for an alert: the fake Alertmanager, the host rule, the signal runner, the automatic start, a real runner loop with a
// fake CLI, the answer checked as an outage and stored. GitHub is not asked, and the stale alert waits for a click.
func TestAnAlertIsDiagnosedWithoutGitHubAndAnOldOneOnlyByAClick(t *testing.T) {
	s := newStack(t, false)
	ctx := context.Background()
	fake := alertmanagertest.New(t, alertmanagerToken)
	// There is no GitHub connection at all: Remedy must work with Alertmanager alone.
	if code, body := s.admin(http.MethodDelete, "/api/github/connection", ""); code != http.StatusNoContent && code != http.StatusOK {
		t.Fatalf("DELETE connection = %d %s", code, body)
	}
	githubBefore := len(s.gh.seen())

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	loop := &runner.Loop{
		Client:        &runner.Client{BaseURL: s.ts.URL, Token: runnerToken, HTTP: s.ts.Client()},
		Providers:     map[string]provider.Provider{"claude": provider.Claude{Binary: testutil.FakeClaudeStructured(t, goodOutage)}},
		WorkspaceRoot: t.TempDir(),
		Env:           os.Environ(),
		Log:           log,
		Backoff:       50 * time.Millisecond,
	}
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { loop.Run(loopCtx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	for key, value := range map[string]string{"alertmanager_url": fake.URL, "alertmanager_token": alertmanagerToken} {
		if code, body := s.admin(http.MethodPut, "/api/settings/"+key, `{"value":"`+value+`"}`); code != http.StatusOK {
			t.Fatalf("PUT %s = %d %s", key, code, body)
		}
	}
	code, body := s.admin(http.MethodPost, "/api/hosts", `{"name":"nas","address":"192.168.178.118"}`)
	if code != http.StatusCreated {
		t.Fatalf("POST /api/hosts = %d %s", code, body)
	}
	var host struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &host); err != nil || host.ID == 0 {
		t.Fatalf("host = %s (%v)", body, err)
	}
	patch, _ := json.Marshal(map[string]string{"matchers": `instance=~"ugreen-nas(:[0-9]+)?"`})
	if code, body := s.admin(http.MethodPatch, "/api/hosts/"+strconv.FormatInt(host.ID, 10), string(patch)); code != http.StatusOK {
		t.Fatalf("PATCH host = %d %s", code, body)
	}

	now := time.Now()
	fake.SetAlerts(
		alertmanagertest.Alert("aaaa1111", "NodeDown", map[string]string{"instance": "ugreen-nas", "severity": "critical"}, now.Add(-5*time.Minute)),
		alertmanagertest.Alert("bbbb2222", "DiskFilling", map[string]string{"severity": "warning"}, now.Add(-5*time.Hour)),
	)
	s.app.Signals.RunOnce(ctx)

	list, err := s.st.ListIncidents(ctx, store.IncidentFilter{State: "active"})
	if err != nil || len(list) != 2 {
		t.Fatalf("incidents = %+v, %v", list, err)
	}
	var young, old store.Incident
	for _, in := range list {
		if in.HostID != 0 {
			young = in
		} else {
			old = in
		}
	}
	if young.ID == 0 || old.ID == 0 || !young.AutoDiagnose || old.AutoDiagnose {
		t.Fatalf("young = %+v, old = %+v", young, old)
	}

	// The signal runner's own hook starts the diagnosis of the young alert only; nothing here calls AutoStart by hand.
	got := waitDiagnosed(t, s.st, young.ID)
	want, err := diagnosis.ParseOutage([]byte(goodOutage))
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Diagnosis) != string(want.JSON()) {
		t.Fatalf("diagnosis = %s, want %s", got.Diagnosis, want.JSON())
	}
	rn, err := s.st.GetRun(ctx, got.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if rn.Role != run.RoleResponder || !rn.MCP || rn.HostCount != 1 || !rn.Automatic || rn.Status != run.Succeeded || rn.HeadSHA != "" {
		t.Fatalf("run = %+v", rn)
	}
	if o, _ := s.st.GetIncident(ctx, old.ID); o.State != store.IncOpen {
		t.Fatalf("the old alert = %+v, want it untouched", o)
	}

	// No second run for the diagnosed alert, and none for the old one.
	s.app.Signals.RunOnce(ctx)
	if runs := s.responderRuns(); len(runs) != 1 {
		t.Fatalf("responder runs = %d, want 1", len(runs))
	}

	// A click starts the old one.
	if code, body := s.admin(http.MethodPost, "/api/incidents/"+strconv.FormatInt(old.ID, 10)+"/diagnose", ""); code != http.StatusAccepted {
		t.Fatalf("diagnose = %d %s", code, body)
	}
	manual := waitDiagnosed(t, s.st, old.ID)
	if r, _ := s.st.GetRun(ctx, manual.RunID); r.Automatic || r.HostCount != 0 || !r.MCP {
		t.Fatalf("the manual run = %+v", r)
	}

	if after := len(s.gh.seen()); after != githubBefore {
		t.Fatalf("GitHub saw %d requests during the alert runs: %v", after-githubBefore, s.gh.seen()[githubBefore:])
	}
}
