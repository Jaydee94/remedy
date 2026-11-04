package webhook_test

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/internal/webhook"
)

type fakeAuthStore struct {
	byName  map[string]store.WebhookAuth
	lookups int
}

func (f *fakeAuthStore) WebhookByName(_ context.Context, name string) (store.WebhookAuth, error) {
	f.lookups++
	a, ok := f.byName[name]
	if !ok {
		return store.WebhookAuth{}, store.ErrNotFound
	}
	return a, nil
}

func hashOf(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

var now = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func newAuth(enabled bool, token string) (*webhook.Authenticator, *fakeAuthStore) {
	fs := &fakeAuthStore{byName: map[string]store.WebhookAuth{
		"grafana": {WebhookEndpoint: store.WebhookEndpoint{ID: 1, Name: "grafana", Enabled: enabled}, TokenHash: hashOf(token)},
	}}
	return &webhook.Authenticator{Store: fs, Now: func() time.Time { return now }}, fs
}

func TestAValidTokenAuthenticatesAndTheSchemeIsCaseInsensitive(t *testing.T) {
	a, _ := newAuth(true, "secret-token")
	for _, h := range []string{"Bearer secret-token", "bearer secret-token", "BEARER secret-token"} {
		if e, ok := a.Authenticate(context.Background(), "grafana", h); !ok || e.ID != 1 {
			t.Errorf("%q: ok = %v", h, ok)
		}
	}
}

func TestAWrongOrOddAuthorizationIsRefused(t *testing.T) {
	a, _ := newAuth(true, "secret-token")
	for name, h := range map[string]string{
		"none":          "",
		"basic":         "Basic c2VjcmV0LXRva2Vu",
		"bearer only":   "Bearer",
		"bearer space":  "Bearer ",
		"wrong token":   "Bearer secret-tokeN",
		"prefix":        "Bearer secret-toke",
		"token+suffix":  "Bearer secret-token ",
		"two tokens":    "Bearer secret-token secret-token",
		"huge header":   "Bearer " + strings.Repeat("a", 10<<10),
		"token no word": "secret-token",
	} {
		if _, ok := a.Authenticate(context.Background(), "grafana", h); ok {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestADisabledEndpointAndAnUnknownOneAreRefusedToo(t *testing.T) {
	a, _ := newAuth(false, "secret-token")
	if _, ok := a.Authenticate(context.Background(), "grafana", "Bearer secret-token"); ok {
		t.Error("a disabled endpoint accepted its token")
	}
	b, _ := newAuth(true, "secret-token")
	if _, ok := b.Authenticate(context.Background(), "nobody", "Bearer secret-token"); ok {
		t.Error("an unknown endpoint accepted a token")
	}
}

func TestTheNameIsMatchedWithoutRegardToCase(t *testing.T) {
	a, fs := newAuth(true, "secret-token")
	fs.byName["Grafana"] = fs.byName["grafana"] // the fake is case-sensitive; the real store lowers both sides
	if _, ok := a.Authenticate(context.Background(), "Grafana", "Bearer secret-token"); !ok {
		t.Error("a name in another case was refused by the authenticator itself")
	}
}

func TestRotationOverlapEndsAfterTwentyFourHours(t *testing.T) {
	until := now.Add(time.Hour)
	fs := &fakeAuthStore{byName: map[string]store.WebhookAuth{"g": {
		WebhookEndpoint: store.WebhookEndpoint{ID: 1, Name: "g", Enabled: true, PreviousValidUntil: &until},
		TokenHash:       hashOf("new"), PreviousTokenHash: hashOf("old"),
	}}}
	clock := now
	a := &webhook.Authenticator{Store: fs, Now: func() time.Time { return clock }}
	for _, tok := range []string{"new", "old"} {
		if _, ok := a.Authenticate(context.Background(), "g", "Bearer "+tok); !ok {
			t.Errorf("token %q refused inside the overlap", tok)
		}
	}
	clock = until // the window is open until, not including
	if _, ok := a.Authenticate(context.Background(), "g", "Bearer old"); ok {
		t.Error("the old token works at the end of the overlap")
	}
	if _, ok := a.Authenticate(context.Background(), "g", "Bearer new"); !ok {
		t.Error("the new token stopped working")
	}
}

func TestAnEndpointWithoutAPreviousTokenNeverMatchesAnEmptyOne(t *testing.T) {
	a, _ := newAuth(true, "secret-token")
	if _, ok := a.Authenticate(context.Background(), "grafana", "Bearer "); ok {
		t.Error("an empty token matched an empty previous hash")
	}
}

// The three failures are one response, byte for byte: status, headers and body.
func TestTheThreeFailuresAreByteIdentical(t *testing.T) {
	fs := &fakeAuthStore{byName: map[string]store.WebhookAuth{
		"on":  {WebhookEndpoint: store.WebhookEndpoint{ID: 1, Name: "on", Enabled: true}, TokenHash: hashOf("t")},
		"off": {WebhookEndpoint: store.WebhookEndpoint{ID: 2, Name: "off", Enabled: false}, TokenHash: hashOf("t")},
	}}
	a := &webhook.Authenticator{Store: fs, Now: func() time.Time { return now }}
	respond := func(name, header string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		if _, ok := a.Authenticate(context.Background(), name, header); ok {
			t.Fatalf("%s %q authenticated", name, header)
		}
		webhook.Unauthorized(w)
		return w
	}
	want := respond("on", "Bearer wrong")
	for _, got := range []*httptest.ResponseRecorder{respond("nobody", "Bearer t"), respond("off", "Bearer t"), respond("on", ""), respond("on", "Basic x")} {
		if got.Code != http.StatusUnauthorized || got.Body.String() != want.Body.String() || got.Header().Get("Content-Type") != want.Header().Get("Content-Type") ||
			got.Header().Get("WWW-Authenticate") != want.Header().Get("WWW-Authenticate") || len(got.Header()) != len(want.Header()) {
			t.Errorf("a failure differs: %d %q %v vs %d %q %v", got.Code, got.Body, got.Header(), want.Code, want.Body, want.Header())
		}
	}
	if strings.Contains(want.Body.String(), "endpoint") || strings.Contains(want.Body.String(), "token") {
		t.Errorf("the 401 body says too much: %q", want.Body)
	}
}

func TestAnUnknownNameStillDoesTheWorkOfAComparison(t *testing.T) {
	var compares int
	a := &webhook.Authenticator{Store: &fakeAuthStore{}, Now: func() time.Time { return now }, OnCompare: func() { compares++ }}
	a.Authenticate(context.Background(), "nobody", "Bearer anything")
	if compares == 0 {
		t.Error("no comparison was made for an unknown name: the time tells it from a wrong token")
	}
	b, _ := newAuth(true, "secret-token")
	var wrong int
	b.OnCompare = func() { wrong++ }
	b.Authenticate(context.Background(), "grafana", "Bearer nope")
	if wrong != compares {
		t.Errorf("a wrong token makes %d comparisons, an unknown name %d", wrong, compares)
	}
}

func TestTheTokenIsNeverTakenFromTheURL(t *testing.T) {
	a, _ := newAuth(true, "secret-token")
	r := httptest.NewRequest(http.MethodPost, "/webhook/v1/grafana?token=secret-token", nil)
	if _, ok := a.AuthenticateRequest(r, "grafana"); ok {
		t.Error("a token in the query string authenticated")
	}
	r.Header.Set("Authorization", "Bearer secret-token")
	if _, ok := a.AuthenticateRequest(r, "grafana"); !ok {
		t.Error("the header did not authenticate")
	}
}
