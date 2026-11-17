# creds

Reading `creds/<role>` mints a new Rancher API token from the role's service
account and returns it with a lease. Revoking the lease deletes the token from
Rancher.

The examples assume the engine is mounted at `rancher/`.

| Method | Path |
|--------|------|
| `GET` / `POST` | `/rancher/creds/:name` |

## Generate a token

### Parameters

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `name` | string | yes | Name of the [role](roles.md) (in the URL). |

### Response

| Field | Description |
|-------|-------------|
| `token` | The bearer token, `ext/<token_name>:<secret>`. Use it as `Authorization: Bearer <token>`. |
| `token_name` | The `ext.cattle.io` Token resource name. |
| `cluster_name` | Cluster the token is scoped to; empty for full scope. |

### Example

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

Use it with `kubectl` through the Rancher proxy:

```sh
TOKEN=$(vault read -field=token rancher/creds/ci)
kubectl --server=https://rancher.example.com/k8s/clusters/c-m-abc123 \
    --token="$TOKEN" get nodes
```

## Renew and revoke

```sh
vault lease renew rancher/creds/ci/kCtk2Fq9...
vault lease revoke rancher/creds/ci/kCtk2Fq9...
```

The Rancher-side token lifetime is fixed when it is minted (the role's `ttl`).
Renewing extends the lease, not the token, so keep renewals within that
lifetime.
