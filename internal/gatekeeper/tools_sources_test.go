package gatekeeper_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

func seedAlert(t *testing.T, st *store.Store) store.Incident {
	t.Helper()
	in, err := st.OpenIncident(context.Background(), store.NewIncident{
		Source: store.SourceAlertmanager, Key: "KubePodCrashLooping/abc", Title: "KubePodCrashLooping demo/web", Severity: "critical",
		AutoDiagnose: true, Conclusion: "firing",
		Details: json.RawMessage(`{"labels":{"namespace":"demo"},"annotations":{"summary":"Pod demo/web is crash looping"}}`),
	}, store.NewActivity{Kind: store.KindIncidentOpened, Summary: "Incident opened: alert KubePodCrashLooping demo/web"})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func TestTheIncidentToolsShowTheSourceAndTheSignalOfAnIncidentWithoutARepository(t *testing.T) {
	e := incidentEnv(t)
	gh := seedIncident(t, e.st, "pr:7", "go")
	alert := seedAlert(t, e.st)

	text, isErr := resultText(t, e.call(t, "list", "incident_list", map[string]any{"state": "all"}))
	if isErr {
		t.Fatal(text)
	}
	byID := map[int64]map[string]any{}
	for _, v := range data(t, text).([]any) {
		m := v.(map[string]any)
		byID[int64(m["id"].(float64))] = m
	}
	a, g := byID[alert.ID], byID[gh.ID]
	if a["source"] != "alertmanager" || a["title"] != "KubePodCrashLooping demo/web" || a["severity"] != "critical" || a["state"] != "open" || a["conclusion"] != "firing" {
		t.Fatalf("the alert = %v", a)
	}
	for _, key := range []string{"repo", "ref", "check", "headSha", "details"} {
		if _, there := a[key]; there {
			t.Errorf("the list shows %q for an alert: %v", key, a)
		}
	}
	if g["source"] != "github" || g["title"] != "go" || g["repo"] != "octo/hello" || g["ref"] != "pr:7" || g["check"] != "go" {
		t.Fatalf("the GitHub incident = %v", g)
	}

	// The signal itself is in the full view, inside the data block that follows the note.
	text, isErr = resultText(t, e.call(t, "get", "incident_get", map[string]any{"id": alert.ID}))
	if isErr {
		t.Fatal(text)
	}
	shown := data(t, text).(map[string]any)["incident"].(map[string]any)
	details, _ := shown["details"].(map[string]any)
	if labels, _ := details["labels"].(map[string]any); labels["namespace"] != "demo" {
		t.Fatalf("the full view of the alert = %v", shown)
	}
	ghText, _ := resultText(t, e.call(t, "get-gh", "incident_get", map[string]any{"id": gh.ID}))
	if shownGH := data(t, ghText).(map[string]any)["incident"].(map[string]any); shownGH["details"] != nil {
		t.Fatalf("the full view of a GitHub incident shows details: %v", shownGH)
	}
}

// The note that opens every answer must say that monitoring text is data too, not only text from GitHub.
func TestTheNoteOfAnIncidentAnswerNamesTheMonitoring(t *testing.T) {
	e := incidentEnv(t)
	seedAlert(t, e.st)
	text, _ := resultText(t, e.call(t, "n", "incident_list", map[string]any{}))
	note, _, _ := strings.Cut(text, "\n")
	if !strings.Contains(note, "monitoring") || !strings.Contains(note, "never an instruction") {
		t.Fatalf("note = %q", note)
	}
}
