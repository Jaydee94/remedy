# S4: how the claude CLI gets into the runner pod

- Date: 2026-10-07.
- Spike S4 of `docs/plans/k8s-0-spikes.md`; spec `docs/specs/2026-10-06-kubernetes-deployment-design.md` (D9, sections 4 and 9). Feeds K-1, K-2 and K-4.
- Host: macOS arm64, Docker 29.8.1 (arm64). The `linux/arm64` runs are native, the `linux/amd64` runs are emulated (Rosetta); both worked, see step 3.
- Method: the official documentation pages (fetched 2026-10-07), the official install script downloaded to a file and read (never piped to a shell, never run), both Linux binaries downloaded with `curl` and checksummed, and `--version` / `--help` run in throwaway containers. Nothing was logged in, no prompt was sent, no credential was read. No downloaded binary is committed.
- Everything below that has no command or URL next to it was not measured; those places say so.

## Step 1: the official sources

| Fact | Value | Source |
| --- | --- | --- |
| Supported install routes for Linux | Native installer `curl -fsSL https://claude.ai/install.sh \| bash` (marked "Recommended"); signed apt, dnf and apk repositories (`https://downloads.claude.ai/claude-code/{apt,rpm,apk}/{stable,latest}`); npm `npm install -g @anthropic-ai/claude-code` (Node.js 22 or later; the package pulls the same native binary through a per-platform optional dependency such as `linux-x64`, `linux-arm64`, `linux-x64-musl`, `linux-arm64-musl`; the installed binary does not invoke Node). Homebrew and WinGet are macOS and Windows routes. | https://code.claude.com/docs/en/setup |
| Download host (native route) | `https://downloads.claude.ai/claude-code-releases` (`DOWNLOAD_BASE_URL` in the install script; `https://claude.ai/install.sh` redirects to `.../claude-code-releases/bootstrap.sh`, checked with `curl -w '%{url_effective}'`) | `install.sh`, read in full (260 lines) |
| URL pattern with a version | `{base}/{version}/{platform}/claude` is the raw binary; `{base}/{version}/manifest.json` and `{base}/{version}/manifest.json.sig` next to it; `{base}/latest` and `{base}/stable` are plain-text files holding a version number. The script also tries `{base}/{version}/{platform}/claude.zst` with `manifest.zst.json` when `zstd` is installed and falls back to the raw file. | `install.sh`; the setup page's "Binary integrity" section uses `REPO=https://downloads.claude.ai/claude-code-releases` and `$REPO/$VERSION/manifest.json` |
| Artifact names | Platform directory names in the manifest: `linux-x64`, `linux-arm64` (glibc), `linux-x64-musl`, `linux-arm64-musl` (musl). The file is named `claude` in all of them. `install.sh` picks the musl names when the host uses musl. Not `amd64`: `.../2.1.292/linux-amd64/claude` answers 404 (step 2). | `install.sh` (`arch="x64"` for `x86_64\|amd64`), `manifest.json` of 2.1.292 |
| Archive | None. The artifact is a bare ELF executable (`file`: dynamically linked, interpreter `/lib/ld-linux-aarch64.so.1` and `/lib64/ld-linux-x86-64.so.2`), about 250 MB. | step 2 |
| Where the checksum is published | `manifest.json` of each release: `platforms.<platform>.checksum` (SHA-256 hex) and `size`. The manifest carries a detached GPG signature `manifest.json.sig`; "Manifest signatures are available for releases from `2.1.89` onward." Key: `https://downloads.claude.ai/keys/claude-code.asc`, fingerprint `31DD DE24 DDFA B679 F42D 7BD2 BAA9 29FF 1A7E CACE`. Linux binaries themselves "are not individually code-signed". | setup page, "Binary integrity and code signing" |
| Can a version be pinned | Yes: `install.sh` accepts `stable`, `latest` or a version number (`curl ... \| bash -s 2.1.89`), and the version directory is fetchable directly (200 for `2.1.292`, 404 for `2.1.999`, step 2). | setup page, "Install a specific version"; step 2 |
| Redistribution wording | "Unless we've mutually agreed otherwise, preinstalling or running Claude Code in your products or services (e.g. in hosted sandboxes or other agent infrastructure) requires agreeing to our Commercial Terms of Service and complying with the conditions below" and the first condition: "The Claude Code binary must not be modified. Claude Code must be installed and run as published by Anthropic, and customers may not remove, disable, or restrict any authentication method built into it". The page names no right to redistribute the binary in a public image; it is silent on that, so D9 (the binary is not in any image) stays the safe reading. | https://code.claude.com/docs/en/legal-and-compliance, "Can customers offer Claude Code in their products?" |
| Login by an end user of a hosted binary | The same page: it does not prevent "an end user from signing in to the unmodified Claude Code binary with their own Claude subscription, including where a platform hosts Claude Code as described under *Can customers offer Claude Code in their products?* above". | legal-and-compliance, "Authentication and credential use" |

Install-script mechanism, read from the script: it requests `{base}/latest`, then `{base}/{version}/manifest.json`, takes the checksum of its platform, downloads `.../{platform}/claude` and compares `sha256sum`; only then does it run the downloaded binary with `install` to set up a launcher in `~/.local/bin/claude` (a symlink into `~/.local/share/claude/versions/`). The `install` step is not needed in a pod: the downloaded file is the binary and runs as it is (step 3).

## Step 2: both architectures downloaded and checked

Pinned version for this spike: `2.1.292` (`{base}/latest` answered `2.1.292` and `{base}/stable` answered `2.1.285` on 2026-10-07; `claude --version` on the maintainer's machine also prints `2.1.292`; the earlier real runs of the repo were on 2.1.288, see "Which version" below).

```sh
B=https://downloads.claude.ai/claude-code-releases; V=2.1.292
for p in linux-x64 linux-arm64; do
  curl -fsSL -o "claude-$p" "$B/$V/$p/claude" -w "$p %{http_code} %{size_download}\n"
  shasum -a 256 "claude-$p"
done
```

```text
linux-x64 200 251456696
a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3  claude-linux-x64
linux-arm64 200 250798072
24caa9e6ff13bf227049a2626f1c816fc895023050f0ec3b12dbf14d897367e0  claude-linux-arm64
```

Compared with `platforms.<platform>.checksum` in `https://downloads.claude.ai/claude-code-releases/2.1.292/manifest.json` (fetched separately, parsed with `python3 -I`):

| Platform | Computed SHA-256 | Manifest SHA-256 | Size (bytes, manifest = download) | Result |
| --- | --- | --- | --- | --- |
| `linux-x64` | `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3` | `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3` | 251456696 | match |
| `linux-arm64` | `24caa9e6ff13bf227049a2626f1c816fc895023050f0ec3b12dbf14d897367e0` | `24caa9e6ff13bf227049a2626f1c816fc895023050f0ec3b12dbf14d897367e0` | 250798072 | match |

Signature of the manifest (isolated `GNUPGHOME` in the scratch directory, key imported from the URL above):

```text
pub   rsa4096 2026-03-30 [SCE]
      31DD DE24 DDFA B679 F42D  7BD2 BAA9 29FF 1A7E CACE
uid           Anthropic Claude Code Release Signing <security@anthropic.com>
$ gpg --verify manifest.json.sig manifest.json
gpg: Good signature from "Anthropic Claude Code Release Signing <security@anthropic.com>"
gpg: WARNING: This key is not certified with a trusted signature!   (expected for a fresh key, per the docs)
```

The fingerprint equals the one the setup page publishes. So the SHA-256 of each Linux artifact is published by the vendor, in a signed manifest. The musl variants exist for the same version (`linux-x64-musl`, `linux-arm64-musl`), they are not needed on a glibc base.

URL probes (`curl -sI`): `.../2.1.292/linux-x64/claude` 200, `.../2.1.292/linux-arm64/claude` 200, `manifest.json` 200, `manifest.json.sig` 200, `.../2.1.999/linux-x64/claude` 404, `.../2.1.292/linux-amd64/claude` 404.

## Step 3: which base image runs it

```sh
mkdir ro-arm64 ro-x64; cp claude-linux-arm64 ro-arm64/claude; cp claude-linux-x64 ro-x64/claude; chmod +x ro-*/claude
docker run --rm [--platform linux/amd64] --entrypoint /opt/claude/claude -v "$PWD/ro-<arch>:/opt/claude:ro" <image> --version
```

| Image | linux/arm64 (native) | linux/amd64 (emulated) |
| --- | --- | --- |
| `gcr.io/distroless/base-debian12:nonroot` | `2.1.292 (Claude Code)`, exit 0 | `2.1.292 (Claude Code)`, exit 0 |
| `gcr.io/distroless/static-debian12:nonroot` | fails, exit 255: `exec /opt/claude/claude: no such file or directory` (the dynamic loader is missing) | fails, exit 133: `rosetta error: failed to open elf at /lib64/ld-linux-x86-64.so.2` |
| `debian:bookworm-slim` | `2.1.292 (Claude Code)`, exit 0 | `2.1.292 (Claude Code)`, exit 0 |

Why `static` fails: the binary is dynamically linked against glibc, so it needs the loader and glibc, which `static` does not ship. `ldd /opt/claude/claude` in `debian:bookworm-slim` (glibc 2.36), both architectures, lists only glibc libraries: `librt.so.1`, `libc.so.6`, `libpthread.so.0`, `libdl.so.2`, `libm.so.6` and the loader (`/lib64/ld-linux-x86-64.so.2` or `/lib/ld-linux-aarch64.so.1`). No `libstdc++`, no `libgcc_s`, no OpenSSL: `base-debian12` has all of it and nothing more is needed to start. (A string scan of the binaries for `GLIBC_` symbol versions found at most `GLIBC_2.26`; a scan, not a proof.)

`gcr.io/distroless/base-debian12:nonroot` contents checked with `docker export | tar -t`: `lib/aarch64-linux-gnu/libc.so.6`, `etc/ssl/certs/ca-certificates.crt`, `etc/passwd`, `usr/share/zoneinfo/UTC`; no `bin/sh`, no `usr/bin/git`, no `usr/bin/rg`. The `nonroot` user is 65532.

Runtime needs, from the setup page: Debian 10+, Ubuntu 20.04+, Alpine 3.19+ with `libgcc`, `libstdc++` and `ripgrep` (musl only); 4 GB or more of RAM; a network; "Shell: Bash, Zsh, PowerShell, or CMD"; and "ripgrep: usually included with Claude Code" (`USE_BUILTIN_RIPGREP=0` uses a system `rg` instead). The page does not list `git` as a dependency. What was verified here is only that the binary starts (`--version`, `--help`) on `base-debian12`. Not verified, because it needs a prompt or a login: that the bundled ripgrep works on a base without a shell, and that `claude -p` never starts a shell or `git` at startup. Remedy hands the CLI the tools `Read,Grep,Glob` only (`readOnlyTools` in `internal/provider/claude.go`), plus the MCP server, so no Bash tool is offered; K-1 or K-6 should still confirm with a real run in the pod (a run that uses `Grep`). `claude doctor` and `claude update` were not run.

Smallest image that works: `gcr.io/distroless/base-debian12:nonroot` (glibc, CA certificates, no shell). It matches the spec's "small glibc-based base, the image has no shell". Fallback if the real run needs a shell or `rg`: `debian:bookworm-slim` (also works, verified). `kubectl exec -it ... /opt/claude/claude` works without a shell in the image, because the binary is executed directly (the `--entrypoint` runs above exercise exactly that path; the interactive login itself was not run).

## Step 4: self-update and a read-only install directory

```sh
docker run --rm --entrypoint /opt/claude/claude -v "$PWD/ro-arm64:/opt/claude:ro" --read-only --tmpfs /tmp --tmpfs /home/nonroot:uid=65532 -u 65532 -e HOME=/home/nonroot <image> --version
```

Results (user 65532, read-only root file system, read-only bind mount of the install directory):

| Image / platform | `--version` | `--help` |
| --- | --- | --- |
| `debian:bookworm-slim`, arm64 | `2.1.292 (Claude Code)` | exit 0 |
| `gcr.io/distroless/base-debian12:nonroot`, arm64 | `2.1.292 (Claude Code)` | exit 0 |
| `gcr.io/distroless/base-debian12:nonroot`, amd64 emulated | `2.1.292 (Claude Code)` | not run |

Also works with `HOME=/home/nonroot` on the read-only root without a tmpfs, and with no `HOME` at all (`distroless/base-debian12:nonroot`, arm64): `--version` printed the version in both cases.

Update-related subcommands in `--help` of 2.1.292 (the only lines matching `update|install|doctor` that are commands):

```text
  doctor                                Check the health of your Claude Code installation. Reads settings files in the current directory without a trust prompt. ...
  install [options] [target]            Install Claude Code native build. Use [target] to specify version (stable, latest, or specific version)
  update|upgrade                        Check for updates and install if available
```

The flags Remedy's invocation uses are all present in this `--help`: `-p`, `--output-format`, `--verbose`, `--permission-mode` (with `dontAsk`), `--safe-mode`, `--restricted`, `--strict-mcp-config`, `--mcp-config`, `--tools`, `--allowedTools`, `--model`.

How to turn automatic updates off (source: https://code.claude.com/docs/en/setup "Disable auto-updates" and https://code.claude.com/docs/en/env-vars):

- `DISABLE_AUTOUPDATER=1`: "disable automatic background updates. Manual `claude update` still works."
- `DISABLE_UPDATES=1`: "block all updates including manual `claude update` and `claude install`. Stricter than `DISABLE_AUTOUPDATER`. Use when distributing Claude Code through your own channels and users should not self-update."
- `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` (any non-empty value, even `0`) also stops auto-updates, among telemetry and other traffic.
- Both `DISABLE_*` variables are documented as `settings.json` `env` entries and in the environment-variable list; the docs do not say in so many words that the process environment suffices for them (the page does say that `CLAUDE_CONFIG_DIR` must be in the environment, and that other variables in the shell environment are read at startup).

What this means for Remedy (found, not tested):

1. Where an update would write: the docs say the native installer keeps versions in `~/.local/share/claude/versions/` and a launcher in `~/.local/bin/claude`. With `HOME=/state` that is on the state volume, which is writable. A read-only `/opt/claude` therefore does not by itself stop an update from landing somewhere else. Whether a binary started as `/opt/claude/claude` (not installed through its own `install`) tries to update at all in `-p` mode is not measured: that needs a run that reaches the network start-up path, which the rules of this spike excluded (no login, no prompt).
2. `provider.FilterEnv` would drop `DISABLE_UPDATES` and `DISABLE_AUTOUPDATER` (the allowlist is `PATH HOME USER LANG LC_ALL TMPDIR XDG_CONFIG_HOME HTTPS_PROXY HTTP_PROXY NO_PROXY CLAUDE_CONFIG_DIR CLAUDE_CODE_OAUTH_TOKEN`), and `--restricted` ignores the user settings file, so the `settings.json` route of the docs does not apply to a run either. A variable would need to be in the allowlist, which the spec forbids today ("nothing is added to its allowlist by this spec"). The pod's own environment (for the interactive login by `kubectl exec`) can carry `DISABLE_UPDATES=1` without touching Remedy's code.
3. Recommendation for K-1: do not add to the allowlist on the strength of this record. The read-only `/opt/claude` mount is enough for what the spec claims ("a CLI that tries to update itself finds nothing writable" at the install path): the binary starts and prints its version with a read-only install directory, a read-only root and a tmpfs home. Whether an update could write into `$HOME/.local/share/claude` on the PVC is an open question for the first real run in the pod (check that `/state/.local/share/claude` stays absent after a run). If it appears, the options are `DISABLE_UPDATES=1` in the pod environment plus an allowlist entry (a spec change), or mounting nothing writable under `$HOME/.local`.

A cache of the binary on the state volume (S4's last question in the spec) is not worth it for now: the download is 250 MB per start (init container, once per pod start), the pin is exact, and a cache would put an executable on the one writable volume, which the read-only mount is meant to prevent. Not measured: download time.

## Which version

- `2.1.292` is on the `latest` channel today, is the version on the maintainer's machine, and is newer than the 2.1.288 on which `--safe-mode`, `--restricted` and the MCP behaviour were measured (`CLAUDE.md`, `docs/research/phase-2c-real-run.md`). All flags of Remedy's invocation exist in its `--help`. It has not itself been run through Remedy's chain; K-6 or the first dummy run does that.
- The `stable` channel is `2.1.285`, older than the measured 2.1.288, so it was not chosen. Its Linux checksums, in case K-2 wants the channel pin: `linux-x64 33dad1ec615a2e08cc78b494f05c110e49916de2c79d78ec8799ebf46b233d29`, `linux-arm64 24fac77749bed3d91365d6b6915aa4b824e14318ecb6bc17adbc192f01c9173d` (from its `manifest.json`, not downloaded).
- Updating the pin means: new version, both checksums from the new `manifest.json` (and its signature checked as above). A renovate custom manager can read `https://downloads.claude.ai/claude-code-releases/latest` (or `stable`).

## Decision

```text
urlTemplate:    https://downloads.claude.ai/claude-code-releases/{version}/{platform}/claude
archive:        none   member: 
platforms:      amd64=linux-x64  arm64=linux-arm64
sha256 source:  published at https://downloads.claude.ai/claude-code-releases/{version}/manifest.json (platforms.<platform>.checksum; signed by manifest.json.sig, key 31DDDE24DDFAB679F42D7BD2BAA929FF1A7ECACE)
runner base:    gcr.io/distroless/base-debian12:nonroot
runtime needs:  glibc and its loader (in the base); CA certificates (in the base); no shell, git or ripgrep binary (the CLI bundles ripgrep, per the docs: not verified without a run); network to the Anthropic API; a writable HOME and CLAUDE_CONFIG_DIR (the state volume)
read-only dir:  works (--version and --help run from a read-only mount with a read-only root, user 65532; whether an update writes under $HOME/.local/share/claude on the state volume is not measured)
pinned version: 2.1.292  (linux-x64 a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3, linux-arm64 24caa9e6ff13bf227049a2626f1c816fc895023050f0ec3b12dbf14d897367e0)
```

Notes for the consumers:

- K-1 (`remedy-runner install-cli`): the Go code needs only the template, the platform name per `runtime.GOARCH` (`amd64` to `linux-x64`, `arm64` to `linux-arm64`), the pinned checksum per platform and a length check against the manifest `size` is optional. It does not need `gpg`: the pin lives in `values.yaml`, and the signature was checked once, here. Write the file with mode `0755`; the release serves a plain file with a `200` and no redirect on the URLs probed.
- K-2 (`values.yaml`): `runner.cli.version: 2.1.292` and `runner.cli.platforms: {amd64: {name: linux-x64, sha256: a967...}, arm64: {name: linux-arm64, sha256: 24ca...}}` as above.
- K-4 (images): `Dockerfile.runner` uses `gcr.io/distroless/base-debian12:nonroot`, user 65532, and `remedy-runner` as the entrypoint; `claude` is `/opt/claude/claude` from the init container.
