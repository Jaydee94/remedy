# Runbook: the diagnosis of an alert against the real CLI (the real run of cycles 2 and 4)

**This run was made on 2026-10-19** (record: [`docs/research/phase-2d-real-run.md`](../research/phase-2d-real-run.md)), with a real Alertmanager in a container instead of the fake
(section 3). It costs subscription quota, so repeat it only on purpose. Everything it needs is built and tested against a fake `claude`; this run is the proof against the real one.
Plan on about an hour.

## 1. Why

The unit tests cannot show four things:

- **The real CLI with `--json-schema` and the MCP tools together.** The responder for GitHub failures uses a schema without tools; the diagnosis
  of an alert needs both in one run (the outage answer, and the gatekeeper's tools). It is not proven that the CLI returns a valid structured
  answer after it has used tools. See "the one question" below.
- **The host tools over SSH**, with the real CLI deciding which `host_read` to ask for.
- **The cluster tools** on an alert about a workload, with the real CLI.
- **An approval and its expiry** in an automatic run: the line "I stop waiting in …", and what the agent does when it is told that nobody decided.

## 2. The testbeds and the processes

The host tools talk to a host the control plane can reach, and the SSH testbed listens on `127.0.0.1` of this machine. A control plane inside the
dummy setup's cluster cannot reach it (and its NetworkPolicy blocks private addresses), so use a **local control plane and runner**, as the
[cluster runbook](cluster-real-run.md) does. The `claude` CLI must be installed and logged in (`claude` once, `/login`; Remedy never reads the login). The first block runs from the root of the checkout, up to the `cd`; after it,
`<path-to-remedy>` is that root.

```sh
dev/kind/up.sh                 # the cluster testbed: demo/web, demo/crashy, ... ; writes ~/remedy-kind/env.sh
dev/ssh/up.sh                  # the SSH testbed: a container with sshd, sudo and a fake docker; writes ~/remedy-ssh/env.sh
make web-install && make build
# a PostgreSQL for the run, a throwaway container on the loopback (data is lost with it; docker rm -f remedy-realrun-db removes it)
docker run -d --name remedy-realrun-db -p 127.0.0.1:55432:5432 -e POSTGRES_USER=remedy -e POSTGRES_PASSWORD=remedy -e POSTGRES_DB=remedy postgres:16
mkdir -p ~/remedy-outage-run && go build -o ~/remedy-outage-run/fakealertmanager ./dev/fakealertmanager   # all of this from the root of the checkout
mkdir -p ~/remedy-outage-run && cd ~/remedy-outage-run
cat > env.sh <<EOF
export REMEDY_ADMIN_PASSWORD='$(openssl rand -hex 12)'
export REMEDY_RUNNER_TOKEN='$(openssl rand -hex 24)'
export REMEDY_MASTER_KEY='$(openssl rand -base64 32)'
export REMEDY_DATABASE_URL='postgres://remedy:remedy@127.0.0.1:55432/remedy?sslmode=disable' REMEDY_LOG_LEVEL=debug REMEDY_ADDR=127.0.0.1:8080
export REMEDY_POLL_INTERVAL=15s
EOF
chmod 600 env.sh && . ./env.sh && . ~/remedy-kind/env.sh
rpsql() { docker exec -i remedy-realrun-db psql -U remedy -d remedy "$@"; }   # psql inside the container
rdump() { docker exec remedy-realrun-db pg_dump -U remedy -d remedy; }        # a text dump of the database, for the leak check
<path-to-remedy>/bin/remedy-server > server.log 2>&1 &
REMEDY_SERVER_URL=http://127.0.0.1:8080 <path-to-remedy>/bin/remedy-runner > runner.log 2>&1 &
```

`server.log` says that the cluster answers with `actions=true`. Sign in at <http://127.0.0.1:8080>. The dummy setup (`make dummy-up`, `make dummy-login`) does not fit this run: the fake Alertmanager and the SSH testbed are on this machine's loopback, which the pod cannot reach.

**The host.** Under **Hosts** add a host for the SSH testbed: address `127.0.0.1`, the port (`REMEDY_SSH_PORT`) and the private key (the file `REMEDY_SSH_KEY_FILE`) from
`. ~/remedy-ssh/env.sh`, account `remedy`. Run the test, compare the fingerprint and pin the key. In the field "Alerts of this host"
write `instance="ssh-testbed"`.

## 3. The fake Alertmanager and a young critical alert

`dev/fakealertmanager` serves a file as `GET /api/v2/alerts` ([its README](../../dev/fakealertmanager/README.md)). Write two alerts that started a few
minutes ago, one for the host and one for the workload:

```sh
now=$(date -u -v-3M +%Y-%m-%dT%H:%M:%S.000Z 2>/dev/null || date -u -d '3 minutes ago' +%Y-%m-%dT%H:%M:%S.000Z)
cat > alerts.json <<EOF
[
 {"fingerprint":"a1b2c3d4e5f60001","status":{"state":"active"},"startsAt":"$now","generatorURL":"http://prometheus.invalid/graph",
  "labels":{"alertname":"ContainerHighMemory","instance":"ssh-testbed","name":"remedy-drill","severity":"critical"},
  "annotations":{"summary":"Container remedy-drill is close to its memory limit.","description":"remedy-drill on ssh-testbed uses 96% of its memory limit."}},
 {"fingerprint":"a1b2c3d4e5f60002","status":{"state":"active"},"startsAt":"$now","generatorURL":"http://prometheus.invalid/graph",
  "labels":{"alertname":"KubePodCrashLooping","namespace":"demo","pod":"crashy","severity":"critical"},
  "annotations":{"summary":"Pod demo/crashy is crash looping.","description":"Pod demo/crashy restarted 6 times in 15 minutes."}}
]
EOF
./fakealertmanager 127.0.0.1:9093 secret-token alerts.json 2> fakealertmanager.log &
```

In **Setup**, block "Alertmanager", enter `http://127.0.0.1:9093` and the token `secret-token`. The state line says it listed 2 active alerts. Under **Runs**
set "How long I wait for an approval in an automatic run" to `1m` (the value `auto_approval_timeout`; the default is 15 minutes). The runner runs one run at a
time, so the second alert is diagnosed after the first. An alert is diagnosed on its own only when it is `critical` or `warning`, started at most "Oldest alert I diagnose on my own"
(1 hour) before the incident opened, and was seen in the last 10 minutes (or three poll intervals, whichever is longer): a stale file starts nothing. The limits of automatic
diagnosis hold (20 a day by default).

**A real Alertmanager instead of the fake.** The run of 2026-10-19 used a real one. A config with a receiver that does nothing is enough:

```sh
cat > alertmanager.yml <<EOF
route: {receiver: blackhole}
receivers: [{name: blackhole}]
EOF
docker run -d --name remedy-alertmanager -p 127.0.0.1:19093:9093 -v "$PWD/alertmanager.yml":/etc/alertmanager/alertmanager.yml:ro prom/alertmanager:latest
curl -H 'Content-Type: application/json' -d @alerts.json http://127.0.0.1:19093/api/v2/alerts
```

Each alert in `alerts.json` has `labels`, `annotations`, `startsAt` (about a minute ago) and `endsAt` (an hour ahead), the way Prometheus posts them. In **Setup** the address
is `http://127.0.0.1:19093` (no token). A restarted Alertmanager loses its alerts, so Remedy resolves their incidents on the next complete, empty answer; an alert that is
posted again afterwards is a new incident and a new run.

## 4. The steps, and what to look for

Note the time of each step: the record needs them.

1. **The incident.** Within one poll the two alerts are under **Conversations**; the first shows the testbed host in the Host row of its details, the second has none.
2. **The automatic start.** A run starts on its own for the oldest incident. `curl -s -b cookies http://127.0.0.1:8080/api/runs/<id>` (sign in with `POST /api/login`
   as in the [cluster runbook](cluster-real-run.md)) shows `"automatic": true`, `"mcp": true`, `"cluster": true` (when the cluster is set up) and, for the host alert, `"hostCount": 1` (the field is absent for the pod alert). Check that its prompt (`prompt`) holds the alert in blocks and says that the blocks are data.
3. **The reads.** In the run page's tool calls: `cluster_*` reads for the pod alert, `host_read` for the host alert (the fake docker of the testbed knows `ps`, `logs` and `inspect`). No call before the agent's answer should be a change that ran without an approval.
4. **The change that waits.** Look for a `host_change` (for example `docker restart remedy-drill`; the sudoers file of the testbed allows containers named `remedy-*`) or a cluster action. It shows in **Needs you** and in the thread with the command
   word for word, and with the line "I stop waiting in …" (minutes, then seconds). Do **not** decide it for this scenario.
5. **The expiry.** After the minute the approval is gone from **Needs you**; the call ends `abandoned` with `no decision within 1m: the action was not run. Do not ask for it
   again; put it in proposed_action instead.`; the activity feed has "Nobody decided … the approval expired"; the agent is told and **still answers**. A second round: set the
   setting back to `15m`, let the other alert's run ask for a change and approve it before the deadline: it runs once.
6. **The answer.** The thread's diagnosis card shows the outage answer: summary, cause, confidence, category, what is affected, the evidence the agent read, and the
   proposed action as text (a change the agent wanted is only a proposal). The incident is `diagnosed`.
7. **A click.** Edit `alerts.json` so that the alert's `startsAt` is two hours ago and add a new fingerprint: a new incident opens but is not diagnosed on its own;
   "Diagnose" in its thread starts the run (its limits are ignored). Then `touch alerts.json.down` and see the state line report the failed request and no incident resolve; remove the file again.

## 5. The one question

**Does the CLI return a valid structured answer when it also used MCP tools?** The run is `succeeded` and its `output` is the outage object
(`GET /api/runs/<id>` shows `output`, not empty and not `null`) when it does. If the run `failed` with the reason `invalid_output`, or `output` is empty after tool calls,
**stop there**: keep the run's whole `output`, `result` and event stream, the CLI version, and record it. Nothing in the control plane works around this; whether to
split a run into a tool phase and an answer phase is a decision for you afterwards.

## 6. What to record

In `docs/research/phase-2d-real-run.md`: the CLI version (`claude --version`), the date, the duration of each run, the tool calls (names and counts), whether the answer
validated and its category, what an approval looked like (the line, the time, the expiry), the cost shown on the run, any surprise (a refused call, a host tool that
did not fit the table, a prompt the agent did not follow).

A recording of a real answer was made on 2026-10-19: `internal/alertmanager/alertmanagertest/testdata/alertmanager-0.34.1.json`, from the Alertmanager of that run. A recording of
the answer of `GET /api/v2/alerts` of the home server is still open and should be added next to it. It is read-only (one `GET`), but it is your machine: do it only on your explicit go-ahead.

Stop everything: `kill` the server, the runner and the fake by their process IDs (do not use a loose `pkill -f` pattern), and, from the root of the checkout, `dev/ssh/down.sh` and `dev/kind/down.sh`.
