package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
)

func claimedRun(t *testing.T, e *env, prompt string) string {
	t.Helper()
	ctx := context.Background()
	r, err := e.store.CreateRun(ctx, "claude", prompt)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := e.store.ClaimNext(ctx); err != nil || claimed == nil || claimed.ID != r.ID {
		t.Fatalf("ClaimNext = %+v, %v", claimed, err)
	}
	return r.ID
}

func TestFinishStoresTheOutputAndTheFailureReason(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	withOutput := claimedRun(t, e, "one")
	resp := e.runnerDo(t, http.MethodPost, "/runner/v1/runs/"+withOutput+"/finish",
		`{"exitCode":0,"result":"done","output":{"summary":"x","confidence":"high"}}`, runnerToken)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("finish with output = %d", resp.StatusCode)
	}
	got, _ := e.store.GetRun(ctx, withOutput)
	if string(got.Output) != `{"summary":"x","confidence":"high"}` || got.Status != run.Succeeded {
		t.Fatalf("run = %+v", got)
	}

	timedOut := claimedRun(t, e, "two")
	resp = e.runnerDo(t, http.MethodPost, "/runner/v1/runs/"+timedOut+"/finish",
		`{"exitCode":-1,"result":"stopped","failureReason":"timeout"}`, runnerToken)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("finish with a timeout = %d", resp.StatusCode)
	}
	if got, _ := e.store.GetRun(ctx, timedOut); got.FailureReason != "timeout" || got.Status != run.Failed {
		t.Fatalf("run = %+v", got)
	}
}

func TestFinishRefusesAnUnknownFailureReason(t *testing.T) {
	e := newEnv(t)
	id := claimedRun(t, e, "one")

	resp := e.runnerDo(t, http.MethodPost, "/runner/v1/runs/"+id+"/finish", `{"exitCode":1,"failureReason":"because"}`, runnerToken)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if got, _ := e.store.GetRun(context.Background(), id); got.Status != run.Running {
		t.Fatalf("status = %q: the refused request must not finish the run", got.Status)
	}
}

func TestFinishStoresHowTheRunWasBilled(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for source, want := range map[string]string{"api_key": "api_key", "subscription": "subscription", "": ""} {
		id := claimedRun(t, e, "billing "+source)
		body := `{"exitCode":0,"result":"done","authSource":"` + source + `"}`
		if resp := e.runnerDo(t, http.MethodPost, "/runner/v1/runs/"+id+"/finish", body, runnerToken); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("finish with authSource %q = %d", source, resp.StatusCode)
		}
		if got, _ := e.store.GetRun(ctx, id); got.AuthSource != want {
			t.Errorf("authSource %q stored as %q, want %q", source, got.AuthSource, want)
		}
	}
}

func TestFinishAcceptsTheBillingMismatchReasonAndRefusesAnotherSource(t *testing.T) {
	e := newEnv(t)
	id := claimedRun(t, e, "mismatch")
	if resp := e.runnerDo(t, http.MethodPost, "/runner/v1/runs/"+id+"/finish",
		`{"exitCode":-1,"result":"stopped","failureReason":"billing_mismatch","authSource":"api_key"}`, runnerToken); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("finish with billing_mismatch = %d", resp.StatusCode)
	}
	if got, _ := e.store.GetRun(context.Background(), id); got.FailureReason != run.ReasonBillingMismatch || got.Status != run.Failed {
		t.Fatalf("run = %+v", got)
	}
	other := claimedRun(t, e, "bad source")
	if resp := e.runnerDo(t, http.MethodPost, "/runner/v1/runs/"+other+"/finish", `{"exitCode":0,"authSource":"banana"}`, runnerToken); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("finish with authSource banana = %d, want 400", resp.StatusCode)
	}
	if got, _ := e.store.GetRun(context.Background(), other); got.Status != run.Running {
		t.Fatalf("status = %q: the refused request must not finish the run", got.Status)
	}
}
