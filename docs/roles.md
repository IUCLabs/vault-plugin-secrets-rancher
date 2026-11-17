# roles

A role describes the tokens minted by [`creds/<role>`](creds.md): which
service account mints them, which cluster they are scoped to, and their lease
TTLs.

The examples assume the engine is mounted at `rancher/`.

| Method | Path |
|--------|------|
| `POST` / `PUT` | `/rancher/roles/:name` |
| `GET` | `/rancher/roles/:name` |
| `DELETE` | `/rancher/roles/:name` |
| `LIST` | `/rancher/roles` |

## Create or update a role

### Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `name` | string | yes | | Name of the role (in the URL). |
| `service_account` | string | yes | | [Service account](service-accounts.md) used to mint tokens. Must exist. Minted tokens inherit its user's RBAC. |
| `cluster_name` | string | no | | Downstream cluster ID (e.g. `c-m-abc123`) to scope tokens to. Empty means full Rancher-server scope. |
| `description` | string | no | `vault dynamic token (role "<name>")` | Description set on each minted Rancher token. |
| `ttl` | duration | no | mount default | Default lease TTL. Also used as the Rancher-side token lifetime. |
| `max_ttl` | duration | no | mount max | Maximum lease TTL. `ttl` must not exceed it. |

Updates are partial: parameters you omit keep their current value.

### Example

```sh
vault write rancher/roles/ci \
    service_account=admin \
    cluster_name=c-m-abc123 \
    description="CI pipeline token" \
    ttl=1h \
    max_ttl=8h
```

## Read a role

```sh
$ vault read rancher/roles/ci
Key                Value
---                -----
cluster_name       c-m-abc123
description        CI pipeline token
max_ttl            28800
service_account    admin
ttl                3600
```

## List roles

```sh
vault list rancher/roles
```

## Delete a role

```sh
vault delete rancher/roles/ci
```
