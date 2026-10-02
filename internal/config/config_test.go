package config_test

import (
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/config"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestServerFromEnvDefaults(t *testing.T) {
	c, err := config.ServerFromEnv(env(map[string]string{
		"REMEDY_ADMIN_PASSWORD": "a-long-enough-password",
		"REMEDY_RUNNER_TOKEN":   "a-runner-token-of-24-chars-or-more",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":8080" || c.DBPath != "remedy.db" {
		t.Fatalf("defaults = %+v", c)
	}
}

func TestServerFromEnvRejectsWeakSecrets(t *testing.T) {
	cases := map[string]map[string]string{
		"missing password":     {"REMEDY_RUNNER_TOKEN": "a-runner-token-of-24-chars-or-more"},
		"short password":       {"REMEDY_ADMIN_PASSWORD": "short", "REMEDY_RUNNER_TOKEN": "a-runner-token-of-24-chars-or-more"},
		"missing runner token": {"REMEDY_ADMIN_PASSWORD": "a-long-enough-password"},
		"short runner token":   {"REMEDY_ADMIN_PASSWORD": "a-long-enough-password", "REMEDY_RUNNER_TOKEN": "short"},
	}
	for name, m := range cases {
		if _, err := config.ServerFromEnv(env(m)); err == nil {
			t.Errorf("%s: expected an error", name)
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
