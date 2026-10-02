# Spike: billing and login behaviour of `claude -p`

Date: 2026-10-02. Claude Code version (`claude --version`): 2.1.287. Plan: Claude Pro, monthly.
Spike helper: `scripts/spike/claude-billing.sh`.

## Evidence status

| Claim | Status |
|---|---|
| `claude -p` runs headless on the interactive `/login` credential | **Observed.** One run returned `result: "pong"`. The environment had no `ANTHROPIC_*` variables set, and the helper unsets them anyway. |
| The usage is billed against the subscription, not the API | **Reported by the maintainer, not measured.** No after-run dashboard screenshots were captured. |
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

## Decision

**Proceed (provisional).** The maintainer states that all usage is billed to the subscription, and the
helper run behaved as expected. The decision becomes final once the follow-ups below confirm it. If any
of them shows API or Console billing, the result flips to **stop** (design section 2.1 and risks change).

## Follow-ups

1. Capture after-run screenshots of claude.ai "Usage" and "Billing" (usage credit must stay 0,00 EUR, no
   new line items) after about ten runs, and a Console baseline plus after-run view if a Console account
   exists.
2. Repeat one run with `CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token`.
3. Record the login expiry (`/status`) and re-check after 7 days.
4. Measure the isolation flags above against the 25k-token baseline, using a separate `CLAUDE_CONFIG_DIR`
   with its own login. If they help, fold them into the Claude adapter (plan Task 3) and design 2.1.

## Deferred

Behaviour of an MCP tool that blocks for minutes (needed for approvals) is not tested here. It is tested in
phase 2 together with the gatekeeper.
