//go:build kind

package kube

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
)

// These tests run against the real testbed of dev/kind, not against a recording. They are how the recordings are kept
// honest: whatever the fake API server (kubetest) answers, the real one must answer in a shape the Reader reads.
//
//	. ~/remedy-kind/env.sh && go test -tags kind -run Live ./internal/kube
func liveReader(t *testing.T) *Reader {
	t.Helper()
	if os.Getenv("REMEDY_K8S_READ_TOKEN_FILE") == "" {
		t.Skip("REMEDY_K8S_READ_TOKEN_FILE is not set: start the testbed (dev/kind/up.sh) and source its env.sh")
	}
	r, err := NewReader(Config{
		API: os.Getenv("REMEDY_K8S_API"), CAFile: os.Getenv("REMEDY_K8S_CA_FILE"), ReadTokenFile: os.Getenv("REMEDY_K8S_READ_TOKEN_FILE"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestLiveTheReadMethodsAgainstTheTestbed(t *testing.T) {
	r, ctx := liveReader(t), context.Background()

	if v, err := r.GetVersion(ctx); err != nil || !strings.HasPrefix(v.GitVersion, "v1.") {
		t.Fatalf("version = %+v, %v", v, err)
	}
	ws, err := r.ListWorkloads(ctx, "demo")
	if err != nil || len(ws.Items) != 4 {
		t.Fatalf("workloads of demo = %+v, %v", ws.Items, err)
	}
	web := workloadNamed(t, ws.Items, "demo", "web")
	if web.Desired != 2 || web.Kind != "deployment" {
		t.Fatalf("web = %+v", web)
	}
	pods, err := r.ListPods(ctx, "demo")
	if err != nil || len(pods.Items) < 5 {
		t.Fatalf("pods = %d, %v", len(pods.Items), err)
	}
	if p := podOf(t, pods.Items, "badimage").Problem(); !strings.Contains(p, "ImagePull") && !strings.Contains(p, "ErrImagePull") {
		t.Fatalf("badimage problem = %q", p)
	}
	if nodes, err := r.ListNodes(ctx); err != nil || len(nodes.Items) == 0 || nodes.Items[0].Ready != "True" {
		t.Fatalf("nodes = %+v, %v", nodes.Items, err)
	}
	crashy := podOf(t, pods.Items, "crashy").Name
	events, err := r.ListEvents(ctx, EventFilter{Namespace: "demo", Kind: "Pod", Name: crashy})
	if err != nil || len(events.Items) == 0 {
		t.Fatalf("events about %s = %d, %v", crashy, len(events.Items), err)
	}
	for _, e := range events.Items {
		if e.Name != crashy || e.Kind != "Pod" {
			t.Fatalf("the field selector did not select: %+v", e)
		}
	}
	warnings, err := r.ListEvents(ctx, EventFilter{Namespace: "demo", WarningsOnly: true})
	if err != nil || len(warnings.Items) == 0 {
		t.Fatalf("warnings = %d, %v", len(warnings.Items), err)
	}
	for _, e := range warnings.Items {
		if e.Type != "Warning" {
			t.Fatalf("the field selector did not select: %+v", e)
		}
	}
	chatty := podOf(t, pods.Items, "chatty").Name
	log, err := r.GetPodLog(ctx, "demo", chatty, "chatty", false, 3)
	if err != nil || strings.Count(log, "\n") != 3 {
		t.Fatalf("log = %q, %v", log, err)
	}
	d, err := r.GetDescription(ctx, "deployment", "demo", "web")
	if err != nil || !strings.Contains(marshal(t, d), "GREETING") || strings.Contains(marshal(t, d), "hello-from-the-demo") {
		t.Fatalf("description = %v, %v", d, err)
	}
	for _, kind := range []string{"pod", "node", "daemonset", "statefulset"} {
		name, ns := crashy, "demo"
		switch kind {
		case "node":
			name, ns = "remedy-dev-control-plane", ""
		case "daemonset":
			name, ns = "kindnet", "kube-system"
		case "statefulset":
			name, ns = "argocd-application-controller", "argocd"
		}
		if d, err := r.GetDescription(ctx, kind, ns, name); err != nil || d.Status == nil {
			t.Fatalf("describe %s %s = %+v, %v", kind, name, d, err)
		}
	}
	apps, err := r.ListApplications(ctx)
	if err != nil || len(apps.Items) != 1 || apps.Items[0].Name != "guestbook" {
		t.Fatalf("applications = %+v, %v", apps.Items, err)
	}
	if apps.Items[0].Sync == "" || apps.Items[0].Health == "" || apps.Items[0].DestinationNamespace != "demo" {
		t.Fatalf("application = %+v", apps.Items[0])
	}
}

// The read identity must not be able to read Secrets or change anything, whatever the code does: this is RBAC, shown
// against the real API server.
func TestLiveTheReadIdentityCannotReadSecretsOrChangeAnything(t *testing.T) {
	r, ctx := liveReader(t), context.Background()
	for _, path := range []string{"/api/v1/namespaces/demo/secrets", "/api/v1/secrets", "/api/v1/namespaces/demo/configmaps"} {
		if _, err := r.do(ctx, "GET", path, nil, "", nil); !errors.Is(err, ErrForbidden) {
			t.Errorf("GET %s = %v, want forbidden", path, err)
		}
	}
	// The client's own transport refuses to send a change; to see what the cluster says, send one around it.
	plain := *r.client
	plain.http = &http.Client{Timeout: requestTimeout, Transport: r.client.http.Transport.(*guard).base}
	if _, err := plain.do(ctx, "PATCH", "/apis/apps/v1/namespaces/demo/deployments/web", nil, mergePatch, []byte(`{}`)); !errors.Is(err, ErrForbidden) {
		t.Errorf("PATCH with the read token = %v, want forbidden", err)
	}
}
