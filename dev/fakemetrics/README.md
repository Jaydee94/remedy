# A fake metrics store

For browser checks of the Setup page's "Metrics" block, on a machine that has no Prometheus or VictoriaMetrics. It answers `GET /api/v1/status/buildinfo` (what the probe asks), an empty instant query and an empty range query, under any base path. Never use it in production.

```sh
go build -o /tmp/fakemetrics ./dev/fakemetrics
/tmp/fakemetrics 127.0.0.1:8428 secret-token
```

Put `http://127.0.0.1:8428` (and the token) into the Metrics block of the Setup page. A wrong bearer token gets `401` (an empty token argument turns the check off), any other path `404`, any other method `405`.

To make it fail, create `fakemetrics.down` in the temporary directory (`touch "$TMPDIR/fakemetrics.down"`, or `/tmp/fakemetrics.down` where `TMPDIR` is not set): it answers `500` until you remove that file. The probe asks at most once a minute (and at once when the address was empty), so after you make the fake fail or change the token, wait up to a minute, then reload the page. The log shows one line per request and never the value of the `Authorization` header.
