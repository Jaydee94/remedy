# The Remedy chart

Runs the control plane and the runner in one namespace. One release per namespace: the resource names are fixed.
Spec: [`docs/specs/2026-10-06-kubernetes-deployment-design.md`](../../docs/specs/2026-10-06-kubernetes-deployment-design.md).

## What you need first

- A Secret with three keys (`admin-password`, `runner-token`, `master-key`; the names are values). The chart never creates
  it. `openssl rand -hex 12` (password), `openssl rand -hex 24` (runner token), `openssl rand -base64 32` (master key).
- The pinned CLI: `runner.cli.version`, `runner.cli.urlTemplate`, and the platform names and SHA-256 per architecture
  (`docs/research/k8s-s4-cli-install.md` says where they come from). No image contains the CLI.

## What it renders

| Object | Name | Note |
|---|---|---|
| Deployment | `remedy-server` | one replica, `Recreate`, ports 8080 (public) and 8081 (internal) |
| Services | `remedy-server`, `remedy-server-internal` | only the first may be exposed |
| PVC | `remedy-data` | kept on uninstall; `server.persistence.existingClaim` uses your own |
| StatefulSet | `remedy-runner` | init container `install-cli`; no Kubernetes token |
| Ingress | `remedy` | optional |
| NetworkPolicies | `remedy-server`, `remedy-runner`, `remedy-token-refresher` (only with `cluster.write.enabled`: DNS and the API server, nothing else) | on by default; `networkPolicy.*` (`hosts` and `alertmanager` add egress to the control plane only) |

With `cluster.enabled` the control plane runs as the read account (a projected token) and the chart creates a read-only ClusterRole.
With `cluster.write.enabled` a CronJob (default schedule `*/30 * * * *`, `cluster.write.tokenRefresh.schedule`) and a Job that runs as a post-install and post-upgrade
hook mint a two-hour token of the write account into the Secret
`remedy-write-token`, which the pod mounts; Roles are created in each of `cluster.write.namespaces` (which must exist) and in
`cluster.argoNamespace`. `cluster.write.namespaces` can never contain the release namespace or `cluster.argoNamespace`, and `cluster.argoNamespace` can never be the release namespace (the chart refuses to render). If the refresher stops, actions
fail closed after the token expires. Under Argo CD add an `ignoreDifferences` for the Secret's `/data`.

## The login from the Setup page

The Setup page can sign the runner in to the vendor's CLI. There is no value for it: the runner card offers
"Log in from here", and the runner runs the login when it is in subscription mode (the default). A runner in another
mode starts no relay; until it reports its mode (cycle 7b) the page still offers the button, and a start is refused as
"runner not connected" (`runner_offline`). It handles the
CLI's login material: the page shows the sign-in address that the CLI prints and takes the one-time code back, and the
runner hands both to the CLI's own login command. The code is write-only, neither value is stored or logged, and the CLI's
login files are never read. It ships enabled, so re-read the vendor's terms of use before you offer this chart to anyone
but yourself. It changes no NetworkPolicy. The `kubectl exec` login stays the fallback. The real run with the real CLI is
not made, so the login from the page is not proven. Until 2026-10-22 it was behind the value `loginRelay.enabled`; that
value is gone, and a values file that still sets it renders without an error (Helm ignores an unknown value) and has no
effect.

## NetworkPolicies: what the address rules mean

The rules are by address **and port**. The control plane reaches the API server through `networkPolicy.apiServer.cidrs`
and `port` (6443 on k3s and kind; fill them from `kubectl get endpoints kubernetes`, not from the service address). The
runner may reach TCP 443 on public addresses only, so an API server on 6443 is out of its reach by port. An API server on
443 with a public address would not be protected by the excepted private ranges; the usual homelab node addresses
(`192.168.0.0/16`, `10.0.0.0/8`) are inside them. Pods in the cluster have addresses in `10.0.0.0/8` too: allow traffic to a
pod with a pod or namespace selector, never with an address.

Two more values open egress of the **control plane only** (the `remedy-server` policy; the runner and the token refresher
never get them). `networkPolicy.hosts` is a list of `{cidr, port}`, one SSH rule per entry for the host tools: the `cidr`
must be a single address (`/32` for IPv4, `/128` for IPv6), the port an integer from 1 to 65535, and an entry may not be an
address of `networkPolicy.apiServer.cidrs`. `networkPolicy.alertmanager` is `{namespace, podSelector, port}`, one rule to the
pods that match `podSelector` in `namespace` (`port` defaults to 9093); `namespace` and a non-empty `podSelector` are
required, because an empty selector would admit every pod of the namespace. There is no free list of extra rules on
purpose: one typo (`0.0.0.0/0`, no port) would open all egress of the pod that holds the credentials. Both default to empty
and then render nothing; the render fails for a value that breaks a rule, and ignores both when `networkPolicy.enabled` is false.

```yaml
networkPolicy:
  hosts:
    - {cidr: 192.168.178.118/32, port: 22}
  alertmanager:
    namespace: monitoring
    podSelector: {app.kubernetes.io/name: vmalertmanager}
```

## Install from the OCI registry

A release publishes the chart as an OCI artifact (expected from the first release; none exists yet):

```sh
helm install remedy oci://ghcr.io/jaydee94/charts/remedy --version X.Y.Z -n remedy-system \
  -f deploy/cli-pin.yaml -f my-values.yaml
```

`deploy/cli-pin.yaml` is the pin file of the repository at the tag `vX.Y.Z` (the chart does not contain it). The chart in the
registry is packaged by the release with `version` and `appVersion` equal to the release, so its default image tag pulls the
images `X.Y.Z`. The package `charts/remedy` must be public. Under Argo CD see the runbook; the OCI install is not yet proven on a
cluster. The release is described in [`docs/runbook/release.md`](../../docs/runbook/release.md).

## Checking it

`make chart-check` lints, renders, validates (kubeconform) and runs the render tests in `deploy/`.

## Installing it in a homelab

[`docs/runbook/homelab-deploy.md`](../../docs/runbook/homelab-deploy.md): the namespace, a SealedSecret, the values, an Argo CD
Application with two sources of this repository (the chart, and the pinned CLI in `deploy/cli-pin.yaml`), the one-time login and
upgrades.
