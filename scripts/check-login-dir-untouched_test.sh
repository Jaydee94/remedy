#!/bin/sh
# Tests of check-login-dir-untouched.sh. Run: sh scripts/check-login-dir-untouched_test.sh
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
CHECK=$HERE/check-login-dir-untouched.sh
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
fails=0

# ok <name> <file content>: the check must pass.  bad <name> <file content>: it must fail.
ok()  { printf '%s\n' "$2" > "$WORK/f.sh"; if sh "$CHECK" "$WORK/f.sh" > /dev/null 2>&1; then echo "ok    $1"; else echo "FAIL  $1 (should pass)"; fails=$((fails + 1)); fi; }
bad() { printf '%s\n' "$2" > "$WORK/f.sh"; if sh "$CHECK" "$WORK/f.sh" > /dev/null 2>&1; then echo "FAIL  $1 (should fail)"; fails=$((fails + 1)); else echo "ok    $1"; fi; }

ok  "an assignment"                 'CLAUDE_DIR=$OUT/claude'
ok  "the mode assignment"           'CLAUDE_DIR_MODE=700'
ok  "creating and chmod-ing"        'mkdir -p "$CLAUDE_DIR"; chmod "$CLAUDE_DIR_MODE" "$CLAUDE_DIR"'
ok  "substituting the path"         'sed "s|__CLAUDE_DIR__|$CLAUDE_DIR|" kind.yaml > out.yaml'
ok  "deleting the content"          'find "$CLAUDE_DIR" -mindepth 1 -delete'
ok  "testing for the directory"     '[ -d "$CLAUDE_DIR" ] && echo present'
ok  "a message"                     'echo "the login in $CLAUDE_DIR stays"'
ok  "a message after a test"        '[ -d "$CLAUDE_DIR" ] || { echo "no login directory at $CLAUDE_DIR"; exit 0; }'
ok  "a prompt"                      'printf "Remove the CLI login in %s? [y/N] " "$CLAUDE_DIR"'
ok  "a state in a variable"         'if [ -d "$CLAUDE_DIR" ]; then login_dir=present; else login_dir=missing; fi'
ok  "a comment"                     '# the files in $CLAUDE_DIR are never read'
ok  "removing the rest of OUT"      'find "$OUT" -mindepth 1 -maxdepth 1 ! -name claude -exec rm -rf {} +'
ok  "a file in OUT"                 'rm -f "$OUT/kind-rendered.yaml"'
ok  "nothing about the login"       'echo hello'
# Beyond the brief: the same allowed forms with braces, and files that merely sit in OUT.
ok  "braces in a message"           'echo "the login in ${CLAUDE_DIR} stays"'
ok  "braces in a test"              '[ -d "${CLAUDE_DIR}" ] && echo present'
ok  "braces in the removal"         'find "${CLAUDE_DIR}" -mindepth 1 -delete'
ok  "braces, sparing the login"     'find "${OUT}" -mindepth 1 -maxdepth 1 ! -name claude -exec rm -rf {} +'
ok  "a file named in OUT, braces"   'rm -f "${OUT}/kind-rendered.yaml"'
ok  "a file named after OUT"        'rm -f "$OUT/$OUT_MARKER"'
ok  "a path built from real"        'find "$real" -mindepth 1 -maxdepth 1 ! -name claude -exec rm -rf {} +'
ok  "a message with OUT and words"  'echo "removed the cluster and what the scripts generated in $OUT; copy more"'
ok  "writing a file into OUT"       'cat > "$OUT/env.sh" <<EOF'

bad "cat of a file in it"           'cat "$CLAUDE_DIR/.credentials.json"'
bad "grep in it"                    'grep -r token "$CLAUDE_DIR"'
bad "a hash of it"                  'shasum -a 256 "$CLAUDE_DIR"/*'
bad "copying it"                    'cp -r "$CLAUDE_DIR" /tmp/x'
bad "a command after an allowed one" 'mkdir -p "$CLAUDE_DIR"; cat "$CLAUDE_DIR/x"'
bad "the literal path"              'cat ~/remedy-kind/claude/x'
bad "a command inside a message"    'echo "$(cat "$CLAUDE_DIR/x")"'
bad "a command after a message"     'echo "x" && cat "$CLAUDE_DIR/x"'
bad "backticks inside a message"    'echo "`cat $CLAUDE_DIR/x`"'
bad "removing all of OUT"           'rm -rf "$OUT"'
bad "archiving OUT"                 'tar cf backup.tar "$OUT"'
bad "copying OUT"                   'cp -r "$OUT" /tmp/out'
bad "listing OUT's content"         'cat "$OUT"/*'
# Beyond the brief: other ways to the login.
bad "cat through OUT/claude"        'cat "$OUT/claude/.credentials.json"'
bad "cat through OUT/claude, bare"  'cat $OUT/claude/.credentials.json'
bad "OUT/claude, quoted apart"      'cat "$OUT"/"claude"/x'
bad "OUT/claude in a message"       'echo "login in $OUT/claude"'
bad "OUT/claude after an assignment" 'CLAUDE_DIR=$OUT/claude; cat "$OUT/claude/x"'
bad "OUT/claude as a longer path"   'CLAUDE_DIR=$OUT/claude/sub'
bad "cat, braces around the name"   'cat "${CLAUDE_DIR}/x"'
bad "grep, braces"                  'grep -r token "${CLAUDE_DIR}"'
bad "cp, braces"                    'cp -r "${CLAUDE_DIR}" /tmp/x'
bad "cat through OUT/claude, braces" 'cat "${OUT}/claude/x"'
bad "removing OUT, braces"          'rm -rf "${OUT}"'
bad "removing OUT, default form"    'rm -rf "${OUT:?}"'
bad "archiving OUT, braces"         'tar cf backup.tar "${OUT}"'
bad "listing OUT's content, braces" 'cat "${OUT}"/*'
bad "OUT's content, quoted glob"    'cat "$OUT/"*'
bad "OUT, no quotes"                'tar cf backup.tar $OUT'
bad "OUT, head"                     'head -c 100 "$OUT"/*'
bad "OUT in a loop"                 'for f in "$OUT"/*; do :; done'
bad "the literal with HOME"         'cat "$HOME/remedy-kind/claude/x"'
bad "the literal with HOME, braces" 'cat "${HOME}/remedy-kind/claude/x"'
bad "the literal as a bare path"    'ls ~/remedy-kind/claude'
bad "the literal in a message"      'echo "the login is in ~/remedy-kind/claude"'
bad "the literal in an assignment"  'dir=$HOME/remedy-kind/claude'
bad "the literal, quoted apart"     'ls ~/remedy-kind/"claude"'
bad "ls of it"                      'ls "$CLAUDE_DIR"'
bad "cd into it"                    'cd "$CLAUDE_DIR"'
bad "a loop over it"                'for f in "$CLAUDE_DIR"/*; do :; done'
bad "a redirection from it"         'read x < "$CLAUDE_DIR/x"'
bad "a redirection, braces"         'read x < "${CLAUDE_DIR}/x"'
bad "a loop reading from it"        'while read l; do :; done < "$CLAUDE_DIR/x"'
bad "a command after find"          'find "$CLAUDE_DIR" -mindepth 1 -exec cat {} +'
bad "a path below it"               'find "$CLAUDE_DIR"/sub -delete'
bad "re-pointing the directory"     'CLAUDE_DIR=/somewhere/else'
bad "a command in the mode"         'CLAUDE_DIR_MODE=$(cat "$CLAUDE_DIR/x")'

[ "$fails" -eq 0 ] && echo "all passed" || { echo "$fails failed" >&2; exit 1; }
