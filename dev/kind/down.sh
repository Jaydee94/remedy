#!/bin/sh
# Removes the kind cluster and the credential files up.sh wrote. Usage: dev/kind/down.sh [output directory]
set -eu
OUT=${1:-$HOME/remedy-kind}
kind delete cluster --name remedy-dev
rm -rf "$OUT"
echo "removed the cluster and $OUT"
