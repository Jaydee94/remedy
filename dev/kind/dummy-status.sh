#!/bin/sh
# make dummy-status: the state of the dummy setup as plain `key: value` lines, for a person or an agent.
set -eu
. "$(dirname "$0")/lib.sh"
need kubectl helm curl jq
if ! kind get clusters 2> /dev/null | grep -qx "$CLUSTER"; then
  echo "cluster: none"
  exit 1
fi
echo "cluster: $CLUSTER"
if ! helm --kube-context "$CTX" -n "$NS" status "$RELEASE" > /dev/null 2>&1; then
  echo "release: not installed"
  exit 1
fi
echo "release: $(helm --kube-context "$CTX" -n "$NS" status "$RELEASE" -o json | jq -r '.info.status + " (revision " + (.version|tostring) + ")"')"
echo "server: $(k -n "$NS" get deployment remedy-server -o jsonpath='{.status.readyReplicas}/{.spec.replicas} ready, image {.spec.template.spec.containers[0].image}')"
echo "runner: $(k -n "$NS" get pod remedy-runner-0 -o jsonpath='{.status.phase}, restarts {.status.containerStatuses[0].restartCount}' 2>/dev/null || echo 'no pod')"
# The hook Job deletes itself when it succeeds, so the proof of a working refresher is the token and the CronJob's clock.
if [ "$(k -n "$NS" get secret remedy-write-token -o jsonpath='{.data.token}' 2> /dev/null | wc -c)" -gt 0 ]; then
  echo "write token: present"
else
  echo "write token: missing"
fi
echo "token refresher: last scheduled success $(k -n "$NS" get cronjob remedy-token-refresh -o jsonpath='{.status.lastSuccessfulTime}' 2> /dev/null | grep . || echo never)"
echo "database volume: $(k -n "$NS" get pvc remedy-data -o jsonpath='{.status.phase}' 2>/dev/null || echo none)"
if [ -d "$CLAUDE_DIR" ]; then login_dir=present; else login_dir=missing; fi
echo "login directory: $CLAUDE_DIR ($login_dir)"
if [ -f "$DUMMY_ENV" ]; then
  load_dummy_env
  echo "url: $REMEDY_URL ($(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$REMEDY_URL/healthz" || true) on /healthz)"
  echo "secrets: $DUMMY_ENV"
else
  echo "url: unknown (no $DUMMY_ENV)"
fi
