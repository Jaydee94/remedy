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

# Prerelease guard: even if chart has the same prerelease, reject it
chart 0.2.0-rc1 0.2.0-rc1
bad "a prerelease tag whose chart says the same" v0.2.0-rc1

# Strict shape X.Y.Z enforcement (no leading zeros, no extra dots)
chart 1.2.3.4 1.2.3.4
bad "four-part version v1.2.3.4"                 v1.2.3.4
chart 01.2.3 01.2.3
bad "leading zero in version v01.2.3"            v01.2.3
chart 1.2.3. 1.2.3.
bad "trailing dot in version v1.2.3."            v1.2.3.
chart 1..3 1..3
bad "double dot in version v1..3"                v1..3

# Field function robustness: trailing comments, CRLF, single quotes
chart 1.2.3 1.2.3
printf 'apiVersion: v2\nname: remedy\nversion: 1.2.3 # release\nappVersion: "1.2.3"\n' > "$WORK/Chart.yaml"
ok  "version with trailing comment"         v1.2.3
printf 'apiVersion: v2\nname: remedy\nversion: 1.2.3\r\nappVersion: "1.2.3"\r\n' > "$WORK/Chart.yaml"
ok  "CRLF line endings"                     v1.2.3
printf 'apiVersion: v2\nname: remedy\nversion: 1.2.3\nappVersion: '"'"'1.2.3'"'"'\n' > "$WORK/Chart.yaml"
ok  "single-quoted appVersion"              v1.2.3
printf 'apiVersion: v2\nname: remedy\n# version: 0.9.9\nversion: 1.2.3\nappVersion: "1.2.3"\n' > "$WORK/Chart.yaml"
ok  "comment line with version ignored"     v1.2.3
printf 'apiVersion: v2\nname: remedy\nsome_key:\n  version: 0.9.9\nversion: 1.2.3\nappVersion: "1.2.3"\n' > "$WORK/Chart.yaml"
ok  "indented version ignored"              v1.2.3

printf 'apiVersion: v2\nname: remedy\n' > "$WORK/Chart.yaml"
bad "a chart without versions"              v0.1.0
rm -f "$WORK/Chart.yaml"
bad "a missing chart file"                  v0.1.0

[ "$fails" -eq 0 ] && echo "all passed" || { echo "$fails failed" >&2; exit 1; }
