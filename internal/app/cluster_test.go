package app_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/app"
	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/kube"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
)

func clusterApp(t *testing.T, cluster kube.Config, log *slog.Logger) *app.App {
	t.Helper()
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
		DiagnoseCooldown: 15 * time.Minute, DiagnoseMaxPerIncident: 3, DiagnoseMaxPerDay: 20, Cluster: cluster,
	}
	return app.New(cfg, st, log, nil)
}

func tokenOf(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(name+"-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWithoutClusterSettingsThereAreNoClusterClients(t *testing.T) {
	a := clusterApp(t, kube.Config{}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if a.KubeReader != nil || a.KubeWriter != nil {
		t.Fatalf("reader = %v, writer = %v", a.KubeReader, a.KubeWriter)
	}
	a.CheckCluster(context.Background()) // must not do anything, and must not panic
	if body, want := capabilities(t, a), `{"cluster":{"read":false,"write":false,"namespaces":[]}}`+"\n"; body != want {
		t.Fatalf("capabilities = %q, want %q", body, want)
	}
}

func TestTheClientsAreBuiltForWhatIsConfigured(t *testing.T) {
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	readOnly := clusterApp(t, kube.Config{API: "http://127.0.0.1:1", ReadTokenFile: tokenOf(t, "read")}, log)
	if readOnly.KubeReader == nil || readOnly.KubeWriter != nil {
		t.Fatalf("read only: reader = %v, writer = %v", readOnly.KubeReader, readOnly.KubeWriter)
	}
	both := clusterApp(t, kube.Config{API: "http://127.0.0.1:1", ReadTokenFile: tokenOf(t, "read"),
		WriteTokenFile: tokenOf(t, "write"), WriteNamespaces: []string{"demo"}}, log)
	if both.KubeReader == nil || both.KubeWriter == nil {
		t.Fatalf("both: reader = %v, writer = %v", both.KubeReader, both.KubeWriter)
	}
}

func TestCheckClusterSaysWhetherTheClusterAnswers(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer read-token" {
			http.Error(w, `{"kind":"Status","message":"Unauthorized","code":401}`, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"gitVersion":"v1.31.0"}`))
	}))
	t.Cleanup(api.Close)

	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, nil))
	a := clusterApp(t, kube.Config{API: api.URL, ReadTokenFile: tokenOf(t, "read")}, log)
	a.CheckCluster(context.Background())
	if out := logged.String(); !strings.Contains(out, "level=INFO") || !strings.Contains(out, "v1.31.0") {
		t.Fatalf("log = %s", out)
	}

	logged.Reset()
	bad := clusterApp(t, kube.Config{API: api.URL, ReadTokenFile: tokenOf(t, "wrong")}, log)
	bad.CheckCluster(context.Background())
	if out := logged.String(); !strings.Contains(out, "level=WARN") || strings.Contains(out, "wrong-token") {
		t.Fatalf("log = %s", out)
	}
}

// capabilities signs in to the app and returns what /api/capabilities answers.
func capabilities(t *testing.T, a *app.App) string {
	t.Helper()
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
	resp, err = client.Get(ts.URL + "/api/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func TestTheUIIsToldWhatIsConfiguredAndNothingSecret(t *testing.T) {
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	read, write := tokenOf(t, "read"), tokenOf(t, "write")
	a := clusterApp(t, kube.Config{API: "http://127.0.0.1:1", ReadTokenFile: read, WriteTokenFile: write,
		WriteNamespaces: []string{"demo", "staging"}}, log)
	body := capabilities(t, a)
	if want := `{"cluster":{"read":true,"write":true,"namespaces":["demo","staging"]}}` + "\n"; body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
	for _, secretText := range []string{read, write, "127.0.0.1:1", "-token"} {
		if strings.Contains(body, secretText) {
			t.Fatalf("the answer contains %q", secretText)
		}
	}
}

func TestActionsAreNotOfferedWithoutAWriteSide(t *testing.T) {
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	a := clusterApp(t, kube.Config{API: "http://127.0.0.1:1", ReadTokenFile: tokenOf(t, "read")}, log)
	if body, want := capabilities(t, a), `{"cluster":{"read":true,"write":false,"namespaces":[]}}`+"\n"; body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}
