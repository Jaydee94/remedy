# Phase 1 run against the real GitHub

Date: 2026-10-04. Remedy commit `7cf1efd` (main after plan 1d, task 5, first part). `claude` CLI 2.1.288 on macOS,
the runner's default model, poll interval 30 seconds, `REMEDY_LOG_LEVEL=debug`. Server and runner ran from `~/remedy-real-run`
for about an hour. The steps are those of [`docs/runbook/first-real-run.md`](../runbook/first-real-run.md).

## What was run

- **Repository:** `Jaydee94/remedy` (public), registered through the API after the token was saved.
- **Token: a deviation from the runbook.** The runbook asks for a fine-grained read-only token (Metadata, Contents, Pull requests,
  Actions, Checks) because GitHub offers no API to create one. The maintainer allowed the run to use the token of the `gh` login
  instead (scopes `repo`, `gist`, `read:org`, `admin:public_key`), which can write. It was piped from `gh auth token` into the
  Remedy API and never printed or written to a file; it was deleted from Remedy (`DELETE /api/github/connection`) when the run was over.
  The run therefore shows that Remedy sends no write request (see the audit), not that a read-only token has enough permissions.
- **Case 1:** the open Renovate pull request 20 (TypeScript 7), whose `web` check was already red.
- **Case 2:** a deliberately red pull request (62): one new test file with a test that calls `t.Fatal`, then a second commit that
  removes it. Opened, fixed and closed (never merged) by the assistant with the maintainer's confirmation; the branch was deleted.

## Case 1: the Renovate pull request

| Time (UTC) | Event |
|---|---|
| 16:26:59.3 | connection saved (`GET /user` answered 200) |
| 16:26:59.6 | repository added |
| 16:27:25.2 | incident opened: `web` failed on PR #20 (the first poll, 25.6 s after the repository was added) |
| 16:27:27.7 | diagnosis started automatically (2.5 s later, after the poll cycle) |
| 16:27:36.3 | diagnosis finished (the run took 8.3 s, cost 0.0622 USD) |

Stored diagnosis (as stored; high confidence, category `dependency_update`, `fix_looks_automatable` false):

- Summary: "The web job failed at `npm ci` because web/package-lock.json was not regenerated after Renovate bumped typescript to ~7.0.0 in web/package.json."
- Cause: the log shows `npm ci` failing with "lock file's typescript@6.0.3 does not satisfy typescript@7.0.2" and a list of missing
  `@typescript/typescript-<platform>@7.0.2` optional packages; only `web/package.json` changed in the PR; `web/package-lock.json`
  still records `~6.0.2` and the 6.0.3 entry. TypeScript 7 ships per-platform packages, which the lock file also lacks.
- Affected files: `web/package.json`, `web/package-lock.json`.
- Proposed fix: run `npm install` in `web/` to regenerate the lock file and commit it, and run build, typecheck and lint locally,
  because a major version may break tooling that depends on the TypeScript 6 API; otherwise stay on `~6.0.x`.

This is right, and it agrees with the dry run before the real run ([`spike-responder-dry-run.md`](spike-responder-dry-run.md)).
The snapshot the runner unpacked had 64 files and 304,570 bytes, none skipped.

## Case 2: the deliberately red pull request

| Time (UTC) | Event |
|---|---|
| 17:32:26 | red commit pushed (commit time 17:32:23), pull request 62 opened |
| 17:33:00 | CI run finished with `failure` (the `go` job; `web` was green) |
| 17:33:14.8 | incident opened: `go` failed on PR #62 (14 s after CI turned red; the next poll) |
| 17:33:17.0 | diagnosis started automatically |
| 17:33:24.5 | diagnosis finished (the run took 7.2 s, cost 0.0505 USD) |
| 17:33:37 | fix pushed (commit time 17:33:35) |
| 17:34:09 | CI run finished with `success` (`go`; `web` at 17:34:01) |
| 17:34:14.7 | incident resolved, reason `green` (5 s after CI turned green) |

Stored diagnosis (high confidence, category `test_failure`, `fix_looks_automatable` true):

- Summary: "The go check failed because the PR adds a test, TestDeliberatelyRedForTheRealRun, that always calls t.Fatal."
- Cause: `make fmt vet test build-go` stopped at the test step; every package passed except `internal/store`, with
  `--- FAIL: TestDeliberatelyRedForTheRealRun` and the message from the file; the PR's only change is that file. The agent also
  noted that the title and description say the failure is deliberate, and that it did not open repository files because the log and
  the patch already showed the cause.
- Affected files: `internal/store/zz_real_run_test.go`.
- Proposed fix: delete that file (and, since the pull request is a demo, close it without merging).

The pull request text reached the agent only inside a delimited data block and the agent used it as evidence, not as an instruction.
The diagnosis is right and names the file.

## What the Timeline showed

After the run the Timeline (home page, live) listed, newest first and grouped under one day heading per day: "go is green again on PR #62",
the diagnosis finished and started entries of PR #62 (with links to the incident and the run), "go failed on PR #62", then the same
three entries for PR #20, "Added repository Jaydee94/remedy" and "GitHub connection saved for Jaydee94". Each entry showed the
repository and the time; the incident and run links opened the incident and the run.

## Cost and duration

Two automatic runs, 0.1127 USD in total as the CLI reports it (the runs went through the subscription login, which is why the figures are
informational), 8.3 s and 7.2 s each. The limits (3 per incident, 20 per day) were nowhere near reached.

## Audit

From `server.log` at the time of the audit (269 requests; 272 at shutdown):

- **Writes:** 0 requests with a method other than `GET`; 0 refusals by the transport guard; 0 failed requests (one request was cut off by the
  shutdown at the end).
- By status: 241 × 304 (answered from the ETag cache), 24 × 200, 4 × 302 (two log downloads and two tarballs).
- By host: `api.github.com` 265, `codeload.github.com` 2 (the tarballs), `productionresultssa*.blob.core.windows.net` 2 (the job logs). For the
  last two the log names only the host, not the signed address.
- By path: 83 polls of `/pulls`, 83 of `/commits/main/check-runs`, 84 of the check runs of PR 20's head commit, 3 of PR 62's head
  commits, and one-offs for `/user`, the repository, two pull requests with their files, two job logs and two tarballs. 30 seconds
  between polls and ETags kept the polling to about 28 requests that counted against the rate limit in an hour.
- No warnings or errors in the server or runner log.
- **Token:** `scripts/check-no-token-leak.sh` (token piped from `gh auth token`) searched `server.log`, `runner.log`, the database file and every admin
  API response including the run streams (connection, repositories, incidents, activity, runs, limits, both incidents, both runs and their
  event streams). The result: "the token appears nowhere that was searched", exit 0.

## Success criteria

| Criterion (spec section 12) | Result |
|---|---|
| A deliberately red PR check appears as an incident within one to two minutes | **Met.** 14 s after CI turned red, 48 s after the push (poll interval 30 s). |
| Remedy produces a plausible diagnosis automatically (cause and affected files) | **Met** in both cases; both diagnoses are right and name the files. |
| The timeline shows the sequence | **Met** (see above). |
| The incident resolves when the check turns green | **Met.** 5 s after CI turned green, reason `green`. |
| No write call to GitHub, and the token in no API response and no log | **Met for what was run**: 0 non-GET requests out of 269 and a clean leak check. Caveat: the token had write scope, so this proves Remedy did not write, not that the guard was needed. |
| `make check` and CI are green | Met (CI of every plan 1d pull request was green before merging). |

## Problems found

- **The fine-grained token was not exercised.** The permissions listed in the runbook (Metadata, Contents, Pull requests, Actions,
  Checks, read-only) are what the requests above need according to GitHub's documentation, but a real fine-grained token has not been
  tried. Do this once before relying on it: create the token, replace the connection under Settings, and watch for a 403 in the log
  (a 403 shows as a `poll_failed` entry in the Timeline).
- **No Reconnecting state was seen** in the Timeline (the browser tools cannot provoke a dropped stream); the resume path is covered by a server test.
- **The `main` checkout had a stale `web/node_modules`**, so `make build` failed with `Cannot find module` errors until `make web-install`
  was run again. It is the known symptom from `CLAUDE.md`; the runbook's `make web-install` step covers it.
- Nothing in Remedy needed a fix during the run.
