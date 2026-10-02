package runner_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/runner"
	"github.com/Jaydee94/remedy/internal/testutil"
)

// tarGz builds a gzipped tar with a top-level directory, like GitHub's archives. The names are used as given.
func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Name: "Octo-hello-abc1234/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(tw, body)
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

// stubControlPlane answers the runner API for one claimed run and records what the runner does.
type stubControlPlane struct {
	mu            sync.Mutex
	events        []recordedEvent
	snapshotAuth  string
	snapshotCalls int
	finished      chan run.Outcome
}

func startStub(t *testing.T, claim run.Claim, snapshot http.HandlerFunc, workspaces string, providers map[string]provider.Provider) *stubControlPlane {
	t.Helper()
	s := &stubControlPlane{finished: make(chan run.Outcome, 1)}
	var claimed sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("POST /runner/v1/claim", func(w http.ResponseWriter, _ *http.Request) {
		first := false
		claimed.Do(func() { first = true })
		if first {
			_ = json.NewEncoder(w).Encode(claim)
			return
		}
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /runner/v1/runs/{id}/snapshot", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.snapshotAuth = r.Header.Get("Authorization")
		s.snapshotCalls++
		s.mu.Unlock()
		snapshot(w, r)
	})
	mux.HandleFunc("POST /runner/v1/runs/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		var e struct {
			Kind    string          `json:"kind"`
			Payload json.RawMessage `json:"payload"`
		}
		_ = json.NewDecoder(r.Body).Decode(&e)
		s.mu.Lock()
		s.events = append(s.events, recordedEvent{e.Kind, string(e.Payload)})
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /runner/v1/runs/{id}/finish", func(w http.ResponseWriter, r *http.Request) {
		var o run.Outcome
		_ = json.NewDecoder(r.Body).Decode(&o)
		s.finished <- o
		w.WriteHeader(http.StatusNoContent)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	loop := &runner.Loop{
		Client:        &runner.Client{BaseURL: ts.URL, Token: "tok", HTTP: ts.Client()},
		Providers:     providers,
		WorkspaceRoot: workspaces,
		Env:           os.Environ(),
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Backoff:       50 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { loop.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return s
}

func (s *stubControlPlane) outcome(t *testing.T) run.Outcome {
	t.Helper()
	select {
	case o := <-s.finished:
		return o
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not finish")
		return run.Outcome{}
	}
}

func (s *stubControlPlane) probe(t *testing.T) (args, files string, ok bool) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.events {
		if e.Kind == "probe" {
			var p struct{ Args, Files string }
			if err := json.Unmarshal([]byte(e.Payload), &p); err != nil {
				t.Fatal(err)
			}
			return p.Args, p.Files, true
		}
	}
	return "", "", false
}

func responderClaim() run.Claim {
	return run.Claim{
		Run:      run.Run{ID: "resp1", Provider: "claude", Prompt: "diagnose", Role: run.RoleResponder, Status: run.Running},
		Schema:   json.RawMessage(`{"type":"object","properties":{}}`),
		Snapshot: true,
	}
}

func structuredProvider(t *testing.T, output string) map[string]provider.Provider {
	return map[string]provider.Provider{"claude": provider.Claude{Binary: testutil.FakeClaudeStructured(t, output)}}
}

func TestAResponderRunGetsTheSnapshotAndTheSchema(t *testing.T) {
	archive := tarGz(t, map[string]string{
		"Octo-hello-abc1234/main.go":   "package main\n",
		"Octo-hello-abc1234/README.md": "# hello\n",
		"Octo-hello-abc1234/.env":      "TOKEN=x",
	})
	stub := startStub(t, responderClaim(), func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) },
		t.TempDir(), structuredProvider(t, `{"summary":"found it"}`))

	out := stub.outcome(t)
	if out.ExitCode != 0 || string(out.Output) != `{"summary":"found it"}` {
		t.Fatalf("outcome = %+v, output %s", out, out.Output)
	}
	args, files, ok := stub.probe(t)
	if !ok {
		t.Fatal("the CLI never ran")
	}
	if !strings.Contains(args, `--json-schema {"type":"object","properties":{}}`) {
		t.Errorf("args = %q", args)
	}
	if files != "README.md main.go " && files != "main.go README.md " {
		t.Errorf("the workspace holds %q, want the snapshot without .env", files)
	}
	if stub.snapshotAuth != "Bearer tok" {
		t.Errorf("snapshot Authorization = %q", stub.snapshotAuth)
	}
}

func TestARunWithoutASnapshotOrSchemaIsUnchanged(t *testing.T) {
	claim := run.Claim{Run: run.Run{ID: "adhoc1", Provider: "claude", Prompt: "hi", Role: run.RoleAdhoc, Status: run.Running}}
	stub := startStub(t, claim, func(http.ResponseWriter, *http.Request) {}, t.TempDir(), structuredProvider(t, `{"a":1}`))

	out := stub.outcome(t)
	args, files, _ := stub.probe(t)
	if out.ExitCode != 0 || strings.Contains(args, "--json-schema") || strings.TrimSpace(files) != "" || stub.snapshotCalls != 0 {
		t.Fatalf("outcome %+v, args %q, files %q, snapshot calls %d", out, args, files, stub.snapshotCalls)
	}
}

func TestAFailedSnapshotFailsTheRunWithoutStartingTheCLI(t *testing.T) {
	stub := startStub(t, responderClaim(), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":"could not read the repository from GitHub"}`)
	}, t.TempDir(), structuredProvider(t, `{"a":1}`))

	out := stub.outcome(t)
	if out.ExitCode == 0 || !strings.Contains(out.Result, "snapshot") || out.Output != nil {
		t.Fatalf("outcome = %+v", out)
	}
	if _, _, ran := stub.probe(t); ran {
		t.Fatal("the CLI ran without a snapshot")
	}
}

func TestAnUnsafeSnapshotFailsTheRunWithoutStartingTheCLI(t *testing.T) {
	archive := tarGz(t, map[string]string{"Octo-hello-abc1234/../../evil.txt": "pwned"})
	stub := startStub(t, responderClaim(), func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) },
		t.TempDir(), structuredProvider(t, `{"a":1}`))

	out := stub.outcome(t)
	if out.ExitCode == 0 || !strings.Contains(out.Result, "snapshot") {
		t.Fatalf("outcome = %+v", out)
	}
	if _, _, ran := stub.probe(t); ran {
		t.Fatal("the CLI ran on an unsafe snapshot")
	}
}

func TestTheWorkspaceIsRemovedAfterTheRun(t *testing.T) {
	root := t.TempDir()
	archive := tarGz(t, map[string]string{"Octo-hello-abc1234/main.go": "package main\n"})
	stub := startStub(t, responderClaim(), func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) },
		root, structuredProvider(t, `{"a":1}`))
	stub.outcome(t)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if entries, _ := os.ReadDir(root); len(entries) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the workspace with the repository snapshot was not removed")
}
