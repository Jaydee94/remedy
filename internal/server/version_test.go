package server_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/version"
)

func TestTheVersionNeedsASession(t *testing.T) {
	e := newEnv(t)
	if resp := e.do(t, e.adminClient(t, false), http.MethodGet, "/api/version", "", false); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestTheVersionAnswersTheRunningBuild(t *testing.T) {
	oldV, oldC := version.Version, version.Commit
	t.Cleanup(func() { version.Version, version.Commit = oldV, oldC })
	version.Version, version.Commit = "9.9.9-test", "0123456789abcdef"

	e := newEnv(t)
	resp := e.do(t, e.adminClient(t, true), http.MethodGet, "/api/version", "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got := decode[map[string]string](t, resp)
	if len(got) != 2 || got["version"] != "9.9.9-test" || got["commit"] != "0123456" {
		t.Fatalf("body = %v", got)
	}
}

func TestTheRunnerReportsItsBuildAndTheViewShowsIt(t *testing.T) {
	e := newRunnerEnv(t)
	body := `{"login":"ok","cliVersion":"2.1.288","version":"0.8.0","commit":"0123456789abcdef"}`
	if resp := e.runnerDo(t, http.MethodPost, "/runner/v1/status", body, runnerToken); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("report = %d, want 204", resp.StatusCode)
	}
	v := getRunnerView(t, e, e.adminClient(t, true))
	if v.Version != "0.8.0" || v.Commit != "0123456" {
		t.Fatalf("view = %+v, want version 0.8.0 and commit 0123456", v)
	}
}

func TestAnOlderRunnerWithoutAVersionLeavesItEmpty(t *testing.T) {
	e := newRunnerEnv(t)
	body := `{"login":"ok","loginCheckedAt":"` + time.Now().UTC().Format(time.RFC3339) + `","cliVersion":"2.1.288"}`
	if resp := e.runnerDo(t, http.MethodPost, "/runner/v1/status", body, runnerToken); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("report = %d, want 204", resp.StatusCode)
	}
	v := getRunnerView(t, e, e.adminClient(t, true))
	if !v.Connected || v.Version != "" || v.Commit != "" {
		t.Fatalf("view = %+v, want connected with no version", v)
	}
}

func TestAHostileVersionFromTheRunnerIsReplaced(t *testing.T) {
	e := newRunnerEnv(t)
	for name, body := range map[string]string{
		"markup":    `{"login":"ok","version":"<img src=x onerror=alert(1)>","commit":"abc 123"}`,
		"newline":   `{"login":"ok","version":"1.0\nInjected","commit":"a\u0000b"}`,
		"too long":  `{"login":"ok","version":"` + strings.Repeat("1", 3800) + `","commit":"` + strings.Repeat("a", 100) + `"}`,
		"non-ASCII": `{"login":"ok","version":"1.0ä","commit":"ä"}`,
	} {
		if resp := e.runnerDo(t, http.MethodPost, "/runner/v1/status", body, runnerToken); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("%s: report = %d, want 204", name, resp.StatusCode)
		}
		v := getRunnerView(t, e, e.adminClient(t, true))
		if v.Version != "unknown" || v.Commit != "unknown" {
			t.Errorf("%s: view = %+v, want unknown for both", name, v)
		}
	}
}
