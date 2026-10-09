// Package tokenrefresh mints a short-lived token for one service account and stores it in one Secret. The control
// plane's write identity is such a token: a pod gets only the token of its own account, so a CronJob that runs this
// keeps a second account's token fresh in a Secret the pod mounts as a file. If the job stops, the token expires and
// the actions fail closed.
//
// The client is as small as it can be: two requests, to two fixed paths, with the refresher's own token, which is read
// from its file for every request. It follows no redirect and uses no proxy: a token must not be sent anywhere the
// configuration did not name. Neither token is ever written to a log line or an error.
package tokenrefresh

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Jaydee94/remedy/internal/kube"
	"github.com/Jaydee94/remedy/internal/secret"
)

const (
	// DefaultTokenFile is the refresher's own service account token inside its pod.
	DefaultTokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"

	// MinLifetime is the least the API server grants a token request. MaxLifetime keeps a stored token short.
	MinLifetime = 10 * time.Minute
	MaxLifetime = 24 * time.Hour

	maxBody        = 1 << 20
	maxMessage     = 300
	requestTimeout = 15 * time.Second
)

var (
	nameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)
	keyRE  = regexp.MustCompile(`^[-._a-zA-Z0-9]{1,253}$`)
)

// Config says what to mint and where to store it.
type Config struct {
	API       string        // the API server, default kube.DefaultAPI
	CAFile    string        // its CA; empty means the pod's own CA when that file exists, else the system's roots
	TokenFile string        // the refresher's own token, default DefaultTokenFile
	Namespace string        // the namespace of the account and of the Secret
	Account   string        // the service account to mint a token for
	Secret    string        // the Secret to store it in; it must exist
	Key       string        // the key in the Secret's data
	Lifetime  time.Duration // MinLifetime to MaxLifetime
}

func (c Config) api() string {
	if c.API == "" {
		return kube.DefaultAPI
	}
	return strings.TrimRight(c.API, "/")
}

func (c Config) tokenFile() string {
	if c.TokenFile == "" {
		return DefaultTokenFile
	}
	return c.TokenFile
}

func (c Config) validate() error {
	if !kube.ValidNamespace(c.Namespace) {
		return fmt.Errorf("the namespace %q is not a namespace name", c.Namespace)
	}
	if !nameRE.MatchString(c.Account) {
		return fmt.Errorf("the service account %q is not a name", c.Account)
	}
	if !nameRE.MatchString(c.Secret) {
		return fmt.Errorf("the Secret %q is not a name", c.Secret)
	}
	if !keyRE.MatchString(c.Key) {
		return fmt.Errorf("the key %q is not a Secret key", c.Key)
	}
	if c.Lifetime < MinLifetime || c.Lifetime > MaxLifetime {
		return fmt.Errorf("the lifetime must be between %s and %s", MinLifetime, MaxLifetime)
	}
	if u, err := url.Parse(c.api()); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("the API must be an http or https URL")
	}
	return nil
}

// guard is the client's transport: whatever calls it, only the two requests of the refresher leave the process.
type guard struct {
	base                 http.RoundTripper
	mintPath, secretPath string
}

func (g *guard) allows(method, path string) bool {
	return (method == http.MethodPost && path == g.mintPath) || (method == http.MethodPatch && path == g.secretPath)
}

func (g *guard) RoundTrip(req *http.Request) (*http.Response, error) {
	if !g.allows(req.Method, req.URL.Path) {
		return nil, fmt.Errorf("tokenrefresh: refusing to send %s %s", req.Method, req.URL.Path)
	}
	return g.base.RoundTrip(req)
}

type client struct {
	base       string
	tokenFile  string
	mintPath   string
	secretPath string
	http       *http.Client
	known      []string // every token held so far, in every form it was sent in; see scrub
}

func newClient(c Config) (*client, error) {
	pool, err := loadPool(c.CAFile)
	if err != nil {
		return nil, err
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil // straight to the API server, whatever the environment says
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	mint := "/api/v1/namespaces/" + c.Namespace + "/serviceaccounts/" + c.Account + "/token"
	store := "/api/v1/namespaces/" + c.Namespace + "/secrets/" + c.Secret
	return &client{
		base: c.api(), tokenFile: c.tokenFile(), mintPath: mint, secretPath: store,
		http: &http.Client{
			Timeout:   requestTimeout,
			Transport: &guard{base: tr, mintPath: mint, secretPath: store},
			// An answer that redirects is an answer, not an instruction to send the token elsewhere.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// loadPool reads the CA. With no path it uses the pod's service account CA when that file exists, otherwise nil,
// which means the system's roots.
func loadPool(path string) (*x509.CertPool, error) {
	if path == "" {
		if _, err := os.Stat(kube.DefaultCAFile); err != nil {
			return nil, nil
		}
		path = kube.DefaultCAFile
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("the CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("the CA file %s holds no certificate", path)
	}
	return pool, nil
}

// readToken reads the refresher's own token. It is called for every request.
func readToken(path string) (secret.Value, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return secret.Value{}, err
	}
	tok := strings.TrimSpace(string(raw))
	switch {
	case tok == "":
		return secret.Value{}, fmt.Errorf("the token file %s is empty", path)
	case strings.ContainsAny(tok, "\r\n"):
		return secret.Value{}, fmt.Errorf("the token file %s holds more than one line", path)
	}
	return secret.NewValue(tok), nil
}

// do sends one request as the refresher and returns the body of a 2xx answer. A failure becomes an error whose text
// carries the status and the API server's own message, never a token.
func (cl *client) do(ctx context.Context, method, path, contentType string, body []byte) ([]byte, error) {
	own, err := readToken(cl.tokenFile)
	if err != nil {
		return nil, err
	}
	cl.know(own.Reveal())
	req, err := http.NewRequestWithContext(ctx, method, cl.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, cl.scrub(err)
	}
	req.Header.Set("Authorization", "Bearer "+own.Reveal())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", contentType)
	resp, err := cl.http.Do(req)
	if err != nil {
		return nil, cl.scrub(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, cl.scrub(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, cl.scrub(fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, cl.apiMessage(raw)))
	}
	return raw, nil
}

// know registers a token to be taken out of every error text, in the form given.
func (cl *client) know(forms ...string) {
	for _, f := range forms {
		if f != "" {
			cl.known = append(cl.known, f)
		}
	}
}

// scrub returns an error with every known token taken out of its text. Every error that leaves this package, and
// every status text built from an API server's answer, goes through it: an API server may echo what it was sent.
func (cl *client) scrub(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(cl.scrubText(err.Error()))
}

// scrubText takes every known token out of text.
func (cl *client) scrubText(text string) string {
	for _, k := range cl.known {
		text = strings.ReplaceAll(text, k, "***")
	}
	return text
}

// apiMessage is the message of an API server's Status answer, scrubbed and then shortened, or a fixed text when there is
// none. The order matters: a token is longer than the limit, and a token cut in two no longer matches what is scrubbed.
func (cl *client) apiMessage(raw []byte) string {
	var s struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &s) != nil || s.Message == "" {
		return "no message"
	}
	msg := cl.scrubText(s.Message)
	if len(msg) <= maxMessage {
		return msg
	}
	cut := maxMessage
	for cut > 0 && !utf8.RuneStart(msg[cut]) { // never end inside a multi-byte character
		cut--
	}
	return msg[:cut] + "..."
}

// Run mints a token for the account and stores it in the Secret. It returns an error for anything that went wrong; the
// token is never in it.
func Run(ctx context.Context, c Config, log *slog.Logger) error {
	if err := c.validate(); err != nil {
		return err
	}
	cl, err := newClient(c)
	if err != nil {
		return err
	}

	request, err := json.Marshal(map[string]any{
		"apiVersion": "authentication.k8s.io/v1",
		"kind":       "TokenRequest",
		"spec":       map[string]any{"expirationSeconds": int64(c.Lifetime.Seconds())},
	})
	if err != nil {
		return err
	}
	raw, err := cl.do(ctx, http.MethodPost, cl.mintPath, "application/json", request)
	if err != nil {
		return cl.scrub(fmt.Errorf("cannot mint a token for %s: %w", c.Account, err))
	}
	var answer struct {
		Status struct {
			Token               string `json:"token"`
			ExpirationTimestamp string `json:"expirationTimestamp"`
		} `json:"status"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil || answer.Status.Token == "" {
		return fmt.Errorf("the token request for %s was answered without a token", c.Account)
	}
	minted := secret.NewValue(answer.Status.Token)
	encoded := base64.StdEncoding.EncodeToString([]byte(minted.Reveal()))
	cl.know(minted.Reveal(), encoded) // the base64 form is the one that goes over the wire

	patch, err := json.Marshal(map[string]any{
		"data": map[string]string{c.Key: encoded},
	})
	if err != nil {
		return cl.scrub(err)
	}
	if _, err := cl.do(ctx, http.MethodPatch, cl.secretPath, "application/merge-patch+json", patch); err != nil {
		if strings.Contains(err.Error(), "HTTP 404") {
			return cl.scrub(fmt.Errorf("the Secret %q does not exist in %s: the chart creates it (%w)", c.Secret, c.Namespace, err))
		}
		return cl.scrub(fmt.Errorf("cannot store the token in the Secret %q: %w", c.Secret, err))
	}
	log.Info("stored a new token", "account", c.Account, "secret", c.Secret, "key", c.Key, "expires", answer.Status.ExpirationTimestamp)
	return nil
}
