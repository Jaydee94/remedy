// Package config reads process configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/kube"
	"github.com/Jaydee94/remedy/internal/secret"
)

const (
	minPasswordLen = 12
	minTokenLen    = 24
	defaultGitHub  = "https://api.github.com"

	defaultPollInterval = time.Minute
	minPollInterval     = 10 * time.Second

	defaultRunTimeout = 10 * time.Minute
	minRunTimeout     = 10 * time.Second

	defaultDiagnoseCooldown = 15 * time.Minute
	minDiagnoseCooldown     = time.Minute
)

type Server struct {
	Addr                   string        // REMEDY_ADDR, default ":8080"
	InternalAddr           string        // REMEDY_INTERNAL_ADDR, default empty: one port. Set, it serves /runner/v1 and /mcp and the main port does not
	DBPath                 string        // REMEDY_DB, default "remedy.db"
	AdminPassword          string        // REMEDY_ADMIN_PASSWORD, required, min 12 chars
	RunnerToken            string        // REMEDY_RUNNER_TOKEN, required, min 24 chars
	MasterKey              secret.Key    // REMEDY_MASTER_KEY, required, 32 random bytes in Base64
	GitHubAPIURL           string        // REMEDY_GITHUB_API_URL, default "https://api.github.com"
	PollInterval           time.Duration // REMEDY_POLL_INTERVAL, a Go duration, default 60s, at least 10s
	DiagnoseCooldown       time.Duration // REMEDY_DIAGNOSE_COOLDOWN, per incident, default 15m, at least 1m
	DiagnoseMaxPerIncident int           // REMEDY_DIAGNOSE_MAX_PER_INCIDENT, automatic diagnoses, default 3, 0 to 20
	DiagnoseMaxPerDay      int           // REMEDY_DIAGNOSE_MAX_PER_DAY, automatic runs in 24 hours, default 20, 0 to 200; 0 turns it off
	Cluster                kube.Config   // REMEDY_K8S_*: how to reach the cluster; the zero value means no cluster
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

	c.PollInterval = defaultPollInterval
	if v := get("REMEDY_POLL_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < minPollInterval {
			return Server{}, errors.New("REMEDY_POLL_INTERVAL must be a duration of at least 10s, for example 60s or 2m")
		}
		c.PollInterval = d
	}

	c.DiagnoseCooldown = defaultDiagnoseCooldown
	if v := get("REMEDY_DIAGNOSE_COOLDOWN"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < minDiagnoseCooldown {
			return Server{}, errors.New("REMEDY_DIAGNOSE_COOLDOWN must be a duration of at least 1m, for example 15m")
		}
		c.DiagnoseCooldown = d
	}
	if c.DiagnoseMaxPerIncident, err = wholeNumber(get, "REMEDY_DIAGNOSE_MAX_PER_INCIDENT", 3, 20); err != nil {
		return Server{}, err
	}
	if c.DiagnoseMaxPerDay, err = wholeNumber(get, "REMEDY_DIAGNOSE_MAX_PER_DAY", 20, 200); err != nil {
		return Server{}, err
	}

	namespaces, err := kube.ParseNamespaces(get("REMEDY_K8S_WRITE_NAMESPACES"))
	if err != nil {
		return Server{}, fmt.Errorf("REMEDY_K8S_WRITE_NAMESPACES: %w", err)
	}
	c.Cluster = kube.Config{
		API:             get("REMEDY_K8S_API"),
		CAFile:          get("REMEDY_K8S_CA_FILE"),
		ReadTokenFile:   get("REMEDY_K8S_READ_TOKEN_FILE"),
		WriteTokenFile:  get("REMEDY_K8S_WRITE_TOKEN_FILE"),
		WriteNamespaces: namespaces,
		ArgoNamespace:   get("REMEDY_K8S_ARGO_NAMESPACE"),
	}
	if err := c.Cluster.Validate(); err != nil {
		return Server{}, err
	}
	return c, nil
}

// wholeNumber reads a whole number from 0 to max, or returns def when the variable is empty.
func wholeNumber(get func(string) string, name string, def, max int) (int, error) {
	v := get(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > max {
		return 0, fmt.Errorf("%s must be a whole number from 0 to %d", name, max)
	}
	return n, nil
}

type Runner struct {
	ServerURL     string        // REMEDY_SERVER_URL, default "http://localhost:8080"
	Token         string        // REMEDY_RUNNER_TOKEN, required, min 24 chars
	WorkspaceRoot string        // REMEDY_WORKSPACES, default $TMPDIR/remedy-workspaces
	ClaudeBin     string        // REMEDY_CLAUDE_BIN, default "claude"
	ClaudeModel   string        // REMEDY_CLAUDE_MODEL, empty means the adapter's default
	RunTimeout    time.Duration // REMEDY_RUN_TIMEOUT, a Go duration, default 10m, at least 10s
	StatusAddr    string        // REMEDY_RUNNER_STATUS_ADDR, default empty: no status listener; set, it serves /livez and /readyz
}

func RunnerFromEnv(get func(string) string) (Runner, error) {
	c := Runner{
		ServerURL:     orDefault(get("REMEDY_SERVER_URL"), "http://localhost:8080"),
		Token:         get("REMEDY_RUNNER_TOKEN"),
		WorkspaceRoot: orDefault(get("REMEDY_WORKSPACES"), filepath.Join(os.TempDir(), "remedy-workspaces")),
		ClaudeBin:     orDefault(get("REMEDY_CLAUDE_BIN"), "claude"),
		ClaudeModel:   get("REMEDY_CLAUDE_MODEL"),
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

	c.RunTimeout = defaultRunTimeout
	if v := get("REMEDY_RUN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < minRunTimeout {
			return Runner{}, errors.New("REMEDY_RUN_TIMEOUT must be a duration of at least 10s, for example 10m")
		}
		c.RunTimeout = d
	}
	return c, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
