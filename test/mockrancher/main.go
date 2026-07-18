// Command mockrancher is an in-memory stand-in for the Rancher aggregated API
// server's tokens.ext.cattle.io resource, used by the e2e tests. It implements
// just enough of the Token contract the plugin uses (create/get/delete) — no
// Kubernetes or real Rancher required. Not for production use.
//
// A bearer is accepted if it is the seed token (MOCKRANCHER_TOKEN) or any token
// this server previously minted; that lets a rotated token mint further tokens,
// exactly as the plugin's root-rotation relies on.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
)

type tokenMeta struct {
	Name         string `json:"name,omitempty"`
	GenerateName string `json:"generateName,omitempty"`
}

type tokenSpec struct {
	Description string `json:"description,omitempty"`
	TTL         int64  `json:"ttl,omitempty"`
	ClusterName string `json:"clusterName,omitempty"`
}

type tokenStatus struct {
	BearerToken string `json:"bearerToken,omitempty"`
	Value       string `json:"value,omitempty"`
}

type token struct {
	APIVersion string      `json:"apiVersion"`
	Kind       string      `json:"kind"`
	Metadata   tokenMeta   `json:"metadata"`
	Spec       tokenSpec   `json:"spec"`
	Status     tokenStatus `json:"status"`
}

type store struct {
	mu     sync.Mutex
	tokens map[string]*token // by metadata.name
	valid  map[string]string // bearer value -> metadata.name
}

func randHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func (s *store) authName(r *http.Request) (string, bool) {
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if bearer == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	name, ok := s.valid[bearer]
	return name, ok
}

const apiPrefix = "/apis/ext.cattle.io/v1/tokens"

func (s *store) handle(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authName(r); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, apiPrefix), "/")

	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case r.Method == http.MethodPost && name == "": // create
		var req token
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		newName := req.Metadata.Name
		if newName == "" {
			newName = req.Metadata.GenerateName + randHex(4)
		}
		// ext.cattle.io returns the usable credential in status.bearerToken,
		// formatted "ext/<name>:<secret>"; status.value is only the secret.
		secret := randHex(16)
		bearer := "ext/" + newName + ":" + secret
		t := &token{
			APIVersion: "ext.cattle.io/v1",
			Kind:       "Token",
			Metadata:   tokenMeta{Name: newName},
			Spec:       req.Spec,
			Status:     tokenStatus{BearerToken: bearer, Value: secret},
		}
		s.tokens[newName] = t
		s.valid[bearer] = newName
		writeJSON(w, http.StatusCreated, t)
	case r.Method == http.MethodGet && name != "": // read
		t, ok := s.tokens[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		// The value is only returned at creation; blank it on read.
		out := *t
		out.Status.Value = ""
		writeJSON(w, http.StatusOK, &out)
	case r.Method == http.MethodDelete && name != "": // delete
		t, ok := s.tokens[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		for v, n := range s.valid {
			if n == name {
				delete(s.valid, v)
			}
		}
		delete(s.tokens, name)
		_ = t
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	addr := os.Getenv("MOCKRANCHER_ADDR")
	if addr == "" {
		addr = ":8443"
	}
	seed := os.Getenv("MOCKRANCHER_TOKEN")
	if seed == "" {
		seed = "seed-token"
	}
	s := &store{
		tokens: map[string]*token{},
		valid:  map[string]string{seed: "seed"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc(apiPrefix, s.handle)
	mux.HandleFunc(apiPrefix+"/", s.handle)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	log.Printf("mock rancher ext.cattle.io API listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux)) //nolint:gosec // test-only mock
}
