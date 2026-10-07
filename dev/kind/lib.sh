# Shared by the scripts of dev/kind. Source it from a script in this directory: it sets variables and defines
# functions, and runs nothing. The scripts never open a file in $CLAUDE_DIR: it holds the CLI's login, which Remedy
# never reads (scripts/check-login-dir-untouched.sh enforces that).

CLUSTER=remedy-dev
CTX=kind-$CLUSTER
ARGOCD_VERSION=v3.5.3
KIND_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$KIND_DIR/../.." && pwd)
# Outside the repository on purpose: it holds credentials.
OUT=${REMEDY_KIND_DIR:-$HOME/remedy-kind}
CLAUDE_DIR=$OUT/claude
CLAUDE_DIR_MODE=700
DUMMY_ENV=$OUT/dummy.env
NS=remedy-system
RELEASE=remedy

# need <tool>...: stops when a tool is missing.
need() {
  for tool in "$@"; do
    command -v "$tool" > /dev/null || { echo "$tool is needed" >&2; exit 1; }
  done
}

# k runs kubectl against the testbed's cluster.
k() { kubectl --context "$CTX" "$@"; }

ensure_out_dir() {
  (umask 077; mkdir -p "$OUT")
  chmod 700 "$OUT"
}

# The login directory must exist before the cluster does: kind mounts it into the node, and a directory that Docker
# creates itself would belong to root.
ensure_claude_dir() {
  mkdir -p "$CLAUDE_DIR"
  chmod "$CLAUDE_DIR_MODE" "$CLAUDE_DIR"
}

# ensure_cluster creates the kind cluster from kind.yaml, with the host directory mounted, unless it exists.
ensure_cluster() {
  ensure_out_dir
  ensure_claude_dir
  if kind get clusters 2> /dev/null | grep -qx "$CLUSTER"; then
    echo "cluster $CLUSTER exists"
    return 0
  fi
  rendered=$OUT/kind-rendered.yaml
  sed "s|__CLAUDE_DIR__|$CLAUDE_DIR|" "$KIND_DIR/kind.yaml" > "$rendered"
  kind create cluster --config "$rendered"
}

# cluster_has_claude_mount says whether the node of the cluster has the login directory mounted. A cluster made
# before the mount existed does not, and only a new cluster can get one.
cluster_has_claude_mount() {
  docker inspect "$CLUSTER-control-plane" --format '{{range .Mounts}}{{.Destination}}{{"\n"}}{{end}}' 2> /dev/null |
    grep -qx /mnt/remedy-claude
}

install_demo() {
  k apply -f "$KIND_DIR/demo.yaml"
}

# install_argocd installs the core installation of Argo CD (no UI, no API server: the controller, the repo server and
# Redis) and the application guestbook. Server-side apply because its CRDs are too large for the annotation a
# client-side apply adds.
install_argocd() {
  k create namespace argocd --dry-run=client -o yaml | k apply -f -
  k apply -n argocd --server-side --force-conflicts \
    -f "https://raw.githubusercontent.com/argoproj/argo-cd/$ARGOCD_VERSION/manifests/core-install.yaml"
  k wait --for=condition=Established crd/applications.argoproj.io --timeout=120s
  k -n argocd rollout status deployment/argocd-repo-server --timeout=300s
  k -n argocd rollout status statefulset/argocd-application-controller --timeout=300s
  k apply -f "$KIND_DIR/guestbook.yaml"
}
