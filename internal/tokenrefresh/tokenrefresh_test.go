package tokenrefresh_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/tokenrefresh"
)

const (
	ownToken    = "refresher-own-token-0123456789"
	mintedToken = "minted-write-token-abcdefghijkl"
)

type recorded struct {
	Method, Path, Auth, ContentType string
	Body                            []byte
}

// fakeAPI answers the two requests of the refresher and records them.
type fakeAPI struct {
	mu                      sync.Mutex
	reqs                    []recorded
	mintStatus, patchStatus int  // 0 means success
	echo                    bool // the error messages repeat every token that was sent, as a hostile or sloppy API server might
}

// leak is what an echoing API server adds to its message: the refresher's own token, the minted token and the form of
// the minted token that goes over the wire in the PATCH body.
func leak(tokens ...string) string {
	var b strings.Builder
	for _, t := range tokens {
		fmt.Fprintf(&b, " [%s]", t)
	}
	return b.String()
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.reqs = append(f.reqs, recorded{r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), body})
	f.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces/remedy-system/serviceaccounts/remedy-write/token":
		if f.mintStatus != 0 {
			w.WriteHeader(f.mintStatus)
			msg := `serviceaccounts "remedy-write" is forbidden`
			if f.echo { // no token has been minted yet, so only the refresher's own can be echoed
				msg += leak(ownToken)
			}
			status, _ := json.Marshal(map[string]string{"kind": "Status", "message": msg})
			w.Write(status)
			return
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"status":{"token":%q,"expirationTimestamp":"2026-10-07T12:00:00Z"}}`, mintedToken)
	case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/namespaces/remedy-system/secrets/remedy-write-token":
		if f.patchStatus != 0 {
			w.WriteHeader(f.patchStatus)
			msg := `secrets "remedy-write-token" not found`
			if f.echo {
				msg += leak(ownToken, mintedToken, base64.StdEncoding.EncodeToString([]byte(mintedToken)))
			}
			status, _ := json.Marshal(map[string]string{"kind": "Status", "message": msg})
			w.Write(status)
			return
		}
		fmt.Fprint(w, `{}`)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeAPI) requests() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.reqs...)
}

// setup starts the fake API and returns a valid configuration for it and the buffer the logs go to.
func setup(t *testing.T, f *fakeAPI) (tokenrefresh.Config, *bytes.Buffer) {
	t.Helper()
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(ownToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return tokenrefresh.Config{
		API: ts.URL, TokenFile: tokenFile,
		Namespace: "remedy-system", Account: "remedy-write", Secret: "remedy-write-token", Key: "token",
		Lifetime: 2 * time.Hour,
	}, &bytes.Buffer{}
}

func logTo(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestRunMintsATokenAndStoresIt(t *testing.T) {
	f := &fakeAPI{}
	cfg, logs := setup(t, f)
	if err := tokenrefresh.Run(context.Background(), cfg, logTo(logs)); err != nil {
		t.Fatal(err)
	}
	reqs := f.requests()
	if len(reqs) != 2 {
		t.Fatalf("%d requests, want 2: %+v", len(reqs), reqs)
	}
	mint, patch := reqs[0], reqs[1]
	if mint.Auth != "Bearer "+ownToken || patch.Auth != "Bearer "+ownToken {
		t.Fatalf("both requests are made with the refresher's own token, got %q and %q", mint.Auth, patch.Auth)
	}
	var tr struct {
		Kind string `json:"kind"`
		Spec struct {
			ExpirationSeconds int64 `json:"expirationSeconds"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(mint.Body, &tr); err != nil || tr.Kind != "TokenRequest" || tr.Spec.ExpirationSeconds != 7200 {
		t.Fatalf("token request = %s (%v)", mint.Body, err)
	}
	if patch.ContentType != "application/merge-patch+json" {
		t.Fatalf("patch content type = %q", patch.ContentType)
	}
	var p struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(patch.Body, &p); err != nil {
		t.Fatal(err)
	}
	if got, _ := base64.StdEncoding.DecodeString(p.Data["token"]); string(got) != mintedToken || len(p.Data) != 1 {
		t.Fatalf("patch data = %v, want only token=%q", p.Data, mintedToken)
	}
}

func TestRunNeverLogsOrReturnsATokenInText(t *testing.T) {
	f := &fakeAPI{patchStatus: http.StatusNotFound}
	cfg, logs := setup(t, f)
	err := tokenrefresh.Run(context.Background(), cfg, logTo(logs))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, text := range []string{logs.String(), err.Error()} {
		if strings.Contains(text, mintedToken) || strings.Contains(text, ownToken) {
			t.Fatalf("a token is in %q", text)
		}
	}
}

// secretsIn lists which of the known tokens (in any form that was sent) appear in text.
func secretsIn(text string) []string {
	var found []string
	for name, tok := range map[string]string{
		"own token":        ownToken,
		"minted token":     mintedToken,
		"minted in base64": base64.StdEncoding.EncodeToString([]byte(mintedToken)),
	} {
		if strings.Contains(text, tok) {
			found = append(found, name)
		}
	}
	return found
}

func TestEveryErrorPathScrubsEveryKnownToken(t *testing.T) {
	cases := map[string]*fakeAPI{
		"the mint fails":        {mintStatus: http.StatusForbidden, echo: true},
		"the patch fails":       {patchStatus: http.StatusInternalServerError, echo: true},
		"the Secret is missing": {patchStatus: http.StatusNotFound, echo: true},
	}
	for name, f := range cases {
		cfg, logs := setup(t, f)
		err := tokenrefresh.Run(context.Background(), cfg, logTo(logs))
		if err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		// main logs the error, so the log line is checked the way main writes it.
		logTo(logs).Error("the token was not refreshed", "err", err)
		if found := secretsIn(err.Error()); len(found) > 0 {
			t.Errorf("%s: the error carries %v: %q", name, found, err.Error())
		}
		if found := secretsIn(logs.String()); len(found) > 0 {
			t.Errorf("%s: the log carries %v: %q", name, found, logs.String())
		}
	}
}

func TestRunDoesNotStoreAnythingWhenMintingFails(t *testing.T) {
	f := &fakeAPI{mintStatus: http.StatusForbidden}
	cfg, _ := setup(t, f)
	err := tokenrefresh.Run(context.Background(), cfg, logTo(&bytes.Buffer{}))
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, want one that says 403", err)
	}
	if reqs := f.requests(); len(reqs) != 1 {
		t.Fatalf("%d requests, want only the token request", len(reqs))
	}
}

func TestRunSaysWhenTheSecretIsMissing(t *testing.T) {
	f := &fakeAPI{patchStatus: http.StatusNotFound}
	cfg, _ := setup(t, f)
	err := tokenrefresh.Run(context.Background(), cfg, logTo(&bytes.Buffer{}))
	if err == nil || !strings.Contains(err.Error(), `"remedy-write-token"`) || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v, want one that names the missing Secret", err)
	}
}

func TestRunRefusesAConfigurationItCannotUse(t *testing.T) {
	cases := map[string]func(*tokenrefresh.Config){
		"a lifetime under ten minutes":  func(c *tokenrefresh.Config) { c.Lifetime = 5 * time.Minute },
		"a lifetime over a day":         func(c *tokenrefresh.Config) { c.Lifetime = 48 * time.Hour },
		"no namespace":                  func(c *tokenrefresh.Config) { c.Namespace = "" },
		"a namespace with a slash":      func(c *tokenrefresh.Config) { c.Namespace = "a/b" },
		"an account with a path":        func(c *tokenrefresh.Config) { c.Account = "../kube-system" },
		"a Secret with upper case":      func(c *tokenrefresh.Config) { c.Secret = "Remedy" },
		"a key with a slash":            func(c *tokenrefresh.Config) { c.Key = "a/b" },
		"an API that is not an URL":     func(c *tokenrefresh.Config) { c.API = "not a url" },
		"a CA file that does not exist": func(c *tokenrefresh.Config) { c.CAFile = "/no/such/ca.crt" },
	}
	for name, change := range cases {
		f := &fakeAPI{}
		cfg, _ := setup(t, f)
		change(&cfg)
		if err := tokenrefresh.Run(context.Background(), cfg, logTo(&bytes.Buffer{})); err == nil {
			t.Errorf("%s: expected an error", name)
		}
		if n := len(f.requests()); n != 0 {
			t.Errorf("%s: %d requests were sent, want none", name, n)
		}
	}
}

func TestRunNeedsItsOwnTokenFile(t *testing.T) {
	f := &fakeAPI{}
	cfg, _ := setup(t, f)
	for name, content := range map[string]*string{"missing": nil, "empty": ptr(""), "two lines": ptr("a\nb\n")} {
		if content == nil {
			cfg.TokenFile = filepath.Join(t.TempDir(), "nothing")
		} else if err := os.WriteFile(cfg.TokenFile, []byte(*content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := tokenrefresh.Run(context.Background(), cfg, logTo(&bytes.Buffer{})); err == nil {
			t.Errorf("%s token file: expected an error", name)
		}
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requests were sent without a token", n)
	}
}

func ptr(s string) *string { return &s }

func TestRunDoesNotFollowARedirect(t *testing.T) {
	elsewhere := &fakeAPI{}
	other := httptest.NewServer(elsewhere)
	t.Cleanup(other.Close)
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirecting.Close)

	cfg, _ := setup(t, &fakeAPI{})
	cfg.API = redirecting.URL
	if err := tokenrefresh.Run(context.Background(), cfg, logTo(&bytes.Buffer{})); err == nil {
		t.Fatal("expected an error for a redirect")
	}
	if n := len(elsewhere.requests()); n != 0 {
		t.Fatalf("the refresher followed a redirect: %d requests reached the other host (its token went with them)", n)
	}
}

// A real service account token is a JWT of about 900 characters, longer than the message limit of an error text. An
// API server that echoes one near the start of its message must not leave a prefix of it behind when the text is cut.
func TestAnEchoedTokenLongerThanTheMessageLimitLeavesNoPrefix(t *testing.T) {
	jwt := func(seed string) string { return "eyJ" + strings.Repeat(seed, 300) } // 903 characters
	long := map[string]string{"own": jwt("a1B"), "minted": jwt("z9Y")}
	forms := map[string]string{
		"own": long["own"], "minted": long["minted"],
		"minted in base64": base64.StdEncoding.EncodeToString([]byte(long["minted"])),
	}
	for name, failing := range map[string]string{"the mint fails": "mint", "the patch fails": "patch"} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.Copy(io.Discard, r.Body)
			echo := func(status int, text string) {
				w.WriteHeader(status)
				msg, _ := json.Marshal(map[string]string{"message": text})
				w.Write(msg)
			}
			if strings.HasSuffix(r.URL.Path, "/token") {
				if failing == "mint" {
					echo(http.StatusForbidden, long["own"]+" is not allowed")
					return
				}
				w.WriteHeader(http.StatusCreated)
				fmt.Fprintf(w, `{"status":{"token":%q}}`, long["minted"])
				return
			}
			echo(http.StatusInternalServerError, long["own"]+" "+forms["minted"]+" "+forms["minted in base64"])
		}))
		tokenFile := filepath.Join(t.TempDir(), "token")
		if err := os.WriteFile(tokenFile, []byte(long["own"]+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := tokenrefresh.Config{
			API: ts.URL, TokenFile: tokenFile, Namespace: "remedy-system", Account: "remedy-write",
			Secret: "remedy-write-token", Key: "token", Lifetime: 2 * time.Hour,
		}
		logs := &bytes.Buffer{}
		err := tokenrefresh.Run(context.Background(), cfg, logTo(logs))
		ts.Close()
		if err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		logTo(logs).Error("the token was not refreshed", "err", err)
		for _, text := range []string{err.Error(), logs.String()} {
			for form, tok := range forms {
				if strings.Contains(text, tok[:40]) {
					t.Errorf("%s: the first 40 characters of the %s token survive in %.200q", name, form, text)
				}
			}
		}
	}
}
