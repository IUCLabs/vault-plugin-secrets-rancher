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

Download a binary from the
[releases page](https://github.com/IUCLabs/vault-plugin-secrets-rancher/releases),
or build it:

```sh
git clone https://github.com/IUCLabs/vault-plugin-secrets-rancher
cd vault-plugin-secrets-rancher
make build    # writes dist/vault-plugin-secrets-rancher
```

Copy `dist/vault-plugin-secrets-rancher` into the directory set as
`plugin_directory` in the server config, then register and enable it. The
commands are identical for OpenBao — use `bao` in place of `vault`.

```sh
SHA256=$(sha256sum dist/vault-plugin-secrets-rancher | cut -d' ' -f1)

vault plugin register -sha256="$SHA256" secret vault-plugin-secrets-rancher
vault secrets enable -path=rancher vault-plugin-secrets-rancher
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
