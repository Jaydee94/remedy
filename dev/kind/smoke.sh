#!/bin/sh
# make dummy-smoke: two real runs against the dummy setup, with the real agent on your subscription.
#   1. an ad-hoc run with cluster tools asks why a deployment in demo crashes: the run succeeds and read tools were used;
#   2. an ad-hoc run asks to restart demo/web: the approval is decided here, and ONLY the restart of demo/web is approved;
#      the restart really happened.
# It asserts states, never text: the agent's words differ every time. It sends the admin password only to the dummy's URL.
# A run that ends "Not logged in" prints what to do and exits non-zero.
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
. "$HERE/lib.sh"
. "$HERE/smoke-lib.sh"
need curl jq kubectl
load_dummy_env

JAR=$(umask 077; mktemp)
WORK=$(mktemp -d)
trap 'rm -rf "$JAR" "$WORK"' EXIT
TIMEOUT=${SMOKE_TIMEOUT:-900}
APPROVED_AT=0
DENIED=0

fail() { echo "smoke: FAILED: $*" >&2; exit 1; }
say() { echo "smoke: $*"; }

# api <method> <path> [json body]: the body on stdout; fails on anything but 2xx.
api() {
  method=$1 path=$2 body=${3:-}
  : > "$WORK/body" # a curl failure must never show the body of an earlier request
  if [ -n "$body" ]; then
    code=$(curl -sS -o "$WORK/body" -w '%{http_code}' -b "$JAR" -c "$JAR" -X "$method" -H 'X-Remedy-CSRF: 1' \
      -H 'Content-Type: application/json' --data-binary "$body" "$REMEDY_URL$path")
  else
    code=$(curl -sS -o "$WORK/body" -w '%{http_code}' -b "$JAR" -c "$JAR" -X "$method" -H 'X-Remedy-CSRF: 1' "$REMEDY_URL$path")
  fi
  case $code in
    2??) cat "$WORK/body" ;;
    *) echo "smoke: $method $path answered $code: $(cat "$WORK/body")" >&2; return 1 ;;
  esac
}

# start_run <prompt>: prints the id of a new ad-hoc run with the gatekeeper tools and the cluster tools.
start_run() {
  out=$(api POST /api/runs "$(jq -cn --arg p "$1" '{prompt:$p,tools:true,cluster:true}')") || return 1
  printf '%s' "$out" | jq -er .id
}

# wait_run <run id> <mode>: waits until the run ends, deciding its approvals on the way (see smoke_decisions).
wait_run() {
  id=$1 mode=$2 started=$(date +%s)
  while :; do
    api GET "/api/runs/$id" > "$WORK/run.json" || return 1
    case $(jq -r .status "$WORK/run.json") in succeeded | failed) return 0 ;; esac
    api GET /api/approvals > "$WORK/approvals.json" || return 1
    smoke_decisions "$WORK/approvals.json" "$id" "$mode" > "$WORK/decisions"
    while read -r verdict call tool; do
      [ -n "$verdict" ] || continue
      # The time is taken before the approval is sent: the restart happens after it, and its annotation has one second.
      now=$(date +%s)
      # A decision that fails (the agent stopped waiting, or the run just ended) is tried again in the next round, and
      # counts only once it was made.
      if api POST "/api/approvals/$call/$verdict" '{"reason":"dummy-smoke"}' > /dev/null; then
        [ "$verdict" = approve ] && APPROVED_AT=$now
        [ "$verdict" = deny ] && DENIED=$((DENIED + 1))
        say "$verdict $tool (call $call)"
      fi
    done < "$WORK/decisions"
    [ $(($(date +%s) - started)) -lt "$TIMEOUT" ] || fail "the run $id did not end within ${TIMEOUT}s"
    sleep 3
  done
}

# finished <run id>: saves the run and its tool calls, and stops with a clear message when the CLI is not logged in.
finished() {
  api GET "/api/runs/$1" > "$WORK/run.json"
  api GET "/api/runs/$1/tool-calls" > "$WORK/calls.json"
  if [ "$(jq -r .status "$WORK/run.json")" = failed ] && smoke_not_logged_in "$WORK/run.json"; then
    fail "the runner is not logged in. Run: make dummy-login"
  fi
}

say "waiting for the control plane"
n=0
until curl -sf -o /dev/null "$REMEDY_URL/healthz"; do
  n=$((n + 1)); [ "$n" -lt 60 ] || fail "$REMEDY_URL/healthz does not answer. Is the dummy up? make dummy-status"
  sleep 2
done

# The password goes to curl on stdin, as JSON built by jq (a quote or a backslash in it stays valid), never in an argument.
jq -cn '{password: env.REMEDY_ADMIN_PASSWORD}' |
  curl -sf -o /dev/null -c "$JAR" -H 'X-Remedy-CSRF: 1' --data-binary @- "$REMEDY_URL/api/login" || fail "cannot sign in at $REMEDY_URL"
api GET /api/capabilities | jq -e '.cluster.read and .cluster.write and (.cluster.namespaces | index("demo") != null)' > /dev/null ||
  fail "the cluster tools are not on for the namespace demo (GET /api/capabilities)"

state=$(api GET /api/runner) || fail "cannot ask the control plane about its runner"
[ "$(printf '%s' "$state" | jq -r .connected)" = true ] || fail "the runner is not connected to the control plane. make dummy-status"
[ "$(printf '%s' "$state" | jq -r .login)" != missing ] || fail "the runner is not logged in. Run: make dummy-login"

say "run 1: why does something in demo crash?"
RUN1=$(start_run "In the namespace demo of the Kubernetes cluster one deployment keeps crashing. Use your tools to find out which one and why, and tell me the cause in two sentences. Do not change anything.")
wait_run "$RUN1" deny-all
finished "$RUN1"
[ "$(jq -r .status "$WORK/run.json")" = succeeded ] || fail "run 1 ended $(jq -r .status "$WORK/run.json"): $(jq -r '.failureReason // ""' "$WORK/run.json")"
smoke_reads_ok "$WORK/calls.json" || fail "run 1 made no successful cluster read call"
smoke_no_denied "$WORK/calls.json" || fail "run 1 asked for something that was denied: a question must not change anything"
say "run 1 ok: $(jq 'length' "$WORK/calls.json") tool calls"

say "run 2: restart demo/web"
DENIED=0
RESTART_JSONPATH='{.spec.template.metadata.annotations.kubectl\.kubernetes\.io/restartedAt}'
before=$(k -n demo get deployment web -o jsonpath="$RESTART_JSONPATH")
RUN2=$(start_run "Restart the deployment web in the namespace demo of the Kubernetes cluster, then check with your tools that its pods are back up and tell me the result in one sentence.")
wait_run "$RUN2" restart-web
finished "$RUN2"
[ "$(jq -r .status "$WORK/run.json")" = succeeded ] || fail "run 2 ended $(jq -r .status "$WORK/run.json"): $(jq -r '.failureReason // ""' "$WORK/run.json")"
[ "$DENIED" -eq 0 ] || fail "run 2 asked for $DENIED thing(s) other than the restart of demo/web; they were denied"
smoke_restart_done "$WORK/calls.json" || fail "run 2 has no single approved and succeeded restart"
[ "$APPROVED_AT" -gt 0 ] || fail "no approval was decided in run 2"
restarted=$(k -n demo get deployment web -o jsonpath="$RESTART_JSONPATH")
[ -n "$restarted" ] || fail "demo/web has no restartedAt annotation: the restart did not happen"
[ "$restarted" != "$before" ] || fail "the restartedAt annotation of demo/web did not change ($restarted): the restart did not happen"
smoke_restarted_after "$restarted" "$APPROVED_AT" || fail "demo/web was last restarted at $restarted, before the approval"
k -n demo rollout status deployment/web --timeout=120s > /dev/null || fail "the rollout of demo/web did not complete"
say "run 2 ok: demo/web restarted at $restarted"

say "all ok (runs $RUN1 and $RUN2)"
