#!/bin/sh
# Pushes a packaged chart to an OCI registry. Extra arguments go to `helm push` (for example --plain-http for a local registry).
# Usage: scripts/push-chart.sh <package.tgz> <oci base, e.g. oci://ghcr.io/jaydee94/charts> [helm push arguments]
set -eu
tgz=${1:?usage: push-chart.sh <package.tgz> <oci base> [helm push arguments]}
base=${2:?usage: push-chart.sh <package.tgz> <oci base> [helm push arguments]}
shift 2
[ -f "$tgz" ] || { echo "$tgz does not exist" >&2; exit 1; }
case $base in
  oci://*) ;;
  *) echo "the target must be an oci:// URL, got '$base'" >&2; exit 1 ;;
esac
helm push "$tgz" "$base" "$@"
