package config_test

import (
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/config"
)

func TestPollIntervalDefaultsToAMinute(t *testing.T) {
	c, err := config.ServerFromEnv(serverEnv(nil))
	if err != nil || c.PollInterval != time.Minute {
		t.Fatalf("PollInterval = %s, err = %v", c.PollInterval, err)
	}
}

func TestPollIntervalIsConfigurable(t *testing.T) {
	c, err := config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_POLL_INTERVAL": "2m30s"}))
	if err != nil || c.PollInterval != 150*time.Second {
		t.Fatalf("PollInterval = %s, err = %v", c.PollInterval, err)
	}
}

func TestPollIntervalRejectsNonsenseAndTooFastPolling(t *testing.T) {
	for _, bad := range []string{"soon", "60", "5s", "0s", "-1m"} {
		if _, err := config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_POLL_INTERVAL": bad})); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}
