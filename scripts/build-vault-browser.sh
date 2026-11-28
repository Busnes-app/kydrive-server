#!/usr/bin/env bash
set -euo pipefail
if [[ ($# != 4 && $# != 6) || $1 != /* ]]; then
  printf 'Usage: %s /absolute/output KYDRIVE_ORIGIN WITNESS_ORIGIN WITNESS_PUBLIC_KEY [CRYPTPAD_ORIGIN GATEWAY_ORIGIN]\n' "$0" >&2
  exit 2
fi
output=$1
cd -- "$(dirname -- "$0")/.."
mkdir -p -- "$output"
GOOS=js GOARCH=wasm CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$output/vault.wasm" ./cmd/vault-browser
cp -- "$(go env GOROOT)/lib/wasm/wasm_exec.js" "$output/wasm_exec.js"
cp -- deploy/vault-browser/{index.html,vault.mjs,office.mjs} "$output/"
python3 - "$output/config.json" "$2" "$3" "$4" "${5:-}" "${6:-}" <<'PY'
import json
import sys

path, drive, witness, witness_key, cryptpad, gateway = sys.argv[1:]
config = {'drive': drive, 'witness': witness, 'witnessKey': witness_key}
if cryptpad and gateway:
    config.update(cryptpad=cryptpad, gateway=gateway)
with open(path, 'w', encoding='utf-8') as output:
    json.dump(config, output)
PY
