// Package responder starts and closes the read-only diagnosis of an incident: it reads the failing run
// from GitHub, builds the prompt, starts the run under the limits, opens the repository snapshot for the
// runner, judges the answer and decides on automatic starts. It does no HTTP of its own.
package responder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Jaydee94/remedy/internal/diagnosis"
	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/prompt"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
)

var (
	// ErrNoConnection means there is no GitHub token that can be used: none stored, not "ok", or it does not
	// open with the master key, or GitHub rejected it.
	ErrNoConnection = errors.New("GitHub is not connected, or the stored token cannot be used")
	// ErrGitHub means a read from GitHub failed.
	ErrGitHub = errors.New("could not read the failing run from GitHub")
	// ErrSourceNotSupported means the incident does not come from GitHub, and the responder handles only those until
	// plan 2d-3.
	ErrSourceNotSupported = errors.New("the diagnosis of an incident from this source is not available yet")
	// ErrNotSnapshotRun means the run is not a running responder run, so it has no snapshot.
	ErrNotSnapshotRun = errors.New("the run does not take a snapshot")
)

// Source is what the responder reads from GitHub. *github.Client implements it.
type Source interface {
	GetPR(ctx context.Context, fullName string, number int) (github.PullRequest, error)
	ListPRFiles(ctx context.Context, fullName string, number int) ([]github.PRFile, error)
	ListCheckRuns(ctx context.Context, fullName, ref string) ([]github.CheckRun, error)
	GetJobLogs(ctx context.Context, fullName string, jobID int64) (text string, truncated bool, err error)
	GetTarball(ctx context.Context, fullName, ref string) (io.ReadCloser, error)
}

type Responder struct {
	Store     *store.Store
	Key       secret.Key
	NewSource func(token secret.Value) Source
	Provider  string // the provider that runs diagnoses, default "claude"
	Limits    store.DiagnosisLimits
	Log       *slog.Logger
	Now       func() time.Time // default time.Now

	mu      sync.Mutex
	backoff map[int64]time.Time // automatic starts that could not read GitHub: do not retry before
}

func (r *Responder) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Responder) provider() string {
	if r.Provider != "" {
		return r.Provider
	}
	return "claude"
}

// source builds a GitHub source from the stored token.
func (r *Responder) source(ctx context.Context) (Source, error) {
	conn, err := r.Store.GetConnection(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNoConnection
	}
	if err != nil {
		return nil, err
	}
	if conn.Status != store.ConnOK {
		return nil, ErrNoConnection
	}
	raw, err := r.Key.Open(conn.TokenCiphertext, store.ConnectionAAD())
	if err != nil {
		return nil, ErrNoConnection
	}
	return r.NewSource(secret.NewValue(string(raw))), nil
}

// githubError turns an error of a GitHub read into the responder's errors.
func githubError(err error) error {
	if errors.Is(err, github.ErrUnauthorized) {
		return ErrNoConnection
	}
	return fmt.Errorf("%w: %v", ErrGitHub, err)
}

// Start begins a manual diagnosis. It ignores the cooldown, the cap and the daily limit, but not "one run
// at a time".
func (r *Responder) Start(ctx context.Context, incidentID int64) (run.Run, error) {
	return r.start(ctx, incidentID, false)
}

func (r *Responder) start(ctx context.Context, incidentID int64, automatic bool) (run.Run, error) {
	in, err := r.Store.GetIncident(ctx, incidentID)
	if err != nil {
		return run.Run{}, err
	}
	// Refuse before any network traffic what the store would refuse anyway.
	if in.State != store.IncOpen && in.State != store.IncDiagnosed {
		return run.Run{}, store.ErrNotDiagnosable
	}
	if in.Source != store.SourceGitHub {
		return run.Run{}, ErrSourceNotSupported
	}
	if busy, err := r.Store.HasActiveRun(ctx); err != nil {
		return run.Run{}, err
	} else if busy {
		return run.Run{}, store.ErrBusy
	}

	src, err := r.source(ctx)
	if err != nil {
		return run.Run{}, err
	}
	text, err := r.buildPrompt(ctx, src, in)
	if err != nil {
		return run.Run{}, err
	}

	how := "by a click"
	if automatic {
		how = "automatically"
	}
	return r.Store.StartDiagnosis(ctx, store.StartParams{
		IncidentID: in.ID, Provider: r.provider(), Prompt: text, HeadSHA: in.HeadSHA,
		Automatic: automatic, Limits: r.Limits, Now: r.now(),
	}, store.NewActivity{
		Kind:    store.KindDiagnosisStarted,
		Summary: fmt.Sprintf("Diagnosis of %s started %s", label(in), how),
	})
}

func label(in store.Incident) string {
	return fmt.Sprintf("%s on %s in %s", in.CheckName, incident.RefLabel(in.Ref), in.RepoName)
}

func prNumber(ref string) (int, bool) {
	rest, ok := strings.CutPrefix(ref, "pr:")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil && n > 0
}

// buildPrompt reads what a human would read and hands it to prompt.Build. A failed read stops the start,
// except for "this check has no log" and "the log expired", which the prompt says.
func (r *Responder) buildPrompt(ctx context.Context, src Source, in store.Incident) (string, error) {
	input := prompt.Input{
		Repo: in.RepoName, Ref: in.Ref, HeadSHA: in.HeadSHA, Conclusion: in.Conclusion, CheckName: in.CheckName,
	}

	if n, ok := prNumber(in.Ref); ok {
		pr, err := src.GetPR(ctx, in.RepoName, n)
		if err != nil {
			return "", githubError(err)
		}
		files, err := src.ListPRFiles(ctx, in.RepoName, n)
		if err != nil {
			return "", githubError(err)
		}
		input.PRTitle, input.PRBody, input.PRAuthor = pr.Title, pr.Body, pr.User.Login
		for _, f := range files {
			input.Files = append(input.Files, prompt.File{Name: f.Filename, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions, Patch: f.Patch})
		}
		input.FilesTruncated = len(files) >= github.PageSize
	}

	checks, err := src.ListCheckRuns(ctx, in.RepoName, in.HeadSHA)
	if err != nil {
		return "", githubError(err)
	}
	check := pickCheck(checks, in.CheckName)
	if check == nil {
		input.LogNote = "no check run named like this exists for this commit any more"
		return prompt.Build(input, ""), nil
	}
	input.CheckTitle, input.CheckSummary, input.CheckText = check.Output.Title, check.Output.Summary, check.Output.Text

	text, truncated, err := src.GetJobLogs(ctx, in.RepoName, check.ID)
	var api *github.APIError
	switch {
	case errors.Is(err, github.ErrNotFound):
		input.LogNote = "this check is not a GitHub Actions job, so it has no log"
	case errors.As(err, &api) && api.Status == 410:
		input.LogNote = "the log has expired"
	case err != nil:
		return "", githubError(err)
	default:
		input.JobLog = text
		if truncated {
			input.LogNote = fmt.Sprintf("the log is longer than %d MB, so its end is cut off", github.MaxLogBytes>>20)
		}
	}
	return prompt.Build(input, ""), nil
}

// pickCheck finds the check run of an incident: the one with its name that failed, else any with its name.
func pickCheck(checks []github.CheckRun, name string) *github.CheckRun {
	var named *github.CheckRun
	for i := range checks {
		c := &checks[i]
		if c.Name != name {
			continue
		}
		if incident.Classify(c.Status, c.Conclusion) == incident.Bad {
			return c
		}
		if named == nil {
			named = c
		}
	}
	return named
}

// AutoStart starts at most one automatic diagnosis, for the oldest incident that is eligible. The poller
// calls it after every cycle. The limits are checked again inside the store, atomically.
func (r *Responder) AutoStart(ctx context.Context) {
	now := r.now()
	if busy, err := r.Store.HasActiveRun(ctx); err != nil || busy {
		return
	}
	// A daily limit of 0 switches automatic diagnosis off: zero runs are already too many.
	if n, err := r.Store.AutoRunsSince(ctx, now.Add(-24*time.Hour)); err != nil || n >= r.Limits.MaxPerDay {
		return
	}
	candidates, err := r.Store.ListAutoCandidates(ctx, now, r.Limits)
	if err != nil {
		r.Log.Error("cannot list the incidents to diagnose", "err", err)
		return
	}
	for _, in := range candidates {
		if r.backedOff(in.ID, now) {
			continue
		}
		started, err := r.start(ctx, in.ID, true)
		switch {
		case err == nil:
			r.Log.Info("diagnosis started", "incident", in.ID, "run", started.ID)
			return
		case errors.Is(err, store.ErrBusy), errors.Is(err, ErrNoConnection):
			return
		case errors.Is(err, store.ErrLimit), errors.Is(err, store.ErrNotDiagnosable), errors.Is(err, store.ErrNotFound):
			continue
		default:
			r.Log.Warn("could not start a diagnosis", "incident", in.ID, "err", err)
			r.backOff(in.ID, now.Add(max(r.Limits.Cooldown, time.Minute)))
		}
	}
}

func (r *Responder) backedOff(id int64, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return now.Before(r.backoff[id])
}

func (r *Responder) backOff(id int64, until time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.backoff == nil {
		r.backoff = map[int64]time.Time{}
	}
	r.backoff[id] = until
}

// CheckOutcome judges what the runner reports for a responder run: a run that exited with 0 must carry a
// valid diagnosis. Otherwise the outcome becomes a failure with the reason invalid_output, so that the run
// is shown as failed and counts as such.
func CheckOutcome(o run.Outcome) run.Outcome {
	if o.ExitCode != 0 || o.FailureReason != "" {
		return o
	}
	if len(o.Output) == 0 || string(o.Output) == "null" {
		o.FailureReason = run.ReasonInvalidOutput
		o.Result = "The agent finished without a structured answer."
		return o
	}
	if _, err := diagnosis.Parse(o.Output); err != nil {
		o.FailureReason = run.ReasonInvalidOutput
		o.Result = "The agent's answer is not a valid diagnosis: " + err.Error()
	}
	return o
}

// Complete closes the diagnosis of a responder run that is finished (the server calls it after the
// runner reported) or failed (the reaper calls it). A valid answer becomes the incident's diagnosis; in every
// other case the diagnosis fails and the incident goes back. Either way an activity entry is logged.
func (r *Responder) Complete(ctx context.Context, runID string) {
	rn, err := r.Store.GetRun(ctx, runID)
	if err != nil || rn.Role != run.RoleResponder || rn.IncidentID == nil || !rn.Status.Terminal() {
		return
	}
	in, err := r.Store.GetIncident(ctx, *rn.IncidentID)
	if err != nil {
		return // the incident is gone with its repo
	}

	if rn.Status == run.Succeeded {
		d, perr := diagnosis.Parse(rn.Output)
		if perr == nil {
			err = r.Store.CompleteDiagnosis(ctx, runID, d.JSON(), store.NewActivity{
				Kind:    store.KindDiagnosisFinished,
				Summary: fmt.Sprintf("Diagnosis of %s finished: %s", label(in), oneLine(d.Summary, 200)),
			})
			r.logCloseError(runID, err)
			return
		}
		rn.FailureReason = run.ReasonInvalidOutput
	}
	err = r.Store.FailDiagnosis(ctx, runID, store.NewActivity{
		Kind:    store.KindDiagnosisFailed,
		Summary: fmt.Sprintf("Diagnosis of %s failed: %s", label(in), failureText(rn)),
	})
	r.logCloseError(runID, err)
}

func (r *Responder) logCloseError(runID string, err error) {
	if err != nil && !errors.Is(err, store.ErrNotFound) { // not found: a newer run took over
		r.Log.Error("cannot close the diagnosis", "run", runID, "err", err)
	}
}

func failureText(rn run.Run) string {
	switch {
	case rn.FailureReason == run.ReasonTimeout:
		return "the run timed out"
	case rn.FailureReason == run.ReasonInvalidOutput:
		return "the answer was not a valid diagnosis"
	case rn.ExitCode != nil && *rn.ExitCode != 0:
		return fmt.Sprintf("the agent exited with code %d", *rn.ExitCode)
	}
	return "the run failed"
}

// oneLine collapses whitespace and keeps at most max characters.
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "..."
}

// OpenSnapshot returns the tarball of the commit a running responder run is for. The server filters it on
// its way to the runner. The commit is the one the prompt was built for, not the incident's current one.
func (r *Responder) OpenSnapshot(ctx context.Context, runID string) (io.ReadCloser, error) {
	rn, err := r.Store.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if rn.Role != run.RoleResponder || rn.IncidentID == nil || rn.Status != run.Running || rn.HeadSHA == "" {
		return nil, ErrNotSnapshotRun
	}
	in, err := r.Store.GetIncident(ctx, *rn.IncidentID)
	if err != nil {
		return nil, err
	}
	src, err := r.source(ctx)
	if err != nil {
		return nil, err
	}
	rc, err := src.GetTarball(ctx, in.RepoName, rn.HeadSHA)
	if err != nil {
		return nil, githubError(err)
	}
	return rc, nil
}
