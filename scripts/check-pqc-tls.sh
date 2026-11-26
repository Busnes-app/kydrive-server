#!/usr/bin/env bash
# Refuse classical TLS overrides and verify hybrid PQ TLS (X25519MLKEM768) under default and FIPS modes.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

# 1. Non-test Go code must not set CurvePreferences (dropping Go's default X25519MLKEM768).
curve_hits=$(git grep -n 'CurvePreferences' -- '*.go' ':!*_test.go' || true)
if [ -n "$curve_hits" ]; then
  echo "CurvePreferences is forbidden in non-test Go code (use Go's default hybrid PQ TLS or ky-primitives/pqtls):" >&2
  echo "$curve_hits" >&2
  exit 1
fi

# 2. Non-test code, workflows, Dockerfiles, Compose and deployment files must not set tlsmlkem.
mlkem_hits=$(git grep -ni 'tlsmlkem' -- . ':!*_test.go' ':!*_test.py' ':!*.md' ':!scripts/check-pqc-tls.sh' || true)
if [ -n "$mlkem_hits" ]; then
  echo "tlsmlkem overrides are forbidden in non-test code, workflows, Dockerfiles and Compose:" >&2
  echo "$mlkem_hits" >&2
  exit 1
fi

# 3. Verify hybrid PQ TLS negotiation under both default and FIPS 140-3 profiles.
env -u GODEBUG go test -count=1 -run '^TestPQTLSHandshake$' ./internal/api
GODEBUG=fips140=on go test -count=1 -run '^TestPQTLSHandshake$' ./internal/api
