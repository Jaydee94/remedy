# The runner status against the real CLI (plan K-6, task 6)

Date: 2026-10-09. Docker Desktop on macOS arm64, kind cluster `remedy-dev` (context `kind-remedy-dev`, kindnet), the chart at the
commit of the branch `feat/kubernetes-deployment`, the pinned `claude` CLI 2.1.292 (`deploy/cli-pin.yaml`). The runner was logged
in (the maintainer had logged in earlier; the login directory `~/remedy-kind/claude` survives `dummy-down`). No secret and no
content of the login appears below; the agent that ran these checks never opened, listed or copied anything in
`~/remedy-kind/claude`. Times are UTC, read from the machine's clock.

Wording: **measured** (a number or output seen in these runs), **observed** (seen, n is small; every n here is 1 or 2),
**expected** (from the design or from known behaviour, not tested here).

## What this run is, and what it is not

The page checks were done with a Node script of Playwright (1.60.0, headless Chromium) that signs in with
`getByLabel(/password/i)`, opens Setup and prints the text of the "My runner" section whenever it changes (it samples the
page's DOM once a second; the page itself polls every 10 s, so a change is seen up to 10 s after the server knew it). The
script reads the admin password from `dummy.env` itself, so that the password never appears in a transcript. The screenshots are
of the same headless browser. A screenshot was looked at for the `Connected` state and for the `Not connected` state.

Not run, on purpose:

- Step 5 item 2 and item 3 (`make dummy-logout`, then `make dummy-login`): both need the maintainer (a confirmation and an
  interactive login). The transition from `login missing` to `login ok` was seen earlier in the same task, after the maintainer's
  login: `make dummy-status` changed from `runner: connected, login missing` to `runner: connected, login ok` (observed once, by
  `dummy-status`, not on the page). The `Logged in` to `Not logged in` transition on the page was therefore **not** seen live.
- Step 5 item 6, `make dummy-smoke`: it costs subscription quota, and it had already run with the code in place in the earlier
  rounds (`docs/research/k8s-dummy-real-run.md`). It was not run in this round, so there is no `all ok` in this record.

## The cold start (the fresh-install path of `dummy-up`)

```sh
make dummy-down      # 1 s (the cluster and what the scripts generated; the login stays)
make dummy-up        # 122 s, exit 0
make dummy-status
```

Measured: 122 s wall clock for `dummy-up`. The output had the line `restarting the control plane once so that its pod starts after
the write token exists` (the fresh-install restart; this was the first run of that path on a cluster since `is_fresh` was
added) and ended with

```
Ready.
  url:       http://127.0.0.1:18080
  password:  REMEDY_ADMIN_PASSWORD in ~/remedy-kind/dummy.env
  login:     the runner is logged in
```

`make dummy-status` then said `runner pod: Running, restarts 0` and `runner: connected, login ok`. So after a cold start with an
existing login directory the runner reported `ok` within the wait loop of `dummy-up` (up to 60 s), without any login step.

## The probes and the network policy

Observed (n=1, in the first part of this task, not in the repository's history: the agent's task report): after
`make dummy-redeploy` the runner pod became Ready with 0 restarts while the `remedy-runner` NetworkPolicy has policy types
Ingress and Egress and no ingress rule, with one `Readiness probe failed: HTTP probe failed with statuscode: 503` event at start
(the runner answers 503 until it has reached the control plane), then Ready. In this round the pod was Ready again after every
restart below (Ready within 5 s of the scale-up in one case, measured with `kubectl wait`), and `restarts` stayed 0. So on
kindnet the kubelet's probe to port 8082 is not blocked, and no `probeFrom` value is needed here. Not measured: k3s's
kube-router, or any CNI other than kindnet. Inferred caveat: kindnet may not enforce policies against node-originated traffic
the way another CNI does; the homelab runbook says what to do if it does.

## Step 5 on the page (Setup, "My runner")

Item 1, the runner is logged in. Measured: the section showed `Connected`, `Logged in`, the sentence `My runner is connected and
logged in, so I can run an agent.` and `Agent CLI: 2.1.292 (Claude Code)`.

Item 4, `kubectl -n remedy-system delete pod remedy-runner-0` at 08:27:56. Observed (n=1): the section **never showed `Not
connected`**. Its states, with the time the script saw them:

```
08:27:44  Connected, Logged in
          (delete at 08:27:56)
08:28:04  Connected, Login unknown   "I do not know yet whether the agent is logged in."
08:28:24  Connected, Logged in
```

The replacement pod was Running (1/1) at 08:28:20 with an age of 24 s, and had reported to the control plane by 08:28:04, within 8 s
of the delete. The control plane counts a runner as connected for 45 s after its last request (`runnerGrace`), so a replacement
that is back in under 45 s is not visible as a disconnection. That is the designed behaviour, not a failure, but it means this
check did not show the `Not connected` text; it showed that the login survived (`Logged in` again about 28 s after the delete,
the state volume survived). The `Login unknown` in between is the new runner's first report before its first login check.

Item 4, repeated so that the state can be seen: `kubectl -n remedy-system scale statefulset remedy-runner --replicas=0` at
08:33:32, and `--replicas=1` at 08:34:48. Observed (n=1):

```
08:33:21  Connected, Logged in
          (scale to 0 at 08:33:32)
08:34:21  Not connected, Login unknown   "My runner is not connected. I last heard from it 49 seconds ago.
                                         Check that the runner is running: in Kubernetes the pod is remedy-runner-0."
08:34:51  Not connected, Login unknown   "... 79 seconds ago ..."
          (scale to 1 at 08:34:48; the pod was Ready at 08:34:53)
08:35:02  Connected, Login unknown
08:35:12  Logged in again
```

So: `Not connected` was on the page at the first poll after the 45 s grace had run out (the first sample after it was 49 s after
the last contact), the sentence is `My runner is not connected.` with the separate `I last heard from it N seconds ago.` line and the
pod hint, the badge next to it says `Login unknown` (never a stale `Logged in`), and after the scale-up it took 14 s to
`Connected` and 24 s to `Logged in` (page polls every 10 s). The login was still there after the pod was recreated: the state
volume survived.

Item 5, `kubectl -n remedy-system rollout restart deployment/remedy-server`, done twice (08:36:17 and 08:37:37). Observed (n=2): about
9 to 10 s after the restart the page's next poll answered 401 and the script saw the login page (the sessions are in memory);
after signing in again (within 0.1 s of that) the section already said `Connected`, `Logged in`. Neither time did the page say
`Not connected` or `Never seen`. The new control plane pod had been up for those few seconds and the runner had already reported to it: the page showed a login state, which only a report carries. Inferred, not looked at in the runner's log: it reconnects after the old connection ends and reports again (the report interval is 15 s, so a report within about 9 s is not guaranteed, and this was 2 of 2). So the "forgets the runner" window is shorter than the time it takes to see the login page and sign in again: it was
**not observed**, only that the state is correct about 10 s after a control plane restart. The first of the two runs of the script
printed only changes and so could not have shown a repeat of the same text after the new sign-in; the second one reset that
and printed the first text after sign-in.

## The login check in the pod, without a network (step 5a, the follow-up of `k8s-s2-login-check.md`)

The command of the check is `claude auth status --text` (S2). A throwaway pod `s2-proxy-login` and `s2-proxy-empty` in
`remedy-system`, built like the runner pod (the runner's own image `remedy-runner:dev-20261009102440`, the same `restricted`
hardening, an init container that runs `install-cli` with the arguments of the runner pod's init container, the runner's claim
`claude-state` mounted), with `HTTPS_PROXY=http://127.0.0.1:1` (nothing listens there), `HOME=/state`, `TMPDIR=/tmp` and
`DISABLE_UPDATES=1`. It was a second pod on the same claim on the single-node testbed; it was not necessary to scale the runner
down. Only the exit code and the container's `startedAt` and `finishedAt` were read from the pod status (one-second resolution);
`kubectl logs` of these pods was never run, because the output is account data. Both pods were deleted afterwards.

| Pod | `CLAUDE_CONFIG_DIR` | Exit code | started to finished (the check) | Init container (install) |
| --- | --- | --- | --- | --- |
| `s2-proxy-login` | `/state/claude` (the login) | 0 | 08:39:08 to 08:39:08 (under 1 s) | 5 s |
| `s2-proxy-empty` | `/empty` (an existing, empty directory) | 1 | 08:39:16 to 08:39:16 (under 1 s) | 4 s |

Measured (n=1 each): with a proxy that cannot be reached, the CLI answered in the same second, exit 0 with the login and exit 1
without. So the status check did not wait for the network in this setting; S2's 0.06 to 0.10 s without a proxy and these
under-a-second values agree, but the resolution here is one second, so it says "under 1 s", not a number. Not shown: that the
check never needs the network in any state (for example a token that is about to expire might be refreshed online; the
`loginCheckTimeout` of 10 s and the `unknown` state stay as the guard), and not shown that it does not write into the login
directory (it was not looked at, on purpose). The runner pod was not touched by this step: after it `make dummy-status` said
`runner pod: Running, restarts 0`, `runner: connected, login ok`.

## Result

What this run shows of D8 (the runner's connection and login are visible, and the runner never restarts for a missing login):

- Shown (observed, with the real CLI): the page says `Connected` and `Logged in` for a runner that is logged in, also after a
  cold start with an existing login directory (`dummy-up` ended with `the runner is logged in`, `dummy-status` with
  `connected, login ok`); the page says `Not connected` with the time since the last contact and the pod hint when the runner is
  gone for more than 45 s, and `Login unknown` rather than an old `Logged in` in that state; the login survives the pod's
  recreation; the pod's probes are not blocked by the network policy on kindnet and the probes never depended on the login (the pod
  was Ready and the readiness event was a 503 only at the start; evidence, observed once: in the part-1 check, before the maintainer's
  login, `make dummy-status` said `runner: connected, login missing` while the runner pod was Running and Ready); a restart of the control plane is healed within about 10 s of the
  moment the page could be used again.
- Shown (measured, n=1): the status command answers in under a second and with the right exit code with an unreachable proxy.
- Not shown: the `Logged in` to `Not logged in` change on the page (item 2 needs the maintainer) and the way back (item 3; only
  the `dummy-status` line was seen change); that a login that has expired on the server side turns the page to `Not logged in`
  (probably it still reads `Logged in`, and a failed run is the real test; this was never tried); a `Not connected` window during a
  pod deletion or a control plane restart (the replacement was faster than the grace of 45 s, so the page never needed to say
  it); the probes on k3s's kube-router or any CNI other than kindnet; `make dummy-smoke` in this round.
