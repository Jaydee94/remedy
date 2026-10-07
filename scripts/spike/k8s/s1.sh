#!/bin/sh
# THROWAWAY (spike S1). Shows whether a non-root pod can write into a host directory that kind mounts, whether the
# host can read what it wrote, and whether it survives a new cluster. It only touches ~/remedy-kind-spike and the
# kind cluster remedy-spike. Usage: scripts/spike/k8s/s1.sh [mode]   (mode is the host directory's mode, default 700)
set -eu
MODE=${1:-700}
DIR=$HOME/remedy-kind-spike
HERE=$(cd "$(dirname "$0")" && pwd)
CTX=kind-remedy-spike
k() { kubectl --context "$CTX" "$@"; }

rm -rf "$DIR"; mkdir -p "$DIR"; chmod "$MODE" "$DIR"
echo "host dir: $(ls -ld "$DIR")"

make_cluster() {
  sed "s|__HOSTDIR__|$DIR|" "$HERE/s1-kind.yaml" > "$DIR/../remedy-kind-spike.yaml"
  kind create cluster --config "$DIR/../remedy-kind-spike.yaml"
  k apply -f "$HERE/s1-storage.yaml"
  # the namespace's default service account is created a moment later; a pod made before it exists is refused
  i=0; until k -n spike get serviceaccount default >/dev/null 2>&1; do i=$((i+1)); [ "$i" -lt 30 ] || break; sleep 1; done
}

echo "== first cluster"
make_cluster
k apply -f "$HERE/s1-pod.yaml"
k -n spike wait --for=jsonpath='{.status.phase}'=Succeeded pod/writer --timeout=120s || true
k -n spike logs writer
echo "== on the host after the pod"
ls -lan "$DIR" "$DIR/claude" 2>&1 || true
cat "$DIR/claude/probe.txt" 2>&1 || echo "the host cannot read the file"
echo "== inside the kind node"
docker exec remedy-spike-control-plane ls -ln /mnt/remedy-claude 2>&1 || true

echo "== delete and create the cluster again"
kind delete cluster --name remedy-spike
ls -lan "$DIR/claude" 2>&1 || echo "the files are gone"
make_cluster
k apply -f "$HERE/s1-pod.yaml"
k -n spike wait --for=jsonpath='{.status.phase}'=Succeeded pod/writer --timeout=120s || true
k -n spike logs writer
echo "== cleanup"
kind delete cluster --name remedy-spike
rm -f "$DIR/../remedy-kind-spike.yaml"
echo "left: $(ls -A "$DIR" | tr '\n' ' ')   (the directory $DIR stays so that you can inspect it; remove it by hand)"
