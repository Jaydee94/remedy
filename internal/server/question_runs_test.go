package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
)

// claimOne is what a runner gets when it claims the next queued run.
func claimOne(t *testing.T, baseURL string) run.Claim {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/runner/v1/claim", nil)
	req.Header.Set("Authorization", "Bearer "+runnerToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d, want 200", resp.StatusCode)
	}
	var c run.Claim
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAQuestionRunIsClaimedWithTheFrameAroundTheQuestion(t *testing.T) {
	e, repoID := withRepo(t)
	in := openIncident(t, e, repoID, "pr:7", "go")
	ctx := context.Background()
	q, err := e.store.CreateQuestionRun(ctx, "claude", "why did it fail twice?", in.ID, false)
	if err != nil {
		t.Fatal(err)
	}

	c := claimOne(t, e.ts.URL)
	if c.ID != q.ID {
		t.Fatalf("claimed %s, want %s", c.ID, q.ID)
	}
	for _, want := range []string{"incident " + strconv.FormatInt(in.ID, 10), "incident_get", "why did it fail twice?"} {
		if !strings.Contains(c.Prompt, want) {
			t.Errorf("the claimed prompt lacks %q:\n%s", want, c.Prompt)
		}
	}
	if c.MCPToken == "" {
		t.Error("a question run has gatekeeper access: the claim must carry a token")
	}
	stored, err := e.store.GetRun(ctx, q.ID)
	if err != nil || stored.Prompt != "why did it fail twice?" {
		t.Fatalf("the stored prompt = %q, %v, want the bare question", stored.Prompt, err)
	}
}

func TestAnAdhocRunWithoutAnIncidentIsClaimedAsItIs(t *testing.T) {
	e, _ := withRepo(t)
	ctx := context.Background()
	r, err := e.store.CreateToolRun(ctx, "claude", "list the incidents")
	if err != nil {
		t.Fatal(err)
	}
	if c := claimOne(t, e.ts.URL); c.ID != r.ID || c.Prompt != "list the incidents" {
		t.Fatalf("claim = %q (run %s), want the prompt unchanged", c.Prompt, c.ID)
	}
}
