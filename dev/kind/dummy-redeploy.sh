#!/bin/sh
# make dummy-redeploy: the fast loop. Builds both images again, loads them and upgrades the release. The cluster, the
# database and the login stay.
set -eu
. "$(dirname "$0")/lib.sh"
need docker kind kubectl helm make
require_dummy
new_tag
build_and_load_images
deploy_release
echo "redeployed with the tag $TAG"
