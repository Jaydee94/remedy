package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

// These tests came with migration 008 (incidents of more than one source) and test the store, not the migration: they
// moved here when the SQLite migration tests were retired.

func TestOpenIncidentRefusesWhatCannotBeStored(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	act := store.NewActivity{Kind: store.KindIncidentOpened, Summary: "opened"}
	for name, n := range map[string]store.NewIncident{
		"an alert without a key":     {Source: store.SourceAlertmanager, Title: "t", Conclusion: "firing"},
		"an alert without a title":   {Source: store.SourceAlertmanager, Key: "k", Conclusion: "firing"},
		"a source nobody knows":      {Source: "loki", Key: "k", Title: "t", Conclusion: "firing"},
		"a severity nobody knows":    {Source: store.SourceAlertmanager, Key: "k", Title: "t", Severity: "urgent", Conclusion: "firing"},
		"details that are no JSON":   {Source: store.SourceAlertmanager, Key: "k", Title: "t", Conclusion: "firing", Details: []byte(`{`)},
		"a GitHub incident, no repo": {Ref: "pr:1", CheckName: "go", Conclusion: "failure", HeadSHA: "a"},
		"an alert with a repo":       {Source: store.SourceAlertmanager, RepoID: 1, Key: "k", Title: "t", Conclusion: "firing"},
	} {
		if _, err := s.OpenIncident(ctx, n, act); err == nil || errors.Is(err, store.ErrExists) {
			t.Errorf("%s: err = %v, want a refusal", name, err)
		}
	}
	if list, _ := s.ListIncidents(ctx, store.IncidentFilter{State: "all"}); len(list) != 0 {
		t.Errorf("a refused incident was stored: %+v", list)
	}
	if log, _ := s.ListActivity(ctx, store.ActivityQuery{}); len(log) != 0 {
		t.Errorf("a refused incident left activity: %+v", log)
	}
}

func TestListIncidentsFiltersBySource(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	if _, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenIncident(ctx, store.NewIncident{Source: store.SourceArgoCD, Key: "guestbook", Title: "guestbook is Degraded", Conclusion: "degraded"},
		store.NewActivity{Kind: store.KindIncidentOpened, Summary: "argo"}); err != nil {
		t.Fatal(err)
	}
	for source, want := range map[string]int{"": 2, store.SourceGitHub: 1, store.SourceArgoCD: 1, store.SourceAlertmanager: 0} {
		list, err := s.ListIncidents(ctx, store.IncidentFilter{State: "all", Source: source})
		if err != nil || len(list) != want {
			t.Errorf("source %q: %d incidents, %v, want %d", source, len(list), err, want)
		}
	}
	// A repository filter still means the incidents of that repository.
	if list, _ := s.ListIncidents(ctx, store.IncidentFilter{State: "all", RepoID: repo.ID}); len(list) != 1 || list[0].Source != store.SourceGitHub {
		t.Errorf("repo filter = %+v", list)
	}
}

func TestGitHubKeyTellsFieldsApart(t *testing.T) {
	keys := map[string]bool{}
	for _, k := range []string{
		store.GitHubKey(1, "branch:a", "b|c"), store.GitHubKey(1, "branch:a|b", "c"), store.GitHubKey(11, "branch:a", "bc"),
		store.GitHubKey(1, "pr:7", "go"), store.GitHubKey(2, "pr:7", "go"), store.GitHubKey(1, "pr:7", "go "),
	} {
		if keys[k] {
			t.Errorf("two different incidents share the key %q", strings.ReplaceAll(k, "\x1f", "<US>"))
		}
		keys[k] = true
	}
}
