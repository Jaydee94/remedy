# The webhook source: one check in a browser and with curl

Date: 2026-11-04. Branch `feat/7c-webhook-source`, before the review. A throwaway server (the binary built with `-tags webui`, a scratch database on the test PostgreSQL, `REMEDY_WEBHOOK_ADDR=127.0.0.1:18082`), the UI in Playwright. No real sender, no reverse proxy, no ingress controller, no Kubernetes.

What was done and seen:

- Setup showed "Webhook listener: 127.0.0.1:18082" among the facts set outside the page, and the block "Webhooks".
- Adding the endpoint `grafana` showed its token once (43 characters). After "I have copied it" and a reload the token was gone and the endpoint line read "Diagnosis by a click, resolves after 24 hours of silence".
- `curl` to the listener: no token gave `401 {"error":"unauthorized"}`; a firing body gave `{"result":"opened"}`; the same body again gave `{"result":"updated"}`; a body with `"severity":"high"` gave a `400` naming the member `severity`. The database held one incident (source `webhook`, key `grafana/disk-full/nas`) and one activity entry (`incident_opened`) after the three accepted and rejected requests.
- The Setup line then read "last used just now, 2 accepted, 1 rejected since the last start, last rejection: severity: ...".
- Conversations listed the incident with the source label "Webhook".
- A POST to the webhook path on the public port (18080) returned the UI's HTML fallback (200) and created nothing; `/api/me` on the webhook port was 404.
- Rotate and Remove (each armed by a first click) worked; after the rotation the page said the previous token still works.

Not covered: a real sender; a reverse proxy; the chart in a cluster; diagnosis by a real CLI (the chain test in `internal/app` uses a fake CLI).
