// Package config reads process configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/secret"
)

const (
	minPasswordLen = 12
	minTokenLen    = 24
	defaultGitHub  = "https://api.github.com"

	defaultPollInterval = time.Minute
	minPollInterval     = 10 * time.Second
)

type Server struct {
	Addr          string        // REMEDY_ADDR, default ":8080"
	DBPath        string        // REMEDY_DB, default "remedy.db"
	AdminPassword string        // REMEDY_ADMIN_PASSWORD, required, min 12 chars
	RunnerToken   string        // REMEDY_RUNNER_TOKEN, required, min 24 chars
	MasterKey     secret.Key    // REMEDY_MASTER_KEY, required, 32 random bytes in Base64
	GitHubAPIURL  string        // REMEDY_GITHUB_API_URL, default "https://api.github.com"
	PollInterval  time.Duration // REMEDY_POLL_INTERVAL, a Go duration, default 60s, at least 10s
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

	c.PollInterval = defaultPollInterval
	if v := get("REMEDY_POLL_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < minPollInterval {
			return Server{}, errors.New("REMEDY_POLL_INTERVAL must be a duration of at least 10s, for example 60s or 2m")
		}
		c.PollInterval = d
	}
	return c, nil
}

type Runner struct {
	ServerURL     string // REMEDY_SERVER_URL, default "http://localhost:8080"
	Token         string // REMEDY_RUNNER_TOKEN, required, min 24 chars
	WorkspaceRoot string // REMEDY_WORKSPACES, default $TMPDIR/remedy-workspaces
	ClaudeBin     string // REMEDY_CLAUDE_BIN, default "claude"
	ClaudeModel   string // REMEDY_CLAUDE_MODEL, empty means the adapter's default
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
	return c, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
