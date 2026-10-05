package config_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/config"
)

func tokenFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNoClusterSettingsMeansNoCluster(t *testing.T) {
	c, err := config.ServerFromEnv(serverEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cluster.ReadEnabled() || c.Cluster.WriteEnabled() {
		t.Fatalf("cluster = %+v", c.Cluster)
	}
}

func TestTheClusterSettingsAreRead(t *testing.T) {
	read, write := tokenFile(t, "read", "r-token"), tokenFile(t, "write", "w-token")
	c, err := config.ServerFromEnv(serverEnv(map[string]string{
		"REMEDY_K8S_API":              "https://127.0.0.1:6443/",
		"REMEDY_K8S_READ_TOKEN_FILE":  read,
		"REMEDY_K8S_WRITE_TOKEN_FILE": write,
		"REMEDY_K8S_WRITE_NAMESPACES": "demo, staging",
		"REMEDY_K8S_ARGO_NAMESPACE":   "gitops",
	}))
	if err != nil {
		t.Fatal(err)
	}
	k := c.Cluster
	if k.API != "https://127.0.0.1:6443/" || k.ReadTokenFile != read || k.WriteTokenFile != write ||
		!slices.Equal(k.WriteNamespaces, []string{"demo", "staging"}) || k.ArgoNamespace != "gitops" {
		t.Fatalf("cluster = %+v", k)
	}
	if !k.ReadEnabled() || !k.WriteEnabled() {
		t.Fatal("both sides must be on")
	}
}

func TestAWrongClusterSettingStopsTheServerAndNamesTheVariable(t *testing.T) {
	read := tokenFile(t, "read", "r-token")
	cases := map[string]struct {
		env  map[string]string
		name string
	}{
		"a bad namespace": {map[string]string{"REMEDY_K8S_READ_TOKEN_FILE": read, "REMEDY_K8S_WRITE_TOKEN_FILE": read,
			"REMEDY_K8S_WRITE_NAMESPACES": "demo,*"}, "REMEDY_K8S_WRITE_NAMESPACES"},
		"namespaces without a write token": {map[string]string{"REMEDY_K8S_READ_TOKEN_FILE": read,
			"REMEDY_K8S_WRITE_NAMESPACES": "demo"}, "REMEDY_K8S_WRITE_NAMESPACES"},
		"a missing token file": {map[string]string{"REMEDY_K8S_READ_TOKEN_FILE": filepath.Join(t.TempDir(), "nope")},
			"REMEDY_K8S_READ_TOKEN_FILE"},
		"an API that is not a URL": {map[string]string{"REMEDY_K8S_READ_TOKEN_FILE": read, "REMEDY_K8S_API": "cluster"},
			"REMEDY_K8S_API"},
	}
	for name, tc := range cases {
		_, err := config.ServerFromEnv(serverEnv(tc.env))
		if err == nil || !strings.Contains(err.Error(), tc.name) {
			t.Errorf("%s: error = %v, want one that names %s", name, err, tc.name)
		}
	}
}
