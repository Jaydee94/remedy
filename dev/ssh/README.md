# The SSH testbed

A throwaway Debian container with a real `sshd`, a real `sudo` and real shells, for proving the host tools against what no
in-process test can fake. `docker` and `systemctl` (and `journalctl`) are small fakes that print what the real ones print; `ps`,
`df`, `free`, `uptime`, `uname` and `ss` are the real ones.

| Account | Shell | Purpose |
|---|---|---|
| `remedy` | bash | the login of the tests; its `sudoers` file is the one the onboarding snippet writes for the container pattern `remedy-*` and the unit `node_exporter.service` |
| `fishy` | fish | a shell that is not POSIX: the canary must refuse it and send nothing |

`dev/ssh/sudoers-remedy` is exactly the output of `sshhost.Snippet` for those rules. `TestTheTestbedSudoersFileIsWhatTheSnippetWrites`
(no Docker needed) fails when the snippet changes and prints the text to put in the file.

Remedy runs `sudo -n -- docker ...` by name, so sudo finds `docker` through its own `secure_path`, not through the login's `PATH`. The
path in a sudoers line (`/usr/bin/docker`) must be where sudo finds it: check with `sudo -l` and `sudo command -v docker`. The testbed's
fakes are in `/usr/bin` for that reason.

You need `docker` and `ssh-keygen`.

```sh
dev/ssh/up.sh                        # builds the image, starts the container on 127.0.0.1 (a free high port), writes ~/remedy-ssh/env.sh
. ~/remedy-ssh/env.sh                # REMEDY_SSH_HOST, REMEDY_SSH_PORT, REMEDY_SSH_KEY_FILE
go test -tags sshlive ./internal/sshhost -run Live -v -count=1 -race
dev/ssh/down.sh                      # removes the container and the image
```

Without `env.sh` the live tests skip. `REMEDY_SSH_DIR` moves the output directory, `REMEDY_SSH_BIND_PORT` pins the port.

The container binds to `127.0.0.1` only. The key `remedy_testbed_ed25519` in `~/remedy-ssh` belongs to the testbed only: `up.sh` generates
it (it never reuses `id_ed25519` or any other key), it opens nothing but this container, and it holds nothing of the maintainer's. `up.sh`
refuses a `REMEDY_SSH_DIR` that is `~/.ssh` or holds a `known_hosts`, `authorized_keys` or `id_*` file. Nothing of the host is mounted
into the container.

`down.sh` removes the container and the image but leaves the state directory behind (the key and `env.sh`). To remove it as well, run
`rm -r ~/remedy-ssh` (or your `REMEDY_SSH_DIR`) yourself.
