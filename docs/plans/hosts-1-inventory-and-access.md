# Hosts, cycle 1: inventory and access — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The maintainer creates a host in the web UI, gives it a login (SSH key or password, sealed), pins its host key after comparing the fingerprint, tests the connection and sees what the account may do. Nothing uses the host yet.

**Architecture:** Two new tables (`hosts`, `host_credentials`, migration 010) with store methods modelled on the GitHub connection. A new package `internal/sshhost` holds the validation of host fields, the onboarding snippet generator, the SSH client (`Probe` for the key exchange only, `Dial` against a pinned key, `Run` with caps) and the report builder with a tolerant `sudo -n -l` parser; `internal/sshhost/sshtest` is an SSH server for tests that runs inside the test process. `internal/server/hosts.go` adds the admin routes (registered only when `Deps.Hosts` is set). The UI gets a navigation item **Hosts** with a list page and a detail page.

**Tech Stack:** Go (stdlib, `golang.org/x/crypto/ssh` from the module `go.mod` already requires, `modernc.org/sqlite`), React 19, TypeScript, Tailwind, shadcn/ui, `node --test`.

**Spec:** [`docs/specs/2026-10-09-hosts-1-inventory-design.md`](../specs/2026-10-09-hosts-1-inventory-design.md); umbrella [`docs/specs/2026-10-09-hosts-and-skills-design.md`](../specs/2026-10-09-hosts-and-skills-design.md) (sections 2, 4 and 11).

## Global Constraints

- Everything in the repo is English: code, comments, docs, commit messages, UI copy.
- Go: stdlib first. **No new module**: `golang.org/x/crypto/ssh` is part of `golang.org/x/crypto v0.57.0`, which `go.mod` already requires. `go 1.27.1`.
- Pull request titles are conventional commits (`feat:`, `fix:`, `docs:`); commit messages end with the line `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.
- The store has **one connection** (`SetMaxOpenConns(1)`): inside `Store.inTx` use only the `tx`, never `s.db`.
- Timestamps in SQLite are written with `formatTS` (nine fractional digits: `2006-01-02T15:04:05.000000000Z`).
- A credential is sealed with `secret.Key.Seal(plain, store.HostCredentialAAD(hostID))`; the additional data's value is **never changed** once released.
- A secret (private key, passphrase, password) is never in a log, an error, an API answer, an activity entry or a database column other than `host_credentials.ciphertext`.
- A host key is never accepted without a pin. There is no option to switch the check off.
- The UI: `web/tsconfig.app.json` sets `erasableSyntaxOnly` and `verbatimModuleSyntax`: no enums, no constructor parameter properties, `import type` for types. Imports use the `@/` alias in components and relative `.ts` imports in pure modules. A button that starts a request keeps focus (`aria-disabled` plus a guard in the handler, not `disabled`). Admin API: non-GET needs the header `X-Remedy-CSRF: 1` (the `api.ts` client sends it).
- Shell in tests runs on macOS and Linux: no BSD-only flags.
- Work in the worktree; run `make check` before the pull request. A fresh worktree needs `make web-install` once.

## Review Focus

The spec implies these, and no task's main tests would otherwise exercise them. Each has a test in the task named in brackets.

1. **A private key pasted with Windows line endings, trailing spaces or no final newline** must still parse (`\r\n` is normalised; the text is trimmed and ends with one newline). [Task 4, `TestNewKeyCredential`]
2. **A passphrase-protected key with no passphrase, or a wrong one,** answers with a fixed message that does not contain the key. [Task 4 `TestNewKeyCredential`, Task 5 `TestPutCredentialRejectsBadKeys`]
3. **A server that rotates to another host key *type*** (ed25519 pinned, RSA presented) is `key changed`, not "unreachable". [Task 4, `TestDialRefusesAKeyOfAnotherType`]
4. **A host without `sudo`** (`sudo -n -l` exits 127 or prints "command not found") gives `sudo: none` and no crash; an output the parser does not understand gives `limited` plus a warning, never `none`. [Task 3, `TestParseSudoList`]
5. **The host is deleted in another tab** while its detail page is open: every call answers 404 and the page says the host is gone instead of showing an error per button. [Task 7, `HostPage`]

An IPv6 literal as an address and a name that differs only in case are covered by `TestValidate` (Task 2) and `TestCreateGetListHost` (Task 1).

---

## File structure

| File | Responsibility |
|---|---|
| `internal/store/migrations/010_hosts.sql` | The two tables. |
| `internal/store/hosts.go` | Host types, status constants and all store methods. |
| `internal/store/seal.go` (modify) | `HostCredentialAAD`. |
| `internal/store/incidents.go` (modify) | The activity kind `KindHostChanged`. |
| `internal/sshhost/validate.go` | `Fields` and its validation and unit normalisation. |
| `internal/sshhost/snippet.go` | The onboarding snippet generator. |
| `internal/sshhost/report.go` | `Report`, `Outcome`, `BuildReport`, `ParseSudoList`. |
| `internal/sshhost/credential.go` | `Credential`, `NewKeyCredential`, `NewPasswordCredential`, the sealed JSON. |
| `internal/sshhost/client.go` | `Probe`, `Dial`, `Client.Run`, error classification. |
| `internal/sshhost/service.go` | `Service`: per-host serialisation, `Probe`, `Check`. |
| `internal/sshhost/export_test.go` | Test-only setters for the timeouts. |
| `internal/sshhost/sshtest/sshtest.go` | The SSH server for tests. |
| `internal/server/hosts.go` | The handlers and views. |
| `internal/server/server.go` (modify) | `Deps.Hosts` and the routes. |
| `internal/app/app.go` (modify) | `Hosts: &sshhost.Service{}`. |
| `web/src/api.ts` (modify) | Types and calls. |
| `web/src/shell.ts`, `components/shell/navIcons.ts`, `App.tsx` (modify) | The navigation item and routes. |
| `web/src/hosts.ts` | Pure: what statuses, lines and warnings say; input checks. |
| `web/src/HostsPage.tsx`, `HostPage.tsx`, `components/hosts/*.tsx` | The pages. |

---

### Task 1: Migration 010 and the store

**Files:**
- Create: `internal/store/migrations/010_hosts.sql`, `internal/store/hosts.go`, `internal/store/hosts_test.go`, `internal/store/migrate010_test.go`
- Modify: `internal/store/seal.go`, `internal/store/incidents.go` (the activity kind constant)

**Interfaces:**
- Produces (used by Task 5): `store.HostStatus` and the constants `HostNew`, `HostUntrusted`, `HostOK`, `HostError`, `HostKeyChanged`, `HostUndecryptable`; `store.HostFields{Name, Address string; Port int; Account, Notes, DockerPattern string; SystemdUnits []string; Enabled bool}`; `store.Host{ID int64; HostFields; PinnedKey string; PinnedAt *time.Time; Status HostStatus; StatusDetail, Report string; CheckedAt *time.Time; CreatedAt, UpdatedAt time.Time}`; `store.HostCredential{HostID int64; Kind string; Ciphertext []byte; Hint, PublicKey string; UpdatedAt time.Time}`; `store.HostEntry{Host Host; Credential *HostCredential}`; methods `CreateHost(ctx, HostFields) (Host, error)`, `GetHost(ctx, id) (HostEntry, error)`, `ListHosts(ctx) ([]HostEntry, error)`, `UpdateHost(ctx, id, HostFields) (Host, bool, error)` (the bool says the pin was cleared), `DeleteHost(ctx, id) error`, `SaveHostCredential(ctx, HostCredential) error`, `DeleteHostCredential(ctx, id) error`, `PinHostKey(ctx, id, line string, at time.Time) error`, `UnpinHostKey(ctx, id) error`, `SaveHostTest(ctx, id, HostStatus, detail, report string, at time.Time) error`; `store.HostCredentialAAD(hostID int64) string`; `store.KindHostChanged`.

- [ ] **Step 1: Write the migration**

`internal/store/migrations/010_hosts.sql`:

```sql
-- Hosts: machines outside the cluster that Remedy may be given a login to. A credential belongs to one host and is
-- stored sealed (AES-256-GCM, bound to the host id); the host key the machine must present is pinned in the host's row.
CREATE TABLE hosts (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  name           TEXT NOT NULL UNIQUE COLLATE NOCASE,
  address        TEXT NOT NULL,
  port           INTEGER NOT NULL DEFAULT 22 CHECK (port BETWEEN 1 AND 65535),
  account        TEXT NOT NULL DEFAULT 'remedy',
  notes          TEXT NOT NULL DEFAULT '',
  docker_pattern TEXT NOT NULL DEFAULT '',
  systemd_units  TEXT NOT NULL DEFAULT '[]',
  enabled        INTEGER NOT NULL DEFAULT 1,
  pinned_key     TEXT NOT NULL DEFAULT '',
  pinned_at      TEXT,
  status         TEXT NOT NULL DEFAULT 'new'
                 CHECK (status IN ('new', 'untrusted', 'ok', 'error', 'key_changed', 'undecryptable')),
  status_detail  TEXT NOT NULL DEFAULT '',
  report         TEXT NOT NULL DEFAULT '',
  checked_at     TEXT,
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL
);

CREATE TABLE host_credentials (
  host_id    INTEGER PRIMARY KEY REFERENCES hosts (id) ON DELETE CASCADE,
  kind       TEXT NOT NULL CHECK (kind IN ('key', 'password')),
  ciphertext BLOB NOT NULL,
  hint       TEXT NOT NULL,
  public_key TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL
);
```

- [ ] **Step 2: Add the AAD function and the activity kind**

Append to `internal/store/seal.go`:

```go
// HostCredentialAAD binds a sealed host credential to its host, so a ciphertext cannot be moved to another one. Credentials
// that are already sealed in a database depend on this exact value: never change it.
func HostCredentialAAD(hostID int64) string { return "host_credential:" + strconv.FormatInt(hostID, 10) }
```

In `internal/store/incidents.go`, in the `const` block with `KindConnectionChanged`, add the line:

```go
	KindHostChanged       = "host_changed"
```

- [ ] **Step 3: Write the failing store tests**

`internal/store/hosts_test.go`:

```go
package store_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
)

func hostFields(name string) store.HostFields {
	return store.HostFields{
		Name: name, Address: "192.168.178.118", Port: 22, Account: "remedy", Notes: "the NAS",
		DockerPattern: "remedy-*", SystemdUnits: []string{"node_exporter.service"}, Enabled: true,
	}
}

func activityKinds(t *testing.T, s *store.Store) []string {
	t.Helper()
	log, err := s.ListActivity(context.Background(), store.ActivityQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, a := range log {
		out = append(out, a.Summary)
	}
	return out
}

func TestCreateGetListHost(t *testing.T) {
	s, ctx := openStore(t), context.Background()

	nas, err := s.CreateHost(ctx, hostFields("nas"))
	if err != nil {
		t.Fatal(err)
	}
	if nas.ID == 0 || nas.Status != store.HostNew || nas.Port != 22 || !nas.Enabled || nas.PinnedKey != "" || nas.Report != "" {
		t.Fatalf("created host = %+v", nas)
	}
	if len(nas.SystemdUnits) != 1 || nas.SystemdUnits[0] != "node_exporter.service" {
		t.Fatalf("units = %v", nas.SystemdUnits)
	}

	// A name that differs only in case is the same name.
	if _, err := s.CreateHost(ctx, hostFields("NAS")); !errors.Is(err, store.ErrExists) {
		t.Fatalf("duplicate name error = %v, want ErrExists", err)
	}
	if _, err := s.CreateHost(ctx, hostFields("alpha")); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListHosts(ctx)
	if err != nil || len(list) != 2 || list[0].Host.Name != "alpha" || list[1].Host.Name != "nas" {
		t.Fatalf("list = %+v, err = %v", list, err)
	}
	if list[0].Credential != nil {
		t.Fatalf("a host without a login has a credential: %+v", list[0].Credential)
	}
	if _, err := s.GetHost(ctx, 999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown host error = %v", err)
	}
	if got := activityKinds(t, s); len(got) != 2 || got[1] != "Host nas created" {
		t.Fatalf("activity = %v", got)
	}
}

func TestUpdateHostClearsThePinWhenTheEndpointChanges(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	h, _ := s.CreateHost(ctx, hostFields("nas"))
	if err := s.PinHostKey(ctx, h.ID, "ssh-ed25519 AAAA", time.Now()); err != nil {
		t.Fatal(err)
	}

	// Notes and account do not touch the pin.
	f := hostFields("nas")
	f.Notes = "changed"
	if _, cleared, err := s.UpdateHost(ctx, h.ID, f); err != nil || cleared {
		t.Fatalf("notes change: cleared = %v, err = %v", cleared, err)
	}
	if e, _ := s.GetHost(ctx, h.ID); e.Host.PinnedKey == "" {
		t.Fatal("a notes change cleared the pin")
	}

	// A new address belongs to another machine until the maintainer says otherwise.
	f.Address = "192.168.178.200"
	got, cleared, err := s.UpdateHost(ctx, h.ID, f)
	if err != nil || !cleared {
		t.Fatalf("address change: cleared = %v, err = %v", cleared, err)
	}
	if got.PinnedKey != "" || got.PinnedAt != nil || got.Status != store.HostUntrusted {
		t.Fatalf("host after an address change = %+v", got)
	}
}

func TestPinUnpinAndTestStatus(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	h, _ := s.CreateHost(ctx, hostFields("nas"))

	at := time.Now().UTC()
	if err := s.SaveHostTest(ctx, h.ID, store.HostOK, "", `{"account":"remedy"}`, at); err != nil {
		t.Fatal(err)
	}
	e, _ := s.GetHost(ctx, h.ID)
	if e.Host.Status != store.HostOK || e.Host.Report != `{"account":"remedy"}` || e.Host.CheckedAt == nil {
		t.Fatalf("after a test = %+v", e.Host)
	}

	if err := s.PinHostKey(ctx, h.ID, "ssh-ed25519 AAAA", at); err != nil {
		t.Fatal(err)
	}
	e, _ = s.GetHost(ctx, h.ID)
	if e.Host.PinnedKey != "ssh-ed25519 AAAA" || e.Host.PinnedAt == nil || e.Host.Status != store.HostNew {
		t.Fatalf("after pinning = %+v", e.Host)
	}
	if err := s.UnpinHostKey(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	e, _ = s.GetHost(ctx, h.ID)
	if e.Host.PinnedKey != "" || e.Host.Status != store.HostUntrusted {
		t.Fatalf("after unpinning = %+v", e.Host)
	}
	if err := s.PinHostKey(ctx, 999, "ssh-ed25519 AAAA", at); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("pin on an unknown host = %v", err)
	}
}

func TestHostCredentialAndCascade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	h, _ := s.CreateHost(ctx, hostFields("nas"))

	cred := store.HostCredential{HostID: h.ID, Kind: "key", Ciphertext: []byte{1, 2, 3}, Hint: "SHA256:abc", PublicKey: "ssh-ed25519 AAAA", UpdatedAt: time.Now()}
	if err := s.SaveHostCredential(ctx, store.HostCredential{HostID: 999, Kind: "key", Ciphertext: []byte{1}}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("credential for an unknown host = %v", err)
	}
	if err := s.SaveHostCredential(ctx, cred); err != nil {
		t.Fatal(err)
	}
	cred.Kind, cred.Hint = "password", "set" // a second save replaces the first
	if err := s.SaveHostCredential(ctx, cred); err != nil {
		t.Fatal(err)
	}
	e, _ := s.GetHost(ctx, h.ID)
	if e.Credential == nil || e.Credential.Kind != "password" || e.Credential.Hint != "set" || !bytes.Equal(e.Credential.Ciphertext, []byte{1, 2, 3}) {
		t.Fatalf("credential = %+v", e.Credential)
	}

	// Deleting the host deletes its credential.
	if err := s.DeleteHost(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var n int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM host_credentials`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("credentials left after deleting the host: %d, %v", n, err)
	}
	if err := s.DeleteHost(ctx, h.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleting twice = %v", err)
	}
}

func TestDeleteHostCredentialAndUndecryptableReset(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	h, _ := s.CreateHost(ctx, hostFields("nas"))
	_ = s.SaveHostCredential(ctx, store.HostCredential{HostID: h.ID, Kind: "password", Ciphertext: []byte{9}, Hint: "set", UpdatedAt: time.Now()})
	_ = s.SaveHostTest(ctx, h.ID, store.HostUndecryptable, "cannot decrypt", "", time.Now())

	// A new login replaces what the master key could not open, so the host is untested again.
	_ = s.SaveHostCredential(ctx, store.HostCredential{HostID: h.ID, Kind: "password", Ciphertext: []byte{8}, Hint: "set", UpdatedAt: time.Now()})
	if e, _ := s.GetHost(ctx, h.ID); e.Host.Status != store.HostNew || e.Host.StatusDetail != "" {
		t.Fatalf("after replacing an undecryptable login = %+v", e.Host)
	}
	if err := s.DeleteHostCredential(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	if e, _ := s.GetHost(ctx, h.ID); e.Credential != nil {
		t.Fatal("the credential is still there")
	}
	if err := s.DeleteHostCredential(ctx, h.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleting a missing credential = %v", err)
	}
}

func TestSealedCredentialIsBoundToItsHost(t *testing.T) {
	key, err := secret.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := key.Seal([]byte(`{"v":1}`), store.HostCredentialAAD(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := key.Open(sealed, store.HostCredentialAAD(1)); err != nil {
		t.Fatalf("opening with the right host: %v", err)
	}
	if _, err := key.Open(sealed, store.HostCredentialAAD(2)); !errors.Is(err, secret.ErrOpen) {
		t.Fatalf("opening for another host = %v, want ErrOpen", err)
	}
	// The value is part of every sealed credential in every database: this test pins it.
	if got := store.HostCredentialAAD(12); got != "host_credential:12" {
		t.Fatalf("HostCredentialAAD(12) = %q", got)
	}
}

func TestHostActivityNamesNoSecrets(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	h, _ := s.CreateHost(ctx, hostFields("nas"))
	_ = s.SaveHostCredential(ctx, store.HostCredential{HostID: h.ID, Kind: "key", Ciphertext: []byte{1}, Hint: "SHA256:abc", UpdatedAt: time.Now()})
	_ = s.PinHostKey(ctx, h.ID, "ssh-ed25519 AAAA", time.Now())
	_ = s.DeleteHost(ctx, h.ID)

	want := []string{"Host nas removed", "Host key of nas pinned", "Credential of nas replaced", "Host nas created"}
	got := activityKinds(t, s)
	if len(got) != len(want) {
		t.Fatalf("activity = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("activity[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
```

`internal/store/migrate010_test.go`:

```go
package store_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

// A database as 009 left it, with a run and an incident, must open under 010 with the two host tables added and every old row unchanged.
func TestMigration010AddsTheHostTablesAndKeepsTheData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v009.db")
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	if _, err := raw.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob("migrations/*.sql")
	sort.Strings(files)
	for _, f := range files {
		name := filepath.Base(f)
		if name >= "010" {
			continue
		}
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(body)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, name); err != nil {
			t.Fatal(err)
		}
	}
	const at = "2026-10-04T12:00:00.000000000Z"
	for _, q := range []string{
		`INSERT INTO runs (id, provider, prompt, status, created_at) VALUES ('run-1', 'claude', 'hello', 'succeeded', '` + at + `')`,
		`INSERT INTO incidents (id, source, key, title, ref, check_name, state, conclusion, head_sha, first_seen, last_seen)
		 VALUES (1, 'alertmanager', 'HighLoad/abc', 'High load', '', '', 'open', 'firing', '', '` + at + `', '` + at + `')`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	_ = raw.Close()

	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open on a 009 database: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	if r, err := s.GetRun(ctx, "run-1"); err != nil || r.Prompt != "hello" {
		t.Fatalf("run after migrating = %+v, %v", r, err)
	}
	if _, err := s.CreateHost(ctx, hostFields("nas")); err != nil {
		t.Fatalf("the host tables were not created: %v", err)
	}
}
```

- [ ] **Step 4: Run the tests to see them fail**

Run: `go test ./internal/store -run 'Host|Migration010' -count=1`
Expected: FAIL to compile (`undefined: store.HostFields` and the like).

- [ ] **Step 5: Implement `internal/store/hosts.go`**

```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type HostStatus string

const (
	HostNew           HostStatus = "new"           // never tested
	HostUntrusted     HostStatus = "untrusted"     // the host key is not pinned
	HostOK            HostStatus = "ok"            // the last test worked
	HostError         HostStatus = "error"         // the last test failed
	HostKeyChanged    HostStatus = "key_changed"   // the host shows another key than the pinned one
	HostUndecryptable HostStatus = "undecryptable" // the master key does not open the stored login
)

// HostFields is what the maintainer edits.
type HostFields struct {
	Name          string
	Address       string
	Port          int
	Account       string
	Notes         string
	DockerPattern string
	SystemdUnits  []string
	Enabled       bool
}

type Host struct {
	ID int64
	HostFields
	PinnedKey    string // the host key as an authorized_keys-style line; empty until pinned
	PinnedAt     *time.Time
	Status       HostStatus
	StatusDetail string
	Report       string // the last test as JSON; empty before the first one
	CheckedAt    *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// HostCredential is the stored login of a host. The ciphertext is sealed; see HostCredentialAAD.
type HostCredential struct {
	HostID     int64
	Kind       string // "key" or "password"
	Ciphertext []byte
	Hint       string // a key's fingerprint, or "set" for a password
	PublicKey  string // for a key: the authorized_keys line
	UpdatedAt  time.Time
}

// HostEntry is a host with its login, if it has one.
type HostEntry struct {
	Host       Host
	Credential *HostCredential
}

const hostSelect = `
	SELECT h.id, h.name, h.address, h.port, h.account, h.notes, h.docker_pattern, h.systemd_units, h.enabled,
	       h.pinned_key, h.pinned_at, h.status, h.status_detail, h.report, h.checked_at, h.created_at, h.updated_at,
	       c.host_id, c.kind, c.ciphertext, c.hint, c.public_key, c.updated_at
	FROM hosts h LEFT JOIN host_credentials c ON c.host_id = h.id`

func nullTS(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid {
		return nil, nil
	}
	t, err := parseTS(ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func scanHostEntry(sc scanner) (HostEntry, error) {
	var (
		h                                      Host
		units, status, created, updated        string
		enabled                                int
		pinnedAt, checked, credUpdated         sql.NullString
		credHost                               sql.NullInt64
		kind, hint, pub                        sql.NullString
		ciphertext                             []byte
	)
	if err := sc.Scan(&h.ID, &h.Name, &h.Address, &h.Port, &h.Account, &h.Notes, &h.DockerPattern, &units, &enabled,
		&h.PinnedKey, &pinnedAt, &status, &h.StatusDetail, &h.Report, &checked, &created, &updated,
		&credHost, &kind, &ciphertext, &hint, &pub, &credUpdated); err != nil {
		return HostEntry{}, err
	}
	h.Enabled = enabled != 0
	h.Status = HostStatus(status)
	if err := json.Unmarshal([]byte(units), &h.SystemdUnits); err != nil {
		return HostEntry{}, err
	}
	var err error
	if h.PinnedAt, err = nullTS(pinnedAt); err != nil {
		return HostEntry{}, err
	}
	if h.CheckedAt, err = nullTS(checked); err != nil {
		return HostEntry{}, err
	}
	if h.CreatedAt, err = parseTS(created); err != nil {
		return HostEntry{}, err
	}
	if h.UpdatedAt, err = parseTS(updated); err != nil {
		return HostEntry{}, err
	}
	e := HostEntry{Host: h}
	if credHost.Valid {
		at, err := parseTS(credUpdated.String)
		if err != nil {
			return HostEntry{}, err
		}
		e.Credential = &HostCredential{HostID: credHost.Int64, Kind: kind.String, Ciphertext: ciphertext, Hint: hint.String, PublicKey: pub.String, UpdatedAt: at}
	}
	return e, nil
}

func unitsJSON(units []string) (string, error) {
	if units == nil {
		units = []string{}
	}
	b, err := json.Marshal(units)
	return string(b), err
}

func hostEnabledInt(f HostFields) int {
	if f.Enabled {
		return 1
	}
	return 0
}

func isUnique(err error) bool { return strings.Contains(err.Error(), "UNIQUE constraint failed") }

func (s *Store) GetHost(ctx context.Context, id int64) (HostEntry, error) {
	e, err := scanHostEntry(s.db.QueryRowContext(ctx, hostSelect+` WHERE h.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return HostEntry{}, ErrNotFound
	}
	return e, err
}

// ListHosts returns every host with its login, ordered by name.
func (s *Store) ListHosts(ctx context.Context) ([]HostEntry, error) {
	rows, err := s.db.QueryContext(ctx, hostSelect+` ORDER BY h.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HostEntry{}
	for rows.Next() {
		e, err := scanHostEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CreateHost adds a host. It returns ErrExists if the name is taken, ignoring case.
func (s *Store) CreateHost(ctx context.Context, f HostFields) (Host, error) {
	units, err := unitsJSON(f.SystemdUnits)
	if err != nil {
		return Host{}, err
	}
	var id int64
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		now := formatTS(time.Now())
		res, err := tx.ExecContext(ctx, `
			INSERT INTO hosts (name, address, port, account, notes, docker_pattern, systemd_units, enabled, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			f.Name, f.Address, f.Port, f.Account, f.Notes, f.DockerPattern, units, hostEnabledInt(f), now, now)
		if err != nil {
			if isUnique(err) {
				return ErrExists
			}
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return insertActivity(ctx, tx, NewActivity{Kind: KindHostChanged, Summary: "Host " + f.Name + " created"})
	})
	if err != nil {
		return Host{}, err
	}
	e, err := s.GetHost(ctx, id)
	return e.Host, err
}

// UpdateHost replaces the editable fields. A new address or port clears the pinned key, because a pin belongs to an endpoint;
// the bool says whether it did.
func (s *Store) UpdateHost(ctx context.Context, id int64, f HostFields) (Host, bool, error) {
	units, err := unitsJSON(f.SystemdUnits)
	if err != nil {
		return Host{}, false, err
	}
	cleared := false
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		var oldAddress, pinned string
		var oldPort int
		err := tx.QueryRowContext(ctx, `SELECT address, port, pinned_key FROM hosts WHERE id = ?`, id).Scan(&oldAddress, &oldPort, &pinned)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		cleared = pinned != "" && (oldAddress != f.Address || oldPort != f.Port)
		q := `UPDATE hosts SET name = ?, address = ?, port = ?, account = ?, notes = ?, docker_pattern = ?, systemd_units = ?, enabled = ?, updated_at = ?`
		if cleared {
			q += `, pinned_key = '', pinned_at = NULL, status = 'untrusted', status_detail = '', report = ''`
		}
		q += ` WHERE id = ?`
		if _, err := tx.ExecContext(ctx, q, f.Name, f.Address, f.Port, f.Account, f.Notes, f.DockerPattern, units, hostEnabledInt(f), formatTS(time.Now()), id); err != nil {
			if isUnique(err) {
				return ErrExists
			}
			return err
		}
		summary := "Host " + f.Name + " changed"
		if cleared {
			summary += "; its pinned key was cleared"
		}
		return insertActivity(ctx, tx, NewActivity{Kind: KindHostChanged, Summary: summary})
	})
	if err != nil {
		return Host{}, false, err
	}
	e, err := s.GetHost(ctx, id)
	return e.Host, cleared, err
}

// hostName reads a host's name inside a transaction; ErrNotFound if it does not exist.
func hostName(ctx context.Context, tx *sql.Tx, id int64) (string, error) {
	var name string
	err := tx.QueryRowContext(ctx, `SELECT name FROM hosts WHERE id = ?`, id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return name, err
}

// DeleteHost removes a host and, through the foreign key, its login.
func (s *Store) DeleteHost(ctx context.Context, id int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		name, err := hostName(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM hosts WHERE id = ?`, id); err != nil {
			return err
		}
		return insertActivity(ctx, tx, NewActivity{Kind: KindHostChanged, Summary: "Host " + name + " removed"})
	})
}

// SaveHostCredential sets or replaces a host's login. A host whose old login could not be decrypted is untested again.
func (s *Store) SaveHostCredential(ctx context.Context, c HostCredential) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		name, err := hostName(ctx, tx, c.HostID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO host_credentials (host_id, kind, ciphertext, hint, public_key, updated_at) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (host_id) DO UPDATE SET kind = excluded.kind, ciphertext = excluded.ciphertext, hint = excluded.hint,
				public_key = excluded.public_key, updated_at = excluded.updated_at`,
			c.HostID, c.Kind, c.Ciphertext, c.Hint, c.PublicKey, formatTS(time.Now())); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE hosts SET status = 'new', status_detail = '' WHERE id = ? AND status = 'undecryptable'`, c.HostID); err != nil {
			return err
		}
		return insertActivity(ctx, tx, NewActivity{Kind: KindHostChanged, Summary: "Credential of " + name + " replaced"})
	})
}

func (s *Store) DeleteHostCredential(ctx context.Context, hostID int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		name, err := hostName(ctx, tx, hostID)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM host_credentials WHERE host_id = ?`, hostID)
		if err := oneRow(res, err); err != nil {
			return err
		}
		return insertActivity(ctx, tx, NewActivity{Kind: KindHostChanged, Summary: "Credential of " + name + " removed"})
	})
}

// PinHostKey stores the host key (an authorized_keys-style line). The host is untested again.
func (s *Store) PinHostKey(ctx context.Context, id int64, line string, at time.Time) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		name, err := hostName(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE hosts SET pinned_key = ?, pinned_at = ?, status = 'new', status_detail = '', report = '' WHERE id = ?`,
			line, formatTS(at), id); err != nil {
			return err
		}
		return insertActivity(ctx, tx, NewActivity{Kind: KindHostChanged, Summary: "Host key of " + name + " pinned"})
	})
}

func (s *Store) UnpinHostKey(ctx context.Context, id int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		name, err := hostName(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE hosts SET pinned_key = '', pinned_at = NULL, status = 'untrusted', status_detail = '', report = '' WHERE id = ?`, id); err != nil {
			return err
		}
		return insertActivity(ctx, tx, NewActivity{Kind: KindHostChanged, Summary: "Host key of " + name + " unpinned"})
	})
}

// SaveHostTest records the result of a connection test. It writes no activity: tests are frequent.
func (s *Store) SaveHostTest(ctx context.Context, id int64, status HostStatus, detail, report string, at time.Time) error {
	return oneRow(s.db.ExecContext(ctx,
		`UPDATE hosts SET status = ?, status_detail = ?, report = ?, checked_at = ? WHERE id = ?`,
		string(status), detail, report, formatTS(at), id))
}
```

- [ ] **Step 6: Run the tests to see them pass**

Run: `gofmt -l internal/store; go vet ./internal/store && go test ./internal/store -count=1`
Expected: no gofmt output, vet clean, PASS (all store tests, old and new).

- [ ] **Step 7: Commit**

```sh
git add internal/store
git commit -m "feat(store): hosts and sealed host credentials (migration 010)

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Validation of host fields and the onboarding snippet

**Files:**
- Create: `internal/sshhost/validate.go`, `internal/sshhost/validate_test.go`, `internal/sshhost/snippet.go`, `internal/sshhost/snippet_test.go`

**Interfaces:**
- Produces (used by Task 5): `sshhost.Fields{Name, Address string; Port int; Account, Notes, DockerPattern string; SystemdUnits []string}`; `(*Fields).Validate() error` (normalises `SystemdUnits` in place: a unit without `.service`, `.timer` or `.socket` gets `.service`; duplicates are removed); `sshhost.FieldError{Field, Msg string}` with `Error() string` returning `Msg`; `sshhost.SnippetInput{Account, AuthorizedKey, DockerPattern string; SystemdUnits []string}`; `sshhost.SnippetOutput{AuthorizedKeys, Sudoers string}`; `sshhost.Snippet(SnippetInput) (SnippetOutput, error)`.

- [ ] **Step 1: Write the failing snippet test first (the hostile inputs)**

`internal/sshhost/snippet_test.go`:

```go
package sshhost_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/sshhost"
)

const (
	pat  = `[A-Za-z0-9][A-Za-z0-9_.-]*\*?`
	unit = `[A-Za-z0-9][A-Za-z0-9_.@:-]*\.(service|timer|socket)`
)

// grantLine is the only shape of a non-comment line the generator may produce for the account.
func grantLine(account string) *regexp.Regexp {
	a := regexp.QuoteMeta(account)
	return regexp.MustCompile(`^` + a + ` ALL=\(root\) NOPASSWD: (` +
		`/usr/bin/docker ps -a` +
		`|/usr/bin/docker logs --tail \* ` + pat +
		`|/usr/bin/docker inspect ` + pat +
		`|/usr/bin/docker start ` + pat + `, /usr/bin/docker restart ` + pat + `, /usr/bin/docker stop ` + pat +
		`|/usr/bin/systemctl start ` + unit + `, /usr/bin/systemctl restart ` + unit + `, /usr/bin/systemctl stop ` + unit +
		`)$`)
}

func TestSnippetForTheNAS(t *testing.T) {
	out, err := sshhost.Snippet(sshhost.SnippetInput{
		Account:       "remedy",
		AuthorizedKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKExample",
		DockerPattern: "remedy-*",
		SystemdUnits:  []string{"node_exporter.service"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKExample\n",
	} {
		if !strings.Contains(out.AuthorizedKeys, want) {
			t.Errorf("authorized_keys lacks %q:\n%s", want, out.AuthorizedKeys)
		}
	}
	for _, want := range []string{
		"remedy ALL=(root) NOPASSWD: /usr/bin/docker ps -a\n",
		"remedy ALL=(root) NOPASSWD: /usr/bin/docker logs --tail * remedy-*\n",
		"remedy ALL=(root) NOPASSWD: /usr/bin/docker inspect remedy-*\n",
		"remedy ALL=(root) NOPASSWD: /usr/bin/docker start remedy-*, /usr/bin/docker restart remedy-*, /usr/bin/docker stop remedy-*\n",
		"remedy ALL=(root) NOPASSWD: /usr/bin/systemctl start node_exporter.service, /usr/bin/systemctl restart node_exporter.service, /usr/bin/systemctl stop node_exporter.service\n",
	} {
		if !strings.Contains(out.Sudoers, want) {
			t.Errorf("sudoers lacks %q:\n%s", want, out.Sudoers)
		}
	}
	assertOnlyGrants(t, out.Sudoers, "remedy")
}

func assertOnlyGrants(t *testing.T, sudoers, account string) {
	t.Helper()
	re := grantLine(account)
	for _, line := range strings.Split(sudoers, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !re.MatchString(line) {
			t.Errorf("a line grants more than the table allows: %q", line)
		}
	}
}

func TestSnippetWithoutDockerOrUnitsGrantsNothing(t *testing.T) {
	out, err := sshhost.Snippet(sshhost.SnippetInput{Account: "remedy"})
	if err != nil {
		t.Fatal(err)
	}
	assertOnlyGrants(t, out.Sudoers, "remedy")
	if strings.Contains(out.Sudoers, "NOPASSWD") {
		t.Fatalf("a snippet without docker or units grants something:\n%s", out.Sudoers)
	}
	if out.AuthorizedKeys != "" {
		t.Fatalf("authorized_keys without a key = %q", out.AuthorizedKeys)
	}
}

func TestSnippetRefusesHostileInput(t *testing.T) {
	base := sshhost.SnippetInput{Account: "remedy", DockerPattern: "remedy-*", SystemdUnits: []string{"a.service"}}
	cases := map[string]func(in *sshhost.SnippetInput){
		"newline in pattern":   func(in *sshhost.SnippetInput) { in.DockerPattern = "remedy-*\nALL ALL=(ALL) NOPASSWD: ALL" },
		"comma in pattern":     func(in *sshhost.SnippetInput) { in.DockerPattern = "a,b" },
		"space in pattern":     func(in *sshhost.SnippetInput) { in.DockerPattern = "a b" },
		"ALL as pattern":       func(in *sshhost.SnippetInput) { in.DockerPattern = "ALL" },
		"leading dash":         func(in *sshhost.SnippetInput) { in.DockerPattern = "-rf" },
		"bare star":            func(in *sshhost.SnippetInput) { in.DockerPattern = "*" },
		"two stars":            func(in *sshhost.SnippetInput) { in.DockerPattern = "x**" },
		"star in the middle":   func(in *sshhost.SnippetInput) { in.DockerPattern = "a*b" },
		"shell in pattern":     func(in *sshhost.SnippetInput) { in.DockerPattern = "$(id)" },
		"nul in pattern":       func(in *sshhost.SnippetInput) { in.DockerPattern = "a\x00" },
		"very long pattern":    func(in *sshhost.SnippetInput) { in.DockerPattern = strings.Repeat("a", 200) },
		"newline in unit":      func(in *sshhost.SnippetInput) { in.SystemdUnits = []string{"a.service\nroot ALL=(ALL) ALL"} },
		"comma in unit":        func(in *sshhost.SnippetInput) { in.SystemdUnits = []string{"a.service, /bin/sh"} },
		"unit with a path":     func(in *sshhost.SnippetInput) { in.SystemdUnits = []string{"../x.service"} },
		"unit with a space":    func(in *sshhost.SnippetInput) { in.SystemdUnits = []string{"a b.service"} },
		"leading dash in unit": func(in *sshhost.SnippetInput) { in.SystemdUnits = []string{"-x.service"} },
		"unit of a wrong kind": func(in *sshhost.SnippetInput) { in.SystemdUnits = []string{"a.mount"} },
		"unit without suffix":  func(in *sshhost.SnippetInput) { in.SystemdUnits = []string{"a"} }, // Snippet wants normalised units
		"account with rights":  func(in *sshhost.SnippetInput) { in.Account = "root ALL=(ALL)" },
		"upper case account":   func(in *sshhost.SnippetInput) { in.Account = "Remedy" },
		"empty account":        func(in *sshhost.SnippetInput) { in.Account = "" },
		"newline in account":   func(in *sshhost.SnippetInput) { in.Account = "remedy\nroot" },
		"key with a newline":   func(in *sshhost.SnippetInput) { in.AuthorizedKey = "ssh-ed25519 AAAA\ncommand=\"/bin/sh\" ssh-rsa BBBB" },
		"key with options":     func(in *sshhost.SnippetInput) { in.AuthorizedKey = `command="/bin/sh" ssh-ed25519 AAAA` },
		"key of an odd type":   func(in *sshhost.SnippetInput) { in.AuthorizedKey = "ssh-dss AAAA" },
		"too many units": func(in *sshhost.SnippetInput) {
			in.SystemdUnits = nil
			for i := 0; i < 21; i++ {
				in.SystemdUnits = append(in.SystemdUnits, "u"+strings.Repeat("a", i)+".service")
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := base
			in.SystemdUnits = append([]string(nil), base.SystemdUnits...)
			mutate(&in)
			if out, err := sshhost.Snippet(in); err == nil {
				t.Fatalf("expected an error, got a snippet:\n%s\n%s", out.AuthorizedKeys, out.Sudoers)
			}
		})
	}
}
```

- [ ] **Step 2: Run to see it fail**

Run: `go test ./internal/sshhost -run Snippet -count=1`
Expected: FAIL to compile (`undefined: sshhost.Snippet`).

- [ ] **Step 3: Implement `validate.go` and `snippet.go`**

`internal/sshhost/validate.go`:

```go
// Package sshhost is Remedy's SSH access to hosts: the checks on a host's fields, the onboarding snippet, a client that only
// talks to a pinned host key, and the report of what an account may do.
package sshhost

import (
	"net"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	MaxNotes = 2000
	MaxUnits = 20
)

var (
	nameRE     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$`)
	hostnameRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	accountRE  = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	dockerRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}\*?$`)
	unitRE     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@:-]{0,100}\.(service|timer|socket)$`)
)

// FieldError is a problem with one field, worded so that it can be shown to the maintainer as it is.
type FieldError struct{ Field, Msg string }

func (e *FieldError) Error() string { return e.Msg }

// Fields are the editable fields of a host that end up in a file or a connection.
type Fields struct {
	Name          string
	Address       string
	Port          int
	Account       string
	Notes         string
	DockerPattern string
	SystemdUnits  []string
}

// Validate checks every field and normalises SystemdUnits in place. The first problem is returned.
func (f *Fields) Validate() error {
	if !nameRE.MatchString(f.Name) {
		return &FieldError{"name", "the name may contain letters, digits, dot, dash and underscore, 1 to 40 characters"}
	}
	if net.ParseIP(f.Address) == nil && !hostnameRE.MatchString(f.Address) {
		return &FieldError{"address", "the address must be a host name or an IP address"}
	}
	if f.Port < 1 || f.Port > 65535 {
		return &FieldError{"port", "the port must be between 1 and 65535"}
	}
	if err := checkAccount(f.Account); err != nil {
		return err
	}
	if utf8.RuneCountInString(f.Notes) > MaxNotes || strings.ContainsRune(f.Notes, 0) {
		return &FieldError{"notes", "the notes may have at most 2000 characters"}
	}
	if err := checkDocker(f.DockerPattern); err != nil {
		return err
	}
	units, err := normalizeUnits(f.SystemdUnits)
	if err != nil {
		return err
	}
	f.SystemdUnits = units
	return nil
}

func checkAccount(a string) error {
	if !accountRE.MatchString(a) {
		return &FieldError{"account", "the account must be a lower-case user name such as remedy"}
	}
	return nil
}

// checkDocker accepts an empty pattern (no docker rules) or one container name that may end in a single star.
func checkDocker(p string) error {
	if p == "" {
		return nil
	}
	if !dockerRE.MatchString(p) || strings.EqualFold(p, "ALL") {
		return &FieldError{"dockerPattern", "the container pattern must be a name such as remedy-*, with at most one star at the end"}
	}
	return nil
}

// normalizeUnits gives a unit without .service, .timer or .socket the suffix .service, drops duplicates and checks each unit.
func normalizeUnits(units []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, u := range units {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		if !strings.HasSuffix(u, ".service") && !strings.HasSuffix(u, ".timer") && !strings.HasSuffix(u, ".socket") {
			u += ".service"
		}
		if !unitRE.MatchString(u) {
			return nil, &FieldError{"systemdUnits", "a systemd unit may contain letters, digits and _ . @ : -, and ends in .service, .timer or .socket"}
		}
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	if len(out) > MaxUnits {
		return nil, &FieldError{"systemdUnits", "at most 20 units"}
	}
	return out, nil
}
```

`internal/sshhost/snippet.go`:

```go
package sshhost

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var authLineRE = regexp.MustCompile(`^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp(256|384|521)) [A-Za-z0-9+/=]+$`)

// SnippetInput is what the onboarding snippet is made from. Every value is checked again here, because the output is a file
// that grants rights.
type SnippetInput struct {
	Account       string
	AuthorizedKey string // the public key line of the login, or empty (a password login needs none)
	DockerPattern string
	SystemdUnits  []string // already normalised: each ends in .service, .timer or .socket
}

type SnippetOutput struct {
	AuthorizedKeys string
	Sudoers        string
}

// Snippet makes the text the maintainer runs on the host. It never runs anything itself. A value that does not pass its check
// is an error, not text.
func Snippet(in SnippetInput) (SnippetOutput, error) {
	if err := checkAccount(in.Account); err != nil {
		return SnippetOutput{}, err
	}
	if err := checkDocker(in.DockerPattern); err != nil {
		return SnippetOutput{}, err
	}
	if len(in.SystemdUnits) > MaxUnits {
		return SnippetOutput{}, errors.New("too many units")
	}
	for _, u := range in.SystemdUnits {
		if !unitRE.MatchString(u) {
			return SnippetOutput{}, fmt.Errorf("the unit %q is not valid", u)
		}
	}
	var out SnippetOutput

	if in.AuthorizedKey != "" {
		if !authLineRE.MatchString(in.AuthorizedKey) {
			return SnippetOutput{}, errors.New("the public key is not a plain key line")
		}
		out.AuthorizedKeys = "# Append to ~" + in.Account + "/.ssh/authorized_keys (mode 600, owned by the account)\n" + in.AuthorizedKey + "\n"
	}

	var b strings.Builder
	b.WriteString("# Save as /etc/sudoers.d/remedy with: sudo visudo -f /etc/sudoers.d/remedy\n")
	b.WriteString("# Read every line. Check the paths first: command -v docker; command -v systemctl\n")
	b.WriteString("# Reading the journal needs the group systemd-journal, not sudo.\n")
	if p := in.DockerPattern; p != "" {
		a := in.Account + " ALL=(root) NOPASSWD: "
		b.WriteString(a + "/usr/bin/docker ps -a\n")
		b.WriteString(a + "/usr/bin/docker logs --tail * " + p + "\n")
		b.WriteString(a + "/usr/bin/docker inspect " + p + "\n")
		b.WriteString(a + "/usr/bin/docker start " + p + ", /usr/bin/docker restart " + p + ", /usr/bin/docker stop " + p + "\n")
	}
	for _, u := range in.SystemdUnits {
		b.WriteString(in.Account + " ALL=(root) NOPASSWD: /usr/bin/systemctl start " + u +
			", /usr/bin/systemctl restart " + u + ", /usr/bin/systemctl stop " + u + "\n")
	}
	out.Sudoers = b.String()
	return out, nil
}
```

- [ ] **Step 4: Write the validation test**

`internal/sshhost/validate_test.go`:

```go
package sshhost_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/sshhost"
)

func valid() sshhost.Fields {
	return sshhost.Fields{Name: "nas", Address: "192.168.178.118", Port: 22, Account: "remedy"}
}

func TestValidateAcceptsGoodFields(t *testing.T) {
	for name, mutate := range map[string]func(f *sshhost.Fields){
		"plain":         func(f *sshhost.Fields) {},
		"a host name":   func(f *sshhost.Fields) { f.Address = "nas.homeserver" },
		"upper case":    func(f *sshhost.Fields) { f.Address = "NAS.Example.com"; f.Name = "Nas-1" },
		"an IPv6 literal": func(f *sshhost.Fields) { f.Address = "fe80::1" },
		"notes":         func(f *sshhost.Fields) { f.Notes = "Paperless lives here.\nSecond line." },
		"docker":        func(f *sshhost.Fields) { f.DockerPattern = "remedy-*" },
	} {
		t.Run(name, func(t *testing.T) {
			f := valid()
			mutate(&f)
			if err := f.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

func TestValidateRejects(t *testing.T) {
	for name, mutate := range map[string]func(f *sshhost.Fields){
		"empty name":      func(f *sshhost.Fields) { f.Name = "" },
		"name with space": func(f *sshhost.Fields) { f.Name = "my nas" },
		"long name":       func(f *sshhost.Fields) { f.Name = strings.Repeat("a", 41) },
		"newline name":    func(f *sshhost.Fields) { f.Name = "nas\n" },
		"empty address":   func(f *sshhost.Fields) { f.Address = "" },
		"address option":  func(f *sshhost.Fields) { f.Address = "-oProxyCommand=x" },
		"address spaces":  func(f *sshhost.Fields) { f.Address = "a b" },
		"port 0":          func(f *sshhost.Fields) { f.Port = 0 },
		"port 70000":      func(f *sshhost.Fields) { f.Port = 70000 },
		"account":         func(f *sshhost.Fields) { f.Account = "Root" },
		"long notes":      func(f *sshhost.Fields) { f.Notes = strings.Repeat("x", 2001) },
		"nul in notes":    func(f *sshhost.Fields) { f.Notes = "a\x00b" },
		"docker":          func(f *sshhost.Fields) { f.DockerPattern = "a b" },
		"unit":            func(f *sshhost.Fields) { f.SystemdUnits = []string{"a;b"} },
	} {
		t.Run(name, func(t *testing.T) {
			f := valid()
			mutate(&f)
			err := f.Validate()
			var fe *sshhost.FieldError
			if !errors.As(err, &fe) || fe.Msg == "" {
				t.Fatalf("Validate = %v, want a FieldError", err)
			}
		})
	}
}

func TestValidateNormalisesUnits(t *testing.T) {
	f := valid()
	f.SystemdUnits = []string{" node_exporter ", "backup.timer", "node_exporter.service", "", "x.socket"}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	want := []string{"node_exporter.service", "backup.timer", "x.socket"}
	if !reflect.DeepEqual(f.SystemdUnits, want) {
		t.Fatalf("units = %v, want %v", f.SystemdUnits, want)
	}
}
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `gofmt -l internal/sshhost; go vet ./internal/sshhost && go test ./internal/sshhost -count=1`
Expected: no gofmt output (run `gofmt -w` if the aligned map literals in the tests are listed), vet clean, PASS.

- [ ] **Step 6: Commit**

```sh
git add internal/sshhost
git commit -m "feat(sshhost): validation of host fields and the onboarding snippet

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The report and the `sudo -n -l` parser

**Files:**
- Create: `internal/sshhost/report.go`, `internal/sshhost/report_test.go`

**Interfaces:**
- Produces (used by Tasks 4 and 5): `sshhost.Report{At time.Time; Account string; Groups []string; System, Sudo string; SudoRules, Warnings []string}` with JSON tags `at, account, groups, system, sudo, sudoRules, warnings`; constants `SudoNone = "none"`, `SudoLimited = "limited"`, `SudoUnrestricted = "unrestricted"`; `sshhost.Outcome{Account, GroupsOut, System, SudoOut string; SudoOK bool}`; `sshhost.BuildReport(at time.Time, o Outcome) Report`; `sshhost.SudoCheck{Level string; Rules []string; Understood bool}`; `sshhost.ParseSudoList(out string, ok bool) SudoCheck`.

- [ ] **Step 1: Write the failing test**

`internal/sshhost/report_test.go`:

```go
package sshhost_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/sshhost"
)

const limitedOut = `Matching Defaults entries for remedy on nas:
    env_reset, mail_badpass, secure_path=/usr/local/sbin\:/usr/local/bin

User remedy may run the following commands on nas:
    (root) NOPASSWD: /usr/bin/docker ps -a
    (root) NOPASSWD: /usr/bin/docker logs --tail * remedy-*
`

func TestParseSudoList(t *testing.T) {
	cases := []struct {
		name       string
		out        string
		ok         bool
		level      string
		understood bool
		rules      int
	}{
		{"exact rules", limitedOut, true, sshhost.SudoLimited, true, 2},
		{"everything without a password", "User a may run the following commands on h:\n    (ALL : ALL) NOPASSWD: ALL\n", true, sshhost.SudoUnrestricted, true, 1},
		{"everything, short form", "User a may run the following commands on h:\n    (ALL) NOPASSWD: ALL\n", true, sshhost.SudoUnrestricted, true, 1},
		{"ALL after other commands", "User a may run the following commands on h:\n    (root) NOPASSWD: /bin/ls, /bin/cat, ALL\n", true, sshhost.SudoUnrestricted, true, 1},
		{"ALL behind a tag", "User a may run the following commands on h:\n    (ALL) NOPASSWD: SETENV: ALL\n", true, sshhost.SudoUnrestricted, true, 1},
		{"ALL that needs a password", "User a may run the following commands on h:\n    (ALL) ALL\n    (root) NOPASSWD: /usr/bin/true\n", true, sshhost.SudoLimited, true, 2},
		{"ALL after PASSWD resets the tag", "User a may run the following commands on h:\n    (ALL) NOPASSWD: /bin/ls, PASSWD: ALL\n", true, sshhost.SudoLimited, true, 1},
		{"password required", "sudo: a password is required\n", false, sshhost.SudoNone, true, 0},
		{"not allowed", "Sorry, user a may not run sudo on h.\n", false, sshhost.SudoNone, true, 0},
		{"sudo is not installed", "sh: 1: sudo: not found\n", false, sshhost.SudoNone, true, 0},
		{"an output nobody expected", "Some other sudo says: hello\n", true, sshhost.SudoLimited, false, 0},
		{"empty output that succeeded", "", true, sshhost.SudoLimited, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sshhost.ParseSudoList(c.out, c.ok)
			if got.Level != c.level || got.Understood != c.understood || len(got.Rules) != c.rules {
				t.Fatalf("ParseSudoList = %+v, want level %s, understood %v, %d rules", got, c.level, c.understood, c.rules)
			}
		})
	}
}

func TestParseSudoListBoundsTheRules(t *testing.T) {
	var b strings.Builder
	b.WriteString("User a may run the following commands on h:\n")
	for i := 0; i < 60; i++ {
		b.WriteString("    (root) NOPASSWD: /usr/bin/" + strings.Repeat("x", 300) + "\n")
	}
	got := sshhost.ParseSudoList(b.String(), true)
	if len(got.Rules) != 40 {
		t.Fatalf("kept %d rules, want 40", len(got.Rules))
	}
	for _, r := range got.Rules {
		if len([]rune(r)) > 200 {
			t.Fatalf("a rule has %d characters", len([]rune(r)))
		}
	}
}

func TestBuildReport(t *testing.T) {
	at := time.Date(2026, 10, 9, 18, 30, 0, 0, time.UTC)
	got := sshhost.BuildReport(at, sshhost.Outcome{
		Account: "remedy\n", GroupsOut: "remedy users docker\n", System: "Linux 6.8.0\n", SudoOut: limitedOut, SudoOK: true,
	})
	want := sshhost.Report{
		At: at, Account: "remedy", Groups: []string{"remedy", "users", "docker"}, System: "Linux 6.8.0",
		Sudo: sshhost.SudoLimited, SudoRules: []string{"(root) NOPASSWD: /usr/bin/docker ps -a", "(root) NOPASSWD: /usr/bin/docker logs --tail * remedy-*"},
		Warnings: []string{"The account is in the group docker, which is the same as root."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("report = %+v\nwant     %+v", got, want)
	}
}

func TestBuildReportWarnings(t *testing.T) {
	root := sshhost.BuildReport(time.Now(), sshhost.Outcome{Account: "root\n", GroupsOut: "root\n", SudoOK: false})
	if root.Sudo != sshhost.SudoNone || len(root.Warnings) != 1 || root.Warnings[0] != "The account is root." {
		t.Fatalf("root report = %+v", root)
	}
	all := sshhost.BuildReport(time.Now(), sshhost.Outcome{
		Account: "jaydee\n", GroupsOut: "jaydee sudo\n", SudoOut: "User jaydee may run the following commands on h:\n    (ALL : ALL) NOPASSWD: ALL\n", SudoOK: true,
	})
	if all.Sudo != sshhost.SudoUnrestricted || len(all.Warnings) != 1 || all.Warnings[0] != "sudo allows every command without a password." {
		t.Fatalf("unrestricted report = %+v", all)
	}
	odd := sshhost.BuildReport(time.Now(), sshhost.Outcome{Account: "a\n", GroupsOut: "a\n", SudoOut: "gibberish\n", SudoOK: true})
	if odd.Sudo != sshhost.SudoLimited || len(odd.Warnings) != 1 || odd.Warnings[0] != "I could not read the sudo rules; check them yourself." {
		t.Fatalf("unreadable report = %+v", odd)
	}
}

func TestBuildReportCleansWhatTheHostSays(t *testing.T) {
	got := sshhost.BuildReport(time.Now(), sshhost.Outcome{
		Account: "re\x1b[31mmedy\n", GroupsOut: strings.Repeat("g", 200) + " ok\n", System: strings.Repeat("L", 300), SudoOK: false,
	})
	if strings.ContainsRune(got.Account, 0x1b) {
		t.Fatalf("account keeps a control character: %q", got.Account)
	}
	if len([]rune(got.Groups[0])) > 64 || len([]rune(got.System)) > 100 {
		t.Fatalf("not bounded: group %d, system %d", len([]rune(got.Groups[0])), len([]rune(got.System)))
	}
}
```

- [ ] **Step 2: Run to see it fail**

Run: `go test ./internal/sshhost -run 'Sudo|Report' -count=1`
Expected: FAIL to compile (`undefined: sshhost.ParseSudoList`).

- [ ] **Step 3: Implement `internal/sshhost/report.go`**

```go
package sshhost

import (
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	SudoNone         = "none"
	SudoLimited      = "limited"
	SudoUnrestricted = "unrestricted"

	maxRules    = 40
	maxRuleLen  = 200
	maxGroupLen = 64
	maxSysLen   = 100
	maxNameLen  = 32
)

// Report is what a connection test learned about the account. Everything in it came from the host and is shown as text.
type Report struct {
	At        time.Time `json:"at"`
	Account   string    `json:"account"`
	Groups    []string  `json:"groups"`
	System    string    `json:"system"`
	Sudo      string    `json:"sudo"`
	SudoRules []string  `json:"sudoRules"`
	Warnings  []string  `json:"warnings"`
}

// Outcome is the raw answer of the four fixed test commands.
type Outcome struct {
	Account   string // id -un
	GroupsOut string // id -Gn
	System    string // uname -sr
	SudoOut   string // sudo -n -l
	SudoOK    bool   // sudo -n -l exited 0
}

// SudoCheck is the reading of the output of sudo -n -l.
type SudoCheck struct {
	Level      string
	Rules      []string
	Understood bool
}

var (
	tagRE     = regexp.MustCompile(`^([A-Z][A-Z_]*):\s*`)
	runasRE   = regexp.MustCompile(`^\([^)]*\)\s*`)
	listingRE = regexp.MustCompile(`(?i)may run the following commands`)
)

// ParseSudoList reads the output of `sudo -n -l`. It is tolerant: when it cannot tell, it says `limited` and Understood false,
// and it never says `none` for an output it did not understand. A failed command is `none`.
func ParseSudoList(out string, ok bool) SudoCheck {
	if !ok {
		return SudoCheck{Level: SudoNone, Understood: true}
	}
	lines := strings.Split(out, "\n")
	start := -1
	for i, l := range lines {
		if listingRE.MatchString(l) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return SudoCheck{Level: SudoLimited, Understood: false}
	}
	check := SudoCheck{Level: SudoLimited, Understood: true}
	for _, l := range lines[start:] {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if len(check.Rules) < maxRules {
			check.Rules = append(check.Rules, truncate(clean(l), maxRuleLen))
		}
		if allowsAllWithoutPassword(l) {
			check.Level = SudoUnrestricted
		}
	}
	if len(check.Rules) == 0 {
		check.Understood = false
	}
	return check
}

// allowsAllWithoutPassword reads one rule line such as "(ALL : ALL) NOPASSWD: ALL": after the run-as list come comma separated
// commands, each optionally behind tags; NOPASSWD: holds until PASSWD: resets it.
func allowsAllWithoutPassword(line string) bool {
	rest := runasRE.ReplaceAllString(line, "")
	if rest == line {
		return false // not a rule line
	}
	nopasswd := false
	for _, entry := range strings.Split(rest, ",") {
		entry = strings.TrimSpace(entry)
		for {
			m := tagRE.FindStringSubmatch(entry)
			if m == nil {
				break
			}
			switch m[1] {
			case "NOPASSWD":
				nopasswd = true
			case "PASSWD":
				nopasswd = false
			}
			entry = entry[len(m[0]):]
		}
		if entry == "ALL" && nopasswd {
			return true
		}
	}
	return false
}

// BuildReport turns the answers of the test commands into the report, with its warnings.
func BuildReport(at time.Time, o Outcome) Report {
	r := Report{At: at.UTC(), Account: truncate(clean(strings.TrimSpace(o.Account)), maxNameLen), System: truncate(clean(strings.TrimSpace(o.System)), maxSysLen)}
	r.Groups = []string{}
	for _, g := range strings.Fields(o.GroupsOut) {
		r.Groups = append(r.Groups, truncate(clean(g), maxGroupLen))
	}
	sudo := ParseSudoList(o.SudoOut, o.SudoOK)
	r.Sudo = sudo.Level
	r.SudoRules = sudo.Rules
	if r.SudoRules == nil {
		r.SudoRules = []string{}
	}
	r.Warnings = []string{}
	if r.Account == "root" {
		r.Warnings = append(r.Warnings, "The account is root.")
	}
	for _, g := range r.Groups {
		if g == "docker" {
			r.Warnings = append(r.Warnings, "The account is in the group docker, which is the same as root.")
			break
		}
	}
	if sudo.Level == SudoUnrestricted {
		r.Warnings = append(r.Warnings, "sudo allows every command without a password.")
	}
	if !sudo.Understood && o.SudoOK {
		r.Warnings = append(r.Warnings, "I could not read the sudo rules; check them yourself.")
	}
	return r
}

// clean drops control characters, so that nothing a host prints can move a cursor or colour a page.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `gofmt -l internal/sshhost; go vet ./internal/sshhost && go test ./internal/sshhost -count=1`
Expected: PASS. If `TestParseSudoList/ALL_that_needs_a_password` fails, the rule line `(ALL) ALL` must give `limited`: `entry == "ALL"` with `nopasswd == false` returns nothing, which is what the code does.

- [ ] **Step 5: Commit**

```sh
git add internal/sshhost
git commit -m "feat(sshhost): report of an account's rights and a tolerant sudo -l parser

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 4: Credentials, the SSH client and the test server

**Files:**
- Create: `internal/sshhost/credential.go`, `internal/sshhost/client.go`, `internal/sshhost/service.go`, `internal/sshhost/export_test.go`, `internal/sshhost/sshtest/sshtest.go`
- Create (tests): `internal/sshhost/credential_test.go`, `internal/sshhost/client_test.go`

**Interfaces:**
- Consumes: `sshhost.Report`, `sshhost.Outcome`, `sshhost.BuildReport` (Task 3).
- Produces (used by Task 5):
  - `sshhost.Credential{Kind string; Key, Passphrase, Password secret.Value}`, constants `KindKey = "key"`, `KindPassword = "password"`.
  - `sshhost.KeyInfo{PublicLine, Fingerprint string}`.
  - `sshhost.NewKeyCredential(pem, passphrase string) (Credential, KeyInfo, error)`, `sshhost.NewPasswordCredential(pw string) (Credential, error)`, `(Credential).Marshal() ([]byte, error)`, `sshhost.ParseCredential([]byte) (Credential, error)`.
  - Errors: `ErrKeyFormat`, `ErrPassphraseNeeded`, `ErrPassphraseWrong`, `ErrPasswordInvalid`, `ErrBadCredential`, `ErrUnreachable`, `ErrRefused`, `ErrTimeout`, `ErrAuth`, `ErrHostKeyChanged`, `ErrNoPin`.
  - `sshhost.Target{Address string; Port int; Account string}`, `sshhost.HostKey{Type, Fingerprint, Line string}`, `sshhost.FingerprintOfLine(line string) (string, error)`.
  - `sshhost.Service` (zero value ready): `(*Service).Probe(ctx, hostID int64, Target) (HostKey, error)` and `(*Service).Check(ctx, hostID int64, Target, Credential, pinLine string) (Report, error)`.
  - Package-level `sshhost.Probe`, `sshhost.Dial(ctx, Target, Credential, pinLine string) (*Client, error)`, `(*Client).Run(ctx, cmd string) (Result, error)`, `(*Client).Close() error`, `sshhost.Result{Output string; ExitCode int; Truncated bool}`, `sshhost.MaxOutput = 16 << 10`.
  - `sshtest`: `Start(testing.TB, Config) *Server`, `Config{Password string; Key ssh.PublicKey; Commands map[string]Reply; HostSigner ssh.Signer}`, `Reply{Out string; Exit int; Delay time.Duration}`, `(*Server).Host`, `.Port`, `.HostKey() ssh.PublicKey`, `.SetHostSigner(ssh.Signer)`, `.Attempts() int`, `.MaxConcurrent() int`, `.Close()`, `NewSigner(testing.TB) ssh.Signer`, `NewRSASigner(testing.TB) ssh.Signer`, `KeyPEM(testing.TB, passphrase string) (pem string, pub ssh.PublicKey)`.

- [ ] **Step 1: Write the test server `internal/sshhost/sshtest/sshtest.go`**

```go
// Package sshtest is an SSH server for tests. It runs in the test process, accepts only the login it is given, and answers
// exactly the commands it is given. It lets the host key be swapped between connections.
package sshtest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// Reply is the answer to one command line.
type Reply struct {
	Out   string
	Exit  int
	Delay time.Duration // how long the server waits before it answers
}

type Config struct {
	Password   string        // the password that logs in; empty accepts none
	Key        ssh.PublicKey // the public key that logs in; nil accepts none
	Commands   map[string]Reply
	HostSigner ssh.Signer // nil: a new ed25519 key
}

type Server struct {
	Host string
	Port int

	cfg       Config
	ln        net.Listener
	mu        sync.Mutex
	signer    ssh.Signer
	attempts  int
	open      int
	maxOpen   int
	conns     map[net.Conn]struct{}
}

// Start listens on a free port of 127.0.0.1 and serves until the test ends.
func Start(t testing.TB, cfg Config) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, cfg: cfg, ln: ln, signer: cfg.HostSigner, conns: map[net.Conn]struct{}{}}
	if s.signer == nil {
		s.signer = NewSigner(t)
	}
	go s.accept()
	t.Cleanup(s.Close)
	return s
}

func (s *Server) Close() {
	_ = s.ln.Close()
	s.mu.Lock()
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
}

func (s *Server) HostKey() ssh.PublicKey { s.mu.Lock(); defer s.mu.Unlock(); return s.signer.PublicKey() }

// SetHostSigner makes the server present another host key from the next connection on.
func (s *Server) SetHostSigner(sg ssh.Signer) { s.mu.Lock(); s.signer = sg; s.mu.Unlock() }

// Attempts counts the authentication attempts (a password or a public key offered). A connection that stops at the key
// exchange makes none.
func (s *Server) Attempts() int { s.mu.Lock(); defer s.mu.Unlock(); return s.attempts }

// MaxConcurrent is the highest number of connections that were open at the same time.
func (s *Server) MaxConcurrent() int { s.mu.Lock(); defer s.mu.Unlock(); return s.maxOpen }

func (s *Server) attempt() { s.mu.Lock(); s.attempts++; s.mu.Unlock() }

func (s *Server) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func (s *Server) serverConfig() *ssh.ServerConfig {
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			s.attempt()
			if s.cfg.Password != "" && string(pw) == s.cfg.Password {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
		PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			s.attempt()
			if s.cfg.Key != nil && bytes.Equal(k.Marshal(), s.cfg.Key.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
	}
	s.mu.Lock()
	cfg.AddHostKey(s.signer)
	s.mu.Unlock()
	return cfg
}

func (s *Server) handle(nc net.Conn) {
	s.mu.Lock()
	s.conns[nc] = struct{}{}
	s.open++
	if s.open > s.maxOpen {
		s.maxOpen = s.open
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, nc)
		s.open--
		s.mu.Unlock()
		_ = nc.Close()
	}()

	sc, chans, reqs, err := ssh.NewServerConn(nc, s.serverConfig())
	if err != nil {
		return
	}
	defer sc.Close()
	go ssh.DiscardRequests(reqs)
	for nch := range chans {
		if nch.ChannelType() != "session" {
			_ = nch.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		ch, creqs, err := nch.Accept()
		if err != nil {
			return
		}
		s.session(ch, creqs)
	}
}

// session answers one exec request per session, then closes the channel.
func (s *Server) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		var p struct{ Command string }
		if err := ssh.Unmarshal(req.Payload, &p); err != nil {
			_ = req.Reply(false, nil)
			return
		}
		_ = req.Reply(true, nil)
		r, ok := s.cfg.Commands[p.Command]
		if !ok {
			r = Reply{Out: "unknown command: " + p.Command + "\n", Exit: 127}
		}
		time.Sleep(r.Delay)
		_, _ = io.WriteString(ch, r.Out)
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(r.Exit)}))
		return
	}
}

// NewSigner makes an ed25519 key.
func NewSigner(t testing.TB) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sg, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return sg
}

// NewRSASigner makes an RSA key, to show a host key of another type than ed25519.
func NewRSASigner(t testing.TB) ssh.Signer {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	sg, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return sg
}

// KeyPEM makes an ed25519 login key in the OpenSSH private key format, optionally protected by a passphrase, and returns it
// with its public key.
func KeyPEM(t testing.TB, passphrase string) (string, ssh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	sg, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(block)), sg.PublicKey()
}
```

- [ ] **Step 2: Write the failing credential tests**

`internal/sshhost/credential_test.go`:

```go
package sshhost_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/Jaydee94/remedy/internal/sshhost"
	"github.com/Jaydee94/remedy/internal/sshhost/sshtest"
)

func TestNewKeyCredential(t *testing.T) {
	plain, pub := sshtest.KeyPEM(t, "")
	wantLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))

	for name, pem := range map[string]string{
		"as written":           plain,
		"windows line endings": strings.ReplaceAll(plain, "\n", "\r\n"),
		"no final newline":     strings.TrimRight(plain, "\n"),
		"trailing spaces":      strings.ReplaceAll(plain, "\n", "  \n"),
		"padded with blanks":   "\n\n" + plain + "\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			cred, info, err := sshhost.NewKeyCredential(pem, "")
			if err != nil {
				t.Fatalf("NewKeyCredential: %v", err)
			}
			if cred.Kind != sshhost.KindKey || info.PublicLine != wantLine || !strings.HasPrefix(info.Fingerprint, "SHA256:") {
				t.Fatalf("credential = %+v, info = %+v", cred, info)
			}
		})
	}

	protected, _ := sshtest.KeyPEM(t, "correct horse")
	if _, _, err := sshhost.NewKeyCredential(protected, ""); !errors.Is(err, sshhost.ErrPassphraseNeeded) {
		t.Fatalf("no passphrase: %v", err)
	}
	if _, _, err := sshhost.NewKeyCredential(protected, "wrong"); !errors.Is(err, sshhost.ErrPassphraseWrong) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	if _, info, err := sshhost.NewKeyCredential(protected, "correct horse"); err != nil || info.PublicLine == "" {
		t.Fatalf("right passphrase: %v", err)
	}
	// A passphrase for a key that has none is ignored, not an error.
	if _, _, err := sshhost.NewKeyCredential(plain, "unused"); err != nil {
		t.Fatalf("passphrase for an unprotected key: %v", err)
	}

	for name, pem := range map[string]string{
		"garbage":    "this is not a key",
		"empty":      "",
		"public key": string(ssh.MarshalAuthorizedKey(pub)),
		"too large":  strings.Repeat("A", 20<<10),
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			if _, _, err := sshhost.NewKeyCredential(pem, ""); !errors.Is(err, sshhost.ErrKeyFormat) {
				t.Fatalf("err = %v, want ErrKeyFormat", err)
			}
		})
	}
}

func TestPasswordCredential(t *testing.T) {
	if _, err := sshhost.NewPasswordCredential(""); !errors.Is(err, sshhost.ErrPasswordInvalid) {
		t.Fatalf("empty password: %v", err)
	}
	if _, err := sshhost.NewPasswordCredential(strings.Repeat("x", 257)); !errors.Is(err, sshhost.ErrPasswordInvalid) {
		t.Fatalf("long password: %v", err)
	}
	if _, err := sshhost.NewPasswordCredential("a\x00b"); !errors.Is(err, sshhost.ErrPasswordInvalid) {
		t.Fatalf("password with a NUL: %v", err)
	}
	if c, err := sshhost.NewPasswordCredential("hunter2"); err != nil || c.Kind != sshhost.KindPassword {
		t.Fatalf("password: %+v, %v", c, err)
	}
}

func TestCredentialRoundTripAndVersion(t *testing.T) {
	pem, _ := sshtest.KeyPEM(t, "pp")
	in, _, err := sshhost.NewKeyCredential(pem, "pp")
	if err != nil {
		t.Fatal(err)
	}
	blob, err := in.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	out, err := sshhost.ParseCredential(blob)
	if err != nil || out.Kind != sshhost.KindKey || out.Key.Reveal() == "" || out.Passphrase.Reveal() != "pp" {
		t.Fatalf("round trip = %+v, %v", out, err)
	}
	pw, _ := sshhost.NewPasswordCredential("hunter2")
	blob, _ = pw.Marshal()
	if got, err := sshhost.ParseCredential(blob); err != nil || got.Password.Reveal() != "hunter2" {
		t.Fatalf("password round trip = %+v, %v", got, err)
	}

	for name, bad := range map[string]string{
		"another version": `{"v":2,"kind":"password","password":"x"}`,
		"no version":      `{"kind":"password","password":"x"}`,
		"unknown kind":    `{"v":1,"kind":"token"}`,
		"not json":        `nope`,
	} {
		if _, err := sshhost.ParseCredential([]byte(bad)); !errors.Is(err, sshhost.ErrBadCredential) {
			t.Fatalf("%s: err = %v, want ErrBadCredential", name, err)
		}
	}
}

func TestACredentialNeverPrintsItsSecrets(t *testing.T) {
	pem, _ := sshtest.KeyPEM(t, "pp-DISTINCTIVE")
	c, _, _ := sshhost.NewKeyCredential(pem, "pp-DISTINCTIVE")
	for _, s := range []string{fmt.Sprint(c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c)} {
		if strings.Contains(s, "pp-DISTINCTIVE") || strings.Contains(s, "OPENSSH PRIVATE KEY") {
			t.Fatalf("a credential printed a secret: %s", s)
		}
	}
}
```

- [ ] **Step 3: Write the failing client tests**

`internal/sshhost/export_test.go`:

```go
package sshhost

import "time"

// SetCommandTimeout shortens the time a command may take, for a test. It returns the function that restores it. Tests that use it
// must not run in parallel.
func SetCommandTimeout(d time.Duration) (restore func()) {
	old := commandTimeout
	commandTimeout = d
	return func() { commandTimeout = old }
}
```

`internal/sshhost/client_test.go`:

```go
package sshhost_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/sshhost"
	"github.com/Jaydee94/remedy/internal/sshhost/sshtest"
)

func targetOf(s *sshtest.Server) sshhost.Target {
	return sshhost.Target{Address: s.Host, Port: s.Port, Account: "remedy"}
}

func lineOf(t *testing.T, s *sshtest.Server) string {
	t.Helper()
	k, err := sshhost.Probe(context.Background(), targetOf(s))
	if err != nil {
		t.Fatal(err)
	}
	return k.Line
}

func passwordCred(t *testing.T, pw string) sshhost.Credential {
	t.Helper()
	c, err := sshhost.NewPasswordCredential(pw)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestProbeReturnsTheKeyAndAuthenticatesNothing(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw"})
	k, err := sshhost.Probe(context.Background(), targetOf(srv))
	if err != nil {
		t.Fatal(err)
	}
	if k.Type != "ssh-ed25519" || !strings.HasPrefix(k.Fingerprint, "SHA256:") || !strings.HasPrefix(k.Line, "ssh-ed25519 ") {
		t.Fatalf("host key = %+v", k)
	}
	if got, err := sshhost.FingerprintOfLine(k.Line); err != nil || got != k.Fingerprint {
		t.Fatalf("FingerprintOfLine = %q, %v", got, err)
	}
	if srv.Attempts() != 0 {
		t.Fatalf("Probe made %d authentication attempts, want none", srv.Attempts())
	}
}

func TestDialRunsACommandWithAPassword(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: map[string]sshtest.Reply{"id -un": {Out: "remedy\n"}, "false": {Exit: 3}}})
	cl, err := sshhost.Dial(context.Background(), targetOf(srv), passwordCred(t, "pw"), lineOf(t, srv))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer cl.Close()
	got, err := cl.Run(context.Background(), "id -un")
	if err != nil || got.Output != "remedy\n" || got.ExitCode != 0 || got.Truncated {
		t.Fatalf("Run = %+v, %v", got, err)
	}
	if got, err := cl.Run(context.Background(), "false"); err != nil || got.ExitCode != 3 {
		t.Fatalf("a failing command = %+v, %v", got, err)
	}
}

func TestDialRunsACommandWithAKey(t *testing.T) {
	pem, pub := sshtest.KeyPEM(t, "pp")
	srv := sshtest.Start(t, sshtest.Config{Key: pub, Commands: map[string]sshtest.Reply{"id -un": {Out: "remedy\n"}}})
	cred, _, err := sshhost.NewKeyCredential(pem, "pp")
	if err != nil {
		t.Fatal(err)
	}
	cl, err := sshhost.Dial(context.Background(), targetOf(srv), cred, lineOf(t, srv))
	if err != nil {
		t.Fatalf("Dial with a key: %v", err)
	}
	defer cl.Close()
	if got, err := cl.Run(context.Background(), "id -un"); err != nil || got.Output != "remedy\n" {
		t.Fatalf("Run = %+v, %v", got, err)
	}
}

func TestDialWithoutAPinIsRefused(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw"})
	if _, err := sshhost.Dial(context.Background(), targetOf(srv), passwordCred(t, "pw"), ""); !errors.Is(err, sshhost.ErrNoPin) {
		t.Fatalf("err = %v, want ErrNoPin", err)
	}
	if srv.Attempts() != 0 {
		t.Fatal("a login was offered to a host whose key is not pinned")
	}
}

func TestDialRefusesAChangedKey(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw"})
	pin := lineOf(t, srv)
	srv.SetHostSigner(sshtest.NewSigner(t))
	if _, err := sshhost.Dial(context.Background(), targetOf(srv), passwordCred(t, "pw"), pin); !errors.Is(err, sshhost.ErrHostKeyChanged) {
		t.Fatalf("err = %v, want ErrHostKeyChanged", err)
	}
	if srv.Attempts() != 0 {
		t.Fatal("a login was offered to a host that showed another key")
	}
}

func TestDialRefusesAKeyOfAnotherType(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw"})
	pin := lineOf(t, srv) // ed25519
	srv.SetHostSigner(sshtest.NewRSASigner(t))
	if _, err := sshhost.Dial(context.Background(), targetOf(srv), passwordCred(t, "pw"), pin); !errors.Is(err, sshhost.ErrHostKeyChanged) {
		t.Fatalf("err = %v, want ErrHostKeyChanged (not unreachable)", err)
	}
}

func TestDialRejectsAWrongPassword(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw"})
	if _, err := sshhost.Dial(context.Background(), targetOf(srv), passwordCred(t, "nope"), lineOf(t, srv)); !errors.Is(err, sshhost.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
}

func TestRefusedConnection(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{})
	srv.Close()
	if _, err := sshhost.Probe(context.Background(), targetOf(srv)); !errors.Is(err, sshhost.ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused", err)
	}
}

func TestRunCutsLongOutput(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: map[string]sshtest.Reply{"big": {Out: strings.Repeat("x", 20000)}}})
	cl, err := sshhost.Dial(context.Background(), targetOf(srv), passwordCred(t, "pw"), lineOf(t, srv))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	got, err := cl.Run(context.Background(), "big")
	if err != nil || len(got.Output) != sshhost.MaxOutput || !got.Truncated {
		t.Fatalf("Run: len %d, truncated %v, err %v", len(got.Output), got.Truncated, err)
	}
}

func TestRunTimesOut(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: map[string]sshtest.Reply{"slow": {Out: "late", Delay: 2 * time.Second}}})
	cl, err := sshhost.Dial(context.Background(), targetOf(srv), passwordCred(t, "pw"), lineOf(t, srv))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	defer sshhost.SetCommandTimeout(200 * time.Millisecond)()
	start := time.Now()
	if _, err := cl.Run(context.Background(), "slow"); !errors.Is(err, sshhost.ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("the timeout took %v", time.Since(start))
	}
}

func okCommands() map[string]sshtest.Reply {
	return map[string]sshtest.Reply{
		"id -un":     {Out: "remedy\n"},
		"id -Gn":     {Out: "remedy users\n"},
		"uname -sr":  {Out: "Linux 6.8.0\n"},
		"sudo -n -l": {Out: "User remedy may run the following commands on nas:\n    (root) NOPASSWD: /usr/bin/docker ps -a\n"},
	}
}

func TestCheckBuildsTheReport(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: okCommands()})
	var svc sshhost.Service
	rep, err := svc.Check(context.Background(), 1, targetOf(srv), passwordCred(t, "pw"), lineOf(t, srv))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if rep.Account != "remedy" || rep.System != "Linux 6.8.0" || rep.Sudo != sshhost.SudoLimited || len(rep.SudoRules) != 1 || len(rep.Warnings) != 0 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestChecksOfOneHostDoNotOverlap(t *testing.T) {
	cmds := okCommands()
	cmds["id -un"] = sshtest.Reply{Out: "remedy\n", Delay: 150 * time.Millisecond}
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: cmds})
	pin := lineOf(t, srv)
	var svc sshhost.Service
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.Check(context.Background(), 7, targetOf(srv), passwordCred(t, "pw"), pin); err != nil {
				t.Errorf("Check: %v", err)
			}
		}()
	}
	wg.Wait()
	if srv.MaxConcurrent() != 1 {
		t.Fatalf("%d connections to one host were open at the same time, want 1", srv.MaxConcurrent())
	}
}
```

Note: `lineOf` itself opens a probe connection before the concurrent part, so it is closed before the checks start; `MaxConcurrent` counts connections, and the probe ends before.

- [ ] **Step 4: Run to see it fail**

Run: `go test ./internal/sshhost/... -count=1`
Expected: FAIL to compile (`undefined: sshhost.NewKeyCredential` and the like).

- [ ] **Step 5: Implement `credential.go`**

```go
package sshhost

import (
	"encoding/json"
	"errors"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/Jaydee94/remedy/internal/secret"
)

const (
	KindKey      = "key"
	KindPassword = "password"

	maxPasswordLen    = 256
	maxKeyLen         = 16 << 10
	credentialVersion = 1
)

var (
	ErrKeyFormat        = errors.New("that is not a private key I can read")
	ErrPassphraseNeeded = errors.New("the key is protected by a passphrase")
	ErrPassphraseWrong  = errors.New("the passphrase does not open the key")
	ErrPasswordInvalid  = errors.New("the password must have 1 to 256 characters")
	ErrBadCredential    = errors.New("the stored login cannot be read")
)

// Credential is a login. Its secrets are secret.Value: printing or logging one gives "***".
type Credential struct {
	Kind       string
	Key        secret.Value // the private key, as text
	Passphrase secret.Value
	Password   secret.Value
}

// KeyInfo is what can be shown about a private key without showing it.
type KeyInfo struct {
	PublicLine  string // the authorized_keys line
	Fingerprint string // SHA256:...
}

var okKeyTypes = map[string]bool{
	ssh.KeyAlgoED25519: true, ssh.KeyAlgoRSA: true,
	ssh.KeyAlgoECDSA256: true, ssh.KeyAlgoECDSA384: true, ssh.KeyAlgoECDSA521: true,
}

// normalizePEM makes a pasted key parseable: any line ending, trailing blanks, no final newline, blank lines around it.
func normalizePEM(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n")) + "\n"
}

func parseSigner(pemBytes []byte, passphrase string) (ssh.Signer, error) {
	raw, err := ssh.ParseRawPrivateKey(pemBytes)
	var missing *ssh.PassphraseMissingError
	switch {
	case errors.As(err, &missing):
		if passphrase == "" {
			return nil, ErrPassphraseNeeded
		}
		if raw, err = ssh.ParseRawPrivateKeyWithPassphrase(pemBytes, []byte(passphrase)); err != nil {
			return nil, ErrPassphraseWrong
		}
	case err != nil:
		return nil, ErrKeyFormat
	}
	signer, err := ssh.NewSignerFromKey(raw)
	if err != nil || !okKeyTypes[signer.PublicKey().Type()] {
		return nil, ErrKeyFormat
	}
	return signer, nil
}

// NewKeyCredential checks a pasted private key (and its passphrase, if it needs one) and derives what may be shown of it.
// Nothing in an error contains the key.
func NewKeyCredential(pemText, passphrase string) (Credential, KeyInfo, error) {
	if len(pemText) > maxKeyLen {
		return Credential{}, KeyInfo{}, ErrKeyFormat
	}
	pemText = normalizePEM(pemText)
	signer, err := parseSigner([]byte(pemText), passphrase)
	if err != nil {
		return Credential{}, KeyInfo{}, err
	}
	pub := signer.PublicKey()
	return Credential{Kind: KindKey, Key: secret.NewValue(pemText), Passphrase: secret.NewValue(passphrase)},
		KeyInfo{PublicLine: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))), Fingerprint: ssh.FingerprintSHA256(pub)}, nil
}

func NewPasswordCredential(pw string) (Credential, error) {
	if pw == "" || len(pw) > maxPasswordLen || strings.ContainsRune(pw, 0) {
		return Credential{}, ErrPasswordInvalid
	}
	return Credential{Kind: KindPassword, Password: secret.NewValue(pw)}, nil
}

// wire is the sealed form. v lets a later layout be told from this one.
type wire struct {
	V          int    `json:"v"`
	Kind       string `json:"kind"`
	Key        string `json:"key,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
	Password   string `json:"password,omitempty"`
}

// Marshal is what gets sealed. Handle the result as a secret.
func (c Credential) Marshal() ([]byte, error) {
	return json.Marshal(wire{V: credentialVersion, Kind: c.Kind, Key: c.Key.Reveal(), Passphrase: c.Passphrase.Reveal(), Password: c.Password.Reveal()})
}

func ParseCredential(b []byte) (Credential, error) {
	var w wire
	if err := json.Unmarshal(b, &w); err != nil || w.V != credentialVersion {
		return Credential{}, ErrBadCredential
	}
	switch w.Kind {
	case KindKey:
		return Credential{Kind: KindKey, Key: secret.NewValue(w.Key), Passphrase: secret.NewValue(w.Passphrase)}, nil
	case KindPassword:
		return Credential{Kind: KindPassword, Password: secret.NewValue(w.Password)}, nil
	}
	return Credential{}, ErrBadCredential
}

// methods are the ways the client offers this login: a key signs; a password is sent as a password and, for servers that only
// ask for it that way, as the answer to every keyboard-interactive question.
func (c Credential) methods() ([]ssh.AuthMethod, error) {
	switch c.Kind {
	case KindKey:
		signer, err := parseSigner([]byte(c.Key.Reveal()), c.Passphrase.Reveal())
		if err != nil {
			return nil, ErrBadCredential
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	case KindPassword:
		pw := c.Password.Reveal()
		return []ssh.AuthMethod{
			ssh.Password(pw),
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = pw
				}
				return answers, nil
			}),
		}, nil
	}
	return nil, ErrBadCredential
}
```

- [ ] **Step 6: Implement `client.go`**

```go
package sshhost

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

// MaxOutput is the most bytes of one command's output that are kept.
const MaxOutput = 16 << 10

var (
	connectTimeout = 10 * time.Second
	commandTimeout = 10 * time.Second
)

// The closed set of ways a connection fails. A raw error from the library is never passed on: it can contain the user name and
// the address.
var (
	ErrUnreachable    = errors.New("the host cannot be reached")
	ErrRefused        = errors.New("the host refused the connection")
	ErrTimeout        = errors.New("the host did not answer in time")
	ErrAuth           = errors.New("the login was rejected")
	ErrHostKeyChanged = errors.New("the host key is not the pinned one")
	ErrNoPin          = errors.New("the host key is not pinned")
)

var errProbeDone = errors.New("probe: key received")

type Target struct {
	Address string
	Port    int
	Account string
}

// HostKey is a host key as it can be shown and pinned.
type HostKey struct {
	Type        string
	Fingerprint string
	Line        string // authorized_keys format; what is stored as the pin
}

func hostKeyOf(k ssh.PublicKey) HostKey {
	return HostKey{Type: k.Type(), Fingerprint: ssh.FingerprintSHA256(k), Line: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))}
}

func parsePin(line string) (ssh.PublicKey, error) {
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	return pub, err
}

// FingerprintOfLine is the SHA-256 fingerprint of a pinned key line.
func FingerprintOfLine(line string) (string, error) {
	pub, err := parsePin(line)
	if err != nil {
		return "", err
	}
	return ssh.FingerprintSHA256(pub), nil
}

// algorithmsFor limits the host key algorithms to the pinned key's type, so that a server which offers several cannot make a
// correct pin fail, and a key of another type shows up as a changed key.
func algorithmsFor(pub ssh.PublicKey) []string {
	if pub.Type() == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}
	}
	return []string{pub.Type()}
}

func classify(err error) error {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return ErrTimeout
	case errors.Is(err, syscall.ECONNREFUSED):
		return ErrRefused
	case strings.Contains(err.Error(), "unable to authenticate"):
		return ErrAuth
	case strings.Contains(err.Error(), "no common algorithm for host key"):
		return ErrHostKeyChanged
	}
	return ErrUnreachable
}

func connect(ctx context.Context, t Target, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	d := net.Dialer{Timeout: connectTimeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(t.Address, strconv.Itoa(t.Port)))
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(connectTimeout))
	c, chans, reqs, err := ssh.NewClientConn(conn, conn.RemoteAddr().String(), cfg)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(c, chans, reqs), nil
}

// Probe connects for the key exchange only and returns the key the host presents. It sends no user name and no login.
func Probe(ctx context.Context, t Target) (HostKey, error) {
	var got ssh.PublicKey
	cfg := &ssh.ClientConfig{
		User: t.Account,
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			got = k
			return errProbeDone
		},
		Timeout: connectTimeout,
	}
	c, err := connect(ctx, t, cfg)
	if c != nil {
		_ = c.Close()
	}
	if got != nil {
		return hostKeyOf(got), nil
	}
	if err == nil {
		return HostKey{}, ErrUnreachable
	}
	return HostKey{}, classify(err)
}

// Client is a connection to a host whose key matched the pin.
type Client struct{ c *ssh.Client }

// Dial connects and logs in. It never offers a login to a host that does not present exactly the pinned key.
func Dial(ctx context.Context, t Target, cred Credential, pinLine string) (*Client, error) {
	if pinLine == "" {
		return nil, ErrNoPin
	}
	pin, err := parsePin(pinLine)
	if err != nil {
		return nil, ErrNoPin
	}
	methods, err := cred.methods()
	if err != nil {
		return nil, err
	}
	mismatch := false
	cfg := &ssh.ClientConfig{
		User:              t.Account,
		Auth:              methods,
		HostKeyAlgorithms: algorithmsFor(pin),
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			if bytes.Equal(k.Marshal(), pin.Marshal()) {
				return nil
			}
			mismatch = true
			return errors.New("host key mismatch")
		},
		Timeout: connectTimeout,
	}
	c, err := connect(ctx, t, cfg)
	if err != nil {
		if mismatch {
			return nil, ErrHostKeyChanged
		}
		return nil, classify(err)
	}
	return &Client{c: c}, nil
}

func (c *Client) Close() error { return c.c.Close() }

// Result is the answer of one command.
type Result struct {
	Output    string
	ExitCode  int
	Truncated bool
}

// capWriter keeps the first max bytes and notes that it dropped the rest. It is safe for the two streams of a session.
type capWriter struct {
	mu  sync.Mutex
	buf []byte
	max int
	cut bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	room := w.max - len(w.buf)
	if room > 0 {
		w.buf = append(w.buf, p[:min(room, len(p))]...)
	}
	if len(p) > room {
		w.cut = true
	}
	return len(p), nil
}

func (w *capWriter) result() (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.buf), w.cut
}

// Run runs one command in its own session: no pty, no environment, no shell of ours. cmd must be built from constants.
func (c *Client) Run(ctx context.Context, cmd string) (Result, error) {
	sess, err := c.c.NewSession()
	if err != nil {
		return Result{}, ErrUnreachable
	}
	defer sess.Close()
	w := &capWriter{max: MaxOutput}
	sess.Stdout, sess.Stderr = w, w
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	timer := time.NewTimer(commandTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		out, cut := w.result()
		res := Result{Output: out, Truncated: cut}
		var ee *ssh.ExitError
		switch {
		case errors.As(err, &ee):
			res.ExitCode = ee.ExitStatus()
		case err != nil:
			return res, ErrUnreachable
		}
		return res, nil
	case <-timer.C:
		return Result{}, ErrTimeout
	case <-ctx.Done():
		return Result{}, ErrTimeout
	}
}
```

- [ ] **Step 7: Implement `service.go`**

```go
package sshhost

import (
	"context"
	"sync"
	"time"
)

// testCommands are the four commands of a connection test. They are constants: no user input ever reaches them.
var testCommands = [4]string{"id -un", "id -Gn", "uname -sr", "sudo -n -l"}

// Service is what the control plane uses. The zero value is ready. It lets only one connection per host be open at a time,
// so two clicks on "Test" do not open two sessions.
type Service struct {
	mu    sync.Mutex
	locks map[int64]*sync.Mutex
}

func (s *Service) lock(hostID int64) func() {
	s.mu.Lock()
	if s.locks == nil {
		s.locks = map[int64]*sync.Mutex{}
	}
	m := s.locks[hostID]
	if m == nil {
		m = &sync.Mutex{}
		s.locks[hostID] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// Probe returns the key a host presents. See the package function.
func (s *Service) Probe(ctx context.Context, hostID int64, t Target) (HostKey, error) {
	defer s.lock(hostID)()
	return Probe(ctx, t)
}

// Check connects against the pinned key, runs the four test commands and returns the report.
func (s *Service) Check(ctx context.Context, hostID int64, t Target, cred Credential, pinLine string) (Report, error) {
	defer s.lock(hostID)()
	cl, err := Dial(ctx, t, cred, pinLine)
	if err != nil {
		return Report{}, err
	}
	defer cl.Close()
	var res [len(testCommands)]Result
	for i, cmd := range testCommands {
		if res[i], err = cl.Run(ctx, cmd); err != nil {
			return Report{}, err
		}
	}
	return BuildReport(time.Now(), Outcome{
		Account: res[0].Output, GroupsOut: res[1].Output, System: res[2].Output, SudoOut: res[3].Output, SudoOK: res[3].ExitCode == 0,
	}), nil
}
```

- [ ] **Step 8: Run the tests to see them pass**

Run: `gofmt -l internal/sshhost; go vet ./internal/sshhost/... && go test ./internal/sshhost/... -count=1 -race`
Expected: no gofmt output (run `gofmt -w internal/sshhost` if the `Server` struct's aligned fields are listed), vet clean, PASS.

If `TestDialRefusesAKeyOfAnotherType` fails with `ErrUnreachable`, print the error the library returns and make `classify` match the text for a host-key algorithm mismatch in this `x/crypto` version (the message contains "no common algorithm for host key" or a disconnect with that reason); do not weaken the test.

- [ ] **Step 9: Commit**

```sh
git add internal/sshhost
git commit -m "feat(sshhost): credentials, a client that talks only to a pinned host key, and an SSH server for tests

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: The admin routes

**Files:**
- Create: `internal/server/hosts.go`, `internal/server/hosts_test.go`
- Modify: `internal/server/server.go` (a field in `Deps`, the routes), `internal/app/app.go` (one line)

**Interfaces:**
- Consumes: everything from Tasks 1 to 4.
- Produces (used by the UI in Tasks 6 and 7), the JSON of these routes (all under the admin session and CSRF rules):
  - `GET /api/hosts` → `hostView[]`; `POST /api/hosts` → `201 hostView`; `GET /api/hosts/{id}` → `hostView` with `onboarding`; `PATCH /api/hosts/{id}` → `hostView` plus `pinCleared: boolean`; `DELETE /api/hosts/{id}` → `204`.
  - `PUT /api/hosts/{id}/credential` body `{kind: "key"|"password", key?, passphrase?, password?}` → `hostView`; `DELETE /api/hosts/{id}/credential` → `204`.
  - `POST /api/hosts/{id}/test` → `hostView` plus `presentedKey?: {type, fingerprint}`.
  - `POST /api/hosts/{id}/hostkey` body `{fingerprint}` → `hostView` (`409` when the host shows another key now); `DELETE /api/hosts/{id}/hostkey` → `204`.
  - `hostView` = `{id, name, address, port, account, notes, dockerPattern, systemdUnits[], enabled, status, statusDetail, checkedAt?, report?, pinnedFingerprint?, pinnedAt?, credential?: {kind, hint, publicKey?, updatedAt}, onboarding?: {authorizedKeys, sudoers, error?}, createdAt, updatedAt}`.

- [ ] **Step 1: Write the failing tests**

`internal/server/hosts_test.go`:

```go
package server_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/sshhost"
	"github.com/Jaydee94/remedy/internal/sshhost/sshtest"
	"github.com/Jaydee94/remedy/internal/store"
)

type hostEnv struct {
	ts     *httptest.Server
	store  *store.Store
	dbPath string
	ssh    *sshtest.Server
	client *http.Client
	seen   []string // every response body, for the walk for secrets
}

func nasCommands() map[string]sshtest.Reply {
	return map[string]sshtest.Reply{
		"id -un":     {Out: "remedy\n"},
		"id -Gn":     {Out: "remedy users\n"},
		"uname -sr":  {Out: "Linux 6.8.0\n"},
		"sudo -n -l": {Out: "User remedy may run the following commands on nas:\n    (root) NOPASSWD: /usr/bin/docker ps -a\n"},
	}
}

// newHostEnv starts a server over st (a new store if nil) that seals with key, next to an SSH test server, and logs in.
func newHostEnv(t *testing.T, st *store.Store, dbPath string, key secret.Key, ssh *sshtest.Server) *hostEnv {
	t.Helper()
	if st == nil {
		dbPath = filepath.Join(t.TempDir(), "test.db")
		var err error
		if st, err = store.Open(dbPath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
	}
	ts := httptest.NewServer(server.New(server.Deps{
		Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Key: key, Hosts: &sshhost.Service{},
	}))
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	e := &hostEnv{ts: ts, store: st, dbPath: dbPath, ssh: ssh, client: &http.Client{Jar: jar}}
	if code, _ := e.do(t, http.MethodPost, "/api/login", `{"password":"`+password+`"}`); code != http.StatusNoContent {
		t.Fatalf("login status = %d", code)
	}
	e.seen = nil
	return e
}

func (e *hostEnv) do(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	req.Header.Set("X-Remedy-CSRF", "1")
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	e.seen = append(e.seen, string(b))
	return resp.StatusCode, string(b)
}

func decodeBody[T any](t *testing.T, body string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return v
}

type hostJSON struct {
	ID                int64    `json:"id"`
	Name              string   `json:"name"`
	Address           string   `json:"address"`
	Port              int      `json:"port"`
	Account           string   `json:"account"`
	Notes             string   `json:"notes"`
	SystemdUnits      []string `json:"systemdUnits"`
	Status            string   `json:"status"`
	StatusDetail      string   `json:"statusDetail"`
	PinnedFingerprint string   `json:"pinnedFingerprint"`
	PinCleared        bool     `json:"pinCleared"`
	Report            *struct {
		Account   string   `json:"account"`
		Sudo      string   `json:"sudo"`
		Warnings  []string `json:"warnings"`
		SudoRules []string `json:"sudoRules"`
	} `json:"report"`
	Credential *struct {
		Kind      string `json:"kind"`
		Hint      string `json:"hint"`
		PublicKey string `json:"publicKey"`
	} `json:"credential"`
	Onboarding *struct {
		AuthorizedKeys string `json:"authorizedKeys"`
		Sudoers        string `json:"sudoers"`
		Error          string `json:"error"`
	} `json:"onboarding"`
	PresentedKey *struct {
		Type        string `json:"type"`
		Fingerprint string `json:"fingerprint"`
	} `json:"presentedKey"`
}

func (e *hostEnv) create(t *testing.T, name string) hostJSON {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"address":%q,"port":%d}`, name, e.ssh.Host, e.ssh.Port)
	code, b := e.do(t, http.MethodPost, "/api/hosts", body)
	if code != http.StatusCreated {
		t.Fatalf("create = %d %s", code, b)
	}
	return decodeBody[hostJSON](t, b)
}

func (e *hostEnv) setPassword(t *testing.T, id int64, pw string) {
	t.Helper()
	if code, b := e.do(t, http.MethodPut, fmt.Sprintf("/api/hosts/%d/credential", id), fmt.Sprintf(`{"kind":"password","password":%q}`, pw)); code != http.StatusOK {
		t.Fatalf("set password = %d %s", code, b)
	}
}

func (e *hostEnv) test(t *testing.T, id int64) hostJSON {
	t.Helper()
	code, b := e.do(t, http.MethodPost, fmt.Sprintf("/api/hosts/%d/test", id), "")
	if code != http.StatusOK {
		t.Fatalf("test = %d %s", code, b)
	}
	return decodeBody[hostJSON](t, b)
}

func (e *hostEnv) pin(t *testing.T, id int64, fp string) (int, hostJSON) {
	t.Helper()
	code, b := e.do(t, http.MethodPost, fmt.Sprintf("/api/hosts/%d/hostkey", id), fmt.Sprintf(`{"fingerprint":%q}`, fp))
	var h hostJSON
	if code == http.StatusOK {
		h = decodeBody[hostJSON](t, b)
	}
	return code, h
}

func TestHostRoutesNeedALogin(t *testing.T) {
	e := newHostEnv(t, nil, "", ghKey(t, 1), sshtest.Start(t, sshtest.Config{}))
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/hosts"}, {"POST", "/api/hosts"}, {"GET", "/api/hosts/1"}, {"PATCH", "/api/hosts/1"}, {"DELETE", "/api/hosts/1"},
		{"PUT", "/api/hosts/1/credential"}, {"DELETE", "/api/hosts/1/credential"}, {"POST", "/api/hosts/1/test"},
		{"POST", "/api/hosts/1/hostkey"}, {"DELETE", "/api/hosts/1/hostkey"},
	} {
		req, _ := http.NewRequest(c.method, e.ts.URL+c.path, strings.NewReader("{}"))
		req.Header.Set("X-Remedy-CSRF", "1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s without a session = %d, want 401", c.method, c.path, resp.StatusCode)
		}
	}
}

func TestHostLifecycle(t *testing.T) {
	e := newHostEnv(t, nil, "", ghKey(t, 1), sshtest.Start(t, sshtest.Config{}))

	h := e.create(t, "nas")
	if h.Port != e.ssh.Port || h.Account != "remedy" || h.Status != "new" || h.Credential != nil {
		t.Fatalf("created = %+v", h)
	}
	if code, _ := e.do(t, http.MethodPost, "/api/hosts", `{"name":"NAS","address":"10.0.0.1"}`); code != http.StatusConflict {
		t.Fatalf("duplicate name = %d, want 409", code)
	}
	if code, b := e.do(t, http.MethodPost, "/api/hosts", `{"name":"my nas","address":"10.0.0.1"}`); code != http.StatusBadRequest || !strings.Contains(b, "name") {
		t.Fatalf("bad name = %d %s", code, b)
	}
	if code, _ := e.do(t, http.MethodPost, "/api/hosts", `{"name":"x","address":"10.0.0.1","dockerPattern":"a b"}`); code != http.StatusBadRequest {
		t.Fatalf("bad docker pattern = %d", code)
	}

	code, b := e.do(t, http.MethodPatch, fmt.Sprintf("/api/hosts/%d", h.ID), `{"notes":"Paperless lives here","systemdUnits":["node_exporter"]}`)
	got := decodeBody[hostJSON](t, b)
	if code != http.StatusOK || got.Notes != "Paperless lives here" || len(got.SystemdUnits) != 1 || got.SystemdUnits[0] != "node_exporter.service" || got.PinCleared {
		t.Fatalf("patch = %d %+v", code, got)
	}

	code, b = e.do(t, http.MethodGet, "/api/hosts", "")
	if list := decodeBody[[]hostJSON](t, b); code != http.StatusOK || len(list) != 1 || list[0].Name != "nas" {
		t.Fatalf("list = %d %s", code, b)
	}

	if code, _ := e.do(t, http.MethodDelete, fmt.Sprintf("/api/hosts/%d", h.ID), ""); code != http.StatusNoContent {
		t.Fatalf("delete = %d", code)
	}
	if code, _ := e.do(t, http.MethodGet, fmt.Sprintf("/api/hosts/%d", h.ID), ""); code != http.StatusNotFound {
		t.Errorf("GET after delete = %d, want 404", code)
	}
	if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/api/hosts/%d/test", h.ID), ""); code != http.StatusNotFound {
		t.Errorf("test after delete = %d, want 404", code)
	}
	if code, _ := e.do(t, http.MethodGet, "/api/hosts/abc", ""); code != http.StatusNotFound {
		t.Fatalf("a non-numeric id = %d, want 404", code)
	}
}

func TestPutCredentialRejectsBadKeys(t *testing.T) {
	e := newHostEnv(t, nil, "", ghKey(t, 1), sshtest.Start(t, sshtest.Config{}))
	h := e.create(t, "nas")
	url := fmt.Sprintf("/api/hosts/%d/credential", h.ID)
	protected, _ := sshtest.KeyPEM(t, "right-passphrase")

	for name, c := range map[string]struct{ body, mention string }{
		"garbage":          {`{"kind":"key","key":"NOT-A-KEY-DISTINCTIVE"}`, "private key"},
		"no passphrase":    {fmt.Sprintf(`{"kind":"key","key":%q}`, protected), "passphrase"},
		"wrong passphrase": {fmt.Sprintf(`{"kind":"key","key":%q,"passphrase":"wrong-DISTINCTIVE"}`, protected), "passphrase"},
		"empty password":   {`{"kind":"password","password":""}`, "password"},
		"unknown kind":     {`{"kind":"token"}`, "kind"},
		"oversize body":    {`{"kind":"password","password":"` + strings.Repeat("x", 20<<10) + `"}`, "request"},
	} {
		code, b := e.do(t, http.MethodPut, url, c.body)
		if code != http.StatusBadRequest || !strings.Contains(b, c.mention) {
			t.Errorf("%s: %d %s", name, code, b)
		}
		for _, secretText := range []string{"NOT-A-KEY-DISTINCTIVE", "wrong-DISTINCTIVE", "OPENSSH PRIVATE KEY"} {
			if strings.Contains(b, secretText) {
				t.Errorf("%s: the answer repeats %q", name, secretText)
			}
		}
	}
}

// dump returns every value of the tables as text, except the sealed column.
func dump(t *testing.T, path string) string {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var out strings.Builder
	for _, q := range []string{
		`SELECT * FROM hosts`,
		`SELECT host_id, kind, hint, public_key, updated_at FROM host_credentials`,
		`SELECT * FROM activity`,
	} {
		rows, err := raw.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for _, v := range vals {
				fmt.Fprintf(&out, "%s|", v)
			}
			out.WriteString("\n")
		}
		rows.Close()
	}
	return out.String()
}

func TestCredentialsAreWriteOnly(t *testing.T) {
	e := newHostEnv(t, nil, "", ghKey(t, 1), sshtest.Start(t, sshtest.Config{Commands: nasCommands()}))
	keyHost, pwHost := e.create(t, "keyed"), e.create(t, "passworded")

	pem, _ := sshtest.KeyPEM(t, "pp-DISTINCTIVE")
	pemBody := strings.Split(strings.TrimSpace(pem), "\n")[1] // a line of the base64 body
	code, b := e.do(t, http.MethodPut, fmt.Sprintf("/api/hosts/%d/credential", keyHost.ID), fmt.Sprintf(`{"kind":"key","key":%q,"passphrase":"pp-DISTINCTIVE"}`, pem))
	if code != http.StatusOK {
		t.Fatalf("put key = %d %s", code, b)
	}
	view := decodeBody[hostJSON](t, b)
	if view.Credential == nil || view.Credential.Kind != "key" || !strings.HasPrefix(view.Credential.Hint, "SHA256:") || !strings.HasPrefix(view.Credential.PublicKey, "ssh-ed25519 ") {
		t.Fatalf("credential view = %+v", view.Credential)
	}
	e.setPassword(t, pwHost.ID, "hunter2-DISTINCTIVE")

	for _, p := range []string{"", fmt.Sprintf("/%d", keyHost.ID), fmt.Sprintf("/%d", pwHost.ID)} {
		e.do(t, http.MethodGet, "/api/hosts"+p, "")
	}
	e.do(t, http.MethodPost, fmt.Sprintf("/api/hosts/%d/test", pwHost.ID), "")
	e.do(t, http.MethodDelete, fmt.Sprintf("/api/hosts/%d/credential", pwHost.ID), "")

	haystack := strings.Join(e.seen, "\n") + "\n" + dump(t, e.dbPath)
	for _, secretText := range []string{"pp-DISTINCTIVE", "hunter2-DISTINCTIVE", pemBody, "OPENSSH PRIVATE KEY"} {
		if strings.Contains(haystack, secretText) {
			t.Errorf("%q appears outside the sealed column", secretText)
		}
	}
	if code, b := e.do(t, http.MethodGet, fmt.Sprintf("/api/hosts/%d", pwHost.ID), ""); code != http.StatusOK || strings.Contains(b, `"credential"`) {
		t.Errorf("after deleting the credential: %d %s", code, b)
	}
}

func TestATestWithoutAPinOnlyProbes(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: nasCommands()})
	e := newHostEnv(t, nil, "", ghKey(t, 1), srv)
	h := e.create(t, "nas")

	if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/api/hosts/%d/test", h.ID), ""); code != http.StatusConflict {
		t.Fatalf("test without a login = %d, want 409", code)
	}
	e.setPassword(t, h.ID, "pw")

	got := e.test(t, h.ID)
	if got.Status != "untrusted" || got.PresentedKey == nil || got.PresentedKey.Type != "ssh-ed25519" || !strings.HasPrefix(got.PresentedKey.Fingerprint, "SHA256:") {
		t.Fatalf("first test = %+v", got)
	}
	if srv.Attempts() != 0 {
		t.Fatalf("the first test offered a login %d times", srv.Attempts())
	}
}

func TestPinningNeedsTheFingerprintYouSaw(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: nasCommands()})
	e := newHostEnv(t, nil, "", ghKey(t, 1), srv)
	h := e.create(t, "nas")
	e.setPassword(t, h.ID, "pw")
	seen := e.test(t, h.ID).PresentedKey.Fingerprint

	if code, _ := e.pin(t, h.ID, "SHA256:notwhatyousaw"); code != http.StatusConflict {
		t.Fatalf("pin with another fingerprint = %d, want 409", code)
	}
	code, pinned := e.pin(t, h.ID, seen)
	if code != http.StatusOK || pinned.PinnedFingerprint != seen || pinned.Status != "new" {
		t.Fatalf("pin = %d %+v", code, pinned)
	}

	// The host's key changes between the first test and the pin: the call refuses.
	e2 := newHostEnv(t, nil, "", ghKey(t, 1), srv)
	h2 := e2.create(t, "nas2")
	e2.setPassword(t, h2.ID, "pw")
	shown := e2.test(t, h2.ID).PresentedKey.Fingerprint
	srv.SetHostSigner(sshtest.NewSigner(t))
	if code, _ := e2.pin(t, h2.ID, shown); code != http.StatusConflict {
		t.Fatalf("pin of a key that is gone = %d, want 409", code)
	}

	if code, _ := e.do(t, http.MethodDelete, fmt.Sprintf("/api/hosts/%d/hostkey", h.ID), ""); code != http.StatusNoContent {
		t.Fatalf("unpin = %d", code)
	}
	_, b := e.do(t, http.MethodGet, fmt.Sprintf("/api/hosts/%d", h.ID), "")
	if g := decodeBody[hostJSON](t, b); g.PinnedFingerprint != "" || g.Status != "untrusted" {
		t.Fatalf("after unpinning = %+v", g)
	}
}

func TestATestAfterPinningReportsTheAccountsRights(t *testing.T) {
	cmds := nasCommands()
	cmds["id -Gn"] = sshtest.Reply{Out: "remedy users docker\n"}
	cmds["sudo -n -l"] = sshtest.Reply{Out: "User remedy may run the following commands on nas:\n    (ALL : ALL) NOPASSWD: ALL\n"}
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: cmds})
	e := newHostEnv(t, nil, "", ghKey(t, 1), srv)
	h := e.create(t, "nas")
	e.setPassword(t, h.ID, "pw")
	e.pin(t, h.ID, e.test(t, h.ID).PresentedKey.Fingerprint)

	got := e.test(t, h.ID)
	if got.Status != "ok" || got.Report == nil || got.Report.Account != "remedy" || got.Report.Sudo != "unrestricted" || len(got.Report.Warnings) != 2 {
		t.Fatalf("test = %+v report %+v", got, got.Report)
	}
}

func TestAChangedHostKeyIsRefused(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: nasCommands()})
	e := newHostEnv(t, nil, "", ghKey(t, 1), srv)
	h := e.create(t, "nas")
	e.setPassword(t, h.ID, "pw")
	e.pin(t, h.ID, e.test(t, h.ID).PresentedKey.Fingerprint)

	srv.SetHostSigner(sshtest.NewSigner(t))
	got := e.test(t, h.ID)
	if got.Status != "key_changed" || !strings.Contains(got.StatusDetail, "another key") {
		t.Fatalf("test after the key changed = %+v", got)
	}
	if srv.Attempts() != 0 {
		t.Fatal("a login was offered to a host that showed another key")
	}
}

func TestWrongLoginAndUnreachableHostGiveFixedMessages(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: nasCommands()})
	e := newHostEnv(t, nil, "", ghKey(t, 1), srv)
	h := e.create(t, "nas")
	e.setPassword(t, h.ID, "wrong")
	e.pin(t, h.ID, e.test(t, h.ID).PresentedKey.Fingerprint)
	if got := e.test(t, h.ID); got.Status != "error" || got.StatusDetail != "The host rejected the login." {
		t.Fatalf("wrong password = %+v", got)
	}

	srv.Close()
	got := e.test(t, h.ID)
	if got.Status != "error" || got.StatusDetail != "The host refused the connection." || strings.Contains(got.StatusDetail, "127.0.0.1") {
		t.Fatalf("closed port = %+v", got)
	}
}

func TestAnAddressChangeClearsThePin(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: nasCommands()})
	e := newHostEnv(t, nil, "", ghKey(t, 1), srv)
	h := e.create(t, "nas")
	e.setPassword(t, h.ID, "pw")
	e.pin(t, h.ID, e.test(t, h.ID).PresentedKey.Fingerprint)

	code, b := e.do(t, http.MethodPatch, fmt.Sprintf("/api/hosts/%d", h.ID), `{"address":"localhost"}`)
	got := decodeBody[hostJSON](t, b)
	if code != http.StatusOK || !got.PinCleared || got.PinnedFingerprint != "" || got.Status != "untrusted" {
		t.Fatalf("patch of the address = %d %+v", code, got)
	}
}

func TestAnUndecryptableLoginIsReported(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: nasCommands()})
	a := newHostEnv(t, nil, "", ghKey(t, 1), srv)
	h := a.create(t, "nas")
	a.setPassword(t, h.ID, "pw")
	a.pin(t, h.ID, a.test(t, h.ID).PresentedKey.Fingerprint)

	// The same database, opened by a control plane with another master key.
	b := newHostEnv(t, a.store, a.dbPath, ghKey(t, 2), srv)
	got := b.test(t, h.ID)
	if got.Status != "undecryptable" || !strings.Contains(got.StatusDetail, "REMEDY_MASTER_KEY") {
		t.Fatalf("test with the wrong master key = %+v", got)
	}
}

func TestTheDetailCarriesTheOnboardingSnippet(t *testing.T) {
	e := newHostEnv(t, nil, "", ghKey(t, 1), sshtest.Start(t, sshtest.Config{}))
	h := e.create(t, "nas")
	e.do(t, http.MethodPatch, fmt.Sprintf("/api/hosts/%d", h.ID), `{"dockerPattern":"remedy-*","systemdUnits":["node_exporter"]}`)
	pem, _ := sshtest.KeyPEM(t, "")
	e.do(t, http.MethodPut, fmt.Sprintf("/api/hosts/%d/credential", h.ID), fmt.Sprintf(`{"kind":"key","key":%q}`, pem))

	_, b := e.do(t, http.MethodGet, fmt.Sprintf("/api/hosts/%d", h.ID), "")
	got := decodeBody[hostJSON](t, b)
	if got.Onboarding == nil || got.Onboarding.Error != "" ||
		!strings.Contains(got.Onboarding.AuthorizedKeys, got.Credential.PublicKey) ||
		!strings.Contains(got.Onboarding.Sudoers, "remedy ALL=(root) NOPASSWD: /usr/bin/docker ps -a") ||
		!strings.Contains(got.Onboarding.Sudoers, "/usr/bin/systemctl restart node_exporter.service") {
		t.Fatalf("onboarding = %+v", got.Onboarding)
	}
	// The list does not carry the snippet.
	_, list := e.do(t, http.MethodGet, "/api/hosts", "")
	if strings.Contains(list, "onboarding") {
		t.Fatal("the list carries the onboarding snippet")
	}
}
```

- [ ] **Step 2: Run to see it fail**

Run: `go test ./internal/server -run 'Host|Credential|Pin|Onboarding|Undecryptable|Changed|Wrong' -count=1`
Expected: FAIL to compile (`unknown field Hosts in struct literal of type server.Deps`).

- [ ] **Step 3: Add the seam and the routes**

In `internal/server/server.go`, add to the imports `"github.com/Jaydee94/remedy/internal/sshhost"`, and to `Deps`, after `RunnerStatus`:

```go
	// Hosts connects to the machines in the inventory. When it is nil the host routes are not registered.
	Hosts *sshhost.Service
```

In `routes`, after the `if d.NewGitHub != nil { ... }` block, add:

```go
	if d.Hosts != nil {
		public.HandleFunc("GET /api/hosts", s.session(s.listHosts))
		public.HandleFunc("POST /api/hosts", s.session(s.createHost))
		public.HandleFunc("GET /api/hosts/{id}", s.session(s.getHost))
		public.HandleFunc("PATCH /api/hosts/{id}", s.session(s.patchHost))
		public.HandleFunc("DELETE /api/hosts/{id}", s.session(s.deleteHost))
		public.HandleFunc("PUT /api/hosts/{id}/credential", s.session(s.putHostCredential))
		public.HandleFunc("DELETE /api/hosts/{id}/credential", s.session(s.deleteHostCredential))
		public.HandleFunc("POST /api/hosts/{id}/test", s.session(s.testHost))
		public.HandleFunc("POST /api/hosts/{id}/hostkey", s.session(s.pinHostKey))
		public.HandleFunc("DELETE /api/hosts/{id}/hostkey", s.session(s.unpinHostKey))
	}
```

In `internal/app/app.go`, add the import `"github.com/Jaydee94/remedy/internal/sshhost"` and the line `Hosts: &sshhost.Service{},` to the `server.Deps{...}` literal (after `RunnerStatus: server.NewRunnerStatus(),`).

- [ ] **Step 4: Implement `internal/server/hosts.go`**

```go
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/sshhost"
	"github.com/Jaydee94/remedy/internal/store"
)

const (
	maxHostBody       = 16 << 10
	undecryptableHost = "The stored login cannot be decrypted. Check REMEDY_MASTER_KEY or enter the login again."
)

type credentialView struct {
	Kind      string    `json:"kind"`
	Hint      string    `json:"hint"`
	PublicKey string    `json:"publicKey,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type onboardingView struct {
	AuthorizedKeys string `json:"authorizedKeys"`
	Sudoers        string `json:"sudoers"`
	Error          string `json:"error,omitempty"`
}

// hostView has no field that could carry a secret: a credential shows its kind, a hint and a public key.
type hostView struct {
	ID                int64           `json:"id"`
	Name              string          `json:"name"`
	Address           string          `json:"address"`
	Port              int             `json:"port"`
	Account           string          `json:"account"`
	Notes             string          `json:"notes"`
	DockerPattern     string          `json:"dockerPattern"`
	SystemdUnits      []string        `json:"systemdUnits"`
	Enabled           bool            `json:"enabled"`
	Status            string          `json:"status"`
	StatusDetail      string          `json:"statusDetail"`
	CheckedAt         *time.Time      `json:"checkedAt,omitempty"`
	Report            *sshhost.Report `json:"report,omitempty"`
	PinnedFingerprint string          `json:"pinnedFingerprint,omitempty"`
	PinnedAt          *time.Time      `json:"pinnedAt,omitempty"`
	Credential        *credentialView `json:"credential,omitempty"`
	Onboarding        *onboardingView `json:"onboarding,omitempty"`
	CreatedAt         time.Time       `json:"createdAt"`
	UpdatedAt         time.Time       `json:"updatedAt"`
}

type presentedKey struct {
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
}

type testView struct {
	hostView
	PresentedKey *presentedKey `json:"presentedKey,omitempty"`
}

type patchView struct {
	hostView
	PinCleared bool `json:"pinCleared"`
}

// hostViewOf builds the view of a host. The detail view also carries the report and the onboarding snippet. (The name viewOf is
// taken by the GitHub connection's view.)
func hostViewOf(e store.HostEntry, detail bool) hostView {
	h := e.Host
	v := hostView{
		ID: h.ID, Name: h.Name, Address: h.Address, Port: h.Port, Account: h.Account, Notes: h.Notes, DockerPattern: h.DockerPattern,
		SystemdUnits: h.SystemdUnits, Enabled: h.Enabled, Status: string(h.Status), StatusDetail: h.StatusDetail,
		CheckedAt: h.CheckedAt, PinnedAt: h.PinnedAt, CreatedAt: h.CreatedAt, UpdatedAt: h.UpdatedAt,
	}
	if v.SystemdUnits == nil {
		v.SystemdUnits = []string{}
	}
	if h.PinnedKey != "" {
		if fp, err := sshhost.FingerprintOfLine(h.PinnedKey); err == nil {
			v.PinnedFingerprint = fp
		}
	}
	if c := e.Credential; c != nil {
		v.Credential = &credentialView{Kind: c.Kind, Hint: c.Hint, PublicKey: c.PublicKey, UpdatedAt: c.UpdatedAt}
	}
	if !detail {
		return v
	}
	if h.Report != "" {
		var rep sshhost.Report
		if json.Unmarshal([]byte(h.Report), &rep) == nil {
			v.Report = &rep
		}
	}
	in := sshhost.SnippetInput{Account: h.Account, DockerPattern: h.DockerPattern, SystemdUnits: h.SystemdUnits}
	if e.Credential != nil {
		in.AuthorizedKey = e.Credential.PublicKey
	}
	if out, err := sshhost.Snippet(in); err != nil {
		v.Onboarding = &onboardingView{Error: "I cannot make a snippet from these fields."}
	} else {
		v.Onboarding = &onboardingView{AuthorizedKeys: out.AuthorizedKeys, Sudoers: out.Sudoers}
	}
	return v
}

func hostID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil
}

// hostBody is the body of a create or a patch. A field that is absent keeps its value.
type hostBody struct {
	Name          *string   `json:"name"`
	Address       *string   `json:"address"`
	Port          *int      `json:"port"`
	Account       *string   `json:"account"`
	Notes         *string   `json:"notes"`
	DockerPattern *string   `json:"dockerPattern"`
	SystemdUnits  *[]string `json:"systemdUnits"`
	Enabled       *bool     `json:"enabled"`
}

func (b hostBody) apply(f store.HostFields) store.HostFields {
	if b.Name != nil {
		f.Name = strings.TrimSpace(*b.Name)
	}
	if b.Address != nil {
		f.Address = strings.TrimSpace(*b.Address)
	}
	if b.Port != nil {
		f.Port = *b.Port
	}
	if b.Account != nil {
		f.Account = strings.TrimSpace(*b.Account)
	}
	if b.Notes != nil {
		f.Notes = *b.Notes
	}
	if b.DockerPattern != nil {
		f.DockerPattern = strings.TrimSpace(*b.DockerPattern)
	}
	if b.SystemdUnits != nil {
		f.SystemdUnits = *b.SystemdUnits
	}
	if b.Enabled != nil {
		f.Enabled = *b.Enabled
	}
	return f
}

// checked validates the fields and returns them with the units normalised.
func checked(f store.HostFields) (store.HostFields, error) {
	v := sshhost.Fields{Name: f.Name, Address: f.Address, Port: f.Port, Account: f.Account, Notes: f.Notes, DockerPattern: f.DockerPattern, SystemdUnits: f.SystemdUnits}
	if err := v.Validate(); err != nil {
		return f, err
	}
	f.SystemdUnits = v.SystemdUnits
	return f, nil
}

func (s *srv) badFields(w http.ResponseWriter, err error) {
	var fe *sshhost.FieldError
	if errors.As(err, &fe) {
		writeErr(w, http.StatusBadRequest, fe.Msg)
		return
	}
	writeErr(w, http.StatusBadRequest, "invalid request body")
}

func readHostBody(w http.ResponseWriter, r *http.Request) (hostBody, bool) {
	var b hostBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxHostBody)).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return b, false
	}
	return b, true
}

func (s *srv) listHosts(w http.ResponseWriter, r *http.Request) {
	entries, err := s.d.Store.ListHosts(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list hosts")
		return
	}
	views := make([]hostView, 0, len(entries))
	for _, e := range entries {
		views = append(views, hostViewOf(e, false))
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *srv) createHost(w http.ResponseWriter, r *http.Request) {
	body, ok := readHostBody(w, r)
	if !ok {
		return
	}
	fields, err := checked(body.apply(store.HostFields{Port: 22, Account: "remedy", Enabled: true}))
	if err != nil {
		s.badFields(w, err)
		return
	}
	h, err := s.d.Store.CreateHost(r.Context(), fields)
	if errors.Is(err, store.ErrExists) {
		writeErr(w, http.StatusConflict, "a host with this name already exists")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not create the host")
		return
	}
	writeJSON(w, http.StatusCreated, hostViewOf(store.HostEntry{Host: h}, false))
}

// load answers the request itself when the host does not exist.
func (s *srv) loadHost(w http.ResponseWriter, r *http.Request) (store.HostEntry, bool) {
	id, ok := hostID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "host not found")
		return store.HostEntry{}, false
	}
	e, err := s.d.Store.GetHost(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "host not found")
		return store.HostEntry{}, false
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the host")
		return store.HostEntry{}, false
	}
	return e, true
}

func (s *srv) getHost(w http.ResponseWriter, r *http.Request) {
	if e, ok := s.loadHost(w, r); ok {
		writeJSON(w, http.StatusOK, hostViewOf(e, true))
	}
}

func (s *srv) patchHost(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadHost(w, r)
	if !ok {
		return
	}
	body, ok := readHostBody(w, r)
	if !ok {
		return
	}
	fields, err := checked(body.apply(e.Host.HostFields))
	if err != nil {
		s.badFields(w, err)
		return
	}
	_, cleared, err := s.d.Store.UpdateHost(r.Context(), e.Host.ID, fields)
	if errors.Is(err, store.ErrExists) {
		writeErr(w, http.StatusConflict, "a host with this name already exists")
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not update the host")
		return
	}
	updated, err := s.d.Store.GetHost(r.Context(), e.Host.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the host")
		return
	}
	writeJSON(w, http.StatusOK, patchView{hostView: hostViewOf(updated, true), PinCleared: cleared})
}

func (s *srv) deleteHost(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadHost(w, r)
	if !ok {
		return
	}
	err := s.d.Store.DeleteHost(r.Context(), e.Host.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not remove the host")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) putHostCredential(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadHost(w, r)
	if !ok {
		return
	}
	var req struct {
		Kind       string `json:"kind"`
		Key        string `json:"key"`
		Passphrase string `json:"passphrase"`
		Password   string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxHostBody)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var (
		cred sshhost.Credential
		hint string
		pub  string
		err  error
	)
	switch req.Kind {
	case sshhost.KindKey:
		var info sshhost.KeyInfo
		cred, info, err = sshhost.NewKeyCredential(req.Key, req.Passphrase)
		hint, pub = info.Fingerprint, info.PublicLine
	case sshhost.KindPassword:
		cred, err = sshhost.NewPasswordCredential(req.Password)
		hint = "set"
	default:
		writeErr(w, http.StatusBadRequest, "the kind must be key or password")
		return
	}
	switch {
	case errors.Is(err, sshhost.ErrKeyFormat):
		writeErr(w, http.StatusBadRequest, "That is not a private key I can read. Paste the whole file, including the BEGIN and END lines.")
		return
	case errors.Is(err, sshhost.ErrPassphraseNeeded):
		writeErr(w, http.StatusBadRequest, "The key is protected by a passphrase. Enter it too.")
		return
	case errors.Is(err, sshhost.ErrPassphraseWrong):
		writeErr(w, http.StatusBadRequest, "The passphrase does not open the key.")
		return
	case errors.Is(err, sshhost.ErrPasswordInvalid):
		writeErr(w, http.StatusBadRequest, "The password must have 1 to 256 characters.")
		return
	case err != nil:
		writeErr(w, http.StatusBadRequest, "invalid login")
		return
	}

	blob, err := cred.Marshal()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not store the login")
		return
	}
	sealed, err := s.d.Key.Seal(blob, store.HostCredentialAAD(e.Host.ID))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not store the login")
		return
	}
	err = s.d.Store.SaveHostCredential(r.Context(), store.HostCredential{HostID: e.Host.ID, Kind: cred.Kind, Ciphertext: sealed, Hint: hint, PublicKey: pub})
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not store the login")
		return
	}
	updated, err := s.d.Store.GetHost(r.Context(), e.Host.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the host")
		return
	}
	writeJSON(w, http.StatusOK, hostViewOf(updated, true))
}

func (s *srv) deleteHostCredential(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadHost(w, r)
	if !ok {
		return
	}
	err := s.d.Store.DeleteHostCredential(r.Context(), e.Host.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "this host has no login")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not remove the login")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func targetOf(h store.Host) sshhost.Target {
	return sshhost.Target{Address: h.Address, Port: h.Port, Account: h.Account}
}

// hostFailure turns a failed connection into the status and the sentence the page shows. The texts are fixed: nothing the
// library or the host said is passed on.
func hostFailure(err error) (store.HostStatus, string) {
	switch {
	case errors.Is(err, sshhost.ErrHostKeyChanged):
		return store.HostKeyChanged, "The host shows another key than the pinned one. Find out why before you pin it again."
	case errors.Is(err, sshhost.ErrAuth):
		return store.HostError, "The host rejected the login."
	case errors.Is(err, sshhost.ErrTimeout):
		return store.HostError, "The host did not answer in time."
	case errors.Is(err, sshhost.ErrRefused):
		return store.HostError, "The host refused the connection."
	case errors.Is(err, sshhost.ErrBadCredential):
		return store.HostError, "The stored login cannot be used. Enter it again."
	}
	return store.HostError, "I could not reach the host."
}

func (s *srv) openHostCredential(e store.HostEntry) (sshhost.Credential, error) {
	raw, err := s.d.Key.Open(e.Credential.Ciphertext, store.HostCredentialAAD(e.Host.ID))
	if err != nil {
		return sshhost.Credential{}, err
	}
	return sshhost.ParseCredential(raw)
}

func (s *srv) testHost(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadHost(w, r)
	if !ok {
		return
	}
	if e.Credential == nil {
		writeErr(w, http.StatusConflict, "Enter a login first.")
		return
	}
	ctx, h := r.Context(), e.Host
	var presented *presentedKey

	if h.PinnedKey == "" {
		// Not pinned: only the key exchange. The login never leaves the control plane.
		key, err := s.d.Hosts.Probe(ctx, h.ID, targetOf(h))
		if err != nil {
			status, detail := hostFailure(err)
			_ = s.d.Store.SaveHostTest(ctx, h.ID, status, detail, "", time.Now())
		} else {
			presented = &presentedKey{Type: key.Type, Fingerprint: key.Fingerprint}
			_ = s.d.Store.SaveHostTest(ctx, h.ID, store.HostUntrusted, "", "", time.Now())
		}
	} else {
		cred, err := s.openHostCredential(e)
		switch {
		case errors.Is(err, secret.ErrOpen):
			_ = s.d.Store.SaveHostTest(ctx, h.ID, store.HostUndecryptable, undecryptableHost, "", time.Now())
		case err != nil:
			status, detail := hostFailure(err)
			_ = s.d.Store.SaveHostTest(ctx, h.ID, status, detail, "", time.Now())
		default:
			rep, err := s.d.Hosts.Check(ctx, h.ID, targetOf(h), cred, h.PinnedKey)
			if err != nil {
				status, detail := hostFailure(err)
				_ = s.d.Store.SaveHostTest(ctx, h.ID, status, detail, "", time.Now())
			} else if b, merr := json.Marshal(rep); merr == nil {
				_ = s.d.Store.SaveHostTest(ctx, h.ID, store.HostOK, "", string(b), time.Now())
			}
		}
	}

	updated, err := s.d.Store.GetHost(ctx, h.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the host")
		return
	}
	writeJSON(w, http.StatusOK, testView{hostView: hostViewOf(updated, true), PresentedKey: presented})
}

// pinHostKey pins the key the host shows now, but only if it is the one whose fingerprint the maintainer names. The server keeps
// no "pending" key: the call is the maintainer's statement of what they saw, checked against what the host presents now.
func (s *srv) pinHostKey(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadHost(w, r)
	if !ok {
		return
	}
	var req struct {
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Fingerprint == "" {
		writeErr(w, http.StatusBadRequest, "the fingerprint is required")
		return
	}
	key, err := s.d.Hosts.Probe(r.Context(), e.Host.ID, targetOf(e.Host))
	if err != nil {
		_, detail := hostFailure(err)
		writeErr(w, http.StatusBadGateway, detail)
		return
	}
	if key.Fingerprint != req.Fingerprint {
		writeErr(w, http.StatusConflict, "The host shows another key now than the one you checked. Test the connection again.")
		return
	}
	if err := s.d.Store.PinHostKey(r.Context(), e.Host.ID, key.Line, time.Now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "host not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "could not pin the key")
		return
	}
	updated, err := s.d.Store.GetHost(r.Context(), e.Host.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the host")
		return
	}
	writeJSON(w, http.StatusOK, hostViewOf(updated, true))
}

func (s *srv) unpinHostKey(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadHost(w, r)
	if !ok {
		return
	}
	err := s.d.Store.UnpinHostKey(r.Context(), e.Host.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not unpin the key")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `gofmt -l internal; go vet ./... && go test ./internal/server ./internal/app ./internal/sshhost/... ./internal/store -count=1 -race`
Expected: no gofmt output, vet clean, PASS.

If `TestAChangedHostKeyIsRefused` fails, check that the detail contains "another key": that is the text of `hostFailure` for `ErrHostKeyChanged`.

- [ ] **Step 6: Commit**

```sh
git add internal/server internal/app
git commit -m "feat(server): host routes: create, login, host key pinning and connection test

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 6: The UI's types, navigation and pure logic

**Files:**
- Modify: `web/src/api.ts`, `web/src/shell.ts`, `web/src/shell.test.ts`, `web/src/components/shell/navIcons.ts`
- Create: `web/src/hosts.ts`, `web/src/hosts.test.ts`

**Interfaces:**
- Consumes: the JSON of Task 5; `timeAgo(iso: string, now: number): string` from `web/src/incidents.ts`.
- Produces (used by Task 7):
  - In `api.ts`: types `HostStatus`, `HostReport`, `HostCredentialView`, `HostOnboarding`, `Host`, `PresentedKey`, `HostTest` (= `Host & { presentedKey?: PresentedKey }`), `HostPatched` (= `Host & { pinCleared: boolean }`), `HostBody`, `CredentialBody`; and `api.listHosts`, `createHost`, `getHost`, `patchHost`, `deleteHost`, `putHostCredential`, `deleteHostCredential`, `testHost`, `pinHostKey`, `unpinHostKey`.
  - In `hosts.ts`: `statusView(status)`, `hostLine(h, now)`, `statusSentence(h)`, `sudoText(level)`, `credentialText(c)`, `parseUnits(text)`, `validHostName(s)`, `validAddress(s)`, `validDockerPattern(s)`, `FINGERPRINT_HELP`.
  - In `shell.ts`: `Section` includes `'hosts'`; `navItems` has six entries.

- [ ] **Step 1: Write the failing tests**

Add to `web/src/shell.test.ts` (replace the test `has five items, in the order of the navigation` and extend the others):

```ts
  it('maps the hosts pages to Hosts', () => {
    assert.equal(sectionOf('/hosts'), 'hosts')
    assert.equal(sectionOf('/hosts/3'), 'hosts')
    assert.equal(sectionOf('/hostsX'), null)
  })

  it('has six items, in the order of the navigation', () => {
    assert.deepEqual(navItems.map((n) => n.section), ['today', 'conversations', 'needs', 'ask', 'hosts', 'setup'])
    assert.deepEqual(navItems.map((n) => n.short), ['Today', 'Chats', 'Needs you', 'Ask', 'Hosts', 'Setup'])
  })
```

and in the `defaultHeader` block:

```ts
  it('gives a host a back target to the list', () => {
    assert.deepEqual(defaultHeader('/hosts'), { title: 'Hosts' })
    assert.deepEqual(defaultHeader('/hosts/3'), { title: 'Host', back: '/hosts' })
  })
```

Delete the old five-item test in the same edit, so that `npm test` does not fail on it.

`web/src/hosts.test.ts`:

```ts
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { Host } from './api.ts'
import { timeAgo } from './incidents.ts'
import { credentialText, hostLine, parseUnits, statusSentence, statusView, sudoText, validAddress, validDockerPattern, validHostName } from './hosts.ts'

function host(over: Partial<Host> = {}): Host {
  return {
    id: 1, name: 'nas', address: '192.168.178.118', port: 22, account: 'remedy', notes: '', dockerPattern: '', systemdUnits: [], enabled: true,
    status: 'new', statusDetail: '', createdAt: '2026-10-09T10:00:00Z', updatedAt: '2026-10-09T10:00:00Z', ...over,
  }
}

describe('statusView', () => {
  it('names every status and colours the failures', () => {
    assert.equal(statusView('ok').label, 'Connected')
    assert.equal(statusView('new').label, 'Not tested')
    assert.equal(statusView('untrusted').label, 'Key not pinned')
    assert.equal(statusView('key_changed').label, 'Key changed')
    assert.equal(statusView('undecryptable').label, 'Cannot decrypt')
    for (const s of ['error', 'key_changed', 'undecryptable'] as const) assert.match(statusView(s).text, /destructive/)
    assert.match(statusView('ok').text, /success/)
  })
})

describe('hostLine', () => {
  const now = Date.parse('2026-10-09T12:00:00Z')

  it('names the address, shows a port only when it is not 22, and says when it was tested', () => {
    assert.equal(hostLine(host(), now), '192.168.178.118 · never tested')
    assert.equal(
      hostLine(host({ port: 2222, checkedAt: '2026-10-09T11:55:00Z' }), now),
      `192.168.178.118:2222 · tested ${timeAgo('2026-10-09T11:55:00Z', now)}`,
    )
  })

  it('puts an IPv6 address in brackets when it has a port', () => {
    assert.equal(hostLine(host({ address: 'fe80::1', port: 2222 }), now), '[fe80::1]:2222 · never tested')
    assert.equal(hostLine(host({ address: 'fe80::1' }), now), 'fe80::1 · never tested')
  })
})

describe('statusSentence', () => {
  it('says what to do next, in order', () => {
    assert.match(statusSentence(host()), /don't have a login/)
    assert.match(statusSentence(host({ credential: { kind: 'password', hint: 'set', updatedAt: 'x' } })), /test the connection/i)
    assert.match(statusSentence(host({ credential: { kind: 'password', hint: 'set', updatedAt: 'x' }, status: 'untrusted' })), /not pinned/)
    assert.match(statusSentence(host({ credential: { kind: 'password', hint: 'set', updatedAt: 'x' }, pinnedFingerprint: 'SHA256:a' })), /haven't tested/)
    assert.match(statusSentence(host({ status: 'ok', pinnedFingerprint: 'SHA256:a' })), /can log in/)
  })

  it('uses the server\'s detail for a failure', () => {
    assert.equal(statusSentence(host({ status: 'error', statusDetail: 'The host rejected the login.' })), 'The host rejected the login.')
    assert.equal(statusSentence(host({ status: 'error', statusDetail: '' })), "I couldn't log in.")
  })
})

describe('sudoText and credentialText', () => {
  it('says what each sudo level means for Remedy', () => {
    assert.match(sudoText('none'), /changes cannot run/)
    assert.match(sudoText('limited'), /rules below/)
    assert.match(sudoText('unrestricted'), /everything/)
  })

  it('names a login without showing it', () => {
    assert.equal(credentialText({ kind: 'key', hint: 'SHA256:abc', updatedAt: 'x' }), 'Key SHA256:abc')
    assert.equal(credentialText({ kind: 'password', hint: 'set', updatedAt: 'x' }), 'A password')
  })
})

describe('input checks', () => {
  it('parses units from lines and commas, without blanks or duplicates', () => {
    assert.deepEqual(parseUnits('node_exporter, backup.timer\n\n node_exporter \n'), ['node_exporter', 'backup.timer'])
    assert.deepEqual(parseUnits(''), [])
  })

  it('checks a name, an address and a container pattern like the server does', () => {
    assert.equal(validHostName('nas-1'), true)
    assert.equal(validHostName('my nas'), false)
    assert.equal(validHostName(''), false)
    assert.equal(validAddress('192.168.178.118'), true)
    assert.equal(validAddress('nas.homeserver'), true)
    assert.equal(validAddress('fe80::1'), true)
    assert.equal(validAddress('a b'), false)
    assert.equal(validAddress('-oProxyCommand=x'), false)
    assert.equal(validDockerPattern(''), true)
    assert.equal(validDockerPattern('remedy-*'), true)
    assert.equal(validDockerPattern('a*b'), false)
    assert.equal(validDockerPattern('a b'), false)
  })
})
```

- [ ] **Step 2: Run to see them fail**

Run: `cd web && npm test 2>&1 | tail -20`
Expected: FAIL (`Cannot find module './hosts.ts'`, and the shell tests for six items).

- [ ] **Step 3: Implement the navigation**

In `web/src/shell.ts`: change the `Section` type to

```ts
export type Section = 'today' | 'conversations' | 'needs' | 'ask' | 'hosts' | 'setup'
```

add the item before Setup in `navItems`:

```ts
  { section: 'hosts', to: '/hosts', label: 'Hosts', short: 'Hosts' },
```

and in `defaultHeader`, after the `/runs/` line:

```ts
  if (pathname.startsWith('/hosts/')) return { title: 'Host', back: '/hosts' }
```

In `web/src/components/shell/navIcons.ts` import `Server` from `lucide-react` (add it to the import list) and add `hosts: Server,` to the record.

- [ ] **Step 4: Add the types and calls to `web/src/api.ts`**

After the `Repo` interface add:

```ts
export type HostStatus = 'new' | 'untrusted' | 'ok' | 'error' | 'key_changed' | 'undecryptable'

/** What the last connection test learned about the account. Everything in it came from the host: show it as text. */
export interface HostReport {
  at: string
  account: string
  groups: string[]
  system: string
  sudo: 'none' | 'limited' | 'unrestricted'
  sudoRules: string[]
  warnings: string[]
}

/** A host's login, without the login: its kind, a hint (a key's fingerprint, or "set" for a password) and, for a key, the public key. */
export interface HostCredentialView {
  kind: 'key' | 'password'
  hint: string
  publicKey?: string
  updatedAt: string
}

export interface HostOnboarding {
  authorizedKeys: string
  sudoers: string
  /** Set when the snippet could not be made from the host's fields. */
  error?: string
}

export interface Host {
  id: number
  name: string
  address: string
  port: number
  account: string
  notes: string
  dockerPattern: string
  systemdUnits: string[]
  enabled: boolean
  status: HostStatus
  statusDetail: string
  checkedAt?: string
  /** Only `GET /api/hosts/{id}` and the answers of its actions have the report and the onboarding snippet. */
  report?: HostReport
  pinnedFingerprint?: string
  pinnedAt?: string
  credential?: HostCredentialView
  onboarding?: HostOnboarding
  createdAt: string
  updatedAt: string
}

export interface PresentedKey {
  type: string
  fingerprint: string
}

export type HostTest = Host & { presentedKey?: PresentedKey }
export type HostPatched = Host & { pinCleared: boolean }

export interface HostBody {
  name?: string
  address?: string
  port?: number
  account?: string
  notes?: string
  dockerPattern?: string
  systemdUnits?: string[]
  enabled?: boolean
}

export type CredentialBody = { kind: 'key'; key: string; passphrase: string } | { kind: 'password'; password: string }
```

and in the `api` object after `deleteRepo`:

```ts
  listHosts: () => request<Host[]>('GET', '/api/hosts'),
  createHost: (body: HostBody) => request<Host>('POST', '/api/hosts', body),
  getHost: (id: number) => request<Host>('GET', `/api/hosts/${id}`),
  patchHost: (id: number, body: HostBody) => request<HostPatched>('PATCH', `/api/hosts/${id}`, body),
  deleteHost: (id: number) => request<void>('DELETE', `/api/hosts/${id}`),
  putHostCredential: (id: number, body: CredentialBody) => request<Host>('PUT', `/api/hosts/${id}/credential`, body),
  deleteHostCredential: (id: number) => request<void>('DELETE', `/api/hosts/${id}/credential`),
  testHost: (id: number) => request<HostTest>('POST', `/api/hosts/${id}/test`),
  pinHostKey: (id: number, fingerprint: string) => request<Host>('POST', `/api/hosts/${id}/hostkey`, { fingerprint }),
  unpinHostKey: (id: number) => request<void>('DELETE', `/api/hosts/${id}/hostkey`),
```

- [ ] **Step 5: Implement `web/src/hosts.ts`**

```ts
import type { Host, HostCredentialView, HostReport, HostStatus } from './api.ts'
import { timeAgo } from './incidents.ts'

/** The chip of a host's status, in tokens. */
export function statusView(status: HostStatus): { label: string; dot: string; soft: string; text: string } {
  switch (status) {
    case 'ok':
      return { label: 'Connected', dot: 'bg-success', soft: 'bg-soft-resolved', text: 'text-success' }
    case 'error':
      return { label: 'Error', dot: 'bg-destructive', soft: 'bg-soft-open', text: 'text-destructive' }
    case 'key_changed':
      return { label: 'Key changed', dot: 'bg-destructive', soft: 'bg-soft-open', text: 'text-destructive' }
    case 'undecryptable':
      return { label: 'Cannot decrypt', dot: 'bg-destructive', soft: 'bg-soft-open', text: 'text-destructive' }
    case 'untrusted':
      return { label: 'Key not pinned', dot: 'bg-muted-foreground', soft: 'bg-muted', text: 'text-muted-foreground' }
    case 'new':
      return { label: 'Not tested', dot: 'bg-muted-foreground', soft: 'bg-muted', text: 'text-muted-foreground' }
  }
}

/** "192.168.178.118 · tested 5 minutes ago". The port shows only when it is not 22; an IPv6 address gets brackets with a port. */
export function hostLine(h: Host, now: number): string {
  let where = h.address
  if (h.port !== 22) where = h.address.includes(':') ? `[${h.address}]:${h.port}` : `${h.address}:${h.port}`
  const tested = h.checkedAt && !Number.isNaN(Date.parse(h.checkedAt)) ? `tested ${timeAgo(h.checkedAt, now)}` : 'never tested'
  return `${where} · ${tested}`
}

/** What Remedy says about the host at the top of its page: the next thing to do, or what went wrong. */
export function statusSentence(h: Host): string {
  if (h.status === 'error' || h.status === 'key_changed' || h.status === 'undecryptable') return h.statusDetail || "I couldn't log in."
  if (!h.credential) return "I don't have a login for this host yet."
  if (h.status === 'ok') return 'I can log in.'
  if (h.status === 'untrusted') return 'I can reach the host, but its key is not pinned yet. Compare the fingerprint, then pin it.'
  if (h.pinnedFingerprint) return "The key is pinned. I haven't tested the login yet."
  return "I have a login, but I don't trust the host's key yet. Test the connection and I'll show you the key."
}

/** What the sudo level of the report means for Remedy. */
export function sudoText(level: HostReport['sudo']): string {
  switch (level) {
    case 'none':
      return 'sudo does not work for this account without a password: I can read, but changes cannot run.'
    case 'limited':
      return 'sudo is limited to the rules below.'
    case 'unrestricted':
      return 'sudo allows everything without a password.'
  }
}

/** A login in words, without the login. */
export function credentialText(c: HostCredentialView): string {
  return c.kind === 'key' ? `Key ${c.hint}` : 'A password'
}

/** Units typed one per line or separated by commas, without blanks and duplicates. The server normalises and checks them. */
export function parseUnits(text: string): string[] {
  const out: string[] = []
  for (const part of text.split(/[\s,]+/)) {
    if (part !== '' && !out.includes(part)) out.push(part)
  }
  return out
}

export const validHostName = (s: string) => /^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$/.test(s.trim())

/** A host name or an IP address. The API still has the last word. */
export function validAddress(s: string): boolean {
  const a = s.trim()
  return /^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$/.test(a) || (/^[0-9A-Fa-f:.]+$/.test(a) && a.includes(':'))
}

export const validDockerPattern = (s: string) => s.trim() === '' || (/^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}\*?$/.test(s.trim()) && s.trim().toUpperCase() !== 'ALL')

/** How to compare the fingerprint the page shows with the host's own. */
export const FINGERPRINT_HELP =
  'On the host, run: ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub (use the file of the key type shown here). The fingerprints must be identical.'
```

- [ ] **Step 6: Run the tests, lint and type check**

Run: `cd web && npm test 2>&1 | tail -15 && npm run lint && npx tsc -b`
Expected: all tests PASS, lint clean, `tsc` clean. If `tsc` reports that a `switch` over `Section` in a component is not exhaustive, add the `hosts` case there (the icons record is the only one expected).

- [ ] **Step 7: Commit**

```sh
git add web/src
git commit -m "feat(web): hosts in the API client, the navigation and the pure logic

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 7: The pages

**Files:**
- Create: `web/src/HostsPage.tsx`, `web/src/HostPage.tsx`, `web/src/components/hosts/run.ts`, `web/src/components/hosts/Field.tsx`, `web/src/components/hosts/AddHostForm.tsx`, `web/src/components/hosts/HostForm.tsx`, `web/src/components/hosts/LoginSection.tsx`, `web/src/components/hosts/TrustSection.tsx`, `web/src/components/hosts/RightsSection.tsx`, `web/src/components/hosts/SnippetSection.tsx`
- Modify: `web/src/App.tsx` (two routes and a small route component)

**Interfaces:**
- Consumes: Task 6.
- Produces: the routes `/hosts` and `/hosts/:id`.
- The pages hold no logic worth a unit test (the words are in `hosts.ts`); they are checked in a real browser in Task 8.

- [ ] **Step 1: The shared pieces**

`web/src/components/hosts/run.ts`:

```ts
/** Runs one request of the host page: one at a time, a 404 turns the whole page into "this host is gone", any other failure shows its message. */
export type Run = <T>(action: () => Promise<T>) => Promise<T | undefined>
```

`web/src/components/hosts/Field.tsx`:

```tsx
import type { ReactNode } from 'react'

/** A label above a control, with an optional line of help under it. */
export default function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className="text-[13px] font-semibold text-muted-foreground">{label}</span>
      {children}
      {hint && <span className="text-[13px] text-muted-foreground">{hint}</span>}
    </label>
  )
}
```

- [ ] **Step 2: The list page and the add form**

`web/src/components/hosts/AddHostForm.tsx`:

```tsx
import { useState } from 'react'
import type { FormEvent } from 'react'
import { useNavigate } from 'react-router'
import { api, ApiError } from '@/api.ts'
import { validAddress, validHostName } from '@/hosts.ts'
import Field from '@/components/hosts/Field'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

/** The two things a host needs to exist. Everything else is on its own page. */
export default function AddHostForm() {
  const navigate = useNavigate()
  const [name, setName] = useState('')
  const [address, setAddress] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const ready = validHostName(name) && validAddress(address)

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (busy || !ready) return
    setBusy(true)
    setError('')
    try {
      const host = await api.createHost({ name: name.trim(), address: address.trim() })
      navigate(`/hosts/${host.id}`)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Request failed')
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
      <p className="font-serif text-[17px] leading-relaxed text-pretty">Tell me where a machine is. I'll ask for the login on the next page.</p>
      <Field label="Name" hint="Letters, digits, dot, dash and underscore.">
        <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="nas" className="h-11 text-base md:text-sm" autoComplete="off" />
      </Field>
      <Field label="Address" hint="A host name or an IP address.">
        <Input value={address} onChange={(e) => setAddress(e.target.value)} placeholder="192.168.178.118" className="h-11 text-base md:text-sm" autoComplete="off" />
      </Field>
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <div>
        <Button type="submit" disabled={!ready} aria-disabled={busy} className="h-11 aria-disabled:pointer-events-none aria-disabled:opacity-50">
          Add host
        </Button>
      </div>
    </form>
  )
}
```

`web/src/HostsPage.tsx`:

```tsx
import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError } from './api.ts'
import type { Host } from './api.ts'
import { hostLine, statusView } from './hosts.ts'
import { useClock } from './useClock.ts'
import AddHostForm from '@/components/hosts/AddHostForm'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

/** The machines I may log in to. */
export default function HostsPage() {
  const [hosts, setHosts] = useState<Host[] | null>(null)
  const [error, setError] = useState('')
  const now = useClock()

  useEffect(() => {
    api
      .listHosts()
      .then(setHosts)
      .catch((e: unknown) => setError(e instanceof ApiError ? e.message : 'Could not load the hosts'))
  }, [])

  return (
    <div className="mx-auto flex max-w-190 flex-col gap-8 px-4 py-5 md:px-10 md:py-12">
      <div className="flex flex-col gap-1">
        <h1 className="font-serif text-[clamp(26px,3vw,34px)] font-normal">Hosts</h1>
        <span className="text-muted-foreground">The machines I may log in to, and how.</span>
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {hosts === null && !error && <Skeleton className="h-32" />}

      {hosts !== null && hosts.length > 0 && (
        <ul className="flex flex-col gap-3" aria-label="Hosts">
          {hosts.map((h) => {
            const view = statusView(h.status)
            return (
              <li key={h.id}>
                <Link
                  to={`/hosts/${h.id}`}
                  className="flex flex-col gap-1.5 rounded-3xl border border-border bg-card p-5 outline-none transition-colors hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring/50"
                >
                  <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
                    <strong className="text-[17px] font-semibold break-all">{h.name}</strong>
                    <span className={cn('flex items-center gap-2 rounded-2xl px-3 py-1 text-[13px] font-semibold', view.soft, view.text)}>
                      <span aria-hidden className={cn('size-2 rounded-full', view.dot)} />
                      {view.label}
                    </span>
                  </span>
                  <span className="text-[13px] text-muted-foreground break-all">{hostLine(h, now)}</span>
                </Link>
              </li>
            )
          })}
        </ul>
      )}

      {hosts !== null && hosts.length === 0 && (
        <p className="font-serif text-[17px] leading-relaxed text-pretty">
          I don't know any machine yet. Add the ones outside the cluster that I should be able to look at, such as your NAS.
        </p>
      )}

      {hosts !== null && (
        <section className="flex flex-col gap-3.5">
          <h2 className="text-xs font-semibold text-muted-foreground">Add a host</h2>
          <AddHostForm />
        </section>
      )}
    </div>
  )
}
```

- [ ] **Step 3: The sections of the detail page**

`web/src/components/hosts/HostForm.tsx`:

```tsx
import { useState } from 'react'
import type { FormEvent } from 'react'
import { api } from '@/api.ts'
import type { Host, HostPatched } from '@/api.ts'
import { parseUnits, validAddress, validDockerPattern, validHostName } from '@/hosts.ts'
import Field from '@/components/hosts/Field'
import type { Run } from '@/components/hosts/run'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'

interface Props {
  host: Host
  busy: boolean
  run: Run
  onSaved: (h: Host, pinCleared: boolean) => void
}

/** The fields of a host that the maintainer edits. Nothing here is a secret, so the inputs are ordinary controlled ones. */
export default function HostForm({ host, busy, run, onSaved }: Props) {
  const [f, setF] = useState({
    name: host.name,
    address: host.address,
    port: String(host.port),
    account: host.account,
    notes: host.notes,
    dockerPattern: host.dockerPattern,
    units: host.systemdUnits.join('\n'),
    enabled: host.enabled,
  })
  const set = (k: keyof typeof f) => (v: string | boolean) => setF((prev) => ({ ...prev, [k]: v }))
  const ready = validHostName(f.name) && validAddress(f.address) && validDockerPattern(f.dockerPattern) && Number(f.port) >= 1 && Number(f.port) <= 65535

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (busy || !ready) return
    const res: HostPatched | undefined = await run(() =>
      api.patchHost(host.id, {
        name: f.name.trim(),
        address: f.address.trim(),
        port: Number(f.port),
        account: f.account.trim(),
        notes: f.notes,
        dockerPattern: f.dockerPattern.trim(),
        systemdUnits: parseUnits(f.units),
        enabled: f.enabled,
      }),
    )
    if (res) {
      const { pinCleared, ...saved } = res
      onSaved(saved, pinCleared)
    }
  }

  const input = 'h-11 text-base md:text-sm'
  return (
    <form onSubmit={submit} className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
      <Field label="Name">
        <Input value={f.name} onChange={(e) => set('name')(e.target.value)} className={input} autoComplete="off" />
      </Field>
      <div className="grid grid-cols-[1fr_6.5rem] gap-3">
        <Field label="Address">
          <Input value={f.address} onChange={(e) => set('address')(e.target.value)} className={input} autoComplete="off" />
        </Field>
        <Field label="Port">
          <Input value={f.port} onChange={(e) => set('port')(e.target.value)} inputMode="numeric" className={input} autoComplete="off" />
        </Field>
      </div>
      <Field label="Account" hint="The user I log in as. A dedicated account such as remedy is safer than yours.">
        <Input value={f.account} onChange={(e) => set('account')(e.target.value)} className={input} autoComplete="off" />
      </Field>
      <Field label="Notes" hint="For you. I show them as text.">
        <textarea
          value={f.notes}
          onChange={(e) => set('notes')(e.target.value)}
          rows={3}
          className="min-h-20 rounded-xl border border-input bg-transparent px-3 py-2 text-base outline-none focus-visible:ring-3 focus-visible:ring-ring/50 md:text-sm"
        />
      </Field>
      <Field label="Containers I may start and stop" hint="A name with one star at the end, for example remedy-*. Leave it empty for none. It only shapes the snippet below.">
        <Input value={f.dockerPattern} onChange={(e) => set('dockerPattern')(e.target.value)} placeholder="remedy-*" className={input} autoComplete="off" />
      </Field>
      <Field label="systemd units I may start and stop" hint="One per line. It only shapes the snippet below.">
        <textarea
          value={f.units}
          onChange={(e) => set('units')(e.target.value)}
          rows={2}
          className="min-h-16 rounded-xl border border-input bg-transparent px-3 py-2 font-mono text-base outline-none focus-visible:ring-3 focus-visible:ring-ring/50 md:text-sm"
        />
      </Field>
      <label className="flex items-center gap-3">
        <Switch checked={f.enabled} onCheckedChange={(v: boolean) => set('enabled')(v)} />
        <span className="text-[15px]">Remedy may use this host</span>
      </label>
      <div>
        <Button type="submit" disabled={!ready} aria-disabled={busy} className="h-11 aria-disabled:pointer-events-none aria-disabled:opacity-50">
          Save
        </Button>
      </div>
    </form>
  )
}
```

`web/src/components/hosts/LoginSection.tsx`:

```tsx
import { useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { api } from '@/api.ts'
import type { Host } from '@/api.ts'
import { credentialText } from '@/hosts.ts'
import ConfirmButton from '@/components/ConfirmButton'
import Field from '@/components/hosts/Field'
import type { Run } from '@/components/hosts/run'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

interface Props {
  host: Host
  busy: boolean
  run: Run
  onHost: (h: Host) => void
}

/**
 * How I log in. The secrets are write-only: the fields are uncontrolled, so React never mirrors a secret into the DOM, and they
 * are emptied as soon as the request is sent, also when it fails (the page cannot keep what it must not keep). Nothing goes in a
 * URL or in storage.
 */
export default function LoginSection({ host, busy, run, onHost }: Props) {
  const [kind, setKind] = useState<'key' | 'password'>(host.credential?.kind ?? 'key')
  const [editing, setEditing] = useState(!host.credential)
  const keyRef = useRef<HTMLTextAreaElement>(null)
  const passphraseRef = useRef<HTMLInputElement>(null)
  const passwordRef = useRef<HTMLInputElement>(null)

  function clear() {
    for (const r of [keyRef, passphraseRef, passwordRef]) if (r.current) r.current.value = ''
  }

  async function save(e: FormEvent) {
    e.preventDefault()
    if (busy) return
    const body =
      kind === 'key'
        ? ({ kind, key: keyRef.current?.value ?? '', passphrase: passphraseRef.current?.value ?? '' } as const)
        : ({ kind, password: passwordRef.current?.value ?? '' } as const)
    clear()
    const res = await run(() => api.putHostCredential(host.id, body))
    if (res) {
      onHost(res)
      setEditing(false)
    }
  }

  async function remove() {
    const done = await run(async () => {
      await api.deleteHostCredential(host.id)
      return api.getHost(host.id)
    })
    if (done) {
      onHost(done)
      setEditing(true)
    }
  }

  const tab = (k: 'key' | 'password', label: string) => (
    <button
      type="button"
      onClick={() => {
        clear()
        setKind(k)
      }}
      aria-pressed={kind === k}
      className={cn(
        'h-10 rounded-full px-4 text-[14px] outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
        kind === k ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground',
      )}
    >
      {label}
    </button>
  )

  return (
    <div className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
      {host.credential && (
        <p className="font-serif text-[17px] leading-relaxed text-pretty">
          I log in with <strong className="font-semibold break-all">{credentialText(host.credential)}</strong>. It's stored encrypted and never shown again.
        </p>
      )}

      {editing && (
        <form onSubmit={save} className="flex flex-col gap-3" autoComplete="off">
          <div className="flex gap-2" role="group" aria-label="Kind of login">
            {tab('key', 'Private key')}
            {tab('password', 'Password')}
          </div>
          {kind === 'key' ? (
            <>
              <Field label="Private key" hint="Paste the whole file, including the BEGIN and END lines.">
                <textarea
                  ref={keyRef}
                  rows={6}
                  spellCheck={false}
                  autoComplete="off"
                  data-1p-ignore
                  data-lpignore="true"
                  data-bwignore
                  className="rounded-xl border border-input bg-transparent px-3 py-2 font-mono text-[13px] outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
                />
              </Field>
              <Field label="Passphrase" hint="Only if the key is protected by one.">
                <Input ref={passphraseRef} type="password" autoComplete="new-password" data-1p-ignore data-lpignore="true" data-bwignore className="h-11 text-base md:text-sm" />
              </Field>
            </>
          ) : (
            <Field label="Password">
              <Input ref={passwordRef} type="password" autoComplete="new-password" data-1p-ignore data-lpignore="true" data-bwignore className="h-11 text-base md:text-sm" />
            </Field>
          )}
          <div className="flex gap-2">
            <Button type="submit" aria-disabled={busy} className="h-11 aria-disabled:pointer-events-none aria-disabled:opacity-50">
              {host.credential ? 'Replace login' : 'Save login'}
            </Button>
            {host.credential && (
              <Button
                type="button"
                variant="ghost"
                className="h-11"
                onClick={() => {
                  clear()
                  setEditing(false)
                }}
              >
                Cancel
              </Button>
            )}
          </div>
        </form>
      )}

      {host.credential && !editing && (
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" className="h-10" onClick={() => setEditing(true)}>
            Replace login
          </Button>
          <ConfirmButton label="Remove login" confirmLabel="Confirm: remove the login" className="h-10" disabled={busy} onConfirm={() => void remove()} />
        </div>
      )}
    </div>
  )
}
```

`web/src/components/hosts/TrustSection.tsx`:

```tsx
import { api } from '@/api.ts'
import type { Host, PresentedKey } from '@/api.ts'
import { FINGERPRINT_HELP } from '@/hosts.ts'
import ConfirmButton from '@/components/ConfirmButton'
import type { Run } from '@/components/hosts/run'
import { Button } from '@/components/ui/button'

interface Props {
  host: Host
  presented: PresentedKey | null
  busy: boolean
  run: Run
  onHost: (h: Host) => void
  onPresented: (k: PresentedKey | null) => void
}

/** Whether I trust the key the host shows. I pin it only after the maintainer names the fingerprint they compared. */
export default function TrustSection({ host, presented, busy, run, onHost, onPresented }: Props) {
  async function pin() {
    if (busy || !presented) return
    const res = await run(() => api.pinHostKey(host.id, presented.fingerprint))
    if (res) {
      onPresented(null)
      onHost(res)
    }
  }

  async function unpin() {
    const done = await run(async () => {
      await api.unpinHostKey(host.id)
      return api.getHost(host.id)
    })
    if (done) onHost(done)
  }

  return (
    <div className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
      {host.pinnedFingerprint ? (
        <>
          <p className="font-serif text-[17px] leading-relaxed text-pretty">I only log in to a host that shows this key:</p>
          <code className="rounded-xl bg-muted px-3 py-2 text-[13px] break-all">{host.pinnedFingerprint}</code>
          <div>
            <ConfirmButton label="Forget this key" confirmLabel="Confirm: forget the key" className="h-10" disabled={busy} onConfirm={() => void unpin()} />
          </div>
        </>
      ) : presented ? (
        <>
          <p className="font-serif text-[17px] leading-relaxed text-pretty">
            The host shows a key of type <strong className="font-semibold">{presented.type}</strong>. Compare its fingerprint with the host's own before you trust it:
          </p>
          <code className="rounded-xl bg-muted px-3 py-2 text-[13px] break-all">{presented.fingerprint}</code>
          <p className="text-[13px] text-muted-foreground">{FINGERPRINT_HELP}</p>
          <div>
            <Button className="h-11 aria-disabled:pointer-events-none aria-disabled:opacity-50" aria-disabled={busy} onClick={() => void pin()}>
              I checked this fingerprint
            </Button>
          </div>
        </>
      ) : (
        <p className="font-serif text-[17px] leading-relaxed text-pretty">
          I haven't pinned a key for this host. Test the connection, and I'll show you the key it presents. I never send a login to a host whose key I don't know.
        </p>
      )}
    </div>
  )
}
```

`web/src/components/hosts/RightsSection.tsx`:

```tsx
import { api } from '@/api.ts'
import type { Host, PresentedKey } from '@/api.ts'
import { statusView, sudoText } from '@/hosts.ts'
import type { Run } from '@/components/hosts/run'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

interface Props {
  host: Host
  busy: boolean
  run: Run
  onTested: (h: Host, presented: PresentedKey | null) => void
}

/** The test, and what it learned about the account: who it is, its groups, what sudo lets it do. Everything in the report is text from the host. */
export default function RightsSection({ host, busy, run, onTested }: Props) {
  const view = statusView(host.status)
  const report = host.report

  async function test() {
    if (busy) return
    const res = await run(() => api.testHost(host.id))
    if (res) {
      const { presentedKey, ...tested } = res
      onTested(tested, presentedKey ?? null)
    }
  }

  return (
    <div className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
      <div className="flex flex-wrap items-center gap-3">
        <span className={cn('flex items-center gap-2 rounded-2xl px-3 py-1.5 text-[13px] font-semibold', view.soft, view.text)}>
          <span aria-hidden className={cn('size-2 rounded-full', view.dot)} />
          {view.label}
        </span>
        <Button variant="outline" size="sm" className="h-10 aria-disabled:pointer-events-none aria-disabled:opacity-50" aria-disabled={busy} onClick={() => void test()}>
          {busy ? 'Testing…' : 'Test connection'}
        </Button>
      </div>

      {report && (
        <>
          <p className="font-serif text-[17px] leading-relaxed text-pretty break-words">
            I'm <strong className="font-semibold">{report.account}</strong> on {report.system || 'this host'}, in the groups {report.groups.join(', ') || 'none'}.{' '}
            {sudoText(report.sudo)}
          </p>
          {report.warnings.map((w) => (
            <Alert key={w} variant="destructive" role="note">
              <AlertDescription>{w}</AlertDescription>
            </Alert>
          ))}
          {report.sudoRules.length > 0 && (
            <pre className="overflow-x-auto rounded-xl bg-muted px-3 py-2 text-[13px] whitespace-pre-wrap break-words">{report.sudoRules.join('\n')}</pre>
          )}
        </>
      )}
    </div>
  )
}
```

`web/src/components/hosts/SnippetSection.tsx`:

```tsx
import { useState } from 'react'
import type { Host } from '@/api.ts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'

function Block({ title, text }: { title: string; text: string }) {
  const [copied, setCopied] = useState(false)
  async function copy() {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 2000)
    } catch {
      setCopied(false) // no clipboard here: the text is selectable
    }
  }
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-3">
        <h3 className="text-[13px] font-semibold text-muted-foreground">{title}</h3>
        <Button variant="outline" size="sm" className="h-9" onClick={() => void copy()}>
          {copied ? 'Copied' : 'Copy'}
        </Button>
      </div>
      <pre className="overflow-x-auto rounded-xl bg-muted px-3 py-2 text-[13px] whitespace-pre-wrap break-all">{text}</pre>
    </div>
  )
}

/** What to run on the host. I only show it; I never run it. */
export default function SnippetSection({ host }: { host: Host }) {
  const o = host.onboarding
  if (!o) return null
  return (
    <div className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
      <p className="font-serif text-[17px] leading-relaxed text-pretty">
        To set the account up, read this and run it on the host as root. I don't run it for you. It lets me do exactly what these lines say, and nothing else.
      </p>
      {o.error ? (
        <Alert variant="destructive">
          <AlertDescription>{o.error}</AlertDescription>
        </Alert>
      ) : (
        <>
          {o.authorizedKeys && <Block title="authorized_keys" text={o.authorizedKeys} />}
          <Block title="sudoers" text={o.sudoers} />
        </>
      )}
    </div>
  )
}
```

- [ ] **Step 4: The detail page**

`web/src/HostPage.tsx`:

```tsx
import { useCallback, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { Link, useNavigate } from 'react-router'
import { api, ApiError } from './api.ts'
import type { Host, PresentedKey } from './api.ts'
import { statusSentence } from './hosts.ts'
import ConfirmButton from '@/components/ConfirmButton'
import HostForm from '@/components/hosts/HostForm'
import LoginSection from '@/components/hosts/LoginSection'
import RightsSection from '@/components/hosts/RightsSection'
import type { Run } from '@/components/hosts/run'
import SnippetSection from '@/components/hosts/SnippetSection'
import TrustSection from '@/components/hosts/TrustSection'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'

function Part({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-3.5">
      <h2 className="text-xs font-semibold text-muted-foreground">{title}</h2>
      {children}
    </section>
  )
}

/** One host: the form, how I log in, whether I trust its key, what the account can do, and what to run on the host. */
export default function HostPage({ id }: { id: number }) {
  const navigate = useNavigate()
  const [host, setHost] = useState<Host | null>(null)
  const [presented, setPresented] = useState<PresentedKey | null>(null)
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')
  const [gone, setGone] = useState(false)
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)

  useEffect(() => {
    api
      .getHost(id)
      .then(setHost)
      .catch((e: unknown) => {
        if (e instanceof ApiError && e.status === 404) setGone(true)
        else setError(e instanceof ApiError ? e.message : 'Could not load the host')
      })
  }, [id])

  // One request at a time. A 404 means the host was removed elsewhere (another tab): say so once, instead of an error per button.
  const run: Run = useCallback(async <T,>(action: () => Promise<T>) => {
    if (busyRef.current) return undefined
    busyRef.current = true
    setBusy(true)
    setError('')
    try {
      return await action()
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) setGone(true)
      else setError(e instanceof ApiError ? e.message : 'Request failed')
      return undefined
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }, [])

  async function remove() {
    if ((await run(() => api.deleteHost(id).then(() => true))) === true) navigate('/hosts')
  }

  if (gone) {
    return (
      <div className="mx-auto flex max-w-190 flex-col gap-4 px-4 py-5 md:px-10 md:py-12">
        <h1 className="font-serif text-[clamp(26px,3vw,34px)] font-normal">This host is gone</h1>
        <p className="text-muted-foreground">It was removed, maybe in another window.</p>
        <Link to="/hosts" className="text-primary underline">
          Back to the hosts
        </Link>
      </div>
    )
  }

  return (
    <div className="mx-auto flex max-w-190 flex-col gap-8 px-4 py-5 md:px-10 md:py-12">
      {host ? (
        <>
          <div className="flex flex-col gap-1">
            <h1 className="font-serif text-[clamp(26px,3vw,34px)] font-normal break-all">{host.name}</h1>
            <span className="text-muted-foreground">{statusSentence(host)}</span>
          </div>

          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          {notice && (
            <Alert role="status">
              <AlertDescription>{notice}</AlertDescription>
            </Alert>
          )}

          <Part title="The host">
            <HostForm
              key={`${host.updatedAt}`}
              host={host}
              busy={busy}
              run={run}
              onSaved={(h, pinCleared) => {
                setHost(h)
                setNotice(pinCleared ? 'The address changed, so I forgot the pinned key. Test the connection and pin it again.' : '')
                if (pinCleared) setPresented(null)
              }}
            />
          </Part>
          <Part title="How I log in">
            <LoginSection key={host.credential?.updatedAt ?? 'none'} host={host} busy={busy} run={run} onHost={setHost} />
          </Part>
          <Part title="Trust">
            <TrustSection host={host} presented={presented} busy={busy} run={run} onHost={setHost} onPresented={setPresented} />
          </Part>
          <Part title="What the account can do">
            {host.credential ? (
              <RightsSection
                host={host}
                busy={busy}
                run={run}
                onTested={(h, key) => {
                  setHost(h)
                  setPresented(key)
                  setNotice('')
                }}
              />
            ) : (
              <p className="text-muted-foreground">Give me a login first.</p>
            )}
          </Part>
          <Part title="Set the account up">
            <SnippetSection host={host} />
          </Part>

          <div className="border-t border-border pt-4">
            <ConfirmButton label="Delete host" confirmLabel="Confirm: also removes its login" className="h-10" disabled={busy} onConfirm={() => void remove()} />
          </div>
        </>
      ) : (
        !error && <Skeleton className="h-64" />
      )}
      {!host && error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
    </div>
  )
}
```

- [ ] **Step 5: Routes**

In `web/src/App.tsx` add the imports for `HostsPage` and `HostPage`, a route component next to `IncidentRoute`:

```tsx
function HostRoute() {
  const { id } = useParams()
  const n = Number(id)
  return Number.isInteger(n) && n > 0 ? <HostPage key={n} id={n} /> : <NotFoundPage />
}
```

and, before the `settings` route:

```tsx
        <Route path="hosts" element={<HostsPage />} />
        <Route path="hosts/:id" element={<HostRoute />} />
```

- [ ] **Step 6: Build, lint and test**

Run: `cd web && npm run lint && npm test 2>&1 | tail -5 && npm run build 2>&1 | tail -15`
Expected: lint clean, tests PASS, build succeeds. Fix type errors where the shadcn components differ from what is assumed here (`Switch`'s props, `Input`'s `ref` forwarding): read `web/src/components/ui/switch.tsx` and `input.tsx` and adapt the call, not the component.

- [ ] **Step 7: Commit**

```sh
git add web/src
git commit -m "feat(web): the Hosts pages: list, detail, login, trust, test and onboarding snippet

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Look at it in a browser, document it, check everything, open the pull request

**Files:**
- Create: `internal/sshhost/browser_test.go`
- Modify: `CLAUDE.md`

- [ ] **Step 1: A test SSH server you can start by hand**

`internal/sshhost/browser_test.go`:

```go
package sshhost_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/Jaydee94/remedy/internal/sshhost/sshtest"
)

// TestServeForBrowser serves a test SSH server until the test is killed, so that a throwaway Remedy server can be pointed at it
// while the UI is checked in a browser. It does nothing unless REMEDY_BROWSER_SSH is set.
//
//	REMEDY_BROWSER_SSH=1 go test ./internal/sshhost -run TestServeForBrowser -v -timeout 0
func TestServeForBrowser(t *testing.T) {
	if os.Getenv("REMEDY_BROWSER_SSH") == "" {
		t.Skip("set REMEDY_BROWSER_SSH=1 to serve a test SSH server until the test is killed")
	}
	srv := sshtest.Start(t, sshtest.Config{Password: "browser-pw", Commands: okCommands()})
	fmt.Printf("SSH test server on %s:%d, password browser-pw\n", srv.Host, srv.Port)
	select {}
}
```

- [ ] **Step 2: Start the throwaway servers**

Write `browser-server.sh` into the session's scratchpad directory with the Write tool (a worktree session refuses variables in the command position), with literal paths:

```sh
#!/bin/sh
# A throwaway Remedy control plane for a look at the UI. Run from the worktree root.
set -e
export REMEDY_ADMIN_PASSWORD='browser-check-password'
export REMEDY_RUNNER_TOKEN='browser-check-runner-token-0123456789'
export REMEDY_MASTER_KEY='MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE='
export REMEDY_ADDR=':8099'
export REMEDY_DB='<scratchpad>/hosts-browser.db'
exec go run -tags webui ./cmd/remedy-server
```

(`<scratchpad>` is the literal scratchpad path of the session. The master key above is 32 bytes of ASCII digits in Base64: it is only for this throwaway database.)

Run, each as its own command and in the background:

```sh
make web-build
REMEDY_BROWSER_SSH=1 go test ./internal/sshhost -run TestServeForBrowser -v -timeout 0
sh <scratchpad>/browser-server.sh
```

Read the port from the first command's output (`SSH test server on 127.0.0.1:<port>`).

- [ ] **Step 3: The check, with Playwright (real clicks, `getByLabel(/password/i)` to sign in)**

Check each of these and note what you see; fix what is wrong before going on:

1. Sign in at `http://localhost:8099`. The navigation shows **Hosts** between Ask and Setup.
2. `/hosts` with no host: the empty sentence and the "Add a host" form. Add `nas`, address `127.0.0.1`. The detail page opens at `/hosts/1`; the sentence says "I don't have a login for this host yet." Set the port to the test server's port and save.
3. Enter the login: password `browser-pw`. The field is empty after saving and the page says "A password". Reload the page: no secret anywhere (view the page's HTML for `browser-pw`: it must not be there).
4. "Test connection": the page shows the key type and the fingerprint and "I checked this fingerprint". Click it. The pinned fingerprint is shown.
5. "Test connection" again: status **Connected**, the report names the account `remedy`, the groups, and a limited sudo.
6. The snippet: set the container pattern `remedy-*` and a unit `node_exporter`, save; the `sudoers` block shows the exact lines; copy works (or the text is selectable).
7. Change the address to `localhost` and save: the notice says the pinned key was forgotten, and the status goes back to "Key not pinned".
8. In a second tab, delete the host with `fetch('/api/hosts/1', {method: 'DELETE', headers: {'X-Remedy-CSRF': '1'}})` in the page's console; in the first tab click "Test connection": the page becomes "This host is gone".
9. **The tab bar on a phone:** resize to 360 × 740 and run in the page: `(() => { const n = document.querySelector('nav[aria-label="Main"]'); return [n.scrollWidth <= n.clientWidth, ...[...n.children].map(c => Math.round(c.getBoundingClientRect().width))] })()`. Expected: `true` and six widths of at least 44. If it does not fit, stop and ask the maintainer: the spec's fallback is one tab "More".
10. Keyboard: tab through the detail page; every control is reachable, and the buttons keep focus while a request runs.

Stop both servers (`pkill -f -- 'sshhost.test'` and `pkill -f -- 'remedy-server'` are loose patterns; stop them by the process IDs the background tasks reported instead). Delete stray `*.png` and `.playwright-mcp/` in the checkout that launched the browser. Delete the scratchpad database.

- [ ] **Step 4: Document it in `CLAUDE.md`**

In the bullet "Hosts and skills" under "Current state", change "specified, nothing built" to say that **cycle 1 is implemented** (tables `hosts` and `host_credentials`, `internal/sshhost`, the Hosts pages) and that cycles 2 to 6 are not. Add to the "Trust boundaries" list:

```
- **Host credentials** (`internal/sshhost`, tables `hosts` and `host_credentials`) belong to the control plane alone: a private key, its passphrase or a password is sealed like the GitHub token (`store.HostCredentialAAD`, bound to the host id; its value never changes), shown only as a hint, and never in a log, an error, an API answer or an activity entry. `sshhost.Dial` talks only to a host that presents exactly the pinned key; `Probe` stops at the key exchange and sends no login; pinning is a separate call that names the fingerprint the maintainer compared. Only the four constant test commands run in cycle 1 (`host_exec` is cycle 2). The onboarding snippet validates every value it writes into a `sudoers` file (`sshhost.Snippet`). `sshtest` is an SSH server for tests; `REMEDY_BROWSER_SSH=1 go test ./internal/sshhost -run TestServeForBrowser -v -timeout 0` serves one for a look at the UI.
```

- [ ] **Step 5: Everything**

Run: `make check`
Expected: fmt, vet, test, chart check, shell tests, web lint, web test and web build all pass. (`make chart-check` needs helm; if it is missing, run the other targets one by one and say so in the pull request.)

Run: `go test ./... -race -cpu 1`
Expected: PASS. This is the CI-relevant ordering check named in `CLAUDE.md`.

- [ ] **Step 6: Commit, push, open the pull request**

```sh
git add -A internal web CLAUDE.md
git commit -m "feat: hosts, cycle 1 (inventory, sealed logins, host key pinning, connection test)

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
git push -u origin HEAD
```

Then open the pull request. Write the body to a file with the Write tool and pass it with `gh pr create --body-file <that file>`; the sentences marked with a result are filled in from what you saw:

```
## Summary
- Tables `hosts` and `host_credentials` (migration 010): a host, its sealed login and its pinned host key.
- `internal/sshhost`: validation of the fields, the onboarding snippet, a client that talks only to a pinned host key, the report of an account's rights and a test SSH server. No new Go module.
- Admin routes under `/api/hosts` and a navigation item Hosts with a list and a detail page.
- Nothing uses a host yet: `host_exec` and the gatekeeper tools are cycle 2.

## Checks
- `make check`: <the result of the run>
- `go test ./... -race -cpu 1`: <the result of the run>
- Looked at in a browser (steps 1 to 10 of the plan): <what you saw, including the tab bar at 360 px>

A `feat` title makes the merge publish a release.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
```

Do not merge: the maintainer reviews first.

---

## Self-review of this plan

- **Spec coverage.** Spec section 2 (decisions): fields, credential kinds, sealing, hint, pinning, test, warnings, snippet, delete, activity: Tasks 1, 2, 4, 5. Section 3 (data model): Task 1. Section 4 (components, client rules): Tasks 4 and 5. Section 5 (snippet and validation, hostile test): Task 2. Section 6 (test, report, pin call): Tasks 3, 4, 5. Section 7 (API, secret walk): Task 5. Section 8 (UI): Tasks 6 and 7. Section 9 (testing, in the order given; the browser look): Tasks 1 to 8. Section 10 (order): the order of the tasks. Section 11 (success criteria 1 to 8): 1 and 7 in Task 8, 2 in `TestATestWithoutAPinOnlyProbes`, 3 in `TestAChangedHostKeyIsRefused`, 4 in `TestCredentialsAreWriteOnly`, 5 in `TestSnippetRefusesHostileInput`, 6 in `TestBuildReportWarnings`, 8 in Task 8.
- **Not in this plan, on purpose:** logging of host routes at `debug` (no handler logs anything, so there is nothing to leak); the `enabled = false` behaviour beyond storing it (an open point in the spec: the test still works).
- **Names.** `hostViewOf`, `decodeBody` (the server tests already have a `decode` with another signature), `HostCredentialAAD`, `Service.Probe(ctx, hostID, Target)` and `Service.Check(ctx, hostID, Target, Credential, pinLine)` are used with the same signatures in Tasks 4 and 5.

