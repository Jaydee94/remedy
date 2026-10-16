package app_test

import (
	"bytes"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/app"
	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
)

// The Setup page shows the GitHub API address, but never what could be a credential in it.
func TestTheFactsShowTheGitHubAPIWithoutUserinfoQueryOrFragment(t *testing.T) {
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
		AdminPassword: password, RunnerToken: runnerToken, MasterKey: key,
		GitHubAPIURL: "https://user:hunter2pw@ghe.example/api/v3?x=y#frag", PollInterval: time.Minute,
		DiagnoseCooldown: 15 * time.Minute, DiagnoseMaxPerIncident: 3, DiagnoseMaxPerDay: 20,
	}
	a := app.New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	ts := httptest.NewServer(a.Handler)
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/login", strings.NewReader(`{"password":"`+password+`"}`))
	login.Header.Set("X-Remedy-CSRF", "1")
	resp, err := client.Do(login)
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login: %v %v", resp, err)
	}
	resp, err = client.Get(ts.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/settings = %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "https://ghe.example/api/v3") {
		t.Errorf("the facts lack the address: %s", body)
	}
	for _, leak := range []string{"hunter2pw", "user:", "x=y", "frag"} {
		if strings.Contains(body, leak) {
			t.Errorf("the response contains %q: %s", leak, body)
		}
	}
}
