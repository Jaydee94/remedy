package deploy_test

import (
	"reflect"
	"strings"
	"testing"
)

// The chart installs a small single-node PostgreSQL of its own by default (docs/specs/2026-10-22-postgres-only-design.md,
// decision 11), with the password from a Secret that exists already (postgres.existingSecret). database.existingSecret
// names a database of your own instead, and then nothing of the bundled one renders. The control plane's URL is composed in
// its Deployment from the password Secret, the network policies admit only the server and the backup job,
// and a CronJob dumps the database to a second claim.

// bundledValues are baseValues with the bundled database (the default: postgres.enabled unset) instead of an external one.
func bundledValues() map[string]any {
	v := baseValues()
	delete(v, "database")
	delete(v["networkPolicy"].(map[string]any), "database")
	set(v, "postgres.existingSecret", "pgsecret")
	return v
}

func postgresStatefulSet(t *testing.T, d docs) map[string]any {
	t.Helper()
	s := d.find("StatefulSet", "remedy-postgres")
	if s == nil {
		t.Fatal("no StatefulSet remedy-postgres")
	}
	return s
}

func backupCronJob(t *testing.T, d docs) map[string]any {
	t.Helper()
	c := d.find("CronJob", "remedy-postgres-backup")
	if c == nil {
		t.Fatal("no CronJob remedy-postgres-backup")
	}
	return c
}

func backupPodSpec(t *testing.T, c map[string]any) map[string]any {
	t.Helper()
	spec, ok := dig(t, c, "spec", "jobTemplate", "spec", "template", "spec").(map[string]any)
	if !ok {
		t.Fatal("the backup CronJob has no pod spec")
	}
	return spec
}

// checkRestricted pins the restricted Pod Security Standard of a pod of the bundled database: the postgres user 999,
// RuntimeDefault seccomp, and every container without privilege escalation and without capabilities.
func checkRestricted(t *testing.T, what string, pod map[string]any) {
	t.Helper()
	sc := dig(t, pod, "securityContext")
	for _, k := range []string{"runAsUser", "runAsGroup", "fsGroup"} {
		if got := dig(t, sc, k); got != 999 {
			t.Errorf("%s: securityContext.%s = %v, want 999", what, k, got)
		}
	}
	if dig(t, sc, "runAsNonRoot") != true {
		t.Errorf("%s: runAsNonRoot = %v, want true", what, dig(t, sc, "runAsNonRoot"))
	}
	if got := dig(t, sc, "seccompProfile", "type"); got != "RuntimeDefault" {
		t.Errorf("%s: seccompProfile = %v, want RuntimeDefault", what, got)
	}
	if dig(t, pod, "automountServiceAccountToken") != false {
		t.Errorf("%s: automountServiceAccountToken = %v, want false", what, dig(t, pod, "automountServiceAccountToken"))
	}
	for _, list := range []string{"containers", "initContainers"} {
		for _, c := range listOf(pod[list]) {
			csc := dig(t, c, "securityContext")
			name := dig(t, c, "name")
			if dig(t, csc, "allowPrivilegeEscalation") != false {
				t.Errorf("%s/%v: allowPrivilegeEscalation = %v, want false", what, name, dig(t, csc, "allowPrivilegeEscalation"))
			}
			if got := toString(dig(t, csc, "capabilities")); got != "drop:\n    - ALL\n" {
				t.Errorf("%s/%v: capabilities = %q, want drop ALL and nothing added", what, name, got)
			}
			if dig(t, csc, "readOnlyRootFilesystem") != true {
				t.Errorf("%s/%v: readOnlyRootFilesystem = %v, want true", what, name, dig(t, csc, "readOnlyRootFilesystem"))
			}
		}
	}
}

func TestNothingOfTheBundledDatabaseRendersWithADatabaseOfYourOwn(t *testing.T) {
	for name, enabled := range map[string]any{"postgres.enabled unset": nil, "postgres.enabled false": false} {
		t.Run(name, func(t *testing.T) {
			values := baseValues() // database.existingSecret is set
			if enabled != nil {
				set(values, "postgres.enabled", enabled)
			}
			d := mustRender(t, values)
			for _, o := range d {
				if strings.Contains(toString(dig(t, o, "metadata", "name")), "postgres") {
					t.Errorf("%v %v renders with database.existingSecret", o["kind"], dig(t, o, "metadata", "name"))
				}
			}
			if len(d.all("CronJob")) != 0 {
				t.Error("a CronJob renders with database.existingSecret and without cluster.write")
			}
		})
	}
}

func TestTheBundledDatabaseIsTheDefault(t *testing.T) {
	// No database value at all except the password Secret: the bundled database renders.
	postgresStatefulSet(t, mustRender(t, bundledValues()))
	// postgres.enabled: true says the same.
	values := bundledValues()
	set(values, "postgres.enabled", true)
	postgresStatefulSet(t, mustRender(t, values))
	// The default values.yaml leaves postgres.enabled unset, so that an explicit true can be told apart.
	values = bundledValues()
	set(values, "postgres.enabled", nil)
	postgresStatefulSet(t, mustRender(t, values))
}

func TestTheBundledDatabaseIsAOneReplicaStatefulSetWithAPinnedImage(t *testing.T) {
	d := mustRender(t, bundledValues())
	s := postgresStatefulSet(t, d)
	if dig(t, s, "metadata", "namespace") != nil && dig(t, s, "metadata", "namespace") != "remedy-system" {
		t.Errorf("namespace = %v", dig(t, s, "metadata", "namespace"))
	}
	if got := dig(t, s, "spec", "replicas"); got != 1 {
		t.Errorf("replicas = %v, want 1", got)
	}
	if got := dig(t, s, "spec", "serviceName"); got != "remedy-postgres" {
		t.Errorf("serviceName = %v, want remedy-postgres", got)
	}
	pod := podSpec(t, s)
	c := container(t, pod, "postgres")
	if got := c["image"]; got != "postgres:16.15" {
		t.Errorf("image = %v, want postgres:16.15", got)
	}
	e := env(c)
	if e["POSTGRES_USER"]["value"] != "remedy" || e["POSTGRES_DB"]["value"] != "remedy" {
		t.Errorf("POSTGRES_USER = %v, POSTGRES_DB = %v, want remedy and remedy", e["POSTGRES_USER"]["value"], e["POSTGRES_DB"]["value"])
	}
	ref := dig(t, e["POSTGRES_PASSWORD"], "valueFrom", "secretKeyRef")
	if dig(t, ref, "name") != "pgsecret" || dig(t, ref, "key") != "password" {
		t.Errorf("POSTGRES_PASSWORD comes from %v, want the Secret pgsecret key password", ref)
	}
	if got := e["PGDATA"]["value"]; got != "/var/lib/postgresql/data/pgdata" {
		t.Errorf("PGDATA = %v, want a subdirectory of the mount", got)
	}
	if !strings.HasPrefix(toString(e["PGDATA"]["value"]), "/var/lib/postgresql/data/") {
		t.Error("PGDATA must lie below the data mount")
	}
	if m := mounted(c, "/var/lib/postgresql/data"); m == nil || m["name"] != "data" {
		t.Errorf("the data mount = %v, want the claim template data at /var/lib/postgresql/data", m)
	}
	for _, probe := range []string{"readinessProbe", "livenessProbe"} {
		cmd := toString(dig(t, c, probe, "exec", "command"))
		if !strings.Contains(cmd, "pg_isready") || !strings.Contains(cmd, "127.0.0.1") {
			t.Errorf("%s = %s, want pg_isready over TCP", probe, cmd)
		}
	}
	if got := dig(t, c, "ports", 0, "containerPort"); got != 5432 {
		t.Errorf("containerPort = %v, want 5432", got)
	}
	if dig(t, c, "resources", "requests", "memory") == nil || dig(t, c, "resources", "limits", "memory") == nil {
		t.Errorf("resources = %v, want requests and a memory limit", c["resources"])
	}
	checkRestricted(t, "postgres", pod)

	vct := listOf(dig(t, s, "spec", "volumeClaimTemplates"))
	if len(vct) != 1 || dig(t, vct[0], "metadata", "name") != "data" {
		t.Fatalf("volumeClaimTemplates = %v, want one named data", vct)
	}
	if got := dig(t, vct[0], "spec", "resources", "requests", "storage"); got != "5Gi" {
		t.Errorf("the data claim requests %v, want 5Gi", got)
	}
	if got := dig(t, vct[0], "spec", "storageClassName"); got != nil {
		t.Errorf("storageClassName = %v, want none (the cluster default)", got)
	}
	if got := dig(t, s, "spec", "persistentVolumeClaimRetentionPolicy"); !reflect.DeepEqual(got, map[string]any{"whenDeleted": "Retain", "whenScaled": "Retain"}) {
		t.Errorf("persistentVolumeClaimRetentionPolicy = %v, want Retain for both", got)
	}

	svc := d.find("Service", "remedy-postgres")
	if svc == nil {
		t.Fatal("no Service remedy-postgres")
	}
	if got := dig(t, svc, "spec", "clusterIP"); got != "None" {
		t.Errorf("the Service's clusterIP = %v, want None (headless)", got)
	}
	if got := dig(t, svc, "spec", "selector"); !reflect.DeepEqual(got, dig(t, s, "spec", "selector", "matchLabels")) {
		t.Errorf("the Service selects %v, the StatefulSet %v", got, dig(t, s, "spec", "selector", "matchLabels"))
	}
	if got := dig(t, svc, "spec", "ports", 0, "port"); got != 5432 {
		t.Errorf("the Service's port = %v, want 5432", got)
	}
}

func TestTheBundledDatabaseValuesAreUsed(t *testing.T) {
	values := bundledValues()
	set(values, "postgres.image.repository", "mirror.example.invalid/postgres")
	set(values, "postgres.image.tag", "16.14")
	set(values, "postgres.passwordKey", "pw")
	set(values, "postgres.persistence.size", "9Gi")
	set(values, "postgres.persistence.storageClass", "local-path")
	d := mustRender(t, values)
	s := postgresStatefulSet(t, d)
	c := container(t, podSpec(t, s), "postgres")
	if got := c["image"]; got != "mirror.example.invalid/postgres:16.14" {
		t.Errorf("image = %v", got)
	}
	if got := dig(t, env(c)["POSTGRES_PASSWORD"], "valueFrom", "secretKeyRef", "key"); got != "pw" {
		t.Errorf("the password key = %v, want pw", got)
	}
	vct := listOf(dig(t, s, "spec", "volumeClaimTemplates"))
	if got := dig(t, vct[0], "spec", "resources", "requests", "storage"); got != "9Gi" {
		t.Errorf("size = %v, want 9Gi", got)
	}
	if got := dig(t, vct[0], "spec", "storageClassName"); got != "local-path" {
		t.Errorf("storageClassName = %v, want local-path", got)
	}
	// The backup claim follows the data's class unless it names its own.
	claim := d.find("PersistentVolumeClaim", "remedy-postgres-backup")
	if got := dig(t, claim, "spec", "storageClassName"); got != "local-path" {
		t.Errorf("the backup claim's class = %v, want the data's local-path", got)
	}
	set(values, "postgres.backup.storageClass", "nfs")
	claim = mustRender(t, values).find("PersistentVolumeClaim", "remedy-postgres-backup")
	if got := dig(t, claim, "spec", "storageClassName"); got != "nfs" {
		t.Errorf("the backup claim's class = %v, want nfs", got)
	}
}

func TestTheBundledDatabaseImageTagMustBeExact(t *testing.T) {
	for _, tag := range []string{"", "16", "latest", "17"} {
		values := bundledValues()
		set(values, "postgres.image.tag", tag)
		mustFail(t, values, "postgres.image.tag")
	}
	for _, tag := range []string{"16.15", "17.6", "16.15-bookworm"} {
		values := bundledValues()
		set(values, "postgres.image.tag", tag)
		mustRender(t, values)
	}
}

// The labels of the bundled database's pods must never be the ones remedy.selector gives the server, the runner or the
// refresher: a chart policy would then select them by accident.
func TestTheBundledDatabasePodsCarryTheirOwnNameAndNoChartPolicySelectsThem(t *testing.T) {
	values := bundledValues()
	set(values, "cluster.enabled", true)
	set(values, "cluster.write.enabled", true)
	set(values, "cluster.write.namespaces", []any{"demo"})
	set(values, "networkPolicy.apiServer.cidrs", []any{"172.18.0.2/32"})
	d := mustRender(t, values)
	pgLabels, _ := dig(t, postgresStatefulSet(t, d), "spec", "template", "metadata", "labels").(map[string]any)
	backupLabels, _ := dig(t, backupCronJob(t, d), "spec", "jobTemplate", "spec", "template", "metadata", "labels").(map[string]any)
	if pgLabels["app.kubernetes.io/name"] != "remedy-postgres" {
		t.Errorf("the postgres pod's name label = %v, want remedy-postgres", pgLabels["app.kubernetes.io/name"])
	}
	if backupLabels["app.kubernetes.io/name"] != "remedy-postgres-backup" {
		t.Errorf("the backup pod's name label = %v, want remedy-postgres-backup", backupLabels["app.kubernetes.io/name"])
	}
	selects := func(sel map[string]any, labels map[string]any) bool {
		for k, v := range sel {
			if labels[k] != v {
				return false
			}
		}
		return true
	}
	for _, name := range []string{"remedy-server", "remedy-runner", "remedy-token-refresher"} {
		sel, _ := dig(t, policy(t, d, name), "spec", "podSelector", "matchLabels").(map[string]any)
		if len(sel) == 0 {
			t.Fatalf("%s selects every pod", name)
		}
		if selects(sel, pgLabels) || selects(sel, backupLabels) {
			t.Errorf("the policy %s selects a pod of the bundled database (%v)", name, sel)
		}
	}
	// And the postgres policy selects the postgres pods only, not the backup job.
	sel, _ := dig(t, policy(t, d, "remedy-postgres"), "spec", "podSelector", "matchLabels").(map[string]any)
	if !selects(sel, pgLabels) || selects(sel, backupLabels) {
		t.Errorf("the postgres policy selects %v: want the postgres pods and not the backup pods", sel)
	}
}

func TestTheServerComposesItsURLFromThePasswordSecret(t *testing.T) {
	d := mustRender(t, bundledValues())
	c := container(t, podSpec(t, d.find("Deployment", "remedy-server")), "server")
	list := listOf(c["env"])
	pw, url := -1, -1
	for i, e := range list {
		switch dig(t, e, "name") {
		case "REMEDY_POSTGRES_PASSWORD":
			pw = i
		case "REMEDY_DATABASE_URL":
			url = i
		}
	}
	if pw < 0 || url < 0 {
		t.Fatalf("the server has no REMEDY_POSTGRES_PASSWORD (%d) or no REMEDY_DATABASE_URL (%d): %v", pw, url, list)
	}
	if pw > url {
		t.Errorf("REMEDY_POSTGRES_PASSWORD comes after REMEDY_DATABASE_URL: Kubernetes expands $(VAR) only from an earlier entry")
	}
	ref := dig(t, list[pw], "valueFrom", "secretKeyRef")
	if dig(t, ref, "name") != "pgsecret" || dig(t, ref, "key") != "password" {
		t.Errorf("the password comes from %v, want the Secret pgsecret key password", ref)
	}
	if _, literal := list[pw].(map[string]any)["value"]; literal {
		t.Error("the password has a literal value")
	}
	want := "postgres://remedy:$(REMEDY_POSTGRES_PASSWORD)@remedy-postgres.remedy-system.svc:5432/remedy?sslmode=disable"
	if got := dig(t, list[url], "value"); got != want {
		t.Errorf("REMEDY_DATABASE_URL = %v, want %s", got, want)
	}
	if dig(t, list[url], "valueFrom") != nil {
		t.Error("REMEDY_DATABASE_URL has a valueFrom as well")
	}
}

func TestTheBundledDatabaseNeedsAPasswordSecret(t *testing.T) {
	for name, v := range map[string]any{"empty": "", "missing": nil, "not a string": 5, "not a name": "Has Space"} {
		t.Run(name, func(t *testing.T) {
			values := bundledValues()
			if v == nil {
				delete(values["postgres"].(map[string]any), "existingSecret")
			} else {
				set(values, "postgres.existingSecret", v)
			}
			mustFail(t, values, "postgres.existingSecret")
		})
	}
	for _, key := range []string{"has space", "a/b"} {
		values := bundledValues()
		set(values, "postgres.passwordKey", key)
		mustFail(t, values, "postgres.passwordKey")
	}
}

func TestExactlyOneDatabaseIsConfigured(t *testing.T) {
	// An explicit postgres.enabled: true together with a database of your own is a contradiction.
	values := bundledValues()
	set(values, "postgres.enabled", true)
	set(values, "database.existingSecret", "remedy-database")
	mustFail(t, values, "database.existingSecret and postgres.enabled")

	// postgres.enabled: false without a database of your own leaves the control plane without a database.
	for _, policies := range []bool{true, false} {
		values = bundledValues()
		set(values, "postgres.enabled", false)
		set(values, "networkPolicy.enabled", policies)
		mustFail(t, values, "no database: postgres.enabled is false and database.existingSecret is not set")
	}

	// postgres.enabled must be a boolean (a quoted "false" would be true in a template).
	values = bundledValues()
	set(values, "postgres.enabled", "false")
	mustFail(t, values, "postgres.enabled")
}

func TestTheBundledDatabaseRefusesAnExternalDatabaseRule(t *testing.T) {
	values := bundledValues()
	set(values, "networkPolicy.database", databasePodForm("databases", map[string]any{"app": "pg"}, nil))
	mustFail(t, values, "networkPolicy.database and postgres.enabled")
	// With the policies off the value has no effect and is not refused, like the other policy values.
	set(values, "networkPolicy.enabled", false)
	mustRender(t, values)
}

func TestTheServerPolicyReachesTheBundledDatabaseOnly(t *testing.T) {
	d := mustRender(t, bundledValues())
	server := policy(t, d, "remedy-server")
	rules := databaseRules(t, server, "TCP/5432")
	if len(rules) != 1 {
		t.Fatalf("%d rules to 5432 on the server policy, want 1: %v", len(rules), egressRules(t, server))
	}
	peers := listOf(rules[0]["to"])
	if len(peers) != 1 {
		t.Fatalf("the database rule has %d peers, want 1: %v", len(peers), peers)
	}
	want := map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{
		"app.kubernetes.io/name": "remedy-postgres", "app.kubernetes.io/instance": "remedy",
	}}}
	if !reflect.DeepEqual(peers[0], want) {
		t.Errorf("the database peer = %v, want only the bundled postgres pods of this namespace %v", peers[0], want)
	}
	if n := len(egressRules(t, server)); n != 3 {
		t.Errorf("%d egress rules on the server policy, want 3 (DNS, database, internet)", n)
	}
	checkDNSRule(t, "server", server, "kube-system")
	checkInternetRule(t, "server", server)
	// The runner never reaches the database.
	if text := toString(policy(t, d, "remedy-runner")); strings.Contains(text, "remedy-postgres") || strings.Contains(text, "5432") {
		t.Errorf("the runner policy names the database:\n%s", text)
	}
}

func TestThePostgresPolicyAdmitsTheServerAndTheBackupOnly(t *testing.T) {
	d := mustRender(t, bundledValues())
	p := policy(t, d, "remedy-postgres")
	if got := dig(t, p, "spec", "policyTypes"); !reflect.DeepEqual(got, []any{"Ingress"}) {
		t.Errorf("policyTypes = %v, want only Ingress (the database connects to nothing)", got)
	}
	if dig(t, p, "spec", "egress") != nil {
		t.Errorf("egress = %v, want none", dig(t, p, "spec", "egress"))
	}
	ingress := listOf(dig(t, p, "spec", "ingress"))
	if len(ingress) != 1 {
		t.Fatalf("%d ingress rules, want 1: %v", len(ingress), ingress)
	}
	rule := ingress[0].(map[string]any)
	if got := rulePorts(rule); !equalStrings(got, []string{"TCP/5432"}) {
		t.Errorf("ports = %v, want TCP/5432 only", got)
	}
	want := []any{
		map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{
			"app.kubernetes.io/name": "remedy", "app.kubernetes.io/instance": "remedy", "app.kubernetes.io/component": "server",
		}}},
		map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{
			"app.kubernetes.io/name": "remedy-postgres-backup", "app.kubernetes.io/instance": "remedy",
		}}},
	}
	if got := rule["from"]; !reflect.DeepEqual(got, want) {
		t.Errorf("from = %v\nwant exactly %v", got, want)
	}
	// The backup pods match the backup peer.
	labels, _ := dig(t, backupCronJob(t, d), "spec", "jobTemplate", "spec", "template", "metadata", "labels").(map[string]any)
	for k, v := range dig(t, want[1], "podSelector", "matchLabels").(map[string]any) {
		if labels[k] != v {
			t.Errorf("the backup pod's label %s = %v, the policy admits %v", k, labels[k], v)
		}
	}
}

func TestPoliciesOffRenderNoPostgresPolicy(t *testing.T) {
	values := bundledValues()
	set(values, "networkPolicy.enabled", false)
	d := mustRender(t, values)
	if n := len(d.all("NetworkPolicy")); n != 0 {
		t.Errorf("%d policies with networkPolicy.enabled=false, want 0", n)
	}
	postgresStatefulSet(t, d)
}

func TestTheBackupDumpsToItsOwnClaimEveryNight(t *testing.T) {
	d := mustRender(t, bundledValues())
	claim := d.find("PersistentVolumeClaim", "remedy-postgres-backup")
	if claim == nil {
		t.Fatal("no PersistentVolumeClaim remedy-postgres-backup")
	}
	if got := dig(t, claim, "metadata", "annotations", "helm.sh/resource-policy"); got != "keep" {
		t.Errorf("the backup claim's resource policy = %v, want keep", got)
	}
	if got := dig(t, claim, "spec", "resources", "requests", "storage"); got != "2Gi" {
		t.Errorf("the backup claim requests %v, want 2Gi", got)
	}
	if got := dig(t, claim, "spec", "storageClassName"); got != nil {
		t.Errorf("the backup claim's class = %v, want none (the cluster default)", got)
	}
	c := backupCronJob(t, d)
	if got := dig(t, c, "spec", "schedule"); got != "17 3 * * *" {
		t.Errorf("schedule = %v, want 17 3 * * *", got)
	}
	if got := dig(t, c, "spec", "concurrencyPolicy"); got != "Forbid" {
		t.Errorf("concurrencyPolicy = %v, want Forbid", got)
	}
	pod := backupPodSpec(t, c)
	checkRestricted(t, "backup", pod)
	b := container(t, pod, "backup")
	if got, want := b["image"], container(t, podSpec(t, postgresStatefulSet(t, d)), "postgres")["image"]; got != want {
		t.Errorf("the backup image = %v, want the database's %v", got, want)
	}
	e := env(b)
	ref := dig(t, e["PGPASSWORD"], "valueFrom", "secretKeyRef")
	if dig(t, ref, "name") != "pgsecret" || dig(t, ref, "key") != "password" {
		t.Errorf("PGPASSWORD comes from %v, want the Secret pgsecret key password", ref)
	}
	for name, want := range map[string]string{
		"PGHOST": "remedy-postgres.remedy-system.svc", "PGPORT": "5432", "PGUSER": "remedy", "PGDATABASE": "remedy",
		"PGSSLMODE": "disable", "RETENTION_MINUTES": "10080",
	} {
		if got := e[name]["value"]; got != want {
			t.Errorf("%s = %v, want %s", name, got, want)
		}
	}
	script := toString(b["command"])
	for _, want := range []string{
		"pg_dump --format=custom --no-owner", "/backup/remedy-", ".dump.part", "mv ", "-mmin", "-delete",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the backup script lacks %q:\n%s", want, script)
		}
	}
	// No $( in the script: Kubernetes would read it as a variable reference.
	if strings.Contains(script, "$(") {
		t.Errorf("the backup script contains $(, which Kubernetes expands:\n%s", script)
	}
	if m := mounted(b, "/backup"); m == nil {
		t.Error("the backup container does not mount /backup")
	}
	if v := volume(pod, dig(t, mounted(b, "/backup"), "name").(string)); dig(t, v, "persistentVolumeClaim", "claimName") != "remedy-postgres-backup" {
		t.Errorf("/backup is %v, want the claim remedy-postgres-backup", v)
	}
}

func TestTheBackupValuesAreUsed(t *testing.T) {
	values := bundledValues()
	set(values, "postgres.backup.schedule", "0 1 * * 0")
	set(values, "postgres.backup.retentionDays", 30)
	set(values, "postgres.backup.size", "20Gi")
	d := mustRender(t, values)
	c := backupCronJob(t, d)
	if got := dig(t, c, "spec", "schedule"); got != "0 1 * * 0" {
		t.Errorf("schedule = %v", got)
	}
	if got := env(container(t, backupPodSpec(t, c), "backup"))["RETENTION_MINUTES"]["value"]; got != "43200" {
		t.Errorf("RETENTION_MINUTES = %v, want 43200", got)
	}
	if got := dig(t, d.find("PersistentVolumeClaim", "remedy-postgres-backup"), "spec", "resources", "requests", "storage"); got != "20Gi" {
		t.Errorf("size = %v, want 20Gi", got)
	}
	for _, bad := range []any{0, -1, 1.5, "7", nil} {
		values := bundledValues()
		set(values, "postgres.backup.retentionDays", bad)
		mustFail(t, values, "postgres.backup.retentionDays")
	}
	values = bundledValues()
	set(values, "postgres.backup.schedule", "")
	mustFail(t, values, "postgres.backup.schedule")
}

func TestTheBackupCanBeTurnedOff(t *testing.T) {
	values := bundledValues()
	set(values, "postgres.backup.enabled", false)
	d := mustRender(t, values)
	if d.find("CronJob", "remedy-postgres-backup") != nil {
		t.Error("the backup CronJob renders with postgres.backup.enabled=false")
	}
	if d.find("PersistentVolumeClaim", "remedy-postgres-backup") != nil {
		t.Error("the backup claim renders with postgres.backup.enabled=false")
	}
	postgresStatefulSet(t, d)
}

func TestNoPasswordValueIsEverRendered(t *testing.T) {
	values := bundledValues()
	set(values, "server.env", map[string]any{"REMEDY_LOG_LEVEL": "debug"})
	d := mustRender(t, values)
	if n := len(d.all("Secret")); n != 0 {
		t.Errorf("%d Secrets rendered: the chart never creates the database password", n)
	}
	for _, o := range d {
		for _, pod := range []any{dig(t, o, "spec", "template", "spec"), dig(t, o, "spec", "jobTemplate", "spec", "template", "spec")} {
			for _, list := range []string{"containers", "initContainers"} {
				for _, c := range listOf(dig(t, pod, list)) {
					for name, e := range env(c.(map[string]any)) {
						if strings.Contains(name, "PASSWORD") {
							if _, literal := e["value"]; literal {
								t.Errorf("%s of %v has a literal value", name, dig(t, o, "metadata", "name"))
							}
						}
					}
				}
			}
		}
	}
	// A password value set by mistake is not a value of the chart.
	values = bundledValues()
	set(values, "postgres.password", "hunter2")
	mustFail(t, values, "postgres.password")
	if _, stderr, _ := render(t, values); strings.Contains(stderr, "hunter2") {
		t.Errorf("the error repeats the value:\n%s", stderr)
	}
}
