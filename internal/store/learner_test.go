package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/skill"
	"github.com/Jaydee94/remedy/internal/store"
)

func learnStart(hostID int64) store.LearnStart {
	return store.LearnStart{HostID: hostID, Provider: "claude", Prompt: "learn", Now: time.Now()}
}

// pend stores proposals for a host without a run, as a finished learning run would have, and returns how many wait for the host now.
func pend(t *testing.T, s *store.Store, hostID int64, names ...string) int {
	t.Helper()
	var items []store.Proposal
	for _, n := range names {
		items = append(items, store.Proposal{Fields: skillFields(n), Evidence: dockerPS})
	}
	if _, err := s.CreateProposals(context.Background(), store.ProposalSet{HostID: hostID, Items: items}); err != nil {
		t.Fatalf("CreateProposals(%v): %v", names, err)
	}
	n, err := s.CountProposals(context.Background(), hostID)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func names(prefix string, from, to int) []string {
	var out []string
	for i := from; i < to; i++ {
		out = append(out, fmt.Sprintf("%s%d", prefix, i))
	}
	return out
}

func countKind(t *testing.T, s *store.Store, kind string) int {
	t.Helper()
	n := 0
	for _, k := range activityKinds(t, s) {
		if k == kind {
			n++
		}
	}
	return n
}

func runCount(t *testing.T, s *store.Store) int {
	t.Helper()
	list, err := s.ListRuns(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	return len(list)
}

// finish claims the oldest queued run and ends it, so that the next run may start.
func finish(t *testing.T, s *store.Store) run.Run {
	t.Helper()
	ctx := context.Background()
	claimed, err := s.ClaimNext(ctx)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimNext = %v, %v", claimed, err)
	}
	if err := s.FinishRun(ctx, claimed.ID, run.Outcome{ExitCode: 0, Result: "done"}); err != nil {
		t.Fatal(err)
	}
	return *claimed
}

func TestStartLearningCreatesTheRunForOneHostAndLogsIt(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")

	r, err := s.StartLearning(ctx, learnStart(nas.ID))
	if err != nil {
		t.Fatal(err)
	}
	if r.Role != run.RoleLearner || r.Status != run.Queued || !r.MCP || r.Cluster || r.Automatic || r.HostCount != 1 || r.Prompt != "learn" || r.Provider != "claude" || r.IncidentID != nil {
		t.Fatalf("run = %+v", r)
	}
	hosts, err := s.RunHosts(ctx, r.ID)
	if err != nil || len(hosts) != 1 || hosts[0].Host.ID != nas.ID {
		t.Fatalf("RunHosts = %+v, %v", hosts, err)
	}
	log, err := s.ListActivity(ctx, store.ActivityQuery{Limit: 10})
	if err != nil || len(log) == 0 || log[0].Kind != store.KindLearnStarted || log[0].RunID != r.ID || log[0].Summary != "Learning about nas started" {
		t.Fatalf("activity = %+v, %v", log, err)
	}
	if last, ok, err := s.LastLearnerRun(ctx, nas.ID); err != nil || !ok || last.ID != r.ID {
		t.Fatalf("LastLearnerRun = %+v, %v, %v", last, ok, err)
	}
}

func TestStartLearningIsRefusedWhileARunIsQueuedOrRunningAndCreatesNothing(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas, alpha := hostNamed(t, s, "nas"), hostNamed(t, s, "alpha")

	if _, err := s.StartLearning(ctx, learnStart(999)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an unknown host = %v, want ErrNotFound", err)
	}
	if _, err := s.StartLearning(ctx, learnStart(nas.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartLearning(ctx, learnStart(nas.ID)); !errors.Is(err, store.ErrLearning) {
		t.Errorf("a second run for the same host = %v, want ErrLearning", err)
	}
	if _, err := s.StartLearning(ctx, learnStart(alpha.ID)); !errors.Is(err, store.ErrBusy) {
		t.Errorf("a run for another host while one is queued = %v, want ErrBusy", err)
	}
	if n := runCount(t, s); n != 1 {
		t.Errorf("%d runs after the refusals, want 1", n)
	}
	if n := countKind(t, s, store.KindLearnStarted); n != 1 {
		t.Errorf("%d learn_started entries after the refusals, want 1", n)
	}

	// A running run refuses as well; an ended one does not.
	claimed, err := s.ClaimNext(ctx)
	if err != nil || claimed == nil {
		t.Fatal(err)
	}
	if _, err := s.StartLearning(ctx, learnStart(nas.ID)); !errors.Is(err, store.ErrLearning) {
		t.Errorf("while it runs = %v, want ErrLearning", err)
	}
	if err := s.FinishRun(ctx, claimed.ID, run.Outcome{ExitCode: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartLearning(ctx, learnStart(alpha.ID)); err != nil {
		t.Errorf("after the run ended = %v", err)
	}
	// Any other run blocks it too.
	finish(t, s)
	if _, err := s.CreateRun(ctx, "claude", "a question"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartLearning(ctx, learnStart(nas.ID)); !errors.Is(err, store.ErrBusy) {
		t.Errorf("while an ad-hoc run is queued = %v, want ErrBusy", err)
	}
}

func TestStartLearningIsRefusedWhenTooManyProposalsWait(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")

	if n := pend(t, s, nas.ID, names("a", 0, 8)...); n != 8 {
		t.Fatalf("%d proposals", n)
	}
	if n := pend(t, s, nas.ID, names("b", 0, 3)...); n != 11 {
		t.Fatalf("%d proposals", n)
	}
	// 11 wait: a run that adds its profile and 8 skills ends at 20, the most there may be.
	r, err := s.StartLearning(ctx, learnStart(nas.ID))
	if err != nil {
		t.Fatalf("with 11 waiting: %v", err)
	}
	finish(t, s)
	_ = r

	pend(t, s, nas.ID, "c0")
	before := runCount(t, s)
	if _, err := s.StartLearning(ctx, learnStart(nas.ID)); !errors.Is(err, store.ErrProposalsWaiting) {
		t.Errorf("with 12 waiting = %v, want ErrProposalsWaiting", err)
	}
	if runCount(t, s) != before {
		t.Error("a refused start created a run")
	}
	// The proposals of another host do not count.
	alpha := hostNamed(t, s, "alpha")
	if _, err := s.StartLearning(ctx, learnStart(alpha.ID)); err != nil {
		t.Errorf("another host with none waiting = %v", err)
	}
	// Rejecting makes room again.
	finish(t, s)
	list, _ := s.ListSkills(ctx, store.SkillFilter{HostID: nas.ID, State: store.SkillProposed})
	if err := s.RejectSkill(ctx, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartLearning(ctx, learnStart(nas.ID)); err != nil {
		t.Errorf("after a rejection = %v", err)
	}
}

func TestAHostNeverHasMoreThanTwentyProposalsWaiting(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")
	pend(t, s, nas.ID, names("a", 0, 8)...)
	pend(t, s, nas.ID, names("b", 0, 8)...)
	pend(t, s, nas.ID, names("c", 0, 4)...) // 20 wait

	kinds := len(activityKinds(t, s))
	_, err := s.CreateProposals(ctx, store.ProposalSet{HostID: nas.ID, Items: []store.Proposal{{Fields: skillFields("one-too-many"), Evidence: dockerPS}}})
	if !errors.Is(err, store.ErrTooManyProposals) {
		t.Fatalf("the 21st proposal = %v, want ErrTooManyProposals", err)
	}
	if n, _ := s.CountProposals(ctx, nas.ID); n != 20 {
		t.Errorf("%d proposals wait after the refusal, want 20", n)
	}
	if len(activityKinds(t, s)) != kinds {
		t.Error("a refused set left an activity entry behind")
	}
	// A set that replaces pending proposals of the same names does not add to the number.
	if n := pend(t, s, nas.ID, names("a", 0, 8)...); n != 20 {
		t.Errorf("replacing 8 of 20 left %d waiting", n)
	}
}

func TestTheProfileIsNotCountedAmongTheEightSkills(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")
	var items []store.Proposal
	items = append(items, store.Proposal{Fields: skillFields(skill.ProfileName), Evidence: dockerPS})
	for _, n := range names("s", 0, 8) {
		items = append(items, store.Proposal{Fields: skillFields(n), Evidence: dockerPS})
	}
	if got, err := s.CreateProposals(ctx, store.ProposalSet{HostID: nas.ID, Items: items}); err != nil || len(got) != 9 {
		t.Fatalf("the profile and 8 skills = %d, %v", len(got), err)
	}
	items = append(items[:1:1], store.Proposal{Fields: skillFields("s0")})
	for _, n := range names("t", 0, 8) {
		items = append(items, store.Proposal{Fields: skillFields(n)})
	}
	var fe *skill.FieldError
	if _, err := s.CreateProposals(ctx, store.ProposalSet{HostID: nas.ID, Items: items}); !errors.As(err, &fe) || fe.Field != "skills" {
		t.Errorf("the profile and 9 skills = %v, want a *skill.FieldError about skills", err)
	}
}

func TestSucceededReadsAreTheHostReadsThatWorked(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")
	r, err := s.StartLearning(ctx, learnStart(nas.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	call := func(id, tool string, args any, status string) {
		t.Helper()
		raw, _ := json.Marshal(args)
		c, _, err := s.BeginToolCall(ctx, store.NewToolCall{RunID: r.ID, ToolUseID: id, Tool: tool, Kind: store.CallKindRead, Arguments: raw})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.FinishToolCall(ctx, c.ID, status, "out", ""); err != nil {
			t.Fatal(err)
		}
	}
	type hostArgs struct {
		Host string   `json:"host"`
		Argv []string `json:"argv"`
	}
	call("u1", "host_list", struct{}{}, store.CallSucceeded)
	call("u2", "host_read", hostArgs{"nas", []string{"docker", "ps", "-a"}}, store.CallSucceeded)
	call("u3", "host_read", hostArgs{"nas", []string{"rm", "-rf", "/"}}, store.CallFailed)
	call("u4", "host_read", hostArgs{"nas", []string{"uname", "-a"}}, store.CallSucceeded)
	call("u5", "skill_get", map[string]string{"name": "x"}, store.CallSucceeded)

	got, err := s.SucceededReads(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"docker", "ps", "-a"}, {"uname", "-a"}}; !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Errorf("SucceededReads = %v, want %v", got, want)
	}
	if none, err := s.SucceededReads(ctx, "no-such-run"); err != nil || none == nil || len(none) != 0 {
		t.Errorf("SucceededReads of an unknown run = %#v, %v, want an empty list", none, err)
	}
}

func TestLastLearnerRunIsTheNewestOneOfThatHost(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas, alpha := hostNamed(t, s, "nas"), hostNamed(t, s, "alpha")
	if _, ok, err := s.LastLearnerRun(ctx, nas.ID); err != nil || ok {
		t.Fatalf("before any run: ok = %v, %v", ok, err)
	}
	first, _ := s.StartLearning(ctx, learnStart(nas.ID))
	finish(t, s)
	second, err := s.StartLearning(ctx, store.LearnStart{HostID: nas.ID, Provider: "claude", Prompt: "again", Now: time.Now().Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	finish(t, s)
	other, _ := s.StartLearning(ctx, learnStart(alpha.ID))
	if last, ok, err := s.LastLearnerRun(ctx, nas.ID); err != nil || !ok || last.ID != second.ID || last.ID == first.ID {
		t.Errorf("last run of nas = %+v, %v, %v, want %s", last, ok, err, second.ID)
	}
	if last, _, _ := s.LastLearnerRun(ctx, alpha.ID); last.ID != other.ID {
		t.Errorf("last run of alpha = %s", last.ID)
	}
}

func TestALearningRunCanBeCancelledBeforeItStarts(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")
	r, err := s.StartLearning(ctx, learnStart(nas.ID))
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := s.CancelRun(ctx, r.ID)
	if err != nil || cancelled.Status != run.Failed || cancelled.FailureReason != run.ReasonCancelled {
		t.Fatalf("CancelRun = %+v, %v", cancelled, err)
	}
	if _, err := s.StartLearning(ctx, learnStart(nas.ID)); err != nil {
		t.Errorf("a new run after the cancel = %v", err)
	}
}
