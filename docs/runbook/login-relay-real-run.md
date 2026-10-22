# Runbook: the login from the Setup page against the real CLI (the real run of K-7)

**This run has not been made.** What exists is one field use on the home server, recorded in `docs/research/login-relay-real-run.md`. Make this run with the maintainer at the
keyboard: he types the codes and signs in with his own account, and he alone touches the CLI's login directory. It costs no quota (a login is not a run) but it
**logs the dummy runner out**, so plan on about 45 minutes. The record goes next to the field use as `docs/research/login-relay-real-run-full.md`, and it carries the
decision of plan task 12.

## 1. Why

The fake CLI (`testutil.FakeClaudeRelay`) follows what the spike S5 measured, and the spike did not settle ten things. The decision that depends on them is whether the pipe
path (no PTY, the code plus a newline on stdin) is enough. The field use says yes for a valid code. The rest of the ten is what is left, and each line below says what a failed
measurement changes.

## 2. Preconditions

- The dummy setup is up (`make dummy-up`, `docs/runbook/dummy-setup.md`) with the pinned CLI and a logged-in runner. The runner is in subscription mode (the default). There is nothing
  to turn on: the login from the page has no switch since 2026-10-22.
- **The maintainer** runs `make dummy-logout` (interactive, it asks first). No script of ours and no agent opens, lists or copies `~/remedy-kind/claude`
  (`scripts/check-login-dir-untouched.sh`).
- The Setup page shows the runner card with "Log in from here". `GET /api/runner` says `login: missing`.
- Open the runner's and the control plane's log in two terminals (`make dummy-logs`) before the first step; set `REMEDY_LOG_LEVEL=debug` on both for measurement 5.
- A second browser tab on **Today** (`/`): the login writes activity entries (start, wrong code, success, cancel), which are the only trace the page keeps.

Record every result as a **shape**: a count, a boolean, a duration, a name. Never the URL, never the code, never the account name.

## 3. The ten measurements

Run them in this order; 1 first, because the others assume a working login.

| # | Do | Note down | If it fails |
|---|---|---|---|
| 9 | Click "Log in from here" and time it until the address appears | seconds from click to URL | a slow URL changes the 30 s timer of spec section 6 |
| 3 | Look at the URL (the maintainer reads it, you do not) and at the code the sign-in page shows | the length of the code, its character set (letters, digits, `-`, `_`, `#`), the host and path of the URL, the names of its query values | `ValidateURL` or `ValidateCode` refuses a real value: tighten or relax **only** through a follow-up that changes the shared cases and both sides |
| 1 | Sign in, paste the code, submit | does the child exit, with which code, after how long; whether the card turns `ok` before the exit or only after a login check; the wall time from submit to "logged in" | a valid code that is ignored on the pipe: the verdict becomes **yes with a PTY**, which amends the spec before any code |
| 5 | After 1, grep the logs of both pods, the activity on Today and the answers of `GET /api/runner` for the account name the maintainer expects | counts of matches (expected 0) | any match is a leak: stop, fix, then repeat |
| 8 | With the login `ok`, click "Log in from here" again | whether the page offers it, whether the old login keeps working until the new one is done | a broken login after a cancelled re-login is a finding for the spec section 9 |
| 4 | Start again, paste a deliberately wrong code | which stream carries the invalid-code sentence (the runner log says; never the sentence), whether a stray prompt follows, whether the wizard restarts with a new URL (design D7) | the stream decides which reader may drop the sentence |
| 10a | Start a session, then press Cancel before submitting | the state reached, the time until the claim gate opens, the number of `claude` processes in the runner pod afterwards (a count only, however the maintainer can take it in the pod) | a child that stays is a process-hygiene bug |
| 10b | Start a session, then delete the runner pod | what the page says, the activity entry, the state after the new pod is up | a session that stays `awaiting_code` forever is a bug |
| 6 | After a success, after a Cancel (SIGTERM) and after the pod deletion of 10b: the maintainer lists the **names** under `.local`, `backups/` and any `.claude.json.lock/` of the login directory (no file is opened) | names and counts | new names change the backup cleanup of spec section 9 |
| 7 | Look for any name under `.local` after the whole run | a count of names | a staged update means `DISABLE_UPDATES` does not reach the relay's child: the child's environment (spec section 9, "The child") needs it |

## 4. Not measured unless needed

A code without a newline (only if the newline form fails in 1) and a second line in one process (the design starts a new process for a new attempt).

## 5. After the run

1. Write the record `docs/research/login-relay-real-run-full.md`: date, CLI version, the pin of `deploy/cli-pin.yaml`, each result as a shape, surprises, and the **decision**:
   **yes**, **yes with a PTY** (amend the spec and stop; that needs a PTY in the runner) or **no** (remove the feature, keep or withdraw the exception of `docs/design.md` 2.7.1).
2. Only on **yes**: `CLAUDE.md` and the spec's status line say "implemented and run". `docs/research/subscription-cli-usage.md` gets a dated paragraph only after the maintainer has re-read the
   primary text of the terms of use (not done by an agent; until then write "not yet re-read" and keep the feature off the list of things offered to others).
3. `make check`, then a pull request `docs: the real run of the runner login from the Setup page (K-7)`; the maintainer merges.
