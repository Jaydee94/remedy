# Phase 1a: GitHub Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The maintainer can open Remedy, enter a GitHub token (stored encrypted, never shown again), add repositories, and see them listed, in a redesigned UI with a router and shadcn/ui. A spike settles whether the CLI returns structured output in `stream-json` mode.

**Architecture:** New Go packages `secret` (AES-256-GCM sealing and a redacting value type) and `github` (read-only API client), new tables `github_connections` and `repos`, and a connection and repos API in the control plane that depends on a small `GitHub` interface so tests use a fake. The web app moves from hash routing to React Router with a sidebar layout built from shadcn/ui, and gets a Settings page.

**Tech Stack:** Go 1.27 stdlib only (no new Go dependencies), SQLite, React 19, `react-router` 8, shadcn/ui (`radix` base, `nova` preset) on Tailwind 4.

**Spec:** [`docs/specs/2026-10-02-phase-1-detect-and-diagnose-design.md`](../specs/2026-10-02-phase-1-detect-and-diagnose-design.md), sections 7, 8 (connection and repos part), 9 (settings and layout) and 11 steps 0 to 4.

**Scope note:** The spec covers four independent plans. This is the first. The poller and incidents (spec steps 5 and 6), the responder (step 7) and the timeline plus the real run against GitHub (steps 8 and 9) each get their own plan after this one lands, because they depend on what the spike and the real GitHub responses show.

## Global Constraints

- Everything committed is English: docs, code, identifiers, comments, UI copy, commit messages.
- No new Go dependencies. New web dependencies are only `react-router` and what the shadcn CLI installs (`radix-ui`, `lucide-react`, `class-variance-authority`, `cn`, `tw-animate-css`, `@fontsource-variable/geist`, `shadcn`).
- The GitHub token is **write-only**: it never appears in an API response, a log line, an error message or the database in plaintext. Its text form is always `***`.
- The GitHub client is **read-only**: every exported method name starts with `Get` or `List`, and it only sends `GET`.
- `web/tsconfig*.json` must not use `baseUrl` (deprecated in TypeScript 6, removed in 7). Use `paths` only.
- `web/tsconfig.app.json` keeps `erasableSyntaxOnly` and `verbatimModuleSyntax`: no enums, no constructor parameter properties, `import type` for types.
- Every UI change is checked in a real browser (Playwright) before it is called done.
- Every commit message ends with the trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.
- `make check` and `go test ./... -race -count=1` must pass at the end of every task.

## File Structure

| Path | Responsibility |
|---|---|
| `scripts/spike/structured-output.sh`, `docs/research/spike-structured-output.md` | Spike: structured CLI output in `stream-json` mode |
| `internal/secret/secret.go` | `Key` (parse, seal, open) and `Value` (redacting secret string) |
| `internal/config/config.go` | `REMEDY_MASTER_KEY` and `REMEDY_GITHUB_API_URL` |
| `internal/store/migrations/002_github.sql`, `internal/store/github.go` | Connection and repo persistence |
| `internal/github/client.go` | Read-only GitHub API client |
| `internal/server/github.go`, `server.go` | Connection and repos API, `GitHub` interface, wiring |
| `web/src/*` | Router, layout, shadcn components, settings page |

---

### Task 1: Spike: structured output in `stream-json` mode

The responder needs a structured diagnosis (spec section 6). The CLI documents `--json-schema` for `--output-format json`. Whether `--output-format stream-json` delivers it too is **unverified**, and Plan 3 builds on the answer. The other tasks do not depend on it and may proceed in parallel.

**Files:**
- Create: `scripts/spike/structured-output.sh`
- Create: `docs/research/spike-structured-output.md`

**Interfaces:**
- Consumes: the maintainer's logged-in `claude` CLI, `jq`.
- Produces: a written decision in `docs/research/spike-structured-output.md`: `use structured_output from the result event`, `use json mode`, or `extract JSON from the text`.

- [ ] **Step 1: Write the spike script**

Create `scripts/spike/structured-output.sh`:

```sh
#!/bin/sh
# Spike: does `claude -p --json-schema` deliver structured output in stream-json mode?
# Uses the same isolation flags as the Remedy runner. Prints, per output format, where the
# structured answer shows up.
set -eu

OUT=$(mktemp -d)
SCHEMA='{"type":"object","properties":{"summary":{"type":"string"},"confidence":{"type":"string","enum":["high","medium","low"]}},"required":["summary","confidence"],"additionalProperties":false}'
PROMPT="A CI build failed with: cannot find module left-pad. Give a one sentence summary and your confidence."

run() {
  label=$1
  shift
  env -u ANTHROPIC_API_KEY -u ANTHROPIC_AUTH_TOKEN \
    claude -p "$PROMPT" --permission-mode dontAsk \
    --safe-mode --restricted --strict-mcp-config --tools "Read" --model sonnet \
    --json-schema "$SCHEMA" "$@" > "$OUT/$label.out" 2> "$OUT/$label.err" || echo "$label: exit $?"
}

run stream --output-format stream-json --verbose
run json --output-format json

echo "== stream-json: result event"
jq -c 'select(.type=="result") | {subtype, is_error, has_structured_output: (has("structured_output")), structured_output, result}' "$OUT/stream.out"
echo "== json: result object"
jq -c '{subtype, is_error, has_structured_output: (has("structured_output")), structured_output, result}' "$OUT/json.out"
echo "raw output kept in $OUT"
```

Run: `chmod +x scripts/spike/structured-output.sh`

- [ ] **Step 2: Run it**

Run: `./scripts/spike/structured-output.sh`
Expected: two result lines. Note for each whether `has_structured_output` is `true`, whether `structured_output` matches the schema (`summary` string, `confidence` one of `high`, `medium`, `low`), and what `result` contains. If a run fails, read the `.err` file named in the output and record the message.

- [ ] **Step 3: Record the findings**

Create `docs/research/spike-structured-output.md` and fill in every answer from what you observed:

```markdown
# Spike: structured output in stream-json mode

Date: <YYYY-MM-DD>. Claude Code version (`claude --version`): <version>.
Script: `scripts/spike/structured-output.sh`.

## Observations

1. `--output-format stream-json --verbose --json-schema ...`: does the `result` event contain `structured_output`? <yes/no>. If yes, does it match the schema? <yes/no>. What does `result` contain? <text>.
2. `--output-format json --json-schema ...`: does the result object contain `structured_output`? <yes/no>. Matches the schema? <yes/no>.
3. Did the CLI emit any additional event that carries the structured answer (for example a tool use of a synthetic output tool)? <yes/no, describe>.
4. Cost and token use of one run (`total_cost_usd`, `usage`): <numbers>.

## Decision

<one of: "use structured_output from the result event" | "use json mode" | "extract JSON from the text"> and one paragraph of reasoning.

## Consequence for the responder (plan 3)

<what the runner and the control plane must do with the answer>
```

- [ ] **Step 4: Commit**

```bash
git add scripts/spike/structured-output.sh docs/research/spike-structured-output.md
git commit -m "docs: record the structured output spike" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: The `secret` package

**Files:**
- Create: `internal/secret/secret.go`
- Test: `internal/secret/secret_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces (package `secret`):
  - `var ErrOpen error`
  - `type Key` with `func ParseKey(s string) (Key, error)`, `func (Key) Seal(plaintext []byte, aad string) ([]byte, error)`, `func (Key) Open(sealed []byte, aad string) ([]byte, error)` (returns `ErrOpen` on a wrong key, a wrong context or tampering), `String() string` returning `***`
  - `type Value` with `func NewValue(s string) Value`, `func (Value) Reveal() string`, and `***` as its text, `fmt`, JSON and `slog` form

- [ ] **Step 1: Write the failing tests**

Create `internal/secret/secret_test.go`:

```go
package secret_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/secret"
)

func testKey(t *testing.T, fill byte) secret.Key {
	t.Helper()
	k, err := secret.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestParseKey(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	if _, err := secret.ParseKey(valid); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	if _, err := secret.ParseKey("  " + valid + "\n"); err != nil {
		t.Fatalf("surrounding whitespace should be ignored: %v", err)
	}

	bad := map[string]string{
		"empty":      "",
		"not base64": "!!!not-base64!!!",
		"too short":  base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 16)),
		"too long":   base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 33)),
	}
	for name, s := range bad {
		if _, err := secret.ParseKey(s); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	key := testKey(t, 7)
	sealed, err := key.Seal([]byte("ghp_topsecret"), "github_connection:1")
	if err != nil {
		t.Fatal(err)
	}
	got, err := key.Open(sealed, "github_connection:1")
	if err != nil || string(got) != "ghp_topsecret" {
		t.Fatalf("Open = %q, %v", got, err)
	}
}

func TestSealUsesAFreshNonceEveryTime(t *testing.T) {
	key := testKey(t, 7)
	a, _ := key.Seal([]byte("same"), "ctx")
	b, _ := key.Seal([]byte("same"), "ctx")
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same plaintext are identical, the nonce is reused")
	}
}

func TestSealedValueDoesNotContainThePlaintext(t *testing.T) {
	sealed, _ := testKey(t, 7).Seal([]byte("ghp_topsecret"), "ctx")
	if bytes.Contains(sealed, []byte("topsecret")) {
		t.Fatal("the plaintext is visible in the sealed value")
	}
}

func TestOpenRejectsWrongKeyWrongContextAndTampering(t *testing.T) {
	key := testKey(t, 7)
	sealed, _ := key.Seal([]byte("ghp_topsecret"), "ctx")

	if _, err := testKey(t, 8).Open(sealed, "ctx"); !errors.Is(err, secret.ErrOpen) {
		t.Errorf("wrong key: error = %v, want ErrOpen", err)
	}
	if _, err := key.Open(sealed, "another-row"); !errors.Is(err, secret.ErrOpen) {
		t.Errorf("wrong context: error = %v, want ErrOpen", err)
	}
	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := key.Open(tampered, "ctx"); !errors.Is(err, secret.ErrOpen) {
		t.Errorf("tampered: error = %v, want ErrOpen", err)
	}
	if _, err := key.Open(sealed[:5], "ctx"); !errors.Is(err, secret.ErrOpen) {
		t.Errorf("truncated: error = %v, want ErrOpen", err)
	}
}

func TestValueNeverRevealsItsContent(t *testing.T) {
	v := secret.NewValue("ghp_topsecret")

	var text bytes.Buffer
	slog.New(slog.NewTextHandler(&text, nil)).Info("x", "token", v)
	var js bytes.Buffer
	slog.New(slog.NewJSONHandler(&js, nil)).Info("x", "token", v)
	marshaled, err := json.Marshal(map[string]any{"token": v})
	if err != nil {
		t.Fatal(err)
	}

	outputs := []string{
		fmt.Sprint(v),
		fmt.Sprintf("%v|%+v|%#v|%s", v, v, v, v),
		string(marshaled),
		text.String(),
		js.String(),
	}
	for _, o := range outputs {
		if strings.Contains(o, "topsecret") {
			t.Errorf("secret leaked in %q", o)
		}
	}
	if !strings.Contains(string(marshaled), "***") {
		t.Errorf("JSON form = %s, want ***", marshaled)
	}
	if v.Reveal() != "ghp_topsecret" {
		t.Errorf("Reveal() = %q", v.Reveal())
	}
}

func TestKeyNeverPrintsItsBytes(t *testing.T) {
	k := testKey(t, 7)
	out := fmt.Sprintf("%v|%+v|%#v|%s", k, k, k, k)
	if strings.Contains(out, "\x07") || strings.Contains(out, "[7 7 7") {
		t.Fatalf("key bytes leaked in %q", out)
	}
	if !strings.Contains(out, "***") {
		t.Fatalf("output = %q, want ***", out)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/secret/ -count=1`
Expected: FAIL (build error: package `secret` has no non-test Go files).

- [ ] **Step 3: Write the implementation**

Create `internal/secret/secret.go`:

```go
// Package secret seals values with a master key and keeps secrets out of logs.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

const keyLen = 32

// ErrOpen means a sealed value could not be opened: wrong key, wrong context or tampered data.
var ErrOpen = errors.New("secret: cannot open sealed value (wrong key, wrong context or tampered data)")

// Key is a 256-bit master key. Its text form never contains the key.
type Key struct{ b [keyLen]byte }

// ParseKey decodes a standard Base64 string that must hold exactly 32 bytes.
func ParseKey(s string) (Key, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Key{}, errors.New("master key is empty")
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return Key{}, errors.New("master key is not valid Base64")
	}
	if len(raw) != keyLen {
		return Key{}, fmt.Errorf("master key must be %d bytes, got %d", keyLen, len(raw))
	}
	var k Key
	copy(k.b[:], raw)
	return k, nil
}

func (k Key) String() string   { return "***" }
func (k Key) GoString() string { return "secret.Key(***)" }

func (k Key) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(k.b[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal encrypts plaintext with AES-256-GCM and a fresh random nonce. aad binds the result to a
// context, for example a database row, so that it cannot be moved to another one. The nonce is
// prepended to the ciphertext.
func (k Key) Seal(plaintext []byte, aad string) ([]byte, error) {
	g, err := k.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, plaintext, []byte(aad)), nil
}

// Open reverses Seal. It returns ErrOpen for a wrong key, a wrong context and any tampering.
func (k Key) Open(sealed []byte, aad string) ([]byte, error) {
	g, err := k.aead()
	if err != nil {
		return nil, err
	}
	if len(sealed) < g.NonceSize() {
		return nil, ErrOpen
	}
	nonce, ciphertext := sealed[:g.NonceSize()], sealed[g.NonceSize():]
	plaintext, err := g.Open(nil, nonce, ciphertext, []byte(aad))
	if err != nil {
		return nil, ErrOpen
	}
	return plaintext, nil
}

// Value is a secret string. Printing, formatting, JSON encoding and logging it all yield "***";
// the content is only reachable through Reveal.
type Value struct{ s string }

func NewValue(s string) Value { return Value{s: s} }

// Reveal returns the secret. Call it only where the secret is actually used.
func (v Value) Reveal() string { return v.s }

func (Value) String() string               { return "***" }
func (Value) GoString() string             { return "secret.Value(***)" }
func (Value) MarshalJSON() ([]byte, error) { return []byte(`"***"`), nil }
func (Value) LogValue() slog.Value         { return slog.StringValue("***") }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/secret/ -v -race -count=1`
Expected: PASS for all tests.

- [ ] **Step 5: Commit**

```bash
make check
git add internal/secret
git commit -m "feat(secret): add AES-GCM sealing and a redacting secret value" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Master key and GitHub API URL in the configuration

**Files:**
- Modify: `internal/config/config.go`, `internal/config/config_test.go`

**Interfaces:**
- Consumes: `secret.ParseKey`, `secret.Key` (Task 2).
- Produces: `config.Server` gains `MasterKey secret.Key` (from `REMEDY_MASTER_KEY`, required) and `GitHubAPIURL string` (from `REMEDY_GITHUB_API_URL`, default `https://api.github.com`, must be an `http` or `https` URL). A missing or malformed key makes `ServerFromEnv` return an error naming the variable, so the server exits with code 2.

- [ ] **Step 1: Replace the config tests (failing)**

Overwrite `internal/config/config_test.go`:

```go
package config_test

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/config"
)

var masterKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func serverEnv(extra map[string]string) func(string) string {
	m := map[string]string{
		"REMEDY_ADMIN_PASSWORD": "a-long-enough-password",
		"REMEDY_RUNNER_TOKEN":   "a-runner-token-of-24-chars-or-more",
		"REMEDY_MASTER_KEY":     masterKey,
	}
	for k, v := range extra {
		m[k] = v
	}
	return env(m)
}

func TestServerFromEnvDefaults(t *testing.T) {
	c, err := config.ServerFromEnv(serverEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":8080" || c.DBPath != "remedy.db" || c.GitHubAPIURL != "https://api.github.com" {
		t.Fatalf("defaults = %+v", c)
	}
}

func TestServerFromEnvRejectsWeakSecrets(t *testing.T) {
	cases := map[string]map[string]string{
		"missing password":     {"REMEDY_ADMIN_PASSWORD": ""},
		"short password":       {"REMEDY_ADMIN_PASSWORD": "short"},
		"missing runner token": {"REMEDY_RUNNER_TOKEN": ""},
		"short runner token":   {"REMEDY_RUNNER_TOKEN": "short"},
	}
	for name, override := range cases {
		if _, err := config.ServerFromEnv(serverEnv(override)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestServerFromEnvRequiresAValidMasterKey(t *testing.T) {
	cases := map[string]string{
		"missing":    "",
		"not base64": "!!!",
		"too short":  base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 16)),
	}
	for name, key := range cases {
		_, err := config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_MASTER_KEY": key}))
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		if !strings.Contains(err.Error(), "REMEDY_MASTER_KEY") {
			t.Errorf("%s: error %q does not name the variable", name, err)
		}
	}
}

func TestServerFromEnvGitHubAPIURL(t *testing.T) {
	c, err := config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_GITHUB_API_URL": "http://127.0.0.1:9090"}))
	if err != nil || c.GitHubAPIURL != "http://127.0.0.1:9090" {
		t.Fatalf("GitHubAPIURL = %q, err = %v", c.GitHubAPIURL, err)
	}
	for _, bad := range []string{"ftp://example.com", "not a url", "//no-scheme"} {
		if _, err := config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_GITHUB_API_URL": bad})); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func TestRunnerFromEnv(t *testing.T) {
	c, err := config.RunnerFromEnv(env(map[string]string{
		"REMEDY_RUNNER_TOKEN": "a-runner-token-of-24-chars-or-more",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.ServerURL != "http://localhost:8080" || c.ClaudeBin != "claude" ||
		!strings.HasSuffix(c.WorkspaceRoot, "remedy-workspaces") {
		t.Fatalf("defaults = %+v", c)
	}

	if _, err := config.RunnerFromEnv(env(nil)); err == nil {
		t.Fatal("expected an error without a runner token")
	}
}

func TestRunnerFromEnvClaudeModel(t *testing.T) {
	token := "a-runner-token-of-24-chars-or-more"

	unset, err := config.RunnerFromEnv(env(map[string]string{"REMEDY_RUNNER_TOKEN": token}))
	if err != nil {
		t.Fatal(err)
	}
	if unset.ClaudeModel != "" {
		t.Fatalf("ClaudeModel = %q, want empty so that the adapter's default applies", unset.ClaudeModel)
	}

	set, err := config.RunnerFromEnv(env(map[string]string{
		"REMEDY_RUNNER_TOKEN": token, "REMEDY_CLAUDE_MODEL": "opus",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if set.ClaudeModel != "opus" {
		t.Fatalf("ClaudeModel = %q, want opus", set.ClaudeModel)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config/ -count=1`
Expected: FAIL (build error: `c.GitHubAPIURL undefined`).

- [ ] **Step 3: Implement**

Overwrite `internal/config/config.go`:

```go
// Package config reads process configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Jaydee94/remedy/internal/secret"
)

const (
	minPasswordLen = 12
	minTokenLen    = 24
	defaultGitHub  = "https://api.github.com"
)

type Server struct {
	Addr          string     // REMEDY_ADDR, default ":8080"
	DBPath        string     // REMEDY_DB, default "remedy.db"
	AdminPassword string     // REMEDY_ADMIN_PASSWORD, required, min 12 chars
	RunnerToken   string     // REMEDY_RUNNER_TOKEN, required, min 24 chars
	MasterKey     secret.Key // REMEDY_MASTER_KEY, required, 32 random bytes in Base64
	GitHubAPIURL  string     // REMEDY_GITHUB_API_URL, default "https://api.github.com"
}

func ServerFromEnv(get func(string) string) (Server, error) {
	c := Server{
		Addr:          orDefault(get("REMEDY_ADDR"), ":8080"),
		DBPath:        orDefault(get("REMEDY_DB"), "remedy.db"),
		AdminPassword: get("REMEDY_ADMIN_PASSWORD"),
		RunnerToken:   get("REMEDY_RUNNER_TOKEN"),
		GitHubAPIURL:  orDefault(get("REMEDY_GITHUB_API_URL"), defaultGitHub),
	}
	if len(c.AdminPassword) < minPasswordLen {
		return Server{}, errors.New("REMEDY_ADMIN_PASSWORD must be set and at least 12 characters")
	}
	if len(c.RunnerToken) < minTokenLen {
		return Server{}, errors.New("REMEDY_RUNNER_TOKEN must be set and at least 24 characters")
	}

	key, err := secret.ParseKey(get("REMEDY_MASTER_KEY"))
	if err != nil {
		return Server{}, fmt.Errorf("REMEDY_MASTER_KEY: %w (generate one with: openssl rand -base64 32)", err)
	}
	c.MasterKey = key

	u, err := url.Parse(c.GitHubAPIURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Server{}, errors.New("REMEDY_GITHUB_API_URL must be an http or https URL")
	}
	c.GitHubAPIURL = strings.TrimRight(c.GitHubAPIURL, "/")
	return c, nil
}

type Runner struct {
	ServerURL     string // REMEDY_SERVER_URL, default "http://localhost:8080"
	Token         string // REMEDY_RUNNER_TOKEN, required, min 24 chars
	WorkspaceRoot string // REMEDY_WORKSPACES, default $TMPDIR/remedy-workspaces
	ClaudeBin     string // REMEDY_CLAUDE_BIN, default "claude"
	ClaudeModel   string // REMEDY_CLAUDE_MODEL, empty means the adapter's default
}

func RunnerFromEnv(get func(string) string) (Runner, error) {
	c := Runner{
		ServerURL:     orDefault(get("REMEDY_SERVER_URL"), "http://localhost:8080"),
		Token:         get("REMEDY_RUNNER_TOKEN"),
		WorkspaceRoot: orDefault(get("REMEDY_WORKSPACES"), filepath.Join(os.TempDir(), "remedy-workspaces")),
		ClaudeBin:     orDefault(get("REMEDY_CLAUDE_BIN"), "claude"),
		ClaudeModel:   get("REMEDY_CLAUDE_MODEL"),
	}
	if len(c.Token) < minTokenLen {
		return Runner{}, errors.New("REMEDY_RUNNER_TOKEN must be set and at least 24 characters")
	}
	return c, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config/ -v -race -count=1`
Expected: PASS. (`go build ./...` still succeeds: `cmd/remedy-server` only reads config fields it already used.)

- [ ] **Step 5: Commit**

```bash
make check
git add internal/config
git commit -m "feat(config): require REMEDY_MASTER_KEY and add REMEDY_GITHUB_API_URL" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Migration 002 and the connection and repo store

**Files:**
- Create: `internal/store/migrations/002_github.sql`
- Create: `internal/store/github.go`
- Test: `internal/store/github_test.go`

**Interfaces:**
- Consumes: `store.Store`, `ErrNotFound`, `formatTS`, `parseTS`, `scanner` from the existing `store.go`; the test helper `openStore` from `store_test.go`.
- Produces (package `store`):
  - `type ConnectionStatus string` with `ConnOK`, `ConnError`, `ConnUndecryptable`; `const ConnectionID int64 = 1`; `var ErrExists error`
  - `type Connection struct{ ID int64; TokenCiphertext []byte; TokenHint, Login string; Status ConnectionStatus; StatusDetail string; CheckedAt time.Time }`
  - `func (*Store) SaveConnection(ctx, Connection) error` (upserts the single connection), `GetConnection(ctx) (Connection, error)`, `UpdateConnectionStatus(ctx, status ConnectionStatus, detail string, at time.Time) error`, `DeleteConnection(ctx) error` (cascades to repos). The last three return `ErrNotFound` when there is no connection.
  - `type Repo struct{ ID, ConnectionID int64; FullName, DefaultBranch string; Enabled bool; LastPolledAt *time.Time; LastError string; CreatedAt time.Time }`
  - `func (*Store) AddRepo(ctx, connectionID int64, fullName, defaultBranch string) (Repo, error)` (`ErrExists` for a duplicate, case-insensitive), `ListRepos(ctx) ([]Repo, error)` (sorted by name), `GetRepo(ctx, id int64) (Repo, error)`, `SetRepoEnabled(ctx, id int64, enabled bool) error`, `DeleteRepo(ctx, id int64) error` (the last three return `ErrNotFound` for an unknown ID)

- [ ] **Step 1: Write the failing tests**

Create `internal/store/github_test.go`:

```go
package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

func connection(login string) store.Connection {
	return store.Connection{
		TokenCiphertext: []byte{0xde, 0xad, 0xbe, 0xef},
		TokenHint:       "1234",
		Login:           login,
		Status:          store.ConnOK,
		CheckedAt:       time.Now(),
	}
}

func TestSaveAndGetConnection(t *testing.T) {
	s, ctx := openStore(t), context.Background()

	if _, err := s.GetConnection(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetConnection on an empty store = %v, want ErrNotFound", err)
	}

	if err := s.SaveConnection(ctx, connection("octo")); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetConnection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Login != "octo" || got.TokenHint != "1234" || got.Status != store.ConnOK ||
		string(got.TokenCiphertext) != "\xde\xad\xbe\xef" || got.ID != store.ConnectionID {
		t.Fatalf("connection = %+v", got)
	}

	if err := s.SaveConnection(ctx, connection("someone-else")); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetConnection(ctx)
	if got.Login != "someone-else" {
		t.Fatalf("saving again must replace the connection, got login %q", got.Login)
	}
}

func TestUpdateConnectionStatus(t *testing.T) {
	s, ctx := openStore(t), context.Background()

	if err := s.UpdateConnectionStatus(ctx, store.ConnError, "x", time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update without a connection = %v, want ErrNotFound", err)
	}

	_ = s.SaveConnection(ctx, connection("octo"))
	at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	if err := s.UpdateConnectionStatus(ctx, store.ConnUndecryptable, "wrong key", at); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetConnection(ctx)
	if got.Status != store.ConnUndecryptable || got.StatusDetail != "wrong key" || !got.CheckedAt.Equal(at) {
		t.Fatalf("connection = %+v", got)
	}
}

func TestDeleteConnectionRemovesItsRepos(t *testing.T) {
	s, ctx := openStore(t), context.Background()

	if err := s.DeleteConnection(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete without a connection = %v, want ErrNotFound", err)
	}

	_ = s.SaveConnection(ctx, connection("octo"))
	if _, err := s.AddRepo(ctx, store.ConnectionID, "octo/hello", "main"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteConnection(ctx); err != nil {
		t.Fatal(err)
	}
	repos, err := s.ListRepos(ctx)
	if err != nil || len(repos) != 0 {
		t.Fatalf("repos after deleting the connection = %+v, %v", repos, err)
	}
}

func TestAddAndListRepos(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	_ = s.SaveConnection(ctx, connection("octo"))

	b, err := s.AddRepo(ctx, store.ConnectionID, "octo/bravo", "main")
	if err != nil {
		t.Fatal(err)
	}
	if !b.Enabled || b.DefaultBranch != "main" || b.FullName != "octo/bravo" || b.LastPolledAt != nil || b.LastError != "" {
		t.Fatalf("repo = %+v", b)
	}
	if _, err := s.AddRepo(ctx, store.ConnectionID, "octo/alpha", "trunk"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.AddRepo(ctx, store.ConnectionID, "OCTO/Bravo", "main"); !errors.Is(err, store.ErrExists) {
		t.Fatalf("duplicate (different case) = %v, want ErrExists", err)
	}

	repos, err := s.ListRepos(ctx)
	if err != nil || len(repos) != 2 || repos[0].FullName != "octo/alpha" || repos[1].FullName != "octo/bravo" {
		t.Fatalf("repos = %+v, %v", repos, err)
	}

	got, err := s.GetRepo(ctx, b.ID)
	if err != nil || got.FullName != "octo/bravo" {
		t.Fatalf("GetRepo = %+v, %v", got, err)
	}
	if _, err := s.GetRepo(ctx, 9999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetRepo(unknown) = %v, want ErrNotFound", err)
	}
}

func TestAddRepoNeedsAConnection(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	_, err := s.AddRepo(ctx, store.ConnectionID, "octo/hello", "main")
	if err == nil || errors.Is(err, store.ErrExists) {
		t.Fatalf("AddRepo without a connection = %v, want a foreign key error", err)
	}
}

func TestSetRepoEnabledAndDelete(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	_ = s.SaveConnection(ctx, connection("octo"))
	r, _ := s.AddRepo(ctx, store.ConnectionID, "octo/hello", "main")

	if err := s.SetRepoEnabled(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetRepo(ctx, r.ID); got.Enabled {
		t.Fatal("repo is still enabled")
	}
	if err := s.SetRepoEnabled(ctx, 9999, true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SetRepoEnabled(unknown) = %v, want ErrNotFound", err)
	}

	if err := s.DeleteRepo(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRepo(ctx, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store/ -count=1`
Expected: FAIL (build error: `store.Connection` undefined).

- [ ] **Step 3: Write the migration**

Create `internal/store/migrations/002_github.sql`:

```sql
-- One connection for now (the code always uses id 1); the schema allows more without a migration.
CREATE TABLE github_connections (
  id               INTEGER PRIMARY KEY,
  token_ciphertext BLOB NOT NULL,
  token_hint       TEXT NOT NULL,
  login            TEXT NOT NULL,
  status           TEXT NOT NULL CHECK (status IN ('ok', 'error', 'undecryptable')),
  status_detail    TEXT NOT NULL DEFAULT '',
  checked_at       TEXT NOT NULL
);

CREATE TABLE repos (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  connection_id  INTEGER NOT NULL REFERENCES github_connections (id) ON DELETE CASCADE,
  full_name      TEXT NOT NULL UNIQUE COLLATE NOCASE,
  default_branch TEXT NOT NULL,
  enabled        INTEGER NOT NULL DEFAULT 1,
  last_polled_at TEXT,
  last_error     TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL
);
```

- [ ] **Step 4: Write the store methods**

Create `internal/store/github.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type ConnectionStatus string

const (
	ConnOK            ConnectionStatus = "ok"
	ConnError         ConnectionStatus = "error"
	ConnUndecryptable ConnectionStatus = "undecryptable"
)

// ConnectionID is the only connection for now. The schema allows more.
const ConnectionID int64 = 1

// ErrExists is returned when a unique constraint is violated.
var ErrExists = errors.New("already exists")

// Connection is the stored GitHub connection. The token is only ever stored sealed.
type Connection struct {
	ID              int64
	TokenCiphertext []byte
	TokenHint       string
	Login           string
	Status          ConnectionStatus
	StatusDetail    string
	CheckedAt       time.Time
}

// SaveConnection creates the connection or replaces the existing one.
func (s *Store) SaveConnection(ctx context.Context, c Connection) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO github_connections (id, token_ciphertext, token_hint, login, status, status_detail, checked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			token_ciphertext = excluded.token_ciphertext, token_hint = excluded.token_hint,
			login = excluded.login, status = excluded.status,
			status_detail = excluded.status_detail, checked_at = excluded.checked_at`,
		ConnectionID, c.TokenCiphertext, c.TokenHint, c.Login, string(c.Status), c.StatusDetail, formatTS(c.CheckedAt))
	return err
}

func (s *Store) GetConnection(ctx context.Context) (Connection, error) {
	var (
		c       Connection
		status  string
		checked string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, token_ciphertext, token_hint, login, status, status_detail, checked_at
		FROM github_connections WHERE id = ?`, ConnectionID).
		Scan(&c.ID, &c.TokenCiphertext, &c.TokenHint, &c.Login, &status, &c.StatusDetail, &checked)
	if errors.Is(err, sql.ErrNoRows) {
		return Connection{}, ErrNotFound
	}
	if err != nil {
		return Connection{}, err
	}
	c.Status = ConnectionStatus(status)
	if c.CheckedAt, err = parseTS(checked); err != nil {
		return Connection{}, err
	}
	return c, nil
}

func (s *Store) UpdateConnectionStatus(ctx context.Context, status ConnectionStatus, detail string, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE github_connections SET status = ?, status_detail = ?, checked_at = ? WHERE id = ?`,
		string(status), detail, formatTS(at), ConnectionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

// DeleteConnection removes the connection and, through the foreign key, its repos.
func (s *Store) DeleteConnection(ctx context.Context) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM github_connections WHERE id = ?`, ConnectionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

type Repo struct {
	ID            int64
	ConnectionID  int64
	FullName      string
	DefaultBranch string
	Enabled       bool
	LastPolledAt  *time.Time
	LastError     string
	CreatedAt     time.Time
}

const repoCols = `id, connection_id, full_name, default_branch, enabled, last_polled_at, last_error, created_at`

func scanRepo(sc scanner) (Repo, error) {
	var (
		r       Repo
		enabled int
		polled  sql.NullString
		created string
	)
	if err := sc.Scan(&r.ID, &r.ConnectionID, &r.FullName, &r.DefaultBranch, &enabled, &polled, &r.LastError, &created); err != nil {
		return Repo{}, err
	}
	r.Enabled = enabled != 0
	var err error
	if r.CreatedAt, err = parseTS(created); err != nil {
		return Repo{}, err
	}
	if polled.Valid {
		t, err := parseTS(polled.String)
		if err != nil {
			return Repo{}, err
		}
		r.LastPolledAt = &t
	}
	return r, nil
}

// AddRepo registers a repository. It returns ErrExists if the name is already registered,
// ignoring case.
func (s *Store) AddRepo(ctx context.Context, connectionID int64, fullName, defaultBranch string) (Repo, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO repos (connection_id, full_name, default_branch, enabled, created_at) VALUES (?, ?, ?, 1, ?)`,
		connectionID, fullName, defaultBranch, formatTS(time.Now()))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return Repo{}, ErrExists
		}
		return Repo{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Repo{}, err
	}
	return s.GetRepo(ctx, id)
}

func (s *Store) GetRepo(ctx context.Context, id int64) (Repo, error) {
	r, err := scanRepo(s.db.QueryRowContext(ctx, `SELECT `+repoCols+` FROM repos WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Repo{}, ErrNotFound
	}
	return r, err
}

func (s *Store) ListRepos(ctx context.Context) ([]Repo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+repoCols+` FROM repos ORDER BY full_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	repos := []Repo{}
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		repos = append(repos, r)
	}
	return repos, rows.Err()
}

func (s *Store) SetRepoEnabled(ctx context.Context, id int64, enabled bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE repos SET enabled = ? WHERE id = ?`, enabled, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteRepo(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM repos WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/store/ -v -race -count=1 2>&1 | grep -E "^(--- |PASS|FAIL|ok)"`
Expected: PASS for the new tests and the five existing store tests.

- [ ] **Step 6: Commit**

```bash
make check
git add internal/store
git commit -m "feat(store): add the GitHub connection and repo tables" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: The read-only `github` client

**Files:**
- Create: `internal/github/client.go`
- Test: `internal/github/client_test.go`

**Interfaces:**
- Consumes: `secret.Value` (Task 2).
- Produces (package `github`):
  - `var ErrUnauthorized, ErrNotFound, ErrInvalidRepoName error`
  - `type APIError struct{ Status int; Message string }` implementing `error`
  - `type User struct{ Login string }`, `type Repo struct{ FullName, DefaultBranch string; Private bool }`
  - `type Client`, `func New(baseURL string, token secret.Value, httpClient *http.Client) *Client` (a nil client gets a 20 s timeout)
  - `func (*Client) GetUser(ctx) (User, error)`, `func (*Client) GetRepo(ctx, fullName string) (Repo, error)`
  - Guarantees: only `GET` requests; every exported method name starts with `Get` or `List`; the token never appears in an error.

- [ ] **Step 1: Write the failing tests**

Create `internal/github/client_test.go`:

```go
package github_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/secret"
)

const token = "github_pat_TOPSECRET0123456789"

func newClient(t *testing.T, h http.HandlerFunc) *github.Client {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return github.New(ts.URL, secret.NewValue(token), ts.Client())
}

func TestGetUserSendsTheExpectedRequest(t *testing.T) {
	var method, path, auth, accept, version, agent string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		auth, accept = r.Header.Get("Authorization"), r.Header.Get("Accept")
		version, agent = r.Header.Get("X-GitHub-Api-Version"), r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`{"login":"octo"}`))
	})

	u, err := c.GetUser(context.Background())
	if err != nil || u.Login != "octo" {
		t.Fatalf("GetUser = %+v, %v", u, err)
	}
	if method != http.MethodGet || path != "/user" {
		t.Errorf("request = %s %s", method, path)
	}
	if auth != "Bearer "+token {
		t.Errorf("Authorization = %q", auth)
	}
	if accept != "application/vnd.github+json" || version != "2022-11-28" || agent == "" {
		t.Errorf("headers: Accept=%q, X-GitHub-Api-Version=%q, User-Agent=%q", accept, version, agent)
	}
}

func TestGetRepoDecodesTheResponse(t *testing.T) {
	var path string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"full_name":"Octo/Hello","default_branch":"trunk","private":true}`))
	})

	r, err := c.GetRepo(context.Background(), "octo/hello")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/repos/octo/hello" || r.FullName != "Octo/Hello" || r.DefaultBranch != "trunk" || !r.Private {
		t.Fatalf("path = %q, repo = %+v", path, r)
	}
}

func TestStatusCodesMapToErrors(t *testing.T) {
	cases := []struct {
		status int
		check  func(error) bool
	}{
		{http.StatusUnauthorized, func(err error) bool { return errors.Is(err, github.ErrUnauthorized) }},
		{http.StatusNotFound, func(err error) bool { return errors.Is(err, github.ErrNotFound) }},
		{http.StatusInternalServerError, func(err error) bool {
			var api *github.APIError
			return errors.As(err, &api) && api.Status == http.StatusInternalServerError
		}},
		{http.StatusForbidden, func(err error) bool {
			var api *github.APIError
			return errors.As(err, &api) && api.Status == http.StatusForbidden
		}},
	}
	for _, tc := range cases {
		c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"message":"nope"}`))
		})
		if _, err := c.GetUser(context.Background()); err == nil || !tc.check(err) {
			t.Errorf("status %d: error = %v", tc.status, err)
		}
	}
}

func TestInvalidRepoNamesAreRejectedWithoutARequest(t *testing.T) {
	var requests atomic.Int32
	c := newClient(t, func(http.ResponseWriter, *http.Request) { requests.Add(1) })

	for _, name := range []string{"", "a", "a/b/c", "../x", "x/..", "./x", "a/b?x=1", "a /b", "a/b#frag", "/a/b"} {
		if _, err := c.GetRepo(context.Background(), name); !errors.Is(err, github.ErrInvalidRepoName) {
			t.Errorf("%q: error = %v, want ErrInvalidRepoName", name, err)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("%d requests were sent for invalid names", n)
	}
}

func TestErrorsNeverContainTheToken(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"echo ` + token + ` back"}`))
	})

	_, err := c.GetUser(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "TOPSECRET") {
		t.Fatalf("the token leaked into the error: %v", err)
	}
	if !strings.Contains(err.Error(), "***") {
		t.Fatalf("error = %v, want the token replaced by ***", err)
	}
}

// The client is read-only by construction: every exported method is a Get or a List. A method with
// another name (Create, Update, Merge, ...) fails this test and must be a conscious decision.
func TestOnlyReadMethodsAreExported(t *testing.T) {
	typ := reflect.TypeOf(&github.Client{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if !strings.HasPrefix(name, "Get") && !strings.HasPrefix(name, "List") {
			t.Errorf("exported method %q is not a read method", name)
		}
	}
}

func TestEverySendMethodIsGET(t *testing.T) {
	var methods []string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		_, _ = w.Write([]byte(`{"login":"octo","full_name":"o/r","default_branch":"main"}`))
	})
	_, _ = c.GetUser(context.Background())
	_, _ = c.GetRepo(context.Background(), "o/r")

	if len(methods) != 2 {
		t.Fatalf("methods = %v", methods)
	}
	for _, m := range methods {
		if m != http.MethodGet {
			t.Errorf("sent %s, want GET only", m)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/github/ -count=1`
Expected: FAIL (build error: package `github` has no non-test Go files).

- [ ] **Step 3: Write the implementation**

Create `internal/github/client.go`:

```go
// Package github is a read-only client for the parts of the GitHub REST API that Remedy uses.
// It only ever sends GET requests, and its errors never contain the token.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/secret"
)

const (
	apiVersion = "2022-11-28"
	userAgent  = "remedy"
	maxBody    = 1 << 20
)

var (
	// ErrUnauthorized means GitHub rejected the token.
	ErrUnauthorized = errors.New("github: token rejected")
	// ErrNotFound means the resource does not exist or the token has no access to it. GitHub
	// answers 404 in both cases on purpose.
	ErrNotFound = errors.New("github: not found, or no access")
	// ErrInvalidRepoName means the name is not of the form owner/name.
	ErrInvalidRepoName = errors.New("github: repository name must be owner/name")
)

var repoName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// validRepoName accepts owner/name. The character class allows dots, so "." and ".." must be
// rejected explicitly: "../x" would otherwise turn into a request to a different path.
func validRepoName(s string) bool {
	if !repoName.MatchString(s) {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

// APIError is any other non-success answer, for example a rate limit (403, 429) or a 5xx.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("github: HTTP %d: %s", e.Status, e.Message) }

type User struct {
	Login string `json:"login"`
}

type Repo struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

// Client is a read-only GitHub API client. Every exported method is a Get or a List.
type Client struct {
	baseURL string
	token   secret.Value
	http    *http.Client
}

// New returns a client for baseURL (for example https://api.github.com). A nil httpClient gets a
// 20 second timeout.
func New(baseURL string, token secret.Value, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, http: httpClient}
}

// scrub removes the token from text that may be shown to a user or written to a log.
func (c *Client) scrub(s string) string {
	if t := c.token.Reveal(); t != "" {
		s = strings.ReplaceAll(s, t, "***")
	}
	return s
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return errors.New(c.scrub(err.Error()))
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", "Bearer "+c.token.Reveal())

	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New(c.scrub(err.Error()))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))

	switch resp.StatusCode {
	case http.StatusOK:
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("github: unexpected response: %w", err)
		}
		return nil
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNotFound
	default:
		msg := string(body)
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return &APIError{Status: resp.StatusCode, Message: c.scrub(strings.TrimSpace(msg))}
	}
}

// GetUser returns the account the token belongs to. It is the cheapest way to check a token.
func (c *Client) GetUser(ctx context.Context) (User, error) {
	var u User
	if err := c.get(ctx, "/user", &u); err != nil {
		return User{}, err
	}
	if u.Login == "" {
		return User{}, errors.New("github: unexpected response: no login")
	}
	return u, nil
}

// GetRepo returns a repository by its full name (owner/name).
func (c *Client) GetRepo(ctx context.Context, fullName string) (Repo, error) {
	if !validRepoName(fullName) {
		return Repo{}, ErrInvalidRepoName
	}
	var r Repo
	if err := c.get(ctx, "/repos/"+fullName, &r); err != nil {
		return Repo{}, err
	}
	return r, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/github/ -v -race -count=1 2>&1 | grep -E "^(--- |PASS|FAIL|ok)"`
Expected: PASS for all tests.

- [ ] **Step 5: Commit**

```bash
make check
git add internal/github
git commit -m "feat(github): add a read-only GitHub API client" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 6: The connection and repos API

**Files:**
- Modify: `internal/server/server.go`
- Create: `internal/server/github.go`
- Test: `internal/server/github_test.go`
- Modify: `cmd/remedy-server/main.go`

**Interfaces:**
- Consumes: `secret.Key`/`secret.Value` (Task 2), `config.Server.MasterKey`/`GitHubAPIURL` (Task 3), the store methods (Task 4), `github.Client`, `github.User`, `github.Repo` and the errors (Task 5).
- Produces:
  - `server.GitHub` interface `{ GetUser(ctx) (github.User, error); GetRepo(ctx, fullName string) (github.Repo, error) }`; `*github.Client` implements it.
  - `server.Deps` gains `Key secret.Key` and `NewGitHub func(token secret.Value) GitHub`. The GitHub routes are only registered when `NewGitHub` is set.
  - Routes (all behind the admin session and `X-Remedy-CSRF`): `GET|PUT|DELETE /api/github/connection`, `POST /api/github/connection/check`, `GET|POST /api/repos`, `PATCH|DELETE /api/repos/{id}`.
  - JSON: connection `{connected, login, tokenHint, status, statusDetail, checkedAt}` (`tokenHint` is `…` plus the last four characters, never the token); repo `{id, fullName, defaultBranch, enabled, lastPolledAt, lastError, createdAt}`.

- [ ] **Step 1: Write the failing tests**

Create `internal/server/github_test.go`:

```go
package server_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

const ghToken = "ghp_DISTINCTIVE0123456789abcdefghijklmnopqrst"

func ghKey(t *testing.T, fill byte) secret.Key {
	t.Helper()
	k, err := secret.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// fakeGitHub stands in for the GitHub API. It records the tokens it was created with.
type fakeGitHub struct {
	mu      sync.Mutex
	tokens  []string
	user    github.User
	userErr error
	repos   map[string]github.Repo // keyed by lower-case full name
	repoErr error
}

func (f *fakeGitHub) factory(token secret.Value) server.GitHub {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = append(f.tokens, token.Reveal())
	return f
}

func (f *fakeGitHub) lastToken() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.tokens) == 0 {
		return ""
	}
	return f.tokens[len(f.tokens)-1]
}

func (f *fakeGitHub) GetUser(context.Context) (github.User, error) { return f.user, f.userErr }

func (f *fakeGitHub) GetRepo(_ context.Context, name string) (github.Repo, error) {
	if f.repoErr != nil {
		return github.Repo{}, f.repoErr
	}
	if !strings.Contains(name, "/") {
		return github.Repo{}, github.ErrInvalidRepoName
	}
	r, ok := f.repos[strings.ToLower(name)]
	if !ok {
		return github.Repo{}, github.ErrNotFound
	}
	return r, nil
}

type ghEnv struct {
	ts     *httptest.Server
	store  *store.Store
	gh     *fakeGitHub
	key    secret.Key
	client *http.Client
}

// newGHEnv starts a server over st (a new store if nil) that seals with key, and logs in.
func newGHEnv(t *testing.T, st *store.Store, key secret.Key) *ghEnv {
	t.Helper()
	if st == nil {
		var err error
		st, err = store.Open(filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
	}
	gh := &fakeGitHub{
		user:  github.User{Login: "octo"},
		repos: map[string]github.Repo{"octo/hello": {FullName: "Octo/Hello", DefaultBranch: "main"}},
	}
	ts := httptest.NewServer(server.New(server.Deps{
		Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Key: key, NewGitHub: gh.factory,
	}))
	t.Cleanup(ts.Close)

	jar, _ := cookiejar.New(nil)
	e := &ghEnv{ts: ts, store: st, gh: gh, key: key, client: &http.Client{Jar: jar}}
	if code, _ := e.call(t, http.MethodPost, "/api/login", `{"password":"`+password+`"}`); code != http.StatusNoContent {
		t.Fatalf("login status = %d", code)
	}
	return e
}

// call sends an admin request and returns the status and the body.
func (e *ghEnv) call(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	req.Header.Set("X-Remedy-CSRF", "1")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (e *ghEnv) putToken(t *testing.T, token string) (int, string) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"token": token})
	return e.call(t, http.MethodPut, "/api/github/connection", string(b))
}

func field(t *testing.T, body, name string) any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("body %q is not a JSON object: %v", body, err)
	}
	return m[name]
}

func TestConnectionLifecycleNeverExposesTheToken(t *testing.T) {
	e := newGHEnv(t, nil, ghKey(t, 1))

	code, body := e.call(t, http.MethodGet, "/api/github/connection", "")
	if code != http.StatusOK || field(t, body, "connected") != false {
		t.Fatalf("empty GET = %d %s", code, body)
	}

	var bodies []string
	code, body = e.putToken(t, ghToken)
	bodies = append(bodies, body)
	if code != http.StatusOK || field(t, body, "connected") != true || field(t, body, "login") != "octo" ||
		field(t, body, "tokenHint") != "…qrst" || field(t, body, "status") != "ok" {
		t.Fatalf("PUT = %d %s", code, body)
	}
	if e.gh.lastToken() != ghToken {
		t.Fatalf("GitHub was called with token %q", e.gh.lastToken())
	}

	_, body = e.call(t, http.MethodGet, "/api/github/connection", "")
	bodies = append(bodies, body)
	_, body = e.call(t, http.MethodPost, "/api/github/connection/check", "")
	bodies = append(bodies, body)
	_, body = e.call(t, http.MethodGet, "/api/repos", "")
	bodies = append(bodies, body)

	for _, b := range bodies {
		if strings.Contains(b, "DISTINCTIVE") {
			t.Errorf("the token leaked into a response: %s", b)
		}
	}

	if code, _ := e.call(t, http.MethodDelete, "/api/github/connection", ""); code != http.StatusNoContent {
		t.Fatalf("DELETE = %d", code)
	}
	if code, _ := e.call(t, http.MethodDelete, "/api/github/connection", ""); code != http.StatusNotFound {
		t.Fatalf("second DELETE = %d, want 404", code)
	}
	_, body = e.call(t, http.MethodGet, "/api/github/connection", "")
	if field(t, body, "connected") != false {
		t.Fatalf("GET after DELETE = %s", body)
	}
}

func TestTheTokenIsStoredSealedAndBoundToItsRow(t *testing.T) {
	key := ghKey(t, 1)
	e := newGHEnv(t, nil, key)
	if code, body := e.putToken(t, ghToken); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}

	conn, err := e.store.GetConnection(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(conn.TokenCiphertext, []byte("DISTINCTIVE")) {
		t.Fatal("the plaintext token is in the database")
	}
	if conn.TokenHint != "qrst" {
		t.Errorf("TokenHint = %q, want the last four characters", conn.TokenHint)
	}
	if got, err := key.Open(conn.TokenCiphertext, "github_connection:1"); err != nil || string(got) != ghToken {
		t.Fatalf("Open = %q, %v", got, err)
	}
	if _, err := key.Open(conn.TokenCiphertext, "github_connection:2"); err == nil {
		t.Fatal("the ciphertext opens in another row's context")
	}
}

func TestPutConnectionValidatesInput(t *testing.T) {
	e := newGHEnv(t, nil, ghKey(t, 1))

	if code, _ := e.putToken(t, "short"); code != http.StatusBadRequest {
		t.Errorf("short token = %d, want 400", code)
	}
	if code, _ := e.call(t, http.MethodPut, "/api/github/connection", `not json`); code != http.StatusBadRequest {
		t.Errorf("invalid JSON = %d, want 400", code)
	}

	e.gh.userErr = github.ErrUnauthorized
	code, body := e.putToken(t, ghToken)
	if code != http.StatusBadRequest || !strings.Contains(body, "rejected") {
		t.Errorf("rejected token = %d %s, want 400 mentioning rejected", code, body)
	}

	e.gh.userErr = &github.APIError{Status: 500, Message: "boom"}
	if code, _ := e.putToken(t, ghToken); code != http.StatusBadGateway {
		t.Errorf("GitHub failure = %d, want 502", code)
	}

	if _, err := e.store.GetConnection(context.Background()); err == nil {
		t.Error("a connection was stored although validation failed")
	}
}

func TestGitHubRoutesNeedASession(t *testing.T) {
	e := newGHEnv(t, nil, ghKey(t, 1))
	resp, err := http.Get(e.ts.URL + "/api/github/connection")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET without a session = %d, want 401", resp.StatusCode)
	}
}

func TestRepoLifecycle(t *testing.T) {
	e := newGHEnv(t, nil, ghKey(t, 1))

	if code, _ := e.call(t, http.MethodPost, "/api/repos", `{"fullName":"octo/hello"}`); code != http.StatusConflict {
		t.Fatalf("adding a repo without a connection = %d, want 409", code)
	}

	e.putToken(t, ghToken)
	code, body := e.call(t, http.MethodPost, "/api/repos", `{"fullName":"octo/hello"}`)
	if code != http.StatusCreated || field(t, body, "fullName") != "Octo/Hello" ||
		field(t, body, "defaultBranch") != "main" || field(t, body, "enabled") != true {
		t.Fatalf("add = %d %s (the canonical name from GitHub is stored)", code, body)
	}
	if e.gh.lastToken() != ghToken {
		t.Fatalf("the repo check used token %q, want the decrypted stored token", e.gh.lastToken())
	}
	id := int64(field(t, body, "id").(float64))

	if code, _ := e.call(t, http.MethodPost, "/api/repos", `{"fullName":"OCTO/hello"}`); code != http.StatusConflict {
		t.Fatalf("duplicate = %d, want 409", code)
	}

	_, body = e.call(t, http.MethodGet, "/api/repos", "")
	var list []map[string]any
	if err := json.Unmarshal([]byte(body), &list); err != nil || len(list) != 1 {
		t.Fatalf("list = %s (%v)", body, err)
	}

	path := "/api/repos/" + strconv.FormatInt(id, 10)
	if code, _ := e.call(t, http.MethodPatch, path, `{"enabled":false}`); code != http.StatusNoContent {
		t.Fatalf("PATCH = %d", code)
	}
	_, body = e.call(t, http.MethodGet, "/api/repos", "")
	if !strings.Contains(body, `"enabled":false`) {
		t.Fatalf("repo is still enabled: %s", body)
	}
	if code, _ := e.call(t, http.MethodPatch, path, `{}`); code != http.StatusBadRequest {
		t.Fatalf("PATCH without enabled = %d, want 400", code)
	}
	if code, _ := e.call(t, http.MethodPatch, "/api/repos/9999", `{"enabled":true}`); code != http.StatusNotFound {
		t.Fatalf("PATCH unknown = %d, want 404", code)
	}

	if code, _ := e.call(t, http.MethodDelete, path, ""); code != http.StatusNoContent {
		t.Fatalf("DELETE = %d", code)
	}
	if code, _ := e.call(t, http.MethodDelete, path, ""); code != http.StatusNotFound {
		t.Fatalf("second DELETE = %d, want 404", code)
	}
	if code, _ := e.call(t, http.MethodDelete, "/api/repos/not-a-number", ""); code != http.StatusNotFound {
		t.Fatalf("DELETE with a bad id = %d, want 404", code)
	}
}

func TestAddRepoErrors(t *testing.T) {
	e := newGHEnv(t, nil, ghKey(t, 1))
	e.putToken(t, ghToken)

	if code, _ := e.call(t, http.MethodPost, "/api/repos", `{"fullName":"nope"}`); code != http.StatusBadRequest {
		t.Errorf("invalid name = %d, want 400", code)
	}
	if code, body := e.call(t, http.MethodPost, "/api/repos", `{"fullName":"octo/missing"}`); code != http.StatusBadRequest ||
		!strings.Contains(body, "not found") {
		t.Errorf("unknown repo = %d %s, want 400 mentioning not found", code, body)
	}

	e.gh.repoErr = &github.APIError{Status: 502, Message: "bad gateway"}
	if code, _ := e.call(t, http.MethodPost, "/api/repos", `{"fullName":"octo/hello"}`); code != http.StatusBadGateway {
		t.Errorf("GitHub failure = %d, want 502", code)
	}
}

func TestAWrongMasterKeyMarksTheConnectionUndecryptable(t *testing.T) {
	first := newGHEnv(t, nil, ghKey(t, 1))
	first.putToken(t, ghToken)

	second := newGHEnv(t, first.store, ghKey(t, 2))
	code, body := second.call(t, http.MethodPost, "/api/github/connection/check", "")
	if code != http.StatusOK || field(t, body, "status") != "undecryptable" {
		t.Fatalf("check with a wrong key = %d %s", code, body)
	}
	_, body = second.call(t, http.MethodGet, "/api/github/connection", "")
	if field(t, body, "status") != "undecryptable" || field(t, body, "statusDetail") == "" {
		t.Fatalf("GET = %s", body)
	}
	if code, _ := second.call(t, http.MethodPost, "/api/repos", `{"fullName":"octo/hello"}`); code != http.StatusConflict {
		t.Fatalf("adding a repo with an undecryptable token = %d, want 409", code)
	}
	if len(second.gh.tokens) != 0 {
		t.Fatalf("GitHub was called although the token could not be decrypted: %v", second.gh.tokens)
	}

	// Entering the token again repairs the connection.
	if code, body := second.putToken(t, ghToken); code != http.StatusOK || field(t, body, "status") != "ok" {
		t.Fatalf("PUT = %d %s", code, body)
	}
}

func TestCheckConnectionReportsGitHubProblems(t *testing.T) {
	e := newGHEnv(t, nil, ghKey(t, 1))
	if code, _ := e.call(t, http.MethodPost, "/api/github/connection/check", ""); code != http.StatusNotFound {
		t.Fatalf("check without a connection = %d, want 404", code)
	}

	e.putToken(t, ghToken)
	e.gh.userErr = github.ErrUnauthorized
	code, body := e.call(t, http.MethodPost, "/api/github/connection/check", "")
	if code != http.StatusOK || field(t, body, "status") != "error" {
		t.Fatalf("check with a rejected token = %d %s", code, body)
	}

	e.gh.userErr = nil
	_, body = e.call(t, http.MethodPost, "/api/github/connection/check", "")
	if field(t, body, "status") != "ok" {
		t.Fatalf("check after recovery = %s", body)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/server/ -run 'Connection|Repo|Token|Key' -count=1`
Expected: FAIL (build error: `server.Deps` has no field `Key`).

- [ ] **Step 3: Extend `Deps` and register the routes**

In `internal/server/server.go`, add `"github.com/Jaydee94/remedy/internal/secret"` to the imports and replace the `Deps` struct and the end of `New` so they read:

```go
type Deps struct {
	Store       *store.Store
	Auth        *auth.Auth
	RunnerToken string
	Web         fs.FS // optional: the built UI, served for every non-API path

	// Key seals the GitHub token. NewGitHub builds a client for a token; when it is nil the GitHub
	// routes are not registered.
	Key       secret.Key
	NewGitHub func(token secret.Value) GitHub
}
```

and, inside `New`, directly before `if d.Web != nil {`:

```go
	if d.NewGitHub != nil {
		mux.HandleFunc("GET /api/github/connection", s.session(s.getConnection))
		mux.HandleFunc("PUT /api/github/connection", s.session(s.putConnection))
		mux.HandleFunc("POST /api/github/connection/check", s.session(s.checkConnection))
		mux.HandleFunc("DELETE /api/github/connection", s.session(s.deleteConnection))
		mux.HandleFunc("GET /api/repos", s.session(s.listRepos))
		mux.HandleFunc("POST /api/repos", s.session(s.addRepo))
		mux.HandleFunc("PATCH /api/repos/{id}", s.session(s.patchRepo))
		mux.HandleFunc("DELETE /api/repos/{id}", s.session(s.deleteRepo))
	}

```

- [ ] **Step 4: Write the handlers**

Create `internal/server/github.go`:

```go
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
)

// GitHub is what the control plane needs from the GitHub API. *github.Client implements it.
type GitHub interface {
	GetUser(ctx context.Context) (github.User, error)
	GetRepo(ctx context.Context, fullName string) (github.Repo, error)
}

const (
	minTokenLen = 20
	maxTokenLen = 512

	undecryptableDetail = "The stored token cannot be decrypted. Check REMEDY_MASTER_KEY or enter the token again."
)

// connectionAAD binds the sealed token to its row, so a ciphertext cannot be moved to another one.
func connectionAAD() string { return "github_connection:" + strconv.FormatInt(store.ConnectionID, 10) }

type connectionView struct {
	Connected    bool       `json:"connected"`
	Login        string     `json:"login,omitempty"`
	TokenHint    string     `json:"tokenHint,omitempty"`
	Status       string     `json:"status,omitempty"`
	StatusDetail string     `json:"statusDetail,omitempty"`
	CheckedAt    *time.Time `json:"checkedAt,omitempty"`
}

// viewOf never contains the token, only the last four characters.
func viewOf(c store.Connection) connectionView {
	at := c.CheckedAt
	return connectionView{
		Connected:    true,
		Login:        c.Login,
		TokenHint:    "…" + c.TokenHint,
		Status:       string(c.Status),
		StatusDetail: c.StatusDetail,
		CheckedAt:    &at,
	}
}

func (s *srv) openToken(c store.Connection) (secret.Value, error) {
	raw, err := s.d.Key.Open(c.TokenCiphertext, connectionAAD())
	if err != nil {
		return secret.Value{}, err
	}
	return secret.NewValue(string(raw)), nil
}

func (s *srv) markUndecryptable(ctx context.Context) {
	_ = s.d.Store.UpdateConnectionStatus(ctx, store.ConnUndecryptable, undecryptableDetail, time.Now())
}

func (s *srv) getConnection(w http.ResponseWriter, r *http.Request) {
	c, err := s.d.Store.GetConnection(r.Context())
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, connectionView{Connected: false})
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the connection")
		return
	}
	writeJSON(w, http.StatusOK, viewOf(c))
}

func (s *srv) putConnection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	token := strings.TrimSpace(req.Token)
	if len(token) < minTokenLen || len(token) > maxTokenLen {
		writeErr(w, http.StatusBadRequest, "the token must be 20 to 512 characters")
		return
	}

	user, err := s.d.NewGitHub(secret.NewValue(token)).GetUser(r.Context())
	if errors.Is(err, github.ErrUnauthorized) {
		writeErr(w, http.StatusBadRequest, "GitHub rejected the token")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, "could not reach GitHub")
		return
	}

	sealed, err := s.d.Key.Seal([]byte(token), connectionAAD())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not store the token")
		return
	}
	conn := store.Connection{
		TokenCiphertext: sealed,
		TokenHint:       token[len(token)-4:],
		Login:           user.Login,
		Status:          store.ConnOK,
		CheckedAt:       time.Now().UTC(), // UTC, like every time that comes back from the store
	}
	if err := s.d.Store.SaveConnection(r.Context(), conn); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not store the connection")
		return
	}
	writeJSON(w, http.StatusOK, viewOf(conn))
}

// checkConnection re-validates the stored token and records the result.
func (s *srv) checkConnection(w http.ResponseWriter, r *http.Request) {
	conn, err := s.d.Store.GetConnection(r.Context())
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no GitHub connection")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the connection")
		return
	}

	token, err := s.openToken(conn)
	switch {
	case errors.Is(err, secret.ErrOpen):
		s.markUndecryptable(r.Context())
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "could not read the token")
		return
	default:
		status, detail := store.ConnOK, ""
		if _, err := s.d.NewGitHub(token).GetUser(r.Context()); errors.Is(err, github.ErrUnauthorized) {
			status, detail = store.ConnError, "GitHub rejected the stored token."
		} else if err != nil {
			status, detail = store.ConnError, "Could not reach GitHub."
		}
		_ = s.d.Store.UpdateConnectionStatus(r.Context(), status, detail, time.Now())
	}

	updated, err := s.d.Store.GetConnection(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the connection")
		return
	}
	writeJSON(w, http.StatusOK, viewOf(updated))
}

func (s *srv) deleteConnection(w http.ResponseWriter, r *http.Request) {
	err := s.d.Store.DeleteConnection(r.Context())
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no GitHub connection")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not remove the connection")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type repoView struct {
	ID            int64      `json:"id"`
	FullName      string     `json:"fullName"`
	DefaultBranch string     `json:"defaultBranch"`
	Enabled       bool       `json:"enabled"`
	LastPolledAt  *time.Time `json:"lastPolledAt,omitempty"`
	LastError     string     `json:"lastError"`
	CreatedAt     time.Time  `json:"createdAt"`
}

func repoViewOf(r store.Repo) repoView {
	return repoView{
		ID: r.ID, FullName: r.FullName, DefaultBranch: r.DefaultBranch, Enabled: r.Enabled,
		LastPolledAt: r.LastPolledAt, LastError: r.LastError, CreatedAt: r.CreatedAt,
	}
}

func (s *srv) listRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := s.d.Store.ListRepos(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list repos")
		return
	}
	views := make([]repoView, 0, len(repos))
	for _, repo := range repos {
		views = append(views, repoViewOf(repo))
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *srv) addRepo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FullName string `json:"fullName"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	conn, err := s.d.Store.GetConnection(r.Context())
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusConflict, "connect GitHub first")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the connection")
		return
	}
	token, err := s.openToken(conn)
	if errors.Is(err, secret.ErrOpen) {
		s.markUndecryptable(r.Context())
		writeErr(w, http.StatusConflict, undecryptableDetail)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not read the token")
		return
	}

	repo, err := s.d.NewGitHub(token).GetRepo(r.Context(), strings.TrimSpace(req.FullName))
	switch {
	case errors.Is(err, github.ErrInvalidRepoName):
		writeErr(w, http.StatusBadRequest, "use the form owner/name")
		return
	case errors.Is(err, github.ErrNotFound):
		writeErr(w, http.StatusBadRequest, "repository not found, or the token has no access to it")
		return
	case errors.Is(err, github.ErrUnauthorized):
		writeErr(w, http.StatusBadRequest, "GitHub rejected the stored token")
		return
	case err != nil:
		writeErr(w, http.StatusBadGateway, "could not reach GitHub")
		return
	}

	added, err := s.d.Store.AddRepo(r.Context(), conn.ID, repo.FullName, repo.DefaultBranch)
	if errors.Is(err, store.ErrExists) {
		writeErr(w, http.StatusConflict, "this repository is already added")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not add the repository")
		return
	}
	writeJSON(w, http.StatusCreated, repoViewOf(added))
}

// repoID parses the {id} path value. ok is false for anything that is not a number.
func repoID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil
}

func (s *srv) patchRepo(w http.ResponseWriter, r *http.Request) {
	id, ok := repoID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "repository not found")
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Enabled == nil {
		writeErr(w, http.StatusBadRequest, "enabled is required")
		return
	}
	err := s.d.Store.SetRepoEnabled(r.Context(), id, *req.Enabled)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "repository not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not update the repository")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) deleteRepo(w http.ResponseWriter, r *http.Request) {
	id, ok := repoID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "repository not found")
		return
	}
	err := s.d.Store.DeleteRepo(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "repository not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not remove the repository")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/server/ -v -race -count=1 2>&1 | grep -E "^(--- |PASS|FAIL|ok)"`
Expected: PASS for the new tests and the existing server tests.

- [ ] **Step 6: Wire the server binary**

In `cmd/remedy-server/main.go`, add the imports `"github.com/Jaydee94/remedy/internal/github"` and `"github.com/Jaydee94/remedy/internal/secret"`, and extend the `server.Deps` literal so it reads:

```go
		Handler: server.New(server.Deps{
			Store:       st,
			Auth:        auth.New(cfg.AdminPassword),
			RunnerToken: cfg.RunnerToken,
			Web:         web.FS(),
			Key:         cfg.MasterKey,
			NewGitHub: func(token secret.Value) server.GitHub {
				return github.New(cfg.GitHubAPIURL, token, nil)
			},
		}),
```

Run: `go build ./... && go vet ./...`
Expected: no output.

- [ ] **Step 7: Smoke-test the real binary**

Run:

```bash
make build-go
export REMEDY_ADMIN_PASSWORD='smoke-test-password' REMEDY_RUNNER_TOKEN='smoke-runner-token-0123456789'
REMEDY_MASTER_KEY=short ./bin/remedy-server; echo "exit with a bad key: $?"
```

Expected: a log line `invalid configuration` naming `REMEDY_MASTER_KEY`, and `exit with a bad key: 2`.

- [ ] **Step 8: Commit**

```bash
make check
git add internal/server cmd/remedy-server
git commit -m "feat(server): add the GitHub connection and repos API" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 7: UI foundation: router, shadcn/ui and the sidebar layout

Every command below was run against a scratch copy of `web/` while writing this plan. `shadcn init` asks an interactive question unless `--preset` is given, so the flags matter.

**Files:**
- Modify: `web/package.json`, `web/package-lock.json` (by `npm`), `web/tsconfig.json`, `web/tsconfig.app.json`, `web/vite.config.ts`, `web/index.html`, `web/.oxlintrc.json`, `web/src/index.css` (by the CLI)
- Create (by the CLI): `web/components.json`, `web/src/lib/utils.ts`, `web/src/components/ui/*.tsx`
- Create: `web/src/components/AppLayout.tsx`, `web/src/NotFound.tsx`
- Modify: `web/src/main.tsx`, `web/src/App.tsx`, `web/src/Login.tsx`, `web/src/RunsPage.tsx`, `web/src/RunView.tsx`

**Interfaces:**
- Consumes: the existing `api`, `ApiError`, `streamRun`, `Run`, `RunEvent` from `web/src/api.ts` and `statusColor` from `web/src/status.ts`.
- Produces: routes `/` (redirects to `/runs`), `/runs`, `/runs/:id`, `/settings` (Task 8 adds the page), `*`; the `@/` import alias for `web/src`; shadcn components `button card input label badge table switch alert separator skeleton textarea` under `@/components/ui`.

- [ ] **Step 1: Install the router**

Run: `cd web && npm install react-router`
Expected: `react-router` 8.x is added to `dependencies` (its peer requirement is `react >= 19.2.7`; the project has `^19.2.8`).

- [ ] **Step 2: Add the `@` alias to the TypeScript configuration (without `baseUrl`)**

In `web/tsconfig.json`, add a `compilerOptions` block after `references` so the file reads:

```json
{
  "files": [],
  "references": [
    { "path": "./tsconfig.app.json" },
    { "path": "./tsconfig.node.json" }
  ],
  "compilerOptions": {
    "paths": { "@/*": ["./src/*"] }
  }
}
```

In `web/tsconfig.app.json`, add this line directly after `"noEmit": true,`:

```json
    "paths": { "@/*": ["./src/*"] },
```

- [ ] **Step 3: Add the alias to Vite**

Overwrite `web/vite.config.ts`:

```ts
import { fileURLToPath, URL } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    // Control plane runs on :8080 during development (see `make dev-server`).
    proxy: { '/api': 'http://localhost:8080', '/healthz': 'http://localhost:8080' },
  },
})
```

- [ ] **Step 4: Initialise shadcn/ui and add the components**

Run (from `web/`; `CI=1` and the closed stdin make sure it can never wait for a prompt):

```bash
CI=1 npx --yes shadcn@latest init --yes --base radix --preset nova --css-variables < /dev/null
CI=1 npx --yes shadcn@latest add button card input label badge table switch alert separator skeleton textarea --yes < /dev/null
```

Expected: `components.json`, `src/lib/utils.ts` and eleven files under `src/components/ui/` are created, `src/index.css` is rewritten with the theme variables, and new dependencies appear in `package.json`.

- [ ] **Step 5: Silence the generated files' fast-refresh warning**

The generated `button.tsx` and `badge.tsx` export their variants next to the component, which `oxlint` flags.

Overwrite `web/.oxlintrc.json`:

```json
{
  "$schema": "./node_modules/oxlint/configuration_schema.json",
  "plugins": ["react", "typescript", "oxc"],
  "rules": {
    "react/rules-of-hooks": "error",
    "react/only-export-components": ["warn", { "allowConstantExport": true }]
  },
  "overrides": [
    {
      "files": ["src/components/ui/**"],
      "rules": { "react/only-export-components": "off" }
    }
  ]
}
```

- [ ] **Step 6: Use the dark theme**

In `web/index.html`, change `<html lang="en">` to `<html lang="en" class="dark">`.

- [ ] **Step 7: Write the app shell**

Overwrite `web/src/main.tsx`:

```tsx
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router'
import './index.css'
import App from './App.tsx'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </StrictMode>,
)
```

Create `web/src/components/AppLayout.tsx`:

```tsx
import { LogOut, Play, Settings } from 'lucide-react'
import { NavLink, Outlet } from 'react-router'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

const nav = [
  { to: '/runs', label: 'Runs', icon: Play },
  { to: '/settings', label: 'Settings', icon: Settings },
]

export default function AppLayout({ onSignOut }: { onSignOut: () => void }) {
  return (
    <div className="flex min-h-screen">
      <aside className="flex w-56 shrink-0 flex-col gap-6 border-r border-border bg-card/40 p-4">
        <NavLink to="/" className="px-2 text-xl font-semibold tracking-tight">
          Remedy
        </NavLink>
        <nav className="flex flex-1 flex-col gap-1">
          {nav.map(({ to, label, icon: Icon }) => (
            <NavLink
              key={to}
              to={to}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-2 rounded-lg px-2 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground',
                  isActive && 'bg-accent text-accent-foreground',
                )
              }
            >
              <Icon className="size-4" />
              {label}
            </NavLink>
          ))}
        </nav>
        <Button variant="ghost" size="sm" className="justify-start" onClick={onSignOut}>
          <LogOut /> Sign out
        </Button>
      </aside>
      <main className="min-w-0 flex-1 p-8">
        <div className="mx-auto max-w-4xl">
          <Outlet />
        </div>
      </main>
    </div>
  )
}
```

Create `web/src/NotFound.tsx`:

```tsx
import { Link } from 'react-router'

export default function NotFound() {
  return (
    <div className="flex flex-col gap-2">
      <h1 className="text-2xl font-semibold tracking-tight">Page not found</h1>
      <Link to="/runs" className="text-sm text-muted-foreground hover:text-foreground">
        Back to runs
      </Link>
    </div>
  )
}
```

The Settings route is added to `App.tsx` in Task 8.

Overwrite `web/src/App.tsx`:

```tsx
import { useEffect, useState } from 'react'
import { Navigate, Route, Routes, useParams } from 'react-router'
import { api } from './api.ts'
import AppLayout from './components/AppLayout.tsx'
import Login from './Login.tsx'
import NotFound from './NotFound.tsx'
import RunsPage from './RunsPage.tsx'
import RunView from './RunView.tsx'

/** Mounts RunView with key={id} so that switching runs resets its state. */
function RunRoute() {
  const { id } = useParams()
  return id ? <RunView key={id} id={id} /> : <Navigate to="/runs" replace />
}

export default function App() {
  const [auth, setAuth] = useState<'loading' | 'in' | 'out'>('loading')

  useEffect(() => {
    api.me().then(() => setAuth('in')).catch(() => setAuth('out'))
  }, [])

  if (auth === 'loading') return null
  if (auth === 'out') return <Login onLoggedIn={() => setAuth('in')} />

  async function signOut() {
    await api.logout()
    setAuth('out')
  }

  return (
    <Routes>
      <Route element={<AppLayout onSignOut={signOut} />}>
        <Route index element={<Navigate to="/runs" replace />} />
        <Route path="runs" element={<RunsPage />} />
        <Route path="runs/:id" element={<RunRoute />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  )
}
```

- [ ] **Step 8: Restyle the existing pages with shadcn components and router links**

Overwrite `web/src/Login.tsx`:

```tsx
import { useState } from 'react'
import type { FormEvent } from 'react'
import { api, ApiError } from './api.ts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

export default function Login({ onLoggedIn }: { onLoggedIn: () => void }) {
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api.login(password)
      onLoggedIn()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Login failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle className="text-2xl">Remedy</CardTitle>
          <CardDescription>Sign in with the admin password.</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={submit} className="flex flex-col gap-3">
            <Input
              type="password"
              autoFocus
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="Admin password"
              aria-label="Admin password"
            />
            <Button type="submit" disabled={busy || password === ''}>
              Sign in
            </Button>
            {error && (
              <Alert variant="destructive">
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}
          </form>
        </CardContent>
      </Card>
    </main>
  )
}
```

Overwrite `web/src/RunsPage.tsx`:

```tsx
import { useCallback, useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { Link, useNavigate } from 'react-router'
import { api, ApiError } from './api.ts'
import type { Run } from './api.ts'
import { statusColor } from './status.ts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Textarea } from '@/components/ui/textarea'

export default function RunsPage() {
  const navigate = useNavigate()
  const [runs, setRuns] = useState<Run[]>([])
  const [prompt, setPrompt] = useState('')
  const [error, setError] = useState('')

  const refresh = useCallback(() => {
    api.listRuns().then(setRuns).catch((e: unknown) => setError(String(e)))
  }, [])

  useEffect(() => {
    refresh()
    const t = setInterval(refresh, 3000)
    return () => clearInterval(t)
  }, [refresh])

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    try {
      const created = await api.createRun(prompt)
      setPrompt('')
      navigate(`/runs/${created.id}`)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not start the run')
    }
  }

  return (
    <div className="flex flex-col gap-8">
      <h1 className="text-2xl font-semibold tracking-tight">Runs</h1>

      <Card>
        <CardHeader>
          <CardTitle>Start a run</CardTitle>
        </CardHeader>
        <CardContent>
          <form onSubmit={submit} className="flex flex-col gap-3">
            <Textarea
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              rows={4}
              placeholder="What should the agent do? (read-only)"
              aria-label="Prompt"
            />
            <Button type="submit" className="self-start" disabled={prompt.trim() === ''}>
              Start run
            </Button>
            {error && (
              <Alert variant="destructive">
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}
          </form>
        </CardContent>
      </Card>

      <section className="flex flex-col gap-2">
        <h2 className="text-sm uppercase tracking-wide text-muted-foreground">Recent runs</h2>
        {runs.length === 0 && <p className="text-muted-foreground">No runs yet.</p>}
        {runs.map((r) => (
          <Link
            key={r.id}
            to={`/runs/${r.id}`}
            className="flex items-center gap-3 rounded-lg border border-border bg-card/50 px-3 py-2 transition-colors hover:border-ring"
          >
            <span className={`h-2.5 w-2.5 rounded-full ${statusColor[r.status]}`} />
            <span className="flex-1 truncate">{r.prompt}</span>
            <span className="text-xs text-muted-foreground">{new Date(r.createdAt).toLocaleString()}</span>
          </Link>
        ))}
      </section>
    </div>
  )
}
```

Overwrite `web/src/RunView.tsx`:

```tsx
import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, streamRun } from './api.ts'
import type { Run, RunEvent } from './api.ts'
import { statusColor } from './status.ts'
import { Badge } from '@/components/ui/badge'

function summarize(e: RunEvent): string {
  if (e.kind === 'result' && typeof e.payload === 'object' && e.payload !== null) {
    const result = (e.payload as { result?: unknown }).result
    if (typeof result === 'string') return result
  }
  if (typeof e.payload === 'string') return e.payload
  const text = JSON.stringify(e.payload)
  return text.length > 600 ? `${text.slice(0, 600)}...` : text
}

/** Mount with `key={id}` so that switching runs resets the state instead of resetting it in the effect. */
export default function RunView({ id }: { id: string }) {
  const [run, setRun] = useState<Run | null>(null)
  const [events, setEvents] = useState<RunEvent[]>([])

  useEffect(() => {
    api.getRun(id).then(setRun).catch(() => setRun(null))
    return streamRun(
      id,
      (e) => setEvents((prev) => (prev.some((p) => p.seq === e.seq) ? prev : [...prev, e])),
      setRun,
    )
  }, [id])

  // The run is only fetched once and replaced on "done". Events exist only after the runner has
  // started the run, so a queued run that already has events is in fact running.
  const status = run && run.status === 'queued' && events.length > 0 ? 'running' : run?.status

  return (
    <div className="flex flex-col gap-6">
      <Link to="/runs" className="text-sm text-muted-foreground hover:text-foreground">
        ← All runs
      </Link>

      {run && status && (
        <header className="flex flex-col gap-2">
          <div className="flex items-center gap-3">
            <Badge variant="outline" className="gap-2">
              <span className={`h-2 w-2 rounded-full ${statusColor[status]}`} />
              {status}
            </Badge>
            {run.exitCode !== undefined && <span className="text-sm text-muted-foreground">exit {run.exitCode}</span>}
          </div>
          <p className="whitespace-pre-wrap rounded-lg bg-card p-3">{run.prompt}</p>
        </header>
      )}

      <ol className="flex flex-col gap-2 font-mono text-sm">
        {events.map((e) => (
          <li key={e.seq} className="rounded-lg border border-border bg-card/50 p-2">
            <Badge variant="secondary" className="mr-2">
              {e.kind}
            </Badge>
            <span className="whitespace-pre-wrap break-words text-muted-foreground">{summarize(e)}</span>
          </li>
        ))}
      </ol>

      {status === 'running' && <p className="text-sm text-amber-400">Waiting for more output...</p>}
    </div>
  )
}
```

- [ ] **Step 9: Typecheck, lint, build**

Run: `cd web && npm run lint && npm run build`
Expected: `oxlint` reports nothing, `tsc -b` and `vite build` succeed.

- [ ] **Step 10: Check it in a real browser**

Start the control plane and the Vite dev server (a throw-away setup, nothing is committed). From the repository root:

```bash
SCRATCH="$(mktemp -d)"
make build-go
export REMEDY_ADMIN_PASSWORD='ui-test-password' REMEDY_RUNNER_TOKEN='ui-runner-token-0123456789' REMEDY_MASTER_KEY="$(openssl rand -base64 32)"
REMEDY_DB="$SCRATCH/remedy.db" REMEDY_ADDR=127.0.0.1:8080 ./bin/remedy-server &
(cd web && npm run dev -- --host 127.0.0.1 --port 5173 --strictPort) &
```

With the Playwright tools: open `http://127.0.0.1:5173/`, and verify that
1. the login card renders, a wrong password shows the error alert, the right one (`ui-test-password`) opens the app with the sidebar (Remedy, Runs, Settings, Sign out);
2. `/` redirects to `/runs`, and reloading `http://127.0.0.1:5173/runs` keeps working (no 404);
3. a run can be started and its page shows the status badge and events (no runner is needed: the run stays `queued`);
4. a made-up URL such as `/nope` shows "Page not found";
5. "Sign out" returns to the login card;
6. the browser console shows no errors apart from the expected 401 of `/api/me` before login.

Stop the two background processes and run `rm -rf "$SCRATCH"` afterwards. Delete any `.playwright-mcp/` folder the browser tool left in the working tree (it is git-ignored, but there is no reason to keep it).

- [ ] **Step 11: Commit**

```bash
make check
git add web
git commit -m "feat(web): add the router, shadcn/ui and the sidebar layout" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 8: The Settings page

**Files:**
- Modify: `web/src/api.ts`, `web/src/App.tsx`
- Create: `web/src/components/ConfirmButton.tsx`, `web/src/GitHubConnectionCard.tsx`, `web/src/ReposCard.tsx`, `web/src/SettingsPage.tsx`

**Interfaces:**
- Consumes: the API of Task 6 and the shadcn components and router of Task 7.
- Produces: the route `/settings` and a sidebar entry that already exists. `api` gains `getConnection`, `putConnection`, `checkConnection`, `deleteConnection`, `listRepos`, `addRepo`, `setRepoEnabled`, `deleteRepo`, plus the types `GitHubConnection` and `Repo`.

- [ ] **Step 1: Extend the API client**

Overwrite `web/src/api.ts`:

```ts
export type RunStatus = 'queued' | 'running' | 'succeeded' | 'failed'

export interface Run {
  id: string
  provider: string
  prompt: string
  status: RunStatus
  exitCode?: number
  result: string
  sessionId: string
  costUsd: number
  createdAt: string
  startedAt?: string
  finishedAt?: string
}

export interface RunEvent {
  seq: number
  kind: string
  payload: unknown
  createdAt: string
}

export interface GitHubConnection {
  connected: boolean
  login?: string
  /** Only the last four characters of the token, never the token itself. */
  tokenHint?: string
  status?: 'ok' | 'error' | 'undecryptable'
  statusDetail?: string
  checkedAt?: string
}

export interface Repo {
  id: number
  fullName: string
  defaultBranch: string
  enabled: boolean
  lastPolledAt?: string
  lastError: string
  createdAt: string
}

export class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: { 'Content-Type': 'application/json', 'X-Remedy-CSRF': '1' },
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'same-origin',
  })
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string }
    throw new ApiError(res.status, data.error ?? res.statusText)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export const api = {
  login: (password: string) => request<void>('POST', '/api/login', { password }),
  logout: () => request<void>('POST', '/api/logout'),
  me: () => request<{ user: string }>('GET', '/api/me'),
  listRuns: () => request<Run[]>('GET', '/api/runs'),
  createRun: (prompt: string) => request<Run>('POST', '/api/runs', { prompt }),
  getRun: (id: string) => request<Run>('GET', `/api/runs/${id}`),

  getConnection: () => request<GitHubConnection>('GET', '/api/github/connection'),
  putConnection: (token: string) => request<GitHubConnection>('PUT', '/api/github/connection', { token }),
  checkConnection: () => request<GitHubConnection>('POST', '/api/github/connection/check'),
  deleteConnection: () => request<void>('DELETE', '/api/github/connection'),
  listRepos: () => request<Repo[]>('GET', '/api/repos'),
  addRepo: (fullName: string) => request<Repo>('POST', '/api/repos', { fullName }),
  setRepoEnabled: (id: number, enabled: boolean) => request<void>('PATCH', `/api/repos/${id}`, { enabled }),
  deleteRepo: (id: number) => request<void>('DELETE', `/api/repos/${id}`),
}

/** Opens the live stream of a run. The browser reconnects with Last-Event-ID on its own. */
export function streamRun(
  id: string,
  onEvent: (e: RunEvent) => void,
  onDone: (r: Run) => void,
): () => void {
  const es = new EventSource(`/api/runs/${id}/events`)
  es.addEventListener('run_event', (m) => onEvent(JSON.parse((m as MessageEvent<string>).data) as RunEvent))
  es.addEventListener('done', (m) => {
    onDone(JSON.parse((m as MessageEvent<string>).data) as Run)
    es.close()
  })
  return () => es.close()
}
```

- [ ] **Step 2: Write the confirm button**

Create `web/src/components/ConfirmButton.tsx`:

```tsx
import { useState } from 'react'
import { Button } from '@/components/ui/button'

interface Props {
  label: string
  confirmLabel: string
  onConfirm: () => void
  disabled?: boolean
}

/** A destructive action that needs a second click. It disarms itself when it loses focus. */
export default function ConfirmButton({ label, confirmLabel, onConfirm, disabled }: Props) {
  const [armed, setArmed] = useState(false)

  return (
    <Button
      type="button"
      size="sm"
      variant={armed ? 'destructive' : 'outline'}
      disabled={disabled}
      onBlur={() => setArmed(false)}
      onClick={() => {
        if (!armed) {
          setArmed(true)
          return
        }
        setArmed(false)
        onConfirm()
      }}
    >
      {armed ? confirmLabel : label}
    </Button>
  )
}
```

- [ ] **Step 3: Write the connection card**

Create `web/src/GitHubConnectionCard.tsx`:

```tsx
import { useState } from 'react'
import type { FormEvent } from 'react'
import { api, ApiError } from './api.ts'
import type { GitHubConnection } from './api.ts'
import ConfirmButton from '@/components/ConfirmButton'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

interface Props {
  connection: GitHubConnection
  onChange: (c: GitHubConnection) => void
}

const statusLabel = { ok: 'Connected', error: 'Error', undecryptable: 'Cannot decrypt' } as const

export default function GitHubConnectionCard({ connection, onChange }: Props) {
  const [token, setToken] = useState('')
  const [editing, setEditing] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const showForm = !connection.connected || editing

  async function run(action: () => Promise<GitHubConnection>) {
    setBusy(true)
    setError('')
    try {
      onChange(await action())
      setToken('')
      setEditing(false)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Request failed')
    } finally {
      setBusy(false)
    }
  }

  function save(e: FormEvent) {
    e.preventDefault()
    void run(() => api.putConnection(token))
  }

  async function disconnect() {
    setBusy(true)
    setError('')
    try {
      await api.deleteConnection()
      onChange({ connected: false })
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Request failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>GitHub connection</CardTitle>
        <CardDescription>
          Remedy reads pull requests and check runs. The token is stored encrypted and is never shown again.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {connection.connected && connection.status && (
          <div className="flex flex-col gap-2">
            <div className="flex flex-wrap items-center gap-3">
              <Badge variant={connection.status === 'ok' ? 'secondary' : 'destructive'}>
                {statusLabel[connection.status]}
              </Badge>
              <span>
                Account <span className="font-medium">@{connection.login}</span>
              </span>
              <span className="text-muted-foreground">Token {connection.tokenHint}</span>
              {connection.checkedAt && (
                <span className="text-sm text-muted-foreground">
                  checked {new Date(connection.checkedAt).toLocaleString()}
                </span>
              )}
            </div>
            {connection.statusDetail && (
              <Alert variant="destructive">
                <AlertDescription>{connection.statusDetail}</AlertDescription>
              </Alert>
            )}
          </div>
        )}

        {showForm && (
          <form onSubmit={save} className="flex flex-col gap-3">
            <Input
              type="password"
              autoComplete="off"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder="github_pat_..."
              aria-label="GitHub token"
            />
            <p className="text-sm text-muted-foreground">
              Use a fine-grained personal access token with read-only access to the repositories: Metadata,
              Contents, Pull requests, Actions and Checks.
            </p>
            <div className="flex gap-2">
              <Button type="submit" disabled={busy || token.trim() === ''}>
                {connection.connected ? 'Replace token' : 'Connect'}
              </Button>
              {editing && (
                <Button type="button" variant="ghost" onClick={() => setEditing(false)}>
                  Cancel
                </Button>
              )}
            </div>
          </form>
        )}

        {connection.connected && !editing && (
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" size="sm" disabled={busy} onClick={() => void run(api.checkConnection)}>
              Check connection
            </Button>
            <Button variant="outline" size="sm" disabled={busy} onClick={() => setEditing(true)}>
              Replace token
            </Button>
            <ConfirmButton
              label="Disconnect"
              confirmLabel="Confirm: also removes the repositories"
              disabled={busy}
              onConfirm={() => void disconnect()}
            />
          </div>
        )}

        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
      </CardContent>
    </Card>
  )
}
```

- [ ] **Step 4: Write the repos card**

Create `web/src/ReposCard.tsx`:

```tsx
import { useCallback, useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { api, ApiError } from './api.ts'
import type { Repo } from './api.ts'
import ConfirmButton from '@/components/ConfirmButton'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'

export default function ReposCard({ connected }: { connected: boolean }) {
  const [repos, setRepos] = useState<Repo[]>([])
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const reload = useCallback(() => {
    api.listRepos().then(setRepos).catch((e: unknown) => setError(e instanceof ApiError ? e.message : String(e)))
  }, [])

  useEffect(() => {
    if (connected) reload()
  }, [connected, reload])

  async function act(action: () => Promise<unknown>) {
    setBusy(true)
    setError('')
    try {
      await action()
      reload()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Request failed')
    } finally {
      setBusy(false)
    }
  }

  function add(e: FormEvent) {
    e.preventDefault()
    void act(async () => {
      await api.addRepo(name.trim())
      setName('')
    })
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Repositories</CardTitle>
        <CardDescription>Remedy watches the enabled repositories for failed checks.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {!connected ? (
          <p className="text-muted-foreground">Connect GitHub first.</p>
        ) : (
          <>
            <form onSubmit={add} className="flex gap-2">
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="owner/name"
                aria-label="Repository"
              />
              <Button type="submit" disabled={busy || name.trim() === ''}>
                Add
              </Button>
            </form>

            {repos.length === 0 ? (
              <p className="text-muted-foreground">No repositories yet.</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Repository</TableHead>
                    <TableHead>Default branch</TableHead>
                    <TableHead>Last poll</TableHead>
                    <TableHead>Enabled</TableHead>
                    <TableHead />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {repos.map((repo) => (
                    <TableRow key={repo.id}>
                      <TableCell>
                        <div className="font-medium">{repo.fullName}</div>
                        {repo.lastError && <div className="text-xs text-destructive">{repo.lastError}</div>}
                      </TableCell>
                      <TableCell>{repo.defaultBranch}</TableCell>
                      <TableCell className="text-muted-foreground">
                        {repo.lastPolledAt ? new Date(repo.lastPolledAt).toLocaleString() : 'never'}
                      </TableCell>
                      <TableCell>
                        <Switch
                          checked={repo.enabled}
                          disabled={busy}
                          aria-label={`Watch ${repo.fullName}`}
                          onCheckedChange={(enabled) => void act(() => api.setRepoEnabled(repo.id, enabled))}
                        />
                      </TableCell>
                      <TableCell className="text-right">
                        <ConfirmButton
                          label="Remove"
                          confirmLabel="Confirm remove"
                          disabled={busy}
                          onConfirm={() => void act(() => api.deleteRepo(repo.id))}
                        />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </>
        )}

        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
      </CardContent>
    </Card>
  )
}
```

- [ ] **Step 5: Write the page and add the route**

Create `web/src/SettingsPage.tsx`:

```tsx
import { useEffect, useState } from 'react'
import { api, ApiError } from './api.ts'
import type { GitHubConnection } from './api.ts'
import GitHubConnectionCard from './GitHubConnectionCard.tsx'
import ReposCard from './ReposCard.tsx'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'

export default function SettingsPage() {
  const [connection, setConnection] = useState<GitHubConnection | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    api
      .getConnection()
      .then(setConnection)
      .catch((e: unknown) => setError(e instanceof ApiError ? e.message : 'Could not load the settings'))
  }, [])

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {connection ? (
        <>
          <GitHubConnectionCard connection={connection} onChange={setConnection} />
          <ReposCard connected={connection.connected} />
        </>
      ) : (
        !error && <Skeleton className="h-48 w-full" />
      )}
    </div>
  )
}
```

In `web/src/App.tsx`, add `import SettingsPage from './SettingsPage.tsx'` after the `RunView` import and add this route before the `*` route:

```tsx
        <Route path="settings" element={<SettingsPage />} />
```

- [ ] **Step 6: Typecheck, lint, build**

Run: `cd web && npm run lint && npm run build`
Expected: no lint findings, `tsc -b` and `vite build` succeed.

- [ ] **Step 7: Check it in a real browser against a fake GitHub**

Write a throw-away fake of the GitHub API to `fake-github.mjs` in a scratch directory (`SCRATCH="$(mktemp -d)"`; nothing of this is committed):

```js
import http from 'node:http'

const GOOD = 'github_pat_FAKE0123456789abcdefghijklmnopqrstuvwxyz'
const repos = { 'octo/hello': { full_name: 'Octo/Hello', default_branch: 'main', private: false } }

http
  .createServer((req, res) => {
    const send = (code, body) => {
      res.writeHead(code, { 'content-type': 'application/json' })
      res.end(JSON.stringify(body))
    }
    if (req.method !== 'GET') return send(405, { message: 'this fake is read-only' })
    if ((req.headers.authorization ?? '') !== `Bearer ${GOOD}`) return send(401, { message: 'Bad credentials' })
    if (req.url === '/user') return send(200, { login: 'octo-test' })
    const m = req.url.match(/^\/repos\/([^/]+\/[^/]+)$/)
    if (m && repos[m[1].toLowerCase()]) return send(200, repos[m[1].toLowerCase()])
    send(404, { message: 'Not Found' })
  })
  .listen(9090, '127.0.0.1')
```

Run it and the stack (from the repository root):

```bash
node "$SCRATCH/fake-github.mjs" &
make build-go
export REMEDY_ADMIN_PASSWORD='ui-test-password' REMEDY_RUNNER_TOKEN='ui-runner-token-0123456789' REMEDY_MASTER_KEY="$(openssl rand -base64 32)"
REMEDY_GITHUB_API_URL=http://127.0.0.1:9090 REMEDY_DB="$SCRATCH/remedy.db" REMEDY_ADDR=127.0.0.1:8080 ./bin/remedy-server &
(cd web && npm run dev -- --host 127.0.0.1 --port 5173 --strictPort) &
```

With the Playwright tools, sign in at `http://127.0.0.1:5173/` and open **Settings**. Verify that:
1. before connecting, the card shows the token field, "Connect" and the permissions hint, and the Repositories card says "Connect GitHub first.";
2. a wrong token (for example 30 characters of `x`) shows "GitHub rejected the token" and nothing is stored;
3. the token `github_pat_FAKE0123456789abcdefghijklmnopqrstuvwxyz` connects: the card shows "Connected", `@octo-test` and the hint `…wxyz`; **the full token appears nowhere on the page** (check the page text and the network responses for the string `FAKE0123456789`);
4. adding `octo/hello` lists `Octo/Hello` with default branch `main` and "never" as last poll; adding it again as `OCTO/hello` shows "already added"; adding `octo/missing` shows "repository not found, or the token has no access to it"; adding `nope` shows "use the form owner/name";
5. the switch disables and enables the repo, and "Remove" needs a second click ("Confirm remove");
6. "Check connection" keeps the status "Connected", "Replace token" opens the form, and "Disconnect" needs a second click, after which the repo list is gone and the page shows the connect form again;
7. reloading `http://127.0.0.1:5173/settings` shows the same state; the console has no errors.

Stop `node`, the server and Vite afterwards, run `rm -rf "$SCRATCH"`, and delete any `.playwright-mcp/` folder the browser tool left behind.

- [ ] **Step 8: Commit**

```bash
make check
git add web
git commit -m "feat(web): add the settings page for the GitHub connection and repos" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Documentation

**Files:**
- Modify: `README.md`, `CLAUDE.md`, `docs/specs/2026-10-02-phase-1-detect-and-diagnose-design.md`

**Interfaces:**
- Consumes: everything above.
- Produces: documentation that matches the code.

- [ ] **Step 1: Update the README**

In `README.md`, in the "Quick start (local)" block, add the master key to the exports so the block reads:

```sh
make build       # builds the UI, then both binaries (the server embeds the UI)

export REMEDY_ADMIN_PASSWORD='choose-a-long-password'   # min. 12 characters
export REMEDY_RUNNER_TOKEN="$(openssl rand -hex 24)"    # min. 24 characters
export REMEDY_MASTER_KEY="$(openssl rand -base64 32)"   # seals the GitHub token in the database

./bin/remedy-server &    # control plane on :8080, database in ./remedy.db
./bin/remedy-runner      # needs the same REMEDY_RUNNER_TOKEN
```

Directly after that block, add this paragraph:

```markdown
`REMEDY_MASTER_KEY` is required: Remedy refuses to start without it. Keep it outside the database and its
backups; whoever has both can read the stored GitHub token. If you lose or change the key, enter the token
again under **Settings**. Under **Settings** you also connect GitHub (a fine-grained, read-only personal access
token) and add repositories. `REMEDY_GITHUB_API_URL` (default `https://api.github.com`) exists for tests.
```

In the "Container" block, change the `docker run` command to:

```sh
docker run -p 8080:8080 -v remedy-data:/data \
  -e REMEDY_ADMIN_PASSWORD -e REMEDY_RUNNER_TOKEN -e REMEDY_MASTER_KEY remedy-server
```

- [ ] **Step 2: Update `CLAUDE.md`**

In `CLAUDE.md`, in the list under "Trust boundaries that span several files and are easy to break", add:

```markdown
- **The GitHub token is write-only.** It is sealed with AES-256-GCM (`internal/secret`, key from `REMEDY_MASTER_KEY`, the row ID bound in as additional data), shown only as `…` plus its last four characters, and has a `***` text form (`secret.Value`) so it cannot reach logs or errors. The `github` client is read-only: every exported method is a `Get*` or `List*`, enforced by a test. Do not add a write method without a decision recorded in the spec.
```

In the "Commands" block, add the single-test example for the new packages after the existing single-test line:

```markdown
go test ./internal/secret -run TestSealOpenRoundTrip -v   # another single Go test
```

- [ ] **Step 3: Mark the spec as accepted**

In `docs/specs/2026-10-02-phase-1-detect-and-diagnose-design.md`, replace the status line

```
Status: approved in the planning session of 2026-10-02, pending written-spec review.
```

with

```
Status: accepted by the maintainer on 2026-10-02. Implementation plans: [`phase-1a`](../plans/phase-1a-github-foundation.md) (steps 0 to 4); the plans for the remaining steps follow.
```

- [ ] **Step 4: Verify and commit**

Run: `make check`
Expected: exit 0.

```bash
git add README.md CLAUDE.md docs/specs
git commit -m "docs: document the master key, the GitHub token handling and the settings page" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

## Exit criteria for plan 1a

- `make check` and `go test ./... -race -count=1` pass; CI is green on `main`.
- With a real fine-grained, read-only token, the maintainer connects GitHub under **Settings**, adds the Remedy repository and sees it listed; the token appears in no API response and no log.
- `docs/research/spike-structured-output.md` records the decision for the responder.
- The browser checks of Tasks 7 and 8 pass.

## Follow-up plans

Each is written when the previous one has landed, because it depends on what that one showed:

1. **1b: signals and incidents** (spec steps 5 and 6): `poller`, incident state machine, `activity`, run extension (role, incident link, output, timeout), the reaper, incident list.
2. **1c: the responder** (spec step 7): context package, redaction, snapshot endpoint, unpacking in the runner, schema validation, auto-start rules, diagnosis card. Depends on the spike.
3. **1d: timeline and the real run** (spec steps 8 and 9): timeline with live updates, and a real run against the Remedy repository.

## Self-review notes

- **Spec coverage (steps 0 to 4):** spike (Task 1), `secret` and master key at startup (Tasks 2 and 3), migration and store for the connection and repos (Task 4), read-only client (Task 5), connection and repos API incl. the `undecryptable` state (Task 6), router, layout, shadcn/ui (Task 7), settings page (Task 8). Deviations from the spec, deliberate: migration 002 only creates `github_connections` and `repos` (the incident, activity and run changes come with their own migration in plan 1b); deleting the connection also deletes its repos (foreign key cascade), and the UI says so.
- **Types used across tasks:** `secret.Key`/`secret.Value` (Task 2) are used by `config` (3), `github` (5) and `server` (6). `store.Connection`/`store.Repo` and their methods (4) are used by `server` (6). `server.GitHub` (6) is implemented by `*github.Client` (5) and by `fakeGitHub` in tests. The JSON field names of Task 6 (`tokenHint`, `fullName`, `defaultBranch`, `lastPolledAt`, `lastError`) match the TypeScript types of Task 8.
- **Verified when written (2026-10-02):**
  - Every Go block of Tasks 2 to 6 was extracted into a scratch copy of the repository and the prose edits (`Deps`, routes, `main.go`) were applied. `gofmt` and `go vet` are clean and `go test ./... -race` passes for all packages. A mutation (storing the token unencrypted) made three tests fail, so the sealing tests can fail.
  - The web steps of Tasks 7 and 8 were run in order on a fresh copy of `web/`: `npm install react-router`, the `tsconfig` and `index.html` edits, `shadcn init` and `shadcn add`, then the plan's files. `oxlint` reports nothing and `tsc -b && vite build` succeed.
  - The browser checks of Tasks 7 and 8 were executed with Playwright against the plan's Go code, the plan's web code and the plan's fake GitHub: login and error alert, redirect and deep-link reload, 404 page, wrong and right token, add, duplicate, unknown and invalid repository, enable switch, two-click remove, check connection, replace token, two-click disconnect. The token appears in no API response, no database file (including the WAL file) and no server log.
  - Found and fixed by this verification: `GetRepo` accepted `..` as an owner (now rejected, with tests), the extraction format of two blocks, hard-coded `/tmp` paths, and a local-time `checkedAt` in the PUT response.
  - Tool versions used: `react-router` 8.4.0, `shadcn` 4.21.1, Tailwind 4.3.3, TypeScript 6.0 (the repository's current one). The commands use `shadcn@latest`; if a later CLI behaves differently, pin the version above.
  - **Not verified:** Task 1 (it needs the maintainer's real `claude` login and is the first thing to run), Task 9's documentation edits, and the commit steps.
