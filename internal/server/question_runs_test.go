package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

// claimOne is what a runner gets when it claims the next queued run.
func claimOne(t *testing.T, baseURL string) run.Claim {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/runner/v1/claim", nil)
	req.Header.Set("Authorization", "Bearer "+runnerToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d, want 200", resp.StatusCode)
	}
	var c run.Claim
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAQuestionRunIsClaimedWithTheFrameAroundTheQuestion(t *testing.T) {
	e, repoID := withRepo(t)
	in := openIncident(t, e, repoID, "pr:7", "go")
	ctx := context.Background()
	q, err := e.store.CreateQuestionRun(ctx, "claude", "why did it fail twice?", in.ID, false)
	if err != nil {
		t.Fatal(err)
	}

	c := claimOne(t, e.ts.URL)
	if c.ID != q.ID {
		t.Fatalf("claimed %s, want %s", c.ID, q.ID)
	}
	for _, want := range []string{"incident " + strconv.FormatInt(in.ID, 10), "incident_get", "why did it fail twice?"} {
		if !strings.Contains(c.Prompt, want) {
			t.Errorf("the claimed prompt lacks %q:\n%s", want, c.Prompt)
		}
	}
	if c.MCPToken == "" {
		t.Error("a question run has gatekeeper access: the claim must carry a token")
	}
	stored, err := e.store.GetRun(ctx, q.ID)
	if err != nil || stored.Prompt != "why did it fail twice?" {
		t.Fatalf("the stored prompt = %q, %v, want the bare question", stored.Prompt, err)
	}
}

func TestAnAdhocRunWithoutAnIncidentIsClaimedAsItIs(t *testing.T) {
	e, _ := withRepo(t)
	ctx := context.Background()
	r, err := e.store.CreateToolRun(ctx, "claude", "list the incidents")
	if err != nil {
		t.Fatal(err)
	}
	if c := claimOne(t, e.ts.URL); c.ID != r.ID || c.Prompt != "list the incidents" {
		t.Fatalf("claim = %q (run %s), want the prompt unchanged", c.Prompt, c.ID)
	}
}

// questionEnv is a server with the gatekeeper, a signed-in admin client and one open incident.
func questionEnv(t *testing.T, cluster server.Cluster) (*env, *http.Client, store.Incident) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.SaveConnection(ctx, store.Connection{TokenCiphertext: []byte{1}, TokenHint: "1234", Login: "octo", Status: store.ConnOK}); err != nil {
		t.Fatal(err)
	}
	repo, err := st.AddRepo(ctx, store.ConnectionID, "octo/hello", "main")
	if err != nil {
		t.Fatal(err)
	}
	in, err := st.OpenIncident(ctx, store.NewIncident{
		RepoID: repo.ID, Ref: "pr:7", CheckName: "go", Conclusion: "failure", HeadSHA: "abc1234",
	}, store.NewActivity{Kind: store.KindIncidentOpened, RepoID: repo.ID, Summary: "go failed on pr:7"})
	if err != nil {
		t.Fatal(err)
	}
	g := gatekeeper.New(gatekeeper.Config{Store: st, Tools: gatekeeper.IncidentTools(st)})
	ts := httptest.NewServer(server.New(server.Deps{
		Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Gatekeeper: g, Cluster: cluster,
	}))
	t.Cleanup(ts.Close)
	e := &env{ts: ts, store: st}
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if resp := e.do(t, c, http.MethodPost, "/api/login", `{"password":"`+password+`"}`, true); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	return e, c, in
}

func questionBody(prompt string, tools bool, incidentID string) string {
	return `{"prompt":` + strconv.Quote(prompt) + `,"tools":` + strconv.FormatBool(tools) + `,"incidentId":` + incidentID + `}`
}

func TestAQuestionAboutAnIncidentCreatesAToolRunLinkedToIt(t *testing.T) {
	e, c, in := questionEnv(t, server.Cluster{})
	id := strconv.FormatInt(in.ID, 10)

	resp := e.do(t, c, http.MethodPost, "/api/runs", questionBody("why did it fail twice?", true, id), true)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := bodyOf(t, resp)
	if field(t, body, "role") != "adhoc" || field(t, body, "mcp") != true || field(t, body, "incidentId") != float64(in.ID) ||
		field(t, body, "prompt") != "why did it fail twice?" || field(t, body, "status") != "queued" {
		t.Fatalf("run = %s", body)
	}
}

func TestAQuestionAboutAnIncidentIsRefusedWhenItCannotWork(t *testing.T) {
	e, c, in := questionEnv(t, server.Cluster{})
	id := strconv.FormatInt(in.ID, 10)

	cases := []struct {
		name string
		body string
		want int
	}{
		{"without tools", questionBody("why?", false, id), http.StatusBadRequest},
		{"an unknown incident", questionBody("why?", true, "9999"), http.StatusNotFound},
		{"incident zero", questionBody("why?", true, "0"), http.StatusNotFound},
		{"a negative incident", questionBody("why?", true, "-3"), http.StatusNotFound},
		{"an empty question", questionBody("", true, id), http.StatusBadRequest},
		{"cluster tools with no cluster", `{"prompt":"why?","tools":true,"cluster":true,"incidentId":` + id + `}`, http.StatusConflict},
	}
	for _, tc := range cases {
		if got := e.do(t, c, http.MethodPost, "/api/runs", tc.body, true).StatusCode; got != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, got, tc.want)
		}
	}
	if runs, _ := e.store.ListRuns(context.Background(), 50); len(runs) != 0 {
		t.Fatalf("%d runs, want 0: a refused question must not leave a run", len(runs))
	}
}

func TestAQuestionAboutAnAlertIncidentWorksLikeOneAboutACheck(t *testing.T) {
	e, c, _ := questionEnv(t, server.Cluster{})
	alert, err := e.store.OpenIncident(context.Background(), store.NewIncident{
		Source: store.SourceAlertmanager, Key: "HighLatency/api", Title: "HighLatency api", Severity: "critical", AutoDiagnose: true,
		Conclusion: "firing",
	}, store.NewActivity{Kind: store.KindIncidentOpened, Summary: "Incident opened: alert HighLatency api"})
	if err != nil {
		t.Fatal(err)
	}
	resp := e.do(t, c, http.MethodPost, "/api/runs", questionBody("is it still firing?", true, strconv.FormatInt(alert.ID, 10)), true)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	body := bodyOf(t, resp)
	if got := field(t, body, "incidentId"); got != float64(alert.ID) {
		t.Fatalf("incidentId = %v, want %d: %s", got, alert.ID, body)
	}
}

func TestListRunsCanBeFilteredByIncident(t *testing.T) {
	e, c, in := questionEnv(t, server.Cluster{})
	ctx := context.Background()
	if _, err := e.store.CreateRun(ctx, "claude", "an unrelated run"); err != nil {
		t.Fatal(err)
	}
	q, err := e.store.CreateQuestionRun(ctx, "claude", "why?", in.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(in.ID, 10)

	got := decode[[]run.Run](t, e.do(t, c, http.MethodGet, "/api/runs?incident="+id, "", false))
	if len(got) != 1 || got[0].ID != q.ID {
		t.Fatalf("runs of the incident = %+v, want only the question", got)
	}
	if all := decode[[]run.Run](t, e.do(t, c, http.MethodGet, "/api/runs", "", false)); len(all) != 2 {
		t.Fatalf("all runs = %d, want 2: the filter is opt-in", len(all))
	}

	// An incident with no runs is an empty list, not null and not 404.
	resp := e.do(t, c, http.MethodGet, "/api/runs?incident=9999", "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unknown incident: status = %d, want 200", resp.StatusCode)
	}
	if body := strings.TrimSpace(bodyOf(t, resp)); body != "[]" {
		t.Fatalf("body = %q, want []", body)
	}

	for _, bad := range []string{"abc", "0", "-3", "1.5", "99999999999999999999"} {
		if got := e.do(t, c, http.MethodGet, "/api/runs?incident="+bad, "", false).StatusCode; got != http.StatusBadRequest {
			t.Errorf("?incident=%s: status = %d, want 400", bad, got)
		}
	}
}

// The frame belongs to a question, not to a run in general: a responder run is claimed with the prompt that was stored for it
// (which the responder built from cleaned and bounded data), without the frame of a question.
func TestAResponderRunIsClaimedWithItsStoredPromptAndNotFramed(t *testing.T) {
	e, repoID := withRepo(t)
	in := openIncident(t, e, repoID, "pr:7", "go")
	started, err := e.store.StartDiagnosis(context.Background(), store.StartParams{
		IncidentID: in.ID, Provider: "claude", Prompt: "diagnose go: DATA from GitHub", HeadSHA: in.HeadSHA,
		Limits: store.DefaultLimits(), Now: time.Now(),
	}, store.NewActivity{Kind: store.KindDiagnosisStarted, RepoID: in.RepoID, Summary: "started"})
	if err != nil {
		t.Fatal(err)
	}

	c := claimOne(t, e.ts.URL)
	if c.ID != started.ID {
		t.Fatalf("claimed %s, want %s", c.ID, started.ID)
	}
	if c.Prompt != "diagnose go: DATA from GitHub" {
		t.Errorf("the claimed prompt = %q, want the stored one", c.Prompt)
	}
	for _, unwanted := range []string{"incident_get", "Question:"} {
		if strings.Contains(c.Prompt, unwanted) {
			t.Errorf("the claimed prompt of a responder run contains %q:\n%s", unwanted, c.Prompt)
		}
	}
}

// A responder run's prompt holds the cleaned data from GitHub and can be large: the thread of an incident needs the question runs'
// prompts (they are the user's bubbles) but only the status of the responder runs, so the list leaves their text out. The run page
// reads the whole run.
func TestTheRunsOfAnIncidentCarryNoResponderPromptOrOutput(t *testing.T) {
	e, c, in := questionEnv(t, server.Cluster{})
	ctx := context.Background()
	started, err := e.store.StartDiagnosis(ctx, store.StartParams{
		IncidentID: in.ID, Provider: "claude", Prompt: "diagnose: DATA from GitHub", HeadSHA: in.HeadSHA,
		Limits: store.DefaultLimits(), Now: time.Now(),
	}, store.NewActivity{Kind: store.KindDiagnosisStarted, RepoID: in.RepoID, Summary: "started"})
	if err != nil {
		t.Fatal(err)
	}
	q, err := e.store.CreateQuestionRun(ctx, "claude", "why?", in.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(in.ID, 10)

	got := decode[[]run.Run](t, e.do(t, c, http.MethodGet, "/api/runs?incident="+id, "", false))
	if len(got) != 2 {
		t.Fatalf("runs = %+v, want the question and the responder run", got)
	}
	for _, r := range got {
		switch r.ID {
		case q.ID:
			if r.Prompt != "why?" {
				t.Errorf("question prompt = %q, want it kept", r.Prompt)
			}
		case started.ID:
			if r.Prompt != "" || r.Result != "" || len(r.Output) != 0 {
				t.Errorf("responder run carries text: prompt %q result %q output %s", r.Prompt, r.Result, r.Output)
			}
			if r.Role != run.RoleResponder || r.Status != started.Status {
				t.Errorf("responder run = %+v, want its role and status kept", r)
			}
		default:
			t.Errorf("unexpected run %s", r.ID)
		}
	}
	whole := decode[run.Run](t, e.do(t, c, http.MethodGet, "/api/runs/"+started.ID, "", false))
	if whole.Prompt != "diagnose: DATA from GitHub" {
		t.Errorf("the run itself has prompt %q, want the stored one", whole.Prompt)
	}
}
