#!/bin/sh
# Writes the release version into the Helm chart: `version` and `appVersion` of Chart.yaml, nothing else. Called by
# semantic-release (release/release.config.js, the exec plugin's prepare step) with the version it computed.
# Usage: scripts/set-chart-version.sh X.Y.Z [chart file]
set -eu
version=${1:-}
chart=${2:-deploy/chart/Chart.yaml}

# grep matches line by line: a value with a newline must not get through on the strength of one good line.
case $version in
  *'
'*) echo "the version must be a single line" >&2; exit 1 ;;
esac
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
