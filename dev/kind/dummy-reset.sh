#!/bin/sh
# make dummy-reset: empties the control plane's database and keeps the cluster, the secrets and the login. Incidents, runs
# and sessions are gone; the admin signs in again.
set -eu
. "$(dirname "$0")/lib.sh"
need kind kubectl helm jq
require_dummy
# The release keeps its image tags: only the database is reset.
TAG=$(helm --kube-context "$CTX" -n "$NS" get values "$RELEASE" -o json | jq -r '.image.server.tag')
k -n "$NS" scale deployment/remedy-server --replicas=0
k -n "$NS" wait --for=delete pod -l app.kubernetes.io/component=server --timeout=120s || true
k -n "$NS" delete pvc remedy-data --wait=true
# Helm makes the missing claim again. A scaled-down Deployment stays scaled down on an upgrade, so scale it up by hand.
deploy_release
k -n "$NS" scale deployment/remedy-server --replicas=1
k -n "$NS" rollout status deployment/remedy-server --timeout=300s
echo "the database is empty again"
