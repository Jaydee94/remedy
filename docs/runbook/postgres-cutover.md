# Runbook: PostgreSQL, the cutover from SQLite, backups and operation

Status: **not run on the home server.** The importer and the commands below were run by hand against a fixture file and a
scratch database on the test PostgreSQL (see "What was walked" at the end); the kind testbed, the chart on a cluster and the
real `remedy.db` were not touched. Read every step of the cutover as expected, not as tried.

Background: `docs/specs/2026-10-22-postgres-only-design.md` (decisions 7, 9 and 10), `docs/design.md` 2.6. Since this
version the control plane keeps all its state in PostgreSQL. The chart installs a single-node one of its own by default, or
takes a connection to one you provide (`deploy/chart/README.md`, "The database"); the steps below were written for one you
provide. The importer, `remedy-import-sqlite`, is a one-time tool that is kept for one
release.

Contents: 1 what you need, 2 the cutover (steps 0 to 9), 3 rollback, 4 backup and restore, 5 TLS, 6 pool sizing, 7 when
PostgreSQL is down or restarts, 8 what is tested about secrets at rest, 9 the importer in detail, 10 what was walked, 11
operational notes.

## 1. What you need

- A PostgreSQL 16 or newer, an empty database on it, and a role that owns that database (it must be able to create tables,
  indexes and sequences there; Remedy uses no extension). An operator in the cluster (for example CloudNativePG), a
  StatefulSet of your own, or a server on another host (`database.existingSecret`); or the chart's own, the default
  (`postgres.existingSecret`, `deploy/chart/README.md`), which this runbook does not cover.
- **A dedicated, empty database.** Point `REMEDY_DATABASE_URL` at a database (or schema) that holds nothing but Remedy.
  Before it creates anything, the importer checks the connection's current schema. It accepts a schema with no table, or one
  that Remedy's server already created and never used. Any other table makes it stop with `the destination is not a
  dedicated database for Remedy: its schema public has N table(s) that are not Remedy's (...): use a dedicated empty
  database`, and nothing is created there. Create a new database for Remedy (`CREATE DATABASE remedy OWNER remedy;`) and use
  that.
- The connection URL in a Secret of the Remedy namespace (never in `values.yaml`), for example
  `postgres://remedy:PASSWORD@db.databases.svc:5432/remedy?sslmode=require`. A password with special characters must be
  percent-encoded in the URL. `verify-full` needs a CA that the server image trusts; an operator's private CA cannot be given to
  the chart yet, so for a database in the cluster `require` (encrypted, not verified) is what works: section 5. The chart reads the key `url` of the Secret named by `database.existingSecret`; section 4 of
  `homelab-deploy.md` shows how to make it with `kubeseal` without the password reaching a file or a process list.
- The chart values: `database.existingSecret` (and `database.secretKey` if the key is not `url`), and
  `networkPolicy.database` while the network policies are on, in one of two forms: pods in the cluster
  (`namespace`, `podSelector`, `port`) or an external server (`cidr`, `port`). The database's own policy, if it has one, must
  admit the Remedy namespace as well. `server.persistence.*` is gone from the chart: remove it from your values.
- The same `REMEDY_MASTER_KEY` as before. It is not in the database; the importer copies the sealed values without opening
  them, so they open again only with the key that sealed them. With a different key the Setup page shows the GitHub token as
  undecryptable (the control plane logs `the stored GitHub token cannot be decrypted; polling is off`).
- A workstation with `kubectl`. The importer, `/remedy-import-sqlite`, is in the server image of the release that moves the
  control plane to PostgreSQL and of the releases after it (it is kept for one release, section 9); step 5 runs it in a pod
  of that image, next to the database. Check the image you chose with
  `docker run --rm --entrypoint /remedy-import-sqlite "$IMPORT_IMAGE" --help` (it prints the usage and exits with 2). Only for
  an older image that lacks it, step 5 has a fallback that builds the importer from the release tag (Go, no cgo, no UI) and
  reaches the database with `kubectl port-forward`.
- Time to spare: the control plane is down from step 3 until step 7.
- A working directory outside every checkout for the copy of the SQLite file (it holds secrets): the block *Names and a working directory* of section 2 makes it with `mktemp -d`.

## 2. The cutover

Each step needs your go. Nothing here deletes the old volume.

**Names and a working directory (once, at the top).** Everything below uses these variables; a new shell needs them again.

```sh
NS=remedy                         # the Remedy namespace
CLAIM=remedy-data                 # the old claim: server.persistence.existingClaim of your OLD values, else remedy-data
DBSECRET=remedy-database          # the Secret of step 1 (database.existingSecret)
IMPORT_IMAGE=ghcr.io/jaydee94/remedy-server:X.Y.Z    # the release you move to (its image contains /remedy-import-sqlite)
WORK=$(mktemp -d "$HOME/remedy-cutover.XXXXXX")      # mode 700, OUTSIDE every checkout; write the path down
echo "$WORK"
```

**`$WORK` holds a copy of the SQLite file: sealed secrets, all run output and all incident text. Keep it out of every git
working tree (a stray `git add` of a checkout that is public would publish it), keep it private, and delete it in step 9 once the
cutover is accepted.**

Helpers, used by steps 0, 4 and the rollback (`sh -n` checked, not run on a cluster). `helper_pod` starts a pod that mounts
the old claim and sleeps (read-only for `true`, writable for `false`); the namespace enforces the restricted Pod Security
Standard, so it runs as 65532 without privileges (use any image with `sh` and `tar` that you can pull). If the pod does not
become Ready within the time limit it is removed again, so that a later call does not meet a stale pod. `copy_out` copies the
database files out of the claim into a directory (`copy_files` does the copying) and removes the pod on every path:

```sh
helper_pod() {   # helper_pod true|false
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
      volumeMounts: [{name: data, mountPath: /data, readOnly: $1}]
  volumes:
    - name: data
      persistentVolumeClaim: {claimName: $CLAIM, readOnly: $1}
EOF
  kubectl -n "$NS" wait --for=condition=Ready pod/remedy-db-copy --timeout=120s || {
    kubectl -n "$NS" describe pod remedy-db-copy
    kubectl -n "$NS" delete pod remedy-db-copy --ignore-not-found --wait
    return 1
  }
}

copy_files() {   # copy_files <directory>: the copying of copy_out, with the helper pod running
  kubectl -n "$NS" exec remedy-db-copy -- ls -l /data
  kubectl -n "$NS" cp remedy-db-copy:/data/remedy.db "$1/remedy.db" || return 1
  if kubectl -n "$NS" exec remedy-db-copy -- test -e /data/remedy.db-wal; then
    kubectl -n "$NS" cp remedy-db-copy:/data/remedy.db-wal "$1/remedy.db-wal" || return 1
  fi
  kubectl -n "$NS" exec remedy-db-copy -- sha256sum /data/remedy.db
  shasum -a 256 "$1/remedy.db"     # Linux: sha256sum; the two values are equal when the server is down
}

copy_out() {   # copy_out <directory>
  mkdir -p "$1" && helper_pod true || return 1
  copy_files "$1"; rc=$?
  kubectl -n "$NS" delete pod remedy-db-copy --ignore-not-found --wait
  return $rc
}
```

**Step 0. Copy the volume, then bring the SQLite file to schema 018.** First copy: the upgrade below makes the bridge release
migrate the only SQLite file in place, and nothing else would exist if that went wrong. The server still runs, so this copy is
rough (the `-wal` may be mid-write); it is a safety net, not the import source.

```sh
copy_out "$WORK/before-upgrade"
```

Then upgrade. The importer accepts exactly schema 018, whose last migration is `018_incident_reopen.sql`; that migration ships
with the alert-recurrence release (pull request 174). Upgrade the server to a release that contains it (the last release that
runs on SQLite, the "bridge" release) and let it start once on SQLite, so that it applies 018 to its database file. Note the
release you ran: it is your rollback target. A file without 018 is refused by the importer itself, and its message names the
missing migration (`missing: 018_incident_reopen.sql`), so the dry run of step 5 tells you if this step was skipped.

**Step 1. Prepare PostgreSQL and the chart values, and do not deploy yet.** Create the database and the role, make the
Secret with the URL, and write the values change (remove `server.persistence`, add `database` and `networkPolicy.database`)
and the bump of the chart version in a branch. Rendering it is a good check (`helm template`); merging it is step 7.

```sh
# as a PostgreSQL superuser, for example:  psql -h db.example -U postgres
CREATE ROLE remedy LOGIN;
\password remedy
CREATE DATABASE remedy OWNER remedy;
```

(`\password` asks for the password without echoing it.)

**Step 2. Protect the old volume.** The new chart no longer renders the claim. Under Argo CD with `prune: true` a resource
that the manifests stopped producing may be pruned, and for a `local-path` volume deleting the claim deletes the data. The old
chart marked the claim `helm.sh/resource-policy: keep`; whether Argo CD honors that annotation was not checked, so do not rely
on it. (Without Argo CD, with plain `helm upgrade`, that annotation does keep the claim: Helm leaves a resource with it alone.
The volume step below is still cheap insurance.)

```sh
kubectl -n "$NS" annotate pvc "$CLAIM" argocd.argoproj.io/sync-options=Prune=false
PV=$(kubectl -n "$NS" get pvc "$CLAIM" -o jsonpath='{.spec.volumeName}'); echo "$PV" | tee "$WORK/pv-name"
kubectl patch pv "$PV" -p '{"spec":{"persistentVolumeReclaimPolicy":"Retain"}}'
```

The first keeps Argo CD from pruning the claim, the second keeps the data even if the claim is deleted (the rollback uses the
volume's name from `$WORK/pv-name`). Check that both are in place (`kubectl -n "$NS" get pvc "$CLAIM" -o yaml`,
`kubectl get pv "$PV"`) before you go on.

**Step 3. Scale the control plane to zero.** With `selfHeal` Argo CD scales it back at once, so disable auto-sync on the
Application first (`homelab-deploy.md`, section 11, "Stopping the control plane"). Then
`kubectl -n "$NS" scale deployment/remedy-server --replicas=0` and wait until the pod is gone. Nothing may write the file while
it is copied or imported. The runner can stay up; it retries until the control plane is back.

**Step 4. Copy the database out, with the server down.** This is the consistent copy: your insurance for the rollback, and the
source of the fallback import of step 5.

```sh
copy_out "$WORK"
```

The `sha256sum` of the pod and the `shasum` of the copy must be equal. Copy both files from the same moment; the `-wal` exists
only if the server was not shut down cleanly.

**Step 5. Rehearse.** The importer is in the server image of the release you move to (`/remedy-import-sqlite`, next to
`/remedy-server`; the image's entrypoint stays the server, the pod below replaces it with `command`). A pod of that
image runs it inside the cluster, so the database URL comes straight from the Secret and never reaches your workstation. The
pod's init container (busybox) copies the files from the old claim, mounted read-only, into a scratch `emptyDir`, because
SQLite needs a writable directory next to the file (it may create a `-shm`); the importer then reads that scratch copy. Nothing
is written to the old claim.

```sh
import_pod() {   # import_pod dry|real
  ARGS='"--from=/work/remedy.db"'
  [ "$1" = dry ] && ARGS="$ARGS, \"--dry-run\""
  [ -n "$REPLACE_INVALID_TEXT" ] && ARGS="$ARGS, \"--replace-invalid-text\""   # only after a refusal, see "Text the importer refuses"
  kubectl -n "$NS" delete pod remedy-import --ignore-not-found --wait
  kubectl -n "$NS" apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata: {name: remedy-import, labels: {app.kubernetes.io/name: remedy-import}}
spec:
  restartPolicy: Never
  securityContext:
    runAsNonRoot: true
    runAsUser: 65532
    runAsGroup: 65532
    fsGroup: 65532
    seccompProfile: {type: RuntimeDefault}
  initContainers:
    - name: copy
      image: busybox:1.37
      command: [sh, -c, "cp /data/remedy.db /work/ && { [ ! -e /data/remedy.db-wal ] || cp /data/remedy.db-wal /work/; } && ls -l /work"]
      securityContext: {allowPrivilegeEscalation: false, capabilities: {drop: [ALL]}}
      volumeMounts: [{name: data, mountPath: /data, readOnly: true}, {name: work, mountPath: /work}]
  containers:
    - name: import
      image: $IMPORT_IMAGE
      command: ["/remedy-import-sqlite"]
      args: [$ARGS]
      env:
        - name: REMEDY_DATABASE_URL
          valueFrom: {secretKeyRef: {name: $DBSECRET, key: url}}
      securityContext: {allowPrivilegeEscalation: false, capabilities: {drop: [ALL]}}
      volumeMounts: [{name: work, mountPath: /work}]
  volumes:
    - name: data
      persistentVolumeClaim: {claimName: $CLAIM, readOnly: true}
    - name: work
      emptyDir: {}
EOF
  n=0
  while :; do   # wait at most 5 minutes for the pod to leave Pending (the image pull, the Secret, the claim, the init copy)
    phase=$(kubectl -n "$NS" get pod remedy-import -o jsonpath='{.status.phase}') || return 1
    [ "$phase" = Pending ] || break
    n=$((n + 1))
    if [ "$n" -gt 60 ]; then
      echo "remedy-import is still Pending after 5 minutes:" >&2
      kubectl -n "$NS" describe pod remedy-import
      phase=$(kubectl -n "$NS" get pod remedy-import -o jsonpath='{.status.phase}') || return 1
      [ "$phase" = Pending ] || break   # it started in the meantime: wait for it as usual
      if [ "$1" = real ]; then   # it never started, and it must not start later with nobody reading its report
        kubectl -n "$NS" delete pod remedy-import --wait
        echo "remedy-import never started and is deleted; nothing was imported. Fix what the events show and run import_pod real again." >&2
      fi
      return 1
    fi
    sleep 5
  done
  while [ "$phase" = Running ]; do
    sleep 3
    phase=$(kubectl -n "$NS" get pod remedy-import -o jsonpath='{.status.phase}') || return 1
  done
  kubectl -n "$NS" logs pod/remedy-import --all-containers | tee "$WORK/import-$1.txt"
  [ "$phase" = Succeeded ]
}

import_pod dry
```

(The key of the Secret is `url`; if you set `database.secretKey`, use that. A pod that stays `Pending` for five minutes ends
the function with the pod's events: an `ImagePullBackOff`, a Secret or key that does not exist
(`CreateContainerConfigError`) or a claim that cannot be mounted shows there. For `dry` the pod is left for you to look at (it
only reads, and the next `import_pod` deletes it). For `real` the function deletes the pod after the events are printed,
provided it is still `Pending`: a pod that has not started cannot have imported anything, and one that stayed could start
later with nobody reading its report, after which the retry would be refused with `the destination database is not empty`.
If the pod started in the meantime, the function waits for it as usual. If you ever find that a real import ran unobserved,
read `kubectl -n "$NS" logs pod/remedy-import --all-containers` before you delete the pod, and treat the destination as
populated: compare its row counts with the dry run's report.)

*Network policies.* The pod carries one label, `app.kubernetes.io/name: remedy-import`. The chart's policies select pods by
`app.kubernetes.io/name: remedy` plus an instance and a component (`remedy.selector` in `deploy/chart/templates/_helpers.tpl`),
so none of them matches it, and it reaches the database unless the namespace has a default-deny policy of its own. Check with
`kubectl -n "$NS" get networkpolicy`: a policy with `podSelector: {}` and `Egress` in its `policyTypes` is a default-deny
(the chart's own policies select only its server, runner and token-refresher pods). If there is one, apply the policy below
before `import_pod`, in the form that matches your `networkPolicy.database` (the values are the example of the chart README:
use your own `namespace`, `podSelector`/`cidr` and `port`; `kube-system` is the chart's `networkPolicy.dnsNamespace` default),
and delete it in step 9 with the pod. It selects only the import pod and has no ingress rule.

```sh
# database pods in the cluster (networkPolicy.database: namespace, podSelector, port)
kubectl -n "$NS" apply -f - <<EOF
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: remedy-import}
spec:
  podSelector:
    matchLabels: {app.kubernetes.io/name: remedy-import}
  policyTypes: [Egress]
  egress:
    - to:
        - namespaceSelector:
            matchLabels: {kubernetes.io/metadata.name: kube-system}
          podSelector:
            matchLabels: {k8s-app: kube-dns}
      ports:
        - {protocol: UDP, port: 53}
        - {protocol: TCP, port: 53}
    - to:
        - namespaceSelector:
            matchLabels: {kubernetes.io/metadata.name: databases}
          podSelector:
            matchLabels: {cnpg.io/cluster: remedy}
      ports:
        - {protocol: TCP, port: 5432}
EOF

# or an external server (networkPolicy.database: cidr, port); the same, with the last rule replaced
kubectl -n "$NS" apply -f - <<EOF
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: remedy-import}
spec:
  podSelector:
    matchLabels: {app.kubernetes.io/name: remedy-import}
  policyTypes: [Egress]
  egress:
    - to:
        - namespaceSelector:
            matchLabels: {kubernetes.io/metadata.name: kube-system}
          podSelector:
            matchLabels: {k8s-app: kube-dns}
      ports:
        - {protocol: UDP, port: 53}
        - {protocol: TCP, port: 53}
    - to:
        - ipBlock: {cidr: 192.168.178.20/32}
      ports:
        - {protocol: TCP, port: 5432}
EOF
```

Do **not** give the pod the server's labels (`app.kubernetes.io/name: remedy`, `app.kubernetes.io/instance: <release>`,
`app.kubernetes.io/component: server`) to borrow the chart's rule: at this step the old chart is still deployed, and its
`remedy-server` policy has no rule for PostgreSQL, so the labels would block the pod instead (if the chart's network
policies are on). The database's own policy, if it has one, must admit the Remedy namespace (step 1). A dry run copies and reads back everything inside the transaction and then
rolls it back. The report lists the rows per table and the last id of each sequence and ends with a line that starts with
`Dry run:`. No row is written, and the sequences are not set; an empty database does get the schema (the server's migrations
run when the importer connects), which the real import then finds in place. Compare the row counts with what you expect (runs,
incidents, hosts). A failure names the table and the row; section 9 lists what the importer refuses.

*Text the importer refuses.* A plain `--dry-run` may stop with `nothing was imported: copy <table>.<column>: text with a NUL
character, which PostgreSQL cannot store; run again with --replace-invalid-text ...` (or `text that is not valid UTF-8; ...`),
naming the row by its key. SQLite stored such bytes (a NUL in a pod log or in host output, for example); PostgreSQL refuses
them in a text column. Only in that case, run the dry run again with `--replace-invalid-text`: it is one more element of the
pod's `args`, and `import_pod` adds it when the variable `REPLACE_INVALID_TEXT` is set (the fallback below takes the flag on
its command line).

```sh
REPLACE_INVALID_TEXT=1       # only after the plain dry run refused text; leave it set for step 6, unset it otherwise
import_pod dry                # args: ["--from=/work/remedy.db", "--dry-run", "--replace-invalid-text"]
```

Each NUL byte and each invalid UTF-8 sequence then becomes U+FFFD, the replacement character, exactly as the server itself
stores such text from now on. Sealed values and other bytes (the `bytea` columns) are never changed. The output lists, per
table and column, how many values were changed (counts only, never the values) and says `N values changed: replacement
character (U+FFFD) ...`. Check that the counts are plausible (they should be in result, log and note columns, not in names or
settings), then run the import with the same flag (step 6, with `REPLACE_INVALID_TEXT` still set). Two keys that would become
equal through the replacement are refused (`the SQLite file has two rows with key ...`), never merged.

*Fallback, only for an older image that has no `/remedy-import-sqlite` (the check of section 1 says so):* build the importer
from the tag of the release you move to on your workstation (Go only, no cgo, no UI) and reach the database through a
port-forward. The URL of this path is **not** the
Secret's: the in-cluster host name does not resolve on your workstation, so it must point at `127.0.0.1` and the forwarded port,
with the same user and database.

```sh
git clone --depth 1 --branch vX.Y.Z https://github.com/Jaydee94/remedy.git "$WORK/src"
( cd "$WORK/src" && go build -o "$WORK/remedy-import-sqlite" ./cmd/remedy-import-sqlite )
kubectl -n databases port-forward svc/DB-SERVICE 55432:5432 &      # or pod/NAME; leave it running
read -rs REMEDY_DATABASE_URL && export REMEDY_DATABASE_URL
# paste: postgres://remedy:PASSWORD@127.0.0.1:55432/remedy?sslmode=require     and press Enter; nothing is echoed
"$WORK/remedy-import-sqlite" --from "$WORK/remedy.db" --dry-run     # add --replace-invalid-text only after a refusal of text
```

The URL goes in the environment only (a password in an argument shows in `ps`; the importer refuses a URL given as an
argument). Through a port-forward `sslmode=verify-full` fails unless the certificate names `127.0.0.1`; `verify-ca` works with
`sslrootcert=/path/on/your/workstation/ca.crt` (the file is read on the machine that runs the importer, so a private CA is
possible here, unlike in the chart), and `require` encrypts without verifying.

**Step 6. Import.** The same, without the dry run: `import_pod real`, with `REPLACE_INVALID_TEXT` still set if the dry run
needed `--replace-invalid-text` (fallback: the same command without `--dry-run`, then
`kill %1` for the port-forward and `unset REMEDY_DATABASE_URL`). The report ends with a line that starts with `Imported.`; it
is saved in `$WORK/import-real.txt` (row counts only, no secrets): keep it for the record. The command exits non-zero on any
error (1 for a failure, 2 for a usage error) and then has written nothing. A second run into the now filled database is
refused (`the destination database is not empty`), which is the protection against importing twice. Afterwards
`kubectl -n "$NS" delete pod remedy-import`.

**Step 7. Deploy the PostgreSQL version.** Merge the change of step 1 (the chart version of the release that reads
`REMEDY_DATABASE_URL`, `database`, `networkPolicy.database`), enable auto-sync again and let Argo CD apply it. The control
plane starts, makes no change to the imported schema (the migrations are recorded already) and listens. If it does not
become Ready, read its log (`kubectl -n "$NS" logs deploy/remedy-server`): section 7 explains what the messages mean.

**Step 8. Check.**

- The log has `connecting to the database` (it names `host:port/dbname` only), then `control plane listening`.
- Setup: the GitHub connection shows `ok` with the same last four characters as before, the runner connects, and the settings
  you changed on the page are there. An undecryptable GitHub token means a different master key.
- Hosts: open a host and use its connection test: it opens the sealed login, so it proves that the host credentials survived.
- Today and the activity feed show the old entries in the old order, and the first new entry appears at the top. The counts
  of incidents, runs and hosts equal the importer's report.
- An approval and a diagnosis exercise the gatekeeper and the responder on PostgreSQL. A real diagnosis or approval runs the
  real CLI and **costs subscription quota**; whether to spend it now is your choice. An ad-hoc question on Ask Remedy is the
  smallest such run. Nothing in this runbook requires it, but nothing else proves the claim of a run and the approvals on
  your data.

**Step 9. Accept, then clean up.** When the checks are good and you will not roll back: delete the working directory, which
holds the SQLite copies until then (`rm -rf "$WORK"`; on a machine with backups of `$HOME`, remember that they may hold it too).
Delete the import pod and, if you applied one in step 5, its policy:
`kubectl -n "$NS" delete pod remedy-import --ignore-not-found; kubectl -n "$NS" delete networkpolicy remedy-import --ignore-not-found`.
Leave the old claim where it is. Delete it only when you have taken a `pg_dump` (section 4), restored it once somewhere and
decided that you will not roll back.

## 3. Rollback

The old volume is untouched, so a rollback is a redeploy of the old release. It loses what PostgreSQL got after step 7.

1. Disable auto-sync and scale the control plane to zero (`homelab-deploy.md`, section 11, "Stopping the control plane").
2. Put the previous values and the previous chart version back (the release you noted in step 0; reverting the pull request
   of step 7 does it) and sync. The old chart renders the claim again and the old server opens `/data/remedy.db`. Use exactly
   the release of step 0: the file is at schema 018.
3. If the claim was deleted after all (not tried; the names are those of the cutover, `$NS`, `$CLAIM` and `$WORK`):
   - **The volume still exists** (step 2 set it to `Retain`, so it shows as `Released`): free it and bind a new claim to it
     before the old chart makes an empty one. The data on it is the state of step 3.

     The new claim must ask for the **same access modes as the OLD claim**: the old server mounts it as the old chart set it
     up (a claim the old chart made is `ReadWriteOnce`; one you brought as `server.persistence.existingClaim` is whatever
     you made it). The retained volume lists the modes it supports; read them first and check them against the old claim,
     which you may have in your values or in Argo CD's history:

     ```sh
     PV=$(cat "$WORK/pv-name")
     kubectl get pv "$PV" -o jsonpath='{.spec.accessModes}'; echo     # for example ["ReadWriteOnce"]
     ```

     Then free the volume and bind the claim to it (the manifest takes the modes from the volume, which is right when they
     equal the old claim's; otherwise type the old claim's modes in place of the `$(...)`):

     ```sh
     kubectl patch pv "$PV" --type=json -p '[{"op":"remove","path":"/spec/claimRef"}]'
     kubectl -n "$NS" apply -f - <<EOF
     apiVersion: v1
     kind: PersistentVolumeClaim
     metadata: {name: $CLAIM}
     spec:
       accessModes: $(kubectl get pv "$PV" -o jsonpath='{.spec.accessModes}')
       volumeName: $PV
       storageClassName: "$(kubectl get pv "$PV" -o jsonpath='{.spec.storageClassName}')"
       resources: {requests: {storage: $(kubectl get pv "$PV" -o jsonpath='{.spec.capacity.storage}')}}
     EOF
     ```

   - **The volume is gone too:** let the old chart make an empty claim, keep the control plane at zero, and put the copy of
     step 4 into it with the helper of the cutover (writable this time):

     ```sh
     kubectl -n "$NS" delete pod remedy-db-copy --ignore-not-found --wait    # a helper pod that an earlier call left behind
     helper_pod false
     kubectl -n "$NS" cp "$WORK/remedy.db" remedy-db-copy:/data/remedy.db
     [ -e "$WORK/remedy.db-wal" ] && kubectl -n "$NS" cp "$WORK/remedy.db-wal" remedy-db-copy:/data/remedy.db-wal
     kubectl -n "$NS" delete pod remedy-db-copy --ignore-not-found --wait
     ```

     That copy (do not delete `$WORK` before you have accepted the cutover) is the second source of the rollback.
4. Scale the old control plane to one (`kubectl -n "$NS" scale deployment/remedy-server --replicas=1`), check it, and enable
   Argo CD's auto-sync again.

**The limit:** data written to PostgreSQL after the cutover is not carried back. There is no export to SQLite. Everything
that happened since step 7 is lost to the old installation: new runs, approvals and their results, diagnoses, notes, hosts
and skills you added or confirmed, changes on the Setup page. The pollers see the current state of GitHub and the
Alertmanager again and reopen what is still wrong; the rest you redo by hand. Keep the Postgres database as it is for a
look, or drop it before a second attempt: the importer writes only into an empty database.

## 4. Backup and restore

The database is the only state of the control plane. The runner's volume holds the CLI login only, which you make again with
the login of the Setup page or `homelab-deploy.md` section 7. The master key is not in the database; keep it apart, and keep
it with the backups' owner: a backup without the key restores everything except the sealed values.

Backup, from anywhere that reaches the database (a CronJob of your own, or the operator's backup feature):

```sh
PGPASSWORD=... pg_dump --format=custom --file=remedy-$(date +%F).dump --host=db.example --username=remedy --dbname=remedy
```

Give the password through `PGPASSWORD` or a `~/.pgpass` file, not inside a connection string argument: an argument shows in
`ps` on a shared machine (`pg_dump` does not read `REMEDY_DATABASE_URL`). TLS settings travel as `PGSSLMODE` and
`PGSSLROOTCERT`. The control plane can run during the backup: `pg_dump` takes a consistent snapshot. Take one before every
upgrade and, if you like, right before step 7 of the cutover.

Restore into an **empty** database, with the control plane scaled to zero. If the target is the database in use, take a fresh
dump first and keep it: dropping and recreating it destroys what is there.

```sh
createdb remedy_restored                      # or CREATE DATABASE; the same owner role
pg_restore --dbname=remedy_restored --no-owner --exit-on-error remedy-2026-10-22.dump
```

then point the Secret's URL at it (or restore into the database the Secret already names, after dropping and recreating it)
and start the control plane. Do not start the control plane on the target before the restore: it would make the tables and
`pg_restore` would stop at the first `relation ... already exists` (`--clean --if-exists` drops them first). The id
sequences are part of the dump, so the next id continues above the highest one. This round trip (dump, restore into an empty
database, new rows continue the ids) was run by hand; a restore onto a cluster you actually depend on was not.

A dump contains the sealed values as bytes and everything else as plain text: run output, incident text, host names. Treat the
file like the database.

## 5. TLS to the database

The URL carries the settings: `sslmode` and `sslrootcert` (and `sslcert`/`sslkey` for client certificates). What the driver
does (`pgx` v5.11, read in its source):

- `sslmode` unset means `prefer`: it tries TLS, **does not verify the server** and falls back to plain text. `require`
  encrypts but does not verify either. Only `verify-ca` and `verify-full` check the certificate; `verify-full` also checks the
  host name against the certificate. Use `verify-full` for anything that leaves a trusted network.
- `sslrootcert=/path/ca.crt` names a PEM file with the CA. `sslrootcert=system` (and `verify-full` with no `sslrootcert`) uses
  the root certificates of the container image, which fits a server whose certificate was issued by a public CA.
- **The chart cannot give the pod a CA file.** It has no `extraVolumes` value or any other way to mount a file for this
  (the Secret of the database reaches the pod as the environment variable `REMEDY_DATABASE_URL` only), so
  `sslrootcert=/path` has nothing to read and the control plane fails at start. A private CA (the usual case
  for a database operator in the cluster) is therefore not supported by the chart yet. Until it is: `sslmode=require` (encrypted,
  not verified: it keeps a passive observer out, not an active one on the pod network), or a server whose certificate
  chains to a public root, or `disable` on a network you trust. Mounting a CA through the chart is a follow-up, not done.
- A server that has no TLS while the URL says `require` or `verify-*` does not fail with a TLS error: the control plane
  keeps retrying for 30 s and then reports `cannot reach the database: context deadline exceeded`. If that is what you see and
  the server is reachable, check the `sslmode` first. A wrong password or a missing database fails at once with the server's
  own message (`password authentication failed`, `database "..." does not exist`).
- The README's quick start uses `sslmode=disable` for a container on the same machine only.

## 6. Pool sizing

`REMEDY_DATABASE_MAX_CONNS` is the most connections the control plane opens, 2 to 100, default 10 (set it through
`server.env` in the chart; a value outside the range makes the server exit at start). It is one process serving one operator's
installation: 10 is plenty. Raise it only if requests are slow while every connection is busy (`SELECT count(*) FROM
pg_stat_activity WHERE datname = 'remedy'` shows the pool's size), and then keep PostgreSQL's `max_connections` (default 100)
above the pool plus whatever else connects: `psql`, backups, the importer (2). Connections are recycled after 30 minutes.

Connect to the server directly or through a pooler in **session** mode. A pooler in transaction mode (for example PgBouncer's
default) was not tried and is expected to break the migration runner's session-level advisory lock and the driver's prepared
statements.

## 7. When PostgreSQL is down or restarts

- **`/healthz` does not depend on the database.** It answers `{"status":"ok"}` whenever the process runs, so a pod's
  probe on it stays green during a database outage.
- **Down at start.** The server logs `connecting to the database` and keeps trying for 30 s with growing pauses (1 s up to
  5 s), listening on no port meanwhile (the pod is not Ready). Then it logs
  `cannot open database ... err="store: cannot reach the database: ..."` and exits with status 1; Kubernetes restarts the pod
  (CrashLoopBackOff with growing delays) until the database is back. A signal during the wait ends it at once.
- **Down or restarted while running.** The pool does not log its own reconnects. Connections that were cut are replaced by new
  ones on the next use. Requests that arrive while the database cannot be reached answer 500 with a short text (for example
  `could not load the connection`), and the background loops log the failed step and try again on their next cycle (for
  example `cannot load the GitHub connection`, `cannot list repos`), with the driver's error. When the database is back they
  recover by themselves, no restart of the control plane is needed. A request or a transaction that was in flight at the
  moment fails; the activity entry of a state change is written in the same transaction as the change, so a failed one leaves
  neither. Terminating all the pool's connections on the server (`pg_terminate_backend`) was tried against a running control
  plane: the next requests worked and the log said nothing. A full outage of the server under a running control plane was
  not tried.
- A **run** that is `running` while the database is away records no heartbeat. The reaper judges runs by their last
  heartbeat (2 minutes with gatekeeper access, 15 without), so an outage longer than that is expected to fail runs that were
  fine once the database is back (expected from the code, not tried).

## 8. What is tested about secrets at rest

With SQLite the tests also scanned the database file and its `-wal` for plaintext secrets. There is no file any more, and what
PostgreSQL leaves behind is not visible to a `SELECT`:

- **Covered:** the whole-chain test of the login relay (`internal/app/loginrelay_chain_test.go`) reads every column of every
  row of every table through a second connection, `bytea` columns included, and fails if the planted login material appears in
  any of them or in any API answer, log line or event; the host tests (`internal/server/hosts_test.go`) do the same walk over
  the `hosts`, `host_credentials` (without the sealed column, which is ciphertext) and `activity` tables for the planted
  passwords and keys. Other secrets are covered by the tests of their own packages (sealing, the write-only API fields), not
  by a walk over the database.
- The real-run runbooks (`first-real-run.md`, `gatekeeper-real-run.md`, `cluster-real-run.md`) also scan a text `pg_dump` for the token. A dump shows a `bytea` column as hex, so
  they search for the hex form of the token as well (`od -An -tx1 | tr -d ' \n'`); that finds a plain-text token stored in a `bytea` column, and nothing else.
- **Not covered:** the storage level. Dead tuples that VACUUM has not removed, the write-ahead log, temporary files, backups
  and `pg_dump` files, replicas, and the server's swap can hold old values (a token that was replaced, a cleared field). No test
  reads those, and no test can from SQL. Disk encryption, the WAL and backup retention, and who may read them are the
  operator's concern, the same as for any database that holds credentials. The guarantee rests on the sealing and on the tests that walk every column, not on a scan of a file or a dump. Sealed values (the GitHub token, host logins, the
  Alertmanager and metrics tokens) are ciphertext in all of those places; the plaintext fields (run output, incident text,
  agent answers) are not secrets by design and are redacted before storage where they could hold one.

## 9. The importer in detail

`remedy-import-sqlite --from <file> [--dry-run] [--replace-invalid-text]` copies a SQLite file of Remedy into an empty PostgreSQL database, ids
unchanged, so that every sealed value (host credentials, the GitHub token, the Alertmanager token) still opens with the same
`REMEDY_MASTER_KEY`. It reads the database URL from `REMEDY_DATABASE_URL` only (never from an argument: a password in argv
shows in `ps`) and never prints it.

- It reads the file read-only, in one read transaction, and does not change the contents of the file or its `-wal`. The
  copy may gain an empty `-wal` or a `-shm` next to it (SQLite's index of the `-wal`, not data), and an existing `-shm` may be
  updated. A symlink is followed: the checks and the open use the file it points at.
- It needs a quiet copy: nothing may write the file while it is imported. It refuses a file another process on the same
  machine has open (`another process has the SQLite file open`), such as a server that still runs on it. That check only
  sees processes on the same machine, not a server on another node that has the file on a network volume, so scaling the
  server to zero is required in every case, check or not.
- The file's tables and columns must be exactly the ones of schema 018. An extra or missing table or column is refused, so
  nothing is dropped silently.
- It reads exactly SQLite schema 018 (the 18 migrations up to `018_incident_reopen.sql`). A file at another schema is refused
  with the names that are missing or unknown: upgrade the server through the last release that runs on SQLite (step 0), let
  it start once so that it migrates the file, and copy the file again.
- It refuses a destination that is not a dedicated database for Remedy (a table that is not Remedy's in the current schema)
  before it creates anything (section 1). It refuses a destination that has a row in any table, and names the first such table. It makes the schema itself (the
  server's own migrations, as on a first start) when the database is empty, also for a dry run.
- It locks every table of the destination before it checks that they are empty. `lock_timeout` is 10 s per lock, and there
  are 14 tables, so with a server still connected it can wait up to about 14 x 10 s before it gives up.
- It writes everything in one transaction. Any failure (a value PostgreSQL refuses, a lost connection, Ctrl-C) rolls back
  every row; the database is as empty as before and the import can be run again.
- After the copy it reads every table back from PostgreSQL: the row count and the set of ids equal the file's, and every row
  equals what was read from the file and converted. That catches a row or value lost on the way into PostgreSQL. It does not
  check the conversion from SQLite's values itself; the importer's tests pin that, by dumping the file and the database
  independently and comparing them. Then it sets each id sequence to the higher of the highest id and SQLite's high-water
  mark, so the id of a deleted row is never given again.
- A text that PostgreSQL cannot store (a NUL character, invalid UTF-8) makes the import fail with the table, the column and
  the row's key, unless `--replace-invalid-text` is given (step 5, "Text the importer refuses"); two host or repository names
  that differ only in the case of a non-ASCII letter fail it too (SQLite's `NOCASE` folds ASCII only; PostgreSQL's `lower()`
  may fold more, depending on the database's locale); nothing is written. Fix such a row in a copy of the file with `sqlite3`,
  and import again.

The binary is built by `make build` and `make build-go` (`bin/remedy-import-sqlite`).

## 10. What was walked

By hand, with the importer and the server built from this tree, on the test PostgreSQL of `dev/pg/` (a scratch database, not
the shared test schemas) and a SQLite file built from the 18 migrations in `internal/importsqlite/testdata/sqlite-migrations/`
with a GitHub connection in it:

- step 5 and 6 (`--dry-run`, the import, and the refusal of a second import into the filled database) gave the reports and
  exit codes described above;
- the server started on the imported database, logged `connecting to the database` and `control plane listening`, answered
  `/healthz`, and with a different master key logged `the stored GitHub token cannot be decrypted; polling is off`;
- the server against a closed port waited 30 s and exited with status 1 and the message of section 7, with no port open
  meanwhile; against a wrong password, a missing database and a `require` URL on a server without TLS it behaved as section 5
  and 7 say; with `REMEDY_DB` set it refused to start;
- terminating the pool's connections on the server left the next requests working;
- `pg_dump --format=custom`, `pg_restore` into an empty database (ids continued) and the failure of a restore onto a
  database that already had the tables.

Also checked without a cluster: every shell block of this runbook passes `sh -n`; the functions `helper_pod`, `import_pod` and the
claim of the rollback were run against a stand-in `kubectl` that records what it is given, and the manifests they produce parse as
YAML with the intended values (read-only or writable mount, `--dry-run` or not, the Secret reference). And the token scan of the
real-run runbooks: with a plain-text token stored in a `bytea` column of a scratch database, `scripts/check-no-token-leak.sh` on
a text `pg_dump` with the plain token reports nothing (a dump writes the column as hex), with the hex form of the token
(`od -An -tx1 | tr -d ' \n'`) it reports the leak, and on a clean dump it reports none.

Not walked: steps 0 to 4 and 7 to 9 on a cluster (the helper pod, the import pod, the Secret, Argo CD, the chart, the Setup
page in a browser), the fallback's port-forward, TLS against a real server, the rollback, and anything with the maintainer's
data. The importer is in the image (`Dockerfile`); `docker run --rm --entrypoint /remedy-import-sqlite <image> --help` was run on
an image built from this tree and printed the usage and exited with 2. No release has been published with it yet, so the pod
of step 5 has not run on a released image either.

## 11. Operational notes

- **One database per installation.** The store's advisory locks belong to the database, not to a schema: two Remedy
  installations on one database (on different schemas) would serialise each other's activity writes, diagnosis starts and
  skill changes. That is not supported. Give each installation a database of its own.
- **Activity-writing commits run one after the other.** Every transaction that writes an activity entry takes a lock right
  before its commit and keeps it until the commit has finished, so that the feed's ids stay in commit order. Their rate is
  bounded by one commit (one WAL flush) at a time: some hundreds per second, far above the few entries a minute Remedy
  writes. Nothing to do at this scale.
- **Keep `deadlock_timeout` at 200 ms or more** (PostgreSQL's default is 1 s). A delete that cascades (a host, a skill, a
  repository, the GitHub connection) gives up a lock wait after 50 ms and tries again, which only works while the server's
  deadlock check comes later than that. The control plane reads the setting at start and logs a warning when it is below 200
  ms: `the database's deadlock_timeout is low: a delete that waits for a lock may end in a deadlock error instead of trying
  again` (with `deadlockTimeoutMS` and `recommendedMinimumMS`). The server works either way; only a rare wait cycle of a
  delete may then end in a deadlock error (`40P01`).
- **"store: the database is busy; try again"** means a delete (of the kinds above) ran out of its 40 attempts, about four
  seconds, because other transactions held the rows it needs. Nothing was deleted, not even part of it; do the same delete
  again a moment later. If it keeps happening, look for a long transaction (`SELECT * FROM pg_stat_activity WHERE state <>
  'idle'`).
- **Busy deletes answer 503.** Removing a repository, the GitHub connection, a host or a skill may, rarely, find its rows held
  by other work for about four seconds. The control plane then answers `503 Service Unavailable` with `Retry-After: 2` and
  `{"error":"the database is busy; try again"}`. Nothing was deleted: repeat the action a moment later.

The reasoning and the other limits are in `docs/research/postgres-concurrency-audit.md`, "Operational notes".
