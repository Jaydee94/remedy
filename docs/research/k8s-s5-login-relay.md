# S5: can the CLI login be relayed through a web UI?

Date: 2026-10-07. Spike for the Kubernetes deployment spec. Environment: pod `s2` on the kind cluster `kind-remedy-spike` (debian:bookworm-slim, user 65532, `claude` CLI 2.1.292 at `/tmp/bin/claude`, `HOME=/tmp/home`, `CLAUDE_CONFIG_DIR=/tmp/home/claude`). Everything ran with `kubectl exec`, never with a login completed and never with a code fed to the CLI.

## Question

The maintainer wants to log the unmodified `claude` CLI in from the Remedy web UI (option A): the UI shows the login URL, the maintainer opens it in a browser, pastes the one-time code into a field in the UI, and the runner passes the code to the CLI. Is that feasible without a TTY?

## Rules kept

- No file under `/tmp/home/claude` was read, only listed by name.
- The authorization URL carries OAuth state and a PKCE challenge. This record has its shape only; every value is `<value omitted>`.
- No login was completed, no code was typed or piped, no account data was looked at.

## Measurements

### 1. `--help`

`claude auth --help` lists three subcommands: `login`, `logout`, `status` (`status` has `--json` and `--text`).

`claude auth login --help`, all options:

| Flag | Meaning |
| --- | --- |
| `--claudeai` | Use the Claude subscription (default) |
| `--console` | Use the Anthropic Console (API billing) instead |
| `--email <email>` | Pre-populate the email address on the login page |
| `--sso` | Force the SSO login flow |
| `-h, --help` | Help |

There is no flag about the browser, about printing the URL, or about entering a code. `claude setup-token --help` shows only `-h`: "Set up a long-lived authentication token (requires Claude subscription)". It was not run.

### 2. `claude auth login` without a TTY, stdin held open

Command: `kubectl exec -i s2 -- bash -c 'timeout 60 /tmp/bin/claude auth login < <(sleep 55)'`, stdout and stderr of `kubectl` captured into separate files.

- Everything the CLI printed went to **stdout**. Stderr stayed empty (the only stderr line was the `EXIT=` marker added by the wrapper).
- It printed an authorization URL **without a TTY**. The output had exactly three pieces, in this order:
  1. `Opening browser to sign in…` plus a newline (the ellipsis is the single character U+2026, UTF-8).
  2. `If the browser didn't open, visit: <URL>` plus a newline.
  3. `Paste code here if prompted > ` with **no** trailing newline (it ends with `> ` and a space), which is the prompt for the code.
- URL shape: scheme `https`, host `claude.com`, path `/cai/oauth/authorize`. Query parameter names, in order: `code`, `client_id`, `response_type`, `redirect_uri`, `scope`, `code_challenge`, `code_challenge_method`, `state`. A `code_challenge` is present (PKCE) with a `code_challenge_method`; every value is `<value omitted>`. The `code` parameter is, by inference (not verified), a mode flag of the URL and not a login code: it asks the authorization page to show a code for pasting instead of redirecting back to a local port.
- It did say it was opening a browser. In the pod there is no browser; the failure was silent (no error line, no non-zero early exit), the CLI just went on to the URL line and the prompt and waited. It did not exit on its own within the 60 s.
- The prompt is printed whether or not stdin is a terminal. I did not feed it anything, so what it does with a line on stdin is **not measured** (see "What a relay does" for why the brief forbids measuring it).
- With stdin held open the process ran until `timeout` stopped it: exit code **124** (`timeout`'s own code for "command timed out", not the CLI's).

TTY comparison (one run): `kubectl exec -t s2 -- bash -c 'timeout 30 /tmp/bin/claude auth login </dev/null'`. The output was byte-for-byte the same shape as without a TTY: the same three pieces, the same prompt, exit code 124 from `timeout`, no line-ending differences found (no carriage returns). With a TTY the terminal merges stdout and stderr, so the stream split cannot be told apart in that run, but the content was the same. **A TTY is not required for the URL or for the prompt.**

### 3. Cleanup and leftovers

Names (never contents) under `/tmp/home/claude`:

- Before the first run: `.claude.json`, `.claude.json.lock/` (a directory), `backups/` with `.claude.json.backup.1791355592189`.
- After both runs: `.claude.json`, `backups/` with `.claude.json.backup.1791355592189` and one new `.claude.json.backup.1791358412198`. The lock directory is gone.
- The new backup's millisecond timestamp equals the second the first run started (07:33:32 UTC), so the CLI writes a backup of its config file when `auth login` starts, before any code. The second (TTY) run added no new name.
- No `claude` process was left running in the pod after `timeout` killed it (checked in `/proc`). The pod and the cluster were left running.

A relay therefore has to expect that a login attempt that is abandoned leaves a config backup file behind, one more per attempt that rewrites the file. The runner's login directory is a persistent volume, so these accumulate slowly. This is harmless but worth a clean-up policy.

### 4. A non-interactive code flag

Not found. `auth login --help` has no flag for a code, a file, a pipe, or a device flow. The only way to hand the code over is the prompt on stdin.

### 5. Other flows in `--help`

- `claude setup-token`: "Set up a long-lived authentication token (requires Claude subscription)". Help-level only; not run, so whether it prints a URL or how it takes a code is **not measured**. Its token would be a credential in its own right, which conflicts with the hard rule that Remedy never reads, stores or logs the CLI's credentials, so it would have to stay inside the CLI's own storage.
- `--console`, `--sso`, `--email`: choose the account type and prefill the login page; they do not change the code-paste mechanism.
- `claude gateway` (enterprise auth/telemetry proxy) exists in the top-level help; it is not a login flow for the subscription.
- No device-code style flow (URL plus short code that the CLI polls for) is offered.

## What a relay would have to do, in bytes

1. The runner starts `claude auth login` (with `--claudeai`, which is the default) as a child with **pipes** for stdin, stdout and stderr, no PTY. It must keep stdin open: closing it or giving `/dev/null` is what the TTY comparison used and it did not make the CLI exit, but a login that waits for a code needs the pipe to stay open for as long as the UI waits.
2. It reads **stdout** (and for safety stderr too) as bytes. The URL is on the line that starts with `If the browser didn't open, visit: ` and runs to the newline: match `https://claude.com/cai/oauth/authorize?` up to the first whitespace. The text before the URL contains a non-ASCII character (U+2026), so the parser has to treat the stream as UTF-8 and match on the URL, not on column counts. The URL is available a moment after the process starts, so the runner needs a read deadline (seconds) with a clear error if it does not appear.
3. The runner sends only that URL line's URL to the control plane, which shows it in the UI. The URL contains the OAuth `state` and the PKCE `code_challenge`; it is a one-time link for this attempt but must still be treated as sensitive (no logs, no activity row, UI only to the signed-in admin).
4. The maintainer opens the URL, signs in, and the authorization page displays a one-time code. The maintainer pastes it into the UI field, the control plane forwards it to the runner (the runner API already authenticates with the shared bearer token).
5. The runner writes **the code and one newline** (`\n`) to the CLI's stdin, after the prompt `Paste code here if prompted > ` was seen (the prompt has no newline, so the runner must not wait for one; it waits for the `> ` suffix or simply writes when the code arrives, since a pipe buffers the bytes). The CLI exchanges the code for tokens itself and writes them into its own config directory; Remedy never sees them.
6. The runner reports success from the CLI's **exit code** (0) and confirms with `claude auth status --json`, reading only whether it is logged in, never the account fields (they are account data and must not be stored or logged). The code must be dropped from memory and never logged after it is written.

Two details were **not measured** because that would have required feeding a code: whether the CLI wants a trailing newline, and how it reports a wrong or expired code (exit code, message on stdout or stderr, whether it re-prompts). The first real S2 login, done by the maintainer, should record both.

## What a failing measurement would have changed

- If no URL had been printed without a TTY, the relay would need a PTY (allocate one in the runner, for example with `creack/pty` or `script`, and read the URL from the merged stream with ANSI escapes stripped). That is a new Go dependency or a system tool in the runner image, and the verdict would be "yes with a PTY". It did not happen.
- If the URL had gone to stderr only, the runner would read stderr; the verdict would not change. It did not happen (stdout).
- If the prompt had appeared only with a TTY, input would still need a PTY, same verdict "yes with a PTY".
- If the CLI had exited on its own when the browser failed to open, or when stdin was a pipe or `/dev/null`, there would be no window to paste the code and a relay would be impossible without a PTY or a different flow (`no`, or a switch to `setup-token`). It did not happen: it waited for input until killed.
- If the URL had been shortened or signed so that the code displayed on the page is bound to a local callback port (the `code` parameter would then be absent), pasting a code would not work at all. The URL has the `code` flag and a `redirect_uri` parameter; the measured prompt text "Paste code here if prompted" is the CLI's own statement that a paste is supported.
- If a code flag existed in `--help`, the relay would use it instead of stdin. None exists, so stdin is the one path.

## Decision

```text
prints a URL without a TTY:    yes
URL on:                        stdout
asks for the code on stdin:    yes (prompt text "Paste code here if prompted > ", no trailing newline)
needs a TTY:                   no (same output with and without; nothing breaks without)
non-interactive code flag:     not found
leaves files before the code:  a new backups/.claude.json.backup.<ms timestamp> (name only); lock directory removed; no other change
exit code when killed:         124 (timeout's code; the CLI did not exit on its own)
relay feasible (option A):     yes — the CLI prints the URL and the paste prompt on plain pipes and waits on stdin, so the runner only has to copy the URL out of stdout and write the code plus a newline into stdin (not measured: the effect of the code itself)
alternative flows in --help:   setup-token (long-lived token; not run), --console, --sso, --email (account choice only); no device-code flow
```
