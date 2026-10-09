# S1: a login directory on the host, mounted into a kind node, used by a non-root pod

Spike S1 of `docs/plans/k8s-0-spikes.md`. Question: can the runner pod (uid 65532, non-root) write into a host directory
that kind mounts into its node and a `hostPath` PersistentVolume exposes, so that the CLI login survives
`kind delete cluster`? Which mode does the host directory need, who owns the files, and what does K-4 have to do?

Measured 2026-10-07 on this Mac (macOS, arm64) with Docker Desktop 29.8.1, kind v0.33 (node image `kindest/node:v1.37.0`).
Nothing in the directory is a credential: the probe file holds a uid and a date. The throwaway files are in
`scripts/spike/k8s/` (`s1-kind.yaml`, `s1-storage.yaml`, `s1-pod.yaml`, `s1.sh`); the script only touches the kind
cluster `remedy-spike`, the directory `~/remedy-kind-spike` and the file `~/remedy-kind-spike.yaml`. Both were removed
afterwards, and `kind get clusters` lists none.

## What was run

```sh
scripts/spike/k8s/s1.sh 700
scripts/spike/k8s/s1.sh 755
```

Each run: create the host directory with the mode, create the cluster (the node mounts it at `/mnt/remedy-claude`),
apply the namespace, the PV (`hostPath`, `type: Directory`) and the PVC, run the `writer` pod (uid/gid/fsGroup 65532,
`runAsNonRoot`, read-only root filesystem, all capabilities dropped, seccomp `RuntimeDefault`), look at the host, delete the
cluster, create it again and run the pod a second time over the same directory.

Two changes to the script from the brief:

- First attempt with mode 700 failed at `kubectl apply -f s1-pod.yaml` with `pods "writer" is forbidden: error looking up
  service account spike/default: serviceaccount "default" not found`. This is a race: the namespace's `default` service
  account is created a moment after the namespace. It has nothing to do with the mount. `make_cluster` in `s1.sh` now
  waits (up to 30 s) for `serviceaccount default` in `spike` after applying the storage file. The half-built cluster of
  that attempt was deleted and the run repeated from the start, so the outputs below are from the fixed script. K-4 hits
  the same race for any pod made right after its namespace.
- Added one step that the brief did not have, between the host check and the cluster deletion:
  `docker exec remedy-spike-control-plane ls -ln /mnt/remedy-claude` ("inside the kind node"). It does not change the logic.

## Output, mode 700

```text
host dir: drwx------  2 jaydee  staff  64  7 Oct 08:28 ~/remedy-kind-spike
pod/writer condition met
uid=65532 gid=65532 groups=65532
/state:
drwxr-xr-x    3 65532    65532           96 Oct  7 06:28 claude
/state/claude:
-rw-r--r--    1 65532    65532           49 Oct  7 06:28 probe.txt
written by 65532 at Wed Oct  7 06:28:56 UTC 2026
== on the host after the pod
~/remedy-kind-spike:
drwx------   3 501  20    96  7 Oct 08:28 .
drwxr-xr-x   3 501  20    96  7 Oct 08:28 claude
~/remedy-kind-spike/claude:
-rw-r--r--  1 501  20  49  7 Oct 08:28 probe.txt
written by 65532 at Wed Oct  7 06:28:56 UTC 2026            <- cat on the host
== inside the kind node
drwxr-xr-x 3 0 0 96 Oct  7 06:28 claude
== after kind delete cluster
-rw-r--r--  1 501  20  49  7 Oct 08:28 probe.txt            <- still there
== second cluster, second pod
drwxr-xr-x    3 65532    65532           96 Oct  7 06:28 claude
-rw-r--r--    1 65532    65532           49 Oct  7 06:29 probe.txt
written by 65532 at Wed Oct  7 06:29:26 UTC 2026
```

## Output, mode 755

```text
host dir: drwxr-xr-x  2 jaydee  staff  64  7 Oct 08:29 ~/remedy-kind-spike
uid=65532 gid=65532 groups=65532
/state:
drwxr-xr-x    3 65532    65532           96 Oct  7 06:30 claude
/state/claude:
-rw-r--r--    1 65532    65532           49 Oct  7 06:30 probe.txt
written by 65532 at Wed Oct  7 06:30:05 UTC 2026
== on the host after the pod
~/remedy-kind-spike:
drwxr-xr-x   3 501  20    96  7 Oct 08:30 .
drwxr-xr-x   3 501  20    96  7 Oct 08:30 claude
~/remedy-kind-spike/claude:
-rw-r--r--  1 501  20  49  7 Oct 08:30 probe.txt
written by 65532 at Wed Oct  7 06:30:05 UTC 2026            <- cat on the host
== inside the kind node
drwxr-xr-x 3 0 0 96 Oct  7 06:30 claude
== after kind delete cluster
-rw-r--r--  1 501  20  49  7 Oct 08:30 probe.txt            <- still there
== second cluster, second pod
-rw-r--r--    1 65532    65532           49 Oct  7 06:30 probe.txt
written by 65532 at Wed Oct  7 06:30:35 UTC 2026
```

## Cleanup check

`s1.sh` ends with a check of its own (added after review): it prints `kind get clusters` and lists the host paths that
are left. One more end-to-end run with mode 700 (same result as above: both pods wrote as uid 65532) printed this at
its tail:

```text
== cleanup
Deleting cluster "remedy-spike" ...
Deleted nodes: ["remedy-spike-control-plane"]
left: claude    (the directory ~/remedy-kind-spike stays so that you can inspect it; remove it by hand)
cleanup check: kind clusters left: No kind clusters found. 
cleanup check: host paths left:
~/remedy-kind-spike
```

The script keeps the directory for inspection, so it was removed by hand afterwards
(`rm -rf ~/remedy-kind-spike ~/remedy-kind-spike.yaml`), and the same two checks were run again:

```text
$ kind get clusters
No kind clusters found.
$ command ls -d ~/remedy-kind-spike*
zsh: no matches found: ~/remedy-kind-spike*
```

## Findings

- Both modes work, including the stricter 700 (owner only). The pod wrote as uid 65532 into a host directory owned by
  the host user (uid 501, gid 20) with no chmod or chown. The brief's fallback of a more permissive mode (777) was not
  needed and so was not tried.
- Docker Desktop's file sharing translates ownership at the boundary. The same directory shows three owners:
  the host user (`501:20`) on the Mac, `0:0` from `docker exec` in the node, and `65532:65532` inside the pod. The pod's
  uid does not have to match anything on the host. The host user can read what the pod wrote and can edit or delete it.
- A file written by the pod survives `kind delete cluster` and is overwritten by the pod of the next cluster (the
  timestamp of `probe.txt` moved from 06:28 to 06:29, 06:30 to 06:30:35), so the login would survive too.
- The PV spec of `s1-storage.yaml` worked unchanged (`hostPath` with `type: Directory`, `Retain`, a static PV bound by
  `volumeName`, `storageClassName: remedy-host` with no such StorageClass object). Whether `fsGroup: 65532` in the pod matters was not
  isolated (it was set in both runs, and both worked).
- Whether `extraMounts` needs the directory to exist first was NOT measured: the script always creates the directory
  before the cluster, as the brief's script does. K-4 creates it before the cluster anyway (needed to set its mode).
- Limit of the result: this is Docker Desktop on macOS only. On a Linux Docker host a bind mount keeps the real owner
  (the host user, usually uid 1000, not 65532) and the mode applies as is, so a non-root pod would need a chown to 65532
  or a world-writable directory. That was not measured here and the dummy setup does not run there.

## Decision

```text
works with mode:      700 (and 755); no 777 needed, on Docker Desktop for Mac
file owner on host:   user (501:20, the host user, although the pod wrote as uid 65532)
host can read:        yes
survives new cluster: yes
needs dir first:      not measured (the script always creates it; K-4 creates it first anyway)
PV spec that worked:  the s1-storage.yaml as committed
K-4 must:             mkdir -p the host directory with mode 700 before `kind create cluster` (nothing to chown on
                      Docker Desktop); wait for the namespace's default serviceaccount before creating the pod;
                      not assume this holds on a Linux Docker host (there the directory must be writable by 65532)
```
