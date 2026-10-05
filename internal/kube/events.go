package kube

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Event is an event of the cluster: something that happened to an object.
type Event struct {
	Type      string // Normal or Warning
	Reason    string
	Message   string
	Count     int
	Kind      string // of the object it is about
	Namespace string
	Name      string
	First     time.Time
	Last      time.Time
	Component string
}

type rawEvent struct {
	Type           string     `json:"type"`
	Reason         string     `json:"reason"`
	Message        string     `json:"message"`
	Count          int        `json:"count"`
	FirstTimestamp *time.Time `json:"firstTimestamp"`
	LastTimestamp  *time.Time `json:"lastTimestamp"`
	EventTime      *time.Time `json:"eventTime"`
	Metadata       rawMeta    `json:"metadata"`
	InvolvedObject struct {
		Kind      string `json:"kind"`
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	} `json:"involvedObject"`
	Source struct {
		Component string `json:"component"`
	} `json:"source"`
	ReportingComponent string `json:"reportingComponent"`
}

func (raw rawEvent) event() Event {
	e := Event{Type: raw.Type, Reason: raw.Reason, Message: raw.Message, Count: max(raw.Count, 1),
		Kind: raw.InvolvedObject.Kind, Namespace: raw.InvolvedObject.Namespace, Name: raw.InvolvedObject.Name,
		Component: raw.Source.Component}
	if e.Component == "" {
		e.Component = raw.ReportingComponent
	}
	// An event has its times in one of three places, depending on who wrote it.
	for _, t := range []*time.Time{raw.LastTimestamp, raw.EventTime} {
		if t != nil && !t.IsZero() {
			e.Last = *t
			break
		}
	}
	if raw.FirstTimestamp != nil {
		e.First = *raw.FirstTimestamp
	}
	if e.Last.IsZero() {
		e.Last = e.First
	}
	if e.Last.IsZero() {
		e.Last = raw.Metadata.CreationTimestamp
	}
	if e.First.IsZero() {
		e.First = e.Last
	}
	return e
}

// EventFilter says which events to list.
type EventFilter struct {
	Namespace    string // empty means all namespaces
	Kind         string // of the object the events are about, for example Pod; empty means any
	Name         string // of that object; empty means any
	WarningsOnly bool
	Limit        int // at most this many, the newest first; 0 means 50
}

const defaultEventLimit = 50

// ListEvents lists events, the newest first. The cluster does not sort them: the call reads up to 500 and sorts here.
func (r *Reader) ListEvents(ctx context.Context, f EventFilter) (Listing[Event], error) {
	path, err := collection("/api/v1", f.Namespace, "events")
	if err != nil {
		return Listing[Event]{}, err
	}
	var selectors []string
	if f.WarningsOnly {
		selectors = append(selectors, "type=Warning")
	}
	if f.Kind != "" {
		if !objectName.MatchString(strings.ToLower(f.Kind)) {
			return Listing[Event]{}, fmt.Errorf("%w: %q is not a kind", ErrInvalid, f.Kind)
		}
		selectors = append(selectors, "involvedObject.kind="+f.Kind)
	}
	if f.Name != "" {
		if err := validName(f.Name); err != nil {
			return Listing[Event]{}, err
		}
		selectors = append(selectors, "involvedObject.name="+f.Name)
	}
	query := url.Values{}
	if len(selectors) > 0 {
		query.Set("fieldSelector", strings.Join(selectors, ","))
	}
	l, err := listJSON[rawEvent](ctx, r, path, query)
	if err != nil {
		return Listing[Event]{}, err
	}
	out := Listing[Event]{Truncated: l.Truncated}
	for _, raw := range l.Items {
		out.Items = append(out.Items, raw.event())
	}
	slices.SortStableFunc(out.Items, func(a, b Event) int { return b.Last.Compare(a.Last) })
	limit := f.Limit
	if limit <= 0 {
		limit = defaultEventLimit
	}
	if len(out.Items) > limit {
		out.Items, out.Truncated = out.Items[:limit], true
	}
	return out, nil
}

// maxLogBytes bounds what the cluster sends of a log. The tool cuts it further.
const maxLogBytes = 64 << 10

// GetPodLog returns the last lines of a container's log. A container that has none, or whose previous log is gone, is
// not an error: the cluster answers with a text that says so, and that text is returned.
func (r *Reader) GetPodLog(ctx context.Context, namespace, pod, container string, previous bool, tailLines int) (string, error) {
	if !dnsLabel.MatchString(namespace) {
		return "", fmt.Errorf("%w: %q is not a namespace name", ErrInvalid, namespace)
	}
	if err := validName(pod); err != nil {
		return "", err
	}
	query := url.Values{"tailLines": {fmt.Sprint(max(tailLines, 1))}, "limitBytes": {fmt.Sprint(maxLogBytes)}}
	if container != "" {
		if err := validName(container); err != nil {
			return "", err
		}
		query.Set("container", container)
	}
	if previous {
		query.Set("previous", "true")
	}
	body, err := r.do(ctx, http.MethodGet, "/api/v1/namespaces/"+namespace+"/pods/"+pod+"/log", query, "", nil)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
