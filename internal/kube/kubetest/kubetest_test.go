package kubetest_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/kube/kubetest"
)

func get(t *testing.T, srv *kubetest.Server, method, path, token string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func count(t *testing.T, body string) int {
	t.Helper()
	var l struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &l); err != nil {
		t.Fatalf("not a list: %v: %.200s", err, body)
	}
	return len(l.Items)
}

func TestTheServerKnowsOnlyItsToken(t *testing.T) {
	srv := kubetest.New(t, "tok")
	for _, token := range []string{"", "other"} {
		if code, body := get(t, srv, "GET", "/version", token); code != http.StatusUnauthorized || !strings.Contains(body, `"kind":"Status"`) {
			t.Fatalf("token %q: %d %s", token, code, body)
		}
	}
	if code, body := get(t, srv, "GET", "/version", "tok"); code != 200 || !strings.Contains(body, "gitVersion") {
		t.Fatalf("version: %d %s", code, body)
	}
	if code, body := get(t, srv, "GET", "/no/such/path", "tok"); code != 404 || !strings.Contains(body, `"reason":"NotFound"`) {
		t.Fatalf("unknown path: %d %s", code, body)
	}
}

func TestAnotherTokenCanBeAccepted(t *testing.T) {
	srv := kubetest.New(t, "read")
	srv.Accept("write")
	for _, token := range []string{"read", "write"} {
		if code, _ := get(t, srv, "GET", "/version", token); code != 200 {
			t.Fatalf("token %q: %d", token, code)
		}
	}
	if code, _ := get(t, srv, "GET", "/version", "other"); code != http.StatusUnauthorized {
		t.Fatalf("an unknown token: %d", code)
	}
}

func TestNamespaceViewsAreDerivedFromTheRecordedLists(t *testing.T) {
	srv := kubetest.New(t, "tok")
	for path, want := range map[string]int{
		"/api/v1/pods": 6, "/api/v1/namespaces/demo/pods": 5, "/api/v1/namespaces/other/pods": 1, "/api/v1/namespaces/nowhere/pods": 0,
		"/apis/apps/v1/namespaces/demo/deployments": 4, "/apis/apps/v1/namespaces/demo/daemonsets": 0, "/api/v1/nodes": 1,
		"/apis/argoproj.io/v1alpha1/namespaces/argocd/applications": 1,
	} {
		code, body := get(t, srv, "GET", path, "tok")
		if code != 200 || count(t, body) != want {
			t.Errorf("%s: %d, %d items, want %d", path, code, count(t, body), want)
		}
	}
	pod := kubetest.PodName(t, "web")
	if code, body := get(t, srv, "GET", "/api/v1/namespaces/demo/pods/"+pod, "tok"); code != 200 || !strings.Contains(body, `"name":"`+pod+`"`) {
		t.Fatalf("one pod: %d %.200s", code, body)
	}
	if code, _ := get(t, srv, "GET", "/api/v1/namespaces/demo/pods/gone-1", "tok"); code != 404 {
		t.Fatalf("a pod that is not there: %d", code)
	}
	if code, _ := get(t, srv, "GET", "/api/v1/namespaces/other/pods/"+pod, "tok"); code != 404 {
		t.Fatalf("a pod of another namespace: %d", code)
	}
}

func TestFieldSelectorsOfEventsSelect(t *testing.T) {
	srv := kubetest.New(t, "tok")
	_, all := get(t, srv, "GET", "/api/v1/namespaces/demo/events", "tok")
	_, warnings := get(t, srv, "GET", "/api/v1/namespaces/demo/events?fieldSelector=type%3DWarning", "tok")
	pod := kubetest.PodName(t, "crashy")
	_, about := get(t, srv, "GET", "/api/v1/namespaces/demo/events?fieldSelector=involvedObject.kind%3DPod%2CinvolvedObject.name%3D"+pod, "tok")
	if !(count(t, warnings) > 0 && count(t, warnings) < count(t, all) && count(t, about) > 0 && count(t, about) < count(t, all)) {
		t.Fatalf("all %d, warnings %d, about the pod %d", count(t, all), count(t, warnings), count(t, about))
	}
	if strings.Contains(warnings, `"type": "Normal"`) || strings.Contains(about, `"name": "web-`) {
		t.Fatal("a selector let an event through that it must not")
	}
}

func TestLogsAreTailedLikeTheAPIServerTailsThem(t *testing.T) {
	srv := kubetest.New(t, "tok")
	chatty := kubetest.PodName(t, "chatty")
	_, whole := get(t, srv, "GET", "/api/v1/namespaces/demo/pods/"+chatty+"/log", "tok")
	_, last := get(t, srv, "GET", "/api/v1/namespaces/demo/pods/"+chatty+"/log?tailLines=1", "tok")
	if strings.Count(whole, "\n") < 3 || strings.Count(last, "\n") != 1 || !strings.HasSuffix(whole, last) {
		t.Fatalf("whole = %q, last = %q", whole, last)
	}
	crashy := kubetest.PodName(t, "crashy")
	if code, body := get(t, srv, "GET", "/api/v1/namespaces/demo/pods/"+crashy+"/log?previous=true", "tok"); code != 200 || body == "" {
		t.Fatalf("previous log: %d %q", code, body)
	}
	if code, _ := get(t, srv, "GET", "/api/v1/namespaces/demo/pods/gone-1/log", "tok"); code != 404 {
		t.Fatalf("a log of a pod that is not there: %d", code)
	}
}

func TestChangesAreRecordedAndAnsweredOkAndFailuresCanBeMade(t *testing.T) {
	srv := kubetest.New(t, "tok")
	if code, body := get(t, srv, "PATCH", "/apis/apps/v1/namespaces/demo/deployments/web", "tok"); code != 200 || body != "{}" {
		t.Fatalf("patch: %d %s", code, body)
	}
	if code, _ := get(t, srv, "POST", "/api/v1/namespaces/demo/pods", "tok"); code != http.StatusMethodNotAllowed {
		t.Fatalf("post: %d", code)
	}
	srv.Fail("/api/v1/nodes", 403, "no")
	if code, body := get(t, srv, "GET", "/api/v1/nodes", "tok"); code != 403 || !strings.Contains(body, `"message":"no"`) {
		t.Fatalf("failing path: %d %s", code, body)
	}
	reqs := srv.Requests()
	if len(reqs) != 3 || reqs[0].Method != "PATCH" || reqs[0].Auth != "Bearer tok" {
		t.Fatalf("requests = %+v", reqs)
	}
}
