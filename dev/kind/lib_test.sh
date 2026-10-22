#!/bin/sh
# Tests of the guard of lib.sh (check_out_dir, ensure_out_dir) and of the cleanup line of down.sh. They need no
# cluster and touch only throwaway directories under $TMPDIR. Run: sh dev/kind/lib_test.sh
set -u

T=$(mktemp -d "${TMPDIR:-/tmp}/remedy-libtest.XXXXXX")
trap 'rm -rf "$T"' EXIT
# Sourcing lib.sh only sets variables; point it at a throwaway directory so that it can never name the real one.
REMEDY_KIND_DIR=$T/unused
. "$(dirname "$0")/lib.sh"

failed=0
pass() { echo "ok   $1"; }
fail() { echo "FAIL $1"; failed=1; }

# refuses <name> <dir>: check_out_dir must exit 1 with "refusing" on stderr.
refuses() {
  name=$1
  dir=$2
  err=$( (OUT=$dir; check_out_dir) 2>&1 > /dev/null)
  status=$?
  if [ "$status" -eq 1 ] && echo "$err" | grep -q 'refusing'; then pass "$name"; else fail "$name (status $status: $err)"; fi
}

# accepts <name> <dir>
accepts() {
  name=$1
  dir=$2
  if err=$( (OUT=$dir; check_out_dir) 2>&1); then pass "$name"; else fail "$name ($err)"; fi
}

if ! type check_out_dir > /dev/null 2>&1 || ! type ensure_out_dir > /dev/null 2>&1; then
  fail "lib.sh defines check_out_dir"
  exit 1
fi

refuses "empty path" ""
refuses "root" "/"
refuses "root with slashes" "//"
refuses "home" "$HOME"
refuses "home with a trailing slash" "$HOME/"
refuses "home through a dot" "$HOME/."

mkdir "$T/work"
touch "$T/work/decoy"
refuses "non-empty directory without the marker" "$T/work"
if [ -f "$T/work/decoy" ] && [ "$(find "$T/work" -mindepth 1 | wc -l)" -eq 1 ]; then pass "the refused directory is untouched"; else fail "the refused directory is untouched"; fi

touch "$T/afile"
refuses "a file" "$T/afile"
mkdir "$T/partial"
touch "$T/partial/env.sh" "$T/partial/decoy"
refuses "env.sh without ca.crt" "$T/partial"

accepts "missing directory" "$T/missing"
if [ ! -e "$T/missing" ]; then pass "check_out_dir creates nothing"; else fail "check_out_dir creates nothing"; fi
mkdir "$T/empty"
accepts "empty directory" "$T/empty"
mkdir "$T/marked"
touch "$T/marked/.remedy-kind-dir" "$T/marked/decoy"
accepts "directory with the marker" "$T/marked"
mkdir "$T/old"
touch "$T/old/env.sh" "$T/old/ca.crt" "$T/old/read.token"
accepts "directory made before the marker (env.sh and ca.crt)" "$T/old"
mkdir -p "$T/onlylogin/claude"
accepts "directory that holds only claude (what an old down.sh left)" "$T/onlylogin"

# ensure_out_dir: the marker, mode 700, and the guard before any chmod.
(OUT=$T/fresh; ensure_out_dir)
if [ -f "$T/fresh/.remedy-kind-dir" ]; then pass "ensure_out_dir creates the marker"; else fail "ensure_out_dir creates the marker"; fi
mode=$(stat -c %a "$T/fresh" 2> /dev/null || stat -f %Lp "$T/fresh")
[ "$mode" = 700 ] && pass "ensure_out_dir sets mode 700" || fail "ensure_out_dir sets mode 700 (got $mode)"
mode=$(stat -c %a "$T/fresh/.remedy-kind-dir" 2> /dev/null || stat -f %Lp "$T/fresh/.remedy-kind-dir")
[ "$mode" = 600 ] && pass "the marker has mode 600" || fail "the marker has mode 600 (got $mode)"
(OUT=$T/fresh; ensure_out_dir) && pass "ensure_out_dir twice" || fail "ensure_out_dir twice"
chmod 755 "$T/work"
(OUT=$T/work; ensure_out_dir) > /dev/null 2>&1
mode=$(stat -c %a "$T/work" 2> /dev/null || stat -f %Lp "$T/work")
if [ "$mode" = 755 ] && [ ! -e "$T/work/.remedy-kind-dir" ]; then pass "ensure_out_dir leaves a refused directory alone"; else fail "ensure_out_dir leaves a refused directory alone (mode $mode)"; fi

# The cleanup line of down.sh keeps claude and the marker and removes the rest.
mkdir -p "$T/clean/claude" "$T/clean/sub" "$T/outside"
touch "$T/clean/claude/login" "$T/clean/.remedy-kind-dir" "$T/clean/env.sh" "$T/clean/read.token" "$T/clean/sub/x" "$T/outside/decoy"
(OUT=$T/clean; cleanup_out_dir)
left=$(cd "$T/clean" && find . -mindepth 1 | sort | tr '\n' ' ')
if [ "$left" = "./.remedy-kind-dir ./claude ./claude/login " ] && [ -f "$T/outside/decoy" ]; then pass "cleanup keeps claude and the marker only"; else fail "cleanup keeps claude and the marker only (left: $left)"; fi
(OUT=$T/clean; check_out_dir) && pass "a second down.sh passes the guard" || fail "a second down.sh passes the guard"

# A symlink as the directory: the guard looks at the target, not at the link.
mkdir "$T/pop"
touch "$T/pop/decoy"
ln -s "$T/pop" "$T/linkpop"
refuses "symlink to a populated directory without the marker" "$T/linkpop"
(OUT=$T/linkpop; ensure_out_dir) > /dev/null 2>&1
mode=$(stat -c %a "$T/pop" 2> /dev/null || stat -f %Lp "$T/pop")
if [ ! -e "$T/pop/.remedy-kind-dir" ] && [ "$mode" != 700 ]; then pass "ensure_out_dir leaves the target of a refused symlink alone"; else fail "ensure_out_dir leaves the target of a refused symlink alone (mode $mode)"; fi
mkdir "$T/realmarked"
touch "$T/realmarked/.remedy-kind-dir" "$T/realmarked/junk"
ln -s "$T/realmarked" "$T/linkmarked"
(OUT=$T/linkmarked; cleanup_out_dir)
if [ ! -e "$T/realmarked/junk" ] && [ -e "$T/realmarked/.remedy-kind-dir" ]; then pass "cleanup follows a symlink to a marked directory"; else fail "cleanup follows a symlink to a marked directory"; fi

# A relative path that starts with a dash: refused when populated, never read as an option.
mkdir "$T/rel"
mkdir "$T/rel/-dash"
touch "$T/rel/-dash/decoy"
err=$(cd "$T/rel" && OUT=-dash && check_out_dir 2>&1 > /dev/null)
status=$?
if [ "$status" -eq 1 ] && echo "$err" | grep -q 'refusing'; then pass "relative path starting with a dash"; else fail "relative path starting with a dash (status $status: $err)"; fi

# The home directory, in the cases the no-marker rule would not catch: HOME is empty, holds a marker, or is reached
# through a symlink.
mkdir "$T/fakehome"
ln -s "$T/fakehome" "$T/homelink"
for form in "$T/fakehome" "$T/fakehome/" "$T/homelink" "$T/homelink/"; do
  err=$( (HOME=$T/fakehome; OUT=$form; check_out_dir) 2>&1 > /dev/null)
  status=$?
  if [ "$status" -eq 1 ] && echo "$err" | grep -q 'home directory'; then pass "empty home as $form"; else fail "empty home as $form (status $status: $err)"; fi
done
touch "$T/fakehome/.remedy-kind-dir"
for form in "$T/fakehome" "$T/homelink/"; do
  err=$( (HOME=$T/fakehome; OUT=$form; check_out_dir) 2>&1 > /dev/null)
  status=$?
  if [ "$status" -eq 1 ] && echo "$err" | grep -q 'home directory'; then pass "marked home as $form"; else fail "marked home as $form (status $status: $err)"; fi
done

# is_fresh: only a deployed release is not fresh. helm is replaced by a stub on PATH that answers what the test says.
mkdir "$T/bin"
cat > "$T/bin/helm" <<'STUB'
#!/bin/sh
[ -n "${FAKE_HELM_JSON:-}" ] || exit 1
printf '%s\n' "$FAKE_HELM_JSON"
STUB
chmod +x "$T/bin/helm"
fresh_is() {
  name=$1
  want=$2
  json=$3
  if (PATH=$T/bin:$PATH; export PATH; FAKE_HELM_JSON=$json; export FAKE_HELM_JSON; is_fresh); then got=yes; else got=no; fi
  if [ "$got" = "$want" ]; then pass "is_fresh: $name"; else fail "is_fresh: $name (got $got)"; fi
}
fresh_is "no release (helm status fails)" yes ""
fresh_is "deployed" no '{"info":{"status":"deployed"}}'
fresh_is "failed" yes '{"info":{"status":"failed"}}'
fresh_is "pending-install" yes '{"info":{"status":"pending-install"}}'
fresh_is "unreadable answer" yes 'not json'

# The database password of dummy.env: made with the file, added to a file from before the database existed, never changed.
mkdir "$T/env"
(DUMMY_ENV=$T/env/new.env; write_dummy_env)
for key in REMEDY_URL REMEDY_ADMIN_PASSWORD REMEDY_RUNNER_TOKEN REMEDY_MASTER_KEY REMEDY_POSTGRES_PASSWORD; do
  if grep -q "^$key=." "$T/env/new.env"; then pass "a new dummy.env has $key"; else fail "a new dummy.env has $key"; fi
done
pw=$(sed -n 's/^REMEDY_POSTGRES_PASSWORD=//p' "$T/env/new.env")
if printf '%s' "$pw" | grep -Eq '^[0-9a-f]{32}$'; then pass "the database password is 32 hex characters (it can sit in a URL)"; else fail "the database password is 32 hex characters (got $pw)"; fi
mode=$(stat -c %a "$T/env/new.env" 2> /dev/null || stat -f %Lp "$T/env/new.env")
[ "$mode" = 600 ] && pass "a new dummy.env has mode 600" || fail "a new dummy.env has mode 600 (got $mode)"
before=$(cat "$T/env/new.env")
(DUMMY_ENV=$T/env/new.env; write_dummy_env)
[ "$(cat "$T/env/new.env")" = "$before" ] && pass "write_dummy_env keeps a complete dummy.env as it is" || fail "write_dummy_env keeps a complete dummy.env as it is"

printf 'REMEDY_URL=http://127.0.0.1:18080\nREMEDY_ADMIN_PASSWORD=aaa\nREMEDY_RUNNER_TOKEN=bbb\nREMEDY_MASTER_KEY=ccc\n' > "$T/env/old.env"
chmod 600 "$T/env/old.env"
(DUMMY_ENV=$T/env/old.env; write_dummy_env)
if [ "$(head -n 4 "$T/env/old.env")" = "$(printf 'REMEDY_URL=http://127.0.0.1:18080\nREMEDY_ADMIN_PASSWORD=aaa\nREMEDY_RUNNER_TOKEN=bbb\nREMEDY_MASTER_KEY=ccc')" ] &&
   [ "$(wc -l < "$T/env/old.env" | tr -d ' ')" = 5 ] && grep -q '^REMEDY_POSTGRES_PASSWORD=[0-9a-f]\{32\}$' "$T/env/old.env"; then
  pass "a dummy.env from before the database gets a password and keeps its other lines"
else
  fail "a dummy.env from before the database gets a password and keeps its other lines"
fi
mode=$(stat -c %a "$T/env/old.env" 2> /dev/null || stat -f %Lp "$T/env/old.env")
[ "$mode" = 600 ] && pass "the old dummy.env keeps mode 600" || fail "the old dummy.env keeps mode 600 (got $mode)"
before=$(cat "$T/env/old.env")
(DUMMY_ENV=$T/env/old.env; ensure_postgres_password)
[ "$(cat "$T/env/old.env")" = "$before" ] && pass "an existing database password is never replaced" || fail "an existing database password is never replaced"
printf 'REMEDY_URL=u\nREMEDY_POSTGRES_PASSWORD=\n' > "$T/env/empty.env"
(DUMMY_ENV=$T/env/empty.env; ensure_postgres_password)
if [ "$(. "$T/env/empty.env"; printf '%s' "$REMEDY_POSTGRES_PASSWORD" | wc -c | tr -d ' ')" = 32 ]; then pass "an empty database password is replaced (the later line wins)"; else fail "an empty database password is replaced"; fi
(DUMMY_ENV=$T/env/missing.env; ensure_postgres_password) > /dev/null 2>&1 && fail "ensure_postgres_password refuses a missing file" || pass "ensure_postgres_password refuses a missing file"

# The connection URL: from the password, the service of postgres.yaml and the release namespace; no password, no URL.
got=$(REMEDY_POSTGRES_PASSWORD=abc123 postgres_url)
[ "$got" = "postgres://remedy:abc123@remedy-postgres.remedy-system.svc:5432/remedy?sslmode=disable" ] && pass "postgres_url" || fail "postgres_url (got $got)"
(unset REMEDY_POSTGRES_PASSWORD; postgres_url) > /dev/null 2>&1 && fail "postgres_url without a password fails" || pass "postgres_url without a password fails"

# No password is committed: the manifest reads it from the Secret and holds no Secret, and the chart values hold no URL.
if grep -q 'POSTGRES_PASSWORD' "$KIND_DIR/postgres.yaml" && grep -A2 'name: POSTGRES_PASSWORD' "$KIND_DIR/postgres.yaml" | grep -q secretKeyRef; then pass "postgres.yaml reads the password from a Secret"; else fail "postgres.yaml reads the password from a Secret"; fi
if grep -Eq '^kind: Secret|stringData|^[[:space:]]+data:|password:[[:space:]]+[^ #{]' "$KIND_DIR/postgres.yaml"; then fail "postgres.yaml holds no Secret and no password"; else pass "postgres.yaml holds no Secret and no password"; fi
if grep -Eq 'postgres(ql)?://' "$KIND_DIR/dummy-values.yaml"; then fail "dummy-values.yaml holds no connection URL"; else pass "dummy-values.yaml holds no connection URL"; fi
if grep -q 'existingSecret: remedy-postgres' "$KIND_DIR/dummy-values.yaml"; then pass "dummy-values.yaml points the chart at the Secret remedy-postgres"; else fail "dummy-values.yaml points the chart at the Secret remedy-postgres"; fi

[ "$failed" -eq 0 ] && echo "all passed"
exit "$failed"
