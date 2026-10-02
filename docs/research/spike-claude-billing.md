# Spike: billing and login behaviour of `claude -p`

Date: 2026-10-02. Claude Code version (`claude --version`): 2.1.287. Plan: Claude Pro, monthly.
Spike helper: `scripts/spike/claude-billing.sh`.

## Evidence status

| Claim | Status |
|---|---|
| `claude -p` runs headless on the interactive `/login` credential | **Observed**, twice: through `scripts/spike/claude-billing.sh` and through the Remedy runner (see "Real run through Remedy"). |
| The usage counts against the subscription, not the API | **Observed (CLI-reported).** The run's `system/init` event says `apiKeySource: "none"`, and its `rate_limit_event` shows the subscription's five-hour and seven-day windows with `isUsingOverage: false` and `overageStatus: "rejected"`. Not yet cross-checked in the dashboards. |
| Console (platform.claude.com) shows no usage | **Not checked.** No Console baseline exists. |
| `claude setup-token` / `CLAUDE_CODE_OAUTH_TOKEN` behaves the same | **Not tested.** |
| Login lifetime | **Not tested.** |

## Baseline (claude.ai, before the run, 12:39 to 12:40)

- Plan: Pro, monthly, renews 2026-11-02. Payment method on file.
- Current session: 16 % used, resets 16:10. This week: 3 % used, resets Friday 02:00.
- Usage credit: 0,00 EUR. Automatic reload: off.

Because credit is 0 EUR and auto-reload is off, a mis-billed run could not have been charged; it would
only have been blocked. The measurement is also noisy: the maintainer's interactive Claude Code session
runs on the same subscription, and a "pong" costs far less than one percentage point.

## Observed output of one run

`result: "pong"`, `session_id: e7797071-...`, `total_cost_usd: 0.0696` (a client-side estimate, not a
charge), `service_tier: standard`, `fallback_credit: null`.

Usage: 2 input tokens, 16,709 cache-creation tokens, 8,368 cache-read tokens, 104 output tokens
(100 of them thinking).

## Finding: a trivial run costs about 25k input tokens

Without `--bare` (which cannot be used with a subscription login) `claude -p` loads the maintainer's whole
global setup: skills, plugins, MCP servers and `CLAUDE.md`. A two-word prompt therefore consumed roughly
25k input tokens of subscription quota.

The runner will need to isolate the CLI's configuration. Candidates, all present in `claude --help`:
a dedicated `CLAUDE_CONFIG_DIR` for the runner, `--strict-mcp-config` with Remedy's own `--mcp-config`,
`--setting-sources` and `--disable-slash-commands`. Whether they bring the overhead down is **not yet
measured**; measuring it is a follow-up below.

## Real run through Remedy (2026-10-02)

The first run of Remedy against the real `claude` CLI (server, runner, UI, prompt "Reply with the single
word: pong") ended `succeeded` with exit 0 in about 6 seconds.

### Bug found: the runner's environment allowlist broke the login

The first attempt failed with `Not logged in · Please run /login` after 2 seconds. The same CLI run directly
from a shell worked, so the login was fine. Bisecting the environment one variable at a time:

| Environment | Result |
|---|---|
| `PATH`, `HOME`, `TMPDIR` only | `Not logged in` |
| the same plus `USER` | `pong` |

On macOS the CLI finds its login in the keychain by user name, so `USER` must reach the subprocess. It was
missing from the allowlist in `provider.FilterEnv` and is now on it (with a test). `LOGNAME` was not needed
in this test and is not on the list. On Linux (the pod) the login lives in a file under `HOME` or
`CLAUDE_CONFIG_DIR`, which was not tested.

### Real `stream-json` events

One run produced 10 events. The adapter's `kind` is the event's `type`:

| seq | kind | notes |
|---|---|---|
| 1, 2 | `system` (`hook_started`, `hook_response`) | the user's global `SessionStart` hook ran |
| 3 | `system` (`init`) | model, `permissionMode`, `tools`, `mcp_servers`, `skills`, `plugins`, `apiKeySource`, `cwd` |
| 4 | `system` (`commands_changed`) | |
| 5, 6 | `system` (`thinking_tokens`) | |
| 7, 8 | `assistant` | `message.content[]` holds blocks of `type` `thinking` (no text) and `text` |
| 9 | `rate_limit_event` | see below |
| 10 | `result` (`subtype: "success"`) | `result`, `is_error`, `total_cost_usd`, `usage`, `modelUsage`, `permission_denials`, `terminal_reason`, `duration_ms`, `num_turns` |

Consequences:

- The adapter's parsing (`type` as kind, `result`, `session_id` and `total_cost_usd` from the `result` event)
  matches the real output.
- The UI renders `assistant` events as raw JSON. A readable view should show the `text` blocks of
  `message.content[]` and fold `system` noise (hooks, `commands_changed`, `thinking_tokens`).
- `rate_limit_event.rate_limit_info` carries `rateLimitType`, `status`, `isUsingOverage`, `overageStatus` and
  `unifiedWindows.{five_hour,seven_day}.{utilization,resetsAt}`. This is a machine-readable subscription
  quota signal. It is the natural input for Remedy's budget per time window and for the quota-based
  fallback to another provider (design 2.1 and 2.3), instead of guessing from error messages.

### Evidence about billing

`system/init` reported `apiKeySource: "none"` (no API key in use). The `rate_limit_event` reported
`isUsingOverage: false` and `overageStatus: "rejected"` (`overageDisabledReason: "org_level_disabled"`), and
the utilization it showed belongs to the subscription's five-hour and seven-day windows. So the CLI itself
says the run used the subscription and could not have spilled into paid overage.

### Measured overhead of the unisolated CLI

| | Value |
|---|---|
| Tokens of the "pong" run | 14,994 cache-creation + 8,368 cache-read + 2 input + 101 output, about 23k |
| Loaded by the CLI | 138 tools, 5 MCP servers, 44 skills, 11 plugins, 82 slash commands, 6 agents |
| Estimated cost (`total_cost_usd`) | 0.063 (client-side estimate, not a charge) |

### Security finding: the agent inherits the maintainer's whole Claude setup

The runner's CLI loaded the maintainer's global configuration. Besides the token overhead that means:

- **Global MCP servers are available to the agent**, among them `home-assistant` (controls the home) and
  `claude.ai Higgsfield` (paid credits), plus Playwright and Context7.
- **Global hooks run** (the `SessionStart` hook above), as would any other hook in the user's settings.
- Global `permissions.allow` rules in the user's settings may pre-approve tools. `--permission-mode dontAsk`
  only denies what is not already allowed, so this must be checked, not assumed.

This contradicts the design's central rule that agents get no tools except those of the gatekeeper
(design 2.2). **The runner must isolate the CLI's configuration before any agent runs unattended or gets
write access**: a dedicated `CLAUDE_CONFIG_DIR` with its own login, `--strict-mcp-config` with Remedy's own
`--mcp-config`, `--setting-sources`, and `--disable-slash-commands`. This is no longer only a token
optimisation.

## Decision

**Proceed.** The CLI works headless on the `/login` credential, the full path through Remedy works, and the
CLI's own reporting shows subscription usage without overage. A dashboard cross-check (follow-up 1) would
add confidence but cannot change the architecture. If the cross-check or the `setup-token` test shows API or
Console billing, the result flips to **stop** (design section 2.1 and risks change).

The isolation work (follow-up 3) is now a **prerequisite for phase 1**, not an optimisation.

## Follow-ups

1. Cross-check the dashboards: after the runs above, claude.ai "Usage" and "Billing" (usage credit must stay
   0,00 EUR, no new line items), plus the Console if an account exists.
2. Repeat one run with `CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token`, and record the login expiry
   (`/status`) with a re-check after 7 days.
3. **Isolate the CLI's configuration** (dedicated `CLAUDE_CONFIG_DIR` with its own login, `--strict-mcp-config`,
   `--setting-sources`, `--disable-slash-commands`), verify with `system/init` that no global MCP servers,
   plugins or hooks load, and measure the token overhead again. Fold the result into the Claude adapter and
   design 2.1.
4. Check the Linux behaviour of the login (file under `HOME` / `CLAUDE_CONFIG_DIR`) before the runner goes
   into a pod.

## Deferred

Behaviour of an MCP tool that blocks for minutes (needed for approvals) is not tested here. It is tested in
phase 2 together with the gatekeeper.
