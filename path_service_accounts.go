package rancher

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

const serviceAccountStoragePrefix = "service-account/"

// defaults for the seeded-token rotation schedule.
const (
	defaultTokenTTL       = 90 * 24 * time.Hour // Rancher's common auth-token-max-ttl.
	defaultRotationPeriod = 45 * 24 * time.Hour // rotate at half-life, well before expiry.
)

// serviceAccount is a seeded Rancher user token that the engine keeps rotated.
// Roles reference a service account and mint short-lived tokens with its token,
// so minted tokens inherit that user's RBAC.
type serviceAccount struct {
	// Token is the current live Rancher token. Write-only from the API.
	Token string `json:"token"`
	// TokenName is the ext.cattle.io Token resource name, so the superseded
	// token can be deleted after a rotation.
	TokenName string `json:"token_name"`
	// TokenTTL is the lifetime requested for each rotated replacement token.
	TokenTTL time.Duration `json:"token_ttl"`
	// RotationPeriod is how long a token is used before it is rotated. Keep it
	// comfortably below TokenTTL so rotation always beats expiry.
	RotationPeriod time.Duration `json:"rotation_period"`
	// LastRotated is when the current token was issued (or seeded).
	LastRotated time.Time `json:"last_rotated"`
}

// due reports whether the token is old enough to rotate.
func (s *serviceAccount) due(now time.Time) bool {
	if s.RotationPeriod <= 0 {
		return false
	}
	return now.Sub(s.LastRotated) >= s.RotationPeriod
}

func pathServiceAccount(b *rancherBackend) *framework.Path {
	return &framework.Path{
		Pattern: "service-accounts/" + framework.GenericNameRegex("name"),
		DisplayAttrs: &framework.DisplayAttributes{
			OperationPrefix: "rancher",
			OperationSuffix: "service-account",
		},
		Fields: map[string]*framework.FieldSchema{
			"name": {
				Type:        framework.TypeLowerCaseString,
				Description: "Name of the service account (a seeded, auto-rotated Rancher token).",
				Required:    true,
			},
			"token": {
				Type:        framework.TypeString,
				Description: "Rancher API token to seed this service account with. Write-only; the engine rotates it from here on.",
				DisplayAttrs: &framework.DisplayAttributes{
					Name:      "Seed Token",
					Sensitive: true,
				},
			},
			"token_name": {
				Type:        framework.TypeString,
				Description: "ext.cattle.io Token resource name (metadata.name) of the seed token, so the engine can delete it after the first rotation. Optional but recommended.",
			},
			"token_ttl": {
				Type:        framework.TypeDurationSecond,
				Description: "Lifetime requested for each rotated replacement token (default 90d). Must not exceed Rancher's auth-token-max-ttl-minutes.",
				Default:     int(defaultTokenTTL.Seconds()),
			},
			"rotation_period": {
				Type:        framework.TypeDurationSecond,
				Description: "How long a token is used before it is rotated (default 45d). Keep it below token_ttl.",
				Default:     int(defaultRotationPeriod.Seconds()),
			},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation:   &framework.PathOperation{Callback: b.pathServiceAccountRead},
			logical.CreateOperation: &framework.PathOperation{Callback: b.pathServiceAccountWrite},
			logical.UpdateOperation: &framework.PathOperation{Callback: b.pathServiceAccountWrite},
			logical.DeleteOperation: &framework.PathOperation{Callback: b.pathServiceAccountDelete},
		},
		ExistenceCheck:  b.pathServiceAccountExistenceCheck,
		HelpSynopsis:    "Manage seeded, auto-rotated Rancher service-account tokens.",
		HelpDescription: "Each service account holds a Rancher token the engine rotates before Rancher's TTL cap. Roles reference a service account to mint short-lived tokens with its RBAC.",
	}
}

func pathServiceAccountsList(b *rancherBackend) *framework.Path {
	return &framework.Path{
		Pattern: "service-accounts/?$",
		DisplayAttrs: &framework.DisplayAttributes{
			OperationPrefix: "rancher",
			OperationSuffix: "service-accounts",
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ListOperation: &framework.PathOperation{Callback: b.pathServiceAccountsList},
		},
		HelpSynopsis:    "List service accounts.",
		HelpDescription: "List the seeded Rancher service accounts configured on this backend.",
	}
}

func pathServiceAccountRotate(b *rancherBackend) *framework.Path {
	return &framework.Path{
		Pattern: "service-accounts/" + framework.GenericNameRegex("name") + "/rotate$",
		DisplayAttrs: &framework.DisplayAttributes{
			OperationPrefix: "rancher",
			OperationSuffix: "service-account-rotate",
		},
		Fields: map[string]*framework.FieldSchema{
			"name": {
				Type:        framework.TypeLowerCaseString,
				Description: "Name of the service account to rotate now.",
				Required:    true,
			},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.UpdateOperation: &framework.PathOperation{Callback: b.pathServiceAccountRotateNow},
		},
		HelpSynopsis:    "Rotate a service-account token immediately.",
		HelpDescription: "Forces an out-of-schedule rotation: mints a fresh token with the current one, stores it, and deletes the old token.",
	}
}

func (b *rancherBackend) pathServiceAccountExistenceCheck(ctx context.Context, req *logical.Request, data *framework.FieldData) (bool, error) {
	sa, err := b.getServiceAccount(ctx, req.Storage, data.Get("name").(string))
	if err != nil {
		return false, err
	}
	return sa != nil, nil
}

func (b *rancherBackend) pathServiceAccountRead(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	sa, err := b.getServiceAccount(ctx, req.Storage, data.Get("name").(string))
	if err != nil {
		return nil, err
	}
	if sa == nil {
		return nil, nil
	}
	// The token itself is never returned.
	return &logical.Response{
		Data: map[string]interface{}{
			"token_name":      sa.TokenName,
			"token_ttl":       int64(sa.TokenTTL.Seconds()),
			"rotation_period": int64(sa.RotationPeriod.Seconds()),
			"last_rotated":    sa.LastRotated.Format(time.RFC3339),
		},
	}, nil
}

func (b *rancherBackend) pathServiceAccountWrite(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := data.Get("name").(string)
	sa, err := b.getServiceAccount(ctx, req.Storage, name)
	if err != nil {
		return nil, err
	}
	isCreate := sa == nil
	if isCreate {
		sa = &serviceAccount{}
	}

	if v, ok := data.GetOk("token"); ok {
		sa.Token = v.(string)
		// A freshly seeded token restarts the rotation clock.
		sa.LastRotated = time.Now().UTC()
	}
	if v, ok := data.GetOk("token_name"); ok {
		sa.TokenName = v.(string)
	}
	if v, ok := data.GetOk("token_ttl"); ok {
		sa.TokenTTL = time.Duration(v.(int)) * time.Second
	} else if isCreate {
		sa.TokenTTL = defaultTokenTTL
	}
	if v, ok := data.GetOk("rotation_period"); ok {
		sa.RotationPeriod = time.Duration(v.(int)) * time.Second
	} else if isCreate {
		sa.RotationPeriod = defaultRotationPeriod
	}

	if sa.Token == "" {
		return logical.ErrorResponse("token is required to seed a service account"), nil
	}
	if sa.RotationPeriod > 0 && sa.TokenTTL > 0 && sa.RotationPeriod >= sa.TokenTTL {
		return logical.ErrorResponse("rotation_period must be less than token_ttl so rotation beats expiry"), nil
	}
	if sa.LastRotated.IsZero() {
		sa.LastRotated = time.Now().UTC()
	}

	return nil, setJSON(ctx, req.Storage, serviceAccountStoragePrefix+name, sa)
}

func (b *rancherBackend) pathServiceAccountDelete(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	return nil, req.Storage.Delete(ctx, serviceAccountStoragePrefix+data.Get("name").(string))
}

func (b *rancherBackend) pathServiceAccountsList(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	entries, err := req.Storage.List(ctx, serviceAccountStoragePrefix)
	if err != nil {
		return nil, err
	}
	return logical.ListResponse(entries), nil
}

func (b *rancherBackend) pathServiceAccountRotateNow(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := data.Get("name").(string)

	b.lock.Lock()
	defer b.lock.Unlock()

	sa, err := b.getServiceAccount(ctx, req.Storage, name)
	if err != nil {
		return nil, err
	}
	if sa == nil {
		return logical.ErrorResponse("service account %q does not exist", name), nil
	}
	if err := b.rotateServiceAccount(ctx, req.Storage, name, sa); err != nil {
		return nil, fmt.Errorf("rotating service account %q: %w", name, err)
	}
	return &logical.Response{
		Data: map[string]interface{}{
			"token_name":   sa.TokenName,
			"last_rotated": sa.LastRotated.Format(time.RFC3339),
		},
	}, nil
}

func (b *rancherBackend) getServiceAccount(ctx context.Context, s logical.Storage, name string) (*serviceAccount, error) {
	if name == "" {
		return nil, errors.New("missing service account name")
	}
	entry, err := s.Get(ctx, serviceAccountStoragePrefix+name)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, nil
	}
	sa := &serviceAccount{}
	if err := entry.DecodeJSON(sa); err != nil {
		return nil, err
	}
	return sa, nil
}
