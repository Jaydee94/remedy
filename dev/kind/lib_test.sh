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

[ "$failed" -eq 0 ] && echo "all passed"
exit "$failed"
