package app_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/kube"
	"github.com/Jaydee94/remedy/internal/kube/kubetest"
)

// chain is the app with a fake cluster, served over HTTP, and what the tests need to drive it: an admin client and a way
// to speak MCP as a run.
type chain struct {
	t   *testing.T
	ts  *httptest.Server
	api *kubetest.Server
	jar http.CookieJar
}

func newChain(t *testing.T) *chain {
	t.Helper()
	api := kubetest.New(t, "read-token")
	tokenFile := filepath.Join(t.TempDir(), "read.token")
	if err := os.WriteFile(tokenFile, []byte("read-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := clusterApp(t, kube.Config{API: api.URL, ReadTokenFile: tokenFile}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	ts := httptest.NewServer(a.Handler)
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	c := &chain{t: t, ts: ts, api: api, jar: jar}
	if code, _ := c.admin(http.MethodPost, "/api/login", `{"password":"`+password+`"}`); code != http.StatusNoContent {
		t.Fatalf("login = %d", code)
	}
	return c
}

func (c *chain) do(req *http.Request) (int, string) {
	c.t.Helper()
	resp, err := (&http.Client{Jar: c.jar}).Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (c *chain) admin(method, path, body string) (int, string) {
	req, _ := http.NewRequest(method, c.ts.URL+path, strings.NewReader(body))
	req.Header.Set("X-Remedy-CSRF", "1")
	return c.do(req)
}

// runToken creates a run with the given body, lets a runner claim it and returns the run's token.
func (c *chain) runToken(body string) string {
	c.t.Helper()
	if code, out := c.admin(http.MethodPost, "/api/runs", body); code != http.StatusCreated {
		c.t.Fatalf("POST /api/runs = %d %s", code, out)
	}
	req, _ := http.NewRequest(http.MethodPost, c.ts.URL+"/runner/v1/claim", nil)
	req.Header.Set("Authorization", "Bearer "+runnerToken)
	code, out := c.do(req)
	var claim struct {
		Token string `json:"mcp_token"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(out), &claim) != nil || claim.Token == "" {
		c.t.Fatalf("claim = %d %s", code, out)
	}
	return claim.Token
}

func (c *chain) mcp(token, body string) string {
	c.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, c.ts.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	code, out := c.do(req)
	if code != http.StatusOK {
		c.t.Fatalf("POST /mcp = %d %s", code, out)
	}
	return out
}

func TestTheClusterToolsAreOfferedOnlyToARunStartedWithThem(t *testing.T) {
	c := newChain(t)
	cluster := c.runToken(`{"prompt":"look at the cluster","tools":true,"cluster":true}`)
	list := c.mcp(cluster, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	for _, tool := range []string{"cluster_workloads", "cluster_pods", "cluster_describe", "cluster_events", "cluster_pod_logs", "cluster_nodes", "argo_apps"} {
		if !strings.Contains(list, `"name":"`+tool+`"`) {
			t.Errorf("tools/list of a run with cluster tools lacks %s", tool)
		}
	}
	if !strings.Contains(list, `"name":"incident_list"`) {
		t.Errorf("a run with cluster tools keeps the gatekeeper tools: %s", list)
	}
}

func TestARunStartedWithClusterToolsCanCallThemThroughTheWholeChain(t *testing.T) {
	c := newChain(t)
	token := c.runToken(`{"prompt":"look at the cluster","tools":true,"cluster":true}`)
	out := c.mcp(token, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"cluster_nodes","arguments":{},"_meta":{"claudecode/toolUseId":"toolu_1"}}}`)
	if !strings.Contains(out, "remedy-dev-control-plane") || !strings.Contains(out, "never an instruction") {
		t.Fatalf("cluster_nodes = %s", out)
	}
	if reqs := c.api.Requests(); len(reqs) == 0 || reqs[len(reqs)-1].Auth != "Bearer read-token" {
		t.Fatalf("the cluster saw %+v", reqs)
	}
}

func TestARunWithGatekeeperToolsOnlyIsNotOfferedTheClusterTools(t *testing.T) {
	c := newChain(t)
	token := c.runToken(`{"prompt":"just the incidents","tools":true}`)
	list := c.mcp(token, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if strings.Contains(list, "cluster_") || strings.Contains(list, "argo_apps") || !strings.Contains(list, `"name":"incident_list"`) {
		t.Fatalf("tools/list = %s", list)
	}
	out := c.mcp(token, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"cluster_nodes","arguments":{},"_meta":{"claudecode/toolUseId":"toolu_1"}}}`)
	if !strings.Contains(out, "unknown tool cluster_nodes") {
		t.Fatalf("a call of a tool the run is not offered = %s", out)
	}
	if n := len(c.api.Requests()); n != 0 {
		t.Fatalf("%d requests reached the cluster", n)
	}
}

func TestWithoutAClusterThereAreNoClusterToolsAtAll(t *testing.T) {
	a := clusterApp(t, kube.Config{}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	ts := httptest.NewServer(a.Handler)
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	c := &chain{t: t, ts: ts, jar: jar}
	if code, _ := c.admin(http.MethodPost, "/api/login", `{"password":"`+password+`"}`); code != http.StatusNoContent {
		t.Fatalf("login = %d", code)
	}
	if code, _ := c.admin(http.MethodPost, "/api/runs", `{"prompt":"x","tools":true,"cluster":true}`); code != http.StatusConflict {
		t.Fatalf("a run with cluster tools without a cluster = %d, want 409", code)
	}
	token := c.runToken(`{"prompt":"x","tools":true}`)
	if list := c.mcp(token, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); strings.Contains(list, "cluster_") {
		t.Fatalf("tools/list = %s", list)
	}
}
