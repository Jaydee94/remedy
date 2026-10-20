package store_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/skill"
	"github.com/Jaydee94/remedy/internal/store"
)

func skillFields(name string) skill.Fields {
	return skill.Fields{Name: name, Description: "About " + name + ".", Body: "Body of " + name + "."}
}

func TestCreateSkillWritesAnActiveWrittenSkillAndItsActivity(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")

	host, err := s.CreateSkill(ctx, nas.ID, skillFields("paperless"))
	if err != nil {
		t.Fatal(err)
	}
	if host.HostID != nas.ID || host.HostName != "nas" || host.Origin != store.SkillOriginWritten || host.State != store.SkillActive ||
		host.RunID != "" || host.ReplacesID != 0 || host.ConfirmedAt != nil || len(host.Evidence) != 0 || host.CreatedAt.IsZero() || host.Body != "Body of paperless." {
		t.Errorf("host skill = %+v", host)
	}
	global, err := s.CreateSkill(ctx, 0, skillFields("lab-notes"))
	if err != nil || global.HostID != 0 || global.HostName != "" {
		t.Fatalf("global skill = %+v, %v", global, err)
	}
	got := activitySummaries(t, s)
	if len(got) < 2 || got[0] != "Skill lab-notes created (global)" || got[1] != "Skill paperless created (host nas)" {
		t.Errorf("activity = %q", got)
	}
	for _, line := range got {
		if strings.Contains(line, "Body of") || strings.Contains(line, "About ") {
			t.Errorf("the activity names the text of a skill: %q", line)
		}
	}
	if kinds := activityKinds(t, s); count(kinds, store.KindSkillCreated) != 2 {
		t.Errorf("kinds = %v", kinds)
	}
}

func TestCreateSkillRefusesWhatIsWrong(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")
	if _, err := s.CreateSkill(ctx, nas.ID, skillFields("a")); err != nil {
		t.Fatal(err)
	}

	if _, err := s.CreateSkill(ctx, nas.ID, skillFields("a")); !errors.Is(err, store.ErrExists) {
		t.Errorf("the same name for the same host = %v, want ErrExists", err)
	}
	if _, err := s.CreateSkill(ctx, 0, skillFields("a")); err != nil {
		t.Errorf("the same name for another scope = %v", err)
	}
	if _, err := s.CreateSkill(ctx, 0, skillFields("a")); !errors.Is(err, store.ErrExists) {
		t.Errorf("the same global name twice = %v, want ErrExists", err)
	}
	if _, err := s.CreateSkill(ctx, 999, skillFields("b")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an unknown host = %v, want ErrNotFound", err)
	}
	bad := skillFields("c")
	bad.Body = ""
	var fe *skill.FieldError
	if _, err := s.CreateSkill(ctx, 0, bad); !errors.As(err, &fe) || fe.Field != "body" {
		t.Errorf("an empty body = %v", err)
	}
	list, _ := s.ListSkills(ctx, store.SkillFilter{})
	if len(list) != 2 {
		t.Errorf("%d skills, want the 2 that were accepted", len(list))
	}
}

func TestAScopeHoldsAtMostThirtySkills(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")
	for i := 0; i < skill.MaxPerScope; i++ {
		if _, err := s.CreateSkill(ctx, nas.ID, skillFields(fmt.Sprintf("s%02d", i))); err != nil {
			t.Fatalf("skill %d: %v", i, err)
		}
	}
	if _, err := s.CreateSkill(ctx, nas.ID, skillFields("one-too-many")); !errors.Is(err, store.ErrScopeFull) {
		t.Fatalf("the 31st skill = %v, want ErrScopeFull", err)
	}
	if _, err := s.CreateSkill(ctx, 0, skillFields("global-is-another-scope")); err != nil {
		t.Errorf("a global skill when a host's scope is full = %v", err)
	}
	// An off skill still counts: it is kept.
	first, _ := s.ListSkills(ctx, store.SkillFilter{HostID: nas.ID})
	if _, err := s.UpdateSkill(ctx, first[0].ID, skillFields(first[0].Name), store.SkillOff); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSkill(ctx, nas.ID, skillFields("still-too-many")); !errors.Is(err, store.ErrScopeFull) {
		t.Errorf("with an off skill among them = %v, want ErrScopeFull", err)
	}
	if err := s.DeleteSkill(ctx, first[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSkill(ctx, nas.ID, skillFields("room-again")); err != nil {
		t.Errorf("after a delete = %v", err)
	}
}

func TestUpdateSkillChangesTheTextsAndTheStateAndSaysWhich(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")
	k, _ := s.CreateSkill(ctx, nas.ID, skillFields("paperless"))

	time.Sleep(5 * time.Millisecond) // so that a later updated_at is strictly later
	off, err := s.UpdateSkill(ctx, k.ID, skillFields("paperless"), store.SkillOff)
	if err != nil || off.State != store.SkillOff || off.Origin != store.SkillOriginWritten || !off.UpdatedAt.After(k.UpdatedAt) {
		t.Fatalf("off = %+v, %v", off, err)
	}
	on, err := s.UpdateSkill(ctx, k.ID, skillFields("paperless"), store.SkillActive)
	if err != nil || on.State != store.SkillActive {
		t.Fatalf("on = %+v, %v", on, err)
	}
	changed, err := s.UpdateSkill(ctx, k.ID, skill.Fields{Name: "paperless-ngx", Description: "New description.", Body: "New body."}, store.SkillActive)
	if err != nil || changed.Name != "paperless-ngx" || changed.Description != "New description." || changed.Body != "New body." {
		t.Fatalf("changed = %+v, %v", changed, err)
	}
	got := activitySummaries(t, s)
	want := []string{"Skill paperless-ngx changed (host nas)", "Skill paperless turned on (host nas)", "Skill paperless turned off (host nas)"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("activity %d = %q, want %q", i, got[i], w)
		}
	}
	if kinds := activityKinds(t, s); count(kinds, store.KindSkillUpdated) != 3 {
		t.Errorf("kinds = %v", kinds)
	}
}

func TestUpdateSkillRefusesWhatIsWrong(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	a, _ := s.CreateSkill(ctx, 0, skillFields("a"))
	if _, err := s.CreateSkill(ctx, 0, skillFields("b")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSkill(ctx, a.ID, skillFields("b"), store.SkillActive); !errors.Is(err, store.ErrExists) {
		t.Errorf("a name that is taken = %v, want ErrExists", err)
	}
	if _, err := s.UpdateSkill(ctx, 999, skillFields("x"), store.SkillActive); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an unknown skill = %v, want ErrNotFound", err)
	}
	var fe *skill.FieldError
	if _, err := s.UpdateSkill(ctx, a.ID, skillFields("a"), "proposed"); !errors.As(err, &fe) || fe.Field != "state" {
		t.Errorf("the state proposed = %v", err)
	}
	if _, err := s.UpdateSkill(ctx, a.ID, skill.Fields{Name: "A", Description: "d", Body: "b"}, store.SkillActive); !errors.As(err, &fe) || fe.Field != "name" {
		t.Errorf("a bad name = %v", err)
	}
	if got, _ := s.GetSkill(ctx, a.ID); got.Name != "a" || got.State != store.SkillActive {
		t.Errorf("a refused update changed the skill: %+v", got)
	}
}

func TestDeleteSkillRemovesItAndLogsIt(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	k, _ := s.CreateSkill(ctx, 0, skillFields("lab-notes"))
	if err := s.DeleteSkill(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSkill(ctx, k.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get after Delete = %v", err)
	}
	if err := s.DeleteSkill(ctx, k.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a second Delete = %v", err)
	}
	if got := activitySummaries(t, s); got[0] != "Skill lab-notes deleted (global)" {
		t.Errorf("activity = %q", got)
	}
}

func TestListSkillsIsOrderedAndFiltered(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas, alpha := hostNamed(t, s, "nas"), hostNamed(t, s, "alpha")
	for _, c := range []struct {
		host int64
		name string
	}{{nas.ID, "profile"}, {0, "zeta"}, {alpha.ID, "backup"}, {0, "alpha-notes"}, {nas.ID, "paperless"}} {
		if _, err := s.CreateSkill(ctx, c.host, skillFields(c.name)); err != nil {
			t.Fatal(err)
		}
	}
	off, _ := s.ListSkills(ctx, store.SkillFilter{HostID: nas.ID})
	if _, err := s.UpdateSkill(ctx, off[0].ID, skillFields(off[0].Name), store.SkillOff); err != nil {
		t.Fatal(err)
	}
	names := func(f store.SkillFilter) string {
		list, err := s.ListSkills(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, k := range list {
			out = append(out, k.HostName+"/"+k.Name)
		}
		return strings.Join(out, " ")
	}
	if got, want := names(store.SkillFilter{}), "/alpha-notes /zeta alpha/backup nas/paperless nas/profile"; got != want {
		t.Errorf("all = %q, want %q (global first, then by host and name)", got, want)
	}
	if got := names(store.SkillFilter{HostID: nas.ID}); got != "nas/paperless nas/profile" {
		t.Errorf("host = %q", got)
	}
	if got := names(store.SkillFilter{State: store.SkillOff}); got != "nas/paperless" {
		t.Errorf("state off = %q", got)
	}
	if list, err := s.ListSkills(ctx, store.SkillFilter{State: store.SkillProposed}); err != nil || list == nil || len(list) != 0 {
		t.Errorf("no proposals = %#v, %v, want an empty non-nil list", list, err)
	}
}

func TestDeletingAHostDeletesItsSkillsAndSaysHowMany(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas, alpha := hostNamed(t, s, "nas"), hostNamed(t, s, "alpha")
	for _, c := range []struct {
		host int64
		name string
	}{{nas.ID, "paperless"}, {nas.ID, "profile"}, {alpha.ID, "backup"}, {0, "lab-notes"}} {
		if _, err := s.CreateSkill(ctx, c.host, skillFields(c.name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteHost(ctx, nas.ID); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListSkills(ctx, store.SkillFilter{})
	if len(list) != 2 || list[0].Name != "lab-notes" || list[1].Name != "backup" {
		t.Fatalf("skills left = %+v, want the global one and alpha's", list)
	}
	if got := activitySummaries(t, s); got[0] != "Host nas removed; 2 skills removed with it" {
		t.Errorf("activity = %q", got)
	}
	if err := s.DeleteHost(ctx, alpha.ID); err != nil {
		t.Fatal(err)
	}
	if got := activitySummaries(t, s); got[0] != "Host alpha removed; 1 skill removed with it" {
		t.Errorf("activity = %q", got)
	}
	empty := hostNamed(t, s, "plain")
	if err := s.DeleteHost(ctx, empty.ID); err != nil {
		t.Fatal(err)
	}
	if got := activitySummaries(t, s); got[0] != "Host plain removed" {
		t.Errorf("a host without skills: %q", got)
	}
}
