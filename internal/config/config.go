// Package config reads process configuration from environment variables.
package config

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/kube"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/settings"
)

const (
	minPasswordLen = 12
	minTokenLen    = 24
	defaultGitHub  = "https://api.github.com"
)

type Server struct {
	Addr                   string        // REMEDY_ADDR, default ":8080"
	InternalAddr           string        // REMEDY_INTERNAL_ADDR, default empty: one port. Set, it serves /runner/v1 and /mcp and the main port does not
	DBPath                 string        // REMEDY_DB, default "remedy.db"
	AdminPassword          string        // REMEDY_ADMIN_PASSWORD, required, min 12 chars
	RunnerToken            string        // REMEDY_RUNNER_TOKEN, required, min 24 chars
	MasterKey              secret.Key    // REMEDY_MASTER_KEY, required, 32 random bytes in Base64
	GitHubAPIURL           string        // REMEDY_GITHUB_API_URL, default "https://api.github.com"
	PollInterval           time.Duration // REMEDY_POLL_INTERVAL: the environment's value or the default; the registry in internal/settings has the bounds
	DiagnoseCooldown       time.Duration // REMEDY_DIAGNOSE_COOLDOWN
	DiagnoseMaxPerIncident int           // REMEDY_DIAGNOSE_MAX_PER_INCIDENT
	DiagnoseMaxPerDay      int           // REMEDY_DIAGNOSE_MAX_PER_DAY
	Cluster                kube.Config   // REMEDY_K8S_*: how to reach the cluster; the zero value means no cluster
	AlertmanagerCAFile     string        // REMEDY_ALERTMANAGER_CA_FILE: a PEM bundle for an https Alertmanager; empty means the system's roots
	MetricsCAFile          string        // REMEDY_METRICS_CA_FILE: a PEM bundle for an https metrics store; empty means the system's roots
	// Env holds the variables of the registry that were set (poll interval, diagnosis limits, namespaces, run timeout). They are
	// the defaults of the settings, which the Setup page can override.
	Env settings.Layer
}

func ServerFromEnv(get func(string) string) (Server, error) {
	c := Server{
		Addr:          orDefault(get("REMEDY_ADDR"), ":8080"),
		DBPath:        orDefault(get("REMEDY_DB"), "remedy.db"),
		AdminPassword: get("REMEDY_ADMIN_PASSWORD"),
		RunnerToken:   get("REMEDY_RUNNER_TOKEN"),
		GitHubAPIURL:  orDefault(get("REMEDY_GITHUB_API_URL"), defaultGitHub),
	}
	if len(c.AdminPassword) < minPasswordLen {
		return Server{}, errors.New("REMEDY_ADMIN_PASSWORD must be set and at least 12 characters")
	}
	if len(c.RunnerToken) < minTokenLen {
		return Server{}, errors.New("REMEDY_RUNNER_TOKEN must be set and at least 24 characters")
	}

	key, err := secret.ParseKey(get("REMEDY_MASTER_KEY"))
	if err != nil {
		return Server{}, fmt.Errorf("REMEDY_MASTER_KEY: %w (generate one with: openssl rand -base64 32)", err)
	}
	c.MasterKey = key

	u, err := url.Parse(c.GitHubAPIURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Server{}, errors.New("REMEDY_GITHUB_API_URL must be an http or https URL")
	}
	c.GitHubAPIURL = strings.TrimRight(c.GitHubAPIURL, "/")

	c.InternalAddr = get("REMEDY_INTERNAL_ADDR")
	if c.InternalAddr != "" {
		_, port, err := net.SplitHostPort(c.InternalAddr)
		if err != nil || port == "" {
			return Server{}, errors.New("REMEDY_INTERNAL_ADDR must be host:port or :port, for example :8081")
		}
		if _, mainPort, err := net.SplitHostPort(c.Addr); err == nil && mainPort == port {
			return Server{}, errors.New("REMEDY_INTERNAL_ADDR must use another port than REMEDY_ADDR")
		}
	}

	layer, err := settings.EnvLayer(get)
	if err != nil {
		return Server{}, err
	}
	c.Env = layer
	c.PollInterval = layer.Duration(settings.PollInterval)
	c.DiagnoseCooldown = layer.Duration(settings.DiagnoseCooldown)
	c.DiagnoseMaxPerIncident = layer.Int(settings.DiagnoseMaxPerIncident)
	c.DiagnoseMaxPerDay = layer.Int(settings.DiagnoseMaxPerDay)

	c.Cluster = kube.Config{
		API:             get("REMEDY_K8S_API"),
		CAFile:          get("REMEDY_K8S_CA_FILE"),
		ReadTokenFile:   get("REMEDY_K8S_READ_TOKEN_FILE"),
		WriteTokenFile:  get("REMEDY_K8S_WRITE_TOKEN_FILE"),
		WriteNamespaces: layer.List(settings.K8sWriteNamespaces),
		ArgoNamespace:   get("REMEDY_K8S_ARGO_NAMESPACE"),
	}
	if err := c.Cluster.Validate(); err != nil {
		return Server{}, err
	}
	c.AlertmanagerCAFile = get("REMEDY_ALERTMANAGER_CA_FILE")
	if c.AlertmanagerCAFile != "" {
		pemBytes, err := os.ReadFile(c.AlertmanagerCAFile)
		if err != nil {
			return Server{}, fmt.Errorf("REMEDY_ALERTMANAGER_CA_FILE: %w", err)
		}
		if !x509.NewCertPool().AppendCertsFromPEM(pemBytes) {
			return Server{}, errors.New("REMEDY_ALERTMANAGER_CA_FILE: the file holds no certificate")
		}
	}
	c.MetricsCAFile = get("REMEDY_METRICS_CA_FILE")
	if c.MetricsCAFile != "" {
		pemBytes, err := os.ReadFile(c.MetricsCAFile)
		if err != nil {
			return Server{}, fmt.Errorf("REMEDY_METRICS_CA_FILE: %w", err)
		}
		if !x509.NewCertPool().AppendCertsFromPEM(pemBytes) {
			return Server{}, errors.New("REMEDY_METRICS_CA_FILE: the file holds no certificate")
		}
	}
	return c, nil
}

type Runner struct {
	ServerURL     string        // REMEDY_SERVER_URL, default "http://localhost:8080"
	Token         string        // REMEDY_RUNNER_TOKEN, required, min 24 chars
	WorkspaceRoot string        // REMEDY_WORKSPACES, default $TMPDIR/remedy-workspaces
	ClaudeBin     string        // REMEDY_CLAUDE_BIN, default "claude"
	ClaudeModel   string        // REMEDY_CLAUDE_MODEL, empty means the adapter's default
	RunTimeout    time.Duration // REMEDY_RUN_TIMEOUT, a Go duration, default 10m, at least 10s
	StatusAddr    string        // REMEDY_RUNNER_STATUS_ADDR, default empty: no status listener; set, it serves /livez and /readyz
	AuthMode      string        // REMEDY_AUTH_MODE, default "subscription"; the login relay, which has no switch, runs only in this mode
}

func RunnerFromEnv(get func(string) string) (Runner, error) {
	c := Runner{
		ServerURL:     orDefault(get("REMEDY_SERVER_URL"), "http://localhost:8080"),
		Token:         get("REMEDY_RUNNER_TOKEN"),
		WorkspaceRoot: orDefault(get("REMEDY_WORKSPACES"), filepath.Join(os.TempDir(), "remedy-workspaces")),
		ClaudeBin:     orDefault(get("REMEDY_CLAUDE_BIN"), "claude"),
		ClaudeModel:   get("REMEDY_CLAUDE_MODEL"),
		AuthMode:      orDefault(get("REMEDY_AUTH_MODE"), "subscription"),
	}
	if len(c.Token) < minTokenLen {
		return Runner{}, errors.New("REMEDY_RUNNER_TOKEN must be set and at least 24 characters")
	}
	c.StatusAddr = get("REMEDY_RUNNER_STATUS_ADDR")
	if c.StatusAddr != "" {
		if _, port, err := net.SplitHostPort(c.StatusAddr); err != nil || port == "" {
			return Runner{}, errors.New("REMEDY_RUNNER_STATUS_ADDR must be host:port or :port, for example :8082")
		}
	}

	c.RunTimeout = settings.Layer(nil).Duration(settings.RunTimeout) // the default
	if v, set, err := settings.EnvValue(settings.RunTimeout, get); err != nil {
		return Runner{}, err
	} else if set {
		c.RunTimeout = v.Dur
	}
	return c, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
