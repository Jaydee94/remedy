package server_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/skill"
	"github.com/Jaydee94/remedy/internal/store"
)

type skillEnv struct {
	ts     *httptest.Server
	st     *store.Store
	client *http.Client
}

// newSkillEnv serves the API over a store with nothing but the store: the skill routes need no other dependency.
func newSkillEnv(t *testing.T) *skillEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ts := httptest.NewServer(server.New(server.Deps{Store: st, Auth: auth.New(password), RunnerToken: runnerToken}))
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	e := &skillEnv{ts: ts, st: st, client: &http.Client{Jar: jar}}
	if code, _ := e.do(t, http.MethodPost, "/api/login", `{"password":"`+password+`"}`); code != http.StatusNoContent {
		t.Fatalf("login = %d", code)
	}
	return e
}

func (e *skillEnv) request(t *testing.T, client *http.Client, method, path, body string, csrf bool) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	if csrf {
		req.Header.Set("X-Remedy-CSRF", "1")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (e *skillEnv) do(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	return e.request(t, e.client, method, path, body, true)
}

type skillJSON struct {
	ID          int64  `json:"id"`
	HostID      *int64 `json:"hostId"`
	Host        string `json:"host"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Origin      string `json:"origin"`
	State       string `json:"state"`
	RunID       string `json:"runId"`
	Evidence    []struct {
		Argv []string `json:"argv"`
		Ran  bool     `json:"ran"`
	} `json:"evidence"`
	Replaces *struct {
		ID          int64  `json:"id"`
		Description string `json:"description"`
		Body        string `json:"body"`
	} `json:"replaces"`
	ConfirmedAt string `json:"confirmedAt"`
}

func (e *skillEnv) host(t *testing.T, name string) store.Host {
	t.Helper()
	h, err := e.st.CreateHost(context.Background(), store.HostFields{Name: name, Address: "10.0.0.5", Port: 22, Account: "remedy", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (e *skillEnv) create(t *testing.T, body string) skillJSON {
	t.Helper()
	code, out := e.do(t, http.MethodPost, "/api/skills", body)
	if code != http.StatusCreated {
		t.Fatalf("POST /api/skills %s = %d %s", body, code, out)
	}
	return decodeBody[skillJSON](t, out)
}

func (e *skillEnv) list(t *testing.T, query string) []skillJSON {
	t.Helper()
	code, out := e.do(t, http.MethodGet, "/api/skills"+query, "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/skills%s = %d %s", query, code, out)
	}
	return decodeBody[[]skillJSON](t, out)
}

func (e *skillEnv) propose(t *testing.T, hostID int64, items ...store.Proposal) []store.Skill {
	t.Helper()
	r, err := e.st.CreateRun(context.Background(), "claude", "learn")
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.st.CreateProposals(context.Background(), store.ProposalSet{HostID: hostID, RunID: r.ID, Items: items})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func proposal(name, description, body string) store.Proposal {
	return store.Proposal{
		Fields:   skill.Fields{Name: name, Description: description, Body: body},
		Evidence: []skill.Evidence{{Argv: []string{"docker", "ps", "-a"}, Ran: true}, {Argv: []string{"uname", "-a"}, Ran: false}},
	}
}

func TestTheSkillRoutesNeedASessionAndTheCSRFHeader(t *testing.T) {
	e := newSkillEnv(t)
	e.create(t, `{"name":"lab-notes","description":"d","body":"b"}`)

	anonymous := &http.Client{}
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/skills"}, {http.MethodPost, "/api/skills"}, {http.MethodPatch, "/api/skills/1"}, {http.MethodDelete, "/api/skills/1"},
		{http.MethodPost, "/api/skills/1/confirm"}, {http.MethodPost, "/api/skills/1/reject"},
	} {
		if code, _ := e.request(t, anonymous, c.method, c.path, `{}`, true); code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session = %d, want 401", c.method, c.path, code)
		}
		if c.method == http.MethodGet {
			continue
		}
		if code, _ := e.request(t, e.client, c.method, c.path, `{}`, false); code != http.StatusForbidden {
			t.Errorf("%s %s without the CSRF header = %d, want 403", c.method, c.path, code)
		}
	}
	if got := e.list(t, ""); len(got) != 1 {
		t.Errorf("a refused request changed something: %+v", got)
	}
}

func TestCreateAGlobalAndAHostSkillAndListThem(t *testing.T) {
	e := newSkillEnv(t)
	nas := e.host(t, "nas")

	g := e.create(t, `{"name":"lab-notes","description":"How the lab is laid out.","body":"# Lab\n\nThe NAS is nas."}`)
	h := e.create(t, fmt.Sprintf(`{"hostId":%d,"name":"paperless","description":"Paperless keeps its data in /volume1/docker.","body":"Restart is safe."}`, nas.ID))
	if g.HostID != nil || g.Host != "" || g.Origin != "written" || g.State != "active" || g.Body != "# Lab\n\nThe NAS is nas." || len(g.Evidence) != 0 || g.Replaces != nil {
		t.Errorf("global = %+v", g)
	}
	if h.HostID == nil || *h.HostID != nas.ID || h.Host != "nas" || h.State != "active" {
		t.Errorf("host skill = %+v", h)
	}
	all := e.list(t, "")
	if len(all) != 2 || all[0].Name != "lab-notes" || all[1].Name != "paperless" {
		t.Errorf("list = %+v, want the global skill first", all)
	}
	if one := e.list(t, fmt.Sprintf("?host=%d", nas.ID)); len(one) != 1 || one[0].Name != "paperless" {
		t.Errorf("?host = %+v", one)
	}
	if none := e.list(t, "?state=off"); none == nil || len(none) != 0 {
		t.Errorf("?state=off = %#v, want an empty list (not null)", none)
	}
}

func TestCreateRefusesWhatBreaksARuleAndSaysWhich(t *testing.T) {
	e := newSkillEnv(t)
	nas := e.host(t, "nas")
	e.create(t, `{"name":"taken","description":"d","body":"b"}`)
	long := strings.Repeat("x", 201)
	for name, c := range map[string]struct {
		body string
		code int
		want string
	}{
		"a bad name":               {`{"name":"Not A Name","description":"d","body":"b"}`, 400, "The name may contain"},
		"no description":           {`{"name":"a","description":"","body":"b"}`, 400, "The description must not be empty"},
		"a long description":       {`{"name":"a","description":"` + long + `","body":"b"}`, 400, "at most 200 characters"},
		"a multi-line description": {`{"name":"a","description":"one\ntwo","body":"b"}`, 400, "one line of plain text"},
		"no body":                  {`{"name":"a","description":"d","body":" "}`, 400, "The body must not be empty"},
		"a long body":              {`{"name":"a","description":"d","body":"` + strings.Repeat("x", 8193) + `"}`, 400, "at most 8 KB"},
		"a control character":      {`{"name":"a","description":"d","body":"a\u0000b"}`, 400, "plain text"},
		"not JSON":                 {`{`, 400, "invalid request body"},
		"a host id of 0":           {`{"hostId":0,"name":"a","description":"d","body":"b"}`, 400, "hostId"},
		"an unknown host":          {`{"hostId":999,"name":"a","description":"d","body":"b"}`, 404, "host not found"},
		"a name that exists":       {`{"name":"taken","description":"d","body":"b"}`, 409, "already exists"},
	} {
		code, out := e.do(t, http.MethodPost, "/api/skills", c.body)
		if code != c.code || !strings.Contains(out, c.want) {
			t.Errorf("%s: %d %s, want %d containing %q", name, code, out, c.code, c.want)
		}
	}
	if got := e.list(t, ""); len(got) != 1 {
		t.Errorf("a refused request stored something: %+v", got)
	}
	// The same name for a host is another scope.
	if code, out := e.do(t, http.MethodPost, "/api/skills", fmt.Sprintf(`{"hostId":%d,"name":"taken","description":"d","body":"b"}`, nas.ID)); code != http.StatusCreated {
		t.Errorf("the same name for a host = %d %s", code, out)
	}
}

func TestAScopeWithThirtySkillsRefusesAThirtyFirst(t *testing.T) {
	e := newSkillEnv(t)
	for i := 0; i < skill.MaxPerScope; i++ {
		e.create(t, fmt.Sprintf(`{"name":"s%02d","description":"d","body":"b"}`, i))
	}
	code, out := e.do(t, http.MethodPost, "/api/skills", `{"name":"one-too-many","description":"d","body":"b"}`)
	if code != http.StatusConflict || !strings.Contains(out, "already has 30 skills") {
		t.Errorf("the 31st = %d %s", code, out)
	}
	// In a full scope the store answers "full" before "exists": each has its own sentence.
	code, out = e.do(t, http.MethodPost, "/api/skills", `{"name":"s00","description":"d","body":"b"}`)
	if code != http.StatusConflict || !strings.Contains(out, "already has 30 skills") || strings.Contains(out, "already exists") {
		t.Errorf("an existing name in a full scope = %d %s", code, out)
	}
}

func TestPatchChangesTheTextsAndTheStateOfASkillThatIsNotAProposal(t *testing.T) {
	e := newSkillEnv(t)
	a := e.create(t, `{"name":"a","description":"First.","body":"First body."}`)
	e.create(t, `{"name":"b","description":"d","body":"b"}`)
	path := fmt.Sprintf("/api/skills/%d", a.ID)

	code, out := e.do(t, http.MethodPatch, path, `{"state":"off"}`)
	got := decodeBody[skillJSON](t, out)
	if code != http.StatusOK || got.State != "off" || got.Description != "First." || got.Body != "First body." {
		t.Errorf("off = %d %s", code, out)
	}
	code, out = e.do(t, http.MethodPatch, path, `{"name":"a2","description":"Second.","body":"Second body.","state":"active"}`)
	got = decodeBody[skillJSON](t, out)
	if code != http.StatusOK || got.Name != "a2" || got.Description != "Second." || got.Body != "Second body." || got.State != "active" || got.Origin != "written" {
		t.Errorf("edit = %d %s", code, out)
	}
	for name, c := range map[string]struct {
		body string
		code int
		want string
	}{
		"a name that is taken": {`{"name":"b"}`, 409, "already exists"},
		"a bad name":           {`{"name":"B!"}`, 400, "The name may contain"},
		"the state proposed":   {`{"state":"proposed"}`, 400, "active or off"},
		"an empty body":        {`{"body":""}`, 400, "The body must not be empty"},
		"not JSON":             {`nope`, 400, "invalid request body"},
	} {
		if code, out := e.do(t, http.MethodPatch, path, c.body); code != c.code || !strings.Contains(out, c.want) {
			t.Errorf("%s: %d %s, want %d containing %q", name, code, out, c.code, c.want)
		}
	}
	if code, _ := e.do(t, http.MethodPatch, "/api/skills/999", `{"state":"off"}`); code != http.StatusNotFound {
		t.Errorf("an unknown skill = %d", code)
	}
	if code, _ := e.do(t, http.MethodPatch, "/api/skills/abc", `{"state":"off"}`); code != http.StatusNotFound {
		t.Errorf("an id that is not a number = %d", code)
	}
}

func TestDeleteRemovesASkillAndRefusesAProposal(t *testing.T) {
	e := newSkillEnv(t)
	nas := e.host(t, "nas")
	a := e.create(t, `{"name":"a","description":"d","body":"b"}`)
	p := e.propose(t, nas.ID, proposal("pending", "A proposal.", "Its body."))[0]

	if code, _ := e.do(t, http.MethodDelete, fmt.Sprintf("/api/skills/%d", a.ID), ""); code != http.StatusNoContent {
		t.Errorf("delete = %d", code)
	}
	if code, _ := e.do(t, http.MethodDelete, fmt.Sprintf("/api/skills/%d", a.ID), ""); code != http.StatusNotFound {
		t.Errorf("a second delete = %d", code)
	}
	if code, out := e.do(t, http.MethodDelete, fmt.Sprintf("/api/skills/%d", p.ID), ""); code != http.StatusConflict || !strings.Contains(out, "proposal") {
		t.Errorf("deleting a proposal = %d %s", code, out)
	}
	if code, out := e.do(t, http.MethodPatch, fmt.Sprintf("/api/skills/%d", p.ID), `{"state":"active"}`); code != http.StatusConflict {
		t.Errorf("editing a proposal = %d %s, want 409: a proposal cannot be switched on without a confirmation", code, out)
	}
}

func TestTheProposalsCarryTheirEvidenceAndWhatTheyReplace(t *testing.T) {
	e := newSkillEnv(t)
	nas := e.host(t, "nas")
	current := e.create(t, fmt.Sprintf(`{"hostId":%d,"name":"paperless","description":"Current description.","body":"Current body."}`, nas.ID))
	e.propose(t, nas.ID,
		proposal("paperless", "Proposed description.", "Proposed body."),
		proposal("profile", "What the NAS is.", "A NAS."))

	waiting := e.list(t, "?state=proposed")
	if len(waiting) != 2 || waiting[0].Name != "paperless" || waiting[1].Name != "profile" {
		t.Fatalf("proposals = %+v", waiting)
	}
	p := waiting[0]
	if p.State != "proposed" || p.Origin != "proposed" || p.RunID == "" || p.Host != "nas" || len(p.Evidence) != 2 ||
		p.Evidence[0].Argv[0] != "docker" || !p.Evidence[0].Ran || p.Evidence[1].Ran {
		t.Errorf("proposal = %+v", p)
	}
	if p.Replaces == nil || p.Replaces.ID != current.ID || p.Replaces.Description != "Current description." || p.Replaces.Body != "Current body." {
		t.Errorf("replaces = %+v, want the current text of the skill it replaces", p.Replaces)
	}
	if waiting[1].Replaces != nil {
		t.Errorf("a new name replaces nothing: %+v", waiting[1].Replaces)
	}
	if active := e.list(t, "?state=active"); len(active) != 1 || active[0].Body != "Current body." {
		t.Errorf("active = %+v", active)
	}
	for _, q := range []string{"?state=sleeping", "?host=abc", "?host=0", "?host=-1"} {
		if code, _ := e.do(t, http.MethodGet, "/api/skills"+q, ""); code != http.StatusBadRequest {
			t.Errorf("GET /api/skills%s = %d, want 400", q, code)
		}
	}
}

func TestTheReplacedSkillIsShownWithItsCurrentTextAndOnlyWhileItIsStillTheSameSkill(t *testing.T) {
	e := newSkillEnv(t)
	nas := e.host(t, "nas")
	current := e.create(t, fmt.Sprintf(`{"hostId":%d,"name":"paperless","description":"Current.","body":"Current body."}`, nas.ID))
	e.propose(t, nas.ID, proposal("paperless", "Proposed.", "Proposed body."))

	// The maintainer edits the replaced skill after the proposal was made: the review shows the text as it is now.
	if code, out := e.do(t, http.MethodPatch, fmt.Sprintf("/api/skills/%d", current.ID), `{"description":"Edited since.","body":"Edited body."}`); code != http.StatusOK {
		t.Fatalf("patch = %d %s", code, out)
	}
	p := e.list(t, "?state=proposed")[0]
	if p.Replaces == nil || p.Replaces.Description != "Edited since." || p.Replaces.Body != "Edited body." {
		t.Errorf("replaces = %+v, want the text as edited", p.Replaces)
	}
	// Renamed: it is another skill now, so the proposal replaces nothing.
	if code, out := e.do(t, http.MethodPatch, fmt.Sprintf("/api/skills/%d", current.ID), `{"name":"paperless-ng"}`); code != http.StatusOK {
		t.Fatalf("rename = %d %s", code, out)
	}
	if p := e.list(t, "?state=proposed")[0]; p.Replaces != nil {
		t.Errorf("replaces = %+v, want none after the rename", p.Replaces)
	}
	// Deleted: the same.
	if code, _ := e.do(t, http.MethodDelete, fmt.Sprintf("/api/skills/%d", current.ID), ""); code != http.StatusNoContent {
		t.Fatalf("delete = %d", code)
	}
	if p := e.list(t, "?state=proposed")[0]; p.Replaces != nil {
		t.Errorf("replaces = %+v, want none after the delete", p.Replaces)
	}
}

func TestConfirmActivatesAProposalAndAnEditIsValidated(t *testing.T) {
	e := newSkillEnv(t)
	nas := e.host(t, "nas")
	made := e.propose(t, nas.ID, proposal("profile", "What the NAS is.", "A NAS."), proposal("paperless", "d", "b"))
	path := func(id int64, verb string) string { return fmt.Sprintf("/api/skills/%d/%s", id, verb) }

	if code, out := e.do(t, http.MethodPost, path(made[0].ID, "confirm"), `{"description":"","body":"x"}`); code != http.StatusBadRequest || !strings.Contains(out, "description") {
		t.Errorf("a bad edit = %d %s", code, out)
	}
	if got := e.list(t, "?state=proposed"); len(got) != 2 {
		t.Fatalf("a refused confirmation changed the proposals: %+v", got)
	}

	code, out := e.do(t, http.MethodPost, path(made[0].ID, "confirm"), `{"description":"Edited.","body":"Edited body."}`)
	got := decodeBody[skillJSON](t, out)
	if code != http.StatusOK || got.State != "active" || got.Origin != "proposed" || got.Description != "Edited." || got.Body != "Edited body." || got.ConfirmedAt == "" || got.RunID == "" {
		t.Errorf("confirm with an edit = %d %s", code, out)
	}
	// Only the body of an edit: the description of the proposal stays.
	code, out = e.do(t, http.MethodPost, path(made[1].ID, "confirm"), `{"body":"Only the body."}`)
	if got := decodeBody[skillJSON](t, out); code != http.StatusOK || got.Description != "d" || got.Body != "Only the body." {
		t.Errorf("confirm with a body only = %d %s", code, out)
	}
	if code, out := e.do(t, http.MethodPost, path(made[0].ID, "confirm"), ""); code != http.StatusConflict {
		t.Errorf("a second confirmation = %d %s, want 409", code, out)
	}
	if code, _ := e.do(t, http.MethodPost, path(999, "confirm"), ""); code != http.StatusNotFound {
		t.Errorf("an unknown skill = %d", code)
	}
	if left := e.list(t, "?state=proposed"); len(left) != 0 {
		t.Errorf("proposals left = %+v", left)
	}
}

func TestConfirmWithoutABodyKeepsTheProposalAsItIs(t *testing.T) {
	e := newSkillEnv(t)
	nas := e.host(t, "nas")
	made := e.propose(t, nas.ID, proposal("profile", "What the NAS is.", "A NAS."))
	code, out := e.do(t, http.MethodPost, fmt.Sprintf("/api/skills/%d/confirm", made[0].ID), "")
	got := decodeBody[skillJSON](t, out)
	if code != http.StatusOK || got.Description != "What the NAS is." || got.Body != "A NAS." || got.State != "active" {
		t.Errorf("confirm = %d %s", code, out)
	}
}

func TestConfirmingAReplacingProposalUpdatesTheReplacedSkillAndANameTakenMeanwhileIsRefused(t *testing.T) {
	e := newSkillEnv(t)
	nas := e.host(t, "nas")
	current := e.create(t, fmt.Sprintf(`{"hostId":%d,"name":"paperless","description":"Current.","body":"Current body."}`, nas.ID))
	made := e.propose(t, nas.ID, proposal("paperless", "Proposed.", "Proposed body."))

	code, out := e.do(t, http.MethodPost, fmt.Sprintf("/api/skills/%d/confirm", made[0].ID), "")
	got := decodeBody[skillJSON](t, out)
	if code != http.StatusOK || got.ID != current.ID || got.Description != "Proposed." || got.Body != "Proposed body." {
		t.Fatalf("confirm = %d %s", code, out)
	}
	if all := e.list(t, ""); len(all) != 1 {
		t.Errorf("%d skills, want the one that was updated in place", len(all))
	}

	// A proposal whose replaced skill was deleted, and whose name was taken by a skill the maintainer wrote since.
	second := e.propose(t, nas.ID, proposal("paperless", "Again.", "Again body."))
	if code, _ := e.do(t, http.MethodDelete, fmt.Sprintf("/api/skills/%d", current.ID), ""); code != http.StatusNoContent {
		t.Fatalf("delete = %d", code)
	}
	mine := e.create(t, fmt.Sprintf(`{"hostId":%d,"name":"paperless","description":"Mine.","body":"My own note."}`, nas.ID))
	if code, out := e.do(t, http.MethodPost, fmt.Sprintf("/api/skills/%d/confirm", second[0].ID), ""); code != http.StatusConflict || !strings.Contains(out, "already exists") {
		t.Errorf("confirm over a skill the maintainer wrote = %d %s", code, out)
	}
	if got := e.list(t, fmt.Sprintf("?host=%d&state=active", nas.ID)); len(got) != 1 || got[0].ID != mine.ID || got[0].Body != "My own note." {
		t.Errorf("the maintainer's skill = %+v", got)
	}
}

func TestConfirmingAnAlreadyHandledReplacingProposalIsA404AndANewOneA409(t *testing.T) {
	e := newSkillEnv(t)
	nas := e.host(t, "nas")
	e.create(t, fmt.Sprintf(`{"hostId":%d,"name":"paperless","description":"Current.","body":"Current body."}`, nas.ID))
	replacing := e.propose(t, nas.ID, proposal("paperless", "Proposed.", "Proposed body."))[0]
	fresh := e.propose(t, nas.ID, proposal("profile", "What the NAS is.", "A NAS."))[0]

	for _, p := range []store.Skill{replacing, fresh} {
		if code, out := e.do(t, http.MethodPost, fmt.Sprintf("/api/skills/%d/confirm", p.ID), ""); code != http.StatusOK {
			t.Fatalf("confirm %s = %d %s", p.Name, code, out)
		}
	}
	// The proposal that replaced a skill is gone (its row was deleted): not found. The one that became the skill is not a proposal now.
	if code, out := e.do(t, http.MethodPost, fmt.Sprintf("/api/skills/%d/confirm", replacing.ID), ""); code != http.StatusNotFound || !strings.Contains(out, "skill not found") {
		t.Errorf("confirming a handled replacing proposal = %d %s, want 404", code, out)
	}
	if code, out := e.do(t, http.MethodPost, fmt.Sprintf("/api/skills/%d/confirm", fresh.ID), ""); code != http.StatusConflict {
		t.Errorf("confirming a handled new proposal = %d %s, want 409", code, out)
	}
	if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/api/skills/%d/reject", replacing.ID), ""); code != http.StatusNotFound {
		t.Errorf("rejecting a handled replacing proposal = %d, want 404", code)
	}
}

func TestConfirmRefusesANewSkillInAFullScope(t *testing.T) {
	e := newSkillEnv(t)
	nas := e.host(t, "nas")
	for i := 0; i < skill.MaxPerScope; i++ {
		e.create(t, fmt.Sprintf(`{"hostId":%d,"name":"s%02d","description":"d","body":"b"}`, nas.ID, i))
	}
	made := e.propose(t, nas.ID, proposal("one-too-many", "d", "b"))
	code, out := e.do(t, http.MethodPost, fmt.Sprintf("/api/skills/%d/confirm", made[0].ID), "")
	if code != http.StatusConflict || !strings.Contains(out, "already has 30 skills") {
		t.Errorf("confirm in a full scope = %d %s", code, out)
	}
	if got := e.list(t, "?state=proposed"); len(got) != 1 {
		t.Errorf("the proposal must stay a proposal: %+v", got)
	}
}

func TestNoAnswerEchoesWhatAnAgentWrote(t *testing.T) {
	e := newSkillEnv(t)
	nas := e.host(t, "nas")
	e.create(t, fmt.Sprintf(`{"hostId":%d,"name":"paperless","description":"d","body":"b"}`, nas.ID))
	made := e.propose(t, nas.ID, proposal("paperless", "AGENT-DESCRIPTION", "AGENT-BODY"))
	id := made[0].ID

	var answers []string
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPatch, fmt.Sprintf("/api/skills/%d", id), `{"state":"off"}`},
		{http.MethodDelete, fmt.Sprintf("/api/skills/%d", id), ""},
		{http.MethodPost, fmt.Sprintf("/api/skills/%d/confirm", id), `{"description":"","body":"x"}`},
		{http.MethodPost, fmt.Sprintf("/api/skills/%d/reject", 999), ""},
		{http.MethodPost, fmt.Sprintf("/api/skills/%d/confirm", 999), ""},
	} {
		code, out := e.do(t, c.method, c.path, c.body)
		if code < 400 {
			t.Errorf("%s %s = %d, want an error", c.method, c.path, code)
		}
		answers = append(answers, out)
	}
	for _, out := range answers {
		if strings.Contains(out, "AGENT-") {
			t.Errorf("an error answer echoes what an agent wrote: %s", out)
		}
	}
}

func TestGetOneSkillCarriesWhatAProposalReplaces(t *testing.T) {
	e := newSkillEnv(t)
	nas := e.host(t, "nas")
	current := e.create(t, fmt.Sprintf(`{"hostId":%d,"name":"paperless","description":"Current.","body":"Current body."}`, nas.ID))
	made := e.propose(t, nas.ID, proposal("paperless", "Proposed.", "Proposed body."))

	code, out := e.do(t, http.MethodGet, fmt.Sprintf("/api/skills/%d", made[0].ID), "")
	p := decodeBody[skillJSON](t, out)
	if code != http.StatusOK || p.State != "proposed" || p.Replaces == nil || p.Replaces.ID != current.ID || p.Replaces.Body != "Current body." {
		t.Errorf("proposal = %d %s", code, out)
	}
	code, out = e.do(t, http.MethodGet, fmt.Sprintf("/api/skills/%d", current.ID), "")
	if got := decodeBody[skillJSON](t, out); code != http.StatusOK || got.Replaces != nil || got.Body != "Current body." {
		t.Errorf("active skill = %d %s", code, out)
	}
	if code, _ := e.do(t, http.MethodGet, "/api/skills/999", ""); code != http.StatusNotFound {
		t.Errorf("an unknown skill = %d", code)
	}
	if code, _ := e.do(t, http.MethodGet, "/api/skills/abc", ""); code != http.StatusNotFound {
		t.Errorf("an id that is not a number = %d", code)
	}
}

func TestRejectDeletesAProposalAndNothingElse(t *testing.T) {
	e := newSkillEnv(t)
	nas := e.host(t, "nas")
	a := e.create(t, `{"name":"a","description":"d","body":"b"}`)
	made := e.propose(t, nas.ID, proposal("pending", "d", "b"))

	if code, out := e.do(t, http.MethodPost, fmt.Sprintf("/api/skills/%d/reject", a.ID), ""); code != http.StatusConflict {
		t.Errorf("rejecting an active skill = %d %s, want 409", code, out)
	}
	if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/api/skills/%d/reject", made[0].ID), ""); code != http.StatusNoContent {
		t.Errorf("reject = %d", code)
	}
	if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/api/skills/%d/reject", made[0].ID), ""); code != http.StatusNotFound {
		t.Errorf("a second reject = %d", code)
	}
	if all := e.list(t, ""); len(all) != 1 || all[0].Name != "a" {
		t.Errorf("skills = %+v", all)
	}
}

func TestEveryChangeIsInTheActivityFeedWithoutWhatTheSkillSays(t *testing.T) {
	e := newSkillEnv(t)
	nas := e.host(t, "nas")
	a := e.create(t, fmt.Sprintf(`{"hostId":%d,"name":"paperless","description":"SECRET-DESCRIPTION","body":"SECRET-BODY"}`, nas.ID))
	e.do(t, http.MethodPatch, fmt.Sprintf("/api/skills/%d", a.ID), `{"state":"off"}`)
	made := e.propose(t, nas.ID, proposal("profile", "SECRET-DESCRIPTION", "SECRET-BODY"))
	e.do(t, http.MethodPost, fmt.Sprintf("/api/skills/%d/confirm", made[0].ID), "")
	more := e.propose(t, nas.ID, proposal("other", "SECRET-DESCRIPTION", "SECRET-BODY"))
	e.do(t, http.MethodPost, fmt.Sprintf("/api/skills/%d/reject", more[0].ID), "")
	e.do(t, http.MethodDelete, fmt.Sprintf("/api/skills/%d", a.ID), "")

	code, out := e.do(t, http.MethodGet, "/api/activity?limit=50", "")
	if code != http.StatusOK {
		t.Fatalf("activity = %d %s", code, out)
	}
	for _, want := range []string{"skill_created", "skill_updated", "skills_proposed", "skill_confirmed", "skill_rejected", "skill_deleted",
		"Skill paperless created (host nas)", "Skill paperless turned off (host nas)", "Skill profile confirmed (host nas)", "Proposal other rejected (host nas)", "Skill paperless deleted (host nas)"} {
		if !strings.Contains(out, want) {
			t.Errorf("the activity feed lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "SECRET-") {
		t.Errorf("the activity feed names what a skill says:\n%s", out)
	}
}
