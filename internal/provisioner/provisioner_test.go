package provisioner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"keycloak-provisioner/internal/client"
	"keycloak-provisioner/internal/config"
)

// testServer creates an httptest.Server with a token endpoint and custom handlers.
func testServer(t *testing.T, handlers map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	// Token endpoint always succeeds
	mux.HandleFunc("POST /realms/master/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-token",
			"expires_in":   300,
		})
	})

	for pattern, handler := range handlers {
		mux.HandleFunc(pattern, handler)
	}

	return httptest.NewServer(mux)
}

func newTestClient(t *testing.T, serverURL string) *client.Client {
	t.Helper()
	c := client.New(serverURL, "admin", "admin")
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return c
}

func TestEnsureRealmCreate(t *testing.T) {
	var mu sync.Mutex
	var createdBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
		"POST /admin/realms": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&createdBody)
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
			},
		},
	}

	p := New(c, cfg)
	if err := p.ensureRealm(context.Background(), cfg.Realms[0], "update"); err != nil {
		t.Fatalf("ensureRealm: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if createdBody["realm"] != "test-realm" {
		t.Errorf("expected realm test-realm, got %v", createdBody["realm"])
	}
	if createdBody["enabled"] != true {
		t.Errorf("expected enabled true, got %v", createdBody["enabled"])
	}
}

func TestEnsureRealmCreateStrategySkipsExisting(t *testing.T) {
	var updateCalled atomic.Bool

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"realm": "test-realm"})
		},
		"PUT /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			updateCalled.Store(true)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	cfg := &config.Config{
		Realms: []config.Realm{
			{Realm: "test-realm"},
		},
	}

	p := New(c, cfg)
	if err := p.ensureRealm(context.Background(), cfg.Realms[0], "create"); err != nil {
		t.Fatalf("ensureRealm: %v", err)
	}

	if updateCalled.Load() {
		t.Error("expected PUT not to be called with strategy=create")
	}
}

func TestEnsureRealmUpdate(t *testing.T) {
	var mu sync.Mutex
	var updatedBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"realm": "test-realm"})
		},
		"PUT /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&updatedBody)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	cfg := &config.Config{
		Realms: []config.Realm{
			{
				Realm:       "test-realm",
				DisplayName: "Updated Realm",
			},
		},
	}

	p := New(c, cfg)
	if err := p.ensureRealm(context.Background(), cfg.Realms[0], "update"); err != nil {
		t.Fatalf("ensureRealm: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if updatedBody["displayName"] != "Updated Realm" {
		t.Errorf("expected displayName Updated Realm, got %v", updatedBody["displayName"])
	}
}

func TestEnsureClientCreate(t *testing.T) {
	var mu sync.Mutex
	var createdBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			// Client doesn't exist
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&createdBody)
			w.Header().Set("Location", r.URL.String()+"/uuid-123")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	enabled := true
	clientCfg := config.Client{
		ClientID: "my-app",
		Enabled:  &enabled,
		Protocol: "openid-connect",
	}

	p := New(c, &config.Config{})
	uuid, err := p.ensureClient(context.Background(), "test-realm", clientCfg, "update")
	if err != nil {
		t.Fatalf("ensureClient: %v", err)
	}

	if uuid != "uuid-123" {
		t.Errorf("expected uuid-123, got %s", uuid)
	}

	mu.Lock()
	defer mu.Unlock()
	if createdBody["clientId"] != "my-app" {
		t.Errorf("expected clientId my-app, got %v", createdBody["clientId"])
	}
}

func TestEnsureClientUpdate(t *testing.T) {
	var mu sync.Mutex
	var updatedBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "uuid-456", "clientId": "my-app"},
			})
		},
		"PUT /admin/realms/{realm}/clients/{uuid}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&updatedBody)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	clientCfg := config.Client{
		ClientID: "my-app",
		Secret:   "new-secret",
	}

	p := New(c, &config.Config{})
	uuid, err := p.ensureClient(context.Background(), "test-realm", clientCfg, "update")
	if err != nil {
		t.Fatalf("ensureClient: %v", err)
	}

	if uuid != "uuid-456" {
		t.Errorf("expected uuid-456, got %s", uuid)
	}

	mu.Lock()
	defer mu.Unlock()
	if updatedBody["secret"] != "new-secret" {
		t.Errorf("expected secret new-secret, got %v", updatedBody["secret"])
	}
}

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

func TestEnsureProtocolMapperCreate(t *testing.T) {
	var mu sync.Mutex
	var createdBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients/{uuid}/protocol-mappers/models": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/clients/{uuid}/protocol-mappers/models": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&createdBody)
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	pm := config.ProtocolMapper{
		Name:           "audience-mapper",
		Protocol:       "openid-connect",
		ProtocolMapper: "oidc-audience-mapper",
		Config:         map[string]string{"included.client.audience": "my-app"},
	}

	p := New(c, &config.Config{})
	if err := p.ensureProtocolMapper(context.Background(), "test-realm", "uuid-123", pm, "update"); err != nil {
		t.Fatalf("ensureProtocolMapper: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if createdBody["name"] != "audience-mapper" {
		t.Errorf("expected name audience-mapper, got %v", createdBody["name"])
	}
	if createdBody["protocolMapper"] != "oidc-audience-mapper" {
		t.Errorf("expected protocolMapper oidc-audience-mapper, got %v", createdBody["protocolMapper"])
	}
}

func TestEnsureProtocolMapperUpdate(t *testing.T) {
	var mu sync.Mutex
	var updatedBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients/{uuid}/protocol-mappers/models": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "pm-id-1", "name": "audience-mapper", "protocolMapper": "oidc-audience-mapper"},
			})
		},
		"PUT /admin/realms/{realm}/clients/{uuid}/protocol-mappers/models/{id}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&updatedBody)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	pm := config.ProtocolMapper{
		Name:           "audience-mapper",
		Protocol:       "openid-connect",
		ProtocolMapper: "oidc-audience-mapper",
		Config:         map[string]string{"included.client.audience": "updated-app"},
	}

	p := New(c, &config.Config{})
	if err := p.ensureProtocolMapper(context.Background(), "test-realm", "uuid-123", pm, "update"); err != nil {
		t.Fatalf("ensureProtocolMapper: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if updatedBody["id"] != "pm-id-1" {
		t.Errorf("expected id pm-id-1, got %v", updatedBody["id"])
	}
}

func TestEnsureClientCreateStrategySkipsExisting(t *testing.T) {
	var updateCalled atomic.Bool

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "uuid-789", "clientId": "my-app"},
			})
		},
		"PUT /admin/realms/{realm}/clients/{uuid}": func(w http.ResponseWriter, r *http.Request) {
			updateCalled.Store(true)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	clientCfg := config.Client{
		ClientID: "my-app",
		Secret:   "should-not-be-sent",
	}

	p := New(c, &config.Config{})
	uuid, err := p.ensureClient(context.Background(), "test-realm", clientCfg, "create")
	if err != nil {
		t.Fatalf("ensureClient: %v", err)
	}

	if uuid != "uuid-789" {
		t.Errorf("expected uuid-789, got %s", uuid)
	}
	if updateCalled.Load() {
		t.Error("expected PUT not to be called with strategy=create")
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

func TestEnsureProtocolMapperCreateStrategySkipsExisting(t *testing.T) {
	var updateCalled atomic.Bool

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients/{uuid}/protocol-mappers/models": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "pm-id-1", "name": "audience-mapper", "protocolMapper": "oidc-audience-mapper"},
			})
		},
		"PUT /admin/realms/{realm}/clients/{uuid}/protocol-mappers/models/{id}": func(w http.ResponseWriter, r *http.Request) {
			updateCalled.Store(true)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	pm := config.ProtocolMapper{
		Name:           "audience-mapper",
		ProtocolMapper: "oidc-audience-mapper",
		Config:         map[string]string{"included.client.audience": "should-not-update"},
	}

	p := New(c, &config.Config{})
	if err := p.ensureProtocolMapper(context.Background(), "test-realm", "uuid-123", pm, "create"); err != nil {
		t.Fatalf("ensureProtocolMapper: %v", err)
	}

	if updateCalled.Load() {
		t.Error("expected PUT not to be called with strategy=create")
	}
}

func TestRunFullProvisioning(t *testing.T) {
	var mu sync.Mutex
	realmCreated := false
	clientCreated := false
	realmRoleCreated := false
	clientRoleCreated := false
	pmCreated := false
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
}
