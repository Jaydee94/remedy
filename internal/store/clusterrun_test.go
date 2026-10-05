package store_test

import (
	"context"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
)

func TestCreateClusterRunMarksTheRunAsHavingClusterTools(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r, err := s.CreateClusterRun(ctx, "claude", "look at the cluster")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Cluster || !r.MCP || r.Status != run.Queued || r.Role != run.RoleAdhoc {
		t.Fatalf("run = %+v: a cluster run has gatekeeper access and cluster tools", r)
	}
	got, err := s.GetRun(ctx, r.ID)
	if err != nil || !got.Cluster || !got.MCP {
		t.Fatalf("GetRun = %+v, %v", got, err)
	}
}

func TestOtherRunsHaveNoClusterTools(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	plain, _ := s.CreateRun(ctx, "claude", "plain")
	tools, _ := s.CreateToolRun(ctx, "claude", "tools")
	if plain.Cluster || tools.Cluster {
		t.Fatalf("plain = %+v, tools = %+v", plain, tools)
	}
}

func TestAClusterRunKeepsItsFlagThroughTheClaimTheListAndTheToken(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	made, _ := s.CreateClusterRun(ctx, "claude", "cluster")
	claimed, err := s.ClaimNext(ctx)
	if err != nil || claimed == nil || claimed.ID != made.ID || !claimed.Cluster {
		t.Fatalf("claimed = %+v, %v", claimed, err)
	}
	token, err := s.MintRunToken(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("a cluster run gets a run token like any run with tools: %v", err)
	}
	byToken, err := s.RunForToken(ctx, token)
	if err != nil || !byToken.Cluster {
		t.Fatalf("RunForToken = %+v, %v: the gatekeeper decides the tools by this flag", byToken, err)
	}
	list, err := s.ListRuns(ctx, 10)
	if err != nil || len(list) != 1 || !list[0].Cluster {
		t.Fatalf("ListRuns = %+v, %v", list, err)
	}
}
