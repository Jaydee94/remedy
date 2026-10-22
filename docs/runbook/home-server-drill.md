# Runbook: the drill on the home server (the real run of cycle 6)

**This has not been run.** It is the runbook of cycle 6 ([spec](../specs/2026-10-21-operation-6-design.md), [plan](../plans/operation-6-home-server.md)): Remedy installed on the maintainer's
single-node k3s home server, reading that lab's Alertmanager, looking at the NAS over SSH, asking for one change and waiting for the maintainer. What the earlier cycles proved on testbeds
(`docs/research/skills-5-real-run.md`, `docs/research/phase-2d-real-run.md`) is not proven here: the install under Argo CD, the NetworkPolicy of k3s's kube-router, the NAS's `sudo` and the lab's
real alerts are all unmeasured. Read every step as expected, not as tried. The record of the run is written to `docs/research/home-server-drill.md` (it does not exist yet).

It costs subscription quota (the drill is one or two real runs, the learning run one or two more), so you start it, on purpose. Plan on an afternoon. The install itself is
[`homelab-deploy.md`](homelab-deploy.md); this runbook says what to do around it and in what order.

## 0. Rules for the whole run

- **Nothing on the server or the NAS runs without the maintainer.** Every command below is run by the maintainer, or by the agent that prepares them only after the maintainer said "go" in that session; that includes
  a read-only `kubectl` over SSH on the server (an earlier permission was for one session). The agent prepares the pull requests, the commands and the record.
- Every command that changes something is marked **changes the cluster** or **changes the NAS** (or **changes the home-server repository** for a pull request there). A command with no such mark reads, or only prepares text.
- The maintainer merges every pull request, in every repository. Nothing is merged for you; nothing is force-pushed.
- **The NAS private key is entered only over an encrypted path:** `kubectl -n remedy port-forward svc/remedy-server 8080:8080` and `http://localhost:8080`, or Tailscale. The Ingress serves plain HTTP on the LAN; a key typed there
  crosses the network in clear. The same goes for any token.
- The NAS is touched only in these places: the account `remedy`, its key line, `/etc/sudoers.d/remedy`, the container `remedy-drill` and `docker stop` / `docker start` of it. The Paperless containers, Portainer and the Ansible-managed parts are not touched.
- The values used below: namespace `remedy`; host name `ugreen-nas`, address `192.168.178.118`, port `22`; account `remedy`; container pattern `remedy-*`; drill container `remedy-drill`; UI host `remedy.homeserver`.

## 1. Remedy's pull request and the release

The chart values `networkPolicy.hosts` and `networkPolicy.alertmanager` and the filtered `docker ps -a` are on the branch of the cycle-6a pull request (`feat: ...`). The install uses the release that its merge makes
([`release.md`](release.md)): note the version `X.Y.Z`. Until that merge, nothing below can start.

The filter was measured against a real docker before the code was written (`docs/research/docker-ps-filter.md`). The command a host with the pattern `remedy-*` runs is
`sudo -n -- docker ps -a --filter name=^remedy-`; for an exact name `remedy-drill` it is `... --filter name=^remedy-drill$`. The `sudoers` line is that exact command and has no `*`, so `sudo` itself refuses a plain `docker ps -a`
and any extra flag.

## 2. Facts from the server (reads the cluster; the maintainer says "go")

Read-only commands over SSH on the server. Write each answer into `docs/research/home-server-drill.md`.

```sh
ssh -i ~/.ssh/id_ed25519 jaydee@192.168.178.127 'sudo kubectl get endpoints kubernetes -o jsonpath="{.subsets[0].addresses[0].ip}:{.subsets[0].ports[0].port}"'   # API server: networkPolicy.apiServer
ssh -i ~/.ssh/id_ed25519 jaydee@192.168.178.127 'sudo kubectl -n monitoring get svc,pods -l app.kubernetes.io/name=vmalertmanager --show-labels'                 # Alertmanager: namespace, pod labels, port
ssh -i ~/.ssh/id_ed25519 jaydee@192.168.178.127 'sudo kubectl get nodes -o jsonpath="{.items[0].status.nodeInfo.architecture}"'                                     # amd64 or arm64: the CLI pin has both
```

- The Alertmanager's service name, namespace, pod labels and port are read from the cluster, not guessed. If the label differs from `app.kubernetes.io/name=vmalertmanager`, use what the output shows in `networkPolicy.alertmanager.podSelector`.
- The three packages `remedy-server`, `remedy-runner` and `charts/remedy` under `ghcr.io/jaydee94` must be public (the cluster cannot pull private ones). Check the visibility on GitHub (profile, Packages); you make a private one public.
- The real cAdvisor series for the NAS: from the server, query the VictoriaMetrics instance for `container_last_seen{instance="ugreen-nas"}` and note the labels: is there a `name` label, and what is its value for a Paperless container? After section 5, step 4 you
  repeat it for `remedy-drill`, and after `docker stop` you look at what the series does. The rule of section 6 is built from this.
- **The database.** Remedy needs a PostgreSQL 16 or newer that it can reach (`docs/specs/2026-10-22-postgres-only-design.md`): which one is the maintainer's decision (an operator such as CloudNativePG as an Argo CD app of the home-server repository, a plain StatefulSet, or a database on another host). Read where it will run: its namespace and pod labels (or its address and port) for `networkPolicy.database`, and whether its own isolation policy admits the `remedy` namespace. A Remedy that already runs on SQLite is reinstalled with an empty database; its data is not migrated ([`postgres.md`](postgres.md), section 10).
- DNS: does `remedy.homeserver` resolve (the Pi-hole wildcard of the other `*.homeserver` names) or does Pi-hole need an entry?

## 3. The install (changes the home-server repository, then the cluster)

The details are in [`homelab-deploy.md`](homelab-deploy.md), sections 3 to 6 (the wrapper chart). In short:

1. **Seal the secrets on the server** (changes nothing but a file on your machine; `kubeseal` reads the controller's certificate). Generate the admin password, runner token and master key with the pipeline of section 4 of the deploy runbook, and seal the database URL as the second Secret of that section (`remedy-database`); the plain values
   are never written to disk. Copy the master key into your password manager now: it seals the SSH login of the NAS too, and losing it means entering every host's login again.
2. **Home-server pull request 1: Remedy** (changes the home-server repository). `argocd/apps/remedy/` with `Chart.yaml` (the dependency on `oci://ghcr.io/jaydee94/charts` at the release `X.Y.Z`, exact), `values.yaml` (everything under `remedy:`, the CLI pin
   copied from `deploy/cli-pin.yaml` of that release with the platform of section 2, `cluster.enabled: true`, `cluster.write.enabled: false`, `database.existingSecret: remedy-database`, `networkPolicy.database` from section 2, `networkPolicy.hosts: [{cidr: 192.168.178.118/32, port: 22}]`, `networkPolicy.alertmanager` from section 2, `ingress` on Traefik for `remedy.homeserver`),
   `templates/namespace.yaml`, `templates/sealedsecret-remedy.yaml`, `templates/sealedsecret-database.yaml`; and a second change in the same pull request: the namespace `remedy` added to the sources that the monitoring app's `monitoring-namespace-isolation` policy admits, or the Alertmanager rule cannot be used.
   Render it locally first (`helm dependency build argocd/apps/remedy && helm template remedy argocd/apps/remedy --namespace remedy`): the server's policy shows the database, SSH and Alertmanager rules and no write-token objects.
3. **Merge, and watch the first sync** (changes the cluster). The maintainer merges; Argo CD syncs. Watch the namespace, `remedy-server` and `remedy-runner-0` reaching Ready, and write down every surprise. What the first sync may teach, all unproven: the probes under kube-router
   (the workaround is in `homelab-deploy.md`, sections 6 and 12), Argo CD building an OCI Helm dependency in its repo server, and the `Namespace` template next to `CreateNamespace`.

## 4. The login and the first look

1. **The one-time login** (changes the cluster: it writes the login to the runner's volume). Section 7 of the deploy runbook:
   `kubectl -n remedy exec -it remedy-runner-0 -c runner -- /opt/claude/claude`, then `/login`. If the form `claude auth login` is tried and does not print the URL, use this one.
2. **Open the UI through the port-forward** (opens a path; changes nothing): `kubectl -n remedy port-forward svc/remedy-server 8080:8080`, then `http://localhost:8080`. Sign in with the admin password. In Setup the runner must show connected and logged in, and the cluster read side on.
3. **Turn automatic diagnosis off before anything else:** in Setup set `diagnose_max_per_day` to `0`. This lab's Alertmanager holds firing alerts now, and every young `critical` or `warning` alert would be diagnosed on its own the first time Remedy reads it.
4. **Enter the Alertmanager address** in Setup (the service as the cluster gave it in section 2, for example `http://<service>.<namespace>:9093`; see [`alertmanager.md`](alertmanager.md)). Wait one poll interval. The state line must say how many alerts it listed. Open the incidents list and write down
   which alerts appeared, and that none was diagnosed. A timeout here means the egress rule or the monitoring namespace's isolation policy.
5. Write down the control plane's log lines for the Alertmanager source and the cluster read side (`kubectl -n remedy logs deployment/remedy-server`).

## 5. The NAS

1. **Add the host in the UI** (through the port-forward): Hosts, add: name `ugreen-nas`, address `192.168.178.118`, port `22`, account `remedy`, container pattern `remedy-*`, no units, alert rule `instance=~"ugreen-nas(:[0-9]+)?"`. The name matters: the
   lab's alerts carry `instance="ugreen-nas"`. Copy the generated snippet.
2. **On the NAS, as the maintainer, by hand (changes the NAS).** Remedy never runs any of this.
   - `command -v docker` first. The snippet names `/usr/bin/docker`; if UGOS puts it elsewhere, change the path in the lines before you write the file. Also check that UGOS reads `/etc/sudoers.d` at all (`grep -n includedir /etc/sudoers`).
   - Make a key for this purpose on your machine (`ssh-keygen -t ed25519 -f ~/remedy-nas-key -C remedy`), create the account `remedy` (not root, not in the `docker` group), and append the key's **public** half to its `~/.ssh/authorized_keys`.
   - Write the `sudoers` file with `visudo -f /etc/sudoers.d/remedy` (so it is checked before it is saved) and `chmod 0440` it. It holds exactly the lines of the snippet, among them
     `remedy ALL=(root) NOPASSWD: /usr/bin/docker ps -a --filter name\=^remedy-` (no star on that line, a backslash before the `=`).
3. **Enter the private key in the UI** (through the port-forward or Tailscale only), then **delete the key file** from your machine. Run the connection test. It shows the NAS's host key: compare its fingerprint with the NAS's own
   (`ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub` on the NAS) and pin it only when they agree. The test must report: reachable, host key matches, not root, not in the `docker` group, `sudo -n` limited to the lines.
4. **Start the drill container on the NAS** (changes the NAS). It is not in Ansible, Portainer or any stack: nothing may restart it behind Remedy's back.

   ```sh
   docker run -d --name remedy-drill --label app=remedy-drill --restart=no busybox sh -c 'while true; do echo "remedy-drill alive $(date)"; sleep 10; done'
   ```

   Then check from the server that cAdvisor sees it with `name="remedy-drill"` (the query of section 2).
5. **A first look through Remedy.** On the Ask page choose the NAS host chip and ask for the output of `docker ps -a`. In the run, only containers that start with `remedy-` must be listed, with a line saying the list is filtered, and no Paperless container.
   If a Paperless name appears, stop here: that is a finding, and the cycle is not safe to continue.

**A host that was added before this cycle.** If a host already has the old `docker ps -a` line in its `sudoers` file, `sudo` now refuses the filtered command: the next `docker ps -a` run fails with a `sudo` refusal. Open the host's page in the UI,
copy the new snippet and apply it again on that host (`visudo -f`, replace the old line). Nothing else of a host is migrated.

## 6. The rule for the drill (changes the home-server repository)

Home-server pull request 2: `argocd/apps/monitoring/templates/vmrule-remedy-drill.yaml`, a `VMRule` named `remedy-drill` in `monitoring`, one rule, severity `critical`, `for: 1m`, labels `instance: ugreen-nas` and `name: remedy-drill` (so the host rule matches),
with a `summary` and a `description`. The expression is built from what section 2 measured, for example `absent(container_last_seen{instance="ugreen-nas",name="remedy-drill"})`; **do not open the pull request before you tried the expression in VictoriaMetrics**,
including what `docker stop` does to the series. After the merge and a reload of vmalert, the alert must **not** be firing while the container runs. If it fires, or the labels differ from what the host rule expects, the drill stops here: correct the rule or the host rule. No run has been spent.

## 7. The drill

1. **Set the limits for the drill:** in Setup `diagnose_max_per_day` to `5` and `alertmanager_auto_max_age` to `10 minutes`.
2. **Stop the container** (changes the NAS), on the NAS, and note the time:

   ```sh
   docker stop remedy-drill
   ```

3. **Watch, and write down the time of each stage** (from the UI and the database):
   - the alert appears in Alertmanager (after the rule's `for: 1m`);
   - an incident opens within one poll interval, with the Host row `ugreen-nas`;
   - the responder starts on its own;
   - it reads `docker ps -a` and `docker logs` of `remedy-drill` without an approval;
   - it asks for `docker start remedy-drill`, and **Needs you** shows the exact command word for word;
   - **you approve** (changes the NAS: the container starts again);
   - the container runs, the alert resolves, the incident closes.
4. **The checks of the record**, from the run's own prompt, tool results and answer (in the UI's run view, the database and the control plane's log):
   - no Paperless container name, no credential and no `192.168.178.118` appears in them;
   - `psql` over the `host_credentials` table shows only the sealed column and the hint, never the key;
   - the control plane's log holds no plain secret of the host.
5. If the run asks for something other than `docker start remedy-drill`: **deny it**. That is a finding for the record, not a failure of the drill's safety; the barriers are the approval and the `sudoers` file.

### The second pass (optional, one more run)

Stop the container again (changes the NAS) and **deny** the approval; once more, let it expire (15 minutes, `auto_approval_timeout`). Both must leave the container stopped and the incident open with a clear account. This is the refused or expired
`host_change` that cycles 2 and 4 left open. Afterwards start the container yourself (`docker start remedy-drill`; changes the NAS) if you want it running for the next step.

## 8. The learning run

On the NAS host's page press "Get to know this host" (costs one or two runs). Review the proposals with their evidence; confirm some, reject some. The profile can only describe what `host_read` can show of `remedy-*` containers and the host's plain reads:
write down how useful that is. Skills are used by later runs only once confirmed ([`skills.md`](skills.md)). The drill came first, with no skills, so what the run did was its own doing.

## 9. Clean up (changes the NAS, then the home-server repository)

1. `docker rm -f remedy-drill` on the NAS.
2. Home-server pull request 3 removes the `VMRule`.
3. Decide which of the drill's settings stay (`diagnose_max_per_day`, `alertmanager_auto_max_age`) and whether the NAS host stays in the inventory. Set `diagnose_max_per_day` to what you want.
4. Finish the record `docs/research/home-server-drill.md` (what was installed, the first sync, the first look, the NAS, the drill with its timings, the second pass, the learning run, findings, what was not covered), and update the status line of
   [`homelab-deploy.md`](homelab-deploy.md) with what is proven now and what is not.

## 10. Abort and take it away

- **Stop everything:** scale `remedy-server` to 0 (changes the cluster; with `selfHeal` Argo CD scales it back, so disable auto-sync first, as in `homelab-deploy.md`, section 11), or delete the app directory in a pull request. Nothing else in the lab depends on Remedy.
- **Take the NAS access away:** delete the host in the UI (the sealed credential goes with it), then on the NAS (changes the NAS) remove `/etc/sudoers.d/remedy`, the key line from `authorized_keys` and the account. Either half alone is enough to stop SSH use; both is clean.
- **The control plane never becomes Ready** (the probes of port 8080 under kube-router, unverified): add the node range to `networkPolicy.public.from`; as a last resort `networkPolicy.enabled: false` for the time of the check, with the exposure that means, said in the record.
- **The alert does not appear in Alertmanager or has other labels than expected:** the drill stops at section 6; correct the rule or the host rule. No run has been spent.
- **The run asks for something other than `docker start remedy-drill`:** deny it (section 7, item 5).
- **A quota surprise:** `diagnose_max_per_day` at `0` keeps automatic diagnosis off; the limits (3 per incident, `diagnose_max_per_day`) bound the rest.
- **A Paperless name, a credential or the NAS address in a run:** stop, delete the host, and write it down. The cycle does not go on until it is understood.

## 11. If it does not work

| Symptom | Look at |
|---|---|
| Argo CD cannot build the chart | the packages are private, the dependency version is not a release, or Argo CD's repo server does not build an OCI dependency (`homelab-deploy.md`, section 6 and 12) |
| the Alertmanager line says connection error or timeout | `networkPolicy.alertmanager` (namespace, labels, port), then the monitoring app's isolation policy, then the address |
| the connection test cannot connect | `networkPolicy.hosts` (the address must be a `/32`), then the NAS's firewall and `sshd` |
| the connection test says the account is in `docker` or is root | do not go on; fix the account on the NAS |
| `docker ps -a` is refused with a `sudo` message | the `sudoers` file has the old unfiltered line, or a different path of `docker`: apply the new snippet again (section 5) |
| the alert fires while the container runs | the rule's expression; measure the series again (sections 2 and 6) |
| no incident is mapped to the NAS | the host rule against the alert's `instance` label ([`alertmanager.md`](alertmanager.md), section 4) |
| an approved `docker start` has no effect | the host changed after the question was asked (an approved call refuses then), or the container name differs from the pattern |
