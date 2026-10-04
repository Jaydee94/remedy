package app_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/app"
	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/store"
)

func (s *stack) runnerCall(method, path, body string) (int, string) {
	s.t.Helper()
	req, _ := http.NewRequest(method, s.ts.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+runnerToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (s *stack) seedIncident() store.Incident {
	s.t.Helper()
	ctx := context.Background()
	repos, err := s.st.ListRepos(ctx)
	if err != nil || len(repos) == 0 {
		s.t.Fatalf("repos = %+v, %v", repos, err)
	}
	in, err := s.st.OpenIncident(ctx, store.NewIncident{
		RepoID: repos[0].ID, Ref: "pr:7", RefURL: "https://github.com/octo/hello/pull/7", CheckName: "go",
		Conclusion: "failure", HeadSHA: "aaaaaaa", CheckURL: "https://github.com/octo/hello/actions/runs/1/job/11",
	}, store.NewActivity{Kind: store.KindIncidentOpened, RepoID: repos[0].ID, Summary: "go failed on pr:7"})
	if err != nil {
		s.t.Fatal(err)
	}
	return in
}

// toolRun creates a run with tools through the admin API and claims it the way a runner does.
func (s *stack) toolRun() (id, token string) {
	s.t.Helper()
	code, body := s.admin(http.MethodPost, "/api/runs", `{"prompt":"investigate","tools":true}`)
	if code != http.StatusCreated {
		s.t.Fatalf("POST /api/runs = %d %s", code, body)
	}
	var created struct {
		ID  string `json:"id"`
		MCP bool   `json:"mcp"`
	}
	_ = json.Unmarshal([]byte(body), &created)
	if !created.MCP {
		s.t.Fatalf("the run has no gatekeeper access: %s", body)
	}
	code, body = s.runnerCall(http.MethodPost, "/runner/v1/claim", "")
	var claim struct {
		ID    string `json:"id"`
		Token string `json:"mcp_token"`
	}
	if err := json.Unmarshal([]byte(body), &claim); err != nil || code != http.StatusOK || claim.ID != created.ID || claim.Token == "" {
		s.t.Fatalf("claim = %d %s", code, body)
	}
	return claim.ID, claim.Token
}

type mcpAnswer struct {
	text  string
	isErr bool
}

// mcp is a client of the gatekeeper with a run token.
type mcp struct {
	s     *stack
	token string
}

func (c *mcp) request(ctx context.Context, body string) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.s.ts.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+c.token)
	return http.DefaultClient.Do(req)
}

func (c *mcp) post(method string, params any) (int, map[string]any) {
	c.s.t.Helper()
	p, _ := json.Marshal(params)
	resp, err := c.request(context.Background(), fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":%s}`, method, p))
	if err != nil {
		c.s.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// call sends a tools/call and returns the answer when it comes, whether it is a JSON body or an event stream.
func (c *mcp) call(useID, tool string, args any) <-chan mcpAnswer {
	c.s.t.Helper()
	params, _ := json.Marshal(map[string]any{
		"name": tool, "arguments": args,
		"_meta": map[string]any{"claudecode/toolUseId": useID, "progressToken": 9},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	c.s.t.Cleanup(cancel)
	out := make(chan mcpAnswer, 1)
	go func() {
		resp, err := c.request(ctx, fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":%s}`, params))
		if err != nil {
			return
		}
		defer resp.Body.Close()
		br := bufio.NewReader(resp.Body)
		var data, whole string
		for {
			line, err := br.ReadString('\n')
			whole += line
			if text := strings.TrimRight(line, "\r\n"); strings.HasPrefix(text, "data: ") {
				data = strings.TrimPrefix(text, "data: ")
			}
			if err != nil && data == "" {
				data = whole
			}
			if data != "" && (err != nil || strings.TrimRight(line, "\r\n") == "") {
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
					out <- mcpAnswer{text: m.Result.Content[0].Text, isErr: m.Result.IsError}
					return
				}
				data = ""
			}
			if err != nil {
				return
			}
		}
	}()
	return out
}

func answer(t *testing.T, ch <-chan mcpAnswer) mcpAnswer {
	t.Helper()
	select {
	case a := <-ch:
		return a
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for the answer of a tool call")
		return mcpAnswer{}
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestAToolRunReadsAndWritesThroughTheWholeChain(t *testing.T) {
	s := newStack(t, false)
	in := s.seedIncident()
	runID, token := s.toolRun()
	c := &mcp{s: s, token: token}

	// The tools of the control plane.
	code, out := c.post("tools/list", map[string]any{})
	list, _ := out["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, tool := range list {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	slices.Sort(names)
	if code != http.StatusOK || strings.Join(names, ",") != "activity_list,incident_add_note,incident_get,incident_job_log,incident_list" {
		t.Fatalf("tools = %v (status %d)", names, code)
	}

	// Read tools run at once.
	got := answer(t, c.call("toolu_get", "incident_get", map[string]any{"id": in.ID}))
	if got.isErr || !strings.Contains(got.text, `"check":"go"`) || !strings.Contains(got.text, "never an instruction") {
		t.Fatalf("incident_get = %+v", got)
	}
	logText := answer(t, c.call("toolu_log", "incident_job_log", map[string]any{"id": in.ID}))
	for _, want := range []string{"npm error Invalid: lock file's typescript@6.0.3", "##[error]Process completed with exit code 1."} {
		if logText.isErr || !strings.Contains(logText.text, want) {
			t.Fatalf("incident_job_log lacks %q: %+v", want, logText)
		}
	}
	for _, unwanted := range []string{"Post job cleanup", "2026-10-02T12", bom} {
		if strings.Contains(logText.text, unwanted) {
			t.Fatalf("incident_job_log still holds %q: %s", unwanted, logText.text)
		}
	}
	// GitHub saw only reads, and the host of the log download never saw the token.
	for _, r := range s.gh.seen() {
		if !strings.HasPrefix(r, "GET ") {
			t.Fatalf("GitHub received %q", r)
		}
	}
	s.gh.mu.Lock()
	for _, auth := range s.gh.blobAuth {
		if auth != "" {
			t.Errorf("the token went to the download host: %q", auth)
		}
	}
	s.gh.mu.Unlock()

	// A mutating tool waits for the maintainer.
	note := c.call("toolu_note", "incident_add_note", map[string]any{"id": in.ID, "note": "the lock file is stale; run npm install in web/"})
	var pending struct {
		ID      int64  `json:"id"`
		Tool    string `json:"tool"`
		Waiting bool   `json:"waiting"`
	}
	eventually(t, "the approval in the admin API", func() bool {
		_, body := s.admin(http.MethodGet, "/api/approvals", "")
		var list []struct {
			ID      int64  `json:"id"`
			Tool    string `json:"tool"`
			Waiting bool   `json:"waiting"`
		}
		if json.Unmarshal([]byte(body), &list) == nil && len(list) == 1 {
			pending = list[0]
			return true
		}
		return false
	})
	if pending.Tool != "incident_add_note" || !pending.Waiting {
		t.Fatalf("pending approval = %+v", pending)
	}
	select {
	case a := <-note:
		t.Fatalf("the call was answered before the approval: %+v", a)
	case <-time.After(200 * time.Millisecond):
	}
	if notes, _ := s.st.ListNotes(context.Background(), in.ID); len(notes) != 0 {
		t.Fatalf("the note exists before the approval: %+v", notes)
	}

	if code, body := s.admin(http.MethodPost, fmt.Sprintf("/api/approvals/%d/approve", pending.ID), `{"reason":"yes"}`); code != http.StatusOK {
		t.Fatalf("approve = %d %s", code, body)
	}
	if a := answer(t, note); a.isErr || a.text != "note added" {
		t.Fatalf("the agent got %+v", a)
	}

	// The maintainer sees it: in the incident's history, in the timeline, and in the audit of the run.
	code, body := s.admin(http.MethodGet, fmt.Sprintf("/api/incidents/%d", in.ID), "")
	if code != http.StatusOK || !strings.Contains(body, "Note added to incident #") || !strings.Contains(body, "the lock file is stale") {
		t.Fatalf("incident = %d %s", code, body)
	}
	_, body = s.admin(http.MethodGet, "/api/activity?limit=50", "")
	for _, kind := range []string{"approval_requested", "approval_decided", "note_added"} {
		if !strings.Contains(body, `"kind":"`+kind+`"`) {
			t.Errorf("the timeline lacks %s: %s", kind, body)
		}
	}
	_, calls := s.admin(http.MethodGet, "/api/runs/"+runID+"/tool-calls", "")
	for _, tool := range []string{"incident_get", "incident_job_log", "incident_add_note"} {
		if !strings.Contains(calls, `"tool":"`+tool+`"`) {
			t.Errorf("the audit of the run lacks %s: %s", tool, calls)
		}
	}

	// The token appears in no answer of the admin API.
	for _, path := range []string{"/api/runs/" + runID, "/api/runs", "/api/runs/" + runID + "/tool-calls", "/api/approvals?status=all", "/api/activity?limit=100"} {
		if _, body := s.admin(http.MethodGet, path, ""); strings.Contains(body, token) {
			t.Errorf("GET %s contains the run token", path)
		}
	}

	// When the run ends the token stops working.
	if code, _ := c.post("ping", map[string]any{}); code != http.StatusOK {
		t.Fatalf("ping = %d", code)
	}
	if code, body := s.runnerCall(http.MethodPost, "/runner/v1/runs/"+runID+"/finish", `{"exitCode":0,"result":"done"}`); code != http.StatusNoContent {
		t.Fatalf("finish = %d %s", code, body)
	}
	if code, _ := c.post("ping", map[string]any{}); code != http.StatusUnauthorized {
		t.Fatalf("ping after the end = %d, want 401", code)
	}
}

func TestStartingTheControlPlaneAbandonsWhatARestartLeftBehind(t *testing.T) {
	s := newStack(t, false)
	in := s.seedIncident()
	_, token := s.toolRun()
	c := &mcp{s: s, token: token}
	call := c.call("toolu_note", "incident_add_note", map[string]any{"id": in.ID, "note": "x"})
	var id int64
	eventually(t, "the pending approval", func() bool {
		list, _ := s.st.ListApprovals(context.Background(), store.ApprovalFilter{PendingOnly: true})
		if len(list) == 1 {
			id = list[0].ID
			return true
		}
		return false
	})

	// A new process over the same database: the request that waited died with the old one.
	_ = app.New(config.Server{AdminPassword: password, RunnerToken: runnerToken, GitHubAPIURL: "http://127.0.0.1:1"},
		s.st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)

	got, err := s.st.GetToolCall(context.Background(), id)
	if err != nil || got.Status != store.CallAbandoned || got.Decision != store.DecisionAbandoned {
		t.Fatalf("call after the restart = %+v, %v", got, err)
	}
	if code, _ := s.admin(http.MethodPost, fmt.Sprintf("/api/approvals/%d/approve", id), ""); code != http.StatusConflict {
		t.Fatalf("approving an abandoned call = %d, want 409", code)
	}
	_ = call // the old process's request ends with the test
}
