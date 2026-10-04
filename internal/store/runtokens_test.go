package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

// claimedToolRun creates a tool run and lets a runner claim it.
func claimedToolRun(t *testing.T, s *store.Store) run.Run {
	t.Helper()
	ctx := context.Background()
	r, err := s.CreateToolRun(ctx, "claude", "use the tools")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimNext(ctx)
	if err != nil || claimed == nil || claimed.ID != r.ID {
		t.Fatalf("ClaimNext = %+v, %v, want %s", claimed, err, r.ID)
	}
	return *claimed
}

func TestCreateToolRunMarksTheRunAsHavingTools(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r, err := s.CreateToolRun(ctx, "claude", "use the tools")
	if err != nil {
		t.Fatal(err)
	}
	if !r.MCP || r.Status != run.Queued || r.Role != run.RoleAdhoc || r.CancelRequested {
		t.Fatalf("run = %+v", r)
	}
	plain, _ := s.CreateRun(ctx, "claude", "plain")
	if plain.MCP {
		t.Fatalf("a plain run has gatekeeper access: %+v", plain)
	}
}

func TestMintRunTokenGivesATokenToARunningToolRunOnce(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	queued, _ := s.CreateToolRun(ctx, "claude", "x")
	if _, err := s.MintRunToken(ctx, queued.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a queued run got a token: %v", err)
	}
	claimed, _ := s.ClaimNext(ctx)

	token, err := s.MintRunToken(ctx, claimed.ID)
	if err != nil || len(token) != 43 {
		t.Fatalf("token = %q (%d characters), err = %v, want 43 characters", token, len(token), err)
	}
	got, err := s.RunForToken(ctx, token)
	if err != nil || got.ID != claimed.ID || got.Status != run.Running || !got.MCP {
		t.Fatalf("RunForToken = %+v, %v", got, err)
	}
	if _, err := s.RunForToken(ctx, token+"x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a wrong token was accepted: %v", err)
	}
	if _, err := s.RunForToken(ctx, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an empty token was accepted: %v", err)
	}
	if _, err := s.MintRunToken(ctx, claimed.ID); !errors.Is(err, store.ErrExists) {
		t.Fatalf("a second token for the same run: %v, want ErrExists", err)
	}
}

func TestOnlyAToolRunGetsAToken(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	if _, err := s.CreateRun(ctx, "claude", "plain"); err != nil {
		t.Fatal(err)
	}
	claimed, _ := s.ClaimNext(ctx)
	if _, err := s.MintRunToken(ctx, claimed.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a plain run got a token: %v", err)
	}
	if _, err := s.MintRunToken(ctx, "no-such-run"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an unknown run got a token: %v", err)
	}
}

func TestTheTokenIsStoredOnlyAsAHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	claimed := claimedToolRun(t, s)
	token, err := s.MintRunToken(context.Background(), claimed.ID)
	if err != nil {
		t.Fatal(err)
	}

	// A second connection reads the table as it is on disk.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var stored []byte
	if err := db.QueryRow(`SELECT token_hash FROM run_tokens WHERE run_id = ?`, claimed.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(token))
	if !bytes.Equal(stored, sum[:]) {
		t.Fatalf("the stored value is not the SHA-256 of the token: %x", stored)
	}
	if bytes.Contains(stored, []byte(token)) {
		t.Fatal("the token is stored in plaintext")
	}
}

func TestTheTokenIsRevokedWhenTheRunEnds(t *testing.T) {
	ctx := context.Background()
	for name, end := range map[string]func(s *store.Store, r run.Run) error{
		"finished": func(s *store.Store, r run.Run) error { return s.FinishRun(ctx, r.ID, run.Outcome{Result: "ok"}) },
		"reaped": func(s *store.Store, r run.Run) error {
			ids, err := s.FailLostRuns(ctx, time.Now().Add(time.Hour), "stuck")
			if err == nil && len(ids) != 1 {
				t.Errorf("reaped %v, want the run", ids)
			}
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "test.db")
			s, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })

			claimed := claimedToolRun(t, s)
			token, err := s.MintRunToken(ctx, claimed.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.RunForToken(ctx, token); err != nil {
				t.Fatalf("the token does not work while the run runs: %v", err)
			}
			var revoked sql.NullString
			if err := db.QueryRow(`SELECT revoked_at FROM run_tokens WHERE run_id = ?`, claimed.ID).Scan(&revoked); err != nil || revoked.Valid {
				t.Fatalf("revoked_at = %v, %v while the run runs, want NULL", revoked, err)
			}

			if err := end(s, claimed); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT revoked_at FROM run_tokens WHERE run_id = ?`, claimed.ID).Scan(&revoked); err != nil || !revoked.Valid {
				t.Fatalf("revoked_at = %v, %v after the run ended, want a time", revoked, err)
			}
			if _, err := s.RunForToken(ctx, token); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("the token still works after the run ended: %v", err)
			}
		})
	}
}
