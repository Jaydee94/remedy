package github_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/secret"
)

const token = "github_pat_TOPSECRET0123456789"

func newClient(t *testing.T, h http.HandlerFunc) *github.Client {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return github.New(ts.URL, secret.NewValue(token), ts.Client())
}

func TestGetUserSendsTheExpectedRequest(t *testing.T) {
	var method, path, auth, accept, version, agent string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		auth, accept = r.Header.Get("Authorization"), r.Header.Get("Accept")
		version, agent = r.Header.Get("X-GitHub-Api-Version"), r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`{"login":"octo"}`))
	})

	u, err := c.GetUser(context.Background())
	if err != nil || u.Login != "octo" {
		t.Fatalf("GetUser = %+v, %v", u, err)
	}
	if method != http.MethodGet || path != "/user" {
		t.Errorf("request = %s %s", method, path)
	}
	if auth != "Bearer "+token {
		t.Errorf("Authorization = %q", auth)
	}
	if accept != "application/vnd.github+json" || version != "2022-11-28" || agent == "" {
		t.Errorf("headers: Accept=%q, X-GitHub-Api-Version=%q, User-Agent=%q", accept, version, agent)
	}
}

func TestGetRepoDecodesTheResponse(t *testing.T) {
	var path string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"full_name":"Octo/Hello","default_branch":"trunk","private":true}`))
	})

	r, err := c.GetRepo(context.Background(), "octo/hello")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/repos/octo/hello" || r.FullName != "Octo/Hello" || r.DefaultBranch != "trunk" || !r.Private {
		t.Fatalf("path = %q, repo = %+v", path, r)
	}
}

func TestStatusCodesMapToErrors(t *testing.T) {
	cases := []struct {
		status int
		check  func(error) bool
	}{
		{http.StatusUnauthorized, func(err error) bool { return errors.Is(err, github.ErrUnauthorized) }},
		{http.StatusNotFound, func(err error) bool { return errors.Is(err, github.ErrNotFound) }},
		{http.StatusInternalServerError, func(err error) bool {
			var api *github.APIError
			return errors.As(err, &api) && api.Status == http.StatusInternalServerError
		}},
		{http.StatusForbidden, func(err error) bool {
			var api *github.APIError
			return errors.As(err, &api) && api.Status == http.StatusForbidden
		}},
	}
	for _, tc := range cases {
		c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"message":"nope"}`))
		})
		if _, err := c.GetUser(context.Background()); err == nil || !tc.check(err) {
			t.Errorf("status %d: error = %v", tc.status, err)
		}
	}
}

func TestInvalidRepoNamesAreRejectedWithoutARequest(t *testing.T) {
	var requests atomic.Int32
	c := newClient(t, func(http.ResponseWriter, *http.Request) { requests.Add(1) })

	for _, name := range []string{"", "a", "a/b/c", "../x", "x/..", "./x", "a/b?x=1", "a /b", "a/b#frag", "/a/b"} {
		if _, err := c.GetRepo(context.Background(), name); !errors.Is(err, github.ErrInvalidRepoName) {
			t.Errorf("%q: error = %v, want ErrInvalidRepoName", name, err)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("%d requests were sent for invalid names", n)
	}
}

func TestErrorsNeverContainTheToken(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"echo ` + token + ` back"}`))
	})

	_, err := c.GetUser(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "TOPSECRET") {
		t.Fatalf("the token leaked into the error: %v", err)
	}
	if !strings.Contains(err.Error(), "***") {
		t.Fatalf("error = %v, want the token replaced by ***", err)
	}
}

// The client is read-only by construction: every exported method is a Get or a List. A method with
// another name (Create, Update, Merge, ...) fails this test and must be a conscious decision.
func TestOnlyReadMethodsAreExported(t *testing.T) {
	typ := reflect.TypeOf(&github.Client{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if !strings.HasPrefix(name, "Get") && !strings.HasPrefix(name, "List") {
			t.Errorf("exported method %q is not a read method", name)
		}
	}
}

func TestEverySendMethodIsGET(t *testing.T) {
	var methods []string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		_, _ = w.Write([]byte(`{"login":"octo","full_name":"o/r","default_branch":"main"}`))
	})
	_, _ = c.GetUser(context.Background())
	_, _ = c.GetRepo(context.Background(), "o/r")

	if len(methods) != 2 {
		t.Fatalf("methods = %v", methods)
	}
	for _, m := range methods {
		if m != http.MethodGet {
			t.Errorf("sent %s, want GET only", m)
		}
	}
}
