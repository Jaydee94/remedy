package deploy_test

import (
	"reflect"
	"testing"
)

// The login from the Setup page has no switch (decision 2026-10-22): the chart has no value for it and sets no variable
// for it. It opens no network path either: the relay uses the runner API that exists already. An old values file that
// still sets loginRelay.enabled renders without an error, and the value has no effect.

const oldLoginRelayVar = "REMEDY_LOGIN_RELAY"

// everythingOn is a value set that renders every workload the chart has: the cluster read side, the write side with
// its CronJob and hook Job, and the policies.
func everythingOn() map[string]any {
	values := baseValues()
	set(values, "cluster.enabled", true)
	set(values, "cluster.write.enabled", true)
	set(values, "cluster.write.namespaces", []any{"demo"})
	set(values, "networkPolicy.apiServer.cidrs", []any{"172.18.0.2/32"})
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

func TestNoWorkloadHasTheOldLoginRelaySwitch(t *testing.T) {
	for name, values := range map[string]map[string]any{"defaults": baseValues(), "everything on": everythingOn()} {
		t.Run(name, func(t *testing.T) {
			specs := podSpecs(t, mustRender(t, values))
			// The render must really contain the server and the runner, or "no workload" would hold for nothing.
			for _, w := range []string{"Deployment/remedy-server", "StatefulSet/remedy-runner"} {
				if specs[w] == nil {
					t.Fatalf("no %s in the render", w)
				}
			}
			if name == "everything on" && len(specs) < 4 {
				t.Fatalf("only %d workloads rendered, want the server, the runner and the token refresher's CronJob and hook Job", len(specs))
			}
			for w, spec := range specs {
				for _, list := range []string{"containers", "initContainers"} {
					cs, _ := spec[list].([]any)
					for _, c := range cs {
						if _, ok := env(c.(map[string]any))[oldLoginRelayVar]; ok {
							t.Errorf("%s %v has %s", w, c.(map[string]any)["name"], oldLoginRelayVar)
						}
					}
				}
			}
		})
	}
}

// A values file written for the switch (loginRelay.enabled true or false) still renders, and renders exactly what a file
// without it renders: the NetworkPolicies and every other document.
func TestAnOldLoginRelayValueChangesNothing(t *testing.T) {
	want := mustRender(t, everythingOn())
	if n := len(want.all("NetworkPolicy")); n < 3 {
		t.Fatalf("%d NetworkPolicies rendered, want at least the server, the runner and the refresher", n)
	}
	for _, enabled := range []bool{true, false} {
		values := everythingOn()
		set(values, "loginRelay.enabled", enabled)
		got := mustRender(t, values)
		if !reflect.DeepEqual(got.all("NetworkPolicy"), want.all("NetworkPolicy")) {
			t.Errorf("loginRelay.enabled=%v changes the NetworkPolicies:\nwithout: %s\nwith: %s", enabled,
				toString(want.all("NetworkPolicy")), toString(got.all("NetworkPolicy")))
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("loginRelay.enabled=%v changes the render", enabled)
		}
	}
}
