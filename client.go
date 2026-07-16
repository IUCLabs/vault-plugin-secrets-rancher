package rancher

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultHTTPTimeout = 30 * time.Second

// tokensAPIPath is the ext.cattle.io Token collection endpoint on the Rancher
// aggregated API server. Token is a cluster-scoped resource, so there is no
// namespace segment.
const tokensAPIPath = "/apis/ext.cattle.io/v1/tokens"

// rancherClient talks to the Rancher aggregated Kubernetes API, specifically the
// public tokens.ext.cattle.io resource. It authenticates with a Rancher API
// token (bearer). Every token it mints belongs to the user that owns the bearer
// token — the ext API forbids creating a Token for a different spec.userID — so
// the seeded service-account user's RBAC is what a minted token inherits.
type rancherClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// token is the ext.cattle.io/v1 Token resource, trimmed to the fields the
// plugin reads or writes.
type token struct {
	APIVersion string        `json:"apiVersion,omitempty"`
	Kind       string        `json:"kind,omitempty"`
	Metadata   tokenMetadata `json:"metadata,omitempty"`
	Spec       tokenSpec     `json:"spec,omitempty"`
	Status     tokenStatus   `json:"status,omitempty"`
}

type tokenMetadata struct {
	Name         string `json:"name,omitempty"`
	GenerateName string `json:"generateName,omitempty"`
}

type tokenSpec struct {
	Description string `json:"description,omitempty"`
	// TTL is the token lifetime in milliseconds. Rancher clamps it to
	// auth-token-max-ttl-minutes.
	TTL int64 `json:"ttl,omitempty"`
	// ClusterName scopes the token to a single downstream cluster. Empty means
	// the token is valid against the Rancher server (full user scope).
	ClusterName string `json:"clusterName,omitempty"`
	Enabled     *bool  `json:"enabled,omitempty"`
}

type tokenStatus struct {
	// Value is the usable token string. It is returned only in the creation
	// response and never again.
	Value string `json:"value,omitempty"`
}

// mintRequest describes a token to create.
type mintRequest struct {
	// GenerateName is the metadata.generateName prefix; Rancher assigns a unique
	// metadata.name from it.
	GenerateName string
	Description  string
	TTL          time.Duration
	ClusterName  string
}

func newClient(cfg *rancherConfig, bearer string) (*rancherClient, error) {
	if cfg == nil {
		return nil, errors.New("rancher client configuration is nil")
	}
	if cfg.RancherURL == "" {
		return nil, errors.New("rancher_url is required")
	}

	timeout := defaultHTTPTimeout
	if cfg.RequestTimeoutSeconds > 0 {
		timeout = time.Duration(cfg.RequestTimeoutSeconds) * time.Second
	}

	tlsConfig := &tls.Config{InsecureSkipVerify: cfg.TLSSkipVerify} //nolint:gosec // opt-in via config
	if cfg.CACert != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(cfg.CACert)) {
			return nil, errors.New("ca_cert is not a valid PEM certificate")
		}
		tlsConfig.RootCAs = pool
	}

	return &rancherClient{
		baseURL: strings.TrimRight(cfg.RancherURL, "/"),
		token:   bearer,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: &http.Transport{TLSClientConfig: tlsConfig},
		},
	}, nil
}

// MintToken creates a new ext.cattle.io Token for the bearer user and returns
// its usable value and resource name. The value is only ever available here.
func (c *rancherClient) MintToken(ctx context.Context, req mintRequest) (value, name string, err error) {
	body := token{
		APIVersion: "ext.cattle.io/v1",
		Kind:       "Token",
		Metadata:   tokenMetadata{GenerateName: req.GenerateName},
		Spec: tokenSpec{
			Description: req.Description,
			TTL:         req.TTL.Milliseconds(),
			ClusterName: req.ClusterName,
		},
	}

	var out token
	if err := c.do(ctx, http.MethodPost, tokensAPIPath, body, &out); err != nil {
		return "", "", err
	}
	if out.Status.Value == "" {
		return "", "", errors.New("rancher returned an empty token value")
	}
	return out.Status.Value, out.Metadata.Name, nil
}

// DeleteToken removes a Token by its resource name. A missing token is success.
func (c *rancherClient) DeleteToken(ctx context.Context, name string) error {
	if name == "" {
		return nil
	}
	return c.do(ctx, http.MethodDelete, tokensAPIPath+"/"+name, nil, nil)
}

// GetToken reads a Token by resource name (used to verify existence in tests /
// health checks). Returns nil if the token does not exist.
func (c *rancherClient) GetToken(ctx context.Context, name string) (*token, error) {
	var out token
	err := c.do(ctx, http.MethodGet, tokensAPIPath+"/"+name, nil, &out)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

// errNotFound flags a 404 so callers can treat absence as non-fatal.
var errNotFound = errors.New("not found")

func (c *rancherClient) do(ctx context.Context, method, path string, payload, out interface{}) error {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		body = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling rancher %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode == http.StatusNotFound {
		if method == http.MethodDelete {
			return nil
		}
		return errNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("rancher %s %s returned %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decoding rancher response: %w", err)
	}
	return nil
}
