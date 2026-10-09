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
| NetworkPolicies | `remedy-server`, `remedy-runner` | on by default; `networkPolicy.*` |

With `cluster.enabled` the control plane runs as the read account (a projected token) and the chart creates a read-only ClusterRole.
With `cluster.write.enabled` a CronJob (default schedule `*/30 * * * *`, `cluster.write.tokenRefresh.schedule`) and a Job that runs as a post-install and post-upgrade
hook mint a two-hour token of the write account into the Secret
`remedy-write-token`, which the pod mounts; Roles are created in each of `cluster.write.namespaces` (which must exist) and in
`cluster.argoNamespace`. `cluster.write.namespaces` can never contain the release namespace. If the refresher stops, actions
fail closed after the token expires. Under Argo CD add an `ignoreDifferences` for the Secret's `/data`.

## NetworkPolicies: what the address rules mean

The rules are by address **and port**. The control plane reaches the API server through `networkPolicy.apiServer.cidrs`
and `port` (6443 on k3s and kind; fill them from `kubectl get endpoints kubernetes`, not from the service address). The
runner may reach TCP 443 on public addresses only, so an API server on 6443 is out of its reach by port. An API server on
443 with a public address would not be protected by the excepted private ranges; the usual homelab node addresses
(`192.168.0.0/16`, `10.0.0.0/8`) are inside them. Pods in the cluster have addresses in `10.0.0.0/8` too: allow traffic to a
pod with a pod or namespace selector, never with an address.

## Checking it

`make chart-check` lints, renders, validates (kubeconform) and runs the render tests in `deploy/`.

## Installing it in a homelab

[`docs/runbook/homelab-deploy.md`](../../docs/runbook/homelab-deploy.md): the namespace, a SealedSecret, the values, an Argo CD
Application with two sources of this repository (the chart, and the pinned CLI in `deploy/cli-pin.yaml`), the one-time login and
upgrades.
