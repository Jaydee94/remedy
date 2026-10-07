# S5: can the CLI login be relayed through a web UI?

Date: 2026-10-07. Spike for the Kubernetes deployment spec. Environment: pod `s2` on the kind cluster `kind-remedy-spike` (debian:bookworm-slim, user 65532, `claude` CLI 2.1.292 at `/tmp/bin/claude`, `HOME=/tmp/home`, `CLAUDE_CONFIG_DIR=/tmp/home/claude`). Everything ran with `kubectl exec`. No login was completed by these runs; the only text ever written to the CLI's stdin was the deliberately invalid string in section 2b.

## Question

The maintainer wants to log the unmodified `claude` CLI in from the Remedy web UI (option A): the UI shows the login URL, the maintainer opens it in a browser, pastes the one-time code into a field in the UI, and the runner passes the code to the CLI. Is that feasible without a TTY?

## Rules kept

- No file under `/tmp/home/claude` was read, only listed by name.
- The authorization URL carries OAuth state and a PKCE challenge. This record has its shape only; every value is `<value omitted>`.
- No login was completed and no real code was used; the only input was the invalid string `not-a-real-code` (section 2b). No account data was looked at.

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
- URL shape: scheme `https`, host `claude.com`, path `/cai/oauth/authorize`. Query parameter names, in order: `code`, `client_id`, `response_type`, `redirect_uri`, `scope`, `code_challenge`, `code_challenge_method`, `state`. A `code_challenge` is present (PKCE) with a `code_challenge_method`; every value is `<value omitted>`. What the `code` parameter means was not measured and nothing below depends on it (it may be a flag that asks the authorization page to show a code for pasting; that is a guess).
- It did say it was opening a browser. In the pod there is no browser; the failure was silent (no error line, no non-zero early exit), the CLI just went on to the URL line and the prompt and waited. It did not exit on its own within the 60 s.
- In these first runs the prompt was printed with stdin as a pipe from `sleep` (never a terminal). Nothing was written to stdin in them; the follow-up measurement below did that.
- With stdin held open the process ran until `timeout` stopped it: exit code **124** (`timeout`'s own code for "command timed out", not the CLI's).

TTY comparison (one run): `kubectl exec -t s2 -- bash -c 'timeout 30 /tmp/bin/claude auth login </dev/null'`. In this run only stdout was a terminal (`-t` without `-i` attaches no terminal stdin; stdin was `/dev/null`, at EOF). The output was byte-for-byte the same shape as without a TTY: the same three pieces, the same prompt, exit code 124 from `timeout`, no carriage returns. With a terminal on stdout the stream split cannot be told apart in that run. What this shows: **the URL and the prompt are printed whether or not stdout is a terminal, and the CLI did not exit when stdin was at EOF.** It does not show anything about reading a code from a piped stdin; the next section measures that.

### 2b. Writing a deliberately invalid code into a piped stdin (no TTY)

Taken on 2026-10-07 while the pod was still logged out (`claude auth status --text` answered "Not logged in" just before). The text `not-a-real-code` is not a credential and cannot log anyone in. Command shape, twice (the second run added timestamps):

```text
( sleep 5; printf 'not-a-real-code\n'; sleep 50 ) | kubectl exec -i s2 -- bash -c 'timeout 60 /tmp/bin/claude auth login 2>&1'
```

Both runs used plain pipes, no TTY anywhere. Standard error was merged into standard output (`2>&1`) inside the pod, so which stream carries the message below is **not measured**.

- Measurement (a), invalid code with a trailing newline: **measured, twice, same result.** The line was consumed from the pipe: at 5.0 s, the second the line was written, the CLI printed `Invalid code. Please make sure the full code was copied.` and a newline right after the prompt (`Paste code here if prompted > Invalid code. ...`). So a **piped stdin is read and a line on it is treated as the pasted code.**
- It did **not** exit by itself: it ran until `timeout` stopped it at 60 s (exit code 124). It also printed no second prompt in the 55 s after the error (so no visible re-prompt, though the process stayed alive; whether a second line would be read was not tried). In the first run my wrapper printed `EXIT=0` because `$?` was read after an `echo`; that value is wrong and is not used. The second run read the code correctly (124).
- Measurement (b), the same text **without** a trailing newline: **not measured.** The controller stopped further `auth login` starts because the maintainer completed a real login in the pod during this work, and another start could have rewritten or backed up that login's config. So whether a newline is needed is unknown (the measured case used a newline and worked).
- Measurement (c), a PTY run: **not measured**, and not needed: it was only to be run if (a) showed the line is not consumed, and (a) showed it is.
- New names under `/tmp/home/claude` (names only): between the listing before the first (a) run and the listing after it, about 245 new names appeared: `cache/model-catalog` and a tree under `plugins/marketplaces/claude-plugins-official/` (a `.claude-plugin` directory and `external_plugins/<name>/` directories). The second (a) run added none. I cannot say whether these came from the invalid-code run or from the maintainer's real login that happened in the same period, so **attribution is unknown**; no file was opened.
- The pod's login state was not changed by an invalid code as far as measured (the check before the runs said "Not logged in"; no check was made after, see the stop above).

### 3. Cleanup and leftovers

Names (never contents) under `/tmp/home/claude`:

- Before the first run: `.claude.json`, `.claude.json.lock/` (a directory), `backups/` with `.claude.json.backup.1791355592189`.
- After both runs: `.claude.json`, `backups/` with `.claude.json.backup.1791355592189` and one new `.claude.json.backup.1791358412198`. The lock directory is gone.
- The new backup's millisecond timestamp equals the second the first run started (07:33:32 UTC), so the CLI writes a backup of its config file when `auth login` starts, before any code. The second (TTY) run added no new name.
- No `claude` process was left running in the pod after `timeout` killed it (checked in `/proc`). The pod and the cluster were left running.

A relay therefore has to expect that a login attempt that is abandoned leaves a config backup file behind, one more per attempt that rewrites the file. The runner's login directory is a persistent volume, so these accumulate slowly. This is harmless but worth a clean-up policy. (The invalid-code runs in 2b also started `auth login`; their backup files were not listed separately because of the stop described there.)

### 4. A non-interactive code flag

Not found. `auth login --help` has no flag for a code, a file, a pipe, or a device flow. The only way to hand the code over is the prompt on stdin (measured below to read a plain pipe).

### 5. Other flows in `--help`

- `claude setup-token`: "Set up a long-lived authentication token (requires Claude subscription)". Help-level only; not run, so whether it prints a URL or how it takes a code is **not measured**. Its token would be a credential in its own right, which conflicts with the hard rule that Remedy never reads, stores or logs the CLI's credentials, so it would have to stay inside the CLI's own storage.
- `--console`, `--sso`, `--email`: choose the account type and prefill the login page; they do not change the code-paste mechanism.
- `claude gateway` (enterprise auth/telemetry proxy) exists in the top-level help; it is not a login flow for the subscription.
- No device-code style flow (URL plus short code that the CLI polls for) is offered.

## What a relay would have to do, in bytes

1. The runner starts `claude auth login` (with `--claudeai`, which is the default) as a child with **pipes** for stdin, stdout and stderr, no PTY. It must keep stdin open: the CLI did not exit when stdin was at EOF (the `-t` run with `/dev/null`) and it stayed alive for 55 s after an invalid code on a pipe, but the code is read from that pipe, so it has to stay open for as long as the UI waits.
2. It reads **stdout** (and for safety stderr too) as bytes. The URL is on the line that starts with `If the browser didn't open, visit: ` and runs to the newline: match `https://claude.com/cai/oauth/authorize?` up to the first whitespace. The text before the URL contains a non-ASCII character (U+2026), so the parser has to treat the stream as UTF-8 and match on the URL, not on column counts. The URL is available a moment after the process starts, so the runner needs a read deadline (seconds) with a clear error if it does not appear.
3. The runner sends only that URL line's URL to the control plane, which shows it in the UI. The URL contains the OAuth `state` and the PKCE `code_challenge`; it is a one-time link for this attempt but must still be treated as sensitive (no logs, no activity row, UI only to the signed-in admin).
4. The maintainer opens the URL, signs in, and the authorization page displays a one-time code. The maintainer pastes it into the UI field, the control plane forwards it to the runner (the runner API already authenticates with the shared bearer token).
5. The runner writes **the code followed by a newline** (`\n`) to the CLI's stdin (the newline form is the one that was measured to be consumed; a form without a newline is unmeasured, so use the newline), after the prompt `Paste code here if prompted > ` was seen (the prompt has no newline, so the runner must not wait for one; it waits for the `> ` suffix or simply writes when the code arrives, since a pipe buffers the bytes). The CLI itself then checks the code. A wrong code gets the message `Invalid code. Please make sure the full code was copied.` and the process stays alive (it does not exit), so the runner must treat that line as a failed attempt, show the message to the maintainer and either offer another try (not measured whether the same process reads a second line) or kill the process and start a new login. A right code is not measured: it is assumed that the CLI exchanges it for tokens itself and writes them into its own config directory, so Remedy never sees them.
6. The runner reports success from the CLI's **exit code** (0 is assumed, not measured: only the invalid-code path was run) and confirms with `claude auth status --json`, reading only whether it is logged in, never the account fields (they are account data and must not be stored or logged). The code must be dropped from memory and never logged after it is written.

Still **not measured**: what a valid code does (exit code, output), whether a line without a newline is consumed, which stream the invalid-code message is on, whether a second line is read after an invalid code, and what an expired code says. The first real relay run (the login done through the UI, not the maintainer's direct login in the pod) is the check for the valid-code path.

## What a failing measurement would have changed

- If no URL had been printed without a TTY, the relay would need a PTY (allocate one in the runner, for example with `creack/pty` or `script`, and read the URL from the merged stream with ANSI escapes stripped). That is a new Go dependency or a system tool in the runner image, and the verdict would be "yes with a PTY". It did not happen.
- If the URL had gone to stderr only, the runner would read stderr; the verdict would not change. It did not happen (stdout).
- If the prompt had appeared only with a TTY, input would still need a PTY, same verdict "yes with a PTY".
- If the CLI had exited on its own when the browser failed to open, or when stdin was a pipe or `/dev/null`, there would be no window to paste the code and a relay would be impossible without a PTY or a different flow (`no`, or a switch to `setup-token`). It did not happen: it stayed alive until killed.
- (2b, a) If the line written to a piped stdin had been ignored (no reaction, the process just waiting), the answer would have changed from "yes" to "yes with a PTY" (or "no" if a PTY did not work either), because the code could then only be delivered through a terminal. It did not happen: the line was consumed and judged within the same second.
- (2b, a) If the CLI had exited by itself on the invalid code, the runner could not retry in the same process and would need a fresh login start per attempt; the verdict would stay "yes". It did not exit (it stayed alive until killed).
- (2b, b) If a newline had been required and was missing, the runner would have to append one; if a newline had been refused, it would have to write the bare code. Not measured, so the relay writes the newline form that was measured to work. A failure of that form is the one thing the first real relay run has to rule out; a bare-code form would be the fallback.
- If the CLI had not read pasted codes at all (for example only a local callback listener), pasting a code would not work and the verdict would be "no". The measurement in 2b shows it does read and check a pasted code from stdin.
- If a code flag existed in `--help`, the relay would use it instead of stdin. None exists, so stdin is the one path.

## Decision

```text
prints a URL without a TTY:    yes
URL on:                        stdout
asks for the code on stdin:    yes (prompt text "Paste code here if prompted > ", no trailing newline); a line on a piped stdin is read and judged (measured with an invalid code: "Invalid code. Please make sure the full code was copied.")
needs a TTY:                   no (URL, prompt and reading of a piped stdin all worked without a terminal on stdin)
non-interactive code flag:     not found
leaves files before the code:  a new backups/.claude.json.backup.<ms timestamp> (name only); lock directory removed; later runs showed new names under cache/model-catalog and plugins/marketplaces/claude-plugins-official (attribution unknown)
exit code when killed:         124 (timeout's code; the CLI did not exit on its own, also not after an invalid code)
relay feasible (option A):     yes, conditional on the valid-code path — a piped stdin is consumed (measured with an invalid code, newline-terminated); not measured: a valid code, a code without a newline, the stream of the error message; the first real relay run decides whether "yes" holds or "yes with a PTY" is needed
alternative flows in --help:   setup-token (long-lived token; not run), --console, --sso, --email (account choice only); no device-code flow
```
