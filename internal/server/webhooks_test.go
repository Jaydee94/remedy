package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/signals"
	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/internal/webhook"
)

type webhookJSON struct {
	ID                  int64   `json:"id"`
	Name                string  `json:"name"`
	Enabled             bool    `json:"enabled"`
	AutoDiagnose        bool    `json:"autoDiagnose"`
	ResolveAfterSeconds int     `json:"resolveAfterSeconds"`
	RatePerMinute       int     `json:"ratePerMinute"`
	NewPerHour          int     `json:"newPerHour"`
	LastUsedAt          *string `json:"lastUsedAt"`
	PreviousValidUntil  *string `json:"previousValidUntil"`
	Accepted            int     `json:"accepted"`
	Rejected            int     `json:"rejected"`
	LastError           string  `json:"lastError"`
}

type webhookList struct {
	Listening bool          `json:"listening"`
	Endpoints []webhookJSON `json:"endpoints"`
}

type webhookCreated struct {
	Endpoint webhookJSON `json:"endpoint"`
	Token    string      `json:"token"`
}

func newWebhookEnv(t *testing.T, listening bool) (*hostEnv, *webhook.Limits) {
	t.Helper()
	limits := webhook.NewLimits()
	e := newHostEnv(t, nil, "", secret.Key{}, nil, func(d *server.Deps) { d.WebhookLimits, d.WebhookListening = limits, listening })
	return e, limits
}

func TestTheWebhookAdminAPIWantsASessionAndTheCSRFHeader(t *testing.T) {
	e, _ := newWebhookEnv(t, true)
	anon := httptest.NewRecorder()
	_ = anon
	req, _ := http.NewRequest(http.MethodGet, e.ts.URL+"/api/webhooks", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET without a session: %v %v", resp, err)
	}
	req, _ = http.NewRequest(http.MethodPost, e.ts.URL+"/api/webhooks", strings.NewReader(`{"name":"x"}`))
	resp, err = e.client.Do(req) // a session, but no CSRF header
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST without the CSRF header: %v %v", resp, err)
	}
}

func TestCreateShowsTheTokenOnceAndItAuthenticates(t *testing.T) {
	e, _ := newWebhookEnv(t, true)
	code, body := e.do(t, http.MethodPost, "/api/webhooks", `{"name":"grafana"}`)
	if code != http.StatusCreated {
		t.Fatalf("create = %d %s", code, body)
	}
	created := decodeBody[webhookCreated](t, body)
	if created.Token == "" || created.Endpoint.Name != "grafana" || !created.Endpoint.Enabled || created.Endpoint.AutoDiagnose ||
		created.Endpoint.ResolveAfterSeconds != 86400 || created.Endpoint.RatePerMinute != 60 || created.Endpoint.NewPerHour != 30 {
		t.Fatalf("created = %+v", created)
	}
	h := &webhook.Handler{Store: e.store, Engine: &incident.Engine{Store: e.store}, Limits: webhook.NewLimits(), Auth: &webhook.Authenticator{Store: e.store},
		Hosts: func(context.Context) ([]signals.HostRules, error) { return nil, nil }}
	mux := http.NewServeMux()
	mux.Handle("POST /webhook/v1/{endpoint}", h)
	req := httptest.NewRequest(http.MethodPost, "/webhook/v1/grafana", strings.NewReader(`{"version":1,"key":"k","status":"firing","title":"t"}`))
	req.Header.Set("Authorization", "Bearer "+created.Token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("the new token did not authenticate: %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"/api/webhooks"} {
		if code, body := e.do(t, http.MethodGet, path, ""); code != http.StatusOK || strings.Contains(body, created.Token) {
			t.Fatalf("GET %s = %d, and it repeats the token: %v", path, code, strings.Contains(body, created.Token))
		}
	}
	list := decodeBody[webhookList](t, mustGet(t, e, "/api/webhooks"))
	if !list.Listening || len(list.Endpoints) != 1 || list.Endpoints[0].LastUsedAt == nil {
		t.Fatalf("list = %+v", list)
	}
}

func mustGet(t *testing.T, e *hostEnv, path string) string {
	t.Helper()
	code, body := e.do(t, http.MethodGet, path, "")
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", path, code, body)
	}
	return body
}

func TestTheListSaysWhenNoListenerExistsAndShowsTheCounters(t *testing.T) {
	e, limits := newWebhookEnv(t, false)
	code, body := e.do(t, http.MethodPost, "/api/webhooks", `{"name":"grafana"}`)
	if code != http.StatusCreated {
		t.Fatal(body)
	}
	id := decodeBody[webhookCreated](t, body).Endpoint.ID
	limits.Record(id, true, "", time.Now())
	limits.Record(id, false, "severity: must be critical, warning, info or none", time.Now())
	list := decodeBody[webhookList](t, mustGet(t, e, "/api/webhooks"))
	if list.Listening || list.Endpoints[0].Accepted != 1 || list.Endpoints[0].Rejected != 1 || !strings.HasPrefix(list.Endpoints[0].LastError, "severity:") {
		t.Fatalf("list = %+v", list)
	}
}

func TestRotateGivesANewTokenAndTheOldOneStaysForADay(t *testing.T) {
	e, _ := newWebhookEnv(t, true)
	_, body := e.do(t, http.MethodPost, "/api/webhooks", `{"name":"grafana"}`)
	created := decodeBody[webhookCreated](t, body)
	code, body := e.do(t, http.MethodPost, "/api/webhooks/"+itoa(created.Endpoint.ID)+"/rotate", "")
	if code != http.StatusOK {
		t.Fatalf("rotate = %d %s", code, body)
	}
	rotated := decodeBody[webhookCreated](t, body)
	if rotated.Token == "" || rotated.Token == created.Token || rotated.Endpoint.PreviousValidUntil == nil {
		t.Fatalf("rotated = %+v", rotated)
	}
	until, err := time.Parse(time.RFC3339Nano, *rotated.Endpoint.PreviousValidUntil)
	if err != nil || until.Sub(time.Now()) < 23*time.Hour || until.Sub(time.Now()) > 25*time.Hour {
		t.Fatalf("previousValidUntil = %v, %v", until, err)
	}
	if code, _ := e.do(t, http.MethodPost, "/api/webhooks/999/rotate", ""); code != http.StatusNotFound {
		t.Errorf("rotating an unknown endpoint = %d", code)
	}
}

func TestPatchChangesTheFieldsAndRefusesBadOnes(t *testing.T) {
	e, _ := newWebhookEnv(t, true)
	_, body := e.do(t, http.MethodPost, "/api/webhooks", `{"name":"grafana"}`)
	id := itoa(decodeBody[webhookCreated](t, body).Endpoint.ID)
	code, body := e.do(t, http.MethodPatch, "/api/webhooks/"+id, `{"autoDiagnose":true,"resolveAfterSeconds":3600,"enabled":false}`)
	got := decodeBody[webhookJSON](t, body)
	if code != http.StatusOK || !got.AutoDiagnose || got.Enabled || got.ResolveAfterSeconds != 3600 || got.RatePerMinute != 60 {
		t.Fatalf("patch = %d %+v", code, got)
	}
	for _, bad := range []string{`{"resolveAfterSeconds":60}`, `{"ratePerMinute":0}`, `{"newPerHour":5000}`, `{"name":"other"}`, `{"nope":1}`, `not json`} {
		if code, _ := e.do(t, http.MethodPatch, "/api/webhooks/"+id, bad); code != http.StatusBadRequest {
			t.Errorf("patch %s = %d, want 400", bad, code)
		}
	}
	if code, _ := e.do(t, http.MethodPatch, "/api/webhooks/999", `{"enabled":true}`); code != http.StatusNotFound {
		t.Errorf("patching an unknown endpoint = %d", code)
	}
}

func TestCreateRefusesABadNameADuplicateAndUnknownMembers(t *testing.T) {
	e, _ := newWebhookEnv(t, true)
	for _, bad := range []string{`{"name":"Has Space"}`, `{"name":""}`, `{}`, `{"name":"a","token":"mine"}`, `{"name":"a","resolveAfterSeconds":10}`} {
		if code, _ := e.do(t, http.MethodPost, "/api/webhooks", bad); code != http.StatusBadRequest {
			t.Errorf("create %s = %d, want 400", bad, code)
		}
	}
	if code, _ := e.do(t, http.MethodPost, "/api/webhooks", `{"name":"grafana"}`); code != http.StatusCreated {
		t.Fatal("create")
	}
	if code, _ := e.do(t, http.MethodPost, "/api/webhooks", `{"name":"GRAFANA"}`); code == http.StatusCreated {
		t.Error("a name in another case was accepted") // upper case is refused by the rule, so this is a 400, never a 201
	}
	if code, _ := e.do(t, http.MethodPost, "/api/webhooks", `{"name":"grafana"}`); code != http.StatusConflict {
		t.Errorf("a duplicate = %d, want 409", code)
	}
}

func TestDeleteRemovesTheEndpointAndNoAnswerHoldsATokenOrAHash(t *testing.T) {
	e, _ := newWebhookEnv(t, true)
	_, body := e.do(t, http.MethodPost, "/api/webhooks", `{"name":"grafana"}`)
	created := decodeBody[webhookCreated](t, body)
	id := itoa(created.Endpoint.ID)
	e.do(t, http.MethodPost, "/api/webhooks/"+id+"/rotate", "")
	e.do(t, http.MethodPatch, "/api/webhooks/"+id, `{"enabled":true}`)
	mustGet(t, e, "/api/webhooks")
	// every answer after the creating one: no token, no hash
	for _, seen := range e.seen[2:] {
		if strings.Contains(seen, created.Token) || strings.Contains(strings.ToLower(seen), "hash") {
			t.Errorf("an answer holds a token or a hash: %s", seen)
		}
	}
	if code, _ := e.do(t, http.MethodDelete, "/api/webhooks/"+id, ""); code != http.StatusNoContent {
		t.Fatalf("delete = %d", code)
	}
	if list := decodeBody[webhookList](t, mustGet(t, e, "/api/webhooks")); len(list.Endpoints) != 0 {
		t.Fatalf("list after delete = %+v", list)
	}
	if code, _ := e.do(t, http.MethodDelete, "/api/webhooks/"+id, ""); code != http.StatusNotFound {
		t.Errorf("deleting twice = %d", code)
	}
	if _, err := e.store.WebhookByName(context.Background(), "grafana"); err == nil {
		t.Error("the endpoint is still in the store")
	}
	_ = store.ErrNotFound
}
