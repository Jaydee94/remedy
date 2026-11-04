package deploy_test

import (
	"strings"
	"testing"
)

// allPodSpecs returns the pod spec of every workload of the render, by "Kind/name".
func allPodSpecs(t *testing.T, d docs) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, kind := range []string{"Deployment", "StatefulSet", "Job", "CronJob"} {
		for _, w := range d.all(kind) {
			var spec any
			switch kind {
			case "CronJob":
				spec = dig(nil, w, "spec", "jobTemplate", "spec", "template", "spec")
			default:
				spec = dig(nil, w, "spec", "template", "spec")
			}
			if m, ok := spec.(map[string]any); ok {
				name, _ := dig(nil, w, "metadata", "name").(string)
				out[kind+"/"+name] = m
			}
		}
	}
	return out
}

func apiKeyValues() map[string]any {
	v := bundledValues() // the bundled database adds a StatefulSet and a CronJob to the walk
	set(v, "runner.auth.mode", "apiKey")
	set(v, "runner.auth.apiKeySecret.name", "anthropic-key")
	set(v, "runner.auth.apiKeySecret.key", "api-key")
	return v
}

func TestTheDefaultInstallKnowsNoApiKey(t *testing.T) {
	d := mustRender(t, baseValues())
	for name, pod := range allPodSpecs(t, d) {
		for _, list := range []string{"containers", "initContainers"} {
			cs, _ := pod[list].([]any)
			for _, c := range cs {
				e := env(c.(map[string]any))
				if e["ANTHROPIC_API_KEY"] != nil || e["REMEDY_AUTH_MODE"] != nil {
					t.Errorf("%s/%v has an auth variable in the default install", name, c.(map[string]any)["name"])
				}
			}
		}
	}
}

func TestApiKeyModeGivesTheKeyToTheRunnerContainerAndNoOtherContainer(t *testing.T) {
	d := mustRender(t, apiKeyValues())
	pods := allPodSpecs(t, d)
	if len(pods) < 4 {
		t.Fatalf("found only %d workloads: the walk looks at too little", len(pods))
	}
	runnerSeen := false
	for name, pod := range pods {
		for _, list := range []string{"containers", "initContainers"} {
			cs, _ := pod[list].([]any)
			for _, c := range cs {
				cm := c.(map[string]any)
				e := env(cm)
				isRunner := name == "StatefulSet/remedy-runner" && cm["name"] == "runner"
				switch {
				case isRunner:
					runnerSeen = true
					if dig(t, e["ANTHROPIC_API_KEY"], "valueFrom", "secretKeyRef", "name") != "anthropic-key" ||
						dig(t, e["ANTHROPIC_API_KEY"], "valueFrom", "secretKeyRef", "key") != "api-key" {
						t.Errorf("the runner's ANTHROPIC_API_KEY = %v, want a reference to anthropic-key/api-key", e["ANTHROPIC_API_KEY"])
					}
					if _, literal := e["ANTHROPIC_API_KEY"]["value"]; literal {
						t.Error("the key is a literal value")
					}
					if e["REMEDY_AUTH_MODE"]["value"] != "api_key" {
						t.Errorf("REMEDY_AUTH_MODE = %v, want api_key", e["REMEDY_AUTH_MODE"])
					}
				case e["ANTHROPIC_API_KEY"] != nil || e["REMEDY_AUTH_MODE"] != nil:
					t.Errorf("%s/%v has an auth variable: only the runner container may", name, cm["name"])
				}
			}
		}
	}
	if !runnerSeen {
		t.Fatal("the runner container was not found")
	}
	// the Secret is named once in the whole render, and the chart creates none
	raw := toString(d)
	if n := strings.Count(raw, "anthropic-key"); n != 1 {
		t.Errorf("the key Secret is named %d times in the render, want once", n)
	}
	for _, s := range d.all("Secret") {
		if dig(nil, s, "metadata", "name") == "anthropic-key" {
			t.Error("the chart creates the key Secret")
		}
	}
}

func TestApiKeyModeNeedsTheSecretAndNothingElse(t *testing.T) {
	for name, c := range map[string]struct {
		path string
		val  any
		want string
	}{
		"another word for the mode":   {"runner.auth.mode", "apikey", "runner.auth.mode"},
		"the old spelling":            {"runner.auth.mode", "api_key", "runner.auth.mode"},
		"a missing name":              {"runner.auth.apiKeySecret.name", "", "runner.auth.apiKeySecret.name"},
		"a missing key":               {"runner.auth.apiKeySecret.key", "", "runner.auth.apiKeySecret.key"},
		"a key that is no Secret key": {"runner.auth.apiKeySecret.key", "a b", "runner.auth.apiKeySecret.key"},
	} {
		v := apiKeyValues()
		set(v, c.path, c.val)
		t.Run(name, func(t *testing.T) { mustFail(t, v, c.want) })
	}
	// the key itself is never a value of the chart
	v := apiKeyValues()
	set(v, "runner.auth.apiKeySecret.value", "sk-ant-oops")
	mustFail(t, v, "runner.auth.apiKeySecret")
	// a Secret named while the mode is the subscription would do nothing: refused
	v = baseValues()
	set(v, "runner.auth.apiKeySecret.name", "anthropic-key")
	mustFail(t, v, "runner.auth.apiKeySecret")
}

func TestApiKeyModeWithoutARunnerRendersNoKey(t *testing.T) {
	v := apiKeyValues()
	set(v, "runner.enabled", false)
	set(v, "runner.cli", map[string]any{})
	d := mustRender(t, v)
	if strings.Contains(toString(d), "ANTHROPIC_API_KEY") || strings.Contains(toString(d), "anthropic-key") {
		t.Error("a render without the runner mentions the key")
	}
}
