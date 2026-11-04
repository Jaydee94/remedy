package webhook_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/internal/testutil"
	"github.com/Jaydee94/remedy/internal/webhook"
)

type sEnv struct {
	t   *testing.T
	st  *store.Store
	dsn string
	sw  *webhook.Sweeper
	at  time.Time
}

func newSEnv(t *testing.T) *sEnv {
	t.Helper()
	st, dsn := testutil.StoreDSN(t)
	e := &sEnv{t: t, st: st, dsn: dsn, at: time.Now()}
	e.sw = &webhook.Sweeper{Store: st, Now: func() time.Time { return e.at }}
	return e
}

func (e *sEnv) endpoint(name string, after time.Duration) {
	e.t.Helper()
	if _, _, err := e.st.CreateWebhook(context.Background(), name, store.WebhookFields{Enabled: true, ResolveAfter: after, RatePerMinute: 60, NewPerHour: 30}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *sEnv) open(source, key string) store.Incident {
	e.t.Helper()
	in, err := e.st.OpenIncident(context.Background(), store.NewIncident{Source: source, Key: key, Title: "Disk full", Conclusion: "firing", Details: json.RawMessage(`{}`)},
		store.NewActivity{Kind: store.KindIncidentOpened, Summary: "opened"})
	if err != nil {
		e.t.Fatal(err)
	}
	return in
}

func (e *sEnv) silentFor(in store.Incident, d time.Duration) {
	e.t.Helper()
	testutil.SQL(e.t, e.dsn, `UPDATE incidents SET last_seen = ? WHERE id = ?`, testutil.TS(e.at.Add(-d)), in.ID)
}

func (e *sEnv) state(in store.Incident) store.Incident {
	e.t.Helper()
	got, err := e.st.GetIncident(context.Background(), in.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return got
}

func TestASilentIncidentIsResolvedAsExpiredAndTheThreadSaysNotReported(t *testing.T) {
	e := newSEnv(t)
	e.endpoint("grafana", 24*time.Hour)
	silent, fresh := e.open(store.SourceWebhook, "grafana/a"), e.open(store.SourceWebhook, "grafana/b")
	e.silentFor(silent, 25*time.Hour)
	e.silentFor(fresh, 23*time.Hour)
	n, err := e.sw.SweepOnce(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("SweepOnce = %d, %v", n, err)
	}
	got := e.state(silent)
	if got.State != store.IncResolved || got.ResolvedReason != incident.ReasonExpired {
		t.Fatalf("silent incident = %s / %s", got.State, got.ResolvedReason)
	}
	if e.state(fresh).State != store.IncOpen {
		t.Fatal("an incident inside the limit was resolved")
	}
	log, _ := e.st.ListActivity(context.Background(), store.ActivityQuery{IncidentID: silent.ID, Limit: 5})
	if log[0].Summary != "Incident resolved: webhook alert Disk full was not reported for 24h0m0s" {
		t.Fatalf("activity = %q", log[0].Summary)
	}
}

func TestAnEndpointWithNoSilenceLimitNeverExpires(t *testing.T) {
	e := newSEnv(t)
	e.endpoint("grafana", 0)
	in := e.open(store.SourceWebhook, "grafana/a")
	e.silentFor(in, 30*24*time.Hour)
	if n, err := e.sw.SweepOnce(context.Background()); err != nil || n != 0 || e.state(in).State != store.IncOpen {
		t.Fatalf("SweepOnce = %d, %v, state %s", n, err, e.state(in).State)
	}
}

func TestAnIgnoredIncidentExpiresAndADiagnosingOneWaits(t *testing.T) {
	e := newSEnv(t)
	e.endpoint("grafana", time.Hour)
	ignored, diagnosing := e.open(store.SourceWebhook, "grafana/i"), e.open(store.SourceWebhook, "grafana/d")
	if _, err := (&incident.Engine{Store: e.st}).Ignore(context.Background(), ignored.ID); err != nil {
		t.Fatal(err)
	}
	testutil.SQL(t, e.dsn, `UPDATE incidents SET state = 'diagnosing' WHERE id = ?`, diagnosing.ID)
	e.silentFor(ignored, 2*time.Hour)
	e.silentFor(diagnosing, 2*time.Hour)
	if n, err := e.sw.SweepOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("SweepOnce = %d, %v", n, err)
	}
	if e.state(ignored).State != store.IncResolved {
		t.Error("an ignored incident did not expire")
	}
	if e.state(diagnosing).State != store.IncDiagnosing {
		t.Error("an incident that is being diagnosed was resolved under the run")
	}
}

func TestAnIncidentOfADeletedEndpointUsesTheDefaultOfTwentyFourHours(t *testing.T) {
	e := newSEnv(t)
	young, old := e.open(store.SourceWebhook, "gone/a"), e.open(store.SourceWebhook, "gone/b")
	e.silentFor(young, 23*time.Hour)
	e.silentFor(old, 25*time.Hour)
	if n, _ := e.sw.SweepOnce(context.Background()); n != 1 || e.state(old).State != store.IncResolved || e.state(young).State != store.IncOpen {
		t.Fatalf("swept %d", n)
	}
}

func TestOnlyWebhookIncidentsAreSwept(t *testing.T) {
	e := newSEnv(t)
	e.endpoint("grafana", 5*time.Minute)
	alert := e.open(store.SourceAlertmanager, "grafana/a")
	e.silentFor(alert, 48*time.Hour)
	if n, err := e.sw.SweepOnce(context.Background()); err != nil || n != 0 || e.state(alert).State != store.IncOpen {
		t.Fatalf("SweepOnce = %d, %v, state %s", n, err, e.state(alert).State)
	}
}

func TestTheSweepIsFollowedByTheHookThatStartsDiagnoses(t *testing.T) {
	e := newSEnv(t)
	called := 0
	e.sw.AfterSweep = func(context.Context) { called++ }
	e.sw.Interval = func() time.Duration { return time.Minute }
	ctx, cancel := context.WithCancel(context.Background())
	e.sw.Tick = make(chan time.Time)
	done := make(chan struct{})
	go func() { e.sw.Run(ctx); close(done) }()
	e.sw.Tick <- e.at
	e.sw.Tick <- e.at // the second send returns only after the first sweep and its hook have finished
	cancel()
	<-done
	if called < 1 {
		t.Fatalf("AfterSweep ran %d times", called)
	}
}
