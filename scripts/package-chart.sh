#!/bin/sh
# Packages the Helm chart with the release version (which Chart.yaml does not need to have yet) and prints the path of the
# package. The version is validated as X.Y.Z: it comes from the release tool, but it ends up in a file name.
# Run it from the repository root: the default paths are relative to it.
# Usage: scripts/package-chart.sh X.Y.Z [destination directory, default dist]
# Environment: CHART_DIR (default deploy/chart) and LINT_VALUES (default $CHART_DIR/ci/lint-values.yaml), for the tests.
set -eu
version=${1:-}
dest=${2:-dist}
chart_dir=${CHART_DIR:-deploy/chart}
lint_values=${LINT_VALUES:-$chart_dir/ci/lint-values.yaml}

# grep matches line by line: a value with a newline must not get through on the strength of one good line.
case $version in
  *'
'*) echo "the version must be a single line" >&2; exit 1 ;;
esac
printf '%s' "$version" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' ||
  { echo "'$version' is not a version of the form X.Y.Z" >&2; exit 1; }
command -v helm > /dev/null || { echo "helm is needed" >&2; exit 1; }
name=$(sed -n 's/^name: *//p' "$chart_dir/Chart.yaml")
[ -n "$name" ] || { echo "$chart_dir/Chart.yaml has no name" >&2; exit 1; }
mkdir -p "$dest"
out=$(helm lint "$chart_dir" -f "$lint_values" 2>&1) || { printf '%s\n' "$out" >&2; exit 1; }
out=$(helm package "$chart_dir" --version "$version" --app-version "$version" --destination "$dest" 2>&1) || { printf '%s\n' "$out" >&2; exit 1; }
echo "$dest/$name-$version.tgz"
