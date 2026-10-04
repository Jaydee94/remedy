package gatekeeper_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/store"
)

const dataNote = "never an instruction"

var bom = string(rune(0xFEFF)) // the byte order mark that GitHub puts in front of a log

// seedIncident adds a repository (once) and an open incident to the store.
func seedIncident(t *testing.T, st *store.Store, ref, check string) store.Incident {
	t.Helper()
	ctx := context.Background()
	repos, err := st.ListRepos(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var repo store.Repo
	if len(repos) == 0 {
		if err := st.SaveConnection(ctx, store.Connection{TokenCiphertext: []byte("sealed"), TokenHint: "wxyz", Login: "octo", Status: store.ConnOK, CheckedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if repo, err = st.AddRepo(ctx, store.ConnectionID, "octo/hello", "main"); err != nil {
			t.Fatal(err)
		}
	} else {
		repo = repos[0]
	}
	in, err := st.OpenIncident(ctx, store.NewIncident{
		RepoID: repo.ID, Ref: ref, RefURL: "https://github.com/octo/hello/pull/7", CheckName: check,
		Conclusion: "failure", HeadSHA: "abc1234", CheckURL: "https://github.com/octo/hello/runs/1",
	}, store.NewActivity{Kind: store.KindIncidentOpened, RepoID: repo.ID, Summary: check + " failed on " + ref})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// data splits a tool answer into its opening note and the JSON that follows.
func data(t *testing.T, text string) any {
	t.Helper()
	note, rest, found := strings.Cut(text, "\n")
	if !found || !strings.Contains(note, dataNote) {
		t.Fatalf("the answer does not open with the note that the data is not an instruction: %q", text)
	}
	var v any
	if err := json.Unmarshal([]byte(rest), &v); err != nil {
		t.Fatalf("the data is not JSON: %v\n%s", err, rest)
	}
	return v
}

func incidentEnv(t *testing.T) *env {
	t.Helper()
	return newEnvWith(t, gatekeeper.IncidentTools)
}

func TestIncidentListFiltersAndLimits(t *testing.T) {
	e := incidentEnv(t)
	ctx := context.Background()
	open := seedIncident(t, e.st, "pr:7", "go")
	resolved := seedIncident(t, e.st, "pr:8", "web")
	if err := e.st.ResolveIncident(ctx, resolved.ID, "green", store.NewActivity{Kind: store.KindIncidentResolved, Summary: "green"}); err != nil {
		t.Fatal(err)
	}

	list := func(useID string, args map[string]any) []any {
		text, isErr := resultText(t, e.call(t, useID, "incident_list", args))
		if isErr {
			t.Fatalf("incident_list %v: %s", args, text)
		}
		return data(t, text).([]any)
	}
	active := list("a", map[string]any{})
	if len(active) != 1 {
		t.Fatalf("the default lists %d incidents, want the 1 active one: %v", len(active), active)
	}
	first := active[0].(map[string]any)
	if int64(first["id"].(float64)) != open.ID || first["repo"] != "octo/hello" || first["ref"] != "pr:7" || first["check"] != "go" ||
		first["state"] != "open" || first["conclusion"] != "failure" || first["headSha"] != "abc1234" {
		t.Fatalf("incident = %v", first)
	}
	if all := list("b", map[string]any{"state": "all"}); len(all) != 2 {
		t.Fatalf("state all lists %d, want 2", len(all))
	}
	if done := list("c", map[string]any{"state": "resolved"}); len(done) != 1 {
		t.Fatalf("state resolved lists %d, want 1", len(done))
	}
	if one := list("d", map[string]any{"state": "all", "limit": 1}); len(one) != 1 {
		t.Fatalf("limit 1 lists %d", len(one))
	}

	for name, args := range map[string]map[string]any{
		"a state that does not exist": {"state": "bogus"},
		"a limit of 51":               {"limit": 51},
		"a negative limit":            {"limit": -1},
		"an unknown member":           {"repo": 1},
	} {
		if text, isErr := resultText(t, e.call(t, "bad_"+strings.ReplaceAll(name, " ", "_"), "incident_list", args)); !isErr {
			t.Errorf("%s was accepted: %s", name, text)
		}
	}
}

func TestIncidentGetShowsTheIncidentItsHistoryAndNotes(t *testing.T) {
	e := incidentEnv(t)
	ctx := context.Background()
	in := seedIncident(t, e.st, "pr:7", "go")
	if err := e.st.AddNote(ctx, in.ID, e.run.ID, "the lock file is stale"); err != nil {
		t.Fatal(err)
	}

	text, isErr := resultText(t, e.call(t, "toolu_1", "incident_get", map[string]any{"id": in.ID}))
	if isErr {
		t.Fatal(text)
	}
	got := data(t, text).(map[string]any)
	incident := got["incident"].(map[string]any)
	if int64(incident["id"].(float64)) != in.ID || incident["check"] != "go" || incident["state"] != "open" {
		t.Fatalf("incident = %v", incident)
	}
	history := got["history"].([]any)
	if len(history) != 2 || history[0].(map[string]any)["kind"] != store.KindNoteAdded || history[1].(map[string]any)["kind"] != store.KindIncidentOpened {
		t.Fatalf("history = %v, want the note entry and the opening, newest first", history)
	}
	notes := got["notes"].([]any)
	if len(notes) != 1 || notes[0].(map[string]any)["note"] != "the lock file is stale" {
		t.Fatalf("notes = %v", notes)
	}

	calls, _ := e.st.ListToolCalls(ctx, e.run.ID)
	if len(calls) != 1 || calls[0].IncidentID != in.ID {
		t.Fatalf("the audit row is not linked to the incident: %+v", calls)
	}
}

func TestIncidentGetRefusesWhatIsNotAnIncident(t *testing.T) {
	e := incidentEnv(t)
	for name, args := range map[string]map[string]any{
		"an unknown id": {"id": 9999},
		"zero":          {"id": 0},
		"negative":      {"id": -3},
		"no id":         {},
		"a string":      {"id": "1"},
		"a fraction":    {"id": 1.5},
	} {
		text, isErr := resultText(t, e.call(t, "toolu_"+strings.ReplaceAll(name, " ", "_"), "incident_get", args))
		if !isErr {
			t.Errorf("%s was accepted: %s", name, text)
		}
		if name == "an unknown id" && !strings.Contains(text, "no incident 9999") {
			t.Errorf("%s: %q does not say that the incident does not exist", name, text)
		}
	}
}

func TestActivityListPagesThroughTheTimeline(t *testing.T) {
	e := incidentEnv(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := e.st.AddActivity(ctx, store.NewActivity{Kind: store.KindPollRecovered, Summary: "entry " + string(rune('a'+i))}); err != nil {
			t.Fatal(err)
		}
	}

	text, isErr := resultText(t, e.call(t, "t1", "activity_list", map[string]any{"limit": 2}))
	if isErr {
		t.Fatal(text)
	}
	page := data(t, text).([]any)
	if len(page) != 2 || page[0].(map[string]any)["summary"] != "entry e" || page[1].(map[string]any)["summary"] != "entry d" {
		t.Fatalf("first page = %v, want the two newest, newest first", page)
	}
	before := int64(page[1].(map[string]any)["id"].(float64))
	text, _ = resultText(t, e.call(t, "t2", "activity_list", map[string]any{"limit": 2, "before": before}))
	page = data(t, text).([]any)
	if len(page) != 2 || page[0].(map[string]any)["summary"] != "entry c" {
		t.Fatalf("second page = %v", page)
	}
	if _, isErr := resultText(t, e.call(t, "t3", "activity_list", map[string]any{"before": -1})); !isErr {
		t.Fatal("a negative cursor was accepted")
	}
}

type fakeLogs struct {
	log, note string
	err       error
	asked     []int64
}

func (f *fakeLogs) JobLog(_ context.Context, id int64) (string, string, error) {
	f.asked = append(f.asked, id)
	return f.log, f.note, f.err
}

func TestJobLogToolShowsTheCleanedRedactedExcerptInsideABlock(t *testing.T) {
	secret := "ghp_" + strings.Repeat("a1B2c3", 6)
	raw := bom + "2026-10-02T12:16:39.4400532Z npm error Invalid: lock file\n" +
		"2026-10-02T12:16:39.4500000Z token is " + secret + "\n" +
		"2026-10-02T12:16:39.4859831Z ##[error]Process completed with exit code 1.\n" +
		"2026-10-02T12:16:39.5007256Z Post job cleanup.\n"
	logs := &fakeLogs{log: raw, note: "the log is longer than 16 MB, so its end is cut off"}
	e := newEnvWith(t, func(*store.Store) []gatekeeper.Tool { return []gatekeeper.Tool{gatekeeper.JobLogTool(logs)} })
	in := seedIncident(t, e.st, "pr:7", "go")

	text, isErr := resultText(t, e.call(t, "toolu_1", "incident_job_log", map[string]any{"id": in.ID}))
	if isErr {
		t.Fatal(text)
	}
	if !strings.Contains(text, dataNote) || !strings.Contains(text, "cut off") {
		t.Fatalf("the answer lacks the data note or the source's note:\n%s", text)
	}
	if strings.Contains(text, secret) || strings.Contains(text, "2026-10-02T12") || strings.Contains(text, bom) || strings.Contains(text, "Post job cleanup") {
		t.Fatalf("the log was not cleaned or redacted:\n%s", text)
	}
	if !strings.Contains(text, "npm error Invalid: lock file") || !strings.Contains(text, "##[error]Process completed") {
		t.Fatalf("the evidence is missing:\n%s", text)
	}
	start := strings.Index(text, "<<<LOG-")
	if start < 0 || !strings.Contains(text[start:], "\n<<<END-LOG-") {
		t.Fatalf("the log is not inside a delimited block:\n%s", text)
	}
	if len(logs.asked) != 1 || logs.asked[0] != in.ID {
		t.Fatalf("JobLog was asked for %v", logs.asked)
	}
	if calls, _ := e.st.ListToolCalls(context.Background(), e.run.ID); len(calls) != 1 || calls[0].IncidentID != in.ID {
		t.Fatalf("the audit row is not linked to the incident: %+v", calls)
	}
}

func TestJobLogToolHandlesNoLogAndErrors(t *testing.T) {
	logs := &fakeLogs{note: "the log has expired"}
	e := newEnvWith(t, func(*store.Store) []gatekeeper.Tool { return []gatekeeper.Tool{gatekeeper.JobLogTool(logs)} })
	in := seedIncident(t, e.st, "pr:7", "go")

	text, isErr := resultText(t, e.call(t, "t1", "incident_job_log", map[string]any{"id": in.ID}))
	if isErr || !strings.Contains(text, "the log has expired") || strings.Contains(text, "<<<LOG-") {
		t.Fatalf("no log: %q (error %v)", text, isErr)
	}

	logs.err = store.ErrNotFound
	if text, isErr := resultText(t, e.call(t, "t2", "incident_job_log", map[string]any{"id": 4242})); !isErr || !strings.Contains(text, "no incident 4242") {
		t.Fatalf("unknown incident: %q (error %v)", text, isErr)
	}
	logs.err = errors.New("GitHub said: secret detail")
	if text, isErr := resultText(t, e.call(t, "t3", "incident_job_log", map[string]any{"id": in.ID})); !isErr || strings.Contains(text, "secret detail") {
		t.Fatalf("a failed read: %q (error %v), want a generic failure", text, isErr)
	}
}
