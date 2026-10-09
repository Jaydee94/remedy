# Runbook: Remedy on a homelab k3s cluster, under Argo CD

This installs Remedy from a tag of this repository, the way the maintainer runs it. The pieces are the chart
[`deploy/chart`](../../deploy/chart/README.md), the pinned CLI [`deploy/cli-pin.yaml`](../../deploy/cli-pin.yaml) and the
[spec](../specs/2026-10-06-kubernetes-deployment-design.md) behind both. Plan on about an hour, most of it waiting for images
and DNS.

## 1. What this installs

Remedy's control plane (a Deployment, `remedy-server`) and its runner (a StatefulSet, `remedy-runner`) in one namespace, from
the Helm chart `deploy/chart` of a tag of this repository, managed by an Argo CD Application, on k3s (Traefik, local-path
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
each one. The chart refuses to render when `cluster.write.namespaces` contains `remedy-system`, and when the network policies and
`cluster.enabled` are on but `networkPolicy.apiServer.cidrs` is empty.

## 6. The Argo CD Application

Two sources of this repository (the chart, and the pinned CLI as a values file through `ref`) and one of the GitOps repository:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: remedy
  namespace: argocd
spec:
  project: default
  sources:
    - repoURL: https://github.com/Jaydee94/remedy.git
      targetRevision: v0.1.0              # the release; bump it to upgrade
      path: deploy/chart
      helm:
        releaseName: remedy
        valueFiles:
          - $remedy/deploy/cli-pin.yaml
          - $gitops/apps/remedy/values.yaml
    - repoURL: https://github.com/Jaydee94/remedy.git
      targetRevision: v0.1.0
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

At the first sync:

- The Secret `remedy-secrets` comes from the SealedSecret of section 4. The pods wait for it.
- The control plane (`remedy-server`) and the runner (`remedy-runner-0`) start. The runner's init container `install-cli`
  downloads the pinned CLI and checks its SHA-256, so the cluster needs outbound HTTPS (443) to the vendor's download host.
- The chart's hook, the Job `remedy-token-refresh-hook`, runs as an Argo CD `PostSync` job and fills the Secret
  `remedy-write-token` with the first write token.
- The CronJob `remedy-token-refresh` mints the token again every 30 minutes, so its first run follows within half an hour.

## 7. The one-time login

```sh
kubectl -n remedy-system exec -it remedy-runner-0 -c runner -- /opt/claude/claude
```

Type `/login`, open the URL in a browser, paste the code, `/exit`. The login is on the runner's volume and survives restarts of
the pod, but not the loss of the volume. (After plan K-6, Setup shows whether the runner is logged in.)

## 8. First use

Open the host name, sign in with the admin password of section 4 (the login page has no user name), then in Setup connect GitHub (the token is write-only) and
add repositories. Ask Remedy with "Read the cluster" to see the cluster tools work; each change waits for your approval.

## 9. Upgrading

A new release: bump `targetRevision` of both sources of this repository in the Application (the chart, and the one with `ref:
remedy`) to the new tag. Do it after the images of that tag exist: `images.yml` pushes only from `main` and from `v*` tags, so
no other branch or tag has images the chart could pull. The control plane restarts with `Recreate`: a minute without UI, runs in
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
tag: release it (next section), then bump both sources.

## 10. The release process (for the maintainer)

1. Change `version` and `appVersion` of `deploy/chart/Chart.yaml` to `X.Y.Z` in a pull request and merge it.
2. Tag `vX.Y.Z` on the merge commit and push the tag.
3. `images.yml` checks the tag against the chart (`scripts/check-release.sh`) and pushes `X.Y.Z` for both images.
4. After the first release make both packages public once in GitHub (profile, Packages, `remedy-server` and `remedy-runner`,
   Package settings, Change visibility, Public). Otherwise the cluster cannot pull them.

The images are built per image in a matrix with fail-fast off, so a tag can publish one image and fail the other. Re-run the
failed job of that workflow run; do not make a new tag.

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
- Every run ends "Not logged in": do section 7.
- The UI loads but nothing live updates: a proxy buffers the streams. Traefik streams by default; with nginx set
  `nginx.ingress.kubernetes.io/proxy-buffering: "off"` in `ingress.annotations`.
- "the cluster cannot be reached with the read token" in the control plane log: the network policy. Check
  `networkPolicy.apiServer.cidrs` and `port` against `kubectl get endpoints kubernetes`.
- Actions fail with "the token file … is empty": the refresher has not run. Read `kubectl -n remedy-system get cronjob,job` and the
  logs of the last job.
- Argo CD shows the Secret `remedy-write-token` as out of sync: the `ignoreDifferences` of section 6 is missing.
  The refresher's data is not the chart's, so this is a drift report only; the entry silences it.
- The session cookie has no `Secure` flag: the control plane sets it when the request arrives with `X-Forwarded-Proto: https`,
  which Traefik sends when TLS ends there. Without `ingress.tls.secretName` the chart has no TLS block.
