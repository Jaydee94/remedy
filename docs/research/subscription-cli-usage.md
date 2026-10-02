# Research: using the AI CLIs with a subscription (as of 2026-10-02)

Core assumption of Remedy: agents run on a Claude Pro/Max subscription or a Google
subscription, without API tokens. This assumption was checked against the available
sources before bootstrapping. **This is not legal advice.** The terms change, and the
items marked "open" below must be settled by a spike before or during phase 0.

## Claude (Claude Code CLI)

### Established (official docs)

- `claude -p` (headless) is an officially supported way to run scripts and CI, with
  `--output-format stream-json`, `--mcp-config`, `--allowedTools`, `--permission-mode`
  and `--permission-prompt-tool` / `--permission-prompts none`.
  Source: <https://code.claude.com/docs/en/headless>
- For environments without a browser there is `claude setup-token` (one-year OAuth token
  for the subscription, set via `CLAUDE_CODE_OAUTH_TOKEN`). The token can only make model
  requests. Source: <https://code.claude.com/docs/en/authentication>
- `--bare` does **not** read OAuth credentials. With a subscription login the runner must
  therefore not use `--bare`. Source: same page.
- Legal & compliance: OAuth auth is "designed to support ordinary use of Claude Code and
  other native Anthropic applications". Developers building products or services (also
  with the Agent SDK) should use API key auth. It is forbidden to **collect, store or
  intermediate** Claude.ai credentials or session tokens, or to route requests through
  subscription credentials on behalf of others. Still allowed: an end user signing in to
  the **unmodified** Claude Code binary with their own subscription. "Advertised usage
  limits for Pro and Max plans assume ordinary, individual usage".
  Source: <https://code.claude.com/docs/en/legal-and-compliance>

### Consequences for Remedy

1. Remedy starts the **unmodified** `claude` binary as a subprocess. Remedy **never**
   reads, copies or stores the credentials. The login lives in the CLI's config dir on
   the runner (`CLAUDE_CONFIG_DIR`) or is passed through as `CLAUDE_CODE_OAUTH_TOKEN` to
   the process, which Remedy does not read.
2. The **Agent SDK** is not intended for subscription auth. The "Claude Agent SDK" option
   from the planning session is dropped. We stay with the CLI subprocess.
3. Remedy is a single-person tool for the owner's own homelab. It offers no access to
   third parties and no multi-user login. This must stay that way (**non-goal:** no
   multi-user operation, no reselling, no access for others through the subscription).
4. A 24/7 reactive system only partly fits "ordinary, individual usage". That is a
   **remaining risk** (rate limits, in the extreme case enforcement by Anthropic).
   Mitigations: low parallelism, budget per time window, incident deduplication,
   API-key fallback as a documented option.

### Open (spike in phase 0)

- **Billing:** issue #43333 reports that `claude -p` with OAuth was in some cases billed
  as API usage instead of against the subscription. The issue is closed; its resolution
  could not be verified. The spike must confirm with a real `claude -p` run that usage
  shows up in the subscription dashboard and not in Console billing.
  Source: <https://github.com/anthropics/claude-code/issues/43333>
- Login lifetime behaviour in a pod (refresh, expiry), and whether `setup-token` is
  better than the interactive login for continuous operation.
- How an MCP tool that blocks for a long time behaves (approval wait time, see design
  2.2) and which timeouts `claude -p` applies to it.

## Google (Gemini CLI → Antigravity CLI)

### Established (announcement, partly secondary sources)

- **The Gemini CLI no longer serves personal subscriptions since 2026-06-18** (Google AI
  Pro/Ultra, free tier). The successor is the **Antigravity CLI (`agy`)**, written in Go
  and closed source. Gemini CLI remains for enterprise licences and paid API keys.
  Source: <https://github.com/google-gemini/gemini-cli/discussions/27274>
- `agy` has a headless mode: `agy -p "<prompt>" --output-format json|stream-json`, using
  the subscription login without an API key in the environment. Permissions must be set
  in advance via `permissions.allow` in the settings, because headless mode cannot ask.
  Source (secondary, field report): <https://github.com/noogram/cosmon/issues/152>
- Per the Antigravity ToS, Google forbids third-party software from using the Antigravity
  / Gemini CLI OAuth ("harvesting or piggybacking"). Starting the official `agy` binary as
  a subprocess is described in secondary sources as the allowed path.
  Source (secondary, wording not verified): <https://github.com/can1357/oh-my-pi/issues/12487>

### Consequences for Remedy

1. The Gemini adapter targets **`agy`**, not the Gemini CLI. The planning session said
   "Gemini or Antigravity CLI"; only Antigravity remains.
2. Same principle as for Claude: subprocess of the official binary, Remedy touches no
   tokens.
3. The community reports higher token consumption and tighter quotas for `agy`. The
   fallback from Claude to Gemini (design 2.1) is therefore less dependable than assumed.

### Open (spike before phase 4)

- Obtain the primary ToS source for automated use on a personal subscription and read
  the exact wording.
- **Keyring in a container:** `agy` keeps credentials in the OS keyring. Whether that works
  in a Linux pod without a desktop keyring is unknown.
- How quota exhaustion is reported in headless mode (needed for the fallback).
- MCP integration of `agy`, and whether permissions can be set per invocation instead of
  globally.

## Consequences for the plan

| Affected | Change |
|---|---|
| Branch 1a | CLI subprocess stays. Agent SDK dropped. Runner never touches credentials. |
| Branch 1b | Fallback to Gemini is **phase 4, conditional** on the `agy` spike. |
| Phase 0 | Contains a spike "subscription billing and login lifetime of `claude -p`". |
| Risks | The residual "ordinary individual usage" risk for continuous operation is tracked in the design. |
