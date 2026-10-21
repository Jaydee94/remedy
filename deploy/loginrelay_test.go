package deploy_test

import (
	"reflect"
	"testing"
)

// loginRelay.enabled is one switch that sets REMEDY_LOGIN_RELAY=on on the control plane and on the runner, and nowhere
// else, so the two cannot disagree. It opens no network path: the relay uses the runner API that exists already.

const loginRelayVar = "REMEDY_LOGIN_RELAY"

// everythingOn is a value set that renders every workload the chart has: the cluster read side, the write side with
// its CronJob and hook Job, and the policies.
func everythingOn(relay bool) map[string]any {
	values := baseValues()
	set(values, "cluster.enabled", true)
	set(values, "cluster.write.enabled", true)
	set(values, "cluster.write.namespaces", []any{"demo"})
	set(values, "networkPolicy.apiServer.cidrs", []any{"172.18.0.2/32"})
	if relay {
		set(values, "loginRelay.enabled", true)
	}
	return values
}

// podSpecs returns the pod spec of every workload in the render, by "Kind/name".
func podSpecs(t *testing.T, d docs) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, o := range d {
		var path []any
		switch o["kind"] {
		case "Deployment", "StatefulSet", "DaemonSet", "Job":
			path = []any{"spec", "template", "spec"}
		case "CronJob":
			path = []any{"spec", "jobTemplate", "spec", "template", "spec"}
		default:
			continue
		}
		spec, ok := dig(t, o, path...).(map[string]any)
		if !ok {
			t.Fatalf("%v/%v has no pod spec", o["kind"], dig(t, o, "metadata", "name"))
		}
		out[o["kind"].(string)+"/"+dig(t, o, "metadata", "name").(string)] = spec
	}
	return out
}

func TestLoginRelayIsOffByDefault(t *testing.T) {
	d := mustRender(t, baseValues())
	if _, ok := env(container(t, podSpec(t, d.find("Deployment", "remedy-server")), "server"))[loginRelayVar]; ok {
		t.Errorf("the server container has %s with the default values", loginRelayVar)
	}
	if _, ok := env(container(t, podSpec(t, d.find("StatefulSet", "remedy-runner")), "runner"))[loginRelayVar]; ok {
		t.Errorf("the runner container has %s with the default values", loginRelayVar)
	}
	// Explicitly false is the same.
	values := baseValues()
	set(values, "loginRelay.enabled", false)
	d = mustRender(t, values)
	for name, spec := range podSpecs(t, d) {
		for _, list := range []string{"containers", "initContainers"} {
			cs, _ := spec[list].([]any)
			for _, c := range cs {
				if _, ok := env(c.(map[string]any))[loginRelayVar]; ok {
					t.Errorf("%s %v has %s with loginRelay.enabled=false", name, c.(map[string]any)["name"], loginRelayVar)
				}
			}
		}
	}
}

func TestLoginRelayOnSetsTheVariableOnServerAndRunnerOnly(t *testing.T) {
	d := mustRender(t, everythingOn(true))
	specs := podSpecs(t, d)
	// The render must really contain the workloads this test is about, or "nowhere else" would hold for nothing.
	for _, name := range []string{"Deployment/remedy-server", "StatefulSet/remedy-runner"} {
		if specs[name] == nil {
			t.Fatalf("no %s in the render", name)
		}
	}
	if len(specs) < 4 {
		t.Fatalf("only %d workloads rendered (%v), want the server, the runner and the token refresher's CronJob and hook Job", len(specs), specs)
	}
	var withVar []string
	for name, spec := range specs {
		for _, list := range []string{"containers", "initContainers"} {
			cs, _ := spec[list].([]any)
			for _, c := range cs {
				cm := c.(map[string]any)
				e, ok := env(cm)[loginRelayVar]
				if !ok {
					continue
				}
				withVar = append(withVar, name+"/"+cm["name"].(string))
				if e["value"] != "on" {
					t.Errorf("%s %v: %s = %v, want the string on", name, cm["name"], loginRelayVar, e["value"])
				}
			}
		}
	}
	want := map[string]bool{"Deployment/remedy-server/server": true, "StatefulSet/remedy-runner/runner": true}
	got := map[string]bool{}
	for _, w := range withVar {
		got[w] = true
	}
	if !reflect.DeepEqual(got, want) || len(withVar) != 2 {
		t.Errorf("%s is set on %v, want only the server container and the runner container", loginRelayVar, withVar)
	}
	if e := env(container(t, specs["Deployment/remedy-server"], "server"))[loginRelayVar]; e["value"] != "on" {
		t.Errorf("server: %v", e)
	}
	if e := env(container(t, specs["StatefulSet/remedy-runner"], "runner"))[loginRelayVar]; e["value"] != "on" {
		t.Errorf("runner: %v", e)
	}
}

func TestLoginRelayLeavesTheNetworkPoliciesAlone(t *testing.T) {
	off := mustRender(t, everythingOn(false)).all("NetworkPolicy")
	on := mustRender(t, everythingOn(true)).all("NetworkPolicy")
	if len(off) < 3 {
		t.Fatalf("%d NetworkPolicies rendered, want at least the server, the runner and the refresher", len(off))
	}
	if !reflect.DeepEqual(off, on) {
		t.Errorf("the NetworkPolicies differ with loginRelay.enabled:\noff: %s\non: %s", toString(off), toString(on))
	}
}

func TestLoginRelayInServerEnvIsRefused(t *testing.T) {
	values := baseValues()
	set(values, "loginRelay.enabled", true)
	set(values, "server.env", map[string]any{loginRelayVar: "off"})
	mustFail(t, values, "server.env."+loginRelayVar)
}

// The refusal does not depend on the switch: a server.env entry with the switch off or unset would turn the control
// plane on and leave the runner off.
func TestLoginRelayInServerEnvIsRefusedWithTheSwitchOffOrUnset(t *testing.T) {
	for _, name := range []string{"switch false", "switch unset"} {
		t.Run(name, func(t *testing.T) {
			values := baseValues()
			if name == "switch false" {
				set(values, "loginRelay.enabled", false)
			}
			set(values, "server.env", map[string]any{loginRelayVar: "on"})
			mustFail(t, values, "loginRelay.enabled")
		})
	}
}
