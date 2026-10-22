package deploy_test

import (
	"strings"
	"testing"
)

// A database of your own instead of the bundled one (docs/specs/2026-10-22-postgres-only-design.md, decisions 7 and 11;
// the bundled one is in postgres_test.go): the URL holds a password, so it comes from a Secret that exists already (database.existingSecret, key
// database.secretKey), and the control plane's egress to the database is one typed value, networkPolicy.database, in one of
// two forms: pods inside the cluster (namespace + podSelector, like the Alertmanager) or an external server (cidr).

func TestTheServerTakesItsDatabaseURLFromTheSecretAndNeverFromValues(t *testing.T) {
	d := mustRender(t, baseValues())
	e := env(container(t, podSpec(t, d.find("Deployment", "remedy-server")), "server"))
	ref := dig(t, e["REMEDY_DATABASE_URL"], "valueFrom", "secretKeyRef")
	if dig(t, ref, "name") != "remedy-database" || dig(t, ref, "key") != "url" {
		t.Errorf("REMEDY_DATABASE_URL comes from %v, want the Secret remedy-database key url", ref)
	}
	if _, literal := e["REMEDY_DATABASE_URL"]["value"]; literal {
		t.Error("REMEDY_DATABASE_URL has a literal value")
	}
	if _, found := e["REMEDY_DB"]; found {
		t.Error("REMEDY_DB is gone: the server refuses it")
	}
	// The runner never gets a database.
	for _, c := range []string{"runner", "install-cli"} {
		for name := range env(container(t, podSpec(t, d.find("StatefulSet", "remedy-runner")), c)) {
			if strings.HasPrefix(name, "REMEDY_DATABASE") {
				t.Errorf("the %s container of the runner has %s", c, name)
			}
		}
	}
	// No rendered object spells a connection URL.
	if text := toString(d); strings.Contains(text, "postgres://") || strings.Contains(text, "postgresql://") {
		t.Errorf("a rendered object contains a connection URL:\n%s", text)
	}
}

func TestTheDatabaseSecretKeyIsConfigurable(t *testing.T) {
	values := baseValues()
	set(values, "database.existingSecret", "pg-app")
	set(values, "database.secretKey", "uri")
	e := env(container(t, podSpec(t, mustRender(t, values).find("Deployment", "remedy-server")), "server"))
	ref := dig(t, e["REMEDY_DATABASE_URL"], "valueFrom", "secretKeyRef")
	if dig(t, ref, "name") != "pg-app" || dig(t, ref, "key") != "uri" {
		t.Errorf("REMEDY_DATABASE_URL comes from %v, want the Secret pg-app key uri", ref)
	}
}

// Without database.existingSecret the chart installs its own PostgreSQL (postgres_test.go), which needs its password Secret;
// a database of your own needs exactly the name of its Secret.
func TestWithoutTheDatabaseSecretTheBundledDatabaseNeedsItsPasswordSecret(t *testing.T) {
	for name, v := range map[string]any{"empty": "", "missing": nil} {
		t.Run(name, func(t *testing.T) {
			values := baseValues()
			if v == nil {
				set(values, "database", map[string]any{})
			} else {
				set(values, "database.existingSecret", v)
			}
			mustFail(t, values, "postgres.existingSecret is required for the bundled PostgreSQL")
		})
	}
	// Whether or not the policies are on.
	values := baseValues()
	set(values, "networkPolicy.enabled", false)
	set(values, "database.existingSecret", "")
	mustFail(t, values, "postgres.existingSecret is required for the bundled PostgreSQL")
	// The message names any password as fine: the control plane gets it as PGPASSWORD, not inside a URL.
	mustFail(t, values, "holds the database password under postgres.passwordKey (any characters")
	// A database Secret that is not a name is refused.
	values = baseValues()
	set(values, "database.existingSecret", 5)
	mustFail(t, values, "database.existingSecret must be the name of a Secret")
}

func TestTheDatabaseSecretKeyMustBeAKeyName(t *testing.T) {
	for _, key := range []string{"has space", "a/b", "ü"} {
		values := baseValues()
		set(values, "database.secretKey", key)
		mustFail(t, values, "database.secretKey")
	}
	// A URL pasted into the key by mistake fails without being echoed: it holds a password.
	values := baseValues()
	set(values, "database.secretKey", "postgres://u:pw@h/db")
	mustFail(t, values, "database.secretKey is not a valid Secret key name")
	if _, stderr, _ := render(t, values); strings.Contains(stderr, "pw@h") || strings.Contains(stderr, "postgres://") {
		t.Errorf("the error echoes the value:\n%s", stderr)
	}
	// An empty key falls back to the default.
	values = baseValues()
	set(values, "database.secretKey", "")
	e := env(container(t, podSpec(t, mustRender(t, values).find("Deployment", "remedy-server")), "server"))
	if got := dig(t, e["REMEDY_DATABASE_URL"], "valueFrom", "secretKeyRef", "key"); got != "url" {
		t.Errorf("an empty secretKey gave the key %v, want url", got)
	}
}

func TestTheConnectionURLIsRefusedInValues(t *testing.T) {
	for _, name := range []string{"url", "password", "dsn"} {
		values := baseValues()
		set(values, "database."+name, "postgres://remedy:hunter2@db.example.invalid/remedy")
		mustFail(t, values, "database."+name)
		_, stderr, _ := render(t, values)
		if !strings.Contains(stderr, "Secret") {
			t.Errorf("database.%s: the message must say to use a Secret:\n%s", name, stderr)
		}
		if strings.Contains(stderr, "hunter2") {
			t.Errorf("database.%s: the message repeats the value:\n%s", name, stderr)
		}
	}
}

func TestServerEnvRefusesTheDatabaseVariablesTheChartSets(t *testing.T) {
	for _, name := range []string{"REMEDY_DATABASE_URL", "REMEDY_DB"} {
		values := baseValues()
		set(values, "server.env", map[string]any{name: "postgres://x"})
		mustFail(t, values, "server.env."+name)
	}
	// The pool size is not a secret; the Setup page has no field for it, so server.env is its place.
	values := baseValues()
	set(values, "server.env", map[string]any{"REMEDY_DATABASE_MAX_CONNS": "20"})
	e := env(container(t, podSpec(t, mustRender(t, values).find("Deployment", "remedy-server")), "server"))
	if e["REMEDY_DATABASE_MAX_CONNS"]["value"] != "20" {
		t.Errorf("REMEDY_DATABASE_MAX_CONNS = %v, want 20", e["REMEDY_DATABASE_MAX_CONNS"]["value"])
	}
}

// databasePodForm and databaseExternalForm are the two forms of networkPolicy.database.
func databasePodForm(ns string, labels map[string]any, port any) map[string]any {
	m := alertmanagerValues(ns, labels)
	if port != nil {
		m["port"] = port
	}
	return m
}

func databaseExternalForm(cidr string, port any) map[string]any {
	m := map[string]any{"cidr": cidr}
	if port != nil {
		m["port"] = port
	}
	return m
}

// databaseRules are the rules of the server policy that carry TCP 5432 (or another port named by a test).
func databaseRules(t *testing.T, p map[string]any, port string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, r := range egressRules(t, p) {
		if ports := rulePorts(r); len(ports) == 1 && ports[0] == port {
			out = append(out, r)
		}
	}
	return out
}

func TestThePodFormOpensOneRuleToTheDatabasePods(t *testing.T) {
	values := baseValues()
	set(values, "networkPolicy.database", databasePodForm("databases", map[string]any{"cnpg.io/cluster": "remedy"}, 5433))
	d := mustRender(t, values)
	server := policy(t, d, "remedy-server")
	rules := databaseRules(t, server, "TCP/5433")
	if len(rules) != 1 {
		t.Fatalf("%d database rules, want 1: %v", len(rules), egressRules(t, server))
	}
	peers := listOf(rules[0]["to"])
	if len(peers) != 1 {
		t.Fatalf("the database rule has %d peers, want 1: %v", len(peers), peers)
	}
	if got := dig(t, peers[0], "namespaceSelector", "matchLabels", "kubernetes.io/metadata.name"); got != "databases" {
		t.Errorf("namespaceSelector = %v, want databases", got)
	}
	if got := dig(t, peers[0], "podSelector", "matchLabels", "cnpg.io/cluster"); got != "remedy" {
		t.Errorf("podSelector = %v, want the cluster label", got)
	}
	if m := peers[0].(map[string]any); len(m) != 2 {
		t.Errorf("the peer must have exactly the two selectors in one entry (one peer, not two): %v", m)
	}
	// The rule goes before the extra rules and the internet rule, which stays last.
	all := egressRules(t, server)
	if last := toString(all[len(all)-1]); !strings.Contains(last, "cidr: 0.0.0.0/0") {
		t.Errorf("the internet rule must stay last: %v", all)
	}
}

func TestThePortOfTheDatabaseDefaultsTo5432(t *testing.T) {
	values := baseValues()
	set(values, "networkPolicy.database", databasePodForm("databases", map[string]any{"app": "pg"}, nil))
	if n := len(databaseRules(t, policy(t, mustRender(t, values), "remedy-server"), "TCP/5432")); n != 1 {
		t.Errorf("pod form without a port: %d rules on TCP/5432, want 1", n)
	}
	set(values, "networkPolicy.database", databaseExternalForm("192.168.178.20/32", nil))
	if n := len(databaseRules(t, policy(t, mustRender(t, values), "remedy-server"), "TCP/5432")); n != 1 {
		t.Errorf("external form without a port: %d rules on TCP/5432, want 1", n)
	}
}

func TestTheExternalFormOpensOneRuleToTheGivenRange(t *testing.T) {
	for _, cidr := range []string{
		"192.168.178.20/32", "10.20.0.0/16", "172.16.4.0/24", "203.0.113.7/32", "192.168.178.0/24", "10.1.2.0/23",
		"fd00::20/128", "fd00:1234::/32", "2001:db8::/16", "fe80::1/128",
	} {
		t.Run(cidr, func(t *testing.T) {
			values := baseValues()
			set(values, "networkPolicy.database", databaseExternalForm(cidr, 6432))
			d := mustRender(t, values)
			server := policy(t, d, "remedy-server")
			rules := databaseRules(t, server, "TCP/6432")
			if len(rules) != 1 {
				t.Fatalf("%d database rules, want 1: %v", len(rules), egressRules(t, server))
			}
			blocks := ipBlocks(rules)
			if len(blocks) != 1 || blocks[0].cidr != cidr || len(blocks[0].except) != 0 {
				t.Errorf("ipBlocks = %v, want exactly %s without an except list", blocks, cidr)
			}
			if peers := listOf(rules[0]["to"]); len(peers) != 1 || len(peers[0].(map[string]any)) != 1 {
				t.Errorf("the rule must have one peer, an ipBlock: %v", peers)
			}
			if text := toString(rules[0]); strings.Contains(text, "namespaceSelector") || strings.Contains(text, "podSelector") {
				t.Errorf("the external form must not select pods:\n%s", text)
			}
		})
	}
}

func TestOnlyTheServerPolicyCarriesTheDatabaseRule(t *testing.T) {
	forms := map[string]map[string]any{
		"pod":      databasePodForm("databases", map[string]any{"app": "pg-marker"}, 5439),
		"external": databaseExternalForm("203.0.113.9/32", 5439),
	}
	for name, form := range forms {
		t.Run(name, func(t *testing.T) {
			values := baseValues()
			set(values, "networkPolicy.database", form)
			// The runner policy always renders; the refresher's with the write side on.
			set(values, "cluster.enabled", true)
			set(values, "cluster.write.enabled", true)
			set(values, "cluster.write.namespaces", []any{"demo"})
			set(values, "networkPolicy.apiServer.cidrs", []any{"172.18.0.2/32"})
			d := mustRender(t, values)
			if n := len(databaseRules(t, policy(t, d, "remedy-server"), "TCP/5439")); n != 1 {
				t.Fatalf("the server policy has %d database rules, want 1", n)
			}
			for _, other := range []string{"remedy-runner", "remedy-token-refresher"} {
				text := toString(policy(t, d, other))
				for _, marker := range []string{"5439", "pg-marker", "203.0.113.9", "databases"} {
					if strings.Contains(text, marker) {
						t.Errorf("%s must not carry the database rule (found %q):\n%s", other, marker, text)
					}
				}
			}
		})
	}
}

func TestTheDatabaseRuleIsRequiredWhileThePoliciesAreOn(t *testing.T) {
	values := baseValues()
	set(values, "networkPolicy.database", map[string]any{})
	mustFail(t, values, "networkPolicy.database")
	values = baseValues()
	set(values, "networkPolicy", map[string]any{})
	mustFail(t, values, "networkPolicy.database")
}

func TestTheDatabaseRuleNeedsExactlyOneForm(t *testing.T) {
	labels := map[string]any{"app": "pg"}
	refused := []struct {
		name string
		m    map[string]any
		want string
	}{
		{"neither form and only a port", map[string]any{"port": 5432}, "exactly one"},
		{"both forms", map[string]any{"namespace": "databases", "podSelector": labels, "cidr": "192.168.1.5/32"}, "exactly one"},
		{"a namespace and a cidr", map[string]any{"namespace": "databases", "cidr": "192.168.1.5/32"}, "exactly one"},
		{"a podSelector and a cidr", map[string]any{"podSelector": labels, "cidr": "192.168.1.5/32"}, "exactly one"},
		{"an unknown key", map[string]any{"cidr": "192.168.1.5/32", "host": "db.example.invalid"}, "networkPolicy.database.host"},
		{"a typo that is no form", map[string]any{"podselector": labels, "namespace": "databases"}, "networkPolicy.database"},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			values := baseValues()
			set(values, "networkPolicy.database", c.m)
			mustFail(t, values, c.want)
		})
	}
}

func TestThePodFormNeedsANamespaceALabelAndAGoodPort(t *testing.T) {
	labels := map[string]any{"app": "pg"}
	refused := []struct {
		name string
		m    map[string]any
	}{
		{"no namespace", databasePodForm("", labels, 5432)},
		{"no podSelector", databasePodForm("databases", nil, 5432)},
		{"an empty podSelector", databasePodForm("databases", map[string]any{}, 5432)},
		{"an upper case namespace", databasePodForm("Databases", labels, 5432)},
		{"a namespace of 64 characters", databasePodForm(strings.Repeat("a", 64), labels, 5432)},
		{"port 0", databasePodForm("databases", labels, 0)},
		{"port 70000", databasePodForm("databases", labels, 70000)},
		{"a fractional port", databasePodForm("databases", labels, 5432.5)},
		{"a port that is text", databasePodForm("databases", labels, "5432")},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			values := baseValues()
			set(values, "networkPolicy.database", c.m)
			mustFail(t, values, "networkPolicy.database")
		})
	}
	t.Run("an empty selector says why", func(t *testing.T) {
		values := baseValues()
		set(values, "networkPolicy.database", databasePodForm("databases", map[string]any{}, 5432))
		mustFail(t, values, "empty selector would admit every pod")
	})
	t.Run("a namespace of 63 characters renders", func(t *testing.T) {
		values := baseValues()
		set(values, "networkPolicy.database", databasePodForm(strings.Repeat("a", 63), labels, 5432))
		mustRender(t, values)
	})
}

func TestTheExternalFormRefusesWhatWouldOpenEverything(t *testing.T) {
	refused := []string{
		"0.0.0.0/0", "::/0", "0::/0", "0.0.0.0/1", "10.0.0.0/7", "128.0.0.0/4", "192.0.0.0/2",
		"fc00::/7", "fd00::/15",
		"192.168.1.5", "192.168.1.5/33", "192.168.1.5/", "300.1.1.1/32", "192.168.1/24", "db.example.invalid", "192.168.1.5/32, 0.0.0.0/0",
		" 192.168.1.5/32", "fd00::20/129", "",
		// Wider than /16 for IPv4: a typo of the prefix (192.168.178.20/8 for /32) would open a whole network.
		"10.0.0.0/8", "10.0.0.0/15", "192.168.178.20/8", "10.1.2.3/8",
		// Host bits set: the address is not the start of its range, which is a typo of the prefix or of the address.
		"192.168.178.20/16", "192.168.178.20/24", "10.1.2.1/23", "192.168.178.21/31",
		// The IPv4-mapped range ::ffff:0:0/96, and anything in ::/16, would reach IPv4 addresses past the IPv4 checks.
		"::/16", "::/64", "::ffff:0:0/96", "::ffff:c0a8:b214/128", "0:0::/32", "0000::/24", "::1/128",
	}
	for _, cidr := range refused {
		t.Run(cidr, func(t *testing.T) {
			values := baseValues()
			set(values, "networkPolicy.database", databaseExternalForm(cidr, 5432))
			mustFail(t, values, "networkPolicy.database.cidr")
		})
	}
	t.Run("the message says why", func(t *testing.T) {
		values := baseValues()
		set(values, "networkPolicy.database", databaseExternalForm("0.0.0.0/0", 5432))
		mustFail(t, values, "would open")
	})
	t.Run("a cidr that is not text", func(t *testing.T) {
		values := baseValues()
		set(values, "networkPolicy.database", map[string]any{"cidr": 5, "port": 5432})
		mustFail(t, values, "networkPolicy.database.cidr")
	})
	for name, port := range map[string]any{"port 0": 0, "port 70000": 70000, "a fractional port": 5432.5, "a port that is text": "5432"} {
		t.Run(name, func(t *testing.T) {
			values := baseValues()
			set(values, "networkPolicy.database", databaseExternalForm("192.168.1.5/32", port))
			mustFail(t, values, "networkPolicy.database")
		})
	}
}

func TestPoliciesOffIgnoreTheDatabaseRuleButNotTheSecret(t *testing.T) {
	values := baseValues()
	set(values, "networkPolicy.enabled", false)
	set(values, "networkPolicy.database", map[string]any{"cidr": "0.0.0.0/0", "namespace": "x"})
	if n := len(mustRender(t, values).all("NetworkPolicy")); n != 0 {
		t.Errorf("%d NetworkPolicy objects with networkPolicy.enabled=false, want 0", n)
	}
	delete(values["networkPolicy"].(map[string]any), "database")
	mustRender(t, values)
}
