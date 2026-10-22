# The test Postgres

The Go tests run against a real PostgreSQL 16 (`docs/specs/2026-10-22-postgres-only-design.md`, decision 8). There is no
testcontainers dependency and no skipping: a test that needs the database and cannot reach it fails, with the instruction below.

The tests read the server's URL from `REMEDY_TEST_DATABASE_URL`. `internal/testutil/pgtest` gives each test a schema of its own
(`t_<hex>`, with the connections' `search_path` set to it), drops it when the test ends, and so lets packages run in parallel against
one server. `pgtest.New(t)` returns the DSN; `pgtest.Admin(t)` is the connection that is not scoped, for a test that makes its own
databases.

You need `docker`.

```sh
make pg-up                 # starts postgres:16 on 127.0.0.1 (a free high port), writes dev/pg/env.sh (git-ignored)
. dev/pg/env.sh            # REMEDY_TEST_DATABASE_URL
go test ./... -count=1
make pg-down               # removes the container and env.sh
```

`make test` does the first two steps by itself when `REMEDY_TEST_DATABASE_URL` is unset and docker exists; it leaves the container running
for the next run (`make pg-down` removes it). `make check` calls `make test`, so it works either way. Without docker, `make test` says so and
runs the tests, which then fail where they need the database.

The container is a throwaway: its data directory is a tmpfs, `fsync` is off, `max_connections` is 400 (packages run in parallel, each
test with its own pool). It binds to `127.0.0.1` only, with a random password that lives in `dev/pg/env.sh` (mode 0600) and in the
container's environment. `up.sh` reuses a container that is already running (also one started from another checkout: it reads the port
and password back from the container), so two checkouts share one server and never see each other's tables.

`REMEDY_PG_NAME` (the container's name, default `remedy-pg-testbed`) and `REMEDY_PG_IMAGE` (default `postgres:16`) change what `up.sh`
and `down.sh` act on.

To use a server of your own instead, set `REMEDY_TEST_DATABASE_URL` (`postgres://user:pass@host:5432/db?sslmode=disable`, or a
`key=value` string): the user must be allowed to create and drop schemas in that database. Do not point it at a database that holds
anything you want to keep: the tests only touch the schemas they create, but they do create many.

CI starts the same image as a service of the job `go` and sets the variable (`.github/workflows/ci.yml`); there the server keeps
Postgres' default `max_connections` of 100, so a test's pool should stay small.
