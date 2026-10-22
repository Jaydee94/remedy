package app_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/app"
	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/kube"
	"github.com/Jaydee94/remedy/internal/testutil"
)

func TestAChangedSettingReachesTheComponentsThatUseIt(t *testing.T) {
	s := newStack(t, false)

	put := func(key, body string) {
		t.Helper()
		if code, out := s.admin(http.MethodPut, "/api/settings/"+key, body); code != http.StatusOK {
			t.Fatalf("PUT %s = %d %s", key, code, out)
		}
	}
	put("poll_interval", `{"value":"30s"}`)
	put("diagnose_max_per_day", `{"value":5}`)
	put("diagnose_cooldown", `{"value":"20m"}`)
	put("run_timeout", `{"value":"30m"}`)

	if got := s.app.Poller.IntervalFunc(); got != 30*time.Second {
		t.Errorf("the poller's interval = %v", got)
	}
	if got := s.app.Responder.LimitsFunc(); got.MaxPerDay != 5 || got.Cooldown != 20*time.Minute || got.MaxPerIncident != 3 {
		t.Errorf("the responder's limits = %+v", got)
	}
	if got := s.app.Reaper.MaxAgeFunc(); got != 35*time.Minute {
		t.Errorf("the reaper's threshold = %v, want 35m", got)
	}
	code, body := s.admin(http.MethodGet, "/api/limits", "")
	var limits struct {
		PollIntervalSeconds int `json:"pollIntervalSeconds"`
		DiagnoseMaxPerDay   int `json:"diagnoseMaxPerDay"`
		StaleRunMinutes     int `json:"staleRunMinutes"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &limits) != nil || limits.PollIntervalSeconds != 30 || limits.DiagnoseMaxPerDay != 5 || limits.StaleRunMinutes != 35 {
		t.Errorf("limits = %d %s", code, body)
	}

	// A reset falls back to the default.
	if code, out := s.admin(http.MethodDelete, "/api/settings/poll_interval", ""); code != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", code, out)
	}
	if got := s.app.Poller.IntervalFunc(); got != time.Minute {
		t.Errorf("after the reset the interval = %v", got)
	}
}

func TestTheEnvironmentIsTheDefaultOfTheSettings(t *testing.T) {
	a := appWithEnv(t, map[string]string{"REMEDY_DIAGNOSE_MAX_PER_DAY": "9", "REMEDY_RUN_TIMEOUT": "20m"})
	if got := a.Responder.LimitsFunc().MaxPerDay; got != 9 {
		t.Errorf("per day from the environment = %d", got)
	}
	if got := a.Reaper.MaxAgeFunc(); got != 25*time.Minute {
		t.Errorf("the reaper's threshold = %v, want 25m", got)
	}
}

func TestTheActionsFollowTheAllowlistOfTheSettings(t *testing.T) {
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	a := clusterApp(t, kube.Config{API: "http://127.0.0.1:1", ReadTokenFile: tokenOf(t, "read"), WriteTokenFile: tokenOf(t, "write")}, log)
	if a.KubeWriter == nil {
		t.Fatal("a write token is enough for the actions to exist; the allowlist may be empty")
	}
	ts := httptest.NewServer(a.Handler)
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	do := func(method, path, body string) string {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("X-Remedy-CSRF", "1")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	do(http.MethodPost, "/api/login", `{"password":"`+password+`"}`)
	if got := do(http.MethodGet, "/api/capabilities", ""); got != `{"cluster":{"read":true,"write":true,"namespaces":[]}}`+"\n" {
		t.Fatalf("before: %s", got)
	}
	do(http.MethodPut, "/api/settings/k8s_write_namespaces", `{"value":["demo"]}`)
	if got := do(http.MethodGet, "/api/capabilities", ""); got != `{"cluster":{"read":true,"write":true,"namespaces":["demo"]}}`+"\n" {
		t.Fatalf("after: %s", got)
	}
}

// A stored empty list overrides the environment's list: every action is refused. It is not the same as no override.
func TestAStoredEmptyAllowlistOverridesTheEnvironment(t *testing.T) {
	a := appWithEnv(t, map[string]string{
		"REMEDY_K8S_API": "http://127.0.0.1:1", "REMEDY_K8S_READ_TOKEN_FILE": tokenOf(t, "read"),
		"REMEDY_K8S_WRITE_TOKEN_FILE": tokenOf(t, "write"), "REMEDY_K8S_WRITE_NAMESPACES": "a,b",
	})
	ts := httptest.NewServer(a.Handler)
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	do := func(method, path, body string) string {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("X-Remedy-CSRF", "1")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	do(http.MethodPost, "/api/login", `{"password":"`+password+`"}`)
	if got := do(http.MethodGet, "/api/capabilities", ""); got != `{"cluster":{"read":true,"write":true,"namespaces":["a","b"]}}`+"\n" {
		t.Fatalf("before: %s", got)
	}
	do(http.MethodPut, "/api/settings/k8s_write_namespaces", `{"value":[]}`)
	if got := do(http.MethodGet, "/api/capabilities", ""); got != `{"cluster":{"read":true,"write":true,"namespaces":[]}}`+"\n" {
		t.Fatalf("after: %s", got)
	}
	var out struct {
		Settings []struct {
			Key    string `json:"key"`
			Source string `json:"source"`
		} `json:"settings"`
	}
	if err := json.Unmarshal([]byte(do(http.MethodGet, "/api/settings", "")), &out); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range out.Settings {
		if s.Key == "k8s_write_namespaces" {
			found = true
			if s.Source != "set here" {
				t.Errorf("source = %q, want set here", s.Source)
			}
		}
	}
	if !found {
		t.Error("k8s_write_namespaces is not in /api/settings")
	}
}

func appWithEnv(t *testing.T, extra map[string]string) *app.App {
	t.Helper()
	env := map[string]string{
		"REMEDY_ADMIN_PASSWORD": password, "REMEDY_RUNNER_TOKEN": runnerToken,
		"REMEDY_MASTER_KEY": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32)),
		// only parsed here; the store is opened below
		"REMEDY_DATABASE_URL": "postgres://remedy:unused@localhost/remedy",
	}
	for k, v := range extra {
		env[k] = v
	}
	cfg, err := config.ServerFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	st := testutil.Store(t)
	return app.New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
}
