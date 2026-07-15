#!/usr/bin/env bash
#
# End-to-end test for vault-plugin-secrets-rancher.
#
# Builds the plugin, brings up a mock Rancher ext.cattle.io API plus both Vault
# and OpenBao, then drives the identical lifecycle against each engine to prove
# the same binary works on both:
#   configure -> service-account (seed) -> rotate -> role -> creds -> revoke.
#
# Select engines with ENGINES (default "vault openbao"), e.g. ENGINES=openbao.
#
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="${ROOT_DIR}/test/docker-compose.yml"
COMPOSE="docker compose -f ${COMPOSE_FILE}"
BINARY="vault-plugin-secrets-rancher"

SEED_TOKEN="seed-token"
RANCHER_ADDR="http://127.0.0.1:8443"   # mock rancher, from the host
MOUNT="rancher"
ENGINES="${ENGINES:-vault openbao}"

red()   { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
blue()  { printf '\033[34m==> %s\033[0m\n' "$*"; }

cleanup() { blue "Tearing down containers"; ${COMPOSE} down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT
fail() { red "FAIL: $*"; exit 1; }

wait_for() {
  local desc="$1"; shift
  local i=0
  until "$@" >/dev/null 2>&1; do
    i=$((i + 1))
    [ "$i" -ge "${WAIT_RETRIES:-90}" ] && fail "timed out waiting for ${desc}"
    sleep 2
  done
  green "ready: ${desc}"
}

# token_status NAME BEARER -> HTTP status of GET on the mock rancher token.
token_status() {
  curl -s -o /dev/null -w '%{http_code}' \
    -H "Authorization: Bearer $2" \
    "${RANCHER_ADDR}/apis/ext.cattle.io/v1/tokens/$1"
}

jq_field() { python3 -c "import sys,json;print(json.load(sys.stdin)$1)"; }

run_engine() {
  local engine="$1" container="$2" cli="$3"
  blue "[${engine}] exercising the plugin"
  ex() { ${COMPOSE} exec -T "${container}" "${cli}" "$@"; }

  local sha; sha="$(sha256sum "${ROOT_DIR}/dist/${BINARY}" | awk '{print $1}')"
  ex plugin register -sha256="${sha}" secret "${BINARY}" >/dev/null || true
  ex secrets disable "${MOUNT}" >/dev/null 2>&1 || true
  ex secrets enable -path="${MOUNT}" "${BINARY}" >/dev/null
  green "[${engine}] plugin registered and mounted"

  # The plugin runs inside the engine container, so it reaches rancher by name.
  ex write "${MOUNT}/config" rancher_url="http://rancher:8443" tls_skip_verify=true >/dev/null
  green "[${engine}] configured"

  # --- service account: seed + manual rotate ---
  ex write "${MOUNT}/service-accounts/admin" token="${SEED_TOKEN}" \
    token_ttl=2160h rotation_period=1080h >/dev/null
  local rot newname
  rot="$(ex write -format=json "${MOUNT}/service-accounts/admin/rotate" -force)"
  newname="$(printf '%s' "${rot}" | jq_field '["data"]["token_name"]')"
  [ -n "${newname}" ] || fail "[${engine}] rotate returned no token_name"
  green "[${engine}] service account rotated -> ${newname}"

  # --- role + dynamic creds ---
  ex write "${MOUNT}/roles/ci" service_account="admin" cluster_name="c-m-abc123" ttl=1h max_ttl=24h >/dev/null
  local json name lease tok
  json="$(ex read -format=json "${MOUNT}/creds/ci")"
  name="$(printf '%s' "${json}" | jq_field '["data"]["token_name"]')"
  lease="$(printf '%s' "${json}" | jq_field '["lease_id"]')"
  tok="$(printf '%s' "${json}" | jq_field '["data"]["token"]')"
  [ -n "${name}" ] || fail "[${engine}] no dynamic token returned"
  [ -n "${tok}" ] || fail "[${engine}] dynamic creds returned empty token value"
  # The rotated admin token (not the original seed) minted it; verify via that token.
  [ "$(token_status "${name}" "${tok}")" = "200" ] || fail "[${engine}] dynamic token ${name} not present in rancher"
  green "[${engine}] dynamic token ${name} issued (lease ${lease})"

  # revoke -> token deleted from rancher
  ex lease revoke "${lease}" >/dev/null
  sleep 2
  [ "$(token_status "${name}" "${SEED_TOKEN}")" = "404" ] || fail "[${engine}] revoked token still present in rancher"
  green "[${engine}] revoked token removed from rancher"

  green "[${engine}] PASSED"
}

blue "Building plugin for linux/amd64"
OS=linux ARCH=amd64 PLUGIN_DIR="${ROOT_DIR}/dist" make -C "${ROOT_DIR}" build

blue "Starting Docker stack (rancher + vault + openbao)"
${COMPOSE} up -d --build

wait_for "rancher" curl -fsS "${RANCHER_ADDR}/healthz"

for engine in ${ENGINES}; do
  case "${engine}" in
    vault)   wait_for "vault"   ${COMPOSE} exec -T vault   vault status -address=http://127.0.0.1:8200; run_engine vault vault vault ;;
    openbao) wait_for "openbao" ${COMPOSE} exec -T openbao bao   status -address=http://127.0.0.1:8200; run_engine openbao openbao bao ;;
    *) fail "unknown engine: ${engine}" ;;
  esac
done

green "ALL END-TO-END CHECKS PASSED (${ENGINES})"
