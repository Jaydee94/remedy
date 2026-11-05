# The webhook source

A webhook lets a tool that is not polled report an incident to Remedy: it sends one HTTP request per event, and the event opens, updates or resolves an incident of the source "Webhook". The incident is diagnosed like an alert (the outage responder: the same tools, the same approvals). Spec: [`../specs/2026-10-21-self-hosted-for-others-design.md`](../specs/2026-10-21-self-hosted-for-others-design.md) section 6; plan: [`../plans/self-hosted-7c-webhook-source.md`](../plans/self-hosted-7c-webhook-source.md).

**State: implemented and tested with fakes, and checked once with `curl` and a browser against a throwaway server ([`../research/webhook-browser-check.md`](../research/webhook-browser-check.md)). It has not been tried with a real sender (Grafana, Uptime Kuma, an Alertmanager webhook receiver): section 9.**

## 1. When to use it

- The tool can send an HTTP request when something happens, and cannot be polled for "everything that is wrong now". If it can be polled and speaks the Alertmanager API, use the [Alertmanager source](alertmanager.md): it resolves by absence, which a webhook cannot.
- Grafana's embedded Alertmanager and VictoriaMetrics' `vmalert` may already be readable by the Alertmanager source. Nobody has checked; if one of them is, that is the better way.

## 2. Open the door

No route exists until the listener is switched on: the public and the internal port never serve `/webhook/v1/`. The listener serves only `POST /webhook/v1/{endpoint}` and `GET /healthz`.

- Binary: `REMEDY_WEBHOOK_ADDR=:8082` (a port of its own, not the public and not the internal one). Without it Setup says "none: the webhook source has no route".
- Chart: `webhook.enabled: true` and `webhook.from` (who may reach it: required, an empty list in a NetworkPolicy would admit everybody; the render fails and shows examples). A sender inside the cluster needs no Ingress: use the Service `remedy-server-webhook:8082`. For a sender outside, `webhook.ingress.{enabled,host,className,tls.secretName}` makes an Ingress for the path `/webhook/v1/` only, so the UI stays off the internet.

The listener has real timeouts (header 10 s, read and write 15 s, idle 60 s, header size 16 KiB) because a body arrives from the network.

## 3. Make an endpoint

Setup, block "Webhooks": give a name (1 to 40 lower-case letters, digits or dashes) and press Add. The token is shown once; copy it into the sender's configuration. Remedy keeps only its hash and cannot show it again. If it is lost, rotate it.

Each endpoint has: a switch (off: every request is refused as if the endpoint did not exist), "Diagnose on my own" (off by default: a leaked token must not be able to spend your quota or money; a click diagnoses; when it is on the ordinary daily limits and, in key mode, the budget apply), the silence limit (default 24 hours, 0 = only an explicit "resolved" resolves, otherwise at least 5 minutes) and the limits of section 6. The page has the switches (accept events, diagnose on my own), Rotate and Remove; the silence limit and the two rate limits are changed through the admin API only, `PATCH /api/webhooks/{id}` with `resolveAfterSeconds`, `ratePerMinute` or `newPerHour` (session and CSRF header, like hosts).

## 4. The request

```sh
curl -sS -X POST "https://YOUR-HOST/webhook/v1/grafana" \
  -H "Authorization: Bearer $REMEDY_WEBHOOK_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"version":1,"key":"disk-full/nas","status":"firing","title":"Disk almost full on nas","severity":"warning","labels":{"instance":"nas"},"annotations":{"summary":"90% used"},"url":"https://monitor.example/alerts/42","startsAt":"2026-10-21T08:00:00Z"}'
```

The token goes in the `Authorization` header only. It is never accepted in the URL (proxies log URLs), so a tool that cannot set a header cannot use this endpoint.

| Member | Rule |
|---|---|
| `version` | required, must be `1` |
| `key` | required, 1 to 200 printable characters (no control, invisible or non-ASCII space characters), and nothing that looks like a secret. The same key is the same incident: **a sender must send the same key for the same problem**. |
| `status` | required, `firing` or `resolved` |
| `title` | required for `firing`; cleaned to one line, redacted, cut to 200 characters |
| `severity` | `critical`, `warning`, `info` or `none` (default `none`); only the first two can be diagnosed on their own |
| `labels` | object of strings, at most 30 kept (key 64, value 300). **They also choose the host:** the host rules of Hosts ("Alerts of this host") are matched against them. |
| `annotations` | object of strings, at most 20 kept (value 1000) |
| `url` | `http` or `https` only |
| `startsAt` | RFC 3339; an event older than the "oldest alert I diagnose on my own" setting is not diagnosed on its own |

The body is at most 64 KiB, strictly decoded: an unknown member, a wrong type, a second JSON value or a `null` where a string belongs is a `400` that names the member (never repeats a value). The answer is `200 {"result": "opened" | "updated" | "resolved" | "ignored"}`. A repeat of the same body is a touch: it refreshes the incident's last-seen time and details and writes no activity entry; `resolved` for an unknown or already resolved key is `200 ignored`; a `firing` after a resolve reopens the incident (it is not diagnosed again on its own).

Statuses: `401` (unknown endpoint, a disabled one and a wrong token are the same answer), `415`, `413`, `400`, `429` with `Retry-After`, `503` (too many requests at once), `500` (the database failed: the sender should retry; the key makes a retry safe).

## 5. Senders

The body is fixed on purpose: there is no mapping language, because a mapping decides what becomes an incident. The sender builds the body. Whatever your tool calls it (a custom payload, a body template, a script) it must produce the JSON above, with a stable `key` and `status` `firing` while the problem exists. `curl` is the one recipe that has been run. **Not tried against the real tool:** Grafana (a contact point of type webhook with a custom payload), Uptime Kuma (a webhook notification with a custom body), an Alertmanager `webhook_configs` receiver (its body is a different shape and needs a small translator in between).

A sender that repeats while the problem lasts (Alertmanager's `repeat_interval`, Grafana's repeat) keeps the incident alive. A sender that fires once needs a long silence limit, or 0 and an explicit `resolved`.

## 6. Limits

All per endpoint, never per IP (Remedy does not trust `X-Forwarded-For`, so behind a proxy every request has the proxy's address and a limit per address would let one sender lock out the others):

- 60 requests per minute with a burst of 120 (`429`);
- 30 new incidents per hour (`429` for a new key only; an update and a `resolved` always pass this limit, so a flood of new keys cannot stop a resolve). A key that comes back after a resolve counts as new, so a sender that flaps more than 30 times an hour is limited on its `firing` events. The rate limit above is one bucket per endpoint: a flood made with a leaked token drains the bucket that the real sender's `resolved` events need too;
- at most 8 requests at once on the listener (`503`, answered before anything is read from the database). A request without a valid token still costs one small database lookup before it is refused, so up to 8 such lookups can run at once next to the control plane's other work (the pool has 10 connections by default); put a rate limit at the reverse proxy if the listener is on the internet.

The first two are fields of the endpoint (1 to 6000 a minute, 1 to 1000 an hour). The counters on Setup ("2 accepted, 1 rejected since the last start", and the last rejection) are in memory and reset when the control plane restarts. A rejected request writes nothing to the database or to the activity log.

## 7. Silence

A sweeper (about once a minute) resolves the unresolved webhook incidents whose `last_seen` is older than the silence limit of their endpoint, with the reason "expired"; the thread says "was not reported for 24 hours", not "fixed". An incident you ignored is kept alive as long as the sender still reports it (an ignored alert of the Alertmanager source is left alone for another reason: it resolves by absence). An incident that is being diagnosed is left until its run has ended. An incident whose endpoint was deleted uses 24 hours.

## 8. Behind a reverse proxy, and a leaked token

- Terminate TLS at the proxy (the token is a bearer secret; without TLS it crosses the network in clear, acceptable only inside a cluster), restrict the path to `/webhook/v1/`, set a body limit as a second line, and filter by source address if the sender's addresses are known. A body logged by the proxy holds titles and labels; the token is in a header: do not log headers.
- A leaked token lets a stranger open incidents (bounded by the limits above) and, with a host rule matching a label they choose, aim a read-only diagnosis at that host. It cannot change anything: every change still needs your approval. Revoke it by rotating the token, but note that the old one stays valid for 24 hours (so a sender can be changed without a gap): to end that overlap at once, switch the endpoint off (this clears the previous token for good; switching it on again does not bring it back), or remove the endpoint. A removed endpoint keeps its incidents.

## 9. Not verified

- A real sender (section 5), and a real reverse proxy in front of the listener.
- The chart's Ingress and NetworkPolicy against a real controller (only their render is tested, `deploy/webhook_test.go`).
- Whether Grafana's or `vmalert`'s Alertmanager-compatible endpoints already work with the Alertmanager source (section 1).
- Run in a real cluster: write the result to `docs/research/webhook-real-run.md`, and until then do not call the source tried.
