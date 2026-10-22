#!/bin/sh
# Removes the test Postgres container and the env file written by up.sh. Nothing else is touched.
here=$(cd "$(dirname "$0")" && pwd)
name="${REMEDY_PG_NAME:-remedy-pg-testbed}"
if command -v docker >/dev/null 2>&1; then
  docker rm -f "$name" >/dev/null 2>&1 || true
fi
rm -f "$here/env.sh"
echo "The test Postgres is gone."
