// Package kube is the control plane's access to the Kubernetes API. It has two clients with different credentials:
// a Reader that can only read, and a Writer that can do four things, in the namespaces of an allowlist. Both are
// thin clients on the standard library: a token read from a file at every request, an optional CA, a bounded
// answer, and a transport that refuses what the client is not meant to send.
package kube

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
)

const (
	// DefaultAPI is the API server as seen from inside a cluster.
	DefaultAPI = "https://kubernetes.default.svc"
	// DefaultCAFile is the CA of a pod's service account. It is used when no CA file is set and the file exists;
	// otherwise the system's roots apply.
	DefaultCAFile = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	// DefaultArgoNamespace is where Argo CD keeps its Application objects.
	DefaultArgoNamespace = "argocd"
)

// Config is how the control plane reaches the cluster. The zero value means no cluster.
type Config struct {
	API             string   // REMEDY_K8S_API
	CAFile          string   // REMEDY_K8S_CA_FILE
	ReadTokenFile   string   // REMEDY_K8S_READ_TOKEN_FILE: turns the read side on
	WriteTokenFile  string   // REMEDY_K8S_WRITE_TOKEN_FILE
	WriteNamespaces []string // REMEDY_K8S_WRITE_NAMESPACES: where actions may be used
	ArgoNamespace   string   // REMEDY_K8S_ARGO_NAMESPACE
}

// ReadEnabled says whether the read side is configured.
func (c Config) ReadEnabled() bool { return c.ReadTokenFile != "" }

// WriteEnabled says whether actions are possible: a write token and at least one namespace to use them in.
func (c Config) WriteEnabled() bool { return c.WriteTokenFile != "" && len(c.WriteNamespaces) > 0 }

// NamespaceAllowed says whether an action may be used in the namespace.
func (c Config) NamespaceAllowed(namespace string) bool {
	return namespace != "" && slices.Contains(c.WriteNamespaces, namespace)
}

func (c Config) api() string {
	if c.API == "" {
		return DefaultAPI
	}
	return strings.TrimRight(c.API, "/")
}

func (c Config) argoNamespace() string {
	if c.ArgoNamespace == "" {
		return DefaultArgoNamespace
	}
	return c.ArgoNamespace
}

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// ValidNamespace says whether s is a namespace name.
func ValidNamespace(s string) bool { return dnsLabel.MatchString(s) }

// ParseNamespaces reads a comma separated list of namespaces. It drops empty entries and duplicates and refuses
// anything that is not a namespace name: a wildcard or a path would widen what the list is meant to say.
func ParseNamespaces(list string) ([]string, error) {
	var out []string
	for _, n := range strings.Split(list, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !dnsLabel.MatchString(n) {
			return nil, fmt.Errorf("%q is not a namespace name (lower case letters, digits and dashes, at most 63 characters)", n)
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out, nil
}

// Validate checks a configuration that is meant to be used: the files can be read, the CA parses, the namespaces
// are names. It does not talk to the cluster. A configuration without any cluster setting is fine.
func (c Config) Validate() error {
	if !c.ReadEnabled() && c.WriteTokenFile == "" && len(c.WriteNamespaces) == 0 {
		return nil
	}
	if c.WriteTokenFile != "" && !c.ReadEnabled() {
		return errors.New("REMEDY_K8S_WRITE_TOKEN_FILE needs REMEDY_K8S_READ_TOKEN_FILE: the actions check their target with the read side")
	}
	if len(c.WriteNamespaces) > 0 && c.WriteTokenFile == "" {
		return errors.New("REMEDY_K8S_WRITE_NAMESPACES needs REMEDY_K8S_WRITE_TOKEN_FILE")
	}
	u, err := url.Parse(c.api())
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("REMEDY_K8S_API must be an http or https URL")
	}
	if c.ReadEnabled() {
		if _, err := readToken(c.ReadTokenFile); err != nil {
			return fmt.Errorf("REMEDY_K8S_READ_TOKEN_FILE: %w", err)
		}
	}
	if c.WriteTokenFile != "" {
		// A refresher fills this file after the control plane has started (in a cluster the Secret it comes from is
		// empty at install), so a file that is not there yet or holds no token is not a mistake: Warnings says so
		// and every action fails until the file holds a token. It is read again at every request. A file that holds
		// something wrong is still refused.
		if _, err := readToken(c.WriteTokenFile); err != nil && !tokenNotThereYet(err) {
			return fmt.Errorf("REMEDY_K8S_WRITE_TOKEN_FILE: %w", err)
		}
	}
	if _, err := loadPool(c.CAFile); err != nil {
		return fmt.Errorf("REMEDY_K8S_CA_FILE: %w", err)
	}
	for _, n := range c.WriteNamespaces {
		if !dnsLabel.MatchString(n) {
			return fmt.Errorf("REMEDY_K8S_WRITE_NAMESPACES: %q is not a namespace name", n)
		}
	}
	if c.ArgoNamespace != "" && !dnsLabel.MatchString(c.ArgoNamespace) {
		return fmt.Errorf("REMEDY_K8S_ARGO_NAMESPACE: %q is not a namespace name", c.ArgoNamespace)
	}
	return nil
}

// tokenNotThereYet says whether readToken failed because the file does not exist or holds no token.
func tokenNotThereYet(err error) bool {
	var empty emptyTokenError
	return errors.Is(err, os.ErrNotExist) || errors.As(err, &empty)
}

// Warnings lists what is configured but cannot be used, for a start-up log. It assumes a valid configuration.
func (c Config) Warnings() []string {
	var w []string
	if c.WriteTokenFile != "" {
		if _, err := readToken(c.WriteTokenFile); err != nil && tokenNotThereYet(err) {
			w = append(w, "REMEDY_K8S_WRITE_TOKEN_FILE holds no token yet: every cluster action fails until it does (the token refresher fills it)")
		}
	}
	if c.WriteTokenFile != "" && len(c.WriteNamespaces) == 0 {
		w = append(w, "REMEDY_K8S_WRITE_TOKEN_FILE is set but REMEDY_K8S_WRITE_NAMESPACES is empty: no cluster action can be used")
	}
	return w
}

// loadPool reads the CA the cluster's certificate is checked against. With no path it uses the service account's CA
// when that file exists and otherwise returns nil, which means the system's roots.
func loadPool(path string) (*x509.CertPool, error) {
	if path == "" {
		if _, err := os.Stat(DefaultCAFile); err != nil {
			return nil, nil
		}
		path = DefaultCAFile
	}
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("%s holds no PEM certificate", path)
	}
	return pool, nil
}
