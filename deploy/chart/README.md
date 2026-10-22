# The Remedy chart

Runs the control plane and the runner in one namespace. One release per namespace: the resource names are fixed.
Spec: [`docs/specs/2026-10-06-kubernetes-deployment-design.md`](../../docs/specs/2026-10-06-kubernetes-deployment-design.md).

## What you need first

- A Secret with three keys (`admin-password`, `runner-token`, `master-key`; the names are values). The chart never creates
  it. `openssl rand -hex 12` (password), `openssl rand -hex 24` (runner token), `openssl rand -base64 32` (master key).
- For the database, one of two (see "The database" below): by default the chart installs a small single-node PostgreSQL of
  its own and needs a Secret with its password (`postgres.existingSecret`, the key `postgres.passwordKey`, default
  `password`); or a PostgreSQL 16 or newer of your own and a Secret with its connection URL (`database.existingSecret`, the
  key `database.secretKey`, default `url`). The chart never creates either Secret and refuses a password or URL as a value.
- The pinned CLI: `runner.cli.version`, `runner.cli.urlTemplate`, and the platform names and SHA-256 per architecture
  (`docs/research/k8s-s4-cli-install.md` says where they come from). No image contains the CLI.

## What it renders

| Object | Name | Note |
|---|---|---|
| Deployment | `remedy-server` | one replica, `Recreate`, ports 8080 (public) and 8081 (internal); no volume, the state is in PostgreSQL |
| Services | `remedy-server`, `remedy-server-internal` | only the first may be exposed |
| StatefulSet | `remedy-runner` | init container `install-cli`; no Kubernetes token |
| Ingress | `remedy` | optional |
| StatefulSet, Service | `remedy-postgres` | the bundled PostgreSQL (the default; not with `database.existingSecret`): one pod, headless Service, claim `data-remedy-postgres-0` |
| CronJob, PersistentVolumeClaim | `remedy-postgres-backup` | the nightly `pg_dump` of the bundled PostgreSQL (`postgres.backup.enabled`, default on) |
| NetworkPolicies | `remedy-server`, `remedy-runner`, `remedy-token-refresher` (only with `cluster.write.enabled`: DNS and the API server, nothing else), `remedy-postgres` (only with the bundled PostgreSQL: ingress from the control plane and the backup job) | on by default; `networkPolicy.*` (`database` is required with `database.existingSecret` and refused otherwise; `hosts`, `alertmanager` and `metrics` are optional; all add egress to the control plane only) |

With `cluster.enabled` the control plane runs as the read account (a projected token) and the chart creates a read-only ClusterRole.
With `cluster.write.enabled` a CronJob (default schedule `*/30 * * * *`, `cluster.write.tokenRefresh.schedule`) and a Job that runs as a post-install and post-upgrade
hook mint a two-hour token of the write account into the Secret
`remedy-write-token`, which the pod mounts; Roles are created in each of `cluster.write.namespaces` (which must exist) and in
`cluster.argoNamespace`. `cluster.write.namespaces` can never contain the release namespace or `cluster.argoNamespace`, and `cluster.argoNamespace` can never be the release namespace (the chart refuses to render). If the refresher stops, actions
fail closed after the token expires. Under Argo CD add an `ignoreDifferences` for the Secret's `/data`.

## The database

The control plane keeps all its state in PostgreSQL, in one of two ways:

- **The bundled PostgreSQL (the default).** With `database.existingSecret` empty the chart installs a small single-node
  PostgreSQL of its own in the release namespace (`postgres.*`).
- **A database of your own.** With `database.existingSecret` set the chart renders nothing of the bundled one and takes only
  a connection: an operator (for example CloudNativePG), a server on another host, or anything else that speaks PostgreSQL
  16 or newer.

`postgres.enabled` is unset by default, which means "bundled unless `database.existingSecret` is set". The render fails
for `postgres.enabled: true` together with `database.existingSecret` (a contradiction) and for `postgres.enabled: false`
without it (no database at all).

### The bundled PostgreSQL

The smallest values:

```yaml
postgres:
  existingSecret: remedy-postgres   # required: a Secret that exists already, with the password under the key password
```

for example made with

```sh
kubectl -n remedy create secret generic remedy-postgres --from-literal=password="$(openssl rand -hex 24)"
```

(or the same as a SealedSecret). The chart never generates or stores the password: a generated one would change on every
render, and Helm's `lookup` does not work under Argo CD. **The password must be URL-safe** (letters and digits, as
`openssl rand -hex` gives): the control plane's connection URL is composed with it as it is, without percent-encoding.

What it renders:

- A StatefulSet `remedy-postgres` with one pod, the image `postgres.image.repository:postgres.image.tag` (default
  `postgres:16.15`; the tag must be an exact minor version such as `16.15`, the render refuses `16` or `latest`), user and
  database `remedy`, the data directory in `pgdata` below the claim's mount, readiness and liveness by `pg_isready` over TCP,
  and the restricted Pod Security Standard (user, group and `fsGroup` 999, no privilege escalation, no capabilities, a
  read-only root file system with `emptyDir` for the socket directory and `/tmp`, no service account token). Its claim
  `data-remedy-postgres-0` comes from the claim template `data` (`postgres.persistence.size`, default `5Gi`;
  `postgres.persistence.storageClass`, empty: the cluster's default). `postgres.resources` sizes the pod.
- A headless Service `remedy-postgres`. The control plane gets `REMEDY_POSTGRES_PASSWORD` from the Secret
  (`secretKeyRef`) and, after it, `REMEDY_DATABASE_URL` as
  `postgres://remedy:$(REMEDY_POSTGRES_PASSWORD)@remedy-postgres.<namespace>.svc:5432/remedy?sslmode=disable`, which
  Kubernetes expands when it starts the container. No rendered object holds the password.
- With `networkPolicy.enabled`: a rule on the `remedy-server` policy to the PostgreSQL pods on 5432 (instead of
  `networkPolicy.database`, which is refused here), and a policy `remedy-postgres` that admits ingress on 5432 from the control
  plane's pods and the backup job's pods of the release only. It has no egress rule and does not restrict the database's
  egress. The PostgreSQL pods carry `app.kubernetes.io/name: remedy-postgres`, so none of the chart's other policies selects
  them.
- With `postgres.backup.enabled` (the default): a claim `remedy-postgres-backup` (`postgres.backup.size`, default `2Gi`;
  `postgres.backup.storageClass`, empty: the data's class) and a CronJob `remedy-postgres-backup` (`postgres.backup.schedule`,
  default `17 3 * * *`) that runs `pg_dump --format=custom --no-owner` with the same image into
  `/backup/remedy-<UTC timestamp>.dump`. The dump is written as `.part` and renamed only when `pg_dump` succeeded; dumps older
  than `postgres.backup.retentionDays` (default 7) are deleted only after a new dump was written. Run one now with
  `kubectl -n <namespace> create job --from=cronjob/remedy-postgres-backup backup-now`; restore one with `pg_restore` into an
  empty database, with the control plane scaled to zero. A claim of a storage class that binds on first use (such as
  `local-path`) stays `Pending` until the first backup runs.

**`sslmode=disable`** is deliberate: the bundled server has no TLS, and the connection stays on the pod network inside the
namespace, where the `remedy-postgres` policy admits only the control plane and the backup job.

What it does **not** provide: high availability, replication, backups that leave the node (the backup claim is in the same
cluster and, on a single-node cluster, on the same disk: it protects against a damaged or wrongly changed database, not
against losing the disk; copy the dumps elsewhere for that), TLS, and major-version upgrades. A new minor version of the
same major (for example `16.15` to `16.16`) is a change of `postgres.image.tag` and a restart. A new major version cannot
open the old data directory: take a dump, install the new major on an empty claim, and restore the dump into it.

Ownership: the claim `data-remedy-postgres-0` is made by the StatefulSet, not by Helm, and the StatefulSet keeps it when it is
deleted or scaled down (`persistentVolumeClaimRetentionPolicy: Retain`): `helm uninstall` leaves it, and a new install with
the same namespace finds the old data. The backup claim carries `helm.sh/resource-policy: keep`, so `helm uninstall` leaves
it too. Delete either by hand to start empty. The claim template cannot be changed after the install (a new size or class
fails the upgrade): resize the claim itself if its storage class allows it. The password is set when the data directory is
first created; changing it in the Secret later does not change it in the database (change it with `ALTER ROLE remedy
PASSWORD ...` and in the Secret, then restart the control plane).

### A database of your own

```yaml
database:
  existingSecret: remedy-database   # a Secret that exists already
  secretKey: url                    # the key that holds the URL (default url)
```

The Secret's value is a libpq-style URL, for example
`postgres://remedy:PASSWORD@db.databases.svc:5432/remedy?sslmode=require` (`sslmode` and the like travel in the URL; the chart
cannot mount a CA file, so `sslrootcert=/path` has nothing to read and the control plane would fail at start: `verify-full`
works only with a CA the server image already trusts, and for a private CA `require` is what is left: encrypted, not verified). The pod receives the URL as `REMEDY_DATABASE_URL` through a `secretKeyRef`.
There is no `database.url` value and no other key under `database`: the render fails for them (a password in
`values.yaml` ends up in the repository), and `server.env` may not set `REMEDY_DATABASE_URL` or `REMEDY_DB` either. The
pool size is `REMEDY_DATABASE_MAX_CONNS` (default 10, 2 to 100), which `server.env` may set. The claim `remedy-data` and the
values `server.persistence.*` are gone. The chart renders no claim for the control plane any more, and Helm itself deletes nothing: the
claim an earlier release made carries `helm.sh/resource-policy: keep`, which keeps it on a plain `helm upgrade`. Under Argo CD with
pruning on, the old claim is an orphan that Argo CD may prune (whether it honors that annotation was not checked): protect it
first, as step 2 of the cutover runbook says. This chart is only usable together with the version of
Remedy that reads `REMEDY_DATABASE_URL`; moving an existing SQLite file into the database is the job of the importer and the
cutover runbook of that version.

The control plane may reach a database of your own only through `networkPolicy.database`, required while
`networkPolicy.enabled` is true, in exactly one of two forms (the render fails for neither, both or any other key):

```yaml
networkPolicy:
  database:                          # pods inside the cluster, like the Alertmanager
    namespace: databases
    podSelector: {cnpg.io/cluster: remedy}   # must not be empty
    port: 5432                       # default 5432
  # or an external server:
  # database: {cidr: 192.168.178.20/32, port: 5432}
```

`cidr` is the range of the external server. An IPv4 range must be `/16` to `/32` and start at its network address
(`192.168.178.20/8` or `192.168.178.20/24` is refused: a typo of the prefix would open a whole network), and an IPv6 range
must be `/16` to `/128` and lie outside `::/16` (which holds `::/0`, `::1` and the IPv4-mapped range `::ffff:0:0/96`); the
rest is refused because it would open everything to the pod that holds the credentials. Use the single address (`/32`,
`/128`) when you can. The rule is on the `remedy-server` policy only: the runner and the token refresher never
get it.

The probes of the control plane (`/healthz`) do not depend on the database. At start the control plane waits up to 30 s
for a database that is not reachable yet and then exits, so a database that is down when the pod starts makes the pod
crash-loop until it is back. Once the control plane runs, a database that goes away does not restart the pod: the pod
stays ready, and requests that need the database fail until it is back.

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
required, because an empty selector would admit every pod of the namespace. `networkPolicy.metrics` has the same shape for a Prometheus-compatible
query API (the metrics tools) and requires the `port` too (Prometheus serves 9090, VictoriaMetrics 8428). There is no free list of extra rules on
purpose: one typo (`0.0.0.0/0`, no port) would open all egress of the pod that holds the credentials. All three default to empty
and then render nothing; the render fails for a value that breaks a rule, and ignores them when `networkPolicy.enabled` is false.

```yaml
networkPolicy:
  hosts:
    - {cidr: 192.168.178.118/32, port: 22}
  alertmanager:
    namespace: monitoring
    podSelector: {app.kubernetes.io/name: vmalertmanager}
  metrics:
    namespace: monitoring
    podSelector: {app.kubernetes.io/name: vmsingle}
    port: 8428
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
