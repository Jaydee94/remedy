package gatekeeper_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/kube"
	"github.com/Jaydee94/remedy/internal/kube/kubetest"
	"github.com/Jaydee94/remedy/internal/store"
)

// The action tools run against the recorded testbed (see tools_cluster_test.go) with two identities: the read token
// "read-token" and the write token "write-token", and an allowlist of the namespaces given.

type actionRig struct {
	*env
	api *kubetest.Server
}

func tokenFileOf(t *testing.T, name, token string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// newActionRigFor builds the action tools on the API at url and a run with cluster tools whose token the rig's
// environment uses.
func newActionRigFor(t *testing.T, url string, allowlist []string) *env {
	t.Helper()
	cfg := kube.Config{API: url, ReadTokenFile: tokenFileOf(t, "read", "read-token"), WriteTokenFile: tokenFileOf(t, "write", "write-token"),
		WriteNamespaces: allowlist}
	reader, err := kube.NewReader(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := kube.NewWriter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e := newEnvWith(t, func(*store.Store) []gatekeeper.Tool {
		return gatekeeper.ClusterActionTools(reader, writer, cfg, func() time.Time { return kubetest.RecordedAt })
	}, func(c *gatekeeper.Config) {
		c.ProgressInterval = 20 * time.Millisecond
		c.Grace = testGrace
	})
	e.token = clusterRunToken(t, e)
	return e
}

func newActionRig(t *testing.T, allowlist ...string) *actionRig {
	t.Helper()
	api := kubetest.New(t, "read-token")
	api.Accept("write-token")
	return &actionRig{env: newActionRigFor(t, api.URL, allowlist), api: api}
}

// ask makes a call that is refused at once (or answered at once) and returns its text.
func (r *actionRig) ask(t *testing.T, tool string, args any) (string, bool) {
	t.Helper()
	r.next++
	raw, _ := json.Marshal(args)
	status, out := r.post(t, r.token, fmt.Sprintf(
		`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s,"_meta":{"claudecode/toolUseId":"toolu_%d"}}}`,
		r.next, tool, raw, r.next))
	if status != http.StatusOK {
		t.Fatalf("tools/call = %d %v", status, out)
	}
	return resultText(t, out)
}

func (r *actionRig) changes() []kubetest.Request {
	var out []kubetest.Request
	for _, q := range r.api.Requests() {
		if q.Method == http.MethodPatch || q.Method == http.MethodDelete {
			out = append(out, q)
		}
	}
	return out
}

func noApproval(t *testing.T, e *env) {
	t.Helper()
	list, err := e.st.ListApprovals(context.Background(), store.ApprovalFilter{PendingOnly: true})
	if err != nil || len(list) != 0 {
		t.Fatalf("pending approvals = %+v, %v: nothing may be asked for", list, err)
	}
}

// approve opens a call, lets the maintainer approve it and returns the pending call and the answer.
func approve(t *testing.T, e *env, tool string, args any) (store.ToolCall, string, bool) {
	t.Helper()
	s := e.open(t, fmt.Sprintf("toolu_%d", e.next+1), tool, args)
	call := e.pending(t)
	if _, err := e.g.Decide(context.Background(), call.ID, true, "go on"); err != nil {
		t.Fatal(err)
	}
	text, isErr := s.result()
	return call, text, isErr
}

func TestEveryActionToolIsMutatingInTheClusterGroupAndLogsWhatItDid(t *testing.T) {
	tools := gatekeeper.ClusterActionTools(nil, nil, kube.Config{WriteNamespaces: []string{"demo"}}, nil)
	want := []string{"cluster_rollout_restart", "cluster_delete_pod", "argo_refresh", "argo_sync"}
	if len(tools) != len(want) {
		t.Fatalf("%d action tools", len(tools))
	}
	for i, tool := range tools {
		if tool.Name != want[i] || !tool.Mutating || tool.Group != gatekeeper.GroupCluster || tool.Check == nil || tool.Activity == nil || !json.Valid(tool.Schema) {
			t.Errorf("%s: mutating = %v, group = %q, check = %v, activity = %v", tool.Name, tool.Mutating, tool.Group, tool.Check != nil, tool.Activity != nil)
		}
		if !strings.Contains(tool.Description, "maintainer has to approve") {
			t.Errorf("%s: the description does not say that the call waits for an approval", tool.Name)
		}
	}
}

func TestARestartOutsideTheAllowlistIsRefusedBeforeAnApprovalIsAskedFor(t *testing.T) {
	r := newActionRig(t, "demo")
	text, isErr := r.ask(t, "cluster_rollout_restart", map[string]any{"kind": "deployment", "namespace": "other", "name": "web"})
	if !isErr || text != `actions are not allowed in the namespace "other"; they are allowed in: demo` {
		t.Fatalf("answer = %q (%v)", text, isErr)
	}
	noApproval(t, r.env)
	if n := len(r.changes()); n != 0 {
		t.Fatalf("%d changes were sent", n)
	}
	if n := len(r.api.Requests()); n != 0 {
		t.Fatalf("%d requests reached the cluster: the allowlist is checked before anything is asked", n)
	}
}

func TestAnActionOnSomethingThatIsNotThereIsRefusedBeforeAnApprovalIsAskedFor(t *testing.T) {
	r := newActionRig(t, "demo")
	for tool, args := range map[string]map[string]any{
		"cluster_rollout_restart": {"kind": "deployment", "namespace": "demo", "name": "nope"},
		"cluster_delete_pod":      {"namespace": "demo", "name": "gone-1"},
		"argo_refresh":            {"app": "nope"},
		"argo_sync":               {"app": "nope"},
	} {
		if text, isErr := r.ask(t, tool, args); !isErr || !strings.Contains(text, "not found") {
			t.Errorf("%s: answer = %q (%v)", tool, text, isErr)
		}
	}
	noApproval(t, r.env)
}

func TestArgumentsThatCannotBeUsedAreRefusedBeforeAnythingIsSent(t *testing.T) {
	r := newActionRig(t, "demo")
	for name, c := range map[string]struct {
		tool string
		args map[string]any
	}{
		"a kind that is not a workload":  {"cluster_rollout_restart", map[string]any{"kind": "secret", "namespace": "demo", "name": "x"}},
		"a missing namespace":            {"cluster_rollout_restart", map[string]any{"kind": "deployment", "name": "x"}},
		"a path in a name":               {"cluster_delete_pod", map[string]any{"namespace": "demo", "name": "../x"}},
		"an unknown argument":            {"cluster_delete_pod", map[string]any{"namespace": "demo", "name": "x", "grace": 0}},
		"a bad application name":         {"argo_sync", map[string]any{"app": "a/b"}},
		"an option a sync must not have": {"argo_sync", map[string]any{"app": "guestbook", "prune": true}},
	} {
		if text, isErr := r.ask(t, c.tool, c.args); !isErr || strings.Contains(text, "the tool failed") {
			t.Errorf("%s: answer = %q (%v): the agent is told what to fix", name, text, isErr)
		}
	}
	noApproval(t, r.env)
	if n := len(r.api.Requests()); n != 0 {
		t.Fatalf("%d requests were sent for arguments that cannot be used", n)
	}
}

func TestARestartWaitsForTheApprovalAndThenPatchesTheWorkloadWithTheWriteToken(t *testing.T) {
	r := newActionRig(t, "demo")
	s := r.open(t, "toolu_1", "cluster_rollout_restart", map[string]any{"kind": "deployment", "namespace": "demo", "name": "web"})
	call := r.pending(t)
	if string(call.Arguments) != `{"kind":"deployment","namespace":"demo","name":"web"}` || call.Kind != store.CallKindMutating {
		t.Fatalf("pending call = %+v", call)
	}
	if n := len(r.changes()); n != 0 {
		t.Fatalf("%d changes were sent before the approval", n)
	}
	if _, err := r.g.Decide(context.Background(), call.ID, true, "go on"); err != nil {
		t.Fatal(err)
	}
	text, isErr := s.result()
	if isErr || text != "restarted deployment demo/web: its pods are replaced one after the other" {
		t.Fatalf("answer = %q (%v)", text, isErr)
	}
	changes := r.changes()
	if len(changes) != 1 || changes[0].Method != "PATCH" || changes[0].Path != "/apis/apps/v1/namespaces/demo/deployments/web" ||
		changes[0].Auth != "Bearer write-token" || !strings.Contains(changes[0].Body, `"kubectl.kubernetes.io/restartedAt":"2026-10-05T05:40:00Z"`) {
		t.Fatalf("changes = %+v", changes)
	}
	for _, q := range r.api.Requests() {
		if q.Method == http.MethodGet && q.Auth != "Bearer read-token" {
			t.Fatalf("a read used %q", q.Auth)
		}
	}
	acts := activityOf(t, r.st, store.KindClusterAction)
	if len(acts) != 1 || acts[0].Summary != "Restarted deployment demo/web" {
		t.Fatalf("activity = %+v", acts)
	}
}

func TestADeniedActionChangesNothing(t *testing.T) {
	r := newActionRig(t, "demo")
	s := r.open(t, "toolu_1", "cluster_rollout_restart", map[string]any{"kind": "deployment", "namespace": "demo", "name": "web"})
	call := r.pending(t)
	if _, err := r.g.Decide(context.Background(), call.ID, false, "not now"); err != nil {
		t.Fatal(err)
	}
	if text, isErr := s.result(); !isErr || text != "denied: not now" {
		t.Fatalf("answer = %q (%v)", text, isErr)
	}
	if n := len(r.changes()); n != 0 {
		t.Fatalf("%d changes were sent for a denied action", n)
	}
	if n := len(activityOf(t, r.st, store.KindClusterAction)); n != 0 {
		t.Fatalf("%d cluster actions were logged", n)
	}
}

func TestADeletedPodIsTheOneThatWasApproved(t *testing.T) {
	r := newActionRig(t, "demo")
	pod := kubetest.PodName(t, "web")
	_, text, isErr := approve(t, r.env, "cluster_delete_pod", map[string]any{"namespace": "demo", "name": pod})
	if isErr || text != fmt.Sprintf("deleted pod demo/%s: its controller makes a new one", pod) {
		t.Fatalf("answer = %q (%v)", text, isErr)
	}
	changes := r.changes()
	if len(changes) != 1 || changes[0].Method != "DELETE" || changes[0].Path != "/api/v1/namespaces/demo/pods/"+pod || changes[0].Auth != "Bearer write-token" {
		t.Fatalf("changes = %+v", changes)
	}
	if acts := activityOf(t, r.st, store.KindClusterAction); len(acts) != 1 || acts[0].Summary != "Deleted pod demo/"+pod {
		t.Fatalf("activity = %+v", acts)
	}
}

// A pod of the same name that was made after the question was asked is another pod (a StatefulSet makes them): the
// action refuses it. The fake answers a pod that is dated far in the future.
func TestAPodThatWasReplacedAfterTheQuestionIsNotDeleted(t *testing.T) {
	var mu sync.Mutex
	var deletes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.Method == http.MethodDelete {
			mu.Lock()
			deletes++
			mu.Unlock()
		}
		_, _ = w.Write([]byte(`{"metadata":{"name":"db-0","namespace":"demo","creationTimestamp":"2999-01-01T00:00:00Z"},"status":{"phase":"Running"}}`))
	}))
	t.Cleanup(srv.Close)
	e := newActionRigFor(t, srv.URL, []string{"demo"})
	_, text, isErr := approve(t, e, "cluster_delete_pod", map[string]any{"namespace": "demo", "name": "db-0"})
	if !isErr || !strings.Contains(text, "was replaced after the approval was asked for, so nothing was deleted") {
		t.Fatalf("answer = %q (%v)", text, isErr)
	}
	mu.Lock()
	defer mu.Unlock()
	if deletes != 0 {
		t.Fatalf("the replaced pod was deleted (%d deletes)", deletes)
	}
	if n := len(activityOf(t, e.st, store.KindClusterAction)); n != 0 {
		t.Fatalf("%d cluster actions were logged", n)
	}
}

func TestRefreshingAnApplication(t *testing.T) {
	r := newActionRig(t, "demo")
	_, text, isErr := approve(t, r.env, "argo_refresh", map[string]any{"app": "guestbook"})
	if isErr || !strings.HasPrefix(text, "refresh requested for application guestbook") {
		t.Fatalf("answer = %q (%v)", text, isErr)
	}
	_, text, isErr = approve(t, r.env, "argo_refresh", map[string]any{"app": "guestbook", "hard": true})
	if isErr || !strings.HasPrefix(text, "refresh requested for application guestbook") {
		t.Fatalf("answer = %q (%v)", text, isErr)
	}
	changes := r.changes()
	if len(changes) != 2 || changes[0].Body != `{"metadata":{"annotations":{"argocd.argoproj.io/refresh":"normal"}}}` ||
		changes[1].Body != `{"metadata":{"annotations":{"argocd.argoproj.io/refresh":"hard"}}}` ||
		changes[0].Path != "/apis/argoproj.io/v1alpha1/namespaces/argocd/applications/guestbook" || changes[0].Auth != "Bearer write-token" {
		t.Fatalf("changes = %+v", changes)
	}
	acts := activityOf(t, r.st, store.KindClusterAction)
	if len(acts) != 2 {
		t.Fatalf("activity = %+v", acts)
	}
	got := []string{acts[0].Summary, acts[1].Summary}
	if !(has(got, "Requested a refresh of application guestbook") && has(got, "Requested a hard refresh of application guestbook")) {
		t.Fatalf("activity = %v", got)
	}
}

func TestSyncingAnApplicationNeverPrunesForcesOrReplaces(t *testing.T) {
	r := newActionRig(t, "demo")
	_, text, isErr := approve(t, r.env, "argo_sync", map[string]any{"app": "guestbook"})
	if isErr || !strings.HasPrefix(text, "sync requested for application guestbook") {
		t.Fatalf("answer = %q (%v)", text, isErr)
	}
	changes := r.changes()
	if len(changes) != 1 || changes[0].Body != `{"operation":{"initiatedBy":{"username":"remedy"},"sync":{}}}` {
		t.Fatalf("changes = %+v", changes)
	}
	if acts := activityOf(t, r.st, store.KindClusterAction); len(acts) != 1 || acts[0].Summary != "Requested a sync of application guestbook" {
		t.Fatalf("activity = %+v", acts)
	}
}

// The destination of an application is what the allowlist is checked against: RBAC cannot say it.
func TestAnApplicationThatDeploysOutsideTheAllowlistIsRefused(t *testing.T) {
	r := newActionRig(t, "staging")
	for _, tool := range []string{"argo_refresh", "argo_sync"} {
		text, isErr := r.ask(t, tool, map[string]any{"app": "guestbook"})
		if !isErr || !strings.Contains(text, `deploys to the namespace "demo", where actions are not allowed; they are allowed in: staging`) {
			t.Errorf("%s: answer = %q (%v)", tool, text, isErr)
		}
	}
	noApproval(t, r.env)
	if n := len(r.changes()); n != 0 {
		t.Fatalf("%d changes were sent", n)
	}
}

func TestAnApplicationWithAnOperationIsNotSyncedAgain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"metadata":{"name":"guestbook"},"spec":{"project":"default","destination":{"namespace":"demo"}},"operation":{"sync":{}},"status":{}}`))
	}))
	t.Cleanup(srv.Close)
	e := newActionRigFor(t, srv.URL, []string{"demo"})
	r := &actionRig{env: e}
	text, isErr := r.ask(t, "argo_sync", map[string]any{"app": "guestbook"})
	if !isErr || !strings.Contains(text, "an operation is already requested or running for the application guestbook") {
		t.Fatalf("answer = %q (%v)", text, isErr)
	}
	noApproval(t, e)
}
