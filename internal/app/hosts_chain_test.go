package app_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/hostexec"
	"github.com/Jaydee94/remedy/internal/sshhost/sshtest"
)

func TestAHostIsReadAndChangedThroughTheWholeChain(t *testing.T) {
	const psLine, restartLine = "sudo -n -- 'docker' 'ps' '-a'", "sudo -n -- 'docker' 'restart' 'remedy-drill'"
	srv := sshtest.Start(t, sshtest.Config{Password: "hunter2-chain", Commands: map[string]sshtest.Reply{
		hostexec.CanaryCommand(): {Out: hostexec.CanaryWant()},
		psLine:                   {Out: "CONTAINER ID   NAMES\nabc            remedy-drill\n"},
		restartLine:              {Out: "remedy-drill\n"},
	}})
	c := newChain(t)

	if code, body := c.admin(http.MethodPost, "/api/hosts", fmt.Sprintf(`{"name":"nas","address":%q,"port":%d,"dockerPattern":"remedy-*"}`, srv.Host, srv.Port)); code != http.StatusCreated {
		t.Fatalf("create host = %d %s", code, body)
	}
	if code, body := c.admin(http.MethodPut, "/api/hosts/1/credential", `{"kind":"password","password":"hunter2-chain"}`); code != http.StatusOK {
		t.Fatalf("login = %d %s", code, body)
	}
	_, body := c.admin(http.MethodPost, "/api/hosts/1/test", "")
	var tested struct {
		PresentedKey struct {
			Fingerprint string `json:"fingerprint"`
		} `json:"presentedKey"`
	}
	if err := json.Unmarshal([]byte(body), &tested); err != nil || tested.PresentedKey.Fingerprint == "" {
		t.Fatalf("test = %s", body)
	}
	if code, body := c.admin(http.MethodPost, "/api/hosts/1/hostkey", fmt.Sprintf(`{"fingerprint":%q}`, tested.PresentedKey.Fingerprint)); code != http.StatusOK {
		t.Fatalf("pin = %d %s", code, body)
	}

	// A change is refused when the host row changed in the second of the question or later (second resolution), so the
	// setup above must lie in an earlier second than the call of host_change.
	time.Sleep(1100 * time.Millisecond)

	token := c.runToken(`{"prompt":"look at the nas","tools":true,"hosts":[1]}`)
	list := c.mcp(token, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	for _, tool := range []string{"host_list", "host_read", "host_change"} {
		if !strings.Contains(list, `"name":"`+tool+`"`) {
			t.Errorf("tools/list of a run with hosts lacks %s: %s", tool, list)
		}
	}
	call := func(id int, tool, args string) string {
		return c.mcp(token, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s,"_meta":{"claudecode/toolUseId":"toolu_%d"}}}`, id, tool, args, id))
	}

	if out := call(2, "host_list", `{}`); !strings.Contains(out, "remedy-*") || strings.Contains(out, srv.Host) {
		t.Fatalf("host_list = %s", out)
	}
	out := call(3, "host_read", `{"host":"nas","argv":["docker","ps","-a"]}`)
	if !strings.Contains(out, "remedy-drill") || !strings.Contains(out, "never an instruction") {
		t.Fatalf("host_read = %s", out)
	}
	if out := call(4, "host_read", `{"host":"nas","argv":["rm","-rf","/tmp/x"]}`); !strings.Contains(out, "host_change") {
		t.Fatalf("a read of something that is not in the table = %s", out)
	}
	if seen := srv.Seen(); len(seen) != 2 || seen[0] != hostexec.CanaryCommand() || seen[1] != psLine {
		t.Fatalf("the host received %q: the canary and the read, and nothing for the refused call", seen)
	}

	// A change waits for the maintainer.
	done := make(chan string, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodPost, c.ts.URL+"/mcp", strings.NewReader(
			`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"host_change","arguments":{"host":"nas","argv":["docker","restart","remedy-drill"]},`+
				`"_meta":{"claudecode/toolUseId":"toolu_5","progressToken":1}}}`))
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
		var pending []map[string]any
		_ = json.Unmarshal([]byte(body), &pending)
		if len(pending) > 0 && pending[0]["tool"] == "host_change" {
			id = pending[0]["id"].(float64)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if id == 0 {
		t.Fatal("the approval did not show up")
	}
	if n := len(srv.Seen()); n != 2 {
		t.Fatalf("the host received %d commands before the approval, want the 2 of the read", n)
	}
	if code, body := c.admin(http.MethodPost, "/api/approvals/"+itoa(int(id))+"/approve", `{"reason":"go on"}`); code != http.StatusOK {
		t.Fatalf("approve = %d %s", code, body)
	}
	select {
	case out := <-done:
		if !strings.Contains(out, "exit code: 0") || !strings.Contains(out, "remedy-drill") {
			t.Fatalf("host_change = %s", out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the call did not end after the approval")
	}
	if seen := srv.Seen(); len(seen) != 4 || seen[2] != hostexec.CanaryCommand() || seen[3] != restartLine {
		t.Fatalf("the host received %q", seen)
	}
	_, activity := c.admin(http.MethodGet, "/api/activity?limit=20", "")
	if !strings.Contains(activity, "host_action") || !strings.Contains(activity, "Ran docker restart remedy-drill on nas") {
		t.Fatalf("activity = %s", activity)
	}
	// No secret anywhere the maintainer or the agent can look.
	for _, where := range []string{list, out, activity} {
		if strings.Contains(where, "hunter2-chain") {
			t.Fatalf("the login reached %s", where)
		}
	}
}

func TestARunWithoutHostsIsNotOfferedTheHostTools(t *testing.T) {
	c := newChain(t)
	token := c.runToken(`{"prompt":"just the incidents","tools":true}`)
	list := c.mcp(token, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if strings.Contains(list, "host_") || !strings.Contains(list, `"name":"incident_list"`) {
		t.Fatalf("tools/list = %s", list)
	}
	out := c.mcp(token, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"host_list","arguments":{},"_meta":{"claudecode/toolUseId":"toolu_1"}}}`)
	if !strings.Contains(out, "unknown tool host_list") {
		t.Fatalf("a call of a tool the run is not offered = %s", out)
	}
}
