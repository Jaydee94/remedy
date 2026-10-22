package deploy_test

import (
	"strings"
	"testing"
)

// networkPolicy.metrics: the egress of the control plane to a Prometheus-compatible query API inside the cluster, rendered
// and validated like networkPolicy.alertmanager, except that the port is required (Prometheus serves 9090, VictoriaMetrics
// 8428: there is no default that is right for both).

func metricsValues(ns string, labels map[string]any, port any) map[string]any {
	m := alertmanagerValues(ns, labels)
	if port != nil {
		m["port"] = port
	}
	return m
}

func TestTheMetricsRuleNamesNamespacePodLabelsAndPort(t *testing.T) {
	values := baseValues()
	set(values, "networkPolicy.metrics", metricsValues("monitoring", map[string]any{"app.kubernetes.io/name": "vmsingle"}, 8428))
	d := mustRender(t, values)
	server := policy(t, d, "remedy-server")
	extra := extraRules(t, server)
	if len(extra) != 1 {
		t.Fatalf("%d extra rules, want 1 (metrics): %v", len(extra), extra)
	}
	peers := listOf(extra[0]["to"])
	if len(peers) != 1 {
		t.Fatalf("the metrics rule has %d peers, want 1: %v", len(peers), peers)
	}
	if got := dig(t, peers[0], "namespaceSelector", "matchLabels", "kubernetes.io/metadata.name"); got != "monitoring" {
		t.Errorf("namespaceSelector = %v, want monitoring", got)
	}
	if got := dig(t, peers[0], "podSelector", "matchLabels", "app.kubernetes.io/name"); got != "vmsingle" {
		t.Errorf("podSelector = %v, want vmsingle", got)
	}
	if m := peers[0].(map[string]any); len(m) != 2 {
		t.Errorf("the peer must have exactly the two selectors in one entry: %v", m)
	}
	if got := rulePorts(extra[0]); len(got) != 1 || got[0] != "TCP/8428" {
		t.Errorf("ports = %v, want [TCP/8428]", got)
	}
	for _, name := range []string{"remedy-runner"} {
		if text := toString(policy(t, d, name)); strings.Contains(text, "vmsingle") || strings.Contains(text, "monitoring") {
			t.Errorf("%s must not carry the metrics rule:\n%s", name, text)
		}
	}
}

func TestTheMetricsRuleIsAbsentByDefaultAndIndependentOfTheAlertmanagerRule(t *testing.T) {
	if n := len(egressRules(t, policy(t, mustRender(t, baseValues()), "remedy-server"))); n != 2 {
		t.Errorf("no value: %d egress rules on the server policy, want 2 (DNS, internet)", n)
	}
	values := baseValues()
	set(values, "networkPolicy.alertmanager", alertmanagerValues("monitoring", map[string]any{"app.kubernetes.io/name": "vmalertmanager"}))
	extra := extraRules(t, policy(t, mustRender(t, values), "remedy-server"))
	if len(extra) != 1 || strings.Contains(toString(extra[0]), "vmsingle") {
		t.Errorf("the Alertmanager value alone must render only its own rule: %v", extra)
	}

	set(values, "networkPolicy.metrics", metricsValues("monitoring", map[string]any{"app.kubernetes.io/name": "vmsingle"}, 8428))
	server := policy(t, mustRender(t, values), "remedy-server")
	rules := egressRules(t, server)
	extra = extraRules(t, server)
	if len(extra) != 2 {
		t.Fatalf("%d extra rules with both values, want 2: %v", len(extra), extra)
	}
	if got := rulePorts(extra[0]); len(got) != 1 || got[0] != "TCP/9093" {
		t.Errorf("first extra rule ports = %v, want the Alertmanager's [TCP/9093]", got)
	}
	if got := rulePorts(extra[1]); len(got) != 1 || got[0] != "TCP/8428" {
		t.Errorf("second extra rule ports = %v, want the metrics [TCP/8428]", got)
	}
	// The internet rule stays last.
	if last := toString(rules[len(rules)-1]); !strings.Contains(last, "cidr: 0.0.0.0/0") {
		t.Errorf("the internet rule must stay last: %v", rules)
	}
}

func TestTheMetricsRuleNeedsANamespaceALabelAndAPort(t *testing.T) {
	labels := map[string]any{"app.kubernetes.io/name": "vmsingle"}
	refused := []struct {
		name string
		m    map[string]any
	}{
		{"no namespace", metricsValues("", labels, 8428)},
		{"no podSelector", metricsValues("monitoring", nil, 8428)},
		{"an empty podSelector", metricsValues("monitoring", map[string]any{}, 8428)},
		{"an upper case namespace", metricsValues("Monitoring", labels, 8428)},
		{"a namespace of 64 characters", metricsValues(strings.Repeat("a", 64), labels, 8428)},
		{"no port", metricsValues("monitoring", labels, nil)},
		{"port 0", metricsValues("monitoring", labels, 0)},
		{"port 70000", metricsValues("monitoring", labels, 70000)},
		{"a fractional port", metricsValues("monitoring", labels, 8428.5)},
		{"a port that is text", metricsValues("monitoring", labels, "8428")},
		{"only a port", map[string]any{"port": 8428}},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			values := baseValues()
			set(values, "networkPolicy.metrics", c.m)
			mustFail(t, values, "networkPolicy.metrics")
		})
	}
	t.Run("a namespace of 63 characters renders", func(t *testing.T) {
		values := baseValues()
		set(values, "networkPolicy.metrics", metricsValues(strings.Repeat("a", 63), labels, 8428))
		mustRender(t, values)
	})
}

func TestPoliciesOffIgnoreTheMetricsValue(t *testing.T) {
	values := baseValues()
	set(values, "networkPolicy.enabled", false)
	set(values, "networkPolicy.metrics", map[string]any{"port": 0})
	if n := len(mustRender(t, values).all("NetworkPolicy")); n != 0 {
		t.Errorf("%d NetworkPolicy objects with networkPolicy.enabled=false, want 0", n)
	}
}
