# service-accounts

A service account holds a seeded Rancher API token that the engine keeps alive.
Once a token is older than `rotation_period`, the engine uses it to mint a
replacement with a `token_ttl` lifetime for the same Rancher user, stores the
new token, and deletes the old one. Rotation runs in the background on the
active node; failures are logged and retried on the next tick.

Roles reference a service account to mint short-lived tokens, and those tokens
inherit the service account user's Rancher RBAC.

The examples assume the engine is mounted at `rancher/`.

| Method | Path |
|--------|------|
| `POST` / `PUT` | `/rancher/service-accounts/:name` |
| `GET` | `/rancher/service-accounts/:name` |
| `DELETE` | `/rancher/service-accounts/:name` |
| `LIST` | `/rancher/service-accounts` |
| `POST` | `/rancher/service-accounts/:name/rotate` |

## Create or update a service account

### Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `name` | string | yes | | Name of the service account (in the URL). |
| `token` | string | on create | | Rancher API token to seed with (`ext/<name>:<secret>`). Write-only; writing it again re-seeds and restarts the rotation clock. |
| `token_name` | string | no | | `metadata.name` of the seed token, so the engine can delete it after the first rotation. Recommended. |
| `token_ttl` | duration | no | `2160h` (90d) | Lifetime requested for each replacement token. Must not exceed Rancher's `auth-token-max-ttl-minutes`. |
| `rotation_period` | duration | no | `1080h` (45d) | How long a token is used before it is rotated. Must be less than `token_ttl`. `0` disables automatic rotation. |

### Example

```sh
vault write rancher/service-accounts/admin \
    token="ext/token-abc12:<secret>" \
    token_name=token-abc12 \
    token_ttl=2160h \
    rotation_period=720h
```

## Read a service account

The token itself is never returned.

```sh
$ vault read rancher/service-accounts/admin
Key                Value
---                -----
last_rotated       2026-01-15T09:30:00Z
rotation_period    2592000
token_name         token-abc12
token_ttl          7776000
```

## List service accounts

```sh
vault list rancher/service-accounts
```

## Delete a service account

```sh
vault delete rancher/service-accounts/admin
```

The engine forgets the token but does not delete it from Rancher. Leases
already issued through roles that use this service account can no longer be
revoked by the engine; Rancher expires those tokens at their own TTL.

## Rotate now

Forces an immediate rotation: mints a new token with the current one, stores
it, and deletes the previous token.

```sh
$ vault write -f rancher/service-accounts/admin/rotate
Key             Value
---             -----
last_rotated    2026-01-20T11:02:41Z
token_name      vault-admin-q8w4z
```
