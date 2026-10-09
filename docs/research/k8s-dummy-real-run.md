# The dummy setup against the real CLI (plan K-4, task 6)

Date: 2026-10-07 (steps 1 and 2) and 2026-10-09 (steps 3 to 6). Docker Desktop (client 29.8.2, server 29.8.1) on macOS arm64,
kind v0.33.0 (Kubernetes server v1.37.0), kubectl client v1.37.1, Helm v4.3.0, the pinned `claude` CLI 2.1.292
(`deploy/cli-pin.yaml`). The cluster is `remedy-dev` (context `kind-remedy-dev`). No secret and no content of the login appears
below: of `~/remedy-kind/claude` only the existence, the modes and the birth times of directories were looked at, never a file name
inside, a size, or a content.

## Step 1: a cold start (2026-10-07)

```sh
make dummy-down      # 0.65 s: Deleting cluster "remedy-dev" ... removed the cluster and what the scripts generated; the login stays
make dummy-up        # 82 s wall clock, exit 0, ends with the Ready. block
make dummy-status
```

```
cluster: remedy-dev
release: deployed (revision 1)
server: 1/1 ready, image remedy-server:dev-20261007200200
runner: Running, restarts 0
write token: present
token refresher: last scheduled success never
database volume: Bound
login directory: /Users/jaydee/remedy-kind/claude (present)
url: http://127.0.0.1:18080 (200 on /healthz)
```

`kubectl -n remedy-system get pods`: two pods (`remedy-runner-0`, `remedy-server-...`), both `1/1 Running`, 0 restarts. Modes:
`~/remedy-kind` and `~/remedy-kind/claude` are `drwx------`. (At that time `dummy-status` had no `runner:` line about the login
and `dummy-up` did not report it; plan K-6 added both.)

## Step 2: without a login (2026-10-07)

`make dummy-smoke` took 4 s and stopped at run 1:

```
smoke: run 1: why does something in demo crash?
smoke: FAILED: the runner is not logged in. Run: make dummy-login
make: *** [dummy-smoke] Error 1
```

`echo "exit=$?"` after `make` printed **2**: GNU make answers 2 whenever a recipe fails. `dev/kind/smoke.sh` itself exits 1
(make prints `Error 1`). The plan's expectation of `exit=1` holds only when the script is called directly. Since plan K-6 the smoke
test stops even before run 1 when the runner reports that it is not connected or has no login (checked on 2026-10-09: it stopped
at once, exit 2, no run started, nothing spent).

## Step 3: the login (2026-10-09, the maintainer)

`make dummy-login` was run by the maintainer (`/login`, code pasted, `/exit`). Afterwards `make dummy-status` showed
`runner: connected, login ok`.

## Step 4: the smoke test with a login (2026-10-09)

`make dummy-smoke; echo "exit=$?"`: **19 s**, exit 0:

```
smoke: waiting for the control plane
smoke: run 1: why does something in demo crash?
smoke: run 1 ok: 4 tool calls
smoke: run 2: restart demo/web
smoke: approve cluster_rollout_restart (call 5)
smoke: run 2 ok: demo/web restarted at 2026-10-09T07:57:58Z
smoke: all ok (runs 86dd7ab87fae96c6dbe638e4bac7d336 and 77e81df436dfbcd42c733099c3921b11)
```

The CLI printed no quota cost; the smoke test does not show one either. Two short runs.

## Step 4a: the state volume's `.local`

`command ls -d ~/remedy-kind/claude/.local/share/claude` printed the path: **the directory exists**. The brief expected "No such
file or directory"; that was wrong for this setup. Only the directories were looked at (`stat`, never their contents):

| directory | birth | mode |
|---|---|---|
| `~/remedy-kind/claude` | 2026-10-07 13:11:44 | `drwx------` |
| `~/remedy-kind/claude/.local`, `.local/share`, `.local/share/claude` | 2026-10-09 09:55:55 | `drwxr-xr-x` |

The three directories were born at 09:55:55, the time of the maintainer's login session. I looked at 09:57:40, before the smoke
test above, and again at 09:58:07 after it: the birth and modification times did not change. So the directory came with the
interactive login (the CLI started by hand as `/opt/claude/claude` with `HOME=/state`), not from the two headless runs. **What it
holds is not known**: it could be state of the CLI or an update the CLI downloaded because it was not installed by its own installer.
Looking inside was out of bounds (it sits in the login directory). This is an open decision for the maintainer, not an accepted
risk. The options are listed and undecided: `DISABLE_UPDATES=1` in the runner pod's environment together with an entry in
`provider.FilterEnv`'s allowlist (a spec change: the spec says nothing is added to it), or mounting nothing writable under
`$HOME/.local`. The maintainer may measure the directory's size and contents themselves.

## Step 5: the login survives, and the other targets work

```sh
make dummy-redeploy   # 38 s, ends: redeployed with the tag dev-20261009095906
make dummy-reset      # ends: the database is empty again
make dummy-status     # revision 4, runner pod Running, restarts 0, "runner: connected, login unknown" (a few seconds after the restart)
make dummy-down
make dummy-up         # 90 s wall clock, exit 0; the Ready. block said "login: the runner is logged in" without any login step
make dummy-status     # release deployed (revision 1), runner: connected, login ok, 2 pods
make dummy-smoke
```

`make dummy-up` after `dummy-down` needed **no `make dummy-login`**: the Ready block already said
`login:     the runner is logged in`, and the smoke test ran with it. That is the proof of D2. The cold starts took 82 s and 90 s.

**The first smoke test of the second cold start failed**, 22 s, exit 2, started about 40 s after `dummy-up` returned:

```
smoke: run 1 ok: 4 tool calls
smoke: run 2: restart demo/web
smoke: approve cluster_rollout_restart (call 5)
smoke: approve cluster_rollout_restart (call 7)
smoke: FAILED: run 2 has no single approved and succeeded restart
```

Cause (the server log, not the agent): both approved restarts failed with
`an approved tool failed ... err="open /var/run/remedy/write/token: no such file or directory"`, and the server had logged at its
start `REMEDY_K8S_WRITE_TOKEN_FILE holds no token yet`. `make dummy-status` already said `write token: present`, because the Secret
`remedy-write-token` held a token (one key, 1280 base64 characters), but the server pod mounts that Secret `optional: true`, it was
empty when the pod started, and the kubelet had not yet shown the file in the pod. The agent behaved correctly: it asked, was
approved, saw the error, and asked again once. This is a race of the setup. The one allowed re-run, about three minutes later,
passed:

```
smoke: run 1 ok: 4 tool calls
smoke: run 2: restart demo/web
smoke: approve cluster_rollout_restart (call 12)
smoke: run 2 ok: demo/web restarted at 2026-10-09T08:04:48Z
smoke: all ok (runs ab79fa4e8ff487899da794ed0bb98e7d and bef3f8abf10283c992768dd4df84338d)
```

22 s, exit 0 when `smoke.sh` ran through make. Not fixed in this task (a change to `dummy-up.sh` or the chart; open): the fix would
be that `dummy-up` waits until the server pod sees the token file, not only the Secret. The runbook says to wait a few minutes.

## Step 6: the secrets are nowhere they should not be

- `git status --short`: clean.
- The admin password is in no file of the repository (`grep -rln` over the tree outside `.git`).
- The master key: 0 lines in the server log, 0 in the runner log.
- `~/remedy-kind` is `drwx------`, `dummy.env` is `-rw-------`, `~/remedy-kind/claude` is `drwx------`. Besides `claude/` the directory holds `dummy.env`, `kind-rendered.yaml` and the marker `.remedy-kind-dir`.

## Result

Success criterion 1 (a reachable UI, a healthy control plane and a runner connected to it after `make dummy-up` without manual
steps but the one-time login): proven twice (82 s and 90 s cold starts, `/healthz` 200, `runner: connected, login ok`, two pods),
with the caveat of the write-token race above: right after `dummy-up` the cluster actions can still fail for a few minutes.
Criterion 2 (`make dummy-down` removes everything in the cluster, the login survives): proven; after `dummy-down` and `dummy-up`
the runner was logged in without a second login. Criterion 3 (`make dummy-smoke` runs a real ad-hoc cluster run and an approved
action and fails with a clear message otherwise): proven for the success path (19 s, 22 s) and for the clear failure without a
login (4 s; at once since K-6), and for a failed assertion (the write-token race printed `run 2 has no single approved and
succeeded restart`; the reason is only in the server log, which the message does not say).

Not proven: criteria 4 and 5 (the homelab, the trust boundaries); a Linux Docker host (the `0700` login directory was measured on
Docker Desktop for Mac only); the quota cost (the CLI printed none); the stability of the agent's behaviour (the smoke test
asserts states, not text; it ran three times here, and in the failed pass the agent asked for the restart twice because the first
failed). Open points: the content and cause of `~/remedy-kind/claude/.local/share/claude` (step 4a, undecided), and the
write-token race at the cold start.
