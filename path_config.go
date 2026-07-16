package rancher

import (
	"context"
	"errors"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

const configStoragePath = "config"

// rancherConfig is the connection to the Rancher server. Per-user tokens live on
// service-account entries, not here — this is only how to reach Rancher.
type rancherConfig struct {
	RancherURL            string `json:"rancher_url"`
	CACert                string `json:"ca_cert"`
	TLSSkipVerify         bool   `json:"tls_skip_verify"`
	RequestTimeoutSeconds int    `json:"request_timeout_seconds"`
}

func pathConfig(b *rancherBackend) *framework.Path {
	return &framework.Path{
		Pattern: "config",
		DisplayAttrs: &framework.DisplayAttributes{
			OperationPrefix: "rancher",
			OperationSuffix: "config",
		},
		Fields: map[string]*framework.FieldSchema{
			"rancher_url": {
				Type:        framework.TypeString,
				Description: "Base URL of the Rancher server, e.g. https://rancher.example.com.",
				Required:    true,
			},
			"ca_cert": {
				Type:        framework.TypeString,
				Description: "PEM CA certificate that signed the Rancher server's TLS certificate.",
			},
			"tls_skip_verify": {
				Type:        framework.TypeBool,
				Description: "Skip TLS verification of the Rancher server (not recommended).",
				Default:     false,
			},
			"request_timeout_seconds": {
				Type:        framework.TypeInt,
				Description: "HTTP timeout in seconds for calls to Rancher (default 30).",
				Default:     30,
			},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation:   &framework.PathOperation{Callback: b.pathConfigRead},
			logical.CreateOperation: &framework.PathOperation{Callback: b.pathConfigWrite},
			logical.UpdateOperation: &framework.PathOperation{Callback: b.pathConfigWrite},
			logical.DeleteOperation: &framework.PathOperation{Callback: b.pathConfigDelete},
		},
		ExistenceCheck:  b.pathConfigExistenceCheck,
		HelpSynopsis:    "Configure the connection to the Rancher server.",
		HelpDescription: "Configure the URL and TLS settings the backend uses to reach Rancher. Per-user tokens are configured on service-accounts/<name>.",
	}
}

func (b *rancherBackend) pathConfigExistenceCheck(ctx context.Context, req *logical.Request, _ *framework.FieldData) (bool, error) {
	config, err := getConfig(ctx, req.Storage)
	if err != nil {
		return false, err
	}
	return config != nil, nil
}

func (b *rancherBackend) pathConfigRead(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	config, err := getConfig(ctx, req.Storage)
	if err != nil {
		return nil, err
	}
	if config == nil {
		return nil, nil
	}
	return &logical.Response{
		Data: map[string]interface{}{
			"rancher_url":             config.RancherURL,
			"tls_skip_verify":         config.TLSSkipVerify,
			"request_timeout_seconds": config.RequestTimeoutSeconds,
		},
	}, nil
}

func (b *rancherBackend) pathConfigWrite(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	config, err := getConfig(ctx, req.Storage)
	if err != nil {
		return nil, err
	}
	if config == nil {
		if req.Operation == logical.UpdateOperation {
			return nil, errors.New("config not found during update operation")
		}
		config = &rancherConfig{}
	}

	if v, ok := data.GetOk("rancher_url"); ok {
		config.RancherURL = v.(string)
	}
	if v, ok := data.GetOk("ca_cert"); ok {
		config.CACert = v.(string)
	}
	if v, ok := data.GetOk("tls_skip_verify"); ok {
		config.TLSSkipVerify = v.(bool)
	}
	if v, ok := data.GetOk("request_timeout_seconds"); ok {
		config.RequestTimeoutSeconds = v.(int)
	} else if req.Operation == logical.CreateOperation {
		config.RequestTimeoutSeconds = data.Get("request_timeout_seconds").(int)
	}

	if config.RancherURL == "" {
		return logical.ErrorResponse("rancher_url is required"), nil
	}

	return nil, setJSON(ctx, req.Storage, configStoragePath, config)
}

func (b *rancherBackend) pathConfigDelete(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	return nil, req.Storage.Delete(ctx, configStoragePath)
}

func getConfig(ctx context.Context, s logical.Storage) (*rancherConfig, error) {
	entry, err := s.Get(ctx, configStoragePath)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, nil
	}
	config := &rancherConfig{}
	if err := entry.DecodeJSON(config); err != nil {
		return nil, err
	}
	return config, nil
}

// setJSON stores a value as a JSON storage entry.
func setJSON(ctx context.Context, s logical.Storage, key string, value interface{}) error {
	entry, err := logical.StorageEntryJSON(key, value)
	if err != nil {
		return err
	}
	return s.Put(ctx, entry)
}
