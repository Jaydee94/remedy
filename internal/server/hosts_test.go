package server_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/sshhost"
	"github.com/Jaydee94/remedy/internal/sshhost/sshtest"
	"github.com/Jaydee94/remedy/internal/store"
)

type hostEnv struct {
	ts     *httptest.Server
	store  *store.Store
	dbPath string
	ssh    *sshtest.Server
	client *http.Client
	seen   []string // every response body, for the walk for secrets
}

func nasCommands() map[string]sshtest.Reply {
	return map[string]sshtest.Reply{
		"id -un":                  {Out: "remedy\n"},
		"id -Gn":                  {Out: "remedy users\n"},
		"uname -sr":               {Out: "Linux 6.8.0\n"},
		"env LC_ALL=C sudo -n -l": {Out: "User remedy may run the following commands on nas:\n    (root) NOPASSWD: /usr/bin/docker ps -a\n"},
	}
}

// newHostEnv starts a server over st (a new store if nil) that seals with key, next to an SSH test server, and logs in.
func newHostEnv(t *testing.T, st *store.Store, dbPath string, key secret.Key, ssh *sshtest.Server) *hostEnv {
	t.Helper()
	if st == nil {
		dbPath = filepath.Join(t.TempDir(), "test.db")
		var err error
		if st, err = store.Open(dbPath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
	}
	ts := httptest.NewServer(server.New(server.Deps{
		Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Key: key, Hosts: &sshhost.Service{},
	}))
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	e := &hostEnv{ts: ts, store: st, dbPath: dbPath, ssh: ssh, client: &http.Client{Jar: jar}}
	if code, _ := e.do(t, http.MethodPost, "/api/login", `{"password":"`+password+`"}`); code != http.StatusNoContent {
		t.Fatalf("login status = %d", code)
	}
	e.seen = nil
	return e
}

func (e *hostEnv) do(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	req.Header.Set("X-Remedy-CSRF", "1")
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	e.seen = append(e.seen, string(b))
	return resp.StatusCode, string(b)
}

func decodeBody[T any](t *testing.T, body string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return v
}

type hostJSON struct {
	ID                int64    `json:"id"`
	Name              string   `json:"name"`
	Address           string   `json:"address"`
	Port              int      `json:"port"`
	Account           string   `json:"account"`
	Notes             string   `json:"notes"`
	SystemdUnits      []string `json:"systemdUnits"`
	Status            string   `json:"status"`
	StatusDetail      string   `json:"statusDetail"`
	PinnedFingerprint string   `json:"pinnedFingerprint"`
	PinCleared        bool     `json:"pinCleared"`
	Report            *struct {
		Account   string   `json:"account"`
		Sudo      string   `json:"sudo"`
		Warnings  []string `json:"warnings"`
		SudoRules []string `json:"sudoRules"`
	} `json:"report"`
	Credential *struct {
		Kind      string `json:"kind"`
		Hint      string `json:"hint"`
		PublicKey string `json:"publicKey"`
	} `json:"credential"`
	Onboarding *struct {
		AuthorizedKeys string `json:"authorizedKeys"`
		Sudoers        string `json:"sudoers"`
		Error          string `json:"error"`
	} `json:"onboarding"`
	PresentedKey *struct {
		Type        string `json:"type"`
		Fingerprint string `json:"fingerprint"`
	} `json:"presentedKey"`
}

func (e *hostEnv) create(t *testing.T, name string) hostJSON {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"address":%q,"port":%d}`, name, e.ssh.Host, e.ssh.Port)
	code, b := e.do(t, http.MethodPost, "/api/hosts", body)
	if code != http.StatusCreated {
		t.Fatalf("create = %d %s", code, b)
	}
	return decodeBody[hostJSON](t, b)
}

func (e *hostEnv) setPassword(t *testing.T, id int64, pw string) {
	t.Helper()
	if code, b := e.do(t, http.MethodPut, fmt.Sprintf("/api/hosts/%d/credential", id), fmt.Sprintf(`{"kind":"password","password":%q}`, pw)); code != http.StatusOK {
		t.Fatalf("set password = %d %s", code, b)
	}
}

func (e *hostEnv) test(t *testing.T, id int64) hostJSON {
	t.Helper()
	code, b := e.do(t, http.MethodPost, fmt.Sprintf("/api/hosts/%d/test", id), "")
	if code != http.StatusOK {
		t.Fatalf("test = %d %s", code, b)
	}
	return decodeBody[hostJSON](t, b)
}

func (e *hostEnv) pin(t *testing.T, id int64, fp string) (int, hostJSON) {
	t.Helper()
	code, b := e.do(t, http.MethodPost, fmt.Sprintf("/api/hosts/%d/hostkey", id), fmt.Sprintf(`{"fingerprint":%q}`, fp))
	var h hostJSON
	if code == http.StatusOK {
		h = decodeBody[hostJSON](t, b)
	}
	return code, h
}

func TestHostRoutesNeedALogin(t *testing.T) {
	e := newHostEnv(t, nil, "", ghKey(t, 1), sshtest.Start(t, sshtest.Config{}))
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/hosts"}, {"POST", "/api/hosts"}, {"GET", "/api/hosts/1"}, {"PATCH", "/api/hosts/1"}, {"DELETE", "/api/hosts/1"},
		{"PUT", "/api/hosts/1/credential"}, {"DELETE", "/api/hosts/1/credential"}, {"POST", "/api/hosts/1/test"},
		{"POST", "/api/hosts/1/hostkey"}, {"DELETE", "/api/hosts/1/hostkey"},
	} {
		req, _ := http.NewRequest(c.method, e.ts.URL+c.path, strings.NewReader("{}"))
		req.Header.Set("X-Remedy-CSRF", "1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s without a session = %d, want 401", c.method, c.path, resp.StatusCode)
		}
	}
}

func TestHostLifecycle(t *testing.T) {
	e := newHostEnv(t, nil, "", ghKey(t, 1), sshtest.Start(t, sshtest.Config{}))

	h := e.create(t, "nas")
	if h.Port != e.ssh.Port || h.Account != "remedy" || h.Status != "new" || h.Credential != nil {
		t.Fatalf("created = %+v", h)
	}
	if code, _ := e.do(t, http.MethodPost, "/api/hosts", `{"name":"NAS","address":"10.0.0.1"}`); code != http.StatusConflict {
		t.Fatalf("duplicate name = %d, want 409", code)
	}
	if code, b := e.do(t, http.MethodPost, "/api/hosts", `{"name":"my nas","address":"10.0.0.1"}`); code != http.StatusBadRequest || !strings.Contains(b, "name") {
		t.Fatalf("bad name = %d %s", code, b)
	}
	if code, _ := e.do(t, http.MethodPost, "/api/hosts", `{"name":"x","address":"10.0.0.1","dockerPattern":"a b"}`); code != http.StatusBadRequest {
		t.Fatalf("bad docker pattern = %d", code)
	}

	code, b := e.do(t, http.MethodPatch, fmt.Sprintf("/api/hosts/%d", h.ID), `{"notes":"Paperless lives here","systemdUnits":["node_exporter"]}`)
	got := decodeBody[hostJSON](t, b)
	if code != http.StatusOK || got.Notes != "Paperless lives here" || len(got.SystemdUnits) != 1 || got.SystemdUnits[0] != "node_exporter.service" || got.PinCleared {
		t.Fatalf("patch = %d %+v", code, got)
	}

	code, b = e.do(t, http.MethodGet, "/api/hosts", "")
	if list := decodeBody[[]hostJSON](t, b); code != http.StatusOK || len(list) != 1 || list[0].Name != "nas" {
		t.Fatalf("list = %d %s", code, b)
	}

	if code, _ := e.do(t, http.MethodDelete, fmt.Sprintf("/api/hosts/%d", h.ID), ""); code != http.StatusNoContent {
		t.Fatalf("delete = %d", code)
	}
	if code, _ := e.do(t, http.MethodGet, fmt.Sprintf("/api/hosts/%d", h.ID), ""); code != http.StatusNotFound {
		t.Errorf("GET after delete = %d, want 404", code)
	}
	if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/api/hosts/%d/test", h.ID), ""); code != http.StatusNotFound {
		t.Errorf("test after delete = %d, want 404", code)
	}
	if code, _ := e.do(t, http.MethodGet, "/api/hosts/abc", ""); code != http.StatusNotFound {
		t.Fatalf("a non-numeric id = %d, want 404", code)
	}
}

func TestPutCredentialRejectsBadKeys(t *testing.T) {
	e := newHostEnv(t, nil, "", ghKey(t, 1), sshtest.Start(t, sshtest.Config{}))
	h := e.create(t, "nas")
	url := fmt.Sprintf("/api/hosts/%d/credential", h.ID)
	protected, _ := sshtest.KeyPEM(t, "right-passphrase")

	for name, c := range map[string]struct{ body, mention string }{
		"garbage":          {`{"kind":"key","key":"NOT-A-KEY-DISTINCTIVE"}`, "private key"},
		"no passphrase":    {fmt.Sprintf(`{"kind":"key","key":%q}`, protected), "passphrase"},
		"wrong passphrase": {fmt.Sprintf(`{"kind":"key","key":%q,"passphrase":"wrong-DISTINCTIVE"}`, protected), "passphrase"},
		"empty password":   {`{"kind":"password","password":""}`, "password"},
		"unknown kind":     {`{"kind":"token"}`, "kind"},
		"oversize body":    {`{"kind":"password","password":"` + strings.Repeat("x", 20<<10) + `"}`, "request"},
	} {
		code, b := e.do(t, http.MethodPut, url, c.body)
		if code != http.StatusBadRequest || !strings.Contains(b, c.mention) {
			t.Errorf("%s: %d %s", name, code, b)
		}
		for _, secretText := range []string{"NOT-A-KEY-DISTINCTIVE", "wrong-DISTINCTIVE", "OPENSSH PRIVATE KEY"} {
			if strings.Contains(b, secretText) {
				t.Errorf("%s: the answer repeats %q", name, secretText)
			}
		}
	}
}

// dump returns every value of the tables as text, except the sealed column.
func dump(t *testing.T, path string) string {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var out strings.Builder
	for _, q := range []string{
		`SELECT * FROM hosts`,
		`SELECT host_id, kind, hint, public_key, updated_at FROM host_credentials`,
		`SELECT * FROM activity`,
	} {
		rows, err := raw.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for _, v := range vals {
				fmt.Fprintf(&out, "%s|", v)
			}
			out.WriteString("\n")
		}
		rows.Close()
	}
	return out.String()
}

func TestCredentialsAreWriteOnly(t *testing.T) {
	e := newHostEnv(t, nil, "", ghKey(t, 1), sshtest.Start(t, sshtest.Config{Commands: nasCommands()}))
	keyHost, pwHost := e.create(t, "keyed"), e.create(t, "passworded")

	pem, _ := sshtest.KeyPEM(t, "pp-DISTINCTIVE")
	pemBody := strings.Split(strings.TrimSpace(pem), "\n")[1] // a line of the base64 body
	code, b := e.do(t, http.MethodPut, fmt.Sprintf("/api/hosts/%d/credential", keyHost.ID), fmt.Sprintf(`{"kind":"key","key":%q,"passphrase":"pp-DISTINCTIVE"}`, pem))
	if code != http.StatusOK {
		t.Fatalf("put key = %d %s", code, b)
	}
	view := decodeBody[hostJSON](t, b)
	if view.Credential == nil || view.Credential.Kind != "key" || !strings.HasPrefix(view.Credential.Hint, "SHA256:") || !strings.HasPrefix(view.Credential.PublicKey, "ssh-ed25519 ") {
		t.Fatalf("credential view = %+v", view.Credential)
	}
	e.setPassword(t, pwHost.ID, "hunter2-DISTINCTIVE")

	for _, p := range []string{"", fmt.Sprintf("/%d", keyHost.ID), fmt.Sprintf("/%d", pwHost.ID)} {
		e.do(t, http.MethodGet, "/api/hosts"+p, "")
	}
	e.do(t, http.MethodPost, fmt.Sprintf("/api/hosts/%d/test", pwHost.ID), "")
	e.do(t, http.MethodDelete, fmt.Sprintf("/api/hosts/%d/credential", pwHost.ID), "")

	haystack := strings.Join(e.seen, "\n") + "\n" + dump(t, e.dbPath)
	for _, secretText := range []string{"pp-DISTINCTIVE", "hunter2-DISTINCTIVE", pemBody, "OPENSSH PRIVATE KEY"} {
		if strings.Contains(haystack, secretText) {
			t.Errorf("%q appears outside the sealed column", secretText)
		}
	}
	if code, b := e.do(t, http.MethodGet, fmt.Sprintf("/api/hosts/%d", pwHost.ID), ""); code != http.StatusOK || strings.Contains(b, `"credential"`) {
		t.Errorf("after deleting the credential: %d %s", code, b)
	}
}

func TestATestWithoutAPinOnlyProbes(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: nasCommands()})
	e := newHostEnv(t, nil, "", ghKey(t, 1), srv)
	h := e.create(t, "nas")

	if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/api/hosts/%d/test", h.ID), ""); code != http.StatusConflict {
		t.Fatalf("test without a login = %d, want 409", code)
	}
	e.setPassword(t, h.ID, "pw")

	got := e.test(t, h.ID)
	if got.Status != "untrusted" || got.PresentedKey == nil || got.PresentedKey.Type != "ssh-ed25519" || !strings.HasPrefix(got.PresentedKey.Fingerprint, "SHA256:") {
		t.Fatalf("first test = %+v", got)
	}
	if srv.Attempts() != 0 {
		t.Fatalf("the first test offered a login %d times", srv.Attempts())
	}
}

func TestPinningNeedsTheFingerprintYouSaw(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: nasCommands()})
	e := newHostEnv(t, nil, "", ghKey(t, 1), srv)
	h := e.create(t, "nas")
	e.setPassword(t, h.ID, "pw")
	seen := e.test(t, h.ID).PresentedKey.Fingerprint

	if code, _ := e.pin(t, h.ID, "SHA256:notwhatyousaw"); code != http.StatusConflict {
		t.Fatalf("pin with another fingerprint = %d, want 409", code)
	}
	code, pinned := e.pin(t, h.ID, seen)
	if code != http.StatusOK || pinned.PinnedFingerprint != seen || pinned.Status != "new" {
		t.Fatalf("pin = %d %+v", code, pinned)
	}

	// The host's key changes between the first test and the pin: the call refuses.
	e2 := newHostEnv(t, nil, "", ghKey(t, 1), srv)
	h2 := e2.create(t, "nas2")
	e2.setPassword(t, h2.ID, "pw")
	shown := e2.test(t, h2.ID).PresentedKey.Fingerprint
	srv.SetHostSigner(sshtest.NewSigner(t))
	if code, _ := e2.pin(t, h2.ID, shown); code != http.StatusConflict {
		t.Fatalf("pin of a key that is gone = %d, want 409", code)
	}

	if code, _ := e.do(t, http.MethodDelete, fmt.Sprintf("/api/hosts/%d/hostkey", h.ID), ""); code != http.StatusNoContent {
		t.Fatalf("unpin = %d", code)
	}
	_, b := e.do(t, http.MethodGet, fmt.Sprintf("/api/hosts/%d", h.ID), "")
	if g := decodeBody[hostJSON](t, b); g.PinnedFingerprint != "" || g.Status != "untrusted" {
		t.Fatalf("after unpinning = %+v", g)
	}
}

func TestATestAfterPinningReportsTheAccountsRights(t *testing.T) {
	cmds := nasCommands()
	cmds["id -Gn"] = sshtest.Reply{Out: "remedy users docker\n"}
	cmds["env LC_ALL=C sudo -n -l"] = sshtest.Reply{Out: "User remedy may run the following commands on nas:\n    (ALL : ALL) NOPASSWD: ALL\n"}
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: cmds})
	e := newHostEnv(t, nil, "", ghKey(t, 1), srv)
	h := e.create(t, "nas")
	e.setPassword(t, h.ID, "pw")
	e.pin(t, h.ID, e.test(t, h.ID).PresentedKey.Fingerprint)

	got := e.test(t, h.ID)
	if got.Status != "ok" || got.Report == nil || got.Report.Account != "remedy" || got.Report.Sudo != "unrestricted" || len(got.Report.Warnings) != 2 {
		t.Fatalf("test = %+v report %+v", got, got.Report)
	}
}

func TestAChangedHostKeyIsRefused(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: nasCommands()})
	e := newHostEnv(t, nil, "", ghKey(t, 1), srv)
	h := e.create(t, "nas")
	e.setPassword(t, h.ID, "pw")
	e.pin(t, h.ID, e.test(t, h.ID).PresentedKey.Fingerprint)

	srv.SetHostSigner(sshtest.NewSigner(t))
	got := e.test(t, h.ID)
	if got.Status != "key_changed" || !strings.Contains(got.StatusDetail, "another key") {
		t.Fatalf("test after the key changed = %+v", got)
	}
	if srv.Attempts() != 0 {
		t.Fatal("a login was offered to a host that showed another key")
	}
}

func TestWrongLoginAndUnreachableHostGiveFixedMessages(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: nasCommands()})
	e := newHostEnv(t, nil, "", ghKey(t, 1), srv)
	h := e.create(t, "nas")
	e.setPassword(t, h.ID, "wrong")
	e.pin(t, h.ID, e.test(t, h.ID).PresentedKey.Fingerprint)
	if got := e.test(t, h.ID); got.Status != "error" || got.StatusDetail != "The host rejected the login." {
		t.Fatalf("wrong password = %+v", got)
	}

	srv.Close()
	got := e.test(t, h.ID)
	if got.Status != "error" || got.StatusDetail != "The host refused the connection." || strings.Contains(got.StatusDetail, "127.0.0.1") {
		t.Fatalf("closed port = %+v", got)
	}
}

func TestAnAddressChangeClearsThePin(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: nasCommands()})
	e := newHostEnv(t, nil, "", ghKey(t, 1), srv)
	h := e.create(t, "nas")
	e.setPassword(t, h.ID, "pw")
	e.pin(t, h.ID, e.test(t, h.ID).PresentedKey.Fingerprint)

	code, b := e.do(t, http.MethodPatch, fmt.Sprintf("/api/hosts/%d", h.ID), `{"address":"localhost"}`)
	got := decodeBody[hostJSON](t, b)
	if code != http.StatusOK || !got.PinCleared || got.PinnedFingerprint != "" || got.Status != "untrusted" {
		t.Fatalf("patch of the address = %d %+v", code, got)
	}
}

func TestAnUndecryptableLoginIsReported(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: nasCommands()})
	a := newHostEnv(t, nil, "", ghKey(t, 1), srv)
	h := a.create(t, "nas")
	a.setPassword(t, h.ID, "pw")
	a.pin(t, h.ID, a.test(t, h.ID).PresentedKey.Fingerprint)

	// The same database, opened by a control plane with another master key.
	b := newHostEnv(t, a.store, a.dbPath, ghKey(t, 2), srv)
	got := b.test(t, h.ID)
	if got.Status != "undecryptable" || !strings.Contains(got.StatusDetail, "REMEDY_MASTER_KEY") {
		t.Fatalf("test with the wrong master key = %+v", got)
	}
}

func TestTheDetailCarriesTheOnboardingSnippet(t *testing.T) {
	e := newHostEnv(t, nil, "", ghKey(t, 1), sshtest.Start(t, sshtest.Config{}))
	h := e.create(t, "nas")
	e.do(t, http.MethodPatch, fmt.Sprintf("/api/hosts/%d", h.ID), `{"dockerPattern":"remedy-*","systemdUnits":["node_exporter"]}`)
	pem, _ := sshtest.KeyPEM(t, "")
	e.do(t, http.MethodPut, fmt.Sprintf("/api/hosts/%d/credential", h.ID), fmt.Sprintf(`{"kind":"key","key":%q}`, pem))

	_, b := e.do(t, http.MethodGet, fmt.Sprintf("/api/hosts/%d", h.ID), "")
	got := decodeBody[hostJSON](t, b)
	if got.Onboarding == nil || got.Onboarding.Error != "" ||
		!strings.Contains(got.Onboarding.AuthorizedKeys, got.Credential.PublicKey) ||
		!strings.Contains(got.Onboarding.Sudoers, "remedy ALL=(root) NOPASSWD: /usr/bin/docker ps -a") ||
		!strings.Contains(got.Onboarding.Sudoers, "/usr/bin/systemctl restart node_exporter.service") {
		t.Fatalf("onboarding = %+v", got.Onboarding)
	}
	// The list does not carry the snippet.
	_, list := e.do(t, http.MethodGet, "/api/hosts", "")
	if strings.Contains(list, "onboarding") {
		t.Fatal("the list carries the onboarding snippet")
	}
}
