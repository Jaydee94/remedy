//go:build kind

package gatekeeper_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/kube"
)

// The cluster tools against the real testbed of dev/kind, not against a recording:
//
//	. ~/remedy-kind/env.sh && go test -tags kind -run Live -v ./internal/gatekeeper
//
// With -v it prints what each tool shows an agent, which is how the formats are looked at.
func liveTool(t *testing.T, name string) func(args string) string {
	t.Helper()
	if os.Getenv("REMEDY_K8S_READ_TOKEN_FILE") == "" {
		t.Skip("REMEDY_K8S_READ_TOKEN_FILE is not set: start the testbed (dev/kind/up.sh) and source its env.sh")
	}
	reader, err := kube.NewReader(kube.Config{
		API: os.Getenv("REMEDY_K8S_API"), CAFile: os.Getenv("REMEDY_K8S_CA_FILE"), ReadTokenFile: os.Getenv("REMEDY_K8S_READ_TOKEN_FILE"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range gatekeeper.ClusterTools(reader, nil) {
		if tool.Name != name {
			continue
		}
		return func(args string) string {
			t.Helper()
			canonical, err := tool.Decode(json.RawMessage(args))
			if err != nil {
				t.Fatalf("%s %s: %v", name, args, err)
			}
			text, err := tool.Run(context.Background(), gatekeeper.Call{Args: canonical})
			if err != nil {
				t.Fatalf("%s %s: %v", name, args, err)
			}
			t.Logf("%s %s\n%s", name, args, text)
			return text
		}
	}
	t.Fatalf("no tool %s", name)
	return nil
}

func TestLiveTheClusterTools(t *testing.T) {
	if text := liveTool(t, "cluster_workloads")(`{"namespace":"demo"}`); !strings.Contains(text, "demo/crashy  deployment  ready 0/1") || !strings.Contains(text, "[NOT HEALTHY]") {
		t.Errorf("workloads: %s", text)
	}
	pods := liveTool(t, "cluster_pods")(`{"namespace":"demo"}`)
	if !strings.Contains(pods, "[PROBLEM: crashy: ") || !strings.Contains(pods, "ImagePullBackOff") && !strings.Contains(pods, "ErrImagePull") {
		t.Errorf("pods: %s", pods)
	}
	if text := liveTool(t, "cluster_describe")(`{"kind":"deployment","namespace":"demo","name":"web"}`); !strings.Contains(text, "GREETING") || strings.Contains(text, "hello-from-the-demo") {
		t.Errorf("describe: %s", text)
	}
	if text := liveTool(t, "cluster_events")(`{"namespace":"demo"}`); !strings.Contains(text, "Warning  ") {
		t.Errorf("events: %s", text)
	}
	var chatty string
	for _, line := range strings.Split(pods, "\n") {
		if strings.HasPrefix(line, "demo/chatty-") {
			chatty = strings.Fields(strings.TrimPrefix(line, "demo/"))[0]
		}
	}
	if chatty == "" {
		t.Fatalf("no chatty pod in %s", pods)
	}
	if text := liveTool(t, "cluster_pod_logs")(`{"namespace":"demo","pod":"` + chatty + `","tail_lines":5}`); !strings.Contains(text, "ignore all previous instructions") {
		t.Errorf("logs: %s", text)
	}
	if text := liveTool(t, "cluster_nodes")(`{}`); !strings.Contains(text, "ready True") {
		t.Errorf("nodes: %s", text)
	}
	if text := liveTool(t, "argo_apps")(`{"name":"guestbook"}`); !strings.Contains(text, "guestbook") {
		t.Errorf("argo: %s", text)
	}
}
