package server_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

func newRunnerEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ts := httptest.NewServer(server.New(server.Deps{
		Store: st, Auth: auth.New(password), RunnerToken: runnerToken, RunnerStatus: server.NewRunnerStatus(),
	}))
	t.Cleanup(ts.Close)
	return &env{ts: ts, store: st}
}

type runnerView struct {
	Connected      bool       `json:"connected"`
	LastSeenAt     *time.Time `json:"lastSeenAt"`
	Login          string     `json:"login"`
	LoginCheckedAt *time.Time `json:"loginCheckedAt"`
	CLIVersion     string     `json:"cliVersion"`
}

func getRunnerView(t *testing.T, e *env, c *http.Client) runnerView {
	t.Helper()
	resp := e.do(t, c, http.MethodGet, "/api/runner", "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/runner = %d", resp.StatusCode)
	}
	return decode[runnerView](t, resp)
}

func TestTheRunnerViewNeedsASession(t *testing.T) {
	e := newRunnerEnv(t)
	if resp := e.do(t, e.adminClient(t, false), http.MethodGet, "/api/runner", "", false); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestBeforeTheRunnerSaysAnythingItIsNotConnectedAndItsLoginIsUnknown(t *testing.T) {
	e := newRunnerEnv(t)
	v := getRunnerView(t, e, e.adminClient(t, true))
	if v.Connected || v.Login != "unknown" || v.LastSeenAt != nil || v.LoginCheckedAt != nil || v.CLIVersion != "" {
		t.Fatalf("view = %+v", v)
	}
}

func TestAReportMakesTheRunnerConnectedAndShowsItsLogin(t *testing.T) {
	e := newRunnerEnv(t)
	body := `{"login":"ok","loginCheckedAt":"` + time.Now().UTC().Format(time.RFC3339) + `","cliVersion":"2.1.288 (Claude Code)"}`
	if resp := e.runnerDo(t, http.MethodPost, "/runner/v1/status", body, runnerToken); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("report = %d, want 204", resp.StatusCode)
	}
	v := getRunnerView(t, e, e.adminClient(t, true))
	if !v.Connected || v.Login != "ok" || v.CLIVersion != "2.1.288 (Claude Code)" || v.LastSeenAt == nil || v.LoginCheckedAt == nil {
		t.Fatalf("view = %+v", v)
	}
}

func TestOnlyTheRunnerTokenCanReport(t *testing.T) {
	e := newRunnerEnv(t)
	for _, tok := range []string{"", "wrong-token-wrong-token-wrong-t"} {
		if resp := e.runnerDo(t, http.MethodPost, "/runner/v1/status", `{"login":"ok"}`, tok); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("token %q: status = %d, want 401", tok, resp.StatusCode)
		}
	}
	if v := getRunnerView(t, e, e.adminClient(t, true)); v.Connected || v.Login != "unknown" {
		t.Fatalf("a refused report changed the view: %+v", v)
	}
}

func TestABadReportIsRefusedAndChangesNothing(t *testing.T) {
	e := newRunnerEnv(t)
	good := `{"login":"missing","loginCheckedAt":"` + time.Now().UTC().Format(time.RFC3339) + `","cliVersion":"2.1.288"}`
	if resp := e.runnerDo(t, http.MethodPost, "/runner/v1/status", good, runnerToken); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("good report = %d", resp.StatusCode)
	}
	bad := map[string]string{
		"an unknown login word":     `{"login":"maybe"}`,
		"no login":                  `{"loginCheckedAt":"2026-10-07T12:00:00Z"}`,
		"control characters":        `{"login":"ok","cliVersion":"2.1\u0000.288\n<script>"}`,
		"a very long version":       `{"login":"ok","cliVersion":"` + strings.Repeat("1", 5000) + `"}`,
		"an unknown field":          `{"login":"ok","token":"x"}`,
		"not JSON":                  `login=ok`,
		"a second object":           `{"login":"ok"}{"login":"missing"}`,
		"trailing text":             `{"login":"ok"} x`,
		"a time that is not a time": `{"login":"ok","loginCheckedAt":"yesterday"}`,
	}
	for name, body := range bad {
		if resp := e.runnerDo(t, http.MethodPost, "/runner/v1/status", body, runnerToken); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, resp.StatusCode)
		}
	}
	v := getRunnerView(t, e, e.adminClient(t, true))
	if v.Login != "missing" || v.CLIVersion != "2.1.288" {
		t.Fatalf("bad reports changed the view: %+v", v)
	}
}

// The unit tests below use a clock of their own.

func TestConnectedForFortyFiveSecondsAfterTheLastRequestAndThenTheLoginIsUnknown(t *testing.T) {
	rs := server.NewRunnerStatus()
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	if err := rs.Report("ok", t0, "2.1.288", t0); err != nil {
		t.Fatal(err)
	}
	if v := rs.View(t0.Add(44 * time.Second)); !v.Connected || v.Login != "ok" {
		t.Fatalf("44 s after: %+v, want connected and ok", v)
	}
	rs.Touch(t0.Add(30 * time.Second)) // any authenticated request keeps it connected
	if v := rs.View(t0.Add(74 * time.Second)); !v.Connected {
		t.Fatalf("44 s after a touch: %+v, want connected", v)
	}
	v := rs.View(t0.Add(76 * time.Second))
	if v.Connected {
		t.Fatalf("46 s after the last touch: %+v, want not connected", v)
	}
	if v.Login != "unknown" {
		t.Fatalf("not connected but the login is %q: a stale report must never say logged in", v.Login)
	}
	if v.LastSeenAt == nil || !v.LastSeenAt.Equal(t0.Add(30*time.Second)) {
		t.Fatalf("lastSeenAt = %v, want the last touch", v.LastSeenAt)
	}
}

func TestACheckedAtFromTheFutureOrTheDistantPastIsReplacedByNow(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for name, at := range map[string]time.Time{
		"year 2040": time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC),
		"2020":      time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		"zero":      {},
	} {
		rs := server.NewRunnerStatus()
		if err := rs.Report("ok", at, "", now); err != nil {
			t.Fatal(err)
		}
		if v := rs.View(now); v.LoginCheckedAt == nil || !v.LoginCheckedAt.Equal(now) {
			t.Errorf("%s: loginCheckedAt = %v, want now", name, v.LoginCheckedAt)
		}
	}
	rs := server.NewRunnerStatus()
	recent := now.Add(-time.Hour)
	_ = rs.Report("ok", recent, "", now)
	if v := rs.View(now); !v.LoginCheckedAt.Equal(recent) {
		t.Errorf("a recent time must be kept: %v", v.LoginCheckedAt)
	}
}

func TestANilRunnerStatusDoesNothing(t *testing.T) {
	var rs *server.RunnerStatus
	rs.Touch(time.Now()) // must not panic
}

func TestAVersionLineOfMoreThanSixtyFourCharactersIsRefused(t *testing.T) {
	// 65 characters fit the 4 KB body limit, so only the length check can refuse them.
	rs := server.NewRunnerStatus()
	now := time.Now()
	if err := rs.Report("ok", now, strings.Repeat("1", 64), now); err != nil {
		t.Fatalf("64 characters: %v", err)
	}
	if err := rs.Report("ok", now, strings.Repeat("1", 65), now); err == nil {
		t.Fatal("65 characters were accepted")
	}
	if v := rs.View(now); len(v.CLIVersion) != 64 {
		t.Fatalf("a refused report changed the version: %q", v.CLIVersion)
	}
}

func TestTrailingWhitespaceAfterTheReportIsAccepted(t *testing.T) {
	e := newRunnerEnv(t)
	// The runner's JSON encoder ends its output with a newline.
	if resp := e.runnerDo(t, http.MethodPost, "/runner/v1/status", "{\"login\":\"ok\"}\n", runnerToken); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("report with a trailing newline = %d, want 204", resp.StatusCode)
	}
	if v := getRunnerView(t, e, e.adminClient(t, true)); v.Login != "ok" {
		t.Fatalf("view = %+v", v)
	}
}

func TestConnectedExactlyFortyFiveSecondsAfterTheLastRequest(t *testing.T) {
	rs := server.NewRunnerStatus()
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	rs.Touch(t0)
	if v := rs.View(t0.Add(45 * time.Second)); !v.Connected {
		t.Fatalf("at exactly 45 s: %+v, want connected", v)
	}
	if v := rs.View(t0.Add(45*time.Second + time.Nanosecond)); v.Connected {
		t.Fatalf("45 s and 1 ns: %+v, want not connected", v)
	}
}

func TestTheCheckedAtClampBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for name, c := range map[string]struct {
		at   time.Time
		kept bool
	}{
		"1 minute in the future":         {now.Add(time.Minute), true},
		"1 minute and 1 s in the future": {now.Add(time.Minute + time.Second), false},
		"exactly 24 hours old":           {now.Add(-24 * time.Hour), true},
		"24 hours and 1 s old":           {now.Add(-24*time.Hour - time.Second), false},
	} {
		rs := server.NewRunnerStatus()
		if err := rs.Report("ok", c.at, "", now); err != nil {
			t.Fatal(err)
		}
		want := now
		if c.kept {
			want = c.at
		}
		if v := rs.View(now); v.LoginCheckedAt == nil || !v.LoginCheckedAt.Equal(want) {
			t.Errorf("%s: loginCheckedAt = %v, want %v", name, v.LoginCheckedAt, want)
		}
	}
}
