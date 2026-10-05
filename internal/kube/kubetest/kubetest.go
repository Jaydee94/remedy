// Package kubetest is a fake Kubernetes API server for tests. It answers with responses that were recorded from a real
// cluster (the testbed of dev/kind, see dev/kind/record.sh), so that what the tests see has the shape the API server
// really has. Namespace views, field selectors, single objects and log tails are derived from the recorded lists.
package kubetest

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

//go:embed testdata
var files embed.FS

// RecordedAt is a moment shortly after the recording. Tests that show ages use it as "now".
var RecordedAt = time.Date(2026, 10, 5, 5, 40, 0, 0, time.UTC)

// Request is what the server saw of a request.
type Request struct {
	Method string
	Path   string
	Query  string
	Auth   string
	Body   string
}

type failure struct {
	status  int
	message string
}

// Server is the fake API server. It accepts one bearer token and answers 401 to any other.
type Server struct {
	*httptest.Server
	token string

	mu       sync.Mutex
	reqs     []Request
	failures map[string]failure
}

// New starts a server that accepts the token.
func New(t testing.TB, token string) *Server {
	t.Helper()
	s := &Server{token: token, failures: map[string]failure{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Requests returns every request the server has seen, oldest first.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.reqs...)
}

// Fail makes every request for the path answer with a Status object of the given code.
func (s *Server) Fail(path string, status int, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[path] = failure{status, message}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body := new(strings.Builder)
	if r.Body != nil {
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			body.Write(buf[:n])
			if err != nil {
				break
			}
		}
	}
	s.mu.Lock()
	s.reqs = append(s.reqs, Request{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), body.String()})
	fail, failing := s.failures[r.URL.Path]
	s.mu.Unlock()

	switch {
	case r.Header.Get("Authorization") != "Bearer "+s.token:
		status(w, http.StatusUnauthorized, "Unauthorized")
	case failing:
		status(w, fail.status, fail.message)
	case r.Method == http.MethodPatch || r.Method == http.MethodDelete:
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	case r.Method != http.MethodGet:
		status(w, http.StatusMethodNotAllowed, "method not allowed")
	default:
		s.get(w, r)
	}
}

func status(w http.ResponseWriter, code int, message string) {
	reason := map[int]string{401: "Unauthorized", 403: "Forbidden", 404: "NotFound", 405: "MethodNotAllowed", 409: "Conflict", 500: "InternalError"}[code]
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Failure", "message": message, "reason": reason, "code": code})
}

var (
	workloadPath = regexp.MustCompile(`^/apis/apps/v1(?:/namespaces/([^/]+))?/(deployments|statefulsets|daemonsets)(?:/([^/]+))?$`)
	podPath      = regexp.MustCompile(`^/api/v1(?:/namespaces/([^/]+))?/pods(?:/([^/]+)(/log)?)?$`)
	eventsPath   = regexp.MustCompile(`^/api/v1(?:/namespaces/([^/]+))?/events$`)
	nodePath     = regexp.MustCompile(`^/api/v1/nodes(?:/([^/]+))?$`)
	appPath      = regexp.MustCompile(`^/apis/argoproj\.io/v1alpha1/namespaces/([^/]+)/applications(?:/([^/]+))?$`)
)

type object = map[string]any

func load(name string) []byte {
	b, err := files.ReadFile("testdata/" + name)
	if err != nil {
		panic("kubetest: " + err.Error())
	}
	return b
}

func items(name string) []object {
	var l struct {
		Items []object `json:"items"`
	}
	if err := json.Unmarshal(load(name), &l); err != nil {
		panic("kubetest: " + name + ": " + err.Error())
	}
	return l.Items
}

func nested(o object, keys ...string) string {
	var cur any = o
	for _, k := range keys {
		m, ok := cur.(object)
		if !ok {
			return ""
		}
		cur = m[k]
	}
	s, _ := cur.(string)
	return s
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// serveItems answers a list, or one object of it when name is given. A namespace keeps only that namespace's items.
func serveItems(w http.ResponseWriter, all []object, namespace, name, what string) {
	var keep []object
	for _, o := range all {
		if namespace != "" && nested(o, "metadata", "namespace") != namespace {
			continue
		}
		if name != "" {
			if nested(o, "metadata", "name") == name {
				reply(w, o)
				return
			}
			continue
		}
		keep = append(keep, o)
	}
	if name != "" {
		status(w, http.StatusNotFound, fmt.Sprintf("%s %q not found", what, name))
		return
	}
	if keep == nil {
		keep = []object{}
	}
	reply(w, object{"kind": "List", "apiVersion": "v1", "metadata": object{}, "items": keep})
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == "/version":
		_, _ = w.Write(load("version.json"))
	case workloadPath.MatchString(p):
		m := workloadPath.FindStringSubmatch(p)
		serveItems(w, items(m[2]+"-all.json"), m[1], m[3], strings.TrimSuffix(m[2], "s"))
	case podPath.MatchString(p):
		m := podPath.FindStringSubmatch(p)
		if m[3] == "/log" {
			s.log(w, r, m[1], m[2])
			return
		}
		serveItems(w, items("pods-all.json"), m[1], m[2], "pod")
	case eventsPath.MatchString(p):
		s.events(w, r, eventsPath.FindStringSubmatch(p)[1])
	case nodePath.MatchString(p):
		serveItems(w, items("nodes.json"), "", nodePath.FindStringSubmatch(p)[1], "node")
	case appPath.MatchString(p):
		m := appPath.FindStringSubmatch(p)
		serveItems(w, items("applications.json"), "", m[2], "application")
	default:
		status(w, http.StatusNotFound, "the server could not find the requested resource")
	}
}

// events serves the recorded events of the namespace demo, filtered by the field selectors the read tools use.
func (s *Server) events(w http.ResponseWriter, r *http.Request, namespace string) {
	if namespace != "" && namespace != "demo" {
		reply(w, object{"kind": "EventList", "metadata": object{}, "items": []object{}})
		return
	}
	selectors := map[string]string{}
	for _, part := range strings.Split(r.URL.Query().Get("fieldSelector"), ",") {
		if k, v, ok := strings.Cut(part, "="); ok {
			selectors[k] = v
		}
	}
	keep := []object{}
	for _, e := range items("events-demo.json") {
		if v, ok := selectors["type"]; ok && nested(e, "type") != v {
			continue
		}
		if v, ok := selectors["involvedObject.kind"]; ok && nested(e, "involvedObject", "kind") != v {
			continue
		}
		if v, ok := selectors["involvedObject.name"]; ok && nested(e, "involvedObject", "name") != v {
			continue
		}
		keep = append(keep, e)
	}
	reply(w, object{"kind": "EventList", "metadata": object{}, "items": keep})
}

// log serves the recorded logs of the pods of chatty and crashy, tailed like the API server tails them.
func (s *Server) log(w http.ResponseWriter, r *http.Request, namespace, pod string) {
	var file string
	switch {
	case namespace != "demo":
	case strings.HasPrefix(pod, "chatty-"):
		file = "pod-log-chatty"
	case strings.HasPrefix(pod, "crashy-") && r.URL.Query().Get("previous") == "true":
		file = "pod-log-crashy-previous"
	case strings.HasPrefix(pod, "crashy-"):
		file = "pod-log-crashy"
	}
	if file == "" {
		status(w, http.StatusNotFound, fmt.Sprintf("pods %q not found", pod))
		return
	}
	text := string(load(file))
	if n, err := strconv.Atoi(r.URL.Query().Get("tailLines")); err == nil && n > 0 {
		lines := strings.SplitAfter(text, "\n")
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		if len(lines) > n {
			text = strings.Join(lines[len(lines)-n:], "")
		}
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(text))
}

// PodName returns the name of the pod of a workload of the namespace demo in the recording, for example "crashy".
func PodName(t testing.TB, workload string) string {
	t.Helper()
	for _, o := range items("pods-all.json") {
		name := nested(o, "metadata", "name")
		if nested(o, "metadata", "namespace") == "demo" && strings.HasPrefix(name, workload+"-") {
			return name
		}
	}
	t.Fatalf("kubetest: no pod of %q in the recording", workload)
	return ""
}
