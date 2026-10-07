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

# The marker says that a directory was made by these scripts. check_out_dir refuses anything else before the scripts
# chmod it or delete in it: the output directory is an argument (or REMEDY_KIND_DIR) and a typo such as `down.sh ~`
# must not reach the home directory.
OUT_MARKER=.remedy-kind-dir

# check_out_dir stops (exit 1, a message on stderr) unless $OUT is a path the scripts may chmod and clean: not empty,
# not /, not $HOME, and either missing, empty, marked, made by an earlier version (env.sh and ca.crt, no marker yet) or
# holding nothing but claude (what that version's down.sh left). It creates nothing.
check_out_dir() {
  out=$OUT
  while [ "${out%/}" != "$out" ]; do out=${out%/}; done
  if [ -z "$out" ]; then
    echo "refusing to use '$OUT' as the output directory (empty or /): give a directory of its own, e.g. ~/remedy-kind" >&2
    exit 1
  fi
  if [ -e "$OUT" ] && [ ! -d "$OUT" ]; then
    echo "refusing to use '$OUT' as the output directory (it is not a directory)" >&2
    exit 1
  fi
  [ -d "$OUT" ] || return 0
  # The physical path: find does not descend into a symlink given as its root, so a link to a populated directory
  # would look empty. cd -- also takes a relative path that starts with a dash.
  real=$(cd -- "$OUT" && pwd -P) || { echo "refusing to use '$OUT' as the output directory (cannot enter it)" >&2; exit 1; }
  if [ "$real" = "$(cd "$HOME" && pwd -P)" ]; then
    echo "refusing to use '$OUT' as the output directory (it is your home directory): give a directory of its own, e.g. ~/remedy-kind" >&2
    exit 1
  fi
  [ -z "$(find "$real" -mindepth 1 -maxdepth 1 | head -n 1)" ] && return 0
  [ -e "$real/$OUT_MARKER" ] && return 0
  { [ -e "$real/env.sh" ] && [ -e "$real/ca.crt" ]; } && return 0
  [ -z "$(find "$real" -mindepth 1 -maxdepth 1 ! -name claude | head -n 1)" ] && return 0
  echo "refusing to touch '$OUT': this is not a directory these scripts made (it has no $OUT_MARKER). Use an empty or new directory, or the default ~/remedy-kind." >&2
  exit 1
}

ensure_out_dir() {
  check_out_dir
  (umask 077; mkdir -p "$OUT"; touch "$OUT/$OUT_MARKER")
  chmod 700 "$OUT"
}

# cleanup_out_dir removes what the scripts generated in $OUT and keeps the login (claude) and the marker, so that a
# second run still passes the guard.
cleanup_out_dir() {
  [ -d "$OUT" ] || return 0
  real=$(cd -- "$OUT" && pwd -P) || return 1
  find "$real" -mindepth 1 -maxdepth 1 ! -name claude ! -name "$OUT_MARKER" -exec rm -rf {} +
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
