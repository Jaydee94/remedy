#!/bin/sh
# Tests of check-expected-version.sh. Run: sh scripts/check-expected-version_test.sh
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
SCRIPT=$HERE/check-expected-version.sh
fails=0

# run <name> <want: ok|bad> <EXPECTED_VERSION or - for unset> <args...>
run() {
  name=$1 want=$2 exp=$3; shift 3
  if [ "$exp" = - ]; then
    env -u EXPECTED_VERSION sh "$SCRIPT" "$@" > /dev/null 2>&1; rc=$?
  else
    EXPECTED_VERSION=$exp sh "$SCRIPT" "$@" > /dev/null 2>&1; rc=$?
  fi
  if { [ "$want" = ok ] && [ "$rc" -eq 0 ]; } || { [ "$want" = bad ] && [ "$rc" -ne 0 ]; }; then echo "ok    $name"
  else echo "FAIL  $name (exit $rc, wanted $want)"; fails=$((fails + 1)); fi
}

run "equal, required"                  ok  0.1.0 0.1.0 require
run "equal, optional"                  ok  0.1.0 0.1.0 optional
run "different, required"              bad 0.1.0 0.1.1 require
run "different, optional"              bad 0.1.0 0.1.1 optional
run "a prefix is not equal"            bad 0.1.0 0.1.00 optional
run "unset, required"                  bad -     0.1.0 require
run "empty, required"                  bad ""    0.1.0 require
run "unset, optional"                  ok  -     0.1.0 optional
run "empty, optional"                  ok  ""    0.1.0 optional
run "no version given"                 bad 0.1.0 "" optional
run "an unknown mode"                  bad 0.1.0 0.1.0 sometimes
run "no mode"                          bad 0.1.0 0.1.0

# the message names both versions
out=$(EXPECTED_VERSION=0.9.9 sh "$SCRIPT" 0.1.0 require 2>&1 || true)
case $out in *0.9.9*0.1.0*|*0.1.0*0.9.9*) echo "ok    the message names both versions" ;; *) echo "FAIL  the message: $out"; fails=$((fails + 1)) ;; esac

[ "$fails" -eq 0 ] && echo "all passed" || { echo "$fails failed" >&2; exit 1; }
