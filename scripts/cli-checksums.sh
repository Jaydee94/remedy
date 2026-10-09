#!/bin/sh
# Writes, or checks, the version and the SHA-256 checksums of the pinned claude CLI in deploy/cli-pin.yaml.
#
#   scripts/cli-checksums.sh <version> [--pin FILE] [--expect ARCH=SHA]...
#       downloads the artifact of that version for both platforms, and rewrites the version and the two sha256 fields.
#       With --expect (once per platform, 64 hex characters, taken from the release's manifest.json,
#       platforms.<platform>.checksum; trust is otherwise "what the HTTPS download returned") it first compares the
#       computed checksum with the published one, and writes nothing on a difference.
#   scripts/cli-checksums.sh --check [--pin FILE]
#       recomputes the checksums for the version in the file and exits 1 on any difference. CI runs this when the pin
#       changes, because Renovate can bump the version but cannot compute the checksums. Takes no version, no --expect.
#
# It never executes what it downloads. Only https is spoken, also across redirects (curl --proto, --proto-redir);
# file:// only for its own test, with CLI_CHECKSUMS_ALLOW_FILE=1. An empty download, a checksum that is not 64
# lowercase hex characters, and the same checksum for both platforms are errors. Needs curl, sed, and sha256sum or shasum.
set -eu

pin=deploy/cli-pin.yaml
mode=write
version=
expect_amd64=
expect_arm64=
given_amd64=0
given_arm64=0

die() { code=$1; shift; echo "$*" >&2; exit "$code"; }

# is_sha <value>: exactly 64 lowercase hex characters (a newline or anything else inside makes it fail).
is_sha() {
  case $1 in
    *[!0-9a-f]*) return 1 ;;
  esac
  [ "${#1}" -eq 64 ]
}

while [ $# -gt 0 ]; do
  case $1 in
    --check) mode=check ;;
    --pin) pin=${2:?--pin needs a file}; shift ;;
    --expect)
      arg=${2:?--expect needs ARCH=SHA}; shift
      case $arg in *=*) ;; *) die 2 "--expect takes amd64=SHA or arm64=SHA" ;; esac
      arch=${arg%%=*}
      value=$(printf '%s' "${arg#*=}" | tr '[:upper:]' '[:lower:]')
      case $arch in amd64 | arm64) ;; *) die 2 "--expect takes amd64=SHA or arm64=SHA" ;; esac
      is_sha "$value" || die 2 "--expect $arch needs 64 hexadecimal characters, got '$value'"
      if [ "$arch" = amd64 ]; then
        [ "$given_amd64" = 0 ] || die 2 "--expect amd64 given twice"
        given_amd64=1; expect_amd64=$value
      else
        [ "$given_arm64" = 0 ] || die 2 "--expect arm64 given twice"
        given_arm64=1; expect_arm64=$value
      fi ;;
    -*) die 2 "unknown option $1" ;;
    *) [ -z "$version" ] || die 2 "one version only"; version=$1 ;;
  esac
  shift
done

if [ "$mode" = check ]; then
  if [ -n "$version" ] || [ "$given_amd64" = 1 ] || [ "$given_arm64" = 1 ]; then
    die 2 "usage: cli-checksums.sh --check [--pin FILE] (it takes the version and the checksums from the pin: no version, no --expect)"
  fi
else
  [ -n "$version" ] || die 2 "usage: cli-checksums.sh <version> [--pin FILE] [--expect ARCH=SHA]... | --check [--pin FILE]"
fi

command -v sha256sum > /dev/null || command -v shasum > /dev/null || die 2 "cli-checksums.sh needs sha256sum or shasum"
[ -f "$pin" ] || die 2 "$pin does not exist"

# The shapes of the lines that are read and rewritten. Exactly one of each, so that no lookalike line is ever rewritten.
re_version='^ *version: *"[^"]*"'
re_url='^ *urlTemplate: *"[^"]*"'
re_amd64='^ *amd64: *{name: *"[^"]*", *sha256: *"[^"]*"'
re_arm64='^ *arm64: *{name: *"[^"]*", *sha256: *"[^"]*"'
for re in "$re_version" "$re_url" "$re_amd64" "$re_arm64"; do
  n=$(grep -c -- "$re" "$pin" || true)
  [ "$n" = 1 ] || die 2 "$pin must have exactly one line like '$re' (it has $n)"
done

if [ "$mode" = check ]; then
  version=$(sed -n 's/^ *version: *"\([^"]*\)".*/\1/p' "$pin" | head -1)
  [ -n "$version" ] || die 2 "$pin has no version"
fi
case $version in
  *[!0-9A-Za-z._-]* | "" | .*) die 2 "'$version' is not a version string" ;;
esac

template=$(sed -n 's/^ *urlTemplate: *"\([^"]*\)".*/\1/p' "$pin" | head -1)
name_amd64=$(sed -n 's/^ *amd64: *{name: *"\([^"]*\)".*/\1/p' "$pin" | head -1)
name_arm64=$(sed -n 's/^ *arm64: *{name: *"\([^"]*\)".*/\1/p' "$pin" | head -1)
[ -n "$template" ] && [ -n "$name_amd64" ] && [ -n "$name_arm64" ] || die 2 "$pin lacks the urlTemplate or a platform name"

# pin_field <file> <amd64|arm64|version>: what the file says now.
pin_field() {
  case $2 in
    version) sed -n 's/^ *version: *"\([^"]*\)".*/\1/p' "$1" | head -1 ;;
    *) sed -n "s/^ *$2: *{name: *\"[^\"]*\", *sha256: *\"\\([^\"]*\\)\".*/\\1/p" "$1" | head -1 ;;
  esac
}

if [ "$mode" = check ]; then
  have_amd64=$(pin_field "$pin" amd64)
  have_arm64=$(pin_field "$pin" arm64)
  is_sha "$have_amd64" || die 1 "$pin has '$have_amd64' as the amd64 checksum, which is not a SHA-256 checksum (64 lowercase hex characters)"
  is_sha "$have_arm64" || die 1 "$pin has '$have_arm64' as the arm64 checksum, which is not a SHA-256 checksum (64 lowercase hex characters)"
fi

# Only https, also across redirects. The test may add file.
proto='=https'
if [ "${CLI_CHECKSUMS_ALLOW_FILE:-}" = 1 ]; then proto='=https,file'; fi

# sha256 <file> prints the checksum, and fails when the hash tool fails.
sha256() {
  if command -v sha256sum > /dev/null; then out=$(sha256sum "$1") || return 1; else out=$(shasum -a 256 "$1") || return 1; fi
  printf '%s\n' "${out%% *}"
}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# fetch <platform name> prints the SHA-256 of the artifact for the platform.
fetch() {
  url=$(printf '%s' "$template" | sed "s|{version}|$version|g; s|{platform}|$1|g")
  case $url in
    https://*) ;;
    file://*) [ "${CLI_CHECKSUMS_ALLOW_FILE:-}" = 1 ] || die 2 "only https URLs are accepted: $url" ;;
    *) die 2 "only https URLs are accepted: $url" ;;
  esac
  rm -f "$work/artifact"
  curl -q --proto "$proto" --proto-redir "$proto" --max-time 600 -fsSL --retry 2 -o "$work/artifact" "$url" || die 1 "cannot download $url"
  [ -s "$work/artifact" ] || die 1 "$url returned an empty file"
  sum=$(sha256 "$work/artifact") || die 1 "cannot compute the SHA-256 of the download of $url"
  is_sha "$sum" || die 1 "the hash tool printed '$sum' for $url, which is not a SHA-256 checksum"
  printf '%s\n' "$sum"
}

got_amd64=$(fetch "$name_amd64")
got_arm64=$(fetch "$name_arm64")
echo "amd64 ($name_amd64): $got_amd64"
echo "arm64 ($name_arm64): $got_arm64"
[ "$got_amd64" != "$got_arm64" ] || die 1 "both platforms have the same checksum: the downloads are not two different CLI builds (an error or a captive page?)"

if [ "$mode" = check ]; then
  if [ "$have_amd64" != "$got_amd64" ] || [ "$have_arm64" != "$got_arm64" ]; then
    die 1 "$pin pins version $version with checksums that are not the ones of the download. Run: scripts/cli-checksums.sh $version"
  fi
  echo "$pin is consistent for version $version"
  exit 0
fi

if [ "$given_amd64" = 1 ] && [ "$expect_amd64" != "$got_amd64" ]; then die 1 "the published amd64 checksum differs from the download's: writing nothing"; fi
if [ "$given_arm64" = 1 ] && [ "$expect_arm64" != "$got_arm64" ]; then die 1 "the published arm64 checksum differs from the download's: writing nothing"; fi

# verify_pin <file>: the file says exactly the version and the checksums that were computed.
verify_pin() {
  [ "$(pin_field "$1" version)" = "$version" ] && [ "$(pin_field "$1" amd64)" = "$got_amd64" ] && [ "$(pin_field "$1" arm64)" = "$got_arm64" ]
}

tmp=$work/pin.new
sed "s|^\( *version: *\)\"[^\"]*\"|\1\"$version\"|; \
     s|^\( *amd64: *{name: *\"[^\"]*\", *sha256: *\"\)[^\"]*\(\".*\)|\1$got_amd64\2|; \
     s|^\( *arm64: *{name: *\"[^\"]*\", *sha256: *\"\)[^\"]*\(\".*\)|\1$got_arm64\2|" "$pin" > "$tmp"
verify_pin "$tmp" || die 1 "the rewritten pin does not say what was computed: writing nothing"

# Deliberately `cat >` and not `mv`: the file keeps its mode, its owner and a symlink to it. Not atomic: it is a
# few hundred bytes, and the read back below fails loudly when it did not work.
cat "$tmp" > "$pin"
verify_pin "$pin" || die 1 "$pin does not say what was computed after the write: check it (git diff)"
echo "wrote version $version and both checksums to $pin"
