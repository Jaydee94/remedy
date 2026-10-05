//go:build kindwrite

package gatekeeper_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/kube"
)

// The action tools against the real testbed of dev/kind. They CHANGE it (see internal/kube/live_write_test.go):
//
//	. ~/remedy-kind/env.sh && go test -tags kindwrite -run LiveActions -v ./internal/gatekeeper
//
// The approval is not part of this: the tools' Check and Run are called the way the gatekeeper calls them.
type liveActions struct {
	t      *testing.T
	reader *kube.Reader
	tools  map[string]gatekeeper.Tool
}

func newLiveActions(t *testing.T) *liveActions {
	t.Helper()
	if os.Getenv("REMEDY_K8S_WRITE_TOKEN_FILE") == "" {
		t.Skip("REMEDY_K8S_WRITE_TOKEN_FILE is not set: start the testbed (dev/kind/up.sh) and source its env.sh")
	}
	cfg := kube.Config{
		API: os.Getenv("REMEDY_K8S_API"), CAFile: os.Getenv("REMEDY_K8S_CA_FILE"), ReadTokenFile: os.Getenv("REMEDY_K8S_READ_TOKEN_FILE"),
		WriteTokenFile: os.Getenv("REMEDY_K8S_WRITE_TOKEN_FILE"), WriteNamespaces: []string{"demo"},
	}
	reader, err := kube.NewReader(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := kube.NewWriter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a := &liveActions{t: t, reader: reader, tools: map[string]gatekeeper.Tool{}}
	for _, tool := range gatekeeper.ClusterActionTools(reader, writer, cfg, nil) {
		a.tools[tool.Name] = tool
	}
	return a
}

// check decodes and checks the arguments as the gatekeeper does before it asks for an approval.
func (a *liveActions) check(tool, args string) (json.RawMessage, error) {
	a.t.Helper()
	canonical, err := a.tools[tool].Decode(json.RawMessage(args))
	if err != nil {
		return nil, err
	}
	return canonical, a.tools[tool].Check(context.Background(), canonical)
}

func (a *liveActions) run(tool string, args json.RawMessage, requested time.Time) (string, error) {
	a.t.Helper()
	return a.tools[tool].Run(context.Background(), gatekeeper.Call{Args: args, RequestedAt: requested})
}

func TestLiveActionsAreRefusedOutsideTheAllowlistAndDoneInsideIt(t *testing.T) {
	a := newLiveActions(t)
	var argErr gatekeeper.ArgumentError
	if _, err := a.check("cluster_rollout_restart", `{"kind":"deployment","namespace":"other","name":"web"}`); !errors.As(err, &argErr) || !strings.Contains(err.Error(), "allowed in: demo") {
		t.Fatalf("a restart in other = %v", err)
	}
	if _, err := a.check("cluster_rollout_restart", `{"kind":"deployment","namespace":"demo","name":"nope"}`); !errors.As(err, &argErr) || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("a restart of what is not there = %v", err)
	}
	args, err := a.check("cluster_rollout_restart", `{"kind":"deployment","namespace":"demo","name":"web"}`)
	if err != nil {
		t.Fatal(err)
	}
	text, err := a.run("cluster_rollout_restart", args, time.Now())
	if err != nil || !strings.HasPrefix(text, "restarted deployment demo/web") {
		t.Fatalf("restart = %q, %v", text, err)
	}
}

func TestLiveActionsAPodOfTheSameSecondIsNotDeletedButAnOlderOneIs(t *testing.T) {
	a := newLiveActions(t)
	time.Sleep(10 * time.Second) // the pods of the restart above are young: let them be older than the second the question is asked in
	pods, err := a.reader.ListPods(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	var victim kube.Pod
	for _, p := range pods.Items {
		if strings.HasPrefix(p.Name, "web-") && p.Ready == p.Total {
			victim = p
			break
		}
	}
	if victim.Name == "" {
		t.Fatal("no ready pod of web")
	}
	args, err := a.check("cluster_delete_pod", `{"namespace":"demo","name":"`+victim.Name+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	// Asked for in the second the pod was made in: the pod might be a replacement, so it is not deleted.
	var argErr gatekeeper.ArgumentError
	if _, err := a.run("cluster_delete_pod", args, victim.Created.Add(300*time.Millisecond)); !errors.As(err, &argErr) || !strings.Contains(err.Error(), "was replaced") {
		t.Fatalf("a pod of the second of the question = %v", err)
	}
	if _, err := a.reader.GetPod(context.Background(), "demo", victim.Name); err != nil {
		t.Fatalf("the pod is gone: %v", err)
	}
	// Asked for later than that: it is the pod that was approved.
	text, err := a.run("cluster_delete_pod", args, time.Now())
	if err != nil || !strings.HasPrefix(text, "deleted pod demo/"+victim.Name) {
		t.Fatalf("delete = %q, %v", text, err)
	}
}

func TestLiveActionsArgoRefreshAndSync(t *testing.T) {
	a := newLiveActions(t)
	args, err := a.check("argo_refresh", `{"app":"guestbook","hard":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if text, err := a.run("argo_refresh", args, time.Now()); err != nil || !strings.HasPrefix(text, "refresh requested") {
		t.Fatalf("refresh = %q, %v", text, err)
	}
	args, err = a.check("argo_sync", `{"app":"guestbook"}`)
	if err != nil {
		t.Fatal(err)
	}
	if text, err := a.run("argo_sync", args, time.Now()); err != nil || !strings.HasPrefix(text, "sync requested") {
		t.Fatalf("sync = %q, %v", text, err)
	}
	// An operation that is requested or running is not started a second time (it is gone again within seconds, so this
	// either is refused or finds the application done: both are right, and an error of another kind is not).
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		_, err := a.check("argo_sync", `{"app":"guestbook"}`)
		var argErr gatekeeper.ArgumentError
		if err == nil {
			return
		}
		if !errors.As(err, &argErr) || !strings.Contains(err.Error(), "already requested or running") {
			t.Fatalf("a second sync = %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("the operation did not end")
}
