# The real run of the runner login from the Setup page (K-7): one field use, the full run not made

Date: 2026-10-22. This is **not** the run that `docs/runbook/login-relay-real-run.md` describes. It records the one thing that is known about the wizard and the
real CLI so far: the maintainer logged the home server's runner in from the Setup page, and it worked. The ten measurements of spec section 13 were not made, and the
decision of plan task 12 (yes, yes with a PTY, no) is **not taken**. Nothing in this record is a measurement of the relay; it is a report of one use plus what the
logs of the running instance show afterwards.

## What is known

| Part | What |
|---|---|
| CLI | `claude` 2.1.292, the pin of `deploy/cli-pin.yaml`, installed by the runner pod's init container (`installed the CLI ... version=2.1.292 arch=amd64`) |
| Remedy | the home server's installation (cycle 6, namespace `remedy`), images `remedy-server:0.14.0` and `remedy-runner:0.14.0` after the redeploy of 2026-10-22 06:59Z; the login itself was made in the pod that ran before that, with an earlier release that already had the wizard. The exact time and release were not recorded |
| What the maintainer did | logged in from the Setup page ("Log in from here"), a real sign-in with the maintainer's own account, once |
| Outcome | the login worked |

What the logs of the running instance show afterwards, read from the outside on 2026-10-22 (the pods' logs only; no file of the login directory was opened, and the
admin API was not called):

- The new runner pod's first login check, at its start, was `login=ok` (06:59:51Z), and every check after it, every ten minutes, was `ok` up to 07:29:52Z. So the
  login survived a pod restart: it lives on the runner's volume.
- Two real runs followed, with `provider=claude`: 07:00Z (8 s) and 07:02Z (31 s), both `exit=0`. The login is therefore usable by a run, not only reported as `ok`.
- The log line `login relay on` is there; no relay event, URL or code is in the runner's or the control plane's log (the attempt happened in the earlier pod, whose log is gone,
  so this says nothing about the attempt itself).

## What this settles, and what it does not

Settled, for CLI 2.1.292 on Linux amd64 in a pod:

- **A valid code written to the child's stdin on a pipe, with no PTY, completes the login.** The fallback "yes with a PTY" is not needed for the success path.

Not settled: every other point of spec section 13.

| # | Measurement | Result |
|---|---|---|
| 1 | A valid code: does the CLI exit, with which code, after how long; does `auth status` turn `ok` before or after the exit | the login succeeded; **not observed** how (exit or the second path of spec section 9), nor the times |
| 2 | Which stream carries the invalid-code sentence; a stray prompt after it | not measured |
| 3 | The format of the code and the shape of the URL's values (spec section 8) | not measured; the validators accepted a real code and a real URL, or the wizard would not have worked |
| 4 | A deliberately wrong code and the restart | not measured |
| 5 | No output of a successful login reaches any sink (logs, database, activity, `GET` answers) | not measured; the logs of the current pods hold nothing of it, which says nothing about the earlier attempt |
| 6 | Names under `.local`, `backups/` and any `.claude.json.lock/` after SIGTERM, after SIGKILL and after a success | not measured (the directory is never opened) |
| 7 | Whether `DISABLE_UPDATES` keeps `.local` away under the relay's child | not measured |
| 8 | A re-login while logged in | not measured |
| 9 | The time to the URL | not observed |
| 10 | A cancel mid-session; a runner pod deletion mid-session | not measured |

## Surprises

None reported by the maintainer. Nothing was recorded during the attempt, so a surprise could have gone unnoticed.

## Decision

**Open.** The status line of `docs/specs/2026-10-21-ui-login-relay-design.md` and `CLAUDE.md` keep saying "not proven". The terms of use were not re-read in their primary text; the
feature stays out of reach of anyone but the maintainer (`docs/specs/2026-10-21-ui-login-relay-design.md`, status line). When the full run is made, write its record next to this
one and let that record carry the decision.
