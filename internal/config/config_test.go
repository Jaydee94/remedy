package config_test

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/config"
)

var masterKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func serverEnv(extra map[string]string) func(string) string {
	m := map[string]string{
		"REMEDY_ADMIN_PASSWORD": "a-long-enough-password",
		"REMEDY_RUNNER_TOKEN":   "a-runner-token-of-24-chars-or-more",
		"REMEDY_MASTER_KEY":     masterKey,
	}
	for k, v := range extra {
		m[k] = v
	}
	return env(m)
}

func TestServerFromEnvDefaults(t *testing.T) {
	c, err := config.ServerFromEnv(serverEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":8080" || c.DBPath != "remedy.db" || c.GitHubAPIURL != "https://api.github.com" {
		t.Fatalf("defaults = %+v", c)
	}
}

func TestServerFromEnvRejectsWeakSecrets(t *testing.T) {
	cases := map[string]map[string]string{
		"missing password":     {"REMEDY_ADMIN_PASSWORD": ""},
		"short password":       {"REMEDY_ADMIN_PASSWORD": "short"},
		"missing runner token": {"REMEDY_RUNNER_TOKEN": ""},
		"short runner token":   {"REMEDY_RUNNER_TOKEN": "short"},
	}
	for name, override := range cases {
		if _, err := config.ServerFromEnv(serverEnv(override)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestServerFromEnvRequiresAValidMasterKey(t *testing.T) {
	cases := map[string]string{
		"missing":    "",
		"not base64": "!!!",
		"too short":  base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 16)),
	}
	for name, key := range cases {
		_, err := config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_MASTER_KEY": key}))
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		if !strings.Contains(err.Error(), "REMEDY_MASTER_KEY") {
			t.Errorf("%s: error %q does not name the variable", name, err)
		}
	}
}

func TestServerFromEnvGitHubAPIURL(t *testing.T) {
	c, err := config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_GITHUB_API_URL": "http://127.0.0.1:9090"}))
	if err != nil || c.GitHubAPIURL != "http://127.0.0.1:9090" {
		t.Fatalf("GitHubAPIURL = %q, err = %v", c.GitHubAPIURL, err)
	}
	for _, bad := range []string{"ftp://example.com", "not a url", "//no-scheme"} {
		if _, err := config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_GITHUB_API_URL": bad})); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func TestRunnerFromEnv(t *testing.T) {
	c, err := config.RunnerFromEnv(env(map[string]string{
		"REMEDY_RUNNER_TOKEN": "a-runner-token-of-24-chars-or-more",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.ServerURL != "http://localhost:8080" || c.ClaudeBin != "claude" ||
		!strings.HasSuffix(c.WorkspaceRoot, "remedy-workspaces") {
		t.Fatalf("defaults = %+v", c)
	}

	if _, err := config.RunnerFromEnv(env(nil)); err == nil {
		t.Fatal("expected an error without a runner token")
	}
}

func TestRunnerFromEnvClaudeModel(t *testing.T) {
	token := "a-runner-token-of-24-chars-or-more"

	unset, err := config.RunnerFromEnv(env(map[string]string{"REMEDY_RUNNER_TOKEN": token}))
	if err != nil {
		t.Fatal(err)
	}
	if unset.ClaudeModel != "" {
		t.Fatalf("ClaudeModel = %q, want empty so that the adapter's default applies", unset.ClaudeModel)
	}

	set, err := config.RunnerFromEnv(env(map[string]string{
		"REMEDY_RUNNER_TOKEN": token, "REMEDY_CLAUDE_MODEL": "opus",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if set.ClaudeModel != "opus" {
		t.Fatalf("ClaudeModel = %q, want opus", set.ClaudeModel)
	}
}
