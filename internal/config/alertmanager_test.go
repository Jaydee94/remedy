package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/config"
)

func TestTheAlertmanagerCAFileIsRead(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, []byte("not a certificate, but readable"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := baseEnv()
	env["REMEDY_ALERTMANAGER_CA_FILE"] = ca
	c, err := config.ServerFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.AlertmanagerCAFile != ca {
		t.Errorf("AlertmanagerCAFile = %q", c.AlertmanagerCAFile)
	}
}

func TestWithoutTheVariableThereIsNoAlertmanagerCAFile(t *testing.T) {
	env := baseEnv()
	c, err := config.ServerFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.AlertmanagerCAFile != "" {
		t.Errorf("AlertmanagerCAFile = %q", c.AlertmanagerCAFile)
	}
}

func TestAMissingAlertmanagerCAFileStopsTheServer(t *testing.T) {
	env := baseEnv()
	env["REMEDY_ALERTMANAGER_CA_FILE"] = filepath.Join(t.TempDir(), "missing.pem")
	_, err := config.ServerFromEnv(func(k string) string { return env[k] })
	if err == nil || !strings.HasPrefix(err.Error(), "REMEDY_ALERTMANAGER_CA_FILE:") {
		t.Fatalf("err = %v", err)
	}
}
