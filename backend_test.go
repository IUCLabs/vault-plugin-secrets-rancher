package rancher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/vault/sdk/logical"
)

// fakeRancher is an in-memory ext.cattle.io Token API for tests. A bearer is
// valid if it is the seed token or any token this server minted, so rotation
// chains work exactly as in production.
type fakeRancher struct {
	mu     sync.Mutex
	byName map[string]string // metadata.name -> clusterName
	valid  map[string]string // bearer -> metadata.name
	minted int
}

func newFakeRancher(seed string) *fakeRancher {
	// "seed" is the resource name of the seed token, registered so a rotation
	// that knows token_name can delete it.
	return &fakeRancher{
		byName: map[string]string{"seed": ""},
		valid:  map[string]string{seed: "seed"},
	}
}

func (f *fakeRancher) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, ok := f.valid[bearer]; !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		name := strings.Trim(strings.TrimPrefix(r.URL.Path, tokensAPIPath), "/")
		switch {
		case r.Method == http.MethodPost && name == "":
			var in token
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.minted++
			newName := in.Metadata.GenerateName + "abcd"
			bearer := "ext/" + newName + ":secret"
			f.byName[newName] = in.Spec.ClusterName
			f.valid[bearer] = newName
			out := token{Metadata: tokenMetadata{Name: newName}, Status: tokenStatus{BearerToken: bearer, Value: "secret"}}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(out)
		case r.Method == http.MethodGet && name != "":
			if _, ok := f.byName[name]; !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(token{Metadata: tokenMetadata{Name: name}})
		case r.Method == http.MethodDelete && name != "":
			if _, ok := f.byName[name]; !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			delete(f.byName, name)
			for v, n := range f.valid {
				if n == name {
					delete(f.valid, v)
				}
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func newTestBackend(t *testing.T) (*rancherBackend, logical.Storage) {
	t.Helper()
	config := logical.TestBackendConfig()
	config.StorageView = &logical.InmemStorage{}
	b, err := Factory(context.Background(), config)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	return b.(*rancherBackend), config.StorageView
}

func req(t *testing.T, b *rancherBackend, s logical.Storage, op logical.Operation, path string, data map[string]interface{}) *logical.Response {
	t.Helper()
	resp, err := b.HandleRequest(context.Background(), &logical.Request{
		Operation: op,
		Path:      path,
		Data:      data,
		Storage:   s,
	})
	if err != nil {
		t.Fatalf("%s %s: %v", op, path, err)
	}
	if resp != nil && resp.IsError() {
		t.Fatalf("%s %s: %v", op, path, resp.Error())
	}
	return resp
}

func TestLifecycle(t *testing.T) {
	fake := newFakeRancher("seed-token")
	srv := fake.server(t)
	defer srv.Close()

	b, s := newTestBackend(t)

	req(t, b, s, logical.CreateOperation, "config", map[string]interface{}{
		"rancher_url": srv.URL,
	})

	// Seed a service account.
	req(t, b, s, logical.CreateOperation, "service-accounts/admin", map[string]interface{}{
		"token":           "seed-token",
		"token_name":      "seed",
		"token_ttl":       "2160h",
		"rotation_period": "1080h",
	})

	// Rotate: a new token replaces the seed and the old bearer is retired.
	rot := req(t, b, s, logical.UpdateOperation, "service-accounts/admin/rotate", nil)
	newName, _ := rot.Data["token_name"].(string)
	if newName == "" {
		t.Fatal("rotate returned no token_name")
	}
	fake.mu.Lock()
	if _, ok := fake.valid["seed-token"]; ok {
		t.Error("seed token should be retired after rotation")
	}
	fake.mu.Unlock()

	// Role + dynamic creds.
	req(t, b, s, logical.CreateOperation, "roles/ci", map[string]interface{}{
		"service_account": "admin",
		"cluster_name":    "c-m-abc123",
		"ttl":             "1h",
		"max_ttl":         "24h",
	})
	creds := req(t, b, s, logical.ReadOperation, "creds/ci", nil)
	if creds.Secret == nil {
		t.Fatal("creds returned no secret")
	}
	tokenName, _ := creds.Data["token_name"].(string)
	tokenVal, _ := creds.Data["token"].(string)
	if tokenName == "" || tokenVal == "" {
		t.Fatal("creds missing token/token_name")
	}
	// Must be the usable bearer credential (ext/<name>:<secret>), not the bare
	// secret fragment from status.value — see the bearerToken fix.
	if !strings.HasPrefix(tokenVal, "ext/") {
		t.Errorf("token = %q, want the ext/ bearerToken form", tokenVal)
	}
	if creds.Data["cluster_name"].(string) != "c-m-abc123" {
		t.Errorf("cluster_name = %q, want c-m-abc123", creds.Data["cluster_name"])
	}
	fake.mu.Lock()
	if _, ok := fake.byName[tokenName]; !ok {
		t.Error("minted token not present in rancher")
	}
	fake.mu.Unlock()

	// Revoke deletes the token from rancher.
	if _, err := b.HandleRequest(context.Background(), &logical.Request{
		Operation: logical.RevokeOperation,
		Secret:    creds.Secret,
		Storage:   s,
	}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	fake.mu.Lock()
	if _, ok := fake.byName[tokenName]; ok {
		t.Error("token still present after revoke")
	}
	fake.mu.Unlock()
}

func TestRoleRequiresServiceAccount(t *testing.T) {
	b, s := newTestBackend(t)
	req(t, b, s, logical.CreateOperation, "config", map[string]interface{}{"rancher_url": "https://rancher.example.com"})
	resp, err := b.HandleRequest(context.Background(), &logical.Request{
		Operation: logical.CreateOperation,
		Path:      "roles/x",
		Data:      map[string]interface{}{"service_account": "missing"},
		Storage:   s,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp == nil || !resp.IsError() {
		t.Fatal("expected error for role referencing missing service account")
	}
}

func TestServiceAccountRejectsBadRotationPeriod(t *testing.T) {
	b, s := newTestBackend(t)
	resp, err := b.HandleRequest(context.Background(), &logical.Request{
		Operation: logical.CreateOperation,
		Path:      "service-accounts/bad",
		Data:      map[string]interface{}{"token": "x", "token_ttl": "10h", "rotation_period": "20h"},
		Storage:   s,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp == nil || !resp.IsError() {
		t.Fatal("expected error when rotation_period >= token_ttl")
	}
}
