package rancher

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/go-uuid"
	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

func pathCredentials(b *rancherBackend) *framework.Path {
	return &framework.Path{
		Pattern: "creds/" + framework.GenericNameRegex("name"),
		DisplayAttrs: &framework.DisplayAttributes{
			OperationPrefix: "rancher",
			OperationSuffix: "credentials",
		},
		Fields: map[string]*framework.FieldSchema{
			"name": {
				Type:        framework.TypeLowerCaseString,
				Description: "Name of the role to mint a token for.",
				Required:    true,
			},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation:   &framework.PathOperation{Callback: b.pathCredentialsRead},
			logical.UpdateOperation: &framework.PathOperation{Callback: b.pathCredentialsRead},
		},
		HelpSynopsis:    "Mint a short-lived Rancher token from a role.",
		HelpDescription: "Reading this path mints a new, lease-bound Rancher API token via the role's service account; the token is deleted from Rancher when the lease is revoked.",
	}
}

func (b *rancherBackend) pathCredentialsRead(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	roleName := data.Get("name").(string)
	role, err := b.getRole(ctx, req.Storage, roleName)
	if err != nil {
		return nil, err
	}
	if role == nil {
		return logical.ErrorResponse("role %q does not exist", roleName), nil
	}

	sa, err := b.getServiceAccount(ctx, req.Storage, role.ServiceAccount)
	if err != nil {
		return nil, err
	}
	if sa == nil {
		return logical.ErrorResponse("service account %q referenced by role %q does not exist", role.ServiceAccount, roleName), nil
	}

	client, err := b.clientFor(ctx, req.Storage, sa.Token)
	if err != nil {
		return nil, err
	}

	ttl, maxTTL := b.resolveTTLs(role.TTL, role.MaxTTL)

	suffix, err := uuid.GenerateUUID()
	if err != nil {
		return nil, fmt.Errorf("generating token name suffix: %w", err)
	}

	description := role.Description
	if description == "" {
		description = fmt.Sprintf("vault dynamic token (role %q)", roleName)
	}

	value, tokenName, err := client.MintToken(ctx, mintRequest{
		GenerateName: fmt.Sprintf("vault-%s-%s-", roleName, suffix[:8]),
		Description:  description,
		TTL:          ttl,
		ClusterName:  role.ClusterName,
	})
	if err != nil {
		return nil, fmt.Errorf("minting rancher token: %w", err)
	}

	internal := map[string]interface{}{
		"token_name":      tokenName,
		"service_account": role.ServiceAccount,
	}
	external := map[string]interface{}{
		"token":        value,
		"token_name":   tokenName,
		"cluster_name": role.ClusterName,
	}

	resp := b.Secret(rancherTokenType).Response(external, internal)
	resp.Secret.TTL = ttl
	resp.Secret.MaxTTL = maxTTL
	resp.Secret.Renewable = true
	return resp, nil
}

// resolveTTLs clamps a role's TTL/MaxTTL against the mount and system limits.
func (b *rancherBackend) resolveTTLs(roleTTL, roleMaxTTL time.Duration) (ttl, maxTTL time.Duration) {
	sysMaxTTL := b.System().MaxLeaseTTL()
	maxTTL = roleMaxTTL
	if maxTTL <= 0 || maxTTL > sysMaxTTL {
		maxTTL = sysMaxTTL
	}
	ttl = roleTTL
	if ttl <= 0 {
		ttl = b.System().DefaultLeaseTTL()
	}
	if ttl > maxTTL {
		ttl = maxTTL
	}
	return ttl, maxTTL
}
