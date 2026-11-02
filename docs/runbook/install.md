# Runbook: installing Remedy on a cluster of your own

**Status.** Walked on 2026-11-02 on a throwaway kind cluster as far as the login, with the chart of this repository: sections 3 to 7 and 11. Not walked: the login (section 8) and
everything after it, an Ingress controller, the OCI install of the release that carries this text, k3s, amd64. The record is
[`docs/research/install-walkthrough.md`](../research/install-walkthrough.md).

This installs Remedy from a release with plain `helm install`: no GitOps tool, no second repository, no file besides your own
values. It needs the first release after 0.17.1 (the release that moved the CLI pin into the chart and removed the default of
`networkPolicy.public.from`); for an older chart see "Upgrading from 0.17" in the [chart README](../../deploy/chart/README.md).
Plan on half an hour. The install under Argo CD, with sealed secrets, is the maintainer's example,
[`homelab-deploy.md`](homelab-deploy.md).

**One installation belongs to one operator.** Remedy has a single admin password and no user accounts. The runner holds *your*
Claude login; do not share it, and do not point a colleague at your installation: a second person runs a second one.

## 1. What this installs

The control plane (a Deployment, `remedy-server`), the runner (a StatefulSet, `remedy-runner`, the only pod that holds the
CLI login) and a small PostgreSQL of its own (`remedy-postgres`, with a nightly dump), in one namespace, `remedy`. Network
policies keep the pods apart. By default Remedy touches nothing in your cluster and reads nothing from it (`cluster.enabled`
is off); you turn that on later.

## 2. You need

- A Kubernetes cluster whose CNI enforces NetworkPolicies (k3s, kind, Cilium, Calico). If it does not, the policies do nothing
  and the install still works, but nothing keeps the pods apart.
- `kubectl`, `helm` 3.8 or later, and `openssl` on your machine.
- Storage from the cluster's default storage class: 5 GiB for the database, 2 GiB for its dumps, 1 GiB for the runner.
- Outbound access from the cluster to `ghcr.io` (the images and the chart), to `downloads.claude.ai` (the runner's init
  container downloads the CLI and checks its SHA-256) and to `api.github.com`.
- A Claude subscription of your own, for the one-time login.
- A way to reach the UI: an Ingress controller, or `kubectl port-forward` (section 7).

## 3. The namespace

```sh
kubectl create namespace remedy
kubectl label namespace remedy pod-security.kubernetes.io/enforce=restricted
```

Every pod of the chart passes the restricted Pod Security Standard.

## 4. The two Secrets

The chart never creates a Secret and refuses a password as a value. The first holds the admin password, the runner's token and
the master key; the second holds the database password. `printf` is a shell builtin, so the values are in no process's
argument list.

```sh
printf 'admin-password=%s\nrunner-token=%s\nmaster-key=%s\n' "$(openssl rand -hex 12)" "$(openssl rand -hex 24)" "$(openssl rand -base64 32)" \
  | kubectl -n remedy create secret generic remedy-secrets --from-env-file=/dev/stdin

printf 'password=%s\n' "$(openssl rand -hex 24)" \
  | kubectl -n remedy create secret generic remedy-postgres --from-env-file=/dev/stdin
```

**Keep a copy of the master key** (`kubectl -n remedy get secret remedy-secrets -o jsonpath='{.data.master-key}' | base64 -d`)
in a password manager. It seals the GitHub token and the SSH credentials of every host, and it cannot be recovered: lose it
and you enter those logins again (nothing else is lost). The admin password is read the same way (`.data.admin-password`).

## 5. The values

Save this as `values.yaml`. It is the smallest install: no cluster access, no Ingress.

```yaml
existingSecret:
  name: remedy-secrets
postgres:
  existingSecret: remedy-postgres

networkPolicy:
  # Who may reach the UI. The chart has no default; pick the line that matches what is in front of Remedy.
  public:
    from:
      - namespaceSelector: {matchLabels: {kubernetes.io/metadata.name: ingress-nginx}}   # ingress-nginx
      # - namespaceSelector: {matchLabels: {kubernetes.io/metadata.name: kube-system}}   # k3s with its Traefik
      # - ipBlock: {cidr: 192.0.2.10/32}                                                 # a node or proxy address
```

With no Ingress controller at all (you only use the port-forward of section 7), any one of the three lines is enough; it only
has to be there.

With an Ingress controller, add:

```yaml
ingress:
  enabled: true
  className: nginx               # your controller's class
  host: remedy.example.org       # your host name
  tls:
    secretName: remedy-tls       # optional; without it the cookie is not Secure (section 10)
```

The pinned CLI needs nothing: the chart ships one. To let Remedy read your cluster, see section 9. The other values are in the
[chart README](../../deploy/chart/README.md).

## 6. Install

```sh
helm install remedy oci://ghcr.io/jaydee94/charts/remedy --version X.Y.Z -n remedy -f values.yaml
kubectl -n remedy get pods -w
```

Use the latest release for `X.Y.Z`. After a minute `remedy-postgres-0`, `remedy-server-…` and `remedy-runner-0` are `Running`
and `Ready`. The runner is Ready before it is logged in; readiness never depends on the login. The claim `remedy-postgres-backup`
stays `Pending` until the first nightly dump on a storage class that binds on first use; that is normal.

## 7. Open the UI

```sh
kubectl -n remedy port-forward svc/remedy-server 8080:8080
```

Open `http://localhost:8080` and sign in with the admin password of section 4 (the login page has no user name). Enter a token
or an SSH key only over an encrypted path: the port-forward, a VPN, or a host name with TLS. Never over plain HTTP on a
network you do not trust.

## 8. The one-time login

Open **Setup**. The runner card says whether the runner is connected and logged in. Press **Log in from here**: a wizard shows
the sign-in link of the vendor, you sign in with your own account, paste the one-time code, and the runner hands it to the CLI.
The card can take about 15 seconds to turn green. The URL and the code live in memory for that one attempt only.

If the card does not offer the button, or you prefer a terminal:

```sh
kubectl -n remedy exec -it remedy-runner-0 -c runner -- /opt/claude/claude
```

Type `/login`, open the URL in a browser, paste the code, `/exit`. The login is on the runner's volume and survives restarts of
the pod, not the loss of the volume. A reverse proxy in front of the control plane may log request bodies, and the code is in
the body of a POST: make sure yours does not.

## 9. First steps

In Setup connect GitHub (a token; it is write-only and shown only as `…` and its last four characters) and add the repositories
to watch. A failed check becomes an incident, and Remedy diagnoses it. Then, as you need them:

- **Your cluster.** `cluster.enabled: true`, and `networkPolicy.apiServer.cidrs` with the address behind
  `kubernetes.default.svc` (`kubectl get endpoints kubernetes`). Actions on the cluster stay off until you set
  `cluster.write.enabled` and the namespaces they may touch. See the [chart README](../../deploy/chart/README.md).
- **Other machines.** The Hosts page and `networkPolicy.hosts` for the SSH egress; [`skills.md`](skills.md) for what Remedy
  learns about them.
- **An Alertmanager.** [`alertmanager.md`](alertmanager.md). If it already holds firing alerts, set "Automatic diagnoses per 24 hours" (`diagnose_max_per_day`) to 0
  in Setup first: every young `critical` or `warning` alert would otherwise be diagnosed on its own, at the cost of
  subscription quota.

Every change an agent wants to make waits for your approval in **Needs you**.

## 10. What to know

- **The UI is protected by a password only.** Anyone who reaches it can approve actions. Keep it on a LAN or behind a VPN, not
  on the open internet. Five failed sign-ins from one TCP peer lock the login for everyone behind that peer, which for a
  reverse proxy means everyone.
- **The session cookie is `Secure` only behind TLS** (the control plane sets it when the request carries
  `X-Forwarded-Proto: https`). Without `ingress.tls.secretName` it is not.
- **Sessions are in memory:** a restart of the control plane signs everyone out and ends runs in flight.
- **Backups.** The chart dumps the database nightly to a second claim. Restore and the rest: [`postgres.md`](postgres.md).
- **The master key** cannot be recovered (section 4).

## 11. Upgrading

```sh
helm upgrade remedy oci://ghcr.io/jaydee94/charts/remedy --version X.Y.Z -n remedy -f values.yaml
```

The control plane restarts with `Recreate`: a minute without the UI, runs in flight end as lost. The CLI pin is part of the
chart, so a new CLI version arrives with a new release, *unless you set `runner.cli` yourself* (or still pass the pin file of an
older release with `-f`): a pin of your own wins over the chart's and stays until you remove it.

## 12. If it does not work

- A pod stays `Pending`: no default storage class (`kubectl get storageclass`).
- `helm install` fails with `networkPolicy.public.from must list at least one peer`: section 5.
- The runner restarts in a loop and its probes fail: the CNI may block the kubelet's probes on port 8082. Set
  `networkPolicy.enabled=false` for the time of the check. More in [`homelab-deploy.md`](homelab-deploy.md), section 12.
- The control plane never becomes Ready: its probes on port 8080 reach the pod from the node, and `networkPolicy.public.from` names
  only the peers you gave it. On a CNI that blocks the node's traffic add the node's address range as an `ipBlock` to
  `networkPolicy.public.from`, or set `networkPolicy.enabled=false` for the time of the check.
- `init:Error` on the runner: the init container could not download the CLI or its checksum did not match
  (`kubectl -n remedy logs remedy-runner-0 -c install-cli`).
