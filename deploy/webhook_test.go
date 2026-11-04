package deploy_test

import (
	"strings"
	"testing"
)

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func countOf(s, sub string) int { return strings.Count(s, sub) }

func webhookValues() map[string]any {
	v := baseValues()
	set(v, "webhook.enabled", true)
	set(v, "webhook.from", []any{map[string]any{"namespaceSelector": map[string]any{"matchLabels": map[string]any{"kubernetes.io/metadata.name": "monitoring"}}}})
	return v
}

func TestTheDefaultInstallRendersNoWebhookObjectAndNoAddress(t *testing.T) {
	d := mustRender(t, baseValues())
	if d.find("Service", "remedy-server-webhook") != nil || d.find("Ingress", "remedy-webhook") != nil {
		t.Error("a webhook object exists in the default install")
	}
	srv := container(t, podSpec(t, d.find("Deployment", "remedy-server")), "server")
	if env(srv)["REMEDY_WEBHOOK_ADDR"] != nil {
		t.Error("REMEDY_WEBHOOK_ADDR is set in the default install")
	}
	if got := rulesText(policy(t, d, "remedy-server"), "ingress"); contains(got, "8082") {
		t.Errorf("the server policy opens the webhook port in the default install: %s", got)
	}
}

func TestEnablingTheWebhookNeedsPeersThatMayReachIt(t *testing.T) {
	v := webhookValues()
	set(v, "webhook.from", []any{})
	mustFail(t, v, "webhook.from")
	v = baseValues()
	set(v, "webhook.enabled", true)
	mustFail(t, v, "webhook.from")
}

func TestTheWebhookListenerHasItsOwnPortServiceAndPolicyRule(t *testing.T) {
	d := mustRender(t, webhookValues())
	srv := container(t, podSpec(t, d.find("Deployment", "remedy-server")), "server")
	if got := strings.TrimSpace(toString(env(srv)["REMEDY_WEBHOOK_ADDR"]["value"])); got != ":8082" {
		t.Errorf("REMEDY_WEBHOOK_ADDR = %q", got)
	}
	svc := d.find("Service", "remedy-server-webhook")
	if svc == nil || dig(t, svc, "spec", "type") != "ClusterIP" {
		t.Fatalf("service = %v", svc)
	}
	p := policy(t, d, "remedy-server")
	var found bool
	for _, r := range listOf(dig(t, p, "spec", "ingress")) {
		rule := r.(map[string]any)
		if equalStrings(rulePorts(rule), []string{"TCP/8082"}) {
			found = true
			if !contains(toString(rule["from"]), "monitoring") || contains(toString(rule["from"]), "runner") {
				t.Errorf("the webhook rule admits the wrong peers: %v", rule["from"])
			}
		} else if contains(toString(rule["ports"]), "8082") {
			t.Errorf("the webhook port shares a rule with another port: %v", rule)
		}
	}
	if !found {
		t.Errorf("no ingress rule for the webhook port: %s", rulesText(p, "ingress"))
	}
	// the other rules stay as they were: public from its peers, internal from the runner only
	if contains(toString(rulesText(p, "ingress")), "monitoring") && countOf(rulesText(p, "ingress"), "monitoring") != 1 {
		t.Error("the webhook peers were added to another rule")
	}
}

func TestTheWebhookPortMustBeFreeAndValid(t *testing.T) {
	for name, port := range map[string]any{"public port": 8080, "internal port": 8081, "zero": 0, "too high": 70000, "a string": "x"} {
		v := webhookValues()
		set(v, "webhook.port", port)
		t.Run(name, func(t *testing.T) { mustFail(t, v, "webhook.port") })
	}
	v := webhookValues()
	set(v, "webhook.port", 9090)
	d := mustRender(t, v)
	if got := strings.TrimSpace(toString(env(container(t, podSpec(t, d.find("Deployment", "remedy-server")), "server"))["REMEDY_WEBHOOK_ADDR"]["value"])); got != ":9090" {
		t.Errorf("REMEDY_WEBHOOK_ADDR = %q", got)
	}
}

func TestTheWebhookIngressRoutesOnlyTheWebhookPathToTheWebhookService(t *testing.T) {
	v := webhookValues()
	set(v, "webhook.ingress.enabled", true)
	mustFail(t, v, "webhook.ingress.host")
	set(v, "webhook.ingress.host", "hooks.example.com")
	set(v, "webhook.ingress.className", "nginx")
	set(v, "webhook.ingress.tls.secretName", "hooks-tls")
	d := mustRender(t, v)
	ing := d.find("Ingress", "remedy-webhook")
	if ing == nil {
		t.Fatal("no webhook Ingress")
	}
	text := toString(dig(t, ing, "spec"))
	if !contains(text, "/webhook/v1/") || !contains(text, "remedy-server-webhook") || contains(text, "name:remedy-server ") || contains(text, "8080") {
		t.Errorf("ingress spec = %s", text)
	}
	paths := listOf(dig(t, ing, "spec", "rules", 0, "http", "paths"))
	if len(paths) != 1 || dig(t, paths[0], "path") != "/webhook/v1/" {
		t.Errorf("paths = %v", paths)
	}
	// the UI Ingress is a different object and is unchanged
	off := webhookValues()
	set(off, "webhook.ingress.enabled", false)
	if mustRender(t, off).find("Ingress", "remedy-webhook") != nil {
		t.Error("the Ingress exists while it is off")
	}
	// an Ingress without the listener would point at nothing
	bad := baseValues()
	set(bad, "webhook.ingress.enabled", true)
	set(bad, "webhook.ingress.host", "hooks.example.com")
	mustFail(t, bad, "webhook.enabled")
}
