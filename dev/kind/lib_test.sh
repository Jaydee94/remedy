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

[ "$failed" -eq 0 ] && echo "all passed"
exit "$failed"
