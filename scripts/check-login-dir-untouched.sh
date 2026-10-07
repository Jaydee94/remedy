#!/bin/sh
# Checks that no script opens a file in the CLI's login directory. Remedy never reads, copies, logs or stores the agent
# CLIs' credentials; the dummy setup keeps the login in $CLAUDE_DIR (~/remedy-kind/claude) and its scripts may only
# create the directory, set its mode, put its path into the kind configuration, test that it exists, mention it in a
# message, and delete its content. Any other line that names it, any line that spells its path ($OUT/claude, a variable
# followed by /claude, or the literal ~/remedy-kind/claude), and any line that reads, copies, archives or removes "$OUT"
# (the directory that holds it) without sparing it, fails. ${CLAUDE_DIR} and ${OUT} (also ${OUT%/} and the like) count
# like $CLAUDE_DIR and $OUT. A `find "$OUT"` is spared only when -maxdepth 1 or -prune comes before ! -name claude; a
# glob such as "$OUT"/*.token passes because it cannot match claude, one that can ("$OUT"/*) fails.
#
# This is a safety net for accidents, not a proof. It reads text lines only (it never looks at the directory itself) and
# cannot see: a variable that holds the path (d=$CLAUDE_DIR, or real and out in lib.sh), sourced code, eval, a command
# split over several lines, files outside the scanned set, or what a pipe appends to an allowed message
# (echo "$CLAUDE_DIR" | xargs cat). Known gaps kept on purpose: an echo or printf is not anchored to a command position, the
# allowed forms are a deny-list of commands and not an allow-list, ./claude, and globs that can match claude ("$OUT"/c*).
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

    # ${CLAUDE_DIR}, ${OUT}, ${OUT:?} and ${OUT%/} are the same variables as $CLAUDE_DIR and $OUT.
    norm = line
    gsub(/\$\{CLAUDE_DIR((%|#|:|\/)[^}]*)?\}/, "$CLAUDE_DIR", norm)
    gsub(/\$\{CLAUDE_DIR_MODE((%|#|:|\/)[^}]*)?\}/, "$CLAUDE_DIR_MODE", norm)
    gsub(/\$\{OUT((%|#|:|\/)[^}]*)?\}/, "$OUT", norm)

    # The one place that may spell the path: the assignment. Whatever follows it on the line is checked like any other
    # text, and a longer path (CLAUDE_DIR=$OUT/claude/sub) is not the assignment.
    if (sub(/^[[:space:]]*CLAUDE_DIR=\$OUT\/claude/, "", norm) && norm !~ /^([[:space:]]|;|$)/) {
      norm = "CLAUDE_DIR=$OUT/claude" norm
    }

    # The path to the login in any spelling, even inside a message: $OUT/claude, any variable followed by /claude,
    # ~/remedy-kind/claude, $HOME/remedy-kind/claude. Quotes inside the path do not hide it ($OUT/"claude").
    bare = norm
    gsub(/["\047]/, "", bare)
    if (bare ~ /\$\{?[A-Za-z_][A-Za-z_0-9]*\}?\/+claude/ || bare ~ /remedy-kind\/+claude/) {
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

    # "$OUT" holds the login. A command that works on all of it ("$OUT", "$OUT"/*, "$OUT/"*, "$OUT/.", a loop over it) must
    # spare it. What follows $OUT must not continue the name or the path ($OUT/env.sh and $OUT_MARKER are files of their own).
    all = rest
    gsub(/["\047]/, "", all)
    # A find that stops one level down and skips claude is the spared form; the rest of the line is still checked.
    gsub(/(^|[^A-Za-z0-9_.-])find[[:space:]][^|;&]*(-maxdepth 1|-prune)[[:space:]][^|;&]*! -name claude[^|;&]*/, " ", all)
    # A glob that needs a literal dot (*.token) cannot match claude.
    gsub(/\$OUT\/+\*\./, " ", all)
    cmds = "(^|[^A-Za-z0-9_.-])(rm|cp|mv|tar|zip|rsync|cat|grep|shasum|sha256sum|md5|base64|xxd|strings|find|head|tail|less|more|od|hexdump|gzip|ln)[[:space:]][^|;&]*"
    if (all ~ (cmds "\\$OUT/*\\*?([^A-Za-z0-9_./$-]|$)") ||
        all ~ (cmds "\\$OUT/+\\.([^A-Za-z0-9_$-]|$)") ||
        all ~ /(^|[^A-Za-z0-9_])for[[:space:]]+[A-Za-z_][A-Za-z_0-9]*[[:space:]]+in[[:space:]][^;]*\$OUT\/*\*/) {
      printf "%s:%d: works on all of $OUT, which holds the login, without sparing it: %s\n", FILENAME, FNR, line
      bad = 1
    }
  }
  END { exit bad }
' "$@" && echo "no checked line names the login directory in a way that could read it"
