package provisioner

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"keycloak-provisioner/internal/config"
)

func TestRunFullProvisioning(t *testing.T) {
	var mu sync.Mutex
	realmCreated := false
	clientCreated := false
	realmRoleCreated := false
	clientRoleCreated := false
	pmCreated := false
	groupCreated := false
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
		"POST /admin/realms": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			realmCreated = true
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			clientCreated = true
			mu.Unlock()
			w.Header().Set("Location", r.URL.String()+"/uuid-1")
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/clients/{uuid}/protocol-mappers/models": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/clients/{uuid}/protocol-mappers/models": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			pmCreated = true
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/clients/{uuid}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
		"POST /admin/realms/{realm}/clients/{uuid}/roles": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			clientRoleCreated = true
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
		"POST /admin/realms/{realm}/roles": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			realmRoleCreated = true
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			groupCreated = true
			mu.Unlock()
			w.Header().Set("Location", r.URL.String()+"/group-uuid-1")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	enabled := true
	cfg := &config.Config{
		Realms: []config.Realm{
			{
				Realm:   "test-realm",
				Enabled: &enabled,
				Clients: []config.Client{
					{
						ClientID: "my-app",
						Enabled:  &enabled,
						ProtocolMappers: []config.ProtocolMapper{
							{
								Name:           "aud",
								ProtocolMapper: "oidc-audience-mapper",
							},
						},
						ClientRoles: []config.ClientRole{
							{Name: "admin"},
						},
					},
				},
				Roles: []config.RealmRole{
					{Name: "app-admin"},
				},
				Groups: []config.Group{
					{Name: "engineering"},
				},
			},
		},
	}

	p := New(c, cfg)
	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !realmCreated {
		t.Error("realm was not created")
	}
	if !clientCreated {
		t.Error("client was not created")
	}
	if !realmRoleCreated {
		t.Error("realm role was not created")
	}
	if !clientRoleCreated {
		t.Error("client role was not created")
	}
	if !pmCreated {
		t.Error("protocol mapper was not created")
	}
	if !groupCreated {
		t.Error("group was not created")
	}
}

func TestProvisionRealmErrorOnGetRealm(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	cfg := &config.Config{
		Realms: []config.Realm{{Realm: "test-realm"}},
	}

	p := New(c, cfg)
	err := p.Run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestProvisionRealmErrorOnClient(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
		"POST /admin/realms": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	cfg := &config.Config{
		Realms: []config.Realm{
			{
				Realm:   "test-realm",
				Clients: []config.Client{{ClientID: "app"}},
			},
		},
	}

	p := New(c, cfg)
	err := p.Run(context.Background())
	if err == nil {
		t.Fatal("expected error on client provisioning failure")
	}
}

func TestProvisionRealmErrorOnProtocolMapper(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
		"POST /admin/realms": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", r.URL.String()+"/uuid-1")
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/clients/{uuid}/protocol-mappers/models": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	cfg := &config.Config{
		Realms: []config.Realm{
			{
				Realm: "test-realm",
				Clients: []config.Client{
					{
						ClientID: "app",
						ProtocolMappers: []config.ProtocolMapper{
							{Name: "mapper", ProtocolMapper: "oidc-audience-mapper"},
						},
					},
				},
			},
		},
	}

	p := New(c, cfg)
	err := p.Run(context.Background())
	if err == nil {
		t.Fatal("expected error on protocol mapper provisioning failure")
	}
}

func TestProvisionRealmErrorOnClientRole(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
		"POST /admin/realms": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", r.URL.String()+"/uuid-1")
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/clients/{uuid}/protocol-mappers/models": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/clients/{uuid}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	cfg := &config.Config{
		Realms: []config.Realm{
			{
				Realm: "test-realm",
				Clients: []config.Client{
					{
						ClientID:    "app",
						ClientRoles: []config.ClientRole{{Name: "admin"}},
					},
				},
			},
		},
	}

	p := New(c, cfg)
	err := p.Run(context.Background())
	if err == nil {
		t.Fatal("expected error on client role provisioning failure")
	}
}

func TestProvisionRealmErrorOnRealmRole(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
		"POST /admin/realms": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	cfg := &config.Config{
		Realms: []config.Realm{
			{
				Realm: "test-realm",
				Roles: []config.RealmRole{{Name: "admin"}},
			},
		},
	}

	p := New(c, cfg)
	err := p.Run(context.Background())
	if err == nil {
		t.Fatal("expected error on realm role provisioning failure")
	}
}

func TestRunWithStrategy(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"realm": "test-realm"})
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	cfg := &config.Config{
		Strategy: "create",
		Realms: []config.Realm{
			{Realm: "test-realm"},
		},
	}

	p := New(c, cfg)
	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunFullProvisioningWithUsers(t *testing.T) {
	var mu sync.Mutex
	userCreated := false
	passwordSet := false
	realmRolesAssigned := false
	roleCreated := false

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
		"POST /admin/realms": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			created := roleCreated
			mu.Unlock()
			if created {
				// After creation, return the role for user role assignment lookup
				json.NewEncoder(w).Encode(map[string]any{"id": "role-id-1", "name": "app-admin"})
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		},
		"POST /admin/realms/{realm}/roles": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			roleCreated = true
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			userCreated = true
			mu.Unlock()
			w.Header().Set("Location", r.URL.String()+"/user-uuid-1")
			w.WriteHeader(http.StatusCreated)
		},
		"PUT /admin/realms/{realm}/users/{id}/reset-password": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			passwordSet = true
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/users/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/users/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			realmRolesAssigned = true
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	enabled := true
	cfg := &config.Config{
		Realms: []config.Realm{
			{
				Realm:   "test-realm",
				Enabled: &enabled,
				Roles:   []config.RealmRole{{Name: "app-admin"}},
				Users: []config.User{
					{
						Username: "admin",
						Password: "password",
						Enabled:  &enabled,
						Roles: &config.UserRoles{
							Realm: []string{"app-admin"},
						},
					},
				},
			},
		},
	}

	p := New(c, cfg)
	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !userCreated {
		t.Error("user was not created")
	}
	if !passwordSet {
		t.Error("password was not set")
	}
	if !realmRolesAssigned {
		t.Error("realm roles were not assigned")
	}
}

func TestRunWithMasterRealm(t *testing.T) {
	var mu sync.Mutex
	masterUpdated := false
	masterUserCreated := false

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"realm":       "master",
				"sslRequired": "none",
			})
		},
		"PUT /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			masterUpdated = true
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			masterUserCreated = true
			mu.Unlock()
			w.Header().Set("Location", r.URL.String()+"/user-uuid-1")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	cfg := &config.Config{
		MasterRealm: &config.MasterRealmConfig{
			SslRequired: "external",
			Users: []config.User{
				{Username: "admin-new"},
			},
		},
	}

	p := New(c, cfg)
	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !masterUpdated {
		t.Error("master realm was not updated")
	}
	if !masterUserCreated {
		t.Error("master realm user was not created")
	}
}

func TestRunWithServiceAccountRoles(t *testing.T) {
	var mu sync.Mutex
	saRolesAssigned := false
	roleCreated := false

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
		"POST /admin/realms": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", r.URL.String()+"/client-uuid-1")
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/clients/{uuid}/protocol-mappers/models": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/clients/{uuid}/service-account-user": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"id":       "sa-user-uuid",
				"username": "service-account-my-service",
			})
		},
		"GET /admin/realms/{realm}/users/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			created := roleCreated
			mu.Unlock()
			if created {
				json.NewEncoder(w).Encode(map[string]any{"id": "role-id-1", "name": "app-admin"})
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		},
		"POST /admin/realms/{realm}/roles": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			roleCreated = true
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		},
		"POST /admin/realms/{realm}/users/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			saRolesAssigned = true
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	saEnabled := true
	cfg := &config.Config{
		Realms: []config.Realm{
			{
				Realm: "test-realm",
				Clients: []config.Client{
					{
						ClientID:               "my-service",
						ServiceAccountsEnabled: &saEnabled,
						ServiceAccountRoles: &config.UserRoles{
							Realm: []string{"app-admin"},
						},
					},
				},
				Roles: []config.RealmRole{{Name: "app-admin"}},
			},
		},
	}

	p := New(c, cfg)
	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !saRolesAssigned {
		t.Error("service account roles were not assigned")
	}
}
