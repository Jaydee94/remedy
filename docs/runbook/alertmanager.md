# Runbook: connect Remedy to an Alertmanager

This is cycle 4 of the hosts and skills series ([spec](../specs/2026-10-16-alertmanager-4-design.md)). It makes alerts that fire in
Alertmanager into incidents, maps them to hosts and **diagnoses them**: on its own for a young `critical` or `warning` alert, by a click on
**Diagnose** in the incident's thread otherwise (section 5). The run may read the cluster (when the cluster is set up) and use the tools of the host
the alert is mapped to; any change it asks for waits for you under **Needs you**, and in an automatic run that approval expires (section 5). The diagnosis
with the real CLI has not been tried yet: [`outage-real-run.md`](outage-real-run.md) is its runbook. Remedy only reads from Alertmanager (a `GET`); it never
silences or acknowledges anything.

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

How often Remedy asks is the setting **How often I check GitHub** (`REMEDY_POLL_INTERVAL`, default 60 s, at least 10 s): the Alertmanager source
has its own loop and does not need a GitHub connection, but it uses the same interval.

### Reaching Alertmanager from the chart

With the chart's default `networkPolicy.enabled: true`, the control plane's pod may open connections only to DNS, the Kubernetes API (when
`cluster.enabled`) and **public addresses on port 443**; every private range is excepted (`deploy/chart/templates/networkpolicy.yaml`). The chart has no value
for extra egress yet. So an Alertmanager inside the cluster (`http://alertmanager:9093`) or on a private address **cannot be reached** by default, and the state
line shows a connection error or a timeout. The options:

- put Alertmanager behind a public HTTPS address on port 443 (an Ingress with a certificate) and enter that address;
- turn `networkPolicy.enabled` off (this opens all egress of the control plane: weigh it);
- wait for a chart value for extra egress, which is follow-up work of the operation cycle.

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
diagnosed again on its own.

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
