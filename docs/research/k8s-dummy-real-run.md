# The dummy setup against the real CLI (plan K-4, task 6)

Date: 2026-10-07 (steps 1 and 2) and 2026-10-09 (steps 3 to 6 and the follow-up rounds). Docker Desktop (client 29.8.2, server
29.8.1) on macOS arm64, kind v0.33.0 (Kubernetes server v1.37.0), kubectl client v1.37.1, Helm v4.3.0, the pinned `claude` CLI
2.1.292 (`deploy/cli-pin.yaml`). The cluster is `remedy-dev` (context `kind-remedy-dev`). No secret and no content of the login
appears below. The agent that ran the steps looked in `~/remedy-kind/claude` only at the existence, the modes and the birth times of
directories by name (`stat`, `ls -d`), never at a file name inside, a size or a content. The one size and file count in this
record (step 4a) are the maintainer's own measurement, which they ran (`du -sk` and a file count) and reported.

Wording used below: **measured** (a number or output seen in these runs), **observed** (seen, but n is small), **expected** (from the
vendor's documentation or from known kubelet behaviour, not tested here), **inferred** (a conclusion drawn from something measured).

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
login directory: <you>/remedy-kind/claude (present)
url: http://127.0.0.1:18080 (200 on /healthz)
```

`kubectl -n remedy-system get pods`: two pods (`remedy-runner-0`, `remedy-server-...`), both `1/1 Running`, 0 restarts. Modes:
`~/remedy-kind` and `~/remedy-kind/claude` are `drwx------`. (At that time `dummy-status` had no `runner:` line about the login
and `dummy-up` did not report it; plan K-6 added both. So this 82 s start did not show a "runner connected" state.)

## Step 2: without a login (2026-10-07)

`make dummy-smoke` took 4 s and stopped at run 1:

```
smoke: run 1: why does something in demo crash?
smoke: FAILED: the runner is not logged in. Run: make dummy-login
make: *** [dummy-smoke] Error 1
```

`echo "exit=$?"` after `make` printed **2**: GNU make answers 2 whenever a recipe fails. `dev/kind/smoke.sh` itself exits 1
(make prints `Error 1`). The plan's expectation of `exit=1` holds only when the script is called directly. Since plan K-6 the smoke
test stops even before run 1 when the runner reports that it is not connected or has no login; the K-6 task report
(not part of the repository) records that on 2026-10-09, before the maintainer's login, `make
dummy-smoke` stopped at once with the login message, make exit 2, and started no run.

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

**Observed** (directory times): the three directories were born at 09:55:55, the time of the maintainer's login session. I looked
at 09:57:40, before the smoke test above, and again at 09:58:07 after it: the birth and modification times did not change. So in
the runs observed the directory came with the interactive login (the CLI started by hand as `/opt/claude/claude` with `HOME=/state`)
and the two headless runs created nothing at those levels.

**Measured by the maintainer** (not by me; I did not look inside): under `.local` there is one file of 249528 KiB (about 244 MiB),
created at the interactive login. `.local` is not the login: the CLI's configuration directory is `CLAUDE_CONFIG_DIR=/state/claude`,
another subdirectory of the same host directory.

**Inferred** from the size: that file is the CLI's native auto-updater staging a copy of itself under
`$HOME/.local/share/claude/versions`. Its contents were never inspected. **Expected** from the vendor's documentation (found, not
tested, in `docs/research/k8s-s4-cli-install.md`): such a copy takes effect only through a launcher, which the runner does not use
(it starts `/opt/claude/claude`), so it would be unused disk and traffic; and `DISABLE_UPDATES=1` is meant to block all update
paths. That record also notes that the documentation does not say in so many words that the process environment alone suffices.

**Decided** by the maintainer on 2026-10-09 (spec updated, sections 4 and 7): the chart sets `DISABLE_UPDATES=1` in the runner
container's environment only, with no entry in `provider.FilterEnv`'s allowlist, because the headless runs observed did not stage
anything and the interactive login session started by `kubectl exec` is expected to inherit the pod environment. A chart test pins the
variable; on the cluster rebuilt for the follow-up the runner pod shows `DISABLE_UPDATES=1` (observed). **Not yet verified**: that the
directory stays away. The maintainer will delete `~/remedy-kind/claude/.local` themselves, start `make dummy-login` once after the
redeploy (the runner is logged in, so the CLI is expected to open directly), type `/exit`, and confirm. Until then this is a decision
and a configuration, not a measured effect.

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
`login:     the runner is logged in`, and the smoke test ran with it. That is the proof of D2 (twice: this 90 s cold start and the 119 s
one below). The cold starts took 82 s (step 1) and 90 s, both before the restart change below.

**The first smoke test of the second cold start failed**, 22 s, exit 2, started about 40 s after `dummy-up` returned:

```
smoke: run 1 ok: 4 tool calls
smoke: run 2: restart demo/web
smoke: approve cluster_rollout_restart (call 5)
smoke: approve cluster_rollout_restart (call 7)
smoke: FAILED: run 2 has no single approved and succeeded restart
```

**Observed** (server log): both approved restarts failed with
`an approved tool failed ... err="open /var/run/remedy/write/token: no such file or directory"`, and the server had logged at its
start `REMEDY_K8S_WRITE_TOKEN_FILE holds no token yet`. `make dummy-status` already said `write token: present`, because the Secret
`remedy-write-token` held a token (one key, 1280 base64 characters). The server pod mounts that Secret `optional: true`. **Expected
kubelet behaviour, not measured**: a running pod shows a new value of a mounted Secret only after the kubelet's next sync, which takes
up to one or two minutes. What was observed is only a failure when the smoke test ran shortly after `dummy-up` returned, and a pass
when it ran again about three minutes later (22 s, exit 0, `approve ... (call 12)`, restarted at 2026-10-09T08:04:48Z). The agent
behaved correctly: it asked, was approved, saw the error, and asked again once.

### Mitigation and its check (follow-up rounds, 2026-10-09)

`dev/kind/dummy-up.sh` decides before the install whether the release is deployed (`is_fresh`: anything but a `deployed` release,
including one an interrupted first install left failed or pending, counts as fresh). On a fresh install, after `helm` has finished
(the post-install hook has completed by then), it checks that the Secret holds a token (otherwise it fails with a message about
the hook Job; a failing `kubectl get secret` is reported as such), prints `restarting the control plane once so that its pod starts
after the write token exists`, and runs `rollout restart` and `rollout status --timeout=300s` of `deployment/remedy-server`. An
upgrade does not restart. `dummy-status` keeps `write token:` on the Secret; the runbook says that the pod's view cannot be checked.
The restart is meant to make the new pod start after the token exists, so that the file is there from the start; that mechanism is
inferred, not measured.

Check, one pass (1 of 1): `make dummy-down`, then `make dummy-up` (**119 s**, exit 0, the restart line and `successfully rolled out`
printed, `login: the runner is logged in`; this is the only cold start with the restart, so 119 s is one sample), then `make
dummy-smoke` ran immediately after and took **25 s**, exit 0:

```
smoke: run 1 ok: 5 tool calls
smoke: run 2: restart demo/web
smoke: approve cluster_rollout_restart (call 6)
smoke: run 2 ok: demo/web restarted at 2026-10-09T08:11:15Z
smoke: all ok (runs e810a2770ad13d236b1b0701ed074f8f and a72a50c583107c83055dab1daaa4f1d5)
```

The first approved restart succeeded: the failure did not occur in this one pass. That is mitigated, not proven gone: n is 1.
A second `make dummy-up` on the running cluster (an upgrade) took 29 s and printed no restart line, as designed.

## Step 6: the secrets are nowhere they should not be

- `git status --short`: clean.
- The admin password is in no file of the repository (`grep -rln` over the tree outside `.git`).
- The master key: 0 lines in the server log, 0 in the runner log.
- `~/remedy-kind` is `drwx------`, `dummy.env` is `-rw-------`, `~/remedy-kind/claude` is `drwx------`. Besides `claude/` the directory holds `dummy.env`, `kind-rendered.yaml` and the marker `.remedy-kind-dir`.

## Result

The smoke test with a login ran four times on 2026-10-09: 19 s pass, 22 s fail (the write-token window above), 22 s pass (about
three minutes later), 25 s pass (right after the cold start with the restart). One more call without a login stopped at once.

Success criterion 1 (a reachable UI, a healthy control plane and a runner connected to it after `make dummy-up` without manual
steps but the one-time login): shown on the 90 s and the 119 s cold starts (`/healthz` 200, `runner: connected, login ok`, two
pods); the 82 s start of step 1 predates the `runner:` line and showed only a ready server and a running runner. The 119 s start
includes the restart added later and is one sample. Criterion 2 (`make dummy-down` removes everything in the cluster, the login
survives): shown; after `dummy-down` and `dummy-up` (twice) the runner was logged in without a second login. Criterion 3
(`make dummy-smoke` runs a real ad-hoc cluster run and an approved action and fails with a clear message otherwise): shown for the
success path (19 s, 22 s, 25 s) and for the clear message without a login (4 s; at once since K-6). The other failure seen, the
write-token window, was caught by the assertion but its message (`run 2 has no single approved and succeeded restart`) did not name
the cause, which is only in the server log: so "a clear message otherwise" is shown for the missing login only.

Not shown: criteria 4 and 5 (the homelab, the trust boundaries); a Linux Docker host (the `0700` login directory was measured on
Docker Desktop for Mac only); the quota cost (the CLI printed none); the stability of the agent's behaviour (the smoke test asserts
states, not text; in the failed pass the agent asked for the restart twice because the first failed). Open points: the write-token
window is mitigated by one restart on a fresh install, checked in one pass (1 of 1); and whether `DISABLE_UPDATES=1` keeps
`~/remedy-kind/claude/.local/share/claude` from coming back is decided and configured, not yet verified by the maintainer.
