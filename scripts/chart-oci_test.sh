#!/bin/sh
# Packages the chart with a version it does not have in Chart.yaml, pushes it to a local OCI registry and pulls it back.
# Needs helm and docker (registry:2); without docker it says so and exits 0 (CI has it). Run: sh scripts/chart-oci_test.sh
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/.." && pwd)
command -v helm > /dev/null || { echo "helm is needed" >&2; exit 1; }
if ! { command -v docker > /dev/null && docker info > /dev/null 2>&1; }; then
  # CI sets REMEDY_REQUIRE_DOCKER=1: a missing docker there is a failure, not a skip.
  [ -z "${REMEDY_REQUIRE_DOCKER:-}" ] || { echo "docker is needed (REMEDY_REQUIRE_DOCKER is set)" >&2; exit 1; }
  echo "skip: docker is not available (the chart push test needs registry:2)"
  exit 0
fi

WORK=$(mktemp -d)
NAME=remedy-chart-oci-test-$$
trap 'docker rm -f "$NAME" > /dev/null 2>&1; rm -rf "$WORK"' EXIT
trap 'exit 130' INT TERM HUP
fails=0
eq() { if [ "$2" = "$3" ]; then echo "ok    $1"; else echo "FAIL  $1"; echo "  want: $2"; echo "  got:  $3"; fails=$((fails + 1)); fi; }
has() { if printf '%s' "$3" | grep -q "$2"; then echo "ok    $1"; else echo "FAIL  $1 (no '$2' in the output)"; fails=$((fails + 1)); fi; }

cd "$ROOT"
chart_before=$(cksum < deploy/chart/Chart.yaml)
chart_source=$(sed -n '/^sources:/,$ s/^ *- *//p' deploy/chart/Chart.yaml | sed -n 1p)
[ -n "$chart_source" ] || { echo "Chart.yaml has no sources entry" >&2; exit 1; }

tgz=$(sh scripts/package-chart.sh 9.9.9 "$WORK/dist") || { echo "package-chart failed" >&2; exit 1; }
eq "the package name" "$WORK/dist/remedy-9.9.9.tgz" "$tgz"
eq "Chart.yaml in the repository is untouched" "$chart_before" "$(cksum < deploy/chart/Chart.yaml)"
eq "version inside the package" "version: 9.9.9" "$(tar -xzOf "$tgz" remedy/Chart.yaml | grep '^version:')"
# Helm 3 may quote the value and Helm 4 does not: compare without quotes.
eq "appVersion inside the package" "appVersion: 9.9.9" "$(tar -xzOf "$tgz" remedy/Chart.yaml | grep '^appVersion:' | tr -d '"')"

if sh scripts/package-chart.sh 1.2 "$WORK/dist2" > /dev/null 2>&1; then echo "FAIL  a bad version must be refused"; fails=$((fails + 1)); else echo "ok    a bad version is refused"; fi

nl='
'
refused=$(sh scripts/package-chart.sh "1.0.0${nl}x" "$WORK/dist3" 2>&1) && { echo "FAIL  a version with a newline must be refused"; fails=$((fails + 1)); } || has "a version with a newline is refused by the script" "single line" "$refused"

# A broken chart makes the script fail, and helm's complaint reaches stderr (it is not swallowed).
cp -R deploy/chart "$WORK/broken"
printf 'server: [unclosed\n' >> "$WORK/broken/values.yaml"
broken=$(CHART_DIR="$WORK/broken" sh scripts/package-chart.sh 9.9.9 "$WORK/dist4" 2>&1 > /dev/null) && { echo "FAIL  a broken chart must make package-chart fail"; fails=$((fails + 1)); } || has "a broken chart fails and the lint error is printed" "ERROR" "$broken"

# push-chart.sh refuses a target that is not an oci:// URL and a package that does not exist, with its own message (helm would
# fail on both as well, so the exit status alone would not show that the script checked).
refused=$(sh scripts/push-chart.sh "$tgz" "https://example.invalid/charts" 2>&1) && { echo "FAIL  a non-oci target must be refused"; fails=$((fails + 1)); } || has "a non-oci target is refused by the script" "must be an oci://" "$refused"
refused=$(sh scripts/push-chart.sh "$WORK/dist/missing.tgz" "oci://127.0.0.1:1/charts" 2>&1) && { echo "FAIL  a missing package must be refused"; fails=$((fails + 1)); } || has "a missing package is refused by the script" "does not exist" "$refused"

docker image inspect registry:2 > /dev/null 2>&1 || docker pull -q registry:2 > /dev/null || { echo "could not pull registry:2" >&2; exit 1; }
docker run -d --rm --name "$NAME" -p 127.0.0.1::5000 registry:2 > /dev/null || { echo "could not start registry:2" >&2; exit 1; }
PORT=$(docker port "$NAME" 5000/tcp | sed -n 1p)
PORT=${PORT##*:}
[ -n "$PORT" ] || { echo "could not read the registry port" >&2; exit 1; }
i=0
until curl -sf "http://127.0.0.1:$PORT/v2/" > /dev/null; do i=$((i + 1)); [ "$i" -lt 30 ] || { echo "the registry did not start" >&2; exit 1; }; sleep 1; done

out=$(sh scripts/push-chart.sh "$tgz" "oci://127.0.0.1:$PORT/charts" --plain-http 2>&1) || { echo "$out"; echo "push failed" >&2; exit 1; }
has "the push says it pushed" "Pushed" "$out"

mkdir -p "$WORK/pulled"
helm pull "oci://127.0.0.1:$PORT/charts/remedy" --version 9.9.9 --plain-http --destination "$WORK/pulled" > /dev/null 2>&1 || { echo "pull failed" >&2; exit 1; }
eq "the pulled chart is the pushed one" "version: 9.9.9" "$(tar -xzOf "$WORK/pulled/remedy-9.9.9.tgz" remedy/Chart.yaml | grep '^version:')"

# The manifest annotation that ties a GHCR package to the repository (org.opencontainers.image.source), with the first
# `sources` entry of Chart.yaml as its value.
manifest=$(curl -sf -H 'Accept: application/vnd.oci.image.manifest.v1+json' "http://127.0.0.1:$PORT/v2/charts/remedy/manifests/9.9.9")
has "the manifest names the source repository" "\"org.opencontainers.image.source\":\"$chart_source\"" "$manifest"

[ "$fails" -eq 0 ] && echo "all passed" || { echo "$fails failed" >&2; exit 1; }
