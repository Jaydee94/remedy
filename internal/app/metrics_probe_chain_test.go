package app_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/metrics/metricstest"
)

func metricsState(t *testing.T, do func(string, string, string) (int, string)) signalState {
	t.Helper()
	for _, s := range allSignalStates(t, do) {
		if s.Name == "metrics" {
			return s
		}
	}
	t.Fatalf("GET /api/signals has no metrics entry: %+v", allSignalStates(t, do))
	return signalState{}
}

func waitForMetricsState(t *testing.T, do func(string, string, string) (int, string), want string) signalState {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s := metricsState(t, do)
		if s.State == want {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("metrics state = %+v, want %s", s, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The metrics store is a signal source of its own: it is listed beside the Alertmanager, it is probed with no Alertmanager set up,
// and its token is nowhere an API reader can see it.
func TestTheMetricsProbeIsListedBesideAlertmanagerAndWorksWithoutIt(t *testing.T) {
	const token = "metrics-tok-0123456789"
	fake := metricstest.New(t, token)
	a := appWithEnv(t, nil)
	ts := httptest.NewServer(a.Handler)
	t.Cleanup(ts.Close)
	do := adminSession(t, ts)

	got := allSignalStates(t, do)
	if len(got) != 2 || got[0].Name != "alertmanager" || got[1].Name != "metrics" || got[1].State != "off" {
		t.Fatalf("before anything is set: %+v", got)
	}

	// A wrong token: the probe says so, in words that do not hold the token.
	if code, body := do(http.MethodPut, "/api/settings/metrics_url", `{"value":"`+fake.URL+`"}`); code != http.StatusOK {
		t.Fatalf("PUT metrics_url = %d %s", code, body)
	}
	if code, body := do(http.MethodPut, "/api/settings/metrics_token", `{"value":"wrong-token-0123456789"}`); code != http.StatusOK {
		t.Fatalf("PUT metrics_token = %d %s", code, body)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { a.Probe.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	s := waitForMetricsState(t, do, "error")
	if !strings.Contains(s.Error, "401") {
		t.Errorf("error = %q, want the status", s.Error)
	}

	// The Alertmanager stays off: the probe does not depend on it.
	if am := signalStates(t, do); len(am) != 1 || am[0].State != "off" {
		t.Errorf("alertmanager = %+v", am)
	}

	for _, path := range []string{"/api/signals", "/api/settings"} {
		code, body := do(http.MethodGet, path, "")
		if code != http.StatusOK || strings.Contains(body, token) || strings.Contains(body, "wrong-token-0123456789") {
			t.Errorf("GET %s = %d, shows a token: %t", path, code, strings.Contains(body, "token-0123456789"))
		}
	}
}

func TestTheMetricsProbeIsOKWithTheRightToken(t *testing.T) {
	const token = "metrics-tok-0123456789"
	fake := metricstest.New(t, token)
	a := appWithEnv(t, nil)
	ts := httptest.NewServer(a.Handler)
	t.Cleanup(ts.Close)
	do := adminSession(t, ts)
	for k, v := range map[string]string{"metrics_url": fake.URL, "metrics_token": token} {
		if code, body := do(http.MethodPut, "/api/settings/"+k, `{"value":"`+v+`"}`); code != http.StatusOK {
			t.Fatalf("PUT %s = %d %s", k, code, body)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Probe.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	if s := waitForMetricsState(t, do, "ok"); s.Error != "" {
		t.Errorf("state = %+v", s)
	}
	for _, r := range fake.Requests() {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.Path, "/api/v1/status/buildinfo") {
			t.Errorf("the probe sent %+v", r)
		}
	}
}
