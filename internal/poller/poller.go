// Package poller watches the enabled repositories on GitHub and feeds what it sees to the incident
// engine. It only reads from GitHub.
package poller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
)

// Source is what the poller needs from GitHub. *github.Client implements it.
type Source interface {
	ListOpenPRs(ctx context.Context, fullName string) ([]github.PullRequest, error)
	ListCheckRuns(ctx context.Context, fullName, ref string) ([]github.CheckRun, error)
}

// Poller polls once per Interval. Run and PollOnce must not be called concurrently.
type Poller struct {
	Store     *store.Store
	Engine    *incident.Engine
	Key       secret.Key
	NewSource func(token secret.Value) Source
	Interval  time.Duration // default one minute
	Log       *slog.Logger
	Now       func() time.Time // default time.Now

	source      Source // reused between cycles so that its ETag cache works
	sealed      []byte // the ciphertext source was built from
	pausedUntil time.Time
}

func (p *Poller) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Run polls at once and then every Interval until ctx ends.
func (p *Poller) Run(ctx context.Context) {
	interval := p.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	p.PollOnce(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.PollOnce(ctx)
		}
	}
}

// PollOnce runs one cycle over all enabled repos.
func (p *Poller) PollOnce(ctx context.Context) {
	if p.now().Before(p.pausedUntil) {
		return
	}
	conn, err := p.Store.GetConnection(ctx)
	if errors.Is(err, store.ErrNotFound) {
		p.source, p.sealed = nil, nil
		return
	}
	if err != nil {
		p.Log.Error("cannot load the GitHub connection", "err", err)
		return
	}
	if conn.Status != store.ConnOK {
		return // a rejected or undecryptable token needs the maintainer
	}

	src, err := p.sourceFor(conn)
	if errors.Is(err, secret.ErrOpen) {
		_ = p.Store.UpdateConnectionStatus(ctx, store.ConnUndecryptable, store.UndecryptableDetail, p.now())
		p.Log.Warn("the stored GitHub token cannot be decrypted; polling is off")
		return
	}
	if err != nil {
		p.Log.Error("cannot read the GitHub token", "err", err)
		return
	}

	repos, err := p.Store.ListRepos(ctx)
	if err != nil {
		p.Log.Error("cannot list repos", "err", err)
		return
	}
	for _, repo := range repos {
		if !repo.Enabled {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if err := p.pollRepo(ctx, src, repo); fatal(err) {
			p.react(ctx, err)
			return
		}
	}
}

// sourceFor returns the GitHub source for the stored token, building a new one only when the token changed.
func (p *Poller) sourceFor(conn store.Connection) (Source, error) {
	if p.source != nil && bytes.Equal(p.sealed, conn.TokenCiphertext) {
		return p.source, nil
	}
	raw, err := p.Key.Open(conn.TokenCiphertext, store.ConnectionAAD())
	if err != nil {
		return nil, err
	}
	p.source, p.sealed = p.NewSource(secret.NewValue(string(raw))), conn.TokenCiphertext
	return p.source, nil
}

// fatal reports errors that make every further request of this cycle pointless.
func fatal(err error) bool {
	var rl *github.RateLimitError
	return errors.As(err, &rl) || errors.Is(err, github.ErrUnauthorized)
}

func (p *Poller) react(ctx context.Context, err error) {
	var rl *github.RateLimitError
	if errors.As(err, &rl) {
		p.pausedUntil = p.now().Add(rl.RetryAfter)
		p.Log.Warn("GitHub rate limit, polling paused", "for", rl.RetryAfter.Round(time.Second))
		return
	}
	_ = p.Store.UpdateConnectionStatus(ctx, store.ConnError, "GitHub rejected the stored token.", p.now())
	p.Log.Warn("GitHub rejected the stored token; polling is off")
}

func (p *Poller) pollRepo(ctx context.Context, src Source, repo store.Repo) error {
	err := p.observeRepo(ctx, src, repo)
	if ctx.Err() != nil {
		return err // shutting down: record nothing
	}
	p.record(ctx, repo, err)
	return err
}

// record stores the outcome on the repo and logs the transitions between working and failing.
func (p *Poller) record(ctx context.Context, repo store.Repo, pollErr error) {
	msg := ""
	if pollErr != nil {
		msg = pollErr.Error()
		if len(msg) > 300 {
			msg = msg[:300]
		}
		p.Log.Warn("polling failed", "repo", repo.FullName, "err", msg)
	}
	if err := p.Store.MarkRepoPolled(ctx, repo.ID, p.now(), msg); err != nil {
		p.Log.Error("cannot record the poll", "repo", repo.FullName, "err", err)
		return
	}
	var act *store.NewActivity
	switch {
	case pollErr != nil && repo.LastError == "":
		act = &store.NewActivity{Kind: store.KindPollFailed, Summary: fmt.Sprintf("Polling %s failed: %s", repo.FullName, msg)}
	case pollErr == nil && repo.LastError != "":
		act = &store.NewActivity{Kind: store.KindPollRecovered, Summary: fmt.Sprintf("Polling %s works again", repo.FullName)}
	}
	if act != nil {
		act.RepoID = repo.ID
		if err := p.Store.AddActivity(ctx, *act); err != nil {
			p.Log.Error("cannot log the poll", "repo", repo.FullName, "err", err)
		}
	}
}

func (p *Poller) observeRepo(ctx context.Context, src Source, repo store.Repo) error {
	prs, err := src.ListOpenPRs(ctx, repo.FullName)
	if err != nil {
		return err
	}

	var (
		agg      aggregate
		firstErr error
		open     = make(map[int]bool, len(prs))
	)
	note := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}
	for _, pr := range prs {
		open[pr.Number] = true
		runs, err := src.ListCheckRuns(ctx, repo.FullName, pr.Head.SHA)
		if fatal(err) {
			return err
		}
		if err != nil {
			note(fmt.Errorf("check runs of PR #%d: %w", pr.Number, err))
			continue
		}
		agg.add(observations(repo, "pr:"+strconv.Itoa(pr.Number), pr.HTMLURL, runs)...)
	}
	runs, err := src.ListCheckRuns(ctx, repo.FullName, repo.DefaultBranch)
	if fatal(err) {
		return err
	}
	if err != nil {
		note(fmt.Errorf("check runs of %s: %w", repo.DefaultBranch, err))
	} else {
		agg.add(observations(repo, "branch:"+repo.DefaultBranch, "", runs)...)
	}

	for _, o := range agg.list {
		if err := p.Engine.Observe(ctx, o); err != nil {
			note(err)
		}
	}
	if len(prs) < github.PageSize {
		if err := p.Engine.ResolveClosedPRs(ctx, repo.ID, open); err != nil {
			note(err)
		}
	} else {
		p.Log.Warn("a full page of open pull requests may be truncated; closed PRs are not resolved", "repo", repo.FullName)
	}
	return firstErr
}

func observations(repo store.Repo, ref, refURL string, runs []github.CheckRun) []incident.Observation {
	out := make([]incident.Observation, 0, len(runs))
	for _, r := range runs {
		out = append(out, incident.Observation{
			RepoID: repo.ID, RepoName: repo.FullName, Ref: ref, RefURL: refURL, CheckName: r.Name,
			Class: incident.Classify(r.Status, r.Conclusion), Conclusion: r.Conclusion, HeadSHA: r.HeadSHA, URL: r.HTMLURL,
		})
	}
	return out
}

// aggregate keeps one observation per (ref, check name): when several check runs share the key, the
// most severe one wins, so that a green twin cannot resolve a red one. The order of first appearance is kept.
type aggregate struct {
	list  []incident.Observation
	index map[[2]string]int
}

func (a *aggregate) add(obs ...incident.Observation) {
	if a.index == nil {
		a.index = map[[2]string]int{}
	}
	for _, o := range obs {
		k := [2]string{o.Ref, o.CheckName}
		i, ok := a.index[k]
		switch {
		case !ok:
			a.index[k] = len(a.list)
			a.list = append(a.list, o)
		case o.Class > a.list[i].Class:
			a.list[i] = o
		}
	}
}
