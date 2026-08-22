package provisioner

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"keycloak-provisioner/internal/config"
)

func TestEnsureServiceAccountRoles(t *testing.T) {
	var mu sync.Mutex
	var addedRoles []map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients/{uuid}/service-account-user": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"id":       "sa-user-uuid",
				"username": "service-account-my-client",
			})
		},
		"GET /admin/realms/{realm}/users/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"id":   "role-id-1",
				"name": "admin",
			})
		},
		"POST /admin/realms/{realm}/users/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&addedRoles)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	roles := &config.UserRoles{
		Realm: []string{"admin"},
	}

	p := New(c, &config.Config{})
	if err := p.ensureServiceAccountRoles(context.Background(), "test-realm", "client-uuid-1", "my-client", roles); err != nil {
		t.Fatalf("ensureServiceAccountRoles: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(addedRoles) != 1 {
		t.Fatalf("expected 1 role added, got %d", len(addedRoles))
	}
	if addedRoles[0]["name"] != "admin" {
		t.Errorf("expected role name admin, got %v", addedRoles[0]["name"])
	}
}

func TestEnsureServiceAccountRolesGetSAUserError(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients/{uuid}/service-account-user": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	roles := &config.UserRoles{Realm: []string{"admin"}}

	p := New(c, &config.Config{})
	err := p.ensureServiceAccountRoles(context.Background(), "test-realm", "client-uuid-1", "my-client", roles)
	if err == nil {
		t.Fatal("expected error when GetServiceAccountUser fails")
	}
}

func TestEnsureServiceAccountRolesInvalidSAUserID(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients/{uuid}/service-account-user": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"id":       12345,
				"username": "service-account-my-client",
			})
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	roles := &config.UserRoles{Realm: []string{"admin"}}

	p := New(c, &config.Config{})
	err := p.ensureServiceAccountRoles(context.Background(), "test-realm", "client-uuid-1", "my-client", roles)
	if err == nil {
		t.Fatal("expected error for invalid service account user id type")
	}
}

func TestEnsureServiceAccountRolesWithClientRoles(t *testing.T) {
	var mu sync.Mutex
	var addedClientRoles []map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients/{uuid}/service-account-user": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"id":       "sa-user-uuid",
				"username": "service-account-my-client",
			})
		},
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "target-client-uuid", "clientId": "target-client"},
			})
		},
		"GET /admin/realms/{realm}/users/{id}/role-mappings/clients/{clientUUID}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/clients/{uuid}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"id": "role-id-1", "name": "editor"})
		},
		"POST /admin/realms/{realm}/users/{id}/role-mappings/clients/{clientUUID}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&addedClientRoles)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	roles := &config.UserRoles{
		Clients: map[string][]string{
			"target-client": {"editor"},
		},
	}

	p := New(c, &config.Config{})
	if err := p.ensureServiceAccountRoles(context.Background(), "test-realm", "client-uuid-1", "my-client", roles); err != nil {
		t.Fatalf("ensureServiceAccountRoles: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(addedClientRoles) != 1 {
		t.Fatalf("expected 1 client role added, got %d", len(addedClientRoles))
	}
	if addedClientRoles[0]["name"] != "editor" {
		t.Errorf("expected role name editor, got %v", addedClientRoles[0]["name"])
	}
}
