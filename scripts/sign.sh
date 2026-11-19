#!/usr/bin/env bash
# Detached binary signature of $1 into $2 via the Vault gpg engine.
set -euo pipefail
b=$(base64 -w0 "$1")
jq -n --arg input "$b" '{input: $input, format: "base64"}' \
  | curl -sSf --cacert "${VAULT_CACERT:-/etc/vault-ca/ca.crt}" \
      -H "X-Vault-Token: ${VAULT_TOKEN}" -d @- \
      "${VAULT_ADDR}/v1/gpg/sign/iuclabs-release" \
  | jq -er .data.signature | base64 -d > "$2"
