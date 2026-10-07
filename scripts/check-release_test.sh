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
