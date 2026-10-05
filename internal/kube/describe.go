package kube

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Description is the excerpt of one object that the describe tool shows: what Remedy decided an agent may see of it,
// and the events that are about it.
//
// What is left out on purpose: the values of environment variables (a value is often a secret; only the names are
// shown), annotations (the last-applied-configuration annotation holds the whole manifest, clear text and all),
// managed fields, and anything about volumes beyond their names and kinds.
type Description struct {
	Kind      string
	Namespace string
	Name      string
	Created   time.Time
	Labels    map[string]string
	Spec      map[string]any
	Status    map[string]any
	Events    []Event
}

type objectMap = map[string]any

type describedKind struct {
	prefix     string // API path up to the resource
	resource   string
	namespaced bool
	eventKind  string
	project    func(o objectMap) (spec, status objectMap)
}

var describedKinds = map[string]describedKind{
	"deployment":  {"/apis/apps/v1", "deployments", true, "Deployment", projectDeployment},
	"statefulset": {"/apis/apps/v1", "statefulsets", true, "StatefulSet", projectStatefulSet},
	"daemonset":   {"/apis/apps/v1", "daemonsets", true, "DaemonSet", projectDaemonSet},
	"pod":         {"/api/v1", "pods", true, "Pod", projectPod},
	"node":        {"/api/v1", "nodes", false, "Node", projectNode},
}

// DescribedKinds are the kinds GetDescription knows.
var DescribedKinds = []string{"deployment", "statefulset", "daemonset", "pod", "node"}

// GetDescription describes one deployment, statefulset, daemonset, pod or node. For a node the namespace is ignored.
func (r *Reader) GetDescription(ctx context.Context, kind, namespace, name string) (Description, error) {
	k, ok := describedKinds[kind]
	if !ok {
		return Description{}, fmt.Errorf("%w: %q is not one of %s", ErrInvalid, kind, strings.Join(DescribedKinds, ", "))
	}
	if err := validName(name); err != nil {
		return Description{}, err
	}
	path := k.prefix + "/" + k.resource + "/" + name
	if k.namespaced {
		var err error
		if path, err = collection(k.prefix, namespace, k.resource); err != nil {
			return Description{}, err
		}
		if namespace == "" {
			return Description{}, fmt.Errorf("%w: a %s needs a namespace", ErrInvalid, kind)
		}
		path += "/" + name
	}
	var obj objectMap
	if err := getJSON(ctx, r, path, &obj); err != nil {
		return Description{}, fmt.Errorf("%s %s: %w", kind, name, err)
	}
	d := Description{Kind: kind, Namespace: str(obj, "metadata", "namespace"), Name: str(obj, "metadata", "name")}
	d.Created, _ = time.Parse(time.RFC3339, str(obj, "metadata", "creationTimestamp"))
	if labels, ok := get(obj, "metadata", "labels").(objectMap); ok {
		d.Labels = map[string]string{}
		for key, v := range labels {
			if s, ok := v.(string); ok {
				d.Labels[key] = s
			}
		}
	}
	d.Spec, d.Status = k.project(obj)

	events, err := r.ListEvents(ctx, EventFilter{Namespace: d.Namespace, Kind: k.eventKind, Name: name, Limit: 20})
	if err != nil {
		return Description{}, err
	}
	d.Events = events.Items
	return d, nil
}

// get follows a path of keys through nested objects. It returns nil when the path is not there.
func get(o objectMap, keys ...string) any {
	var cur any = o
	for _, k := range keys {
		m, ok := cur.(objectMap)
		if !ok {
			return nil
		}
		cur = m[k]
	}
	return cur
}

func str(o objectMap, keys ...string) string {
	s, _ := get(o, keys...).(string)
	return s
}

func items(o objectMap, keys ...string) []objectMap {
	var out []objectMap
	list, _ := get(o, keys...).([]any)
	for _, v := range list {
		if m, ok := v.(objectMap); ok {
			out = append(out, m)
		}
	}
	return out
}

// pick copies the listed keys of o that are there.
func pick(o objectMap, keys ...string) objectMap {
	out := objectMap{}
	for _, k := range keys {
		if v, ok := o[k]; ok && v != nil {
			out[k] = v
		}
	}
	return out
}

func conditions(o objectMap, keys ...string) []objectMap {
	var out []objectMap
	for _, c := range items(o, keys...) {
		out = append(out, pick(c, "type", "status", "reason", "message", "lastTransitionTime"))
	}
	return out
}

// containerSpec is what is shown of a container's specification.
func containerSpec(c objectMap) objectMap {
	out := pick(c, "name", "image", "imagePullPolicy", "command", "args", "resources")
	var ports []objectMap
	for _, p := range items(c, "ports") {
		ports = append(ports, pick(p, "name", "containerPort", "protocol"))
	}
	if len(ports) > 0 {
		out["ports"] = ports
	}
	// Names only. A value is often a secret, and a value taken from a Secret or a ConfigMap is not looked up.
	var env []string
	for _, e := range items(c, "env") {
		if n, ok := e["name"].(string); ok {
			env = append(env, n)
		}
	}
	if len(env) > 0 {
		out["envNames"] = env
	}
	var from []string
	for _, e := range items(c, "envFrom") {
		for _, kind := range []string{"configMapRef", "secretRef"} {
			if name := str(e, kind, "name"); name != "" {
				from = append(from, kind+" "+name)
			}
		}
	}
	if len(from) > 0 {
		out["envFrom"] = from
	}
	var mounts []objectMap
	for _, m := range items(c, "volumeMounts") {
		mounts = append(mounts, pick(m, "name", "mountPath", "readOnly"))
	}
	if len(mounts) > 0 {
		out["volumeMounts"] = mounts
	}
	for _, probe := range []string{"livenessProbe", "readinessProbe", "startupProbe"} {
		if p, ok := c[probe].(objectMap); ok {
			out[probe] = pick(p, "httpGet", "tcpSocket", "exec", "grpc", "initialDelaySeconds", "periodSeconds", "timeoutSeconds",
				"successThreshold", "failureThreshold")
		}
	}
	return out
}

func containerSpecs(o objectMap, keys ...string) []objectMap {
	var out []objectMap
	for _, c := range items(o, keys...) {
		out = append(out, containerSpec(c))
	}
	return out
}

// podSpec is the part of a pod specification that is shown, from a pod or from a workload's template.
func podSpec(spec objectMap, ps ...string) objectMap {
	out := objectMap{}
	if c := containerSpecs(spec, append(ps, "containers")...); len(c) > 0 {
		out["containers"] = c
	}
	if c := containerSpecs(spec, append(ps, "initContainers")...); len(c) > 0 {
		out["initContainers"] = c
	}
	return out
}

func mergeInto(dst, src objectMap) {
	for k, v := range src {
		dst[k] = v
	}
}

func projectDeployment(o objectMap) (objectMap, objectMap) {
	spec := pick(objectMapOr(o, "spec"), "replicas", "selector", "minReadySeconds", "progressDeadlineSeconds")
	if s := objectMapOr(o, "spec", "strategy"); len(s) > 0 {
		spec["strategy"] = pick(s, "type", "rollingUpdate")
	}
	mergeInto(spec, podSpec(o, "spec", "template", "spec"))
	status := pick(objectMapOr(o, "status"), "replicas", "readyReplicas", "updatedReplicas", "availableReplicas", "unavailableReplicas")
	status["conditions"] = conditions(o, "status", "conditions")
	return spec, status
}

func projectStatefulSet(o objectMap) (objectMap, objectMap) {
	spec := pick(objectMapOr(o, "spec"), "replicas", "serviceName", "selector", "podManagementPolicy", "updateStrategy")
	mergeInto(spec, podSpec(o, "spec", "template", "spec"))
	status := pick(objectMapOr(o, "status"), "replicas", "readyReplicas", "updatedReplicas", "availableReplicas", "currentRevision", "updateRevision")
	status["conditions"] = conditions(o, "status", "conditions")
	return spec, status
}

func projectDaemonSet(o objectMap) (objectMap, objectMap) {
	spec := pick(objectMapOr(o, "spec"), "selector", "updateStrategy")
	mergeInto(spec, podSpec(o, "spec", "template", "spec"))
	status := pick(objectMapOr(o, "status"), "desiredNumberScheduled", "currentNumberScheduled", "numberReady",
		"updatedNumberScheduled", "numberAvailable", "numberUnavailable", "numberMisscheduled")
	status["conditions"] = conditions(o, "status", "conditions")
	return spec, status
}

// volumeKinds turns the volumes of a pod into "name (kind)": what is mounted, never what is in it.
func volumeKinds(o objectMap) []string {
	var out []string
	for _, v := range items(o, "spec", "volumes") {
		name, _ := v["name"].(string)
		kind := "other"
		for k := range v {
			if k != "name" {
				kind = k
			}
		}
		out = append(out, name+" ("+kind+")")
	}
	return out
}

func projectPod(o objectMap) (objectMap, objectMap) {
	spec := pick(objectMapOr(o, "spec"), "nodeName", "serviceAccountName", "restartPolicy", "priorityClassName", "nodeSelector", "tolerations")
	mergeInto(spec, podSpec(o, "spec"))
	if v := volumeKinds(o); len(v) > 0 {
		spec["volumes"] = v
	}
	status := pick(objectMapOr(o, "status"), "phase", "reason", "message", "startTime", "qosClass")
	status["conditions"] = conditions(o, "status", "conditions")
	for _, key := range []string{"containerStatuses", "initContainerStatuses"} {
		var list []objectMap
		for _, cs := range items(o, "status", key) {
			item := pick(cs, "name", "ready", "restartCount", "started", "state", "lastState")
			list = append(list, item)
		}
		if len(list) > 0 {
			status[key] = list
		}
	}
	return spec, status
}

func projectNode(o objectMap) (objectMap, objectMap) {
	spec := pick(objectMapOr(o, "spec"), "unschedulable", "taints")
	status := pick(objectMapOr(o, "status"), "capacity", "allocatable")
	status["conditions"] = conditions(o, "status", "conditions")
	if info := objectMapOr(o, "status", "nodeInfo"); len(info) > 0 {
		status["nodeInfo"] = pick(info, "kubeletVersion", "osImage", "kernelVersion", "containerRuntimeVersion", "architecture")
	}
	return spec, status
}

func objectMapOr(o objectMap, keys ...string) objectMap {
	m, _ := get(o, keys...).(objectMap)
	if m == nil {
		return objectMap{}
	}
	return m
}
