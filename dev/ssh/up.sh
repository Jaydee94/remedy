#!/bin/sh
# The SSH testbed of the host tools: a container with a real sshd and a real sudo. See README.md.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out="${REMEDY_SSH_DIR:-$HOME/remedy-ssh}"
name=remedy-ssh-testbed
image=remedy-ssh-testbed:dev

mkdir -p "$out"
[ -f "$out/id_ed25519" ] || ssh-keygen -q -t ed25519 -N '' -f "$out/id_ed25519"
docker build -q -t "$image" "$here" >/dev/null
docker rm -f "$name" >/dev/null 2>&1 || true
# Bound to the loopback only. Without REMEDY_SSH_BIND_PORT docker picks a free high port.
docker run -d --name "$name" -p "127.0.0.1:${REMEDY_SSH_BIND_PORT:-}:22" "$image" >/dev/null
port=$(docker port "$name" 22/tcp | head -n 1 | sed 's/.*://')

for u in remedy fishy; do
  docker exec "$name" sh -c "mkdir -p /home/$u/.ssh && chmod 700 /home/$u/.ssh && chown $u:$u /home/$u/.ssh"
  docker cp "$out/id_ed25519.pub" "$name:/home/$u/.ssh/authorized_keys"
  docker exec "$name" sh -c "chown $u:$u /home/$u/.ssh/authorized_keys && chmod 600 /home/$u/.ssh/authorized_keys"
done

# Wait until sshd answers with its key.
tries=0
until ssh-keyscan -T 2 -p "$port" 127.0.0.1 2>/dev/null | grep -q ssh-; do
  tries=$((tries + 1))
  if [ "$tries" -gt 20 ]; then
    echo "sshd of the testbed does not answer on 127.0.0.1:$port" >&2
    exit 1
  fi
  sleep 1
done

cat >"$out/env.sh" <<EOF
export REMEDY_SSH_HOST=127.0.0.1
export REMEDY_SSH_PORT=$port
export REMEDY_SSH_KEY_FILE=$out/id_ed25519
EOF
echo "The testbed runs on 127.0.0.1:$port. Then: . $out/env.sh && go test -tags sshlive ./internal/sshhost -run Live -v"
