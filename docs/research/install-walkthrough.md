# Walkthrough of the generic install runbook (cycle 7a)

Date: 2026-11-02. Runbook walked: [`docs/runbook/install.md`](../runbook/install.md), from section 3 to section 7 and section 11, on a
throwaway kind cluster. No login was made (it costs subscription quota and needs the maintainer).

## What was walked

| | |
|---|---|
| Cluster | kind v0.33.0, Kubernetes v1.37.0, one node (`remedy-walk`), default storage class `standard` (local-path), kindnet |
| Tools | helm v4.3.1, kubectl v1.37.2, on an arm64 Mac |
| Chart | the local `deploy/chart` at commit `8ed49d6` of the 7a branch, not the published chart: the published 0.17.1 still has the old defaults (the pin as a required value, `networkPolicy.public.from` defaulting to `kube-system`), so the OCI install of the new text can be walked only after the release that carries this change |
| Images | `ghcr.io/jaydee94/remedy-server` and `remedy-runner` 0.17.1 (anonymous pull worked). They do not contain the UI copy of the same branch |
| Values | exactly the YAML of section 5 (extracted from the runbook by `awk`, not retyped) |

## Result, section by section

- **3, the namespace:** both commands work.
- **4, the two Secrets:** `printf ... | kubectl create secret generic ... --from-env-file=/dev/stdin` works without `--dry-run`. The keys
  `admin-password` (24 characters), `runner-token` and `master-key` (44 characters) exist.
- **5 and 6, values and install:** `helm install remedy deploy/chart -n remedy -f values.yaml` ran with no pin and no second file. Pods
  `remedy-postgres-0`, `remedy-runner-0` and `remedy-server-…` were `Ready` 30 seconds after the install. The init container
  `install-cli` installed `2.1.316` for `arm64` from the chart's default pin and checked its SHA-256. The claim `remedy-postgres-backup` stays
  `Pending` until the first nightly dump (the storage class binds on first use); `install.md` now says so.
- **7, the UI:** `kubectl port-forward svc/remedy-server 18080:8080`; `GET /healthz` 200; a wrong password answers 401 and the admin
  password 204; `GET /api/runner` says `connected: true`, `login: "missing"` and `cliVersion: "2.1.316 (Claude Code)"`. In a real browser
  the login page and the Hosts page (empty state, "Add a host") load.
- **11, upgrading:** `helm upgrade ... -f values.yaml` with unchanged values made revision 2 and restarted nothing.
- **The policy:** a pod in another namespace could not reach `remedy-server:8080` (`curl` timed out, exit 28), so kindnet enforces the
  chart's NetworkPolicies and the peer named in `networkPolicy.public.from` is what opens the port.

## The published chart, 0.18.0

After the release of this change (v0.18.0, the same day) the runbook was walked again from the registry on a second throwaway kind cluster, anonymously:
`helm show chart oci://ghcr.io/jaydee94/charts/remedy --version 0.18.0` works, its default `runner.cli` is the pin of 2.1.316 with both checksums, and
`helm install remedy oci://ghcr.io/jaydee94/charts/remedy --version 0.18.0 -n remedy -f values.yaml` with the YAML of section 5 (nothing else) made `remedy-postgres-0`, `remedy-runner-0`
and `remedy-server-…` `Ready` within a minute; `install-cli` installed 2.1.316 for arm64. A render without `networkPolicy.public.from` fails with the message
that names the value and the three presets. The server image 0.18.0 pulls anonymously. The cluster was deleted afterwards.

## Not walked

- **The login** (section 8) and everything after it: a first diagnosis, GitHub, a host.
- **The upgrade from 0.17.1 to 0.18.0** of an existing install (only an upgrade with unchanged values was walked, on the local chart).
- **A real Ingress controller** and TLS: no controller was installed, the UI was reached by port-forward only. The preset peers `kube-system`
  (k3s) and `ipBlock` were rendered by the chart tests, not walked.
- **`cluster.enabled`** and the write side, `networkPolicy.hosts`, an Alertmanager.
- **A CNI that does not enforce policies**, k3s with kube-router, amd64, Helm 3 (the runbook says 3.8 or later; helm 4.3.1 was used).
- Section 12's failure cases were not provoked.

## Findings

1. The `Pending` backup claim looks like a fault to a newcomer. Added a sentence to section 6.
2. The runbook said Remedy is "not walked yet" in its status line; it now links this record.
3. The UI placeholder for a host's address was the maintainer's NAS address (`192.168.178.118`), and the chart's example values used it too. The lab-values
   check does not cover addresses; they were replaced by `192.0.2.x` (RFC 5737) in the placeholder, the chart values and the chart README.
