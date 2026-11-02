#!/bin/sh
# Tests of check-no-lab-values.sh. Run: sh scripts/check-no-lab-values_test.sh
# Each case builds a scratch git repository, copies the script and a deny file into its scripts/ directory and runs it there.
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
SCRIPT=$HERE/check-no-lab-values.sh
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
fails=0

pass() { echo "ok    $1"; }
fail() { echo "FAIL  $1"; fails=$((fails + 1)); }

# mkrepo <name>: an empty repository with the script and the standard deny file; prints its path.
mkrepo() {
  r=$WORK/$1
  mkdir -p "$r/scripts"
  git -C "$r" init -q
  cp "$SCRIPT" "$r/scripts/check-no-lab-values.sh" 2> /dev/null || : > "$r/scripts/check-no-lab-values.sh"
  printf '%s\n' 'ugreen' 'paperless' > "$r/scripts/lab-values.deny"
  printf '%s\n' "$r"
}

# run <repo>: runs the check inside the repository; sets rc and out.
run() {
  out=$(cd "$1" && sh scripts/check-no-lab-values.sh 2>&1)
  rc=$?
}

# want <name> <rc>: the last run must have exited with that code.
want() { if [ "$rc" -eq "$2" ]; then pass "$1"; else fail "$1 (exit $rc, wanted $2; output: $out)"; fi; }
# has <name> <text>: the last output must contain the text.
has() { case $out in *"$2"*) pass "$1" ;; *) fail "$1 (output: $out)" ;; esac; }

# (a) a tracked product file with the word, any case: fails and names file and line.
r=$(mkrepo a); mkdir -p "$r/web"; printf 'Ugreen-NAS\n' > "$r/web/x.tsx"; git -C "$r" add -A
run "$r"; want "a tracked file with the word fails" 1; has "the hit names file and line" "web/x.tsx:1:"; has "the closing line names the deny file" "scripts/lab-values.deny"

# (b) the excluded places hold the word without a hit.
r=$(mkrepo b)
for f in docs/specs/a.md docs/research/a.md docs/plans/a.md docs/runbook/home-server-drill.md CHANGELOG.md scripts/spike/a.sh; do
  mkdir -p "$r/$(dirname "$f")"; printf 'ugreen paperless\n' > "$r/$f"
done
git -C "$r" add -A; run "$r"; want "excluded places are not scanned" 0

# (c) a tracked binary file is skipped.
r=$(mkrepo c); printf 'a\000ugreen\n' > "$r/bin.dat"; git -C "$r" add -A
run "$r"; want "a binary file is skipped" 0

# (d) an untracked file is not scanned.
r=$(mkrepo d); printf 'ugreen\n' > "$r/new.txt"
run "$r"; want "an untracked file is not scanned" 0

# (e) a file name with a space.
r=$(mkrepo e); printf 'paperless\n' > "$r/a b.md"; git -C "$r" add -A
run "$r"; want "a file name with a space fails" 1; has "the output names it" "a b.md"

# (f) comments and blank lines in the deny file are ignored, the rest is an extended regular expression.
r=$(mkrepo f); printf '%s\n' '# a comment' '' 'a.c' > "$r/scripts/lab-values.deny"; printf 'abc\n' > "$r/x.txt"; git -C "$r" add -A
run "$r"; want "a pattern from the deny file matches" 1
r=$(mkrepo f2); printf '%s\n' '# ugreen is only in a comment' '' 'a.c' > "$r/scripts/lab-values.deny"; printf 'ugreen\n' > "$r/x.txt"; git -C "$r" add -A
run "$r"; want "a comment line is no pattern" 0

# (g) not a git work tree: an error, never a pass.
r=$WORK/g; mkdir -p "$r/scripts"; cp "$SCRIPT" "$r/scripts/" 2> /dev/null || : > "$r/scripts/check-no-lab-values.sh"; printf 'ugreen\n' > "$r/scripts/lab-values.deny"
run "$r"; want "outside a git work tree is an error" 2

# (h) a missing or empty deny file is an error.
r=$(mkrepo h); rm "$r/scripts/lab-values.deny"; git -C "$r" add -A
run "$r"; want "a missing deny file is an error" 2
r=$(mkrepo h2); printf '%s\n' '# nothing but a comment' '' > "$r/scripts/lab-values.deny"; git -C "$r" add -A
run "$r"; want "a deny file with no pattern is an error" 2

# (i) a clean repository exits 0 and prints nothing.
r=$(mkrepo i); printf 'hello\n' > "$r/README.md"; git -C "$r" add -A
run "$r"; want "a clean repository passes" 0; [ -z "$out" ] && pass "and prints nothing" || fail "and prints nothing (output: $out)"

# The script, its deny file and its test are never scanned, although the deny file names the words.
r=$(mkrepo j); cp "$HERE/check-no-lab-values_test.sh" "$r/scripts/" 2> /dev/null || printf 'ugreen\n' > "$r/scripts/check-no-lab-values_test.sh"
printf 'ugreen\n' >> "$r/scripts/check-no-lab-values_test.sh"; git -C "$r" add -A
run "$r"; want "the script, its deny file and its test are not scanned" 0

# (k) A text file that is not valid UTF-8 is still scanned: grep -I treats it as binary in a UTF-8 locale, so the script reads in the C locale.
UTF=
for l in en_US.UTF-8 C.UTF-8 en_US.utf8 C.utf8; do if locale -a 2> /dev/null | grep -qix "$l"; then UTF=$l; break; fi; done
r=$(mkrepo k); printf 'caf\351 ugreen\n' > "$r/latin1.txt"; git -C "$r" add -A
if [ -n "$UTF" ]; then
  export LC_ALL=$UTF; run "$r"; unset LC_ALL
  want "a Latin-1 text file is scanned under a UTF-8 locale" 1
else
  echo "skip  no UTF-8 locale installed: the Latin-1 case is not run"
fi

# (l) A deny file written with CRLF line endings, or with trailing blanks, still matches.
r=$(mkrepo l); printf 'ugreen\r\n' > "$r/scripts/lab-values.deny"; printf 'ugreen\n' > "$r/x.txt"; git -C "$r" add -A
run "$r"; want "a CRLF line in the deny file matches" 1
r=$(mkrepo l2); printf 'ugreen  \n' > "$r/scripts/lab-values.deny"; printf 'ugreen\n' > "$r/x.txt"; git -C "$r" add -A
run "$r"; want "trailing blanks in the deny file are ignored" 1

# (m) A pattern that is not a valid regular expression is an error, not a pass.
r=$(mkrepo m); printf 'a(\n' > "$r/scripts/lab-values.deny"; printf 'ugreen\n' > "$r/x.txt"; git -C "$r" add -A
run "$r"; want "an invalid regular expression is an error" 2

# (n) A grep that cannot do its job must not look like a clean pass.
r=$(mkrepo n); printf 'ugreen\n' > "$r/x.txt"; git -C "$r" add -A
real_grep=$(command -v grep); fake=$WORK/fakebin; mkdir -p "$fake"
printf '#!/bin/sh\nfor a in "$@"; do [ "$a" = -I ] && { echo "grep: broken" >&2; exit 2; }; done\nexec %s "$@"\n' "$real_grep" > "$fake/grep"; chmod +x "$fake/grep"
out=$(cd "$r" && PATH="$fake:$PATH" sh scripts/check-no-lab-values.sh 2>&1); rc=$?
want "a failing grep is an error" 2

[ "$fails" -eq 0 ] && echo "all passed" || { echo "$fails failed" >&2; exit 1; }
