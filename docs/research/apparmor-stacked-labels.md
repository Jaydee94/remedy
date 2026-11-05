# Research: containers lose signals between their own processes under AppArmor 5 and kernel 7.0

Date: 2026-11-04. Host: the maintainer's single-node k3s (Ubuntu 26.04, kernel `7.0.0-42-generic`, AppArmor parser 5.0.2, containerd `2.3.4-k3s1.36`, Kubernetes v1.36.5). Everything here was read from the node (`dmesg`, `aa-status`, the pods' logs and events); nothing was changed on the node for this note.

## What was seen

| Workload | Symptom | Where fixed |
|---|---|---|
| `vmagent` | panic `FATAL: cannot send SIGHUP to itself: permission denied` after the config-reloader's first reload, 1378 restarts in 6 days | home-server#546: the annotation `container.apparmor.security.beta.kubernetes.io/vmagent: unconfined` (the `monitoring` namespace enforces no Pod Security level) |
| `vmalert` | its rule reload (`SIGHUP` to itself) denied, 7 denials | same change |
| PostgreSQL 16 (bundled in the Remedy chart) | `WARNING: autovacuum worker took too long to start; canceled` every minute: autovacuum never runs | **open**, see below |
| `frr-k8s`, Argo CD's `helm`, `remedy-server` | about 80 (both days), 68 and 5 denials (the last two counted on 2026-11-02; the kernel log has rotated since). `remedy-server`'s are `SIGURG`, which Go uses for preemption; no effect seen | none |

The audit line is always the same shape:

```
apparmor="DENIED" operation="signal" class="signal" profile="cri-containerd.apparmor.d" comm="postgres"
requested_mask="send" denied_mask="send" signal=urg peer="cri-containerd.apparmor.d//&unconfined"
```

For `comm="postgres"` since boot: 86 × `int`, 29 × `urg`, 4 × `kill` (a `SIGURG` every five seconds, matching the probe interval; PostgreSQL 16 uses `SIGURG` to wake the latch of another process).

## Why

`aa-status` lists the processes of the containers with **stacked** labels, for example `/bin/node_exporter (6223) cri-containerd.apparmor.d//&unconfined` and `bgpd//&cri-containerd.apparmor.d`. The `//&` is AppArmor 5's notation for a profile stacked with another one. The default profile that containerd loads for every container (`cri-containerd.apparmor.d`) allows signals within itself by naming the profile as the peer. A peer whose label is `cri-containerd.apparmor.d//&unconfined` is not that profile, so the kernel denies the signal, even when sender and receiver are two processes of one container. This is the same family as the KubeVirt entry in the home-server's `docs/gotchas.md` (`qemu:cpus_kick_thread: Permission denied`). It is my reading of the audit lines and the process list; the profile text itself was not read (containerd generates it at start and keeps no file under `/etc/apparmor.d`).

## What does not work

- **`appArmorProfile: Unconfined` on the container** (the Remedy chart's `postgres.appArmorProfile`, 0.19.0): the Pod Security Standards `baseline` and `restricted` both forbid it. In the namespace `remedy` (`enforce=restricted`) the StatefulSet could not create `remedy-postgres-0` (`FailedCreate … forbidden AppArmor profile`), and Remedy was down for about twenty minutes on 2026-11-04. It works only in a namespace that enforces `privileged` or nothing (as `monitoring` does), and it takes the container out of AppArmor altogether.
- **Pinning an older image** (vmagent v1.154.1): no help, it panics the same way.

## Options for the PostgreSQL container

1. **Do nothing and vacuum by hand.** The database is small. A monthly `kubectl -n remedy exec remedy-postgres-0 -- psql -U remedy -d remedy -c 'VACUUM (ANALYZE)'` replaces what autovacuum would do. Cost: a chore; bloat in the meantime. Cheapest, no risk.
2. **A `Localhost` AppArmor profile** (allowed by `restricted`): a copy of containerd's default profile under another name, with the signal rule widened to the stacked label (`signal (send,receive) peer=<name>//&unconfined`), installed on the node with `apparmor_parser -r` (the home-server repository has an Ansible role for the host) and referenced by the chart as `appArmorProfile: {type: Localhost, localhostProfile: <name>}`. This fixes the cause for one workload and keeps the container confined. Cost: a security profile to write, review and test on the node, a chart value and a host change; a profile with a mistake can stop the container from starting. Not built; it needs the maintainer's go and a test on the node (load the profile, start the pod, watch `dmesg` for denials).
3. **Wait for a fix upstream** (containerd's default profile or the kernel). Not looked up.

The recommendation is option 1 now, option 2 if autovacuum turns out to matter (watch `pg_stat_user_tables.n_dead_tup`), and never `Unconfined` in the `remedy` namespace.

## What missing autovacuum cost, measured on 2026-11-04

A read of `pg_stat_user_tables` and a `VACUUM (ANALYZE, VERBOSE)` (the first autovacuum never ran: `last_autovacuum` was `never` for every table) showed the cost in one table. `incidents` held 41 rows (2.2 KB each, `details` about 1 KB) in **2089 pages (16 MB)**, its index `incidents_state_seen` was 378 pages, and the vacuum removed 27,157 dead item identifiers; the database was 29 MB. The cause is the poller: `TouchIncidentSignal` rewrites `last_seen`, `host_id` and `details` of every open alert incident once a minute (about 25,000 versions a day), and `last_seen` was in the index `incidents_state_seen`, so no touch could be a HOT update.

Migration `002_incidents_hot_updates.sql` drops that index (nothing needs it for a table of a few dozen rows). On a scratch database with 19 rows of 2 KB, 6000 touches and autovacuum off, the table is 37 pages with the index and 4 without. With HOT updates PostgreSQL clears old versions of a page by itself when the page is read, so this table no longer depends on autovacuum. The other tables are written rarely (`activity` only appends). Option 1 above (a vacuum by hand) is therefore only a chore for the rest. The 2089 pages already allocated stay allocated (a plain `VACUUM` does not shrink the file); they are reused.

## Not verified

That the stacked label is the whole reason: no profile with a widened rule was tried. Whether any bloat has built up (the database was not queried). Whether `SIGURG`/`SIGINT` denials slow PostgreSQL's wake-ups noticeably (a crash recovery on 2026-11-04 took about six minutes, two start attempts, where a start after a clean shutdown took seconds; the cause of the first failed start, `startup process exited with exit code 1`, was not found).
