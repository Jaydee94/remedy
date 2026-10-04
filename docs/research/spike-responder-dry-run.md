# Spike: the responder inputs against the real CLI

Date: 2026-10-02. Claude Code version (`claude --version`): 2.1.287. Model pinned to `sonnet`, subscription
login, no API key in the environment.

## What was run

A dry run of everything the responder hands to the agent, with real data and without the control plane:

- **The failure:** the `web` job of Remedy pull request 20 (Renovate bumping `typescript` to 7). Its log is 26 KB:
  a byte order mark, an ISO timestamp in front of every line, ANSI escapes, `npm error` lines that name the cause,
  then `##[error]Process completed with exit code 1.`, then about 25 lines of runner cleanup.
- **The prompt:** built by `prompt.Build` from the real log, the real pull request (title, description) and its
  real file list with the patch: 16,780 bytes, the cleanup after the error cut away.
- **The snapshot:** the real tarball of the repository at the failing commit (88 entries, 64 files, 304,570 bytes of
  content; the first entry is a pax global header, then one top-level directory), passed through `snapshot.Filter`
  and `snapshot.Unpack`.
- **The call:** `claude -p --output-format stream-json --verbose --permission-mode dontAsk --safe-mode
  --restricted --strict-mcp-config --tools Read,Grep,Glob --model sonnet --json-schema <diagnosis.Schema>`,
  prompt on stdin, working directory the unpacked snapshot.

## Result

- Exit code 0, `is_error` false, 4 turns, 7.2 seconds, estimated cost 0.063 (12,634 cache-creation, 12,139
  cache-read, 986 output tokens). Billed to the subscription.
- Events: `system`, three `assistant` (two `Grep` calls in the workspace, then the `StructuredOutput` call),
  three `user` (tool results), one `rate_limit_event`, one `result`.
- The schema with an array of strings, a boolean and two enums was **accepted**, with `description` members and
  without any length keyword. The `result` event carried `structured_output` (1,284 bytes), and
  `diagnosis.Parse` accepted it without changes.
- The answer, shortened: the web job fails at `npm ci` because `web/package-lock.json` was not updated after the
  bump (`typescript` `~7.0.0` in `web/package.json`, the lock file still pins 6.0.3); confidence `high`; category
  `dependency_update`; affected files `web/package.json` and `web/package-lock.json`; the fix is `npm install` in
  `web/` and committing the lock file, then checking that the build and the linter work with TypeScript 7, with a
  fallback to keep `~6.0.x`; `fix_looks_automatable` true. The cause cites a line of the lock file that the agent
  found with `Grep` in the snapshot, so the snapshot was used and not only the log.

## What this does and does not show

- The inputs, the schema and the validator fit together on a real failure, and the answer is useful.
- It is one run on one failure. It does not test an adversarial log or pull request (the defences are the delimited
  data blocks, redaction, the read-only tools and the validation, each tested on its own), a schema the model
  cannot satisfy, or a failure whose cause is not in the log.
- What GitHub itself serves, found on the way and now in the code and the fixtures: `GET .../actions/jobs/{id}/logs`
  and `GET .../tarball/{sha}` answer `302` to another host (the token must not follow, and the signed URL must not
  reach a log); the id of an Actions check run is the id of its job; the `output` of an Actions check run is empty,
  other apps fill it; the `conclusion` of an unfinished run is `null`.
