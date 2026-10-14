package server_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/sshhost/sshtest"
)

type runJSON struct {
	ID        string `json:"id"`
	MCP       bool   `json:"mcp"`
	HostCount int    `json:"hostCount"`
	Hosts     []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"hosts"`
}

func newRunHostEnv(t *testing.T) *hostEnv {
	t.Helper()
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: nasCommands()})
	return newHostEnv(t, nil, "", ghKey(t, 1), srv, func(d *server.Deps) {
		d.Gatekeeper = gatekeeper.New(gatekeeper.Config{Store: d.Store})
	})
}

// usableHost makes a host with a login and a pinned key.
func usableHost(t *testing.T, e *hostEnv, name string) hostJSON {
	t.Helper()
	h := e.create(t, name)
	e.setPassword(t, h.ID, "pw")
	e.pin(t, h.ID, e.test(t, h.ID).PresentedKey.Fingerprint)
	return h
}

func (e *hostEnv) startRun(t *testing.T, body string) (int, string) {
	t.Helper()
	return e.do(t, http.MethodPost, "/api/runs", body)
}

func TestARunIsStartedWithUsableHostsAndSaysWhichOnes(t *testing.T) {
	e := newRunHostEnv(t)
	nas := usableHost(t, e, "nas")

	code, body := e.startRun(t, fmt.Sprintf(`{"prompt":"look at the nas","tools":true,"hosts":[%d]}`, nas.ID))
	run := decodeBody[runJSON](t, body)
	if code != http.StatusCreated || !run.MCP || run.HostCount != 1 {
		t.Fatalf("POST /api/runs = %d %s", code, body)
	}
	_, detail := e.do(t, http.MethodGet, "/api/runs/"+run.ID, "")
	got := decodeBody[runJSON](t, detail)
	if len(got.Hosts) != 1 || got.Hosts[0].Name != "nas" || got.Hosts[0].ID != nas.ID {
		t.Fatalf("GET /api/runs/{id} = %s", detail)
	}
	if strings.Contains(detail, "127.0.0.1") {
		t.Fatalf("the run shows an address: %s", detail)
	}
	_, list := e.do(t, http.MethodGet, "/api/runs", "")
	if !strings.Contains(list, `"hostCount":1`) || strings.Contains(list, `"hosts":[`) {
		t.Fatalf("the list has the count and not the names: %s", list)
	}
}

func TestARunIsRefusedForHostsThatCannotBeUsed(t *testing.T) {
	e := newRunHostEnv(t)
	ready := usableHost(t, e, "ready")
	bare := e.create(t, "bare") // no login, no pinned key
	noPin := e.create(t, "nopin")
	e.setPassword(t, noPin.ID, "pw")
	off := usableHost(t, e, "off")
	e.do(t, http.MethodPatch, fmt.Sprintf("/api/hosts/%d", off.ID), `{"enabled":false}`)

	for name, c := range map[string]struct {
		body string
		code int
		want string
	}{
		"hosts without the gatekeeper tools": {fmt.Sprintf(`{"prompt":"p","hosts":[%d]}`, ready.ID), http.StatusBadRequest, "gatekeeper tools"},
		"a host without a login":             {fmt.Sprintf(`{"prompt":"p","tools":true,"hosts":[%d]}`, bare.ID), http.StatusConflict, "no login"},
		"a host without a pinned key":        {fmt.Sprintf(`{"prompt":"p","tools":true,"hosts":[%d]}`, noPin.ID), http.StatusConflict, "not pinned"},
		"a disabled host":                    {fmt.Sprintf(`{"prompt":"p","tools":true,"hosts":[%d]}`, off.ID), http.StatusConflict, "disabled"},
		"a host that does not exist":         {`{"prompt":"p","tools":true,"hosts":[999]}`, http.StatusConflict, "does not exist"},
		"one good and one bad host":          {fmt.Sprintf(`{"prompt":"p","tools":true,"hosts":[%d,%d]}`, ready.ID, bare.ID), http.StatusConflict, "bare"},
		"more than eight hosts":              {`{"prompt":"p","tools":true,"hosts":[1,2,3,4,5,6,7,8,9]}`, http.StatusBadRequest, "at most 8"},
	} {
		code, body := e.startRun(t, c.body)
		if code != c.code || !strings.Contains(body, c.want) {
			t.Errorf("%s: %d %s, want %d with %q", name, code, body, c.code, c.want)
		}
	}
	if _, list := e.do(t, http.MethodGet, "/api/runs", ""); strings.Contains(list, `"id"`) {
		t.Fatalf("a refused request created a run: %s", list)
	}
}

func TestTheRunSwitchesStillBehaveAsBefore(t *testing.T) {
	e := newRunHostEnv(t)
	for name, c := range map[string]struct {
		body string
		code int
	}{
		"a plain run":                          {`{"prompt":"p"}`, http.StatusCreated},
		"a run with tools":                     {`{"prompt":"p","tools":true}`, http.StatusCreated},
		"cluster tools need the gatekeeper":    {`{"prompt":"p","cluster":true}`, http.StatusBadRequest},
		"a question needs the tools":           {`{"prompt":"p","incidentId":1}`, http.StatusBadRequest},
		"a question about an unknown incident": {`{"prompt":"p","tools":true,"incidentId":999}`, http.StatusNotFound},
		"a question about incident 0":          {`{"prompt":"p","tools":true,"incidentId":0}`, http.StatusNotFound},
	} {
		if code, body := e.startRun(t, c.body); code != c.code {
			t.Errorf("%s: %d %s, want %d", name, code, body, c.code)
		}
	}
}
