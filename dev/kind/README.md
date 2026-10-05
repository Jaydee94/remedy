# The kind testbed

A throwaway Kubernetes cluster for trying Remedy's cluster tools without touching a real one. It has the workloads the
tools are meant to be tried on, the two identities the control plane uses (a read-only one and one for approved actions),
and a real Argo CD.

You need `docker`, `kind`, `kubectl`, `openssl` and, to record, `jq`.

```sh
dev/kind/up.sh              # a minute or two; writes ~/remedy-kind/{ca.crt,read.token,write.token,env.sh}
. ~/remedy-kind/env.sh      # REMEDY_K8S_API, REMEDY_K8S_CA_FILE, the two token files, REMEDY_K8S_WRITE_NAMESPACES=demo
make build && ./bin/remedy-server    # with the usual REMEDY_ADMIN_PASSWORD, REMEDY_RUNNER_TOKEN and REMEDY_MASTER_KEY
dev/kind/check-rbac.sh      # what each identity may and may not do, shown with curl
dev/kind/down.sh            # removes the cluster and ~/remedy-kind
```

The tokens are service account tokens that are good for 24 hours; run `up.sh` again for new ones. The output directory is
outside the repository on purpose: it holds credentials.

## What is in it

| | |
|---|---|
| `demo/web` | healthy, two pods, one environment variable (`GREETING`) |
| `demo/crashy` | starts, fails and starts again: `CrashLoopBackOff`, exit code 1, a message in its log |
| `demo/badimage` | an image that does not exist: `ErrImagePull`, then `ImagePullBackOff` |
| `demo/chatty` | healthy; its log has a made-up token (`ghp_...`) and an instruction aimed at whoever reads the log |
| `other/web` | healthy, in the namespace that is not in the allowlist |
| `argocd` | Argo CD (the pinned version in `up.sh`, core installation) with the application `guestbook`, which has no automatic sync and so starts `OutOfSync` |
| `remedy-system` | the service accounts `remedy-read` and `remedy-write` and their roles (`rbac.yaml`, `argocd-rbac.yaml`) |

`remedy-read` can `get` and `list` pods, their logs, events, nodes, workloads and Argo CD applications, and nothing else:
no Secrets, no ConfigMaps, no change. `remedy-write` can restart workloads and delete pods in `demo`, and patch Argo CD
applications in `argocd`; it cannot read anything. RBAC cannot say that an Argo CD application may only deploy to `demo`:
that limit is in Remedy's code.

## The recorded test data

`internal/kube/kubetest/testdata` holds what the real API server answered to the requests of the read tools on this
testbed. `internal/kube/kubetest` serves it as a fake API server for the Go tests. To record again:

```sh
dev/kind/record.sh          # needs the testbed to be up and to have settled (the crashing pod needs a minute)
```

A recording changes the names of the pods, the times and the counters, so after recording again the expectations of the
tests that name them need to be looked at: `kubetest.RecordedAt` (the moment ages are measured from) and the ages and
counts in `internal/gatekeeper/tools_cluster_test.go` and `internal/kube/objects_test.go`.

## Checking the recording against the real cluster

```sh
. ~/remedy-kind/env.sh && go test -tags kind -run Live ./internal/kube
```

runs the read methods against the real testbed instead of the recording, and shows that the read identity cannot read
Secrets or change anything. If it fails after a Kubernetes or Argo CD upgrade, the recording is out of date.
