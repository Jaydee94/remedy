package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"time"
)

const mergePatch = "application/merge-patch+json"

// Writer is the client for the actions the maintainer approved. It has exactly four methods, enforced by a test, and
// its transport refuses everything but PATCH and DELETE. It uses the write identity's token and works only in the
// namespaces of the allowlist; the Argo CD methods work in the Argo CD namespace.
type Writer struct {
	*client
	namespaces []string
	argo       string
}

// NewWriter builds a Writer for a configuration whose write side is on: a write token and at least one namespace.
func NewWriter(c Config, opts ...Option) (*Writer, error) {
	if !c.WriteEnabled() {
		return nil, errors.New("kube: the write side is not configured (REMEDY_K8S_WRITE_TOKEN_FILE and REMEDY_K8S_WRITE_NAMESPACES)")
	}
	cl, err := newClient(c, c.WriteTokenFile, func(m string) bool { return m == http.MethodPatch || m == http.MethodDelete },
		"this client only patches and deletes", opts)
	if err != nil {
		return nil, err
	}
	return &Writer{client: cl, namespaces: slices.Clone(c.WriteNamespaces), argo: c.argoNamespace()}, nil
}

// A name that goes into a request path: a DNS subdomain. Nothing in it can add a path segment or a query.
var objectName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)

func validName(name string) error {
	if len(name) > 253 || !objectName.MatchString(name) {
		return fmt.Errorf("%w: %q is not an object name", ErrInvalid, name)
	}
	return nil
}

// ValidName says whether s can be the name of an object in a request path.
func ValidName(s string) bool { return validName(s) == nil }

// target checks the namespace against the allowlist and the name against the name rules, before anything is sent.
func (w *Writer) target(namespace, name string) error {
	if !slices.Contains(w.namespaces, namespace) {
		return fmt.Errorf("%w: %q", ErrNamespaceNotAllowed, namespace)
	}
	return validName(name)
}

var workloadPlurals = map[string]string{"deployment": "deployments", "statefulset": "statefulsets", "daemonset": "daemonsets"}

func (w *Writer) patch(ctx context.Context, path string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = w.do(ctx, http.MethodPatch, path, nil, mergePatch, raw)
	return err
}

// RestartWorkload restarts a Deployment, StatefulSet or DaemonSet the way `kubectl rollout restart` does: it sets
// the restartedAt annotation of the pod template, which makes the controller roll the pods.
func (w *Writer) RestartWorkload(ctx context.Context, kind, namespace, name string, now time.Time) error {
	plural, ok := workloadPlurals[kind]
	if !ok {
		return fmt.Errorf("%w: %q is not deployment, statefulset or daemonset", ErrInvalid, kind)
	}
	if err := w.target(namespace, name); err != nil {
		return err
	}
	return w.patch(ctx, "/apis/apps/v1/namespaces/"+namespace+"/"+plural+"/"+name, map[string]any{
		"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"annotations": map[string]string{
			"kubectl.kubernetes.io/restartedAt": now.UTC().Format(time.RFC3339),
		}}}},
	})
}

// DeletePod deletes one pod, with the pod's own grace period. The controller that owns it makes a new one.
func (w *Writer) DeletePod(ctx context.Context, namespace, name string) error {
	if err := w.target(namespace, name); err != nil {
		return err
	}
	_, err := w.do(ctx, http.MethodDelete, "/api/v1/namespaces/"+namespace+"/pods/"+name, nil, "", nil)
	return err
}

func (w *Writer) applicationPath(app string) (string, error) {
	if err := validName(app); err != nil {
		return "", err
	}
	return "/apis/argoproj.io/v1alpha1/namespaces/" + w.argo + "/applications/" + app, nil
}

// RefreshApplication asks Argo CD to compare an application with Git again, the way the refresh annotation does.
// A hard refresh also drops Argo CD's cache of the manifests.
func (w *Writer) RefreshApplication(ctx context.Context, app string, hard bool) error {
	path, err := w.applicationPath(app)
	if err != nil {
		return err
	}
	mode := "normal"
	if hard {
		mode = "hard"
	}
	return w.patch(ctx, path, map[string]any{"metadata": map[string]any{"annotations": map[string]string{
		"argocd.argoproj.io/refresh": mode,
	}}})
}

// SyncApplication starts a sync of an application: the operation that makes Argo CD apply what Git says. It sets no
// options: no prune, no force, no replace.
func (w *Writer) SyncApplication(ctx context.Context, app string) error {
	path, err := w.applicationPath(app)
	if err != nil {
		return err
	}
	return w.patch(ctx, path, map[string]any{"operation": map[string]any{
		"initiatedBy": map[string]string{"username": "remedy"},
		"sync":        map[string]any{},
	}})
}
