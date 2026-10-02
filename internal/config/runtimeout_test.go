package config_test

import (
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/config"
)

func runnerEnv(extra map[string]string) func(string) string {
	m := map[string]string{"REMEDY_RUNNER_TOKEN": "a-runner-token-of-24-chars-or-more"}
	for k, v := range extra {
		m[k] = v
	}
	return env(m)
}

func TestRunTimeoutDefaultsToTenMinutes(t *testing.T) {
	c, err := config.RunnerFromEnv(runnerEnv(nil))
	if err != nil || c.RunTimeout != 10*time.Minute {
		t.Fatalf("RunTimeout = %s, err = %v", c.RunTimeout, err)
	}
}

func TestRunTimeoutIsConfigurable(t *testing.T) {
	c, err := config.RunnerFromEnv(runnerEnv(map[string]string{"REMEDY_RUN_TIMEOUT": "90s"}))
	if err != nil || c.RunTimeout != 90*time.Second {
		t.Fatalf("RunTimeout = %s, err = %v", c.RunTimeout, err)
	}
}

func TestRunTimeoutRejectsNonsenseAndTinyValues(t *testing.T) {
	for _, bad := range []string{"later", "600", "1s", "0s", "-5m"} {
		if _, err := config.RunnerFromEnv(runnerEnv(map[string]string{"REMEDY_RUN_TIMEOUT": bad})); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}
