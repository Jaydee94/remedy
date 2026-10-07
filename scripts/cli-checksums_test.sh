#!/bin/sh
# Tests of cli-checksums.sh against local files. Run: sh scripts/cli-checksums_test.sh
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
SCRIPT=$HERE/cli-checksums.sh
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
fails=0
export CLI_CHECKSUMS_ALLOW_FILE=1

sum() { if command -v sha256sum > /dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }

for v in 1.0.0 2.0.0; do
  for p in linux-x64 linux-arm64; do
    mkdir -p "$WORK/art/$v/$p"
    printf 'cli %s for %s\n' "$v" "$p" > "$WORK/art/$v/$p/claude"
  done
done
X1=$(sum "$WORK/art/1.0.0/linux-x64/claude"); A1=$(sum "$WORK/art/1.0.0/linux-arm64/claude")
X2=$(sum "$WORK/art/2.0.0/linux-x64/claude"); A2=$(sum "$WORK/art/2.0.0/linux-arm64/claude")

pin() { # pin <version> <amd64 sha> <arm64 sha>
  cat > "$WORK/pin.yaml" <<EOF
# a comment that must survive
runner:
  cli:
    version: "$1"
    urlTemplate: "file://$WORK/art/{version}/{platform}/claude"
    archive: none
    member: ""
    platforms:
      amd64: {name: "linux-x64", sha256: "$2"}
      arm64: {name: "linux-arm64", sha256: "$3"}
EOF
}
eq()  { if [ "$2" = "$3" ]; then echo "ok    $1"; else echo "FAIL  $1"; echo "  want: $2"; echo "  got:  $3"; fails=$((fails + 1)); fi; }
ok()  { n=$1; shift; if "$@" > "$WORK/out" 2>&1; then echo "ok    $n"; else echo "FAIL  $n (should pass)"; cat "$WORK/out"; fails=$((fails + 1)); fi; }
bad() { n=$1; shift; if "$@" > "$WORK/out" 2>&1; then echo "FAIL  $n (should fail)"; fails=$((fails + 1)); else echo "ok    $n"; fi; }

zero=0000000000000000000000000000000000000000000000000000000000000000

pin 1.0.0 "$X1" "$A1"
ok  "--check passes for a correct pin" sh "$SCRIPT" --check --pin "$WORK/pin.yaml"

pin 1.0.0 "$zero" "$A1"
bad "--check fails for a wrong checksum" sh "$SCRIPT" --check --pin "$WORK/pin.yaml"

pin 1.0.0 "$X1" "$A1"
ok  "a new version is written" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml"
eq  "the version is now 2.0.0" 'version: "2.0.0"' "$(grep 'version:' "$WORK/pin.yaml" | sed 's/^ *//')"
eq  "the amd64 checksum is the new one" "$X2" "$(sed -n 's/.*amd64: {name: "linux-x64", sha256: "\([0-9a-f]*\)"}/\1/p' "$WORK/pin.yaml")"
eq  "the arm64 checksum is the new one" "$A2" "$(sed -n 's/.*arm64: {name: "linux-arm64", sha256: "\([0-9a-f]*\)"}/\1/p' "$WORK/pin.yaml")"
eq  "the comment survived" "# a comment that must survive" "$(head -1 "$WORK/pin.yaml")"
ok  "and --check passes on the result" sh "$SCRIPT" --check --pin "$WORK/pin.yaml"

pin 1.0.0 "$X1" "$A1"
ok  "published checksums that match are accepted" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml" --expect "amd64=$X2" --expect "arm64=$A2"

pin 1.0.0 "$X1" "$A1"
cp "$WORK/pin.yaml" "$WORK/before.yaml"
bad "a published checksum that differs refuses" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml" --expect "amd64=$zero"
eq  "and leaves the pin untouched" "$(cat "$WORK/before.yaml")" "$(cat "$WORK/pin.yaml")"

bad "a version that does not exist (no file) refuses" sh "$SCRIPT" 9.9.9 --pin "$WORK/pin.yaml"
eq  "and leaves the pin untouched again" "$(cat "$WORK/before.yaml")" "$(cat "$WORK/pin.yaml")"
bad "a version with a path in it refuses" sh "$SCRIPT" ../1.0.0 --pin "$WORK/pin.yaml"
bad "no version and no --check refuses" sh "$SCRIPT" --pin "$WORK/pin.yaml"
bad "an unknown option refuses" sh "$SCRIPT" 2.0.0 --nope

unset CLI_CHECKSUMS_ALLOW_FILE
bad "a file:// URL is refused without the test switch" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml"

# A refused scheme must say so: a connection error to the unused port 9 would also fail, but with another message.
refused() { # refused <name> <template>
  sed "s|^\( *urlTemplate: *\)\"[^\"]*\"|\1\"$2\"|" "$WORK/before.yaml" > "$WORK/scheme.yaml"
  if sh "$SCRIPT" 2.0.0 --pin "$WORK/scheme.yaml" > "$WORK/out" 2>&1; then
    echo "FAIL  $1 (should fail)"; fails=$((fails + 1))
  elif grep -q 'only https URLs are accepted' "$WORK/out"; then echo "ok    $1"
  else echo "FAIL  $1 (failed for another reason)"; cat "$WORK/out"; fails=$((fails + 1)); fi
}
refused "an http URL is refused" 'http://127.0.0.1:9/{version}/{platform}/claude'
refused "an ftp URL is refused" 'ftp://127.0.0.1:9/{version}/{platform}/claude'

[ "$fails" -eq 0 ] && echo "all passed" || { echo "$fails failed" >&2; exit 1; }
