package gatekeeper_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

// echoTool is a read tool that returns its text and counts how often it ran.
func echoTool(runs *atomic.Int32) gatekeeper.Tool {
	return gatekeeper.Tool{
		Name:        "echo",
		Description: "Returns its text.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`),
		Decode: func(raw json.RawMessage) (json.RawMessage, error) {
			var a struct {
				Text string `json:"text"`
			}
			if err := gatekeeper.DecodeArgs(raw, &a); err != nil {
				return nil, err
			}
			if a.Text == "" {
				return nil, gatekeeper.ArgumentError("text must not be empty")
			}
			return json.Marshal(a)
		},
		Run: func(_ context.Context, c gatekeeper.Call) (string, error) {
			runs.Add(1)
			var a struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(c.Args, &a)
			return a.Text, nil
		},
	}
}

// failingTool is a read tool whose handler fails with an internal error.
func failingTool() gatekeeper.Tool {
	return gatekeeper.Tool{
		Name:        "boom",
		Description: "Always fails.",
		Schema:      json.RawMessage(`{"type":"object","additionalProperties":false}`),
		Decode: func(raw json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{}`), gatekeeper.DecodeArgs(raw, &struct{}{})
		},
		Run: func(context.Context, gatekeeper.Call) (string, error) {
			return "", errors.New("database is locked at /var/lib/remedy/remedy.db")
		},
	}
}

type env struct {
	st    *store.Store
	ts    *httptest.Server
	run   run.Run
	token string
	runs  *atomic.Int32
	next  int
}

func newEnv(t *testing.T, extra ...gatekeeper.Tool) *env {
	t.Helper()
	return newEnvWith(t, func(*store.Store) []gatekeeper.Tool { return extra })
}

// newEnvWith builds the tools after the store exists, and lets a test change the configuration.
func newEnvWith(t *testing.T, build func(st *store.Store) []gatekeeper.Tool, opts ...func(*gatekeeper.Config)) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	e := &env{st: st, runs: &atomic.Int32{}}
	cfg := gatekeeper.Config{Store: st, Tools: append([]gatekeeper.Tool{echoTool(e.runs), failingTool()}, build(st)...)}
	for _, opt := range opts {
		opt(&cfg)
	}
	e.ts = httptest.NewServer(gatekeeper.New(cfg))
	t.Cleanup(e.ts.Close)

	ctx := context.Background()
	if _, err := st.CreateToolRun(ctx, "claude", "use the tools"); err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimNext(ctx)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimNext = %+v, %v", claimed, err)
	}
	e.run = *claimed
	if e.token, err = st.MintRunToken(ctx, claimed.ID); err != nil {
		t.Fatal(err)
	}
	return e
}

// post sends a JSON-RPC request with the given token and returns the status and the decoded answer.
func (e *env) post(t *testing.T, token string, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.ts.URL, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	if buf.Len() > 0 {
		if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
			t.Fatalf("body %q: %v", buf.String(), err)
		}
	}
	return resp.StatusCode, out
}

// rpc sends a request with the run's token.
func (e *env) rpc(t *testing.T, method string, params any) (int, map[string]any) {
	t.Helper()
	e.next++
	p, _ := json.Marshal(params)
	return e.post(t, e.token, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, e.next, method, p))
}

// call is a tools/call with a tool use ID, the way the CLI sends it.
func (e *env) call(t *testing.T, useID, tool string, args any) map[string]any {
	t.Helper()
	status, out := e.rpc(t, "tools/call", map[string]any{
		"name": tool, "arguments": args,
		"_meta": map[string]any{"claudecode/toolUseId": useID, "progressToken": 7},
	})
	if status != http.StatusOK {
		t.Fatalf("tools/call = %d %v", status, out)
	}
	return out
}

// resultText returns the text and the isError flag of a tools/call answer.
func resultText(t *testing.T, out map[string]any) (string, bool) {
	t.Helper()
	res, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", out)
	}
	content, _ := res["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content = %v", res["content"])
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	isErr, _ := res["isError"].(bool)
	return text, isErr
}

func TestTheHandshakeTheCLIPerforms(t *testing.T) {
	e := newEnv(t)

	status, out := e.rpc(t, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}})
	res, _ := out["result"].(map[string]any)
	info, _ := res["serverInfo"].(map[string]any)
	if status != http.StatusOK || res["protocolVersion"] != "2025-06-18" || info["name"] != "remedy" || res["capabilities"] == nil {
		t.Fatalf("initialize = %d %v, want the client's protocol version echoed", status, out)
	}
	if out["id"] != float64(1) || out["jsonrpc"] != "2.0" {
		t.Fatalf("the answer does not carry the request id: %v", out)
	}

	if status, _ := e.post(t, e.token, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); status != http.StatusAccepted {
		t.Fatalf("a notification = %d, want 202", status)
	}
	if status, out := e.rpc(t, "ping", map[string]any{}); status != http.StatusOK || out["result"] == nil {
		t.Fatalf("ping = %d %v", status, out)
	}

	status, out = e.rpc(t, "tools/list", map[string]any{})
	res, _ = out["result"].(map[string]any)
	tools, _ := res["tools"].([]any)
	if status != http.StatusOK || len(tools) != 2 {
		t.Fatalf("tools/list = %d %v", status, out)
	}
	first, _ := tools[0].(map[string]any)
	if first["name"] != "echo" || first["description"] == "" || first["inputSchema"] == nil {
		t.Fatalf("first tool = %v", first)
	}

	status, out = e.rpc(t, "server/discover", map[string]any{})
	errObj, _ := out["error"].(map[string]any)
	if status != http.StatusOK || errObj["code"] != float64(-32601) {
		t.Fatalf("an unknown method = %d %v, want a method-not-found error", status, out)
	}
}

func TestOtherHTTPMethodsAndBadRequests(t *testing.T) {
	e := newEnv(t)
	for method, want := range map[string]int{http.MethodGet: http.StatusMethodNotAllowed, http.MethodDelete: http.StatusOK, http.MethodPut: http.StatusMethodNotAllowed} {
		req, _ := http.NewRequest(method, e.ts.URL, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%s = %d, want %d", method, resp.StatusCode, want)
		}
	}
	for name, body := range map[string]string{
		"not JSON":     `{`,
		"a batch":      `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`,
		"no method":    `{"jsonrpc":"2.0","id":1}`,
		"wrong rpc":    `{"jsonrpc":"1.0","id":1,"method":"ping"}`,
		"empty object": `{}`,
	} {
		if status, out := e.post(t, e.token, body); status != http.StatusBadRequest || out["error"] == nil {
			t.Errorf("%s = %d %v, want 400 and an error", name, status, out)
		}
	}
}

func TestTheRunTokenIsRequired(t *testing.T) {
	e := newEnv(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	for name, token := range map[string]string{"none": "", "wrong": "not-the-token", "almost": e.token + "x"} {
		if status, _ := e.post(t, token, body); status != http.StatusUnauthorized {
			t.Errorf("token %s = %d, want 401", name, status)
		}
	}
	if status, _ := e.post(t, e.token, body); status != http.StatusOK {
		t.Fatalf("the right token = %d", status)
	}

	if err := e.st.FinishRun(context.Background(), e.run.ID, run.Outcome{Result: "ok"}); err != nil {
		t.Fatal(err)
	}
	if status, _ := e.post(t, e.token, body); status != http.StatusUnauthorized {
		t.Fatalf("the token of an ended run = %d, want 401", status)
	}
}

func TestAReadToolCallIsRunRedactedAndAudited(t *testing.T) {
	e := newEnv(t)
	secret := "ghp_" + strings.Repeat("a1B2c3", 6)

	text, isErr := resultText(t, e.call(t, "toolu_1", "echo", map[string]any{"text": "the key is " + secret}))
	if isErr || strings.Contains(text, secret) || !strings.Contains(text, "the key is") {
		t.Fatalf("result = %q (error %v), want the text with the secret removed", text, isErr)
	}

	calls, err := e.st.ListToolCalls(context.Background(), e.run.ID)
	if err != nil || len(calls) != 1 {
		t.Fatalf("calls = %+v, %v", calls, err)
	}
	c := calls[0]
	if c.Tool != "echo" || c.Kind != store.CallKindRead || c.Status != store.CallSucceeded || c.ToolUseID != "toolu_1" || c.FinishedAt == nil {
		t.Fatalf("audit row = %+v", c)
	}
	if strings.Contains(c.Result, secret) || c.Result != text {
		t.Fatalf("the audit row holds %q, want the redacted text %q", c.Result, text)
	}
	if string(c.Arguments) != `{"text":"the key is `+secret+`"}` {
		t.Fatalf("the arguments are stored as validated: %s", c.Arguments)
	}
}

func TestACallThatCannotRunIsAnErrorResultAndAudited(t *testing.T) {
	e := newEnv(t)
	for name, tc := range map[string]struct {
		tool    string
		args    any
		message string
	}{
		"unknown tool":   {"nothing", map[string]any{}, "unknown tool"},
		"unknown member": {"echo", map[string]any{"text": "x", "extra": 1}, "extra"},
		"wrong type":     {"echo", map[string]any{"text": 5}, "text"},
		"missing":        {"echo", map[string]any{}, "text must not be empty"},
		"not an object":  {"echo", []int{1}, "arguments"},
		"internal error": {"boom", map[string]any{}, "the tool failed"},
	} {
		text, isErr := resultText(t, e.call(t, "toolu_"+strings.ReplaceAll(name, " ", "_"), tc.tool, tc.args))
		if !isErr || !strings.Contains(text, tc.message) {
			t.Errorf("%s: result %q (error %v), want an error mentioning %q", name, text, isErr, tc.message)
		}
		if strings.Contains(text, "/var/lib") {
			t.Errorf("%s: the answer leaks an internal error: %q", name, text)
		}
	}
	calls, _ := e.st.ListToolCalls(context.Background(), e.run.ID)
	if len(calls) != 6 {
		t.Fatalf("%d audit rows, want 6 (every call is audited)", len(calls))
	}
	for _, c := range calls {
		if c.Status != store.CallFailed || c.Error == "" || c.Decision != "" {
			t.Errorf("row %+v, want a failed read call with an error", c)
		}
	}
	if e.runs.Load() != 0 {
		t.Fatalf("the echo tool ran %d times for calls that were refused", e.runs.Load())
	}
}

func TestACallWithoutAToolUseIDIsRefused(t *testing.T) {
	e := newEnv(t)
	status, out := e.rpc(t, "tools/call", map[string]any{"name": "echo", "arguments": map[string]any{"text": "x"}})
	text, isErr := resultText(t, out)
	if status != http.StatusOK || !isErr || !strings.Contains(text, "toolUseId") {
		t.Fatalf("answer = %d %q (error %v)", status, text, isErr)
	}
	if calls, _ := e.st.ListToolCalls(context.Background(), e.run.ID); len(calls) != 0 {
		t.Fatalf("a call without an identity was audited: %+v", calls)
	}
}

func TestARepeatOfACallRunsNothingAndGetsTheFirstAnswer(t *testing.T) {
	e := newEnv(t)
	first, _ := resultText(t, e.call(t, "toolu_1", "echo", map[string]any{"text": "one"}))
	// The same tool use ID again, even with other arguments: it is the first call.
	again, isErr := resultText(t, e.call(t, "toolu_1", "echo", map[string]any{"text": "two"}))
	if isErr || again != first || first != "one" {
		t.Fatalf("repeat = %q (error %v), first = %q", again, isErr, first)
	}
	if e.runs.Load() != 1 {
		t.Fatalf("the tool ran %d times, want 1", e.runs.Load())
	}
	if calls, _ := e.st.ListToolCalls(context.Background(), e.run.ID); len(calls) != 1 {
		t.Fatalf("%d audit rows, want 1", len(calls))
	}

	// A repeat of a failed call gets the same error.
	_, _ = resultText(t, e.call(t, "toolu_bad", "echo", map[string]any{}))
	text, isErr := resultText(t, e.call(t, "toolu_bad", "echo", map[string]any{"text": "now valid"}))
	if !isErr || !strings.Contains(text, "text must not be empty") {
		t.Fatalf("repeat of a failed call = %q (error %v)", text, isErr)
	}
}

func TestAResultIsLimited(t *testing.T) {
	long := gatekeeper.Tool{
		Name: "long", Description: "A long result.", Schema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		Decode: func(raw json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{}`), gatekeeper.DecodeArgs(raw, &struct{}{})
		},
		Run: func(context.Context, gatekeeper.Call) (string, error) {
			return strings.Repeat("é", gatekeeper.MaxResultBytes), nil // two bytes per character
		},
	}
	e := newEnv(t, long)
	text, isErr := resultText(t, e.call(t, "toolu_1", "long", map[string]any{}))
	if isErr || len(text) > gatekeeper.MaxResultBytes+100 || !strings.Contains(text, "cut") {
		t.Fatalf("a %d byte result of a long tool: error %v, tail %q", len(text), isErr, text[max(0, len(text)-60):])
	}
	if strings.ContainsRune(text, '�') {
		t.Fatal("the limit cut a character in two")
	}
	calls, _ := e.st.ListToolCalls(context.Background(), e.run.ID)
	if len(calls) != 1 || len(calls[0].Result) > gatekeeper.MaxResultBytes+100 {
		t.Fatalf("the audit row holds %d bytes", len(calls[0].Result))
	}
}

func TestARunMayMakeOnlyAHundredCalls(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < store.MaxCallsPerRun; i++ {
		if _, isErr := resultText(t, e.call(t, "toolu_"+strconv.Itoa(i), "echo", map[string]any{"text": "x"})); isErr {
			t.Fatalf("call %d failed", i)
		}
	}
	text, isErr := resultText(t, e.call(t, "toolu_over", "echo", map[string]any{"text": "x"}))
	if !isErr || !strings.Contains(text, "limit") {
		t.Fatalf("call %d = %q (error %v), want the limit", store.MaxCallsPerRun+1, text, isErr)
	}
}

func TestNewRefusesAnUnusableRegistry(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	runs := &atomic.Int32{}
	for name, tools := range map[string][]gatekeeper.Tool{
		"duplicate": {echoTool(runs), echoTool(runs)},
		"no name":   {{Description: "x", Decode: echoTool(runs).Decode, Run: echoTool(runs).Run}},
		"no run":    {{Name: "x", Decode: echoTool(runs).Decode}},
		"bad name":  {func() gatekeeper.Tool { t := echoTool(runs); t.Name = "Bad Name"; return t }()},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: New accepted the registry", name)
				}
			}()
			gatekeeper.New(gatekeeper.Config{Store: st, Tools: tools})
		}()
	}
}
