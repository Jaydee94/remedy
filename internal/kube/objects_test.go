package kube

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/kube/kubetest"
)

// The tests of the typed read methods run against responses recorded from a real cluster (see kubetest and
// dev/kind/record.sh). The testbed has, in the namespace demo: web (healthy, two pods, one environment variable),
// crashy (CrashLoopBackOff), badimage (ImagePullBackOff) and chatty (healthy, with a made-up token and an instruction
// in its log); in other: web. Argo CD has one application, guestbook, out of sync.

func recordedReader(t *testing.T, argoNamespace string) (*Reader, *kubetest.Server) {
	t.Helper()
	srv := kubetest.New(t, "read-token")
	r, err := NewReader(Config{API: srv.URL, ReadTokenFile: writeFile(t, "read", "read-token\n"), ArgoNamespace: argoNamespace})
	if err != nil {
		t.Fatal(err)
	}
	return r, srv
}

func workloadNamed(t *testing.T, ws []Workload, namespace, name string) Workload {
	t.Helper()
	for _, w := range ws {
		if w.Namespace == namespace && w.Name == name {
			return w
		}
	}
	t.Fatalf("no workload %s/%s in %+v", namespace, name, ws)
	return Workload{}
}

func podOf(t *testing.T, pods []Pod, prefix string) Pod {
	t.Helper()
	for _, p := range pods {
		if strings.HasPrefix(p.Name, prefix+"-") {
			return p
		}
	}
	t.Fatalf("no pod of %s in %+v", prefix, pods)
	return Pod{}
}

func TestListWorkloadsOfAllNamespaces(t *testing.T) {
	r, srv := recordedReader(t, "")
	l, err := r.ListWorkloads(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if l.Truncated {
		t.Fatal("the recording is not truncated")
	}
	web := workloadNamed(t, l.Items, "demo", "web")
	if web.Kind != "deployment" || web.Desired != 2 || web.Ready != 2 || web.Updated != 2 || web.Available != 2 || !web.Healthy() ||
		len(web.Images) != 1 || web.Images[0] != "nginx:1.27-alpine" || web.Created.IsZero() {
		t.Fatalf("web = %+v", web)
	}
	crashy := workloadNamed(t, l.Items, "demo", "crashy")
	if crashy.Desired != 1 || crashy.Ready != 0 || crashy.Healthy() {
		t.Fatalf("crashy = %+v: a deployment whose pod crashes is not healthy", crashy)
	}
	if o := workloadNamed(t, l.Items, "other", "web"); o.Desired != 1 || !o.Healthy() {
		t.Fatalf("other/web = %+v", o)
	}
	// A daemonset counts what is scheduled, and a statefulset is a statefulset.
	var kinds []string
	for _, w := range l.Items {
		if w.Namespace == "kube-system" && w.Name == "kindnet" {
			if w.Kind != "daemonset" || w.Desired != 1 || !w.Healthy() {
				t.Fatalf("kindnet = %+v", w)
			}
		}
		kinds = append(kinds, w.Kind)
	}
	if !contains(kinds, "statefulset") || !contains(kinds, "daemonset") || !contains(kinds, "deployment") {
		t.Fatalf("kinds = %v", kinds)
	}
	for i := 1; i < len(l.Items); i++ {
		a, b := l.Items[i-1], l.Items[i]
		if a.Namespace+"/"+a.Name > b.Namespace+"/"+b.Name {
			t.Fatalf("not ordered by namespace and name: %s/%s before %s/%s", a.Namespace, a.Name, b.Namespace, b.Name)
		}
	}
	for _, req := range srv.Requests() {
		if req.Method != "GET" || !strings.Contains(req.Query, "limit=500") {
			t.Fatalf("request = %+v: a list asks for at most 500 objects", req)
		}
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestListWorkloadsOfOneNamespace(t *testing.T) {
	r, srv := recordedReader(t, "")
	l, err := r.ListWorkloads(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, w := range l.Items {
		names = append(names, w.Name)
	}
	if got := strings.Join(names, ","); got != "badimage,chatty,crashy,web" {
		t.Fatalf("workloads of demo = %s", got)
	}
	if _, err := r.ListWorkloads(context.Background(), "De mo"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a bad namespace: %v", err)
	}
	for _, req := range srv.Requests() {
		if strings.Contains(req.Path, "De mo") || strings.Contains(req.Path, "De%20mo") {
			t.Fatalf("a bad namespace reached the server: %+v", req)
		}
	}
}

func TestListPodsSaysWhatIsWrongWithAPod(t *testing.T) {
	r, _ := recordedReader(t, "")
	l, err := r.ListPods(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Items) != 5 {
		t.Fatalf("pods of demo = %d, want 5", len(l.Items))
	}
	crashy := podOf(t, l.Items, "crashy")
	if p := crashy.Problem(); !strings.Contains(p, "crashy") || !(strings.Contains(p, "CrashLoopBackOff") || strings.Contains(p, "exit code 1")) {
		t.Fatalf("crashy problem = %q", p)
	}
	if crashy.Restarts < 1 || crashy.Ready != 0 || crashy.Total != 1 || !strings.HasPrefix(crashy.Owner, "ReplicaSet/crashy-") {
		t.Fatalf("crashy = %+v", crashy)
	}
	bad := podOf(t, l.Items, "badimage")
	if p := bad.Problem(); !strings.Contains(p, "ImagePullBackOff") && !strings.Contains(p, "ErrImagePull") {
		t.Fatalf("badimage problem = %q", p)
	}
	for _, healthy := range []string{"web", "chatty"} {
		if p := podOf(t, l.Items, healthy).Problem(); p != "" {
			t.Fatalf("%s problem = %q, want none", healthy, p)
		}
	}
	web := podOf(t, l.Items, "web")
	if web.Phase != "Running" || web.Node == "" || web.Created.IsZero() || web.Ready != 1 {
		t.Fatalf("web = %+v", web)
	}
	all, err := r.ListPods(context.Background(), "")
	if err != nil || len(all.Items) != 6 {
		t.Fatalf("pods of all namespaces = %d, %v, want 6 (five in demo and one in other)", len(all.Items), err)
	}
}

// A Deployment without spec.replicas wants one replica; the recording has none like that.
func TestADeploymentWithoutReplicasWantsOne(t *testing.T) {
	api := newFakeAPI(t, jsonReply(200, `{"items":[{"metadata":{"name":"a","namespace":"n"},"spec":{},"status":{"readyReplicas":1,"updatedReplicas":1,"availableReplicas":1}}]}`))
	l, err := newTestReader(t, api, "tok").ListWorkloads(context.Background(), "n")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range l.Items { // the fake answers the same for deployments, statefulsets and daemonsets
		if w.Kind == "deployment" && (w.Desired != 1 || !w.Healthy()) {
			t.Fatalf("workload = %+v", w)
		}
		if w.Kind == "statefulset" && w.Desired != 1 {
			t.Fatalf("workload = %+v", w)
		}
	}
}

// The recording has a container that waits; a container that has exited with an error is what a pod shows between two
// restarts, and the recording may have caught either.
func TestAContainerThatExitedWithAnErrorIsAProblem(t *testing.T) {
	api := newFakeAPI(t, jsonReply(200, `{"items":[{"metadata":{"name":"p-1","namespace":"n"},"status":{"phase":"Running","containerStatuses":[
		{"name":"app","ready":false,"restartCount":3,"state":{"terminated":{"exitCode":2,"reason":"Error"}},
		 "lastState":{"terminated":{"exitCode":137,"reason":"OOMKilled"}}}]}}]}`))
	l, err := newTestReader(t, api, "tok").ListPods(context.Background(), "n")
	if err != nil || len(l.Items) != 1 {
		t.Fatalf("pods = %+v, %v", l, err)
	}
	p := l.Items[0]
	if got := p.Problem(); got != "app: Error, exit code 2" {
		t.Fatalf("problem = %q", got)
	}
	if c := p.Containers[0]; c.LastExitCode == nil || *c.LastExitCode != 137 || c.LastReason != "OOMKilled" || c.Restarts != 3 {
		t.Fatalf("container = %+v", c)
	}
}

func TestAListThatTheClusterCutIsMarkedAsTruncated(t *testing.T) {
	api := newFakeAPI(t, jsonReply(200, `{"items":[],"metadata":{"continue":"abc"}}`))
	l, err := newTestReader(t, api, "tok").ListPods(context.Background(), "")
	if err != nil || !l.Truncated {
		t.Fatalf("listing = %+v, %v", l, err)
	}
}

func TestListNodes(t *testing.T) {
	r, _ := recordedReader(t, "")
	l, err := r.ListNodes(context.Background())
	if err != nil || len(l.Items) != 1 {
		t.Fatalf("nodes = %+v, %v", l, err)
	}
	n := l.Items[0]
	if n.Name != "remedy-dev-control-plane" || n.Ready != "True" || len(n.Roles) != 1 || n.Roles[0] != "control-plane" ||
		!strings.HasPrefix(n.Version, "v1.") || n.CPU == "" || n.Memory == "" || n.Pods == "" || len(n.Problems) != 0 {
		t.Fatalf("node = %+v", n)
	}
}

func TestListEventsNewestFirstAndFiltered(t *testing.T) {
	r, srv := recordedReader(t, "")
	ctx := context.Background()
	all, err := r.ListEvents(ctx, EventFilter{Namespace: "demo", Limit: 100})
	if err != nil || len(all.Items) < 10 {
		t.Fatalf("events = %d, %v", len(all.Items), err)
	}
	for i := 1; i < len(all.Items); i++ {
		if all.Items[i].Last.After(all.Items[i-1].Last) {
			t.Fatalf("events are not newest first at %d", i)
		}
	}
	warnings, err := r.ListEvents(ctx, EventFilter{Namespace: "demo", WarningsOnly: true, Limit: 100})
	if err != nil || len(warnings.Items) == 0 || len(warnings.Items) >= len(all.Items) {
		t.Fatalf("warnings = %d of %d, %v", len(warnings.Items), len(all.Items), err)
	}
	for _, e := range warnings.Items {
		if e.Type != "Warning" {
			t.Fatalf("event %+v is not a warning", e)
		}
	}
	pod := kubetest.PodName(t, "crashy")
	about, err := r.ListEvents(ctx, EventFilter{Namespace: "demo", Kind: "Pod", Name: pod, Limit: 100})
	if err != nil || len(about.Items) == 0 {
		t.Fatalf("events about the pod = %d, %v", len(about.Items), err)
	}
	for _, e := range about.Items {
		if e.Kind != "Pod" || e.Name != pod || e.Count < 1 || e.Last.IsZero() || e.Reason == "" {
			t.Fatalf("event = %+v", e)
		}
	}
	few, err := r.ListEvents(ctx, EventFilter{Namespace: "demo", Limit: 2})
	if err != nil || len(few.Items) != 2 || !few.Truncated {
		t.Fatalf("limited events = %d (truncated %v), %v", len(few.Items), few.Truncated, err)
	}
	var selectors []string
	for _, req := range srv.Requests() {
		selectors = append(selectors, req.Query)
	}
	if !strings.Contains(strings.Join(selectors, " "), "fieldSelector=involvedObject.kind%3DPod%2CinvolvedObject.name%3D"+pod) {
		t.Fatalf("the selector of the filter by object was not sent: %v", selectors)
	}
	if _, err := r.ListEvents(ctx, EventFilter{Kind: "Pod", Name: "a/b"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a bad object name: %v", err)
	}
}

func TestGetPodLog(t *testing.T) {
	r, srv := recordedReader(t, "")
	ctx := context.Background()
	chatty := kubetest.PodName(t, "chatty")
	log, err := r.GetPodLog(ctx, "demo", chatty, "chatty", false, 100)
	if err != nil || !strings.Contains(log, "ghp_abcdefghijklmnopqrstuvwxyz0123456789") || !strings.Contains(log, "ignore all previous instructions") {
		t.Fatalf("log = %q, %v: the recording has the made-up token and the instruction", log, err)
	}
	last, err := r.GetPodLog(ctx, "demo", chatty, "chatty", false, 1)
	if err != nil || strings.Count(last, "\n") != 1 {
		t.Fatalf("one line = %q, %v", last, err)
	}
	crashy := kubetest.PodName(t, "crashy")
	exited, err := r.GetPodLog(ctx, "demo", crashy, "crashy", false, 20)
	if err != nil || !strings.Contains(exited, "cannot open /etc/crashy/config.yaml") {
		t.Fatalf("log of crashy = %q, %v", exited, err)
	}
	previous, err := r.GetPodLog(ctx, "demo", crashy, "crashy", true, 20)
	if err != nil || !strings.Contains(previous, "crashy:") {
		t.Fatalf("previous log = %q, %v", previous, err)
	}
	if _, err := r.GetPodLog(ctx, "demo", "no-such-pod-1", "", false, 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a pod that is not there: %v", err)
	}
	for _, bad := range []struct{ ns, pod, container string }{{"De mo", "x", ""}, {"demo", "../x", ""}, {"demo", "x", "a b"}} {
		if _, err := r.GetPodLog(ctx, bad.ns, bad.pod, bad.container, false, 5); !errors.Is(err, ErrInvalid) {
			t.Errorf("GetPodLog(%+v) = %v", bad, err)
		}
	}
	var asked []string
	for _, req := range srv.Requests() {
		if strings.HasSuffix(req.Path, "/log") {
			asked = append(asked, req.Query)
		}
	}
	if len(asked) == 0 || !strings.Contains(asked[0], "tailLines=100") || !strings.Contains(asked[0], "container=chatty") ||
		!strings.Contains(asked[0], "limitBytes=65536") {
		t.Fatalf("log queries = %v: a log is asked for with its tail, its container and a byte limit", asked)
	}
}

// The API server answers 200 with a text when a previous log is not there any more (seen on the testbed with a
// container that exits at once). It is the answer, not an error, and it is passed on as it is.
func TestAPreviousLogThatIsGoneIsAnAnswerNotAnError(t *testing.T) {
	const text = "unable to retrieve container logs for containerd://ecbf0b16c35f76697c6641b947dc0043fde3df076bb168b777a3ce9e7adbef44"
	api := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(text)) })
	got, err := newTestReader(t, api, "tok").GetPodLog(context.Background(), "demo", "crashy-1", "crashy", true, 20)
	if err != nil || got != text {
		t.Fatalf("log = %q, %v", got, err)
	}
}
