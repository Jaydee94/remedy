package webhook

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

// AuthStore is what the authenticator needs from the store.
type AuthStore interface {
	WebhookByName(ctx context.Context, name string) (store.WebhookAuth, error)
}

// Authenticator checks a bearer token against an endpoint. It answers true or false and nothing else: the caller sends the same 401
// for every false, so an unknown endpoint, a disabled one and a wrong token cannot be told apart.
type Authenticator struct {
	Store AuthStore
	Now   func() time.Time
	// OnCompare is called once per hash comparison; tests use it to see that every path does the same work.
	OnCompare func()
}

// maxHeader bounds the Authorization header that is looked at; a longer one cannot be a token of ours.
const maxHeader = 512

// dummyHash is compared against when the endpoint is unknown, so that the time does not tell an unknown name from a wrong token.
var dummyHash = func() []byte { s := sha256.Sum256([]byte("remedy-webhook-dummy")); return s[:] }()

func (a *Authenticator) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Authenticator) compare(hash, got []byte) bool {
	if a.OnCompare != nil {
		a.OnCompare()
	}
	return len(hash) == len(got) && subtle.ConstantTimeCompare(hash, got) == 1
}

// bearer returns the token of a header "Bearer <token>" (the scheme is case-insensitive) and whether there was one.
func bearer(h string) (string, bool) {
	if len(h) > maxHeader {
		return "", false
	}
	scheme, token, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.ContainsAny(token, " \t") {
		return "", false
	}
	return token, true
}

// Authenticate returns the endpoint when the header holds the token of the enabled endpoint, or its previous token inside the
// rotation overlap.
func (a *Authenticator) Authenticate(ctx context.Context, name, authorization string) (store.WebhookEndpoint, bool) {
	token, present := bearer(authorization)
	sum := sha256.Sum256([]byte(token))
	got := sum[:]

	ep, err := a.Store.WebhookByName(ctx, name)
	if err != nil {
		// Unknown endpoint (or a store that failed): do the same two comparisons anyway, then refuse.
		a.compare(dummyHash, got)
		a.compare(dummyHash, got)
		return store.WebhookEndpoint{}, false
	}
	current := a.compare(ep.TokenHash, got)
	prev := false
	if ep.PreviousValidUntil != nil && a.now().Before(*ep.PreviousValidUntil) && len(ep.PreviousTokenHash) > 0 {
		prev = a.compare(ep.PreviousTokenHash, got)
	} else {
		a.compare(dummyHash, got)
	}
	if !present || !ep.Enabled || !(current || prev) {
		return store.WebhookEndpoint{}, false
	}
	return ep.WebhookEndpoint, true
}

// AuthenticateRequest reads the Authorization header of r. The query string is never looked at.
func (a *Authenticator) AuthenticateRequest(r *http.Request, name string) (store.WebhookEndpoint, bool) {
	return a.Authenticate(r.Context(), name, r.Header.Get("Authorization"))
}

// Unauthorized writes the one answer of every authentication failure.
func Unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="remedy"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized"}` + "\n"))
}
