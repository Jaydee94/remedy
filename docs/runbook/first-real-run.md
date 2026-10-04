# Runbook: the first run against the real GitHub

This runs everything of phase 1 once against a real repository, and checks the success criteria of the
[spec](../specs/2026-10-02-phase-1-detect-and-diagnose-design.md#12-success-criteria). It uses the Remedy
repository itself. Nothing in it writes to GitHub from Remedy; the only writes are the ones you make with your
own `git` and `gh` to create and fix a deliberately red pull request.

Plan on about an hour, most of it waiting for CI. The run uses subscription quota: a diagnosis is one agent
run, and the limits of the quick start (at most 3 per incident, 20 per day) apply.

## 1. The token

Create a **fine-grained personal access token** at <https://github.com/settings/personal-access-tokens/new>:

- Resource owner: your account. Repository access: **only** `Jaydee94/remedy`.
- Repository permissions, all **read-only**: Metadata, Contents, Pull requests, Actions, Checks.
- Nothing else, no account permissions. A short expiry (7 days) is enough.

Do not paste the token into a terminal, a chat or a file. You enter it once, in the Remedy UI.

## 2. Build and start

```sh
make web-install && make build
mkdir -p ~/remedy-real-run && cd ~/remedy-real-run
export REMEDY_ADMIN_PASSWORD='choose-a-long-password'
export REMEDY_RUNNER_TOKEN="$(openssl rand -hex 24)"
export REMEDY_MASTER_KEY="$(openssl rand -base64 32)"
export REMEDY_DB="$PWD/remedy.db" REMEDY_LOG_LEVEL=debug REMEDY_POLL_INTERVAL=30s

<path-to-remedy>/bin/remedy-server > server.log 2>&1 &
<path-to-remedy>/bin/remedy-runner > runner.log 2>&1 &
```

The `claude` CLI must be installed and logged in (`claude`, then `/login`). The shell that starts the runner must
not have `ANTHROPIC_API_KEY` set to anything you want billed; the runner removes it from the CLI's environment
anyway.

## 3. Connect and register

Open <http://localhost:8080>, sign in, go to **Settings**, paste the token into the GitHub connection field and
save, then add the repository `Jaydee94/remedy`. The **Timeline** (home) shows "connected" and "added" entries.

## 4. A real red check: the Renovate pull request

Pull request 20 (the TypeScript 7 update) fails `npm ci` while it is open; if it has been closed or fixed since, skip to section 5. Within about a minute of adding the
repository the Timeline shows an **incident opened** entry for it, then **diagnosing**, and a few minutes later
**diagnosis finished**. Open the incident: the diagnosis should name the lock file mismatch in `web/`.

## 5. A deliberately red pull request, fixed again

This part pushes to GitHub with your own credentials. From a clean checkout of Remedy:

```sh
git switch -c real-run/red-check origin/main
cat > internal/store/zz_real_run_test.go <<'EOF'
package store_test

import "testing"

func TestDeliberatelyRedForTheRealRun(t *testing.T) { t.Fatal("deliberately red: remove this file") }
EOF
git add internal/store/zz_real_run_test.go
git commit -m "test: deliberately red check for the Remedy real run (do not merge)"
git push -u origin real-run/red-check
gh pr create --title "do not merge: deliberately red check for the Remedy real run" \
  --body "Opened on purpose to see Remedy detect, diagnose and resolve a red check. Closed without merging."
```

When CI has failed, the Timeline shows an incident for the new pull request and, shortly after, its diagnosis;
the diagnosis should name `internal/store/zz_real_run_test.go`. Then fix it:

```sh
git rm internal/store/zz_real_run_test.go
git commit -m "test: remove the deliberately red check"
git push
```

When CI is green, the incident resolves with the reason "the check turned green", and the Timeline shows it.
Finally close the pull request without merging and delete the branch:

```sh
gh pr close --delete-branch
git switch main && git branch -D real-run/red-check
```

## 6. The audit

- Writes: `grep 'github request' server.log | grep -vc 'method=GET'` must print `0`, and
  `grep -c 'refused' server.log` must print `0`.
- Requests: `grep -c 'github request' server.log` and, per status, `grep -o 'status=[0-9]*' server.log | sort | uniq -c`.
- The token: `scripts/check-no-token-leak.sh http://localhost:8080 server.log runner.log "$REMEDY_DB"` (from the
  Remedy checkout, with `REMEDY_ADMIN_PASSWORD` set). It asks for the token with the input hidden, and searches the
  logs, the database file and every admin API response. It must end with "the token appears nowhere that was searched".

## 7. Clean up

Stop the server and the runner. Revoke the token at <https://github.com/settings/personal-access-tokens> unless
you keep using it. `~/remedy-real-run` holds the database (with the sealed token) and the logs; delete it when
you no longer need it.
