package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/responder"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/settings"
	"github.com/Jaydee94/remedy/internal/store"
)

type settingJSON struct {
	Key      string          `json:"key"`
	Group    string          `json:"group"`
	Kind     string          `json:"kind"`
	Value    json.RawMessage `json:"value"`
	Source   string          `json:"source"`
	Fallback string          `json:"fallback"`
	Env      string          `json:"env"`
	Problem  string          `json:"problem"`
}

type settingsPage struct {
	Settings []settingJSON `json:"settings"`
	Facts    []struct{ Label, Value string }
}

func mustReadAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return b
}

func TestTheSettingsAreListedWithTheirSourceAndTheBootstrapFacts(t *testing.T) {
	e, _ := newSettingsEnv(t, func(d *server.Deps) {
		d.Facts = []server.Fact{{Label: "Listener", Value: ":8080"}}
	})
	c := e.adminClient(t, true)
	resp := e.do(t, c, http.MethodGet, "/api/settings", "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	page := decode[settingsPage](t, resp)
	// 6 settings of cycle 3 and 2 of the Alertmanager source
	if len(page.Settings) != 8 || page.Settings[0].Key != "poll_interval" || string(page.Settings[0].Value) != `"1m"` ||
		page.Settings[0].Source != "default" || page.Settings[0].Env != "REMEDY_POLL_INTERVAL" || page.Settings[0].Kind != "duration" {
		t.Fatalf("settings = %+v", page.Settings)
	}
	if len(page.Facts) != 1 || page.Facts[0].Value != ":8080" {
		t.Fatalf("facts = %+v", page.Facts)
	}
}

func TestASettingIsChangedAndResetThroughTheAPI(t *testing.T) {
	e, set := newSettingsEnv(t, nil)
	c := e.adminClient(t, true)

	resp := e.do(t, c, http.MethodPut, "/api/settings/poll_interval", `{"value":"30s"}`, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %d", resp.StatusCode)
	}
	if got := decode[settingJSON](t, resp); got.Source != "set here" || string(got.Value) != `"30s"` {
		t.Fatalf("answer = %+v", got)
	}
	if set.Duration("poll_interval").String() != "30s" {
		t.Fatalf("the reader still has %v", set.Duration("poll_interval"))
	}
	resp = e.do(t, c, http.MethodPut, "/api/settings/k8s_write_namespaces", `{"value":["demo","staging"]}`, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT list = %d", resp.StatusCode)
	}
	if resp = e.do(t, c, http.MethodDelete, "/api/settings/poll_interval", "", true); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE = %d", resp.StatusCode)
	}
	if resp = e.do(t, c, http.MethodDelete, "/api/settings/poll_interval", "", true); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("a second DELETE = %d, want 204", resp.StatusCode)
	}
	if set.Duration("poll_interval").String() != "1m0s" {
		t.Fatalf("after the reset: %v", set.Duration("poll_interval"))
	}
}

func TestARefusedValueIsA400WithAReadableMessage(t *testing.T) {
	e, _ := newSettingsEnv(t, nil)
	c := e.adminClient(t, true)
	for _, body := range []string{`{"value":"5s"}`, `{"value":30}`, `{"value":null}`, `{}`, `not json`} {
		resp := e.do(t, c, http.MethodPut, "/api/settings/poll_interval", body, true)
		got := decode[struct{ Error string }](t, resp)
		if resp.StatusCode != http.StatusBadRequest || got.Error == "" {
			t.Errorf("%s: %d %q", body, resp.StatusCode, got.Error)
		}
		if body == `{"value":"5s"}` && !strings.HasPrefix(got.Error, "poll_interval must be a duration of at least 10s") {
			t.Errorf("message = %q", got.Error)
		}
	}
	if resp := e.do(t, c, http.MethodPut, "/api/settings/no_such_key", `{"value":1}`, true); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown key = %d", resp.StatusCode)
	}
	if resp := e.do(t, c, http.MethodDelete, "/api/settings/no_such_key", "", true); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown key on DELETE = %d", resp.StatusCode)
	}
}

func TestTheSettingsRoutesNeedASessionAndTheCSRFHeader(t *testing.T) {
	e, _ := newSettingsEnv(t, nil)
	anon := e.adminClient(t, false)
	if got := e.do(t, anon, http.MethodGet, "/api/settings", "", false).StatusCode; got != http.StatusUnauthorized {
		t.Errorf("GET without a session = %d", got)
	}
	c := e.adminClient(t, true)
	if got := e.do(t, c, http.MethodPut, "/api/settings/poll_interval", `{"value":"30s"}`, false).StatusCode; got != http.StatusForbidden {
		t.Errorf("PUT without the CSRF header = %d", got)
	}
}

func TestNoBootstrapSecretIsInAnyAnswer(t *testing.T) {
	e, _ := newSettingsEnv(t, func(d *server.Deps) { d.Facts = []server.Fact{{Label: "Listener", Value: ":8080"}} })
	c := e.adminClient(t, true)
	resp := e.do(t, c, http.MethodGet, "/api/settings", "", false)
	body := string(mustReadAll(t, resp))
	for _, secret := range []string{password, runnerToken} {
		if strings.Contains(body, secret) {
			t.Errorf("the answer contains a bootstrap secret")
		}
	}
}

func TestTheLimitsAreReadFromTheSettings(t *testing.T) {
	e, set := newSettingsEnv(t, func(d *server.Deps) {
		d.Responder = &responder.Responder{Store: d.Store, Limits: store.DefaultLimits()}
		d.PollInterval = 90 * time.Second // the old source, which the settings must replace
	})
	ctx := context.Background()
	for key, raw := range map[string]string{settings.DiagnoseMaxPerDay: `7`, settings.RunTimeout: `"30m"`, settings.PollInterval: `"2m"`} {
		if _, err := set.Set(ctx, key, json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
	c := e.adminClient(t, true)
	resp := e.do(t, c, http.MethodGet, "/api/limits", "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got := decode[struct {
		PollIntervalSeconds int `json:"pollIntervalSeconds"`
		DiagnoseMaxPerDay   int `json:"diagnoseMaxPerDay"`
		StaleRunMinutes     int `json:"staleRunMinutes"`
	}](t, resp)
	if got.PollIntervalSeconds != 120 || got.DiagnoseMaxPerDay != 7 || got.StaleRunMinutes != 35 {
		t.Fatalf("limits = %+v, want 120 s, 7 a day and a stale threshold of 35 minutes", got)
	}
}
