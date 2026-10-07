#!/bin/sh
# Checks that no script opens a file in the CLI's login directory. Remedy never reads, copies, logs or stores the agent
# CLIs' credentials; the dummy setup keeps the login in $CLAUDE_DIR (~/remedy-kind/claude) and its scripts may only
# create the directory, set its mode, put its path into the kind configuration, test that it exists, mention it in a
# message, and delete its content. Any other line that names it, any line that spells its path ($OUT/claude or the
# literal ~/remedy-kind/claude), and any line that reads, copies, archives or removes "$OUT" (the directory that holds
# it) without sparing it, fails. ${CLAUDE_DIR} and ${OUT} count like $CLAUDE_DIR and $OUT. The check reads text lines
# only; it never looks at the directory itself.
# Usage: scripts/check-login-dir-untouched.sh [file...]    (default: dev/kind/*.sh)
# Portable awk only (BSD awk on macOS, gawk, mawk and busybox awk on Linux): no gawk extensions, no intervals.
set -eu
if [ $# -eq 0 ]; then
  set -- dev/kind/*.sh
fi

awk '
  {
    line = $0
    if (line ~ /^[[:space:]]*#/) next

    # ${CLAUDE_DIR}, ${OUT} and ${OUT:?} are the same variables as $CLAUDE_DIR and $OUT.
    norm = line
    gsub(/\$\{CLAUDE_DIR(:[-?+=][^}]*)?\}/, "$CLAUDE_DIR", norm)
    gsub(/\$\{CLAUDE_DIR_MODE(:[-?+=][^}]*)?\}/, "$CLAUDE_DIR_MODE", norm)
    gsub(/\$\{OUT(:[-?+=][^}]*)?\}/, "$OUT", norm)

    # The one place that may spell the path: the assignment. Whatever follows it on the line is checked like any other
    # text, and a longer path (CLAUDE_DIR=$OUT/claude/sub) is not the assignment.
    if (sub(/^[[:space:]]*CLAUDE_DIR=\$OUT\/claude/, "", norm) && norm !~ /^([[:space:]]|;|$)/) {
      norm = "CLAUDE_DIR=$OUT/claude" norm
    }

    # The path to the login in any spelling, even inside a message: $OUT/claude, ~/remedy-kind/claude,
    # $HOME/remedy-kind/claude. Quotes inside the path do not hide it ($OUT/"claude").
    bare = norm
    gsub(/["\047]/, "", bare)
    if (bare ~ /\$OUT\/+claude/ || bare ~ /remedy-kind\/+claude/) {
      printf "%s:%d: spells the path of the login directory: %s\n", FILENAME, FNR, line
      bad = 1
    }

    # Take out what is allowed. What is left must not name the login directory. A message may mention a variable, but never
    # run a command ($( and backticks are not allowed inside the quotes of an allowed echo or printf).
    rest = norm
    gsub(/^[[:space:]]*CLAUDE_DIR_MODE=[0-9]+/, "", rest)
    gsub(/mkdir -p "\$CLAUDE_DIR"/, "", rest)
    gsub(/chmod "\$CLAUDE_DIR_MODE" "\$CLAUDE_DIR"/, "", rest)
    gsub(/sed "s[|]__CLAUDE_DIR__[|]\$CLAUDE_DIR[|]"/, "", rest)
    gsub(/find "\$CLAUDE_DIR" -mindepth 1 -delete/, "", rest)
    gsub(/\[ -d "\$CLAUDE_DIR" \]/, "", rest)
    gsub(/echo "([^"$`]|\$[A-Za-z_][A-Za-z_0-9]*)*"/, "", rest)
    gsub(/printf "([^"$`]|\$[A-Za-z_][A-Za-z_0-9]*)*"( "\$[A-Za-z_][A-Za-z_0-9]*")*/, "", rest)
    if (rest ~ /CLAUDE_DIR/) {
      printf "%s:%d: names the login directory in a way that could read it: %s\n", FILENAME, FNR, line
      bad = 1
    }

    # "$OUT" holds the login. A command that works on all of it ("$OUT", "$OUT"/*, "$OUT/"*, a loop over it) must spare
    # it. What follows $OUT must not continue the name or the path ($OUT/env.sh and $OUT_MARKER are files of their own).
    all = rest
    gsub(/["\047]/, "", all)
    if (line !~ /! -name claude/ &&
        (all ~ /(^|[^A-Za-z0-9_.-])(rm|cp|mv|tar|zip|rsync|cat|grep|shasum|sha256sum|md5|base64|xxd|strings|find|head|tail|less|more|od|hexdump|gzip|ln)[[:space:]][^|;&]*\$OUT\/*\*?([^A-Za-z0-9_.\/$-]|$)/ ||
         all ~ /(^|[^A-Za-z0-9_])for[[:space:]]+[A-Za-z_][A-Za-z_0-9]*[[:space:]]+in[[:space:]][^;]*\$OUT\/*\*/)) {
      printf "%s:%d: works on all of $OUT, which holds the login, without sparing it: %s\n", FILENAME, FNR, line
      bad = 1
    }
  }
  END { exit bad }
' "$@" && echo "no script reads the login directory"
