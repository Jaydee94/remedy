package config_test

import (
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/settings"
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

func TestTheAlertmanagerIntervalFollowsThePollIntervalUntilItHasItsOwnVariable(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		want time.Duration
	}{
		"neither":       {nil, time.Minute},
		"only the poll": {map[string]string{"REMEDY_POLL_INTERVAL": "5m"}, 5 * time.Minute},
		"only its own":  {map[string]string{"REMEDY_ALERTMANAGER_POLL_INTERVAL": "20s"}, 20 * time.Second},
		"both":          {map[string]string{"REMEDY_POLL_INTERVAL": "5m", "REMEDY_ALERTMANAGER_POLL_INTERVAL": "20s"}, 20 * time.Second},
	} {
		c, err := config.ServerFromEnv(serverEnv(tc.env))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := c.Env.Duration(settings.AlertmanagerPollInterval); got != tc.want {
			t.Errorf("%s: alertmanager interval = %s, want %s", name, got, tc.want)
		}
		if name == "both" && c.PollInterval != 5*time.Minute {
			t.Errorf("both: the GitHub interval = %s", c.PollInterval)
		}
	}
	if _, err := config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_ALERTMANAGER_POLL_INTERVAL": "5s"})); err == nil {
		t.Error("REMEDY_ALERTMANAGER_POLL_INTERVAL=5s: expected an error")
	}
}
