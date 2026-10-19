package store_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/skill"
	"github.com/Jaydee94/remedy/internal/store"
)

func learningRun(t *testing.T, s *store.Store) string {
	t.Helper()
	r, err := s.CreateRun(context.Background(), "claude", "learn")
	if err != nil {
		t.Fatal(err)
	}
	return r.ID
}

var dockerPS = []skill.Evidence{{Argv: []string{"docker", "ps", "-a"}, Ran: true}}

// propose stores one proposal per name for the host, as a learning run would, and returns them.
func propose(t *testing.T, s *store.Store, hostID int64, names ...string) []store.Skill {
	t.Helper()
	var items []store.Proposal
	for _, n := range names {
		items = append(items, store.Proposal{Fields: skillFields(n), Evidence: dockerPS})
	}
	got, err := s.CreateProposals(context.Background(), store.ProposalSet{HostID: hostID, RunID: learningRun(t, s), Items: items})
	if err != nil {
		t.Fatalf("CreateProposals(%v): %v", names, err)
	}
	return got
}

func proposals(t *testing.T, s *store.Store) []store.Skill {
	t.Helper()
	list, err := s.ListSkills(context.Background(), store.SkillFilter{State: store.SkillProposed})
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestCreateProposalsStoresProposedSkillsWithTheirEvidenceAndOneActivityEntry(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")
	runID := learningRun(t, s)

	got, err := s.CreateProposals(ctx, store.ProposalSet{HostID: nas.ID, RunID: runID, Items: []store.Proposal{
		{Fields: skillFields("profile"), Evidence: dockerPS},
		{Fields: skillFields("paperless"), Evidence: []skill.Evidence{{Argv: []string{"uname", "-a"}, Ran: false}}},
	}})
	if err != nil || len(got) != 2 {
		t.Fatalf("CreateProposals = %+v, %v", got, err)
	}
	p := got[0]
	if p.Name != "profile" || p.State != store.SkillProposed || p.Origin != store.SkillOriginProposed || p.HostID != nas.ID || p.HostName != "nas" ||
		p.RunID != runID || p.ReplacesID != 0 || p.ConfirmedAt != nil || len(p.Evidence) != 1 || p.Evidence[0].Argv[2] != "-a" || !p.Evidence[0].Ran {
		t.Errorf("proposal = %+v", p)
	}
	if got[1].Evidence[0].Ran {
		t.Errorf("the evidence that did not run lost its flag: %+v", got[1].Evidence)
	}
	log, _ := s.ListActivity(ctx, store.ActivityQuery{Limit: 5})
	if log[0].Kind != store.KindSkillsProposed || log[0].Summary != "Learned about nas: 2 proposals" || log[0].RunID != runID {
		t.Errorf("activity = %+v", log[0])
	}
	if n := count(activityKinds(t, s), store.KindSkillsProposed); n != 1 {
		t.Errorf("%d skills_proposed entries, want one for the whole set", n)
	}
	if got := propose(t, s, nas.ID, "single"); got[0].Name != "single" {
		t.Fatal("single")
	}
	if first := activitySummaries(t, s)[0]; first != "Learned about nas: 1 proposal" {
		t.Errorf("activity = %q", first)
	}
}

func TestCreateProposalsRedactsBeforeItStoresAndRefusesWhatIsInvalid(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")

	secret := store.Proposal{Fields: skill.Fields{Name: "db", Description: "login: password=hunter2hunter2", Body: "export API_TOKEN=abcdef123456"}, Evidence: dockerPS}
	got, err := s.CreateProposals(ctx, store.ProposalSet{HostID: nas.ID, Items: []store.Proposal{secret}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got[0].Description, "hunter2hunter2") || strings.Contains(got[0].Body, "abcdef123456") {
		t.Errorf("a secret was stored: %+v", got[0])
	}

	before := len(proposals(t, s))
	kindsBefore := len(activityKinds(t, s))
	var fe *skill.FieldError
	bad := store.Proposal{Fields: skill.Fields{Name: "Not A Name", Description: "d", Body: "b"}}
	if _, err := s.CreateProposals(ctx, store.ProposalSet{HostID: nas.ID, Items: []store.Proposal{{Fields: skillFields("fine")}, bad}}); !errors.As(err, &fe) || fe.Field != "name" {
		t.Errorf("a set with a bad name = %v", err)
	}
	if _, err := s.CreateProposals(ctx, store.ProposalSet{HostID: nas.ID, Items: []store.Proposal{{Fields: skillFields("twice")}, {Fields: skillFields("twice")}}}); !errors.As(err, &fe) || fe.Field != "name" {
		t.Errorf("a name twice in one set = %v", err)
	}
	var nine []store.Proposal
	for i := 0; i < 9; i++ {
		nine = append(nine, store.Proposal{Fields: skillFields(fmt.Sprintf("p%d", i))})
	}
	if _, err := s.CreateProposals(ctx, store.ProposalSet{HostID: nas.ID, Items: nine}); !errors.As(err, &fe) || fe.Field != "skills" {
		t.Errorf("nine proposals = %v", err)
	}
	if _, err := s.CreateProposals(ctx, store.ProposalSet{HostID: 999, Items: []store.Proposal{{Fields: skillFields("x")}}}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an unknown host = %v", err)
	}
	if _, err := s.CreateProposals(ctx, store.ProposalSet{HostID: 0, Items: []store.Proposal{{Fields: skillFields("x")}}}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("no host (a global proposal) = %v", err)
	}
	if len(proposals(t, s)) != before || len(activityKinds(t, s)) != kindsBefore {
		t.Error("a refused set left a proposal or an activity entry behind")
	}
	if got, err := s.CreateProposals(ctx, store.ProposalSet{HostID: nas.ID}); err != nil || len(got) != 0 || len(activityKinds(t, s)) != kindsBefore {
		t.Errorf("an empty set = %v, %v, want nothing stored and nothing logged", got, err)
	}
}

func TestAProposalDoesNotCountAgainstTheLimitAndNeverCollidesWithAnActiveSkill(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")
	for i := 0; i < skill.MaxPerScope; i++ {
		if _, err := s.CreateSkill(ctx, nas.ID, skillFields(fmt.Sprintf("s%02d", i))); err != nil {
			t.Fatal(err)
		}
	}
	got := propose(t, s, nas.ID, "new-one", "s00")
	if got[0].ReplacesID != 0 {
		t.Errorf("a new name replaces %d", got[0].ReplacesID)
	}
	active, _ := s.ListSkills(ctx, store.SkillFilter{HostID: nas.ID, State: store.SkillActive})
	if got[1].ReplacesID != active[0].ID || active[0].Name != "s00" {
		t.Errorf("the proposal for s00 replaces %d, want %d", got[1].ReplacesID, active[0].ID)
	}
	if len(active) != skill.MaxPerScope {
		t.Errorf("%d active skills", len(active))
	}
}

func TestANewProposalReplacesThePendingOneOfTheSameName(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas, alpha := hostNamed(t, s, "nas"), hostNamed(t, s, "alpha")
	first := propose(t, s, nas.ID, "paperless", "profile")
	propose(t, s, alpha.ID, "paperless")
	second := propose(t, s, nas.ID, "paperless")

	var onNas []string
	for _, p := range proposals(t, s) {
		if p.HostID == nas.ID {
			onNas = append(onNas, p.Name)
		}
	}
	if strings.Join(onNas, " ") != "paperless profile" {
		t.Errorf("proposals of nas = %v, want one paperless and the profile", onNas)
	}
	if n := len(proposals(t, s)); n != 3 {
		t.Errorf("%d proposals, want 3 (the other host's is untouched)", n)
	}
	if _, err := s.GetSkill(ctx, first[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the older proposal of the same name is still there: %v", err)
	}
	if got, err := s.GetSkill(ctx, second[0].ID); err != nil || got.State != store.SkillProposed {
		t.Errorf("the newer proposal = %+v, %v", got, err)
	}
}

func TestConfirmMakesAProposalActiveAndKeepsItsOrigin(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")
	p := propose(t, s, nas.ID, "paperless")[0]

	got, err := s.ConfirmSkill(ctx, p.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != p.ID || got.State != store.SkillActive || got.Origin != store.SkillOriginProposed || got.ConfirmedAt == nil || got.RunID != p.RunID ||
		len(got.Evidence) != 1 || got.Description != p.Description || got.Body != p.Body {
		t.Errorf("confirmed = %+v", got)
	}
	if first := activitySummaries(t, s)[0]; first != "Skill paperless confirmed (host nas)" {
		t.Errorf("activity = %q", first)
	}
	if _, err := s.ConfirmSkill(ctx, p.ID, nil); !errors.Is(err, store.ErrSkillState) {
		t.Errorf("a second confirmation = %v, want ErrSkillState", err)
	}
	if _, err := s.ConfirmSkill(ctx, 999, nil); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an unknown skill = %v", err)
	}
	if n := count(activityKinds(t, s), store.KindSkillConfirmed); n != 1 {
		t.Errorf("%d confirmed entries", n)
	}
}

func TestConfirmWithAnEditUsesTheEditedTextAndRefusesABadOne(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")
	p := propose(t, s, nas.ID, "paperless")[0]

	var fe *skill.FieldError
	if _, err := s.ConfirmSkill(ctx, p.ID, &skill.Fields{Description: "", Body: "b"}); !errors.As(err, &fe) || fe.Field != "description" {
		t.Errorf("an empty description = %v", err)
	}
	if _, err := s.ConfirmSkill(ctx, p.ID, &skill.Fields{Description: "d", Body: strings.Repeat("x", 9000)}); !errors.As(err, &fe) || fe.Field != "body" {
		t.Errorf("a long body = %v", err)
	}
	if got, _ := s.GetSkill(ctx, p.ID); got.State != store.SkillProposed || got.Body != p.Body {
		t.Fatalf("a refused edit changed the proposal: %+v", got)
	}
	got, err := s.ConfirmSkill(ctx, p.ID, &skill.Fields{Name: "ignored", Description: "Edited description.", Body: "Edited body."})
	if err != nil || got.Name != "paperless" || got.Description != "Edited description." || got.Body != "Edited body." || got.State != store.SkillActive {
		t.Fatalf("confirmed with an edit = %+v, %v", got, err)
	}
}

func TestConfirmingAReplacingProposalUpdatesTheReplacedSkillInPlace(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")
	old, _ := s.CreateSkill(ctx, nas.ID, skillFields("paperless"))
	if _, err := s.UpdateSkill(ctx, old.ID, skillFields("paperless"), store.SkillOff); err != nil {
		t.Fatal(err)
	}
	p := propose(t, s, nas.ID, "paperless")[0]
	if p.ReplacesID != old.ID {
		t.Fatalf("the proposal replaces %d, want %d", p.ReplacesID, old.ID)
	}

	got, err := s.ConfirmSkill(ctx, p.ID, &skill.Fields{Description: "Replaced.", Body: "Replaced body."})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != old.ID || got.Description != "Replaced." || got.Body != "Replaced body." || got.Origin != store.SkillOriginProposed || got.State != store.SkillActive ||
		got.ConfirmedAt == nil || got.RunID != p.RunID || got.ReplacesID != 0 {
		t.Errorf("the replaced skill = %+v", got)
	}
	if _, err := s.GetSkill(ctx, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the proposal is still there: %v", err)
	}
	all, _ := s.ListSkills(ctx, store.SkillFilter{})
	if len(all) != 1 {
		t.Errorf("%d skills, want the one that was updated", len(all))
	}
	if first := activitySummaries(t, s)[0]; first != "Skill paperless confirmed (host nas); it replaces the earlier text" {
		t.Errorf("activity = %q", first)
	}
}

func TestConfirmingAProposalWhoseReplacedSkillWasDeletedCreatesANewSkill(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")
	old, _ := s.CreateSkill(ctx, nas.ID, skillFields("paperless"))
	p := propose(t, s, nas.ID, "paperless")[0]
	if err := s.DeleteSkill(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.ConfirmSkill(ctx, p.ID, nil)
	if err != nil || got.ID != p.ID || got.State != store.SkillActive || got.Origin != store.SkillOriginProposed {
		t.Fatalf("confirmed = %+v, %v", got, err)
	}
}

func TestConfirmingRefusesToOverwriteASkillTheMaintainerWroteMeanwhile(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")
	old, _ := s.CreateSkill(ctx, nas.ID, skillFields("paperless"))
	p := propose(t, s, nas.ID, "paperless")[0]
	if err := s.DeleteSkill(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	mine, err := s.CreateSkill(ctx, nas.ID, skill.Fields{Name: "paperless", Description: "Mine.", Body: "My own note."})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.ConfirmSkill(ctx, p.ID, nil); !errors.Is(err, store.ErrExists) {
		t.Fatalf("Confirm = %v, want ErrExists", err)
	}
	if got, _ := s.GetSkill(ctx, mine.ID); got.Body != "My own note." || got.Origin != store.SkillOriginWritten {
		t.Errorf("the maintainer's skill was changed: %+v", got)
	}
	if got, _ := s.GetSkill(ctx, p.ID); got.State != store.SkillProposed {
		t.Errorf("the proposal after a refused confirmation: %+v", got)
	}
}

func TestConfirmingAtTheLimitIsRefusedUnlessItReplaces(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")
	for i := 0; i < skill.MaxPerScope; i++ {
		if _, err := s.CreateSkill(ctx, nas.ID, skillFields(fmt.Sprintf("s%02d", i))); err != nil {
			t.Fatal(err)
		}
	}
	both := propose(t, s, nas.ID, "brand-new", "s00")
	if _, err := s.ConfirmSkill(ctx, both[0].ID, nil); !errors.Is(err, store.ErrScopeFull) {
		t.Errorf("a new skill at the limit = %v, want ErrScopeFull", err)
	}
	if _, err := s.ConfirmSkill(ctx, both[1].ID, nil); err != nil {
		t.Errorf("a replacing confirmation at the limit = %v", err)
	}
	if got, _ := s.GetSkill(ctx, both[0].ID); got.State != store.SkillProposed {
		t.Errorf("the refused proposal = %+v", got)
	}
}

func TestRejectDeletesTheProposalAndLogsIt(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")
	p := propose(t, s, nas.ID, "paperless")[0]
	if err := s.RejectSkill(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSkill(ctx, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the rejected proposal is still there: %v", err)
	}
	if first := activitySummaries(t, s)[0]; first != "Proposal paperless rejected (host nas)" {
		t.Errorf("activity = %q", first)
	}
	if n := count(activityKinds(t, s), store.KindSkillRejected); n != 1 {
		t.Errorf("%d rejected entries", n)
	}
	if err := s.RejectSkill(ctx, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a second rejection = %v", err)
	}
	active, _ := s.CreateSkill(ctx, nas.ID, skillFields("mine"))
	if err := s.RejectSkill(ctx, active.ID); !errors.Is(err, store.ErrSkillState) {
		t.Errorf("rejecting an active skill = %v, want ErrSkillState", err)
	}
}

func TestAProposalCanOnlyBeConfirmedOrRejectedNotEditedOrDeleted(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")
	p := propose(t, s, nas.ID, "paperless")[0]
	if _, err := s.UpdateSkill(ctx, p.ID, skillFields("paperless"), store.SkillActive); !errors.Is(err, store.ErrSkillState) {
		t.Errorf("UpdateSkill on a proposal = %v, want ErrSkillState", err)
	}
	if err := s.DeleteSkill(ctx, p.ID); !errors.Is(err, store.ErrSkillState) {
		t.Errorf("DeleteSkill on a proposal = %v, want ErrSkillState", err)
	}
	if got, _ := s.GetSkill(ctx, p.ID); got.State != store.SkillProposed {
		t.Errorf("the proposal = %+v", got)
	}
}

func TestDeletingAHostDeletesItsProposalsToo(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas, alpha := hostNamed(t, s, "nas"), hostNamed(t, s, "alpha")
	propose(t, s, nas.ID, "a", "b")
	propose(t, s, alpha.ID, "c")
	if err := s.DeleteHost(ctx, nas.ID); err != nil {
		t.Fatal(err)
	}
	left := proposals(t, s)
	if len(left) != 1 || left[0].Name != "c" {
		t.Errorf("proposals left = %+v", left)
	}
	if first := activitySummaries(t, s)[0]; first != "Host nas removed; 2 skills removed with it" {
		t.Errorf("activity = %q", first)
	}
}
