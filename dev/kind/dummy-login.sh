#!/bin/sh
# make dummy-login: the one-time login of the CLI in the runner pod. It runs the unmodified CLI interactively; the login
# is written by the CLI to the state volume, which is the host directory. Nothing here reads it.
set -eu
. "$(dirname "$0")/lib.sh"
need kubectl
k -n "$NS" get pod remedy-runner-0 > /dev/null 2>&1 || { echo "the runner pod does not exist: run make dummy-up" >&2; exit 1; }
echo "Type /login, open the URL in a browser, paste the code, then /exit."
exec kubectl --context "$CTX" -n "$NS" exec -it remedy-runner-0 -c runner -- /opt/claude/claude
