# Runbook: Remedy on a homelab k3s cluster, under Argo CD

**Status of this runbook.** It has **not** been run end to end. Success criterion 4 of the spec is not shown. The k3s half of the
NetworkPolicy spike (S3) is not measured (`docs/research/k8s-s3-networkpolicy.md`). The automated release ([`release.md`](release.md)) has made releases (`0.8.0` at the time of writing), but whether the three `ghcr.io/jaydee94` packages are public was not checked from a cluster, and they are
expected to be private until the maintainer makes them public.
The **install of the OCI chart under Argo CD is not proven on a cluster**, in either of the two forms below. Argo CD's documentation shows an OCI Helm source (a
registry URL without `oci://`, `chart:`, `targetRevision:`), but that source together with a `ref` source for
`deploy/cli-pin.yaml` is not shown there and was not tried; and the wrapper chart of section 6 (a chart whose only dependency is the OCI chart, built by Argo CD's repo
server) is equally untried. The chart was only rendered with Helm 4.3.0 (also from a local
package of the chart, as an OCI install would pull it), while Argo CD renders with its own bundled Helm. Argo CD's handling of the `PostSync` hook and of the Secret without data
(`remedy-write-token`) is unproven. What was proven is the same chart on kind with the real agent
(`docs/research/k8s-dummy-real-run.md`, `docs/research/k8s-runner-status-real-run.md`). Read every step as expected, not as tried.
The first install is cycle 6 of the hosts and skills series ([spec](../specs/2026-10-21-operation-6-design.md)); its drill is [`home-server-drill.md`](home-server-drill.md), and
the record of what it showed will say here what is proven.

This installs Remedy from a release of this repository, the way the maintainer runs it. The pieces are the chart
[`deploy/chart`](../../deploy/chart/README.md), the pinned CLI [`deploy/cli-pin.yaml`](../../deploy/cli-pin.yaml) and the
[spec](../specs/2026-10-06-kubernetes-deployment-design.md) behind both. Plan on about an hour, most of it waiting for images
and DNS.

## 1. What this installs

Remedy's control plane (a Deployment, `remedy-server`) and its runner (a StatefulSet, `remedy-runner`) in one namespace, `remedy`, from
the Helm chart of a release (an OCI artifact; the same chart is `deploy/chart` at the release tag), managed by an Argo CD Application, on k3s (Traefik, local-path
storage, kube-router for network policies). A SealedSecret holds the three application secrets and an Ingress serves the UI,
which is reachable on the LAN or over a VPN only.

There are two ways to give Argo CD the chart. **The wrapper chart** (sections 5 and 6) is for a GitOps repository whose ApplicationSet turns every directory
under `argocd/apps/*` into a one-source Application named after the directory, as the maintainer's home-server repository does: the directory `argocd/apps/remedy/` holds a small
chart whose only dependency is the OCI chart of the release. **The three-source Application** (the end of section 6) is the alternative for a cluster without such an ApplicationSet.

## 2. You need

- k3s with Argo CD and Sealed Secrets installed. Argo CD must be 2.6 or later (an Application with several sources and `ref`; the wrapper chart needs no `ref`).
- The `default` AppProject, or one that allows what the chart creates: a ClusterRole and a ClusterRoleBinding, and (with the write side on) Roles in
  the Remedy namespace, in each of the write namespaces and in the Argo CD namespace.
- `kubectl`, `kubeseal`, `openssl` and (for the wrapper chart, to render it before the pull request) `helm` on your machine, or on the server where `kubeseal` runs.
- A GitOps repository that Argo CD watches.
- DNS for the UI's host name. A TLS certificate for it is optional (cert-manager, or a Secret you provide); the home server serves plain HTTP on the LAN and over Tailscale.
- A Claude subscription, for the one-time login.
- With the write side on: the namespaces in which Remedy may act. They must exist.

`kubectl get endpoints kubernetes` (used below) prints a deprecation warning on newer Kubernetes versions. It is harmless;
`kubectl get endpointslices -l kubernetes.io/service-name=kubernetes` is the alternative.

## 3. The namespace

Remedy runs in its own namespace, `remedy`, with the restricted Pod Security Standard (every pod of the chart passes it). Two ways to get it:

- With an ApplicationSet that sets `CreateNamespace=true` (the home server's does), the namespace is made by the first sync. Put a `Namespace` manifest with the label
  `pod-security.kubernetes.io/enforce: restricted` into the wrapper's `templates/`. Whether Argo CD accepts that manifest next to `CreateNamespace` is **unproven**; the first sync answers it. If it
  refuses, drop the template and label the namespace by hand: `kubectl label namespace remedy pod-security.kubernetes.io/enforce=restricted`.
- Otherwise create it yourself, before anything else (the SealedSecret of the next section needs the namespace to exist):

```sh
kubectl create namespace remedy
kubectl label namespace remedy pod-security.kubernetes.io/enforce=restricted
```

The three-source Application sets the same label through `managedNamespaceMetadata`; with the namespace made here, that is only a safeguard.

## 4. The secret

Three values, generated once and kept: the master key seals the GitHub token **and the SSH credentials of every host**; losing it loses them all (you enter the logins again, nothing else
is lost). Keep a copy of the master key in a password manager.

```sh
printf 'admin-password=%s\nrunner-token=%s\nmaster-key=%s\n' "$(openssl rand -hex 12)" "$(openssl rand -hex 24)" "$(openssl rand -base64 32)" \
  | kubectl -n remedy create secret generic remedy-secrets --from-env-file=/dev/stdin --dry-run=client -o yaml \
  | kubeseal --format yaml > remedy-secrets.sealed.yaml
```

`printf` is a shell builtin, so the values are in no process's argument list, and the plain Secret is never written to disk: it
goes from `kubectl` to `kubeseal` through pipes. `kubeseal` asks the Sealed Secrets controller in your cluster for its
certificate, so `kubectl` must reach the cluster (on the home server `kubeseal` runs on the server itself, over SSH; do not copy the values out of that pipe). If `kubeseal` cannot find the controller, name it:
`--controller-name sealed-secrets --controller-namespace kube-system` (or whatever your install uses). The sealed file is bound to the namespace and the name, so seal it for `remedy` and `remedy-secrets`.

Commit `remedy-secrets.sealed.yaml` to the GitOps repository (it is safe there). With the wrapper chart it becomes the file `argocd/apps/remedy/templates/sealedsecret-remedy.yaml`, and the sync applies it. For the
three-source Application, the Application does not apply it: let the way your GitOps repository applies its other manifests do so, or apply it once with
`kubectl apply -f remedy-secrets.sealed.yaml`. Either way the namespace of section 3 must exist first. The controller then
makes the Secret `remedy-secrets` in `remedy`.

The pipeline does not show the admin password. Read it from the Secret once the controller has made it:
`kubectl -n remedy get secret remedy-secrets -o jsonpath='{.data.admin-password}' | base64 -d`.

## 5. The values

For the wrapper chart, two files in the GitOps repository, in `argocd/apps/remedy/`. The chart:

```yaml
# Chart.yaml
apiVersion: v2
name: remedy
description: Remedy, installed from a release of the OCI chart
type: application
version: 0.1.0                  # the wrapper's own version; not the release
dependencies:
  - name: remedy
    version: 0.8.0              # the release X.Y.Z of the OCI chart, exact (no ~ or ^); bump it to upgrade
    repository: oci://ghcr.io/jaydee94/charts
```

(`0.8.0` is the release at the time of writing: use the latest one when you install.) The values, **everything under the key `remedy:`** (the name of the dependency), with the pin of `deploy/cli-pin.yaml`
of the same release copied under it:

```yaml
# values.yaml
remedy:
  existingSecret:
    name: remedy-secrets

  server:
    persistence:
      size: 1Gi          # local-path: the claim is bound to one node; a node loss loses the database (see the backup chapter)

  runner:
    model: sonnet
    persistence:
      size: 1Gi
    cli:                  # copied from deploy/cli-pin.yaml at the release tag: a bump of the chart version and of the pin are one pull request
      version: "2.1.292"
      urlTemplate: "https://downloads.claude.ai/claude-code-releases/{version}/{platform}/claude"
      archive: none
      member: ""
      platforms:
        amd64: {name: "linux-x64", sha256: "..."}
        arm64: {name: "linux-arm64", sha256: "..."}

  ingress:
    enabled: true
    className: traefik
    host: remedy.homeserver       # your host name; plain HTTP here, add tls.secretName for a certificate
    annotations: {}

  cluster:
    enabled: true                 # the read identity
    write:
      enabled: false              # no actions on the cluster; see below to turn it on
    argoNamespace: argocd

  networkPolicy:
    enabled: true
    public:
      from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: kube-system     # where k3s's Traefik runs
    apiServer:
      cidrs: ["192.168.1.10/32"]  # the address behind kubernetes.default.svc: kubectl get endpoints kubernetes
      port: 6443
    hosts:                        # where the control plane may open SSH connections: single addresses (/32 or /128), never a range
      - {cidr: 192.168.1.20/32, port: 22}
    alertmanager:                 # an Alertmanager inside the cluster; read the namespace, labels and port from the cluster, do not guess
      namespace: monitoring
      podSelector: {app.kubernetes.io/name: vmalertmanager}
      port: 9093
```

The pinned CLI must be in the values: no image contains it. Copy it from `deploy/cli-pin.yaml` **at the release tag** you depend on (the file's `runner:` block goes under `remedy:`), with both platforms. Find the API server's address
with `kubectl get endpoints kubernetes -o jsonpath='{.subsets[0].addresses[0].ip}'`; on a k3s cluster with several servers list
each one. The chart refuses to render when `cluster.write.namespaces` contains the Remedy namespace or the Argo CD namespace, when `cluster.argoNamespace` is the Remedy namespace, when `server.env` sets a variable the chart sets itself (`REMEDY_ADDR`, `REMEDY_INTERNAL_ADDR`, `REMEDY_DB`, `REMEDY_K8S_*`), when
the network policies and `cluster.enabled` are on but `networkPolicy.apiServer.cidrs` is empty, and for a `networkPolicy.hosts` entry that is not a single address or whose port is not 1 to 65535, or an `alertmanager` without a namespace or with an empty `podSelector`
(`deploy/chart/README.md`). The two values open one egress rule each, on the control plane's policy only: the runner never opens an SSH connection.
The monitoring namespace's own isolation policy must admit the Remedy namespace as well, or the rule alone is not enough (on the home server that is a pull request to its monitoring app).

To act on the cluster later: set `cluster.write.enabled: true` and `cluster.write.namespaces: [apps]` (where actions are allowed; they must exist; never the Remedy namespace). That adds the write identity, the
refresher CronJob and the `PostSync` hook that section 6 describes. With `write.enabled: false` none of those objects are expected to render; check with
`helm template` before the pull request.

Render the wrapper before you open the pull request: `helm dependency build argocd/apps/remedy && helm template remedy argocd/apps/remedy --namespace remedy` (and `kubeconform -strict`
when it is installed). Without the wrapper, the same values go to a file `apps/remedy/values.yaml` **without** the `remedy:` key.

## 6. The Argo CD Application

**With the wrapper chart** there is nothing to write: the ApplicationSet makes a one-source Application `remedy` from the directory `argocd/apps/remedy/`, destination namespace `remedy`, with its own sync options.
Argo CD's repo server builds the dependency (`helm dependency build`) from `ghcr.io`, which needs the three packages to be public (`release.md`, one-time setup) and no repository entry. **Unproven**: that the repo
server builds an OCI dependency without one, that a `Namespace` template sits next to `CreateNamespace`, and that the hook below is handled in a wrapper. The fallback when the OCI dependency does not build is the
git form of the same chart: copy `deploy/chart` at the release tag into the wrapper directory (as `charts/remedy/`, so the dependency is local) and keep the same values. Add the
`ignoreDifferences` entry for `remedy-write-token` below to the ApplicationSet's template only if you turn the write side on.

**Without such an ApplicationSet**, use the **three-source Application**: the chart of a release from the OCI registry, the pinned CLI as a values file of this repository at the
release tag (through `ref`), and the values of the GitOps repository (the values file without the `remedy:` key):

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: remedy
  namespace: argocd
spec:
  project: default
  sources:
    - repoURL: ghcr.io/jaydee94/charts   # an OCI registry: no oci:// prefix and no path; the chart is oci://ghcr.io/jaydee94/charts/remedy
      chart: remedy
      targetRevision: 0.8.0               # the release version X.Y.Z (no v); bump it to upgrade
      helm:
        releaseName: remedy
        valueFiles:
          - $remedy/deploy/cli-pin.yaml
          - $gitops/apps/remedy/values.yaml
    - repoURL: https://github.com/Jaydee94/remedy.git
      targetRevision: v0.8.0              # the tag vX.Y.Z of the same release: the pin file of that version
      ref: remedy
    - repoURL: https://github.com/you/gitops.git      # your GitOps repository
      targetRevision: main
      ref: gitops
  destination:
    server: https://kubernetes.default.svc
    namespace: remedy
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
    managedNamespaceMetadata:
      labels:
        pod-security.kubernetes.io/enforce: restricted
  ignoreDifferences:
    # The token refresher writes the data of this Secret; the chart's Secret has no data, so a sync cannot overwrite it.
    # This entry is a safeguard against drift reports. RespectIgnoreDifferences is not needed: there is no data to apply.
    # Only with cluster.write.enabled: true; without it the Secret does not exist.
    - group: ""
      kind: Secret
      name: remedy-write-token
      jsonPointers:
        - /data
```

The form of the first source is the one in Argo CD's documentation for a public OCI Helm chart ("the oci:// syntax is not
included", `chart:` and `targetRevision:`, no `path`). A public registry needs no repository entry in Argo CD; this needs the
three packages to be public (`release.md`, one-time setup). The documentation also shows a newer `oci://` source with `path: .`
for OCI artifacts in general; this runbook does not use it. Verified in the documentation (2026-10-09): the form of the source
and the rule that a source with `ref` has no `chart`. Not verified anywhere: that Argo CD resolves `$remedy/deploy/cli-pin.yaml`
next to an OCI chart source (its documentation shows `ref` next to a classic Helm repository); the first sync is the proof.

The chart in the registry has `version` and `appVersion` equal to the release, and the chart's image tag defaults to its
`appVersion`: the OCI chart `0.8.0` pulls `remedy-server:0.8.0` and `remedy-runner:0.8.0`. `helm template` of a local package of
the chart with values like those of section 5 and `deploy/cli-pin.yaml` renders about 25 objects that `kubeconform -strict` accepts (measured with the
write side on, before the two network values of cycle 6 existed).

Installing from git at the tag works too, and is the alternative while the OCI install is unproven: replace the first source by

```yaml
    - repoURL: https://github.com/Jaydee94/remedy.git
      targetRevision: v0.8.0
      path: deploy/chart
      helm:
        releaseName: remedy
        valueFiles:
          - $remedy/deploy/cli-pin.yaml
          - $gitops/apps/remedy/values.yaml
```

This is expected to work because `Chart.yaml` at a release tag has the release version (the release commit writes it before the
tag is made); on a commit of `main` that is not a tag it says whatever the last release wrote, and the images of the chart's
`appVersion` exist only for a release.

At the first sync:

- The Secret `remedy-secrets` comes from the SealedSecret of section 4. The pods wait for it.
- The control plane (`remedy-server`) and the runner (`remedy-runner-0`) start. The runner's init container `install-cli`
  downloads the pinned CLI and checks its SHA-256, so the cluster needs outbound HTTPS (443) to the vendor's download host.
- With the write side on, the chart's hook, the Job `remedy-token-refresh-hook`, runs as an Argo CD `PostSync` job and fills the Secret
  `remedy-write-token` with the first write token, and the CronJob `remedy-token-refresh` mints the token again every 30 minutes, so its first run follows within half an hour.
  With `cluster.write.enabled: false` (the home server's first install) there is no hook, no CronJob and no write token; that the chart renders none of them is expected, check it with `helm template`.
- With the write side on, the control plane mounts `remedy-write-token` as an optional Secret that was still empty when its pod started. Observed once on
  kind (docs/research/k8s-dummy-real-run.md): an approved action failed with `open /var/run/remedy/write/token: no such file or
  directory` when it ran shortly after the install, and the same action passed about three minutes later. Expected kubelet
  behaviour, not measured: a running pod shows a new Secret file only after the kubelet's next sync, up to a minute or two. Reads
  are not affected. A restart of the control plane (`kubectl -n remedy rollout restart deployment/remedy-server`) starts a
  pod after the token exists; the dummy setup does that after a fresh install, and in one check (1 of 1) the failure did not recur.
- The runner's readiness and liveness probes use port 8082. The default-deny ingress policy lets them through only if the CNI
  exempts traffic that the node itself originates. Evidence for kindnet: the dummy's runner pod ran with 0 restarts and became Ready
  with `networkPolicy.enabled=true` and no ingress rule for port 8082 (one `503` readiness event at start, then Ready; one
  observation, recorded in `docs/research/k8s-runner-status-real-run.md`). k3s's kube-router was not measured. A runner pod
  that restarts in a loop with failing probes means the CNI blocks them. A value `networkPolicy.runner.probeFrom` does not
  exist yet; until it does, the workaround is `networkPolicy.enabled=false`.
- The control plane's own readiness and liveness probes (port 8080) pass the default policy only if the CNI lets the kubelet in:
  `networkPolicy.public.from` defaults to the `kube-system` namespace and does not include the node. This was never exercised on
  the kind dummy, because `dummy-values.yaml` opens `public.from` to `0.0.0.0/0`. On a CNI that blocks the node's traffic the
  server would never become Ready, Argo CD would never report it Healthy and (with the write side on) the `PostSync` hook would never run. k3s's
  kube-router is believed to let sources on the local node through, but that is unverified. The workaround that exists today: add
  the node range to `networkPolicy.public.from`, or set `networkPolicy.enabled=false`.

## 7. The one-time login

```sh
kubectl -n remedy exec -it remedy-runner-0 -c runner -- /opt/claude/claude
```

Type `/login`, open the URL in a browser, paste the code, `/exit`. (A form `claude auth login` is proposed in the follow-ups and unverified here: if it does not print the URL, use the form above.) The login is on the runner's volume and survives restarts of
the pod, but not the loss of the volume. Setup shows whether the runner is connected and what the CLI's own status command says about the login ("Logged in", "Not logged in" or "Login unknown"); the runner checks every minute while the login is not fine and every 10 minutes while it is. A login that has expired on the server side may still read as "Logged in" until a run fails with "Not logged in": that was not measured (`docs/research/k8s-runner-status-real-run.md`).

### Or: log in from the Setup page

Status: the control plane, the runner, the chart and the wizard on the Setup page are implemented (pull requests A and B of
`docs/plans/ui-login-relay.md`); the wizard was walked in a browser with a fake CLI only, and the real run with the real CLI is not made (record:
`docs/research/login-relay-real-run.md`, to be written), so the feature is not proven. The `kubectl exec` login above stays the way that works and is the
fallback.

There is nothing to turn on (since 2026-10-22 the login from the page has no switch and no chart value): whenever the runner is in subscription mode,
the default, the runner card offers "Log in from here" ("Log in again", with a confirmation, when the agent is logged in). It opens a wizard of four steps: start the sign-in, open the sign-in page, paste the
code, result. The runner starts the CLI's own `claude auth login`, the page shows a link to the sign-in page, you sign in at the vendor with your own
account and paste the one-time code into the page, and the runner hands it to the CLI. The `kubectl exec` command stays below the opener as "or do it in a
terminal" (and on the steps where it helps: before the start, for an address the page does not recognise, after a failure), and it is the only thing shown
when the login from the page is not offered (a runner in another mode). An older values file that still sets `loginRelay.enabled` renders without an
error; the value has no effect. The address and the code are kept in memory only, for one attempt of at most ten minutes (`docs/design.md` 2.7.1); the code is
write-only. While a login session is open no queued run is started, and a login is refused while a run is running (the admin API answers 409
`run_active`, and the page says that a run is running and that the sign-in can start when it has ended).

Two things to check yourself. A reverse proxy or an Ingress controller in front of the control plane may log request bodies, and the code is in the body
of a POST: make sure yours does not. And after a successful login the runner card in Setup can take up to about 15 seconds to turn green, because the
runner reports its status every 15 seconds. Read the vendor's terms of use before you offer this to anyone but yourself
(`docs/research/subscription-cli-usage.md`).

## 8. First use

Open the host name, sign in with the admin password of section 4 (the login page has no user name), then in Setup connect GitHub (the token is write-only) and
add repositories. Ask Remedy with "Read the cluster" to see the cluster tools work; each change waits for your approval.

Typing a secret into the UI over plain HTTP on the LAN sends it in clear. Enter an SSH key or a token only over an encrypted path: `kubectl -n remedy port-forward svc/remedy-server 8080:8080` and
`http://localhost:8080`, or Tailscale. The session cookie has no `Secure` flag on plain HTTP (see the last item of section 12).

To connect an Alertmanager and a host, follow [`alertmanager.md`](alertmanager.md) and [`home-server-drill.md`](home-server-drill.md). Turn automatic diagnosis off first (`diagnose_max_per_day` 0 in Setup) if the Alertmanager already
holds firing alerts: every young `critical` or `warning` alert would otherwise be diagnosed on its own, at the cost of subscription quota.

## 9. Upgrading

A new release: with the wrapper chart, bump the dependency's `version` in `Chart.yaml` and the `runner.cli` block in `values.yaml` to the new release's `deploy/cli-pin.yaml` in one pull request
(and run `helm dependency build` so that `Chart.lock` agrees, if your repository commits it). With the three-source form, bump `targetRevision` of the chart source to the new version `X.Y.Z` and of the source with `ref: remedy` to the
tag `vX.Y.Z` (with the git form of the chart, both sources of this repository take the tag). A release exists only when the
GitHub release `vX.Y.Z` does: the images and the chart are pushed before the tag is made, so a version with a tag has its images
and its chart. `release.yml` pushes `sha-<short>` and `edge` images from `main` too, but the chart pulls only `X.Y.Z`. The control plane restarts with `Recreate`: a minute without UI, runs in
flight end as lost, sessions end.

A new CLI version comes with a Renovate pull request on `deploy/cli-pin.yaml`. Check out that branch and run
`scripts/cli-checksums.sh <version>` with the checksums the vendor publishes. This prints the checksums, amd64 then arm64:

```sh
B=https://downloads.claude.ai/claude-code-releases
curl -fsSL $B/<version>/manifest.json | jq -r '.platforms["linux-x64"].checksum, .platforms["linux-arm64"].checksum'
```

Pass them as `--expect amd64=… --expect arm64=…`. To trust the manifest itself, download `manifest.json.sig` next to it and check
it with `gpg --verify` against the key at `https://downloads.claude.ai/keys/claude-code.asc`, fingerprint
`31DD DE24 DDFA B679 F42D 7BD2 BAA9 29FF 1A7E CACE`. Push, and merge when the `cli-pin` check is green.

The pin is read from `deploy/cli-pin.yaml` at the tag the Application points to (three-source form) or from the copy in the wrapper's values, so a new CLI reaches the cluster with the next
release and the next bump of the pin, not with the merge of the pin in this repository: a pin bump is a `chore` or `ci` commit, which releases nothing. To release it, give the
pull request the title `fix: pin the claude CLI X.Y.Z` (the squash title decides), then bump the chart version and the pin copy (this section).

A host that was added before cycle 6 and already has the `docker ps -a` line in its `sudoers` file: that command is now run as `docker ps -a --filter name=...`, which the old line does not allow. Apply
the new snippet from the Hosts page again (`home-server-drill.md`, section 5).

## 10. The release process (for the maintainer)

The full runbook is [`release.md`](release.md). In short:

1. A merge to `main` that contains a `feat` or a `fix` makes the release: images `X.Y.Z`, the OCI chart, the tag `vX.Y.Z`, the
   GitHub release with the changelog. Nobody tags by hand. The version comes from the commit messages.
2. The pull request title must be a conventional commit (`feat: ...`, `fix: ...`, `docs: ...`): the `pr-title` check enforces it,
   and the squash merge uses the title as the commit.
3. The changelog is `CHANGELOG.md` and the text of the GitHub release.
4. Make `remedy-server`, `remedy-runner` and `charts/remedy` public once (GitHub: profile, Packages, the package, Package settings,
   Change visibility). Otherwise the cluster cannot pull them.
5. If `images` or `chart` fail, re-run the failed jobs of that run, but only while `main` has not moved (a re-run computes the
   same version and overwrites the same tags). Never move or delete a tag.
6. To skip a release on purpose, merge only `docs`, `chore` or `ci` commits (they release nothing).

## 11. Backup

The database is the only state of the control plane (incidents, runs, the sealed GitHub token and host credentials); the runner's volume holds the
CLI login, which you can make again with section 7. A `local-path` volume lives on one node: copy it or snapshot it. The master key of section 4 is not in the database: keep it apart.

1. Find the node that runs the control plane before you stop it, because the volume is on that node:
   `kubectl -n remedy get pod -l app.kubernetes.io/component=server -o wide` (the PV's node affinity says the same).
2. Stop the control plane. With `selfHeal` Argo CD scales it back at once, so disable auto-sync on the Application first (Argo
   CD UI, App Details, Disable Auto-Sync, or `argocd app set remedy --sync-policy none`). If a parent application with `selfHeal`
   manages the Application, it must stop syncing it too. Then
   `kubectl -n remedy scale deployment/remedy-server --replicas=0`.
3. Find the volume's directory:
   `kubectl get pv "$(kubectl -n remedy get pvc remedy-data -o jsonpath='{.spec.volumeName}')" -o jsonpath='{.spec.local.path}{.spec.hostPath.path}'`
   (one of the two fields is set, depending on the provisioner).
4. As root on that node, copy `remedy.db` and `remedy.db-wal` (if it exists) out of that directory.
5. Scale back to one (`kubectl -n remedy scale deployment/remedy-server --replicas=1`) and enable auto-sync again.

Longhorn or Velero snapshots work the same way; Litestream is a separate plan.

## 12. Troubleshooting

- The runner pod stays in `Init`: the CLI download or its checksum. Read `kubectl -n remedy logs remedy-runner-0 -c install-cli`.
- The runner stays in `Init` right after a bump of the pin, with a checksum that differs: the pin's version and its checksums
  disagree. Run `scripts/cli-checksums.sh --check` on the pinned revision, and fix the pin as in section 9.
- Argo CD cannot pull the chart (an error from `ghcr.io` about authorization or a missing chart): expected cause, not seen: the
  package `charts/remedy` is still private, or the version is not a released one. Make it public (`release.md`) and check
  the version with `helm show chart oci://ghcr.io/jaydee94/charts/remedy --version X.Y.Z`. With the wrapper chart the error appears as a failed `helm dependency build` in the Application's conditions.
- Every run ends "Not logged in": do section 7.
- The UI loads but nothing live updates: a proxy buffers the streams. Traefik streams by default; with nginx set
  `nginx.ingress.kubernetes.io/proxy-buffering: "off"` in `ingress.annotations`.
- "the cluster cannot be reached with the read token" in the control plane log: the network policy. Check
  `networkPolicy.apiServer.cidrs` and `port` against `kubectl get endpoints kubernetes`.
- The Alertmanager line in Setup shows a connection error or a timeout, or a host's connection test cannot connect: the egress rule is missing or wrong (`networkPolicy.alertmanager`, `networkPolicy.hosts`), or the
  target's own policy does not admit the Remedy namespace.
- With the write side on, actions fail with `open /var/run/remedy/write/token: no such file or directory` right after the first sync: the kubelet has
  not shown the new Secret in the pod yet (expected kubelet behaviour: up to a minute or two). Wait, or restart the control plane
  (`kubectl -n remedy rollout restart deployment/remedy-server`).
- The runner pod restarts in a loop and its readiness or liveness probe fails (`kubectl -n remedy describe pod
  remedy-runner-0`): the CNI blocks the node's probes on port 8082. See section 6; `networkPolicy.runner.probeFrom` does not
  exist yet, so the workaround is `networkPolicy.enabled=false`.
- The control plane pod never becomes Ready, Argo CD stays `Progressing` (and with the write side on the hook never runs): the probes on port 8080 may be
  blocked by the policy (unverified on k3s; see section 6). Add the node range to `networkPolicy.public.from`, or set
  `networkPolicy.enabled=false` for the time of the check.
- With the write side on, actions fail with "the token file … is empty": the refresher has not run. Read `kubectl -n remedy get cronjob,job` and the
  logs of the last job.
- With the write side on, Argo CD shows the Secret `remedy-write-token` as out of sync: the `ignoreDifferences` of section 6 is missing.
  The refresher's data is not the chart's, so this is a drift report only; the entry silences it.
- The session cookie has no `Secure` flag: the control plane sets it when the request arrives with `X-Forwarded-Proto: https`,
  which Traefik sends when TLS ends there. Without `ingress.tls.secretName` the chart has no TLS block.
