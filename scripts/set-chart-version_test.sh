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
bad "refuses a second line after a good version" sh "$SCRIPT" "1.2.3
x/; s/^/#/" "$WORK/Chart.yaml"
# The refusal must come from the script's own check, not from sed choking on the newline later.
sh "$SCRIPT" "1.2.3
x" "$WORK/Chart.yaml" > "$WORK/out" 2>&1
eq  "a newline is refused by the version check" "the version must be a single line" "$(cat "$WORK/out")"
bad "refuses a good version after a first line" sh "$SCRIPT" "x
1.2.3" "$WORK/Chart.yaml"
eq  "and the chart is untouched" "$(cat "$WORK/before.yaml")" "$(cat "$WORK/Chart.yaml")"

# A symlinked chart stays a symlink and the target gets the version (the script writes with cat, it does not replace the file).
chart 0.1.0 0.1.0
mv "$WORK/Chart.yaml" "$WORK/real.yaml"
ln -s real.yaml "$WORK/Chart.yaml"
ok  "a chart that is a symlink" sh "$SCRIPT" 4.5.6 "$WORK/Chart.yaml"
ok  "the symlink is still a symlink" test -L "$WORK/Chart.yaml"
eq  "the target has the version" "version: 4.5.6" "$(grep '^version:' "$WORK/real.yaml")"
eq  "the target has the appVersion" 'appVersion: "4.5.6"' "$(grep '^appVersion:' "$WORK/real.yaml")"
rm -f "$WORK/Chart.yaml" "$WORK/real.yaml"

bad "a missing file" sh "$SCRIPT" 1.2.3 "$WORK/nope.yaml"
printf 'apiVersion: v2\nversion: 0.1.0\nversion: 0.2.0\nappVersion: "0.1.0"\n' > "$WORK/two.yaml"
bad "two version lines" sh "$SCRIPT" 1.2.3 "$WORK/two.yaml"
printf 'apiVersion: v2\nversion: 0.1.0\n' > "$WORK/noapp.yaml"
bad "no appVersion line" sh "$SCRIPT" 1.2.3 "$WORK/noapp.yaml"
bad "no argument" sh "$SCRIPT"

[ "$fails" -eq 0 ] && echo "all passed" || { echo "$fails failed" >&2; exit 1; }
