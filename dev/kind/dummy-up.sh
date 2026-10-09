#!/bin/sh
# make dummy-up: a kind cluster with the demo workloads and Argo CD, both images built and loaded, and the Helm chart
# installed with the real agent. Safe to run again: it upgrades what exists. The CLI login is a separate, one-time step
# (make dummy-login).
set -eu
. "$(dirname "$0")/lib.sh"
need docker kind kubectl helm openssl jq curl make

ensure_cluster
if ! cluster_has_claude_mount; then
  echo "the cluster $CLUSTER has no mount for the login directory (it was made before the dummy setup existed)." >&2
  echo "run make dummy-down, then make dummy-up again; the login directory is kept." >&2
  exit 1
fi
# The host-run testbed (up.sh) applies rbac.yaml, which creates the accounts the chart creates too. Helm would refuse them.
if k -n "$NS" get serviceaccount remedy-read > /dev/null 2>&1 &&
   [ "$(k -n "$NS" get serviceaccount remedy-read -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by}')" != Helm ]; then
  echo "the cluster was started with dev/kind/up.sh, whose accounts collide with the chart's." >&2
  echo "run make dummy-down, then make dummy-up again." >&2
  exit 1
fi

install_demo
install_argocd
k apply -f "$KIND_DIR/dummy-namespace.yaml"
wait_default_serviceaccount
k apply -f "$KIND_DIR/dummy-storage.yaml"
write_dummy_env
new_tag
build_and_load_images
apply_secret
fresh=no
is_fresh && fresh=yes
deploy_release

# On a fresh install the post-install hook fills the Secret remedy-write-token only after the control plane's pod started. The
# pod mounts it optional, and a running pod is expected to show a new Secret file only after the kubelet's next sync, so an
# approved action in that window failed once ("open /var/run/remedy/write/token: no such file"). A restart of the control plane
# starts a pod after the token exists. An upgrade keeps the file, and a restart would end sessions for nothing.
if [ "$fresh" = yes ]; then
  if ! token=$(k -n "$NS" get secret remedy-write-token -o jsonpath='{.data.token}' 2> /dev/null); then
    echo "cannot read the Secret remedy-write-token (kubectl -n $NS get secret remedy-write-token failed)" >&2
    exit 1
  fi
  [ -n "$token" ] || {
    echo "the Secret remedy-write-token holds no token although the install is done: the hook Job may have failed. kubectl -n $NS get jobs; kubectl -n $NS logs job/remedy-token-refresh-hook" >&2
    exit 1
  }
  echo "restarting the control plane once so that its pod starts after the write token exists"
  k -n "$NS" rollout restart deployment/remedy-server
  k -n "$NS" rollout status deployment/remedy-server --timeout=300s || {
    echo "the control plane did not become ready within 300 s after its restart: kubectl -n $NS describe pod -l app.kubernetes.io/component=server. Running make dummy-up again is safe." >&2
    exit 1
  }
fi

load_dummy_env
# The runner reports its login within a few seconds of starting; wait for it, up to a minute.
login=unknown
n=0
while [ "$n" -lt 20 ]; do
  login=$(runner_state 2> /dev/null | jq -r 'select(.connected) | .login' 2> /dev/null || true)
  [ -n "$login" ] && [ "$login" != unknown ] && break
  n=$((n + 1))
  sleep 3
done
case ${login:-unknown} in
  ok) login_line="the runner is logged in" ;;
  missing) login_line="the runner is NOT logged in: run make dummy-login (once; it survives make dummy-down)" ;;
  *) login_line="the runner has not said yet: make dummy-status" ;;
esac

echo
echo "Ready."
echo "  url:       $REMEDY_URL"
echo "  password:  REMEDY_ADMIN_PASSWORD in $DUMMY_ENV"
echo "  login:     $login_line"
echo "  then:      make dummy-smoke   (two real runs on your subscription)"
echo "  status:    make dummy-status  /  logs: make dummy-logs  /  remove: make dummy-down"
