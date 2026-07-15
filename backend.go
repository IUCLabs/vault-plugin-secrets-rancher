// Package rancher implements a Vault / OpenBao secrets engine that manages
// Rancher API tokens through the public tokens.ext.cattle.io API.
//
// The engine is seeded with a long-lived Rancher admin (or service-account)
// token. Because Rancher caps token TTLs (commonly 90 days), the engine rotates
// each seeded token well before expiry: on a schedule it uses the current token
// to mint a fresh one for the same user and swaps it in, so the credential never
// lapses. Roles then mint short-lived, cluster-scoped tokens on demand, each
// bound to a Vault lease and deleted from Rancher on revocation.
//
// Rancher only lets a token be created for the caller's own user, so a minted
// token inherits the seeding user's RBAC. To get least-privilege tokens, seed a
// separate service-account per privilege level and point roles at it.
package rancher

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

// errBackendNotConfigured is returned when a credential is requested before the
// Rancher connection has been configured.
var errBackendNotConfigured = errors.New("rancher backend not configured; write config first")

type rancherBackend struct {
	*framework.Backend

	lock sync.RWMutex
}

// Factory returns a configured Rancher secrets backend.
func Factory(ctx context.Context, conf *logical.BackendConfig) (logical.Backend, error) {
	b := backend()
	if err := b.Setup(ctx, conf); err != nil {
		return nil, err
	}
	return b, nil
}

func backend() *rancherBackend {
	b := &rancherBackend{}

	b.Backend = &framework.Backend{
		Help:        strings.TrimSpace(backendHelp),
		BackendType: logical.TypeLogical,
		PathsSpecial: &logical.Paths{
			SealWrapStorage: []string{configStoragePath},
		},
		Paths: framework.PathAppend(
			[]*framework.Path{
				pathConfig(b),
				pathServiceAccount(b),
				pathServiceAccountsList(b),
				pathServiceAccountRotate(b),
				pathRole(b),
				pathRolesList(b),
				pathCredentials(b),
			},
		),
		Secrets: []*framework.Secret{
			b.rancherTokenSecret(),
		},
		// PeriodicFunc runs on the active node roughly once a minute; it drives
		// the automatic root-token rotation.
		PeriodicFunc: b.periodicRotate,
	}

	return b
}

// clientFor builds a Rancher client from the global config, authenticated with
// the given bearer token (a service account's current token).
func (b *rancherBackend) clientFor(ctx context.Context, s logical.Storage, bearer string) (*rancherClient, error) {
	config, err := getConfig(ctx, s)
	if err != nil {
		return nil, err
	}
	if config == nil {
		return nil, errBackendNotConfigured
	}
	return newClient(config, bearer)
}

// periodicRotate rotates every service-account token whose age has reached its
// rotation_period. It never blocks the engine: individual failures are logged
// and retried on the next tick.
func (b *rancherBackend) periodicRotate(ctx context.Context, req *logical.Request) error {
	b.lock.Lock()
	defer b.lock.Unlock()

	names, err := req.Storage.List(ctx, serviceAccountStoragePrefix)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, name := range names {
		sa, err := b.getServiceAccount(ctx, req.Storage, name)
		if err != nil || sa == nil {
			continue
		}
		if !sa.due(now) {
			continue
		}
		if err := b.rotateServiceAccount(ctx, req.Storage, name, sa); err != nil {
			b.Logger().Error("rancher: service-account rotation failed", "service_account", name, "error", err)
			continue
		}
		b.Logger().Info("rancher: rotated service-account token", "service_account", name)
	}
	return nil
}

// rotateServiceAccount mints a fresh token for the service-account user with the
// current token, persists it, then best-effort deletes the previous token. The
// caller holds b.lock.
func (b *rancherBackend) rotateServiceAccount(ctx context.Context, s logical.Storage, name string, sa *serviceAccount) error {
	if sa.Token == "" {
		return fmt.Errorf("service account %q has no token to rotate with", name)
	}
	client, err := b.clientFor(ctx, s, sa.Token)
	if err != nil {
		return err
	}

	value, tokenName, err := client.MintToken(ctx, mintRequest{
		GenerateName: fmt.Sprintf("vault-%s-", name),
		Description:  fmt.Sprintf("vault-managed root token for service-account %q", name),
		TTL:          sa.TokenTTL,
	})
	if err != nil {
		return fmt.Errorf("minting replacement token: %w", err)
	}

	old := sa.TokenName
	sa.Token = value
	sa.TokenName = tokenName
	sa.LastRotated = time.Now().UTC()
	if err := setJSON(ctx, s, serviceAccountStoragePrefix+name, sa); err != nil {
		// The new token is live but unstored; deleting it keeps Rancher tidy and
		// leaves the old token (still stored) in charge for the next tick.
		_ = client.DeleteToken(ctx, tokenName)
		return fmt.Errorf("persisting rotated token: %w", err)
	}

	// Retire the previous token. The new token authenticates the delete.
	if old != "" && old != tokenName {
		newClient, cerr := b.clientFor(ctx, s, value)
		if cerr == nil {
			if derr := newClient.DeleteToken(ctx, old); derr != nil {
				b.Logger().Warn("rancher: could not delete superseded token", "service_account", name, "token", old, "error", derr)
			}
		}
	}
	return nil
}

const backendHelp = `
The rancher secrets engine manages Rancher API tokens via the public
tokens.ext.cattle.io API.

Seed a service account with a long-lived Rancher token; the engine rotates it
before Rancher's TTL cap expires so it never lapses. Roles mint short-lived,
optionally cluster-scoped tokens bound to a Vault lease and deleted on
revocation. A minted token inherits the seeding user's RBAC, so seed one service
account per privilege level for least-privilege scoping.
`
