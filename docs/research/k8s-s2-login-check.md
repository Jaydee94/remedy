# S2: how to tell whether the runner is logged in

- Date: 2026-10-07.
- Spike S2 of `docs/plans/k8s-0-spikes.md`; spec `docs/specs/2026-10-06-kubernetes-deployment-design.md` (D8, sections 4 and 9). Feeds K-6 (`provider.loginCheckArgs` and the "not logged in" phrase).
- CLI: `2.1.292`, `linux-arm64`, installed as S4 decides (`docs/research/k8s-s4-cli-install.md`): downloaded on the host from `https://downloads.claude.ai/claude-code-releases/2.1.292/linux-arm64/claude`, SHA-256 `24caa9e6ff13bf227049a2626f1c816fc895023050f0ec3b12dbf14d897367e0` (equal to S4's value, checked on the host and again in the pod after `kubectl cp`), copied to `/tmp/bin/claude` with `kubectl cp`, `chmod +x`. `--version` in the pod printed `2.1.292 (Claude Code)`. The binary was never run on the host and is not committed.
- Setup: a throwaway kind cluster `remedy-spike`, the pod of `scripts/spike/k8s/s2-pod.yaml` (user 65532, `HOME=/tmp/home`, `CLAUDE_CONFIG_DIR=/tmp/home/claude`, `emptyDir` on `/tmp`). The maintainer logged in once by hand (`kubectl exec -it s2 -- /tmp/bin/claude`, `/login`). No file of the login was opened, copied or listed; "logged in" below means only what the CLI itself says.
- Everything below that has no command next to it was not measured.

## What this record is not

- The pod's base image was `debian:bookworm-slim` (it has a shell and `bash`, needed for `time` and `sh -c`). The runner image will be `gcr.io/distroless/base-debian12:nonroot` (S4). A status command that reads a config directory and prints text does not depend on the image, and S4 showed the binary starts on both; but the check was not run on distroless.
- The first logged-in call was made while the config directory also held unrelated cache and plugin entries from an earlier spike, so "logged in" was measured on a directory that was not minimal. The empty-directory contrast below is the clean "no config" case.
- The pod's own `sh` is dash and has no `time` builtin (`sh: 1: time: not found`, exit 127): the brief's `sh -c 'time ...'` has to be `bash -c` on this image.

## Step 3: the logged-out state (`CLAUDE_CONFIG_DIR=/tmp/home/claude`, no login yet)

```sh
kubectl --context kind-remedy-spike exec s2 -- sh -c 'mkdir -p /tmp/home/claude; for c in "auth status" "auth --help" "--help"; do echo "== claude $c"; /tmp/bin/claude $c; echo "exit=$?"; done'
```

| Candidate | Exit | First line / shape |
| --- | --- | --- |
| `claude auth status` | 1 | JSON on stdout (the default): `{` then `"loggedIn": false` |
| `claude auth status --text` | 1 | `Not logged in. Run claude auth login to authenticate.` |
| `claude auth --help` | 0 | `Usage: claude auth [options] [command]`; subcommands `login`, `logout`, `status` |
| `claude --help` | 0 | `Usage: claude [options] [command] [prompt]`; commands include `auth`, `doctor`, `setup-token`, `install`, `update`, `mcp`, `plugin`; there is no top-level `status`, `whoami` or `login` |

`claude auth status --help` says: `Show authentication status`, options `--json` (`Output as JSON (default)`) and `--text` (`Output as human-readable text`). So there is one status command, `claude auth status`, with two output forms. `doctor` is about the installation and reads settings files in the current directory; it was not run (a status command exists, and `doctor` is not a login check).

Logged-out JSON, complete (nothing in it is personal):

```json
{
  "loggedIn": false,
  "authMethod": "none",
  "apiProvider": "firstParty",
  "analyticsDisabled": false,
  "projectsDirectory": "/tmp/home/claude/projects",
  "configDirectory": "/tmp/home/claude"
}
```

## Step 5: logged in, with `time`, and the contrast

Run with `bash -c 'time ...; echo exit=$?'` (the `time` lines are bash's `real`). The logged-in output has account data; it was filtered before it reached this record: only the field names and the non-personal values below are kept, every other line is `<account line omitted>`.

```sh
kubectl --context kind-remedy-spike exec s2 -- bash -c 'time /tmp/bin/claude auth status; echo "exit=$?"'
kubectl --context kind-remedy-spike exec s2 -- bash -c 'time /tmp/bin/claude auth status --text; echo "exit=$?"'
kubectl --context kind-remedy-spike exec s2 -- bash -c 'mkdir -p /tmp/empty; time CLAUDE_CONFIG_DIR=/tmp/empty /tmp/bin/claude auth status; echo "exit=$?"'
kubectl --context kind-remedy-spike exec s2 -- bash -c 'time CLAUDE_CONFIG_DIR=/tmp/empty /tmp/bin/claude auth status --text; echo "exit=$?"'
```

| State | Command | Exit | Shape | `real` |
| --- | --- | --- | --- | --- |
| logged in | `auth status` | 0 | JSON, keys in order: `loggedIn` (`true`), `authMethod` (`"claude.ai"`), `apiProvider` (`"firstParty"`), `analyticsDisabled`, `projectsDirectory`, `configDirectory`, then four account keys `email`, `orgId`, `orgName`, `subscriptionType`: `<account line omitted>` for each | 0.096 s |
| logged in | `auth status --text` | 0 | three lines: `Login method: <...>` (five words, the rest `<account line omitted>`), then two lines that contain an e-mail address: `<account line omitted>` | 0.094 s |
| empty config dir (`/tmp/empty`) | `auth status` | 1 | the logged-out JSON above, with `/tmp/empty` in the two path fields | 0.070 s |
| empty config dir (`/tmp/empty`) | `auth status --text` | 1 | one line starting `Not logged in.` (nine words, the same sentence as above) | 0.058 s |
| no login yet (step 3, `/tmp/home/claude`) | `auth status` / `--text` | 1 / 1 | as in step 3 | 0.076 s / 0.062 s |

Findings:

- The two states differ in the exit code (0 against 1), in the JSON `loggedIn` value (`true` against `false`) and in the text (`Login method: ...` against `Not logged in. ...`). The exit code alone distinguishes them, in both the JSON and the text form. What was not tried: a corrupt config, an expired or revoked login with a config that is still present (the CLI may then still say `loggedIn: true` here, because the command may not ask the server; see the network paragraph), and a missing config directory (the empty directory behaves like no login: exit 1). Exit 1 was seen only for "not logged in"; whether other failures also exit 1 is not known, so K-6 should treat any other result (the process did not start, a different code, no output) as "unknown", not as "not logged in".
- The empty `CLAUDE_CONFIG_DIR` gives the same answer as a never-logged-in directory (exit 1, `loggedIn: false`, `authMethod: "none"`). No error about a missing file; the command does not create the login or fail on a directory that has nothing in it.
- The status command is called with no prompt, no model and no tool. It printed no usage, token count or cost, and the whole call took under 0.1 s.

### Does it call the network?

Not measured directly (the pod's network was not cut, by instruction). The evidence is indirect:

- Each call took 0.06 to 0.10 s of wall time in the pod, including process start of a 250 MB binary. A request to the Anthropic API from a kind pod would add at least a TLS handshake and a round trip, which is normally well above that; all five calls (logged in and out, JSON and text) are in the same narrow band.
- The logged-out calls take 0.06 to 0.07 s and the logged-in ones 0.09 to 0.10 s. The difference is about 25 ms and is the only trace of extra work when logged in; it is small, and this record does not claim what it is (reading a larger config, or a local check).
- The help text of `auth status` is only `Show authentication status`; it does not say that it contacts a server.

So: the timings and the help text are consistent with a purely local read of the configuration, but network use is NOT measured, and this record does not claim that the command needs no network. A login that exists but has been revoked or has expired on the server side is probably still reported as logged in by this command. Whether that is so was not tested (the login must not be revoked by this spike); the first real run that fails with an authentication error shows it. Remedy's run result is the real test of a login; this command tells whether there is one.

Follow-up for K-6 task 1 (not done here, and it needs no cut of the pod's network): in the real runner pod, run the check once with a proxy that cannot be reached, for example `HTTPS_PROXY=http://127.0.0.1:1 claude auth status --text` (and the same with an empty `CLAUDE_CONFIG_DIR`), and record the exit codes and the time. Until then K-6 must not rely on "needs no network": it must not assume the check works in an air-gapped pod, and it must not treat the check as free of network failures. The one-minute re-check while the runner is not logged in is a design choice that does not depend on the answer.

### Does it cost quota?

No sign of it. The command sends no prompt and no model request (no `-p`, no model flag, no tool), prints no usage and finishes in under 0.1 s; a model call is not that fast. The account's usage page was not looked at before and after, so "no quota" rests on this reasoning, not on a measurement. The brief's last-resort model call (`claude -p ... "reply with ok"`) was not run because a status command exists.

## Account data in the output

The logged-in output of `claude auth status` (JSON and text) contains the account's e-mail address, organization id and name and the subscription type. K-6 and the runner must therefore:

- prefer the exit code (0 against 1) and never store, log, send to the control plane or show in the UI the command's stdout or stderr;
- if the output is read at all (for the phrase `Not logged in` or the JSON `loggedIn` key), parse it in memory, keep only the boolean, and drop the rest. The runner has no use for the account fields; the control plane needs only "logged in: yes or no".

## Clean-up

```text
$ kind delete cluster --name remedy-spike
Deleting cluster "remedy-spike" ...
Deleted nodes: ["remedy-spike-control-plane"]
$ kind get clusters
No kind clusters found.
```

The cluster is gone, and the login with it (it lived on the pod's `emptyDir`). The downloaded binary stays only in the session's scratch directory, outside the repository.

## Decision

```text
check command:         claude auth status --text     (K-6 sets loginCheckArgs to ["auth", "status", "--text"]; the default JSON form carries the same exit codes)
logged in:             exit 0, output matches "Login method: ..." first line (JSON form: "loggedIn": true); output holds account data
not logged in:         exit 1, output matches "Not logged in. Run claude auth login to authenticate." (JSON form: "loggedIn": false, "authMethod": "none")
no config at all:      exit 1     (an empty CLAUDE_CONFIG_DIR answers exactly like a never-logged-in one)
calls the network:     unknown (not measured: timings and help text are consistent with a purely local read; K-6 task 1 tests it with an unreachable HTTPS_PROXY; a revoked login probably still reads as logged in)     takes: 0.06 to 0.10 s
costs quota:           no (no model call, no usage printed; usage page not checked)
K-6 parses:            the exit code (0 logged in, 1 with output starting "Not logged in" is not logged in; any other result is unknown); never store or log the output, it holds the account's e-mail, organization and subscription
```

Recommendation for K-6 (a recommendation, not a measurement): run the check with a timeout of 10 s. The measured 0.06 to 0.10 s leaves a wide margin, and a timeout of 10 s is well under the 30 s upper bound that plan K-6 gives the runner's `LoginChecker`. A timeout means `unknown`, never `missing` (not logged in): the runner reports "could not tell" and checks again, and it must not tell the UI to ask for a login because of a slow or hung check.
