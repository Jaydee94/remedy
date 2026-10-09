# Hosts, cycle 1: the inventory and access

Status: draft for the maintainer's review, 2026-10-09. Implementation plan: not written yet.
Parent document: [`2026-10-09-hosts-and-skills-design.md`](2026-10-09-hosts-and-skills-design.md) (the umbrella: sections 2, 4 and 11 are binding here).
Models it copies: the GitHub connection (`internal/store/github.go`, `internal/server/github.go`, `internal/secret`) for sealed credentials and the views that never show them.

## 1. Goal and scope

The maintainer can create a host in the web UI, enter how to log in, confirm the host's key, test the connection and see what the account may do. Nothing uses
the host yet: no agent, no tool, no incident. This cycle builds the part that holds the credentials, which is the part to get right before anything can use it.

### In scope

- The `hosts` and `host_credentials` tables, their store methods and the admin API.
- An SSH client package for the control plane (`internal/sshhost`) that connects only to a **pinned** host key and runs a **fixed** set of commands for the test.
- The onboarding snippet (`authorized_keys` line and a `sudoers` file), generated from the host's fields.
- A navigation item **Hosts** with a list page and a detail page.

### Non-goals

- `host_exec`, the classification of commands, the gatekeeper tools and the `runs.hosts` binding (cycle 2).
- Label matchers that map alerts to a host. They are evaluated in cycle 4 and are added there, as a column and a part of the form. (The umbrella lists them
  under this cycle; this spec moves them, and the umbrella is changed to say so.)
- Skills, notes written by an agent, groups or tags of hosts, and any use of the Kubernetes API through the inventory.
- Running the onboarding snippet. Remedy shows it; the maintainer runs it.
- More than one credential per host (the schema allows it; the API does not yet).

## 2. Decisions

| Topic | Decision |
|---|---|
| Where it lives | A navigation item **Hosts** (`/hosts`, `/hosts/:id`), between Ask Remedy and Setup. Setup does not change. |
| Fields of a host | `name` (unique, ignoring case, 1 to 40 characters of `A-Za-z0-9._-`), `address` (a DNS name or an IP), `port` (default 22), `account` (default `remedy`), `notes` (free text, 2000 characters, shown as text), `dockerPattern` (optional), `systemdUnits` (optional list), `enabled`. |
| Credential | One per host: a private key (OpenSSH format; ed25519, ecdsa or rsa; with an optional passphrase) or a password. Both log in with `publickey`, or with `password` and `keyboard-interactive`. |
| Sealing | AES-256-GCM through `internal/secret`, with the additional data `host_credential:<host id>`. The plain value exists only in the request that sets it and in the memory of a connection. |
| What the API shows | The kind, `hint` and (for a key) the public key as an `authorized_keys` line. For a **key** the hint is the key's SHA-256 fingerprint, because the last characters of a PEM block say nothing. For a **password** it is the text `set`: no character of a password is ever shown. (The umbrella says "last characters"; this refines it for the two kinds.) |
| Host key | Pinned in the host's row as the key's public line and fingerprint. A first test **never proceeds past the key exchange**; it reports the fingerprint. Pinning is a separate call that names the fingerprint the maintainer saw. A different key than the pinned one is refused and shown as `key changed`. No option switches the check off. |
| Test | Fixed commands, built from constants, no user input: `id -un`, `id -Gn`, `uname -sr`, `sudo -n -l`. Ten seconds per command, 16 KB per output. It is not an agent run and does not use the gatekeeper. |
| Warnings | The test reports an account that is `root`, one in the group `docker`, and `sudo -n -l` output that allows everything without a password. Warnings are advice; none of them blocks saving. |
| Snippet | Generated on the server from the fields, as plain text, never executed. Every value that is put into it is validated against a strict pattern first (section 5). |
| Delete | Deleting a host deletes its credential. Rows that refer to a host do not exist yet; cycle 2 and 4 add `host_id` columns with `ON DELETE SET NULL` and a test that the delete clears them. |
| Activity | `host_changed`, with a summary that names the host and what happened ("Host nas created", "Host key of nas pinned", "Credential of nas replaced"). No credential, no key and no command in it. |

## 3. Data model (migration 010)

```sql
CREATE TABLE hosts (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  name           TEXT NOT NULL UNIQUE COLLATE NOCASE,
  address        TEXT NOT NULL,
  port           INTEGER NOT NULL DEFAULT 22 CHECK (port BETWEEN 1 AND 65535),
  account        TEXT NOT NULL DEFAULT 'remedy',
  notes          TEXT NOT NULL DEFAULT '',
  docker_pattern TEXT NOT NULL DEFAULT '',
  systemd_units  TEXT NOT NULL DEFAULT '[]',   -- a JSON array of unit names
  enabled        INTEGER NOT NULL DEFAULT 1,
  pinned_key     TEXT NOT NULL DEFAULT '',     -- the host key as an authorized_keys-style line; empty until pinned
  pinned_at      TEXT,
  status         TEXT NOT NULL DEFAULT 'new'
                 CHECK (status IN ('new', 'untrusted', 'ok', 'error', 'key_changed', 'undecryptable')),
  status_detail  TEXT NOT NULL DEFAULT '',
  report         TEXT NOT NULL DEFAULT '',     -- the last test as bounded JSON (section 6); empty before the first test
  checked_at     TEXT,
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL
);

CREATE TABLE host_credentials (
  host_id    INTEGER PRIMARY KEY REFERENCES hosts (id) ON DELETE CASCADE,
  kind       TEXT NOT NULL CHECK (kind IN ('key', 'password')),
  ciphertext BLOB NOT NULL,
  hint       TEXT NOT NULL,
  public_key TEXT NOT NULL DEFAULT '',         -- for kind key: the authorized_keys line, derived at the time the key was set
  updated_at TEXT NOT NULL
);
```

Timestamps are written with the store's own format (nine fractional digits). The primary key of `host_credentials` is the host's id: a second credential per host is a later migration, and the umbrella's retrofit (two identities) adds a `role` column to the key.

The sealed value is JSON: `{"kind":"key","key":"<PEM>","passphrase":"<optional>"}` or `{"kind":"password","password":"<text>"}`. A change of this layout needs a version field, so it has one: `"v":1`.

The additional data is built in one function next to `ConnectionAAD`, `HostCredentialAAD(hostID)`, and, like that one, **its value is never changed**: sealed credentials in a database depend on it.

## 4. Components

| Package | Content |
|---|---|
| `internal/store` | `hosts.go`: `CreateHost`, `GetHost`, `ListHosts`, `UpdateHost`, `DeleteHost`, `SaveHostCredential`, `GetHostCredential`, `DeleteHostCredential`, `PinHostKey`, `UnpinHostKey`, `SaveHostTest`. Inside `inTx` only the `tx`. |
| `internal/sshhost` | The client. `Dial(ctx, Target, Credential, Pin)`: the target is address and port, the credential is the opened value, the pin is a public key or none. `Probe(ctx, Target)` connects for the key exchange only and returns the presented key. `Run(ctx, conn, fixedCommand)` for the test. |
| `internal/sshhost/sshtest` | An SSH server in the test process (`x/crypto/ssh` as a server): configurable host key, accepted key or password, canned answers for commands, and the option to present a different host key after a restart. |
| `internal/server` | `hosts.go`: the handlers of section 7. A `Hosts` seam in `Deps` (like `NewGitHub`) so that server tests need no network. |
| `web/src` | `HostsPage.tsx`, `HostPage.tsx`, `hosts.ts` (pure: what a status, a warning and the list line say), `components/hosts/*`, an item in `shell.ts` and an icon in `navIcons.ts`. |

No new module: `golang.org/x/crypto/ssh` is part of the `golang.org/x/crypto` that `go.mod` already requires.

### The client's rules

- The host key callback is the only authority. With a pin, it accepts exactly that key and nothing else; without a pin, `Dial` is not available and only `Probe` is.
- `HostKeyAlgorithms` is set from the pinned key's type, so that a server offering several types cannot make a correct pin fail, or a new type slip in unnoticed.
- No agent forwarding, no X11, no port forwarding, no pty, no environment sent. `Run` opens one session, runs one command and closes it.
- Timeouts: 10 seconds to connect, 10 seconds per command; the output is cut at 16 KB and the cut is reported.
- One connection at a time per host (a mutex per host id): two clicks on "Test" do not open two sessions.
- Errors are mapped to a closed set (`unreachable`, `host key changed`, `authentication failed`, `timeout`, `refused`). A raw error text never reaches the API, because the library's messages can include the user name and the address.
- The credential is a `secret.Value`; the private key is parsed from it and the buffers are not kept after the connection.

## 5. The onboarding snippet

The snippet has two blocks, both text for the maintainer to read and run as root on the host.

1. **`authorized_keys`**, when the credential is a key: the line for the derived public key, for the account.
2. **`sudoers`**, to be saved as `/etc/sudoers.d/remedy` and checked with `visudo -cf`. It contains only exact lines that follow from the fields:

```
# docker: only containers whose names match the pattern
remedy ALL=(root) NOPASSWD: /usr/bin/docker ps -a
remedy ALL=(root) NOPASSWD: /usr/bin/docker logs --tail * <pattern>
remedy ALL=(root) NOPASSWD: /usr/bin/docker inspect <pattern>
remedy ALL=(root) NOPASSWD: /usr/bin/docker start <pattern>, /usr/bin/docker restart <pattern>, /usr/bin/docker stop <pattern>
# systemd: one line per unit
remedy ALL=(root) NOPASSWD: /usr/bin/systemctl start <unit>, /usr/bin/systemctl restart <unit>, /usr/bin/systemctl stop <unit>
```

Comments in the snippet tell the maintainer what to check: the path of `docker` and `systemctl` on this host (`command -v`), and that reading the journal needs the group `systemd-journal`, not `sudo`.

**What is put into the snippet is validated first**, because it ends up in a file that grants rights:

- `account`: `^[a-z_][a-z0-9_-]{0,31}$`.
- `dockerPattern`: `^[A-Za-z0-9][A-Za-z0-9_.-]*\*?$` — a name, optionally ending in one `*`. No other wildcard, no comma, no space, no newline.
- `systemdUnits`: each `^[A-Za-z0-9][A-Za-z0-9_.@:-]*$` with a suffix that must be one of `.service`, `.timer`, `.socket` or none (then `.service` is written). At most 20.
- Everything is validated again when the snippet is generated, not only when the host is saved, and a value that fails makes the generator return an error instead of text.

The snippet is a suggestion. Its exact lines, and the limits of the wildcards in them, are the maintainer's to read; the text says so. A test generates snippets for a set of hostile inputs (a newline, a comma, `ALL`, a leading dash, a very long value) and asserts that none produces a line that grants more than the table above.

## 6. The test and its report

`POST /api/hosts/{id}/test`:

1. Without a credential: `409`, "enter a login first".
2. Without a pinned key: **only** `Probe`. The answer has `status: "untrusted"` and the presented key's type and SHA-256 fingerprint. Nothing is authenticated, so no credential leaves the control plane.
3. With a pinned key: `Dial`, then the four fixed commands, then the report.

The report (bounded JSON, stored in `hosts.report`, shown by the page):

```jsonc
{
  "at": "2026-10-09T18:30:00.000000000Z",
  "account": "remedy",
  "groups": ["remedy", "users"],
  "system": "Linux 6.8.0",
  "sudo": "limited",            // "none", "limited" or "unrestricted"
  "sudoRules": ["(root) NOPASSWD: /usr/bin/docker ps -a", "..."],
  "warnings": ["The account is in the group docker, which is the same as root."]
}
```

- `sudo` is `none` when `sudo -n -l` fails, `unrestricted` when a rule allows `ALL` as the command (`NOPASSWD: ALL`, with any runas list), else `limited`. The parser is tolerant, tested against sample outputs of several sudo versions, and when it cannot tell it says `limited` and adds the line "could not read the rules" to the warnings; it never says `none` for an output it did not understand.
- `sudoRules` keeps at most 40 lines of at most 200 characters, as text.
- The status becomes `ok`, `error` (with a detail from the closed set of section 4), `key_changed` or `undecryptable` (the master key does not open the credential, like the GitHub connection).

`POST /api/hosts/{id}/hostkey` with `{"fingerprint": "SHA256:..."}` runs `Probe` **again** and pins the key only if its fingerprint equals the one in the request, and answers `409` otherwise. The server keeps no "pending key": the call is the maintainer's statement of what they saw, checked against what the host presents now. `DELETE` on the same path unpins and sets the status back to `untrusted`.

## 7. API

All routes are in the admin domain (session cookie, `X-Remedy-CSRF` on every non-GET).

| Route | Purpose |
|---|---|
| `GET /api/hosts` | The list: id, name, address, status, `credential` kind or none, `checkedAt`. |
| `POST /api/hosts` | Create from the fields. `201` with the view. `409` if the name exists. |
| `GET /api/hosts/{id}` | The view: the fields, the status, the pinned fingerprint, the credential's kind, hint and public key, the last report and the snippet (or the reason it cannot be made). |
| `PATCH /api/hosts/{id}` | Change fields. A change of address or port **clears the pinned key**: a pin belongs to an endpoint. The response says so. |
| `DELETE /api/hosts/{id}` | `204`. |
| `PUT /api/hosts/{id}/credential` | `{kind, key, passphrase}` or `{kind, password}`. The body is read with a limit (16 KB). A key that cannot be parsed, or needs a passphrase that was not given, is `400` with a fixed message; the message never contains the key. |
| `DELETE /api/hosts/{id}/credential` | Removes it. |
| `POST /api/hosts/{id}/test`, `POST` and `DELETE /api/hosts/{id}/hostkey` | Section 6. |

A view never has a field that could carry a secret, and a test walks the JSON of every host route for a key, a passphrase and a password that were just set. The handlers log at `debug` only the host id and the route, never a body.

## 8. UI

Words are in Remedy's voice and come from pure modules in `web/src/hosts.ts`, tested with `node --test`; agent-written text does not exist in this cycle, and the maintainer's notes are shown as text.

- **Navigation.** `Section` gets `hosts`; `navItems` gets `{ section: 'hosts', to: '/hosts', label: 'Hosts', short: 'Hosts' }` before Setup. The phone's tab bar then has six items; it is checked at 360 px (section 10). `sectionOf` already matches `/hosts/:id`.
- **`/hosts`.** One line per host: name, address, a status chip (`new`, `untrusted`, `ok`, `error`, `key changed`), and the age of the last test. An empty list says what a host is for and has the button "Add a host". A host that is not `ok` shows its detail line.
- **`/hosts/:id`.** Four parts in one column: *The host* (the form), *How I log in* (kind, the replace form, the hint or fingerprint), *Trust* (the pinned fingerprint; or, after a test, the presented fingerprint with the button "I checked this fingerprint" and a line that says to compare it on the host with `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub`), and *What the account can do* (the report with its warnings). A fifth part shows the snippet in a block with a copy button.
- **Behaviour.** The rules of the repository's UI conventions apply: a button that starts a request keeps focus and uses `aria-disabled`; a removed control returns the focus to the heading; deleting a host and replacing a credential use `ConfirmButton`; the secret fields are `type="password"` with `autocomplete="off"` and are cleared as soon as the request has been sent, also on failure; they are never put in a URL, a query or `localStorage`.
- **A warning** is shown in the warning style with the text of the report. It does not disable a button.

## 9. Testing

Test first for the logic with behaviour, in this order:

1. **Snippet generator** (pure Go): the table of hostile inputs of section 5. Written first.
2. **Sealing**: a credential round-trips; a ciphertext copied to another host's row does not open (`ErrOpen`); a wrong master key marks the host `undecryptable`; the plain value is in no column (a test reads every column of both tables after a save and searches for the secret).
3. **`sshhost` against `sshtest`**: a pinned key connects; a different key is refused as `key changed`; `Probe` returns the key and authenticates nothing (the test server counts authentication attempts and expects zero); a wrong password is `authentication failed`; a server that stalls hits the timeout; an output over 16 KB is cut and reported; two parallel tests of one host run one after the other.
4. **The sudo parser**: sample outputs of `sudo -n -l` (limited rules, `NOPASSWD: ALL`, `(ALL : ALL) ALL`, a password required, an unknown format).
5. **Handlers** (with the `Hosts` seam): the routes of section 7 with their errors; the secret-walk of section 7; `PATCH` of the address clears the pin; the activity entries contain no secret.
6. **Migration 010**: it runs on a database built from 001 to 009 with incidents, runs and tool calls in it, and changes none of them.
7. **UI**: `hosts.ts` (statuses, lines, warnings) and `shell.ts` (the new item, `sectionOf('/hosts/3')`) with `node --test`.
8. **A look in a real browser** (repository rule): a throwaway server, a host against a test SSH server, the whole flow on a phone-sized and a desktop-sized window, including the tab bar with six items.

There is no real run against the maintainer's machines in this cycle. The first real connection is the NAS in cycle 6, after the tools of cycle 2.

## 10. Order of work

1. Migration 010 and the store methods, with the sealing tests.
2. The snippet generator and its tests.
3. `sshhost`, `sshtest` and the sudo parser.
4. The handlers and the `Hosts` seam, with the secret-walk.
5. The shell item, `hosts.ts` and the pages, checked in a browser, the tab bar at 360 px included.

Each step ends with `make check`. The umbrella already says that the matchers belong to cycle 4, that Hosts is a navigation item, and what the hint of a credential is
(changed together with this spec).

## 11. Success criteria

1. A host is created, given a login, its key is pinned after a comparison of the fingerprint and its connection is tested, all in the UI, against a test SSH server.
2. A test without a pinned key authenticates nothing.
3. A host key that changed is refused, shown as `key changed`, and does not connect until the maintainer pins it again.
4. After a credential is saved, no endpoint, log line, error, activity entry or database column except `host_credentials.ciphertext` contains it (tests and a look at the database).
5. The snippet generator produces no line that grants more than the table in section 5, for every hostile input of the test.
6. The report names root, the `docker` group and unrestricted sudo when they are present.
7. The Hosts page works on a phone-sized window with six tabs.
8. `make check` passes.

## 12. Risks

| Risk | Decision | Retrofit |
|---|---|---|
| The control plane now holds logins to the maintainer's machines. A leak of the database and the master key together is a leak of those logins. | The same protection as the GitHub token: sealed with the master key, bound to the row, never in a view, a log or an error. The account on the host is meant to be limited (the warnings say when it is not). | Credentials in an external secret store; short-lived SSH certificates. |
| A first connection pins whatever answers, if the maintainer does not compare the fingerprint. | The pin is a separate, deliberate step that shows the fingerprint and says how to check it. | Fetch the fingerprint from a second channel (a tailnet or the cluster) and show a mismatch. |
| The snippet grants rights on a machine. | Strict validation of every value, exact lines, comments that tell the maintainer to read them, and a hostile-input test. | A wrapper script owned by root instead of wildcards. |
| The `sudo -n -l` parser misreads an output and reports "limited" for an unrestricted account. | When it cannot tell, it says so in the warnings and never reports `none`. | Ask `sudo` for a specific command (`sudo -n -l <command>`) instead of parsing the list. |
| The tab bar with six items is too narrow on a phone. | Checked at 360 px in the browser; labels are short. | Show Hosts and Setup under one tab "More". |
| A password credential is weaker than a key and works with `sudo -n` only if the account may use it without a password. | Allowed, because the maintainer asked for it; the hint says `set` and nothing more, and the warnings apply. | Refuse password logins for accounts with sudo. |

## 13. Open points for the plan

- The exact wording of the status lines and warnings (they are tested in `hosts.ts`).
- Whether `enabled = false` hides a host from the tools of cycle 2 only, or also stops the test (proposal: the test still works, the tools do not).
- The size limits of the form fields beyond those named here.
