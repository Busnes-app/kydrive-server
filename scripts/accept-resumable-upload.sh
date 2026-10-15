#!/usr/bin/env bash
# Acceptance: a large resumable upload survives a cut connection and a server restart, and
# downloads byte for byte. Usage: scripts/accept-resumable-upload.sh [GiB, default 5]
# Needs .browser/server (go build -o .browser/server ./cmd/server) and about 3x the size free
# under /var/tmp (disk-backed, short enough for the setup socket path; not tmpfs /tmp).
# Owns its server and scratch directory and removes both on exit.
set -euo pipefail
gib=${1:-5}
root=$(cd "$(dirname "$0")/.." && pwd)
url=http://127.0.0.1:5393
chunk=$((64 << 20))
pid=
secret=$(openssl rand -hex 32) # one session secret for both server runs, as in production

start() {
  env -i PATH="$PATH" KY_APP_URL=$url KY_HOST=127.0.0.1 KY_PORT=5393 KY_DB_DRIVER=sqlite \
    KY_DATA_DIR="$work/data" KY_ADMIN_PASSWORD=AcceptInitial123! KY_CAPTCHA_PROVIDER=none \
    KY_SESSION_SECRET="$secret" KY_WRITE_TIMEOUT=15m "$root/.browser/server" >>"$work/server.log" 2>&1 &
  pid=$!
  curl -fs --retry 100 --retry-delay 0 --retry-connrefused --retry-max-time 30 -o /dev/null "$url/healthz" || fail "server did not start"
}
stop() { if [ -n "$pid" ]; then kill "$pid" 2>/dev/null || true; wait "$pid" || true; pid=; fi; }
cleanup() { stop; rm -rf "$work"; }
fail() { echo "FAIL: $*" >&2; tail -20 "$work/server.log" >&2; exit 1; }

work=$(mktemp -d /var/tmp/kyaccept.XXXXXX)
trap cleanup EXIT
start

jar=$work/cookies
api() { curl -fsS -b "$jar" -c "$jar" -H "X-CSRF-Token: $(awk '$6=="ky_csrf"{print $7}' "$jar")" "$@"; }
login() { curl -fsS -c "$jar" -H 'Content-Type: application/json' -d "{\"username\":\"admin\",\"password\":\"$1\"}" "$url/api/auth/login" >/dev/null; }
login AcceptInitial123!
api -H 'Content-Type: application/json' -d '{"current_password":"AcceptInitial123!","new_password":"AcceptUpdated456!"}' "$url/api/auth/change-password" >/dev/null
login AcceptUpdated456!
api -X POST -H 'Content-Type: application/json' -d '{}' "$url/api/drive/personal-workspace" >/dev/null
ws=$(api "$url/api/drive/workspaces" | jq -r '.[] | select(.kind=="personal") | .id')

src=$work/source.bin
head -c $((gib << 30)) /dev/urandom >"$src"
size=$(stat -c %s "$src")
want=$(sha256sum "$src" | cut -d' ' -f1)
sid=$(api -H 'Content-Type: application/json' -d "{\"name\":\"accept.bin\",\"size\":$size}" "$url/api/drive/workspaces/$ws/upload-sessions" | jq -r .id)
received() { api "$url/api/drive/upload-sessions/$sid" | jq -r .received; }

put() { # put OFFSET [curl options...]: send one chunk from OFFSET
  local off=$1 len=$(( size - $1 < chunk ? size - $1 : chunk ))
  shift
  dd if="$src" bs=4M iflag=skip_bytes,count_bytes skip="$off" count="$len" status=none |
    api -X PUT -H 'Content-Type: application/octet-stream' -H "Content-Range: bytes $off-$((off + len - 1))/$size" --data-binary @- "$@" "$url/api/drive/upload-sessions/$sid"
}

half=$(( (size / chunk / 2) * chunk ))
off=0 cut=no restarted=no
while [ "$off" -lt "$size" ]; do
  if [ "$cut" = no ] && [ "$off" -ge $((chunk * 2)) ]; then
    cut=yes
    if put "$off" --limit-rate 8M --max-time 2 2>/dev/null; then fail "throttled chunk was not cut"; fi
    [ "$(received)" = "$off" ] || fail "cut chunk changed the received count"
    echo "cut a chunk at $off; server still reports $off"
  fi
  if [ "$restarted" = no ] && [ "$off" -ge "$half" ]; then
    restarted=yes
    stop
    start
    [ "$(received)" = "$off" ] || fail "restart lost the received count"
    echo "restarted the server at $off of $size"
  fi
  put "$off"
  off=$(received)
done

file=$(api -X POST "$url/api/drive/upload-sessions/$sid/complete")
[ "$(jq -r .digest <<<"$file")" = "$want" ] || fail "digest $(jq -r .digest <<<"$file") != $want"
[ "$(jq -r .size <<<"$file")" = "$size" ] || fail "size mismatch"
got=$(api "$url/api/drive/files/$(jq -r .id <<<"$file")/download" | sha256sum | cut -d' ' -f1)
[ "$got" = "$want" ] || fail "download sha256 $got != $want"
[ -z "$(ls -A "$work/data/staging" 2>/dev/null)" ] || fail "staging not cleaned up"
echo "PASS: $gib GiB upload resumed after a cut chunk and a restart; sha256 $want"
