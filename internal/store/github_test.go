package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

func connection(login string) store.Connection {
	return store.Connection{
		TokenCiphertext: []byte{0xde, 0xad, 0xbe, 0xef},
		TokenHint:       "1234",
		Login:           login,
		Status:          store.ConnOK,
		CheckedAt:       time.Now(),
	}
}

func TestSaveAndGetConnection(t *testing.T) {
	s, ctx := openStore(t), context.Background()

	if _, err := s.GetConnection(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetConnection on an empty store = %v, want ErrNotFound", err)
	}

	if err := s.SaveConnection(ctx, connection("octo")); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetConnection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Login != "octo" || got.TokenHint != "1234" || got.Status != store.ConnOK ||
		string(got.TokenCiphertext) != "\xde\xad\xbe\xef" || got.ID != store.ConnectionID {
		t.Fatalf("connection = %+v", got)
	}

	if err := s.SaveConnection(ctx, connection("someone-else")); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetConnection(ctx)
	if got.Login != "someone-else" {
		t.Fatalf("saving again must replace the connection, got login %q", got.Login)
	}
}

func TestUpdateConnectionStatus(t *testing.T) {
	s, ctx := openStore(t), context.Background()

	if err := s.UpdateConnectionStatus(ctx, store.ConnError, "x", time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update without a connection = %v, want ErrNotFound", err)
	}

	_ = s.SaveConnection(ctx, connection("octo"))
	at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	if err := s.UpdateConnectionStatus(ctx, store.ConnUndecryptable, "wrong key", at); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetConnection(ctx)
	if got.Status != store.ConnUndecryptable || got.StatusDetail != "wrong key" || !got.CheckedAt.Equal(at) {
		t.Fatalf("connection = %+v", got)
	}
}

func TestDeleteConnectionRemovesItsRepos(t *testing.T) {
	s, ctx := openStore(t), context.Background()

	if err := s.DeleteConnection(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete without a connection = %v, want ErrNotFound", err)
	}

	_ = s.SaveConnection(ctx, connection("octo"))
	if _, err := s.AddRepo(ctx, store.ConnectionID, "octo/hello", "main"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteConnection(ctx); err != nil {
		t.Fatal(err)
	}
	repos, err := s.ListRepos(ctx)
	if err != nil || len(repos) != 0 {
		t.Fatalf("repos after deleting the connection = %+v, %v", repos, err)
	}
}

func TestAddAndListRepos(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	_ = s.SaveConnection(ctx, connection("octo"))

	b, err := s.AddRepo(ctx, store.ConnectionID, "octo/bravo", "main")
	if err != nil {
		t.Fatal(err)
	}
	if !b.Enabled || b.DefaultBranch != "main" || b.FullName != "octo/bravo" || b.LastPolledAt != nil || b.LastError != "" {
		t.Fatalf("repo = %+v", b)
	}
	if _, err := s.AddRepo(ctx, store.ConnectionID, "octo/alpha", "trunk"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.AddRepo(ctx, store.ConnectionID, "OCTO/Bravo", "main"); !errors.Is(err, store.ErrExists) {
		t.Fatalf("duplicate (different case) = %v, want ErrExists", err)
	}

	repos, err := s.ListRepos(ctx)
	if err != nil || len(repos) != 2 || repos[0].FullName != "octo/alpha" || repos[1].FullName != "octo/bravo" {
		t.Fatalf("repos = %+v, %v", repos, err)
	}

	got, err := s.GetRepo(ctx, b.ID)
	if err != nil || got.FullName != "octo/bravo" {
		t.Fatalf("GetRepo = %+v, %v", got, err)
	}
	if _, err := s.GetRepo(ctx, 9999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetRepo(unknown) = %v, want ErrNotFound", err)
	}
}

func TestAddRepoNeedsAConnection(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	_, err := s.AddRepo(ctx, store.ConnectionID, "octo/hello", "main")
	if err == nil || errors.Is(err, store.ErrExists) {
		t.Fatalf("AddRepo without a connection = %v, want a foreign key error", err)
	}
}

func TestSetRepoEnabledAndDelete(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	_ = s.SaveConnection(ctx, connection("octo"))
	r, _ := s.AddRepo(ctx, store.ConnectionID, "octo/hello", "main")

	if err := s.SetRepoEnabled(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetRepo(ctx, r.ID); got.Enabled {
		t.Fatal("repo is still enabled")
	}
	if err := s.SetRepoEnabled(ctx, 9999, true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SetRepoEnabled(unknown) = %v, want ErrNotFound", err)
	}

	if err := s.DeleteRepo(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRepo(ctx, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}
