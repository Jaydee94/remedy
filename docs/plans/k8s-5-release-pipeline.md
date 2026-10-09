# K-5: The Release Pipeline and the Homelab Runbook Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A tag `vX.Y.Z` builds the control plane image and the runner image for `linux/amd64` and `linux/arm64`, pushes them to GHCR, and refuses to do so unless the Helm chart's version matches the tag; the pinned CLI in `deploy/cli-pin.yaml` is bumped by Renovate and kept honest by a check that recomputes its checksums; and a runbook takes the maintainer from an empty k3s namespace to a running Remedy under Argo CD, with a SealedSecret, an Ingress and a one-time login.

**Architecture:** One workflow (`images.yml`) builds and pushes (and only builds on a pull request); one workflow (`cli-pin.yml`) runs `scripts/cli-checksums.sh --check` when the pin changes; two small shell scripts with tests (`check-release.sh`, `cli-checksums.sh`); Renovate custom managers for the CLI version and the Argo CD version; and `docs/runbook/homelab-deploy.md`.

**Tech Stack:** GitHub Actions (Docker's official actions), POSIX `sh`, `curl`, `sed`, Renovate.

**Spec:** [`docs/specs/2026-10-06-kubernetes-deployment-design.md`](../specs/2026-10-06-kubernetes-deployment-design.md), section 8 and decisions D5, D6, D9. Needs [K-1](k8s-1-images-and-listener.md) (the Dockerfiles) and [K-2](k8s-2-chart-core.md) (the chart); K-3 and K-4 make the runbook complete (the cluster identities, `deploy/cli-pin.yaml`).

**Scope note:** Pushing a tag and changing a package's visibility are outward-facing, hard-to-reverse actions. Task 6 stops and asks the maintainer before each. Everything else is a file in the repository.

## Decisions made while planning

| Topic | Spec said | This plan |
|---|---|---|
| Tags of the images | `sha-<short>` and `vX.Y.Z` | `sha-<short>` on every push to `main`, `edge` on `main`, and `X.Y.Z` (no `v`) on a tag. The chart's `appVersion` is the default image tag, so it must be `X.Y.Z` too, and `check-release.sh` enforces that. |
| Pull requests | not mentioned | A pull request that touches the Dockerfiles, the Go sources or the web sources builds both images for `linux/amd64` without pushing, so a broken Dockerfile fails in review, not on the tag. |
| Keeping the CLI pin honest | Renovate bumps the version | Renovate can bump the version but cannot compute the artifacts' checksums (that needs a self-hosted post-upgrade task). `cli-pin.yml` fails such a pull request until `scripts/cli-checksums.sh <version>` has written the right checksums; the maintainer runs it and pushes. |
| Where the checksum comes from | "published at … or pinned by us" (spike S4) | S4's record shows that the vendor publishes them: `https://downloads.claude.ai/claude-code-releases/<version>/manifest.json` has `platforms.<platform>.checksum` for `linux-x64` and `linux-arm64`, signed by `manifest.json.sig` (key fingerprint `31DD DE24 DDFA B679 F42D 7BD2 BAA9 29FF 1A7E CACE`). `cli-checksums.sh` still computes the SHA-256 of what the HTTPS download returns, and the maintainer feeds `--expect amd64=<sha> --expect arm64=<sha>` from that manifest, so that nothing is written unless the download equals the published value. `--check` in CI recomputes the download only: it is a consistency check against the file, not against the vendor. The manifest comes from the same host as the binary, so it is an independent proof only when its signature is verified; the runbook says how, once per bump, and the script does not run `gpg`. |
| Where the CLI's versions come from (Renovate) | "the npm package" | S4's record names `https://downloads.claude.ai/claude-code-releases/latest` (a plain-text file holding one version number; `stable` is the slower channel and was older than the measured 2.1.288) as the version source for a Renovate manager. It does not say that the npm package `@anthropic-ai/claude-code` carries the same version numbers, so task 4 uses a custom datasource over `latest` and not npm. The Renovate configuration is **not verified against a Renovate run**: the first pull request it opens, or a dry run, is the check. |
| Package visibility | public images | GHCR makes a first-time package from an Actions push private. Making both packages public is a manual step in GitHub's UI, once; the runbook and task 6 say so. |
| Argo CD hooks | PostSync | The chart's Helm hook annotations are `post-install,post-upgrade`, which Argo CD runs as `PostSync`; the Application example in the runbook does not set anything for that. |
| Action versions | not mentioned | Task 3 looks up the latest major of each action with `gh` and uses it. Renovate keeps them current afterwards. |

## Global Constraints

- Everything committed is English: docs, workflows, scripts, comments, commit messages.
- The image workflow has `contents: read` everywhere and `packages: write` on the one job that pushes, and nothing else. It logs in to GHCR with `GITHUB_TOKEN`; no personal token and no other secret.
- No image contains the `claude` binary (the runner image is built from `Dockerfile.runner`, whose only Remedy file is `remedy-runner`).
- The installer's checksums are the ones in `deploy/cli-pin.yaml`; `cli-checksums.sh` writes only the `version` and the two `sha256` fields of that file, nothing else.
- `scripts/cli-checksums.sh` accepts only `https` URLs, except for `file://` when `CLI_CHECKSUMS_ALLOW_FILE=1` is set (the script's own test uses it).
- A tag that is not `vX.Y.Z` (no prerelease suffix) is not published.
- Shell runs on macOS and on Linux: no BSD-only flags (`sed -i ''`, `stat -f`, `date -j`).
- `make check` passes at the end of every task. Every commit message ends with the trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.

## Review Focus

- A tag `v0.2.0` on a chart that says `0.1.0`, a tag `v0.1.0-rc1`, a tag without the `v`: each refuses to publish (task 1).
- A checksum recomputed after the artifact changed on the vendor's side, or a published checksum that differs from what was downloaded: `--check` and `--expect` fail and the pin file is untouched (task 2).
- A pull request from a fork: the build job must not need a secret; it builds and does not push (task 3).
- Renovate bumping only `version`: the `cli-pin` check fails on that pull request instead of letting a pin with a wrong checksum merge (task 3 and 4).
- A homelab install on k3s: the runbook's values give a working network policy (the API server's address), a Secure cookie behind Traefik, and an Argo CD Application that does not fight the refresher over the Secret (task 5).

## How to read the code blocks

A line `Create `path`:` or `Overwrite `path`:` is followed by the complete file. A line `In `path`, replace:` is followed by a block with the exact old text, a line `with:` and a block with the new text; the old text occurs exactly once in the file.

## File Structure

| Path | Responsibility |
|---|---|
| `scripts/check-release.sh`, `scripts/check-release_test.sh` | A tag must match the chart's `version` and `appVersion` |
| `scripts/cli-checksums.sh`, `scripts/cli-checksums_test.sh` | Write or check the CLI pin's version and checksums |
| `.github/workflows/images.yml`, `.github/workflows/cli-pin.yml` | Build and push the images; check the pin |
| `renovate.json` | Managers for the CLI version and the Argo CD version |
| `docs/runbook/homelab-deploy.md` | The homelab install and the release process |
| `Makefile` | `shell-test` runs the two new tests |

---

### Task 1: A tag must match the chart

**Files:**
- Create: `scripts/check-release.sh`, `scripts/check-release_test.sh`
- Modify: `Makefile`

**Interfaces:**
- Produces: `scripts/check-release.sh <tag> [chart file]`: exit 0 when the tag is `vX.Y.Z` and `Chart.yaml`'s `version` and `appVersion` both equal `X.Y.Z`; exit 1 with a message otherwise. Used by `images.yml` (task 3).

- [ ] **Step 1: Write the failing test**

Create `scripts/check-release_test.sh`:

```sh
#!/bin/sh
# Tests of check-release.sh. Run: sh scripts/check-release_test.sh
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
CHECK=$HERE/check-release.sh
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
fails=0

chart() { printf 'apiVersion: v2\nname: remedy\nversion: %s\nappVersion: "%s"\n' "$1" "$2" > "$WORK/Chart.yaml"; }
# ok <name> <tag>: must pass. bad <name> <tag>: must fail.
ok()  { if sh "$CHECK" "$2" "$WORK/Chart.yaml" > /dev/null 2>&1; then echo "ok    $1"; else echo "FAIL  $1 (should pass)"; fails=$((fails + 1)); fi; }
bad() { if sh "$CHECK" "$2" "$WORK/Chart.yaml" > /dev/null 2>&1; then echo "FAIL  $1 (should fail)"; fails=$((fails + 1)); else echo "ok    $1"; fi; }

chart 0.1.0 0.1.0
ok  "a tag that matches"                    v0.1.0
bad "a tag of another version"              v0.2.0
bad "a prerelease tag"                      v0.1.0-rc1
bad "a tag without the v"                   0.1.0
bad "a branch name"                         main
bad "an empty tag"                          ""

chart 0.2.0 0.1.0
bad "the chart's version differs from appVersion" v0.1.0
chart 0.1.0 0.2.0
bad "appVersion differs from the chart's version" v0.1.0
chart 0.10.0 0.10.0
ok  "two-digit minor"                       v0.10.0
bad "0.1.0 is not 0.10.0"                   v0.1.0

printf 'apiVersion: v2\nname: remedy\n' > "$WORK/Chart.yaml"
bad "a chart without versions"              v0.1.0
rm -f "$WORK/Chart.yaml"
bad "a missing chart file"                  v0.1.0

[ "$fails" -eq 0 ] && echo "all passed" || { echo "$fails failed" >&2; exit 1; }
```

- [ ] **Step 2: Run it to see it fail**

Run: `sh scripts/check-release_test.sh | tail -4`
Expected: FAIL lines for the `ok` cases (the script does not exist yet, so nothing passes).

- [ ] **Step 3: The script**

Create `scripts/check-release.sh`:

```sh
#!/bin/sh
# A release tag must match the Helm chart: the images are tagged with the version of the tag, and the chart's default
# image tag is its appVersion. Used by .github/workflows/images.yml before anything is pushed.
# Usage: scripts/check-release.sh <tag> [chart file]    e.g. scripts/check-release.sh v0.1.0
set -eu
tag=${1:-}
chart=${2:-deploy/chart/Chart.yaml}

case $tag in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "the tag '$tag' is not vX.Y.Z" >&2; exit 1 ;;
esac
want=${tag#v}
case $want in
  *[!0-9.]*) echo "the tag '$tag' is not vX.Y.Z (no prerelease suffix is published)" >&2; exit 1 ;;
esac
[ -f "$chart" ] || { echo "$chart does not exist" >&2; exit 1; }

field() { sed -n "s/^$1: *\"\{0,1\}\([^\" ]*\)\"\{0,1\} *\$/\1/p" "$chart" | head -1; }
version=$(field version)
app=$(field appVersion)
[ -n "$version" ] && [ -n "$app" ] || { echo "$chart has no version or no appVersion" >&2; exit 1; }
if [ "$version" != "$want" ] || [ "$app" != "$want" ]; then
  echo "the tag says $want but $chart has version $version and appVersion $app: change both, merge, then tag" >&2
  exit 1
fi
echo "the tag $tag matches the chart"
```

- [ ] **Step 4: Run the test**

Run: `sh scripts/check-release_test.sh`
Expected: every line `ok`, then `all passed`. (The `field` function's sed handles `appVersion: "0.1.0"` and `version: 0.1.0`; the `version` pattern does not match the `appVersion` line because it is anchored at the start of the line.)

- [ ] **Step 5: Put it in `make shell-test`, commit**

In `Makefile`, replace:

```make
	sh scripts/check-login-dir-untouched.sh
```

with:

```make
	sh scripts/check-login-dir-untouched.sh
	sh scripts/check-release_test.sh
```

```bash
chmod +x scripts/check-release.sh
git add scripts Makefile
git commit -m "feat: a check that a release tag matches the Helm chart

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: `cli-checksums.sh`

**Files:**
- Create: `scripts/cli-checksums.sh`, `scripts/cli-checksums_test.sh`
- Modify: `Makefile`

**Interfaces:**
- Consumes: the shape of `deploy/cli-pin.yaml` (plan K-4): lines `version: "…"`, `urlTemplate: "…"`, `archive: …`, and `amd64: {name: "…", sha256: "…"}` and `arm64: {name: "…", sha256: "…"}`.
- Produces: `scripts/cli-checksums.sh <version> [--pin FILE] [--expect ARCH=SHA]...` rewrites `version` and both `sha256` fields; `scripts/cli-checksums.sh --check [--pin FILE]` recomputes for the file's own version and exits 1 on any difference. Prints one line per platform.

- [ ] **Step 1: Write the failing test**

Create `scripts/cli-checksums_test.sh`:

```sh
#!/bin/sh
# Tests of cli-checksums.sh against local files. Run: sh scripts/cli-checksums_test.sh
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
SCRIPT=$HERE/cli-checksums.sh
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
fails=0
export CLI_CHECKSUMS_ALLOW_FILE=1

sum() { if command -v sha256sum > /dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }

for v in 1.0.0 2.0.0; do
  for p in linux-x64 linux-arm64; do
    mkdir -p "$WORK/art/$v/$p"
    printf 'cli %s for %s\n' "$v" "$p" > "$WORK/art/$v/$p/claude"
  done
done
X1=$(sum "$WORK/art/1.0.0/linux-x64/claude"); A1=$(sum "$WORK/art/1.0.0/linux-arm64/claude")
X2=$(sum "$WORK/art/2.0.0/linux-x64/claude"); A2=$(sum "$WORK/art/2.0.0/linux-arm64/claude")

pin() { # pin <version> <amd64 sha> <arm64 sha>
  cat > "$WORK/pin.yaml" <<EOF
# a comment that must survive
runner:
  cli:
    version: "$1"
    urlTemplate: "file://$WORK/art/{version}/{platform}/claude"
    archive: none
    member: ""
    platforms:
      amd64: {name: "linux-x64", sha256: "$2"}
      arm64: {name: "linux-arm64", sha256: "$3"}
EOF
}
eq()  { if [ "$2" = "$3" ]; then echo "ok    $1"; else echo "FAIL  $1"; echo "  want: $2"; echo "  got:  $3"; fails=$((fails + 1)); fi; }
ok()  { n=$1; shift; if "$@" > "$WORK/out" 2>&1; then echo "ok    $n"; else echo "FAIL  $n (should pass)"; cat "$WORK/out"; fails=$((fails + 1)); fi; }
bad() { n=$1; shift; if "$@" > "$WORK/out" 2>&1; then echo "FAIL  $n (should fail)"; fails=$((fails + 1)); else echo "ok    $n"; fi; }

zero=0000000000000000000000000000000000000000000000000000000000000000

pin 1.0.0 "$X1" "$A1"
ok  "--check passes for a correct pin" sh "$SCRIPT" --check --pin "$WORK/pin.yaml"

pin 1.0.0 "$zero" "$A1"
bad "--check fails for a wrong checksum" sh "$SCRIPT" --check --pin "$WORK/pin.yaml"

pin 1.0.0 "$X1" "$A1"
ok  "a new version is written" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml"
eq  "the version is now 2.0.0" 'version: "2.0.0"' "$(grep 'version:' "$WORK/pin.yaml" | sed 's/^ *//')"
eq  "the amd64 checksum is the new one" "$X2" "$(sed -n 's/.*amd64: {name: "linux-x64", sha256: "\([0-9a-f]*\)"}/\1/p' "$WORK/pin.yaml")"
eq  "the arm64 checksum is the new one" "$A2" "$(sed -n 's/.*arm64: {name: "linux-arm64", sha256: "\([0-9a-f]*\)"}/\1/p' "$WORK/pin.yaml")"
eq  "the comment survived" "# a comment that must survive" "$(head -1 "$WORK/pin.yaml")"
ok  "and --check passes on the result" sh "$SCRIPT" --check --pin "$WORK/pin.yaml"

pin 1.0.0 "$X1" "$A1"
ok  "published checksums that match are accepted" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml" --expect "amd64=$X2" --expect "arm64=$A2"

pin 1.0.0 "$X1" "$A1"
cp "$WORK/pin.yaml" "$WORK/before.yaml"
bad "a published checksum that differs refuses" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml" --expect "amd64=$zero"
eq  "and leaves the pin untouched" "$(cat "$WORK/before.yaml")" "$(cat "$WORK/pin.yaml")"

bad "a version that does not exist (no file) refuses" sh "$SCRIPT" 9.9.9 --pin "$WORK/pin.yaml"
eq  "and leaves the pin untouched again" "$(cat "$WORK/before.yaml")" "$(cat "$WORK/pin.yaml")"
bad "a version with a path in it refuses" sh "$SCRIPT" ../1.0.0 --pin "$WORK/pin.yaml"
bad "no version and no --check refuses" sh "$SCRIPT" --pin "$WORK/pin.yaml"
bad "an unknown option refuses" sh "$SCRIPT" 2.0.0 --nope

unset CLI_CHECKSUMS_ALLOW_FILE
bad "a file:// URL is refused without the test switch" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml"

[ "$fails" -eq 0 ] && echo "all passed" || { echo "$fails failed" >&2; exit 1; }
```

- [ ] **Step 2: Run it to see it fail**

Run: `sh scripts/cli-checksums_test.sh | tail -5`
Expected: FAIL lines (the script does not exist).

- [ ] **Step 3: The script**

Create `scripts/cli-checksums.sh`:

```sh
#!/bin/sh
# Writes, or checks, the version and the SHA-256 checksums of the pinned claude CLI in deploy/cli-pin.yaml.
#
#   scripts/cli-checksums.sh <version> [--pin FILE] [--expect ARCH=SHA]...
#       downloads the artifact of that version for both platforms, and rewrites the version and the two sha256 fields.
#       With --expect it first compares the computed checksum with a checksum the vendor published (in the release's
#       manifest.json, platforms.<platform>.checksum; trust is otherwise "what the HTTPS download returned"), and writes
#       nothing on a difference.
#   scripts/cli-checksums.sh --check [--pin FILE]
#       recomputes the checksums for the version in the file and exits 1 on any difference. CI runs this when the pin
#       changes, because Renovate can bump the version but cannot compute the checksums.
#
# It never executes what it downloads. Only https URLs are accepted (file:// only for its own test, with
# CLI_CHECKSUMS_ALLOW_FILE=1). Needs curl and sed.
set -eu

pin=deploy/cli-pin.yaml
mode=write
version=
expect_amd64=
expect_arm64=

while [ $# -gt 0 ]; do
  case $1 in
    --check) mode=check ;;
    --pin) pin=${2:?--pin needs a file}; shift ;;
    --expect)
      arg=${2:?--expect needs ARCH=SHA}; shift
      case $arg in
        amd64=*) expect_amd64=${arg#amd64=} ;;
        arm64=*) expect_arm64=${arg#arm64=} ;;
        *) echo "--expect takes amd64=SHA or arm64=SHA" >&2; exit 2 ;;
      esac ;;
    -*) echo "unknown option $1" >&2; exit 2 ;;
    *) [ -z "$version" ] || { echo "one version only" >&2; exit 2; }; version=$1 ;;
  esac
  shift
done

[ -f "$pin" ] || { echo "$pin does not exist" >&2; exit 2; }
if [ "$mode" = write ]; then
  [ -n "$version" ] || { echo "usage: cli-checksums.sh <version> | --check" >&2; exit 2; }
else
  version=$(sed -n 's/^ *version: *"\([^"]*\)".*/\1/p' "$pin" | head -1)
  [ -n "$version" ] || { echo "$pin has no version" >&2; exit 2; }
fi
case $version in
  *[!0-9A-Za-z._-]* | "" | .*) echo "'$version' is not a version string" >&2; exit 2 ;;
esac

template=$(sed -n 's/^ *urlTemplate: *"\([^"]*\)".*/\1/p' "$pin" | head -1)
name_amd64=$(sed -n 's/^ *amd64: *{name: *"\([^"]*\)".*/\1/p' "$pin" | head -1)
name_arm64=$(sed -n 's/^ *arm64: *{name: *"\([^"]*\)".*/\1/p' "$pin" | head -1)
[ -n "$template" ] && [ -n "$name_amd64" ] && [ -n "$name_arm64" ] || { echo "$pin lacks the urlTemplate or a platform name" >&2; exit 2; }

sha256() {
  if command -v sha256sum > /dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# fetch <platform name> prints the SHA-256 of the artifact for the platform.
fetch() {
  url=$(printf '%s' "$template" | sed "s|{version}|$version|g; s|{platform}|$1|g")
  case $url in
    https://*) ;;
    file://*) [ "${CLI_CHECKSUMS_ALLOW_FILE:-}" = 1 ] || { echo "only https URLs are accepted: $url" >&2; exit 2; } ;;
    *) echo "only https URLs are accepted: $url" >&2; exit 2 ;;
  esac
  curl -fsSL --retry 2 -o "$work/artifact" "$url" || { echo "cannot download $url" >&2; exit 1; }
  sha256 "$work/artifact"
}

got_amd64=$(fetch "$name_amd64")
got_arm64=$(fetch "$name_arm64")
echo "amd64 ($name_amd64): $got_amd64"
echo "arm64 ($name_arm64): $got_arm64"

if [ "$mode" = check ]; then
  have_amd64=$(sed -n 's/^ *amd64: *{name: *"[^"]*", *sha256: *"\([0-9a-f]*\)".*/\1/p' "$pin" | head -1)
  have_arm64=$(sed -n 's/^ *arm64: *{name: *"[^"]*", *sha256: *"\([0-9a-f]*\)".*/\1/p' "$pin" | head -1)
  if [ "$have_amd64" != "$got_amd64" ] || [ "$have_arm64" != "$got_arm64" ]; then
    echo "$pin pins version $version with checksums that are not the ones of the download. Run: scripts/cli-checksums.sh $version" >&2
    exit 1
  fi
  echo "$pin is consistent for version $version"
  exit 0
fi

if [ -n "$expect_amd64" ] && [ "$expect_amd64" != "$got_amd64" ]; then echo "the published amd64 checksum differs from the download's: writing nothing" >&2; exit 1; fi
if [ -n "$expect_arm64" ] && [ "$expect_arm64" != "$got_arm64" ]; then echo "the published arm64 checksum differs from the download's: writing nothing" >&2; exit 1; fi

tmp=$work/pin.new
sed "s|^\( *version: *\)\"[^\"]*\"|\1\"$version\"|; \
     s|^\( *amd64: *{name: *\"[^\"]*\", *sha256: *\"\)[0-9a-f]*\(\".*\)|\1$got_amd64\2|; \
     s|^\( *arm64: *{name: *\"[^\"]*\", *sha256: *\"\)[0-9a-f]*\(\".*\)|\1$got_arm64\2|" "$pin" > "$tmp"
cat "$tmp" > "$pin"
echo "wrote version $version and both checksums to $pin"
```

(The script writes through `cat … > "$pin"` and not `mv`, so the file keeps its mode and any symlink.)

- [ ] **Step 4: Run the test**

Run: `sh scripts/cli-checksums_test.sh`
Expected: every line `ok`, then `all passed`. If the "arm64 checksum" test fails while the amd64 one passes, check that the third `sed` expression's `arm64` pattern is not eaten by the second: both anchor on `^ *amd64:` and `^ *arm64:`.

- [ ] **Step 5: Put it in `make shell-test`, commit**

In `Makefile`, replace:

```make
	sh scripts/check-release_test.sh
```

with:

```make
	sh scripts/check-release_test.sh
	sh scripts/cli-checksums_test.sh
```

```bash
chmod +x scripts/cli-checksums.sh
git add scripts Makefile
git commit -m "feat: cli-checksums.sh, to write and to check the pinned CLI's version and checksums

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The workflows

**Files:**
- Create: `.github/workflows/images.yml`, `.github/workflows/cli-pin.yml`

**Interfaces:**
- Consumes: `Dockerfile`, `Dockerfile.runner` (K-1), `scripts/check-release.sh` (task 1), `scripts/cli-checksums.sh --check` (task 2).
- Produces: images `ghcr.io/jaydee94/remedy-server` and `ghcr.io/jaydee94/remedy-runner` tagged `sha-<short>`, `edge` (main) and `X.Y.Z` (tag), for `linux/amd64` and `linux/arm64`.

- [ ] **Step 1: Look up the latest major of each action**

Run:

```sh
for a in actions/checkout docker/setup-qemu-action docker/setup-buildx-action docker/login-action docker/metadata-action docker/build-push-action; do
  printf '%-34s %s\n' "$a" "$(gh api "repos/$a/releases/latest" -q .tag_name)"
done
```

Expected: one line per action with its latest release tag. In the two workflow files below, the `@v…` of each Docker action is the default this plan was written with (`setup-qemu-action@v3`, `setup-buildx-action@v3`, `login-action@v3`, `metadata-action@v5`, `build-push-action@v6`); replace each with the major version this command shows if it is newer, and keep `actions/checkout` at the major `ci.yml` already uses.

- [ ] **Step 2: The image workflow**

Create `.github/workflows/images.yml`:

```yaml
name: images

# Builds the control plane image and the runner image. A pull request that touches what goes into them builds for amd64
# and pushes nothing. A push to main pushes sha-<short> and edge; a tag vX.Y.Z pushes X.Y.Z, after checking that the Helm
# chart says the same version. No image contains the claude CLI.
on:
  push:
    branches: [main]
    tags: ['v*']
  pull_request:
    paths:
      - Dockerfile
      - Dockerfile.runner
      - .dockerignore
      - go.mod
      - go.sum
      - cmd/**
      - internal/**
      - web/**
      - .github/workflows/images.yml
  workflow_dispatch:

permissions:
  contents: read

jobs:
  images:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    strategy:
      fail-fast: false
      matrix:
        include:
          - {name: remedy-server, file: Dockerfile}
          - {name: remedy-runner, file: Dockerfile.runner}
    env:
      PUSH: ${{ github.event_name != 'pull_request' }}
    steps:
      - uses: actions/checkout@v7

      - name: A tag must match the Helm chart
        if: startsWith(github.ref, 'refs/tags/')
        run: sh scripts/check-release.sh "$GITHUB_REF_NAME"

      - uses: docker/setup-qemu-action@v3
        if: env.PUSH == 'true'
      - uses: docker/setup-buildx-action@v3

      - uses: docker/login-action@v3
        if: env.PUSH == 'true'
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - id: meta
        uses: docker/metadata-action@v5
        with:
          images: ghcr.io/jaydee94/${{ matrix.name }}
          tags: |
            type=sha,prefix=sha-,format=short
            type=edge,branch=main
            type=semver,pattern={{version}}

      - uses: docker/build-push-action@v6
        with:
          context: .
          file: ${{ matrix.file }}
          platforms: ${{ env.PUSH == 'true' && 'linux/amd64,linux/arm64' || 'linux/amd64' }}
          push: ${{ env.PUSH == 'true' }}
          tags: ${{ steps.meta.outputs.tags }}
          labels: ${{ steps.meta.outputs.labels }}
          cache-from: type=gha,scope=${{ matrix.name }}
          cache-to: type=gha,scope=${{ matrix.name }},mode=max
```

(`docker/metadata-action` also sets the label `org.opencontainers.image.source` from the repository, which is what links the package to the public repository.)

- [ ] **Step 3: The pin workflow**

Create `.github/workflows/cli-pin.yml`:

```yaml
name: cli-pin

# Renovate bumps the version in deploy/cli-pin.yaml but cannot compute the checksums of the new artifacts. This recomputes
# them and fails when they differ, so that a pin with a wrong checksum cannot merge. To fix a failing pull request:
#   scripts/cli-checksums.sh <the new version>   and push the result.
on:
  pull_request:
    paths:
      - deploy/cli-pin.yaml
      - scripts/cli-checksums.sh
  workflow_dispatch:

permissions:
  contents: read

jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - run: sh scripts/cli-checksums.sh --check
```

- [ ] **Step 4: Validate both files**

Run: `python3 -c "import sys,yaml; [yaml.safe_load(open(f)) for f in sys.argv[1:]]; print('yaml ok')" .github/workflows/images.yml .github/workflows/cli-pin.yml`
Expected: `yaml ok`. If `PyYAML` is missing, use `ruby -ryaml -e 'ARGV.each{|f| YAML.load_file(f)}; puts "yaml ok"' .github/workflows/*.yml` or an online linter, and if `actionlint` is installed run it over both files.

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/images.yml .github/workflows/cli-pin.yml
git commit -m "ci: build and push the two images for amd64 and arm64, and check the CLI pin's checksums

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

The workflows run for the first time on the pull request that carries this plan's commits (`images.yml` builds without pushing, as `Dockerfile` and `Dockerfile.runner` changed in K-1; `cli-pin.yml` runs if `deploy/cli-pin.yaml` is in the diff). Watch them: `gh pr checks`.

---

### Task 4: Renovate

**Files:**
- Overwrite: `renovate.json`

**Interfaces:**
- Produces: custom managers for the CLI's `version` in `deploy/cli-pin.yaml` and for `ARGOCD_VERSION` in `dev/kind/lib.sh`.

- [ ] **Step 1: The configuration**

Overwrite `renovate.json`:

```json
{
  "$schema": "https://docs.renovatebot.com/renovate-schema.json",
  "extends": [
    "config:recommended"
  ],
  "customDatasources": {
    "claude-cli": {
      "defaultRegistryUrlTemplate": "https://downloads.claude.ai/claude-code-releases/latest",
      "format": "plain"
    }
  },
  "customManagers": [
    {
      "customType": "regex",
      "description": "The pinned claude CLI. Renovate bumps the version only; the cli-pin workflow fails until scripts/cli-checksums.sh has written the checksums.",
      "managerFilePatterns": ["/^deploy/cli-pin\\.yaml$/"],
      "matchStrings": ["cli:\\n\\s+version: \"(?<currentValue>[0-9][0-9.]*)\""],
      "datasourceTemplate": "custom.claude-cli",
      "depNameTemplate": "claude-code-cli"
    },
    {
      "customType": "regex",
      "description": "The Argo CD version of the kind testbed.",
      "managerFilePatterns": ["/^dev/kind/lib\\.sh$/"],
      "matchStrings": ["ARGOCD_VERSION=(?<currentValue>v[0-9][0-9.]*)"],
      "datasourceTemplate": "github-releases",
      "depNameTemplate": "argoproj/argo-cd"
    }
  ],
  "packageRules": [
    {
      "description": "A new CLI version needs its checksums: the maintainer runs scripts/cli-checksums.sh. Do not merge it by itself.",
      "matchDepNames": ["claude-code-cli"],
      "automerge": false,
      "labels": ["cli-pin"]
    }
  ]
}
```

The version source is the one S4's record names: the plain-text file `latest` of the vendor's download host, read through Renovate's custom datasource with the format `plain` (one version per file). The npm package `@anthropic-ai/claude-code` is not used, because the record does not show that its versions equal the download host's. The `latest` channel can move to a version that has not been run through Remedy: the pull request is labelled `cli-pin`, is never merged by itself, and the maintainer reads the record's "Which version" checks (the flags of Remedy's invocation exist in `--help`) before merging. If Renovate rejects the `customDatasources` block or never opens a pull request, say so in `docs/research/k8s-first-release.md` (task 6) and fall back to the manual bump of the runbook; do not guess a second source.

- [ ] **Step 2: Check the JSON and the patterns**

Run: `python3 -c "import json; json.load(open('renovate.json')); print('json ok')"`
Expected: `json ok`. Then test the two regexes against the real files:

```sh
python3 - <<'EOF'
import re
pin = open('deploy/cli-pin.yaml').read()
lib = open('dev/kind/lib.sh').read()
m1 = re.search(r'cli:\n\s+version: "(?P<currentValue>[0-9][0-9.]*)"', pin)
m2 = re.search(r'ARGOCD_VERSION=(?P<currentValue>v[0-9][0-9.]*)', lib)
print("cli version:", m1.group('currentValue') if m1 else "NO MATCH")
print("argo version:", m2.group('currentValue') if m2 else "NO MATCH")
EOF
```

Expected: both lines show a version, no `NO MATCH`. If the CLI one does not match, `deploy/cli-pin.yaml` has a different layout between `cli:` and `version:` (a comment line, for example): change the `matchStrings` to allow it, for instance `cli:\\n(?:\\s+#[^\\n]*\\n)*\\s+version: …`.

- [ ] **Step 3: Commit**

```bash
git add renovate.json
git commit -m "chore(renovate): managers for the pinned CLI version and the Argo CD version of the testbed

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: The homelab runbook

**Files:**
- Create: `docs/runbook/homelab-deploy.md`
- Modify: `deploy/chart/README.md`, `README.md`

- [ ] **Step 1: Write the runbook**

Create `docs/runbook/homelab-deploy.md`. It has these sections, in this order, with the content given here. Commands and YAML are exact; the surrounding sentences are short.

**1. What this installs.** One paragraph: Remedy's control plane and runner in one namespace, from the Helm chart `deploy/chart` of a tag of this repository, managed by an Argo CD Application, on k3s (Traefik, local-path storage, kube-router for network policies), with a SealedSecret for the three application secrets and an Ingress for the UI, reachable on the LAN or VPN only.

**2. You need.** k3s with Argo CD and Sealed Secrets installed; `kubectl`, `kubeseal`, `openssl`; a GitOps repository Argo CD watches; DNS for the UI's host name; a TLS certificate (cert-manager or one you provide) for it; a Claude subscription for the one-time login; the namespaces in which Remedy may act must exist.

**3. The namespace.** Remedy runs in its own namespace, with the restricted Pod Security Standard (every pod of the chart passes it):

```sh
kubectl create namespace remedy-system
kubectl label namespace remedy-system pod-security.kubernetes.io/enforce=restricted
```

(With Argo CD's `managedNamespaceMetadata` below the label is set by Argo instead; do one of the two.)

**4. The secret.** Three values, generated once and kept: the master key seals the GitHub token, losing it loses that token.

```sh
kubectl -n remedy-system create secret generic remedy-secrets --dry-run=client -o yaml \
  --from-literal=admin-password="$(openssl rand -hex 12)" \
  --from-literal=runner-token="$(openssl rand -hex 24)" \
  --from-literal=master-key="$(openssl rand -base64 32)" \
  | kubeseal --format yaml > remedy-secrets.sealed.yaml
```

Commit `remedy-secrets.sealed.yaml` to the GitOps repository (it is safe there; the plain Secret is never written to disk). Note the admin password before it is gone: it is only in the cluster after this. `kubectl -n remedy-system get secret remedy-secrets -o jsonpath='{.data.admin-password}' | base64 -d` shows it once the controller has made the Secret.

**5. The values.** A file in the GitOps repository, `apps/remedy/values.yaml`:

```yaml
existingSecret:
  name: remedy-secrets

server:
  persistence:
    size: 1Gi          # local-path: the claim is bound to one node; a node loss loses the database (see the backup chapter)

runner:
  model: sonnet
  persistence:
    size: 1Gi

ingress:
  enabled: true
  className: traefik
  host: remedy.home.example     # your host name
  tls:
    secretName: remedy-tls      # a Secret with the certificate, or let cert-manager make it (annotations)
  annotations: {}

cluster:
  enabled: true
  write:
    enabled: true
    namespaces: [apps]          # where actions are allowed; they must exist; never remedy-system
  argoNamespace: argocd

networkPolicy:
  enabled: true
  public:
    from:
      - namespaceSelector:
          matchLabels:
            kubernetes.io/metadata.name: kube-system     # where k3s's Traefik runs
  apiServer:
    cidrs: ["192.168.1.10/32"]  # the address behind kubernetes.default.svc: kubectl get endpoints kubernetes
    port: 6443
```

The pinned CLI is not here: it comes from `deploy/cli-pin.yaml` of this repository (next section). Find the API server's address with `kubectl get endpoints kubernetes -o jsonpath='{.subsets[0].addresses[0].ip}'`; on a k3s cluster with several servers list each one.

**6. The Argo CD Application.** Two sources of this repository (the chart, and the pinned CLI as a values file through `ref`) and one of the GitOps repository:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: remedy
  namespace: argocd
spec:
  project: default
  sources:
    - repoURL: https://github.com/Jaydee94/remedy.git
      targetRevision: v0.1.0              # the release; bump it to upgrade
      path: deploy/chart
      helm:
        releaseName: remedy
        valueFiles:
          - $remedy/deploy/cli-pin.yaml
          - $gitops/apps/remedy/values.yaml
    - repoURL: https://github.com/Jaydee94/remedy.git
      targetRevision: v0.1.0
      ref: remedy
    - repoURL: https://github.com/you/gitops.git      # your GitOps repository
      targetRevision: main
      ref: gitops
  destination:
    server: https://kubernetes.default.svc
    namespace: remedy-system
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
    managedNamespaceMetadata:
      labels:
        pod-security.kubernetes.io/enforce: restricted
  ignoreDifferences:
    # The token refresher writes the data of this Secret; the chart has none. Without this Argo CD (selfHeal) would see
    # a difference and fight the refresher.
    - group: ""
      kind: Secret
      name: remedy-write-token
      jsonPointers:
        - /data
```

State what happens at the first sync: the Secret is created by the controller from the SealedSecret; the control plane and the runner start (the runner's init container downloads the pinned CLI and checks its checksum, so the cluster needs outbound HTTPS); the chart's hook runs as an Argo `PostSync` job and fills the write token; the first run of the CronJob follows within half an hour.

**7. The one-time login.**

```sh
kubectl -n remedy-system exec -it remedy-runner-0 -c runner -- /opt/claude/claude
```

Type `/login`, open the URL in a browser, paste the code, `/exit`. The login is on the runner's volume and survives restarts of the pod, but not the loss of the volume. (After plan K-6, Setup shows whether the runner is logged in.)

**8. First use.** Open the host name, sign in as `admin` with the password of step 4, Setup: connect GitHub (the token is write-only), add repositories. Ask Remedy with "Read the cluster" to see the cluster tools work; each change waits for your approval.

**9. Upgrading.** Bump `targetRevision` of both sources of the Application to the new tag. A new CLI version comes with a Renovate pull request on `deploy/cli-pin.yaml`: run `scripts/cli-checksums.sh <version>` on that branch with the checksums the vendor publishes (`B=https://downloads.claude.ai/claude-code-releases; curl -fsSL $B/<version>/manifest.json | jq -r '.platforms["linux-x64"].checksum, .platforms["linux-arm64"].checksum'` prints amd64 then arm64; pass them as `--expect amd64=… --expect arm64=…`; to trust the manifest itself, download `manifest.json.sig` next to it and check it with `gpg --verify` against the key at `https://downloads.claude.ai/keys/claude-code.asc`, fingerprint `31DD DE24 DDFA B679 F42D 7BD2 BAA9 29FF 1A7E CACE`), push, and merge when the `cli-pin` check is green. The control plane restarts with `Recreate`: a minute without UI, runs in flight end as lost, sessions end.

**10. The release process (for the maintainer).** Change `version` and `appVersion` of `deploy/chart/Chart.yaml` to `X.Y.Z` in a pull request and merge it; tag `vX.Y.Z` on the merge commit and push the tag; `images.yml` checks the tag against the chart and pushes `X.Y.Z`; after the first release make both packages public once in GitHub (profile → Packages → `remedy-server` and `remedy-runner` → Package settings → Change visibility → Public), otherwise the cluster cannot pull them.

**11. Backup.** The database is the only state (incidents, runs, the sealed GitHub token). A `local-path` volume lives on one node: copy it or snapshot it. With the control plane scaled to zero (`kubectl -n remedy-system scale deployment/remedy-server --replicas=0`) copy `remedy.db` and `remedy.db-wal` out of the volume (the path is on the node under the `local-path` directory; `kubectl get pv` shows it), then scale back to one. Longhorn or Velero snapshots work the same way; Litestream is a separate plan.

**12. Troubleshooting.** One line each: the runner pod stuck in `Init` (the CLI download or its checksum: `kubectl -n remedy-system logs remedy-runner-0 -c install-cli`); every run ends "Not logged in" (step 7); the UI loads but nothing live updates (Traefik buffering: the default Traefik does not buffer; with nginx set `nginx.ingress.kubernetes.io/proxy-buffering: "off"`); "the cluster cannot be reached with the read token" in the control plane log (the network policy: check `networkPolicy.apiServer.cidrs`); actions fail "the token file … is empty" (the refresher has not run: `kubectl -n remedy-system get cronjob,job` and the logs of the last job); Argo CD shows the Secret `remedy-write-token` as out of sync (the `ignoreDifferences` is missing); the Secure flag: the cookie is Secure when Traefik sends `X-Forwarded-Proto: https`, which it does when TLS ends there.

- [ ] **Step 2: Link it**

In `deploy/chart/README.md`, append at the end:

```markdown

## Installing it in a homelab

[`docs/runbook/homelab-deploy.md`](../../docs/runbook/homelab-deploy.md): the namespace, a SealedSecret, the values, an Argo CD
Application with two sources of this repository (the chart, and the pinned CLI in `deploy/cli-pin.yaml`), the one-time login and
upgrades.
```

In `README.md`, replace:

```markdown
`make images` builds both). It does not contain the `claude` CLI: the Helm chart's init
container installs the pinned CLI, and the login is done once with `kubectl exec`. Until
the chart exists (plan K-2) the runner runs on the host.
```

with:

```markdown
`make images` builds both; a tag `vX.Y.Z` publishes them to GHCR). It does not contain the
`claude` CLI: the Helm chart's init container installs the pinned CLI, and the login is done
once with `kubectl exec`. The chart is in [`deploy/chart`](deploy/chart/README.md); how to
install it on k3s under Argo CD is in [`docs/runbook/homelab-deploy.md`](docs/runbook/homelab-deploy.md).
```

- [ ] **Step 3: Check the example against the chart**

```sh
cat > "$TMPDIR/homelab-values.yaml" <<'EOF'
existingSecret: {name: remedy-secrets}
runner: {model: sonnet}
ingress: {enabled: true, className: traefik, host: remedy.home.example, tls: {secretName: remedy-tls}}
cluster: {enabled: true, write: {enabled: true, namespaces: [apps]}, argoNamespace: argocd}
networkPolicy: {enabled: true, apiServer: {cidrs: ["192.168.1.10/32"], port: 6443}}
EOF
helm template remedy deploy/chart -n remedy-system -f deploy/cli-pin.yaml -f "$TMPDIR/homelab-values.yaml" | kubeconform -strict -summary
rm -f "$TMPDIR/homelab-values.yaml"
```

Expected: `Invalid: 0, Errors: 0` (skip with a note if `kubeconform` is missing; `helm template` must succeed either way). The example in the runbook and this check use the same keys; if a key had to change here, change the runbook.

- [ ] **Step 4: Commit**

```bash
git add docs/runbook/homelab-deploy.md deploy/chart/README.md README.md
git commit -m "docs: the homelab runbook, from a SealedSecret to an Argo CD Application and the one-time login

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 6: The first release

**Files:**
- Modify: `CLAUDE.md`; `docs/research/k8s-first-release.md` (create)

This task changes things outside the repository. The agent stops before each of the two outward steps and asks the maintainer.

- [ ] **Step 1: Everything merged, the pull request green**

Run: `gh pr checks` on the pull request that carries plans K-1 to K-5's work, and `git log --oneline origin/main -1` after the merge.
Expected: `images` (build only), `chart`, `shell`, `go`, `web` and, if the pin changed, `cli-pin` are green; the pull request is merged to `main`.

- [ ] **Step 2: Ask, then tag**

Ask the maintainer: "May I push the tag `v0.1.0` on the merge commit? It publishes both images to GHCR." Only on a yes:

```sh
git switch main && git pull
sh scripts/check-release.sh v0.1.0
git tag v0.1.0
git push origin v0.1.0
gh run watch "$(gh run list --workflow images.yml --limit 1 --json databaseId -q '.[0].databaseId')"
```

Expected: `check-release.sh` prints `the tag v0.1.0 matches the chart`; both matrix jobs of `images` finish green; `gh api users/jaydee94/packages/container/remedy-server/versions -q '[.[].metadata.container.tags[]] | unique'` lists `0.1.0` and a `sha-…` tag, and no `latest` (same for `remedy-runner`; the pushed images also carry provenance attestations, which show up as untagged package versions, hence the filter on tags).

- [ ] **Step 3: Ask, then make the packages public**

The packages are private after their first push. Ask the maintainer to make both public in GitHub's UI (profile → Packages → `remedy-server` and `remedy-runner` → Package settings → Change visibility → Public); the agent cannot do that. Then check anonymously:

```sh
docker logout ghcr.io 2> /dev/null || true
docker pull ghcr.io/jaydee94/remedy-server:0.1.0
docker pull ghcr.io/jaydee94/remedy-runner:0.1.0
docker manifest inspect ghcr.io/jaydee94/remedy-runner:0.1.0 | grep -E '"architecture"' | grep -v unknown | sort -u
```

Expected: both pulls succeed without a login; the manifest lists `amd64` and `arm64` (the `unknown/unknown` entries of the provenance attestation are filtered out).

- [ ] **Step 4: The published images in the dummy**

Prove that the released images and the chart's defaults work, once, without building anything locally:

```sh
make dummy-down && make dummy-up    # builds local images as before
helm --kube-context kind-remedy-dev -n remedy-system upgrade remedy deploy/chart \
  -f deploy/cli-pin.yaml -f dev/kind/dummy-values.yaml \
  --set image.server.repository=ghcr.io/jaydee94/remedy-server --set image.server.tag=0.1.0 \
  --set image.runner.repository=ghcr.io/jaydee94/remedy-runner --set image.runner.tag=0.1.0 \
  --set "networkPolicy.apiServer.cidrs={$(kubectl --context kind-remedy-dev get endpoints kubernetes -o jsonpath='{.subsets[0].addresses[0].ip}')/32}" \
  --wait --timeout 10m
make dummy-smoke
```

Expected: the upgrade pulls `ghcr.io/jaydee94/remedy-server:0.1.0` and `…runner:0.1.0` from the registry, `make dummy-smoke` ends `all ok`.

- [ ] **Step 5: The record and the project's state**

Create `docs/research/k8s-first-release.md`: the date, the commit and the tag, the workflow run's URL, the tags and architectures of both images (the commands of steps 2 and 3 with their output), the anonymous pull, the dummy run of step 4 with its last lines, and what was done by hand (the visibility).

In `CLAUDE.md`, in the Kubernetes bullet of "Current state", append: ` Images are published to GHCR on a tag (`.github/workflows/images.yml`; `scripts/check-release.sh` ties the tag to the chart's version); the pinned CLI in `deploy/cli-pin.yaml` is bumped by Renovate and checked by `scripts/cli-checksums.sh --check` in CI; `docs/runbook/homelab-deploy.md` is the install on k3s under Argo CD.`

```bash
git add docs/research/k8s-first-release.md CLAUDE.md
git commit -m "docs: the first release, v0.1.0, and what it showed

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```
