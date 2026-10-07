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
