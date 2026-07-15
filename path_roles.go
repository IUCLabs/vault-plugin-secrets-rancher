package rancher

import (
	"context"
	"errors"
	"time"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

const roleStoragePrefix = "role/"

// rancherRole mints short-lived Rancher tokens from a service account. Each read
// of creds/<name> produces a unique, lease-bound token.
type rancherRole struct {
	// ServiceAccount is the seeded service account whose token mints the creds,
	// and whose RBAC the minted token inherits.
	ServiceAccount string `json:"service_account"`
	// ClusterName scopes minted tokens to a single downstream cluster (empty =
	// full Rancher-server scope).
	ClusterName string `json:"cluster_name"`
	// Description is applied to each minted token (helps auditing in Rancher).
	Description string        `json:"description"`
	TTL         time.Duration `json:"ttl"`
	MaxTTL      time.Duration `json:"max_ttl"`
}

func pathRole(b *rancherBackend) *framework.Path {
	return &framework.Path{
		Pattern: "roles/" + framework.GenericNameRegex("name"),
		DisplayAttrs: &framework.DisplayAttributes{
			OperationPrefix: "rancher",
			OperationSuffix: "role",
		},
		Fields: map[string]*framework.FieldSchema{
			"name": {
				Type:        framework.TypeLowerCaseString,
				Description: "Name of the role.",
				Required:    true,
			},
			"service_account": {
				Type:        framework.TypeString,
				Description: "Service account (seeded token) used to mint credentials for this role. Its user's RBAC is inherited by minted tokens.",
				Required:    true,
			},
			"cluster_name": {
				Type:        framework.TypeString,
				Description: "Downstream cluster the minted tokens are scoped to (empty = full Rancher-server scope).",
			},
			"description": {
				Type:        framework.TypeString,
				Description: "Description applied to each minted Rancher token.",
			},
			"ttl": {
				Type:        framework.TypeDurationSecond,
				Description: "Default lease TTL for tokens minted from this role.",
			},
			"max_ttl": {
				Type:        framework.TypeDurationSecond,
				Description: "Maximum lease TTL for tokens minted from this role.",
			},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation:   &framework.PathOperation{Callback: b.pathRoleRead},
			logical.CreateOperation: &framework.PathOperation{Callback: b.pathRoleWrite},
			logical.UpdateOperation: &framework.PathOperation{Callback: b.pathRoleWrite},
			logical.DeleteOperation: &framework.PathOperation{Callback: b.pathRoleDelete},
		},
		ExistenceCheck:  b.pathRoleExistenceCheck,
		HelpSynopsis:    "Manage roles that mint short-lived Rancher tokens.",
		HelpDescription: "Each read of creds/<name> mints a unique, lease-bound Rancher token via the role's service account.",
	}
}

func pathRolesList(b *rancherBackend) *framework.Path {
	return &framework.Path{
		Pattern: "roles/?$",
		DisplayAttrs: &framework.DisplayAttributes{
			OperationPrefix: "rancher",
			OperationSuffix: "roles",
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ListOperation: &framework.PathOperation{Callback: b.pathRolesList},
		},
		HelpSynopsis:    "List roles.",
		HelpDescription: "List the token-minting roles configured on this backend.",
	}
}

func (b *rancherBackend) pathRoleExistenceCheck(ctx context.Context, req *logical.Request, data *framework.FieldData) (bool, error) {
	role, err := b.getRole(ctx, req.Storage, data.Get("name").(string))
	if err != nil {
		return false, err
	}
	return role != nil, nil
}

func (b *rancherBackend) pathRoleRead(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	role, err := b.getRole(ctx, req.Storage, data.Get("name").(string))
	if err != nil {
		return nil, err
	}
	if role == nil {
		return nil, nil
	}
	return &logical.Response{
		Data: map[string]interface{}{
			"service_account": role.ServiceAccount,
			"cluster_name":    role.ClusterName,
			"description":     role.Description,
			"ttl":             int64(role.TTL.Seconds()),
			"max_ttl":         int64(role.MaxTTL.Seconds()),
		},
	}, nil
}

func (b *rancherBackend) pathRoleWrite(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := data.Get("name").(string)
	role, err := b.getRole(ctx, req.Storage, name)
	if err != nil {
		return nil, err
	}
	if role == nil {
		role = &rancherRole{}
	}

	if v, ok := data.GetOk("service_account"); ok {
		role.ServiceAccount = v.(string)
	}
	if v, ok := data.GetOk("cluster_name"); ok {
		role.ClusterName = v.(string)
	}
	if v, ok := data.GetOk("description"); ok {
		role.Description = v.(string)
	}
	if v, ok := data.GetOk("ttl"); ok {
		role.TTL = time.Duration(v.(int)) * time.Second
	}
	if v, ok := data.GetOk("max_ttl"); ok {
		role.MaxTTL = time.Duration(v.(int)) * time.Second
	}

	if role.ServiceAccount == "" {
		return logical.ErrorResponse("service_account is required"), nil
	}
	if role.MaxTTL > 0 && role.TTL > role.MaxTTL {
		return logical.ErrorResponse("ttl must not exceed max_ttl"), nil
	}
	// Fail fast if the referenced service account does not exist.
	sa, err := b.getServiceAccount(ctx, req.Storage, role.ServiceAccount)
	if err != nil {
		return nil, err
	}
	if sa == nil {
		return logical.ErrorResponse("service_account %q does not exist", role.ServiceAccount), nil
	}

	return nil, setJSON(ctx, req.Storage, roleStoragePrefix+name, role)
}

func (b *rancherBackend) pathRoleDelete(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	return nil, req.Storage.Delete(ctx, roleStoragePrefix+data.Get("name").(string))
}

func (b *rancherBackend) pathRolesList(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	entries, err := req.Storage.List(ctx, roleStoragePrefix)
	if err != nil {
		return nil, err
	}
	return logical.ListResponse(entries), nil
}

func (b *rancherBackend) getRole(ctx context.Context, s logical.Storage, name string) (*rancherRole, error) {
	if name == "" {
		return nil, errors.New("missing role name")
	}
	entry, err := s.Get(ctx, roleStoragePrefix+name)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, nil
	}
	role := &rancherRole{}
	if err := entry.DecodeJSON(role); err != nil {
		return nil, err
	}
	return role, nil
}
