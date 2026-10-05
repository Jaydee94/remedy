//go:build kindwrite

package kube

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// These tests CHANGE the real testbed of dev/kind: they restart the deployment demo/web, delete one of its pods and
// sync the Argo CD application guestbook (which creates guestbook-ui in demo). Run them on a testbed you are about to
// throw away, and run the read-only live tests (-tags kind) first: after a sync the testbed is not what they expect.
//
//	. ~/remedy-kind/env.sh && go test -tags kindwrite -run LiveWrite -v ./internal/kube
func liveWriter(t *testing.T, namespaces ...string) (*Writer, *Reader) {
	t.Helper()
	if os.Getenv("REMEDY_K8S_WRITE_TOKEN_FILE") == "" {
		t.Skip("REMEDY_K8S_WRITE_TOKEN_FILE is not set: start the testbed (dev/kind/up.sh) and source its env.sh")
	}
	cfg := Config{
		API: os.Getenv("REMEDY_K8S_API"), CAFile: os.Getenv("REMEDY_K8S_CA_FILE"), ReadTokenFile: os.Getenv("REMEDY_K8S_READ_TOKEN_FILE"),
		WriteTokenFile: os.Getenv("REMEDY_K8S_WRITE_TOKEN_FILE"), WriteNamespaces: namespaces,
	}
	w, err := NewWriter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return w, r
}

func within(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", d, what)
}

func webPods(t *testing.T, r *Reader) []Pod {
	t.Helper()
	l, err := r.ListPods(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	var out []Pod
	for _, p := range l.Items {
		if strings.HasPrefix(p.Name, "web-") {
			out = append(out, p)
		}
	}
	return out
}

func TestLiveWriteARestartReplacesThePods(t *testing.T) {
	w, r := liveWriter(t, "demo")
	ctx := context.Background()
	before := map[string]bool{}
	for _, p := range webPods(t, r) {
		before[p.Name] = true
	}
	if len(before) == 0 {
		t.Fatal("no pods of demo/web")
	}
	now := time.Now()
	if err := w.RestartWorkload(ctx, "deployment", "demo", "web", now); err != nil {
		t.Fatal(err)
	}
	within(t, 90*time.Second, "all pods of web to be new and ready", func() bool {
		pods := webPods(t, r)
		ready := 0
		for _, p := range pods {
			if before[p.Name] {
				return false
			}
			if p.Ready == p.Total && p.Total > 0 {
				ready++
			}
		}
		return len(pods) == 2 && ready == 2
	})
	var raw struct {
		Spec struct {
			Template struct {
				Metadata struct {
					Annotations map[string]string `json:"annotations"`
				} `json:"metadata"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := getJSON(ctx, r, "/apis/apps/v1/namespaces/demo/deployments/web", &raw); err != nil {
		t.Fatal(err)
	}
	if got := raw.Spec.Template.Metadata.Annotations["kubectl.kubernetes.io/restartedAt"]; got != now.UTC().Format(time.RFC3339) {
		t.Fatalf("restartedAt = %q, want %q", got, now.UTC().Format(time.RFC3339))
	}
}

func TestLiveWriteADeletedPodIsReplacedByItsController(t *testing.T) {
	w, r := liveWriter(t, "demo")
	pods := webPods(t, r)
	if len(pods) != 2 {
		t.Fatalf("pods of web = %d, want 2 (restart test first, or wait)", len(pods))
	}
	gone := pods[0].Name
	if err := w.DeletePod(context.Background(), "demo", gone); err != nil {
		t.Fatal(err)
	}
	within(t, 60*time.Second, "a new pod to replace "+gone, func() bool {
		now := webPods(t, r)
		ready := 0
		for _, p := range now {
			if p.Name == gone {
				return false
			}
			if p.Ready == p.Total && p.Total > 0 {
				ready++
			}
		}
		return len(now) == 2 && ready == 2
	})
	if err := w.DeletePod(context.Background(), "demo", gone); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a pod that is gone: %v", err)
	}
}

func TestLiveWriteArgoRefreshAndSync(t *testing.T) {
	w, r := liveWriter(t, "demo")
	ctx := context.Background()
	if err := w.RefreshApplication(ctx, "guestbook", false); err != nil {
		t.Fatal(err)
	}
	// Argo CD takes the annotation and removes it.
	within(t, 60*time.Second, "Argo CD to consume the refresh annotation", func() bool {
		var raw struct {
			Metadata struct {
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		}
		if err := getJSON(ctx, r, "/apis/argoproj.io/v1alpha1/namespaces/argocd/applications/guestbook", &raw); err != nil {
			t.Fatal(err)
		}
		_, still := raw.Metadata.Annotations["argocd.argoproj.io/refresh"]
		return !still
	})
	if err := w.RefreshApplication(ctx, "guestbook", true); err != nil {
		t.Fatal(err)
	}

	app, err := r.GetApplication(ctx, "guestbook")
	if err != nil || app.Syncing {
		t.Fatalf("before the sync: %+v, %v", app, err)
	}
	asked := time.Now()
	if err := w.SyncApplication(ctx, "guestbook"); err != nil {
		t.Fatal(err)
	}
	// The operation is at the top level of the application and is gone again when it has ended: watch for it, then for
	// its result.
	sawOperation := false
	within(t, 120*time.Second, "the application to be synced and healthy", func() bool {
		a, err := r.GetApplication(ctx, "guestbook")
		if err != nil {
			t.Fatal(err)
		}
		if a.Syncing {
			sawOperation = true
		}
		// The result of an earlier operation stays in the status: only one that started after the request counts.
		return a.Sync == "Synced" && a.Health == "Healthy" && a.Operation != nil && a.Operation.Phase == "Succeeded" && !a.Syncing &&
			a.Operation.StartedAt.After(asked.Add(-2*time.Second))
	})
	if !sawOperation {
		t.Log("the operation was too quick to be seen while it ran")
	}
	done, _ := r.GetApplication(ctx, "guestbook")
	if done.Operation.Revision == "" || len(done.OutOfSync) != 0 {
		t.Fatalf("application = %+v", done)
	}
}

// The code limits the namespaces; RBAC limits them too, whatever the code says. A Writer that is told it may act in
// "other" is stopped by the cluster, and the write identity cannot read.
func TestLiveWriteRBACStopsAWriterThatTheCodeWouldAllow(t *testing.T) {
	w, _ := liveWriter(t, "other", "kube-system")
	ctx := context.Background()
	if err := w.RestartWorkload(ctx, "deployment", "other", "web", time.Now()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("restart in other = %v, want forbidden", err)
	}
	if err := w.DeletePod(ctx, "kube-system", "coredns-x"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("delete in kube-system = %v, want forbidden", err)
	}
	// The client's own transport refuses to send a read; to see what the cluster says, send one around it.
	plain := *w.client
	plain.http = &http.Client{Timeout: requestTimeout, Transport: w.client.http.Transport.(*guard).base}
	if _, err := plain.do(ctx, "GET", "/api/v1/namespaces/demo/pods", nil, "", nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a read with the write token = %v, want forbidden", err)
	}
}
