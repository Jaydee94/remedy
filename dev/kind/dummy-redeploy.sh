#!/bin/sh
# make dummy-redeploy: the fast loop. Builds both images again, loads them and upgrades the release. The cluster, the
# database and the login stay.
set -eu
. "$(dirname "$0")/lib.sh"
need docker kind kubectl helm make
require_dummy
new_tag
build_and_load_images
# No restart for the write token is needed here: the new tag rolls the pods, and they start after the token exists.
deploy_release
echo "redeployed with the tag $TAG"
