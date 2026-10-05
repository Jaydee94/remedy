package kube

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func newTestWriter(t *testing.T, api *fakeAPI) *Writer {
	t.Helper()
	w, err := NewWriter(Config{
		API: api.URL, ReadTokenFile: writeFile(t, "read", "read-token"),
		WriteTokenFile: writeFile(t, "write", "write-token\n"), WriteNamespaces: []string{"demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func okReply() http.HandlerFunc { return jsonReply(200, `{}`) }

func TestRestartWorkloadSetsTheRestartedAtAnnotation(t *testing.T) {
	api := newFakeAPI(t, okReply())
	now := time.Date(2026, 10, 4, 12, 30, 0, 0, time.UTC)
	for kind, plural := range map[string]string{"deployment": "deployments", "statefulset": "statefulsets", "daemonset": "daemonsets"} {
		if err := newTestWriter(t, api).RestartWorkload(context.Background(), kind, "demo", "web", now); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		got := api.requests()
		last := got[len(got)-1]
		if last.Method != "PATCH" || last.Path != "/apis/apps/v1/namespaces/demo/"+plural+"/web" ||
			last.ContentType != "application/merge-patch+json" || last.Auth != "Bearer write-token" {
			t.Fatalf("%s: request = %+v", kind, last)
		}
		var body struct {
			Spec struct {
				Template struct {
					Metadata struct {
						Annotations map[string]string `json:"annotations"`
					} `json:"metadata"`
				} `json:"template"`
			} `json:"spec"`
		}
		if err := json.Unmarshal([]byte(last.Body), &body); err != nil {
			t.Fatal(err)
		}
		if got := body.Spec.Template.Metadata.Annotations["kubectl.kubernetes.io/restartedAt"]; got != "2026-10-04T12:30:00Z" {
			t.Fatalf("%s: restartedAt = %q", kind, got)
		}
	}
}

func TestDeletePod(t *testing.T) {
	api := newFakeAPI(t, okReply())
	if err := newTestWriter(t, api).DeletePod(context.Background(), "demo", "crashy-7d9f-abcde"); err != nil {
		t.Fatal(err)
	}
	got := api.requests()
	if len(got) != 1 || got[0].Method != "DELETE" || got[0].Path != "/api/v1/namespaces/demo/pods/crashy-7d9f-abcde" ||
		got[0].Auth != "Bearer write-token" {
		t.Fatalf("requests = %+v", got)
	}
}

func TestRefreshAndSyncAnApplication(t *testing.T) {
	api := newFakeAPI(t, okReply())
	w := newTestWriter(t, api)
	if err := w.RefreshApplication(context.Background(), "guestbook", false); err != nil {
		t.Fatal(err)
	}
	if err := w.RefreshApplication(context.Background(), "guestbook", true); err != nil {
		t.Fatal(err)
	}
	if err := w.SyncApplication(context.Background(), "guestbook"); err != nil {
		t.Fatal(err)
	}
	got := api.requests()
	if len(got) != 3 {
		t.Fatalf("requests = %+v", got)
	}
	for _, r := range got {
		if r.Method != "PATCH" || r.Path != "/apis/argoproj.io/v1alpha1/namespaces/argocd/applications/guestbook" ||
			r.ContentType != "application/merge-patch+json" {
			t.Fatalf("request = %+v", r)
		}
	}
	if got[0].Body != `{"metadata":{"annotations":{"argocd.argoproj.io/refresh":"normal"}}}` ||
		got[1].Body != `{"metadata":{"annotations":{"argocd.argoproj.io/refresh":"hard"}}}` {
		t.Fatalf("refresh bodies = %s, %s", got[0].Body, got[1].Body)
	}
	if got[2].Body != `{"operation":{"initiatedBy":{"username":"remedy"},"sync":{}}}` {
		t.Fatalf("sync body = %s: a sync must not prune, force or replace", got[2].Body)
	}
}

func TestTheArgoNamespaceIsConfigurable(t *testing.T) {
	api := newFakeAPI(t, okReply())
	w, err := NewWriter(Config{API: api.URL, ReadTokenFile: writeFile(t, "r", "r"), WriteTokenFile: writeFile(t, "w", "w"),
		WriteNamespaces: []string{"demo"}, ArgoNamespace: "gitops"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SyncApplication(context.Background(), "guestbook"); err != nil {
		t.Fatal(err)
	}
	if got := api.requests()[0].Path; got != "/apis/argoproj.io/v1alpha1/namespaces/gitops/applications/guestbook" {
		t.Fatalf("path = %s", got)
	}
}

func TestTheWriterRefusesANamespaceOutsideTheAllowlistWithoutSendingAnything(t *testing.T) {
	api := newFakeAPI(t, okReply())
	w := newTestWriter(t, api)
	ctx := context.Background()
	if err := w.RestartWorkload(ctx, "deployment", "other", "web", time.Now()); !errors.Is(err, ErrNamespaceNotAllowed) {
		t.Fatalf("restart: %v", err)
	}
	if err := w.DeletePod(ctx, "kube-system", "coredns-1"); !errors.Is(err, ErrNamespaceNotAllowed) {
		t.Fatalf("delete: %v", err)
	}
	if n := len(api.requests()); n != 0 {
		t.Fatalf("%d requests were sent", n)
	}
}

func TestTheWriterRefusesNamesThatCouldChangeThePath(t *testing.T) {
	api := newFakeAPI(t, okReply())
	w := newTestWriter(t, api)
	ctx := context.Background()
	for _, name := range []string{"", "..", "../secrets/x", "web/../../x", "Web", "web?x=1", "web%2Fx", strings.Repeat("a", 254)} {
		if err := w.DeletePod(ctx, "demo", name); !errors.Is(err, ErrInvalid) {
			t.Errorf("DeletePod(%q) = %v", name, err)
		}
		if err := w.RestartWorkload(ctx, "deployment", "demo", name, time.Now()); !errors.Is(err, ErrInvalid) {
			t.Errorf("RestartWorkload(%q) = %v", name, err)
		}
		if err := w.SyncApplication(ctx, name); !errors.Is(err, ErrInvalid) {
			t.Errorf("SyncApplication(%q) = %v", name, err)
		}
	}
	if err := w.RestartWorkload(ctx, "secret", "demo", "web", time.Now()); !errors.Is(err, ErrInvalid) {
		t.Errorf("a kind that is not a workload: %v", err)
	}
	if n := len(api.requests()); n != 0 {
		t.Fatalf("%d requests were sent", n)
	}
}

func TestTheWriterRefusesEveryMethodButPatchAndDelete(t *testing.T) {
	api := newFakeAPI(t, okReply())
	w := newTestWriter(t, api)
	for _, method := range []string{"GET", "HEAD", "POST", "PUT"} {
		req, _ := http.NewRequest(method, api.URL+"/api/v1/namespaces/demo/pods", nil)
		if _, err := w.http.Do(req); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("%s: error = %v", method, err)
		}
	}
	if n := len(api.requests()); n != 0 {
		t.Fatalf("%d refused requests reached the server", n)
	}
}

func TestTheWriterReportsWhatTheClusterSays(t *testing.T) {
	api := newFakeAPI(t, jsonReply(404, `{"kind":"Status","reason":"NotFound","message":"pods \"x\" not found","code":404}`))
	if err := newTestWriter(t, api).DeletePod(context.Background(), "demo", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("404: %v", err)
	}
	api = newFakeAPI(t, jsonReply(403, `{"kind":"Status","reason":"Forbidden","message":"forbidden","code":403}`))
	if err := newTestWriter(t, api).DeletePod(context.Background(), "demo", "x"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("403: %v", err)
	}
}

func TestANewWriterNeedsTheWriteSideToBeConfigured(t *testing.T) {
	read := writeFile(t, "r", "r")
	if _, err := NewWriter(Config{API: "http://k8s", ReadTokenFile: read}); err == nil {
		t.Fatal("a writer without a write token")
	}
	if _, err := NewWriter(Config{API: "http://k8s", ReadTokenFile: read, WriteTokenFile: writeFile(t, "w", "w")}); err == nil {
		t.Fatal("a writer without namespaces")
	}
	if _, err := NewReader(Config{API: "http://k8s"}); err == nil {
		t.Fatal("a reader without a read token")
	}
}
