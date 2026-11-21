# vault-plugin-secrets-rancher

A HashiCorp Vault / OpenBao secrets engine that manages Rancher API tokens through
Rancher's `tokens.ext.cattle.io` API. It keeps a seeded token alive by rotating
it before Rancher's TTL cap, and mints short-lived, lease-bound tokens on demand.

To manage this engine with Terraform, see
[terraform-provider-vault-secrets-rancher](https://github.com/IUCLabs/terraform-provider-vault-secrets-rancher).

## Overview

Rancher caps API-token lifetimes (commonly 90 days via
`auth-token-max-ttl-minutes`), so a long-lived automation token eventually
expires. This engine handles that two ways:

- **Service accounts** hold a seeded Rancher token. The engine rotates it on a
  schedule (by default every 45 days, requesting a 90-day replacement) using the
  current token to mint its successor, so the credential never lapses.
- **Roles** mint short-lived tokens from a service account. Each read of
  `creds/<role>` returns a unique token bound to a lease, and the token is
  deleted from Rancher when the lease is revoked.

Rancher tokens inherit the RBAC of the user that created them; the only
per-token restrictions are cluster scope (`cluster_name`) and TTL. For
least-privilege, create a dedicated Rancher user per privilege level, seed a
service account with each, and point roles at the right one.

## Requirements

- HashiCorp Vault or OpenBao (tested against Vault 1.18 and the latest OpenBao)
- A Rancher release that serves the `ext.cattle.io/v1` Token API
- Go 1.25+ to build from source

## Installation

Release assets download through artifactapi's GitHub proxy, which serves
`https://artifactapi.k8s.syd1.au.unkin.net/api/v1/remote/github/IUCLabs/vault-plugin-secrets-rancher/releases/download/<tag>/<file>`.
Each method below ends with `SHA256` set for [registration](#register-and-enable).
With OpenBao, use `bao` in place of `vault`.

```sh
TAG=vX.Y.Z  # the release to install
V=${TAG#v}
BASE=https://artifactapi.k8s.syd1.au.unkin.net/api/v1/remote/github/IUCLabs/vault-plugin-secrets-rancher/releases/download/$TAG
```

### RPM

`vault-plugin-secrets-rancher` installs `/usr/libexec/vault-plugins/vault-plugin-secrets-rancher`;
`openbao-plugin-secrets-rancher` installs it in `/usr/libexec/openbao-plugins/`.
Set the server's `plugin_directory` to that directory.

```sh
dnf install vault-plugin-secrets-rancher  # from a yum repo carrying it, or from the release:
dnf install "$BASE/vault-plugin-secrets-rancher-$V-1.x86_64.rpm"  # openbao-plugin-secrets-rancher-$V-1.x86_64.rpm for OpenBao

SHA256=$(sha256sum /usr/libexec/vault-plugins/vault-plugin-secrets-rancher | cut -d' ' -f1)
```

### Released binary

`vault-plugin-secrets-rancher_<version>_SHA256SUMS` lists the binary and the RPMs, and
`SHA256SUMS.sig` is its detached signature by the IUCLabs release GPG key.

```sh
curl -fLO "$BASE/vault-plugin-secrets-rancher_${V}_linux_amd64"
curl -fLO "$BASE/vault-plugin-secrets-rancher_${V}_SHA256SUMS"
curl -fLO "$BASE/vault-plugin-secrets-rancher_${V}_SHA256SUMS.sig"
gpg --verify "vault-plugin-secrets-rancher_${V}_SHA256SUMS.sig" "vault-plugin-secrets-rancher_${V}_SHA256SUMS"
sha256sum --check --ignore-missing "vault-plugin-secrets-rancher_${V}_SHA256SUMS"

install -m 0755 "vault-plugin-secrets-rancher_${V}_linux_amd64" <plugin_directory>/vault-plugin-secrets-rancher
SHA256=$(sha256sum <plugin_directory>/vault-plugin-secrets-rancher | cut -d' ' -f1)
```

### Kubernetes image volume

`ghcr.io/iuclabs/vault-plugin-secrets-rancher:<tag>` is a `FROM scratch` image holding only
`/vault-plugin-secrets-rancher`. Mount it read-only into the Vault/OpenBao container with an
[image volume](https://kubernetes.io/docs/concepts/storage/volumes/#image):
Kubernetes 1.31+ with the `ImageVolume` feature gate (beta, on by default from
1.33). Vault and OpenBao only run a binary that sits directly in
`plugin_directory`, not in a subdirectory, so mount the file itself with
`subPath`, which on image volumes needs 1.33+.

```yaml
spec:
  containers:
    - name: vault
      volumeMounts:
        - name: plugin-rancher
          mountPath: /vault/plugins/vault-plugin-secrets-rancher  # <plugin_directory>/vault-plugin-secrets-rancher
          subPath: vault-plugin-secrets-rancher
          readOnly: true
  volumes:
    - name: plugin-rancher
      image:
        reference: ghcr.io/iuclabs/vault-plugin-secrets-rancher:vX.Y.Z
        pullPolicy: IfNotPresent
```

The image holds the released binary, so its sha256 is the `linux_amd64` line
of `SHA256SUMS`:

```sh
SHA256=$(curl -fsSL "$BASE/vault-plugin-secrets-rancher_${V}_SHA256SUMS" | awk '/_linux_amd64$/ {print $1}')
```

### From source

```sh
make build  # writes ./dist/vault-plugin-secrets-rancher
```

Copy it into `plugin_directory` and take `SHA256` from `sha256sum` as above.

### Register and enable

```sh
vault plugin register -sha256="$SHA256" secret vault-plugin-secrets-rancher
vault secrets enable -path=rancher vault-plugin-secrets-rancher
```

After replacing the binary, register the new sha256 and reload the plugin:

```sh
vault write sys/plugins/reload/backend plugin=vault-plugin-secrets-rancher
```

## Configuration

Point the engine at Rancher:

```sh
vault write rancher/config \
    rancher_url=https://rancher.example.com \
    ca_cert=@rancher-ca.pem
```

Seed a service account with an existing Rancher token. Pass `token_name` (the
token's `metadata.name`) so the engine can delete the seed token after the first
rotation:

```sh
vault write rancher/service-accounts/admin \
    token="ext/token-abc12:<secret>" \
    token_name=token-abc12
```

## Usage

Create a role that mints one-hour tokens scoped to a single downstream cluster:

```sh
vault write rancher/roles/ci \
    service_account=admin \
    cluster_name=c-m-abc123 \
    ttl=1h \
    max_ttl=8h
```

Mint a token:

```sh
$ vault read rancher/creds/ci
Key                Value
---                -----
lease_id           rancher/creds/ci/kCtk2Fq9...
lease_duration     1h
lease_renewable    true
cluster_name       c-m-abc123
token              ext/vault-ci-1a2b3c4d-x7k2p:...
token_name         vault-ci-1a2b3c4d-x7k2p
```

Revoking the lease deletes the token from Rancher:

```sh
vault lease revoke rancher/creds/ci/kCtk2Fq9...
```

Force an out-of-schedule rotation of a service account:

```sh
vault write -f rancher/service-accounts/admin/rotate
```

## Documentation

The full path reference lives in [`docs/`](docs/):

- [`config`](docs/config.md) — Rancher connection settings
- [`service-accounts`](docs/service-accounts.md) — seeded, auto-rotated tokens
- [`roles`](docs/roles.md) — token-minting roles
- [`creds`](docs/creds.md) — dynamic token issuance

## Development

```sh
make build    # build the plugin into dist/
make test     # unit tests
make e2e      # full lifecycle against a mock Rancher on Vault and OpenBao (Docker)
```

## License

[MIT](LICENSE)
