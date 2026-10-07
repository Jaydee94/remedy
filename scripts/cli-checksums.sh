#!/bin/sh
# Writes, or checks, the version and the SHA-256 checksums of the pinned claude CLI in deploy/cli-pin.yaml.
#
#   scripts/cli-checksums.sh <version> [--pin FILE] [--expect ARCH=SHA]...
#       downloads the artifact of that version for both platforms, and rewrites the version and the two sha256 fields.
#       With --expect it first compares the computed checksum with a checksum the vendor published (in the release's
#       manifest.json, platforms.<platform>.checksum; trust is otherwise "what the HTTPS download returned"), and writes
#       nothing on a difference.
#   scripts/cli-checksums.sh --check [--pin FILE]
#       recomputes the checksums for the version in the file and exits 1 on any difference. CI runs this when the pin
#       changes, because Renovate can bump the version but cannot compute the checksums.
#
# It never executes what it downloads. Only https URLs are accepted (file:// only for its own test, with
# CLI_CHECKSUMS_ALLOW_FILE=1). Needs curl and sed.
set -eu

pin=deploy/cli-pin.yaml
mode=write
version=
expect_amd64=
expect_arm64=

while [ $# -gt 0 ]; do
  case $1 in
    --check) mode=check ;;
    --pin) pin=${2:?--pin needs a file}; shift ;;
    --expect)
      arg=${2:?--expect needs ARCH=SHA}; shift
      case $arg in
        amd64=*) expect_amd64=${arg#amd64=} ;;
        arm64=*) expect_arm64=${arg#arm64=} ;;
        *) echo "--expect takes amd64=SHA or arm64=SHA" >&2; exit 2 ;;
      esac ;;
    -*) echo "unknown option $1" >&2; exit 2 ;;
    *) [ -z "$version" ] || { echo "one version only" >&2; exit 2; }; version=$1 ;;
  esac
  shift
done

[ -f "$pin" ] || { echo "$pin does not exist" >&2; exit 2; }
if [ "$mode" = write ]; then
  [ -n "$version" ] || { echo "usage: cli-checksums.sh <version> | --check" >&2; exit 2; }
else
  version=$(sed -n 's/^ *version: *"\([^"]*\)".*/\1/p' "$pin" | head -1)
  [ -n "$version" ] || { echo "$pin has no version" >&2; exit 2; }
fi
case $version in
  *[!0-9A-Za-z._-]* | "" | .*) echo "'$version' is not a version string" >&2; exit 2 ;;
esac

template=$(sed -n 's/^ *urlTemplate: *"\([^"]*\)".*/\1/p' "$pin" | head -1)
name_amd64=$(sed -n 's/^ *amd64: *{name: *"\([^"]*\)".*/\1/p' "$pin" | head -1)
name_arm64=$(sed -n 's/^ *arm64: *{name: *"\([^"]*\)".*/\1/p' "$pin" | head -1)
[ -n "$template" ] && [ -n "$name_amd64" ] && [ -n "$name_arm64" ] || { echo "$pin lacks the urlTemplate or a platform name" >&2; exit 2; }

sha256() {
  if command -v sha256sum > /dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# fetch <platform name> prints the SHA-256 of the artifact for the platform.
fetch() {
  url=$(printf '%s' "$template" | sed "s|{version}|$version|g; s|{platform}|$1|g")
  case $url in
    https://*) ;;
    file://*) [ "${CLI_CHECKSUMS_ALLOW_FILE:-}" = 1 ] || { echo "only https URLs are accepted: $url" >&2; exit 2; } ;;
    *) echo "only https URLs are accepted: $url" >&2; exit 2 ;;
  esac
  curl -fsSL --retry 2 -o "$work/artifact" "$url" || { echo "cannot download $url" >&2; exit 1; }
  sha256 "$work/artifact"
}

got_amd64=$(fetch "$name_amd64")
got_arm64=$(fetch "$name_arm64")
echo "amd64 ($name_amd64): $got_amd64"
echo "arm64 ($name_arm64): $got_arm64"

if [ "$mode" = check ]; then
  have_amd64=$(sed -n 's/^ *amd64: *{name: *"[^"]*", *sha256: *"\([0-9a-f]*\)".*/\1/p' "$pin" | head -1)
  have_arm64=$(sed -n 's/^ *arm64: *{name: *"[^"]*", *sha256: *"\([0-9a-f]*\)".*/\1/p' "$pin" | head -1)
  if [ "$have_amd64" != "$got_amd64" ] || [ "$have_arm64" != "$got_arm64" ]; then
    echo "$pin pins version $version with checksums that are not the ones of the download. Run: scripts/cli-checksums.sh $version" >&2
    exit 1
  fi
  echo "$pin is consistent for version $version"
  exit 0
fi

if [ -n "$expect_amd64" ] && [ "$expect_amd64" != "$got_amd64" ]; then echo "the published amd64 checksum differs from the download's: writing nothing" >&2; exit 1; fi
if [ -n "$expect_arm64" ] && [ "$expect_arm64" != "$got_arm64" ]; then echo "the published arm64 checksum differs from the download's: writing nothing" >&2; exit 1; fi

tmp=$work/pin.new
sed "s|^\( *version: *\)\"[^\"]*\"|\1\"$version\"|; \
     s|^\( *amd64: *{name: *\"[^\"]*\", *sha256: *\"\)[0-9a-f]*\(\".*\)|\1$got_amd64\2|; \
     s|^\( *arm64: *{name: *\"[^\"]*\", *sha256: *\"\)[0-9a-f]*\(\".*\)|\1$got_arm64\2|" "$pin" > "$tmp"
cat "$tmp" > "$pin"
echo "wrote version $version and both checksums to $pin"
