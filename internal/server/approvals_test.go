package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

// gateEnv is a server with the gatekeeper, the incident and note tools, and a signed-in admin client.
type gateEnv struct {
	ts       *httptest.Server
	st       *store.Store
	g        *gatekeeper.Gatekeeper
	client   *http.Client
	incident store.Incident
}

func newGateEnv(t *testing.T) *gateEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	g := gatekeeper.New(gatekeeper.Config{
		Store: st, Tools: append(gatekeeper.IncidentTools(st), gatekeeper.NoteTool(st)),
		ProgressInterval: 20 * time.Millisecond, Grace: 200 * time.Millisecond,
	})
	ts := httptest.NewServer(server.New(server.Deps{
		Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Gatekeeper: g, Incidents: &incident.Engine{Store: st},
	}))
	t.Cleanup(ts.Close)

	ctx := context.Background()
	if err := st.SaveConnection(ctx, store.Connection{TokenCiphertext: []byte("sealed"), TokenHint: "wxyz", Login: "octo", Status: store.ConnOK, CheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	repo, err := st.AddRepo(ctx, store.ConnectionID, "octo/hello", "main")
	if err != nil {
		t.Fatal(err)
	}
	in, err := st.OpenIncident(ctx, store.NewIncident{
		RepoID: repo.ID, Ref: "pr:7", RefURL: "https://github.com/octo/hello/pull/7", CheckName: "go",
		Conclusion: "failure", HeadSHA: "abc1234", CheckURL: "https://github.com/octo/hello/runs/1",
	}, store.NewActivity{Kind: store.KindIncidentOpened, RepoID: repo.ID, Summary: "go failed on pr:7"})
	if err != nil {
		t.Fatal(err)
	}

	jar, _ := cookiejar.New(nil)
	e := &gateEnv{ts: ts, st: st, g: g, client: &http.Client{Jar: jar}, incident: in}
	if code, _ := e.admin(t, http.MethodPost, "/api/login", `{"password":"`+password+`"}`); code != http.StatusNoContent {
		t.Fatalf("login = %d", code)
	}
	return e
}

func (e *gateEnv) admin(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	req.Header.Set("X-Remedy-CSRF", "1")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (e *gateEnv) runner(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+runnerToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// toolRun creates a run with tools through the admin API, lets a runner claim it and returns its id and token.
func (e *gateEnv) toolRun(t *testing.T) (id, token string) {
	t.Helper()
	code, body := e.admin(t, http.MethodPost, "/api/runs", `{"prompt":"use the tools","tools":true}`)
	if code != http.StatusCreated || field(t, body, "mcp") != true {
		t.Fatalf("POST /api/runs = %d %s, want a created run with mcp", code, body)
	}
	id = field(t, body, "id").(string)
	code, body = e.runner(t, http.MethodPost, "/runner/v1/claim", "")
	if code != http.StatusOK || field(t, body, "id") != id {
		t.Fatalf("claim = %d %s", code, body)
	}
	token, _ = field(t, body, "mcp_token").(string)
	return id, token
}

func (e *gateEnv) mcpPost(t *testing.T, token, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.ts.URL+"/mcp", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

type mcpResult struct {
	text  string
	isErr bool
}

// asyncCall is a tools/call whose answer arrives later.
type asyncCall struct {
	done   chan mcpResult
	cancel context.CancelFunc
}

// callAsync sends a tools/call and reads its answer, JSON or event stream, in the background.
func (e *gateEnv) callAsync(t *testing.T, token, useID, tool string, args any) *asyncCall {
	t.Helper()
	params, _ := json.Marshal(map[string]any{
		"name": tool, "arguments": args,
		"_meta": map[string]any{"claudecode/toolUseId": useID, "progressToken": 3},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, e.ts.URL+"/mcp",
		strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":%s}`, params)))
	req.Header.Set("Authorization", "Bearer "+token)
	c := &asyncCall{done: make(chan mcpResult, 1), cancel: cancel}
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		br := bufio.NewReader(resp.Body)
		var data, whole string
		for {
			line, err := br.ReadString('\n')
			whole += line
			line = strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(line, "data: ") {
				data = strings.TrimPrefix(line, "data: ")
			}
			if (line == "" && data != "") || (err != nil && data == "" && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json")) {
				if data == "" {
					data = whole
				}
				var m struct {
					Method string `json:"method"`
					Result struct {
						Content []struct {
							Text string `json:"text"`
						} `json:"content"`
						IsError bool `json:"isError"`
					} `json:"result"`
				}
				if json.Unmarshal([]byte(data), &m) == nil && m.Method == "" && len(m.Result.Content) > 0 {
					c.done <- mcpResult{text: m.Result.Content[0].Text, isErr: m.Result.IsError}
					return
				}
				data = ""
			}
			if err != nil {
				return
			}
		}
	}()
	return c
}

func (c *asyncCall) wait(t *testing.T) mcpResult {
	t.Helper()
	select {
	case r := <-c.done:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the answer of a tool call")
		return mcpResult{}
	}
}

func (e *gateEnv) eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type approvalJSON struct {
	ID         int64           `json:"id"`
	RunID      string          `json:"runId"`
	IncidentID int64           `json:"incidentId"`
	Tool       string          `json:"tool"`
	Kind       string          `json:"kind"`
	Arguments  json.RawMessage `json:"arguments"`
	Status     string          `json:"status"`
	Decision   string          `json:"decision"`
	Reason     string          `json:"reason"`
	Result     string          `json:"result"`
	Error      string          `json:"error"`
	DecidedAt  string          `json:"decidedAt"`
	Waiting    bool            `json:"waiting"`
}

func (e *gateEnv) approvals(t *testing.T, query string) []approvalJSON {
	t.Helper()
	code, body := e.admin(t, http.MethodGet, "/api/approvals"+query, "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/approvals%s = %d %s", query, code, body)
	}
	var list []approvalJSON
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	return list
}

func (e *gateEnv) pendingApproval(t *testing.T) approvalJSON {
	t.Helper()
	var got approvalJSON
	e.eventually(t, "a pending approval in the API", func() bool {
		list := e.approvals(t, "")
		if len(list) > 0 {
			got = list[0]
			return true
		}
		return false
	})
	return got
}

func TestARunWithToolsGetsATokenWhenItIsClaimed(t *testing.T) {
	e := newGateEnv(t)
	_, token := e.toolRun(t)
	if len(token) != 43 {
		t.Fatalf("mcp_token = %q, want 43 characters", token)
	}

	// The token works at /mcp and sees the four tools of this server: three read tools and the note tool.
	code, out := e.mcpPost(t, token, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools, _ := out["result"].(map[string]any)["tools"].([]any)
	if code != http.StatusOK || len(tools) != 4 {
		t.Fatalf("tools/list = %d %v", code, out)
	}

	// A plain run gets no token, and the runner token is not a run token.
	if code, body := e.admin(t, http.MethodPost, "/api/runs", `{"prompt":"plain"}`); code != http.StatusCreated || field(t, body, "mcp") != nil {
		t.Fatalf("a plain run = %d %s", code, body)
	}
	code, body := e.runner(t, http.MethodPost, "/runner/v1/claim", "")
	if code != http.StatusOK || field(t, body, "mcp_token") != nil {
		t.Fatalf("claim of a plain run = %d %s, want no token", code, body)
	}
	if code, _ := e.mcpPost(t, runnerToken, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); code != http.StatusUnauthorized {
		t.Fatalf("the runner token at /mcp = %d, want 401", code)
	}
	if code, _ := e.mcpPost(t, "", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); code != http.StatusUnauthorized {
		t.Fatalf("no token at /mcp = %d, want 401", code)
	}
	// The admin session is no way in either.
	req, _ := http.NewRequest(http.MethodPost, e.ts.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if resp, err := e.client.Do(req); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the admin cookie at /mcp: %v %v, want 401", resp, err)
	}
}

func TestToolsNeedTheGatekeeper(t *testing.T) {
	e := newGHEnv(t, nil, ghKey(t, 1)) // a server without a gatekeeper
	if code, body := e.call(t, http.MethodPost, "/api/runs", `{"prompt":"x","tools":true}`); code != http.StatusBadRequest {
		t.Fatalf("POST /api/runs with tools = %d %s, want 400", code, body)
	}
	for _, path := range []string{"/api/approvals"} {
		if code, _ := e.call(t, http.MethodGet, path, ""); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s = %d without a gatekeeper, want it not to exist", path, code)
		}
	}
}

func TestTheApprovalRoutesNeedASession(t *testing.T) {
	e := newGateEnv(t)
	for method, path := range map[string]string{
		http.MethodGet:  "/api/approvals",
		http.MethodPost: "/api/approvals/1/approve",
	} {
		req, _ := http.NewRequest(method, e.ts.URL+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s without a session = %d, want 401", method, path, resp.StatusCode)
		}
	}
	// A state change needs the CSRF header.
	req, _ := http.NewRequest(http.MethodPost, e.ts.URL+"/api/approvals/1/deny", nil)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("deny without the CSRF header = %d, want 403", resp.StatusCode)
	}
}

func TestApprovingACallThroughTheAPI(t *testing.T) {
	e := newGateEnv(t)
	runID, token := e.toolRun(t)
	call := e.callAsync(t, token, "toolu_1", "incident_add_note", map[string]any{"id": e.incident.ID, "note": "the lock file is stale"})

	pending := e.pendingApproval(t)
	if pending.RunID != runID || pending.Tool != "incident_add_note" || pending.Kind != "mutating" || pending.IncidentID != e.incident.ID ||
		pending.Decision != "pending" || pending.Status != "waiting" || !pending.Waiting ||
		string(pending.Arguments) != fmt.Sprintf(`{"id":%d,"note":"the lock file is stale"}`, e.incident.ID) {
		t.Fatalf("pending approval = %+v", pending)
	}
	if code, body := e.admin(t, http.MethodGet, "/api/runs/"+runID, ""); code != http.StatusOK || field(t, body, "waitingApproval") != float64(pending.ID) {
		t.Fatalf("GET /api/runs/%s = %d %s, want waitingApproval %d", runID, code, body, pending.ID)
	}

	code, body := e.admin(t, http.MethodPost, fmt.Sprintf("/api/approvals/%d/approve", pending.ID), `{"reason":"looks right"}`)
	if code != http.StatusOK || field(t, body, "decision") != "approved" || field(t, body, "reason") != "looks right" {
		t.Fatalf("approve = %d %s", code, body)
	}
	if r := call.wait(t); r.isErr || r.text != "note added" {
		t.Fatalf("the agent got %+v", r)
	}

	if list := e.approvals(t, ""); len(list) != 0 {
		t.Fatalf("pending approvals after the decision = %+v", list)
	}
	all := e.approvals(t, "?status=all")
	if len(all) != 1 || all[0].Decision != "approved" || all[0].Status != "succeeded" || all[0].Result != "note added" || all[0].DecidedAt == "" || all[0].Waiting {
		t.Fatalf("all approvals = %+v", all)
	}
	if code, body := e.admin(t, http.MethodGet, "/api/runs/"+runID, ""); code != http.StatusOK || field(t, body, "waitingApproval") != nil {
		t.Fatalf("GET /api/runs/%s = %d %s, want no waitingApproval", runID, code, body)
	}
	if notes, _ := e.st.ListNotes(context.Background(), e.incident.ID); len(notes) != 1 || notes[0].Note != "the lock file is stale" {
		t.Fatalf("notes = %+v", notes)
	}

	code, body = e.admin(t, http.MethodGet, "/api/runs/"+runID+"/tool-calls", "")
	var calls []approvalJSON
	if err := json.Unmarshal([]byte(body), &calls); err != nil || code != http.StatusOK || len(calls) != 1 || calls[0].Tool != "incident_add_note" {
		t.Fatalf("tool-calls = %d %s", code, body)
	}
	if code, _ := e.admin(t, http.MethodGet, "/api/runs/no-such-run/tool-calls", ""); code != http.StatusNotFound {
		t.Fatalf("tool-calls of an unknown run = %d, want 404", code)
	}
}

func TestDenyingACallAndTheErrorsOfTheRoutes(t *testing.T) {
	e := newGateEnv(t)
	_, token := e.toolRun(t)
	call := e.callAsync(t, token, "toolu_1", "incident_add_note", map[string]any{"id": e.incident.ID, "note": "x"})
	pending := e.pendingApproval(t)
	path := func(verb string) string { return fmt.Sprintf("/api/approvals/%d/%s", pending.ID, verb) }

	for name, body := range map[string]string{
		"a reason that is too long": `{"reason":"` + strings.Repeat("x", 501) + `"}`,
		"a body that is not JSON":   `{`,
	} {
		if code, _ := e.admin(t, http.MethodPost, path("deny"), body); code != http.StatusBadRequest {
			t.Errorf("deny with %s = %d, want 400", name, code)
		}
	}
	if code, _ := e.admin(t, http.MethodGet, "/api/approvals?status=bogus", ""); code != http.StatusBadRequest {
		t.Errorf("an unknown status = %d, want 400", code)
	}
	if code, _ := e.admin(t, http.MethodPost, "/api/approvals/9999/approve", ""); code != http.StatusNotFound {
		t.Errorf("an unknown approval = %d, want 404", code)
	}
	if code, _ := e.admin(t, http.MethodPost, "/api/approvals/abc/approve", ""); code != http.StatusNotFound {
		t.Errorf("an approval id that is not a number = %d, want 404", code)
	}

	// An empty body is fine: the reason is optional.
	if code, body := e.admin(t, http.MethodPost, path("deny"), ""); code != http.StatusOK || field(t, body, "decision") != "denied" {
		t.Fatalf("deny = %d %s", code, body)
	}
	if r := call.wait(t); !r.isErr || r.text != "denied" {
		t.Fatalf("the agent got %+v", r)
	}
	for _, verb := range []string{"approve", "deny"} {
		if code, _ := e.admin(t, http.MethodPost, path(verb), ""); code != http.StatusConflict {
			t.Errorf("%s after the decision = %d, want 409", verb, code)
		}
	}
	if notes, _ := e.st.ListNotes(context.Background(), e.incident.ID); len(notes) != 0 {
		t.Fatalf("a denied call changed something: %+v", notes)
	}
}

func TestADecisionForACallThatNobodyWaitsForIs409(t *testing.T) {
	e := newGateEnv(t)
	_, token := e.toolRun(t)
	call := e.callAsync(t, token, "toolu_1", "incident_add_note", map[string]any{"id": e.incident.ID, "note": "x"})
	pending := e.pendingApproval(t)

	call.cancel() // the agent goes away
	e.eventually(t, "the call to have no waiter", func() bool { return !e.g.Waiting(pending.ID) })
	code, body := e.admin(t, http.MethodPost, fmt.Sprintf("/api/approvals/%d/approve", pending.ID), "")
	if code != http.StatusConflict || !strings.Contains(body, "no longer waiting") {
		t.Fatalf("approve = %d %s, want 409 and the reason", code, body)
	}
}

func TestCancellingARun(t *testing.T) {
	e := newGateEnv(t)

	// A queued run ends at once.
	_, body := e.admin(t, http.MethodPost, "/api/runs", `{"prompt":"x","tools":true}`)
	queued := field(t, body, "id").(string)
	if code, _ := e.admin(t, http.MethodPost, "/api/runs/"+queued+"/cancel", ""); code != http.StatusNoContent {
		t.Fatalf("cancel of a queued run = %d, want 204", code)
	}
	if _, body := e.admin(t, http.MethodGet, "/api/runs/"+queued, ""); field(t, body, "status") != "failed" || field(t, body, "failureReason") != "cancelled" {
		t.Fatalf("the cancelled run = %s", body)
	}

	// A running run with tools: its waiting call is abandoned and its token stops working.
	runID, token := e.toolRun(t)
	call := e.callAsync(t, token, "toolu_1", "incident_add_note", map[string]any{"id": e.incident.ID, "note": "x"})
	e.pendingApproval(t)
	if code, _ := e.admin(t, http.MethodPost, "/api/runs/"+runID+"/cancel", ""); code != http.StatusNoContent {
		t.Fatalf("cancel of a running run = %d, want 204", code)
	}
	if code, _ := e.admin(t, http.MethodPost, "/api/runs/"+runID+"/cancel", ""); code != http.StatusNoContent {
		t.Fatalf("a second cancel = %d, want 204", code)
	}
	if r := call.wait(t); !r.isErr || !strings.Contains(r.text, "abandoned") {
		t.Fatalf("the waiting agent got %+v", r)
	}
	if _, body := e.admin(t, http.MethodGet, "/api/runs/"+runID, ""); field(t, body, "cancelRequested") != true || field(t, body, "status") != "running" {
		t.Fatalf("the cancelled running run = %s, want it running with the request recorded", body)
	}
	if code, _ := e.mcpPost(t, token, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); code != http.StatusUnauthorized {
		t.Fatalf("the token of a cancelled run = %d, want 401", code)
	}
	if list := e.approvals(t, "?status=all"); len(list) != 1 || list[0].Decision != "abandoned" {
		t.Fatalf("approvals = %+v, want the abandoned one", list)
	}

	// The runner reports the end, as cancelled.
	if code, body := e.runner(t, http.MethodPost, "/runner/v1/runs/"+runID+"/finish", `{"exitCode":-1,"failureReason":"cancelled"}`); code != http.StatusNoContent {
		t.Fatalf("finish as cancelled = %d %s", code, body)
	}
	if _, body := e.admin(t, http.MethodGet, "/api/runs/"+runID, ""); field(t, body, "status") != "failed" || field(t, body, "failureReason") != "cancelled" {
		t.Fatalf("the finished run = %s", body)
	}

	// What cannot be cancelled, and what does not exist.
	if code, _ := e.admin(t, http.MethodPost, "/api/runs/"+runID+"/cancel", ""); code != http.StatusConflict {
		t.Errorf("cancel of an ended run = %d, want 409", code)
	}
	if code, _ := e.admin(t, http.MethodPost, "/api/runs/no-such-run/cancel", ""); code != http.StatusNotFound {
		t.Errorf("cancel of an unknown run = %d, want 404", code)
	}
	_, body = e.admin(t, http.MethodPost, "/api/runs", `{"prompt":"plain"}`)
	plain := field(t, body, "id").(string)
	e.runner(t, http.MethodPost, "/runner/v1/claim", "")
	if code, _ := e.admin(t, http.MethodPost, "/api/runs/"+plain+"/cancel", ""); code != http.StatusConflict {
		t.Errorf("cancel of a running run without tools = %d, want 409", code)
	}
}

func TestTheTokenStopsWorkingWhenTheRunnerFinishesTheRun(t *testing.T) {
	e := newGateEnv(t)
	runID, token := e.toolRun(t)
	if code, _ := e.mcpPost(t, token, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); code != http.StatusOK {
		t.Fatalf("ping = %d", code)
	}
	if code, body := e.runner(t, http.MethodPost, "/runner/v1/runs/"+runID+"/finish", `{"exitCode":0,"result":"done"}`); code != http.StatusNoContent {
		t.Fatalf("finish = %d %s", code, body)
	}
	if code, _ := e.mcpPost(t, token, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); code != http.StatusUnauthorized {
		t.Fatalf("ping after the end = %d, want 401", code)
	}
	if code, _ := e.runner(t, http.MethodPost, "/runner/v1/runs/"+runID+"/finish", `{"exitCode":0,"failureReason":"something else"}`); code != http.StatusBadRequest {
		t.Fatalf("an unknown failure reason = %d, want 400", code)
	}
}
