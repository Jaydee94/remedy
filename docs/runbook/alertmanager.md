# Runbook: connect Remedy to an Alertmanager

This is cycle 4 of the hosts and skills series ([spec](../specs/2026-10-16-alertmanager-4-design.md)). It makes alerts that fire in
Alertmanager into incidents, maps them to hosts and **diagnoses them**: on its own for a young `critical` or `warning` alert, by a click on
**Diagnose** in the incident's thread otherwise (section 5). The run may read the cluster (when the cluster is set up) and use the tools of the host
the alert is mapped to; any change it asks for waits for you under **Needs you**, and in an automatic run that approval expires (section 5). The diagnosis
with the real CLI has not been tried yet: [`outage-real-run.md`](outage-real-run.md) is its runbook. Remedy only reads from Alertmanager (a `GET`); it never
silences or acknowledges anything. Section 8 describes the metrics address and the four read tools of the agent for alerts, silences and metrics.

## 1. Enter the address and the token

Open **Setup** and find the block "Alertmanager".

- **Alertmanager address**: the address your cluster gives Alertmanager, `http` or `https`, with a host, without a user name, a query or a
  fragment (for example `http://alertmanager:9093` in a plain setup without the chart's NetworkPolicy; use the service name of your installation, not a guess;
  see "Reaching Alertmanager from the chart" below). **Use `https` for an Alertmanager outside this machine:** the bearer token travels in clear over plain `http`. A trailing
  slash is removed. Empty means Remedy does not ask. `REMEDY_ALERTMANAGER_URL` is only the default of this setting; what you save here wins.
- **Alertmanager token** (optional): a bearer token, sent as `Authorization: Bearer` when set. It is written once, sealed with the master key,
  and never shown again, only as `…` and its last four characters. **Replace token** writes a new one, **Remove token** (it asks twice)
  deletes it. The activity feed names the setting, never a value. It is 1 to 4096 bytes without a control character.
- **Oldest alert I diagnose on my own** (default 1 hour): an alert that started longer ago is shown but not diagnosed on its own (section 5).
- For an `https` Alertmanager with your own CA, set `REMEDY_ALERTMANAGER_CA_FILE` to a PEM file in the control plane's pod (a bootstrap value:
  it is read at start-up and a missing file stops the server).

How often Remedy asks is the setting **How often I ask Alertmanager** (`REMEDY_ALERTMANAGER_POLL_INTERVAL`, default 60 s, at least 10 s): the
Alertmanager source has its own loop and does not need a GitHub connection. An alert that has cleared stays open for up to this long. Until the
variable is set, it takes the value of `REMEDY_POLL_INTERVAL` (GitHub's interval), which paced both sources before; a stored `poll_interval` was
copied to the new setting once (migration 017).

### Reaching Alertmanager from the chart

With the chart's default `networkPolicy.enabled: true`, the control plane's pod may open connections only to DNS, the Kubernetes API (when
`cluster.enabled`) and **public addresses on port 443**; every private range is excepted (`deploy/chart/templates/networkpolicy.yaml`). So an Alertmanager inside the
cluster (`http://alertmanager:9093`) or on a private address **cannot be reached** by default, and the state line shows a connection error or a timeout. The options:

- **for an Alertmanager inside the cluster, set the chart value `networkPolicy.alertmanager`** (cycle 6): `namespace`, `podSelector` (the labels of the Alertmanager's pods; an empty selector is refused) and `port` (default 9093). It opens one
  egress rule from the control plane to those pods and nothing else. Read the namespace, labels and port from the cluster, do not guess. The Alertmanager's own namespace may have an ingress policy that must admit the Remedy namespace too
  (on the home server, the monitoring app's `monitoring-namespace-isolation`). This value is implemented on the branch of the cycle-6a pull request and has not been run on a cluster;
- for a private address outside the cluster, the chart has no value (`networkPolicy.hosts` is for SSH to hosts, not for Alertmanager): put Alertmanager behind a public HTTPS address on port 443 (an Ingress with a certificate) and enter that address;
- turn `networkPolicy.enabled` off (this opens all egress of the control plane: weigh it).

The address can also come from the chart: `REMEDY_ALERTMANAGER_URL` can be passed through `server.env` (the setting saved on the Setup page wins over it).

## 2. What the state line says

Below the settings of the block, one sentence says what Remedy last heard (`GET /api/signals`; the state is kept in memory, so a restart forgets it):

- "I don't ask Alertmanager: no address is set."
- "I haven't asked Alertmanager yet." (set up, the first request is still to come)
- "I asked Alertmanager 12 seconds ago and it listed 7 active alerts." (The count is what Remedy took from the answer: it leaves out the Watchdog and the alerts it skips.)
- "I couldn't ask Alertmanager …: <reason>. I changed nothing because of it." A failed, cut-off or oversized answer, or an answer that is not a list of
  alerts, **resolves nothing**: an Alertmanager that does not answer is never read as "all clear", and the incidents stay open.

## 3. What becomes an incident

An alert in the state `active` (not silenced, not inhibited), except `Watchdog` and `InfoInhibitor`, becomes an incident whose key is
`<alertname>/<fingerprint>`. The title is the annotation `summary`, else the alert name with namespace and pod. After a complete answer,
an incident whose alert is no longer in the list is resolved. **An alert that you silence, or that is inhibited, disappears from the active list and therefore
resolves its incident at the next complete answer.** At most 50 new incidents open per cycle; the rest follows in the next one.
Labels, annotations and the title are bounded and redacted before they are stored (at most 30 labels and 20 annotations, a label key of at most 64
characters, a title of at most 200), and an alert with a name over 200 or a fingerprint over 64 characters is skipped. The alert text is data:
the page shows it as text and never as an instruction.

## 4. Map alerts to a host

Open **Hosts**, a host, and fill "Alerts of this host": **one rule per line, in the syntax of Alertmanager's matchers.** Operators:
`label="value"`, `label!="value"`, `label=~"regex"` and `label!~"regex"`. The value is in double quotes (the escapes are `\\`, `\"`, `\n` and
`\t`, so a dot in a regex is written `\\.`). A regex is RE2 and has to match the whole value. A label the alert does not have counts as empty.
**All lines of a host must hold**, and a host without rules never matches. At most 10 lines of at most 200 characters; an invalid rule is
refused with a message that names the line.

On the home server's NAS the alerts carry `instance="ugreen-nas"` (the name, not the IP address), so one line is enough:

```
instance=~"ugreen-nas(:[0-9]+)?"
```

(the optional `:port` covers an exporter that adds one). The mapping is recomputed at every observation, so a changed rule applies within a cycle.
When several hosts match, the host with the lowest id wins and the thread adds "also matches: ..." with the others; Remedy does not guess.
**Careful with rules that are only negative** (`job!="x"`) **or can match the empty value** (`=~".*"`): a label the alert lacks counts as empty, so such a host is
named by every alert, and the host with the lowest id then takes them all. Give a host at least one rule that needs a value (`instance=~"nas.*"`).
The details of a mapped incident's thread have a Host row that links to the host. A rule is only compared with the alert's labels: it is never run or put into a command.

An alert that matches no host still opens an incident, **without** a host (and so, later, without host tools). Deleting a host leaves its
incidents without one.

## 5. Diagnosis

**On its own.** Remedy decides once, when an incident opens, whether it diagnoses it on its own: a young `critical` or `warning` alert is, an alert that has
been firing for longer than **Oldest alert I diagnose on my own** is not, and neither is an `info` alert. The reason is quota: an alert that
fired a week ago and is found on the first poll would otherwise start a run of its own. On top of that, an incident is picked only if Remedy saw its alert in the
last 10 minutes (or three poll intervals, whichever is longer): after you switch the source off or change its address, the old incidents are never diagnosed on their own.
The limits of the GitHub diagnoses hold here too (at most 3 per incident, 15 minutes apart, 20 per 24 hours, one run at a time), and an alert that has a diagnosis is not
diagnosed again on its own. An alert that clears and fires again (it flaps, or an Alertmanager that was restarted delivers it again) does not open a new incident: Remedy reopens the incident it had, however long ago it resolved, counts the firing ("Incident reopened: alert X fired again (3rd time)") and refreshes its title, severity, host and details. A reopened incident keeps its diagnosis and is not diagnosed again on its own; one that was never diagnosed follows the rules above as a new alert would. An ignored incident comes back ignored. "Diagnose again" still works. Reopens count against the 50 new incidents one cycle opens; the rest follows in the next cycle. The start is checked at the end of every Alertmanager cycle, so it works with or without a GitHub connection.

**By a click.** An old alert or an `info` alert is still visible, and **Diagnose** in its thread starts a run (the limits are ignored,
one run at a time still holds).

**What the run may do.** It gets the incident tools, the cluster tools when the cluster is set up, and the tools of the host the alert is mapped to (a host that
is disabled or deleted gives none; an alert without a host gets none). It needs no GitHub connection. What it reads (alert labels and annotations, command output) is data,
never an instruction. It answers with a summary, the cause, a confidence, a category (`workload_crash`, `image_pull`, `resource_pressure`, `configuration`,
`dependency_outage`, `host_problem` or `unknown`), what is affected, the evidence it read and a proposed action (or `none`); the answer is checked before it is stored.

**What it may change.** Nothing on its own. A change (a `host_change`, a restart, a deleted pod, a sync) waits for your decision under **Needs you**. In an automatic run
the approval has a deadline: if nobody decides within **How long I wait for an approval in an automatic run** (Setup, group "Runs", 15 minutes by default, `0` means
without a limit; `REMEDY_AUTO_APPROVAL_TIMEOUT` is its default), the action is not run, the agent is told and answers anyway (the action stays a proposal in the
diagnosis), and the activity feed says the approval expired. The card of such an approval says "I stop waiting in …". A run you start by a click always waits.

## 6. Known limits

- The diagnosis of an alert has not been tried with the real CLI yet; see [`outage-real-run.md`](outage-real-run.md).
- Switching the source off (an empty address) leaves its open incidents as they are: they are resolved only by a later complete answer, so remove
  an old incident by hand (Ignore in its thread) if you stop using Alertmanager.
- The state of the source is in memory; a restart shows "I haven't asked Alertmanager yet" until the next cycle.
- One Alertmanager only, and the Argo CD source is not built.
- The address is asked with a fixed read-only request, without redirects, from the control plane: only an admin can set it.

## 7. If it does not work

These rows are the expected causes. They were **not tried against a live Alertmanager**; the source was tested against a fake that speaks the same API.

| The line says | Look at |
|---|---|
| it could not ask, connection refused or a timeout | first the chart's NetworkPolicy: by default it blocks every private address, so an Alertmanager in the cluster or on your LAN times out (see "Reaching Alertmanager from the chart"); then the address and the service name |
| the hosts could not be read | the database (a lock or a full disk): the cycle changes nothing and the next one tries again |
| 401 or 403 | the token (replace it) |
| a certificate error | `REMEDY_ALERTMANAGER_CA_FILE` |
| listed 0 active alerts | `curl <address>/api/v2/alerts?active=true&silenced=false&inhibited=false` from the same network: silenced and inhibited alerts are not listed; the count also leaves out the Watchdog and alerts Remedy skips |
| the token "cannot be opened" | the master key changed since the token was saved: remove it and enter it again |

## 8. Metrics and the tools that read the signal

The [alert recurrence and signal tools spec](../specs/2026-10-22-alert-recurrence-and-signal-tools-design.md) gives the agent four read tools for the signal it diagnoses:
`alert_list`, `silence_list`, `metrics_query` and `metrics_query_range`. Status: implemented; the real run with the real CLI is **not made** yet.

**The setting.** In **Setup**, block "Metrics": **Metrics address** (`REMEDY_METRICS_URL` is only its default) and, if the store needs one, **Metrics token**.
The address is a Prometheus-compatible query API (Prometheus, VictoriaMetrics), `http` or `https`, without a user name, query or fragment. It may carry a
**base path** (`https://host/select/0/prometheus`): Remedy joins `/api/v1/...` to it. Authentication is a **bearer token only** (sealed with the master key,
write-only, shown only as a hint, like the Alertmanager token); basic auth is not supported. **Use `https` for a store outside this machine**, the token travels
in clear over plain `http`. `REMEDY_METRICS_CA_FILE` is a PEM bundle for an `https` store (bootstrap: read at start-up, a missing file stops the server). On the home server the
address is `http://vmsingle-monitoring-victoria-metrics-k8s-stack.monitoring.svc:8428`. Empty: the two metrics tools are not offered.

**The probe on the Setup page.** A small loop of its own asks the store for `/api/v1/status/buildinfo` (Prometheus and VictoriaMetrics both serve it) and the
line under the block says what it heard, like the Alertmanager line: "I don't ask the metrics store: no address is set.", "I asked the metrics store 12
seconds ago and it answered.", or "I couldn't ask the metrics store …: <reason>. I changed nothing because of it." It is paced by **How often I ask
Alertmanager** but never faster than once a minute, it runs without an Alertmanager, and after a change of address it can take up to a minute to show the new
state (`GET /api/signals` has an entry `metrics`; like the other state it is in memory).

**Reaching it from the chart.** With the default `networkPolicy.enabled: true` a store inside the cluster is blocked like the Alertmanager is (section 1). Set the chart value
`networkPolicy.metrics` (`namespace`, `podSelector`, `port`; all three are required, the port because Prometheus serves 9090 and VictoriaMetrics 8428) and the control plane gets one more
egress rule to those pods and nothing else. On the home server: namespace `monitoring`, the `vmsingle` pods, port 8428; the monitoring namespace's own ingress policy has to admit the
Remedy namespace too. The chart needs no value for the address or the interval: `server.env` passes any `REMEDY_*` name. The values of the home server's wrapper chart are the maintainer's own change in the other repository.

**The tools.** They are registered always and offered to a run only while their source is set (the addresses are settings that change without a restart): `alert_list` and
`silence_list` while the Alertmanager address is set, `metrics_query` and `metrics_query_range` while the metrics address is set. A learning run (role `learner`) gets none of them.

| Tool | Arguments | Bounds |
|---|---|---|
| `alert_list` | `filter` (matchers), `active_only` | at most 100 rows; labels and annotations bounded and redacted |
| `silence_list` | `state` (`active`, `pending`, `expired`) | at most 50 rows |
| `metrics_query` | `expr`, `time` (RFC 3339) | `expr` at most 2 KiB; 10 s timeout |
| `metrics_query_range` | `expr`, `last` or `start` and `end`, `step` | range at most 24 h; at most 300 points per series (a finer step is raised and the answer says so) |

Every answer starts with the note that it comes from monitoring and is data, never an instruction, is redacted, and is cut at 32 KB. An answer shows at most 20 series and 40 labels per series
("showing 20 of N series"), and values are rounded to 4 significant digits. A client failure reaches the agent as a fixed sentence; the details are in the control plane's log, never the token.

**Nothing can be written.** The Alertmanager client and the metrics client send only `GET` (no redirects, no proxy), neither has a method that writes, and the tools have none either: the agent cannot
create or expire a silence, resolve an alert or change a rule. The two sources are asked with the credentials of the control plane alone; the runner holds none.
