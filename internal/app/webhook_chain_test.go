package app_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/diagnosis"
	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/runner"
	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/internal/testutil"
)

func post(h http.Handler, path string) int {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestNoRouteExistsWithoutTheAddress(t *testing.T) {
	a := appWithEnv(t, nil)
	if a.WebhookHandler != nil {
		t.Fatal("a webhook handler exists although REMEDY_WEBHOOK_ADDR is empty")
	}
	if code := post(a.Handler, "/webhook/v1/grafana"); code != http.StatusNotFound {
		t.Errorf("the single-port handler answers %d", code)
	}
	split := appWithEnv(t, map[string]string{"REMEDY_INTERNAL_ADDR": ":8081"})
	for name, h := range map[string]http.Handler{"public": split.Handler, "internal": split.InternalHandler} {
		if code := post(h, "/webhook/v1/grafana"); code != http.StatusNotFound {
			t.Errorf("%s answers %d without the webhook address", name, code)
		}
	}
}

func TestTheWebhookListenerIsTheOnlyOneThatServesTheRoute(t *testing.T) {
	a := appWithEnv(t, map[string]string{"REMEDY_INTERNAL_ADDR": ":8081", "REMEDY_WEBHOOK_ADDR": ":8082"})
	if a.WebhookHandler == nil {
		t.Fatal("no webhook handler although the address is set")
	}
	for name, h := range map[string]http.Handler{"public": a.Handler, "internal": a.InternalHandler} {
		if code := post(h, "/webhook/v1/grafana"); code != http.StatusNotFound {
			t.Errorf("%s answers %d, want 404", name, code)
		}
	}
	if code := post(a.WebhookHandler, "/webhook/v1/grafana"); code != http.StatusUnauthorized {
		t.Errorf("the webhook listener answers %d for an unknown endpoint, want 401", code)
	}
	for _, p := range []string{"/api/me", "/runner/v1/claim", "/mcp"} {
		if code := post(a.WebhookHandler, p); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
			t.Errorf("the webhook listener answers %d for %s", code, p)
		}
	}
}

// The whole chain for a webhook: an endpoint made through the admin API, a firing event, the automatic start of the sweeper's hook, a
// real runner loop with a fake CLI, the answer checked as an outage and stored, a resolved event that closes the incident, and the sweeper
// that closes one whose sender fell silent.
func TestAWebhookEventIsDiagnosedAndResolvedAndASilentOneExpires(t *testing.T) {
	s := newStackCfg(t, false, func(c *config.Server) { c.WebhookAddr = ":8082" })
	ctx := context.Background()
	hook := httptest.NewServer(s.app.WebhookHandler)
	t.Cleanup(hook.Close)

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

	code, body := s.admin(http.MethodPost, "/api/webhooks", `{"name":"grafana","autoDiagnose":true,"resolveAfterSeconds":300}`)
	if code != http.StatusCreated {
		t.Fatalf("create endpoint = %d %s", code, body)
	}
	var created struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.Token == "" {
		t.Fatalf("created = %s (%v)", body, err)
	}
	send := func(status, key, extra string) (int, string) {
		title := `,"title":"Disk almost full on nas"`
		if status == "resolved" {
			title = ""
		}
		req, _ := http.NewRequest(http.MethodPost, hook.URL+"/webhook/v1/grafana", strings.NewReader(`{"version":1,"key":"`+key+`","status":"`+status+`"`+title+extra+`}`))
		req.Header.Set("Authorization", "Bearer "+created.Token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	if code, out := send("firing", "disk-full/nas", `,"severity":"critical","labels":{"instance":"nas"}`); code != http.StatusOK || out != "{\"result\":\"opened\"}\n" {
		t.Fatalf("firing = %d %q", code, out)
	}
	in, err := s.st.FindActiveIncidentByKey(ctx, store.SourceWebhook, "grafana/disk-full/nas")
	if err != nil || !in.AutoDiagnose {
		t.Fatalf("incident = %+v, %v", in, err)
	}

	// Nothing here starts the diagnosis by hand: the sweeper's hook does, as it does in the server.
	s.app.Sweeper.AfterSweep(ctx)
	got := waitDiagnosed(t, s.st, in.ID)
	want, err := diagnosis.ParseOutage([]byte(goodOutage))
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Diagnosis) != string(want.JSON()) {
		t.Fatalf("diagnosis = %s, want %s", got.Diagnosis, want.JSON())
	}
	rn, err := s.st.GetRun(ctx, got.RunID)
	if err != nil || rn.Role != run.RoleResponder || !rn.MCP || !rn.Automatic || rn.Status != run.Succeeded {
		t.Fatalf("run = %+v, %v", rn, err)
	}
	if !strings.Contains(rn.Prompt, "\nSource: Webhook\n") {
		t.Errorf("the prompt does not say that the source is a webhook")
	}

	// The same event again changes nothing; a diagnosed incident is not diagnosed again on its own.
	if code, out := send("firing", "disk-full/nas", `,"severity":"critical"`); code != http.StatusOK || !strings.Contains(out, "updated") {
		t.Fatalf("repeat = %d %q", code, out)
	}
	s.app.Sweeper.AfterSweep(ctx)
	if runs := s.responderRuns(); len(runs) != 1 {
		t.Fatalf("responder runs = %d, want 1", len(runs))
	}

	// resolved closes it
	if code, out := send("resolved", "disk-full/nas", ""); code != http.StatusOK || !strings.Contains(out, "resolved") {
		t.Fatalf("resolved = %d %q", code, out)
	}
	if after, _ := s.st.GetIncident(ctx, in.ID); after.State != store.IncResolved {
		t.Fatalf("state after resolved = %s", after.State)
	}

	// A sender that stops: the sweeper closes it as expired, once the silence limit has passed.
	if code, out := send("firing", "backup/nas", ""); code != http.StatusOK || !strings.Contains(out, "opened") {
		t.Fatalf("second incident = %d %q", code, out)
	}
	quiet, _ := s.st.FindActiveIncidentByKey(ctx, store.SourceWebhook, "grafana/backup/nas")
	s.app.Sweeper.Now = func() time.Time { return time.Now().Add(10 * time.Minute) }
	if n, err := s.app.Sweeper.SweepOnce(ctx); err != nil || n != 1 {
		t.Fatalf("SweepOnce = %d, %v", n, err)
	}
	if after, _ := s.st.GetIncident(ctx, quiet.ID); after.State != store.IncResolved || after.ResolvedReason != "expired" {
		t.Fatalf("the silent incident = %s / %s", after.State, after.ResolvedReason)
	}
}
