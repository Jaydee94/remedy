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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/kube"
	"github.com/Jaydee94/remedy/internal/kube/kubetest"
)

// newChainWith is newChain with the write side of the cluster too, when write is set: a write token and the allowlist
// {demo}. The fake cluster accepts both tokens.
func newChainWith(t *testing.T, write bool) *chain {
	t.Helper()
	api := kubetest.New(t, "read-token")
	api.Accept("write-token")
	cfg := kube.Config{API: api.URL, ReadTokenFile: tokenFileWith(t, "read", "read-token")}
	if write {
		cfg.WriteTokenFile, cfg.WriteNamespaces = tokenFileWith(t, "write", "write-token"), []string{"demo"}
	}
	a := clusterApp(t, cfg, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	ts := httptest.NewServer(a.Handler)
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	c := &chain{t: t, ts: ts, api: api, jar: jar}
	if code, _ := c.admin(http.MethodPost, "/api/login", `{"password":"`+password+`"}`); code != http.StatusNoContent {
		t.Fatalf("login = %d", code)
	}
	return c
}

func tokenFileWith(t *testing.T, name, token string) string {
	t.Helper()
	path := tokenOf(t, name)
	if err := writeFileContent(path, token+"\n"); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFileContent(path, content string) error { return os.WriteFile(path, []byte(content), 0o600) }

func itoa(n int) string { return strconv.Itoa(n) }

var actionTools = []string{"cluster_rollout_restart", "cluster_delete_pod", "argo_refresh", "argo_sync"}

func TestTheActionToolsExistOnlyWhenTheWriteSideIsConfigured(t *testing.T) {
	for _, write := range []bool{false, true} {
		c := newChainWith(t, write)
		token := c.runToken(`{"prompt":"x","tools":true,"cluster":true}`)
		list := c.mcp(token, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
		for _, tool := range actionTools {
			if got := strings.Contains(list, `"name":"`+tool+`"`); got != write {
				t.Errorf("write side %v: %s listed = %v", write, tool, got)
			}
		}
		if !strings.Contains(list, `"name":"cluster_pods"`) {
			t.Errorf("write side %v: the read tools are always there", write)
		}
	}
}

func TestAnActionWaitsForTheMaintainerAndIsDoneAfterTheApprovalThroughTheWholeChain(t *testing.T) {
	c := newChainWith(t, true)
	token := c.runToken(`{"prompt":"restart the web","tools":true,"cluster":true}`)

	done := make(chan string, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodPost, c.ts.URL+"/mcp", strings.NewReader(
			`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"cluster_rollout_restart","arguments":{"kind":"deployment","namespace":"demo","name":"web"},`+
				`"_meta":{"claudecode/toolUseId":"toolu_1","progressToken":1}}}`))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- err.Error()
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		done <- string(b)
	}()

	var id float64
	deadline := time.Now().Add(5 * time.Second)
	for id == 0 && time.Now().Before(deadline) {
		_, body := c.admin(http.MethodGet, "/api/approvals", "")
		var list []map[string]any
		_ = json.Unmarshal([]byte(body), &list)
		if len(list) > 0 && list[0]["tool"] == "cluster_rollout_restart" {
			id = list[0]["id"].(float64)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if id == 0 {
		t.Fatal("the approval did not show up")
	}
	for _, q := range c.api.Requests() {
		if q.Method == http.MethodPatch {
			t.Fatalf("the workload was changed before the approval: %+v", q)
		}
	}
	if code, body := c.admin(http.MethodPost, "/api/approvals/"+itoa(int(id))+"/approve", `{"reason":"go on"}`); code != http.StatusOK {
		t.Fatalf("approve = %d %s", code, body)
	}
	select {
	case out := <-done:
		if !strings.Contains(out, "restarted deployment demo/web") {
			t.Fatalf("answer = %s", out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the call did not end after the approval")
	}
	var patches int
	for _, q := range c.api.Requests() {
		if q.Method == http.MethodPatch {
			patches++
			if q.Auth != "Bearer write-token" || q.Path != "/apis/apps/v1/namespaces/demo/deployments/web" {
				t.Fatalf("patch = %+v", q)
			}
		}
	}
	if patches != 1 {
		t.Fatalf("%d patches", patches)
	}
	if _, body := c.admin(http.MethodGet, "/api/activity?limit=20", ""); !strings.Contains(body, "cluster_action") || !strings.Contains(body, "Restarted deployment demo/web") {
		t.Fatalf("activity = %s", body)
	}
}

func TestARunWithoutTheClusterSwitchCannotCallAnAction(t *testing.T) {
	c := newChainWith(t, true)
	token := c.runToken(`{"prompt":"x","tools":true}`)
	out := c.mcp(token, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"cluster_delete_pod","arguments":{"namespace":"demo","name":"x"},"_meta":{"claudecode/toolUseId":"toolu_1"}}}`)
	if !strings.Contains(out, "unknown tool cluster_delete_pod") {
		t.Fatalf("answer = %s", out)
	}
	if n := len(c.api.Requests()); n != 0 {
		t.Fatalf("%d requests reached the cluster", n)
	}
}
