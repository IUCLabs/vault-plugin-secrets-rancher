package rancher

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

const rancherTokenType = "rancher_token"

func (b *rancherBackend) rancherTokenSecret() *framework.Secret {
	return &framework.Secret{
		Type: rancherTokenType,
		Fields: map[string]*framework.FieldSchema{
			"token": {
				Type:        framework.TypeString,
				Description: "The Rancher API token value.",
			},
			"token_name": {
				Type:        framework.TypeString,
				Description: "The ext.cattle.io Token resource name.",
			},
			"cluster_name": {
				Type:        framework.TypeString,
				Description: "The cluster the token is scoped to (empty = full scope).",
			},
		},
		Revoke: b.secretRevoke,
		Renew:  b.secretRenew,
	}
}

// secretRevoke deletes the minted Rancher token via the ext.cattle.io API, using
// the service account that issued it.
func (b *rancherBackend) secretRevoke(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	tokenName, err := internalString(req.Secret.InternalData, "token_name")
	if err != nil {
		return nil, err
	}
	saName, err := internalString(req.Secret.InternalData, "service_account")
	if err != nil {
		return nil, err
	}

	sa, err := b.getServiceAccount(ctx, req.Storage, saName)
	if err != nil {
		return nil, err
	}
	if sa == nil {
		// The issuing service account is gone; nothing left that could revoke
		// the token here. Rancher will expire it at its own TTL.
		return nil, fmt.Errorf("service account %q no longer exists; cannot revoke token %q", saName, tokenName)
	}

	client, err := b.clientFor(ctx, req.Storage, sa.Token)
	if err != nil {
		return nil, err
	}
	if err := client.DeleteToken(ctx, tokenName); err != nil {
		return nil, fmt.Errorf("revoking rancher token %q: %w", tokenName, err)
	}
	return nil, nil
}

// secretRenew extends the Vault lease; the token material is unchanged. Note the
// Rancher-side token TTL is fixed at creation, so a renewed lease should stay
// within the token's original TTL (bounded by the role max_ttl).
func (b *rancherBackend) secretRenew(_ context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	return &logical.Response{Secret: req.Secret}, nil
}

func internalString(data map[string]interface{}, key string) (string, error) {
	raw, ok := data[key]
	if !ok {
		return "", fmt.Errorf("secret is missing internal %s data", key)
	}
	s, ok := raw.(string)
	if !ok {
		return "", errors.New("secret internal " + key + " data is not a string")
	}
	return s, nil
}
