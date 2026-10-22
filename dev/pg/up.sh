#!/bin/sh
# A throwaway Postgres for the Go tests. See README.md.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
name="${REMEDY_PG_NAME:-remedy-pg-testbed}"
image="${REMEDY_PG_IMAGE:-postgres:16}"
envfile="$here/env.sh"

command -v docker >/dev/null 2>&1 || { echo "docker is needed for the test Postgres (or set REMEDY_TEST_DATABASE_URL to a server of your own)." >&2; exit 1; }

# Everything is read back from the container, so a container that another checkout started is reused, not replaced.
write_env() {
  pw=$(docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$name" | sed -n 's/^POSTGRES_PASSWORD=//p')
  port=$(docker port "$name" 5432/tcp | head -n 1 | sed 's/.*://')
  [ -n "$pw" ] && [ -n "$port" ] || { echo "cannot read the password and port of $name" >&2; return 1; }
  umask 077
  cat >"$envfile" <<EOF
export REMEDY_TEST_DATABASE_URL='postgres://remedy:$pw@127.0.0.1:$port/remedy?sslmode=disable'
EOF
  echo "The test Postgres runs on 127.0.0.1:$port. Then: . '$envfile' && go test ./..."
}

# Real queries over TCP: the image's first server (initdb) listens on its socket only, so a check over the socket would
# answer too early.
wait_ready() {
  tries=0
  until docker exec "$name" psql -h 127.0.0.1 -U remedy -d remedy -tAc 'select 1' >/dev/null 2>&1; do
    tries=$((tries + 1))
    if [ "$tries" -gt 60 ]; then
      echo "the test Postgres does not answer; its log:" >&2
      docker logs --tail 20 "$name" >&2 || true
      docker rm -f "$name" >/dev/null 2>&1 || true
      exit 1
    fi
    sleep 1
  done
}

state=$(docker inspect -f '{{.State.Running}}' "$name" 2>/dev/null || echo none)
if [ "$state" = "true" ]; then
  wait_ready
  write_env
  exit 0
fi
docker rm -f "$name" >/dev/null 2>&1 || true

pw=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
# Bound to the loopback only; without a host port docker picks a free high one. The data lives in memory and fsync is off:
# the container is thrown away, and the tests are faster. max_connections is raised because packages run in parallel.
docker run -d --name "$name" \
  -e POSTGRES_USER=remedy -e POSTGRES_PASSWORD="$pw" -e POSTGRES_DB=remedy \
  -p 127.0.0.1::5432 \
  --tmpfs /var/lib/postgresql/data \
  "$image" \
  -c fsync=off -c synchronous_commit=off -c full_page_writes=off -c max_connections=400 >/dev/null

wait_ready
write_env
