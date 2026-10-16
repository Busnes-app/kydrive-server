#!/usr/bin/env bash
# Acceptance: thumbnails from the compiled server, with Office and PDF pages rendered by a real
# Euro-Office Document Server.
# Usage: KYDRIVE_EDITOR_URL=http://127.0.0.1 KYDRIVE_EDITOR_SECRET=... scripts/accept-thumbnails.sh
# Needs .browser/server (go build -o .browser/server ./cmd/server), python3 and ImageMagick
# (magick). The Document Server must reach this host's loopback (e.g. docker --network host, with
# ALLOW_PRIVATE_IP_ADDRESS=true) and share KYDRIVE_EDITOR_SECRET as its JWT secret. Without
# KYDRIVE_EDITOR_URL the Office checks are reported as SKIP, never as a pass. Owns its server and
# scratch directory; writes the thumbnails to $KYDRIVE_THUMBS_OUT when set.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
port=5395
url=http://127.0.0.1:$port
pid=
work=$(mktemp -d /var/tmp/kythumbs.XXXXXX)
failed=0

stop() { if [ -n "$pid" ]; then kill "$pid" 2>/dev/null || true; wait "$pid" || true; pid=; fi; }
cleanup() { stop; rm -rf "$work"; }
trap cleanup EXIT
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; failed=1; }

editor=()
if [ -n "${KYDRIVE_EDITOR_URL:-}" ]; then
  editor=(KYDRIVE_EDITOR_URL="$KYDRIVE_EDITOR_URL" KYDRIVE_EDITOR_SECRET="$KYDRIVE_EDITOR_SECRET" KYDRIVE_EDITOR_DRIVE_URL=$url)
fi
env -i PATH="$PATH" KY_ENV=development KY_APP_URL=$url KY_HOST=127.0.0.1 KY_PORT=$port KY_DB_DRIVER=sqlite \
  KY_DATA_DIR="$work/data" KY_ADMIN_PASSWORD=ThumbInitial123! KY_CAPTCHA_PROVIDER=none \
  KY_SESSION_SECRET="$(openssl rand -hex 32)" "${editor[@]}" "$root/.browser/server" >"$work/server.log" 2>&1 &
pid=$!
curl -fs --retry 100 --retry-delay 0 --retry-connrefused --retry-max-time 30 -o /dev/null "$url/healthz" || { tail "$work/server.log"; exit 1; }

jar=$work/cookies
api() { curl -fsS -b "$jar" -c "$jar" -H "X-CSRF-Token: $(awk '$6=="ky_csrf"{print $7}' "$jar")" "$@"; }
login() { curl -fsS -c "$jar" -H 'Content-Type: application/json' -d "{\"username\":\"admin\",\"password\":\"$1\"}" "$url/api/auth/login" >/dev/null; }
login ThumbInitial123!
api -H 'Content-Type: application/json' -d '{"current_password":"ThumbInitial123!","new_password":"ThumbUpdated456!"}' "$url/api/auth/change-password" >/dev/null
login ThumbUpdated456!
api -X POST -H 'Content-Type: application/json' -d '{}' "$url/api/drive/personal-workspace" >/dev/null
ws=$(api "$url/api/drive/workspaces" | jq -r '.[] | select(.kind=="personal") | .id')
upload() { api --data-binary "@$2" "$url/api/drive/workspaces/$ws/uploads?name=$1&parent=&revision=0" | jq -r .id; }

# thumb NAME FILE WANT: WANT is a PNG no larger than 256 a side, or a status such as 415.
thumb() {
  local id code
  id=$(upload "$1" "$2")
  code=$(curl -s -b "$jar" -o "$work/$1.png" -w '%{http_code}' "$url/api/drive/files/$id/thumbnail")
  if [ "$3" = png ]; then
    local size
    size=$(magick identify -format '%wx%h' "$work/$1.png" 2>/dev/null || true)
    if [ "$code" = 200 ] && [[ $size =~ ^([0-9]+)x([0-9]+)$ ]] && [ "${BASH_REMATCH[1]}" -le 256 ] && [ "${BASH_REMATCH[2]}" -le 256 ]; then
      pass "$1 thumbnail $size"
      [ -n "${KYDRIVE_THUMBS_OUT:-}" ] && cp "$work/$1.png" "$KYDRIVE_THUMBS_OUT/"
    else
      fail "$1 thumbnail: status $code, size '$size'"
    fi
  elif [ "$code" = "$3" ]; then pass "$1 refused ($code)"; else fail "$1: status $code, want $3"; fi
}

magick -size 1200x800 gradient:navy-orange "$work/photo.jpg"
magick -size 640x900 xc:white -fill '#c0392b' -pointsize 64 -annotate +40+120 'KyDrive PDF' "$work/paper.pdf"
python3 -I - "$work/hello.docx" <<'EOF'
import sys, zipfile
with zipfile.ZipFile(sys.argv[1], "w") as z:
    z.writestr("[Content_Types].xml", '<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>')
    z.writestr("_rels/.rels", '<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>')
    z.writestr("word/document.xml", '<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:rPr><w:sz w:val="96"/><w:color w:val="C0392B"/></w:rPr><w:t>KyDrive DOCX</w:t></w:r></w:p></w:body></w:document>')
EOF
printf '<!DOCTYPE html><script>alert(1)</script>' >"$work/page.html"

thumb photo.jpg "$work/photo.jpg" png
thumb page.html "$work/page.html" 415
if [ ${#editor[@]} -gt 0 ]; then
  thumb hello.docx "$work/hello.docx" png
  thumb paper.pdf "$work/paper.pdf" png
  thumb blank.pptx "$root/internal/api/templates/blank.pptx" png
else
  echo "SKIP: Office and PDF thumbnails (KYDRIVE_EDITOR_URL not set)"
fi
exit $failed
