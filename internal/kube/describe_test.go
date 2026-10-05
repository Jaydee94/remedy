package kube

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/kube/kubetest"
)

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDescribeADeploymentShowsNoEnvironmentValuesAndNoAnnotations(t *testing.T) {
	r, _ := recordedReader(t, "")
	d, err := r.GetDescription(context.Background(), "deployment", "demo", "web")
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != "deployment" || d.Namespace != "demo" || d.Name != "web" || d.Created.IsZero() {
		t.Fatalf("description = %+v", d)
	}
	text := marshal(t, d)
	if !strings.Contains(text, `"envNames":["GREETING"]`) {
		t.Fatalf("the names of the environment variables are missing: %s", text)
	}
	for _, leak := range []string{"hello-from-the-demo", "annotations", "last-applied-configuration", "deployment.kubernetes.io/revision", "managedFields"} {
		if strings.Contains(text, leak) {
			t.Fatalf("the description contains %q: %s", leak, text)
		}
	}
	containers, _ := d.Spec["containers"].([]objectMap)
	if len(containers) != 1 || containers[0]["image"] != "nginx:1.27-alpine" || containers[0]["name"] != "web" {
		t.Fatalf("containers = %+v", d.Spec["containers"])
	}
	if d.Spec["replicas"] == nil || d.Status["conditions"] == nil {
		t.Fatalf("spec = %+v, status = %+v", d.Spec, d.Status)
	}
}

func TestDescribeACrashingDeploymentAndItsEvents(t *testing.T) {
	r, _ := recordedReader(t, "")
	d, err := r.GetDescription(context.Background(), "deployment", "demo", "crashy")
	if err != nil {
		t.Fatal(err)
	}
	text := marshal(t, d)
	for _, want := range []string{`"command":["sh","-c"`, "MinimumReplicasUnavailable", `"unavailableReplicas":1`} {
		if !strings.Contains(text, want) {
			t.Fatalf("the description lacks %q: %s", want, text)
		}
	}
	for _, e := range d.Events {
		if e.Kind != "Deployment" || e.Name != "crashy" {
			t.Fatalf("event %+v is not about the deployment", e)
		}
	}
}

func TestDescribeAPod(t *testing.T) {
	r, _ := recordedReader(t, "")
	pod := kubetest.PodName(t, "crashy")
	d, err := r.GetDescription(context.Background(), "pod", "demo", pod)
	if err != nil {
		t.Fatal(err)
	}
	text := marshal(t, d)
	for _, want := range []string{`"phase":"Running"`, `"restartCount"`, `"lastState"`, `"exitCode":1`} {
		if !strings.Contains(text, want) {
			t.Fatalf("the description lacks %q: %s", want, text)
		}
	}
	volumes, _ := d.Spec["volumes"].([]string)
	if len(volumes) == 0 || !strings.Contains(volumes[0], "(projected)") {
		t.Fatalf("volumes = %v: they are shown as name and kind", d.Spec["volumes"])
	}
	if len(d.Events) == 0 {
		t.Fatal("a crashing pod has events")
	}
	for _, e := range d.Events {
		if e.Kind != "Pod" || e.Name != pod {
			t.Fatalf("event %+v is not about the pod", e)
		}
	}
}

func TestDescribeANodeADaemonSetAndAStatefulSet(t *testing.T) {
	r, _ := recordedReader(t, "")
	ctx := context.Background()
	node, err := r.GetDescription(ctx, "node", "", "remedy-dev-control-plane")
	if err != nil || !strings.Contains(marshal(t, node), `"kubeletVersion":"v1.`) || node.Status["conditions"] == nil {
		t.Fatalf("node = %+v, %v", node, err)
	}
	ds, err := r.GetDescription(ctx, "daemonset", "kube-system", "kindnet")
	if err != nil || ds.Status["numberReady"] == nil {
		t.Fatalf("daemonset = %+v, %v", ds, err)
	}
	sts, err := r.GetDescription(ctx, "statefulset", "argocd", "argocd-application-controller")
	if err != nil || sts.Spec["serviceName"] == nil {
		t.Fatalf("statefulset = %+v, %v", sts, err)
	}
}

func TestDescribeRefusesWhatItDoesNotKnow(t *testing.T) {
	r, srv := recordedReader(t, "")
	ctx := context.Background()
	for _, tc := range []struct{ kind, ns, name string }{
		{"secret", "demo", "x"}, {"configmap", "demo", "x"}, {"pod", "", "x"}, {"pod", "demo", "../x"}, {"pod", "De mo", "x"}, {"", "demo", "x"},
	} {
		if _, err := r.GetDescription(ctx, tc.kind, tc.ns, tc.name); !errors.Is(err, ErrInvalid) {
			t.Errorf("GetDescription(%+v) = %v", tc, err)
		}
	}
	if n := len(srv.Requests()); n != 0 {
		t.Fatalf("%d requests were sent for arguments that cannot be used", n)
	}
	if _, err := r.GetDescription(ctx, "deployment", "demo", "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a deployment that is not there: %v", err)
	}
}

func TestListAndGetApplications(t *testing.T) {
	r, _ := recordedReader(t, "")
	ctx := context.Background()
	l, err := r.ListApplications(ctx)
	if err != nil || len(l.Items) != 1 {
		t.Fatalf("applications = %+v, %v", l, err)
	}
	a := l.Items[0]
	if a.Name != "guestbook" || a.Project != "default" || a.Sync != "OutOfSync" || a.Health != "Missing" ||
		a.DestinationNamespace != "demo" || a.Path != "guestbook" || !strings.Contains(a.RepoURL, "argocd-example-apps") ||
		a.TargetRevision != "HEAD" || a.Revision == "" || a.Syncing || len(a.Conditions) != 0 {
		t.Fatalf("application = %+v", a)
	}
	if len(a.OutOfSync) != 2 || !contains(a.OutOfSync, "Deployment demo/guestbook-ui") || !contains(a.OutOfSync, "Service demo/guestbook-ui") {
		t.Fatalf("out of sync = %v", a.OutOfSync)
	}
	got, err := r.GetApplication(ctx, "guestbook")
	if err != nil || got.Name != "guestbook" || got.Sync != "OutOfSync" {
		t.Fatalf("GetApplication = %+v, %v", got, err)
	}
	if _, err := r.GetApplication(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an application that is not there: %v", err)
	}
	if _, err := r.GetApplication(ctx, "../x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a bad name: %v", err)
	}
}

func TestTheApplicationsAreReadFromTheConfiguredArgoNamespace(t *testing.T) {
	r, srv := recordedReader(t, "gitops")
	if _, err := r.ListApplications(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := srv.Requests()[0].Path; got != "/apis/argoproj.io/v1alpha1/namespaces/gitops/applications" {
		t.Fatalf("path = %s", got)
	}
}

// What the recording does not have: an operation that was requested but has not started (Argo CD keeps it at the top level
// of the application, next to spec and status, and removes it when the operation is done), one that runs, and an
// application in which only some of the resources differ.
func TestAnApplicationWithARequestedOperationIsSyncing(t *testing.T) {
	api := newFakeAPI(t, jsonReply(200, `{"metadata":{"name":"app"},"spec":{"project":"p"},"operation":{"sync":{}},"status":{}}`))
	a, err := newTestReader(t, api, "tok").GetApplication(context.Background(), "app")
	if err != nil || !a.Syncing || a.Operation != nil {
		t.Fatalf("application = %+v, %v", a, err)
	}
}

func TestAnApplicationWithARunningOperationIsSyncing(t *testing.T) {
	api := newFakeAPI(t, jsonReply(200, `{"metadata":{"name":"app"},"spec":{"project":"p"},
		"status":{"operationState":{"phase":"Running","message":"going","startedAt":"2026-10-05T05:00:00Z"}}}`))
	a, err := newTestReader(t, api, "tok").GetApplication(context.Background(), "app")
	if err != nil || !a.Syncing || a.Operation == nil || a.Operation.Phase != "Running" || a.Operation.StartedAt.IsZero() {
		t.Fatalf("application = %+v, %v", a, err)
	}
	done := newFakeAPI(t, jsonReply(200, `{"metadata":{"name":"app"},"spec":{"project":"p"},"status":{"operationState":{"phase":"Succeeded"}}}`))
	if a, err := newTestReader(t, done, "tok").GetApplication(context.Background(), "app"); err != nil || a.Syncing {
		t.Fatalf("a finished operation is not a running one: %+v, %v", a, err)
	}
}

func TestOnlyTheResourcesThatDifferAreListedAsOutOfSync(t *testing.T) {
	api := newFakeAPI(t, jsonReply(200, `{"metadata":{"name":"app"},"spec":{"project":"p"},"status":{"resources":[
		{"kind":"Service","name":"a","namespace":"n","status":"Synced"},
		{"group":"apps","kind":"Deployment","name":"b","namespace":"n","status":"OutOfSync"},
		{"kind":"Namespace","name":"n","status":"OutOfSync"}]}}`))
	a, err := newTestReader(t, api, "tok").GetApplication(context.Background(), "app")
	if err != nil || len(a.OutOfSync) != 2 || a.OutOfSync[0] != "Deployment n/b" || a.OutOfSync[1] != "Namespace n" {
		t.Fatalf("application = %+v, %v", a, err)
	}
}
