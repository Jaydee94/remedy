// Package app wires the control plane together: the HTTP handler and the background workers (the poller and
// the reaper) with the incident engine and the responder they share. The server binary and the tests that run
// the whole chain use this one wiring.
package app

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/Jaydee94/remedy/internal/alertmanager"
	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/kube"
	"github.com/Jaydee94/remedy/internal/learner"
	"github.com/Jaydee94/remedy/internal/loginrelay"
	"github.com/Jaydee94/remedy/internal/metrics"
	"github.com/Jaydee94/remedy/internal/poller"
	"github.com/Jaydee94/remedy/internal/reaper"
	"github.com/Jaydee94/remedy/internal/responder"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/settings"
	"github.com/Jaydee94/remedy/internal/signals"
	"github.com/Jaydee94/remedy/internal/sshhost"
	"github.com/Jaydee94/remedy/internal/store"
)

type App struct {
	// Handler serves the UI and the admin API; when no internal address is configured it serves everything.
	Handler http.Handler
	// InternalHandler serves /runner/v1 and /mcp and is non-nil only when REMEDY_INTERNAL_ADDR is set.
	InternalHandler http.Handler

	Poller     *poller.Poller
	Reaper     *reaper.Reaper
	Responder  *responder.Responder
	Learner    *learner.Learner
	Gatekeeper *gatekeeper.Gatekeeper

	// KubeReader reads the cluster; nil when no cluster is configured. KubeWriter does the approved actions; it is built
	// whenever a write token is set. The allowlist is read at every use and may be empty (every action is then refused).
	// The cluster tools are built on them.
	KubeReader *kube.Reader
	KubeWriter *kube.Writer

	// Settings are the values the Setup page changes.
	Settings *settings.Settings

	// Signals asks the signal sources (Alertmanager) and gives what they find to the incident engine.
	Signals *signals.Runner
	// Probe asks the metrics store whether it answers, for the Setup page. It runs without an Alertmanager.
	Probe *metrics.Prober

	// Login relays the runner's login from the Setup page; New always builds it (there is no switch). Its Run applies the
	// time rules of a session and is started with the other loops.
	Login *loginrelay.Manager

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
	set := settings.New(context.Background(), st, envLayer(cfg), log, settings.WithKey(cfg.MasterKey))
	reader := func(token secret.Value) *github.Client {
		return github.New(cfg.GitHubAPIURL, token, nil, github.WithLog(log))
	}

	diagnoser := &responder.Responder{
		Store:      st,
		Key:        cfg.MasterKey,
		NewSource:  func(token secret.Value) responder.Source { return reader(token) },
		LimitsFunc: set.DiagnosisLimits,
		// An alert incident must have been seen within three polls (at least ten minutes) to be diagnosed on its own.
		AlertFresh: func() time.Duration { return max(10*time.Minute, 3*set.Duration(settings.AlertmanagerPollInterval)) },
		Log:        log,
	}
	// One configuration, with the live allowlist, for the writer, the action tools and the capabilities: the tool, the
	// writer and the UI read the same list.
	cluster := cfg.Cluster
	cluster.Namespaces = func() []string { return set.List(settings.K8sWriteNamespaces) }
	kubeReader, kubeWriter := clusterClients(cluster, log)
	diagnoser.ClusterEnabled = func() bool { return kubeReader != nil }
	if kubeWriter != nil && len(set.List(settings.K8sWriteNamespaces)) == 0 {
		log.Warn("cluster actions are on but the allowlist is empty: every action is refused until a namespace is set (REMEDY_K8S_WRITE_NAMESPACES or the Setup page)")
	}
	tools := append(append(gatekeeper.IncidentTools(st), gatekeeper.JobLogTool(diagnoser)), gatekeeper.NoteTool(st), gatekeeper.SkillTool(st))
	if kubeReader != nil {
		tools = append(tools, gatekeeper.ClusterTools(kubeReader, nil)...)
		if kubeWriter != nil {
			tools = append(tools, gatekeeper.ClusterActionTools(kubeReader, kubeWriter, cluster, nil)...)
		}
	}
	hostSvc := &sshhost.Service{} // one lock per host, shared by the host routes and the host tools
	tools = append(tools, gatekeeper.HostTools(&HostAccess{Store: st, Key: cfg.MasterKey, SSH: hostSvc, Log: log})...)
	tools = append(tools, signalTools(cfg, set, log)...)
	gate := gatekeeper.New(gatekeeper.Config{
		Store: st,
		Tools: tools,
		Log:   log,
		// An approval nobody decides in an automatic run expires after this long (0: never).
		ApprovalTimeout: func() time.Duration { return set.Duration(settings.AutoApprovalTimeout) },
	})
	// The requests that waited for an approval died with the previous process; nobody can receive their results.
	if n, err := st.AbandonAllWaiting(context.Background()); err != nil {
		log.Error("could not abandon the approvals a restart left behind", "err", err)
	} else if n > 0 {
		log.Warn("abandoned the approvals a restart left behind", "count", n)
	}

	// The learning run of a host: it starts on a click and stores proposals when the run is over.
	learning := &learner.Learner{Store: st, Log: log}

	sigs := signalRunner(cfg, st, engine, set, log)
	// The same responder as the poller's hook: an alert is diagnosed with no GitHub connection, in which case the poller's cycle
	// stops before its hook. Both hooks may run at once; the responder keeps its limits inside one store transaction.
	sigs.AfterCycle = diagnoser.AutoStart
	probe := metricsProbe(cfg, set, log)

	// The login relay has no switch (decision 2026-10-22): the manager is always built and its routes always registered.
	// Only the runner's mode withholds it: in another mode the runner starts no driver. The server cannot tell the mode
	// until the runner reports it (cycle 7b), so a start then ends as runner_offline; the api_key_mode answer comes with 7b.
	login := loginRelay(st, log)

	deps := server.Deps{
		Store:        st,
		Signals:      func() []signals.State { return append(sigs.States(), probe.States()...) },
		Auth:         auth.New(cfg.AdminPassword),
		RunnerToken:  cfg.RunnerToken,
		Web:          web,
		Key:          cfg.MasterKey,
		NewGitHub:    func(token secret.Value) server.GitHub { return reader(token) },
		Incidents:    engine,
		Responder:    diagnoser,
		Learner:      learning,
		PollInterval: cfg.PollInterval,
		Gatekeeper:   gate,
		Cluster:      clusterCapabilities(cluster, kubeReader, kubeWriter),
		RunnerStatus: server.NewRunnerStatus(),
		Hosts:        hostSvc,
		Settings:     set,
		Facts:        facts(cfg, kubeReader, kubeWriter),
		Login:        login,
	}
	var handler, internalHandler http.Handler
	if cfg.InternalAddr != "" {
		handler, internalHandler = server.NewSplit(deps)
	} else {
		handler = server.New(deps)
	}

	return &App{
		Handler:         handler,
		InternalHandler: internalHandler,
		Responder:       diagnoser,
		Learner:         learning,
		Gatekeeper:      gate,
		KubeReader:      kubeReader,
		KubeWriter:      kubeWriter,
		Settings:        set,
		Signals:         sigs,
		Probe:           probe,
		Login:           login,
		log:             log,
		Poller: &poller.Poller{
			Store:        st,
			Engine:       engine,
			Key:          cfg.MasterKey,
			NewSource:    func(token secret.Value) poller.Source { return reader(token) },
			IntervalFunc: func() time.Duration { return set.Duration(settings.PollInterval) },
			Log:          log,
			AfterCycle:   diagnoser.AutoStart,
		},
		Reaper: &reaper.Reaper{
			Store:      st,
			Log:        log,
			MaxAgeFunc: func() time.Duration { return reaper.MaxAgeFor(set.Duration(settings.RunTimeout)) },
			OnFailed: func(ctx context.Context, ids []string) {
				for _, id := range ids {
					diagnoser.Complete(ctx, id)
					learning.Complete(ctx, id) // each ignores a run of another role
				}
			},
		},
	}
}

// signalRunner builds the runner of the signal sources. The Alertmanager's address and token are settings read at every
// request. When the client cannot be set up (a CA bundle without a certificate) the source gets no client at all: its Client
// stays a nil interface, never a nil *alertmanager.Client inside one, so it is off and never fetches.
func signalRunner(cfg config.Server, st *store.Store, engine *incident.Engine, set *settings.Settings, log *slog.Logger) *signals.Runner {
	am := &signals.Alertmanager{
		MaxAge: func() time.Duration { return set.Duration(settings.AlertmanagerAutoMaxAge) },
	}
	client, err := alertmanager.New(func() (string, secret.Value, error) {
		base := set.Text(settings.AlertmanagerURL)
		tok, err := set.Secret(settings.AlertmanagerToken)
		switch {
		case errors.Is(err, settings.ErrNotSet):
			return base, secret.Value{}, nil
		case err != nil:
			return "", secret.Value{}, err
		}
		return base, tok, nil
	}, cfg.AlertmanagerCAFile, alertmanager.WithLog(log))
	if err != nil {
		log.Error("the Alertmanager source is off: cannot set up its client", "err", err)
	} else {
		am.Client = client
		am.On = func() bool { return set.Text(settings.AlertmanagerURL) != "" }
	}
	return &signals.Runner{
		Sources:  []signals.Source{am},
		Engine:   engine,
		Interval: func() time.Duration { return set.Duration(settings.AlertmanagerPollInterval) },
		Log:      log,
		Hosts: func(ctx context.Context) ([]signals.HostRules, error) {
			entries, err := st.ListHosts(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]signals.HostRules, 0, len(entries))
			for _, e := range entries {
				out = append(out, signals.HostRules{ID: e.Host.ID, Name: e.Host.Name, Matchers: e.Host.Matchers})
			}
			return out, nil
		},
	}
}

// metricsProbe builds the probe of the metrics store. The address and the token are settings read at every request. When the
// client cannot be set up (a CA bundle without a certificate) the probe gets no client at all, never a nil *metrics.Client inside
// the interface, so it is off. It is paced by the Alertmanager's poll interval and never asks more than once a minute.
func metricsProbe(cfg config.Server, set *settings.Settings, log *slog.Logger) *metrics.Prober {
	p := &metrics.Prober{
		Interval: func() time.Duration { return set.Duration(settings.AlertmanagerPollInterval) },
		Log:      log,
		On:       func() bool { return set.Text(settings.MetricsURL) != "" },
	}
	client, err := metrics.New(func() (string, secret.Value, error) {
		base := set.Text(settings.MetricsURL)
		tok, err := set.Secret(settings.MetricsToken)
		switch {
		case errors.Is(err, settings.ErrNotSet):
			return base, secret.Value{}, nil
		case err != nil:
			return "", secret.Value{}, err
		}
		return base, tok, nil
	}, cfg.MetricsCAFile, metrics.WithLog(log))
	if err != nil {
		log.Error("the metrics source is off: cannot set up its client", "err", err)
	} else {
		p.Client = client
	}
	return p
}

// clusterCapabilities is what the UI is told: the read side as far as it was set up, and the namespaces of the
// allowlist only when actions are possible at all.
func clusterCapabilities(c kube.Config, reader *kube.Reader, writer *kube.Writer) server.Cluster {
	caps := server.Cluster{Read: reader != nil, Write: writer != nil}
	if writer != nil {
		caps.NamespacesFunc = c.AllowedNamespaces
	}
	return caps
}

// envLayer is the environment's part of the settings. A configuration that was built by hand (a test) and not read from the
// environment has no layer; its allowlist is then the environment's value.
func envLayer(cfg config.Server) settings.Layer {
	layer := maps.Clone(cfg.Env)
	if layer == nil {
		layer = settings.Layer{}
	}
	if _, ok := layer[settings.K8sWriteNamespaces]; !ok && len(cfg.Cluster.WriteNamespaces) > 0 {
		layer[settings.K8sWriteNamespaces] = settings.Value{List: slices.Clone(cfg.Cluster.WriteNamespaces)}
	}
	return layer
}

// displayURL keeps scheme, host and path of an address: userinfo, query and fragment could hold a credential.
func displayURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(not shown)"
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
}

// facts are what the Setup page shows as read-only: set outside the page, never a secret.
func facts(cfg config.Server, reader *kube.Reader, writer *kube.Writer) []server.Fact {
	yesNo := func(b bool) string {
		if b {
			return "set up"
		}
		return "not set up"
	}
	internal := cfg.InternalAddr
	if internal == "" {
		internal = "none: one port serves everything"
	}
	out := []server.Fact{
		{Label: "Listener", Value: cfg.Addr},
		{Label: "Internal listener (runner and tools)", Value: internal},
		{Label: "GitHub API", Value: displayURL(cfg.GitHubAPIURL)},
		{Label: "Cluster, reading", Value: yesNo(reader != nil)},
		{Label: "Cluster, actions", Value: yesNo(writer != nil)},
	}
	if cfg.AlertmanagerCAFile != "" {
		out = append(out, server.Fact{Label: "Alertmanager CA file", Value: cfg.AlertmanagerCAFile})
	}
	if cfg.MetricsCAFile != "" {
		out = append(out, server.Fact{Label: "Metrics CA file", Value: cfg.MetricsCAFile})
	}
	return out
}
