#!/bin/sh
# Checks that a GitHub token appears nowhere Remedy writes or serves: in the files you name (the logs, the
# database file) and in every admin API response, including the run streams.
#
#   REMEDY_ADMIN_PASSWORD=... scripts/check-no-token-leak.sh <base-url> <file>...
#
# The token is read from standard input, with echo turned off on a terminal, so it never lands in the shell
# history or in the argument list of another process. Needs curl and jq. Exits 0 when nothing was found, 1 when
# the token was found, 2 when the check itself could not run.
set -u

base=${1:?usage: check-no-token-leak.sh <base-url> <file>...}
shift
[ "$#" -gt 0 ] || { echo "name at least one file to search" >&2; exit 2; }
[ -n "${REMEDY_ADMIN_PASSWORD:-}" ] || { echo "REMEDY_ADMIN_PASSWORD must be set" >&2; exit 2; }
command -v jq > /dev/null || { echo "jq is needed" >&2; exit 2; }

printf 'GitHub token to look for (input is hidden): ' >&2
[ -t 0 ] && stty -echo
IFS= read -r token
[ -t 0 ] && stty echo
echo >&2
[ "${#token}" -ge 8 ] || { echo "that is too short to search for" >&2; exit 2; }

for f in "$@"; do
  [ -r "$f" ] || { echo "cannot read $f" >&2; exit 2; }
done

work=$(mktemp -d)
chmod 700 "$work"
trap 'rm -rf "$work"' EXIT
printf '%s\n' "$token" > "$work/pattern"
unset token

# count prints how many lines of the file contain the token.
count() { grep -a -c -F -f "$work/pattern" "$1"; }

# The search must be able to find something.
{ echo "noise"; cat "$work/pattern"; echo "noise"; } > "$work/probe"
[ "$(count "$work/probe")" = "1" ] || { echo "the search does not find a planted token: not trusting it" >&2; exit 2; }

leaks=0
report() { # report <name> <count>
  if [ "$2" != "0" ]; then
    echo "LEAK  $1 ($2 lines)"
    leaks=$((leaks + 1))
  else
    echo "ok    $1"
  fi
}

for f in "$@"; do
  report "$f" "$(count "$f")"
done

printf '{"password":"%s"}' "$REMEDY_ADMIN_PASSWORD" |
  curl -sf -c "$work/cookies" -H 'X-Remedy-CSRF: 1' --data-binary @- "$base/api/login" > /dev/null ||
  { echo "could not sign in at $base" >&2; exit 2; }

# fetch <path> <file> saves an answer. A stream that never ends is cut off after 10 seconds (curl exit 28);
# what arrived until then is still searched.
fetch() {
  curl -sfN --max-time 10 -b "$work/cookies" "$base$1" > "$2"
  rc=$?
  [ "$rc" -eq 0 ] || [ "$rc" -eq 28 ] || { echo "GET $1 failed (curl exit $rc)" >&2; exit 2; }
}

check_endpoint() {
  fetch "$1" "$work/answer"
  report "GET $1" "$(count "$work/answer")"
}

for path in /api/github/connection /api/repos "/api/incidents?state=all" "/api/activity?limit=200" /api/runs /api/limits; do
  check_endpoint "$path"
done

fetch "/api/incidents?state=all" "$work/incidents.json"
for id in $(jq -r '.[].id' "$work/incidents.json"); do
  check_endpoint "/api/incidents/$id"
done
fetch "/api/runs" "$work/runs.json"
for id in $(jq -r '.[].id' "$work/runs.json"); do
  check_endpoint "/api/runs/$id"
  check_endpoint "/api/runs/$id/events"
done

if [ "$leaks" -gt 0 ]; then
  echo "the token was found in $leaks places" >&2
  exit 1
fi
echo "the token appears nowhere that was searched"
