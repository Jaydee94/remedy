package gatekeeper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/kube"
)

// ClusterActionTools returns the tools that change something in the cluster: restart a workload, delete a pod, ask
// Argo CD to refresh or to sync an application. They are mutating: each call waits for the maintainer's approval. They
// work only in the namespaces of cfg's allowlist, which is checked before an approval is asked for, and again by the
// Writer. now is the clock a restart is stamped with; nil means the real one.
func ClusterActionTools(r *kube.Reader, w *kube.Writer, cfg kube.Config, now func() time.Time) []Tool {
	if now == nil {
		now = time.Now
	}
	a := &actionTools{r: r, w: w, cfg: cfg, now: now}
	return []Tool{a.rolloutRestart(), a.deletePod(), a.argoRefresh(), a.argoSync()}
}

type actionTools struct {
	r   *kube.Reader
	w   *kube.Writer
	cfg kube.Config
	now func() time.Time
}

const waitsForApproval = " This changes the cluster, so the maintainer has to approve the call first; the call waits until they decide."

// allowed refuses a namespace outside the allowlist and says where actions are allowed.
func (a *actionTools) allowed(namespace string) error {
	if a.cfg.NamespaceAllowed(namespace) {
		return nil
	}
	return ArgumentError(fmt.Sprintf("actions are not allowed in the namespace %q; they are allowed in: %s", namespace, strings.Join(a.cfg.WriteNamespaces, ", ")))
}

// actionErr is what the agent is told when the Writer or the Reader failed during an approved action.
func actionErr(err error) error {
	switch {
	case errors.Is(err, kube.ErrNamespaceNotAllowed), errors.Is(err, kube.ErrInvalid), errors.Is(err, kube.ErrNotFound):
		return ArgumentError(err.Error())
	}
	return err
}

var restartKinds = []string{"deployment", "statefulset", "daemonset"}

type restartArgs struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

func (a *actionTools) rolloutRestart() Tool {
	return Tool{
		Name:        "cluster_rollout_restart",
		Description: "Restarts a deployment, statefulset or daemonset the way `kubectl rollout restart` does: its pods are replaced one after the other." + waitsForApproval,
		Mutating:    true,
		Group:       GroupCluster,
		Schema: schema(`"kind":{"type":"string","enum":["deployment","statefulset","daemonset"]},"namespace":{"type":"string"},"name":{"type":"string"}`,
			"kind", "namespace", "name"),
		Decode: func(raw json.RawMessage) (json.RawMessage, error) {
			var v restartArgs
			if err := DecodeArgs(raw, &v); err != nil {
				return nil, err
			}
			if !slices.Contains(restartKinds, v.Kind) {
				return nil, ArgumentError("kind must be one of " + strings.Join(restartKinds, ", "))
			}
			if err := namespaceArg(v.Namespace, true); err != nil {
				return nil, err
			}
			if err := nameArg("name", v.Name); err != nil {
				return nil, err
			}
			return json.Marshal(v)
		},
		Check: func(ctx context.Context, args json.RawMessage) error {
			var v restartArgs
			if err := json.Unmarshal(args, &v); err != nil {
				return err
			}
			if err := a.allowed(v.Namespace); err != nil {
				return err
			}
			if _, err := a.r.GetWorkload(ctx, v.Kind, v.Namespace, v.Name); err != nil {
				return clusterErr(err)
			}
			return nil
		},
		Activity: func(args json.RawMessage) string {
			var v restartArgs
			_ = json.Unmarshal(args, &v)
			return fmt.Sprintf("Restarted %s %s/%s", v.Kind, v.Namespace, v.Name)
		},
		Run: func(ctx context.Context, c Call) (string, error) {
			var v restartArgs
			if err := json.Unmarshal(c.Args, &v); err != nil {
				return "", err
			}
			if err := a.w.RestartWorkload(ctx, v.Kind, v.Namespace, v.Name, a.now()); err != nil {
				return "", actionErr(err)
			}
			return fmt.Sprintf("restarted %s %s/%s: its pods are replaced one after the other", v.Kind, v.Namespace, v.Name), nil
		},
	}
}

type podArgs struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

func (a *actionTools) deletePod() Tool {
	return Tool{
		Name:        "cluster_delete_pod",
		Description: "Deletes one pod, so that the controller that owns it makes a new one. Use it for a pod that is stuck; to restart a whole workload use cluster_rollout_restart." + waitsForApproval,
		Mutating:    true,
		Group:       GroupCluster,
		Schema:      schema(`"namespace":{"type":"string"},"name":{"type":"string","description":"The name of the pod."}`, "namespace", "name"),
		Decode: func(raw json.RawMessage) (json.RawMessage, error) {
			var v podArgs
			if err := DecodeArgs(raw, &v); err != nil {
				return nil, err
			}
			if err := namespaceArg(v.Namespace, true); err != nil {
				return nil, err
			}
			if err := nameArg("name", v.Name); err != nil {
				return nil, err
			}
			return json.Marshal(v)
		},
		Check: func(ctx context.Context, args json.RawMessage) error {
			var v podArgs
			if err := json.Unmarshal(args, &v); err != nil {
				return err
			}
			if err := a.allowed(v.Namespace); err != nil {
				return err
			}
			if _, err := a.r.GetPod(ctx, v.Namespace, v.Name); err != nil {
				return clusterErr(err)
			}
			return nil
		},
		Activity: func(args json.RawMessage) string {
			var v podArgs
			_ = json.Unmarshal(args, &v)
			return fmt.Sprintf("Deleted pod %s/%s", v.Namespace, v.Name)
		},
		Run: func(ctx context.Context, c Call) (string, error) {
			var v podArgs
			if err := json.Unmarshal(c.Args, &v); err != nil {
				return "", err
			}
			pod, err := a.r.GetPod(ctx, v.Namespace, v.Name)
			if err != nil {
				return "", actionErr(err)
			}
			// The maintainer approved the pod that was there when the question was asked. A pod of the same name that
			// was made since (a StatefulSet does that) is another pod. The cluster dates a pod to the second, so a pod
			// of the same second as the question is refused too: refusing costs a second request, deleting the wrong
			// pod costs more.
			if !pod.Created.Before(c.RequestedAt.Truncate(time.Second)) {
				return "", ArgumentError(fmt.Sprintf("the pod %s/%s was replaced after the approval was asked for, so nothing was deleted; ask again if the new pod should be deleted",
					v.Namespace, v.Name))
			}
			if err := a.w.DeletePod(ctx, v.Namespace, v.Name); err != nil {
				return "", actionErr(err)
			}
			return fmt.Sprintf("deleted pod %s/%s: its controller makes a new one", v.Namespace, v.Name), nil
		},
	}
}

// application returns an application whose destination is in the allowlist, or the error the agent is told.
func (a *actionTools) application(ctx context.Context, name string) (kube.Application, error) {
	app, err := a.r.GetApplication(ctx, name)
	if err != nil {
		return kube.Application{}, actionErr(err)
	}
	if !a.cfg.NamespaceAllowed(app.DestinationNamespace) {
		if app.DestinationNamespace == "" {
			return kube.Application{}, ArgumentError(fmt.Sprintf("the application %s has no destination namespace, so actions on it are not allowed", name))
		}
		return kube.Application{}, ArgumentError(fmt.Sprintf("the application %s deploys to the namespace %q, where actions are not allowed; they are allowed in: %s",
			name, app.DestinationNamespace, strings.Join(a.cfg.WriteNamespaces, ", ")))
	}
	return app, nil
}

type refreshArgs struct {
	App  string `json:"app"`
	Hard bool   `json:"hard"`
}

func (a *actionTools) argoRefresh() Tool {
	return Tool{
		Name:        "argo_refresh",
		Description: "Asks Argo CD to compare an application with Git again. hard=true also drops Argo CD's cache of the manifests. This changes no workload; look at argo_apps afterwards." + waitsForApproval,
		Mutating:    true,
		Group:       GroupCluster,
		Schema:      schema(`"app":{"type":"string"},"hard":{"type":"boolean","description":"Also drop the cache. Default false."}`, "app"),
		Decode: func(raw json.RawMessage) (json.RawMessage, error) {
			var v refreshArgs
			if err := DecodeArgs(raw, &v); err != nil {
				return nil, err
			}
			if err := nameArg("app", v.App); err != nil {
				return nil, err
			}
			return json.Marshal(v)
		},
		Check: func(ctx context.Context, args json.RawMessage) error {
			var v refreshArgs
			if err := json.Unmarshal(args, &v); err != nil {
				return err
			}
			_, err := a.application(ctx, v.App)
			return err
		},
		Activity: func(args json.RawMessage) string {
			var v refreshArgs
			_ = json.Unmarshal(args, &v)
			if v.Hard {
				return "Requested a hard refresh of application " + v.App
			}
			return "Requested a refresh of application " + v.App
		},
		Run: func(ctx context.Context, c Call) (string, error) {
			var v refreshArgs
			if err := json.Unmarshal(c.Args, &v); err != nil {
				return "", err
			}
			if _, err := a.application(ctx, v.App); err != nil { // the application may have changed since the question was asked
				return "", err
			}
			if err := a.w.RefreshApplication(ctx, v.App, v.Hard); err != nil {
				return "", actionErr(err)
			}
			return fmt.Sprintf("refresh requested for application %s; argo_apps shows what Argo CD finds", v.App), nil
		},
	}
}

type syncArgs struct {
	App string `json:"app"`
}

func (a *actionTools) argoSync() Tool {
	// notRunning refuses an application that has an operation: a second sync would not start, or would replace the first.
	notRunning := func(app kube.Application) error {
		if app.Syncing {
			return ArgumentError(fmt.Sprintf("an operation is already requested or running for the application %s; wait for it (argo_apps shows it)", app.Name))
		}
		return nil
	}
	return Tool{
		Name:        "argo_sync",
		Description: "Asks Argo CD to sync an application: to apply what Git says to the cluster. It never prunes, forces or replaces. It runs in the background; look at argo_apps for the outcome." + waitsForApproval,
		Mutating:    true,
		Group:       GroupCluster,
		Schema:      schema(`"app":{"type":"string"}`, "app"),
		Decode: func(raw json.RawMessage) (json.RawMessage, error) {
			var v syncArgs
			if err := DecodeArgs(raw, &v); err != nil {
				return nil, err
			}
			if err := nameArg("app", v.App); err != nil {
				return nil, err
			}
			return json.Marshal(v)
		},
		Check: func(ctx context.Context, args json.RawMessage) error {
			var v syncArgs
			if err := json.Unmarshal(args, &v); err != nil {
				return err
			}
			app, err := a.application(ctx, v.App)
			if err != nil {
				return err
			}
			return notRunning(app)
		},
		Activity: func(args json.RawMessage) string {
			var v syncArgs
			_ = json.Unmarshal(args, &v)
			return "Requested a sync of application " + v.App
		},
		Run: func(ctx context.Context, c Call) (string, error) {
			var v syncArgs
			if err := json.Unmarshal(c.Args, &v); err != nil {
				return "", err
			}
			app, err := a.application(ctx, v.App)
			if err != nil {
				return "", err
			}
			if err := notRunning(app); err != nil {
				return "", err
			}
			if err := a.w.SyncApplication(ctx, v.App); err != nil {
				return "", actionErr(err)
			}
			return fmt.Sprintf("sync requested for application %s; it runs in the background, argo_apps shows the outcome", v.App), nil
		},
	}
}
