#!/usr/bin/env bash
#
# Package the (already built) plugin binary into RPMs with nfpm. Builds one RPM
# per target server: Vault (/opt/vault-plugins) and OpenBao (/opt/openbao-plugins).
# Usage: scripts/build-rpm.sh [version]   (version defaults to $CI_COMMIT_TAG)
#
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

VERSION="${1:-${CI_COMMIT_TAG:-0.0.0-dev}}"
VERSION="${VERSION#v}"
BINARY="vault-plugin-secrets-rancher"
DIST="dist"

if [ ! -f "${DIST}/${BINARY}" ]; then
  echo "ERROR: ${DIST}/${BINARY} not found; run 'make build' first" >&2
  exit 1
fi

export PACKAGE_VERSION="${VERSION}"
export PACKAGE_RELEASE="1"
export PACKAGE_ARCH="amd64"
export PACKAGE_PLATFORM="linux"
export PACKAGE_DESCRIPTION="Vault/OpenBao secrets engine for Rancher API tokens (tokens.ext.cattle.io)"
export PACKAGE_MAINTAINER="IUCLabs"
export PACKAGE_HOMEPAGE="https://github.com/IUCLabs/vault-plugin-secrets-rancher"
export PACKAGE_LICENSE="MIT"

build_flavor() {
  export PACKAGE_NAME="$1"
  export PACKAGE_PLUGIN_DIR="$2"
  export PACKAGE_PREINSTALL="${DIST}/preinstall-${PACKAGE_NAME}.sh"
  envsubst '${PACKAGE_PLUGIN_DIR}' < packaging/scripts/preinstall.sh.tmpl > "${PACKAGE_PREINSTALL}"
  envsubst < packaging/nfpm.yaml > "${DIST}/nfpm-${PACKAGE_NAME}.yaml"
  nfpm pkg --config "${DIST}/nfpm-${PACKAGE_NAME}.yaml" --target "${DIST}" --packager rpm
}

build_flavor "vault-plugin-secrets-rancher"   "/opt/vault-plugins"
build_flavor "openbao-plugin-secrets-rancher" "/opt/openbao-plugins"

echo "Built:"
ls -1 "${DIST}"/*.rpm
