#!/bin/sh
# make dummy-reset: empties the control plane's database and keeps the cluster, the secrets and the login. Incidents, runs
# and sessions are gone; the admin signs in again.
set -eu
. "$(dirname "$0")/lib.sh"
need kind kubectl helm
require_dummy
# The control plane first: nothing may hold a connection while the database is dropped, and the new pod migrates the empty
# database when it starts. The release keeps its image tags: only the database is reset.
k -n "$NS" scale deployment/remedy-server --replicas=0
k -n "$NS" wait --for=delete pod -l app.kubernetes.io/component=server --timeout=120s || true
# The local socket of the database pod trusts the pod's own user (the image's default), so no password is needed or printed.
k -n "$NS" exec pod/remedy-postgres-0 -- psql -U remedy -d postgres -v ON_ERROR_STOP=1 \
  -c 'DROP DATABASE IF EXISTS remedy WITH (FORCE)' -c 'CREATE DATABASE remedy'
# No restart for the write token is needed here: the pod is started again anyway, after the token exists.
k -n "$NS" scale deployment/remedy-server --replicas=1
k -n "$NS" rollout status deployment/remedy-server --timeout=300s
echo "the database is empty again"
