# Release Automation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A merge to `main` with a `feat` or `fix` publishes the two images, the Helm chart as an OCI artifact, a git tag and a GitHub release whose notes are the changelog, with the version computed by semantic-release from conventional commits.

**Architecture:** One workflow `release.yml` (`plan` then `images` and `chart` then `publish`) so that artifacts exist before the tag; semantic-release configured in one JS file with three modes (`plan`, `local`, `full`); small shell scripts with tests (`set-chart-version.sh`, `check-pr-title.sh`, `package-chart.sh`, `push-chart.sh`); a scratch-repository test of the release configuration; `images.yml` reduced to the pull-request build.

**Tech Stack:** GitHub Actions, semantic-release (Node, pinned by a lockfile in `release/`), Helm OCI, POSIX `sh`, Docker (a local registry for the chart test).

**Spec:** [`docs/specs/2026-10-09-release-automation-design.md`](../specs/2026-10-09-release-automation-design.md). It supersedes section 8 of `docs/specs/2026-10-06-kubernetes-deployment-design.md` and plan K-5 task 6 for the release.

## Global Constraints

- Everything committed is English: docs, workflows, scripts, comments, commit messages.
- The release uses `GITHUB_TOKEN` only. Workflow permissions: top level `contents: read`; `plan` and `publish` `contents: write`; `images` and `chart` `contents: read` and `packages: write`. No PAT, no `issues: write`, no `pull-requests: write`.
- No action outside `actions/*` and `docker/*`. Helm is the one preinstalled on the runner (its version is printed).
- Untrusted text (commit messages, a pull request title) is never interpolated into a shell line: the title goes through `env:`, the version is validated as `X.Y.Z` before use.
- `npm ci --ignore-scripts` everywhere; the lockfile `release/package-lock.json` is committed; no `package.json` in the repository root.
- The version rule is 0.x: `feat` is minor, `fix` and `perf` are patch, a breaking change is **minor**, `revert` is patch, nothing else releases.
- `Chart.yaml` keeps `version` equal to `appVersion`, both `X.Y.Z` (the latter quoted).
- Shell runs on macOS and on Linux: no BSD-only flags (`sed -i ''`, `stat -f`, `date -j`), no `local`.
- `make check` passes at the end of every task. Every commit message ends with the trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.

## Review Focus

- A merge that only has `docs`, `chore` or `ci` commits, or a title that is not conventional (like `Kubernetes deployment: ...`): no release, and no failure (task 2 tests, task 1 title check).
- A breaking change on 0.x must give the next **minor**, never `1.0.0`; a `BREAKING CHANGE` footer on a `fix` too (task 2).
- A failure between the artifacts and the tag: no release without images and chart; a re-run recomputes the same version (task 4).
- The bump commit `chore(release): X.Y.Z [skip ci]` must not start a second release; a push from the bot is not a release (task 2 and 4).
- Permissions and untrusted text: the workflow files against the constraints above (task 4).

## File Structure

| Path | Responsibility |
|---|---|
| `scripts/set-chart-version.sh`, `scripts/set-chart-version_test.sh` | Write `version` and `appVersion` of `Chart.yaml` |
| `scripts/check-pr-title.sh`, `scripts/check-pr-title_test.sh` | A pull request title must be a conventional commit |
| `release/package.json`, `release/package-lock.json`, `release/.nvmrc`, `release/release.config.js` | semantic-release and its configuration |
| `release/test/dry-run.sh` | The configuration tested in a scratch git repository |
| `scripts/package-chart.sh`, `scripts/push-chart.sh`, `scripts/chart-oci_test.sh` | Package and push the chart; tested against a local registry |
| `.github/workflows/release.yml`, `pr-title.yml`, `images.yml`, `ci.yml` | The pipeline, the title check, the pull-request image build, the CI jobs |
| `Makefile`, `.gitignore` | `shell-test` and `release-test` targets; ignored files |
| `docs/…`, `README.md`, `CLAUDE.md`, `renovate.json` (unchanged) | The documents of spec section 9 |

---

### Task 1: The two small scripts

**Files:**
- Create: `scripts/set-chart-version.sh`, `scripts/set-chart-version_test.sh`, `scripts/check-pr-title.sh`, `scripts/check-pr-title_test.sh`
- Modify: `Makefile`, `.github/workflows/ci.yml`

**Interfaces:**
- Produces: `scripts/set-chart-version.sh X.Y.Z [chart file]` rewrites exactly the lines `version:` and `appVersion:` (exit 1 for a version that is not `X.Y.Z` or a chart with other than one of each); `scripts/check-pr-title.sh [title]` (default: `$PR_TITLE`) exits 0 for a conventional title, 1 otherwise.

- [ ] **Step 1: The failing test of `set-chart-version.sh`**

Create `scripts/set-chart-version_test.sh`:

```sh
#!/bin/sh
# Tests of set-chart-version.sh. Run: sh scripts/set-chart-version_test.sh
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
SCRIPT=$HERE/set-chart-version.sh
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
fails=0

eq()  { if [ "$2" = "$3" ]; then echo "ok    $1"; else echo "FAIL  $1"; echo "  want: $2"; echo "  got:  $3"; fails=$((fails + 1)); fi; }
ok()  { n=$1; shift; if "$@" > "$WORK/out" 2>&1; then echo "ok    $n"; else echo "FAIL  $n (should pass)"; cat "$WORK/out"; fails=$((fails + 1)); fi; }
bad() { n=$1; shift; if "$@" > "$WORK/out" 2>&1; then echo "FAIL  $n (should fail)"; fails=$((fails + 1)); else echo "ok    $n"; fi; }

chart() { # chart <version line value> <appVersion line value>
  cat > "$WORK/Chart.yaml" <<EOF
# a comment that must survive
apiVersion: v2
name: remedy
description: A sentence that mentions version: 1.0 and appVersion: x inside, which must stay as it is.
type: application
version: $1
appVersion: "$2"
sources:
  - https://example.invalid/remedy
EOF
}

chart 0.1.0 0.1.0
ok  "a good version is written" sh "$SCRIPT" 1.2.3 "$WORK/Chart.yaml"
eq  "version line"    "version: 1.2.3"        "$(grep '^version:' "$WORK/Chart.yaml")"
eq  "appVersion line" 'appVersion: "1.2.3"'   "$(grep '^appVersion:' "$WORK/Chart.yaml")"
eq  "the comment survived" "# a comment that must survive" "$(head -1 "$WORK/Chart.yaml")"
eq  "the description survived" "description: A sentence that mentions version: 1.0 and appVersion: x inside, which must stay as it is." "$(grep '^description:' "$WORK/Chart.yaml")"
eq  "the sources survived" "  - https://example.invalid/remedy" "$(tail -1 "$WORK/Chart.yaml")"
cp "$WORK/Chart.yaml" "$WORK/before.yaml"
ok  "the same version again" sh "$SCRIPT" 1.2.3 "$WORK/Chart.yaml"
eq  "and the file is unchanged" "$(cat "$WORK/before.yaml")" "$(cat "$WORK/Chart.yaml")"
ok  "a two-digit part" sh "$SCRIPT" 0.10.20 "$WORK/Chart.yaml"
eq  "written" "version: 0.10.20" "$(grep '^version:' "$WORK/Chart.yaml")"

chart 0.1.0 0.1.0
cp "$WORK/Chart.yaml" "$WORK/before.yaml"
for v in v1.2.3 1.2 1.2.3.4 01.2.3 1.2.3-rc1 1.2.3+meta "1.2.3/../x" "1.2.3&" "" " 1.2.3" "1.2.3 "; do
  bad "refuses '$v'" sh "$SCRIPT" "$v" "$WORK/Chart.yaml"
done
eq  "and the chart is untouched" "$(cat "$WORK/before.yaml")" "$(cat "$WORK/Chart.yaml")"

bad "a missing file" sh "$SCRIPT" 1.2.3 "$WORK/nope.yaml"
printf 'apiVersion: v2\nversion: 0.1.0\nversion: 0.2.0\nappVersion: "0.1.0"\n' > "$WORK/two.yaml"
bad "two version lines" sh "$SCRIPT" 1.2.3 "$WORK/two.yaml"
printf 'apiVersion: v2\nversion: 0.1.0\n' > "$WORK/noapp.yaml"
bad "no appVersion line" sh "$SCRIPT" 1.2.3 "$WORK/noapp.yaml"
bad "no argument" sh "$SCRIPT"

[ "$fails" -eq 0 ] && echo "all passed" || { echo "$fails failed" >&2; exit 1; }
```

- [ ] **Step 2: Run it to see it fail**

Run: `sh scripts/set-chart-version_test.sh 2>&1 | tail -5`
Expected: FAIL lines (the script does not exist).

- [ ] **Step 3: The script**

Create `scripts/set-chart-version.sh`:

```sh
#!/bin/sh
# Writes the release version into the Helm chart: `version` and `appVersion` of Chart.yaml, nothing else. Called by
# semantic-release (release/release.config.js, the exec plugin's prepare step) with the version it computed.
# Usage: scripts/set-chart-version.sh X.Y.Z [chart file]
set -eu
version=${1:-}
chart=${2:-deploy/chart/Chart.yaml}

printf '%s' "$version" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' ||
  { echo "'$version' is not a version of the form X.Y.Z" >&2; exit 1; }
[ -f "$chart" ] || { echo "$chart does not exist" >&2; exit 1; }

n_version=$(grep -c '^version:' "$chart" || true)
n_app=$(grep -c '^appVersion:' "$chart" || true)
if [ "$n_version" != 1 ] || [ "$n_app" != 1 ]; then
  echo "$chart must have exactly one 'version:' line and one 'appVersion:' line (found $n_version and $n_app)" >&2
  exit 1
fi

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
sed -e "s/^version:.*/version: $version/" -e "s/^appVersion:.*/appVersion: \"$version\"/" "$chart" > "$tmp"
# cat, not mv: the file keeps its mode and a symlink.
cat "$tmp" > "$chart"
echo "set version and appVersion of $chart to $version"
```

- [ ] **Step 4: Run the test**

Run: `sh scripts/set-chart-version_test.sh`
Expected: every line `ok`, then `all passed`.

- [ ] **Step 5: The failing test of `check-pr-title.sh`**

Create `scripts/check-pr-title_test.sh`:

```sh
#!/bin/sh
# Tests of check-pr-title.sh. Run: sh scripts/check-pr-title_test.sh
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
SCRIPT=$HERE/check-pr-title.sh
fails=0

ok()  { if sh "$SCRIPT" "$2" > /dev/null 2>&1; then echo "ok    $1"; else echo "FAIL  $1 (should pass)"; fails=$((fails + 1)); fi; }
bad() { if sh "$SCRIPT" "$2" > /dev/null 2>&1; then echo "FAIL  $1 (should fail)"; fails=$((fails + 1)); else echo "ok    $1"; fi; }

ok  "feat"                         "feat: add the release pipeline"
ok  "feat with a scope"            "feat(web): announce a failed diagnosis (ui-10)"
ok  "fix"                          "fix: do not restart the server twice"
ok  "breaking"                     "feat!: drop the old runner route"
ok  "scope and breaking"           "fix(server)!: refuse a bad token"
ok  "docs"                         "docs: README for the release"
ok  "chore"                        "chore(release): 1.0.0 [skip ci]"
ok  "ci"                           "ci: build both images on a pull request"
ok  "perf"                         "perf: read the claim once"
ok  "revert"                       "revert: feat: add the release pipeline"
ok  "a scope with dots and slashes" "fix(deploy/chart.v2): a value"

bad "the title of pull request 130" "Kubernetes deployment: Helm chart, dummy setup with the real agent"
bad "no type"                      "add the release pipeline"
bad "no colon"                     "feat add the pipeline"
bad "no space after the colon"     "feat:add the pipeline"
bad "an unknown type"              "feature: add the pipeline"
bad "upper case type"              "Feat: add the pipeline"
bad "an empty subject"             "feat: "
bad "an empty title"               ""
bad "an empty scope"               "feat(): x"
bad "a scope with a space"         "feat(my scope): x"
bad "two lines"                    "feat: one
second line"
bad "a title that is too long"     "feat: $(printf 'x%.0s' $(seq 1 130))"

# The title can also come from the environment (the workflow passes it there).
if PR_TITLE="fix: from the environment" sh "$SCRIPT" > /dev/null 2>&1; then echo "ok    PR_TITLE"; else echo "FAIL  PR_TITLE"; fails=$((fails + 1)); fi
if PR_TITLE="nonsense" sh "$SCRIPT" > /dev/null 2>&1; then echo "FAIL  PR_TITLE nonsense"; fails=$((fails + 1)); else echo "ok    PR_TITLE nonsense"; fi

[ "$fails" -eq 0 ] && echo "all passed" || { echo "$fails failed" >&2; exit 1; }
```

(`seq` exists on macOS and Linux.)

- [ ] **Step 6: Run it to see it fail, then write the script**

Run: `sh scripts/check-pr-title_test.sh 2>&1 | tail -4` — Expected: FAIL lines.

Create `scripts/check-pr-title.sh`:

```sh
#!/bin/sh
# A pull request title must be a conventional commit: `type(scope)?!?: subject`. The squash merge uses the title as the
# commit subject, and semantic-release decides the release from it (feat is minor, fix and perf are patch, the rest
# releases nothing). The title is untrusted text: it is only matched, never executed or interpolated.
# Usage: scripts/check-pr-title.sh "<title>"     or     PR_TITLE="<title>" scripts/check-pr-title.sh
set -eu
title=${1:-${PR_TITLE:-}}

fail() {
  echo "the title is not a conventional commit: type(scope)?!?: subject" >&2
  echo "types: feat, fix, perf, revert, docs, chore, ci, test, refactor, style, build" >&2
  echo "example: feat(web): announce a failed diagnosis" >&2
  exit 1
}

case $title in
  *'
'*) fail ;;
esac
[ "${#title}" -le 120 ] || fail
printf '%s' "$title" | grep -Eq '^(feat|fix|perf|revert|docs|chore|ci|test|refactor|style|build)(\([a-z0-9._/-]+\))?!?: [^ ]' || fail
echo "the title is a conventional commit"
```

- [ ] **Step 7: Run both tests, put them in `make shell-test`, add the CI assertion**

Run: `sh scripts/check-pr-title_test.sh && sh scripts/set-chart-version_test.sh` — Expected: `all passed` twice.

In `Makefile`, replace (the line occurs once):

```make
	sh scripts/cli-checksums_test.sh
```

with:

```make
	sh scripts/cli-checksums_test.sh
	sh scripts/set-chart-version_test.sh
	sh scripts/check-pr-title_test.sh
```

In `.github/workflows/ci.yml`, in the `chart` job, after its existing steps, add a step (look at the job first and keep its indentation):

```yaml
      - name: Chart version equals appVersion
        run: sh scripts/check-release.sh "v$(sed -n 's/^version: *//p' deploy/chart/Chart.yaml)"
```

Run: `sh scripts/check-release.sh "v$(sed -n 's/^version: *//p' deploy/chart/Chart.yaml)"` — Expected: `the tag v0.1.0 matches the chart`. Run `make check`; Expected: PASS.

- [ ] **Step 8: Commit**

```bash
chmod +x scripts/set-chart-version.sh scripts/check-pr-title.sh
git add scripts Makefile .github/workflows/ci.yml
git commit -m "feat: scripts that set the chart version and check a pull request title

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: The semantic-release configuration and its scratch-repository test

**Files:**
- Create: `release/package.json`, `release/package-lock.json`, `release/.nvmrc`, `release/release.config.js`, `release/test/dry-run.sh`
- Modify: `Makefile`, `.gitignore`, `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: `scripts/set-chart-version.sh` (task 1).
- Produces: `RELEASE_MODE=plan|local|full semantic-release --extends ./release/release.config.js` (`plan`: writes the next version to `.release-version`); `make release-test`.

- [ ] **Step 1: The package and the lockfile**

Create `release/.nvmrc` containing `22`. Create `release/package.json`:

```json
{
  "name": "remedy-release",
  "version": "0.0.0",
  "private": true,
  "description": "The release tooling of Remedy (semantic-release). Not part of the product.",
  "engines": { "node": ">=22" }
}
```

Run, in `release/`: `npm install --save-exact --ignore-scripts semantic-release @semantic-release/changelog @semantic-release/exec @semantic-release/git @semantic-release/github conventional-changelog-conventionalcommits`. Look at what was installed (`npm ls --depth=0`) and at the peer requirements: if a plugin needs another major of semantic-release, choose the set that installs without `--force`. Commit `package.json` and `package-lock.json`. Add to `.gitignore`: `release/node_modules/`, `.release-version`, `dist/`.

- [ ] **Step 2: The failing scratch test**

Create `release/test/dry-run.sh`:

```sh
#!/bin/sh
# Tests the release configuration in a scratch git repository with a local bare remote and crafted commits: the version
# rules (0.x), what releases nothing, the notes, and what the real (local) run writes. Run from anywhere:
#   cd release && npm ci --ignore-scripts && cd .. && sh release/test/dry-run.sh
set -eu
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
SR=$ROOT/release/node_modules/.bin/semantic-release
CONFIG=$ROOT/release/release.config.js
[ -x "$SR" ] || { echo "run npm ci in release/ first" >&2; exit 1; }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
REMOTE=$WORK/remote.git
REPO=$WORK/repo
git init -q --bare "$REMOTE"
git init -q -b main "$REPO"
cd "$REPO"
git config user.email release-test@example.invalid
git config user.name release-test
git remote add origin "$REMOTE"

mkdir -p scripts deploy/chart
cp "$ROOT/scripts/set-chart-version.sh" scripts/
cp "$ROOT/deploy/chart/Chart.yaml" deploy/chart/Chart.yaml
sh scripts/set-chart-version.sh 0.0.0 > /dev/null
git add -A
git commit -q -m "chore: init"
git tag v0.0.0
git push -q origin main --tags

fails=0
n=0
say() { echo "ok    $1"; }
fail() { echo "FAIL  $1"; fails=$((fails + 1)); }
commit() { # commit <subject> [<body>]
  n=$((n + 1))
  echo "$n" >> work.txt
  git add work.txt
  if [ $# -gt 1 ]; then git commit -q -m "$1" -m "$2"; else git commit -q -m "$1"; fi
  git push -q origin main
}
sr() { # sr <mode> [extra args]: runs semantic-release in the scratch repository, output in $WORK/sr.log
  mode=$1; shift
  RELEASE_MODE=$mode "$SR" --no-ci --repository-url "$REMOTE" --extends "$CONFIG" "$@" > "$WORK/sr.log" 2>&1 || { cat "$WORK/sr.log"; echo "semantic-release failed ($mode)" >&2; exit 1; }
}
planned() { # planned: the version the plan mode computes, or empty
  rm -f .release-version
  sr plan --dry-run
  if [ -f .release-version ]; then cat .release-version; fi
}
expect() { # expect <name> <version or empty>
  got=$(planned)
  if [ "$got" = "$2" ]; then say "$1 -> '${2:-no release}'"; else fail "$1: want '$2', got '$got'"; fi
}
release() { sr local; git pull -q --ff-only origin main; }

expect "nothing since the tag" ""
commit "feat: the first feature"
expect "a feature on 0.0.0 gives 0.1.0" "0.1.0"
release
grep -q '^version: 0.1.0$' deploy/chart/Chart.yaml && say "Chart.yaml version written" || fail "Chart.yaml version not written"
grep -q '^appVersion: "0.1.0"$' deploy/chart/Chart.yaml && say "Chart.yaml appVersion written" || fail "Chart.yaml appVersion not written"
grep -q '^## ' CHANGELOG.md && grep -q '### Features' CHANGELOG.md && grep -q 'the first feature' CHANGELOG.md && say "CHANGELOG.md has the Features section" || fail "CHANGELOG.md lacks the Features section"
[ "$(git --git-dir="$REMOTE" tag | grep -cx v0.1.0)" = 1 ] && say "the tag v0.1.0 is on the remote" || fail "no tag v0.1.0 on the remote"
git log -1 --format=%s | grep -qx 'chore(release): 0.1.0 \[skip ci\]' && say "the bump commit message" || fail "the bump commit message is $(git log -1 --format=%s)"
expect "right after the release" ""

commit "fix: a fix"
expect "a fix gives a patch" "0.1.1"
release
grep -q '### Bug Fixes' CHANGELOG.md && say "CHANGELOG.md has the Bug Fixes section" || fail "no Bug Fixes section"

commit "docs: only documents"
commit "chore: only chores"
commit "ci: only ci"
commit "Kubernetes deployment: a title that is not conventional"
expect "docs, chore, ci and a non-conventional title release nothing" ""

commit "feat!: a breaking feature"
expect "a breaking change on 0.x gives the next minor, not 1.0.0" "0.2.0"
release

commit "fix: a fix with a breaking footer" "BREAKING CHANGE: the old route is gone"
expect "a BREAKING CHANGE footer on a fix gives the next minor" "0.3.0"
release

commit "perf: faster"
expect "perf gives a patch" "0.3.1"
commit "fix: and a feature after it" 
commit "feat: with a scope inside"
expect "the highest wins: a feature after a fix and a perf" "0.4.0"
release

if grep -q 'only documents' CHANGELOG.md || grep -q 'only chores' CHANGELOG.md || grep -q 'not conventional' CHANGELOG.md; then fail "hidden types or a non-conventional title are in the changelog"; else say "docs, chore, ci and the odd title are not in the changelog"; fi
grep -q 'BREAKING CHANGES' CHANGELOG.md && say "the breaking change note is in the changelog" || fail "no BREAKING CHANGES note in the changelog"

[ "$fails" -eq 0 ] && echo "all passed" || { echo "$fails failed" >&2; exit 1; }
```

- [ ] **Step 3: Run it to see it fail**

Run: `sh release/test/dry-run.sh 2>&1 | tail -6`
Expected: a failure (the configuration does not exist, so semantic-release reports no plugins or fails).

- [ ] **Step 4: The configuration**

Create `release/release.config.js`:

```js
// The semantic-release configuration of Remedy, one file with three modes chosen by RELEASE_MODE:
//   plan  analyzer, notes generator and exec: says the next version (writes it to .release-version), changes nothing
//   local plan + changelog, the chart version (exec prepare) and git: a real release into a local remote; used by
//         release/test/dry-run.sh, which has no GitHub
//   full  local + the GitHub release with the chart attached; used by .github/workflows/release.yml (publish)
// The rules are those of 0.x (docs/specs/2026-10-09-release-automation-design.md R3): feat is minor, fix and perf are
// patch, a breaking change is minor too (1.0.0 is the maintainer's decision), revert is patch, nothing else releases.
const mode = process.env.RELEASE_MODE || 'full'
if (!['plan', 'local', 'full'].includes(mode)) {
  throw new Error(`RELEASE_MODE must be plan, local or full, got "${mode}"`)
}

const preset = 'conventionalcommits'

const analyzer = [
  '@semantic-release/commit-analyzer',
  {
    preset,
    releaseRules: [
      { breaking: true, release: 'minor' },
      { revert: true, release: 'patch' },
      { type: 'feat', release: 'minor' },
      { type: 'fix', release: 'patch' },
      { type: 'perf', release: 'patch' },
    ],
  },
]

const hiddenTypes = ['docs', 'chore', 'ci', 'test', 'refactor', 'style', 'build']

const notes = [
  '@semantic-release/release-notes-generator',
  {
    preset,
    presetConfig: {
      types: [
        { type: 'feat', section: 'Features' },
        { type: 'fix', section: 'Bug Fixes' },
        { type: 'perf', section: 'Performance' },
        { type: 'revert', section: 'Reverts' },
        ...hiddenTypes.map((type) => ({ type, hidden: true })),
      ],
    },
  },
]

const changelog = ['@semantic-release/changelog', { changelogFile: 'CHANGELOG.md', changelogTitle: '# Changelog' }]

// ${nextRelease.version} is filled in by the exec plugin (lodash template), not by the shell.
const execPlan = ['@semantic-release/exec', { verifyReleaseCmd: 'printf %s "${nextRelease.version}" > .release-version' }]
const execPrepare = ['@semantic-release/exec', { prepareCmd: 'sh scripts/set-chart-version.sh ${nextRelease.version}' }]

const git = [
  '@semantic-release/git',
  {
    assets: ['CHANGELOG.md', 'deploy/chart/Chart.yaml'],
    message: 'chore(release): ${nextRelease.version} [skip ci]\n\n${nextRelease.notes}',
  },
]

// No comments on pull requests or issues, no labels, no failure issue: the job then needs only contents: write.
const github = [
  '@semantic-release/github',
  {
    assets: [{ path: 'dist/remedy-*.tgz', label: 'Helm chart (also oci://ghcr.io/jaydee94/charts/remedy)' }],
    successComment: false,
    failComment: false,
    releasedLabels: false,
  },
]

const plugins =
  mode === 'plan' ? [analyzer, notes, execPlan] : [analyzer, notes, changelog, execPrepare, git, ...(mode === 'full' ? [github] : [])]

module.exports = { branches: ['main'], tagFormat: 'v${version}', plugins }
```

- [ ] **Step 5: Run the scratch test, and fix the configuration where the tool behaves otherwise**

Run: `sh release/test/dry-run.sh`
Expected: every line `ok`, then `all passed`. This test is how the configuration is proven, so if semantic-release behaves differently from what the comments above say (for example the `--extends` of an absolute path, the `verifyReleaseCmd` in a dry run, the `breaking` rule order, the `conventionalcommits` preset's handling of `feat!:`), change the configuration (never the expectation of the 0.x rules) and say in the report what you found. If `--extends` of a file does not work, look at how semantic-release loads a shareable config (`extends` resolves like `require`) and use `--extends` with a path that does, or a one-line `release.config.js` in the repository root that requires `./release/release.config.js` (then document it in the spec's section 5).

- [ ] **Step 6: `make release-test` and the CI job**

In `Makefile`, add to the `.PHONY` list `release-test`, and after the `shell-test` target:

```make
release-test: ## Test the release configuration in a scratch repository and the chart's OCI push (needs node, git, helm, docker)
	cd release && npm ci --ignore-scripts
	sh release/test/dry-run.sh
	sh scripts/chart-oci_test.sh
```

(`scripts/chart-oci_test.sh` comes in task 3: until then the target fails at its last line; run the first two lines by hand in this task.) In `.github/workflows/ci.yml`, append a job, using the same `actions/checkout` and `actions/setup-node` majors that `ci.yml` already uses:

```yaml

  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-node@v7
        with:
          node-version-file: release/.nvmrc
          cache: npm
          cache-dependency-path: release/package-lock.json
      - run: make release-test
```

- [ ] **Step 7: Commit**

```bash
git add release Makefile .gitignore .github/workflows/ci.yml
git commit -m "feat: semantic-release configuration with a 0.x version rule, tested in a scratch repository

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Package and push the chart

**Files:**
- Create: `scripts/package-chart.sh`, `scripts/push-chart.sh`, `scripts/chart-oci_test.sh`
- Modify: `deploy/chart/Chart.yaml` (only if the test says `sources` is missing), `Makefile`

**Interfaces:**
- Produces: `scripts/package-chart.sh X.Y.Z [dest]` prints the path of `remedy-X.Y.Z.tgz`; `scripts/push-chart.sh <tgz> <oci base> [helm push args]`.

- [ ] **Step 1: The failing test against a local registry**

Create `scripts/chart-oci_test.sh`:

```sh
#!/bin/sh
# Packages the chart with a version it does not have in Chart.yaml, pushes it to a local OCI registry and pulls it back.
# Needs helm and docker (registry:2); without docker it says so and exits 0 (CI has it). Run: sh scripts/chart-oci_test.sh
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/.." && pwd)
command -v helm > /dev/null || { echo "helm is needed" >&2; exit 1; }
command -v docker > /dev/null && docker info > /dev/null 2>&1 || { echo "skip: docker is not available (the chart push test needs registry:2)"; exit 0; }

WORK=$(mktemp -d)
NAME=remedy-chart-oci-test-$$
trap 'docker rm -f "$NAME" > /dev/null 2>&1; rm -rf "$WORK"' EXIT
fails=0
eq() { if [ "$2" = "$3" ]; then echo "ok    $1"; else echo "FAIL  $1"; echo "  want: $2"; echo "  got:  $3"; fails=$((fails + 1)); fi; }
has() { if printf '%s' "$3" | grep -q "$2"; then echo "ok    $1"; else echo "FAIL  $1 (no '$2' in the output)"; fails=$((fails + 1)); fi; }

cd "$ROOT"
chart_version_before=$(sed -n 's/^version: *//p' deploy/chart/Chart.yaml)

tgz=$(sh scripts/package-chart.sh 9.9.9 "$WORK/dist") || { echo "package-chart failed" >&2; exit 1; }
eq "the package name" "$WORK/dist/remedy-9.9.9.tgz" "$tgz"
eq "Chart.yaml in the repository is untouched" "$chart_version_before" "$(sed -n 's/^version: *//p' deploy/chart/Chart.yaml)"
eq "version inside the package" "version: 9.9.9" "$(tar -xzOf "$tgz" remedy/Chart.yaml | grep '^version:')"
eq "appVersion inside the package" 'appVersion: "9.9.9"' "$(tar -xzOf "$tgz" remedy/Chart.yaml | grep '^appVersion:')"

if sh scripts/package-chart.sh 1.2 "$WORK/dist2" > /dev/null 2>&1; then echo "FAIL  a bad version must be refused"; fails=$((fails + 1)); else echo "ok    a bad version is refused"; fi

PORT=$((20000 + $$ % 20000))
docker run -d --rm --name "$NAME" -p "127.0.0.1:$PORT:5000" registry:2 > /dev/null || { echo "could not start registry:2" >&2; exit 1; }
i=0
until curl -sf "http://127.0.0.1:$PORT/v2/" > /dev/null; do i=$((i + 1)); [ "$i" -lt 30 ] || { echo "the registry did not start" >&2; exit 1; }; sleep 1; done

out=$(sh scripts/push-chart.sh "$tgz" "oci://127.0.0.1:$PORT/charts" --plain-http 2>&1) || { echo "$out"; echo "push failed" >&2; exit 1; }
has "the push says it pushed" "Pushed" "$out"

helm pull "oci://127.0.0.1:$PORT/charts/remedy" --version 9.9.9 --plain-http --destination "$WORK/pulled" > /dev/null 2>&1 || { echo "pull failed" >&2; exit 1; }
eq "the pulled chart is the pushed one" "version: 9.9.9" "$(tar -xzOf "$WORK/pulled/remedy-9.9.9.tgz" remedy/Chart.yaml | grep '^version:')"

# The manifest annotation that ties a GHCR package to the repository (org.opencontainers.image.source).
manifest=$(curl -sf -H 'Accept: application/vnd.oci.image.manifest.v1+json' "http://127.0.0.1:$PORT/v2/charts/remedy/manifests/9.9.9")
has "the manifest names the source repository" "org.opencontainers.image.source" "$manifest"

[ "$fails" -eq 0 ] && echo "all passed" || { echo "$fails failed" >&2; exit 1; }
```

- [ ] **Step 2: Run it to see it fail**

Run: `sh scripts/chart-oci_test.sh 2>&1 | tail -4` — Expected: it stops because `scripts/package-chart.sh` does not exist (or, without docker, `skip: docker is not available`: then start Docker Desktop first).

- [ ] **Step 3: The two scripts**

Create `scripts/package-chart.sh`:

```sh
#!/bin/sh
# Packages the Helm chart with the release version (which Chart.yaml does not need to have yet) and prints the path of the
# package. The version is validated as X.Y.Z: it comes from the release tool, but it ends up in a file name.
# Usage: scripts/package-chart.sh X.Y.Z [destination directory, default dist]
set -eu
version=${1:-}
dest=${2:-dist}
printf '%s' "$version" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' ||
  { echo "'$version' is not a version of the form X.Y.Z" >&2; exit 1; }
command -v helm > /dev/null || { echo "helm is needed" >&2; exit 1; }
mkdir -p "$dest"
helm lint deploy/chart -f deploy/chart/ci/lint-values.yaml > /dev/null
helm package deploy/chart --version "$version" --app-version "$version" --destination "$dest" > /dev/null
echo "$dest/remedy-$version.tgz"
```

Create `scripts/push-chart.sh`:

```sh
#!/bin/sh
# Pushes a packaged chart to an OCI registry. Extra arguments go to `helm push` (for example --plain-http for a local registry).
# Usage: scripts/push-chart.sh <package.tgz> <oci base, e.g. oci://ghcr.io/jaydee94/charts> [helm push arguments]
set -eu
tgz=${1:?usage: push-chart.sh <package.tgz> <oci base> [helm push arguments]}
base=${2:?usage: push-chart.sh <package.tgz> <oci base> [helm push arguments]}
shift 2
[ -f "$tgz" ] || { echo "$tgz does not exist" >&2; exit 1; }
case $base in
  oci://*) ;;
  *) echo "the target must be an oci:// URL, got '$base'" >&2; exit 1 ;;
esac
helm push "$tgz" "$base" "$@"
```

- [ ] **Step 4: Run the test**

Run: `sh scripts/chart-oci_test.sh`
Expected: `all passed`. If the last check fails (`org.opencontainers.image.source` absent), Helm takes that annotation from the chart's `sources`: add to `deploy/chart/Chart.yaml`, after `appVersion`, `sources:` with the line `  - https://github.com/Jaydee94/remedy` (and `home: https://github.com/Jaydee94/remedy`), run the test again, and run `go test ./deploy -count=1` and `make chart-check` (a chart test may pin the file). If it still fails, record what the manifest holds in the task report (the annotations you do get) and stop for the controller: the package would not be tied to the repository on GHCR.

- [ ] **Step 5: `make check`, commit**

Run `make check` (Expected: PASS) and `make release-test` (Expected: PASS, with Docker running).

```bash
chmod +x scripts/package-chart.sh scripts/push-chart.sh
git add scripts deploy/chart/Chart.yaml Makefile
git commit -m "feat: scripts to package and push the Helm chart as an OCI artifact, tested against a local registry

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: The workflows

**Files:**
- Create: `.github/workflows/release.yml`, `.github/workflows/pr-title.yml`
- Modify: `.github/workflows/images.yml`

**Interfaces:**
- Consumes: the scripts and the configuration of tasks 1 to 3.
- Produces: the pipeline of spec section 4.

- [ ] **Step 1: Look up the action majors**

Run:

```sh
for a in actions/checkout actions/setup-node actions/upload-artifact actions/download-artifact docker/setup-qemu-action docker/setup-buildx-action docker/login-action docker/metadata-action docker/build-push-action; do
  printf '%-34s %s\n' "$a" "$(gh api "repos/$a/releases/latest" -q .tag_name)"
done
```

Use the major of each in the files below; keep the majors that `ci.yml` and the current `images.yml` already use for `actions/checkout`, `actions/setup-node` and the docker actions unless a newer major exists (then use it and say so in the report).

- [ ] **Step 2: `release.yml`**

Create `.github/workflows/release.yml` (replace the `@vN` by the majors of step 1):

```yaml
name: release

# On every push to main: semantic-release says whether the commits make a release (plan); both images are built and pushed
# (sha-<short>, edge, and X.Y.Z when there is a release); the chart is packaged and pushed as an OCI artifact; and only
# when both are done does semantic-release write the version, tag it and make the GitHub release (publish). A failure in
# between leaves unused X.Y.Z artifacts, never a release without artifacts. See docs/specs/2026-10-09-release-automation-design.md.
on:
  push:
    branches: [main]

permissions:
  contents: read

concurrency:
  group: release
  cancel-in-progress: false

jobs:
  plan:
    runs-on: ubuntu-latest
    # contents: write only because semantic-release checks push access with a dry-run push even in a dry run.
    permissions:
      contents: write
    outputs:
      version: ${{ steps.next.outputs.version }}
    steps:
      - uses: actions/checkout@v7
        with:
          fetch-depth: 0
          persist-credentials: false
      - uses: actions/setup-node@v7
        with:
          node-version-file: release/.nvmrc
          cache: npm
          cache-dependency-path: release/package-lock.json
      - run: npm ci --ignore-scripts --prefix release
      - id: next
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          RELEASE_MODE: plan
        run: |
          rm -f .release-version
          release/node_modules/.bin/semantic-release --dry-run --extends ./release/release.config.js
          version=$(cat .release-version 2> /dev/null || true)
          echo "version=$version" >> "$GITHUB_OUTPUT"
          echo "next release version: ${version:-none}"

  images:
    needs: plan
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
    steps:
      - uses: actions/checkout@v7
        with:
          persist-credentials: false
      - uses: docker/setup-qemu-action@v4
      - uses: docker/setup-buildx-action@v4
      - uses: docker/login-action@v4
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - id: meta
        uses: docker/metadata-action@v6
        with:
          images: ghcr.io/jaydee94/${{ matrix.name }}
          flavor: latest=false
          tags: |
            type=sha,prefix=sha-,format=short
            type=edge,branch=main
            type=raw,value=${{ needs.plan.outputs.version }},enable=${{ needs.plan.outputs.version != '' }}
      - uses: docker/build-push-action@v7
        with:
          context: .
          file: ${{ matrix.file }}
          platforms: linux/amd64,linux/arm64
          push: true
          tags: ${{ steps.meta.outputs.tags }}
          labels: ${{ steps.meta.outputs.labels }}
          cache-from: type=gha,scope=${{ matrix.name }}
          cache-to: type=gha,scope=${{ matrix.name }},mode=max,ignore-error=true

  chart:
    needs: plan
    if: needs.plan.outputs.version != ''
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v7
        with:
          persist-credentials: false
      - name: Helm version
        run: helm version
      - name: Package
        env:
          VERSION: ${{ needs.plan.outputs.version }}
        run: sh scripts/package-chart.sh "$VERSION" dist
      - name: Push to GHCR
        env:
          VERSION: ${{ needs.plan.outputs.version }}
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          ACTOR: ${{ github.actor }}
        run: |
          printf '%s' "$GITHUB_TOKEN" | helm registry login ghcr.io --username "$ACTOR" --password-stdin
          sh scripts/push-chart.sh "dist/remedy-$VERSION.tgz" oci://ghcr.io/jaydee94/charts
      - uses: actions/upload-artifact@v7
        with:
          name: chart
          path: dist/remedy-*.tgz
          if-no-files-found: error

  publish:
    needs: [plan, images, chart]
    if: needs.plan.outputs.version != ''
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@v7
        with:
          fetch-depth: 0
          persist-credentials: false
      - uses: actions/download-artifact@v7
        with:
          name: chart
          path: dist
      - uses: actions/setup-node@v7
        with:
          node-version-file: release/.nvmrc
          cache: npm
          cache-dependency-path: release/package-lock.json
      - run: npm ci --ignore-scripts --prefix release
      - name: semantic-release
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          RELEASE_MODE: full
        run: release/node_modules/.bin/semantic-release --extends ./release/release.config.js
      - name: The tag matches the chart
        env:
          VERSION: ${{ needs.plan.outputs.version }}
        run: sh scripts/check-release.sh "v$VERSION"
```

- [ ] **Step 3: `images.yml` becomes the pull-request build**

Overwrite `.github/workflows/images.yml` (keep the majors of step 1):

```yaml
name: images

# The pull request build of the two images: linux/amd64, nothing is pushed and no secret is needed (a pull request from a
# fork builds too). Images are pushed by release.yml on main. No image contains the claude CLI.
on:
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
    strategy:
      fail-fast: false
      matrix:
        include:
          - {name: remedy-server, file: Dockerfile}
          - {name: remedy-runner, file: Dockerfile.runner}
    steps:
      - uses: actions/checkout@v7
        with:
          persist-credentials: false
      - uses: docker/setup-buildx-action@v4
      - uses: docker/build-push-action@v7
        with:
          context: .
          file: ${{ matrix.file }}
          platforms: linux/amd64
          push: false
          cache-from: type=gha,scope=${{ matrix.name }}
          # ignore-error: a pull request from a fork has a read-only cache token and must still build.
          cache-to: type=gha,scope=${{ matrix.name }},mode=max,ignore-error=true
```

- [ ] **Step 4: `pr-title.yml`**

Create `.github/workflows/pr-title.yml`:

```yaml
name: pr-title

# The squash merge uses the pull request title as the commit subject and semantic-release decides the release from it:
# it must be a conventional commit (scripts/check-pr-title.sh). The title is untrusted text: it goes through the environment.
on:
  pull_request:
    types: [opened, edited, synchronize, reopened]

permissions:
  contents: read

jobs:
  title:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
        with:
          persist-credentials: false
      - name: The title is a conventional commit
        env:
          PR_TITLE: ${{ github.event.pull_request.title }}
        run: sh scripts/check-pr-title.sh
```

- [ ] **Step 5: Validate and read against the constraints**

Run: `ruby -ryaml -e 'ARGV.each{|f| d = YAML.load_file(f); puts "#{f}: #{d.keys.inspect}"}' .github/workflows/release.yml .github/workflows/images.yml .github/workflows/pr-title.yml .github/workflows/ci.yml` (or python3 with PyYAML; `actionlint` if it is installed).
Expected: all four parse. Then read the three new files against the Global Constraints and say so in the report: permissions per job (top level `contents: read`; `plan` and `publish` `contents: write`; `images` and `chart` `packages: write`), no action outside `actions/*` and `docker/*`, no `${{ }}` inside a `run:` (every value goes through `env:`), `persist-credentials: false` on every checkout, `if:` expressions use contexts valid at that level (`needs.plan.outputs.version` in a job `if` and in a step `with`). One thing to check against the action's own
documentation: `docker/metadata-action`'s `type=raw,value=,enable=false` with an empty `value` (no release); if the action rejects an empty value even when
disabled, make the tag list conditional another way (two steps with an `if:`, or a `tags` input built in a prior step through `$GITHUB_OUTPUT`), and say so in the report.

- [ ] **Step 6: `make check`, commit**

```bash
git add .github/workflows
git commit -m "ci: release workflow (plan, images, chart, publish), pull-request title check, images.yml only builds on pull requests

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

The workflows run for the first time on the pull request that carries them (`pr-title`, `images`, `ci` with the new `release` job) and `release.yml` runs on the merge. Do not push anything in this task.

---

### Task 5: The documents

**Files:**
- Modify: `docs/design.md` (the release paragraph, if there is one), `docs/specs/2026-10-06-kubernetes-deployment-design.md` (section 8 pointer, section 11), `docs/runbook/homelab-deploy.md`, `docs/plans/k8s-followups.md`, `deploy/chart/README.md`, `README.md`, `CLAUDE.md`, `docs/plans/k8s-5-release-pipeline.md` (a note at the top of task 6 that it is replaced)
- Create: `docs/research/release-first-run.md` is **not** created here (task 6 writes it from the real run)

- [ ] **Step 1: Make the edits of spec section 9**

Do these, in English, exact and short; where a sentence of an older document is false after this change, replace it:

1. `docs/specs/2026-10-06-kubernetes-deployment-design.md`: in section 8 replace the release sentences (tag by hand, `images.yml` pushes on a tag, `check-release.sh` before the build) by a pointer: "The release is automated: `docs/specs/2026-10-09-release-automation-design.md`. A merge with a `feat` or `fix` to `main` publishes the images (`X.Y.Z`, `sha-<short>`, `edge`), the chart as `oci://ghcr.io/jaydee94/charts/remedy` and a GitHub release; `images.yml` only builds on pull requests." Check section 11 and the status block for mentions of "the tag" and update them.
2. `docs/runbook/homelab-deploy.md`: section 6 (the Application): the chart source becomes `repoURL: ghcr.io/jaydee94/charts`, `chart: remedy`, `targetRevision: X.Y.Z` (an OCI chart: with Argo CD the OCI registry is a Helm source with `chart:` and no `path`), the second source `ref: remedy` stays on the git repository at the tag `vX.Y.Z` for `deploy/cli-pin.yaml`; add that installing from git at the tag still works (`path: deploy/chart`) because `Chart.yaml` at the tag has the release version. Section 9 (upgrading): bump `targetRevision` of the chart source and of the `ref` source to the new release. Section 10 (the release process): replace by: a merge to `main` with a `feat`/`fix` makes the release; the PR title must be a conventional commit (`pr-title` check); the changelog is `CHANGELOG.md` and the GitHub release; after the first release make `remedy-server`, `remedy-runner` and `charts/remedy` public once; if `images`/`chart` fail the run is re-run (a re-run computes the same version and overwrites the same tags); to skip a release on purpose use `docs`/`chore`/`ci` commits. In the status paragraph at the top add that the OCI chart install is not yet proven on a cluster.
3. `docs/plans/k8s-followups.md`: replace the K-5 task 6 item by the first-release steps of the release automation (create the tag `v0.0.0` on the first commit, merge the pipeline pull request with a `feat:` title, make the three packages public, run the released images in the dummy once, write `docs/research/release-first-run.md`).
4. `deploy/chart/README.md`: an "Install from the OCI registry" section: `helm install remedy oci://ghcr.io/jaydee94/charts/remedy --version X.Y.Z -n remedy-system -f deploy/cli-pin.yaml -f my-values.yaml` (the pin file from the repository at the same tag), and the note that the chart in the registry is packaged by the release with `version` and `appVersion` equal to the release.
5. `README.md`: where it names the images and the chart, mention the OCI chart and the releases.
6. `CLAUDE.md`: in "Current state" (Kubernetes bullet) append: ` The release is automated (`.github/workflows/release.yml`, `release/`, `docs/specs/2026-10-09-release-automation-design.md`): a merge with a `feat` or `fix` to `main` publishes the images, the chart as an OCI artifact, the tag and the GitHub release; pull request titles must be conventional commits (`scripts/check-pr-title.sh`); `CHANGELOG.md` and `Chart.yaml`'s version are written by the release.` In "Commands" add `make release-test   # the release configuration in a scratch repository and the chart's OCI push (needs node, git, helm, docker)`. In "Conventions" add: "Pull request titles are conventional commits (`feat:`, `fix:`, `docs:` ...): the squash title decides the release."
7. `docs/plans/k8s-5-release-pipeline.md`: at the start of task 6 add one line: "Replaced by the release automation (`docs/plans/release-1-automation.md`, task 6): the tag is made by the pipeline."
8. `docs/design.md`: search for the place that describes releases or images; if there is none, add nothing.

- [ ] **Step 2: Check the links and the claims**

Run `grep -rn "check-release.sh\|images.yml\|v0.1.0" docs README.md CLAUDE.md deploy/chart/README.md | head -40` and fix every sentence that still says the old release process. Every claim about a future first release says "expected" or "proposed", not "proven". Run `make check`.

- [ ] **Step 3: Commit**

```bash
git add docs README.md CLAUDE.md deploy/chart/README.md
git commit -m "docs: the automated release in the specs, the runbook, the chart README and CLAUDE.md

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 6: The first release

**Files:**
- Create: `docs/research/release-first-run.md`

This task changes things outside the repository and cannot be undone by a commit: a tag, a public release and packages. The agent stops and asks the maintainer before each outward step.

- [ ] **Step 1: The pull request is green**

Push the branch, open the pull request with a **conventional title** (`feat: automated releases with semantic-release, images and the Helm chart as OCI artifacts`) and check `pr-title`, `ci` (including `release`), `images` and `chart`.
Expected: all green.

- [ ] **Step 2: Ask, then the start tag**

Ask the maintainer: "May I create the tag `v0.0.0` on the first commit of the repository and push it? It starts no workflow; it only tells semantic-release where to count from, so that the first release is 0.1.0 with the whole history in its changelog." Only on a yes:

```sh
first=$(git rev-list --max-parents=0 origin/main)
git tag v0.0.0 "$first"
git push origin v0.0.0
```

- [ ] **Step 3: Ask, then merge**

Ask: "May I merge the pull request? The merge publishes release 0.1.0: two images and the chart to GHCR (private until you make them public), the tag v0.1.0 and a public GitHub release." Only on a yes: squash-merge with the title above, then `gh run watch` the `release` run.
Expected: `plan` says `0.1.0`; `images` and `chart` push; `publish` makes the commit `chore(release): 0.1.0 [skip ci]`, the tag `v0.1.0` and the release with the chart attached; the last step prints `the tag v0.1.0 matches the chart`.

- [ ] **Step 4: What only the first run can show**

Check and write down: that the bump commit was pushed to `main` by the token (no branch protection); that the package `charts/remedy` exists and is linked to the repository (`gh api` needs the `read:packages` scope: if the agent's token lacks it, ask the maintainer to look at the package page); the release notes and `CHANGELOG.md`; that the run did not start a second release (the bump commit has `[skip ci]`: look at `gh run list`).

- [ ] **Step 5: Ask, then the visibility**

Ask the maintainer to make `remedy-server`, `remedy-runner` and `charts/remedy` public in GitHub (profile, Packages, Package settings, Change visibility). Then check anonymously: `docker logout ghcr.io; docker pull ghcr.io/jaydee94/remedy-server:0.1.0`, the same for the runner, and `helm pull oci://ghcr.io/jaydee94/charts/remedy --version 0.1.0`.

- [ ] **Step 6: The released images and chart in the dummy, once**

```sh
make dummy-down && make dummy-up
helm --kube-context kind-remedy-dev -n remedy-system upgrade remedy oci://ghcr.io/jaydee94/charts/remedy --version 0.1.0 \
  -f deploy/cli-pin.yaml -f dev/kind/dummy-values.yaml \
  --set "networkPolicy.apiServer.cidrs={$(kubectl --context kind-remedy-dev get endpoints kubernetes -o jsonpath='{.subsets[0].addresses[0].ip}')/32}" \
  --wait --timeout 10m
make dummy-smoke
```

Expected: the upgrade pulls the images `ghcr.io/jaydee94/remedy-server:0.1.0` and `…runner:0.1.0` (the chart's default tag is its `appVersion`) and the smoke test ends `all ok` (two short runs of the maintainer's quota: ask first).

- [ ] **Step 7: The record**

Create `docs/research/release-first-run.md`: the date, the run's URL, the version, what each job did, the tags and architectures (`docker buildx imagetools inspect`), the chart as pulled, the release page and the changelog, what was done by hand (the tag `v0.0.0`, the visibility), and what the first run showed about the open points of spec section 7. Exact wording: measured, observed, expected. Commit it on a branch and open a pull request titled `docs: record of the first automated release`.
