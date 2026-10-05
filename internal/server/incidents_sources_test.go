package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

type sourceJSON struct {
	ID           int64          `json:"id"`
	Source       string         `json:"source"`
	Title        string         `json:"title"`
	Severity     string         `json:"severity"`
	AutoDiagnose bool           `json:"autoDiagnose"`
	Details      map[string]any `json:"details"`
	RepoID       int64          `json:"repoId"`
	Repo         string         `json:"repo"`
	CheckName    string         `json:"checkName"`
	State        string         `json:"state"`
	Conclusion   string         `json:"conclusion"`
}

func openAlert(t *testing.T, e *ghEnv, key, title string) store.Incident {
	t.Helper()
	in, err := e.store.OpenIncident(context.Background(), store.NewIncident{
		Source: store.SourceAlertmanager, Key: key, Title: title, Severity: "critical", AutoDiagnose: true, Conclusion: "firing",
		Details: json.RawMessage(`{"labels":{"namespace":"demo","alertname":"KubePodCrashLooping"}}`),
	}, store.NewActivity{Kind: store.KindIncidentOpened, Summary: "Incident opened: alert " + title})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func listBySource(t *testing.T, e *ghEnv, query string) []sourceJSON {
	t.Helper()
	code, body := e.call(t, http.MethodGet, "/api/incidents"+query, "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/incidents%s = %d %s", query, code, body)
	}
	var list []sourceJSON
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	return list
}

func TestIncidentsOfEverySourceAreListedAndCanBeFilteredBySource(t *testing.T) {
	e, repoID := withRepo(t)
	gh := openIncident(t, e, repoID, "pr:7", "go")
	alert := openAlert(t, e, "KubePodCrashLooping/abc", "KubePodCrashLooping demo/web")

	all := listBySource(t, e, "")
	if len(all) != 2 || all[0].ID != alert.ID || all[1].ID != gh.ID {
		t.Fatalf("all = %+v, want the alert first (newest)", all)
	}
	a, g := all[0], all[1]
	if a.Source != "alertmanager" || a.Title != "KubePodCrashLooping demo/web" || a.Severity != "critical" || !a.AutoDiagnose ||
		a.RepoID != 0 || a.Repo != "" || a.CheckName != "" || a.State != "open" || a.Conclusion != "firing" {
		t.Fatalf("the alert = %+v", a)
	}
	labels, _ := a.Details["labels"].(map[string]any)
	if labels["namespace"] != "demo" {
		t.Fatalf("the details of the alert = %+v", a.Details)
	}
	if g.Source != "github" || g.Title != "go" || g.Severity != "none" || !g.AutoDiagnose || g.RepoID != repoID || g.CheckName != "go" {
		t.Fatalf("the GitHub incident = %+v", g)
	}

	for query, want := range map[string]int{
		"?source=github":                                             1,
		"?source=alertmanager":                                       1,
		"?source=argocd":                                             0,
		"?source=alertmanager&state=resolved":                        0,
		"?source=github&repo=" + strconv.FormatInt(repoID, 10):       1,
		"?source=alertmanager&repo=" + strconv.FormatInt(repoID, 10): 0,
	} {
		if got := listBySource(t, e, query); len(got) != want {
			t.Errorf("%s: %d incidents, want %d", query, len(got), want)
		}
	}
	for _, bad := range []string{"?source=loki", "?source=", "?source=github%27%20OR%201=1"} {
		code, _ := e.call(t, http.MethodGet, "/api/incidents"+bad, "")
		want := http.StatusBadRequest
		if bad == "?source=" {
			want = http.StatusOK // an empty value means every source, like no value
		}
		if code != want {
			t.Errorf("%s = %d, want %d", bad, code, want)
		}
	}
}

func TestAnIncidentOfAnotherSourceHasItsHistoryAndCanBeIgnored(t *testing.T) {
	e, _ := withRepo(t)
	alert := openAlert(t, e, "NodeDown/1", "NodeDown n1")
	id := strconv.FormatInt(alert.ID, 10)

	code, body := e.call(t, http.MethodGet, "/api/incidents/"+id, "")
	if code != http.StatusOK || !strings.Contains(body, `"source":"alertmanager"`) || !strings.Contains(body, "Incident opened: alert NodeDown n1") {
		t.Fatalf("GET = %d %s", code, body)
	}

	code, body = e.call(t, http.MethodPost, "/api/incidents/"+id+"/ignore", "")
	if code != http.StatusOK || !strings.Contains(body, `"state":"ignored"`) {
		t.Fatalf("ignore = %d %s", code, body)
	}
	log, _ := e.store.ListActivity(context.Background(), store.ActivityQuery{IncidentID: alert.ID})
	if len(log) != 2 || log[0].Summary != "Ignored the incident for alert NodeDown n1" {
		t.Fatalf("activity = %+v", log)
	}
}

func TestAnIncidentOfAnotherSourceCannotBeDiagnosedYet(t *testing.T) {
	e := newRespEnv(t, goodKey(t))
	alert, err := e.st.OpenIncident(context.Background(), store.NewIncident{
		Source: store.SourceAlertmanager, Key: "NodeDown/1", Title: "NodeDown n1", AutoDiagnose: true, Conclusion: "firing",
	}, store.NewActivity{Kind: store.KindIncidentOpened, Summary: "opened"})
	if err != nil {
		t.Fatal(err)
	}
	code, body := e.diagnose(t, alert)
	if code != http.StatusConflict || !strings.Contains(body, "not available yet") {
		t.Fatalf("diagnose = %d %s, want 409 saying it is not available yet", code, body)
	}
}
