#!/bin/sh
# Starts the kind cluster for trying Remedy's cluster tools: demo workloads, two service accounts with their roles, a
# pinned Argo CD, and the files the control plane needs. Usage: dev/kind/up.sh [output directory]
# The output directory (default ~/remedy-kind) gets the CA, one token file per identity and an env.sh. It is outside
# the repository on purpose: it holds credentials.
set -eu

ARGOCD_VERSION=v3.5.3
CLUSTER=remedy-dev
CTX=kind-$CLUSTER
DIR=$(cd "$(dirname "$0")" && pwd)
OUT=${1:-$HOME/remedy-kind}

for tool in docker kind kubectl openssl; do
  command -v "$tool" > /dev/null || { echo "$tool is needed" >&2; exit 1; }
done

if kind get clusters 2> /dev/null | grep -qx "$CLUSTER"; then
  echo "cluster $CLUSTER exists"
else
  kind create cluster --config "$DIR/kind.yaml"
fi
k() { kubectl --context "$CTX" "$@"; }

k apply -f "$DIR/demo.yaml"
k apply -f "$DIR/rbac.yaml"

# Argo CD, the core installation (no UI, no API server): the controller, the repo server and Redis. Server-side
# apply because its CRDs are too large for the annotation a client-side apply adds.
k create namespace argocd --dry-run=client -o yaml | k apply -f -
k apply -n argocd --server-side --force-conflicts \
  -f "https://raw.githubusercontent.com/argoproj/argo-cd/$ARGOCD_VERSION/manifests/core-install.yaml"
k wait --for=condition=Established crd/applications.argoproj.io --timeout=120s
k -n argocd rollout status deployment/argocd-repo-server --timeout=300s
k -n argocd rollout status statefulset/argocd-application-controller --timeout=300s
k apply -f "$DIR/argocd-rbac.yaml"
k apply -f "$DIR/guestbook.yaml"

(umask 077; mkdir -p "$OUT")
chmod 700 "$OUT"
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
