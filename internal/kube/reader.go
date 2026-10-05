package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Reader is the read-only client. Every exported method is a Get or a List, enforced by a test, and its transport
// refuses everything but GET and HEAD. It uses the read identity's token.
type Reader struct {
	*client
	argo string // the namespace of the Argo CD applications
}

// NewReader builds a Reader for a configuration whose read side is on.
func NewReader(c Config, opts ...Option) (*Reader, error) {
	if !c.ReadEnabled() {
		return nil, errors.New("kube: the read side is not configured (REMEDY_K8S_READ_TOKEN_FILE)")
	}
	cl, err := newClient(c, c.ReadTokenFile, func(m string) bool { return m == http.MethodGet || m == http.MethodHead },
		"this client is read-only", opts)
	if err != nil {
		return nil, err
	}
	return &Reader{client: cl, argo: c.argoNamespace()}, nil
}

// Version is what the API server says about itself.
type Version struct {
	Major      string `json:"major"`
	Minor      string `json:"minor"`
	GitVersion string `json:"gitVersion"`
	Platform   string `json:"platform"`
}

// GetVersion asks the API server for its version. Remedy uses it at start-up to say whether the cluster can be
// reached with the read token.
func (r *Reader) GetVersion(ctx context.Context) (Version, error) {
	body, err := r.do(ctx, http.MethodGet, "/version", nil, "", nil)
	if err != nil {
		return Version{}, err
	}
	var v Version
	if err := json.Unmarshal(body, &v); err != nil {
		return Version{}, fmt.Errorf("kube: unexpected answer to /version: %w", err)
	}
	return v, nil
}
