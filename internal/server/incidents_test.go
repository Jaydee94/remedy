package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

type incidentJSON struct {
	ID             int64  `json:"id"`
	RepoID         int64  `json:"repoId"`
	Repo           string `json:"repo"`
	Ref            string `json:"ref"`
	RefURL         string `json:"refUrl"`
	CheckName      string `json:"checkName"`
	State          string `json:"state"`
	Conclusion     string `json:"conclusion"`
	HeadSHA        string `json:"headSha"`
	CheckURL       string `json:"checkUrl"`
	Occurrences    int    `json:"occurrences"`
	ResolvedReason string `json:"resolvedReason"`
}

// withRepo connects GitHub and adds octo/hello through the API, so that the activity of both steps exists.
func withRepo(t *testing.T) (*ghEnv, int64) {
	t.Helper()
	e := newGHEnv(t, nil, ghKey(t, 1))
	if code, body := e.putToken(t, ghToken); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}
	code, body := e.call(t, http.MethodPost, "/api/repos", `{"fullName":"octo/hello"}`)
	if code != http.StatusCreated {
		t.Fatalf("POST /api/repos = %d %s", code, body)
	}
	return e, int64(field(t, body, "id").(float64))
}

func openIncident(t *testing.T, e *ghEnv, repoID int64, ref, check string) store.Incident {
	t.Helper()
	in, err := e.store.OpenIncident(context.Background(), store.NewIncident{
		RepoID: repoID, Ref: ref, RefURL: "https://github.com/octo/hello/pull/7", CheckName: check,
		Conclusion: "failure", HeadSHA: "abc1234", CheckURL: "https://github.com/octo/hello/runs/1",
	}, store.NewActivity{Kind: store.KindIncidentOpened, Summary: check + " failed on " + ref})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func listIncidents(t *testing.T, e *ghEnv, query string) []incidentJSON {
	t.Helper()
	code, body := e.call(t, http.MethodGet, "/api/incidents"+query, "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/incidents%s = %d %s", query, code, body)
	}
	var list []incidentJSON
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	return list
}

func TestIncidentRoutesNeedASession(t *testing.T) {
	e := newGHEnv(t, nil, ghKey(t, 1))
	for _, path := range []string{"/api/incidents", "/api/incidents/1"} {
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

func TestListIncidentsFiltersByStateAndRepo(t *testing.T) {
	e, repoID := withRepo(t)
	a := openIncident(t, e, repoID, "pr:7", "go")
	b := openIncident(t, e, repoID, "pr:8", "go")
	_ = e.store.ResolveIncident(context.Background(), b.ID, "green", store.NewActivity{Kind: store.KindIncidentResolved, Summary: "green"})

	all := listIncidents(t, e, "")
	if len(all) != 2 || all[0].ID != b.ID || all[1].ID != a.ID {
		t.Fatalf("all = %+v, want newest first", all)
	}
	got := all[1]
	if got.Repo != "Octo/Hello" || got.RepoID != repoID || got.Ref != "pr:7" || got.CheckName != "go" || got.State != "open" ||
		got.Conclusion != "failure" || got.HeadSHA != "abc1234" || got.Occurrences != 1 ||
		got.RefURL != "https://github.com/octo/hello/pull/7" || got.CheckURL != "https://github.com/octo/hello/runs/1" {
		t.Fatalf("incident = %+v", got)
	}
	if all[0].ResolvedReason != "green" {
		t.Fatalf("resolved incident = %+v", all[0])
	}

	for query, want := range map[string]int{
		"?state=active":                          1,
		"?state=open":                            1,
		"?state=resolved":                        1,
		"?state=all":                             2,
		"?state=ignored":                         0,
		"?repo=" + strconv.FormatInt(repoID, 10): 2,
		"?repo=9999":                             0,
	} {
		if got := listIncidents(t, e, query); len(got) != want {
			t.Errorf("%s: %d incidents, want %d", query, len(got), want)
		}
	}
	for _, bad := range []string{"?state=bogus", "?repo=abc", "?repo=-1", "?state=" + url.QueryEscape("open' OR 1=1")} {
		if code, _ := e.call(t, http.MethodGet, "/api/incidents"+bad, ""); code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", bad, code)
		}
	}
}

func TestGetIncidentReturnsItsHistory(t *testing.T) {
	e, repoID := withRepo(t)
	in := openIncident(t, e, repoID, "pr:7", "go")
	_ = e.store.RecordRecurrence(context.Background(), in.ID, "failure", "def5678", "", store.NewActivity{
		Kind: store.KindIncidentRecurred, RepoID: repoID, Summary: "go failed again",
	})

	code, body := e.call(t, http.MethodGet, "/api/incidents/"+strconv.FormatInt(in.ID, 10), "")
	if code != http.StatusOK {
		t.Fatalf("GET = %d %s", code, body)
	}
	var got struct {
		Incident incidentJSON `json:"incident"`
		Activity []struct {
			Kind    string `json:"kind"`
			Summary string `json:"summary"`
			At      string `json:"at"`
		} `json:"activity"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.Incident.ID != in.ID || got.Incident.Occurrences != 2 || got.Incident.HeadSHA != "def5678" {
		t.Fatalf("incident = %+v", got.Incident)
	}
	if len(got.Activity) != 2 || got.Activity[0].Kind != "incident_recurred" || got.Activity[1].Kind != "incident_opened" ||
		got.Activity[0].Summary != "go failed again" || got.Activity[0].At == "" {
		t.Fatalf("activity = %+v, want only this incident's entries, newest first", got.Activity)
	}

	for _, id := range []string{"9999", "abc"} {
		if code, _ := e.call(t, http.MethodGet, "/api/incidents/"+id, ""); code != http.StatusNotFound {
			t.Errorf("GET /api/incidents/%s = %d, want 404", id, code)
		}
	}
}

func TestIgnoreIncident(t *testing.T) {
	e, repoID := withRepo(t)
	in := openIncident(t, e, repoID, "pr:7", "go")
	path := "/api/incidents/" + strconv.FormatInt(in.ID, 10) + "/ignore"

	code, body := e.call(t, http.MethodPost, path, "")
	if code != http.StatusOK || field(t, body, "state") != "ignored" {
		t.Fatalf("POST ignore = %d %s", code, body)
	}
	if code, _ := e.call(t, http.MethodPost, path, ""); code != http.StatusConflict {
		t.Errorf("ignoring twice = %d, want 409", code)
	}
	if code, _ := e.call(t, http.MethodPost, "/api/incidents/9999/ignore", ""); code != http.StatusNotFound {
		t.Errorf("ignoring an unknown incident = %d, want 404", code)
	}
	if got := listIncidents(t, e, "?state=ignored"); len(got) != 1 {
		t.Fatalf("ignored = %+v", got)
	}
	log, _ := e.store.ListActivity(context.Background(), store.ActivityQuery{IncidentID: in.ID})
	if len(log) != 2 || log[0].Kind != store.KindIncidentIgnored {
		t.Fatalf("activity = %+v", log)
	}
}

func TestIgnoreNeedsTheCSRFHeader(t *testing.T) {
	e, repoID := withRepo(t)
	in := openIncident(t, e, repoID, "pr:7", "go")

	req, _ := http.NewRequest(http.MethodPost, e.ts.URL+"/api/incidents/"+strconv.FormatInt(in.ID, 10)+"/ignore", strings.NewReader(""))
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST without X-Remedy-CSRF = %d, want 403", resp.StatusCode)
	}
	if got, _ := e.store.GetIncident(context.Background(), in.ID); got.State != store.IncOpen {
		t.Fatalf("state = %q: the request without the header changed the incident", got.State)
	}
}

func TestUnignoreIncident(t *testing.T) {
	e, repoID := withRepo(t)
	in := openIncident(t, e, repoID, "pr:7", "go")
	id := strconv.FormatInt(in.ID, 10)

	if code, _ := e.call(t, http.MethodPost, "/api/incidents/"+id+"/unignore", ""); code != http.StatusConflict {
		t.Errorf("un-ignoring an open incident = %d, want 409", code)
	}
	if code, body := e.call(t, http.MethodPost, "/api/incidents/"+id+"/ignore", ""); code != http.StatusOK {
		t.Fatalf("ignore = %d %s", code, body)
	}

	code, body := e.call(t, http.MethodPost, "/api/incidents/"+id+"/unignore", "")
	if code != http.StatusOK || field(t, body, "state") != "open" {
		t.Fatalf("POST unignore = %d %s", code, body)
	}
	if code, _ := e.call(t, http.MethodPost, "/api/incidents/"+id+"/unignore", ""); code != http.StatusConflict {
		t.Errorf("un-ignoring twice = %d, want 409", code)
	}
	if code, _ := e.call(t, http.MethodPost, "/api/incidents/9999/unignore", ""); code != http.StatusNotFound {
		t.Errorf("un-ignoring an unknown incident = %d, want 404", code)
	}
	if got := listIncidents(t, e, "?state=ignored"); len(got) != 0 {
		t.Fatalf("ignored = %+v", got)
	}
	log, _ := e.store.ListActivity(context.Background(), store.ActivityQuery{IncidentID: in.ID})
	if len(log) != 3 || log[0].Kind != store.KindIncidentUnignored {
		t.Fatalf("activity = %+v", log)
	}
}

func TestUnignoreNeedsASessionAndTheCSRFHeader(t *testing.T) {
	e, repoID := withRepo(t)
	in := openIncident(t, e, repoID, "pr:7", "go")
	if err := e.store.IgnoreIncident(context.Background(), in.ID, store.NewActivity{Kind: store.KindIncidentIgnored, RepoID: repoID, Summary: "ignored"}); err != nil {
		t.Fatal(err)
	}
	url := e.ts.URL + "/api/incidents/" + strconv.FormatInt(in.ID, 10) + "/unignore"

	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(""))
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST without X-Remedy-CSRF = %d, want 403", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPost, url, strings.NewReader(""))
	req.Header.Set("X-Remedy-CSRF", "1")
	resp, err = http.DefaultClient.Do(req) // no session cookie
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST without a session = %d, want 401", resp.StatusCode)
	}
	if got, _ := e.store.GetIncident(context.Background(), in.ID); got.State != store.IncIgnored {
		t.Fatalf("state = %q: a refused request changed the incident", got.State)
	}
}

func TestConnectionAndRepoChangesAreLogged(t *testing.T) {
	e, repoID := withRepo(t)
	if code, _ := e.call(t, http.MethodDelete, "/api/repos/"+strconv.FormatInt(repoID, 10), ""); code != http.StatusNoContent {
		t.Fatalf("DELETE repo = %d", code)
	}
	if code, _ := e.call(t, http.MethodDelete, "/api/github/connection", ""); code != http.StatusNoContent {
		t.Fatalf("DELETE connection = %d", code)
	}

	log, err := e.store.ListActivity(context.Background(), store.ActivityQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var kinds, summaries []string
	for i := len(log) - 1; i >= 0; i-- {
		kinds = append(kinds, log[i].Kind)
		summaries = append(summaries, log[i].Summary+string(log[i].Data))
	}
	want := []string{store.KindConnectionChanged, store.KindRepoAdded, store.KindRepoRemoved, store.KindConnectionChanged}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("activity kinds = %v, want %v (%v)", kinds, want, summaries)
	}
	if strings.Contains(strings.Join(summaries, " "), "DISTINCTIVE") {
		t.Fatalf("the token leaked into the activity: %v", summaries)
	}
	// The log is newest first: connection removed, repo removed, repo added, connection saved.
	if log[1].Summary != "Removed repository Octo/Hello" || log[2].Summary != "Added repository Octo/Hello" {
		t.Fatalf("summaries = %q and %q", log[2].Summary, log[1].Summary)
	}
	if log[1].RepoID != 0 {
		t.Fatalf("the removal entry links to the deleted repo %d", log[1].RepoID)
	}
}

// diagnosedAt is when the stored diagnosis was written: the list and the single incident carry it for a diagnosed incident
// and leave the key out for one without a diagnosis, like lastDiagnosisAt for an incident that was never diagnosed.
func TestIncidentJSONCarriesDiagnosedAtOnlyWithADiagnosis(t *testing.T) {
	e, repoID := withRepo(t)
	ctx := context.Background()
	plain := openIncident(t, e, repoID, "pr:7", "go")
	diagnosed := openIncident(t, e, repoID, "pr:8", "go")
	r, err := e.store.StartDiagnosis(ctx, store.StartParams{
		IncidentID: diagnosed.ID, Provider: "claude", Prompt: "diagnose", HeadSHA: diagnosed.HeadSHA,
		Limits: store.DefaultLimits(), Now: time.Now(),
	}, store.NewActivity{Kind: store.KindDiagnosisStarted, RepoID: repoID, Summary: "started"})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := e.store.ClaimNext(ctx); err != nil || claimed == nil || claimed.ID != r.ID {
		t.Fatalf("ClaimNext = %+v, %v", claimed, err)
	}
	if err := e.store.FinishRun(ctx, r.ID, run.Outcome{}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.CompleteDiagnosis(ctx, r.ID, []byte(`{"summary":"s"}`), store.NewActivity{Kind: store.KindDiagnosisFinished, RepoID: repoID, Summary: "done"}); err != nil {
		t.Fatal(err)
	}

	code, body := e.call(t, http.MethodGet, "/api/incidents", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/incidents = %d %s", code, body)
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	byID := map[int64]map[string]any{}
	for _, m := range list {
		byID[int64(m["id"].(float64))] = m
	}
	checkDiagnosed := func(where string, m map[string]any) {
		t.Helper()
		s, ok := m["diagnosedAt"].(string)
		if !ok {
			t.Fatalf("%s: diagnosedAt missing in %v", where, m)
		}
		at, err := time.Parse(time.RFC3339Nano, s)
		if err != nil || time.Since(at) > time.Minute || time.Until(at) > time.Minute {
			t.Errorf("%s: diagnosedAt = %q (%v), want about now in RFC 3339", where, s, err)
		}
	}
	checkDiagnosed("list", byID[diagnosed.ID])
	if _, has := byID[plain.ID]["diagnosedAt"]; has {
		t.Errorf("list: an incident without a diagnosis has diagnosedAt: %v", byID[plain.ID])
	}

	for id, want := range map[int64]bool{diagnosed.ID: true, plain.ID: false} {
		code, body := e.call(t, http.MethodGet, "/api/incidents/"+strconv.FormatInt(id, 10), "")
		if code != http.StatusOK {
			t.Fatalf("GET = %d %s", code, body)
		}
		var got struct {
			Incident map[string]any `json:"incident"`
		}
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatal(err)
		}
		if want {
			checkDiagnosed("single", got.Incident)
		} else if _, has := got.Incident["diagnosedAt"]; has {
			t.Errorf("single: an incident without a diagnosis has diagnosedAt: %v", got.Incident)
		}
	}
}
