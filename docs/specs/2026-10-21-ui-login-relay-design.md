# The runner's login from the Setup page (K-7)

Status: approved by the maintainer on 2026-10-21 (this spec, the exception of `docs/design.md` 2.7.1 and the matching hard rule of `CLAUDE.md`). The terms of use must still be re-read in their primary text before the feature is offered to anyone but the maintainer (section 4). Nothing in this document is built yet.
Parent documents: [`2026-10-06-kubernetes-deployment-design.md`](2026-10-06-kubernetes-deployment-design.md) (sections 4, 10.1 and 11: this is the "K-7" it demands),
[`../design.md`](../design.md) (2.1, 2.7, the exception 2.7.1 and 4),
[`../research/subscription-cli-usage.md`](../research/subscription-cli-usage.md) (the terms-of-use constraints; on conflict the research wins until it is updated),
[`../research/k8s-s5-login-relay.md`](../research/k8s-s5-login-relay.md) (the spike: what was measured), [`../research/k8s-s2-login-check.md`](../research/k8s-s2-login-check.md) and [`../research/k8s-runner-status-real-run.md`](../research/k8s-runner-status-real-run.md) (the login check),
[`2026-10-21-self-hosted-for-others-design.md`](2026-10-21-self-hosted-for-others-design.md) (cycle 7: other operators, API-key mode).
**Not legal advice.** Section 4 quotes the research file; the vendor's legal page was **not** read again (no network was used), so the quotes are only as current as 2026-10-02. Everything marked **(call)** is the author's and open to change in the plan. What was and was not measured is stated where it matters; the CLI behaviour that S5 did not measure is listed in section 13 and settled only by the real run.

## 1. Goal and scope

Today the runner's CLI login is `kubectl exec -it remedy-runner-0 -c runner -- /opt/claude/claude`, then `/login` (`web/src/runnerstatus.ts` `loginCommand`; Setup shows it when the login is missing). The maintainer decided on 2026-10-21 that the login can also be done from the Setup page, **way A** of K-7: the runner starts the unmodified `claude auth login`, the page shows the sign-in link, the maintainer signs in at the vendor and pastes the one-time code into the page, the control plane hands it to the runner, the runner writes it to the CLI's stdin. Success is confirmed with `claude auth status`, as a boolean only.

### Non-goals

- Reading, copying, storing or displaying anything the CLI writes (tokens, `.claude.json`, backups); only **names** in the backup directory are ever listed (section 9).
- Way B, `claude setup-token` into a Secret (section 11); API-key mode and any login for it (cycle 7b); `--console`, `--sso`, `--email` logins (only `--claudeai`, S5).
- A login for any account other than the operator's own; several runners or several sessions (one session, one runner); a notification on expiry; re-login by a schedule.
- Running the login while a run is active (section 9); a UI setting that turns the relay on (section 12); a PTY (only as the fallback of section 13).

## 2. Decisions

| # | Decision | Source |
|---|---|---|
| D1 | **Mechanism:** the runner starts `claude auth login` as a child with **pipes** (no PTY), reads stdout for the URL, the control plane shows it, the maintainer pastes the code, the runner writes the code plus `\n` to the child's stdin, then confirms with `claude auth status` (boolean only). Measured in S5: URL on stdout without a TTY, a line on a piped stdin is read and judged. **Not measured:** a valid code (exit code, output), a code without a newline, a second attempt in one process, the stream of the invalid-code message. | maintainer |
| D2 | **Policy:** the first place where Remedy's code carries login material (the URL: OAuth state and a PKCE challenge; the code: a one-time secret). An **exception** is recorded in `docs/design.md` 2.7.1 and in the hard rule of `CLAUDE.md` (section 4, section 17). The rule stays; the exception is narrow, named, in memory only. | maintainer |
| D3 | **Way B** is a documented alternative, not built here (section 11). `kubectl exec -it` stays as the always-available fallback and stays on the page. | maintainer |
| D4 | The page offers the login only in **subscription mode** (cycle 7's API-key mode hides it); the seam is in section 12. | maintainer |
| D5 | **Transport (call):** a dedicated long-poll by the runner (`POST /runner/v1/login/poll`) for start, code and cancel, and a report call for the URL and the outcome; the claim loop and the status post are not touched (section 5). | author |
| D6 | **One session at a time, TTL 10 minutes, memory only (call).** Sessions, URL and code live in the control plane's memory; a restart ends the session (section 6). | author |
| D7 | **A wrong code restarts the process (call):** S5 left the CLI alive after `Invalid code`, but whether it reads a second line is unmeasured, so a new attempt always kills the child and starts `claude auth login` again, with a new URL. Works whichever way the CLI behaves. | author |
| D8 | **No run while a session is open (call):** a start is refused while a run is running; while a session is open the control plane hands out no run (section 9). | author |
| D9 | **Off by default (call):** `REMEDY_LOGIN_RELAY=on` on both processes, one chart value; not a UI setting (section 12). | author |
| D10 | The relay never uses `runner.Execute` and never posts CLI output as an event; it has its own reader and a fixed vocabulary (section 7). | author, from the code |

## 3. Components, and what the code showed

| Piece | Where | What |
|---|---|---|
| Rules | `internal/loginrelay` (new) | `ValidateURL`, `ValidateCode`, the reason vocabulary, `Material` (a `[]byte` holder with `String`, `GoString`, `MarshalJSON`, `LogValue` all answering `***`, and `Zero()`), the wire types, the session `Manager` (section 6). Stdlib only. |
| Command | `internal/provider` | `Claude.LoginCommand(ctx, parentEnv)`: `auth login --claudeai`, the filtered environment **plus `DISABLE_UPDATES=1` for this child only** (finding 2). |
| Driver and poller | `internal/runner/login.go` (new), `cmd/remedy-runner` | The child, its pipes and reader, the poll loop as a fourth goroutine next to `Reporter` and `LoginChecker`; `LoginChecker` gets a pause hook and a kick. |
| Server | `internal/server/loginrelay.go` (new), `internal/app`, `internal/config` | Four admin routes, two runner routes, the claim gate, `Deps.Login` (nil: no routes), `REMEDY_LOGIN_RELAY`, a fact on Setup. |
| Activity | `internal/store` (`AddActivity`) | Four kinds; no migration (`activity.kind` has no CHECK). |
| UI | `web/src/loginrelay.ts` (pure), `web/src/components/setup/LoginRelay.tsx`, `api.ts` | Inside `RunnerSection`, above the existing command. |
| Chart | `deploy/chart` | `loginRelay.enabled` (default false) sets the variable on the server and the runner. |
| Tests helper | `internal/testutil` | `FakeClaudeRelay` (section 13). |

Findings from reading the code that shaped this spec (all read-only):

1. **`runner.Execute` posts every stdout and stderr line, unredacted, as a run event** (`internal/runner/execute.go`; the control plane stores events as they come, `server/runnerapi.go` `postEvent`, and `internal/redact` is not applied to them). Using it for the login would write the URL and whatever the CLI prints into `run_events` and SSE. Hence D10. Also `bufio.Scanner` waits for a newline and the prompt `Paste code here if prompted > ` has none (S5), so the reader works on bytes.
2. **`provider.FilterEnv` drops `DISABLE_UPDATES`.** The chart sets it in the container (`runner-statefulset.yaml`), and the `kubectl exec` login inherits it, but a child started by the runner gets only the allowlist. The maintainer measured a 244 MiB staged copy under `$HOME/.local` after an interactive login without it. So the login child needs the variable explicitly, without widening the allowlist that runs use. Whether it keeps `.local` away is itself unverified (CLAUDE.md).
3. **The runner can only be reached by pull.** Its NetworkPolicy has no ingress and `automountServiceAccountToken` is false; its egress is DNS, the internal port and 443 to public addresses, which is what the CLI needs to exchange the code at the vendor. `Loop.Run` is sequential (a claim long-poll, then one run); `Reporter`, `LoginChecker` and the run heartbeat are separate goroutines. A fourth goroutine fits; the claim loop does not (it is busy during a run). `postRunnerStatus` decodes strictly (`DisallowUnknownFields`), so a new field there would break an older server: D5 avoids it.
4. **The control plane has no access log and no logging in `internal/server` at all** (checked by grep: the only server log lines are in `cmd/remedy-server`, `internal/app` and the gatekeeper's debug line, which never carries arguments). So a leak by the existing code is unlikely; the risks are new code, `http.Server` panic lines (no body), and a reverse proxy that logs bodies or URLs (so nothing goes in a path or query).
5. **`ClaimNext` sets `running` in the same statement that selects the run** (`store.go`), so "a run is running" is a fact the store can answer atomically against a claim (section 9).
6. **`LoginChecker` rechecks every minute while the login is missing** and would read the config while `auth login` rewrites it (S5: backup file at start, lock directory). A flapping answer is avoidable by pausing it.
7. **Cycle 7 §4 item 1 says "No image, chart, script or document may ship, collect or relay a login."** The intent is the maintainer's login not reaching others, but the words also cover this feature. That sentence needs a clarification (section 17); I did not edit the approved spec.

## 4. Terms of use, and the exception

The research (2026-10-02) says it is forbidden to **"collect, store or intermediate** Claude.ai credentials or session tokens, or to route requests through subscription credentials on behalf of others"; still allowed is **"an end user signing in to the unmodified Claude Code binary with their own subscription"**. Consequences, none of them legal advice:

- The relay is a **borderline case**. Remedy would **intermediate** a one-time authorization code between the maintainer's browser and the unmodified binary. A one-time code bound to a PKCE challenge is not a token and the CLI itself exchanges it, so Remedy never sees a credential or a session token (assumed, not measured: section 13); but the research does not say where "intermediate" ends, and I cannot settle it.
- **For the maintainer's own installation** the page replaces a terminal: the same person, their own login, the unmodified binary, a paste of the vendor's own code into the vendor's own prompt (`Paste code here if prompted >` exists for a browser on another machine; that reading is my inference). Accepted by the maintainer.
- **For other operators (cycle 7)** it weighs more: a public tool whose UI handles login material of strangers may be read as "intermediating" credentials. Hence D9 (off by default) and the rule that the primary text is **re-read before this feature ships to anyone else**, together with cycle 7's open point 1. If the reading is unfavourable the feature is removed for others and `kubectl exec -it` remains.
- What does not change: the CLI is started unmodified; Remedy never reads, copies, stores or shows what the CLI writes (tokens, `.claude.json`, account fields from `auth status`); the CLI's own output is never forwarded.

**The exception, as recorded in `docs/design.md` 2.7.1** (full text there): two pieces of login material pass through Remedy's code, only these two, in memory only, for one attempt of at most ten minutes, never in a database row, activity entry, log line, event, error or answer to a browser, the code write-only; subscription mode only; opt-in. **The hard rule in `CLAUDE.md`** keeps its sentence and names the exception (section 17).

## 5. Transport (a)

The runner only dials out (finding 3). Two calls on the internal port, behind the runner token like every `/runner/v1` route:

- `POST /runner/v1/login/poll`, body `{"boot": "<random id of this runner process>", "session": "<id or empty>", "ack": <seq>}`. The server holds it up to 25 s (the claim's `claimWait`; a wake-up channel, not the claim's 500 ms tick) and answers `204`, or `200 {"seq": n, "session": id, "action": "start" | "code" | "cancel", "code": "..."}`. `code` is present only for `code`. Commands carry a sequence number; the runner sends the last number it executed as `ack`; an unacknowledged command is sent again; the runner executes each number once (a lost response cannot write the code twice). The server drops its copy of the code when it is acknowledged.
- `POST /runner/v1/login/report`, strict JSON like the status post (`DisallowUnknownFields`, nothing after the object, 4 KiB): `{"session": id, "state": "awaiting_code" | "succeeded" | "failed", "attempt": n, "url": "...", "reason": "..."}`. `url` only with `awaiting_code`, `reason` only with `failed`, from the vocabulary of section 6; the server validates both again (section 8) and answers `204`.

The runner starts the poll goroutine only when `REMEDY_LOGIN_RELAY=on` (section 12); a server without the relay has no route and answers `404`, on which the runner backs off 60 s. The poll is an authenticated request, so `RunnerStatus.Touch` still counts it.
**Runner offline:** a start is refused with `runner_offline` unless a poll was seen in the last 45 s (`runnerGrace`). **Gone mid-session:** no poll and no report for 60 s fails the session `runner_lost`. **Restarted mid-session:** the child dies with the process; the new process polls with a new `boot` and no session, the server fails the session `runner_restarted`. **Control plane restarted:** the session is gone; the runner's next poll names a session the server does not know, the answer is `cancel` for it, the runner kills the child. Neither side keeps a session across a restart (D6).
The code and the URL cross the cluster network in clear over HTTP between two pods (accepted risk R2 for the runner token and the run tokens, now for login material too; the NetworkPolicy admits only runner pods on the internal port; a service mesh or TLS inside the cluster is a retrofit, not this cycle).

## 6. The session and the API (b)

States, held in `loginrelay.Manager` behind a mutex, with an injectable clock:

```
idle -> starting -> awaiting_code -> submitting -> succeeded
              |           |              |-> (invalid code, attempt < 3) -> starting   [new process, new URL]
              +-----------+--------------+-> failed(reason) | cancelled | expired
```

`starting`: the start command is queued and the child is being started; 30 s without a URL fails `no_url`. `awaiting_code`: the URL is known, the page shows it. `submitting`: the code was accepted by the API; 60 s without a result fails `timeout`. A session lives at most 10 minutes from its start in any non-terminal state, then `expired`; a ticker in the `Manager` (every 5 s) applies the time rules and writes the activity entry even when no browser is open. The last terminal session stays readable for 10 minutes without URL or code, then the view says `idle`. A `succeeded` is reported by the runner only after its own confirmation (section 9).

**Fixed reasons (call):** `no_url`, `bad_url`, `invalid_code` (shown with "I started a fresh sign-in", up to 3 attempts, then failed), `process_exited`, `not_confirmed` (the CLI ended well, `auth status` did not say logged in), `timeout`, `runner_lost`, `runner_restarted`, `expired`, `cancelled`. Refusals before a session exists answer `409` with a reason: `disabled`, `runner_offline`, `run_active`, `already_active`, `api_key_mode`. The UI maps each to one template sentence; an unknown word shows a generic one.

**Admin routes** (session cookie plus `X-Remedy-CSRF: 1` on every non-GET, as `session` does; `Deps.Login == nil` registers none and the page treats `404` as "not offered", as `hideOnError` already does for `GET /api/runner`). **Every answer, errors included, carries `Cache-Control: no-store`.** Session ids are random 128-bit values, in bodies only: no path or query ever carries one.

| Route | |
|---|---|
| `GET /api/runner/login` | `{"available": bool, "unavailable": "<reason>", "session": null or {"id", "state", "attempt", "url", "reason", "expiresAt", "waitingRuns": n}}`. `url` only in `awaiting_code`. Never a code, in any state. |
| `POST /api/runner/login` | Start. `201` with the session (state `starting`), or `409` with a reason above. Body: `{"replace": true}` required when the runner is logged in, so a re-login is a conscious click. |
| `POST /api/runner/login/code` | `{"session": id, "code": "..."}`. `202`; `400 invalid code` for a bad shape (the message never echoes the value); `409` when the session is not `awaiting_code` or the id is not the current one; `429`. |
| `POST /api/runner/login/cancel` | `{"session": id}`; `204`; kills the child. |

**Rate limit (call, new: the admin API has only the per-IP password lockout today, which `Auth` keys by TCP peer):** a global counter, since there is one admin and behind an Ingress every request shares a peer: at most 5 starts per 10 minutes and 10 code submissions per session, `429` with `Retry-After`. Body limit 1 KiB on the code route, strict decode, `DisallowUnknownFields`.
Activity entries (written by the control plane when the state is reached, summary a fixed template, `data` `{}` or `{"reason": "<word>"}`): `login_started`, `login_succeeded`, `login_failed`, `login_cancelled` ("The runner's login was started from the Setup page."). No URL, no code, no account text, no run id (a login is not a run: no `runs` row, no event, nothing on the Ask page).

**The UI is a wizard (the maintainer asked for one, 2026-10-21).** `LoginWizard.tsx` inside the runner section (a step panel in the section, not a modal, so the page keeps its focus order and a reload lands on the same step); `loginwizard.ts` is a pure module, tested by `node --test`, that maps the session view to the current step and builds every sentence as a template (nothing the runner sends decides a sentence's structure). The step comes from the server's state, not from a client counter, so a reload or a second tab shows the same step. A button "Log in from here" (or "Log in again", with a confirmation, when logged in) opens the wizard; a step indicator ("Step 2 of 4", with the four names) is shown, as text for a screen reader and as a bar:

| Step | Server state | What the page shows | Actions |
|---|---|---|---|
| 1. Start | `idle`, `starting` | What will happen in two sentences (the runner starts the vendor's own sign-in, you open a page of the vendor, you paste a one-time code here, the code is not stored); why it might be refused (`run_active`, `runner_offline`, `disabled`); while `starting` a short wait | "Start sign-in", "Cancel" |
| 2. Open the sign-in page | `awaiting_code` | The link as `claude.com` (host only as text), "Open the sign-in page" (new tab, `rel="noopener noreferrer"`), "Copy link"; one sentence: sign in with your own account, then the page shows a code | "Next" is not a button: the step ends when the user starts typing a code |
| 3. Paste the code | `awaiting_code`, `submitting` | A code field (`autocomplete="off"`, `spellcheck="false"`, no draft kept in `localStorage`: the usual "unsent draft" convenience does not apply here), an expiry countdown from `expiresAt` | "Send code", "Back to the link", "Cancel" |
| 4. Result | `succeeded`, `failed(reason)`, `cancelled`, `expired` | One sentence per fixed reason (section 6); on `invalid_code` "I started a fresh sign-in" and the wizard returns to step 2 (up to 3 attempts); on success "The agent is logged in" and the runner card turns green | "Done", "Try again" |

Accessibility and the repository's UI rules: the step panel is a live region that announces by key (`useAnnouncement`), focus moves to the step heading when the step changes (`useFocusRestore`), a button that starts a request keeps focus (`aria-disabled` plus a guard), the input is cleared on submit, on cancel and on unmount, and the wizard works with the keyboard alone and at phone width. The page polls the view every 2 s while a session is open (10 s otherwise, as now). The `kubectl exec` command stays below the wizard as "or do it in a terminal", and it is the only thing shown when the relay is off (`available: false`). The real-browser check (section 13) walks all four steps with the fake `claude`, including a wrong code, a cancel and a reload in the middle.

## 7. Handling of the material (c)

| Surface | Rule |
|---|---|
| Memory | URL and code live in `Material` holders, `[]byte`, zeroed when acknowledged, when the session ends and on error paths (the runner zeroes the code right after the write, the server when the poll is acknowledged). **Go cannot promise this:** the HTTP stack and `encoding/json` keep their own buffers, strings are immutable, a core dump would hold what is live. Zeroing shortens the life of the long-lived copies; the protection is the short life (seconds for the code, at most 10 minutes for the URL) and the absence of any sink. Not verified: swap, core dump settings of the pod. |
| Database, activity, events, SSE | Nothing. There is no column, no table, no `runs` row; the activity data is `{}` or a reason word; the session view is read from memory. |
| Logs | The relay logs only fixed events with fixed fields: `login session`, `state`, `attempt`, `reason`, `exit` (a number), and at debug level `login output` with `stream`, byte count, line count and booleans for the fixed phrases it recognises. **Never** a line of the CLI's output, an `err` from the process or the pipes, a URL, a code. `Material` answers `***` to `%v`, `%#v`, JSON and `slog`, so a careless `log.Info("x", "code", m)` cannot leak. The server has no access log (finding 4); a proxy in front may log, so no material is ever in a path, a query or a header. |
| Errors | Fixed sentences. A decode error is `invalid request body`, as the other handlers do. |
| Runner and the CLI | Never `Execute`. Everything the child prints after the URL line is read and dropped except the match for the invalid-code sentence; the output of a successful login (it may name an account, S2's warning) is dropped unread. `auth status` is run through the existing `LoginCheck`, whose output never leaves it. |
| Browser | `no-store`; `rel="noopener noreferrer"` on the link (the Remedy host must not reach the vendor as a referrer); the code is not rendered back, not in a toast, not in the URL, not in `localStorage` or `sessionStorage`. |
| Agents | No run is active while a session is open (section 9), so the agent CLI never shares the pod's lifetime window with the code; R10 (the agent's uid can read `/proc`) is not widened. |

## 8. Validation (d)

**URL**, in Go (`ValidateURL`, before the runner reports it and again in the control plane) and mirrored in `loginrelay.ts` (the page does not trust the server): at most 2048 bytes; printable ASCII only, no whitespace or control character or backslash; parses; scheme exactly `https`; host exactly `claude.com` (no port, no userinfo, no trailing dot, case-sensitive, so look-alikes and `claude.com@evil.test` fail); path exactly `/cai/oauth/authorize`; no fragment; every query name in the measured set (`code`, `client_id`, `response_type`, `redirect_uri`, `scope`, `code_challenge`, `code_challenge_method`, `state`), none twice; `code_challenge` and `state` present. Values are not judged (S5 omitted them; the real run records their shape). A CLI version that adds a parameter fails closed with `bad_url`; the pin is bumped by hand (`deploy/cli-pin.yaml`) and the real-run check repeats. This guards against output from anything but the unmodified CLI (a replaced binary, a poisoned image) sending the maintainer to a phishing page; it is not a defence against the vendor.
**On an invalid URL** the runner reports `failed/bad_url` without the URL and kills the child; the control plane never stores or shows one; the page says "I got a sign-in address I do not recognise, so I did not show it" and offers the terminal command. A URL that is valid but not shown by a page that rejects it is the same case.
**Code** (`ValidateCode`, the page, the control plane, **and the runner before it writes**, because it writes control-plane text into a process's stdin): trimmed of surrounding whitespace, 8 to 512 bytes, only `0x21` to `0x7E` (so never `\n`, `\r`, a space or a control character: one line, nothing smuggled onto a second prompt). The real format is **not measured**; this is deliberately loose and is tightened or relaxed by the real run.

## 9. Interaction with runs, the status check, process hygiene (e, f)

**Runs.** A login rewrites the CLI's config (S5: a backup at start, a lock directory) and an agent must not start in the middle of it. A start is refused (`run_active`) when the store has a run `running`. **Atomically (call):** the claim handler takes the read side of an `RWMutex` around each `ClaimNext` and skips it (`204`) while a session is open; a start takes the write side, checks the store (finding 5) and opens the session. So a run is either `running` before the session opens (refused) or not claimed until it ends. Queued runs wait at most the session's 10 minutes; the page says "N runs wait for this". The runner has a second guard: a start while its loop is busy answers `failed/run_active` (belt and braces; it should never fire). `POST /api/runs`, the responder and the poller keep queuing.
**Status check.** The runner's `LoginChecker` is paused while a session is open (a new `Pause func() bool`, checked each cycle) so it cannot read a config mid-rewrite and flap the published state, and is **kicked** at once when the session ends, so the page turns green within seconds instead of a minute.
**Success** is: the child exited 0 **and** a following `LoginCheck` is `ok`. Because S5 did not measure whether the CLI exits on success, a second path exists **only when the login was missing before the attempt**: while `submitting`, the runner runs `LoginCheck` every 2 s; on `ok` it gives the child 10 s to exit by itself (SIGTERM, then the kill below), then reports success. When it was `ok` before (a re-login) only the exit code counts and a still-running child after 60 s is `timeout`. Both paths are settled by the real run.
**The child.** `exec.CommandContext` with its own context, `Setpgid`, `cmd.Cancel` = SIGTERM to the process group, `WaitDelay` 3 s (Go then kills the child), and after `Wait` a best-effort SIGKILL of the group for stragglers (an opener the CLI starts for "Opening browser…" may exist; the distroless image has no `xdg-open`, which is why that step fails silently, S5). **(call)** SIGTERM first, not SIGKILL: in S5 `timeout`'s SIGTERM left no lock directory, and a SIGKILLed login may leave `.claude.json.lock/` behind (unmeasured; the real run kills one on purpose and looks). The group's pgid is the only process-level state kept. No precedent for `Setpgid` exists in the repository; it is POSIX and the runner builds only for Unix. Kill on cancel, on expiry, on `runner_lost`, on `bad_url`, on a restart for a new attempt and when the runner's context ends; `main` waits up to 5 s for the relay goroutine before it exits, and a test counts child processes afterwards.
**Backups.** `$CLAUDE_CONFIG_DIR/backups/.claude.json.backup.<ms>` accumulates one file per attempt (S5). **(call)** After a terminal state the runner lists that directory **by name only** (`os.ReadDir`, no `Open` of an entry, no `Stat` of contents) and removes entries matching `^\.claude\.json\.backup\.[0-9]{13}$` except the 5 newest by the number in the **name** (not mtime). It never opens, reads, copies or lists any other path of the login directory, and it is the only thing Remedy's code does there. If the maintainer prefers, this cleanup is dropped; nothing else depends on it (the files are small and the volume is persistent, S5).
The environment of the child is `FilterEnv(os.Environ())` plus `DISABLE_UPDATES=1` (finding 2); `ANTHROPIC_API_KEY` stays out as always, and in `api_key` mode there is no login at all (section 12).

## 10. Failure and abort (g)

| Situation | Result |
|---|---|
| Wrong or expired code | `invalid_code`; the runner kills the child and starts a new one (D7); the session goes back to `starting`, the new URL appears, the page says the code was not accepted. After 3 attempts, `failed/invalid_code`. |
| The CLI prints no URL in 30 s, or an unrecognised line first | `failed/no_url` (the output is dropped, only its size is logged at debug). |
| The CLI exits before or after the code without success | `process_exited` or `not_confirmed`; the terminal command is offered. |
| The maintainer closes the page mid-session | The session continues until the TTL; a later visit shows it. Cancel works from any tab. |
| A second tab or a stale tab submits a code | `409` (not the current session or not `awaiting_code`). |
| Start while a run is running / the runner is offline / a session exists | `409` with `run_active` / `runner_offline` / `already_active`; no session. |
| Control plane or runner restarts | Section 5: the session ends, the child is killed, the activity entry says `login_failed` (`runner_restarted`) or, if the control plane died, there is none (it was memory). |
| The relay is switched off | No routes (`404`), the runner does not poll; the page shows the terminal command only. |
| Login succeeds but the pod later says unknown | Not this feature's concern: the status check and a failed run are the truth (S2: an expired login probably still reads `ok`). |

## 11. Way B, and why it is not built here

`claude setup-token` prints a long-lived (one-year, per the research) OAuth token that is passed as `CLAUDE_CODE_OAUTH_TOKEN`. It is documented here as the alternative that needs no relay of a code, but **not built in this cycle**: the token would live in a Kubernetes Secret and in the runner's environment, readable by the agent's uid through `/proc` (the same exposure as R10, with a longer-lived credential); `provider.FilterEnv` already lets the variable through, but that is a standing exception of its own to be decided; the token is a credential in its own right and the hard rule is about exactly that; `setup-token` was never run (S5 read only its help), so its prompts and stream are unknown. It belongs with cycle 7b's credential modes, which already decide how a long-lived secret reaches the runner and how it is scrubbed. Until then `kubectl exec -it` is the fallback of both features.

## 12. The switch, and the mode (i, D4)

`REMEDY_LOGIN_RELAY` (`on`, anything else or unset is off) on the **control plane** and on the **runner**; the chart has one value, `loginRelay.enabled: false`, that sets both. Default off **(call)**: the feature changes what the system handles (login material), the terms are borderline for others (section 4), and the maintainer turns it on in his own values. A runner without it has no poll goroutine, so no code path that writes to a CLI's stdin is reachable; a server without it registers no route. **Not a Setup setting (call):** design 2.7 keeps bootstrap values (the master key, listener addresses, token files) in Helm and the environment; this one belongs there because anyone holding an admin session could otherwise turn it on, and cycle 7 puts the billing mode out of the UI for the same reason. Setup shows it as a read-only fact ("Login from this page: on/off") through `Facts`.
**The mode.** Today only subscription mode exists, so `RunnerStatus` gets one function, `authMode()`, that returns `"subscription"` (server) and a constant of the same value (runner). Cycle 7b has the runner report `authMode` in the status post; this feature then reads it from `RunnerView`: the page and `POST /api/runner/login` refuse with `api_key_mode` for anything but `subscription`, and the runner refuses a `start` in key mode from its own environment (`REMEDY_AUTH_MODE`). No other change is needed here; the sentence to add to cycle 7 is in section 17.

## 13. Tests (j) and the real run (h)

**Fake CLI.** `testutil.FakeClaudeRelay(t, mode)` writes a POSIX `sh` script (no `stat -f`, `sed -i ''`, `date -j`, no `bash`-isms; `read -r`, `printf`, `trap`, `[ -f ]` only). Called as `auth login ...` it prints the three pieces of S5 (`Opening browser to sign in` plus U+2026 written as `printf '\342\200\246\n'`, `If the browser didn't open, visit: <planted URL>`, and `Paste code here if prompted > ` without a newline), then `read -r line`. Modes: `valid-exits` (planted code: print a line, create a marker file, exit 0), `valid-stays` (marker, stays alive until signalled), `invalid-stdout` / `invalid-stderr` (the S5 sentence on that stream, stays alive), `no-url`, `bad-url` (another host), `exit-early`, `lingering` (a background `sleep` holding the pipes), `ignores-term` (traps TERM, so only the kill ends it). Called as `auth status --text` it exits 0 iff the marker exists, else 1 with the S2 sentence. The script writes its marker in its own temp directory and never touches a real config.
- **State machine** (`internal/loginrelay`, fake clock): every arrow of section 6 and every illegal one (code in `starting`, start twice, cancel in `idle`), the 30 s, 60 s and 10-minute timers, the three attempts, `ack` repeat idempotency (a code delivered twice is written once), runner lost, runner restarted (new `boot`), server restart (unknown session answers `cancel`), the rate limits, no URL or code in any view after a terminal state.
- **Validators:** a table for `ValidateURL` (valid shape with fake values; `http`; `HTTPS`; `claude.com.evil.test`; `claude.com@evil.test`; `evil.test#claude.com`; `CLAUDE.COM`; a Cyrillic look-alike; `claude.com:443`; a trailing dot; a wrong path, `/cai/oauth/authorize/`, `%2e` and `..`; a fragment; a backslash, a space, a control character; 2049 bytes; a duplicate, an unknown and a missing query name; empty) and for `ValidateCode` (empty, 7 and 513 bytes, a space inside, `\n`, `\r`, a tab, NUL, non-ASCII, the plain good case); the same tables run in `node --test` against `loginrelay.ts`, and a test pins that both sides agree on a shared list of cases.
- **API:** missing session cookie `401`, missing CSRF header `403` on each of the three non-GET routes, `no-store` on every answer including `409`, `400` that does not echo the code, `409` for the wrong state or id, `429` after the limits, `404` with the relay off, the claim gate (a start while a run is running is refused; with a session open a queued run is not claimed; a claim racing a start under `-race`), the runner routes refuse a wrong token.
- **The no-leak test (`internal/app`, the whole chain: real server handlers, the real runner code, the fake CLI):** plant a code and a URL whose substrings are unique; set every logger (server, runner, app, gatekeeper) to a buffer at Debug; run a full session with success and one with an invalid code; then assert that the planted strings appear **nowhere** in: the log buffers, a dump of every table of the database (every column of every row from `sqlite_master`), the activity API and its SSE stream, `GET /api/runner` and every answer of the relay's GET after the session ends, and the response and request records of `httptest` outside the two bodies that must carry them (the report with the URL, the poll with the code, the admin code POST). A second case plants them in the CLI's stderr after the URL line and asserts they are dropped. A `Material` test proves `%v`, `%+v`, `%#v`, JSON, `slog` text and JSON handlers all print `***`.
- **Process hygiene:** after cancel, expiry, `bad_url`, a restart for a new attempt and runner shutdown no child of the fake CLI remains (counted by a marker pid file and `kill -0`), including `ignores-term` and `lingering`; the backup cleanup removes only matching names beyond the 5 newest, keeps everything else and opens nothing (the test directory holds files that fail the test if read: unreadable mode, run as a normal user).
- **Chart:** a render test that the value defaults to off, that on sets the variable on the server and the runner only, and that no NetworkPolicy changes.
- **UI:** `node --test` for `loginrelay.ts` (every state's sentence, the reason table with an unknown word, the button and field states, the URL and code mirrors); **a real browser look** (Playwright against a throwaway server and a real runner binary with the fake CLI, as `CLAUDE.md` requires for UI work): the states, the focus after each, the live region, the narrow width, that the code field is empty after submit and that no code is in the page's storage or network log after the session.
- **The real run**, with the maintainer present, the last plan task (section 14, task 11). Runbook `docs/runbook/login-relay-real-run.md`, record `docs/research/login-relay-real-run.md`. Preconditions: the dummy setup (`make dummy-up`), the maintainer runs `make dummy-logout` (his confirmation; no script of ours touches the directory), `loginRelay.enabled` in the dummy values, an agent never opens the login directory. The run settles, each recorded as a shape (counts and booleans, never content): (1) a **valid code**: does the CLI exit, with which code, after how long, and does `auth status` turn `ok` before or after the exit; (2) which **stream** carries the invalid-code sentence, and whether a stray prompt follows; (3) the **format** of the code and the shape of the URL's values (confirms section 8); (4) a deliberately **wrong code** and the restart; (5) that **no output of a successful login** reaches any sink; (6) `.local` and the `backups/` names and any `.claude.json.lock/` after a SIGTERM, after a SIGKILL and after a success; (7) whether **`DISABLE_UPDATES` keeps `.local`** away under the relay's child; (8) a re-login while logged in; (9) the time to the URL; (10) a cancel mid-session and a runner pod deletion mid-session. **Not settled by it unless needed:** a code without a newline (only if the newline form fails) and a second line in one process (the design does not use it). **Fallback "yes with a PTY":** if the pipe path fails on a valid code (the CLI ignores a pipe for the code, or behaves differently), the verdict becomes "yes with a PTY"; that needs a PTY in the runner (a new dependency such as `creack/pty`, or raw `x/sys/unix`; the image has no `script`), so this spec is amended and re-approved before any code, and the maintainer decides between that and keeping only the terminal.

## 14. Order of work

Each task is test first where it has logic, and small. The exception text is already recorded in this commit; the first task of the plan is the maintainer's approval of it.

1. Done on 2026-10-21: the spec and the exception are approved, and 2.7.1 and the hard rule lost their "draft" mark. Still to do: clarify cycle 7 §4 (section 17).
2. `internal/loginrelay`: `Material`, `ValidateURL`, `ValidateCode`, the reasons; their tables.
3. `Manager`: the state machine, timers, rate limits, commands and acks, activity hook; its transition tests (no HTTP).
4. Server: `REMEDY_LOGIN_RELAY` in `config`, the admin and runner routes, `Deps.Login`, the claim gate, `no-store`, the Setup fact; the API tests.
5. `provider.LoginCommand` and `testutil.FakeClaudeRelay`.
6. Runner driver and poller: reader, child hygiene, `LoginChecker` pause and kick, `main` wiring; the process tests.
7. The chain and **no-leak** test in `internal/app`.
8. Web: `loginrelay.ts` and its tests, `api.ts`, `LoginRelay.tsx` in `RunnerSection`; the real browser check.
9. Chart value, render tests, README and runbook sections, `docs/design.md` and `CLAUDE.md` current-state lines.
10. Backup cleanup (droppable).
11. **The real run** (section 13), the record, and the decision gate: "yes", "yes with a PTY" (amend this spec) or "no" (remove the feature, keep the exception unused or withdraw it). Only then does the status line say "implemented".

## 15. Risks

| Risk | Decision | Retrofit |
|---|---|---|
| **The terms of use** (section 4): the relay may be read as "intermediating" credentials, more so for other operators. | Off by default; own login only; the primary text re-read before others get it; the fallback stays. | Remove the relay for others; the terminal login. |
| **Login material in memory** of two processes and on the wire inside the cluster (HTTP, R2). | Short life, no sink, `Material`, zeroing, NetworkPolicy; the no-leak test. Go cannot guarantee zeroing. | TLS between the pods; a PTY-less handover that never leaves the runner (not designed). |
| **A phishing link from a compromised runner image.** | URL validator on both sides and in the page; the host is shown. | None needed. |
| **A leak through a reverse proxy** that logs bodies. | Nothing in paths or queries; the operator's proxy is theirs (said in the runbook). | None. |
| **Unmeasured CLI behaviour** (valid code, streams, exit, lock after SIGKILL). | Defensive design (restart, status check, SIGTERM first); the real run settles it; nothing ships before. | A PTY. |
| **An aborted login leaves the config half-written** or a stale lock. | No run during a session; SIGTERM first; the real run looks. | A repair by `kubectl exec`. |
| **The 244 MiB `.local` copy** returns under the relay's child. | `DISABLE_UPDATES=1` on the child; the real run checks. | Delete by hand. |
| **The cleanup touches the login directory.** | Names only, one pattern, droppable. | Drop it. |
| **An older server or runner.** | The relay uses new routes only; the status post is unchanged; a missing route is "not offered". | None. |
| **A stuck session blocks runs for up to 10 minutes.** | The TTL, a visible "N runs wait", cancel from the page. | A shorter TTL. |

## 16. Open points for the plan

1. The real code's format and the URL's values (section 8 is deliberately loose until then).
2. Whether the CLI exits on success, and which output a success prints (the `submitting` path of section 9 covers both).
3. Whether `auth login` and the interactive `/login` leave the same files (S5 measured `auth login` only; the maintainer's existing logins came from `/login`).
4. Whether the poll should also carry `boot` for the status post later (not needed now).
5. The exact 30 s, 60 s and attempt limit (calls; measure the URL time first).
6. Whether the backup cleanup is wanted at all.
7. Whether Setup should show a "waiting runs" count from a store query or from the manager (a count of queued rows is cheap).

## 17. Documents to change

- **In this commit:** [`../design.md`](../design.md) a new 2.7.1, "Exception: the runner's login from the Setup page"; `CLAUDE.md` the first hard rule, now naming the exception. Nothing else of those files.
- Done on approval: "draft" dropped from both. With the implementation: `CLAUDE.md` "Current state" (this cycle), the new variable `REMEDY_LOGIN_RELAY` under the optional variables, and the two new runner-API routes in Architecture; `docs/design.md` 2.7 line "CLI login once via `kubectl exec`" gains "or from the Setup page (2.7.1)".
- [`../research/subscription-cli-usage.md`](../research/subscription-cli-usage.md): a dated paragraph on the relay once the primary text is re-read; consequence 1 ("Remedy never reads, copies or stores the credentials") gains the exception.
- [`2026-10-21-self-hosted-for-others-design.md`](2026-10-21-self-hosted-for-others-design.md) §4 item 1 ("No image, chart, script or document may ship, collect or relay a login") and §13 open point 1: reword to "no installation may ship, collect or relay a login **of anyone but its own operator**, and the relay of this cycle is opt-in" and say the primary text decides whether the relay is offered to others. Not edited here.
- [`2026-10-06-kubernetes-deployment-design.md`](2026-10-06-kubernetes-deployment-design.md) §10.1: a pointer to this spec. `deploy/chart/README.md`, `docs/runbook/homelab-deploy.md` and `dummy-setup.md` (the optional login from the page), `docs/runbook/login-relay-real-run.md` (new), `docs/research/login-relay-real-run.md` (new).
