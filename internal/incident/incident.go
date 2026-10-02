// Package incident turns observations of CI checks into incidents and keeps their lifecycle. It
// knows nothing about GitHub: the poller hands it observations.
package incident

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Jaydee94/remedy/internal/store"
)

// Reasons an incident was resolved.
const (
	ReasonGreen    = "green"
	ReasonPRClosed = "pr_closed"
)

// ErrNotActive is returned when an incident cannot be changed because it is resolved or ignored.
var ErrNotActive = errors.New("incident is already resolved or ignored")

// Class says what a check run means for an incident. The values are ordered by severity: when
// several check runs share a key, the highest wins.
type Class int

const (
	// Green is a finished check that passed (success, neutral, skipped).
	Green Class = iota
	// Pending is a check that is not finished or has a result Remedy does not act on.
	Pending
	// Bad is a finished check that failed (failure, timed_out, startup_failure, cancelled, action_required).
	Bad
)

// Classify maps the status and conclusion of a GitHub check run to a Class. Anything unknown, such
// as "stale" or a conclusion GitHub adds later, is Pending: Remedy waits for a clear result.
func Classify(status, conclusion string) Class {
	if status != "completed" {
		return Pending
	}
	switch conclusion {
	case "success", "neutral", "skipped":
		return Green
	case "failure", "timed_out", "startup_failure", "cancelled", "action_required":
		return Bad
	}
	return Pending
}

// Observation is the state of one check on one ref, as seen by the poller.
type Observation struct {
	RepoID     int64
	RepoName   string
	Ref        string // "pr:<number>" or "branch:<name>"
	RefURL     string
	CheckName  string
	Class      Class
	Conclusion string
	HeadSHA    string
	URL        string
}

type Engine struct{ Store *store.Store }

// Observe applies one observation. It is idempotent: observing the same state again changes
// nothing except last_seen.
func (e *Engine) Observe(ctx context.Context, o Observation) error {
	if o.Class == Pending {
		return nil
	}
	cur, err := e.Store.FindActiveIncident(ctx, o.RepoID, o.Ref, o.CheckName)
	if errors.Is(err, store.ErrNotFound) {
		if o.Class == Green {
			return nil
		}
		_, err := e.Store.OpenIncident(ctx, store.NewIncident{
			RepoID: o.RepoID, Ref: o.Ref, RefURL: o.RefURL, CheckName: o.CheckName,
			Conclusion: o.Conclusion, HeadSHA: o.HeadSHA, CheckURL: o.URL,
		}, store.NewActivity{
			Kind:    store.KindIncidentOpened,
			Summary: fmt.Sprintf("%s %s on %s in %s", o.CheckName, verb(o.Conclusion), refLabel(o.Ref), o.RepoName),
			Data:    payload(o),
		})
		if errors.Is(err, store.ErrExists) {
			return nil // another writer was faster; the next cycle sees its incident
		}
		return err
	}
	if err != nil {
		return err
	}

	var change error
	switch {
	case o.Class == Green:
		change = e.Store.ResolveIncident(ctx, cur.ID, ReasonGreen, store.NewActivity{
			Kind:    store.KindIncidentResolved,
			RepoID:  cur.RepoID,
			Summary: fmt.Sprintf("%s is green again on %s in %s", cur.CheckName, refLabel(cur.Ref), cur.RepoName),
			Data:    payload(o),
		})
	case cur.State == store.IncIgnored:
		return nil
	case cur.HeadSHA == o.HeadSHA:
		change = e.Store.TouchIncident(ctx, cur.ID)
	default:
		change = e.Store.RecordRecurrence(ctx, cur.ID, o.Conclusion, o.HeadSHA, o.URL, store.NewActivity{
			Kind:   store.KindIncidentRecurred,
			RepoID: cur.RepoID,
			Summary: fmt.Sprintf("%s %s again on %s in %s (commit %s)",
				cur.CheckName, verb(o.Conclusion), refLabel(cur.Ref), cur.RepoName, short(o.HeadSHA)),
			Data: payload(o),
		})
	}
	if errors.Is(change, store.ErrNotFound) {
		return nil // resolved by someone else in the meantime
	}
	return change
}

// ResolveClosedPRs resolves the active incidents of pull requests that are no longer open.
// openPRs holds the numbers of the pull requests that are open right now.
func (e *Engine) ResolveClosedPRs(ctx context.Context, repoID int64, openPRs map[int]bool) error {
	active, err := e.Store.ListActiveIncidents(ctx, repoID)
	if err != nil {
		return err
	}
	for _, in := range active {
		rest, ok := strings.CutPrefix(in.Ref, "pr:")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(rest)
		if err != nil || openPRs[n] {
			continue
		}
		err = e.Store.ResolveIncident(ctx, in.ID, ReasonPRClosed, store.NewActivity{
			Kind:   store.KindIncidentResolved,
			RepoID: in.RepoID,
			Summary: fmt.Sprintf("%s in %s was closed or merged; the incident for %s is resolved",
				refLabel(in.Ref), in.RepoName, in.CheckName),
			Data: payload(Observation{Ref: in.Ref, CheckName: in.CheckName, Conclusion: in.Conclusion, HeadSHA: in.HeadSHA, URL: in.CheckURL}),
		})
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	return nil
}

// Ignore moves an incident to ignored. It returns store.ErrNotFound for an unknown incident and
// ErrNotActive for one that is resolved or already ignored.
func (e *Engine) Ignore(ctx context.Context, id int64) (store.Incident, error) {
	cur, err := e.Store.GetIncident(ctx, id)
	if err != nil {
		return store.Incident{}, err
	}
	if cur.State == store.IncResolved || cur.State == store.IncIgnored {
		return store.Incident{}, ErrNotActive
	}
	err = e.Store.IgnoreIncident(ctx, id, store.NewActivity{
		Kind:    store.KindIncidentIgnored,
		RepoID:  cur.RepoID,
		Summary: fmt.Sprintf("Ignored the incident for %s on %s in %s", cur.CheckName, refLabel(cur.Ref), cur.RepoName),
	})
	if errors.Is(err, store.ErrNotFound) {
		return store.Incident{}, ErrNotActive
	}
	if err != nil {
		return store.Incident{}, err
	}
	return e.Store.GetIncident(ctx, id)
}

func refLabel(ref string) string {
	if n, ok := strings.CutPrefix(ref, "pr:"); ok {
		return "PR #" + n
	}
	if b, ok := strings.CutPrefix(ref, "branch:"); ok {
		return "branch " + b
	}
	return ref
}

func verb(conclusion string) string {
	switch conclusion {
	case "failure":
		return "failed"
	case "timed_out":
		return "timed out"
	case "startup_failure":
		return "failed to start"
	case "cancelled":
		return "was cancelled"
	case "action_required":
		return "needs action"
	}
	return conclusion
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// payload is the machine-readable part of an activity entry. It never contains more than names,
// SHAs and links that GitHub itself shows.
func payload(o Observation) json.RawMessage {
	b, _ := json.Marshal(map[string]string{
		"checkName": o.CheckName, "ref": o.Ref, "conclusion": o.Conclusion, "headSha": o.HeadSHA, "url": o.URL,
	})
	return b
}
