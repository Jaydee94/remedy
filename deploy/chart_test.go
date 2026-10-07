package deploy_test

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestTheRenderNeedsAnExistingSecret(t *testing.T) {
	values := baseValues()
	set(values, "existingSecret.name", "")
	mustFail(t, values, "existingSecret.name")
}

func TestTheServerRunsOnceWithRecreateAndHardening(t *testing.T) {
	d := mustRender(t, baseValues())
	dep := d.find("Deployment", "remedy-server")
	if dep == nil {
		t.Fatal("no Deployment remedy-server")
	}
	if got := dig(t, dep, "spec", "replicas"); got != 1 {
		t.Fatalf("replicas = %v, want 1 (SQLite has one connection)", got)
	}
	if got := dig(t, dep, "spec", "strategy", "type"); got != "Recreate" {
		t.Fatalf("strategy = %v, want Recreate (a second pod must not open the same file)", got)
	}
	pod := podSpec(t, dep)
	if pod["automountServiceAccountToken"] != false {
		t.Fatalf("automountServiceAccountToken = %v, want false", pod["automountServiceAccountToken"])
	}
	if dig(t, pod, "securityContext", "runAsNonRoot") != true || dig(t, pod, "securityContext", "runAsUser") != 65532 ||
		dig(t, pod, "securityContext", "seccompProfile", "type") != "RuntimeDefault" {
		t.Fatalf("pod securityContext = %v", pod["securityContext"])
	}
	c := container(t, pod, "server")
	sc := c["securityContext"]
	if dig(t, sc, "readOnlyRootFilesystem") != true || dig(t, sc, "allowPrivilegeEscalation") != false ||
		dig(t, sc, "capabilities", "drop", 0) != "ALL" {
		t.Fatalf("container securityContext = %v", sc)
	}
	if dig(t, c, "readinessProbe", "httpGet", "path") != "/healthz" || dig(t, c, "livenessProbe", "httpGet", "path") != "/healthz" {
		t.Fatalf("probes = %v / %v", c["readinessProbe"], c["livenessProbe"])
	}
	if got := c["image"]; got != "ghcr.io/jaydee94/remedy-server:0.1.0" {
		t.Fatalf("image = %v, want the default repository at the chart's appVersion", got)
	}
}

func TestTheServerListensOnTwoPortsAndTakesItsSecretsFromTheSecret(t *testing.T) {
	d := mustRender(t, baseValues())
	c := container(t, podSpec(t, d.find("Deployment", "remedy-server")), "server")
	e := env(c)
	for name, want := range map[string]string{"REMEDY_ADDR": ":8080", "REMEDY_INTERNAL_ADDR": ":8081", "REMEDY_DB": "/data/remedy.db"} {
		if e[name]["value"] != want {
			t.Errorf("%s = %v, want %q", name, e[name]["value"], want)
		}
	}
	for name, key := range map[string]string{
		"REMEDY_ADMIN_PASSWORD": "admin-password", "REMEDY_RUNNER_TOKEN": "runner-token", "REMEDY_MASTER_KEY": "master-key",
	} {
		ref := dig(t, e[name], "valueFrom", "secretKeyRef")
		if dig(t, ref, "name") != "remedy-secrets" || dig(t, ref, "key") != key {
			t.Errorf("%s comes from %v, want the Secret remedy-secrets key %s", name, ref, key)
		}
		if _, literal := e[name]["value"]; literal {
			t.Errorf("%s has a literal value", name)
		}
	}
}

func TestNoSecretIsEverALiteralInARenderedEnvironment(t *testing.T) {
	values := baseValues()
	set(values, "server.env", map[string]any{"REMEDY_LOG_LEVEL": "debug"})
	d := mustRender(t, values)
	for _, o := range append(d.all("Deployment"), d.all("StatefulSet")...) {
		for _, list := range []string{"containers", "initContainers"} {
			cs, _ := podSpec(t, o)[list].([]any)
			for _, c := range cs {
				for name, e := range env(c.(map[string]any)) {
					secretLike := strings.Contains(name, "PASSWORD") || strings.Contains(name, "TOKEN") || strings.Contains(name, "KEY")
					if _, literal := e["value"]; secretLike && literal {
						t.Errorf("%s of %v has a literal value; a secret must come from a Secret", name, dig(t, o, "metadata", "name"))
					}
				}
			}
		}
	}
}

func TestServerEnvRefusesAnythingNamedLikeASecret(t *testing.T) {
	for _, name := range []string{"MY_API_TOKEN", "DB_PASSWORD", "SIGNING_KEY"} {
		values := baseValues()
		set(values, "server.env", map[string]any{name: "x"})
		mustFail(t, values, name)
	}
	values := baseValues()
	set(values, "server.env", map[string]any{"REMEDY_POLL_INTERVAL": "2m", "REMEDY_LOG_LEVEL": "debug"})
	e := env(container(t, podSpec(t, mustRender(t, values).find("Deployment", "remedy-server")), "server"))
	if e["REMEDY_POLL_INTERVAL"]["value"] != "2m" || e["REMEDY_LOG_LEVEL"]["value"] != "debug" {
		t.Fatalf("server.env did not reach the container: %v", e)
	}
}

func TestTheServicesSeparateThePublicAndTheInternalPort(t *testing.T) {
	d := mustRender(t, baseValues())
	public, internal := d.find("Service", "remedy-server"), d.find("Service", "remedy-server-internal")
	if public == nil || internal == nil {
		t.Fatal("both Services must exist")
	}
	if ports, _ := dig(t, public, "spec", "ports").([]any); len(ports) != 1 || dig(t, ports[0], "port") != 8080 {
		t.Fatalf("the public Service ports = %v, want only 8080", dig(t, public, "spec", "ports"))
	}
	if ports, _ := dig(t, internal, "spec", "ports").([]any); len(ports) != 1 || dig(t, ports[0], "port") != 8081 {
		t.Fatalf("the internal Service ports = %v, want only 8081", dig(t, internal, "spec", "ports"))
	}
	if dig(t, internal, "spec", "type") != "ClusterIP" {
		t.Fatalf("the internal Service type = %v, want ClusterIP", dig(t, internal, "spec", "type"))
	}
}

func TestThePublicServiceCanBeANodePort(t *testing.T) {
	values := baseValues()
	set(values, "server.service.type", "NodePort")
	set(values, "server.service.nodePort", 30080)
	svc := mustRender(t, values).find("Service", "remedy-server")
	if dig(t, svc, "spec", "type") != "NodePort" || dig(t, svc, "spec", "ports", 0, "nodePort") != 30080 {
		t.Fatalf("service = %v", svc["spec"])
	}
}

func TestTheDataVolumeIsKeptAndAnExistingClaimIsUsedAsIs(t *testing.T) {
	d := mustRender(t, baseValues())
	pvc := d.find("PersistentVolumeClaim", "remedy-data")
	if pvc == nil {
		t.Fatal("no PersistentVolumeClaim remedy-data")
	}
	if dig(t, pvc, "metadata", "annotations", "helm.sh/resource-policy") != "keep" {
		t.Fatalf("the data claim must survive helm uninstall: annotations = %v", dig(t, pvc, "metadata", "annotations"))
	}
	if dig(t, pvc, "spec", "accessModes", 0) != "ReadWriteOnce" {
		t.Fatalf("accessModes = %v", dig(t, pvc, "spec", "accessModes"))
	}

	values := baseValues()
	set(values, "server.persistence.existingClaim", "my-claim")
	d = mustRender(t, values)
	if d.find("PersistentVolumeClaim", "remedy-data") != nil {
		t.Fatal("no claim may be created when an existing one is named")
	}
	vols, _ := podSpec(t, d.find("Deployment", "remedy-server"))["volumes"].([]any)
	found := false
	for _, v := range vols {
		if dig(t, v, "persistentVolumeClaim", "claimName") == "my-claim" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the pod does not use my-claim: %v", vols)
	}
}

func TestTheRunnerHasNoServiceAccountToken(t *testing.T) {
	d := mustRender(t, baseValues())
	sts := d.find("StatefulSet", "remedy-runner")
	if sts == nil {
		t.Fatal("no StatefulSet remedy-runner")
	}
	pod := podSpec(t, sts)
	if pod["automountServiceAccountToken"] != false {
		t.Fatalf("pod automountServiceAccountToken = %v, want false", pod["automountServiceAccountToken"])
	}
	sa := d.find("ServiceAccount", "remedy-runner")
	if sa == nil || sa["automountServiceAccountToken"] != false {
		t.Fatalf("the runner's ServiceAccount must exist and not mount a token: %v", sa)
	}
	if pod["serviceAccountName"] != "remedy-runner" {
		t.Fatalf("serviceAccountName = %v", pod["serviceAccountName"])
	}
	vols, _ := pod["volumes"].([]any)
	for _, v := range vols {
		if dig(t, v, "projected") != nil && strings.Contains(toString(v), "serviceAccountToken") {
			t.Fatalf("the runner pod has a projected service account token: %v", v)
		}
	}
}

func TestTheRunnerIsHardenedAndHasOneReplica(t *testing.T) {
	sts := mustRender(t, baseValues()).find("StatefulSet", "remedy-runner")
	if dig(t, sts, "spec", "replicas") != 1 {
		t.Fatalf("replicas = %v, want 1 (the runner is sequential)", dig(t, sts, "spec", "replicas"))
	}
	pod := podSpec(t, sts)
	if dig(t, pod, "securityContext", "runAsNonRoot") != true || dig(t, pod, "securityContext", "runAsUser") != 65532 ||
		dig(t, pod, "securityContext", "seccompProfile", "type") != "RuntimeDefault" {
		t.Fatalf("pod securityContext = %v", pod["securityContext"])
	}
	for _, name := range []string{"runner", "install-cli"} {
		sc := container(t, pod, name)["securityContext"]
		if dig(t, sc, "readOnlyRootFilesystem") != true || dig(t, sc, "allowPrivilegeEscalation") != false ||
			dig(t, sc, "capabilities", "drop", 0) != "ALL" {
			t.Errorf("%s securityContext = %v", name, sc)
		}
	}
}

func TestTheRunnerReachesTheInternalServiceWithItsTokenFromTheSecret(t *testing.T) {
	values := baseValues()
	set(values, "runner.model", "sonnet")
	set(values, "runner.runTimeout", "15m")
	e := env(container(t, podSpec(t, mustRender(t, values).find("StatefulSet", "remedy-runner")), "runner"))
	if e["REMEDY_SERVER_URL"]["value"] != "http://remedy-server-internal.remedy-system.svc:8081" {
		t.Errorf("REMEDY_SERVER_URL = %v", e["REMEDY_SERVER_URL"]["value"])
	}
	ref := dig(t, e["REMEDY_RUNNER_TOKEN"], "valueFrom", "secretKeyRef")
	if dig(t, ref, "name") != "remedy-secrets" || dig(t, ref, "key") != "runner-token" {
		t.Errorf("REMEDY_RUNNER_TOKEN comes from %v", ref)
	}
	for name, want := range map[string]string{
		"REMEDY_WORKSPACES": "/workspaces", "REMEDY_CLAUDE_BIN": "/opt/claude/claude", "HOME": "/state",
		"CLAUDE_CONFIG_DIR": "/state/claude", "TMPDIR": "/tmp", "TERM": "xterm-256color",
		"REMEDY_CLAUDE_MODEL": "sonnet", "REMEDY_RUN_TIMEOUT": "15m",
	} {
		if e[name]["value"] != want {
			t.Errorf("%s = %v, want %q", name, e[name]["value"], want)
		}
	}
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if _, ok := e[name]; ok {
			t.Errorf("the runner must not have %s: it would bill the API or hold a credential", name)
		}
	}
}

func TestTheInitContainerInstallsThePinnedCLIAndTheRunnerMountsItReadOnly(t *testing.T) {
	pod := podSpec(t, mustRender(t, baseValues()).find("StatefulSet", "remedy-runner"))
	init := container(t, pod, "install-cli")
	if init["image"] != container(t, pod, "runner")["image"] {
		t.Fatalf("the init container must use the runner image (the image has no shell): %v", init["image"])
	}
	args := toString(init["args"])
	sha := strings.Repeat("a", 64)
	for _, want := range []string{
		"install-cli", "--version=2.1.288",
		"--url-template=https://downloads.example.invalid/{version}/{platform}/claude",
		"--archive=none", "--dest=/opt/claude",
		"--platform=amd64=linux-x64@" + sha, "--platform=arm64=linux-arm64@" + sha,
	} {
		if !strings.Contains(args, want) {
			t.Errorf("the init container's args lack %q:\n%s", want, args)
		}
	}
	mounts, _ := container(t, pod, "runner")["volumeMounts"].([]any)
	readOnly := false
	for _, m := range mounts {
		if dig(t, m, "mountPath") == "/opt/claude" && dig(t, m, "readOnly") == true {
			readOnly = true
		}
	}
	if !readOnly {
		t.Fatalf("the runner must mount /opt/claude read-only: %v", mounts)
	}
}

func TestATarGzArtifactPassesItsMember(t *testing.T) {
	values := baseValues()
	set(values, "runner.cli.archive", "tar.gz")
	set(values, "runner.cli.member", "claude")
	args := toString(container(t, podSpec(t, mustRender(t, values).find("StatefulSet", "remedy-runner")), "install-cli")["args"])
	if !strings.Contains(args, "--archive=tar.gz") || !strings.Contains(args, "--member=claude") {
		t.Fatalf("args = %s", args)
	}
}

func TestTheRunnerNeedsThePinnedCLI(t *testing.T) {
	for path, want := range map[string]string{
		"runner.cli.version":     "runner.cli.version",
		"runner.cli.urlTemplate": "runner.cli.urlTemplate",
	} {
		values := baseValues()
		set(values, path, "")
		mustFail(t, values, want)
	}
	values := baseValues()
	set(values, "runner.cli.platforms.amd64.sha256", "")
	mustFail(t, values, "runner.cli.platforms.amd64.sha256")
}

func TestTheRunnerKeepsItsStateOnAClaimThatSurvivesARestart(t *testing.T) {
	sts := mustRender(t, baseValues()).find("StatefulSet", "remedy-runner")
	tpls, _ := dig(t, sts, "spec", "volumeClaimTemplates").([]any)
	if len(tpls) != 1 || dig(t, tpls[0], "metadata", "name") != "state" {
		t.Fatalf("volumeClaimTemplates = %v, want one named state", tpls)
	}

	values := baseValues()
	set(values, "runner.persistence.existingClaim", "claude-state")
	sts = mustRender(t, values).find("StatefulSet", "remedy-runner")
	if dig(t, sts, "spec", "volumeClaimTemplates") != nil {
		t.Fatal("no claim template may be rendered when an existing claim is named")
	}
	vols, _ := podSpec(t, sts)["volumes"].([]any)
	found := false
	for _, v := range vols {
		if dig(t, v, "name") == "state" && dig(t, v, "persistentVolumeClaim", "claimName") == "claude-state" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the state volume does not use claude-state: %v", vols)
	}
}

func TestTheRunnerCanBeLeftOut(t *testing.T) {
	values := baseValues()
	set(values, "runner.enabled", false)
	set(values, "runner.cli", map[string]any{})
	d := mustRender(t, values)
	if d.find("StatefulSet", "remedy-runner") != nil || d.find("ServiceAccount", "remedy-runner") != nil {
		t.Fatal("nothing of the runner may be rendered when runner.enabled is false")
	}
}

func TestThereIsNoIngressByDefault(t *testing.T) {
	if got := mustRender(t, baseValues()).all("Ingress"); len(got) != 0 {
		t.Fatalf("%d Ingress objects rendered, want none", len(got))
	}
}

func TestTheIngressPointsAtThePublicServiceOnly(t *testing.T) {
	values := baseValues()
	set(values, "ingress.enabled", true)
	set(values, "ingress.host", "remedy.example.invalid")
	set(values, "ingress.className", "traefik")
	set(values, "ingress.tls.secretName", "remedy-tls")
	set(values, "ingress.annotations", map[string]any{"cert-manager.io/cluster-issuer": "letsencrypt"})
	ing := mustRender(t, values).find("Ingress", "remedy")
	if ing == nil {
		t.Fatal("no Ingress remedy")
	}
	if dig(t, ing, "spec", "ingressClassName") != "traefik" {
		t.Errorf("ingressClassName = %v", dig(t, ing, "spec", "ingressClassName"))
	}
	rules, _ := dig(t, ing, "spec", "rules").([]any)
	if len(rules) != 1 || dig(t, rules[0], "host") != "remedy.example.invalid" {
		t.Fatalf("rules = %v", rules)
	}
	paths, _ := dig(t, rules[0], "http", "paths").([]any)
	if len(paths) != 1 || dig(t, paths[0], "path") != "/" || dig(t, paths[0], "backend", "service", "name") != "remedy-server" ||
		dig(t, paths[0], "backend", "service", "port", "number") != 8080 {
		t.Fatalf("paths = %v, want one path / to remedy-server:8080 (never the internal Service)", paths)
	}
	if dig(t, ing, "spec", "tls", 0, "secretName") != "remedy-tls" || dig(t, ing, "spec", "tls", 0, "hosts", 0) != "remedy.example.invalid" {
		t.Errorf("tls = %v", dig(t, ing, "spec", "tls"))
	}
	if dig(t, ing, "metadata", "annotations", "cert-manager.io/cluster-issuer") != "letsencrypt" {
		t.Errorf("annotations = %v", dig(t, ing, "metadata", "annotations"))
	}
}

func TestTheIngressNeedsAHostAndHasNoTLSBlockWithoutASecret(t *testing.T) {
	values := baseValues()
	set(values, "ingress.enabled", true)
	mustFail(t, values, "ingress.host")

	set(values, "ingress.host", "remedy.example.invalid")
	ing := mustRender(t, values).find("Ingress", "remedy")
	if dig(t, ing, "spec", "tls") != nil {
		t.Fatalf("tls = %v, want none without ingress.tls.secretName", dig(t, ing, "spec", "tls"))
	}
	if dig(t, ing, "spec", "ingressClassName") != nil {
		t.Fatalf("ingressClassName = %v, want none when className is empty (the cluster's default applies)", dig(t, ing, "spec", "ingressClassName"))
	}
}

func policy(t *testing.T, d docs, name string) map[string]any {
	t.Helper()
	p := d.find("NetworkPolicy", name)
	if p == nil {
		t.Fatalf("no NetworkPolicy %s", name)
	}
	return p
}

// rulesText is a policy's ingress or egress rules as YAML text, for substring checks.
func rulesText(p map[string]any, which string) string { return toString(dig(nil, p, "spec", which)) }

func TestTheServerPolicyLetsOnlyRunnersReachTheInternalPort(t *testing.T) {
	p := policy(t, mustRender(t, baseValues()), "remedy-server")
	ingress, _ := dig(t, p, "spec", "ingress").([]any)
	if len(ingress) != 2 {
		t.Fatalf("%d ingress rules, want 2 (public, internal): %v", len(ingress), ingress)
	}
	public, internal := toString(ingress[0]), toString(ingress[1])
	if !strings.Contains(public, "port: 8080") || strings.Contains(public, "8081") {
		t.Errorf("the public rule must list only 8080:\n%s", public)
	}
	if !strings.Contains(public, "kubernetes.io/metadata.name: kube-system") {
		t.Errorf("the public rule must name the configured peers:\n%s", public)
	}
	if !strings.Contains(internal, "port: 8081") || strings.Contains(internal, "8080") ||
		!strings.Contains(internal, "app.kubernetes.io/component: runner") || strings.Contains(internal, "namespaceSelector") {
		t.Errorf("the internal rule must list only runner pods and 8081:\n%s", internal)
	}
}

func TestThePublicPeersAreConfigurable(t *testing.T) {
	values := baseValues()
	set(values, "networkPolicy.public.from", []any{map[string]any{"ipBlock": map[string]any{"cidr": "0.0.0.0/0"}}})
	p := policy(t, mustRender(t, values), "remedy-server")
	if got := rulesText(p, "ingress"); !strings.Contains(got, "cidr: 0.0.0.0/0") || strings.Contains(got, "kube-system") {
		t.Fatalf("ingress = %s", got)
	}
}

func TestTheRunnerPolicyAllowsNoIngressAndOnlyServerDNSAndTheInternet(t *testing.T) {
	p := policy(t, mustRender(t, baseValues()), "remedy-runner")
	types := toString(dig(t, p, "spec", "policyTypes"))
	if !strings.Contains(types, "Ingress") || !strings.Contains(types, "Egress") {
		t.Fatalf("policyTypes = %s, want both: the runner takes no connection", types)
	}
	if dig(t, p, "spec", "ingress") != nil {
		t.Fatalf("ingress = %v, want no rule at all", dig(t, p, "spec", "ingress"))
	}
	egress := rulesText(p, "egress")
	for _, want := range []string{
		"k8s-app: kube-dns", "app.kubernetes.io/component: server", "port: 8081", "port: 443",
		"cidr: 0.0.0.0/0", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16",
	} {
		if !strings.Contains(egress, want) {
			t.Errorf("the runner's egress lacks %q:\n%s", want, egress)
		}
	}
	if strings.Contains(egress, "6443") {
		t.Errorf("the runner must not be allowed to reach the API server:\n%s", egress)
	}
}

func TestTheServerPolicyNamesTheAPIServerOnlyWhenTheClusterIsOn(t *testing.T) {
	p := policy(t, mustRender(t, baseValues()), "remedy-server")
	if strings.Contains(rulesText(p, "egress"), "6443") {
		t.Fatalf("the cluster is off: no API server rule expected:\n%s", rulesText(p, "egress"))
	}

	values := baseValues()
	set(values, "cluster.enabled", true)
	mustFail(t, values, "networkPolicy.apiServer.cidrs")

	set(values, "networkPolicy.apiServer.cidrs", []any{"172.18.0.2/32"})
	p = policy(t, mustRender(t, values), "remedy-server")
	egress := rulesText(p, "egress")
	if !strings.Contains(egress, "cidr: 172.18.0.2/32") || !strings.Contains(egress, "port: 6443") {
		t.Fatalf("the API server rule is missing:\n%s", egress)
	}
}

func TestPoliciesCanBeTurnedOff(t *testing.T) {
	values := baseValues()
	set(values, "networkPolicy.enabled", false)
	if got := mustRender(t, values).all("NetworkPolicy"); len(got) != 0 {
		t.Fatalf("%d policies rendered, want none", len(got))
	}
}

func TestClusterOnWithoutPoliciesNeedsNoAPIServerAddress(t *testing.T) {
	values := baseValues()
	set(values, "networkPolicy.enabled", false)
	set(values, "cluster.enabled", true)
	mustRender(t, values)
}

// The tests below decode the rendered policies and check their structure: a substring check would pass if a rule
// were widened or split.

var privateRanges = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16"}

func listOf(v any) []any {
	l, _ := v.([]any)
	return l
}

func egressRules(t *testing.T, p map[string]any) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, r := range listOf(dig(t, p, "spec", "egress")) {
		m, ok := r.(map[string]any)
		if !ok {
			t.Fatalf("egress rule %v is not an object", r)
		}
		out = append(out, m)
	}
	return out
}

// ports of a rule as sorted "PROTOCOL/port" strings.
func rulePorts(rule map[string]any) []string {
	var out []string
	for _, p := range listOf(rule["ports"]) {
		out = append(out, fmt.Sprintf("%v/%v", dig(nil, p, "protocol"), dig(nil, p, "port")))
	}
	sort.Strings(out)
	return out
}

// ipBlocks lists the ipBlock of every peer of the rules, as cidr plus the except list.
type block struct {
	cidr   string
	except []string
}

func ipBlocks(rules []map[string]any) []block {
	var out []block
	for _, r := range rules {
		for _, peer := range listOf(r["to"]) {
			ib, ok := dig(nil, peer, "ipBlock").(map[string]any)
			if !ok {
				continue
			}
			b := block{cidr: fmt.Sprint(ib["cidr"])}
			for _, e := range listOf(ib["except"]) {
				b.except = append(b.except, fmt.Sprint(e))
			}
			out = append(out, b)
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// checkInternetRule pins the one rule that opens the public internet: TCP 443 to 0.0.0.0/0 except the private ranges,
// in a rule of its own.
func checkInternetRule(t *testing.T, name string, p map[string]any) {
	t.Helper()
	var found []map[string]any
	for _, r := range egressRules(t, p) {
		for _, b := range ipBlocks([]map[string]any{r}) {
			if b.cidr == "0.0.0.0/0" {
				found = append(found, r)
			}
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s: %d egress rules open 0.0.0.0/0, want exactly 1", name, len(found))
	}
	rule := found[0]
	if n := len(listOf(rule["to"])); n != 1 {
		t.Errorf("%s: the internet rule has %d peers, want only the ipBlock: %v", name, n, rule["to"])
	}
	if got := ipBlocks(found)[0].except; !equalStrings(got, privateRanges) {
		t.Errorf("%s: except = %v, want exactly %v", name, got, privateRanges)
	}
	if got := rulePorts(rule); !equalStrings(got, []string{"TCP/443"}) {
		t.Errorf("%s: the internet rule's ports = %v, want only TCP/443", name, got)
	}
}

// checkDNSRule pins the DNS rule: one peer that is the kube-dns pods of the DNS namespace (both selectors in the same
// peer, otherwise it would be every pod of that namespace or every kube-dns pod), on port 53 only.
func checkDNSRule(t *testing.T, name string, p map[string]any, namespace string) {
	t.Helper()
	var found []map[string]any
	for _, r := range egressRules(t, p) {
		for _, peer := range listOf(r["to"]) {
			if dig(nil, peer, "podSelector", "matchLabels", "k8s-app") == "kube-dns" {
				found = append(found, r)
			}
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s: %d egress rules name kube-dns, want exactly 1", name, len(found))
	}
	rule := found[0]
	peers := listOf(rule["to"])
	if len(peers) != 1 {
		t.Fatalf("%s: the DNS rule has %d peers, want 1 peer that holds both selectors: %v", name, len(peers), peers)
	}
	if got := dig(nil, peers[0], "namespaceSelector", "matchLabels"); !reflect.DeepEqual(got, map[string]any{"kubernetes.io/metadata.name": namespace}) {
		t.Errorf("%s: the DNS peer's namespaceSelector = %v, want the namespace %s", name, got, namespace)
	}
	if got := dig(nil, peers[0], "podSelector", "matchLabels"); !reflect.DeepEqual(got, map[string]any{"k8s-app": "kube-dns"}) {
		t.Errorf("%s: the DNS peer's podSelector = %v, want k8s-app: kube-dns", name, got)
	}
	if got := rulePorts(rule); !equalStrings(got, []string{"TCP/53", "UDP/53"}) {
		t.Errorf("%s: the DNS rule's ports = %v, want TCP/53 and UDP/53 only", name, got)
	}
}

func TestTheEgressStructureOfTheServerPolicyIsPinned(t *testing.T) {
	for _, ns := range []string{"kube-system", "dns-system"} {
		values := baseValues()
		set(values, "networkPolicy.dnsNamespace", ns)
		p := policy(t, mustRender(t, values), "remedy-server")
		checkDNSRule(t, "server", p, ns)
		checkInternetRule(t, "server", p)
		rules := egressRules(t, p)
		if len(rules) != 2 {
			t.Errorf("server, cluster off: %d egress rules, want 2 (DNS, internet): %v", len(rules), rules)
		}
		if blocks := ipBlocks(rules); len(blocks) != 1 || blocks[0].cidr != "0.0.0.0/0" {
			t.Errorf("server, cluster off: ipBlocks = %v, want only 0.0.0.0/0", blocks)
		}
	}
}

func TestTheServerPolicyWithTheClusterOnHasExactlyTheConfiguredAPIAddresses(t *testing.T) {
	values := baseValues()
	set(values, "cluster.enabled", true)
	set(values, "networkPolicy.apiServer.cidrs", []any{"172.18.0.2/32", "172.18.0.3/32"})
	set(values, "networkPolicy.apiServer.port", 8443)
	p := policy(t, mustRender(t, values), "remedy-server")
	checkDNSRule(t, "server", p, "kube-system")
	checkInternetRule(t, "server", p)
	rules := egressRules(t, p)
	if len(rules) != 3 {
		t.Fatalf("%d egress rules, want 3 (DNS, API server, internet): %v", len(rules), rules)
	}
	var api []map[string]any
	for _, r := range rules {
		for _, b := range ipBlocks([]map[string]any{r}) {
			if b.cidr != "0.0.0.0/0" {
				api = append(api, r)
				break
			}
		}
	}
	if len(api) != 1 {
		t.Fatalf("%d rules with an address of their own, want 1 (the API server)", len(api))
	}
	var cidrs []string
	for _, b := range ipBlocks(api) {
		if len(b.except) != 0 {
			t.Errorf("the API server ipBlock %s has an except list %v", b.cidr, b.except)
		}
		cidrs = append(cidrs, b.cidr)
	}
	if !equalStrings(cidrs, []string{"172.18.0.2/32", "172.18.0.3/32"}) || len(listOf(api[0]["to"])) != 2 {
		t.Errorf("API server peers = %v, want exactly the two configured /32 addresses", cidrs)
	}
	if got := rulePorts(api[0]); !equalStrings(got, []string{"TCP/8443"}) {
		t.Errorf("API server ports = %v, want only the configured port TCP/8443", got)
	}
}

func TestTheEgressStructureOfTheRunnerPolicyIsPinned(t *testing.T) {
	values := baseValues()
	set(values, "networkPolicy.dnsNamespace", "dns-system")
	set(values, "cluster.enabled", true)
	set(values, "networkPolicy.apiServer.cidrs", []any{"172.18.0.2/32"})
	set(values, "networkPolicy.apiServer.port", 8443)
	p := policy(t, mustRender(t, values), "remedy-runner")
	checkDNSRule(t, "runner", p, "dns-system")
	checkInternetRule(t, "runner", p)
	rules := egressRules(t, p)
	if len(rules) != 3 {
		t.Fatalf("%d egress rules, want 3 (DNS, server, internet): %v", len(rules), rules)
	}
	if blocks := ipBlocks(rules); len(blocks) != 1 || blocks[0].cidr != "0.0.0.0/0" {
		t.Errorf("runner: ipBlocks = %v, want only 0.0.0.0/0 (the API server address must never appear)", blocks)
	}
	allowed := map[string]bool{"TCP/53": true, "UDP/53": true, "TCP/8081": true, "TCP/443": true}
	for _, r := range rules {
		for _, port := range rulePorts(r) {
			if !allowed[port] {
				t.Errorf("runner: port %s is allowed, want only 53, 8081 and 443", port)
			}
		}
	}
	// The rule to the control plane: the server pods only, on the internal port only.
	var toServer []map[string]any
	for _, r := range rules {
		for _, peer := range listOf(r["to"]) {
			if dig(nil, peer, "podSelector", "matchLabels", "app.kubernetes.io/component") == "server" {
				toServer = append(toServer, r)
			}
		}
	}
	if len(toServer) != 1 || len(listOf(toServer[0]["to"])) != 1 || !equalStrings(rulePorts(toServer[0]), []string{"TCP/8081"}) {
		t.Errorf("runner: want one rule to one server peer on TCP/8081 only, got %v", toServer)
	}
}

func TestTheRunnerPolicyIsLeftOutWithTheRunner(t *testing.T) {
	values := baseValues()
	set(values, "runner.enabled", false)
	d := mustRender(t, values)
	if d.find("NetworkPolicy", "remedy-runner") != nil {
		t.Fatal("remedy-runner policy rendered with runner.enabled=false")
	}
	if d.find("NetworkPolicy", "remedy-server") == nil {
		t.Fatal("the server policy must stay")
	}
}

func TestAnEmptyPublicPeerListIsRefused(t *testing.T) {
	// In a NetworkPolicy an empty "from" means every source: the public port would be open to everyone.
	for name, v := range map[string]any{"empty list": []any{}, "null": nil} {
		values := baseValues()
		set(values, "networkPolicy.public.from", v)
		t.Run(name, func(t *testing.T) { mustFail(t, values, "networkPolicy.public.from") })
	}
	values := baseValues()
	set(values, "networkPolicy.public", nil)
	mustFail(t, values, "networkPolicy.public.from")

	// With the policies off the value is not used.
	set(values, "networkPolicy.enabled", false)
	mustRender(t, values)
}
