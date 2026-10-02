package config_test

import (
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/config"
)

func TestDiagnoseLimitsDefaultToTheSpec(t *testing.T) {
	c, err := config.ServerFromEnv(serverEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.DiagnoseCooldown != 15*time.Minute || c.DiagnoseMaxPerIncident != 3 || c.DiagnoseMaxPerDay != 20 {
		t.Fatalf("limits = %s, %d, %d", c.DiagnoseCooldown, c.DiagnoseMaxPerIncident, c.DiagnoseMaxPerDay)
	}
}

func TestDiagnoseLimitsAreConfigurable(t *testing.T) {
	c, err := config.ServerFromEnv(serverEnv(map[string]string{
		"REMEDY_DIAGNOSE_COOLDOWN": "90s", "REMEDY_DIAGNOSE_MAX_PER_INCIDENT": "1", "REMEDY_DIAGNOSE_MAX_PER_DAY": "0",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.DiagnoseCooldown != 90*time.Second || c.DiagnoseMaxPerIncident != 1 || c.DiagnoseMaxPerDay != 0 {
		t.Fatalf("limits = %s, %d, %d", c.DiagnoseCooldown, c.DiagnoseMaxPerIncident, c.DiagnoseMaxPerDay)
	}
}

func TestDiagnoseLimitsRejectNonsense(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"cooldown not a duration": {"REMEDY_DIAGNOSE_COOLDOWN": "soon"},
		"cooldown too short":      {"REMEDY_DIAGNOSE_COOLDOWN": "30s"},
		"cooldown without a unit": {"REMEDY_DIAGNOSE_COOLDOWN": "15"},
		"cap not a number":        {"REMEDY_DIAGNOSE_MAX_PER_INCIDENT": "many"},
		"cap negative":            {"REMEDY_DIAGNOSE_MAX_PER_INCIDENT": "-1"},
		"cap too high":            {"REMEDY_DIAGNOSE_MAX_PER_INCIDENT": "21"},
		"daily limit negative":    {"REMEDY_DIAGNOSE_MAX_PER_DAY": "-5"},
		"daily limit too high":    {"REMEDY_DIAGNOSE_MAX_PER_DAY": "201"},
		"daily limit with a unit": {"REMEDY_DIAGNOSE_MAX_PER_DAY": "20/day"},
	} {
		if _, err := config.ServerFromEnv(serverEnv(env)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
