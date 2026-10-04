package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

// activityEnv is a server whose activity stream looks for news every 10 milliseconds.
type activityEnv struct{ *ghEnv }

func newActivityEnv(t *testing.T) *activityEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ts := httptest.NewServer(server.New(server.Deps{
		Store: st, Auth: auth.New(password), RunnerToken: runnerToken, ActivityInterval: 10 * time.Millisecond,
	}))
	t.Cleanup(ts.Close)

	jar, _ := cookiejar.New(nil)
	e := &ghEnv{ts: ts, store: st, client: &http.Client{Jar: jar}}
	if code, _ := e.call(t, http.MethodPost, "/api/login", `{"password":"`+password+`"}`); code != http.StatusNoContent {
		t.Fatalf("login status = %d", code)
	}
	return &activityEnv{e}
}

// addActivity appends an entry and returns its id.
func addActivity(t *testing.T, st *store.Store, summary string) int64 {
	t.Helper()
	ctx := context.Background()
	if err := st.AddActivity(ctx, store.NewActivity{Kind: store.KindPollRecovered, Summary: summary}); err != nil {
		t.Fatal(err)
	}
	id, err := st.LastActivityID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type timelinePage struct {
	Entries []map[string]any `json:"entries"`
	HasMore bool             `json:"hasMore"`
}

func (e *activityEnv) page(t *testing.T, query string) timelinePage {
	t.Helper()
	code, body := e.call(t, http.MethodGet, "/api/activity"+query, "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/activity%s = %d %s", query, code, body)
	}
	var p timelinePage
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	return p
}

func idOf(e map[string]any) string { return strconv.FormatInt(int64(e["id"].(float64)), 10) }

func TestActivityRoutesNeedASession(t *testing.T) {
	e := newActivityEnv(t)
	for _, path := range []string{"/api/activity", "/api/activity/stream"} {
		resp, err := http.Get(e.ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s without a session = %d, want 401", path, resp.StatusCode)
		}
	}
}

func TestListActivityIsNewestFirstAndPaged(t *testing.T) {
	e := newActivityEnv(t)
	ctx := context.Background()
	if err := e.store.SaveConnection(ctx, store.Connection{TokenCiphertext: []byte("sealed"), TokenHint: "wxyz", Login: "octo", Status: store.ConnOK, CheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	repo, err := e.store.AddRepo(ctx, store.ConnectionID, "octo/hello", "main")
	if err != nil {
		t.Fatal(err)
	}
	in, err := e.store.OpenIncident(ctx, store.NewIncident{
		RepoID: repo.ID, Ref: "pr:7", RefURL: "https://github.com/octo/hello/pull/7", CheckName: "go",
		Conclusion: "failure", HeadSHA: "abc1234", CheckURL: "https://github.com/octo/hello/runs/1",
	}, store.NewActivity{Kind: store.KindIncidentOpened, RepoID: repo.ID, Summary: "go failed on pr:7"})
	if err != nil {
		t.Fatal(err)
	}
	code, body := e.call(t, http.MethodPost, "/api/runs", `{"prompt":"hello"}`)
	if code != http.StatusCreated {
		t.Fatalf("POST /api/runs = %d %s", code, body)
	}
	runID := field(t, body, "id").(string)
	for _, a := range []store.NewActivity{
		{Kind: store.KindConnectionChanged, Summary: "connected"},
		{Kind: store.KindDiagnosisStarted, RepoID: repo.ID, IncidentID: in.ID, RunID: runID, Summary: "diagnosing"},
		{Kind: store.KindPollFailed, RepoID: repo.ID, Summary: "poll failed"},
		{Kind: store.KindPollRecovered, RepoID: repo.ID, Summary: "recovered"},
	} {
		if err := e.store.AddActivity(ctx, a); err != nil {
			t.Fatal(err)
		}
	}

	all := e.page(t, "?limit=200")
	if len(all.Entries) != 5 || all.HasMore {
		t.Fatalf("all = %+v", all)
	}
	for i, want := range []string{"recovered", "poll failed", "diagnosing", "connected", "go failed on pr:7"} {
		if all.Entries[i]["summary"] != want {
			t.Fatalf("entry %d = %v, want %q (newest first)", i, all.Entries[i], want)
		}
	}
	opened, diagnosing, connected := all.Entries[4], all.Entries[2], all.Entries[3]
	if opened["repo"] != "octo/hello" || int64(opened["incidentId"].(float64)) != in.ID || opened["kind"] != store.KindIncidentOpened || opened["at"] == "" {
		t.Errorf("opened = %v", opened)
	}
	if diagnosing["runId"] != runID {
		t.Errorf("diagnosing = %v, want the run id", diagnosing)
	}
	for _, key := range []string{"repo", "incidentId", "runId"} {
		if _, has := connected[key]; has {
			t.Errorf("an entry without a %s still has the key: %v", key, connected)
		}
	}

	first := e.page(t, "?limit=2")
	if len(first.Entries) != 2 || !first.HasMore || first.Entries[0]["summary"] != "recovered" {
		t.Fatalf("first page = %+v", first)
	}
	second := e.page(t, "?limit=2&before="+idOf(first.Entries[1]))
	if len(second.Entries) != 2 || !second.HasMore || second.Entries[0]["summary"] != "diagnosing" {
		t.Fatalf("second page = %+v", second)
	}
	third := e.page(t, "?limit=2&before="+idOf(second.Entries[1]))
	if len(third.Entries) != 1 || third.HasMore || third.Entries[0]["summary"] != "go failed on pr:7" {
		t.Fatalf("third page = %+v", third)
	}
	if exact := e.page(t, "?limit=5"); len(exact.Entries) != 5 || exact.HasMore {
		t.Fatalf("a page that holds everything = %d entries, hasMore %v, want 5 and false", len(exact.Entries), exact.HasMore)
	}
}

func TestListActivityOfAnEmptyLogIsAnEmptyList(t *testing.T) {
	e := newActivityEnv(t)
	code, body := e.call(t, http.MethodGet, "/api/activity", "")
	if code != http.StatusOK || strings.ReplaceAll(strings.TrimSpace(body), " ", "") != `{"entries":[],"hasMore":false}` {
		t.Fatalf("GET /api/activity = %d %s", code, body)
	}
}

func TestListActivityRejectsBadParameters(t *testing.T) {
	e := newActivityEnv(t)
	for _, query := range []string{"?limit=0", "?limit=201", "?limit=abc", "?limit=-1", "?before=0", "?before=abc", "?before=-5"} {
		if code, _ := e.call(t, http.MethodGet, "/api/activity"+query, ""); code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", query, code)
		}
	}
}

type sseEvent struct{ id, event, data string }

// readEvent returns the next event of a stream and skips comment lines. ok is false when the stream ends.
func readEvent(br *bufio.Reader) (ev sseEvent, ok bool) {
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return sseEvent{}, false
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if ev.event != "" {
				return ev, true
			}
		case strings.HasPrefix(line, "id: "):
			ev.id = line[len("id: "):]
		case strings.HasPrefix(line, "event: "):
			ev.event = line[len("event: "):]
		case strings.HasPrefix(line, "data: "):
			ev.data = line[len("data: "):]
		}
	}
}

func nextEvent(t *testing.T, br *bufio.Reader) sseEvent {
	t.Helper()
	ev, ok := readEvent(br)
	if !ok {
		t.Fatal("the stream ended before the next event")
	}
	if ev.event != "activity" {
		t.Fatalf("event = %+v, want an activity event", ev)
	}
	return ev
}

func summaryOf(t *testing.T, ev sseEvent) string {
	t.Helper()
	var entry map[string]any
	if err := json.Unmarshal([]byte(ev.data), &entry); err != nil {
		t.Fatalf("data %q: %v", ev.data, err)
	}
	s, _ := entry["summary"].(string)
	return s
}

// openStream connects to the stream. The deadline keeps a broken stream from hanging the test; the returned
// function closes the connection and must run before the test ends.
func (e *activityEnv) openStream(t *testing.T, query string, header map[string]string) (*bufio.Reader, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.ts.URL+"/api/activity/stream"+query, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		b, _ := io.ReadAll(resp.Body)
		cancel()
		t.Fatalf("stream%s = %d %q %s", query, resp.StatusCode, resp.Header.Get("Content-Type"), b)
	}
	return bufio.NewReader(resp.Body), func() { cancel(); _ = resp.Body.Close() }
}

func TestActivityStreamSendsOnlyWhatComesAfterTheResumePoint(t *testing.T) {
	e := newActivityEnv(t)
	a := addActivity(t, e.store, "first")
	b := addActivity(t, e.store, "second")

	br, closeStream := e.openStream(t, "?after="+strconv.FormatInt(a, 10), nil)
	defer closeStream()
	ev := nextEvent(t, br)
	if ev.id != strconv.FormatInt(b, 10) || summaryOf(t, ev) != "second" {
		t.Fatalf("first event = %+v, want the entry after the resume point", ev)
	}

	c := addActivity(t, e.store, "third")
	ev = nextEvent(t, br)
	if ev.id != strconv.FormatInt(c, 10) || summaryOf(t, ev) != "third" {
		t.Fatalf("live event = %+v", ev)
	}
}

func TestActivityStreamWithoutAResumePointStartsAtTheEnd(t *testing.T) {
	e := newActivityEnv(t)
	addActivity(t, e.store, "old")

	br, closeStream := e.openStream(t, "", nil)
	defer closeStream()
	addActivity(t, e.store, "new")
	if ev := nextEvent(t, br); summaryOf(t, ev) != "new" {
		t.Fatalf("first event = %+v, want only what happens after the connection", ev)
	}
}

func TestActivityStreamAfterZeroReplaysTheLog(t *testing.T) {
	e := newActivityEnv(t)
	addActivity(t, e.store, "first")
	addActivity(t, e.store, "second")

	br, closeStream := e.openStream(t, "?after=0", nil)
	defer closeStream()
	if got := summaryOf(t, nextEvent(t, br)); got != "first" {
		t.Fatalf("first event = %q, want the oldest entry", got)
	}
	if got := summaryOf(t, nextEvent(t, br)); got != "second" {
		t.Fatalf("second event = %q", got)
	}
}

func TestLastEventIDWinsOverAfter(t *testing.T) {
	e := newActivityEnv(t)
	addActivity(t, e.store, "first")
	b := addActivity(t, e.store, "second")
	addActivity(t, e.store, "third")

	// A browser that reconnects keeps the address it first used (after=0) and adds Last-Event-ID.
	br, closeStream := e.openStream(t, "?after=0", map[string]string{"Last-Event-ID": strconv.FormatInt(b, 10)})
	defer closeStream()
	if got := summaryOf(t, nextEvent(t, br)); got != "third" {
		t.Fatalf("first event = %q, want the entry after Last-Event-ID", got)
	}
}

func TestActivityStreamRejectsABadResumePoint(t *testing.T) {
	e := newActivityEnv(t)
	for _, query := range []string{"?after=abc", "?after=-1"} {
		if code, _ := e.call(t, http.MethodGet, "/api/activity/stream"+query, ""); code != http.StatusBadRequest {
			t.Errorf("stream%s = %d, want 400", query, code)
		}
	}
}
