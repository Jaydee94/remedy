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

echo
echo "Ready."
echo "  url:       http://127.0.0.1:18080"
echo "  password:  REMEDY_ADMIN_PASSWORD in $DUMMY_ENV"
echo "  login:     make dummy-login   (once; the login survives make dummy-down)"
echo "  then:      make dummy-smoke   (two real runs on your subscription)"
echo "  status:    make dummy-status  /  logs: make dummy-logs  /  remove: make dummy-down"
