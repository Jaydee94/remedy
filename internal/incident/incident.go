// Package incident turns observations (of a CI check, an alert, an Argo CD application) into incidents and keeps their
// lifecycle. It does not fetch anything: a poller hands it observations.
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
	// ReasonCleared is the reason for an incident of another source: the signal is no longer reported.
	ReasonCleared = "cleared"
)

// ErrNotActive is returned when an incident cannot be changed because it is resolved or ignored.
var ErrNotActive = errors.New("incident is already resolved or ignored")

// ErrNotIgnored is returned when an incident cannot be un-ignored because it is not ignored.
var ErrNotIgnored = errors.New("incident is not ignored")

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

// Observation is the state of one signal, as seen by a poller: a check on a ref, an alert, an application.
//
// An empty Source means GitHub. The incident is then named by RepoID, Ref and CheckName, and Key, Title, Severity,
// AutoDiagnose and Details are not used (the store derives them). Any other source names its incident with Key and says in
// Title what the list shows; Class says whether it is there (Bad), gone (Green) or not decided yet (Pending).
type Observation struct {
	Source       string
	Key          string
	Title        string
	Severity     string
	AutoDiagnose bool
	Details      json.RawMessage

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
	source, key := o.Source, o.Key
	if source == "" {
		source, key = store.SourceGitHub, store.GitHubKey(o.RepoID, o.Ref, o.CheckName)
	}
	if key == "" {
		return fmt.Errorf("an observation of %s needs a key", source)
	}
	cur, err := e.Store.FindActiveIncidentByKey(ctx, source, key)
	if errors.Is(err, store.ErrNotFound) {
		if o.Class == Green {
			return nil
		}
		_, err := e.Store.OpenIncident(ctx, store.NewIncident{
			Source: source, Key: o.Key, Title: o.Title, Severity: o.Severity, AutoDiagnose: o.AutoDiagnose, Details: o.Details,
			RepoID: o.RepoID, Ref: o.Ref, RefURL: o.RefURL, CheckName: o.CheckName,
			Conclusion: o.Conclusion, HeadSHA: o.HeadSHA, CheckURL: o.URL,
		}, store.NewActivity{
			Kind:    store.KindIncidentOpened,
			Summary: openedText(source, o),
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
		reason := ReasonCleared
		if cur.Source == store.SourceGitHub {
			reason = ReasonGreen
		}
		change = e.Store.ResolveIncident(ctx, cur.ID, reason, store.NewActivity{
			Kind:    store.KindIncidentResolved,
			RepoID:  cur.RepoID,
			Summary: resolvedText(cur),
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
		Summary: ignoredText(cur),
	})
	if errors.Is(err, store.ErrNotFound) {
		return store.Incident{}, ErrNotActive
	}
	if err != nil {
		return store.Incident{}, err
	}
	return e.Store.GetIncident(ctx, id)
}

// Unignore moves an ignored incident back. It returns store.ErrNotFound for an unknown incident and ErrNotIgnored for one
// that is not ignored, which includes one that turned green while it was ignored.
func (e *Engine) Unignore(ctx context.Context, id int64) (store.Incident, error) {
	cur, err := e.Store.GetIncident(ctx, id)
	if err != nil {
		return store.Incident{}, err
	}
	if cur.State != store.IncIgnored {
		return store.Incident{}, ErrNotIgnored
	}
	err = e.Store.UnignoreIncident(ctx, id, store.NewActivity{
		Kind:    store.KindIncidentUnignored,
		RepoID:  cur.RepoID,
		Summary: unignoredText(cur),
	})
	if errors.Is(err, store.ErrNotFound) {
		return store.Incident{}, ErrNotIgnored // another writer was faster
	}
	if err != nil {
		return store.Incident{}, err
	}
	return e.Store.GetIncident(ctx, id)
}

// noun is what a source's incident is called in a sentence.
func noun(source string) string {
	switch source {
	case store.SourceAlertmanager:
		return "alert"
	case store.SourceArgoCD:
		return "Argo CD application"
	}
	return source
}

// what names the thing an incident of another source is about: "alert HighLatency api".
func what(source, title string) string { return noun(source) + " " + title }

func openedText(source string, o Observation) string {
	if source == store.SourceGitHub {
		return fmt.Sprintf("%s %s on %s in %s", o.CheckName, verb(o.Conclusion), refLabel(o.Ref), o.RepoName)
	}
	return "Incident opened: " + what(source, o.Title)
}

func resolvedText(cur store.Incident) string {
	if cur.Source == store.SourceGitHub {
		return fmt.Sprintf("%s is green again on %s in %s", cur.CheckName, refLabel(cur.Ref), cur.RepoName)
	}
	return "Incident resolved: " + what(cur.Source, cur.Title) + " is no longer reported"
}

func ignoredText(cur store.Incident) string {
	if cur.Source == store.SourceGitHub {
		return fmt.Sprintf("Ignored the incident for %s on %s in %s", cur.CheckName, refLabel(cur.Ref), cur.RepoName)
	}
	return "Ignored the incident for " + what(cur.Source, cur.Title)
}

func unignoredText(cur store.Incident) string {
	if cur.Source == store.SourceGitHub {
		return fmt.Sprintf("Stopped ignoring the incident for %s on %s in %s", cur.CheckName, refLabel(cur.Ref), cur.RepoName)
	}
	return "Stopped ignoring the incident for " + what(cur.Source, cur.Title)
}

// RefLabel is how a ref reads in a sentence: "PR #7" or "branch main".
func RefLabel(ref string) string { return refLabel(ref) }

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
// SHAs and links that the source itself shows.
func payload(o Observation) json.RawMessage {
	if o.Source != "" && o.Source != store.SourceGitHub {
		b, _ := json.Marshal(map[string]string{
			"source": o.Source, "key": o.Key, "title": o.Title, "severity": o.Severity, "conclusion": o.Conclusion, "url": o.URL,
		})
		return b
	}
	b, _ := json.Marshal(map[string]string{
		"checkName": o.CheckName, "ref": o.Ref, "conclusion": o.Conclusion, "headSha": o.HeadSHA, "url": o.URL,
	})
	return b
}
