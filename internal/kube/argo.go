package kube

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Application is an Argo CD application as the cluster tool shows it.
type Application struct {
	Name                 string
	Project              string
	Sync                 string // Synced, OutOfSync or Unknown
	Health               string // Healthy, Progressing, Degraded, Suspended, Missing or Unknown
	HealthMessage        string
	Revision             string // the revision the sync status was compared to
	RepoURL              string
	Path                 string
	TargetRevision       string
	DestinationNamespace string
	DestinationServer    string
	Conditions           []ApplicationCondition
	Operation            *ApplicationOperation // the last operation, if there was one
	Syncing              bool                  // an operation is requested or running
	OutOfSync            []string              // the resources that differ from Git, as "Kind namespace/name", at most 20
}

// ApplicationCondition is a problem Argo CD reports for an application.
type ApplicationCondition struct {
	Type    string
	Message string
}

// ApplicationOperation is the state of the last operation (usually a sync).
type ApplicationOperation struct {
	Phase      string // Running, Succeeded, Failed, Error or Terminating
	Message    string
	StartedAt  time.Time
	FinishedAt time.Time
	Revision   string
}

type rawApplication struct {
	Metadata rawMeta `json:"metadata"`
	Spec     struct {
		Project string `json:"project"`
		Source  struct {
			RepoURL        string `json:"repoURL"`
			Path           string `json:"path"`
			TargetRevision string `json:"targetRevision"`
		} `json:"source"`
		Destination struct {
			Server    string `json:"server"`
			Namespace string `json:"namespace"`
		} `json:"destination"`
		Operation *struct{} `json:"operation"`
	} `json:"spec"`
	Status struct {
		Sync struct {
			Status   string `json:"status"`
			Revision string `json:"revision"`
		} `json:"sync"`
		Health struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"health"`
		Conditions []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"conditions"`
		OperationState *struct {
			Phase      string     `json:"phase"`
			Message    string     `json:"message"`
			StartedAt  *time.Time `json:"startedAt"`
			FinishedAt *time.Time `json:"finishedAt"`
			SyncResult struct {
				Revision string `json:"revision"`
			} `json:"syncResult"`
		} `json:"operationState"`
		Resources []struct {
			Group     string `json:"group"`
			Kind      string `json:"kind"`
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
			Status    string `json:"status"`
		} `json:"resources"`
	} `json:"status"`
}

const maxOutOfSync = 20

func (raw rawApplication) application() Application {
	a := Application{
		Name: raw.Metadata.Name, Project: raw.Spec.Project, Sync: raw.Status.Sync.Status, Health: raw.Status.Health.Status,
		HealthMessage: raw.Status.Health.Message, Revision: raw.Status.Sync.Revision,
		RepoURL: raw.Spec.Source.RepoURL, Path: raw.Spec.Source.Path, TargetRevision: raw.Spec.Source.TargetRevision,
		DestinationNamespace: raw.Spec.Destination.Namespace, DestinationServer: raw.Spec.Destination.Server,
		Syncing: raw.Spec.Operation != nil,
	}
	for _, c := range raw.Status.Conditions {
		a.Conditions = append(a.Conditions, ApplicationCondition{Type: c.Type, Message: c.Message})
	}
	if op := raw.Status.OperationState; op != nil {
		o := &ApplicationOperation{Phase: op.Phase, Message: op.Message, Revision: op.SyncResult.Revision}
		if op.StartedAt != nil {
			o.StartedAt = *op.StartedAt
		}
		if op.FinishedAt != nil {
			o.FinishedAt = *op.FinishedAt
		}
		a.Operation = o
		if op.Phase == "Running" || op.Phase == "Terminating" {
			a.Syncing = true
		}
	}
	for _, res := range raw.Status.Resources {
		if res.Status != "OutOfSync" || len(a.OutOfSync) >= maxOutOfSync {
			continue
		}
		name := res.Name
		if res.Namespace != "" {
			name = res.Namespace + "/" + res.Name
		}
		a.OutOfSync = append(a.OutOfSync, res.Kind+" "+name)
	}
	return a
}

func (r *Reader) applicationsPath() string {
	return "/apis/argoproj.io/v1alpha1/namespaces/" + r.argo + "/applications"
}

// ListApplications lists the Argo CD applications, ordered by name.
func (r *Reader) ListApplications(ctx context.Context) (Listing[Application], error) {
	l, err := listJSON[rawApplication](ctx, r, r.applicationsPath(), nil)
	if err != nil {
		return Listing[Application]{}, err
	}
	out := Listing[Application]{Truncated: l.Truncated}
	for _, raw := range l.Items {
		out.Items = append(out.Items, raw.application())
	}
	slices.SortFunc(out.Items, func(a, b Application) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// GetApplication returns one Argo CD application, or ErrNotFound.
func (r *Reader) GetApplication(ctx context.Context, name string) (Application, error) {
	if err := validName(name); err != nil {
		return Application{}, err
	}
	var raw rawApplication
	if err := getJSON(ctx, r, r.applicationsPath()+"/"+name, &raw); err != nil {
		return Application{}, fmt.Errorf("application %s: %w", name, err)
	}
	return raw.application(), nil
}
