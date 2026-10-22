package signals_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/signals"
	"github.com/Jaydee94/remedy/internal/store"
)

type fakeSource struct {
	mu      sync.Mutex
	on      bool
	sigs    []signals.Signal
	err     error
	fetched int
}

func (f *fakeSource) Name() string { return "alertmanager" }
func (f *fakeSource) Enabled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.on
}
func (f *fakeSource) Fetch(context.Context) ([]signals.Signal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetched++
	return f.sigs, f.err
}
func (f *fakeSource) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fetched
}

func sig(key, severity string, auto bool, labels map[string]string) signals.Signal {
	return signals.Signal{Key: key, Title: "title of " + key, Severity: severity, Auto: auto, Labels: labels, Details: json.RawMessage(`{"fingerprint":"` + key + `"}`)}
}

type rig struct {
	st    *store.Store
	src   *fakeSource
	r     *signals.Runner
	hosts []signals.HostRules
	logs  *bytes.Buffer
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	g := &rig{st: st, src: &fakeSource{on: true}, logs: &bytes.Buffer{}}
	g.r = &signals.Runner{
		Sources: []signals.Source{g.src}, Engine: &incident.Engine{Store: st},
		Hosts:    func(context.Context) ([]signals.HostRules, error) { return g.hosts, nil },
		Interval: func() time.Duration { return time.Minute },
		Log:      slog.New(slog.NewTextHandler(g.logs, nil)),
	}
	return g
}

func (g *rig) incidents(t *testing.T) map[string]store.Incident {
	t.Helper()
	list, err := g.st.ListIncidents(context.Background(), store.IncidentFilter{State: "all", Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]store.Incident{}
	for _, in := range list {
		out[in.Key] = in
	}
	return out
}

func (g *rig) activityCount(t *testing.T) int {
	t.Helper()
	log, err := g.st.ListActivity(context.Background(), store.ActivityQuery{Limit: 10000})
	if err != nil {
		t.Fatal(err)
	}
	return len(log)
}

func (g *rig) cycle(sigs ...signals.Signal) {
	g.src.mu.Lock()
	g.src.sigs, g.src.err = sigs, nil
	g.src.mu.Unlock()
	g.r.RunOnce(context.Background())
}

func (g *rig) addHost(t *testing.T, name, matchers string) store.Host {
	t.Helper()
	h, err := g.st.CreateHost(context.Background(), store.HostFields{Name: name, Address: "192.168.178.118", Port: 22, Account: "remedy", Enabled: true, Matchers: matchers})
	if err != nil {
		t.Fatal(err)
	}
	g.hosts = append(g.hosts, signals.HostRules{ID: h.ID, Name: name, Matchers: matchers})
	return h
}

func (g *rig) openCount(t *testing.T) int {
	t.Helper()
	n := 0
	for _, in := range g.incidents(t) {
		if in.State != store.IncResolved {
			n++
		}
	}
	return n
}

func TestACycleOpensAnIncidentPerSignalAndASecondCycleChangesNothing(t *testing.T) {
	g := newRig(t)
	a, b := sig("A/1", "critical", true, nil), sig("B/2", "warning", false, nil)
	g.cycle(a, b)
	got := g.incidents(t)
	if len(got) != 2 {
		t.Fatalf("%d incidents, want 2", len(got))
	}
	for _, s := range []signals.Signal{a, b} {
		in := got[s.Key]
		if in.Source != store.SourceAlertmanager || in.Title != s.Title || in.Severity != s.Severity || in.AutoDiagnose != s.Auto || in.Conclusion != "firing" {
			t.Errorf("%s: %+v", s.Key, in)
		}
	}
	n := g.activityCount(t)
	g.cycle(a, b)
	if m := g.activityCount(t); m != n {
		t.Errorf("a second cycle wrote activity: %d, then %d", n, m)
	}
	for k, in := range g.incidents(t) {
		if in.Occurrences != 1 {
			t.Errorf("%s: occurrences = %d, want 1", k, in.Occurrences)
		}
	}
	st := g.r.States()
	if len(st) != 1 || st[0].Name != "alertmanager" || st[0].State != signals.StateOK || st[0].Active != 2 || st[0].CheckedAt.IsZero() || st[0].Error != "" {
		t.Fatalf("states = %+v", st)
	}
}

func TestASignalThatIsGoneResolvesItsIncident(t *testing.T) {
	g := newRig(t)
	a, b := sig("A/1", "critical", true, nil), sig("B/2", "warning", false, nil)
	g.cycle(a, b)
	g.cycle(a)
	got := g.incidents(t)
	if got["B/2"].State != store.IncResolved || got["B/2"].ResolvedReason != incident.ReasonCleared || got["A/1"].State != store.IncOpen {
		t.Fatalf("after the second cycle: a = %s, b = %s (%s)", got["A/1"].State, got["B/2"].State, got["B/2"].ResolvedReason)
	}
	g.cycle()
	if got := g.incidents(t); got["A/1"].State != store.IncResolved || got["A/1"].ResolvedReason != incident.ReasonCleared {
		t.Fatalf("after the third cycle: a = %s", got["A/1"].State)
	}
}

func TestAnIgnoredIncidentIsResolvedWhenItsAlertIsGone(t *testing.T) {
	g := newRig(t)
	a := sig("A/1", "critical", true, nil)
	g.cycle(a)
	if _, err := g.r.Engine.Ignore(context.Background(), g.incidents(t)["A/1"].ID); err != nil {
		t.Fatal(err)
	}
	g.cycle(a)
	if got := g.incidents(t)["A/1"]; got.State != store.IncIgnored {
		t.Fatalf("state = %s, want ignored while the alert fires", got.State)
	}
	g.cycle()
	if got := g.incidents(t)["A/1"]; got.State != store.IncResolved || got.ResolvedReason != incident.ReasonCleared {
		t.Fatalf("state = %s (%s), want resolved", got.State, got.ResolvedReason)
	}
}

func TestAFailedFetchResolvesNothingAndShowsTheError(t *testing.T) {
	g := newRig(t)
	a, b := sig("A/1", "critical", true, nil), sig("B/2", "warning", false, nil)
	g.cycle(a, b)
	g.src.mu.Lock()
	g.src.err, g.src.sigs = errors.New("alertmanager: HTTP 500"), nil
	g.src.mu.Unlock()
	g.r.RunOnce(context.Background())
	if n := g.openCount(t); n != 2 {
		t.Fatalf("%d incidents are open after a failed fetch, want 2", n)
	}
	st := g.r.States()[0]
	if st.State != signals.StateError || st.Error != "alertmanager: HTTP 500" || st.CheckedAt.IsZero() {
		t.Fatalf("state = %+v", st)
	}
	g.cycle(a, b)
	if st := g.r.States()[0]; st.State != signals.StateOK || st.Error != "" || st.Active != 2 {
		t.Fatalf("state after a good cycle = %+v", st)
	}
}

func TestAnEmptyAnswerResolvesEverything(t *testing.T) {
	g := newRig(t)
	g.cycle(sig("A/1", "critical", true, nil), sig("B/2", "warning", false, nil))
	g.cycle() // no error, nothing firing: all clear
	if n := g.openCount(t); n != 0 {
		t.Fatalf("%d incidents are open after an empty answer, want 0", n)
	}
	if st := g.r.States()[0]; st.State != signals.StateOK || st.Active != 0 {
		t.Fatalf("state = %+v", st)
	}
}

func TestAtMostFiftyNewIncidentsAreOpenedPerCycle(t *testing.T) {
	g := newRig(t)
	var all []signals.Signal
	for i := 0; i < 60; i++ {
		all = append(all, sig(fmt.Sprintf("A/%02d", i), "warning", false, nil))
	}
	g.cycle(all...)
	if got := g.incidents(t); len(got) != signals.MaxNewPerCycle {
		t.Fatalf("%d incidents after the first cycle, want %d", len(got), signals.MaxNewPerCycle)
	}
	if n := g.openCount(t); n != signals.MaxNewPerCycle {
		t.Fatalf("%d open, an incident was resolved", n)
	}
	if st := g.r.States()[0]; st.State != signals.StateOK || st.Active != 60 {
		t.Fatalf("state = %+v", st)
	}
	if !strings.Contains(g.logs.String(), "deferred=10") {
		t.Errorf("the deferral was not logged: %s", g.logs)
	}
	g.cycle(all...)
	if got := g.incidents(t); len(got) != 60 || g.openCount(t) != 60 {
		t.Fatalf("%d incidents after the second cycle, want 60", len(got))
	}
	n := g.activityCount(t)
	g.cycle(all...)
	if m := g.activityCount(t); m != n {
		t.Errorf("a third cycle changed the activity: %d, then %d", n, m)
	}
}

func TestADisabledSourceIsNotAskedAndLeavesItsIncidentsAlone(t *testing.T) {
	g := newRig(t)
	if st := g.r.States()[0]; st.State != signals.StatePending {
		t.Fatalf("before the first cycle the state is %q, want pending", st.State)
	}
	g.cycle(sig("A/1", "critical", true, nil))
	g.src.mu.Lock()
	g.src.on = false
	g.src.mu.Unlock()
	before := g.src.count()
	g.r.RunOnce(context.Background())
	if g.src.count() != before {
		t.Fatal("a disabled source was asked")
	}
	if n := g.openCount(t); n != 1 {
		t.Fatalf("%d open, want 1", n)
	}
	if st := g.r.States()[0]; st.State != signals.StateOff {
		t.Fatalf("state = %+v, want off", st)
	}
}

func TestAnAlertIsMappedToTheFirstMatchingHostAndTheTieIsShown(t *testing.T) {
	g := newRig(t)
	nas := g.addHost(t, "nas", `instance=~"ugreen-nas(:[0-9]+)?"`)
	backup := g.addHost(t, "backup", `job="node-exporter"`)
	g.addHost(t, "empty", "")
	g.hosts = append(g.hosts, signals.HostRules{ID: 4, Name: "broken", Matchers: "this is not a rule"})

	g.cycle(
		sig("Both/1", "warning", false, map[string]string{"instance": "ugreen-nas", "job": "node-exporter"}),
		sig("Job/2", "warning", false, map[string]string{"job": "node-exporter"}),
		sig("None/3", "warning", false, map[string]string{"job": "other"}),
	)
	got := g.incidents(t)
	if len(got) != 3 {
		t.Fatalf("%d incidents, want 3 (a broken host must not stop the cycle)", len(got))
	}
	if got["Both/1"].HostID != nas.ID || got["Job/2"].HostID != backup.ID || got["None/3"].HostID != 0 {
		t.Fatalf("hosts = %d, %d, %d", got["Both/1"].HostID, got["Job/2"].HostID, got["None/3"].HostID)
	}
	also := func(in store.Incident) []string {
		var d struct {
			AlsoMatches []string `json:"alsoMatches"`
		}
		if err := json.Unmarshal(in.Details, &d); err != nil {
			t.Fatal(err)
		}
		return d.AlsoMatches
	}
	if a := also(got["Both/1"]); len(a) != 1 || a[0] != "backup" {
		t.Errorf("alsoMatches = %v, want [backup]", a)
	}
	if a := also(got["Job/2"]); len(a) != 0 {
		t.Errorf("alsoMatches = %v, want none", a)
	}
	if !strings.Contains(g.logs.String(), "broken") {
		t.Errorf("the skipped host was not logged: %s", g.logs)
	}
}

func TestAChangedRuleAppliesInTheNextCycle(t *testing.T) {
	g := newRig(t)
	nas := g.addHost(t, "nas", `instance="ugreen-nas"`)
	s := sig("A/1", "warning", false, map[string]string{"instance": "ugreen-nas"})
	g.cycle(s)
	if got := g.incidents(t)["A/1"]; got.HostID != nas.ID {
		t.Fatalf("host = %d, want %d", got.HostID, nas.ID)
	}
	g.hosts[0].Matchers = `instance="somewhere-else"`
	g.cycle(s)
	if got := g.incidents(t)["A/1"]; got.HostID != 0 {
		t.Fatalf("host = %d after the rule changed, want none", got.HostID)
	}

	g.hosts[0].Matchers = `instance="ugreen-nas"`
	g.cycle(s)
	if got := g.incidents(t)["A/1"]; got.HostID != nas.ID {
		t.Fatalf("host = %d after the rule matched again, want %d", got.HostID, nas.ID)
	}
	if err := g.st.DeleteHost(context.Background(), nas.ID); err != nil {
		t.Fatal(err)
	}
	g.hosts = nil
	g.cycle(s)
	got := g.incidents(t)["A/1"]
	if got.State == store.IncResolved || got.HostID != 0 {
		t.Fatalf("after the host was deleted: state %s, host %d; want the incident open without a host", got.State, got.HostID)
	}
}

func TestOtherSourcesAreNeverResolvedByThisRunner(t *testing.T) {
	g := newRig(t)
	ctx := context.Background()
	if err := g.st.SaveConnection(ctx, store.Connection{TokenCiphertext: []byte{1}, TokenHint: "1234", Login: "octo", Status: store.ConnOK}); err != nil {
		t.Fatal(err)
	}
	repo, err := g.st.AddRepo(ctx, store.ConnectionID, "octo/hello", "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.r.Engine.Observe(ctx, incident.Observation{
		RepoID: repo.ID, RepoName: repo.FullName, Ref: "pr:7", CheckName: "build", Class: incident.Bad, Conclusion: "failure", HeadSHA: "abc",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.r.Engine.ObserveSignal(ctx, incident.Observation{Source: store.SourceArgoCD, Key: "guestbook", Title: "guestbook", Class: incident.Bad, Conclusion: "degraded"}); err != nil {
		t.Fatal(err)
	}
	g.cycle(sig("A/1", "warning", false, nil))
	g.cycle()
	if n := g.openCount(t); n != 2 {
		t.Fatalf("%d incidents are open, want the GitHub and the Argo CD one", n)
	}
}

func TestRunStopsWithItsContextAndRunsOnce(t *testing.T) {
	g := newRig(t)
	g.r.Interval = func() time.Duration { return 20 * time.Millisecond }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		g.r.Run(ctx)
		close(done)
	}()
	deadline := time.After(5 * time.Second)
	for g.src.count() < 2 {
		select {
		case <-deadline:
			cancel()
			t.Fatalf("the source was asked %d times, want at least 2", g.src.count())
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop with its context")
	}
}

func TestAFailedObservationFailsTheCycleAndResolvesNothing(t *testing.T) {
	g := newRig(t)
	g.cycle(sig("Old/1", "warning", false, nil))
	// A host that was deleted between the mapping and the write: the foreign key refuses the new incident.
	g.hosts = []signals.HostRules{{ID: 999, Name: "gone", Matchers: `job="x"`}}
	g.cycle(sig("New/2", "warning", false, map[string]string{"job": "x"})) // Old/1 is absent from this answer
	if got := g.incidents(t)["Old/1"]; got.State == store.IncResolved {
		t.Fatal("an incident was resolved in a cycle whose observation failed")
	}
	if st := g.r.States()[0]; st.State != signals.StateError || st.Error == "" {
		t.Fatalf("state = %+v, want error", st)
	}
	g.hosts = nil
	g.cycle(sig("New/2", "warning", false, map[string]string{"job": "x"}))
	if st := g.r.States()[0]; st.State != signals.StateOK {
		t.Fatalf("the next cycle did not recover: %+v", st)
	}
	if got := g.incidents(t); got["Old/1"].State != store.IncResolved || got["New/2"].State != store.IncOpen {
		t.Fatalf("after recovery: old %s, new %s", got["Old/1"].State, got["New/2"].State)
	}
}

func TestTheHostListIsAskedOncePerCycleAndItsFailureFailsTheCycle(t *testing.T) {
	g := newRig(t)
	h := g.addHost(t, "nas", `job="x"`)
	g.cycle(sig("Old/1", "warning", false, map[string]string{"job": "x"}))
	if got := g.incidents(t)["Old/1"]; got.HostID != h.ID {
		t.Fatalf("setup: Old/1 host = %d, want %d", got.HostID, h.ID)
	}
	before := g.activityCount(t)

	asked := 0
	working := g.r.Hosts
	g.r.Hosts = func(context.Context) ([]signals.HostRules, error) {
		asked++
		return nil, errors.New("database is locked")
	}
	// Old/1 is absent from this answer and A/1 is new: neither may be resolved or opened.
	g.cycle(sig("A/1", "warning", false, map[string]string{"job": "x"}), sig("B/2", "warning", false, nil))
	if asked != 1 {
		t.Errorf("the hosts were asked %d times in one cycle, want 1", asked)
	}
	got := g.incidents(t)
	if len(got) != 1 || got["Old/1"].State == store.IncResolved || got["Old/1"].HostID != h.ID {
		t.Fatalf("incidents = %+v, want only the untouched Old/1", got)
	}
	if n := g.activityCount(t); n != before {
		t.Errorf("activity %d -> %d: a failed cycle changed something", before, n)
	}
	st := g.r.States()[0]
	if st.State != signals.StateError || !strings.Contains(st.Error, "the hosts could not be read") || !strings.Contains(st.Error, "database is locked") {
		t.Fatalf("state = %+v, want an error that names the hosts", st)
	}
	if n := strings.Count(g.logs.String(), "the hosts could not be read"); n != 1 {
		t.Errorf("%d warnings, want 1: %s", n, g.logs)
	}

	g.r.Hosts = working
	g.cycle(sig("A/1", "warning", false, map[string]string{"job": "x"}), sig("B/2", "warning", false, nil))
	if st := g.r.States()[0]; st.State != signals.StateOK {
		t.Fatalf("the next cycle did not recover: %+v", st)
	}
	got = g.incidents(t)
	if got["Old/1"].State != store.IncResolved || got["A/1"].HostID != h.ID || got["B/2"].State != store.IncOpen {
		t.Fatalf("after recovery: %+v", got)
	}
}

func (g *rig) kindCount(t *testing.T, kind string) int {
	t.Helper()
	log, err := g.st.ListActivity(context.Background(), store.ActivityQuery{Limit: 10000})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, a := range log {
		if a.Kind == kind {
			n++
		}
	}
	return n
}

// A returning alert reopens its incident and counts against the per-cycle cap like a new one: an Alertmanager that restarts and
// delivers every alert again cannot reopen more than MaxNewPerCycle incidents in one cycle.
func TestAtMostFiftyResolvedIncidentsReopenPerCycle(t *testing.T) {
	g := newRig(t)
	var all []signals.Signal
	for i := 0; i < signals.MaxNewPerCycle+1; i++ {
		all = append(all, sig(fmt.Sprintf("A/%02d", i), "warning", false, nil))
	}
	g.cycle(all...)
	g.cycle(all...) // the 51st opens in the second cycle
	g.cycle()       // all clear: every incident resolves
	if n := g.openCount(t); n != 0 {
		t.Fatalf("%d open after the empty answer, want 0", n)
	}
	g.logs.Reset()

	g.cycle(all...)
	if n := g.openCount(t); n != signals.MaxNewPerCycle {
		t.Fatalf("%d open after the alerts returned, want %d", n, signals.MaxNewPerCycle)
	}
	if n := g.kindCount(t, store.KindIncidentReopened); n != signals.MaxNewPerCycle {
		t.Fatalf("%d reopen entries, want %d", n, signals.MaxNewPerCycle)
	}
	if !strings.Contains(g.logs.String(), "deferred=1") {
		t.Errorf("the deferral was not logged: %s", g.logs)
	}

	g.cycle(all...)
	list, err := g.st.ListIncidents(context.Background(), store.IncidentFilter{State: "all", Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != len(all) || g.openCount(t) != len(all) {
		t.Fatalf("%d incidents (%d open) after the next cycle, want %d reopened", len(list), g.openCount(t), len(all))
	}
	if o, r := g.kindCount(t, store.KindIncidentOpened), g.kindCount(t, store.KindIncidentReopened); o != len(all) || r != len(all) {
		t.Fatalf("%d opened and %d reopened entries, want %d of each", o, r, len(all))
	}
	for _, in := range list {
		if in.Occurrences != 2 {
			t.Fatalf("%s: occurrences = %d, want 2", in.Key, in.Occurrences)
		}
	}
}
