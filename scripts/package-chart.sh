#!/bin/sh
# Packages the Helm chart with the release version (which Chart.yaml does not need to have yet) and prints the path of the
# package. The version is validated as X.Y.Z: it comes from the release tool, but it ends up in a file name.
# Usage: scripts/package-chart.sh X.Y.Z [destination directory, default dist]
set -eu
version=${1:-}
dest=${2:-dist}
printf '%s' "$version" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' ||
  { echo "'$version' is not a version of the form X.Y.Z" >&2; exit 1; }
command -v helm > /dev/null || { echo "helm is needed" >&2; exit 1; }
mkdir -p "$dest"
helm lint deploy/chart -f deploy/chart/ci/lint-values.yaml > /dev/null
helm package deploy/chart --version "$version" --app-version "$version" --destination "$dest" > /dev/null
echo "$dest/remedy-$version.tgz"
