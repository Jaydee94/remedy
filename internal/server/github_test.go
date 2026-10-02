package server_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

const ghToken = "ghp_DISTINCTIVE0123456789abcdefghijklmnopqrst"

func ghKey(t *testing.T, fill byte) secret.Key {
	t.Helper()
	k, err := secret.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// fakeGitHub stands in for the GitHub API. It records the tokens it was created with.
type fakeGitHub struct {
	mu      sync.Mutex
	tokens  []string
	user    github.User
	userErr error
	repos   map[string]github.Repo // keyed by lower-case full name
	repoErr error
}

func (f *fakeGitHub) factory(token secret.Value) server.GitHub {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = append(f.tokens, token.Reveal())
	return f
}

func (f *fakeGitHub) lastToken() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.tokens) == 0 {
		return ""
	}
	return f.tokens[len(f.tokens)-1]
}

func (f *fakeGitHub) GetUser(context.Context) (github.User, error) { return f.user, f.userErr }

func (f *fakeGitHub) GetRepo(_ context.Context, name string) (github.Repo, error) {
	if f.repoErr != nil {
		return github.Repo{}, f.repoErr
	}
	if !strings.Contains(name, "/") {
		return github.Repo{}, github.ErrInvalidRepoName
	}
	r, ok := f.repos[strings.ToLower(name)]
	if !ok {
		return github.Repo{}, github.ErrNotFound
	}
	return r, nil
}

type ghEnv struct {
	ts     *httptest.Server
	store  *store.Store
	gh     *fakeGitHub
	key    secret.Key
	client *http.Client
}

// newGHEnv starts a server over st (a new store if nil) that seals with key, and logs in.
func newGHEnv(t *testing.T, st *store.Store, key secret.Key) *ghEnv {
	t.Helper()
	if st == nil {
		var err error
		st, err = store.Open(filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
	}
	gh := &fakeGitHub{
		user:  github.User{Login: "octo"},
		repos: map[string]github.Repo{"octo/hello": {FullName: "Octo/Hello", DefaultBranch: "main"}},
	}
	ts := httptest.NewServer(server.New(server.Deps{
		Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Key: key, NewGitHub: gh.factory,
	}))
	t.Cleanup(ts.Close)

	jar, _ := cookiejar.New(nil)
	e := &ghEnv{ts: ts, store: st, gh: gh, key: key, client: &http.Client{Jar: jar}}
	if code, _ := e.call(t, http.MethodPost, "/api/login", `{"password":"`+password+`"}`); code != http.StatusNoContent {
		t.Fatalf("login status = %d", code)
	}
	return e
}

// call sends an admin request and returns the status and the body.
func (e *ghEnv) call(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	req.Header.Set("X-Remedy-CSRF", "1")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (e *ghEnv) putToken(t *testing.T, token string) (int, string) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"token": token})
	return e.call(t, http.MethodPut, "/api/github/connection", string(b))
}

func field(t *testing.T, body, name string) any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("body %q is not a JSON object: %v", body, err)
	}
	return m[name]
}

func TestConnectionLifecycleNeverExposesTheToken(t *testing.T) {
	e := newGHEnv(t, nil, ghKey(t, 1))

	code, body := e.call(t, http.MethodGet, "/api/github/connection", "")
	if code != http.StatusOK || field(t, body, "connected") != false {
		t.Fatalf("empty GET = %d %s", code, body)
	}

	var bodies []string
	code, body = e.putToken(t, ghToken)
	bodies = append(bodies, body)
	if code != http.StatusOK || field(t, body, "connected") != true || field(t, body, "login") != "octo" ||
		field(t, body, "tokenHint") != "…qrst" || field(t, body, "status") != "ok" {
		t.Fatalf("PUT = %d %s", code, body)
	}
	if e.gh.lastToken() != ghToken {
		t.Fatalf("GitHub was called with token %q", e.gh.lastToken())
	}

	_, body = e.call(t, http.MethodGet, "/api/github/connection", "")
	bodies = append(bodies, body)
	_, body = e.call(t, http.MethodPost, "/api/github/connection/check", "")
	bodies = append(bodies, body)
	_, body = e.call(t, http.MethodGet, "/api/repos", "")
	bodies = append(bodies, body)

	for _, b := range bodies {
		if strings.Contains(b, "DISTINCTIVE") {
			t.Errorf("the token leaked into a response: %s", b)
		}
	}

	if code, _ := e.call(t, http.MethodDelete, "/api/github/connection", ""); code != http.StatusNoContent {
		t.Fatalf("DELETE = %d", code)
	}
	if code, _ := e.call(t, http.MethodDelete, "/api/github/connection", ""); code != http.StatusNotFound {
		t.Fatalf("second DELETE = %d, want 404", code)
	}
	_, body = e.call(t, http.MethodGet, "/api/github/connection", "")
	if field(t, body, "connected") != false {
		t.Fatalf("GET after DELETE = %s", body)
	}
}

func TestTheTokenIsStoredSealedAndBoundToItsRow(t *testing.T) {
	key := ghKey(t, 1)
	e := newGHEnv(t, nil, key)
	if code, body := e.putToken(t, ghToken); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}

	conn, err := e.store.GetConnection(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(conn.TokenCiphertext, []byte("DISTINCTIVE")) {
		t.Fatal("the plaintext token is in the database")
	}
	if conn.TokenHint != "qrst" {
		t.Errorf("TokenHint = %q, want the last four characters", conn.TokenHint)
	}
	if got, err := key.Open(conn.TokenCiphertext, "github_connection:1"); err != nil || string(got) != ghToken {
		t.Fatalf("Open = %q, %v", got, err)
	}
	if _, err := key.Open(conn.TokenCiphertext, "github_connection:2"); err == nil {
		t.Fatal("the ciphertext opens in another row's context")
	}
}

func TestPutConnectionValidatesInput(t *testing.T) {
	e := newGHEnv(t, nil, ghKey(t, 1))

	if code, _ := e.putToken(t, "short"); code != http.StatusBadRequest {
		t.Errorf("short token = %d, want 400", code)
	}
	if code, _ := e.call(t, http.MethodPut, "/api/github/connection", `not json`); code != http.StatusBadRequest {
		t.Errorf("invalid JSON = %d, want 400", code)
	}

	e.gh.userErr = github.ErrUnauthorized
	code, body := e.putToken(t, ghToken)
	if code != http.StatusBadRequest || !strings.Contains(body, "rejected") {
		t.Errorf("rejected token = %d %s, want 400 mentioning rejected", code, body)
	}

	e.gh.userErr = &github.APIError{Status: 500, Message: "boom"}
	if code, _ := e.putToken(t, ghToken); code != http.StatusBadGateway {
		t.Errorf("GitHub failure = %d, want 502", code)
	}

	if _, err := e.store.GetConnection(context.Background()); err == nil {
		t.Error("a connection was stored although validation failed")
	}
}

func TestGitHubRoutesNeedASession(t *testing.T) {
	e := newGHEnv(t, nil, ghKey(t, 1))
	resp, err := http.Get(e.ts.URL + "/api/github/connection")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET without a session = %d, want 401", resp.StatusCode)
	}
}

func TestRepoLifecycle(t *testing.T) {
	e := newGHEnv(t, nil, ghKey(t, 1))

	if code, _ := e.call(t, http.MethodPost, "/api/repos", `{"fullName":"octo/hello"}`); code != http.StatusConflict {
		t.Fatalf("adding a repo without a connection = %d, want 409", code)
	}

	e.putToken(t, ghToken)
	code, body := e.call(t, http.MethodPost, "/api/repos", `{"fullName":"octo/hello"}`)
	if code != http.StatusCreated || field(t, body, "fullName") != "Octo/Hello" ||
		field(t, body, "defaultBranch") != "main" || field(t, body, "enabled") != true {
		t.Fatalf("add = %d %s (the canonical name from GitHub is stored)", code, body)
	}
	if e.gh.lastToken() != ghToken {
		t.Fatalf("the repo check used token %q, want the decrypted stored token", e.gh.lastToken())
	}
	id := int64(field(t, body, "id").(float64))

	if code, _ := e.call(t, http.MethodPost, "/api/repos", `{"fullName":"OCTO/hello"}`); code != http.StatusConflict {
		t.Fatalf("duplicate = %d, want 409", code)
	}

	_, body = e.call(t, http.MethodGet, "/api/repos", "")
	var list []map[string]any
	if err := json.Unmarshal([]byte(body), &list); err != nil || len(list) != 1 {
		t.Fatalf("list = %s (%v)", body, err)
	}

	path := "/api/repos/" + strconv.FormatInt(id, 10)
	if code, _ := e.call(t, http.MethodPatch, path, `{"enabled":false}`); code != http.StatusNoContent {
		t.Fatalf("PATCH = %d", code)
	}
	_, body = e.call(t, http.MethodGet, "/api/repos", "")
	if !strings.Contains(body, `"enabled":false`) {
		t.Fatalf("repo is still enabled: %s", body)
	}
	if code, _ := e.call(t, http.MethodPatch, path, `{}`); code != http.StatusBadRequest {
		t.Fatalf("PATCH without enabled = %d, want 400", code)
	}
	if code, _ := e.call(t, http.MethodPatch, "/api/repos/9999", `{"enabled":true}`); code != http.StatusNotFound {
		t.Fatalf("PATCH unknown = %d, want 404", code)
	}

	if code, _ := e.call(t, http.MethodDelete, path, ""); code != http.StatusNoContent {
		t.Fatalf("DELETE = %d", code)
	}
	if code, _ := e.call(t, http.MethodDelete, path, ""); code != http.StatusNotFound {
		t.Fatalf("second DELETE = %d, want 404", code)
	}
	if code, _ := e.call(t, http.MethodDelete, "/api/repos/not-a-number", ""); code != http.StatusNotFound {
		t.Fatalf("DELETE with a bad id = %d, want 404", code)
	}
}

func TestAddRepoErrors(t *testing.T) {
	e := newGHEnv(t, nil, ghKey(t, 1))
	e.putToken(t, ghToken)

	if code, _ := e.call(t, http.MethodPost, "/api/repos", `{"fullName":"nope"}`); code != http.StatusBadRequest {
		t.Errorf("invalid name = %d, want 400", code)
	}
	if code, body := e.call(t, http.MethodPost, "/api/repos", `{"fullName":"octo/missing"}`); code != http.StatusBadRequest ||
		!strings.Contains(body, "not found") {
		t.Errorf("unknown repo = %d %s, want 400 mentioning not found", code, body)
	}

	e.gh.repoErr = &github.APIError{Status: 502, Message: "bad gateway"}
	if code, _ := e.call(t, http.MethodPost, "/api/repos", `{"fullName":"octo/hello"}`); code != http.StatusBadGateway {
		t.Errorf("GitHub failure = %d, want 502", code)
	}
}

func TestAWrongMasterKeyMarksTheConnectionUndecryptable(t *testing.T) {
	first := newGHEnv(t, nil, ghKey(t, 1))
	first.putToken(t, ghToken)

	second := newGHEnv(t, first.store, ghKey(t, 2))
	code, body := second.call(t, http.MethodPost, "/api/github/connection/check", "")
	if code != http.StatusOK || field(t, body, "status") != "undecryptable" {
		t.Fatalf("check with a wrong key = %d %s", code, body)
	}
	_, body = second.call(t, http.MethodGet, "/api/github/connection", "")
	if field(t, body, "status") != "undecryptable" || field(t, body, "statusDetail") == "" {
		t.Fatalf("GET = %s", body)
	}
	if code, _ := second.call(t, http.MethodPost, "/api/repos", `{"fullName":"octo/hello"}`); code != http.StatusConflict {
		t.Fatalf("adding a repo with an undecryptable token = %d, want 409", code)
	}
	if len(second.gh.tokens) != 0 {
		t.Fatalf("GitHub was called although the token could not be decrypted: %v", second.gh.tokens)
	}

	// Entering the token again repairs the connection.
	if code, body := second.putToken(t, ghToken); code != http.StatusOK || field(t, body, "status") != "ok" {
		t.Fatalf("PUT = %d %s", code, body)
	}
}

func TestCheckConnectionReportsGitHubProblems(t *testing.T) {
	e := newGHEnv(t, nil, ghKey(t, 1))
	if code, _ := e.call(t, http.MethodPost, "/api/github/connection/check", ""); code != http.StatusNotFound {
		t.Fatalf("check without a connection = %d, want 404", code)
	}

	e.putToken(t, ghToken)
	e.gh.userErr = github.ErrUnauthorized
	code, body := e.call(t, http.MethodPost, "/api/github/connection/check", "")
	if code != http.StatusOK || field(t, body, "status") != "error" {
		t.Fatalf("check with a rejected token = %d %s", code, body)
	}

	e.gh.userErr = nil
	_, body = e.call(t, http.MethodPost, "/api/github/connection/check", "")
	if field(t, body, "status") != "ok" {
		t.Fatalf("check after recovery = %s", body)
	}
}
