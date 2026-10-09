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
git init -q --bare -b main "$REMOTE"
git init -q -b main "$REPO"
cd "$REPO"
git config user.email release-test@example.invalid
git config user.name release-test
git config commit.gpgsign false
git config tag.gpgsign false
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
  RELEASE_MODE=$mode "$SR" --no-ci --repository-url "file://$REMOTE" --extends "$CONFIG" "$@" > "$WORK/sr.log" 2>&1 || { cat "$WORK/sr.log" >&2; echo "semantic-release failed ($mode)" >&2; exit 1; }
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
