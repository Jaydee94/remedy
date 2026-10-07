#!/bin/sh
# Removes the kind cluster and the files the scripts generated: the tokens, the env files, the rendered cluster
# definition, dummy.env. It KEEPS ~/remedy-kind/claude, the CLI's login: `make dummy-logout` removes that, after a
# confirmation. This is also `make dummy-down`. Usage: dev/kind/down.sh [output directory]
set -eu
[ $# -gt 0 ] && REMEDY_KIND_DIR=$1
. "$(dirname "$0")/lib.sh"
need kind
check_out_dir

kind delete cluster --name "$CLUSTER"
cleanup_out_dir
echo "removed the cluster and what the scripts generated in $OUT; the login in $CLAUDE_DIR stays"
