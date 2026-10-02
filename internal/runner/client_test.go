package runner_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/runner"
)

func newClient(t *testing.T, h http.HandlerFunc) *runner.Client {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return &runner.Client{BaseURL: ts.URL, Token: "tok", HTTP: ts.Client()}
}

func TestClientClaimReturnsNilOnNoContent(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	got, err := c.Claim(context.Background())
	if err != nil || got != nil {
		t.Fatalf("Claim = %+v, %v, want nil, nil on 204", got, err)
	}
}

func TestClientClaimDecodesRunAndSendsBearerToken(t *testing.T) {
	var auth, method string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		auth, method = r.Header.Get("Authorization"), r.Method
		_ = json.NewEncoder(w).Encode(run.Run{ID: "r1", Provider: "claude", Prompt: "hi", Status: run.Running})
	})

	got, err := c.Claim(context.Background())
	if err != nil || got == nil || got.ID != "r1" || got.Prompt != "hi" {
		t.Fatalf("Claim = %+v, %v", got, err)
	}
	if auth != "Bearer tok" || method != http.MethodPost {
		t.Fatalf("request = %s with Authorization %q", method, auth)
	}
}

func TestClientReportsUnexpectedStatusWithServerMessage(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"invalid runner token"}`)
	})

	if _, err := c.Claim(context.Background()); err == nil {
		t.Fatal("Claim accepted a 401")
	}
	if err := c.Event(context.Background(), "r1", "system", json.RawMessage(`{}`)); err == nil {
		t.Fatal("Event accepted a 401")
	}
	if err := c.Finish(context.Background(), "r1", run.Outcome{}); err == nil {
		t.Fatal("Finish accepted a 401")
	}
}

func TestClientEventAndFinishPostExpectedBodies(t *testing.T) {
	var paths []string
	var bodies []string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		paths, bodies = append(paths, r.URL.Path), append(bodies, string(b))
		w.WriteHeader(http.StatusNoContent)
	})

	if err := c.Event(context.Background(), "r1", "assistant", json.RawMessage(`{"n":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := c.Finish(context.Background(), "r1", run.Outcome{ExitCode: 2, Result: "boom"}); err != nil {
		t.Fatal(err)
	}

	if paths[0] != "/runner/v1/runs/r1/events" || paths[1] != "/runner/v1/runs/r1/finish" {
		t.Fatalf("paths = %v", paths)
	}
	var ev struct {
		Kind    string
		Payload json.RawMessage
	}
	if err := json.Unmarshal([]byte(bodies[0]), &ev); err != nil || ev.Kind != "assistant" || string(ev.Payload) != `{"n":1}` {
		t.Fatalf("event body = %s (%v)", bodies[0], err)
	}
	var out run.Outcome
	if err := json.Unmarshal([]byte(bodies[1]), &out); err != nil || out.ExitCode != 2 || out.Result != "boom" {
		t.Fatalf("finish body = %s (%v)", bodies[1], err)
	}
}
