# Spike: a blocking MCP tool in `claude -p` (approval waits)

Date: 2026-10-04. Claude Code version (`claude --version`): 2.1.288. This answers open point 3 of
[`docs/design.md`](../design.md): how a long-blocking MCP tool, as used for approvals, behaves.

Scripts: `scripts/spike/mcp-blocking.sh` (runs the CLI) and `scripts/spike/mcp_blocking.py` (a stdlib MCP server over
Streamable HTTP whose one tool, `wait`, answers after N seconds). The CLI ran with the Remedy runner's isolation flags,
`--permission-mode dontAsk --restricted --strict-mcp-config`, `--tools Read`, `--allowedTools mcp__spike__wait`,
`--model sonnet`, and `--mcp-config` with `{"type":"http","url":...,"headers":{"Authorization":"Bearer ..."}}`. The
prompt asked the agent to call `wait` with a given number of seconds. Runs of several minutes ran in parallel.

## Observations

1. **`--safe-mode` turns MCP off completely.** With `--safe-mode`, even with `--mcp-config` and `--strict-mcp-config`,
   the `init` event has `mcp_servers: []` and the server receives no request. Without `--safe-mode`, `--restricted --strict-mcp-config`
   loads exactly the one server (`status: connected`). `--restricted` alone still keeps the maintainer's plugins out: the init event lists
   only the three builtin plugins, and the MCP servers of the user are not loaded. (`--safe-mode` alone, without `--restricted`,
   loaded the user's plugins and no MCP server.) The runner's current invocation therefore cannot serve gatekeeper runs.
2. **The handshake.** The CLI sends `POST server/discover`, `initialize` (the server must answer with a `protocolVersion`; echoing the
   client's works), `notifications/initialized`, `GET /mcp` (answering 405 is fine) and `tools/list`. The `Authorization` header from the
   config arrives on every request. Tools appear as `mcp__<server>__<tool>`; with `dontAsk` they must be named in `--allowedTools`.
3. **A plain JSON answer times out after 60 seconds**: the tool result is `The operation timed out.` (`is_error: true`), for a 120 s and a 420 s
   wait alike. `MCP_TOOL_TIMEOUT=1200000` (ms) in the CLI's environment raised that: a 150 s wait completed.
4. **An SSE answer with comment lines as keepalive does not help.** The headers arrive at once and a `: keepalive` line every 20 s keeps the
   connection busy, but after 300 s the CLI aborts: `MCP server "spike" tool "wait" sent no response or progress for 300s; aborting`. The message
   names the two switches: a per-server `timeout` (ms) in the MCP config, or `CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT` (ms, 0 disables).
5. **MCP progress notifications keep a call alive, with no environment setting.** The CLI puts `_meta.progressToken` (a number, here the request
   id) and `_meta["claudecode/toolUseId"]` into every `tools/call`. The server answers with `text/event-stream` and sends
   `{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":<token>,"progress":<n>}}` as an SSE `message` event every
   20 s, then the result. A 420 s wait completed (`waited 420s`), without `MCP_TOOL_TIMEOUT` and without the idle variable.
6. **The environment switches also work**: an SSE answer with comment keepalive and `CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT=0
   MCP_TOOL_TIMEOUT=7200000` completed a 420 s wait.
7. **The CLI going away.** `SIGKILL` and `SIGINT` on the CLI mid-call close the connection and nothing else happens; the server only notices
   at its next write (`BrokenPipe`), a Go server sees the request context cancelled. **`SIGTERM` is different: right after it, a second MCP
   session (`server/discover`, `initialize`, `notifications/initialized`, `tools/call`) arrives with the same `claudecode/toolUseId` and a
   different `progressToken`, and both connections then drop.** The CLI replays the in-flight call while it shuts down. The runner stops the CLI with
   the default of `exec.CommandContext` (`SIGKILL`, `WaitDelay` 5 s), which does not replay, but a `SIGTERM` to the process group (a pod stop,
   a manual `kill`) does.
8. **Cost.** A two-turn run that loads the one MCP server and calls its tool once cost an estimated 0.0136 USD on a cold cache (3.1k
   cache-creation tokens, 58 output tokens). The tool definitions of a real gatekeeper add to every run that uses it.

## Decision

**Transport A (the CLI talks Streamable HTTP to the control plane) is feasible.** The control plane answers a blocking `tools/call` with an
SSE stream and sends a progress notification every 15 to 20 seconds until the decision, then the result. No environment variable is needed.
The runner invocation for runs with gatekeeper access is the current one **without `--safe-mode`**.

## Consequences for the gatekeeper

- **A replayed call must never be a second approval.** The gatekeeper identifies a call by `(run token, claudecode/toolUseId)` and answers a
  repeat of a known id with the state of the first (the same pending request, or its decision), never with a new request. A run that is no longer
  running must not be able to start or complete an approval; execution of an approved action checks the run state in the same transaction
  as the decision.
- Closing of the request (the Go request context) marks the pending request as abandoned; an abandoned request cannot be approved any more.
- The first `tools/call` of a session is preceded by `server/discover`, `initialize`, `notifications/initialized` and `GET /mcp`; the endpoint
  must answer all of them (405 for the `GET`).
- The JSON-only answer path is limited to 60 s. A tool that can wait for a human always uses the SSE answer; a tool that answers at once may
  use plain JSON.

## Not tested

- A wait longer than 30 minutes. A 4 hour run with progress notifications was started (2026-10-04 19:56) and its result is not recorded yet.
- Whether the per-server `timeout` key of the MCP config can replace progress notifications.
- The CLI's behaviour with a response `Content-Type: application/json` that is streamed slowly, and with HTTP/2.
- A denied tool call (`dontAsk` without `--allowedTools`): the gatekeeper is the enforcement point, so this was not needed.
- Linux: everything above ran on macOS.

## Long runs

- **30 minutes, progress every 20 s** (`ssep`, no environment variables): the call completed after 1803 s with `waited 1800s`, the agent
  answered `done`, exit 0, 2 turns, estimated cost 0.0041 USD. The CLI did not retry or reconnect: the server saw exactly one `tools/call`.
- **4 hours:** started, result to be added.
