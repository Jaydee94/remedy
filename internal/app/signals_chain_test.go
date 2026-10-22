package app_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/alertmanager/alertmanagertest"
	"github.com/Jaydee94/remedy/internal/app"
	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
)

const alertmanagerToken = "tok-0123456789abcdef"

// adminSession logs in to a running app and returns a function that does admin requests with the session and the CSRF header.
func adminSession(t *testing.T, ts *httptest.Server) func(method, path, body string) (int, string) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	do := func(method, path, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("X-Remedy-CSRF", "1")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, _ := do(http.MethodPost, "/api/login", `{"password":"`+password+`"}`); code != http.StatusNoContent {
		t.Fatalf("login = %d", code)
	}
	return do
}

type alertIncident struct {
	ID           int64    `json:"id"`
	Title        string   `json:"title"`
	State        string   `json:"state"`
	HostID       int64    `json:"hostId"`
	HostName     string   `json:"hostName"`
	AlsoMatches  []string `json:"alsoMatches"`
	AutoDiagnose bool     `json:"autoDiagnose"`
}

func listIncidents(t *testing.T, do func(string, string, string) (int, string), query string) []alertIncident {
	t.Helper()
	code, body := do(http.MethodGet, "/api/incidents"+query, "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/incidents%s = %d %s", query, code, body)
	}
	var out []alertIncident
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return out
}

type signalState struct {
	Name, State, Error string
	Active             int
}

func signalStates(t *testing.T, do func(string, string, string) (int, string)) []signalState {
	t.Helper()
	code, body := do(http.MethodGet, "/api/signals", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/signals = %d %s", code, body)
	}
	var out struct {
		Sources []signalState `json:"sources"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return out.Sources
}

func TestAnAlertBecomesAnIncidentMappedToAHostAndResolvesWhenItIsGone(t *testing.T) {
	fake := alertmanagertest.New(t, alertmanagerToken)
	a := appWithEnv(t, nil)
	ts := httptest.NewServer(a.Handler)
	t.Cleanup(ts.Close)
	do := adminSession(t, ts)
	ctx := context.Background()

	// Before anything is set the source is off.
	if got := signalStates(t, do); len(got) != 1 || got[0].Name != "alertmanager" || got[0].State != "off" {
		t.Fatalf("before the address is set: %+v", got)
	}

	// 1. The address and the token are settings.
	if code, body := do(http.MethodPut, "/api/settings/alertmanager_url", `{"value":"`+fake.URL+`"}`); code != http.StatusOK {
		t.Fatalf("PUT alertmanager_url = %d %s", code, body)
	}
	if code, body := do(http.MethodPut, "/api/settings/alertmanager_token", `{"value":"`+alertmanagerToken+`"}`); code != http.StatusOK {
		t.Fatalf("PUT alertmanager_token = %d %s", code, body)
	}

	// 2. A host with a rule for the alerts of the NAS.
	code, body := do(http.MethodPost, "/api/hosts", `{"name":"nas","address":"192.168.178.118"}`)
	if code != http.StatusCreated {
		t.Fatalf("POST /api/hosts = %d %s", code, body)
	}
	var host struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &host); err != nil || host.ID == 0 {
		t.Fatalf("host = %s (%v)", body, err)
	}
	hostPath := "/api/hosts/" + strconv.FormatInt(host.ID, 10)
	patch, _ := json.Marshal(map[string]string{"matchers": `instance=~"ugreen-nas(:[0-9]+)?"`})
	if code, body := do(http.MethodPatch, hostPath, string(patch)); code != http.StatusOK {
		t.Fatalf("PATCH host = %d %s", code, body)
	}

	// 3. Three alerts: a young critical one of the NAS, an old warning without an instance, and the Watchdog.
	now := time.Now()
	fake.SetAlerts(
		alertmanagertest.Alert("aaaa1111", "NodeDown", map[string]string{"instance": "ugreen-nas", "severity": "critical"}, now.Add(-5*time.Minute)),
		alertmanagertest.Alert("bbbb2222", "DiskFilling", map[string]string{"severity": "warning"}, now.Add(-5*time.Hour)),
		alertmanagertest.Alert("cccc3333", "Watchdog", map[string]string{"severity": "none"}, now.Add(-24*time.Hour)),
	)

	// 4. One cycle.
	a.Signals.RunOnce(ctx)

	// 5. Two incidents, not the Watchdog.
	active := listIncidents(t, do, "?state=active")
	if len(active) != 2 {
		t.Fatalf("active incidents = %+v", active)
	}
	var nas, other *alertIncident
	for i := range active {
		if active[i].HostID != 0 {
			nas = &active[i]
		} else {
			other = &active[i]
		}
	}
	if nas == nil || other == nil {
		t.Fatalf("one incident with a host and one without expected: %+v", active)
	}
	if nas.HostID != host.ID || nas.HostName != "nas" || !nas.AutoDiagnose || !strings.Contains(nas.Title, "NodeDown") {
		t.Errorf("the NAS incident = %+v", *nas)
	}
	if other.HostName != "" || other.AutoDiagnose || !strings.Contains(other.Title, "DiskFilling") {
		t.Errorf("the other incident = %+v", *other)
	}
	for _, in := range active {
		if strings.Contains(in.Title, "Watchdog") {
			t.Errorf("the Watchdog became an incident: %+v", in)
		}
	}

	// 6. The state: ok, two signals (the Watchdog is skipped).
	if got := signalStates(t, do); len(got) != 1 || got[0].Name != "alertmanager" || got[0].State != "ok" || got[0].Active != 2 || got[0].Error != "" {
		t.Fatalf("after the first cycle: %+v", got)
	}

	// 7. What the fake saw: the token as a bearer token and the filters of the query.
	reqs := fake.Requests()
	if len(reqs) == 0 {
		t.Fatal("the fake saw no request")
	}
	for _, r := range reqs {
		if r.Auth != "Bearer "+alertmanagerToken {
			t.Errorf("Authorization = %q", r.Auth)
		}
		q := r.Query
		if !strings.Contains(q, "active=true") || !strings.Contains(q, "inhibited=false") || !strings.Contains(q, "silenced=false") {
			t.Errorf("query = %q", q)
		}
	}

	// 8. A second cycle on the same answer changes nothing.
	before := listIncidents(t, do, "?state=all")
	a.Signals.RunOnce(ctx)
	after := listIncidents(t, do, "?state=all")
	if len(before) != len(after) || len(listIncidents(t, do, "?state=active")) != 2 {
		t.Fatalf("a second cycle changed the incidents: %+v then %+v", before, after)
	}

	// A failed fetch keeps both incidents open and shows the error.
	fake.SetRaw(http.StatusInternalServerError, "x")
	a.Signals.RunOnce(ctx)
	if got := listIncidents(t, do, "?state=active"); len(got) != 2 {
		t.Fatalf("after a failed fetch: %+v", got)
	}
	if got := signalStates(t, do); len(got) != 1 || got[0].State != "error" || got[0].Error == "" {
		t.Fatalf("after a failed fetch: %+v", got)
	} else if strings.Contains(got[0].Error, alertmanagerToken) {
		t.Fatalf("the error text shows the token: %q", got[0].Error)
	}

	// An empty, complete answer resolves both.
	fake.SetAlerts()
	a.Signals.RunOnce(ctx)
	if got := listIncidents(t, do, "?state=active"); len(got) != 0 {
		t.Fatalf("after an empty answer: %+v", got)
	}
	resolved := listIncidents(t, do, "?state=resolved")
	if len(resolved) != 2 {
		t.Fatalf("resolved incidents = %+v", resolved)
	}
	if got := signalStates(t, do); len(got) != 1 || got[0].State != "ok" || got[0].Active != 0 {
		t.Fatalf("after the empty answer: %+v", got)
	}

	// 9. The token is nowhere a reader of the API can see it.
	for _, path := range []string{"/api/activity", "/api/settings", "/api/signals", "/api/incidents?state=all", hostPath} {
		code, body := do(http.MethodGet, path, "")
		if code != http.StatusOK {
			t.Errorf("GET %s = %d", path, code)
		}
		if strings.Contains(body, alertmanagerToken) {
			t.Errorf("GET %s shows the token", path)
		}
	}
}

// A CA bundle that exists but holds no certificate must not stop the app and must not leave a nil client behind: the source is
// off and the runner never fetches.
func TestAnUnreadableCABundleLeavesTheSourceOff(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, []byte("this is not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := alertmanagertest.New(t, alertmanagerToken)
	fake.SetAlerts(alertmanagertest.Alert("aaaa1111", "NodeDown", map[string]string{"severity": "critical"}, time.Now()))

	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	key, err := secret.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Server{
		AdminPassword: password, RunnerToken: runnerToken, MasterKey: key, GitHubAPIURL: "http://127.0.0.1:1", PollInterval: time.Minute,
		DiagnoseCooldown: 15 * time.Minute, DiagnoseMaxPerIncident: 3, DiagnoseMaxPerDay: 20, AlertmanagerCAFile: ca,
	}
	a := app.New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	ts := httptest.NewServer(a.Handler)
	t.Cleanup(ts.Close)
	do := adminSession(t, ts)

	if code, body := do(http.MethodPut, "/api/settings/alertmanager_url", `{"value":"`+fake.URL+`"}`); code != http.StatusOK {
		t.Fatalf("PUT alertmanager_url = %d %s", code, body)
	}
	a.Signals.RunOnce(context.Background())
	if got := signalStates(t, do); len(got) != 1 || got[0].State != "off" {
		t.Fatalf("states = %+v", got)
	}
	if len(fake.Requests()) != 0 {
		t.Errorf("the fake saw %d requests", len(fake.Requests()))
	}
	if got := listIncidents(t, do, "?state=all"); len(got) != 0 {
		t.Errorf("incidents = %+v", got)
	}
}

// The signals runner is paced by alertmanager_poll_interval, not by poll_interval (GitHub's).
func TestTheSignalsRunnerFollowsTheAlertmanagerInterval(t *testing.T) {
	a := appWithEnv(t, map[string]string{"REMEDY_POLL_INTERVAL": "1h"})
	ts := httptest.NewServer(a.Handler)
	t.Cleanup(ts.Close)
	do := adminSession(t, ts)

	// The environment's poll interval is the default of the Alertmanager one until it has its own.
	if got := a.Signals.Interval(); got != time.Hour {
		t.Fatalf("interval from the environment fallback = %s, want 1h", got)
	}
	if code, body := do(http.MethodPut, "/api/settings/alertmanager_poll_interval", `{"value":"20s"}`); code != http.StatusOK {
		t.Fatalf("PUT alertmanager_poll_interval = %d %s", code, body)
	}
	if got := a.Signals.Interval(); got != 20*time.Second {
		t.Errorf("interval = %s, want 20s", got)
	}
	if code, body := do(http.MethodPut, "/api/settings/poll_interval", `{"value":"3m"}`); code != http.StatusOK {
		t.Fatalf("PUT poll_interval = %d %s", code, body)
	}
	if got := a.Signals.Interval(); got != 20*time.Second {
		t.Errorf("interval after poll_interval changed = %s, want 20s", got)
	}
}
