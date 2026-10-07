#!/bin/sh
# make dummy-logout: removes the CLI's login from the host directory, after a confirmation. This is the only script that
# touches the directory's content, and it only deletes.
set -eu
. "$(dirname "$0")/lib.sh"
check_out_dir
[ -d "$CLAUDE_DIR" ] || { echo "there is no login directory at $CLAUDE_DIR"; exit 0; }
printf "Remove the CLI login in %s? [y/N] " "$CLAUDE_DIR"
read -r answer
[ "$answer" = y ] || { echo "kept"; exit 1; }
find "$CLAUDE_DIR" -mindepth 1 -delete
echo "removed the login; make dummy-login makes a new one"
