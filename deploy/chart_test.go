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
