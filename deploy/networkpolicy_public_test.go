package deploy_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ingressNginx is the peer of an install behind the ingress-nginx controller.
func ingressNginx() []any {
	return []any{map[string]any{"namespaceSelector": map[string]any{"matchLabels": map[string]any{
		"kubernetes.io/metadata.name": "ingress-nginx",
	}}}}
}

// stockValues are the values of an install on a stock cluster and nothing more: the Secrets, the bundled database and
// the one thing the chart cannot guess, who may reach the UI.
func stockValues() map[string]any {
	return map[string]any{
		"existingSecret": map[string]any{"name": "remedy-secrets"},
		"postgres":       map[string]any{"existingSecret": "remedy-postgres"},
		"networkPolicy":  map[string]any{"public": map[string]any{"from": ingressNginx()}},
	}
}

func TestTheChartNeedsToKnowWhoMayReachTheUI(t *testing.T) {
	values := stockValues()
	delete(values["networkPolicy"].(map[string]any), "public")
	for _, want := range []string{"networkPolicy.public.from", "kube-system", "ingress-nginx", "ipBlock"} {
		mustFail(t, values, want)
	}
}

func TestAnEmptyListOfPeersIsRefusedToo(t *testing.T) {
	values := stockValues()
	set(values, "networkPolicy.public.from", []any{})
	mustFail(t, values, "networkPolicy.public.from")
}

func TestPoliciesOffNeedNoPublicPeers(t *testing.T) {
	values := stockValues()
	delete(values["networkPolicy"].(map[string]any), "public")
	set(values, "networkPolicy.enabled", false)
	d := mustRender(t, values)
	if len(d.all("NetworkPolicy")) != 0 {
		t.Fatal("no NetworkPolicy may be rendered when networkPolicy.enabled is false")
	}
}

// Spec section 7, point 4: the chart renders with only the values of a stock cluster, and nothing in what it renders
// is a value that only the maintainer's lab has.
func TestTheChartRendersWithOnlyTheValuesOfAStockCluster(t *testing.T) {
	d := mustRender(t, stockValues())

	public := toString(dig(t, policy(t, d, "remedy-server"), "spec", "ingress").([]any)[0])
	if !strings.Contains(public, "kubernetes.io/metadata.name: ingress-nginx") || strings.Contains(public, "kube-system") {
		t.Errorf("the public rule must name the configured peer and no other:\n%s", public)
	}

	deny, err := os.ReadFile(filepath.Join("..", "scripts", "lab-values.deny"))
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := yaml.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(deny), "\n") {
		if line = strings.TrimSpace(line); line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if re := regexp.MustCompile("(?i)" + line); re.Match(rendered) {
			t.Errorf("the render of a stock install contains %q, a value of the maintainer's lab", line)
		}
	}
}
