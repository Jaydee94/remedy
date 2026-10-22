package config_test

import (
	"testing"

	"github.com/Jaydee94/remedy/internal/config"
)

func TestAuthModeDefaults(t *testing.T) {
	r, err := config.RunnerFromEnv(runnerEnv(nil))
	if err != nil || r.AuthMode != "subscription" {
		t.Fatalf("AuthMode = %q, err = %v, want subscription", r.AuthMode, err)
	}
	r, err = config.RunnerFromEnv(runnerEnv(map[string]string{"REMEDY_AUTH_MODE": "api_key"}))
	if err != nil || r.AuthMode != "api_key" {
		t.Fatalf("AuthMode = %q, err = %v, want api_key", r.AuthMode, err)
	}
}
