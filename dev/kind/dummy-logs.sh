#!/bin/sh
# make dummy-logs: follows the logs of the control plane, the runner and whatever else carries the app label.
set -eu
. "$(dirname "$0")/lib.sh"
need kubectl
exec kubectl --context "$CTX" -n "$NS" logs -f --prefix --tail=50 --max-log-requests=8 \
  -l app.kubernetes.io/name=remedy --all-containers
