package responder

import (
	"context"
	"errors"
	"fmt"

	"github.com/Jaydee94/remedy/internal/github"
)

// JobLog returns the raw log of the job behind an incident's check. note says why there is no log, or what is
// missing of it: GitHub not connected, no such check at the commit any more, no Actions job, an expired log, a log
// that was cut off. An unknown incident is store.ErrNotFound and a failed GitHub read wraps ErrGitHub.
func (r *Responder) JobLog(ctx context.Context, incidentID int64) (log, note string, err error) {
	in, err := r.Store.GetIncident(ctx, incidentID)
	if err != nil {
		return "", "", err
	}
	src, err := r.source(ctx)
	if errors.Is(err, ErrNoConnection) {
		return "", noConnectionNote, nil
	}
	if err != nil {
		return "", "", err
	}

	checks, err := src.ListCheckRuns(ctx, in.RepoName, in.HeadSHA)
	if err != nil {
		if err = githubError(err); errors.Is(err, ErrNoConnection) {
			return "", noConnectionNote, nil
		}
		return "", "", err
	}
	check := pickCheck(checks, in.CheckName)
	if check == nil {
		return "", "no check run named like this exists for this commit any more", nil
	}

	text, truncated, err := src.GetJobLogs(ctx, in.RepoName, check.ID)
	var api *github.APIError
	switch {
	case errors.Is(err, github.ErrNotFound):
		return "", "this check is not a GitHub Actions job, so it has no log", nil
	case errors.As(err, &api) && api.Status == 410:
		return "", "the log has expired", nil
	case errors.Is(err, github.ErrUnauthorized):
		return "", noConnectionNote, nil
	case err != nil:
		return "", "", githubError(err)
	}
	if truncated {
		note = fmt.Sprintf("the log is longer than %d MB, so its end is cut off", github.MaxLogBytes>>20)
	}
	return text, note, nil
}

const noConnectionNote = "GitHub is not connected, or the stored token cannot be used, so no log can be read"
