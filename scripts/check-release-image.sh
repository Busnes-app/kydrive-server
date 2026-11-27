#!/usr/bin/env bash
# Usage: check-release-image.sh <image>
# Fails unless the image is a release build: its binary carries no end-to-end build tag, and a
# complete multi-tenant configuration still meets the startup gate refusal
# (KyCalendar docs/superpowers/specs/2026-11-24-multi-tenant-e2e-design.md, Mechanism).
set -euo pipefail
image=${1:?usage: check-release-image.sh <image>}
dir=$(mktemp -d)
cid=$(docker create "$image")
run="kydrive-release-check-$$"
trap 'docker rm -f "$cid" "$run" >/dev/null 2>&1; rm -rf "$dir"' EXIT
docker cp -q "$cid:/app/kydrive-server" "$dir/kydrive-server"

info=$(go version -m "$dir/kydrive-server")
grep -q -P '^\tpath\tgithub\.com/Busnes-app/kydrive-server/cmd/server$' <<<"$info" ||
  { echo "::error::$image: no build info for kydrive-server:" >&2; echo "$info" >&2; exit 1; }
if tags=$(grep -P '^\tbuild\t-tags=' <<<"$info") && grep -q -w kye2e <<<"$tags"; then
  echo "::error::$image was built with the end-to-end test tag: $tags" >&2
  exit 1
fi

# A complete multi-tenant configuration, issuer under .test as the test build wants: only the
# startup gate may refuse it. --network none: nothing it could reach exists anyway. Run as the
# host UID so the 0600 identity key required by keyfile.Load stays readable on any runner.
printf 'cd%.0s' {1..32} > "$dir/identity.key" && chmod 0600 "$dir/identity.key"
mkdir -p "$dir/data" && chmod 0700 "$dir/data"
set +e
out=$(timeout 60 docker run --rm --name "$run" --network none --memory 256m \
  --user "$(id -u):$(id -g)" \
  -v "$dir/identity.key:/run/identity.key:ro" \
  -v "$dir/data:/tmp/kydrive-data" \
  -e KY_ENV=production \
  -e KY_DATA_DIR=/tmp/kydrive-data \
  -e KY_DB_DRIVER=postgres \
  -e KY_DB_DSN=postgres://drive:pw@db/drive \
  -e KY_SESSION_SECRET="$(printf 's%.0s' {1..32})" \
  -e KY_SCIM_ENABLED=false \
  -e KYDRIVE_TENANCY=multi \
  -e KYDRIVE_STAFF_ORG=00000000-0000-0000-0000-000000000099 \
  -e KY_ENCRYPTION_KEY="$(printf 'ab%.0s' {1..32})" \
  -e KYDRIVE_IDENTITY_KEY_FILE=/run/identity.key \
  -e KY_KYSIGNON_ISSUER=https://id.e2e.test \
  "$image" 2>&1)
code=$?
set -e
docker rm -f "$run" >/dev/null 2>&1 || true
want='KYDRIVE_TENANCY=multi is not available in this release'
if [ "$code" -eq 0 ] || ! grep -qF "$want" <<<"$out"; then
  echo "::error::$image did not refuse a complete multi-tenant configuration at the startup gate (exit $code):" >&2
  echo "$out" >&2
  exit 1
fi
echo "$image: release build, multi-tenant startup refused"
