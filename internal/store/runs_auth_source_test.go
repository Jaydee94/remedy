package store

import (
	"context"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
)

// runs.auth_source says how a run was billed, from the run's own init event. Only the two words and the empty string exist.
func TestARunKeepsHowItWasBilled(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, source := range []string{"subscription", "api_key", ""} {
		r, err := s.CreateRun(ctx, "claude", "p")
		if err != nil {
			t.Fatal(err)
		}
		if c, err := s.ClaimNext(ctx); err != nil || c == nil || c.ID != r.ID {
			t.Fatalf("ClaimNext = %+v, %v", c, err)
		}
		if err := s.FinishRun(ctx, r.ID, run.Outcome{ExitCode: 0, Result: "x", AuthSource: source}); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetRun(ctx, r.ID)
		if err != nil || got.AuthSource != source {
			t.Errorf("run %q: AuthSource = %q, err %v", source, got.AuthSource, err)
		}
		list, err := s.ListRuns(ctx, 10)
		if err != nil || len(list) == 0 || list[0].ID != r.ID || list[0].AuthSource != source {
			t.Errorf("list: first run %+v, err %v", list, err)
		}
	}
}

func TestOnlyTheTwoWordsAreAuthSources(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	r, _ := s.CreateRun(ctx, "claude", "p")
	if _, err := s.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(ctx, r.ID, run.Outcome{AuthSource: "banana"}); err == nil {
		t.Fatal("FinishRun accepted the auth source banana")
	}
}

func TestAnOlderRunHasNoAuthSource(t *testing.T) {
	_, db := schemaTest(t)
	var def, nullable string
	if err := db.QueryRowContext(context.Background(), `SELECT column_default, is_nullable FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'runs' AND column_name = 'auth_source'`).Scan(&def, &nullable); err != nil {
		t.Fatalf("runs.auth_source: %v", err)
	}
	if nullable != "NO" || def != "''::text" {
		t.Errorf("runs.auth_source is nullable %s with default %s, want NOT NULL DEFAULT ''", nullable, def)
	}
}
