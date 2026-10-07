# The decisions of the smoke test, as pure functions over JSON files. They read files with jq and touch no network, so
# smoke-lib_test.sh can test them. Source this file; it runs nothing.
#
# The shapes are those of the admin API (internal/server/approvals.go, callView): GET /api/approvals and GET
# /api/runs/{id}/tool-calls both answer an array of {id, runId, tool, kind (read|mutating), arguments, status, decision,
# waiting, ...}; GET /api/runs/{id} answers {status, result, failureReason, ...}.

# smoke_decisions <approvals.json> <run id> <mode>
# Prints "approve|deny <call id> <tool>" for every approval of the run that is pending and still waited for. Mode
# "restart-web" approves exactly one thing: the restart of the deployment demo/web. Every other mode, and every other
# request (another tool, another namespace, another name, another kind or none, arguments that are not an object), is
# denied.
smoke_decisions() {
  jq -r --arg run "$2" --arg mode "$3" '
    .[]
    | select(.runId == $run and .decision == "pending" and .waiting == true)
    | (if $mode == "restart-web"
          and .tool == "cluster_rollout_restart"
          and (.arguments | type == "object")
          and .arguments.namespace == "demo"
          and .arguments.name == "web"
          and .arguments.kind == "deployment"
       then "approve" else "deny" end) + " \(.id) \(.tool)"' "$1"
}

# smoke_reads_ok <calls.json>: at least one cluster read call succeeded.
smoke_reads_ok() {
  jq -e '[.[] | select((.tool | startswith("cluster_")) and .kind == "read" and .status == "succeeded")] | length > 0' "$1" > /dev/null
}

# smoke_restart_done <calls.json>: exactly one restart was approved and succeeded, and it was the restart of demo/web.
smoke_restart_done() {
  jq -e '[.[] | select(.tool == "cluster_rollout_restart" and .decision == "approved" and .status == "succeeded")] as $r
    | ($r | length == 1) and ($r[0].arguments | type == "object" and .namespace == "demo" and .name == "web")' "$1" > /dev/null
}

# smoke_no_denied <calls.json>: no call was denied.
smoke_no_denied() {
  jq -e '[.[] | select(.status == "denied" or .decision == "denied")] | length == 0' "$1" > /dev/null
}

# smoke_not_logged_in <run.json>: the run says the CLI is not logged in.
smoke_not_logged_in() {
  jq -e '((.result // "") + " " + (.failureReason // "")) | test("not logged in|/login"; "i")' "$1" > /dev/null
}

# smoke_restarted_after <rfc3339 time> <epoch seconds>: the time is not before the epoch by more than the one-second
# resolution of a restart's annotation.
smoke_restarted_after() {
  t=$(jq -rn --arg t "$1" '$t | fromdateiso8601' 2> /dev/null) || return 1
  [ "$t" -ge $(($2 - 2)) ]
}
