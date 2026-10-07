#!/bin/sh
# make dummy-reset: empties the control plane's database and keeps the cluster, the secrets and the login. Incidents, runs
# and sessions are gone; the admin signs in again.
set -eu
. "$(dirname "$0")/lib.sh"
need kind kubectl helm jq
require_dummy
# The release keeps its image tags: only the database is reset.
TAG=$(helm --kube-context "$CTX" -n "$NS" get values "$RELEASE" -o json | jq -r '.image.server.tag')
case "$TAG" in
  "" | null) echo "cannot read the image tag of release $RELEASE" >&2; exit 1 ;;
esac
k -n "$NS" scale deployment/remedy-server --replicas=0
k -n "$NS" wait --for=delete pod -l app.kubernetes.io/component=server --timeout=120s || true
k -n "$NS" delete pvc remedy-data --wait=true
# Scale up before the upgrade, not after it: the claim waits for its first consumer (WaitForFirstConsumer), so an upgrade
# that waits while no pod exists could wait for a claim that is never bound. The pod stays Pending until Helm has made
# the claim again, then starts. (Measured: an upgrade of a scaled-down Deployment leaves it at 0 replicas.)
k -n "$NS" scale deployment/remedy-server --replicas=1
deploy_release
k -n "$NS" rollout status deployment/remedy-server --timeout=300s
echo "the database is empty again"
