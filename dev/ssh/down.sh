#!/bin/sh
# Removes the testbed container and its image. The key and env.sh in ~/remedy-ssh stay; they hold nothing of the maintainer's.
docker rm -f remedy-ssh-testbed >/dev/null 2>&1 || true
docker rmi remedy-ssh-testbed:dev >/dev/null 2>&1 || true
echo "The testbed is gone."
