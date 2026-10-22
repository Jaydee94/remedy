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
| NetworkPolicies | `remedy-server`, `remedy-runner`, `remedy-token-refresher` (only with `cluster.write.enabled`: DNS and the API server, nothing else), `remedy-postgres` and `remedy-postgres-backup` (only with the bundled PostgreSQL and its backup) | on by default; `networkPolicy.*` (`database` is required with `database.existingSecret` and refused otherwise; `hosts`, `alertmanager` and `metrics` are optional; all add egress to the control plane only) |

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
without it (no database at all). With `database.existingSecret` set every other `postgres.*` value must stay at its default:
the render refuses one that would have no effect. An unknown key anywhere under `postgres` is refused in both cases, without
its value in the message.

### The bundled PostgreSQL

The smallest values:

```yaml
postgres:
  existingSecret: remedy-postgres   # required: a Secret that exists already, with the password under the key password
```

for example made with

```sh
printf 'password=%s\n' "$(openssl rand -hex 24)" \
  | kubectl -n remedy create secret generic remedy-postgres --from-env-file=/dev/stdin
```

`printf` is a shell builtin, so the password is in no process's argument list (`--from-literal=password=...` would put it
there). Under GitOps make it a SealedSecret instead: add `--dry-run=client -o yaml | kubeseal --format yaml >
remedy-postgres.sealed.yaml` to the `kubectl` line, so the plain Secret only travels through pipes
(`docs/runbook/homelab-deploy.md`, section 4). The chart never generates or stores the password: a generated one would
change on every render, and Helm's `lookup` does not work under Argo CD. Any password works: the control plane gets it as
`PGPASSWORD`, not inside a URL, so it needs no URL encoding. A hex string is just convenient.

What it renders:

- A StatefulSet `remedy-postgres` with one pod: the image `postgres.image.repository:postgres.image.tag` (default
  `postgres:16.15`), user and database `remedy`, and the data directory in `pgdata` below the claim's mount.
  - **The tag** must be exactly `MAJOR.MINOR` of the Debian-based image, such as `16.15`. The render refuses a moving tag
    (`16`, `latest`), `-alpine` (that image runs postgres as uid 70, the chart as 999), any other suffix, and a digest:
    this value takes a tag only.
  - **Probes:** a startup probe by `pg_isready` over TCP (every 5 s, up to 60 times: five minutes for `initdb` or a crash
    recovery), then readiness and liveness the same way. `terminationGracePeriodSeconds: 60` leaves time for a clean
    shutdown.
  - **Security:** the restricted Pod Security Standard (user, group and `fsGroup` 999, no privilege escalation, no
    capabilities, a read-only root file system with `emptyDir` for the socket directory and `/tmp`, no service account
    token). The pod template carries no chart version, so a chart upgrade that changes nothing of the database does not
    restart it.
  - **Storage:** its claim `data-remedy-postgres-0` comes from the claim template `data` (`postgres.persistence.size`,
    default `5Gi`; `postgres.persistence.storageClass`, empty: the cluster's default). `postgres.resources` sizes the pod.
- A headless Service `remedy-postgres`. The control plane gets `PGPASSWORD` from the Secret (`secretKeyRef`) and
  `REMEDY_DATABASE_URL` as `postgres://remedy@remedy-postgres.<namespace>.svc:5432/remedy?sslmode=disable`, a URL without a
  password: the driver (pgx) takes the password from `PGPASSWORD`. No rendered object holds the password, and `server.env`
  cannot set `PGPASSWORD` (its name contains `PASSWORD`).
- With `networkPolicy.enabled`:
  - A rule on the `remedy-server` policy to the PostgreSQL pods on 5432. It takes the place of `networkPolicy.database`,
    which is refused here.
  - A policy `remedy-postgres`. It admits ingress on 5432 only from the release's control-plane pods and backup-job pods,
    and it allows no egress at all: the database connects to nothing.
  - With the backup on, a policy `remedy-postgres-backup`. The job takes no connection; it may reach DNS (the same kube-dns
    rule as the other policies, `networkPolicy.dnsNamespace`) and the PostgreSQL pods on 5432, nothing else.
  - The PostgreSQL pods carry `app.kubernetes.io/name: remedy-postgres` and the backup pods `remedy-postgres-backup`, so
    none of the chart's other policies selects them.
- With `postgres.backup.enabled` (the default): a claim and a CronJob, both named `remedy-postgres-backup`.
  - **The claim:** `postgres.backup.size`, default `2Gi`; `postgres.backup.storageClass`, empty: the data's class.
  - **The CronJob:** `postgres.backup.schedule`, default `17 3 * * *`; `startingDeadlineSeconds: 86400`, so a run missed by
    less than a day still runs. It runs `pg_dump --format=custom --no-owner` with the same image into
    `/backup/remedy-<UTC timestamp>.dump`.
  - **Safety:** the dump is written as `.part` and renamed only when `pg_dump` succeeded. Old dumps are deleted only after a
    new dump was written. A dump is deleted once it is older than `postgres.backup.retentionDays` (default 7) times 1440
    minutes when a run ends, so the claim holds the dumps of about the last N days.

**Right after the install, run the first backup by hand:**

```sh
kubectl -n <namespace> create job --from=cronjob/remedy-postgres-backup first-backup
```

A claim of a storage class that binds on first use (`WaitForFirstConsumer`, such as `local-path`) stays `Pending` until a
pod uses it, and Argo CD shows the application as `Progressing` until then. The same command takes a backup at any time
(use a new job name).

**Restore a dump.**

1. Stop the control plane. Under Argo CD, turn off auto-sync of the application first: its self-heal would scale the
   control plane back up. Then run `kubectl -n <namespace> scale deployment/remedy-server --replicas=0`.
2. Start a pod that mounts the backup claim read-only. It carries the backup job's labels, so the chart's policies let it
   reach the database. Use your release name for `instance`, and your Secret and key for the password:

   ```yaml
   apiVersion: v1
   kind: Pod
   metadata:
     name: remedy-postgres-restore
     labels: {app.kubernetes.io/name: remedy-postgres-backup, app.kubernetes.io/instance: remedy}
   spec:
     restartPolicy: Never
     automountServiceAccountToken: false
     securityContext:
       runAsNonRoot: true
       runAsUser: 999
       runAsGroup: 999
       fsGroup: 999
       seccompProfile: {type: RuntimeDefault}
     containers:
       - name: restore
         image: postgres:16.15            # the tag of postgres.image.tag
         command: [sleep, "3600"]
         env:
           - {name: PGHOST, value: remedy-postgres}
           - {name: PGUSER, value: remedy}
           - {name: PGDATABASE, value: remedy}
           - {name: PGSSLMODE, value: disable}
           - name: PGPASSWORD
             valueFrom: {secretKeyRef: {name: remedy-postgres, key: password}}
         securityContext:
           allowPrivilegeEscalation: false
           readOnlyRootFilesystem: true
           capabilities: {drop: [ALL]}
         volumeMounts:
           - {name: backup, mountPath: /backup, readOnly: true}
           - {name: tmp, mountPath: /tmp}
     volumes:
       - name: backup
         persistentVolumeClaim: {claimName: remedy-postgres-backup, readOnly: true}
       - name: tmp
         emptyDir: {}
   ```

   Apply it with `kubectl -n <namespace> apply -f restore.yaml`, then wait for it with
   `kubectl -n <namespace> wait --for=condition=Ready pod/remedy-postgres-restore`.
3. List the dumps with `kubectl -n <namespace> exec remedy-postgres-restore -- ls -l /backup`. Then restore one into the
   database named `remedy`, the one the control plane uses:

   ```sh
   kubectl -n <namespace> exec remedy-postgres-restore -- \
     pg_restore --clean --if-exists --no-owner --single-transaction -h remedy-postgres -U remedy -d remedy /backup/remedy-<timestamp>.dump
   ```

   `--clean --if-exists` drops what is there first, and `--single-transaction` leaves the database unchanged if the restore
   fails. `--clean` drops only the objects the dump contains: a table made after the dump stays, for example one from a
   migration of a newer Remedy. To return to exactly the dump, restore into an empty database. To get one, delete the claim
   `data-remedy-postgres-0` with the StatefulSet scaled to zero, then scale it back.
4. Delete the pod: `kubectl -n <namespace> delete pod remedy-postgres-restore`. Scale the control plane back to 1, or turn
   auto-sync on again.

Two notes on the restore:

- The backup claim is `ReadWriteOnce`. On a cluster with several nodes the restore pod must land on the node that holds the
  claim; a `local-path` volume already pins the pod there.
- A dump holds the sealed values as bytes and everything else as plain text. They open again only with the same
  `REMEDY_MASTER_KEY`.

**`sslmode=disable`** is deliberate. The bundled server has no TLS, and the connection stays on the pod network inside the
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
first created; changing it in the Secret later does not change it in the database. To change it, see
`docs/runbook/postgres.md`, section 2 (`\password` in `psql`, then the Secret, then a restart of the control plane).

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
pool size is `REMEDY_DATABASE_MAX_CONNS` (default 10, 2 to 100), which `server.env` may set. The old SQLite claim `remedy-data`
(and the values `server.persistence.*`) is no longer rendered. Existing SQLite data is not migrated: an installation of an
earlier version is reinstalled with an empty database. Copy `remedy.db` out **before** you bump the chart version: under
Argo CD, `prune` can delete the old claim `remedy-data`, and a `local-path` volume loses its data with it
(`docs/runbook/postgres.md`, section 10).

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
