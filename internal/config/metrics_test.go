package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/config"
)

func TestAReadableMetricsCAFileWithoutACertificateStopsTheServer(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, []byte("not a certificate, but readable"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := baseEnv()
	env["REMEDY_METRICS_CA_FILE"] = ca
	_, err := config.ServerFromEnv(func(k string) string { return env[k] })
	if err == nil || err.Error() != "REMEDY_METRICS_CA_FILE: the file holds no certificate" {
		t.Fatalf("err = %v", err)
	}
}

func TestTheMetricsCAFileIsRead(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, selfSignedPEM(t), 0o600); err != nil {
		t.Fatal(err)
	}
	env := baseEnv()
	env["REMEDY_METRICS_CA_FILE"] = ca
	c, err := config.ServerFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.MetricsCAFile != ca || c.AlertmanagerCAFile != "" {
		t.Errorf("MetricsCAFile = %q, AlertmanagerCAFile = %q", c.MetricsCAFile, c.AlertmanagerCAFile)
	}
}

func TestWithoutTheVariableThereIsNoMetricsCAFile(t *testing.T) {
	env := baseEnv()
	c, err := config.ServerFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.MetricsCAFile != "" {
		t.Errorf("MetricsCAFile = %q", c.MetricsCAFile)
	}
}

func TestAMissingMetricsCAFileStopsTheServer(t *testing.T) {
	env := baseEnv()
	env["REMEDY_METRICS_CA_FILE"] = filepath.Join(t.TempDir(), "missing.pem")
	_, err := config.ServerFromEnv(func(k string) string { return env[k] })
	if err == nil || !strings.HasPrefix(err.Error(), "REMEDY_METRICS_CA_FILE:") {
		t.Fatalf("err = %v", err)
	}
}
