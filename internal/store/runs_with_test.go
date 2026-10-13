package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

func hostNamed(t *testing.T, s *store.Store, name string) store.Host {
	t.Helper()
	h, err := s.CreateHost(context.Background(), hostFields(name))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestCreateRunWithSetsWhatWasAskedFor(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas, alpha := hostNamed(t, s, "nas"), hostNamed(t, s, "alpha")

	plain, err := s.CreateRunWith(ctx, store.RunOptions{Provider: "claude", Prompt: "p"})
	if err != nil || plain.MCP || plain.Cluster || plain.HostCount != 0 || plain.IncidentID != nil || plain.Status != "queued" {
		t.Fatalf("plain run = %+v, %v", plain, err)
	}
	tools, _ := s.CreateRunWith(ctx, store.RunOptions{Provider: "claude", Prompt: "p", Tools: true})
	if !tools.MCP || tools.Cluster || tools.HostCount != 0 {
		t.Fatalf("tools run = %+v", tools)
	}
	cluster, _ := s.CreateRunWith(ctx, store.RunOptions{Provider: "claude", Prompt: "p", Cluster: true})
	if !cluster.MCP || !cluster.Cluster {
		t.Fatalf("cluster run = %+v: the cluster tools need the gatekeeper", cluster)
	}

	hosts, err := s.CreateRunWith(ctx, store.RunOptions{Provider: "claude", Prompt: "p", HostIDs: []int64{nas.ID, alpha.ID, nas.ID}})
	if err != nil || !hosts.MCP || hosts.HostCount != 2 {
		t.Fatalf("run with hosts = %+v, %v: hosts need the gatekeeper and a duplicate counts once", hosts, err)
	}
	got, err := s.RunHosts(ctx, hosts.ID)
	if err != nil || len(got) != 2 || got[0].Host.Name != "alpha" || got[1].Host.Name != "nas" {
		t.Fatalf("RunHosts = %+v, %v, want alpha and nas ordered by name", got, err)
	}
	if again, _ := s.GetRun(ctx, hosts.ID); again.HostCount != 2 {
		t.Fatalf("GetRun HostCount = %d", again.HostCount)
	}
	if none, err := s.RunHosts(ctx, plain.ID); err != nil || len(none) != 0 {
		t.Fatalf("RunHosts of a run without hosts = %+v, %v", none, err)
	}
}

func TestCreateRunWithCreatesNothingForAnUnknownHostOrIncident(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")
	if _, err := s.CreateRunWith(ctx, store.RunOptions{Provider: "claude", Prompt: "p", HostIDs: []int64{nas.ID, 999}}); !errors.Is(err, store.ErrNoSuchHost) {
		t.Fatalf("unknown host: err = %v, want ErrNoSuchHost", err)
	}
	if _, err := s.CreateRunWith(ctx, store.RunOptions{Provider: "claude", Prompt: "p", IncidentID: 999}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown incident: err = %v, want ErrNotFound", err)
	}
	if runs, err := s.ListRuns(ctx, 10); err != nil || len(runs) != 0 {
		t.Fatalf("runs after two refusals = %+v, %v: nothing may be created", runs, err)
	}
}

func TestDeletingAHostRemovesItsBindings(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	nas := hostNamed(t, s, "nas")
	r, err := s.CreateRunWith(ctx, store.RunOptions{Provider: "claude", Prompt: "p", HostIDs: []int64{nas.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteHost(ctx, nas.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.RunHosts(ctx, r.ID); err != nil || len(got) != 0 {
		t.Fatalf("RunHosts after the delete = %+v, %v", got, err)
	}
	// The count stays: the host tools find no usable host, and say so.
	if again, _ := s.GetRun(ctx, r.ID); again.HostCount != 1 {
		t.Fatalf("HostCount = %d, want 1", again.HostCount)
	}
}

func TestTheOlderCreatorsKeepTheirBehaviour(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	if r, err := s.CreateRun(ctx, "claude", "p"); err != nil || r.MCP || r.Cluster {
		t.Fatalf("CreateRun = %+v, %v", r, err)
	}
	if r, err := s.CreateToolRun(ctx, "claude", "p"); err != nil || !r.MCP || r.Cluster {
		t.Fatalf("CreateToolRun = %+v, %v", r, err)
	}
	if r, err := s.CreateClusterRun(ctx, "claude", "p"); err != nil || !r.MCP || !r.Cluster {
		t.Fatalf("CreateClusterRun = %+v, %v", r, err)
	}
	if _, err := s.CreateQuestionRun(ctx, "claude", "p", 999, false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("CreateQuestionRun for an unknown incident: %v", err)
	}
	if _, err := s.CreateQuestionRun(ctx, "claude", "p", 0, false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("CreateQuestionRun for incident 0: %v: a question is always about an incident", err)
	}
}
