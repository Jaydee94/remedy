package runner_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
)

func TestClientClaimDecodesTheSchemaAndTheSnapshotFlag(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(run.Claim{
			Run:      run.Run{ID: "r1", Provider: "claude", Prompt: "p", Role: run.RoleResponder, Status: run.Running},
			Schema:   json.RawMessage(`{"type":"object"}`),
			Snapshot: true,
		})
	})
	got, err := c.Claim(context.Background())
	if err != nil || got == nil {
		t.Fatalf("Claim = %+v, %v", got, err)
	}
	if got.ID != "r1" || got.Role != run.RoleResponder || string(got.Schema) != `{"type":"object"}` || !got.Snapshot {
		t.Fatalf("claim = %+v", got)
	}
}

func TestAnAdhocClaimHasNeitherSchemaNorSnapshot(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"id":"r2","provider":"claude","prompt":"hi","status":"running","role":"adhoc"}`)
	})
	got, err := c.Claim(context.Background())
	if err != nil || got == nil || got.Schema != nil || got.Snapshot {
		t.Fatalf("claim = %+v, %v", got, err)
	}
}

func TestClientSnapshotStreamsTheArchiveWithTheBearerToken(t *testing.T) {
	var method, path, auth string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path, auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		_, _ = io.WriteString(w, "archive")
	})
	rc, err := c.Snapshot(context.Background(), "r1")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if string(b) != "archive" || method != http.MethodGet || path != "/runner/v1/runs/r1/snapshot" || auth != "Bearer tok" {
		t.Fatalf("body %q, request %s %s, Authorization %q", b, method, path, auth)
	}
}

func TestClientSnapshotReportsAnErrorStatus(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":"could not read the repository from GitHub"}`)
	})
	rc, err := c.Snapshot(context.Background(), "r1")
	if err == nil || rc != nil {
		t.Fatalf("rc = %v, err = %v", rc, err)
	}
}
