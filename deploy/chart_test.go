package deploy_test

import (
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
