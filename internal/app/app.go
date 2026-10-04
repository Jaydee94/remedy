// Package app wires the control plane together: the HTTP handler and the background workers (the poller and
// the reaper) with the incident engine and the responder they share. The server binary and the tests that run
// the whole chain use this one wiring.
package app

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/poller"
	"github.com/Jaydee94/remedy/internal/reaper"
	"github.com/Jaydee94/remedy/internal/responder"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

type App struct {
	Handler    http.Handler
	Poller     *poller.Poller
	Reaper     *reaper.Reaper
	Responder  *responder.Responder
	Gatekeeper *gatekeeper.Gatekeeper
}

// New wires everything for a configuration. web is the built UI, or nil.
func New(cfg config.Server, st *store.Store, log *slog.Logger, web fs.FS) *App {
	engine := &incident.Engine{Store: st}
	reader := func(token secret.Value) *github.Client {
		return github.New(cfg.GitHubAPIURL, token, nil, github.WithLog(log))
	}

	diagnoser := &responder.Responder{
		Store:     st,
		Key:       cfg.MasterKey,
		NewSource: func(token secret.Value) responder.Source { return reader(token) },
		Limits: store.DiagnosisLimits{
			Cooldown: cfg.DiagnoseCooldown, MaxPerIncident: cfg.DiagnoseMaxPerIncident, MaxPerDay: cfg.DiagnoseMaxPerDay,
		},
		Log: log,
	}
	gate := gatekeeper.New(gatekeeper.Config{
		Store: st,
		Tools: append(append(gatekeeper.IncidentTools(st), gatekeeper.JobLogTool(diagnoser)), gatekeeper.NoteTool(st)),
		Log:   log,
	})
	// The requests that waited for an approval died with the previous process; nobody can receive their results.
	if n, err := st.AbandonAllWaiting(context.Background()); err != nil {
		log.Error("could not abandon the approvals a restart left behind", "err", err)
	} else if n > 0 {
		log.Warn("abandoned the approvals a restart left behind", "count", n)
	}

	return &App{
		Responder:  diagnoser,
		Gatekeeper: gate,
		Poller: &poller.Poller{
			Store:      st,
			Engine:     engine,
			Key:        cfg.MasterKey,
			NewSource:  func(token secret.Value) poller.Source { return reader(token) },
			Interval:   cfg.PollInterval,
			Log:        log,
			AfterCycle: diagnoser.AutoStart,
		},
		Reaper: &reaper.Reaper{
			Store: st,
			Log:   log,
			OnFailed: func(ctx context.Context, ids []string) {
				for _, id := range ids {
					diagnoser.Complete(ctx, id)
				}
			},
		},
		Handler: server.New(server.Deps{
			Store:        st,
			Auth:         auth.New(cfg.AdminPassword),
			RunnerToken:  cfg.RunnerToken,
			Web:          web,
			Key:          cfg.MasterKey,
			NewGitHub:    func(token secret.Value) server.GitHub { return reader(token) },
			Incidents:    engine,
			Responder:    diagnoser,
			PollInterval: cfg.PollInterval,
			Gatekeeper:   gate,
		}),
	}
}
