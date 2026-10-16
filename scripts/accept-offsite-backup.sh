#!/usr/bin/env bash
# Acceptance: offsite bulk backup through real servers. Starts rest-server in --append-only mode
# behind a password and an S3 server (SeaweedFS) with access keys, then, for each, backs a drive
# up, runs the integrity check, and restores on a "fresh host" from the opened capsule alone
# (TestRestoreFromTheCapsuleAlone). Finally it shows the append-only server refusing a delete.
# Usage: scripts/accept-offsite-backup.sh   (needs docker, restic, go, openssl, curl, jq)
# The servers speak plain HTTP on loopback, so the run uses KY_ENV=development; production
# repositories must be https://. Owns its containers and scratch directory.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d /var/tmp/kyoffsite.XXXXXX)
rest=kyaccept-rest-$$
s3=kyaccept-s3-$$
failed=0
cleanup() { docker rm -f "$rest" "$s3" >/dev/null 2>&1 || true; rm -rf "$work"; }
trap cleanup EXIT
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; failed=1; }

user=kydrive
password=$(openssl rand -hex 16)
# rest-server checks bcrypt entries; its image ships htpasswd to write one.
docker run --rm --entrypoint htpasswd restic/rest-server -nbB "$user" "$password" | head -1 >"$work/htpasswd"
access=$(openssl rand -hex 10)
secret=$(openssl rand -hex 20)
printf '{"identities":[{"name":"kydrive","credentials":[{"accessKey":"%s","secretKey":"%s"}],"actions":["Admin","Read","Write","List","Tagging"]}]}\n' "$access" "$secret" >"$work/s3.json"
chmod 644 "$work/htpasswd" "$work/s3.json" # read by the containers' own users

docker run -d --rm --name "$rest" -p 127.0.0.1:8401:8000 -e OPTIONS=--append-only \
  -v "$work/htpasswd:/data/.htpasswd:ro" restic/rest-server >/dev/null
docker run -d --rm --name "$s3" -p 127.0.0.1:8433:8333 -v "$work/s3.json:/etc/s3.json:ro" \
  chrislusf/seaweedfs server -dir=/data -s3 -s3.config=/etc/s3.json >/dev/null
for port in 8401 8433; do
  curl -s --retry 60 --retry-delay 1 --retry-all-errors -o /dev/null "http://127.0.0.1:$port/" || { echo "server on $port did not start"; exit 1; }
done

install -m 600 /dev/null "$work/rest.env"
printf 'RESTIC_REST_USERNAME=%s\nRESTIC_REST_PASSWORD=%s\n' "$user" "$password" >"$work/rest.env"
install -m 600 /dev/null "$work/s3.env"
printf 'AWS_ACCESS_KEY_ID=%s\nAWS_SECRET_ACCESS_KEY=%s\nAWS_DEFAULT_REGION=us-east-1\n' "$access" "$secret" >"$work/s3.env"

roundtrip() { # name repository credentials
  if (cd "$root" && KY_ENV=development KYDRIVE_LIVE_BULK_REPOSITORY="$2" KYDRIVE_LIVE_BULK_CREDENTIALS_FILE="$3" \
      go test -count=1 -run 'TestRestoreFromTheCapsuleAlone$' ./internal/backup/ >"$work/$1.log" 2>&1) &&
     ! grep -q -- '--- SKIP' "$work/$1.log"; then
    pass "$1: backup, integrity check, restore from the capsule alone"
  else
    fail "$1: $(tail -5 "$work/$1.log")"
  fi
}
repo=kydrive-$RANDOM
roundtrip rest-server "rest:http://127.0.0.1:8401/$repo" "$work/rest.env"
roundtrip s3 "s3:http://127.0.0.1:8433/kydrive-$RANDOM" "$work/s3.env"

# Append-only: the repository the run just wrote cannot lose a key, so a compromised drive host
# cannot destroy its own backups. A wrong password is refused outright.
key=$(curl -fs -u "$user:$password" "http://127.0.0.1:8401/$repo/keys/" | jq -r '.[0]')
code=$(curl -s -o /dev/null -w '%{http_code}' -u "$user:$password" -X DELETE "http://127.0.0.1:8401/$repo/keys/$key")
if [ "$code" = 403 ]; then pass "append-only rest-server refuses a delete (403)"; else fail "append-only delete answered $code"; fi
code=$(curl -s -o /dev/null -w '%{http_code}' -u "$user:wrong" "http://127.0.0.1:8401/$repo/keys/")
if [ "$code" = 401 ]; then pass "rest-server refuses a wrong password (401)"; else fail "wrong password answered $code"; fi
exit $failed
