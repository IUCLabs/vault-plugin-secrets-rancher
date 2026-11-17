# config

Configures how the engine reaches the Rancher server. Tokens are not stored
here; they live on [service accounts](service-accounts.md).

The examples assume the engine is mounted at `rancher/`.

| Method | Path |
|--------|------|
| `POST` / `PUT` | `/rancher/config` |
| `GET` | `/rancher/config` |
| `DELETE` | `/rancher/config` |

## Write config

### Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `rancher_url` | string | yes | | Base URL of the Rancher server, e.g. `https://rancher.example.com`. |
| `ca_cert` | string | no | | PEM CA certificate that signed the Rancher server's TLS certificate. |
| `tls_skip_verify` | bool | no | `false` | Skip TLS verification of the Rancher server. Not recommended. |
| `request_timeout_seconds` | int | no | `30` | HTTP timeout in seconds for calls to Rancher. |

Updates are partial: parameters you omit keep their current value.

### Example

```sh
vault write rancher/config \
    rancher_url=https://rancher.example.com \
    ca_cert=@rancher-ca.pem \
    request_timeout_seconds=60
```

## Read config

`ca_cert` is not returned.

```sh
$ vault read rancher/config
Key                        Value
---                        -----
rancher_url                https://rancher.example.com
request_timeout_seconds    60
tls_skip_verify            false
```

## Delete config

```sh
vault delete rancher/config
```

Service accounts and roles are kept, but no tokens can be minted or rotated
until the config is written again.
