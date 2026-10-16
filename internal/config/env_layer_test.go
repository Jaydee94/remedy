package config_test

import (
	"bytes"
	"encoding/base64"
	"slices"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/settings"
)

func baseEnv() map[string]string {
	return map[string]string{
		"REMEDY_ADMIN_PASSWORD": "a-long-enough-password",
		"REMEDY_RUNNER_TOKEN":   "a-runner-token-of-24-chars!!",
		"REMEDY_MASTER_KEY":     base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32)),
	}
}

func TestTheServerKeepsTheEnvironmentAsALayerOfItsOwn(t *testing.T) {
	env := baseEnv()
	env["REMEDY_POLL_INTERVAL"] = "30s"
	env["REMEDY_DIAGNOSE_MAX_PER_DAY"] = "5"
	env["REMEDY_RUN_TIMEOUT"] = "20m"
	c, err := config.ServerFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.PollInterval != 30*time.Second || c.DiagnoseMaxPerDay != 5 || c.DiagnoseCooldown != 15*time.Minute || c.DiagnoseMaxPerIncident != 3 {
		t.Errorf("fields = %v %d %v %d", c.PollInterval, c.DiagnoseMaxPerDay, c.DiagnoseCooldown, c.DiagnoseMaxPerIncident)
	}
	if len(c.Env) != 3 || c.Env.Duration(settings.PollInterval) != 30*time.Second || c.Env.Int(settings.DiagnoseMaxPerDay) != 5 ||
		c.Env.Duration(settings.RunTimeout) != 20*time.Minute {
		t.Errorf("layer = %v", c.Env)
	}
	if _, set := c.Env[settings.DiagnoseCooldown]; set {
		t.Error("a variable that was not set is in the layer")
	}
}

func TestTheNamespacesOfTheEnvironmentAreInTheLayerAndOnTheCluster(t *testing.T) {
	env := baseEnv()
	env["REMEDY_K8S_READ_TOKEN_FILE"] = tokenFile(t, "read", "a-token\n")
	env["REMEDY_K8S_WRITE_TOKEN_FILE"] = tokenFile(t, "write", "a-token\n")
	env["REMEDY_K8S_WRITE_NAMESPACES"] = "demo,staging"
	c, err := config.ServerFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"demo", "staging"}
	if !slices.Equal(c.Cluster.WriteNamespaces, want) || !slices.Equal(c.Env.List(settings.K8sWriteNamespaces), want) {
		t.Errorf("cluster = %v, layer = %v", c.Cluster.WriteNamespaces, c.Env.List(settings.K8sWriteNamespaces))
	}
}

func TestThePrecedenceOfTheErrorsStaysAsItWas(t *testing.T) {
	env := baseEnv()
	env["REMEDY_POLL_INTERVAL"] = "nonsense"
	env["REMEDY_RUN_TIMEOUT"] = "nonsense"
	_, err := config.ServerFromEnv(func(k string) string { return env[k] })
	if err == nil || err.Error() != "REMEDY_POLL_INTERVAL must be a duration of at least 10s, for example 60s or 2m" {
		t.Fatalf("error = %v", err)
	}
}
