package deploy_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// clusterValues are the base values with the cluster on, read and write, and one write namespace. The policy needs the
// API server's address.
func clusterValues() map[string]any {
	v := baseValues()
	set(v, "cluster.enabled", true)
	set(v, "cluster.write.enabled", true)
	set(v, "cluster.write.namespaces", []any{"demo"})
	set(v, "networkPolicy.apiServer.cidrs", []any{"172.18.0.2/32"})
	return v
}

func strs(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, e := range list {
		out = append(out, fmt.Sprint(e))
	}
	return out
}

// ruleSet is a role's rules as sorted lines "groups|resources|verbs|resourceNames", each list joined by commas.
func ruleSet(role map[string]any) []string {
	var out []string
	rules, _ := role["rules"].([]any)
	for _, r := range rules {
		m := r.(map[string]any)
		out = append(out, strings.Join(strs(m["apiGroups"]), ",")+"|"+strings.Join(strs(m["resources"]), ",")+"|"+
			strings.Join(strs(m["verbs"]), ",")+"|"+strings.Join(strs(m["resourceNames"]), ","))
	}
	slices.Sort(out)
	return out
}

func wantRules(t *testing.T, what string, role map[string]any, want ...string) {
	t.Helper()
	if role == nil {
		t.Fatalf("%s does not exist", what)
	}
	slices.Sort(want)
	if got := ruleSet(role); !slices.Equal(got, want) {
		t.Fatalf("%s has the rules\n  %s\nwant exactly\n  %s", what, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func subjectOf(t *testing.T, binding map[string]any) string {
	t.Helper()
	subjects, _ := binding["subjects"].([]any)
	if len(subjects) != 1 {
		t.Fatalf("%v has %d subjects, want 1", dig(t, binding, "metadata", "name"), len(subjects))
	}
	return fmt.Sprintf("%v/%v/%v", dig(t, subjects[0], "kind"), dig(t, subjects[0], "namespace"), dig(t, subjects[0], "name"))
}

func TestWithTheClusterOffThereIsNoClusterIdentity(t *testing.T) {
	d := mustRender(t, baseValues())
	for _, kind := range []string{"ClusterRole", "ClusterRoleBinding", "Role", "RoleBinding", "CronJob", "Job"} {
		if got := d.all(kind); len(got) != 0 {
			t.Errorf("%d %s objects rendered with the cluster off, want none", len(got), kind)
		}
	}
	for _, name := range []string{"remedy-read", "remedy-write", "remedy-token-refresher"} {
		if d.find("ServiceAccount", name) != nil {
			t.Errorf("the ServiceAccount %s must not exist with the cluster off", name)
		}
	}
	if d.find("ServiceAccount", "remedy-server") == nil {
		t.Error("the no-rights account remedy-server must exist with the cluster off")
	}
	if pod := podSpec(t, d.find("Deployment", "remedy-server")); pod["serviceAccountName"] != "remedy-server" {
		t.Errorf("serviceAccountName = %v, want remedy-server", pod["serviceAccountName"])
	}
}

func TestTheReadIdentityCanOnlyGetAndListWhatTheReadToolsShow(t *testing.T) {
	values := baseValues()
	set(values, "cluster.enabled", true)
	set(values, "networkPolicy.apiServer.cidrs", []any{"172.18.0.2/32"})
	d := mustRender(t, values)

	wantRules(t, "the read ClusterRole", d.find("ClusterRole", "remedy-read-remedy-system"),
		"|pods,pods/log,events,nodes|get,list|",
		"apps|deployments,statefulsets,daemonsets,replicasets|get,list|",
		"argoproj.io|applications|get,list|")
	if got := subjectOf(t, d.find("ClusterRoleBinding", "remedy-read-remedy-system")); got != "ServiceAccount/remedy-system/remedy-read" {
		t.Errorf("the read ClusterRoleBinding's subject = %s", got)
	}
	if d.find("ServiceAccount", "remedy-read") == nil || d.find("ServiceAccount", "remedy-server") != nil {
		t.Error("with the cluster on the control plane's account is remedy-read, and remedy-server is not rendered")
	}
	if pod := podSpec(t, d.find("Deployment", "remedy-server")); pod["serviceAccountName"] != "remedy-read" {
		t.Errorf("serviceAccountName = %v, want remedy-read", pod["serviceAccountName"])
	}
	if got := d.all("Role"); len(got) != 0 {
		t.Errorf("read only: %d Roles rendered, want none (no write identity)", len(got))
	}
	if d.find("ServiceAccount", "remedy-write") != nil {
		t.Error("read only: the write account must not exist")
	}
}

func TestTheWriteIdentityCanOnlyRestartDeleteAndSyncAndReadsNothing(t *testing.T) {
	values := clusterValues()
	set(values, "cluster.write.namespaces", []any{"demo", "staging"})
	d := mustRender(t, values)

	for _, ns := range []string{"demo", "staging"} {
		var role map[string]any
		for _, r := range d.all("Role") {
			if dig(t, r, "metadata", "name") == "remedy-write" && dig(t, r, "metadata", "namespace") == ns {
				role = r
			}
		}
		wantRules(t, "the write Role in "+ns, role, "apps|deployments,statefulsets,daemonsets|patch|", "|pods|delete|")
	}
	var argo map[string]any
	for _, r := range d.all("Role") {
		if dig(t, r, "metadata", "name") == "remedy-write" && dig(t, r, "metadata", "namespace") == "argocd" {
			argo = r
		}
	}
	wantRules(t, "the write Role in argocd", argo, "argoproj.io|applications|patch|")

	bindings := 0
	for _, b := range d.all("RoleBinding") {
		if dig(t, b, "metadata", "name") != "remedy-write" {
			continue
		}
		bindings++
		if got := subjectOf(t, b); got != "ServiceAccount/remedy-system/remedy-write" {
			t.Errorf("a write RoleBinding's subject = %s", got)
		}
	}
	if bindings != 3 {
		t.Errorf("%d write RoleBindings, want 3 (demo, staging, argocd)", bindings)
	}
	sa := d.find("ServiceAccount", "remedy-write")
	if sa == nil || sa["automountServiceAccountToken"] != false {
		t.Errorf("the write account must exist and mount no token: %v", sa)
	}
	for _, r := range d.all("Role") {
		for _, line := range ruleSet(r) {
			if strings.Contains(line, "secrets") && dig(t, r, "metadata", "name") == "remedy-write" {
				t.Errorf("a write role names secrets: %s", line)
			}
		}
	}
}

func TestTheArgoNamespaceIsConfigurable(t *testing.T) {
	values := clusterValues()
	set(values, "cluster.argoNamespace", "gitops")
	found := false
	for _, r := range mustRender(t, values).all("Role") {
		if dig(t, r, "metadata", "name") == "remedy-write" && dig(t, r, "metadata", "namespace") == "gitops" {
			found = true
		}
	}
	if !found {
		t.Fatal("no write Role in the configured Argo CD namespace")
	}
}

func TestTheRefresherCanMintOneAccountsTokenAndPatchOneSecretAndNothingElse(t *testing.T) {
	d := mustRender(t, clusterValues())
	var role map[string]any
	for _, r := range d.all("Role") {
		if dig(t, r, "metadata", "name") == "remedy-token-refresher" {
			role = r
			if dig(t, r, "metadata", "namespace") != "remedy-system" {
				t.Errorf("the refresher's Role is in %v, want the release namespace", dig(t, r, "metadata", "namespace"))
			}
		}
	}
	wantRules(t, "the refresher's Role", role,
		"|serviceaccounts/token|create|remedy-write",
		"|secrets|patch|remedy-write-token")
	for _, b := range d.all("RoleBinding") {
		if dig(t, b, "metadata", "name") == "remedy-token-refresher" {
			if got := subjectOf(t, b); got != "ServiceAccount/remedy-system/remedy-token-refresher" {
				t.Errorf("the refresher's binding subject = %s", got)
			}
		}
	}
	if d.find("ServiceAccount", "remedy-token-refresher") == nil {
		t.Error("no ServiceAccount remedy-token-refresher")
	}
}

func TestTheReleaseNamespaceCanNeverBeWritten(t *testing.T) {
	values := clusterValues()
	set(values, "cluster.write.namespaces", []any{"demo", "remedy-system"})
	mustFail(t, values, "remedy-system")
	_, stderr, _ := render(t, values)
	if !strings.Contains(stderr, "never change itself") {
		t.Errorf("the message must say why:\n%s", stderr)
	}
}

func TestWriteNeedsTheClusterAndANamespaceAndGoodNames(t *testing.T) {
	cases := map[string]func(map[string]any){
		"write without the cluster": func(v map[string]any) { set(v, "cluster.enabled", false) },
		"no namespace":              func(v map[string]any) { set(v, "cluster.write.namespaces", []any{}) },
		"an upper case namespace":   func(v map[string]any) { set(v, "cluster.write.namespaces", []any{"Demo"}) },
		"a wildcard":                func(v map[string]any) { set(v, "cluster.write.namespaces", []any{"*"}) },
		"a namespace with a comma":  func(v map[string]any) { set(v, "cluster.write.namespaces", []any{"a,b"}) },
	}
	for name, change := range cases {
		values := clusterValues()
		change(values)
		if _, _, err := render(t, values); err == nil {
			t.Errorf("%s: the render must fail", name)
		}
	}
}

func serverContainer(t *testing.T, d docs) (map[string]any, map[string]any) {
	t.Helper()
	pod := podSpec(t, d.find("Deployment", "remedy-server"))
	return pod, container(t, pod, "server")
}

func volume(pod map[string]any, name string) map[string]any {
	vols, _ := pod["volumes"].([]any)
	for _, v := range vols {
		if m := v.(map[string]any); m["name"] == name {
			return m
		}
	}
	return nil
}

func mounted(c map[string]any, path string) map[string]any {
	mounts, _ := c["volumeMounts"].([]any)
	for _, m := range mounts {
		if mm := m.(map[string]any); mm["mountPath"] == path {
			return mm
		}
	}
	return nil
}

func TestWithTheClusterOffTheServerHasNoClusterSettings(t *testing.T) {
	pod, c := serverContainer(t, mustRender(t, baseValues()))
	for name := range env(c) {
		if strings.HasPrefix(name, "REMEDY_K8S_") {
			t.Errorf("%s is set with the cluster off", name)
		}
	}
	if volume(pod, "cluster-read") != nil || volume(pod, "cluster-write") != nil {
		t.Error("no cluster volume expected with the cluster off")
	}
}

func TestTheServerReadsTheClusterWithAProjectedTokenAndTheClusterCA(t *testing.T) {
	values := baseValues()
	set(values, "cluster.enabled", true)
	set(values, "networkPolicy.apiServer.cidrs", []any{"172.18.0.2/32"})
	pod, c := serverContainer(t, mustRender(t, values))
	e := env(c)
	for name, want := range map[string]string{
		"REMEDY_K8S_API": "https://kubernetes.default.svc", "REMEDY_K8S_READ_TOKEN_FILE": "/var/run/remedy/read/token",
		"REMEDY_K8S_CA_FILE": "/var/run/remedy/read/ca.crt", "REMEDY_K8S_ARGO_NAMESPACE": "argocd",
	} {
		if e[name]["value"] != want {
			t.Errorf("%s = %v, want %q", name, e[name]["value"], want)
		}
	}
	for _, name := range []string{"REMEDY_K8S_WRITE_TOKEN_FILE", "REMEDY_K8S_WRITE_NAMESPACES"} {
		if _, ok := e[name]; ok {
			t.Errorf("%s is set although write is off", name)
		}
	}
	if pod["automountServiceAccountToken"] != false {
		t.Errorf("the pod must not mount the default token volume: %v", pod["automountServiceAccountToken"])
	}
	v := volume(pod, "cluster-read")
	if v == nil {
		t.Fatal("no cluster-read volume")
	}
	text := toString(v)
	for _, want := range []string{"serviceAccountToken:", "path: token", "expirationSeconds: 3600", "name: kube-root-ca.crt", "key: ca.crt", "path: ca.crt"} {
		if !strings.Contains(text, want) {
			t.Errorf("the cluster-read volume lacks %q:\n%s", want, text)
		}
	}
	if m := mounted(c, "/var/run/remedy/read"); m == nil || m["readOnly"] != true {
		t.Errorf("cluster-read must be mounted read-only at /var/run/remedy/read: %v", m)
	}
}

func TestTheServerTakesTheWriteTokenFromAnOptionalSecretAndTheAllowlistFromValues(t *testing.T) {
	values := clusterValues()
	set(values, "cluster.write.namespaces", []any{"demo", "staging"})
	pod, c := serverContainer(t, mustRender(t, values))
	e := env(c)
	if e["REMEDY_K8S_WRITE_TOKEN_FILE"]["value"] != "/var/run/remedy/write/token" {
		t.Errorf("REMEDY_K8S_WRITE_TOKEN_FILE = %v", e["REMEDY_K8S_WRITE_TOKEN_FILE"]["value"])
	}
	if e["REMEDY_K8S_WRITE_NAMESPACES"]["value"] != "demo,staging" {
		t.Errorf("REMEDY_K8S_WRITE_NAMESPACES = %v", e["REMEDY_K8S_WRITE_NAMESPACES"]["value"])
	}
	v := volume(pod, "cluster-write")
	if v == nil || dig(t, v, "secret", "secretName") != "remedy-write-token" || dig(t, v, "secret", "optional") != true {
		t.Fatalf("cluster-write = %v, want the optional Secret remedy-write-token (the server must start before it is filled)", v)
	}
	if m := mounted(c, "/var/run/remedy/write"); m == nil || m["readOnly"] != true {
		t.Errorf("cluster-write must be mounted read-only at /var/run/remedy/write: %v", m)
	}
	if m := mounted(c, "/var/run/remedy/write"); m != nil && m["subPath"] != nil {
		t.Error("a subPath mount is never updated by the kubelet: the refreshed token would not arrive")
	}
}

func TestTheWriteTokenSecretIsEmptyAndTheRefresherFillsItEveryHalfHour(t *testing.T) {
	d := mustRender(t, clusterValues())
	sec := d.find("Secret", "remedy-write-token")
	if sec == nil {
		t.Fatal("no Secret remedy-write-token")
	}
	if sec["data"] != nil || sec["stringData"] != nil {
		t.Fatalf("the Secret must carry no data (a value would be reset by an upgrade and live in git): %v", sec)
	}

	cj := d.find("CronJob", "remedy-token-refresh")
	if cj == nil {
		t.Fatal("no CronJob remedy-token-refresh")
	}
	if dig(t, cj, "spec", "schedule") != "*/30 * * * *" || dig(t, cj, "spec", "concurrencyPolicy") != "Forbid" {
		t.Errorf("schedule/concurrency = %v / %v", dig(t, cj, "spec", "schedule"), dig(t, cj, "spec", "concurrencyPolicy"))
	}
	pod := dig(t, cj, "spec", "jobTemplate", "spec", "template", "spec").(map[string]any)
	if pod["serviceAccountName"] != "remedy-token-refresher" || pod["automountServiceAccountToken"] != true || pod["restartPolicy"] != "Never" {
		t.Errorf("refresher pod = %v", pod)
	}
	c := container(t, pod, "refresh")
	if c["image"] != "ghcr.io/jaydee94/remedy-server:0.1.0" {
		t.Errorf("image = %v, want the control plane's image (it carries /remedy-tokenrefresh)", c["image"])
	}
	if got := toString(c["command"]); !strings.Contains(got, "/remedy-tokenrefresh") {
		t.Errorf("command = %s", got)
	}
	args := toString(c["args"])
	for _, want := range []string{"--namespace=remedy-system", "--account=remedy-write", "--secret=remedy-write-token", "--lifetime=2h", "--api=https://kubernetes.default.svc"} {
		if !strings.Contains(args, want) {
			t.Errorf("the refresher's args lack %q:\n%s", want, args)
		}
	}
	sc := c["securityContext"]
	if dig(t, sc, "readOnlyRootFilesystem") != true || dig(t, sc, "allowPrivilegeEscalation") != false || dig(t, sc, "capabilities", "drop", 0) != "ALL" {
		t.Errorf("the refresher's securityContext = %v", sc)
	}
	if dig(t, pod, "securityContext", "runAsNonRoot") != true || dig(t, pod, "securityContext", "seccompProfile", "type") != "RuntimeDefault" {
		t.Errorf("the refresher pod's securityContext = %v", pod["securityContext"])
	}
}

func TestAHookFillsTheTokenRightAfterAnInstallOrUpgrade(t *testing.T) {
	job := mustRender(t, clusterValues()).find("Job", "remedy-token-refresh-hook")
	if job == nil {
		t.Fatal("no hook Job remedy-token-refresh-hook")
	}
	ann := dig(t, job, "metadata", "annotations")
	if dig(t, ann, "helm.sh/hook") != "post-install,post-upgrade" {
		t.Errorf("hook = %v, want post-install,post-upgrade (Argo CD runs those as PostSync)", dig(t, ann, "helm.sh/hook"))
	}
	if got := fmt.Sprint(dig(t, ann, "helm.sh/hook-delete-policy")); !strings.Contains(got, "before-hook-creation") || !strings.Contains(got, "hook-succeeded") {
		t.Errorf("hook-delete-policy = %v", got)
	}
	pod := dig(t, job, "spec", "template", "spec").(map[string]any)
	if pod["serviceAccountName"] != "remedy-token-refresher" || pod["restartPolicy"] != "Never" {
		t.Errorf("hook pod = %v", pod)
	}
	// The same pod as the CronJob's: one definition.
	cj := mustRender(t, clusterValues()).find("CronJob", "remedy-token-refresh")
	cronPod := dig(t, cj, "spec", "jobTemplate", "spec", "template", "spec").(map[string]any)
	if toString(container(t, pod, "refresh")) != toString(container(t, cronPod, "refresh")) {
		t.Error("the hook and the CronJob must run the same container")
	}
}

func TestWithoutWriteThereIsNoRefresher(t *testing.T) {
	values := baseValues()
	set(values, "cluster.enabled", true)
	set(values, "networkPolicy.apiServer.cidrs", []any{"172.18.0.2/32"})
	d := mustRender(t, values)
	if d.find("Secret", "remedy-write-token") != nil || len(d.all("CronJob")) != 0 || len(d.all("Job")) != 0 {
		t.Fatal("no Secret, CronJob or Job expected without write")
	}
}

func TestTheRefresherMayOnlyReachDNSAndTheAPIServer(t *testing.T) {
	p := policy(t, mustRender(t, clusterValues()), "remedy-token-refresher")
	egress := rulesText(p, "egress")
	for _, want := range []string{"k8s-app: kube-dns", "cidr: 172.18.0.2/32", "port: 6443"} {
		if !strings.Contains(egress, want) {
			t.Errorf("the refresher's egress lacks %q:\n%s", want, egress)
		}
	}
	if strings.Contains(egress, "port: 443\n") || strings.Contains(egress, "port: 443}") || strings.Contains(egress, "0.0.0.0/0") {
		t.Errorf("the refresher must not reach the internet:\n%s", egress)
	}
	if dig(t, p, "spec", "ingress") != nil {
		t.Errorf("the refresher takes no connection: %v", dig(t, p, "spec", "ingress"))
	}
}

func TestTheArgoNamespaceMustNotBeAWriteNamespaceOrTheReleaseNamespace(t *testing.T) {
	values := clusterValues()
	set(values, "cluster.write.namespaces", []any{"demo", "argocd"})
	mustFail(t, values, "cluster.argoNamespace")

	values = clusterValues()
	set(values, "cluster.argoNamespace", "gitops")
	set(values, "cluster.write.namespaces", []any{"gitops"})
	mustFail(t, values, "cluster.argoNamespace")

	values = clusterValues()
	set(values, "cluster.argoNamespace", "remedy-system")
	mustFail(t, values, "cluster.argoNamespace")
	_, stderr, _ := render(t, values)
	if !strings.Contains(stderr, "release namespace") {
		t.Errorf("the message must name the release namespace:\n%s", stderr)
	}
}
