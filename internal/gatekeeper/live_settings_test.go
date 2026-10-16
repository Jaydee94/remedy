//go:build kindwrite

package gatekeeper_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/kube"
)

// . ~/remedy-kind/env.sh && go test -tags kindwrite -run LiveAllowlist -v ./internal/gatekeeper
func TestLiveAllowlistChangeIsUsedWithoutARestart(t *testing.T) {
	if os.Getenv("REMEDY_K8S_WRITE_TOKEN_FILE") == "" {
		t.Skip("REMEDY_K8S_WRITE_TOKEN_FILE is not set: start the testbed (dev/kind/up.sh) and source its env.sh")
	}
	var mu sync.Mutex
	var list []string
	setList := func(v ...string) { mu.Lock(); defer mu.Unlock(); list = v }
	cfg := kube.Config{
		API: os.Getenv("REMEDY_K8S_API"), CAFile: os.Getenv("REMEDY_K8S_CA_FILE"), ReadTokenFile: os.Getenv("REMEDY_K8S_READ_TOKEN_FILE"),
		WriteTokenFile: os.Getenv("REMEDY_K8S_WRITE_TOKEN_FILE"),
		Namespaces:     func() []string { mu.Lock(); defer mu.Unlock(); return slices.Clone(list) },
	}
	reader, err := kube.NewReader(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := kube.NewWriter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var restart gatekeeper.Tool
	for _, tool := range gatekeeper.ClusterActionTools(reader, writer, cfg, nil) {
		if tool.Name == "cluster_rollout_restart" {
			restart = tool
		}
	}
	ctx := context.Background()
	args, err := restart.Decode(json.RawMessage(`{"kind":"deployment","namespace":"demo","name":"web"}`))
	if err != nil {
		t.Fatal(err)
	}

	var argErr gatekeeper.ArgumentError
	if err := restart.Check(ctx, args); !errors.As(err, &argErr) || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("with an empty list: %v, want a refusal", err)
	}
	setList("demo")
	if err := restart.Check(ctx, args); err != nil {
		t.Fatalf("after the list was set to demo: %v", err)
	}
	setList() // removed after the question was asked: the action must not be sent
	if _, err := restart.Run(ctx, gatekeeper.Call{Args: args, RequestedAt: time.Now()}); !errors.As(err, &argErr) {
		t.Fatalf("running after the namespace was removed: %v, want a refusal", err)
	}
}
