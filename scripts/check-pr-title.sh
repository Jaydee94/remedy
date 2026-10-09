#!/bin/sh
# A pull request title must be a conventional commit: `type(scope)?!?: subject`. The squash merge uses the title as the
# commit subject, and semantic-release decides the release from it (feat is minor, fix and perf are patch, the rest
# releases nothing). The title is untrusted text: it is only matched, never executed or interpolated. There is no length
# limit: the squash subject is only matched, and legitimate conventional subjects run past 120 characters.
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
printf '%s' "$title" | grep -Eq '^(feat|fix|perf|revert|docs|chore|ci|test|refactor|style|build)(\([a-z0-9._/-]+\))?!?: [^ ]' || fail
echo "the title is a conventional commit"
