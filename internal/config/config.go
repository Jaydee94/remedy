// Package config reads process configuration from environment variables.
package config

import (
	"errors"
	"os"
	"path/filepath"
)

const (
	minPasswordLen = 12
	minTokenLen    = 24
)

type Server struct {
	Addr          string // REMEDY_ADDR, default ":8080"
	DBPath        string // REMEDY_DB, default "remedy.db"
	AdminPassword string // REMEDY_ADMIN_PASSWORD, required, min 12 chars
	RunnerToken   string // REMEDY_RUNNER_TOKEN, required, min 24 chars
}

func ServerFromEnv(get func(string) string) (Server, error) {
	c := Server{
		Addr:          orDefault(get("REMEDY_ADDR"), ":8080"),
		DBPath:        orDefault(get("REMEDY_DB"), "remedy.db"),
		AdminPassword: get("REMEDY_ADMIN_PASSWORD"),
		RunnerToken:   get("REMEDY_RUNNER_TOKEN"),
	}
	if len(c.AdminPassword) < minPasswordLen {
		return Server{}, errors.New("REMEDY_ADMIN_PASSWORD must be set and at least 12 characters")
	}
	if len(c.RunnerToken) < minTokenLen {
		return Server{}, errors.New("REMEDY_RUNNER_TOKEN must be set and at least 24 characters")
	}
	return c, nil
}

type Runner struct {
	ServerURL     string // REMEDY_SERVER_URL, default "http://localhost:8080"
	Token         string // REMEDY_RUNNER_TOKEN, required, min 24 chars
	WorkspaceRoot string // REMEDY_WORKSPACES, default $TMPDIR/remedy-workspaces
	ClaudeBin     string // REMEDY_CLAUDE_BIN, default "claude"
}

func RunnerFromEnv(get func(string) string) (Runner, error) {
	c := Runner{
		ServerURL:     orDefault(get("REMEDY_SERVER_URL"), "http://localhost:8080"),
		Token:         get("REMEDY_RUNNER_TOKEN"),
		WorkspaceRoot: orDefault(get("REMEDY_WORKSPACES"), filepath.Join(os.TempDir(), "remedy-workspaces")),
		ClaudeBin:     orDefault(get("REMEDY_CLAUDE_BIN"), "claude"),
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
