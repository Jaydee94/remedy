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
deploy_release

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
