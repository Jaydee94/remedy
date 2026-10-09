#!/bin/sh
# Tests the release configuration in a scratch git repository with a local bare remote and crafted commits: the version
# rules (0.x), what releases nothing, the notes, and what the real (local) run writes. Run from anywhere:
#   cd release && npm ci --ignore-scripts && cd .. && sh release/test/dry-run.sh
# The `full` mode (the GitHub release) can not be run without GitHub: here it is only loaded and checked, and it is
# exercised for real only by .github/workflows/release.yml.
# Nothing of the caller's environment may reach the test (CI and GITHUB_* make semantic-release detect another branch,
# GIT_DIR and the like point git elsewhere, tokens would be used): semantic-release runs in `env -i`, and the git commands
# of the test use no global or system git configuration.
set -eu
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_COMMON_DIR GIT_OBJECT_DIRECTORY GIT_NAMESPACE
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
SR=$ROOT/release/node_modules/.bin/semantic-release
CONFIG=$ROOT/release/release.config.js
[ -x "$SR" ] || { echo "run npm ci in release/ first" >&2; exit 1; }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
REMOTE=$WORK/remote.git
REPO=$WORK/repo
git init -q --bare -b main "$REMOTE"
git init -q -b main "$REPO"
cd "$REPO"
git config user.email release-test@example.invalid
git config user.name release-test
git config commit.gpgsign false
git config tag.gpgsign false
git remote add origin "$REMOTE"

mkdir -p scripts deploy/chart
cp "$ROOT/scripts/set-chart-version.sh" "$ROOT/scripts/check-expected-version.sh" scripts/
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
# The environment of semantic-release is built from nothing: PATH (node, git), a HOME of its own and the isolated git config.
clean_env() { env -i PATH="$PATH" HOME="$WORK" TMPDIR="$WORK" GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null "$@"; }
sr() { # sr <mode> [extra args]: runs semantic-release in the scratch repository, output in $WORK/sr.log
  mode=$1; shift
  clean_env RELEASE_MODE="$mode" "$SR" --no-ci --repository-url "file://$REMOTE" --extends "$CONFIG" "$@" > "$WORK/sr.log" 2>&1 || { cat "$WORK/sr.log" >&2; echo "semantic-release failed ($mode)" >&2; exit 1; }
  # A branch detected from the environment makes semantic-release stop without a release; that must never read as "no release".
  if grep -q 'configured to only publish from' "$WORK/sr.log"; then cat "$WORK/sr.log" >&2; echo "semantic-release saw another branch than main ($mode)" >&2; exit 1; fi
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
# plan mode can not publish, even without the --dry-run flag
tags_before=$(git --git-dir="$REMOTE" tag | sort | tr '\n' ' ')
head_before=$(git rev-parse HEAD)
remote_before=$(git --git-dir="$REMOTE" rev-parse main)
rm -f .release-version
sr plan
[ "$(cat .release-version 2>/dev/null)" = "0.1.0" ] && say "plan without --dry-run still writes .release-version" || fail "plan without --dry-run wrote no .release-version"
[ "$(git --git-dir="$REMOTE" tag | sort | tr '\n' ' ')" = "$tags_before" ] && [ "$(git tag | sort | tr '\n' ' ')" = "$tags_before" ] && say "plan without --dry-run creates no tag" || fail "plan without --dry-run created a tag"
[ "$(git rev-parse HEAD)" = "$head_before" ] && [ "$(git --git-dir="$REMOTE" rev-parse main)" = "$remote_before" ] && [ -z "$(git status --porcelain --untracked-files=no)" ] && say "plan without --dry-run makes no commit and changes no file" || fail "plan without --dry-run changed the repository"
# there is no default mode. The configuration says why; semantic-release itself only fails (with --extends it hides the
# message of a configuration that throws behind "Cannot find module"), and it fails before it reads or pushes anything.
if clean_env node -e "require('$CONFIG')" > "$WORK/nomode.log" 2>&1; then fail "the configuration loaded without RELEASE_MODE"
elif grep -q 'RELEASE_MODE must be plan, local or full (got "undefined")' "$WORK/nomode.log"; then say "no RELEASE_MODE is refused by the configuration"
else fail "no RELEASE_MODE failed with another message: $(head -c 300 "$WORK/nomode.log")"; fi
if clean_env "$SR" --no-ci --repository-url "file://$REMOTE" --extends "$CONFIG" --dry-run > "$WORK/nomode.log" 2>&1; then fail "semantic-release ran without RELEASE_MODE"; else say "semantic-release does not run without RELEASE_MODE"; fi
# the full mode loads: six plugins that resolve, the chart attached to the release, no comments
full=$(cd "$ROOT/release" && clean_env RELEASE_MODE=full node -e "
const c = require('$CONFIG')
const names = c.plugins.map((p) => p[0])
for (const n of names) require.resolve(n, { paths: ['$ROOT/release'] })
const gh = c.plugins.find((p) => p[0] === '@semantic-release/github')[1]
const ok = names.length === 6 && !c.dryRun && gh.assets.some((a) => a.path === 'dist/remedy-*.tgz') && gh.successCommentCondition === false && gh.failCommentCondition === false && gh.releasedLabels === false
console.log(ok ? 'full ok' : 'full wrong: ' + JSON.stringify(c))
" 2>&1) || true
# in full mode the version check is mandatory (an unset EXPECTED_VERSION fails), in local mode it is optional
cmds=$(cd "$ROOT/release" && for m in local full; do clean_env RELEASE_MODE=$m node -e "
const c = require('$CONFIG')
const cmd = c.plugins.filter((p) => p[0] === '@semantic-release/exec').map((p) => p[1].verifyReleaseCmd).filter(Boolean).join(' ; ')
console.log('$m: ' + cmd)
"; done 2>&1)
case $cmds in
  *"local: "*check-expected-version.sh*optional*"full: "*check-expected-version.sh*require*) say "the exec step checks EXPECTED_VERSION: optional in local mode, required in full mode" ;;
  *) fail "the exec step of the version check: $cmds" ;;
esac
[ "$full" = "full ok" ] && say "the full mode loads: six plugins, the chart attached, no comments, no dry run" || fail "the full mode: $full"
# the version the workflow planned (EXPECTED_VERSION) must be the version semantic-release computes, checked before any
# commit or tag: a wrong one fails the run and leaves the repository and the remote as they were
rel_expect() { clean_env RELEASE_MODE=local EXPECTED_VERSION="$1" "$SR" --no-ci --repository-url "file://$REMOTE" --extends "$CONFIG" > "$WORK/sr.log" 2>&1; }
tags_before=$(git --git-dir="$REMOTE" tag | sort | tr '\n' ' ')
head_before=$(git rev-parse HEAD)
remote_before=$(git --git-dir="$REMOTE" rev-parse main)
if rel_expect 0.9.9; then fail "a release ran with a wrong EXPECTED_VERSION"; else say "a wrong EXPECTED_VERSION fails the run"; fi
grep -q '0.9.9' "$WORK/sr.log" && grep -q '0.1.0' "$WORK/sr.log" && say "the message names both versions" || fail "the message of a wrong EXPECTED_VERSION: $(tail -c 400 "$WORK/sr.log")"
[ "$(git --git-dir="$REMOTE" tag | sort | tr '\n' ' ')" = "$tags_before" ] && [ "$(git tag | sort | tr '\n' ' ')" = "$tags_before" ] && say "a wrong EXPECTED_VERSION creates no tag" || fail "a wrong EXPECTED_VERSION created a tag"
[ "$(git rev-parse HEAD)" = "$head_before" ] && [ "$(git --git-dir="$REMOTE" rev-parse main)" = "$remote_before" ] && [ -z "$(git status --porcelain --untracked-files=no)" ] && say "a wrong EXPECTED_VERSION makes no commit and changes no file" || fail "a wrong EXPECTED_VERSION changed the repository"
rel_expect 0.1.0 || { cat "$WORK/sr.log" >&2; fail "the right EXPECTED_VERSION did not release"; }
git pull -q --ff-only origin main
[ "$(git --git-dir="$REMOTE" tag | grep -cx v0.1.0)" = 1 ] && say "the right EXPECTED_VERSION releases" || fail "no tag v0.1.0 after the right EXPECTED_VERSION"
grep -q '^version: 0.1.0$' deploy/chart/Chart.yaml && say "Chart.yaml version written" || fail "Chart.yaml version not written"
grep -q '^appVersion: "0.1.0"$' deploy/chart/Chart.yaml && say "Chart.yaml appVersion written" || fail "Chart.yaml appVersion not written"
grep -q '^## ' CHANGELOG.md && grep -q '### Features' CHANGELOG.md && grep -q 'the first feature' CHANGELOG.md && say "CHANGELOG.md has the Features section" || fail "CHANGELOG.md lacks the Features section"
[ "$(git --git-dir="$REMOTE" tag | grep -cx v0.1.0)" = 1 ] && say "the tag v0.1.0 is on the remote" || fail "no tag v0.1.0 on the remote"
git log -1 --format=%s | grep -qx 'chore(release): 0.1.0 \[skip ci\]' && say "the bump commit message" || fail "the bump commit message is $(git log -1 --format=%s)"
[ "$(git show --name-only --format= HEAD | sort | tr '\n' ' ')" = "CHANGELOG.md deploy/chart/Chart.yaml " ] && say "the bump commit holds CHANGELOG.md and Chart.yaml, nothing else" || fail "the bump commit holds: $(git show --name-only --format= HEAD | tr '\n' ' ')"
[ "$(git --git-dir="$REMOTE" log -1 --format=%s main)" = "$(git log -1 --format=%s)" ] && say "the bump commit is on the remote's main" || fail "the bump commit is not on the remote's main"
[ -z "$(git status --porcelain --untracked-files=no)" ] && say "the working tree is clean after the release" || fail "the working tree is dirty after the release: $(git status --porcelain --untracked-files=no | tr '\n' ' ')"
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

commit "refactor!: a breaking change of a type that releases nothing by itself"
expect "a breaking refactor on 0.x gives the next minor (the breaking rule wins over the default of a major)" "0.5.0"
release

commit 'Revert "feat: with a scope inside"' "This reverts commit 0000000000000000000000000000000000000000."
expect "a revert gives a patch" "0.5.1"
release
grep -q '### Reverts' CHANGELOG.md && say "CHANGELOG.md has the Reverts section" || fail "no Reverts section"

if grep -q 'only documents' CHANGELOG.md || grep -q 'only chores' CHANGELOG.md || grep -q 'not conventional' CHANGELOG.md; then fail "hidden types or a non-conventional title are in the changelog"; else say "docs, chore, ci and the odd title are not in the changelog"; fi
grep -q 'BREAKING CHANGES' CHANGELOG.md && say "the breaking change note is in the changelog" || fail "no BREAKING CHANGES note in the changelog"

[ "$fails" -eq 0 ] && echo "all passed" || { echo "$fails failed" >&2; exit 1; }
