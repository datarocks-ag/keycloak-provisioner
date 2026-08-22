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

func TestEnsureUserCreate(t *testing.T) {
	var mu sync.Mutex
	var createdBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&createdBody)
			w.Header().Set("Location", r.URL.String()+"/user-uuid-1")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	enabled := true
	user := config.User{
		Username: "testuser",
		Enabled:  &enabled,
		Email:    "test@example.com",
	}

	p := New(c, &config.Config{})
	if err := p.ensureUser(context.Background(), "test-realm", user, "update"); err != nil {
		t.Fatalf("ensureUser: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if createdBody["username"] != "testuser" {
		t.Errorf("expected username testuser, got %v", createdBody["username"])
	}
	if createdBody["enabled"] != true {
		t.Errorf("expected enabled true, got %v", createdBody["enabled"])
	}
}

func TestEnsureUserUpdate(t *testing.T) {
	var mu sync.Mutex
	var updatedBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "user-uuid-1", "username": "testuser"},
			})
		},
		"PUT /admin/realms/{realm}/users/{id}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&updatedBody)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	user := config.User{
		Username: "testuser",
		Email:    "updated@example.com",
	}

	p := New(c, &config.Config{})
	if err := p.ensureUser(context.Background(), "test-realm", user, "update"); err != nil {
		t.Fatalf("ensureUser: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if updatedBody["email"] != "updated@example.com" {
		t.Errorf("expected email updated@example.com, got %v", updatedBody["email"])
	}
}

func TestEnsureUserCreateStrategySkipsExisting(t *testing.T) {
	var updateCalled atomic.Bool

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "user-uuid-1", "username": "testuser"},
			})
		},
		"PUT /admin/realms/{realm}/users/{id}": func(w http.ResponseWriter, r *http.Request) {
			updateCalled.Store(true)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	user := config.User{
		Username: "testuser",
		Email:    "should-not-update@example.com",
	}

	p := New(c, &config.Config{})
	if err := p.ensureUser(context.Background(), "test-realm", user, "create"); err != nil {
		t.Fatalf("ensureUser: %v", err)
	}

	if updateCalled.Load() {
		t.Error("expected PUT not to be called with strategy=create")
	}
}

func TestEnsureUserWithPassword(t *testing.T) {
	var mu sync.Mutex
	var passwordBody map[string]any
	passwordSet := false

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", r.URL.String()+"/user-uuid-1")
			w.WriteHeader(http.StatusCreated)
		},
		"PUT /admin/realms/{realm}/users/{id}/reset-password": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&passwordBody)
			passwordSet = true
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	user := config.User{
		Username: "testuser",
		Password: "secret123",
	}

	p := New(c, &config.Config{})
	if err := p.ensureUser(context.Background(), "test-realm", user, "update"); err != nil {
		t.Fatalf("ensureUser: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !passwordSet {
		t.Error("expected password to be set")
	}
	if passwordBody["value"] != "secret123" {
		t.Errorf("expected password secret123, got %v", passwordBody["value"])
	}
}

func TestEnsureUserWithInitialPassword_NewUser(t *testing.T) {
	var mu sync.Mutex
	var passwordBody map[string]any
	passwordSet := false

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", r.URL.String()+"/user-uuid-1")
			w.WriteHeader(http.StatusCreated)
		},
		"PUT /admin/realms/{realm}/users/{id}/reset-password": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&passwordBody)
			passwordSet = true
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	user := config.User{
		Username:        "newuser",
		InitialPassword: "changeme",
	}

	p := New(c, &config.Config{})
	if err := p.ensureUser(context.Background(), "test-realm", user, "update"); err != nil {
		t.Fatalf("ensureUser: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !passwordSet {
		t.Error("expected initial password to be set for new user")
	}
	if passwordBody["value"] != "changeme" {
		t.Errorf("expected password changeme, got %v", passwordBody["value"])
	}
	if passwordBody["temporary"] != true {
		t.Errorf("expected temporary=true, got %v", passwordBody["temporary"])
	}
}

func TestEnsureUserWithInitialPassword_ExistingUser(t *testing.T) {
	var passwordResetCalled atomic.Bool

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "existing-uuid", "username": "existinguser"},
			})
		},
		"PUT /admin/realms/{realm}/users/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"PUT /admin/realms/{realm}/users/{id}/reset-password": func(w http.ResponseWriter, r *http.Request) {
			passwordResetCalled.Store(true)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	user := config.User{
		Username:        "existinguser",
		InitialPassword: "changeme",
	}

	p := New(c, &config.Config{})
	if err := p.ensureUser(context.Background(), "test-realm", user, "update"); err != nil {
		t.Fatalf("ensureUser: %v", err)
	}

	if passwordResetCalled.Load() {
		t.Error("expected initial password not to be set for existing user")
	}
}

func TestEnsureUserWithRealmRoles(t *testing.T) {
	var mu sync.Mutex
	var addedRoles []map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", r.URL.String()+"/user-uuid-1")
			w.WriteHeader(http.StatusCreated)
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
	user := config.User{
		Username: "testuser",
		Roles: &config.UserRoles{
			Realm: []string{"admin"},
		},
	}

	p := New(c, &config.Config{})
	if err := p.ensureUser(context.Background(), "test-realm", user, "update"); err != nil {
		t.Fatalf("ensureUser: %v", err)
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

func TestEnsureUserRolesSkipsAlreadyAssigned(t *testing.T) {
	var realmRolesAdded atomic.Bool

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "role-id-1", "name": "admin"},
			})
		},
		"POST /admin/realms/{realm}/users/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			realmRolesAdded.Store(true)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	roles := &config.UserRoles{
		Realm: []string{"admin"},
	}

	p := New(c, &config.Config{})
	if err := p.ensureUserRoles(context.Background(), "test-realm", "user-uuid-1", "testuser", roles); err != nil {
		t.Fatalf("ensureUserRoles: %v", err)
	}

	if realmRolesAdded.Load() {
		t.Error("expected POST not to be called when role already assigned")
	}
}

func TestBuildUserBody(t *testing.T) {
	enabled := true
	emailVerified := false

	user := config.User{
		Username:      "testuser",
		Enabled:       &enabled,
		Email:         "test@example.com",
		FirstName:     "Test",
		LastName:      "User",
		EmailVerified: &emailVerified,
	}

	body := buildUserBody(user)

	checks := map[string]any{
		"username":      "testuser",
		"enabled":       true,
		"email":         "test@example.com",
		"firstName":     "Test",
		"lastName":      "User",
		"emailVerified": false,
	}

	for key, want := range checks {
		got, ok := body[key]
		if !ok {
			t.Errorf("missing key %q", key)
			continue
		}
		if got != want {
			t.Errorf("key %q: got %v, want %v", key, got, want)
		}
	}
}

func TestEnsureUserInvalidID(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": 12345, "username": "testuser"},
			})
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	user := config.User{Username: "testuser"}

	p := New(c, &config.Config{})
	err := p.ensureUser(context.Background(), "test-realm", user, "update")
	if err == nil {
		t.Fatal("expected error for invalid id type")
	}
}

func TestEnsureUserNoPasswordWhenEmpty(t *testing.T) {
	var passwordResetCalled atomic.Bool

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", r.URL.String()+"/user-uuid-1")
			w.WriteHeader(http.StatusCreated)
		},
		"PUT /admin/realms/{realm}/users/{id}/reset-password": func(w http.ResponseWriter, r *http.Request) {
			passwordResetCalled.Store(true)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	user := config.User{Username: "testuser"}

	p := New(c, &config.Config{})
	if err := p.ensureUser(context.Background(), "test-realm", user, "update"); err != nil {
		t.Fatalf("ensureUser: %v", err)
	}

	if passwordResetCalled.Load() {
		t.Error("expected password reset not to be called when password is empty")
	}
}

func TestEnsureUserCreateError(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("conflict"))
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	user := config.User{Username: "testuser"}

	p := New(c, &config.Config{})
	err := p.ensureUser(context.Background(), "test-realm", user, "update")
	if err == nil {
		t.Fatal("expected error on user creation failure")
	}
}

func TestEnsureUserUpdateError(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "user-uuid-1", "username": "testuser"},
			})
		},
		"PUT /admin/realms/{realm}/users/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	user := config.User{Username: "testuser", Email: "test@example.com"}

	p := New(c, &config.Config{})
	err := p.ensureUser(context.Background(), "test-realm", user, "update")
	if err == nil {
		t.Fatal("expected error on user update failure")
	}
}

func TestEnsureUserPasswordResetError(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", r.URL.String()+"/user-uuid-1")
			w.WriteHeader(http.StatusCreated)
		},
		"PUT /admin/realms/{realm}/users/{id}/reset-password": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad"))
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	user := config.User{Username: "testuser", Password: "secret"}

	p := New(c, &config.Config{})
	err := p.ensureUser(context.Background(), "test-realm", user, "update")
	if err == nil {
		t.Fatal("expected error on password reset failure")
	}
}

func TestEnsureUserRolesRealmRoleMappingsError(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	roles := &config.UserRoles{Realm: []string{"admin"}}

	p := New(c, &config.Config{})
	err := p.ensureUserRoles(context.Background(), "test-realm", "user-uuid-1", "testuser", roles)
	if err == nil {
		t.Fatal("expected error when GetUserRealmRoleMappings fails")
	}
}

func TestEnsureUserRolesRealmRoleNotFound(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	roles := &config.UserRoles{Realm: []string{"nonexistent"}}

	p := New(c, &config.Config{})
	err := p.ensureUserRoles(context.Background(), "test-realm", "user-uuid-1", "testuser", roles)
	if err == nil {
		t.Fatal("expected error when realm role not found")
	}
}

func TestEnsureUserRolesRealmRoleLookupError(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	roles := &config.UserRoles{Realm: []string{"admin"}}

	p := New(c, &config.Config{})
	err := p.ensureUserRoles(context.Background(), "test-realm", "user-uuid-1", "testuser", roles)
	if err == nil {
		t.Fatal("expected error when GetRealmRole fails")
	}
}

func TestEnsureUserRolesAddRealmRoleMappingsError(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"id": "role-id-1", "name": "admin"})
		},
		"POST /admin/realms/{realm}/users/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	roles := &config.UserRoles{Realm: []string{"admin"}}

	p := New(c, &config.Config{})
	err := p.ensureUserRoles(context.Background(), "test-realm", "user-uuid-1", "testuser", roles)
	if err == nil {
		t.Fatal("expected error when AddUserRealmRoleMappings fails")
	}
}

func TestEnsureUserRolesClientRoleAssignment(t *testing.T) {
	var mu sync.Mutex
	var addedRoles []map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "client-uuid-1", "clientId": "my-app"},
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
			json.NewDecoder(r.Body).Decode(&addedRoles)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	roles := &config.UserRoles{
		Clients: map[string][]string{
			"my-app": {"editor"},
		},
	}

	p := New(c, &config.Config{})
	if err := p.ensureUserRoles(context.Background(), "test-realm", "user-uuid-1", "testuser", roles); err != nil {
		t.Fatalf("ensureUserRoles: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(addedRoles) != 1 {
		t.Fatalf("expected 1 client role added, got %d", len(addedRoles))
	}
	if addedRoles[0]["name"] != "editor" {
		t.Errorf("expected role name editor, got %v", addedRoles[0]["name"])
	}
}

func TestEnsureUserRolesClientRoleSkipsAlreadyAssigned(t *testing.T) {
	var clientRolesAdded atomic.Bool

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "client-uuid-1", "clientId": "my-app"},
			})
		},
		"GET /admin/realms/{realm}/users/{id}/role-mappings/clients/{clientUUID}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "role-id-1", "name": "editor"},
			})
		},
		"POST /admin/realms/{realm}/users/{id}/role-mappings/clients/{clientUUID}": func(w http.ResponseWriter, r *http.Request) {
			clientRolesAdded.Store(true)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	roles := &config.UserRoles{
		Clients: map[string][]string{
			"my-app": {"editor"},
		},
	}

	p := New(c, &config.Config{})
	if err := p.ensureUserRoles(context.Background(), "test-realm", "user-uuid-1", "testuser", roles); err != nil {
		t.Fatalf("ensureUserRoles: %v", err)
	}

	if clientRolesAdded.Load() {
		t.Error("expected POST not to be called when client role already assigned")
	}
}

func TestEnsureUserRolesClientNotFound(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	roles := &config.UserRoles{
		Clients: map[string][]string{
			"nonexistent": {"editor"},
		},
	}

	p := New(c, &config.Config{})
	err := p.ensureUserRoles(context.Background(), "test-realm", "user-uuid-1", "testuser", roles)
	if err == nil {
		t.Fatal("expected error when client not found")
	}
}

func TestEnsureUserRolesClientLookupError(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	roles := &config.UserRoles{
		Clients: map[string][]string{
			"my-app": {"editor"},
		},
	}

	p := New(c, &config.Config{})
	err := p.ensureUserRoles(context.Background(), "test-realm", "user-uuid-1", "testuser", roles)
	if err == nil {
		t.Fatal("expected error when GetClients fails")
	}
}

func TestEnsureUserRolesClientInvalidID(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": 12345, "clientId": "my-app"},
			})
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	roles := &config.UserRoles{
		Clients: map[string][]string{
			"my-app": {"editor"},
		},
	}

	p := New(c, &config.Config{})
	err := p.ensureUserRoles(context.Background(), "test-realm", "user-uuid-1", "testuser", roles)
	if err == nil {
		t.Fatal("expected error for invalid client id type")
	}
}

func TestEnsureUserRolesGetClientRoleMappingsError(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "client-uuid-1", "clientId": "my-app"},
			})
		},
		"GET /admin/realms/{realm}/users/{id}/role-mappings/clients/{clientUUID}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	roles := &config.UserRoles{
		Clients: map[string][]string{
			"my-app": {"editor"},
		},
	}

	p := New(c, &config.Config{})
	err := p.ensureUserRoles(context.Background(), "test-realm", "user-uuid-1", "testuser", roles)
	if err == nil {
		t.Fatal("expected error when GetUserClientRoleMappings fails")
	}
}

func TestEnsureUserRolesClientRoleNotFound(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "client-uuid-1", "clientId": "my-app"},
			})
		},
		"GET /admin/realms/{realm}/users/{id}/role-mappings/clients/{clientUUID}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/clients/{uuid}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	roles := &config.UserRoles{
		Clients: map[string][]string{
			"my-app": {"nonexistent"},
		},
	}

	p := New(c, &config.Config{})
	err := p.ensureUserRoles(context.Background(), "test-realm", "user-uuid-1", "testuser", roles)
	if err == nil {
		t.Fatal("expected error when client role not found")
	}
}

func TestEnsureUserRolesClientRoleLookupError(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "client-uuid-1", "clientId": "my-app"},
			})
		},
		"GET /admin/realms/{realm}/users/{id}/role-mappings/clients/{clientUUID}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/clients/{uuid}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	roles := &config.UserRoles{
		Clients: map[string][]string{
			"my-app": {"editor"},
		},
	}

	p := New(c, &config.Config{})
	err := p.ensureUserRoles(context.Background(), "test-realm", "user-uuid-1", "testuser", roles)
	if err == nil {
		t.Fatal("expected error when GetClientRole fails")
	}
}

func TestEnsureUserRolesAddClientRoleMappingsError(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "client-uuid-1", "clientId": "my-app"},
			})
		},
		"GET /admin/realms/{realm}/users/{id}/role-mappings/clients/{clientUUID}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/clients/{uuid}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"id": "role-id-1", "name": "editor"})
		},
		"POST /admin/realms/{realm}/users/{id}/role-mappings/clients/{clientUUID}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	roles := &config.UserRoles{
		Clients: map[string][]string{
			"my-app": {"editor"},
		},
	}

	p := New(c, &config.Config{})
	err := p.ensureUserRoles(context.Background(), "test-realm", "user-uuid-1", "testuser", roles)
	if err == nil {
		t.Fatal("expected error when AddUserClientRoleMappings fails")
	}
}

func TestEnsureUserWithClientRoles(t *testing.T) {
	var mu sync.Mutex
	var addedClientRoles []map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", r.URL.String()+"/user-uuid-1")
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "client-uuid-1", "clientId": "my-app"},
			})
		},
		"GET /admin/realms/{realm}/users/{id}/role-mappings/clients/{clientUUID}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/clients/{uuid}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"id": "role-id-1", "name": "admin"})
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
	user := config.User{
		Username: "testuser",
		Roles: &config.UserRoles{
			Clients: map[string][]string{
				"my-app": {"admin"},
			},
		},
	}

	p := New(c, &config.Config{})
	if err := p.ensureUser(context.Background(), "test-realm", user, "update"); err != nil {
		t.Fatalf("ensureUser: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(addedClientRoles) != 1 {
		t.Fatalf("expected 1 client role added, got %d", len(addedClientRoles))
	}
	if addedClientRoles[0]["name"] != "admin" {
		t.Errorf("expected role name admin, got %v", addedClientRoles[0]["name"])
	}
}

// --- Group tests ---

func TestEnsureUserGroupsAddsMembership(t *testing.T) {
	var addedGroupID atomic.Value

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "user-uuid-1", "username": "testuser"},
			})
		},
		"PUT /admin/realms/{realm}/users/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/users/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "group-uuid-1", "name": "engineering"},
			})
		},
		"PUT /admin/realms/{realm}/users/{id}/groups/{groupId}": func(w http.ResponseWriter, r *http.Request) {
			addedGroupID.Store(r.PathValue("groupId"))
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	user := config.User{
		Username: "testuser",
		Groups:   []string{"engineering"},
	}

	p := New(c, &config.Config{})
	if err := p.ensureUser(context.Background(), "test-realm", user, "update"); err != nil {
		t.Fatalf("ensureUser: %v", err)
	}

	if got, _ := addedGroupID.Load().(string); got != "group-uuid-1" {
		t.Errorf("expected membership added to group-uuid-1, got %q", got)
	}
}

func TestEnsureUserGroupsNestedPath(t *testing.T) {
	var addedGroupID atomic.Value

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "user-uuid-1", "username": "testuser"},
			})
		},
		"PUT /admin/realms/{realm}/users/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/users/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "eng-uuid", "name": "engineering"},
			})
		},
		"GET /admin/realms/{realm}/groups/{id}/children": func(w http.ResponseWriter, r *http.Request) {
			if r.PathValue("id") != "eng-uuid" {
				t.Errorf("expected children lookup on eng-uuid, got %s", r.PathValue("id"))
			}
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "backend-uuid", "name": "backend"},
			})
		},
		"PUT /admin/realms/{realm}/users/{id}/groups/{groupId}": func(w http.ResponseWriter, r *http.Request) {
			addedGroupID.Store(r.PathValue("groupId"))
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	user := config.User{
		Username: "testuser",
		Groups:   []string{"/engineering/backend"},
	}

	p := New(c, &config.Config{})
	if err := p.ensureUser(context.Background(), "test-realm", user, "update"); err != nil {
		t.Fatalf("ensureUser: %v", err)
	}

	if got, _ := addedGroupID.Load().(string); got != "backend-uuid" {
		t.Errorf("expected membership added to backend-uuid, got %q", got)
	}
}

func TestEnsureUserGroupsSkipsExistingMembership(t *testing.T) {
	var addCalled atomic.Bool

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "user-uuid-1", "username": "testuser"},
			})
		},
		"PUT /admin/realms/{realm}/users/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/users/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "group-uuid-1", "name": "engineering", "path": "/engineering"},
			})
		},
		"PUT /admin/realms/{realm}/users/{id}/groups/{groupId}": func(w http.ResponseWriter, r *http.Request) {
			addCalled.Store(true)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	user := config.User{
		Username: "testuser",
		Groups:   []string{"engineering"}, // leading slash optional
	}

	p := New(c, &config.Config{})
	if err := p.ensureUser(context.Background(), "test-realm", user, "update"); err != nil {
		t.Fatalf("ensureUser: %v", err)
	}

	if addCalled.Load() {
		t.Error("expected no membership add for existing member")
	}
}

func TestEnsureUserGroupsMissingGroupWarnsAndContinues(t *testing.T) {
	var addedGroupID atomic.Value

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "user-uuid-1", "username": "testuser"},
			})
		},
		"PUT /admin/realms/{realm}/users/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/users/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "group-uuid-1", "name": "engineering"},
			})
		},
		"PUT /admin/realms/{realm}/users/{id}/groups/{groupId}": func(w http.ResponseWriter, r *http.Request) {
			addedGroupID.Store(r.PathValue("groupId"))
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	user := config.User{
		Username: "testuser",
		// "does-not-exist" must be skipped with a warning; "engineering" must
		// still be processed afterwards.
		Groups: []string{"does-not-exist", "engineering"},
	}

	p := New(c, &config.Config{})
	if err := p.ensureUser(context.Background(), "test-realm", user, "update"); err != nil {
		t.Fatalf("ensureUser: %v", err)
	}

	if got, _ := addedGroupID.Load().(string); got != "group-uuid-1" {
		t.Errorf("expected membership added to group-uuid-1 after skipping missing group, got %q", got)
	}
}
