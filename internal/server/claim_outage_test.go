package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/Jaydee94/remedy/internal/diagnosis"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

const outageAnswer = `{"summary":"The pod web keeps crashing.","cause":"It runs out of memory.","confidence":"high","category":"resource_pressure","affected":["deployment/demo/web"],"proposed_action":"Raise the memory limit.","evidence":"cluster_describe showed OOMKilled."}`

func (e *respEnv) alert(t *testing.T) store.Incident {
	t.Helper()
	in, err := e.st.OpenIncident(context.Background(), store.NewIncident{
		Source: store.SourceAlertmanager, Key: "A/1", Title: "Pod web-1 crashes", Severity: "critical", AutoDiagnose: true,
		Conclusion: "firing", Details: json.RawMessage(`{"labels":{"alertname":"A"}}`),
	}, store.NewActivity{Kind: store.KindIncidentOpened, Summary: "opened"})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

type claimView struct {
	ID       string          `json:"id"`
	Schema   json.RawMessage `json:"schema"`
	Snapshot bool            `json:"snapshot"`
	MCPToken string          `json:"mcp_token"`
}

// claim starts the diagnosis of an incident through the API and claims the run like a runner does.
func (e *respEnv) claim(t *testing.T, in store.Incident) claimView {
	t.Helper()
	code, body := e.admin(t, http.MethodPost, "/api/incidents/"+strconv.FormatInt(in.ID, 10)+"/diagnose", "")
	if code != http.StatusAccepted {
		t.Fatalf("diagnose = %d %s", code, body)
	}
	resp, raw := e.runner(t, http.MethodPost, "/runner/v1/claim", "", runnerToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d %s", resp.StatusCode, raw)
	}
	var c claimView
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTheClaimOfAnAlertRunCarriesTheOutageSchemaAndNoSnapshot(t *testing.T) {
	e := newRespEnv(t, goodKey(t))
	c := e.claim(t, e.alert(t))
	if string(c.Schema) != diagnosis.OutageSchema || c.Snapshot || c.MCPToken == "" {
		t.Fatalf("claim = schema %s, snapshot %v, token set %v", c.Schema, c.Snapshot, c.MCPToken != "")
	}
	if code := e.finish(t, c.ID, `{"exitCode":0,"result":"ok","output":`+outageAnswer+`}`); code != http.StatusNoContent {
		t.Fatalf("finish = %d", code)
	}
	if rn, _ := e.st.GetRun(context.Background(), c.ID); rn.Status != run.Succeeded {
		t.Fatalf("run = %+v", rn)
	}
}

func TestAGitHubShapedAnswerToAnAlertRunFails(t *testing.T) {
	e := newRespEnv(t, goodKey(t))
	c := e.claim(t, e.alert(t))
	if code := e.finish(t, c.ID, `{"exitCode":0,"result":"ok","output":`+goodDiagnosis+`}`); code != http.StatusNoContent {
		t.Fatalf("finish = %d", code)
	}
	rn, _ := e.st.GetRun(context.Background(), c.ID)
	if rn.Status != run.Failed || rn.FailureReason != run.ReasonInvalidOutput {
		t.Fatalf("run = %+v", rn)
	}
}

func TestTheClaimOfAGitHubRunStillCarriesTheDiagnosisSchemaAndTheSnapshot(t *testing.T) {
	e := newRespEnv(t, goodKey(t))
	c := e.claim(t, e.incident(t, "pr:20", "failure"))
	if string(c.Schema) != diagnosis.Schema || !c.Snapshot {
		t.Fatalf("claim = schema %s, snapshot %v", c.Schema, c.Snapshot)
	}
	// An outage-shaped answer to a GitHub run is invalid.
	if code := e.finish(t, c.ID, `{"exitCode":0,"result":"ok","output":`+outageAnswer+`}`); code != http.StatusNoContent {
		t.Fatalf("finish = %d", code)
	}
	rn, _ := e.st.GetRun(context.Background(), c.ID)
	if rn.Status != run.Failed || rn.FailureReason != run.ReasonInvalidOutput {
		t.Fatalf("run = %+v", rn)
	}
}
