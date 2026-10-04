package store_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

func addEntries(t *testing.T, s *store.Store, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		err := s.AddActivity(context.Background(), store.NewActivity{Kind: store.KindPollRecovered, Summary: "entry " + strconv.Itoa(i)})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestListActivityNamesTheRepository(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	if err := s.AddActivity(ctx, entry(store.KindRepoAdded, repo.ID)); err != nil {
		t.Fatal(err)
	}
	if err := s.AddActivity(ctx, store.NewActivity{Kind: store.KindConnectionChanged, Summary: "connected"}); err != nil {
		t.Fatal(err)
	}

	log, err := s.ListActivity(ctx, store.ActivityQuery{})
	if err != nil || len(log) != 2 {
		t.Fatalf("activity = %+v, err = %v", log, err)
	}
	if log[0].RepoName != "" || log[0].RepoID != 0 || log[1].RepoName != "octo/hello" || log[1].RepoID != repo.ID {
		t.Fatalf("entries = %+v", log)
	}

	// Removing the repository keeps the entry; its summary still says what happened.
	if err := s.DeleteRepo(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	log, err = s.ListActivity(ctx, store.ActivityQuery{})
	if err != nil || len(log) != 2 {
		t.Fatalf("activity after the removal = %+v, err = %v", log, err)
	}
	if log[1].RepoID != 0 || log[1].RepoName != "" || log[1].Summary != "repo_added summary" {
		t.Fatalf("entry of the removed repository = %+v", log[1])
	}
}

func TestListActivityBeforeIsACursor(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	addEntries(t, s, 5)
	all, err := s.ListActivity(ctx, store.ActivityQuery{})
	if err != nil || len(all) != 5 {
		t.Fatalf("activity = %+v, err = %v", all, err)
	}

	page, err := s.ListActivity(ctx, store.ActivityQuery{Before: all[1].ID, Limit: 2})
	if err != nil || len(page) != 2 || page[0].ID != all[2].ID || page[1].ID != all[3].ID {
		t.Fatalf("page = %+v, err = %v, want the entries after the second one", page, err)
	}
	if rest, err := s.ListActivity(ctx, store.ActivityQuery{Before: all[4].ID}); err != nil || len(rest) != 0 {
		t.Fatalf("before the oldest entry = %+v, err = %v, want nothing", rest, err)
	}
}

func TestListActivitySinceReturnsTheNewerEntriesOldestFirst(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	if last, err := s.LastActivityID(ctx); err != nil || last != 0 {
		t.Fatalf("LastActivityID of an empty log = %d, %v, want 0", last, err)
	}
	addEntries(t, s, 5)
	all, _ := s.ListActivity(ctx, store.ActivityQuery{}) // newest first

	if last, err := s.LastActivityID(ctx); err != nil || last != all[0].ID {
		t.Fatalf("LastActivityID = %d, %v, want %d", last, err, all[0].ID)
	}

	got, err := s.ListActivitySince(ctx, all[3].ID, 10)
	if err != nil || len(got) != 3 || got[0].ID != all[2].ID || got[1].ID != all[1].ID || got[2].ID != all[0].ID {
		t.Fatalf("since the second oldest = %+v, err = %v, want the three newer ones, oldest first", got, err)
	}
	got, err = s.ListActivitySince(ctx, 0, 2)
	if err != nil || len(got) != 2 || got[0].ID != all[4].ID || got[1].ID != all[3].ID {
		t.Fatalf("since 0 with a limit of 2 = %+v, err = %v, want the two oldest", got, err)
	}
	if got, err := s.ListActivitySince(ctx, all[0].ID, 10); err != nil || len(got) != 0 {
		t.Fatalf("since the newest = %+v, err = %v, want nothing", got, err)
	}
}
