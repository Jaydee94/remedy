// Package app wires the control plane together: the HTTP handler and the background workers (the poller and
// the reaper) with the incident engine and the responder they share. The server binary and the tests that run
// the whole chain use this one wiring.
package app

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/kube"
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

	// KubeReader reads the cluster; nil when no cluster is configured. KubeWriter does the approved actions; nil unless
	// a write token and an allowlist are configured. The cluster tools are built on them.
	KubeReader *kube.Reader
	KubeWriter *kube.Writer

	log *slog.Logger
}

// clusterClients builds the cluster clients a configuration asks for. The configuration was validated when it was
// read, so a failure here is unexpected: it is logged and the cluster stays off, as if it were not configured.
func clusterClients(c kube.Config, log *slog.Logger) (*kube.Reader, *kube.Writer) {
	if !c.ReadEnabled() {
		return nil, nil
	}
	reader, err := kube.NewReader(c, kube.WithLog(log))
	if err != nil {
		log.Error("the cluster is off: cannot set up the read side", "err", err)
		return nil, nil
	}
	if !c.WriteEnabled() {
		return reader, nil
	}
	writer, err := kube.NewWriter(c, kube.WithLog(log))
	if err != nil {
		log.Error("cluster actions are off: cannot set up the write side", "err", err)
		return reader, nil
	}
	return reader, writer
}

// CheckCluster says in the log whether the cluster answers to the read token. It changes nothing: a cluster that is
// down at start-up may be up a minute later.
func (a *App) CheckCluster(ctx context.Context) {
	if a.KubeReader == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	v, err := a.KubeReader.GetVersion(ctx)
	if err != nil {
		a.log.Warn("the cluster cannot be reached with the read token: cluster tools will fail", "err", err)
		return
	}
	a.log.Info("the cluster answers", "version", v.GitVersion, "actions", a.KubeWriter != nil)
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
	kubeReader, kubeWriter := clusterClients(cfg.Cluster, log)
	tools := append(append(gatekeeper.IncidentTools(st), gatekeeper.JobLogTool(diagnoser)), gatekeeper.NoteTool(st))
	if kubeReader != nil {
		tools = append(tools, gatekeeper.ClusterTools(kubeReader, nil)...)
		if kubeWriter != nil {
			tools = append(tools, gatekeeper.ClusterActionTools(kubeReader, kubeWriter, cfg.Cluster, nil)...)
		}
	}
	gate := gatekeeper.New(gatekeeper.Config{
		Store: st,
		Tools: tools,
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
		KubeReader: kubeReader,
		KubeWriter: kubeWriter,
		log:        log,
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
			Cluster:      clusterCapabilities(cfg.Cluster, kubeReader, kubeWriter),
		}),
	}
}

// clusterCapabilities is what the UI is told: the read side as far as it was set up, and the namespaces of the
// allowlist only when actions are possible at all.
func clusterCapabilities(c kube.Config, reader *kube.Reader, writer *kube.Writer) server.Cluster {
	caps := server.Cluster{Read: reader != nil, Write: writer != nil}
	if writer != nil {
		caps.Namespaces = slices.Clone(c.WriteNamespaces)
	}
	return caps
}
