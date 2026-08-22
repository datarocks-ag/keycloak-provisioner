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

func TestEnsureRealmRoleCreate(t *testing.T) {
	var mu sync.Mutex
	var createdBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
		"POST /admin/realms/{realm}/roles": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&createdBody)
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	role := config.RealmRole{Name: "app-admin", Description: "Admin role"}

	p := New(c, &config.Config{})
	if err := p.ensureRealmRole(context.Background(), "test-realm", role, "update"); err != nil {
		t.Fatalf("ensureRealmRole: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if createdBody["name"] != "app-admin" {
		t.Errorf("expected name app-admin, got %v", createdBody["name"])
	}
}

func TestEnsureRealmRoleUpdate(t *testing.T) {
	var mu sync.Mutex
	var updatedBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"name": "app-admin"})
		},
		"PUT /admin/realms/{realm}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&updatedBody)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	role := config.RealmRole{Name: "app-admin", Description: "Updated description"}

	p := New(c, &config.Config{})
	if err := p.ensureRealmRole(context.Background(), "test-realm", role, "update"); err != nil {
		t.Fatalf("ensureRealmRole: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if updatedBody["description"] != "Updated description" {
		t.Errorf("expected description Updated description, got %v", updatedBody["description"])
	}
}

func TestEnsureRealmRoleCreateStrategySkipsExisting(t *testing.T) {
	var updateCalled atomic.Bool

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"name": "app-admin"})
		},
		"PUT /admin/realms/{realm}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			updateCalled.Store(true)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	role := config.RealmRole{Name: "app-admin", Description: "Should not update"}

	p := New(c, &config.Config{})
	if err := p.ensureRealmRole(context.Background(), "test-realm", role, "create"); err != nil {
		t.Fatalf("ensureRealmRole: %v", err)
	}

	if updateCalled.Load() {
		t.Error("expected PUT not to be called with strategy=create")
	}
}
