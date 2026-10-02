# Spike: structured output in stream-json mode

Date: 2026-10-02. Claude Code version (`claude --version`): 2.1.287.
Script: `scripts/spike/structured-output.sh` (single turn). A second, multi-turn run (the agent reads a file
first, then answers) was done with a throw-away variant of the same script.

All runs used the Remedy runner's isolation flags: `--safe-mode --restricted --strict-mcp-config`,
`--tools` with a read-only list, `--model sonnet`.

## Observations

1. `--output-format stream-json --verbose --json-schema ...`: the `result` event **contains
   `structured_output`**, and it **matches the schema** (`summary` string, `confidence` one of the enum
   values, and in the second run an `affected_files` array). The event's `result` field holds the same JSON
   as a string.
2. `--output-format json --json-schema ...`: the result object contains `structured_output` as well, with the
   same shape.
3. The structured answer is **also carried by a synthetic tool call**. The CLI offers a tool named
   `StructuredOutput`, and the model answers by calling it (an `assistant` event with a `tool_use` block named
   `StructuredOutput` whose `input` is the answer, followed by a `user` event with its `tool_result`). This
   tool was not in the `--tools` list and caused no permission denial (`permission_denials` was empty).
4. Multi-turn flow, `stream-json`: `system/init`, `assistant` (`tool_use: Read`), `rate_limit_event`, `user`
   (`tool_result`), `assistant` (`tool_use: StructuredOutput`), `user` (`tool_result`), `result`. `num_turns` was
   3 and `structured_output` was present in the final `result`.
5. Cost: the single-turn run cost an estimated 0.016 on a cold cache (3,558 cache-creation tokens, 154 output
   tokens) and 0.002 on a warm one; the multi-turn run with one file read cost an estimated 0.025.

## Decision

**Use `structured_output` from the `result` event.** It is present in `stream-json` mode, so the responder
keeps its live event stream and does not need `--output-format json`. The text fallback (`result` holds the same
JSON as a string) is not needed, but it is available if a later CLI version drops the field.

## Consequence for the responder (plan 1c)

- The runner passes the schema with `--json-schema <schema>` and reads `structured_output` from the `result`
  event. The adapter's `provider.Final` and the runner's `run.Outcome` gain an `Output` field (raw JSON), which
  the runner reports in `POST /runner/v1/runs/{id}/finish`.
- The control plane still **validates the output against the schema itself**. What the CLI does when the model's
  answer violates the schema (retry, error, or pass-through) was not tested, so nothing may rely on the CLI
  having validated it.
- The UI sees the answer twice in the stream (the `StructuredOutput` tool call and the final `result`). The run
  view should render the tool call compactly and show the diagnosis from the stored output, not from the raw
  event.
- The schema travels in the command line. That is fine for a schema of this size and contains no secrets.
- Not tested: a schema the CLI cannot satisfy, very large schemas, and whether `StructuredOutput` shows up in
  the `init` event's `tools` list.
