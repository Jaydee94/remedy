// Package deploy_test renders the Helm chart in deploy/chart and checks the objects. The tests run `helm template`, so
// they need helm: without it they skip, unless REMEDY_REQUIRE_HELM is set (make chart-check and CI set it).
package deploy_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const chartDir = "chart"

// baseValues are the values a chart needs to render at all, and nothing else.
func baseValues() map[string]any {
	sha := strings.Repeat("a", 64)
	return map[string]any{
		"existingSecret": map[string]any{"name": "remedy-secrets"},
		"runner": map[string]any{"cli": map[string]any{
			"version":     "2.1.288",
			"urlTemplate": "https://downloads.example.invalid/{version}/{platform}/claude",
			"archive":     "none",
			"platforms": map[string]any{
				"amd64": map[string]any{"name": "linux-x64", "sha256": sha},
				"arm64": map[string]any{"name": "linux-arm64", "sha256": sha},
			},
		}},
	}
}

// set puts v at a dotted path in m, creating the maps on the way.
func set(m map[string]any, path string, v any) {
	keys := strings.Split(path, ".")
	for _, k := range keys[:len(keys)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[k] = next
		}
		m = next
	}
	m[keys[len(keys)-1]] = v
}

type docs []map[string]any

func helmBinary(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("helm")
	if err != nil {
		if os.Getenv("REMEDY_REQUIRE_HELM") != "" {
			t.Fatal("helm is required (REMEDY_REQUIRE_HELM is set) but is not installed")
		}
		t.Skip("helm is not installed")
	}
	return path
}

// render runs `helm template` for the values and returns the objects, the error output and the error of helm.
func render(t *testing.T, values map[string]any) (docs, string, error) {
	t.Helper()
	bin := helmBinary(t)
	file := filepath.Join(t.TempDir(), "values.yaml")
	raw, err := yaml.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "template", "remedy", chartDir, "--namespace", "remedy-system", "-f", file)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, stderr.String(), err
	}
	dec := yaml.NewDecoder(&stdout)
	var out docs
	for {
		var d map[string]any
		err := dec.Decode(&d)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("helm's output is not YAML: %v", err)
		}
		if d != nil {
			out = append(out, d)
		}
	}
	return out, "", nil
}

func mustRender(t *testing.T, values map[string]any) docs {
	t.Helper()
	d, stderr, err := render(t, values)
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, stderr)
	}
	return d
}

// mustFail renders and requires an error whose output contains want.
func mustFail(t *testing.T, values map[string]any, want string) {
	t.Helper()
	_, stderr, err := render(t, values)
	if err == nil {
		t.Fatalf("the render must fail with %q, but it succeeded", want)
	}
	if !strings.Contains(stderr, want) {
		t.Fatalf("the render failed, but its message does not contain %q:\n%s", want, stderr)
	}
}

func (d docs) all(kind string) []map[string]any {
	var out []map[string]any
	for _, o := range d {
		if o["kind"] == kind {
			out = append(out, o)
		}
	}
	return out
}

// find returns the object of the kind and name, or nil.
func (d docs) find(kind, name string) map[string]any {
	for _, o := range d.all(kind) {
		if dig(nil, o, "metadata", "name") == name {
			return o
		}
	}
	return nil
}

// dig walks maps by string keys and lists by int indexes and returns nil where the path ends. t may be nil.
func dig(t *testing.T, v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil
			}
			v = m[k]
		case int:
			l, ok := v.([]any)
			if !ok || k >= len(l) {
				return nil
			}
			v = l[k]
		}
	}
	return v
}

// container returns the container or init container of a pod spec by name, or fails.
func container(t *testing.T, pod map[string]any, name string) map[string]any {
	t.Helper()
	for _, list := range []string{"containers", "initContainers"} {
		cs, _ := pod[list].([]any)
		for _, c := range cs {
			if m, ok := c.(map[string]any); ok && m["name"] == name {
				return m
			}
		}
	}
	t.Fatalf("no container %q in the pod", name)
	return nil
}

// env returns a container's environment by variable name.
func env(c map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	list, _ := c["env"].([]any)
	for _, e := range list {
		if m, ok := e.(map[string]any); ok {
			out[m["name"].(string)] = m
		}
	}
	return out
}

// toString is a value as YAML text, for substring checks.
func toString(v any) string {
	raw, _ := yaml.Marshal(v)
	return string(raw)
}

// podSpec returns the pod spec of a Deployment or StatefulSet.
func podSpec(t *testing.T, workload map[string]any) map[string]any {
	t.Helper()
	spec, ok := dig(t, workload, "spec", "template", "spec").(map[string]any)
	if !ok {
		t.Fatalf("%v has no pod spec", dig(t, workload, "metadata", "name"))
	}
	return spec
}
