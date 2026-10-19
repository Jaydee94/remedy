package alertmanager_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/alertmanager"
	"github.com/Jaydee94/remedy/internal/alertmanager/alertmanagertest"
	"github.com/Jaydee94/remedy/internal/secret"
)

func creds(url, token string) alertmanager.Credentials {
	return func() (string, secret.Value, error) { return url, secret.NewValue(token), nil }
}

func newClient(t *testing.T, c alertmanager.Credentials) *alertmanager.Client {
	t.Helper()
	cl, err := alertmanager.New(c, "")
	if err != nil {
		t.Fatal(err)
	}
	return cl
}

func TestListAlertsAsksForActiveAlertsAndSendsTheToken(t *testing.T) {
	fake := alertmanagertest.New(t, "tok")
	fake.SetAlerts(alertmanagertest.Alert("f1", "KubePodCrashLooping", map[string]string{"namespace": "demo"}, time.Now()))
	got, err := newClient(t, creds(fake.URL, "tok")).ListAlerts(context.Background())
	if err != nil || len(got) != 1 || got[0].Fingerprint != "f1" || got[0].Labels["alertname"] != "KubePodCrashLooping" || got[0].Status.State != "active" {
		t.Fatalf("%+v, %v", got, err)
	}
	reqs := fake.Requests()
	if len(reqs) != 1 || reqs[0].Method != http.MethodGet || reqs[0].Path != "/api/v2/alerts" ||
		reqs[0].Query != "active=true&inhibited=false&silenced=false" || reqs[0].Auth != "Bearer tok" {
		t.Fatalf("requests = %+v", reqs)
	}
}

func TestNoTokenMeansNoAuthorizationHeader(t *testing.T) {
	fake := alertmanagertest.New(t, "")
	if _, err := newClient(t, creds(fake.URL, "")).ListAlerts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reqs := fake.Requests(); len(reqs) != 1 || reqs[0].Auth != "" {
		t.Fatalf("requests = %+v", reqs)
	}
}

func TestOnlyAlertsInTheStateActiveAreKept(t *testing.T) {
	fake := alertmanagertest.New(t, "")
	silenced := alertmanagertest.Alert("f2", "A", nil, time.Now())
	silenced["status"] = map[string]any{"state": "suppressed"}
	fake.SetAlerts(alertmanagertest.Alert("f1", "A", nil, time.Now()), silenced)
	got, err := newClient(t, creds(fake.URL, "")).ListAlerts(context.Background())
	if err != nil || len(got) != 1 || got[0].Fingerprint != "f1" {
		t.Fatalf("%+v, %v", got, err)
	}
}

func TestTheAddressAndTheTokenAreAskedForAtEveryRequest(t *testing.T) {
	fake := alertmanagertest.New(t, "")
	n := 0
	cl := newClient(t, func() (string, secret.Value, error) {
		n++
		return fake.URL, secret.NewValue("t" + string(rune('0'+n))), nil
	})
	_, _ = cl.ListAlerts(context.Background())
	_, _ = cl.ListAlerts(context.Background())
	reqs := fake.Requests()
	if len(reqs) != 2 || reqs[0].Auth != "Bearer t1" || reqs[1].Auth != "Bearer t2" {
		t.Fatalf("requests = %+v", reqs)
	}
}

func TestAnEmptyAddressIsNotConfigured(t *testing.T) {
	if _, err := newClient(t, creds("", "")).ListAlerts(context.Background()); !errors.Is(err, alertmanager.ErrNotConfigured) {
		t.Fatalf("error = %v", err)
	}
	boom := errors.New("cannot open the token")
	cl := newClient(t, func() (string, secret.Value, error) { return "", secret.Value{}, boom })
	if _, err := cl.ListAlerts(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("error = %v", err)
	}
}

func TestAnAnswerThatIsNotAJSONArrayOfAlertsIsAnErrorAndNeverAPartialList(t *testing.T) {
	fake := alertmanagertest.New(t, "")
	for name, body := range map[string]string{
		"object": `{"status":"success"}`, "null": `null`, "string": `"x"`, "number": `5`, "html": `<html>`, "empty": ``,
		"truncated array":  `[{"fingerprint":"f1","labels":{"alertname":"A"},"status":{"state":"active"}},{"fingerprint":"f2"`,
		"array of strings": `["a","b"]`,
	} {
		fake.SetRaw(200, body)
		got, err := newClient(t, creds(fake.URL, "")).ListAlerts(context.Background())
		if err == nil || got != nil {
			t.Errorf("%s: %+v, %v", name, got, err)
		}
	}
	fake.SetRaw(200, `{"x":1}`)
	if _, err := newClient(t, creds(fake.URL, "")).ListAlerts(context.Background()); !errors.Is(err, alertmanager.ErrNotAlerts) {
		t.Errorf("an object: %v", err)
	}
}

func TestAnAnswerOverTheLimitIsAnError(t *testing.T) {
	fake := alertmanagertest.New(t, "")
	big := make([]byte, 8<<20+10)
	for i := range big {
		big[i] = ' '
	}
	big[0] = '['
	fake.SetBody(big)
	if _, err := newClient(t, creds(fake.URL, "")).ListAlerts(context.Background()); !errors.Is(err, alertmanager.ErrTooLarge) {
		t.Fatalf("error = %v", err)
	}
}

func TestAStatusOtherThan2xxIsAnAPIErrorWithoutTheBody(t *testing.T) {
	fake := alertmanagertest.New(t, "")
	fake.SetRaw(500, `internal secret detail: tok-123456789`)
	_, err := newClient(t, creds(fake.URL, "tok-123456789")).ListAlerts(context.Background())
	var api *alertmanager.APIError
	if !errors.As(err, &api) || api.Status != 500 || strings.Contains(err.Error(), "internal secret detail") || strings.Contains(err.Error(), "tok-123456789") {
		t.Fatalf("error = %v", err)
	}
}

func TestARedirectIsNotFollowed(t *testing.T) {
	var elsewhere int
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { elsewhere++ }))
	t.Cleanup(other.Close)
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	t.Cleanup(redirecting.Close)
	_, err := newClient(t, creds(redirecting.URL, "tok")).ListAlerts(context.Background())
	var api *alertmanager.APIError
	if !errors.As(err, &api) || api.Status != http.StatusFound || elsewhere != 0 {
		t.Fatalf("error = %v, requests to the other server = %d", err, elsewhere)
	}
}

func TestAnUnreachableServerIsAnErrorThatDoesNotShowTheToken(t *testing.T) {
	// closed port: start and close a server to get an address nobody listens on
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	_, err := newClient(t, creds(url, "tok-SECRET-123456")).ListAlerts(context.Background())
	if err == nil || strings.Contains(err.Error(), "tok-SECRET-123456") {
		t.Fatalf("error = %v", err)
	}
}

func TestEveryExportedMethodOfTheClientIsAGetOrAList(t *testing.T) {
	typ := reflect.TypeOf(&alertmanager.Client{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if !strings.HasPrefix(name, "Get") && !strings.HasPrefix(name, "List") {
			t.Errorf("the method %s is neither a Get nor a List: the client must stay read-only", name)
		}
	}
}

func TestACAFileThatIsNotACertificateIsRefused(t *testing.T) {
	f := t.TempDir() + "/ca.pem"
	if err := os.WriteFile(f, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := alertmanager.New(creds("http://x", ""), f); err == nil {
		t.Fatal("a CA file without a certificate was accepted")
	}
	if _, err := alertmanager.New(creds("http://x", ""), t.TempDir()+"/missing.pem"); err == nil {
		t.Fatal("a missing CA file was accepted")
	}
}

// alertmanager-0.34.1.json is the unedited answer of a real Alertmanager 0.34.1 (a container) to the request the client sends, for two
// alerts posted to its API: the alerts are made up, the shape of the answer (including the member mutedBy) is not.
func TestARealAnswerOfAlertmanager0_34IsReadAsItIs(t *testing.T) {
	raw, err := os.ReadFile("alertmanagertest/testdata/alertmanager-0.34.1.json")
	if err != nil {
		t.Fatal(err)
	}
	fake := alertmanagertest.New(t, "")
	fake.SetBody(raw)
	got, err := newClient(t, creds(fake.URL, "")).ListAlerts(context.Background())
	if err != nil || len(got) != 2 {
		t.Fatalf("%d alerts, %v", len(got), err)
	}
	crash := got[0]
	if crash.Labels["alertname"] != "KubePodCrashLooping" || crash.Labels["severity"] != "critical" || crash.Fingerprint == "" ||
		crash.StartsAt.IsZero() || crash.Status.State != "active" || crash.Annotations["summary"] == "" || crash.GeneratorURL == "" {
		t.Errorf("the first alert was not read completely: %+v", crash)
	}
}

func TestTheAnswerOfTheHomeServerIsReadAsItIs(t *testing.T) {
	raw, err := os.ReadFile("alertmanagertest/testdata/home-server.json")
	if err != nil {
		t.Fatal(err)
	}
	fake := alertmanagertest.New(t, "")
	fake.SetBody(raw)
	got, err := newClient(t, creds(fake.URL, "")).ListAlerts(context.Background())
	if err != nil || len(got) != 4 {
		t.Fatalf("%d alerts, %v", len(got), err)
	}
	var nas int
	for _, a := range got {
		if a.Labels["instance"] == "ugreen-nas" {
			nas++
		}
	}
	if nas != 2 {
		t.Errorf("%d alerts of the NAS, want 2", nas)
	}
}
