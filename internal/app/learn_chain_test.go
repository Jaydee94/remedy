package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/reaper"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

const chainLearnAnswer = `{"profile":{"os":"Debian 12","role":"NAS","services":["docker"],"containers":["remedy-drill"],"notes":""},` +
	`"skills":[{"name":"drill","description":"The drill container is safe to restart.","body":"remedy-drill restarts cleanly.","evidence":[["docker","ps","-a"]]}]}`

// startLearning makes a host a learning run may use, starts its learning run through the admin API and claims it the way a runner does.
func startLearning(t *testing.T, s *stack) (store.Host, string) {
	t.Helper()
	ctx := context.Background()
	h, err := s.st.CreateHost(ctx, store.HostFields{Name: "nas", Address: "10.0.0.5", Port: 22, Account: "remedy", DockerPattern: "remedy-*", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	const line = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOhostkeyhostkeyhostkeyhostkeyhostkeyhostkeyhost"
	if err := s.st.SaveHostCredential(ctx, store.HostCredential{HostID: h.ID, Kind: "password", Ciphertext: []byte{1}, Hint: "set"}); err != nil {
		t.Fatal(err)
	}
	if err := s.st.PinHostKey(ctx, h.ID, line, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.st.SaveHostTest(ctx, h.ID, line, store.HostOK, "", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	code, body := s.admin(http.MethodPost, "/api/hosts/"+strconv.FormatInt(h.ID, 10)+"/learn", "")
	if code != http.StatusAccepted {
		t.Fatalf("learn = %d %s", code, body)
	}
	claimed, err := s.st.ClaimNext(ctx)
	if err != nil || claimed == nil || claimed.Role != run.RoleLearner {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	return h, claimed.ID
}

// finishAsRunner posts a run's outcome to the runner API and returns the status code.
func finishAsRunner(t *testing.T, s *stack, id string, o run.Outcome) int {
	t.Helper()
	b, _ := json.Marshal(o)
	req, _ := http.NewRequest(http.MethodPost, s.ts.URL+"/runner/v1/runs/"+id+"/finish", strings.NewReader(string(b)))
	req.Header.Set("Authorization", "Bearer "+runnerToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// learnOutcomes counts the outcome entries of a run in the activity log.
func learnOutcomes(t *testing.T, s *stack, runID string) (proposed, failed int) {
	t.Helper()
	acts, err := s.st.ListActivity(context.Background(), store.ActivityQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range acts {
		switch {
		case a.RunID != runID:
		case a.Kind == store.KindSkillsProposed:
			proposed++
		case a.Kind == store.KindLearnFailed:
			failed++
		}
	}
	return proposed, failed
}

func TestALearningRunEndedByTheReaperAndFinishedLateHasOneOutcome(t *testing.T) {
	s := newStack(t, false)
	ctx := context.Background()
	_, id := startLearning(t, s)

	// The runner went silent: the reaper fails the run and its hook closes it.
	later := time.Now().Add(16 * time.Minute)
	s.app.Reaper.Now = func() time.Time { return later }
	s.app.Reaper.MaxAge = reaper.DefaultMaxAge
	if n, err := s.app.Reaper.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("Sweep = %d, %v", n, err)
	}
	if proposed, failed := learnOutcomes(t, s, id); proposed != 0 || failed != 1 {
		t.Fatalf("after the sweep: %d skills_proposed, %d learn_failed; want 0 and 1", proposed, failed)
	}
	// Then the runner comes back with a valid answer: the run is over, so nothing is stored.
	if code := finishAsRunner(t, s, id, run.Outcome{ExitCode: 0, Result: "done", Output: json.RawMessage(chainLearnAnswer)}); code != http.StatusNotFound {
		t.Errorf("a late finish = %d, want 404", code)
	}
	// A second close (the hook and a finish that raced it) changes nothing either.
	s.app.Learner.Complete(ctx, id)

	if proposed, failed := learnOutcomes(t, s, id); proposed != 0 || failed != 1 {
		t.Errorf("outcomes: %d skills_proposed, %d learn_failed; want 0 and 1", proposed, failed)
	}
	if list, _ := s.st.ListSkills(ctx, store.SkillFilter{}); len(list) != 0 {
		t.Errorf("%d skills were stored for a reaped run", len(list))
	}
}

func TestALearningRunFinishedAndClosedAgainStoresOneSetOfProposals(t *testing.T) {
	s := newStack(t, false)
	ctx := context.Background()
	_, id := startLearning(t, s)

	if code := finishAsRunner(t, s, id, run.Outcome{ExitCode: 0, Result: "done", Output: json.RawMessage(chainLearnAnswer)}); code != http.StatusNoContent {
		t.Fatalf("finish = %d", code)
	}
	// The reaper's hook for the same run (it raced the finish) must not store a second set.
	s.app.Reaper.OnFailed(ctx, []string{id})

	if proposed, failed := learnOutcomes(t, s, id); proposed != 1 || failed != 0 {
		t.Errorf("outcomes: %d skills_proposed, %d learn_failed; want 1 and 0", proposed, failed)
	}
	if list, _ := s.st.ListSkills(ctx, store.SkillFilter{State: store.SkillProposed}); len(list) != 2 {
		t.Errorf("%d proposals, want the profile and one skill", len(list))
	}
}
