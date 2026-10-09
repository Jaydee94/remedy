#!/bin/sh
# Tests of the pure decision functions of the smoke test. Run: sh dev/kind/smoke-lib_test.sh (needs jq).
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
. "$HERE/smoke-lib.sh"
command -v jq > /dev/null || { echo "jq is needed" >&2; exit 1; }

fails=0
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# eq <name> <expected> <actual>
eq() {
  if [ "$2" = "$3" ]; then echo "ok    $1"; else echo "FAIL  $1"; echo "  want: $2"; echo "  got:  $3"; fails=$((fails + 1)); fi
}
# yes <name> <command...>: the command must succeed. no <name> <command...>: it must fail.
yes() { n=$1; shift; if "$@" > /dev/null 2>&1; then echo "ok    $n"; else echo "FAIL  $n (should succeed)"; fails=$((fails + 1)); fi; }
no()  { n=$1; shift; if "$@" > /dev/null 2>&1; then echo "FAIL  $n (should fail)"; fails=$((fails + 1)); else echo "ok    $n"; fi; }

# The shape of GET /api/approvals (internal/server/approvals.go, callView): arguments are the agent's JSON as stored, an
# object, or a string when they were not valid JSON or too long (store: storable).
cat > "$WORK/approvals.json" <<'JSON'
[
 {"id":11,"runId":"r1","tool":"cluster_rollout_restart","kind":"mutating","arguments":{"kind":"deployment","namespace":"demo","name":"web"},"status":"waiting","decision":"pending","waiting":true},
 {"id":12,"runId":"r1","tool":"cluster_rollout_restart","kind":"mutating","arguments":{"kind":"deployment","namespace":"other","name":"web"},"status":"waiting","decision":"pending","waiting":true},
 {"id":13,"runId":"r1","tool":"cluster_delete_pod","kind":"mutating","arguments":{"namespace":"demo","name":"web-1"},"status":"waiting","decision":"pending","waiting":true},
 {"id":14,"runId":"r2","tool":"cluster_rollout_restart","kind":"mutating","arguments":{"kind":"deployment","namespace":"demo","name":"web"},"status":"waiting","decision":"pending","waiting":true},
 {"id":15,"runId":"r1","tool":"cluster_rollout_restart","kind":"mutating","arguments":{"kind":"deployment","namespace":"demo","name":"web"},"status":"waiting","decision":"approved","waiting":false},
 {"id":16,"runId":"r1","tool":"cluster_rollout_restart","kind":"mutating","arguments":{"kind":"deployment","namespace":"demo","name":"web-extra"},"status":"waiting","decision":"pending","waiting":true},
 {"id":17,"runId":"r1","tool":"cluster_rollout_restart","kind":"mutating","arguments":"not an object","status":"waiting","decision":"pending","waiting":true},
 {"id":18,"runId":"r1","tool":"cluster_rollout_restart","kind":"mutating","arguments":{"kind":"statefulset","namespace":"demo","name":"web"},"status":"waiting","decision":"pending","waiting":true},
 {"id":19,"runId":"r1","tool":"cluster_rollout_restart","kind":"mutating","arguments":{"kind":"deployment","namespace":"demo","name":"web"},"status":"waiting","decision":"pending","waiting":false},
 {"id":20,"runId":"r1","tool":"cluster_rollout_restart","kind":"mutating","arguments":{"namespace":"demo","name":"web"},"status":"waiting","decision":"pending","waiting":true}
]
JSON

eq "restart-web approves only the restart of demo/web, and only for this run, and only a waiting one" \
"approve 11 cluster_rollout_restart
deny 12 cluster_rollout_restart
deny 13 cluster_delete_pod
deny 16 cluster_rollout_restart
deny 17 cluster_rollout_restart
deny 18 cluster_rollout_restart
deny 20 cluster_rollout_restart" "$(smoke_decisions "$WORK/approvals.json" r1 restart-web)"

eq "deny-all approves nothing" \
"deny 11 cluster_rollout_restart
deny 12 cluster_rollout_restart
deny 13 cluster_delete_pod
deny 16 cluster_rollout_restart
deny 17 cluster_rollout_restart
deny 18 cluster_rollout_restart
deny 20 cluster_rollout_restart" "$(smoke_decisions "$WORK/approvals.json" r1 deny-all)"

eq "another run's approvals are not decided" "approve 14 cluster_rollout_restart" "$(smoke_decisions "$WORK/approvals.json" r2 restart-web)"
eq "an unknown mode approves nothing" "deny 14 cluster_rollout_restart" "$(smoke_decisions "$WORK/approvals.json" r2 something)"
eq "an empty list gives nothing" "" "$(echo '[]' > "$WORK/none.json"; smoke_decisions "$WORK/none.json" r1 restart-web)"

# The shape of GET /api/runs/{id}/tool-calls (the same callView). A read tool has an empty decision; a refused call is a
# failed read call (gatekeeper: call.go).
cat > "$WORK/calls.json" <<'JSON'
[
 {"id":1,"runId":"r1","tool":"cluster_pods","kind":"read","arguments":{},"status":"succeeded","decision":"","waiting":false},
 {"id":2,"runId":"r1","tool":"cluster_pod_logs","kind":"read","arguments":{},"status":"failed","decision":"","waiting":false},
 {"id":3,"runId":"r1","tool":"cluster_rollout_restart","kind":"mutating","arguments":{"kind":"deployment","namespace":"demo","name":"web"},"status":"succeeded","decision":"approved","waiting":false}
]
JSON
yes "a succeeded cluster read call counts" smoke_reads_ok "$WORK/calls.json"
echo '[{"id":1,"tool":"cluster_pods","kind":"read","status":"failed","decision":""},{"id":2,"tool":"incident_get","kind":"read","status":"succeeded","decision":""}]' > "$WORK/noreads.json"
no "a failed read and a non-cluster read do not count" smoke_reads_ok "$WORK/noreads.json"
yes "one approved and succeeded restart counts" smoke_restart_done "$WORK/calls.json"
echo '[{"tool":"cluster_rollout_restart","kind":"mutating","status":"denied","decision":"denied"}]' > "$WORK/denied.json"
no "a denied restart is not a restart" smoke_restart_done "$WORK/denied.json"
echo '[{"tool":"cluster_rollout_restart","kind":"mutating","status":"failed","decision":"approved"}]' > "$WORK/failedrestart.json"
no "an approved restart that failed is not a restart" smoke_restart_done "$WORK/failedrestart.json"
echo '[{"tool":"cluster_rollout_restart","status":"succeeded","decision":"approved","arguments":{"kind":"deployment","namespace":"demo","name":"web"}},{"tool":"cluster_rollout_restart","status":"succeeded","decision":"approved","arguments":{"kind":"deployment","namespace":"demo","name":"web"}}]' > "$WORK/twice.json"
no "two restarts are not one" smoke_restart_done "$WORK/twice.json"
echo '[{"tool":"cluster_rollout_restart","kind":"mutating","status":"succeeded","decision":"approved","arguments":{"kind":"deployment","namespace":"other","name":"web"}}]' > "$WORK/otherns.json"
no "a restart of another namespace is not the restart of demo/web" smoke_restart_done "$WORK/otherns.json"
echo '[{"tool":"cluster_rollout_restart","kind":"mutating","status":"succeeded","decision":"approved","arguments":{"kind":"deployment","namespace":"demo","name":"crashy"}}]' > "$WORK/othername.json"
no "a restart of another name is not the restart of demo/web" smoke_restart_done "$WORK/othername.json"
echo '[{"tool":"cluster_rollout_restart","kind":"mutating","status":"succeeded","decision":"approved","arguments":"not an object"}]' > "$WORK/strargs.json"
no "a restart with arguments that are not an object does not count" smoke_restart_done "$WORK/strargs.json"
yes "no denied call" smoke_no_denied "$WORK/calls.json"
no "a denied call is found" smoke_no_denied "$WORK/denied.json"

# The shape of GET /api/runs/{id}: result and failureReason (the latter is omitted when empty).
echo '{"status":"failed","result":"","failureReason":"Not logged in · Please run /login"}' > "$WORK/run-login.json"
yes "not logged in is recognised in the failure reason" smoke_not_logged_in "$WORK/run-login.json"
echo '{"status":"failed","result":"Invalid API key · Please run /login"}' > "$WORK/run-login2.json"
yes "and in the result" smoke_not_logged_in "$WORK/run-login2.json"
echo '{"status":"succeeded","result":"all good"}' > "$WORK/run-ok.json"
no "a good run is not a login problem" smoke_not_logged_in "$WORK/run-ok.json"
echo '{"status":"failed"}' > "$WORK/run-bare.json"
no "a run without result and reason is not a login problem" smoke_not_logged_in "$WORK/run-bare.json"

yes "a restart at the approval's second counts" smoke_restarted_after 2026-10-07T12:00:05Z 1791374405
yes "a restart a second before counts (the resolution is one second)" smoke_restarted_after 2026-10-07T12:00:04Z 1791374405
no  "a restart long before the approval does not" smoke_restarted_after 2026-10-07T11:00:00Z 1791374405
no  "a time that is not a time does not" smoke_restarted_after nonsense 1791374405

[ "$fails" -eq 0 ] && echo "all passed" || { echo "$fails failed" >&2; exit 1; }
