# Runbook: operating Remedy's PostgreSQL

Status: **this runbook was not run on a cluster.** The commands below were checked against the chart
(`deploy/chart/templates/postgres.yaml`, `postgres-backup.yaml`, `networkpolicy.yaml`) and the code at the time of writing,
and every shell block passes `sh -n`; none of the blocks of this runbook was run against the bundled PostgreSQL on a
cluster. What was run during development is listed in section 9: a `pg_dump`/`pg_restore` round trip and the server's
behaviour against a database that is down, wrong or without TLS, on a scratch database of the test PostgreSQL of `dev/pg/`,
and the chart's bundled PostgreSQL in a throwaway kind cluster. Read every step as expected, not as tried.

Background: `docs/specs/2026-10-22-postgres-only-design.md` (decisions 7 and 11), `docs/design.md` 2.6,
`deploy/chart/README.md` ("The database"). The control plane keeps all its state in PostgreSQL. The chart installs a
single-node PostgreSQL of its own by default (`postgres.*`), or takes a connection to one you provide
(`database.existingSecret`). Existing SQLite data is not migrated (section 10).

Contents: 1 what you need, 2 the Secret, 3 backup and restore, 4 TLS, 5 pool sizing, 6 when PostgreSQL is down or
restarts, 7 what is tested about secrets at rest, 8 operational notes, 9 what was tried, 10 upgrading from the SQLite
versions.

## 1. What you need

**The bundled PostgreSQL (the default).** Nothing but a Secret with its password (section 2) and the value
`postgres.existingSecret`. The chart renders a StatefulSet `remedy-postgres` (one pod, image `postgres.image`, default
`postgres:16.15`, user and database `remedy`) with the claim `data-remedy-postgres-0` (`postgres.persistence.size`, default
`5Gi`), a headless Service `remedy-postgres`, a NetworkPolicy `remedy-postgres` (while `networkPolicy.enabled`) that admits
only the control plane and the backup job, and a nightly backup (section 3). The control plane gets the password as
`PGPASSWORD` from the Secret (`secretKeyRef`) and `REMEDY_DATABASE_URL` as
`postgres://remedy@remedy-postgres.<namespace>.svc:5432/remedy?sslmode=disable`, a URL without a password: with a URL that
has no password, pgx takes the password from `PGPASSWORD` (pinned by tests in `internal/config` and `internal/store`).
The details, and what the bundled database does not provide (high availability, replication, off-node backups, TLS,
major-version upgrades), are in `deploy/chart/README.md`, "The bundled PostgreSQL".

**A database of your own** (`database.existingSecret`; nothing of the bundled one renders):

- A PostgreSQL 16 or newer, an empty database on it, and a role that owns that database (it must be able to create tables,
  indexes and sequences there; Remedy uses no extension). An operator in the cluster (for example CloudNativePG), a
  StatefulSet of your own, or a server on another host.
- **A dedicated database.** Give Remedy a database that holds nothing else, and one per installation (section 8). For
  example, as a PostgreSQL superuser (`\password` asks for the password without echoing it):

  ```sql
  CREATE ROLE remedy LOGIN;
  \password remedy
  CREATE DATABASE remedy OWNER remedy;
  ```

- The connection URL in a Secret of the Remedy namespace (section 2), never in `values.yaml`.
- `networkPolicy.database` while the network policies are on, in one of two forms: pods in the cluster (`namespace`,
  `podSelector`, `port`) or an external server (`cidr`, `port`); `deploy/chart/README.md`, "A database of your own". The
  database's own policy, if it has one, must admit the Remedy namespace as well.

In both cases: the master key (`REMEDY_MASTER_KEY`, in `remedy-secrets`) is not in the database. It seals the GitHub token,
the host logins and the Alertmanager and metrics tokens; keep it apart from the database and its backups (section 3).

## 2. The Secret

**The bundled PostgreSQL** reads its password from the Secret named by `postgres.existingSecret`, under the key
`postgres.passwordKey` (default `password`). The chart never creates it. Any password works: it reaches the control plane
as `PGPASSWORD`, not inside a URL, so it needs no URL encoding; a hex string is just convenient. Made as a SealedSecret, the
same way as `remedy-secrets` in `homelab-deploy.md` section 4, so that the password reaches no file and no process's
argument list (`printf` is a shell builtin; the plain Secret goes from `kubectl` to `kubeseal` through pipes):

```sh
printf 'password=%s\n' "$(openssl rand -hex 24)" \
  | kubectl -n remedy create secret generic remedy-postgres --from-env-file=/dev/stdin --dry-run=client -o yaml \
  | kubeseal --format yaml > remedy-postgres.sealed.yaml
```

Commit the sealed file to the GitOps repository (with the wrapper chart of `homelab-deploy.md` as
`argocd/apps/remedy/templates/sealedsecret-postgres.yaml`) and set `postgres.existingSecret: remedy-postgres`. Nobody needs
to know the password: the control plane and the backup job read it from the Secret. To read it once:
`kubectl -n remedy get secret remedy-postgres -o jsonpath='{.data.password}' | base64 -d`.

The password takes effect when the data directory is first created. Changing it in the Secret later does not change it in
the database. To change it, in both places:

1. In the database: `kubectl -n remedy exec -it remedy-postgres-0 -- psql -U remedy -d remedy` (over the pod's socket,
   which the image trusts), then `\password remedy`, which asks for the new password twice without echoing it, so it
   reaches no argument list and no shell history; then `\q`. A `psql` warning that it cannot save its history (the root
   file system is read-only) is harmless.
2. In the Secret: make it again with the pipeline above, with your new password in place of `openssl rand` (read it with
   `read -rs NEWPW`, use `"$NEWPW"`, then `unset NEWPW`), and commit the sealed file.
3. Restart the control plane: `kubectl -n remedy rollout restart deployment/remedy-server`. It reads `PGPASSWORD` from the
   Secret only when its pod starts.

Between step 1 and step 3 the running control plane keeps its open connections, but every new connection fails with
`password authentication failed`, and so does a pod that starts with the old Secret. Every user of `PGPASSWORD` from the
Secret reads it when its pod starts: the backup job takes the new password from its next run once the Secret has changed
(a run between steps 1 and 2 fails and is retried twice), and a tools pod of section 3 that is still running keeps the old
one (delete it and start it again). A new install that finds an old claim `data-remedy-postgres-0` keeps the old data and
the old password: the StatefulSet keeps its claim when it is deleted or scaled down (`deploy/chart/README.md`,
"Ownership").

**A database of your own** takes the whole URL from the Secret named by `database.existingSecret`, under the key
`database.secretKey` (default `url`): `homelab-deploy.md` section 4 has the commands (`read -rs`, then the same pipe). A
password with special characters must be percent-encoded in the URL.

## 3. Backup and restore

The database is the only state of the control plane. The runner's volume holds the CLI login only, which you make again
with the login of the Setup page or `homelab-deploy.md` section 7. A backup without the master key restores everything
except the sealed values (the GitHub token, host logins, the Alertmanager and metrics tokens), which you then enter again.

A dump contains the sealed values as ciphertext and everything else as plain text: run output, incident text, host names.
Treat the file like the database.

### The bundled PostgreSQL

With `postgres.backup.enabled` (the default) a CronJob `remedy-postgres-backup` runs `pg_dump --format=custom --no-owner`
on the schedule `postgres.backup.schedule` (default `17 3 * * *`) into the claim `remedy-postgres-backup`
(`postgres.backup.size`, default `2Gi`), as `/backup/remedy-<UTC timestamp>.dump` (for example
`remedy-20261022T031700Z.dump`, mode 600, owned by uid 999). A dump is written as `.part` and renamed only when `pg_dump`
succeeded; dumps older than `postgres.backup.retentionDays` (default 7) are deleted only after a new one was written. The
claim carries `helm.sh/resource-policy: keep`. It is in the same cluster (on a single-node cluster on the same disk): it
protects against a damaged or wrongly changed database, not against losing the disk. Copy dumps elsewhere for that (below).
A claim of a storage class that binds on first use (such as `local-path`) stays `Pending` until the first backup runs.

**Take a dump now** (before an upgrade, before a restore):

```sh
NS=remedy
JOB="backup-$(date -u +%Y%m%d%H%M%S)"
kubectl -n "$NS" create job --from=cronjob/remedy-postgres-backup "$JOB"
# Wait until the job has completed or failed, at most 10 minutes (kubectl wait --for=condition=complete alone would wait
# the full time on a job that failed).
i=0
while [ "$i" -lt 120 ]; do
  state=$(kubectl -n "$NS" get job "$JOB" -o jsonpath='{.status.conditions[?(@.status=="True")].type}')
  case "$state" in *Complete*|*Failed*) break ;; esac
  i=$((i + 1))
  sleep 5
done
kubectl -n "$NS" get job "$JOB"       # COMPLETIONS 1/1: done; otherwise read the log
kubectl -n "$NS" logs "job/$JOB"      # ends with "wrote remedy-....dump" and the list of /backup
```

A job that fails is retried twice (`backoffLimit: 2`) before its condition is `Failed`; its log says `pg_dump failed: no
new dump, no old dump deleted`.

**The tools pod.** No pod of the chart mounts the backup claim while no backup runs, and the policy `remedy-postgres` admits
only the control plane and pods with the backup job's labels. A short-lived pod with those labels, the same image and the
same security as the backup job reaches both the dumps and the database. It runs as 999, so it can read the dumps, and it
passes the restricted Pod Security Standard. The values are the chart's defaults: take yours from your values (`RELEASE` is
the Helm release, under Argo CD the Application's name).

The claim `remedy-postgres-backup` exists only while `postgres.backup.enabled` is true (or was, since the claim is kept).
`pg_tools` looks for it: when it exists the pod mounts it read-only at `/backup`; when it does not, the pod has `/tmp` only.
That is enough to restore a dump from your workstation (`kubectl cp` into `/tmp`, below); copying dumps off the node and a
restore from `/backup` need the claim.

Define the variables and the function once, and run the blocks below **in the same shell**: they use `NS` and `pg_tools`.

```sh
NS=remedy
RELEASE=remedy                 # the Helm release name (app.kubernetes.io/instance of the remedy-postgres pod)
PGIMAGE=postgres:16.15         # postgres.image.repository:postgres.image.tag of your values
PGSECRET=remedy-postgres       # postgres.existingSecret
PGKEY=password                 # postgres.passwordKey

pg_tools() {
  if kubectl -n "$NS" get pvc remedy-postgres-backup > /dev/null 2>&1; then
    BACKUP_MOUNT='{name: backup, mountPath: /backup, readOnly: true}, '
    BACKUP_VOLUME='    - name: backup
      persistentVolumeClaim: {claimName: remedy-postgres-backup, readOnly: true}'
  else
    echo "no claim remedy-postgres-backup: the tools pod has /tmp only, no /backup" >&2
    BACKUP_MOUNT=''
    BACKUP_VOLUME=''
  fi
  kubectl -n "$NS" apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: remedy-pg-tools
  labels:
    app.kubernetes.io/name: remedy-postgres-backup
    app.kubernetes.io/instance: $RELEASE
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
    - name: tools
      image: $PGIMAGE
      command: [sleep, "3600"]
      env:
        - {name: PGHOST, value: remedy-postgres.$NS.svc}
        - {name: PGPORT, value: "5432"}
        - {name: PGUSER, value: remedy}
        - {name: PGDATABASE, value: remedy}
        - {name: PGSSLMODE, value: disable}
        - name: PGPASSWORD
          valueFrom: {secretKeyRef: {name: $PGSECRET, key: $PGKEY}}
      securityContext: {allowPrivilegeEscalation: false, readOnlyRootFilesystem: true, capabilities: {drop: [ALL]}}
      volumeMounts: [${BACKUP_MOUNT}{name: tmp, mountPath: /tmp}]
  volumes:
${BACKUP_VOLUME}
    - name: tmp
      emptyDir: {}
EOF
  kubectl -n "$NS" wait --for=condition=Ready pod/remedy-pg-tools --timeout=120s || {
    kubectl -n "$NS" describe pod remedy-pg-tools
    kubectl -n "$NS" delete pod remedy-pg-tools --ignore-not-found --wait
    return 1
  }
}
```

The claim is `ReadWriteOnce`: the tools pod and a running backup job must be on the same node (on a single-node cluster they
are). Delete the pod when you are done: `kubectl -n "$NS" delete pod remedy-pg-tools`.

**Copy dumps off the node** (needs the backup claim; in the same shell as the tools pod block):

```sh
pg_tools
kubectl -n "$NS" exec remedy-pg-tools -- ls -l /backup
umask 077
kubectl -n "$NS" cp remedy-pg-tools:/backup/remedy-20261022T031700Z.dump ./remedy-20261022T031700Z.dump
kubectl -n "$NS" delete pod remedy-pg-tools
```

**Restore** a dump into a fresh database. This replaces everything in the database, so take a dump first (above) and keep
it. With the control plane stopped (`homelab-deploy.md` section 11, "Stopping the control plane": under Argo CD disable
auto-sync first, then `kubectl -n "$NS" scale deployment/remedy-server --replicas=0` and wait until its pod is gone), in
the same shell as the tools pod block (without the backup claim, use the workstation line and skip the `ls`):

```sh
pg_tools
kubectl -n "$NS" exec remedy-pg-tools -- ls -l /backup
DUMP=/backup/remedy-20261022T031700Z.dump
# or a dump from your workstation: kubectl -n "$NS" cp ./remedy-20261022T031700Z.dump remedy-pg-tools:/tmp/restore.dump; DUMP=/tmp/restore.dump
kubectl -n "$NS" exec remedy-pg-tools -- dropdb --force remedy
kubectl -n "$NS" exec remedy-pg-tools -- createdb remedy
kubectl -n "$NS" exec remedy-pg-tools -- pg_restore --dbname=remedy --no-owner --exit-on-error "$DUMP"
kubectl -n "$NS" delete pod remedy-pg-tools
kubectl -n "$NS" scale deployment/remedy-server --replicas=1
```

`dropdb` and `createdb` connect to the maintenance database `postgres`; the user `remedy` is the bundled server's superuser
(the image's `POSTGRES_USER`), so it may drop and create its database. `--force` ends connections that are still open.
Then enable auto-sync again. The id sequences are part of the dump, so the next id continues above the highest one. Do not
start the control plane on the empty database before the restore: it would make the tables, and `pg_restore` would stop
at the first `relation ... already exists`.

**A new minor version** of the same major (for example `16.15` to `16.16`) is a change of `postgres.image.tag` and a
restart of `remedy-postgres-0`, nothing else.

**A new major version** of PostgreSQL cannot open the old data directory, so it needs both a change of `postgres.image.tag`
and a dump and a restore. The render accepts any exact `MAJOR.MINOR` tag of the Debian-based image (it checks the form,
`^[0-9]+\.[0-9]+$`, not the major), so `17.6` is as valid a value as `16.15`; a tag such as `17` or `17.6-alpine` is
refused. The steps (none of them tried):

1. Take a dump (above) and copy it off the node (above). The backup claim is not touched by the next steps, but a copy off
   the node is the safe choice. Check that the copy is complete (`ls -l`).
2. Under Argo CD disable auto-sync. Scale the control plane to zero, then the database:
   `kubectl -n "$NS" scale deployment/remedy-server --replicas=0`, then
   `kubectl -n "$NS" scale statefulset/remedy-postgres --replicas=0`, and wait until both pods are gone
   (`kubectl -n "$NS" get pods`).
3. Only then delete the data claim: `kubectl -n "$NS" delete pvc data-remedy-postgres-0`. A claim that a pod still uses
   stays `Terminating` until the pod is gone (PVC protection); with `local-path` deleting the claim deletes the data.
4. Set `postgres.image.tag` to the new version and sync (or `helm upgrade`). The StatefulSet goes back to one replica, makes
   a new empty claim, and the new server initialises it with the password of the Secret. The sync also scales the control
   plane back to one: scale it to zero again at once (`kubectl -n "$NS" scale deployment/remedy-server --replicas=0`) and
   wait until its pod is gone. Tables it made meanwhile are dropped by the restore's `dropdb`.
5. Restore as above, with `PGIMAGE` set to the new image (for example `PGIMAGE=postgres:17.6`) before you call `pg_tools`:
   `pg_restore` must be at least the version of the `pg_dump` that wrote the dump.
6. Scale the control plane back to one and enable auto-sync again.

### A database of your own

From anywhere that reaches the database (a CronJob of your own, or the operator's backup feature):

```sh
PGPASSWORD=... pg_dump --format=custom --file="remedy-$(date +%F).dump" --host=db.example --username=remedy --dbname=remedy
```

Give the password through `PGPASSWORD` or a `~/.pgpass` file, not inside a connection string argument: an argument shows
in `ps` on a shared machine (`pg_dump` does not read `REMEDY_DATABASE_URL`). TLS settings travel as `PGSSLMODE` and
`PGSSLROOTCERT`. The control plane can run during the backup: `pg_dump` takes a consistent snapshot. Take one before every
upgrade.

Restore into an **empty** database, with the control plane scaled to zero. If the target is the database in use, take a
fresh dump first and keep it: dropping and recreating it destroys what is there.

```sh
createdb remedy_restored                      # or CREATE DATABASE; the same owner role
pg_restore --dbname=remedy_restored --no-owner --exit-on-error remedy-2026-10-22.dump
```

then point the Secret's URL at it (or restore into the database the Secret already names, after dropping and recreating
it) and start the control plane. The same rule as above: no start of the control plane on the target before the restore
(or use `pg_restore --clean --if-exists`, which drops the tables first).

## 4. TLS to the database

**The bundled PostgreSQL** has no TLS: the chart sets the URL with `sslmode=disable`. The connection stays on the pod
network of the namespace, where the policy `remedy-postgres` admits only the control plane and the backup job's pods.

**A database of your own:** the URL carries the settings, `sslmode` and `sslrootcert` (and `sslcert`/`sslkey` for client
certificates). What the driver does (`pgx` v5.11, read in its source):

- `sslmode` unset means `prefer`: it tries TLS, **does not verify the server** and falls back to plain text. `require`
  encrypts but does not verify either. Only `verify-ca` and `verify-full` check the certificate; `verify-full` also checks
  the host name against the certificate. Use `verify-full` for anything that leaves a trusted network.
- `sslrootcert=/path/ca.crt` names a PEM file with the CA. `sslrootcert=system` (and `verify-full` with no `sslrootcert`)
  uses the root certificates of the container image, which fits a server whose certificate was issued by a public CA.
- **The chart cannot give the pod a CA file.** It has no `extraVolumes` value or any other way to mount a file for this (the
  database Secret reaches the pod as the environment variable `REMEDY_DATABASE_URL` only), so `sslrootcert=/path` has
  nothing to read and the control plane fails at start. A private CA (the usual case for a database operator in the
  cluster) is therefore not supported by the chart yet. Until it is: `sslmode=require` (encrypted, not verified: it keeps a
  passive observer out, not an active one on the pod network), or a server whose certificate chains to a public root, or
  `disable` on a network you trust. Mounting a CA through the chart is a follow-up, not done.
- A server that has no TLS while the URL says `require` or `verify-*` does not fail with a TLS error: the control plane
  keeps retrying for 30 s and then reports `cannot reach the database: context deadline exceeded`. If that is what you see
  and the server is reachable, check the `sslmode` first. A wrong password or a missing database fails at once with the
  server's own message (`password authentication failed`, `database "..." does not exist`).
- The README's quick start uses `sslmode=disable` for a container on the same machine only.

## 5. Pool sizing

`REMEDY_DATABASE_MAX_CONNS` is the most connections the control plane opens, 2 to 100, default 10 (set it through
`server.env` in the chart; a value outside the range makes the server exit at start). It is one process serving one
operator's installation: 10 is plenty. Raise it only if requests are slow while every connection is busy
(`SELECT count(*) FROM pg_stat_activity WHERE datname = 'remedy'` shows the pool's size), and then keep PostgreSQL's
`max_connections` (default 100) above the pool plus whatever else connects: `psql`, backups. Connections are recycled after
30 minutes.

Connect to the server directly or through a pooler in **session** mode. A pooler in transaction mode (for example
PgBouncer's default) was not tried and is expected to break the migration runner's session-level advisory lock and the
driver's prepared statements.

## 6. When PostgreSQL is down or restarts

- **`/healthz` does not depend on the database.** It answers `{"status":"ok"}` whenever the process runs, so a pod's probe
  on it stays green during a database outage.
- **Down at start.** The server logs `connecting to the database` (it names `host:port/dbname` only) and keeps trying for
  30 s with growing pauses (1 s up to 5 s), listening on no port meanwhile (the pod is not Ready). Then it logs
  `cannot open database ... err="store: cannot reach the database: ..."` and exits with status 1; Kubernetes restarts the
  pod (CrashLoopBackOff with growing delays) until the database is back. A signal during the wait ends it at once. On a
  first install with the bundled database this is expected for a minute (not tried): the control plane may start before
  `remedy-postgres` is ready.
- **Down or restarted while running** (for the bundled database: a restart of the pod `remedy-postgres-0`, for example after
  a change of `postgres.image.tag`). The pool does not log its own reconnects. Connections that were cut are replaced by new
  ones on the next use. Requests that arrive while the database cannot be reached answer 500 with a short text (for example
  `could not load the connection`), and the background loops log the failed step and try again on their next cycle (for
  example `cannot load the GitHub connection`, `cannot list repos`), with the driver's error. When the database is back they
  recover by themselves; no restart of the control plane is needed. A request or a transaction that was in flight at the
  moment fails; the activity entry of a state change is written in the same transaction as the change, so a failed one
  leaves neither. Terminating all the pool's connections on the server (`pg_terminate_backend`) was tried against a running
  control plane: the next requests worked and the log said nothing. A full outage or a restart of the server under a
  running control plane was not tried.
- A **run** that is `running` while the database is away records no heartbeat. The reaper judges runs by their last
  heartbeat (2 minutes with gatekeeper access, 15 without), so an outage longer than that is expected to fail runs that were
  fine once the database is back (expected from the code, not tried).

## 7. What is tested about secrets at rest

What PostgreSQL leaves behind on disk is not visible to a `SELECT`:

- **Covered:** the whole-chain test of the login relay (`internal/app/loginrelay_chain_test.go`) reads every column of every
  row of every table through a second connection, `bytea` columns included, and fails if the planted login material appears
  in any of them or in any API answer, log line or event; the host tests (`internal/server/hosts_test.go`) do the same walk
  over the `hosts`, `host_credentials` (without the sealed column, which is ciphertext) and `activity` tables for the
  planted passwords and keys. Other secrets are covered by the tests of their own packages (sealing, the write-only API
  fields), not by a walk over the database.
- The real-run runbooks (`first-real-run.md`, `gatekeeper-real-run.md`, `cluster-real-run.md`) also scan a text `pg_dump`
  for the token. A dump shows a `bytea` column as hex, so they search for the hex form of the token as well
  (`od -An -tx1 | tr -d ' \n'`); that finds a plain-text token stored in a `bytea` column, and nothing else.
- **Not covered:** the storage level. Dead tuples that VACUUM has not removed, the write-ahead log, temporary files,
  backups and `pg_dump` files (the bundled backup claim included), replicas, and the server's swap can hold old values (a
  token that was replaced, a cleared field). No test reads those, and no test can from SQL. Disk encryption, the WAL and
  backup retention, and who may read them are the operator's concern, the same as for any database that holds
  credentials. The guarantee rests on the sealing and on the tests that walk every column, not on a scan of a file or a
  dump. Sealed values (the GitHub token, host logins, the Alertmanager and metrics tokens) are ciphertext in all of those
  places; the plaintext fields (run output, incident text, agent answers) are not secrets by design and are redacted
  before storage where they could hold one.

## 8. Operational notes

- **One database per installation.** The store's advisory locks belong to the database, not to a schema: two Remedy
  installations on one database (on different schemas) would serialise each other's activity writes, diagnosis starts and
  skill changes. That is not supported. Give each installation a database of its own (the bundled one is).
- **Activity-writing commits run one after the other.** Every transaction that writes an activity entry takes a lock right
  before its commit and keeps it until the commit has finished, so that the feed's ids stay in commit order. Their rate is
  bounded by one commit (one WAL flush) at a time: some hundreds per second, far above the few entries a minute Remedy
  writes. Nothing to do at this scale.
- **Keep `deadlock_timeout` at 200 ms or more** (PostgreSQL's default is 1 s, which the bundled database keeps). A delete
  that cascades (a host, a skill, a repository, the GitHub connection) gives up a lock wait after 50 ms and tries again,
  which only works while the server's deadlock check comes later than that. The control plane reads the setting at start
  and logs a warning when it is below 200 ms: `the database's deadlock_timeout is low: a delete that waits for a lock may
  end in a deadlock error instead of trying again` (with `deadlockTimeoutMS` and `recommendedMinimumMS`). The server works
  either way; only a rare wait cycle of a delete may then end in a deadlock error (`40P01`).
- **"store: the database is busy; try again"** means a delete (of the kinds above) ran out of its 40 attempts, about four
  seconds, because other transactions held the rows it needs. Nothing was deleted, not even part of it; do the same delete
  again a moment later. If it keeps happening, look for a long transaction
  (`SELECT * FROM pg_stat_activity WHERE state <> 'idle'`).
- **Busy deletes answer 503.** Removing a repository, the GitHub connection, a host or a skill may, rarely, find its rows
  held by other work for about four seconds. The control plane then answers `503 Service Unavailable` with
  `Retry-After: 2` and `{"error":"the database is busy; try again"}`. Nothing was deleted: repeat the action a moment
  later.

The reasoning and the other limits are in `docs/research/postgres-concurrency-audit.md`, "Operational notes".

## 9. What was tried

During development (plan task 10 of `docs/plans/postgres-only.md`), with the server built from the tree, on the test
PostgreSQL of `dev/pg/` (a scratch database, not the shared test schemas):

- the server against a closed port waited 30 s and exited with status 1 and the message of section 6, with no port open
  meanwhile; against a wrong password, a missing database and a `require` URL on a server without TLS it behaved as
  sections 4 and 6 say; with `REMEDY_DB` set it refused to start;
- terminating the pool's connections on the server left the next requests working;
- `pg_dump --format=custom`, `pg_restore` into an empty database (ids continued) and the failure of a restore onto a
  database that already had the tables.

The token scan of the real-run runbooks: with a plain-text token stored in a `bytea` column of a scratch database,
`scripts/check-no-token-leak.sh` on a text `pg_dump` with the plain token reports nothing (a dump writes the column as hex),
with the hex form of the token (`od -An -tx1 | tr -d ' \n'`) it reports the leak, and on a clean dump it reports none.

During the development of the chart's bundled PostgreSQL, in two throwaway kind clusters (the first with an earlier version
of the chart; a namespace with the restricted Pod Security Standard enforced, deleted afterwards), with the rendered
`postgres.yaml`, `postgres-backup.yaml` and the network policies applied: the StatefulSet became Ready and survived a pod delete; a pod with the control plane's labels
and the control plane's database environment copied from the rendered Deployment (`PGPASSWORD` from the Secret and the URL
without a password), with a password that is not URL-safe (`@`, `/`, `:`, `?`, `#`, `%`, `+`, `=`), connected and read a
row; a Job made from the CronJob wrote a dump; a pod with other labels was blocked by the policy; and the restore pod and
`pg_restore --clean` command of `deploy/chart/README.md` brought a changed row back. That was not the control plane
itself, not installed with `helm install` or Argo CD, and not on k3s.

Not tried: the blocks of this runbook on a cluster (the job wait loop, the tools pod with or without the backup claim, the
copy off the node, the restore of section 3 with `dropdb`/`createdb`, the password change of section 2, the new major
version), the control plane itself against the bundled PostgreSQL, TLS against a real server, and a restart of the server
under a running control plane.

## 10. Upgrading from the SQLite versions

**Before you sync** (better: before you bump the chart version): copy `remedy.db` out (step 1). Under Argo CD, `prune`
can delete the old claim `remedy-data`, and a `local-path` volume loses its data with it.

**Not supported: existing data is not migrated.** The releases before PostgreSQL kept everything in one SQLite file,
`/data/remedy.db`, on the claim `remedy-data` (or the claim named by `server.persistence.existingClaim`). The project is
work in progress, and the maintainer decided on 2026-10-22 that such an installation is reinstalled with an empty
database (`docs/specs/2026-10-22-postgres-only-design.md`, "Existing installations"). A control plane that still has
`REMEDY_DB` set refuses to start and points here.

What a reinstall means:

1. **If the old data matters to you, copy it out first.** The new chart no longer renders the claim `remedy-data`. Under
   Argo CD with `prune: true` it is expected to be deleted (not tried), and for a `local-path` volume deleting the claim
   deletes the data. (A plain `helm upgrade` keeps it: the old chart marked it `helm.sh/resource-policy: keep`.) Stop the
   control plane (`homelab-deploy.md` section 11), then copy the file with a short-lived pod that mounts the claim
   read-only:

   ```sh
   NS=remedy
   CLAIM=remedy-data            # server.persistence.existingClaim of your OLD values, else remedy-data
   kubectl -n "$NS" apply -f - <<EOF
   apiVersion: v1
   kind: Pod
   metadata: {name: remedy-db-copy}
   spec:
     restartPolicy: Never
     securityContext:
       runAsNonRoot: true
       runAsUser: 65532
       runAsGroup: 65532
       fsGroup: 65532
       seccompProfile: {type: RuntimeDefault}
     containers:
       - name: copy
         image: busybox:1.37
         command: [sleep, "3600"]
         securityContext: {allowPrivilegeEscalation: false, capabilities: {drop: [ALL]}}
         volumeMounts: [{name: data, mountPath: /data, readOnly: true}]
     volumes:
       - name: data
         persistentVolumeClaim: {claimName: $CLAIM, readOnly: true}
   EOF
   kubectl -n "$NS" wait --for=condition=Ready pod/remedy-db-copy --timeout=120s
   umask 077
   kubectl -n "$NS" cp remedy-db-copy:/data/remedy.db ./remedy.db
   kubectl -n "$NS" exec remedy-db-copy -- test -e /data/remedy.db-wal && kubectl -n "$NS" cp remedy-db-copy:/data/remedy.db-wal ./remedy.db-wal
   kubectl -n "$NS" delete pod remedy-db-copy
   ```

   Any `sqlite3` reads the copy (`sqlite3 remedy.db .tables`). It holds sealed secrets, all run output and all incident
   text: keep it private and out of every git working tree.
2. **Make the database's Secret** (section 2) and change the values: remove `server.persistence.*`, add
   `postgres.existingSecret` (or `database.existingSecret` and `networkPolicy.database` for a database of your own), and
   bump the chart version. Sync (enable auto-sync again under Argo CD). The control plane starts on the empty database and
   makes its tables.
3. **Enter again on the Setup page** what lived in the old file: the GitHub token and the repositories, the hosts (their
   logins and pinned host keys; with the same login the `sudoers` snippet on the host can stay), skills you wrote, and the settings you had
   changed (among them the Alertmanager connection and its token). Incidents, runs, approvals and the activity feed start
   empty; the pollers open what is still wrong in GitHub and the Alertmanager.
4. **What stays:** the sealed Secret `remedy-secrets` (admin password, runner token, master key) is not in the database and
   can stay as it is. The runner's CLI login lives on the runner's own claim and stays: no new login is needed.
5. The old claim, if it survived (a plain `helm upgrade`, or a claim of your own), is no longer used: delete it by hand
   when you no longer need it.
