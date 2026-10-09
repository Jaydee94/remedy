#!/bin/sh
# The version semantic-release is about to release must be the version the workflow planned (and pushed the images and the chart
# for). Called by the exec plugin's verifyReleaseCmd (release/release.config.js), which runs before any commit or tag.
# The planned version is in the environment variable EXPECTED_VERSION.
# Usage: scripts/check-expected-version.sh <version> require|optional
#   require   an unset or empty EXPECTED_VERSION fails (the publish job)
#   optional  an unset or empty EXPECTED_VERSION passes; a set one must be equal (the scratch test)
set -eu
version=${1:-}
mode=${2:-}
[ -n "$version" ] || { echo "usage: check-expected-version.sh <version> require|optional" >&2; exit 1; }
case $mode in
  require|optional) ;;
  *) echo "usage: check-expected-version.sh <version> require|optional" >&2; exit 1 ;;
esac
expected=${EXPECTED_VERSION:-}
if [ -z "$expected" ]; then
  if [ "$mode" = require ]; then
    echo "EXPECTED_VERSION is not set: the release needs the version the workflow planned (semantic-release computed $version)" >&2
    exit 1
  fi
  exit 0
fi
if [ "$expected" != "$version" ]; then
  echo "semantic-release computed $version but the workflow planned $expected, and pushed the images and the chart for $expected: not releasing" >&2
  exit 1
fi
echo "the release version $version is the planned version"
