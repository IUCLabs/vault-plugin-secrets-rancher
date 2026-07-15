# vault-plugin-secrets-rancher

A Vault / OpenBao secrets engine that manages **Rancher API tokens** through
Rancher's public `tokens.ext.cattle.io` API.

## Why

Rancher caps API-token lifetimes (commonly 90 days via
`auth-token-max-ttl-minutes`). A long-lived automation token therefore expires
and breaks things. This engine solves that two ways:

- **Seeded root rotation** — you seed a *service account* with a Rancher token.
  The engine rotates it on a schedule (default: every 45 days, minting a fresh
  90-day token) using the current token to create its replacement, so the
  credential never lapses. Rancher only lets you mint tokens for *your own*
  user, which is exactly what rotation needs.
- **Dynamic, scoped tokens** — a *role* mints short-lived tokens on demand, each
  bound to a Vault lease and deleted from Rancher on revoke.

### Scope caveat

Rancher tokens inherit the **creating user's RBAC**; the only per-token
restrictions are **cluster scope** (`cluster_name`) and **TTL**. For genuine
least-privilege, create a purpose-built Rancher user per privilege level, seed a
service account with each, and point roles at the right one.

## Paths

| Path | Description |
|------|-------------|
| `config` | Rancher URL + TLS settings. |
| `service-accounts/<name>` | Seed a Rancher token; set `token_ttl` / `rotation_period`. Auto-rotated. |
| `service-accounts/<name>/rotate` | Force an immediate rotation. |
| `roles/<name>` | Mint policy: `service_account`, `cluster_name`, `ttl`, `max_ttl`, `description`. |
| `creds/<role>` | Read to mint a short-lived, lease-bound token. |

## Usage

```sh
vault secrets enable -path=rancher vault-plugin-secrets-rancher

vault write rancher/config rancher_url=https://rancher.example.com ca_cert=@rancher-ca.pem

# Seed a service account (rotate every 45d, 90d token TTL are the defaults).
vault write rancher/service-accounts/admin token="token-abcde:xxxxx"

# A role that mints 1h tokens scoped to one cluster.
vault write rancher/roles/ci service_account=admin \
  cluster_name=c-m-abc123 ttl=1h max_ttl=8h

# Mint one.
vault read rancher/creds/ci
```

## Development

```sh
make build      # build the plugin binary into ./dist
make test       # unit tests
make e2e        # full lifecycle vs mock Rancher on Vault + OpenBao (Docker)
make rpm        # build Vault + OpenBao RPMs via nfpm
```

Releases are tag-driven (`make patch|minor|major`): a Woodpecker pipeline builds
the RPMs and uploads them to the internal artifactapi yum repo.
