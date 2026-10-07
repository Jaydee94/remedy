#!/bin/sh
# Tests of cli-checksums.sh against local files. Run: sh scripts/cli-checksums_test.sh
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
SCRIPT=$HERE/cli-checksums.sh
WORK=$(mktemp -d)
srv=
trap '[ -z "$srv" ] || { kill "$srv"; wait "$srv"; } > /dev/null 2>&1; rm -rf "$WORK"' EXIT
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

# ---- Fix round 1: what must never be accepted ----
export CLI_CHECKSUMS_ALLOW_FILE=1

refuses() { # refuses <name> <message pattern> <command...>: must fail and say the pattern
  n=$1; pat=$2; shift 2
  if "$@" > "$WORK/out" 2>&1; then echo "FAIL  $n (should fail)"; fails=$((fails + 1))
  elif grep -q -- "$pat" "$WORK/out"; then echo "ok    $n"
  else echo "FAIL  $n (failed for another reason)"; cat "$WORK/out"; fails=$((fails + 1)); fi
}
unchanged() { eq "$1" "$(cat "$WORK/before.yaml")" "$(cat "$WORK/pin.yaml")"; }
upper() { printf '%s' "$1" | tr 'a-f' 'A-F'; }

# 1. No downgrade: curl is told to speak https only, also for redirects.
mkdir "$WORK/stub"
cat > "$WORK/stub/curl" <<'EOF'
#!/bin/sh
for a in "$@"; do printf '%s\n' "$a"; done >> "$CURL_ARGS"
exit 22
EOF
chmod +x "$WORK/stub/curl"
pin 1.0.0 "$X1" "$A1"
sed 's|^\( *urlTemplate: *\)"[^"]*"|\1"https://example.invalid/{version}/{platform}/claude"|' "$WORK/pin.yaml" > "$WORK/https.yaml"
cp "$WORK/https.yaml" "$WORK/before.yaml"
: > "$WORK/curl.args"
if ( unset CLI_CHECKSUMS_ALLOW_FILE; env PATH="$WORK/stub:$PATH" CURL_ARGS="$WORK/curl.args" sh "$SCRIPT" 2.0.0 --pin "$WORK/https.yaml" > "$WORK/out" 2>&1 ); then
  echo "FAIL  a failing curl must fail the script"; fails=$((fails + 1))
fi
eq  "curl gets -q first" "-q" "$(head -1 "$WORK/curl.args")"
eq  "curl gets --proto =https" "1 1" "$(grep -cx -- '--proto' "$WORK/curl.args") $(grep -x -A1 -- '--proto' "$WORK/curl.args" | grep -cx -- '=https')"
eq  "curl gets --proto-redir =https" "1 1" "$(grep -cx -- '--proto-redir' "$WORK/curl.args") $(grep -x -A1 -- '--proto-redir' "$WORK/curl.args" | grep -cx -- '=https')"
eq  "curl gets --max-time 600" "1" "$(grep -x -A1 -- '--max-time' "$WORK/curl.args" | grep -cx -- '600')"
eq  "the pin is untouched after a failed download" "$(cat "$WORK/before.yaml")" "$(cat "$WORK/https.yaml")"
pin 1.0.0 "$X1" "$A1"
: > "$WORK/curl.args"
env PATH="$WORK/stub:$PATH" CURL_ARGS="$WORK/curl.args" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml" > /dev/null 2>&1
eq  "with the test switch, file is allowed besides https" "2 0" "$(grep -cx -- '=https,file' "$WORK/curl.args") $(grep -cx -- '=https' "$WORK/curl.args")"

# The real thing: an https URL that redirects to http must fail.
if command -v openssl > /dev/null && command -v python3 > /dev/null; then
  cat > "$WORK/srv.py" <<'EOF'
import http.server, ssl, sys, threading
cert, key, portfile = sys.argv[1:4]
def reply(h, body):
    h.send_response(200); h.send_header("Content-Length", str(len(body))); h.end_headers(); h.wfile.write(body)
class Plain(http.server.BaseHTTPRequestHandler):
    def do_GET(self): reply(self, ("served " + self.path + "\n").encode())
    def log_message(self, *a): pass
plain = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Plain)
class Tls(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.startswith("/direct"): return reply(self, b"direct\n")
        self.send_response(302); self.send_header("Location", "http://127.0.0.1:%d%s" % (plain.server_port, self.path)); self.send_header("Content-Length", "0"); self.end_headers()
    def log_message(self, *a): pass
tls = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Tls)
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER); ctx.load_cert_chain(cert, key)
tls.socket = ctx.wrap_socket(tls.socket, server_side=True)
open(portfile, "w").write(str(tls.server_port))
threading.Thread(target=plain.serve_forever, daemon=True).start()
tls.serve_forever()
EOF
  if openssl req -x509 -newkey rsa:2048 -nodes -keyout "$WORK/tls.key" -out "$WORK/tls.crt" -days 1 -subj /CN=127.0.0.1 -addext subjectAltName=IP:127.0.0.1 > /dev/null 2>&1; then
    python3 -I "$WORK/srv.py" "$WORK/tls.crt" "$WORK/tls.key" "$WORK/port" > /dev/null 2>&1 &
    srv=$!
    i=0; while [ ! -s "$WORK/port" ] && [ "$i" -lt 50 ]; do sleep 0.1; i=$((i + 1)); done
    port=$(cat "$WORK/port" 2> /dev/null || true)
    if [ -n "$port" ] && CURL_CA_BUNDLE="$WORK/tls.crt" curl -q -fsS -o /dev/null "https://127.0.0.1:$port/direct" 2> /dev/null; then
      pin 1.0.0 "$X1" "$A1"
      sed "s|^\( *urlTemplate: *\)\"[^\"]*\"|\1\"https://127.0.0.1:$port/{version}/{platform}/claude\"|" "$WORK/pin.yaml" > "$WORK/tls.yaml"
      cp "$WORK/tls.yaml" "$WORK/before.yaml"
      if ( unset CLI_CHECKSUMS_ALLOW_FILE; CURL_CA_BUNDLE="$WORK/tls.crt" sh "$SCRIPT" 2.0.0 --pin "$WORK/tls.yaml" > "$WORK/out" 2>&1 ); then
        echo "FAIL  an https URL that redirects to http must fail"; cat "$WORK/out"; fails=$((fails + 1))
      else
        echo "ok    an https URL that redirects to http fails"
      fi
      eq  "and the pin is untouched after the redirect" "$(cat "$WORK/before.yaml")" "$(cat "$WORK/tls.yaml")"
    else
      echo "skip  the https-to-http redirect test: this curl does not trust the local test certificate"
    fi
  else
    echo "skip  the https-to-http redirect test: openssl cannot make a certificate here"
  fi
else
  echo "skip  the https-to-http redirect test: needs openssl and python3"
fi

# 2. An empty download, or the same bytes for both platforms (an error or a captive page), is no artifact.
for v in 3.0.0 4.0.0 5.0.0 6.0.0; do mkdir -p "$WORK/art/$v/linux-x64" "$WORK/art/$v/linux-arm64"; done
printf 'x64 3\n' > "$WORK/art/3.0.0/linux-x64/claude"; rm -r "$WORK/art/3.0.0/linux-arm64"   # 3.0.0 has no arm64 artifact
printf 'same\n' > "$WORK/art/4.0.0/linux-x64/claude"; printf 'same\n' > "$WORK/art/4.0.0/linux-arm64/claude"
: > "$WORK/art/5.0.0/linux-x64/claude"; printf 'arm 5\n' > "$WORK/art/5.0.0/linux-arm64/claude"
printf 'x64 6\n' > "$WORK/art/6.0.0/linux-x64/claude"; : > "$WORK/art/6.0.0/linux-arm64/claude"
pin 1.0.0 "$X1" "$A1"; cp "$WORK/pin.yaml" "$WORK/before.yaml"
refuses "an empty amd64 download refuses" "empty" sh "$SCRIPT" 5.0.0 --pin "$WORK/pin.yaml"
unchanged "and leaves the pin untouched (empty amd64)"
refuses "an empty arm64 download refuses" "empty" sh "$SCRIPT" 6.0.0 --pin "$WORK/pin.yaml"
unchanged "and leaves the pin untouched (empty arm64)"
refuses "two identical artifacts refuse" "same checksum" sh "$SCRIPT" 4.0.0 --pin "$WORK/pin.yaml"
unchanged "and leaves the pin untouched (identical)"
refuses "amd64 downloads, arm64 does not: refuses" "cannot download" sh "$SCRIPT" 3.0.0 --pin "$WORK/pin.yaml"
unchanged "and leaves the pin untouched (arm64 missing)"

# 3. Every checksum is 64 lowercase hex characters, the hash tool must exist and must work.
pin 1.0.0 "" "$A1"
refuses "--check refuses an empty checksum in the pin" "not a SHA-256" sh "$SCRIPT" --check --pin "$WORK/pin.yaml"
pin 1.0.0 "$X1" "TBD"
refuses "--check refuses a placeholder checksum in the pin" "not a SHA-256" sh "$SCRIPT" --check --pin "$WORK/pin.yaml"
pin 1.0.0 "$X1" "$(upper "$A1")"
refuses "--check refuses an uppercase checksum in the pin" "not a SHA-256" sh "$SCRIPT" --check --pin "$WORK/pin.yaml"
pin 1.0.0 "TBD" "TBD"
ok  "a placeholder pin can be filled in by a write" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml"
ok  "and then --check passes" sh "$SCRIPT" --check --pin "$WORK/pin.yaml"

mkdir "$WORK/hashfail" "$WORK/hashgarbage" "$WORK/bin"
printf '#!/bin/sh\nexit 1\n' > "$WORK/hashfail/sha256sum"
printf '#!/bin/sh\necho "zzz  $1"\n' > "$WORK/hashgarbage/sha256sum"
chmod +x "$WORK/hashfail/sha256sum" "$WORK/hashgarbage/sha256sum"
pin 1.0.0 "$X1" "$A1"; cp "$WORK/pin.yaml" "$WORK/before.yaml"
refuses "a hash tool that fails refuses" "SHA-256" env PATH="$WORK/hashfail:$PATH" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml"
unchanged "and leaves the pin untouched (hash tool fails)"
refuses "a hash tool that prints garbage refuses" "not a SHA-256" env PATH="$WORK/hashgarbage:$PATH" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml"
unchanged "and leaves the pin untouched (hash garbage)"
for t in sed head mktemp rm cat grep tr curl; do ln -s "$(command -v "$t")" "$WORK/bin/$t"; done
refuses "no hash tool at all refuses" "needs sha256sum or shasum" env PATH="$WORK/bin" "$(command -v sh)" "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml"
unchanged "and leaves the pin untouched (no hash tool)"

# 4. --expect: a value that is given must be 64 hex characters, once per platform, in write mode only.
refuses "empty --expect values refuse" "64 hexadecimal" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml" --expect amd64= --expect arm64=
unchanged "and leave the pin untouched (empty expect)"
refuses "a malformed --expect refuses" "64 hexadecimal" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml" --expect "amd64=${X2}0" --expect "arm64=$A2"
refuses "a duplicate --expect refuses" "twice" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml" --expect "amd64=$X2" --expect "amd64=$X2"
refuses "a bad value and a good one for the same platform refuse" "64 hexadecimal" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml" --expect "amd64=BAD" --expect "amd64=$X2"
unchanged "and leave the pin untouched (duplicate expect)"
ok  "uppercase correct hex is accepted" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml" --expect "amd64=$(upper "$X2")" --expect "arm64=$(upper "$A2")"
pin 1.0.0 "$X1" "$A1"
refuses "--expect in --check mode refuses" "usage" sh "$SCRIPT" --check --pin "$WORK/pin.yaml" --expect "amd64=$X1"
refuses "a version in --check mode refuses" "usage" sh "$SCRIPT" --check 1.0.0 --pin "$WORK/pin.yaml"

# 5. Gaps the mutation run showed.
pin 1.0.0 "$X1" "$zero"
bad "--check fails when only the arm64 checksum is wrong" sh "$SCRIPT" --check --pin "$WORK/pin.yaml"
pin 1.0.0 "$X1" "$A1"; cp "$WORK/pin.yaml" "$WORK/before.yaml"
refuses "a published arm64 checksum that differs refuses" "arm64 checksum differs" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml" --expect "arm64=$zero"
unchanged "and leaves the pin untouched (arm64 expect)"
refuses "a published amd64 checksum that differs refuses with its message" "amd64 checksum differs" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml" --expect "amd64=$zero"
unchanged "and leaves the pin untouched (amd64 expect)"

# Minor: only a pin of the expected shape is rewritten, and the result is read back.
pin 1.0.0 "$X1" "$A1"; printf '    version: "9.9.9"\n' >> "$WORK/pin.yaml"; cp "$WORK/pin.yaml" "$WORK/before.yaml"
refuses "two version lines refuse" "exactly one" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml"
unchanged "and leave the pin untouched (two versions)"
pin 1.0.0 "$X1" "$A1"; printf '      amd64: {name: "other", sha256: "%s"}\n' "$X1" >> "$WORK/pin.yaml"; cp "$WORK/pin.yaml" "$WORK/before.yaml"
refuses "two amd64 lines refuse" "exactly one" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml"
unchanged "and leave the pin untouched (two amd64)"

# The rewrite is read back: a sed that does not rewrite, and a write that does not happen, are both caught.
mkdir "$WORK/nosed" "$WORK/nocat"
cat > "$WORK/nosed/sed" <<'EOF'
#!/bin/sh
# the real sed, except that the call that rewrites the pin leaves the file as it is
case $1 in
  's|^\( *version'*) for f in "$@"; do :; done; cat "$f" ;;
  *) exec "$REAL_SED" "$@" ;;
esac
EOF
printf '#!/bin/sh\nexit 0\n' > "$WORK/nocat/cat"
chmod +x "$WORK/nosed/sed" "$WORK/nocat/cat"
pin 1.0.0 "$X1" "$A1"; cp "$WORK/pin.yaml" "$WORK/before.yaml"
refuses "a rewrite that changes nothing is refused before the write" "writing nothing" env REAL_SED="$(command -v sed)" PATH="$WORK/nosed:$PATH" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml"
unchanged "and leaves the pin untouched (rewrite changed nothing)"
refuses "a write that does not happen is caught by the read back" "after the write" env PATH="$WORK/nocat:$PATH" sh "$SCRIPT" 2.0.0 --pin "$WORK/pin.yaml"

[ "$fails" -eq 0 ] && echo "all passed" || { echo "$fails failed" >&2; exit 1; }
