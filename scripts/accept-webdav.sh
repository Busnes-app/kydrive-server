#!/usr/bin/env bash
# Acceptance: WebDAV through real clients against the compiled server.
# Usage: scripts/accept-webdav.sh
# Needs .browser/server (go build -o .browser/server ./cmd/server). Runs curl protocol checks,
# then each of gio (GNOME), kioclient (KDE), rclone and litmus that is installed; a missing
# client is reported as SKIP, never as a pass. Owns its server and scratch directory.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
port=5394
url=http://127.0.0.1:$port
pid=
secret=$(openssl rand -hex 32)
work=$(mktemp -d /var/tmp/kydav.XXXXXX)
failed=0

stop() { if [ -n "$pid" ]; then kill "$pid" 2>/dev/null || true; wait "$pid" || true; pid=; fi; }
cleanup() { stop; rm -rf "$work"; }
trap cleanup EXIT
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; failed=1; }
skip() { echo "SKIP: $* (not installed)"; }

env -i PATH="$PATH" KY_APP_URL=$url KY_HOST=127.0.0.1 KY_PORT=$port KY_DB_DRIVER=sqlite \
  KY_DATA_DIR="$work/data" KY_ADMIN_PASSWORD=DavInitial123! KY_CAPTCHA_PROVIDER=none \
  KY_SESSION_SECRET="$secret" "$root/.browser/server" >"$work/server.log" 2>&1 &
pid=$!
curl -fs --retry 100 --retry-delay 0 --retry-connrefused --retry-max-time 30 -o /dev/null "$url/healthz" || { tail "$work/server.log"; exit 1; }

jar=$work/cookies
api() { curl -fsS -b "$jar" -c "$jar" -H "X-CSRF-Token: $(awk '$6=="ky_csrf"{print $7}' "$jar")" "$@"; }
login() { curl -fsS -c "$jar" -H 'Content-Type: application/json' -d "{\"username\":\"admin\",\"password\":\"$1\"}" "$url/api/auth/login" >/dev/null; }
login DavInitial123!
api -H 'Content-Type: application/json' -d '{"current_password":"DavInitial123!","new_password":"DavUpdated456!"}' "$url/api/auth/change-password" >/dev/null
login DavUpdated456!
api -X POST -H 'Content-Type: application/json' -d '{}' "$url/api/drive/personal-workspace" >/dev/null
pw=$(api -H 'Content-Type: application/json' -d '{"name":"acceptance"}' "$url/api/auth/app-passwords" | jq -r .password)
dav=$url/dav/My%20files
d() { curl -s -o /dev/null -w '%{http_code}' -u "admin:$pw" "$@"; }

# Protocol checks with curl.
expect() { local want=$1 what=$2; shift 2; local got; got=$(d "$@"); if [ "$got" = "$want" ]; then pass "$what"; else fail "$what: $got, want $want"; fi; }
got=$(curl -s -o /dev/null -w '%{http_code}' -X PROPFIND "$url/dav/")
if [ "$got" = 401 ]; then pass "no credentials refused"; else fail "no credentials: $got"; fi
expect 207 "propfind root" -X PROPFIND -H 'Depth: 1' "$url/dav/"
expect 201 "mkcol" -X MKCOL "$dav/Reports"
expect 201 "put" -T - "$dav/Reports/q1.txt" <<<"quarter one"
expect 201 "overwrite creates a version" -T - "$dav/Reports/q1.txt" <<<"quarter one, revised"
expect 412 "stale If-Match refused" -T - -H 'If-Match: "1"' "$dav/Reports/q1.txt" <<<"stale"
expect 201 "move" -X MOVE -H "Destination: $dav/Reports/q1-final.txt" "$dav/Reports/q1.txt"
expect 204 "delete trashes" -X DELETE "$dav/Reports/q1-final.txt"
versions=$(api "$url/api/drive/workspaces/$(api "$url/api/drive/workspaces" | jq -r '.[] | select(.kind=="personal") | .id')/files?trash=true" | jq -r '.[] | select(.name=="q1-final.txt") | .revision')
if [ "$versions" = 2 ]; then pass "deleted file is in the trash at revision 2"; else fail "trash holds revision $versions"; fi

# GNOME GVfs.
# gio needs a running GVfs with its WebDAV backend (gvfsd-dav), not just the gio binary.
if command -v gio >/dev/null && ! ls /usr/lib/gvfsd-dav /usr/lib/gvfs/gvfsd-dav /usr/libexec/gvfsd-dav >/dev/null 2>&1; then
  echo "SKIP: gio (GVfs WebDAV backend gvfsd-dav not installed)"
elif command -v gio >/dev/null; then
  printf 'admin\n%s\n' "$pw" | gio mount "dav://127.0.0.1:$port/dav/" >/dev/null 2>&1 || true
  printf 'from gio\n' >"$work/gio.txt"
  if gio copy "$work/gio.txt" "dav://127.0.0.1:$port/dav/My%20files/Reports/gio.txt" 2>"$work/gio.err" &&
     [ "$(gio cat "dav://127.0.0.1:$port/dav/My%20files/Reports/gio.txt" 2>>"$work/gio.err")" = "from gio" ] &&
     gio list "dav://127.0.0.1:$port/dav/My%20files/Reports/" 2>>"$work/gio.err" | grep -qx gio.txt; then
    pass "gio mount, copy, cat, list"
  else
    fail "gio: $(tr '\n' ' ' <"$work/gio.err")"
  fi
  gio mount -u "dav://127.0.0.1:$port/dav/" >/dev/null 2>&1 || true
else skip gio; fi

# KDE KIO.
kio=$(command -v kioclient5 || command -v kioclient || true)
if [ -n "$kio" ]; then
  printf 'from kio\n' >"$work/kio.txt"
  k="webdav://admin:$pw@127.0.0.1:$port/dav/My%20files/Reports"
  if "$kio" --noninteractive copy "$work/kio.txt" "$k/kio.txt" 2>"$work/kio.err" &&
     [ "$("$kio" --noninteractive cat "$k/kio.txt" 2>>"$work/kio.err")" = "from kio" ] &&
     "$kio" --noninteractive ls "$k/" 2>>"$work/kio.err" | grep -qx kio.txt; then
    pass "kioclient copy, cat, ls"
  else
    fail "kioclient: $(tr '\n' ' ' <"$work/kio.err")"
  fi
else skip kioclient; fi

if command -v rclone >/dev/null; then
  mkdir -p "$work/tree/a/b" && echo one >"$work/tree/a/1.txt" && echo two >"$work/tree/a/b/2.txt"
  r=(rclone --config /dev/null --webdav-url "$url/dav/" --webdav-vendor other --webdav-user admin --webdav-pass "$(rclone obscure "$pw")")
  if "${r[@]}" copy "$work/tree" ":webdav:My files/rclone" && "${r[@]}" check "$work/tree" ":webdav:My files/rclone"; then
    pass "rclone copy and check"
  else fail "rclone"; fi
else skip rclone; fi

if command -v litmus >/dev/null; then
  if litmus "$dav/litmus/" admin "$pw" >"$work/litmus.log" 2>&1; then pass "litmus"; else fail "litmus: $(grep -c FAIL "$work/litmus.log") failures"; fi
  grep -E '^<- summary|FAIL' "$work/litmus.log" || true
else skip litmus; fi

exit $failed
