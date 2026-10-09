#!/bin/sh
# Starts the kind cluster for trying Remedy's cluster tools with a server on the host: demo workloads, two service
# accounts with their roles, a pinned Argo CD, and the files the control plane needs.
# Usage: dev/kind/up.sh [output directory]
# The output directory (default ~/remedy-kind) gets the CA, one token file per identity and an env.sh. It is outside
# the repository on purpose: it holds credentials. For the chart-installed setup use dummy-up.sh instead; the two do not
# share a running cluster (the account names collide).
set -eu
[ $# -gt 0 ] && REMEDY_KIND_DIR=$1
. "$(dirname "$0")/lib.sh"
need docker kind kubectl openssl

ensure_cluster
install_demo
k apply -f "$KIND_DIR/rbac.yaml"
install_argocd
k apply -f "$KIND_DIR/argocd-rbac.yaml"

server=$(k config view --raw --minify -o jsonpath='{.clusters[0].cluster.server}')
k config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' | openssl base64 -d -A > "$OUT/ca.crt"
(umask 077
 k -n remedy-system create token remedy-read --duration 24h > "$OUT/read.token"
 k -n remedy-system create token remedy-write --duration 24h > "$OUT/write.token"
 cat > "$OUT/env.sh" <<EOF
export REMEDY_K8S_API='$server'
export REMEDY_K8S_CA_FILE='$OUT/ca.crt'
export REMEDY_K8S_READ_TOKEN_FILE='$OUT/read.token'
export REMEDY_K8S_WRITE_TOKEN_FILE='$OUT/write.token'
export REMEDY_K8S_WRITE_NAMESPACES='demo'
EOF
)
echo
echo "Ready. The tokens are good for 24 hours; run this script again for new ones."
echo "  . $OUT/env.sh   # then start remedy-server"
