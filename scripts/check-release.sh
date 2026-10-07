#!/bin/sh
# A release tag must match the Helm chart: the images are tagged with the version of the tag, and the chart's default
# image tag is its appVersion. Used by .github/workflows/images.yml before anything is pushed.
# Usage: scripts/check-release.sh <tag> [chart file]    e.g. scripts/check-release.sh v0.1.0
set -eu
tag=${1:-}
chart=${2:-deploy/chart/Chart.yaml}

case $tag in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "the tag '$tag' is not vX.Y.Z" >&2; exit 1 ;;
esac
want=${tag#v}
case $want in
  *[!0-9.]*) echo "the tag '$tag' is not vX.Y.Z (no prerelease suffix is published)" >&2; exit 1 ;;
esac
# Enforce strict X.Y.Z shape (no leading zeros, exactly three numeric parts)
printf '%s' "$want" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || {
  echo "the tag '$tag' is not vX.Y.Z" >&2; exit 1
}
[ -f "$chart" ] || { echo "$chart does not exist" >&2; exit 1; }

field() {
  # Try double-quoted value
  local val
  val=$(sed -n "s/^$1: *\"\([^\"]*\)\".*/\1/p" "$chart" | head -1)
  [ -n "$val" ] && { echo "$val"; return; }
  # Try single-quoted value
  val=$(sed -n "s/^$1: *'\([^']*\)'.*/\1/p" "$chart" | head -1)
  [ -n "$val" ] && { echo "$val"; return; }
  # Try unquoted value (until space, comment, or control char like CR)
  sed -n "s/^$1: *\([^ #[:cntrl:]]*\).*/\1/p" "$chart" | head -1
}
version=$(field version)
app=$(field appVersion)
[ -n "$version" ] && [ -n "$app" ] || { echo "$chart has no version or no appVersion" >&2; exit 1; }
if [ "$version" != "$want" ] || [ "$app" != "$want" ]; then
  echo "the tag says $want but $chart has version $version and appVersion $app: change both, merge, then tag" >&2
  exit 1
fi
echo "the tag $tag matches the chart"
