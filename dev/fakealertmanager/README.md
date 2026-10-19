# A fake Alertmanager

For browser checks and the real run of cycle 4, on a machine that has no Alertmanager. It serves a file as the answer of `GET /api/v2/alerts`. Never use it in production.

```sh
go build -o /tmp/fakealertmanager ./dev/fakealertmanager
/tmp/fakealertmanager 127.0.0.1:9093 secret-token internal/alertmanager/alertmanagertest/testdata/home-server.json
```

The file is read at every request, so an edit shows at the next poll; the query (`active`, `silenced`, `inhibited`) is ignored. A wrong bearer token gets `401` (an empty token argument turns the check off), any other path `404`, any other method `405`.

To make it fail, create `<alerts.json>.down` (`touch home-server.json.down`): it answers `500` until you remove that file. The log shows one line per request and never the value of the `Authorization` header.

`internal/alertmanager/alertmanagertest/testdata/home-server.json` is a usable alerts file (a hand-written sample, not a recording). Copy it and set `startsAt` of an alert to a time a few minutes ago: Remedy diagnoses only a young `critical` or `warning` alert on its own.
