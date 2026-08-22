package provisioner

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"keycloak-provisioner/internal/config"
)

func TestEnsureClientRoleCreate(t *testing.T) {
	var mu sync.Mutex
	var createdBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients/{uuid}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
		"POST /admin/realms/{realm}/clients/{uuid}/roles": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&createdBody)
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	role := config.ClientRole{Name: "admin", Description: "Admin"}

	p := New(c, &config.Config{})
	if err := p.ensureClientRole(context.Background(), "test-realm", "uuid-123", role, "update"); err != nil {
		t.Fatalf("ensureClientRole: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if createdBody["name"] != "admin" {
		t.Errorf("expected name admin, got %v", createdBody["name"])
	}
}

func TestEnsureClientRoleCreateStrategySkipsExisting(t *testing.T) {
	var updateCalled atomic.Bool

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients/{uuid}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"name": "admin"})
		},
		"PUT /admin/realms/{realm}/clients/{uuid}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			updateCalled.Store(true)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	role := config.ClientRole{Name: "admin", Description: "Should not update"}

	p := New(c, &config.Config{})
	if err := p.ensureClientRole(context.Background(), "test-realm", "uuid-123", role, "create"); err != nil {
		t.Fatalf("ensureClientRole: %v", err)
	}

	if updateCalled.Load() {
		t.Error("expected PUT not to be called with strategy=create")
	}
}

func TestEnsureClientRoleUpdate(t *testing.T) {
	var mu sync.Mutex
	var updatedBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients/{uuid}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"name": "editor"})
		},
		"PUT /admin/realms/{realm}/clients/{uuid}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&updatedBody)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	role := config.ClientRole{Name: "editor", Description: "Updated editor"}

	p := New(c, &config.Config{})
	if err := p.ensureClientRole(context.Background(), "test-realm", "uuid-123", role, "update"); err != nil {
		t.Fatalf("ensureClientRole: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if updatedBody["description"] != "Updated editor" {
		t.Errorf("expected Updated editor, got %v", updatedBody["description"])
	}
}
