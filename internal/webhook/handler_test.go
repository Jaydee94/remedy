package webhook_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/signals"
	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/internal/testutil"
	"github.com/Jaydee94/remedy/internal/webhook"
)

type hEnv struct {
	t      *testing.T
	st     *store.Store
	h      *webhook.Handler
	srv    *httptest.Server
	ep     store.WebhookEndpoint
	token  string
	clock  time.Time
	limits *webhook.Limits
	dsn    string
}

func newHEnv(t *testing.T, mod func(*store.WebhookFields)) *hEnv {
	t.Helper()
	st, dsn := testutil.StoreDSN(t)
	f := store.WebhookFields{Enabled: true, ResolveAfter: 24 * time.Hour, RatePerMinute: 6000, NewPerHour: 30}
	if mod != nil {
		mod(&f)
	}
	ep, token, err := st.CreateWebhook(context.Background(), "grafana", f)
	if err != nil {
		t.Fatal(err)
	}
	e := &hEnv{t: t, st: st, ep: ep, token: token, clock: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC), limits: webhook.NewLimits(), dsn: dsn}
	e.h = &webhook.Handler{
		Store: st, Engine: &incident.Engine{Store: st}, Limits: e.limits,
		Auth:       &webhook.Authenticator{Store: st, Now: func() time.Time { return e.clock }},
		AutoMaxAge: func() time.Duration { return time.Hour },
		Hosts: func(ctx context.Context) ([]signals.HostRules, error) {
			entries, err := st.ListHosts(ctx)
			if err != nil {
				return nil, err
			}
			var out []signals.HostRules
			for _, h := range entries {
				out = append(out, signals.HostRules{ID: h.Host.ID, Name: h.Host.Name, Matchers: h.Host.Matchers})
			}
			return out, nil
		},
		Now: func() time.Time { return e.clock },
	}
	mux := http.NewServeMux()
	mux.Handle("POST /webhook/v1/{endpoint}", e.h)
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

func (e *hEnv) post(name, token, contentType, body string) (int, string, http.Header) {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/webhook/v1/"+name, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func (e *hEnv) send(body string) (int, string) {
	e.t.Helper()
	code, out, _ := e.post("grafana", e.token, "application/json", body)
	return code, out
}

func event(key, status string, extra string) string {
	title := `,"title":"Disk almost full"`
	if status == "resolved" {
		title = ""
	}
	return fmt.Sprintf(`{"version":1,"key":%q,"status":%q%s%s}`, key, status, title, extra)
}

func result(t *testing.T, code int, body string) string {
	t.Helper()
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	var r struct{ Result string }
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	return r.Result
}

func (e *hEnv) counts() (incidents, activity int) {
	e.t.Helper()
	list, err := e.st.ListIncidents(context.Background(), store.IncidentFilter{State: "all"})
	if err != nil {
		e.t.Fatal(err)
	}
	log, err := e.st.ListActivity(context.Background(), store.ActivityQuery{Limit: 1000})
	if err != nil {
		e.t.Fatal(err)
	}
	return len(list), len(log)
}

func TestFiringOpensAnIncidentWithTheHostOfItsLabels(t *testing.T) {
	e := newHEnv(t, nil)
	host, err := e.st.CreateHost(context.Background(), store.HostFields{Name: "nas", Address: "10.0.0.2", Port: 22, Account: "remedy", Enabled: true, Matchers: `instance="nas"`})
	if err != nil {
		t.Fatal(err)
	}
	code, body := e.send(event("disk-full/nas", "firing", `,"severity":"warning","labels":{"instance":"nas"},"annotations":{"summary":"90%"}`))
	if got := result(t, code, body); got != "opened" {
		t.Fatalf("result = %q", got)
	}
	in, err := e.st.FindActiveIncidentByKey(context.Background(), store.SourceWebhook, "grafana/disk-full/nas")
	if err != nil {
		t.Fatal(err)
	}
	if in.Title != "Disk almost full" || in.Severity != "warning" || in.HostID != host.ID || in.RepoID != 0 || in.AutoDiagnose || in.State != store.IncOpen {
		t.Fatalf("incident = %+v", in)
	}
}

func TestARepeatIsATouchAndWritesNoActivity(t *testing.T) {
	e := newHEnv(t, nil)
	body := event("k", "firing", "")
	if code, out := e.send(body); result(t, code, out) != "opened" {
		t.Fatal("the first event did not open an incident")
	}
	_, a1 := e.counts()
	for range 5 {
		code, out := e.send(body)
		if got := result(t, code, out); got != "updated" {
			t.Fatalf("result = %q, want updated", got)
		}
	}
	n, a2 := e.counts()
	if n != 1 || a2 != a1 {
		t.Fatalf("%d incidents and %d activity entries after repeats, want 1 and %d", n, a2, a1)
	}
	in, _ := e.st.FindActiveIncidentByKey(context.Background(), store.SourceWebhook, "grafana/k")
	if in.Occurrences != 1 {
		t.Errorf("occurrences = %d", in.Occurrences)
	}
}

func TestResolvedClosesAndAReturnReopens(t *testing.T) {
	e := newHEnv(t, nil)
	e.send(event("k", "firing", ""))
	code, out := e.send(event("k", "resolved", ""))
	if got := result(t, code, out); got != "resolved" {
		t.Fatalf("result = %q", got)
	}
	if _, err := e.st.FindActiveIncidentByKey(context.Background(), store.SourceWebhook, "grafana/k"); err == nil {
		t.Fatal("the incident is still active")
	}
	_, a := e.counts()
	// resolved for an already resolved key, and for one never seen: 200, nothing written
	for _, key := range []string{"k", "never"} {
		code, out := e.send(event(key, "resolved", ""))
		if got := result(t, code, out); got != "ignored" {
			t.Fatalf("resolved for %s: result = %q, want ignored", key, got)
		}
	}
	if _, a2 := e.counts(); a2 != a {
		t.Fatalf("a no-op resolve wrote %d activity entries", a2-a)
	}
	code, out = e.send(event("k", "firing", ""))
	if got := result(t, code, out); got != "opened" {
		t.Fatalf("firing after resolve: result = %q", got)
	}
	log, _ := e.st.ListActivity(context.Background(), store.ActivityQuery{Limit: 10})
	if log[0].Kind != store.KindIncidentReopened {
		t.Fatalf("last activity = %s, want a reopen", log[0].Kind)
	}
}

func TestAnIgnoredIncidentStaysIgnored(t *testing.T) {
	e := newHEnv(t, nil)
	e.send(event("k", "firing", ""))
	in, _ := e.st.FindActiveIncidentByKey(context.Background(), store.SourceWebhook, "grafana/k")
	if _, err := (&incident.Engine{Store: e.st}).Ignore(context.Background(), in.ID); err != nil {
		t.Fatal(err)
	}
	code, out := e.send(event("k", "firing", ""))
	if got := result(t, code, out); got != "ignored" {
		t.Fatalf("result = %q, want ignored", got)
	}
	got, _ := e.st.FindActiveIncidentByKey(context.Background(), store.SourceWebhook, "grafana/k")
	if got.State != store.IncIgnored {
		t.Fatalf("state = %s", got.State)
	}
}

func TestAutomaticDiagnosisNeedsTheEndpointTheSeverityAndAYoungEvent(t *testing.T) {
	young := e0().Format(time.RFC3339)
	old := e0().Add(-3 * time.Hour).Format(time.RFC3339)
	for name, c := range map[string]struct {
		auto  bool
		extra string
		want  bool
	}{
		"endpoint off":       {false, `,"severity":"critical"`, false},
		"critical":           {true, `,"severity":"critical"`, true},
		"warning":            {true, `,"severity":"warning","startsAt":"` + young + `"`, true},
		"info":               {true, `,"severity":"info"`, false},
		"none":               {true, ``, false},
		"critical but stale": {true, `,"severity":"critical","startsAt":"` + old + `"`, false},
	} {
		e := newHEnv(t, func(f *store.WebhookFields) { f.AutoDiagnose = c.auto })
		e.send(event("k", "firing", c.extra))
		in, _ := e.st.FindActiveIncidentByKey(context.Background(), store.SourceWebhook, "grafana/k")
		if in.AutoDiagnose != c.want {
			t.Errorf("%s: AutoDiagnose = %v, want %v", name, in.AutoDiagnose, c.want)
		}
	}
}

func e0() time.Time { return time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC) }

func TestTheNewIncidentLimitDoesNotBlockAnUpdateOrAResolve(t *testing.T) {
	e := newHEnv(t, func(f *store.WebhookFields) { f.NewPerHour = 3 })
	for i := range 3 {
		if code, out := e.send(event(fmt.Sprintf("k%d", i), "firing", "")); code != http.StatusOK {
			t.Fatalf("new incident %d: %d %s", i, code, out)
		}
	}
	code, out, h := e.post("grafana", e.token, "application/json", event("k3", "firing", ""))
	if code != http.StatusTooManyRequests || h.Get("Retry-After") == "" {
		t.Fatalf("the fourth new incident: %d %s, Retry-After %q", code, out, h.Get("Retry-After"))
	}
	if code, out := e.send(event("k0", "firing", "")); code != http.StatusOK {
		t.Errorf("an update of a known key was refused: %d %s", code, out)
	}
	if code, out := e.send(event("k1", "resolved", "")); code != http.StatusOK || result(t, code, out) != "resolved" {
		t.Errorf("a resolve was refused: %d %s", code, out)
	}
	if n, _ := e.counts(); n != 3 {
		t.Errorf("%d incidents, want 3", n)
	}
}

func TestTheRateLimitAnswers429WithRetryAfter(t *testing.T) {
	e := newHEnv(t, func(f *store.WebhookFields) { f.RatePerMinute = 1 })
	e.send(event("a", "firing", ""))
	e.send(event("b", "firing", "")) // burst of two
	code, _, h := e.post("grafana", e.token, "application/json", event("c", "firing", ""))
	if code != http.StatusTooManyRequests || h.Get("Retry-After") == "" {
		t.Fatalf("status %d, Retry-After %q", code, h.Get("Retry-After"))
	}
}

func TestTooManyInFlightNeverTouchesTheDatabase(t *testing.T) {
	e := newHEnv(t, nil)
	e.h.Auth = &webhook.Authenticator{Store: failingAuthStore{t}}
	var releases []func()
	for range webhook.MaxInFlight {
		release, ok := e.limits.Acquire()
		if !ok {
			t.Fatal("could not fill the slots")
		}
		releases = append(releases, release)
	}
	code, _, h := e.post("grafana", e.token, "application/json", event("k", "firing", ""))
	if code != http.StatusServiceUnavailable || h.Get("Retry-After") == "" {
		t.Fatalf("status %d, Retry-After %q", code, h.Get("Retry-After"))
	}
	for _, r := range releases {
		r()
	}
}

type failingAuthStore struct{ t *testing.T }

func (f failingAuthStore) WebhookByName(context.Context, string) (store.WebhookAuth, error) {
	f.t.Error("the store was used although the listener was full")
	return store.WebhookAuth{}, store.ErrNotFound
}

func TestEveryRejectionWritesNothing(t *testing.T) {
	e := newHEnv(t, nil)
	n0, a0 := e.counts()
	huge := strings.Repeat("a", webhook.MaxBody)
	for name, c := range map[string]struct {
		ep, token, ct, body string
		want                int
	}{
		"wrong token":      {"grafana", "nope", "application/json", event("k", "firing", ""), 401},
		"no token":         {"grafana", "", "application/json", event("k", "firing", ""), 401},
		"unknown endpoint": {"nobody", e.token, "application/json", event("k", "firing", ""), 401},
		"wrong type":       {"grafana", e.token, "text/plain", event("k", "firing", ""), 415},
		"no type":          {"grafana", e.token, "", event("k", "firing", ""), 415},
		"bad field":        {"grafana", e.token, "application/json", `{"version":1,"key":"k","status":"firing","title":5}`, 400},
		"too large":        {"grafana", e.token, "application/json", `{"version":1,"key":"k","status":"firing","title":"` + huge + `"}`, 413},
		"two values":       {"grafana", e.token, "application/json", event("k", "firing", "") + `{}`, 400},
	} {
		code, out, _ := e.post(c.ep, c.token, c.ct, c.body)
		if code != c.want {
			t.Errorf("%s: status %d (%s), want %d", name, code, out, c.want)
		}
		if strings.Contains(out, e.token) || strings.Contains(out, "Disk almost full") {
			t.Errorf("%s: the answer repeats a secret or the submitted title: %s", name, out)
		}
	}
	if n, a := e.counts(); n != n0 || a != a0 {
		t.Errorf("rejections wrote rows: incidents %d -> %d, activity %d -> %d", n0, n, a0, a)
	}
}

func TestAcceptsAJSONContentTypeWithACharsetAndTheBadFieldIsNamed(t *testing.T) {
	e := newHEnv(t, nil)
	if code, out, _ := e.post("grafana", e.token, "application/json; charset=utf-8", event("k", "firing", "")); code != http.StatusOK {
		t.Errorf("charset: %d %s", code, out)
	}
	code, out := e.send(`{"version":1,"key":"k","status":"firing","title":"t","severity":"high"}`)
	if code != http.StatusBadRequest || !strings.Contains(out, `"field":"severity"`) {
		t.Errorf("%d %s, want 400 naming severity", code, out)
	}
}

func TestOneEndpointsFloodLeavesAnotherUntouched(t *testing.T) {
	e := newHEnv(t, func(f *store.WebhookFields) { f.RatePerMinute = 1 })
	other, otherToken, err := e.st.CreateWebhook(context.Background(), "uptime", store.WebhookFields{Enabled: true, ResolveAfter: time.Hour, RatePerMinute: 60, NewPerHour: 30})
	if err != nil {
		t.Fatal(err)
	}
	_ = other
	for range 5 {
		e.send(event("k", "firing", ""))
	}
	if code, out, _ := e.post("uptime", otherToken, "application/json", event("u", "firing", "")); code != http.StatusOK {
		t.Fatalf("the other endpoint: %d %s", code, out)
	}
}

// An ignored incident whose sender keeps firing is still reported: its last_seen must move, or the sweeper would resolve it as
// "not reported", and the next firing would reopen it and write activity again and again.
func TestAnIgnoredIncidentThatKeepsFiringIsNotSweptAndWritesNothing(t *testing.T) {
	e := newHEnv(t, func(f *store.WebhookFields) { f.ResolveAfter = 5 * time.Minute })
	e.send(event("k", "firing", ""))
	in, _ := e.st.FindActiveIncidentByKey(context.Background(), store.SourceWebhook, "grafana/k")
	if _, err := (&incident.Engine{Store: e.st}).Ignore(context.Background(), in.ID); err != nil {
		t.Fatal(err)
	}
	testutil.SQL(t, e.dsn, `UPDATE incidents SET last_seen = ? WHERE id = ?`, testutil.TS(time.Now().Add(-10*time.Minute)), in.ID)
	_, a0 := e.counts()
	if code, out := e.send(event("k", "firing", "")); result(t, code, out) != "ignored" {
		t.Fatalf("firing on an ignored incident: %d %s", code, out)
	}
	sw := &webhook.Sweeper{Store: e.st}
	if n, err := sw.SweepOnce(context.Background()); err != nil || n != 0 {
		t.Fatalf("SweepOnce = %d, %v: an ignored incident that is still reported was resolved", n, err)
	}
	got, _ := e.st.GetIncident(context.Background(), in.ID)
	if got.State != store.IncIgnored || got.Occurrences != 1 {
		t.Fatalf("incident = %s, %d occurrences", got.State, got.Occurrences)
	}
	if _, a1 := e.counts(); a1 != a0 {
		t.Fatalf("the repeat wrote %d activity entries", a1-a0)
	}
}
