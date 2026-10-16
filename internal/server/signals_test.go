package server_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/signals"
)

func TestTheSourcesAreListedWithTheirState(t *testing.T) {
	checked := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	e, _ := newSettingsEnv(t, func(d *server.Deps) {
		d.Signals = func() []signals.State {
			return []signals.State{{Name: "alertmanager", State: signals.StateOK, CheckedAt: checked, Active: 7}}
		}
	})
	c := e.adminClient(t, true)
	resp := e.do(t, c, http.MethodGet, "/api/signals", "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got := decode[struct {
		Sources []struct {
			Name, State, CheckedAt, Error string
			Active                        int
		} `json:"sources"`
	}](t, resp)
	if len(got.Sources) != 1 || got.Sources[0].Name != "alertmanager" || got.Sources[0].State != "ok" || got.Sources[0].Active != 7 || got.Sources[0].CheckedAt == "" || got.Sources[0].Error != "" {
		t.Fatalf("%+v", got)
	}
}

func TestTheSignalsNeedASessionAndAreNotThereWithoutARunner(t *testing.T) {
	e, _ := newSettingsEnv(t, func(d *server.Deps) { d.Signals = func() []signals.State { return nil } })
	if got := e.do(t, e.adminClient(t, false), http.MethodGet, "/api/signals", "", false).StatusCode; got != http.StatusUnauthorized {
		t.Errorf("without a session = %d", got)
	}
	empty := decode[struct{ Sources []any }](t, e.do(t, e.adminClient(t, true), http.MethodGet, "/api/signals", "", false))
	if empty.Sources == nil {
		t.Error("no sources must be [] and not null")
	}
	none, _ := newSettingsEnv(t, nil)
	if got := none.do(t, none.adminClient(t, true), http.MethodGet, "/api/signals", "", false).StatusCode; got != http.StatusNotFound {
		t.Errorf("without Deps.Signals = %d, want 404", got)
	}
}
