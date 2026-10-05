package server_test

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

// clusterRunEnv is a server with the gatekeeper and the given cluster, and a signed-in admin client.
func clusterRunEnv(t *testing.T, cluster server.Cluster) (*env, *http.Client) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	g := gatekeeper.New(gatekeeper.Config{Store: st, Tools: gatekeeper.IncidentTools(st)})
	ts := httptest.NewServer(server.New(server.Deps{
		Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Gatekeeper: g, Cluster: cluster,
	}))
	t.Cleanup(ts.Close)
	e := &env{ts: ts, store: st}
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if resp := e.do(t, c, http.MethodPost, "/api/login", `{"password":"`+password+`"}`, true); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	return e, c
}

func TestARunWithClusterToolsIsCreatedWhenTheClusterCanBeRead(t *testing.T) {
	e, c := clusterRunEnv(t, server.Cluster{Read: true})
	resp := e.do(t, c, http.MethodPost, "/api/runs", `{"prompt":"look at the cluster","tools":true,"cluster":true}`, true)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := bodyOf(t, resp)
	if field(t, body, "cluster") != true || field(t, body, "mcp") != true {
		t.Fatalf("run = %s, want a run with gatekeeper access and cluster tools", body)
	}
	id := field(t, body, "id").(string)

	got := bodyOf(t, e.do(t, c, http.MethodGet, "/api/runs/"+id, "", false))
	if field(t, got, "cluster") != true {
		t.Fatalf("GET run = %s", got)
	}
}

func TestARunWithGatekeeperToolsOnlyHasNoClusterFlag(t *testing.T) {
	e, c := clusterRunEnv(t, server.Cluster{Read: true})
	resp := e.do(t, c, http.MethodPost, "/api/runs", `{"prompt":"just the incidents","tools":true}`, true)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body := bodyOf(t, resp); field(t, body, "cluster") == true || field(t, body, "mcp") != true {
		t.Fatalf("run = %s", body)
	}
}

func TestClusterToolsNeedTheGatekeeperTools(t *testing.T) {
	e, c := clusterRunEnv(t, server.Cluster{Read: true})
	resp := e.do(t, c, http.MethodPost, "/api/runs", `{"prompt":"look at the cluster","cluster":true}`, true)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if runs, _ := e.store.ListRuns(t.Context(), 10); len(runs) != 0 {
		t.Fatalf("a refused request created %d runs", len(runs))
	}
}

func TestClusterToolsNeedAConfiguredCluster(t *testing.T) {
	e, c := clusterRunEnv(t, server.Cluster{})
	resp := e.do(t, c, http.MethodPost, "/api/runs", `{"prompt":"look at the cluster","tools":true,"cluster":true}`, true)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	if runs, _ := e.store.ListRuns(t.Context(), 10); len(runs) != 0 {
		t.Fatalf("a refused request created %d runs", len(runs))
	}
}

func TestTheRunListShowsWhichRunsHaveClusterTools(t *testing.T) {
	e, c := clusterRunEnv(t, server.Cluster{Read: true})
	e.do(t, c, http.MethodPost, "/api/runs", `{"prompt":"plain"}`, true)
	e.do(t, c, http.MethodPost, "/api/runs", `{"prompt":"cluster","tools":true,"cluster":true}`, true)
	runs, err := e.store.ListRuns(t.Context(), 10)
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	for _, r := range runs {
		if r.Cluster != (r.Prompt == "cluster") {
			t.Fatalf("run %q has cluster = %v", r.Prompt, r.Cluster)
		}
	}
}
