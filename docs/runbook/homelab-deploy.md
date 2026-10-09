# Runbook: Remedy on a homelab k3s cluster, under Argo CD

**Status of this runbook.** It has **not** been run end to end. Success criterion 4 of the spec is not shown. The k3s half of the
NetworkPolicy spike (S3) is not measured (`docs/research/k8s-s3-networkpolicy.md`). Nothing has been released yet: no tag, no
image and no chart in the registry. The automated release ([`release.md`](release.md)) is implemented and its first run is
expected on the first merge to `main`; the packages are expected to be private until the maintainer makes them public. The
**install of the OCI chart under Argo CD is not proven on a cluster**: Argo CD's documentation shows an OCI Helm source (a
registry URL without `oci://`, `chart:`, `targetRevision:`), but that source together with a `ref` source for
`deploy/cli-pin.yaml` is not shown there and was not tried. The chart was only rendered with Helm 4.3.0 (also from a local
package of the chart, as an OCI install would pull it), while Argo CD renders with its own bundled Helm. Argo CD's handling of the `PostSync` hook and of the Secret without data
(`remedy-write-token`) is unproven. What was proven is the same chart on kind with the real agent
(`docs/research/k8s-dummy-real-run.md`, `docs/research/k8s-runner-status-real-run.md`). Read every step as expected, not as tried.

This installs Remedy from a release of this repository, the way the maintainer runs it. The pieces are the chart
[`deploy/chart`](../../deploy/chart/README.md), the pinned CLI [`deploy/cli-pin.yaml`](../../deploy/cli-pin.yaml) and the
[spec](../specs/2026-10-06-kubernetes-deployment-design.md) behind both. Plan on about an hour, most of it waiting for images
and DNS.

## 1. What this installs

Remedy's control plane (a Deployment, `remedy-server`) and its runner (a StatefulSet, `remedy-runner`) in one namespace, from
the Helm chart of a release (an OCI artifact; the same chart is `deploy/chart` at the release tag), managed by an Argo CD Application, on k3s (Traefik, local-path
storage, kube-router for network policies). A SealedSecret holds the three application secrets and an Ingress serves the UI,
which is reachable on the LAN or over a VPN only.

## 2. You need

- k3s with Argo CD and Sealed Secrets installed. Argo CD must be 2.6 or later (an Application with several sources and `ref`).
- The `default` AppProject, or one that allows what the chart creates: a ClusterRole and a ClusterRoleBinding, and Roles in
  `remedy-system`, in each of the write namespaces and in the Argo CD namespace.
- `kubectl`, `kubeseal` and `openssl` on your machine.
- A GitOps repository that Argo CD watches.
- DNS for the UI's host name, and a TLS certificate for it (cert-manager, or a Secret you provide).
- A Claude subscription, for the one-time login.
- The namespaces in which Remedy may act. They must exist.

`kubectl get endpoints kubernetes` (used below) prints a deprecation warning on newer Kubernetes versions. It is harmless;
`kubectl get endpointslices -l kubernetes.io/service-name=kubernetes` is the alternative.

## 3. The namespace

Remedy runs in its own namespace, with the restricted Pod Security Standard (every pod of the chart passes it). Create it
yourself, before anything else: the SealedSecret of the next section needs the namespace to exist.

```sh
kubectl create namespace remedy-system
kubectl label namespace remedy-system pod-security.kubernetes.io/enforce=restricted
```

The Application in section 6 sets the same label through `managedNamespaceMetadata`; with the namespace made here, that is only
a safeguard.

## 4. The secret

Three values, generated once and kept: the master key seals the GitHub token, losing it loses that token.

```sh
printf 'admin-password=%s\nrunner-token=%s\nmaster-key=%s\n' "$(openssl rand -hex 12)" "$(openssl rand -hex 24)" "$(openssl rand -base64 32)" \
  | kubectl -n remedy-system create secret generic remedy-secrets --from-env-file=/dev/stdin --dry-run=client -o yaml \
  | kubeseal --format yaml > remedy-secrets.sealed.yaml
```

`printf` is a shell builtin, so the values are in no process's argument list, and the plain Secret is never written to disk: it
goes from `kubectl` to `kubeseal` through pipes. `kubeseal` asks the Sealed Secrets controller in your cluster for its
certificate, so `kubectl` must reach the cluster. If `kubeseal` cannot find the controller, name it:
`--controller-name sealed-secrets --controller-namespace kube-system` (or whatever your install uses).

Commit `remedy-secrets.sealed.yaml` to the GitOps repository (it is safe there). The Application in section 6 does not apply
it: let the way your GitOps repository applies its other manifests do so, or apply it once with
`kubectl apply -f remedy-secrets.sealed.yaml`. Either way the namespace of section 3 must exist first. The controller then
makes the Secret `remedy-secrets` in `remedy-system`.

The pipeline does not show the admin password. Read it from the Secret once the controller has made it:
`kubectl -n remedy-system get secret remedy-secrets -o jsonpath='{.data.admin-password}' | base64 -d`.

## 5. The values

A file in the GitOps repository, `apps/remedy/values.yaml`:

```yaml
existingSecret:
  name: remedy-secrets

server:
  persistence:
    size: 1Gi          # local-path: the claim is bound to one node; a node loss loses the database (see the backup chapter)

runner:
  model: sonnet
  persistence:
    size: 1Gi

ingress:
  enabled: true
  className: traefik
  host: remedy.home.example     # your host name
  tls:
    secretName: remedy-tls      # a Secret with the certificate, or let cert-manager make it (annotations)
  annotations: {}

cluster:
  enabled: true
  write:
    enabled: true
    namespaces: [apps]          # where actions are allowed; they must exist; never remedy-system
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
```

The pinned CLI is not here: it comes from `deploy/cli-pin.yaml` of this repository (next section). Find the API server's address
with `kubectl get endpoints kubernetes -o jsonpath='{.subsets[0].addresses[0].ip}'`; on a k3s cluster with several servers list
each one. The chart refuses to render when `cluster.write.namespaces` contains `remedy-system` or the Argo CD namespace, when `cluster.argoNamespace` is `remedy-system`, when `server.env` sets a variable the chart sets itself (`REMEDY_ADDR`, `REMEDY_INTERNAL_ADDR`, `REMEDY_DB`, `REMEDY_K8S_*`), and when the network policies and
`cluster.enabled` are on but `networkPolicy.apiServer.cidrs` is empty.

## 6. The Argo CD Application

Three sources: the chart of a release from the OCI registry, the pinned CLI as a values file of this repository at the
release tag (through `ref`), and the values of the GitOps repository:

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
      targetRevision: 0.1.0               # the release version X.Y.Z (no v); the first release is expected to be 0.1.0; bump it to upgrade
      helm:
        releaseName: remedy
        valueFiles:
          - $remedy/deploy/cli-pin.yaml
          - $gitops/apps/remedy/values.yaml
    - repoURL: https://github.com/Jaydee94/remedy.git
      targetRevision: v0.1.0              # the tag vX.Y.Z of the same release: the pin file of that version
      ref: remedy
    - repoURL: https://github.com/you/gitops.git      # your GitOps repository
      targetRevision: main
      ref: gitops
  destination:
    server: https://kubernetes.default.svc
    namespace: remedy-system
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
`appVersion`: the OCI chart `0.1.0` pulls `remedy-server:0.1.0` and `remedy-runner:0.1.0`. `helm template` of a local package of
the chart with the values of section 5 and `deploy/cli-pin.yaml` renders 25 objects that `kubeconform -strict` accepts, with
those two images (measured).

Installing from git at the tag works too, and is the alternative while the OCI install is unproven: replace the first source by

```yaml
    - repoURL: https://github.com/Jaydee94/remedy.git
      targetRevision: v0.1.0
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
- The chart's hook, the Job `remedy-token-refresh-hook`, runs as an Argo CD `PostSync` job and fills the Secret
  `remedy-write-token` with the first write token.
- The CronJob `remedy-token-refresh` mints the token again every 30 minutes, so its first run follows within half an hour.
- The control plane mounts `remedy-write-token` as an optional Secret that was still empty when its pod started. Observed once on
  kind (docs/research/k8s-dummy-real-run.md): an approved action failed with `open /var/run/remedy/write/token: no such file or
  directory` when it ran shortly after the install, and the same action passed about three minutes later. Expected kubelet
  behaviour, not measured: a running pod shows a new Secret file only after the kubelet's next sync, up to a minute or two. Reads
  are not affected. A restart of the control plane (`kubectl -n remedy-system rollout restart deployment/remedy-server`) starts a
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
  server would never become Ready, Argo CD would never report it Healthy and the `PostSync` hook would never run. k3s's
  kube-router is believed to let sources on the local node through, but that is unverified. The workaround that exists today: add
  the node range to `networkPolicy.public.from`, or set `networkPolicy.enabled=false`.

## 7. The one-time login

```sh
kubectl -n remedy-system exec -it remedy-runner-0 -c runner -- /opt/claude/claude
```

Type `/login`, open the URL in a browser, paste the code, `/exit`. The login is on the runner's volume and survives restarts of
the pod, but not the loss of the volume. Setup shows whether the runner is connected and what the CLI's own status command says about the login ("Logged in", "Not logged in" or "Login unknown"); the runner checks every minute while the login is not fine and every 10 minutes while it is. A login that has expired on the server side may still read as "Logged in" until a run fails with "Not logged in": that was not measured (`docs/research/k8s-runner-status-real-run.md`).

## 8. First use

Open the host name, sign in with the admin password of section 4 (the login page has no user name), then in Setup connect GitHub (the token is write-only) and
add repositories. Ask Remedy with "Read the cluster" to see the cluster tools work; each change waits for your approval.

## 9. Upgrading

A new release: bump `targetRevision` of the chart source to the new version `X.Y.Z` and of the source with `ref: remedy` to the
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

The pin is read from `deploy/cli-pin.yaml` at the tag the Application points to, so a new CLI reaches the cluster with the next
release, not with the merge of the pin: a pin bump is a `chore` or `ci` commit, which releases nothing. To release it, give the
pull request the title `fix: pin the claude CLI X.Y.Z` (the squash title decides), then bump both sources (next section).

## 10. The release process (for the maintainer)

The full runbook is [`release.md`](release.md). In short:

1. A merge to `main` that contains a `feat` or a `fix` makes the release: images `X.Y.Z`, the OCI chart, the tag `vX.Y.Z`, the
   GitHub release with the changelog. Nobody tags by hand. The version comes from the commit messages.
2. The pull request title must be a conventional commit (`feat: ...`, `fix: ...`, `docs: ...`): the `pr-title` check enforces it,
   and the squash merge uses the title as the commit.
3. The changelog is `CHANGELOG.md` and the text of the GitHub release.
4. After the first release make `remedy-server`, `remedy-runner` and `charts/remedy` public once (GitHub: profile, Packages, the
   package, Package settings, Change visibility). Otherwise the cluster cannot pull them.
5. If `images` or `chart` fail, re-run the failed jobs of that run, but only while `main` has not moved (a re-run computes the
   same version and overwrites the same tags). Never move or delete a tag.
6. To skip a release on purpose, merge only `docs`, `chore` or `ci` commits (they release nothing).

## 11. Backup

The database is the only state of the control plane (incidents, runs, the sealed GitHub token); the runner's volume holds the
CLI login, which you can make again with section 7. A `local-path` volume lives on one node: copy it or snapshot it.

1. Find the node that runs the control plane before you stop it, because the volume is on that node:
   `kubectl -n remedy-system get pod -l app.kubernetes.io/component=server -o wide` (the PV's node affinity says the same).
2. Stop the control plane. With `selfHeal` Argo CD scales it back at once, so disable auto-sync on the Application first (Argo
   CD UI, App Details, Disable Auto-Sync, or `argocd app set remedy --sync-policy none`). If a parent application with `selfHeal`
   manages the Application, it must stop syncing it too. Then
   `kubectl -n remedy-system scale deployment/remedy-server --replicas=0`.
3. Find the volume's directory:
   `kubectl get pv "$(kubectl -n remedy-system get pvc remedy-data -o jsonpath='{.spec.volumeName}')" -o jsonpath='{.spec.local.path}{.spec.hostPath.path}'`
   (one of the two fields is set, depending on the provisioner).
4. As root on that node, copy `remedy.db` and `remedy.db-wal` (if it exists) out of that directory.
5. Scale back to one (`kubectl -n remedy-system scale deployment/remedy-server --replicas=1`) and enable auto-sync again.

Longhorn or Velero snapshots work the same way; Litestream is a separate plan.

## 12. Troubleshooting

- The runner pod stays in `Init`: the CLI download or its checksum. Read `kubectl -n remedy-system logs remedy-runner-0 -c install-cli`.
- The runner stays in `Init` right after a bump of the pin, with a checksum that differs: the pin's version and its checksums
  disagree. Run `scripts/cli-checksums.sh --check` on the pinned revision, and fix the pin as in section 9.
- Argo CD cannot pull the chart (an error from `ghcr.io` about authorization or a missing chart): expected cause, not seen: the
  package `charts/remedy` is still private, or `targetRevision` is not a released version. Make it public (`release.md`) and check
  the version with `helm show chart oci://ghcr.io/jaydee94/charts/remedy --version X.Y.Z`.
- Every run ends "Not logged in": do section 7.
- The UI loads but nothing live updates: a proxy buffers the streams. Traefik streams by default; with nginx set
  `nginx.ingress.kubernetes.io/proxy-buffering: "off"` in `ingress.annotations`.
- "the cluster cannot be reached with the read token" in the control plane log: the network policy. Check
  `networkPolicy.apiServer.cidrs` and `port` against `kubectl get endpoints kubernetes`.
- Actions fail with `open /var/run/remedy/write/token: no such file or directory` right after the first sync: the kubelet has
  not shown the new Secret in the pod yet (expected kubelet behaviour: up to a minute or two). Wait, or restart the control plane
  (`kubectl -n remedy-system rollout restart deployment/remedy-server`).
- The runner pod restarts in a loop and its readiness or liveness probe fails (`kubectl -n remedy-system describe pod
  remedy-runner-0`): the CNI blocks the node's probes on port 8082. See section 6; `networkPolicy.runner.probeFrom` does not
  exist yet, so the workaround is `networkPolicy.enabled=false`.
- The control plane pod never becomes Ready, Argo CD stays `Progressing` and the hook never runs: the probes on port 8080 may be
  blocked by the policy (unverified on k3s; see section 6). Add the node range to `networkPolicy.public.from`, or set
  `networkPolicy.enabled=false`.
- Actions fail with "the token file … is empty": the refresher has not run. Read `kubectl -n remedy-system get cronjob,job` and the
  logs of the last job.
- Argo CD shows the Secret `remedy-write-token` as out of sync: the `ignoreDifferences` of section 6 is missing.
  The refresher's data is not the chart's, so this is a drift report only; the entry silences it.
- The session cookie has no `Secure` flag: the control plane sets it when the request arrives with `X-Forwarded-Proto: https`,
  which Traefik sends when TLS ends there. Without `ingress.tls.secretName` the chart has no TLS block.
