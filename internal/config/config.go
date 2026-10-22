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
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

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
	DatabaseURL            secret.Value  // REMEDY_DATABASE_URL, required: a libpq-style URL or key=value string; holds a password, so it prints as ***
	DatabaseMaxConns       int           // REMEDY_DATABASE_MAX_CONNS, default 10, 2 to 100: the size of the connection pool
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

	databaseTarget string // host:port/dbname of DatabaseURL: what may be logged of it
}

// DatabaseTarget names the database as host:port/dbname, the only part of REMEDY_DATABASE_URL that may be logged (never the
// user, the password or the options).
func (c Server) DatabaseTarget() string { return c.databaseTarget }

func ServerFromEnv(get func(string) string) (Server, error) {
	c := Server{
		Addr:          orDefault(get("REMEDY_ADDR"), ":8080"),
		AdminPassword: get("REMEDY_ADMIN_PASSWORD"),
		RunnerToken:   get("REMEDY_RUNNER_TOKEN"),
		GitHubAPIURL:  orDefault(get("REMEDY_GITHUB_API_URL"), defaultGitHub),
	}
	if get("REMEDY_DB") != "" {
		return Server{}, errors.New("REMEDY_DB is gone: the control plane uses PostgreSQL (set REMEDY_DATABASE_URL); see docs/runbook/postgres-cutover.md")
	}
	if err := c.loadDatabase(get); err != nil {
		return Server{}, err
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

const (
	minDatabaseConns     = 2
	maxDatabaseConns     = 100
	defaultDatabaseConns = 10
)

// loadDatabase reads REMEDY_DATABASE_URL and REMEDY_DATABASE_MAX_CONNS. The URL holds a password and the parser's own errors
// quote it, so no error here carries the parser's text.
func (c *Server) loadDatabase(get func(string) string) error {
	raw := get("REMEDY_DATABASE_URL")
	if strings.TrimSpace(raw) == "" {
		// pgx would take a blank string as a connection string of defaults: the PG* environment and a local socket.
		return errors.New("REMEDY_DATABASE_URL must be set, for example postgres://user:password@host:5432/remedy?sslmode=require")
	}
	pc, err := pgx.ParseConfig(raw)
	if err != nil {
		return errors.New("REMEDY_DATABASE_URL cannot be parsed as a PostgreSQL URL or connection string; check sslmode, sslrootcert and the port (it is not shown: it holds a password)")
	}
	// Without a database name pgx falls back to PGDATABASE and then to the user's name: a database nobody named.
	if pc.Database == "" || urlWithoutDatabase(raw) {
		return errors.New("REMEDY_DATABASE_URL has no database name: add it as the path (postgres://user:password@host:5432/remedy) or as dbname=remedy (it is not shown: it holds a password)")
	}
	c.DatabaseURL = secret.NewValue(raw)
	c.databaseTarget = net.JoinHostPort(pc.Host, strconv.Itoa(int(pc.Port))) + "/" + pc.Database

	c.DatabaseMaxConns = defaultDatabaseConns
	if v := get("REMEDY_DATABASE_MAX_CONNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < minDatabaseConns || n > maxDatabaseConns {
			return fmt.Errorf("REMEDY_DATABASE_MAX_CONNS must be a whole number from %d to %d", minDatabaseConns, maxDatabaseConns)
		}
		c.DatabaseMaxConns = n
	}
	return nil
}

// urlWithoutDatabase reports whether raw is a URL (not a key=value string) whose path names no database. The path decides,
// whatever PGDATABASE says.
func urlWithoutDatabase(raw string) bool {
	if !strings.HasPrefix(raw, "postgres://") && !strings.HasPrefix(raw, "postgresql://") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false // pgx parsed it; its own reading stands
	}
	return strings.Trim(u.Path, "/") == ""
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
