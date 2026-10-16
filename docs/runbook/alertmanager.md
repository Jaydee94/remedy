# Runbook: connect Remedy to an Alertmanager

This is cycle 4a of the hosts and skills series ([spec](../specs/2026-10-16-alertmanager-4-design.md)). It makes alerts that fire in
Alertmanager into incidents and maps them to hosts. **Nothing diagnoses these incidents yet:** an Alertmanager incident is shown under
**Conversations**, and its thread says "I can't diagnose incidents from Alertmanager yet". The responder for outages, the expiry of
approvals in automatic runs and the run with the real CLI come with cycle 4b. Remedy only reads from Alertmanager (a `GET`); it never
silences or acknowledges anything.

## 1. Enter the address and the token

Open **Setup** and find the block "Alertmanager".

- **Alertmanager address**: the address your cluster gives Alertmanager, `http` or `https`, with a host, without a user name, a query or a
  fragment (for example `http://alertmanager:9093` in a plain setup; use the service name of your installation, not a guess). A trailing
  slash is removed. Empty means Remedy does not ask. `REMEDY_ALERTMANAGER_URL` is only the default of this setting; what you save here wins.
- **Alertmanager token** (optional): a bearer token, sent as `Authorization: Bearer` when set. It is written once, sealed with the master key,
  and never shown again, only as `…` and its last four characters. **Replace token** writes a new one, **Remove token** (it asks twice)
  deletes it. The activity feed names the setting, never a value. It is 1 to 4096 bytes without a control character.
- **Oldest alert I diagnose on my own** (default 1 hour): kept for cycle 4b. It is stored now and does nothing until the responder exists.
- For an `https` Alertmanager with your own CA, set `REMEDY_ALERTMANAGER_CA_FILE` to a PEM file in the control plane's pod (a bootstrap value:
  it is read at start-up and a missing file stops the server).

How often Remedy asks is the setting **How often I check GitHub** (`REMEDY_POLL_INTERVAL`, default 60 s, at least 10 s): the Alertmanager source
has its own loop and does not need a GitHub connection, but it uses the same interval.

## 2. What the state line says

Below the settings of the block, one sentence says what Remedy last heard (`GET /api/signals`; the state is kept in memory, so a restart forgets it):

- "I don't ask Alertmanager: no address is set."
- "I haven't asked Alertmanager yet." (set up, the first request is still to come)
- "I asked Alertmanager 12 seconds ago and it listed 7 active alerts."
- "I couldn't ask Alertmanager …: <reason>. I changed nothing because of it." A failed, cut-off or oversized answer, or an answer that is not a list of
  alerts, **resolves nothing**: an Alertmanager that does not answer is never read as "all clear", and the incidents stay open.

## 3. What becomes an incident

An alert in the state `active` (not silenced, not inhibited), except `Watchdog` and `InfoInhibitor`, becomes an incident whose key is
`<alertname>/<fingerprint>`. The title is the annotation `summary`, else the alert name with namespace and pod. After a complete answer,
an incident whose alert is no longer in the list is resolved. At most 50 new incidents open per cycle; the rest follows in the next one.
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
The details of a mapped incident's thread have a Host row that links to the host. A rule is only compared with the alert's labels: it is never run or put into a command.

An alert that matches no host still opens an incident, **without** a host (and so, later, without host tools). Deleting a host leaves its
incidents without one.

## 5. Old alerts

Remedy decides once, when an incident opens, whether it diagnoses it on its own: a young `critical` or `warning` alert would be, an alert that has
been firing for longer than **Oldest alert I diagnose on my own** would not, and neither would an `info` alert. The reason is quota: an alert that
fired a week ago and is found on the first poll would otherwise start a run of its own. It stays visible, and a click would diagnose it. **This is
the design of cycle 4b; today nothing starts either way.**

## 6. Known limits

- Only incidents of GitHub are diagnosed today. Alertmanager incidents are shown, mapped and resolved, nothing more.
- Switching the source off (an empty address) leaves its open incidents as they are: they are resolved only by a later complete answer, so remove
  an old incident by hand (Ignore in its thread) if you stop using Alertmanager.
- The state of the source is in memory; a restart shows "I haven't asked Alertmanager yet" until the next cycle.
- One Alertmanager only, and the Argo CD source is not built.
- The address is asked with a fixed read-only request, without redirects, from the control plane: only an admin can set it.

## 7. If it does not work

| The line says | Look at |
|---|---|
| it could not ask, connection refused or a timeout | the address, and whether the control plane's pod can reach it (a NetworkPolicy, a service name) |
| 401 or 403 | the token (replace it) |
| a certificate error | `REMEDY_ALERTMANAGER_CA_FILE` |
| listed 0 active alerts | `curl <address>/api/v2/alerts?active=true&silenced=false&inhibited=false` from the same network: silenced and inhibited alerts are not listed |
| the token "cannot be opened" | the master key changed since the token was saved: remove it and enter it again |
