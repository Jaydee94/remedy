package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/skill"
	"github.com/Jaydee94/remedy/internal/store"
)

// listing is "name" for a global skill and "host/name" for a host's, space separated, in the order of the result.
func listing(list []store.Skill) string {
	var out []string
	for _, k := range list {
		if k.HostName == "" {
			out = append(out, k.Name)
		} else {
			out = append(out, k.HostName+"/"+k.Name)
		}
	}
	return strings.Join(out, " ")
}

type visibleRig struct {
	s                *store.Store
	nas, alpha, gone store.Host
	paperless        store.Skill
}

// newVisibleRig makes every kind of skill that must not be visible next to the ones that must.
func newVisibleRig(t *testing.T) *visibleRig {
	t.Helper()
	s, ctx := openStore(t), context.Background()
	r := &visibleRig{s: s, nas: hostNamed(t, s, "nas"), alpha: hostNamed(t, s, "alpha"), gone: hostNamed(t, s, "gone")}
	mk := func(host int64, name, state string) store.Skill {
		k, err := s.CreateSkill(ctx, host, skillFields(name))
		if err != nil {
			t.Fatal(err)
		}
		if state != store.SkillActive {
			if k, err = s.UpdateSkill(ctx, k.ID, skillFields(name), state); err != nil {
				t.Fatal(err)
			}
		}
		return k
	}
	mk(0, "lab-notes", store.SkillActive)
	mk(0, "global-off", store.SkillOff)
	r.paperless = mk(r.nas.ID, "paperless", store.SkillActive)
	mk(r.nas.ID, "nas-off", store.SkillOff)
	mk(r.alpha.ID, "alpha-only", store.SkillActive)
	mk(r.gone.ID, "gone-skill", store.SkillActive)
	propose(t, s, r.nas.ID, "pending-proposal")
	return r
}

// skills is RunSkills with its error checked: a failing call must fail the test, not pass as an empty list.
func (r *visibleRig) skills(t *testing.T, runID string) []store.Skill {
	t.Helper()
	got, err := r.s.RunSkills(context.Background(), runID)
	if err != nil {
		t.Fatalf("RunSkills(%s): %v", runID, err)
	}
	return got
}

func (r *visibleRig) run(t *testing.T, hosts ...int64) string {
	t.Helper()
	created, err := r.s.CreateRunWith(context.Background(), store.RunOptions{Provider: "claude", Prompt: "p", Tools: true, HostIDs: hosts})
	if err != nil {
		t.Fatal(err)
	}
	return created.ID
}

func TestARunSeesTheActiveGlobalSkillsAndTheActiveSkillsOfItsHostsOnly(t *testing.T) {
	r, ctx := newVisibleRig(t), context.Background()
	for _, c := range []struct {
		name  string
		hosts []int64
		want  string
	}{
		{"a run without hosts", nil, "lab-notes"},
		{"a run with the nas", []int64{r.nas.ID}, "lab-notes nas/paperless"},
		{"a run with two hosts", []int64{r.nas.ID, r.alpha.ID}, "lab-notes alpha/alpha-only nas/paperless"},
	} {
		got, err := r.s.RunSkills(ctx, r.run(t, c.hosts...))
		if err != nil || listing(got) != c.want {
			t.Errorf("RunSkills for %s = %q, %v, want %q", c.name, listing(got), err, c.want)
		}
		byHosts, err := r.s.VisibleSkills(ctx, c.hosts)
		if err != nil || listing(byHosts) != c.want {
			t.Errorf("VisibleSkills for %s = %q, %v, want %q", c.name, listing(byHosts), err, c.want)
		}
	}
}

func TestNoProposalNoOffSkillAndNoOtherHostsSkillIsEverVisible(t *testing.T) {
	r, ctx := newVisibleRig(t), context.Background()
	everything := r.run(t, r.nas.ID, r.alpha.ID, r.gone.ID)
	got := r.skills(t, everything)
	for _, hidden := range []string{"pending-proposal", "global-off", "nas-off"} {
		if strings.Contains(listing(got), hidden) {
			t.Errorf("%s is visible: %q", hidden, listing(got))
		}
	}
	for _, k := range got {
		if k.State != store.SkillActive {
			t.Errorf("%s has the state %s", k.Name, k.State)
		}
	}
	all, err := r.s.VisibleSkills(ctx, []int64{r.nas.ID, r.alpha.ID, r.gone.ID})
	if err != nil || listing(all) != "lab-notes alpha/alpha-only gone/gone-skill nas/paperless" {
		t.Errorf("VisibleSkills for every host = %q, %v", listing(all), err)
	}
}

func TestAnUnknownRunSeesTheGlobalSkillsAndTheResultIsNeverNil(t *testing.T) {
	r, ctx := newVisibleRig(t), context.Background()
	got, err := r.s.RunSkills(ctx, "no-such-run")
	if err != nil || listing(got) != "lab-notes" {
		t.Errorf("an unknown run = %q, %v", listing(got), err)
	}
	empty := openStore(t)
	for name, f := range map[string]func() ([]store.Skill, error){
		"RunSkills":     func() ([]store.Skill, error) { return empty.RunSkills(ctx, "x") },
		"VisibleSkills": func() ([]store.Skill, error) { return empty.VisibleSkills(ctx, []int64{1}) },
	} {
		if list, err := f(); err != nil || list == nil || len(list) != 0 {
			t.Errorf("%s on an empty store = %#v, %v", name, list, err)
		}
	}
}

func TestADeletedHostTakesItsSkillsOutOfTheVisibleSetOfARunThatHadIt(t *testing.T) {
	r, ctx := newVisibleRig(t), context.Background()
	id := r.run(t, r.nas.ID, r.gone.ID)
	if got := r.skills(t, id); !strings.Contains(listing(got), "gone/gone-skill") {
		t.Fatalf("before the delete: %q", listing(got))
	}
	if err := r.s.DeleteHost(ctx, r.gone.ID); err != nil {
		t.Fatal(err)
	}
	got := r.skills(t, id)
	if listing(got) != "lab-notes nas/paperless" {
		t.Errorf("after the delete: %q", listing(got))
	}
}

func TestTurningASkillOffOrDeletingItTakesItOutAtOnceAndAConfirmedProposalComesIn(t *testing.T) {
	r, ctx := newVisibleRig(t), context.Background()
	id := r.run(t, r.nas.ID)
	if _, err := r.s.UpdateSkill(ctx, r.paperless.ID, skillFields("paperless"), store.SkillOff); err != nil {
		t.Fatal(err)
	}
	if got := r.skills(t, id); listing(got) != "lab-notes" {
		t.Errorf("after turning it off: %q", listing(got))
	}
	if _, err := r.s.UpdateSkill(ctx, r.paperless.ID, skillFields("paperless"), store.SkillActive); err != nil {
		t.Fatal(err)
	}
	pending, err := r.s.ListSkills(ctx, store.SkillFilter{State: store.SkillProposed})
	if err != nil || len(pending) == 0 {
		t.Fatalf("ListSkills(proposed) = %v, %v", pending, err)
	}
	if _, err := r.s.ConfirmSkill(ctx, pending[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	if got := r.skills(t, id); listing(got) != "lab-notes nas/paperless nas/pending-proposal" {
		t.Errorf("after the confirmation: %q", listing(got))
	}
	if err := r.s.DeleteSkill(ctx, r.paperless.ID); err != nil {
		t.Fatal(err)
	}
	if got := r.skills(t, id); listing(got) != "lab-notes nas/pending-proposal" {
		t.Errorf("after the delete: %q", listing(got))
	}
}

func TestAReplacingProposalIsInvisibleUntilItIsConfirmedAndThenReplacesTheText(t *testing.T) {
	r, ctx := newVisibleRig(t), context.Background()
	id := r.run(t, r.nas.ID)
	made, err := r.s.CreateProposals(ctx, store.ProposalSet{HostID: r.nas.ID, RunID: learningRun(t, r.s), Items: []store.Proposal{
		{Fields: skill.Fields{Name: "paperless", Description: "Proposed description.", Body: "Proposed body."}},
	}})
	if err != nil || made[0].ReplacesID != r.paperless.ID {
		t.Fatalf("CreateProposals = %+v, %v", made, err)
	}

	bodies := func() []string {
		list := r.skills(t, id)
		var out []string
		for _, k := range list {
			if k.Name == "paperless" {
				out = append(out, k.Body)
			}
		}
		return out
	}
	if got := bodies(); len(got) != 1 || got[0] != "Body of paperless." {
		t.Errorf("before the confirmation the run sees %q, want the current text only", got)
	}
	if _, err := r.s.ConfirmSkill(ctx, made[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	if got := bodies(); len(got) != 1 || got[0] != "Proposed body." {
		t.Errorf("after the confirmation the run sees %q, want the new text once", got)
	}
}

// A host that is switched off keeps its skills visible to a run that already has it. The choice is deliberate: a skill only informs the
// reasoning, the host tools are the barrier and they refuse to run on a disabled host, and the outage path drops disabled hosts before it
// reads the host's skills. Hiding them here would make the answer of skill_get depend on a switch that does not protect anything.
func TestASkillOfADisabledHostStaysVisibleToARunThatHasTheHost(t *testing.T) {
	r, ctx := newVisibleRig(t), context.Background()
	id := r.run(t, r.nas.ID)
	off := hostFields("nas")
	off.Enabled = false
	if _, _, err := r.s.UpdateHost(ctx, r.nas.ID, off); err != nil {
		t.Fatal(err)
	}
	if got := r.skills(t, id); listing(got) != "lab-notes nas/paperless" {
		t.Errorf("after disabling the host: %q, want its active skill to stay", listing(got))
	}
	byHosts, err := r.s.VisibleSkills(ctx, []int64{r.nas.ID})
	if err != nil || listing(byHosts) != "lab-notes nas/paperless" {
		t.Errorf("VisibleSkills for the disabled host = %q, %v", listing(byHosts), err)
	}
}
