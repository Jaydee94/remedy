package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// listLimit is how many objects one list call asks for. A list that comes back with more is reported as truncated.
const listLimit = 500

// Listing is the answer to a list call: the items, and whether the cluster had more than the call asked for.
type Listing[T any] struct {
	Items     []T
	Truncated bool
}

// listOf is the shape of a list answer, as far as Remedy reads it.
type listOf[T any] struct {
	Items    []T `json:"items"`
	Metadata struct {
		Continue string `json:"continue"`
	} `json:"metadata"`
}

func listJSON[T any](ctx context.Context, r *Reader, path string, query url.Values) (Listing[T], error) {
	if query == nil {
		query = url.Values{}
	}
	query.Set("limit", fmt.Sprint(listLimit))
	body, err := r.do(ctx, http.MethodGet, path, query, "", nil)
	if err != nil {
		return Listing[T]{}, err
	}
	var l listOf[T]
	if err := json.Unmarshal(body, &l); err != nil {
		return Listing[T]{}, fmt.Errorf("kube: unexpected answer to %s: %w", path, err)
	}
	return Listing[T]{Items: l.Items, Truncated: l.Metadata.Continue != ""}, nil
}

func getJSON(ctx context.Context, r *Reader, path string, out any) error {
	body, err := r.do(ctx, http.MethodGet, path, nil, "", nil)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("kube: unexpected answer to %s: %w", path, err)
	}
	return nil
}

// collection is the path of a resource, in one namespace or in all of them (namespace "").
func collection(prefix, namespace, resource string) (string, error) {
	if namespace == "" {
		return prefix + "/" + resource, nil
	}
	if !dnsLabel.MatchString(namespace) {
		return "", fmt.Errorf("%w: %q is not a namespace name", ErrInvalid, namespace)
	}
	return prefix + "/namespaces/" + namespace + "/" + resource, nil
}

type rawMeta struct {
	Name              string    `json:"name"`
	Namespace         string    `json:"namespace"`
	CreationTimestamp time.Time `json:"creationTimestamp"`
	OwnerReferences   []struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"ownerReferences"`
}

// Workload is a Deployment, a StatefulSet or a DaemonSet as the list tool shows it.
type Workload struct {
	Kind      string // deployment, statefulset or daemonset
	Namespace string
	Name      string
	Desired   int
	Ready     int
	Updated   int
	Available int
	Images    []string
	Created   time.Time
}

// Healthy says whether every wanted replica is ready, updated and available.
func (w Workload) Healthy() bool {
	return w.Ready == w.Desired && w.Updated == w.Desired && w.Available == w.Desired
}

type rawWorkload struct {
	Metadata rawMeta `json:"metadata"`
	Spec     struct {
		Replicas *int `json:"replicas"`
		Template struct {
			Spec struct {
				Containers []struct {
					Image string `json:"image"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
	Status struct {
		ReadyReplicas          int `json:"readyReplicas"`
		UpdatedReplicas        int `json:"updatedReplicas"`
		AvailableReplicas      int `json:"availableReplicas"`
		DesiredNumberScheduled int `json:"desiredNumberScheduled"`
		NumberReady            int `json:"numberReady"`
		UpdatedNumberScheduled int `json:"updatedNumberScheduled"`
		NumberAvailable        int `json:"numberAvailable"`
	} `json:"status"`
}

func (raw rawWorkload) workload(kind string) Workload {
	w := Workload{Kind: kind, Namespace: raw.Metadata.Namespace, Name: raw.Metadata.Name, Created: raw.Metadata.CreationTimestamp}
	for _, c := range raw.Spec.Template.Spec.Containers {
		w.Images = append(w.Images, c.Image)
	}
	if kind == "daemonset" {
		w.Desired, w.Ready, w.Updated, w.Available = raw.Status.DesiredNumberScheduled, raw.Status.NumberReady,
			raw.Status.UpdatedNumberScheduled, raw.Status.NumberAvailable
		return w
	}
	w.Desired = 1 // what a missing spec.replicas means
	if raw.Spec.Replicas != nil {
		w.Desired = *raw.Spec.Replicas
	}
	w.Ready, w.Updated, w.Available = raw.Status.ReadyReplicas, raw.Status.UpdatedReplicas, raw.Status.AvailableReplicas
	return w
}

var workloadResources = []struct{ kind, resource string }{
	{"deployment", "deployments"}, {"statefulset", "statefulsets"}, {"daemonset", "daemonsets"},
}

// ListWorkloads lists the Deployments, StatefulSets and DaemonSets of a namespace, or of all namespaces, ordered by
// namespace and name.
func (r *Reader) ListWorkloads(ctx context.Context, namespace string) (Listing[Workload], error) {
	var out Listing[Workload]
	for _, wr := range workloadResources {
		path, err := collection("/apis/apps/v1", namespace, wr.resource)
		if err != nil {
			return Listing[Workload]{}, err
		}
		l, err := listJSON[rawWorkload](ctx, r, path, nil)
		if err != nil {
			return Listing[Workload]{}, err
		}
		for _, raw := range l.Items {
			out.Items = append(out.Items, raw.workload(wr.kind))
		}
		out.Truncated = out.Truncated || l.Truncated
	}
	slices.SortFunc(out.Items, func(a, b Workload) int {
		return strings.Compare(a.Namespace+"/"+a.Name+"/"+a.Kind, b.Namespace+"/"+b.Name+"/"+b.Kind)
	})
	return out, nil
}

var workloadResource = map[string]string{"deployment": "deployments", "statefulset": "statefulsets", "daemonset": "daemonsets"}

// GetWorkload returns one deployment, statefulset or daemonset, or ErrNotFound.
func (r *Reader) GetWorkload(ctx context.Context, kind, namespace, name string) (Workload, error) {
	resource, ok := workloadResource[kind]
	if !ok {
		return Workload{}, fmt.Errorf("%w: %q is not deployment, statefulset or daemonset", ErrInvalid, kind)
	}
	if namespace == "" {
		return Workload{}, fmt.Errorf("%w: a %s needs a namespace", ErrInvalid, kind)
	}
	if err := validName(name); err != nil {
		return Workload{}, err
	}
	path, err := collection("/apis/apps/v1", namespace, resource)
	if err != nil {
		return Workload{}, err
	}
	var raw rawWorkload
	if err := getJSON(ctx, r, path+"/"+name, &raw); err != nil {
		return Workload{}, fmt.Errorf("%s %s/%s: %w", kind, namespace, name, err)
	}
	return raw.workload(kind), nil
}

// ContainerStatus is what the cluster says about one container of a pod.
type ContainerStatus struct {
	Name         string
	Ready        bool
	Restarts     int
	State        string // running, waiting or terminated
	Reason       string // of the waiting or terminated state
	Message      string
	ExitCode     *int // of a terminated state
	LastReason   string
	LastExitCode *int // of the state before the last restart
}

type rawState struct {
	Running *struct {
		StartedAt time.Time `json:"startedAt"`
	} `json:"running"`
	Waiting *struct {
		Reason  string `json:"reason"`
		Message string `json:"message"`
	} `json:"waiting"`
	Terminated *struct {
		ExitCode int    `json:"exitCode"`
		Reason   string `json:"reason"`
		Message  string `json:"message"`
	} `json:"terminated"`
}

type rawContainerStatus struct {
	Name         string   `json:"name"`
	Ready        bool     `json:"ready"`
	RestartCount int      `json:"restartCount"`
	State        rawState `json:"state"`
	LastState    rawState `json:"lastState"`
}

func (raw rawContainerStatus) status() ContainerStatus {
	c := ContainerStatus{Name: raw.Name, Ready: raw.Ready, Restarts: raw.RestartCount}
	switch {
	case raw.State.Waiting != nil:
		c.State, c.Reason, c.Message = "waiting", raw.State.Waiting.Reason, raw.State.Waiting.Message
	case raw.State.Terminated != nil:
		code := raw.State.Terminated.ExitCode
		c.State, c.Reason, c.Message, c.ExitCode = "terminated", raw.State.Terminated.Reason, raw.State.Terminated.Message, &code
	case raw.State.Running != nil:
		c.State = "running"
	}
	if t := raw.LastState.Terminated; t != nil {
		code := t.ExitCode
		c.LastReason, c.LastExitCode = t.Reason, &code
	}
	return c
}

// Pod is what the list tool shows of a pod.
type Pod struct {
	Namespace  string
	Name       string
	Phase      string
	Reason     string // of the pod, for example Evicted
	Node       string
	Owner      string // kind/name of the controller
	Created    time.Time
	Ready      int // containers that are ready
	Total      int // containers
	Restarts   int
	Containers []ContainerStatus
}

// Problem says in a few words what is wrong with the pod, or "" when nothing is.
func (p Pod) Problem() string {
	for _, c := range p.Containers {
		switch {
		case c.State == "waiting" && c.Reason != "" && c.Reason != "ContainerCreating" && c.Reason != "PodInitializing":
			return c.Name + ": " + c.Reason
		case c.State == "terminated" && c.ExitCode != nil && *c.ExitCode != 0:
			return fmt.Sprintf("%s: %s, exit code %d", c.Name, c.Reason, *c.ExitCode)
		}
	}
	switch {
	case p.Phase == "Failed" || p.Phase == "Unknown":
		return p.Phase + " " + p.Reason
	case p.Phase == "Pending":
		return "Pending"
	case p.Phase == "Running" && p.Ready < p.Total:
		return "not ready"
	}
	return ""
}

type rawPod struct {
	Metadata rawMeta `json:"metadata"`
	Spec     struct {
		NodeName string `json:"nodeName"`
	} `json:"spec"`
	Status struct {
		Phase             string               `json:"phase"`
		Reason            string               `json:"reason"`
		ContainerStatuses []rawContainerStatus `json:"containerStatuses"`
	} `json:"status"`
}

func (raw rawPod) pod() Pod {
	p := Pod{Namespace: raw.Metadata.Namespace, Name: raw.Metadata.Name, Phase: raw.Status.Phase, Reason: raw.Status.Reason,
		Node: raw.Spec.NodeName, Created: raw.Metadata.CreationTimestamp}
	if len(raw.Metadata.OwnerReferences) > 0 {
		o := raw.Metadata.OwnerReferences[0]
		p.Owner = o.Kind + "/" + o.Name
	}
	for _, cs := range raw.Status.ContainerStatuses {
		c := cs.status()
		p.Containers = append(p.Containers, c)
		p.Total++
		if c.Ready {
			p.Ready++
		}
		p.Restarts += c.Restarts
	}
	return p
}

// GetPod returns one pod, or ErrNotFound.
func (r *Reader) GetPod(ctx context.Context, namespace, name string) (Pod, error) {
	if namespace == "" {
		return Pod{}, fmt.Errorf("%w: a pod needs a namespace", ErrInvalid)
	}
	if err := validName(name); err != nil {
		return Pod{}, err
	}
	path, err := collection("/api/v1", namespace, "pods")
	if err != nil {
		return Pod{}, err
	}
	var raw rawPod
	if err := getJSON(ctx, r, path+"/"+name, &raw); err != nil {
		return Pod{}, fmt.Errorf("pod %s/%s: %w", namespace, name, err)
	}
	return raw.pod(), nil
}

// ListPods lists the pods of a namespace, or of all namespaces, ordered by namespace and name.
func (r *Reader) ListPods(ctx context.Context, namespace string) (Listing[Pod], error) {
	path, err := collection("/api/v1", namespace, "pods")
	if err != nil {
		return Listing[Pod]{}, err
	}
	l, err := listJSON[rawPod](ctx, r, path, nil)
	if err != nil {
		return Listing[Pod]{}, err
	}
	out := Listing[Pod]{Truncated: l.Truncated}
	for _, raw := range l.Items {
		out.Items = append(out.Items, raw.pod())
	}
	slices.SortFunc(out.Items, func(a, b Pod) int { return strings.Compare(a.Namespace+"/"+a.Name, b.Namespace+"/"+b.Name) })
	return out, nil
}

// Node is what the nodes tool shows of a node.
type Node struct {
	Name          string
	Ready         string // True, False or Unknown
	Roles         []string
	Version       string
	Unschedulable bool
	Taints        []string // key=value:effect
	Problems      []string // conditions other than Ready that are true, for example MemoryPressure
	CPU           string   // allocatable
	Memory        string   // allocatable
	Pods          string   // allocatable
	Created       time.Time
}

type rawNode struct {
	Metadata struct {
		rawMeta
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		Unschedulable bool `json:"unschedulable"`
		Taints        []struct {
			Key    string `json:"key"`
			Value  string `json:"value"`
			Effect string `json:"effect"`
		} `json:"taints"`
	} `json:"spec"`
	Status struct {
		Conditions []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
		Allocatable map[string]string `json:"allocatable"`
		NodeInfo    struct {
			KubeletVersion string `json:"kubeletVersion"`
		} `json:"nodeInfo"`
	} `json:"status"`
}

func (raw rawNode) node() Node {
	n := Node{Name: raw.Metadata.Name, Ready: "Unknown", Version: raw.Status.NodeInfo.KubeletVersion,
		Unschedulable: raw.Spec.Unschedulable, Created: raw.Metadata.CreationTimestamp,
		CPU: raw.Status.Allocatable["cpu"], Memory: raw.Status.Allocatable["memory"], Pods: raw.Status.Allocatable["pods"]}
	for label := range raw.Metadata.Labels {
		if role, ok := strings.CutPrefix(label, "node-role.kubernetes.io/"); ok {
			n.Roles = append(n.Roles, role)
		}
	}
	slices.Sort(n.Roles)
	for _, t := range raw.Spec.Taints {
		taint := t.Key
		if t.Value != "" {
			taint += "=" + t.Value
		}
		n.Taints = append(n.Taints, taint+":"+t.Effect)
	}
	for _, c := range raw.Status.Conditions {
		switch {
		case c.Type == "Ready":
			n.Ready = c.Status
		case c.Status == "True":
			n.Problems = append(n.Problems, c.Type)
		}
	}
	return n
}

// ListNodes lists the nodes, ordered by name.
func (r *Reader) ListNodes(ctx context.Context) (Listing[Node], error) {
	l, err := listJSON[rawNode](ctx, r, "/api/v1/nodes", nil)
	if err != nil {
		return Listing[Node]{}, err
	}
	out := Listing[Node]{Truncated: l.Truncated}
	for _, raw := range l.Items {
		out.Items = append(out.Items, raw.node())
	}
	slices.SortFunc(out.Items, func(a, b Node) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}
