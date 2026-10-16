package store_test

import (
	"context"
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

func settingActivity(summary string) store.NewActivity {
	return store.NewActivity{Kind: store.KindSettingChanged, Summary: summary}
}

func TestASettingIsSetReplacedListedAndDeleted(t *testing.T) {
	s, ctx := openStore(t), context.Background()

	if rows, err := s.ListSettings(ctx); err != nil || len(rows) != 0 {
		t.Fatalf("a new database: %+v, %v", rows, err)
	}
	if err := s.SetSetting(ctx, "poll_interval", `"30s"`, settingActivity("first")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "poll_interval", `"45s"`, settingActivity("second")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "diagnose_max_per_day", `5`, settingActivity("third")); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListSettings(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	if rows[0].Key != "diagnose_max_per_day" || rows[0].Value != `5` || rows[1].Key != "poll_interval" || rows[1].Value != `"45s"` || rows[1].UpdatedAt.IsZero() {
		t.Fatalf("rows = %+v", rows)
	}

	removed, err := s.DeleteSetting(ctx, "poll_interval", settingActivity("reset"))
	if err != nil || !removed {
		t.Fatalf("delete = %v, %v", removed, err)
	}
	removed, err = s.DeleteSetting(ctx, "poll_interval", settingActivity("reset again"))
	if err != nil || removed {
		t.Fatalf("deleting a key that is not there = %v, %v", removed, err)
	}
	if rows, _ := s.ListSettings(ctx); len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestEveryChangeOfASettingWritesItsActivityEntryAndNothingElseDoes(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	_ = s.SetSetting(ctx, "poll_interval", `"30s"`, settingActivity("set"))
	_, _ = s.DeleteSetting(ctx, "poll_interval", settingActivity("reset"))
	_, _ = s.DeleteSetting(ctx, "poll_interval", settingActivity("must not appear"))

	got := activitySummaries(t, s) // newest first
	if len(got) != 2 || got[0] != "reset" || got[1] != "set" {
		t.Fatalf("activity = %v", got)
	}
	log, _ := s.ListActivity(ctx, store.ActivityQuery{})
	if log[0].Kind != store.KindSettingChanged {
		t.Errorf("kind = %q", log[0].Kind)
	}
}
