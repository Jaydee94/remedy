package kube

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const versionJSON = `{"major":"1","minor":"31","gitVersion":"v1.31.0","platform":"linux/amd64"}`

// seen is what a fake API server saw of a request.
type seen struct {
	Method, Path, Query, Auth, Accept, ContentType, Body string
}

// fakeAPI serves handler and records every request.
type fakeAPI struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []seen
}

func newFakeAPI(t *testing.T, handler http.HandlerFunc) *fakeAPI {
	t.Helper()
	f := &fakeAPI{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := new(strings.Builder)
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			body.Write(buf[:n])
			if err != nil {
				break
			}
		}
		f.mu.Lock()
		f.reqs = append(f.reqs, seen{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"),
			r.Header.Get("Accept"), r.Header.Get("Content-Type"), body.String()})
		f.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAPI) requests() []seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]seen(nil), f.reqs...)
}

func jsonReply(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func newTestReader(t *testing.T, api *fakeAPI, token string) *Reader {
	t.Helper()
	r, err := NewReader(Config{API: api.URL, ReadTokenFile: writeFile(t, "read", token)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestGetVersionSendsTheTokenAsABearerOnAGetRequest(t *testing.T) {
	api := newFakeAPI(t, jsonReply(200, versionJSON))
	v, err := newTestReader(t, api, "the-read-token\n").GetVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.GitVersion != "v1.31.0" || v.Platform != "linux/amd64" {
		t.Fatalf("version = %+v", v)
	}
	got := api.requests()
	if len(got) != 1 || got[0].Method != "GET" || got[0].Path != "/version" ||
		got[0].Auth != "Bearer the-read-token" || got[0].Accept != "application/json" {
		t.Fatalf("requests = %+v", got)
	}
}

func TestTheTokenFileIsReadAtEveryRequest(t *testing.T) {
	api := newFakeAPI(t, jsonReply(200, versionJSON))
	path := writeFile(t, "read", "first")
	r, err := NewReader(Config{API: api.URL, ReadTokenFile: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetVersion(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetVersion(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := api.requests()
	if len(got) != 2 || got[0].Auth != "Bearer first" || got[1].Auth != "Bearer second" {
		t.Fatalf("a rotated token is not used: %+v", got)
	}
}

func TestAnEmptyTokenFileIsAnErrorAndNothingIsSent(t *testing.T) {
	api := newFakeAPI(t, jsonReply(200, versionJSON))
	path := writeFile(t, "read", "token")
	r, err := NewReader(Config{API: api.URL, ReadTokenFile: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetVersion(context.Background()); err == nil {
		t.Fatal("a request without a token was sent")
	}
	if n := len(api.requests()); n != 0 {
		t.Fatalf("%d requests reached the server", n)
	}
}

func TestStatusesBecomeErrorsThatCanBeTestedFor(t *testing.T) {
	cases := []struct {
		status int
		body   string
		is     error
		text   string
	}{
		{404, `{"kind":"Status","reason":"NotFound","message":"deployments.apps \"web\" not found","code":404}`, ErrNotFound, "not found"},
		{403, `{"kind":"Status","reason":"Forbidden","message":"pods is forbidden: User cannot list","code":403}`, ErrForbidden, "forbidden"},
		{401, `{"kind":"Status","reason":"Unauthorized","message":"Unauthorized","code":401}`, ErrUnauthorized, "Unauthorized"},
		{409, `{"kind":"Status","reason":"Conflict","message":"the object has been modified","code":409}`, ErrConflict, "modified"},
		{500, `<html>internal error</html>`, nil, "500"},
	}
	for _, tc := range cases {
		api := newFakeAPI(t, jsonReply(tc.status, tc.body))
		_, err := newTestReader(t, api, "tok").GetVersion(context.Background())
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != tc.status {
			t.Errorf("%d: error = %v", tc.status, err)
			continue
		}
		if tc.is != nil && !errors.Is(err, tc.is) {
			t.Errorf("%d: error %v is not %v", tc.status, err, tc.is)
		}
		if !strings.Contains(err.Error(), tc.text) {
			t.Errorf("%d: error %q does not mention %q", tc.status, err, tc.text)
		}
	}
}

func TestAnErrorNeverContainsTheToken(t *testing.T) {
	const token = "very-secret-bearer-value"
	api := newFakeAPI(t, jsonReply(403, `{"kind":"Status","message":"the token very-secret-bearer-value may not do this","code":403}`))
	_, err := newTestReader(t, api, token).GetVersion(context.Background())
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("error = %v", err)
	}
}

func TestARedirectIsNotFollowed(t *testing.T) {
	other := newFakeAPI(t, jsonReply(200, versionJSON))
	api := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/version", http.StatusFound)
	})
	_, err := newTestReader(t, api, "tok").GetVersion(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusFound {
		t.Fatalf("error = %v", err)
	}
	if n := len(other.requests()); n != 0 {
		t.Fatalf("the redirect was followed (%d requests, the token went along)", n)
	}
}

func TestAnAnswerThatIsTooLargeIsAnError(t *testing.T) {
	api := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"gitVersion":"`))
		_, _ = w.Write([]byte(strings.Repeat("x", maxBody+10)))
		_, _ = w.Write([]byte(`"}`))
	})
	if _, err := newTestReader(t, api, "tok").GetVersion(context.Background()); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("error = %v", err)
	}
}

func TestARequestHasATimeout(t *testing.T) {
	old := requestTimeout
	requestTimeout = 100 * time.Millisecond
	t.Cleanup(func() { requestTimeout = old })
	api := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	started := time.Now()
	if _, err := newTestReader(t, api, "tok").GetVersion(context.Background()); err == nil {
		t.Fatal("no error")
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("the request did not time out")
	}
}

func TestACancelledContextStopsTheRequest(t *testing.T) {
	api := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if _, err := newTestReader(t, api, "tok").GetVersion(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestTLSUsesTheConfiguredCA(t *testing.T) {
	srv := httptest.NewTLSServer(jsonReply(200, versionJSON))
	t.Cleanup(srv.Close)
	ca := writeFile(t, "ca.crt", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})))
	token := writeFile(t, "read", "tok")

	trusted, err := NewReader(Config{API: srv.URL, ReadTokenFile: token, CAFile: ca})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trusted.GetVersion(context.Background()); err != nil {
		t.Fatalf("with the CA: %v", err)
	}
	untrusted, err := NewReader(Config{API: srv.URL, ReadTokenFile: token})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := untrusted.GetVersion(context.Background()); err == nil {
		t.Fatal("a certificate that no configured CA signed was accepted")
	}
}

func TestTheReaderRefusesEveryMethodButGet(t *testing.T) {
	api := newFakeAPI(t, jsonReply(200, versionJSON))
	r := newTestReader(t, api, "tok")
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		req, _ := http.NewRequest(method, api.URL+"/api/v1/namespaces/demo/pods/x", nil)
		if _, err := r.http.Do(req); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Errorf("%s: error = %v", method, err)
		}
	}
	if n := len(api.requests()); n != 0 {
		t.Fatalf("%d refused requests reached the server", n)
	}
}
