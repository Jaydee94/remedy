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
ok  "a long title (there is no length limit)" "feat: $(printf 'x%.0s' $(seq 1 130))"
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
bad "a type inside the title"      "Add a feat: x"
bad "a bad second line"            "feat: one
 two"

# The title can also come from the environment (the workflow passes it there).
if PR_TITLE="fix: from the environment" sh "$SCRIPT" > /dev/null 2>&1; then echo "ok    PR_TITLE"; else echo "FAIL  PR_TITLE"; fails=$((fails + 1)); fi
if PR_TITLE="nonsense" sh "$SCRIPT" > /dev/null 2>&1; then echo "FAIL  PR_TITLE nonsense"; fails=$((fails + 1)); else echo "ok    PR_TITLE nonsense"; fi

[ "$fails" -eq 0 ] && echo "all passed" || { echo "$fails failed" >&2; exit 1; }
