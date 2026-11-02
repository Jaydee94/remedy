#!/bin/sh
# Checks that no tracked file carries a value that only the maintainer's lab has (a host name, an application, a
# repository, a path). The patterns are in scripts/lab-values.deny, one extended regular expression per line; blank lines
# and lines that start with # are ignored. Matching is case-insensitive.
#
# History is not scanned: docs/specs, docs/research, docs/plans, the drill runbook of cycle 6 (an example by design),
# CHANGELOG.md and scripts/spike. The script, its deny file and its test name the words and are skipped too. Binary files
# are skipped. Only tracked files are read (git ls-files), so a file you have not added is not checked.
#
# Usage: scripts/check-no-lab-values.sh     (inside a git work tree)
# Exit 0: clean. Exit 1: a hit per line on stdout as path:line:text. Exit 2: not a git work tree, or no usable deny file.
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
DENY=$HERE/lab-values.deny

top=$(git rev-parse --show-toplevel 2> /dev/null) || { echo "check-no-lab-values: not inside a git work tree" >&2; exit 2; }
[ -f "$DENY" ] || { echo "check-no-lab-values: $DENY does not exist" >&2; exit 2; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
pat=$tmp/patterns
# Comments and blank lines go; a CRLF line ending or trailing blanks are not part of a pattern.
grep -v -E '^[[:space:]]*(#|$)' "$DENY" | sed 's/[[:space:]]*$//' > "$pat"
[ -s "$pat" ] || { echo "check-no-lab-values: $DENY holds no pattern" >&2; exit 2; }

# A pattern grep cannot read must not look like "no hit": xargs below cannot tell grep's exit 2 from its exit 1.
printf '' | LC_ALL=C grep -E -f "$pat" > /dev/null 2>&1
[ $? -le 1 ] || { echo "check-no-lab-values: a pattern in $DENY is not a valid extended regular expression" >&2; exit 2; }

cd "$top" || exit 2
# The C locale: in a UTF-8 locale grep -I treats a text file with one invalid byte (Latin-1, say) as binary and skips it, and the
# patterns are ASCII, so -i still works. /dev/null is a file operand that keeps grep from reading standard input when xargs has
# nothing to pass.
git ls-files -z -- . \
  ':(exclude)docs/specs' ':(exclude)docs/research' ':(exclude)docs/plans' \
  ':(exclude)docs/runbook/home-server-drill.md' ':(exclude)CHANGELOG.md' ':(exclude)scripts/spike' \
  ':(exclude)scripts/lab-values.deny' ':(exclude)scripts/check-no-lab-values.sh' ':(exclude)scripts/check-no-lab-values_test.sh' \
  ':(exclude).claude' \
  | LC_ALL=C xargs -0 grep -I -n -i -H -E -f "$pat" /dev/null > "$tmp/hits" 2> "$tmp/err"

if [ -s "$tmp/hits" ]; then
  cat "$tmp/hits"
  echo "check-no-lab-values: the lines above carry a lab value (patterns: scripts/lab-values.deny). Rename it, or add the file to the exclusions in scripts/check-no-lab-values.sh if it is history." >&2
  exit 1
fi
# Without a hit, anything on standard error means grep did not do its job: that is not a clean pass.
if [ -s "$tmp/err" ]; then
  cat "$tmp/err" >&2
  echo "check-no-lab-values: grep reported errors, so the result cannot be trusted" >&2
  exit 2
fi
exit 0
