package server_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/responder"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/settings"
	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/internal/testutil"
)

type budgetLimits struct {
	Spend  *int `json:"apiSpendLast24hCents"`
	Budget *int `json:"apiBudgetPerDayCents"`
}

func newBudgetEnv(t *testing.T, rs *server.RunnerStatus) *env {
	t.Helper()
	st := testutil.Store(t)
	set := settings.New(context.Background(), st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(server.New(server.Deps{
		Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Settings: set, RunnerStatus: rs,
		Responder: &responder.Responder{Store: st, Limits: store.DefaultLimits(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))},
	}))
	t.Cleanup(ts.Close)
	e := &env{ts: ts, store: st}
	// a run that was billed to a key and one that was billed to the subscription, each with a cost
	for _, c := range []struct {
		usd    float64
		source string
	}{{1.234, "api_key"}, {0.5, "api_key"}, {9, "subscription"}} {
		r, err := st.CreateRun(context.Background(), "claude", "p")
		if err != nil {
			t.Fatal(err)
		}
		if claimed, err := st.ClaimNext(context.Background()); err != nil || claimed == nil || claimed.ID != r.ID {
			t.Fatalf("ClaimNext = %+v, %v", claimed, err)
		}
		if err := st.FinishRun(context.Background(), r.ID, run.Outcome{ExitCode: 0, CostUSD: c.usd, AuthSource: c.source}); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func getBudgetLimits(t *testing.T, e *env) budgetLimits {
	t.Helper()
	resp := e.do(t, e.adminClient(t, true), http.MethodGet, "/api/limits", "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/limits = %d", resp.StatusCode)
	}
	return decode[budgetLimits](t, resp)
}

func TestTheLimitsLeaveTheMoneyOutWhileTheRunnerBillsASubscription(t *testing.T) {
	rs := server.NewRunnerStatus()
	e := newBudgetEnv(t, rs)
	if got := getBudgetLimits(t, e); got.Spend != nil || got.Budget != nil {
		t.Fatalf("limits = %+v for a runner that never said its mode, want no money", got)
	}
	rs.SetAuthMode("subscription")
	if got := getBudgetLimits(t, e); got.Spend != nil || got.Budget != nil {
		t.Fatalf("limits = %+v in subscription mode, want no money", got)
	}
}

func TestTheLimitsShowTheSpendAndTheBudgetInKeyMode(t *testing.T) {
	rs := server.NewRunnerStatus()
	e := newBudgetEnv(t, rs)
	rs.SetAuthMode("api_key")
	got := getBudgetLimits(t, e)
	if got.Spend == nil || *got.Spend != 173 || got.Budget == nil || *got.Budget != 500 {
		t.Fatalf("limits = %+v, want a spend of 173 cents (the subscription run does not count) and the default budget of 500", got)
	}
}
