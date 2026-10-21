package deploy_test

import (
	"fmt"
	"strings"
	"testing"
)

// The two typed egress values of the control plane: networkPolicy.hosts (SSH to single addresses) and
// networkPolicy.alertmanager (one pod selection in one namespace). Both land in the remedy-server policy only.

func hostEntry(cidr string, port any) map[string]any {
	e := map[string]any{"cidr": cidr}
	if port != nil {
		e["port"] = port
	}
	return e
}

func alertmanagerValues(ns string, labels map[string]any) map[string]any {
	am := map[string]any{}
	if ns != "" {
		am["namespace"] = ns
	}
	if labels != nil {
		am["podSelector"] = labels
	}
	return am
}

// extraRules are the egress rules of the server policy that are neither DNS, the API server nor the internet rule.
func extraRules(t *testing.T, p map[string]any) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, r := range egressRules(t, p) {
		text := toString(r)
		if strings.Contains(text, "k8s-app: kube-dns") || strings.Contains(text, "cidr: 0.0.0.0/0") {
			continue
		}
		if ports := rulePorts(r); len(ports) == 1 && ports[0] == "TCP/6443" {
			continue
		}
		out = append(out, r)
	}
	return out
}

func TestHostAndAlertmanagerEgressAreAbsentByDefault(t *testing.T) {
	d := mustRender(t, baseValues())
	if n := len(egressRules(t, policy(t, d, "remedy-server"))); n != 2 {
		t.Errorf("cluster off: %d egress rules on the server policy, want 2 (DNS, internet)", n)
	}
	values := baseValues()
	set(values, "cluster.enabled", true)
	set(values, "networkPolicy.apiServer.cidrs", []any{"172.18.0.2/32"})
	d = mustRender(t, values)
	if n := len(egressRules(t, policy(t, d, "remedy-server"))); n != 3 {
		t.Errorf("cluster on: %d egress rules on the server policy, want 3 (DNS, API server, internet)", n)
	}
}

func TestAHostEntryOpensOneSSHRuleOnTheServerPolicyOnly(t *testing.T) {
	values := baseValues()
	set(values, "cluster.enabled", true)
	set(values, "cluster.write.enabled", true)
	set(values, "cluster.write.namespaces", []any{"demo"})
	set(values, "networkPolicy.apiServer.cidrs", []any{"172.18.0.2/32"})
	set(values, "networkPolicy.hosts", []any{hostEntry("192.168.178.118/32", 22)})
	d := mustRender(t, values)

	server := policy(t, d, "remedy-server")
	rules := egressRules(t, server)
	if len(rules) != 4 {
		t.Fatalf("%d egress rules on the server policy, want 4 (DNS, API server, host, internet): %v", len(rules), rules)
	}
	extra := extraRules(t, server)
	if len(extra) != 1 {
		t.Fatalf("%d extra rules, want 1: %v", len(extra), extra)
	}
	peers := listOf(extra[0]["to"])
	if len(peers) != 1 {
		t.Fatalf("the host rule has %d peers, want 1: %v", len(peers), peers)
	}
	ib, ok := dig(t, peers[0], "ipBlock").(map[string]any)
	if !ok || len(ib) != 1 || ib["cidr"] != "192.168.178.118/32" {
		t.Errorf("the host rule's peer must be only the ipBlock of the address, without except: %v", peers[0])
	}
	if got := rulePorts(extra[0]); len(got) != 1 || got[0] != "TCP/22" {
		t.Errorf("the host rule's ports = %v, want [TCP/22]", got)
	}
	// Order: after the API server rule, before the internet rule.
	idx := func(sub string) int {
		for i, r := range rules {
			if strings.Contains(toString(r), sub) {
				return i
			}
		}
		return -1
	}
	if !(idx("172.18.0.2/32") < idx("192.168.178.118/32") && idx("192.168.178.118/32") < idx("cidr: 0.0.0.0/0")) {
		t.Errorf("the host rule must come after the API server rule and before the internet rule: %v", rules)
	}
	// The runner and the refresher never get it.
	for _, name := range []string{"remedy-runner", "remedy-token-refresher"} {
		if text := toString(policy(t, d, name)); strings.Contains(text, "192.168.178.118") {
			t.Errorf("%s must not carry the host rule:\n%s", name, text)
		}
	}
}

func TestSeveralHostEntriesMakeSeveralRules(t *testing.T) {
	values := baseValues()
	set(values, "networkPolicy.hosts", []any{
		hostEntry("192.168.178.118/32", 22),
		hostEntry("192.168.178.119/32", 2222),
		hostEntry("fd00::1/128", 22),
	})
	extra := extraRules(t, policy(t, mustRender(t, values), "remedy-server"))
	if len(extra) != 3 {
		t.Fatalf("%d host rules, want 3: %v", len(extra), extra)
	}
	want := []struct{ cidr, port string }{{"192.168.178.118/32", "TCP/22"}, {"192.168.178.119/32", "TCP/2222"}, {"fd00::1/128", "TCP/22"}}
	for i, w := range want {
		blocks := ipBlocks(extra[i : i+1])
		if len(blocks) != 1 || blocks[0].cidr != w.cidr || len(blocks[0].except) != 0 {
			t.Errorf("rule %d: ipBlocks = %v, want only %s", i, blocks, w.cidr)
		}
		if got := rulePorts(extra[i]); len(got) != 1 || got[0] != w.port {
			t.Errorf("rule %d: ports = %v, want [%s]", i, got, w.port)
		}
	}
}

func TestHostEntriesAreSingleAddressesOnly(t *testing.T) {
	refused := []struct {
		name  string
		entry map[string]any
	}{
		{"an IPv4 range", hostEntry("192.168.178.0/24", 22)},
		{"a /31", hostEntry("192.168.178.118/31", 22)},
		{"everything", hostEntry("0.0.0.0/0", 22)},
		{"no length", hostEntry("192.168.178.118", 22)},
		{"an octet above 255", hostEntry("192.168.178.999/32", 22)},
		{"an IPv6 default route", hostEntry("::/0", 22)},
		{"an IPv6 range", hostEntry("fd00::/64", 22)},
		{"a host name", hostEntry("nas.example/32", 22)},
		{"port 0", hostEntry("192.168.178.118/32", 0)},
		{"port 70000", hostEntry("192.168.178.118/32", 70000)},
		{"a negative port", hostEntry("192.168.178.118/32", -22)},
		{"a fractional port", hostEntry("192.168.178.118/32", 22.5)},
		{"a port that is text", hostEntry("192.168.178.118/32", "22")},
		{"a missing port", hostEntry("192.168.178.118/32", nil)},
		{"a missing cidr", map[string]any{"port": 22}},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			values := baseValues()
			set(values, "networkPolicy.hosts", []any{c.entry})
			mustFail(t, values, "networkPolicy.hosts")
		})
	}
	t.Run("an entry that is not an object", func(t *testing.T) {
		values := baseValues()
		set(values, "networkPolicy.hosts", []any{"192.168.178.118/32"})
		mustFail(t, values, "networkPolicy.hosts")
	})
	t.Run("a second entry is checked too", func(t *testing.T) {
		values := baseValues()
		set(values, "networkPolicy.hosts", []any{hostEntry("192.168.178.118/32", 22), hostEntry("10.0.0.0/8", 22)})
		mustFail(t, values, "networkPolicy.hosts")
	})
	for _, c := range []struct {
		cidr string
		port any
		want string
	}{
		{"fd00::1/128", 22, "TCP/22"},
		{"192.168.178.118/32", 65535, "TCP/65535"},
		{"192.168.178.118/32", 1, "TCP/1"},
		{"192.168.178.118/32", 22.0, "TCP/22"},
	} {
		values := baseValues()
		set(values, "networkPolicy.hosts", []any{hostEntry(c.cidr, c.port)})
		extra := extraRules(t, policy(t, mustRender(t, values), "remedy-server"))
		if len(extra) != 1 {
			t.Errorf("%s port %v: %d host rules, want 1", c.cidr, c.port, len(extra))
			continue
		}
		if got := rulePorts(extra[0]); len(got) != 1 || got[0] != c.want {
			t.Errorf("%s port %v: ports = %v, want [%s]", c.cidr, c.port, got, c.want)
		}
	}
}

func TestAHostEntryMayNotBeTheAPIServer(t *testing.T) {
	values := baseValues()
	set(values, "cluster.enabled", true)
	set(values, "networkPolicy.apiServer.cidrs", []any{"172.18.0.2/32", "172.18.0.3/32"})
	set(values, "networkPolicy.hosts", []any{hostEntry("172.18.0.3/32", 22)})
	mustFail(t, values, "networkPolicy.hosts")
	// A different address is fine next to it.
	set(values, "networkPolicy.hosts", []any{hostEntry("172.18.0.4/32", 22)})
	mustRender(t, values)
}

func TestTheAlertmanagerRuleNamesNamespaceAndPodLabels(t *testing.T) {
	values := baseValues()
	set(values, "networkPolicy.alertmanager", alertmanagerValues("monitoring", map[string]any{"app.kubernetes.io/name": "vmalertmanager"}))
	d := mustRender(t, values)
	server := policy(t, d, "remedy-server")
	extra := extraRules(t, server)
	if len(extra) != 1 {
		t.Fatalf("%d extra rules, want 1 (Alertmanager): %v", len(extra), extra)
	}
	peers := listOf(extra[0]["to"])
	if len(peers) != 1 {
		t.Fatalf("the Alertmanager rule has %d peers, want 1: %v", len(peers), peers)
	}
	if got := dig(t, peers[0], "namespaceSelector", "matchLabels", "kubernetes.io/metadata.name"); got != "monitoring" {
		t.Errorf("namespaceSelector = %v, want monitoring", got)
	}
	if got := dig(t, peers[0], "podSelector", "matchLabels", "app.kubernetes.io/name"); got != "vmalertmanager" {
		t.Errorf("podSelector = %v, want vmalertmanager", got)
	}
	if m := peers[0].(map[string]any); len(m) != 2 {
		t.Errorf("the peer must have exactly the two selectors in one entry: %v", m)
	}
	if got := rulePorts(extra[0]); len(got) != 1 || got[0] != "TCP/9093" {
		t.Errorf("default ports = %v, want [TCP/9093]", got)
	}
	for _, name := range []string{"remedy-runner"} {
		if text := toString(policy(t, d, name)); strings.Contains(text, "vmalertmanager") || strings.Contains(text, "monitoring") {
			t.Errorf("%s must not carry the Alertmanager rule:\n%s", name, text)
		}
	}

	am := alertmanagerValues("monitoring", map[string]any{"app.kubernetes.io/name": "vmalertmanager"})
	am["port"] = 9094
	set(values, "networkPolicy.alertmanager", am)
	extra = extraRules(t, policy(t, mustRender(t, values), "remedy-server"))
	if len(extra) != 1 {
		t.Fatalf("%d extra rules, want 1", len(extra))
	}
	if got := rulePorts(extra[0]); len(got) != 1 || got[0] != "TCP/9094" {
		t.Errorf("ports = %v, want [TCP/9094]", got)
	}
}

func TestTheAlertmanagerRuleNeedsANamespaceAndALabel(t *testing.T) {
	labels := map[string]any{"app.kubernetes.io/name": "vmalertmanager"}
	withPort := func(am map[string]any, port any) map[string]any { am["port"] = port; return am }
	refused := []struct {
		name string
		am   map[string]any
	}{
		{"no namespace", alertmanagerValues("", labels)},
		{"no podSelector", alertmanagerValues("monitoring", nil)},
		{"an empty podSelector", alertmanagerValues("monitoring", map[string]any{})},
		{"an upper case namespace", alertmanagerValues("Monitoring", labels)},
		{"a namespace with a slash", alertmanagerValues("monitoring/x", labels)},
		{"a namespace of 64 characters", alertmanagerValues(strings.Repeat("a", 64), labels)},
		{"port 0", withPort(alertmanagerValues("monitoring", labels), 0)},
		{"port 70000", withPort(alertmanagerValues("monitoring", labels), 70000)},
		{"a fractional port", withPort(alertmanagerValues("monitoring", labels), 9093.5)},
		{"only a port", map[string]any{"port": 9093}},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			values := baseValues()
			set(values, "networkPolicy.alertmanager", c.am)
			mustFail(t, values, "networkPolicy.alertmanager")
		})
	}
	t.Run("a namespace of 63 characters renders", func(t *testing.T) {
		values := baseValues()
		set(values, "networkPolicy.alertmanager", alertmanagerValues(strings.Repeat("a", 63), labels))
		mustRender(t, values)
	})
}

func TestPoliciesOffIgnoreBothValues(t *testing.T) {
	values := baseValues()
	set(values, "networkPolicy.enabled", false)
	// Values that would be refused with the policies on.
	set(values, "networkPolicy.hosts", []any{hostEntry("0.0.0.0/0", 0)})
	set(values, "networkPolicy.alertmanager", map[string]any{"port": 0})
	d := mustRender(t, values)
	if n := len(d.all("NetworkPolicy")); n != 0 {
		t.Errorf("%d NetworkPolicy objects with networkPolicy.enabled=false, want 0", n)
	}
}

// The refusal messages name the value and the rule, so the maintainer sees what to change.
func TestTheRefusalMessagesNameTheRule(t *testing.T) {
	values := baseValues()
	set(values, "networkPolicy.hosts", []any{hostEntry("192.168.178.0/24", 22)})
	_, stderr, err := render(t, values)
	if err == nil {
		t.Fatal("the render must fail")
	}
	for _, want := range []string{"networkPolicy.hosts", "192.168.178.0/24", "/32"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the message must contain %q:\n%s", want, fmt.Sprint(stderr))
		}
	}
}
