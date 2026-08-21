package provisioner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestEnsureClientInvalidID(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			// Return a client with a non-string id
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": 12345, "clientId": "my-app"},
			})
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	clientCfg := config.Client{ClientID: "my-app"}

	p := New(c, &config.Config{})
	_, err := p.ensureClient(context.Background(), "test-realm", clientCfg, "update")
	if err == nil {
		t.Fatal("expected error for invalid id type")
	}
}

func TestBuildClientBodyAllFields(t *testing.T) {
	enabled := true
	public := false
	stdFlow := true
	directAccess := false
	serviceAccounts := true
	bearerOnly := false
	consent := true
	frontchannel := false

	c := config.Client{
		ClientID:                  "app",
		Secret:                    "s3cr3t",
		Name:                      "My App",
		Enabled:                   &enabled,
		PublicClient:              &public,
		Protocol:                  "openid-connect",
		RootUrl:                   "https://app.example.com",
		BaseUrl:                   "/",
		AdminUrl:                  "https://app.example.com/admin",
		RedirectUris:              []string{"https://app.example.com/*"},
		WebOrigins:                []string{"https://app.example.com"},
		StandardFlowEnabled:       &stdFlow,
		DirectAccessGrantsEnabled: &directAccess,
		ServiceAccountsEnabled:    &serviceAccounts,
		BearerOnly:                &bearerOnly,
		ConsentRequired:           &consent,
		FrontchannelLogout:        &frontchannel,
		DefaultClientScopes:       []string{"openid", "profile"},
		OptionalClientScopes:      []string{"phone"},
		Attributes:                map[string]string{"key": "val"},
	}

	body := buildClientBody(c, nil, nil)

	checks := map[string]any{
		"clientId":                  "app",
		"secret":                    "s3cr3t",
		"name":                      "My App",
		"enabled":                   true,
		"publicClient":              false,
		"protocol":                  "openid-connect",
		"rootUrl":                   "https://app.example.com",
		"baseUrl":                   "/",
		"adminUrl":                  "https://app.example.com/admin",
		"standardFlowEnabled":       true,
		"directAccessGrantsEnabled": false,
		"serviceAccountsEnabled":    true,
		"bearerOnly":                false,
		"consentRequired":           true,
		"frontchannelLogout":        false,
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

	if _, ok := body["redirectUris"]; !ok {
		t.Error("missing redirectUris")
	}
	if _, ok := body["webOrigins"]; !ok {
		t.Error("missing webOrigins")
	}
	if _, ok := body["defaultClientScopes"]; !ok {
		t.Error("missing defaultClientScopes")
	}
	if _, ok := body["optionalClientScopes"]; !ok {
		t.Error("missing optionalClientScopes")
	}
	if _, ok := body["attributes"]; !ok {
		t.Error("missing attributes")
	}
}

func TestBuildClientBodyStandardTokenExchange(t *testing.T) {
	enabled := true
	disabled := false

	t.Run("enabled sets attribute true", func(t *testing.T) {
		body := buildClientBody(config.Client{
			ClientID:                     "app",
			StandardTokenExchangeEnabled: &enabled,
		}, nil, nil)
		attrs, ok := body["attributes"].(map[string]string)
		if !ok {
			t.Fatalf("expected attributes map, got %T", body["attributes"])
		}
		if attrs["standard.token.exchange.enabled"] != "true" {
			t.Errorf("got %q, want \"true\"", attrs["standard.token.exchange.enabled"])
		}
	})

	t.Run("disabled sets attribute false", func(t *testing.T) {
		body := buildClientBody(config.Client{
			ClientID:                     "app",
			StandardTokenExchangeEnabled: &disabled,
		}, nil, nil)
		attrs, _ := body["attributes"].(map[string]string)
		if attrs["standard.token.exchange.enabled"] != "false" {
			t.Errorf("got %q, want \"false\"", attrs["standard.token.exchange.enabled"])
		}
	})

	t.Run("nil omits attribute", func(t *testing.T) {
		body := buildClientBody(config.Client{ClientID: "app"}, nil, nil)
		if _, ok := body["attributes"]; ok {
			t.Errorf("expected no attributes key, got %v", body["attributes"])
		}
	})

	t.Run("merges with existing attributes without mutating config", func(t *testing.T) {
		userAttrs := map[string]string{"post.logout.redirect.uris": "+"}
		c := config.Client{
			ClientID:                     "app",
			Attributes:                   userAttrs,
			StandardTokenExchangeEnabled: &enabled,
		}
		body := buildClientBody(c, nil, nil)
		attrs, ok := body["attributes"].(map[string]string)
		if !ok {
			t.Fatalf("expected attributes map, got %T", body["attributes"])
		}
		if attrs["post.logout.redirect.uris"] != "+" {
			t.Errorf("user attribute lost: %v", attrs)
		}
		if attrs["standard.token.exchange.enabled"] != "true" {
			t.Errorf("token exchange attribute missing: %v", attrs)
		}
		if _, mutated := userAttrs["standard.token.exchange.enabled"]; mutated {
			t.Error("config Attributes map was mutated")
		}
	})

	t.Run("typed field wins over conflicting raw attribute", func(t *testing.T) {
		c := config.Client{
			ClientID:                     "app",
			Attributes:                   map[string]string{"standard.token.exchange.enabled": "false"},
			StandardTokenExchangeEnabled: &enabled,
		}
		body := buildClientBody(c, nil, nil)
		attrs, ok := body["attributes"].(map[string]string)
		if !ok {
			t.Fatalf("expected attributes map, got %T", body["attributes"])
		}
		if attrs["standard.token.exchange.enabled"] != "true" {
			t.Errorf("typed field should win: got %q, want \"true\"", attrs["standard.token.exchange.enabled"])
		}
	})
}

func TestBuildRealmBodyAllFields(t *testing.T) {
	enabled := true
	regAllowed := false
	resetPw := true

	realm := config.Realm{
		Realm:                "test",
		DisplayName:          "Test Realm",
		Enabled:              &enabled,
		LoginTheme:           "keycloak",
		RegistrationAllowed:  &regAllowed,
		ResetPasswordAllowed: &resetPw,
	}

	body := buildRealmBody(realm, nil)

	checks := map[string]any{
		"realm":                "test",
		"displayName":          "Test Realm",
		"enabled":              true,
		"loginTheme":           "keycloak",
		"registrationAllowed":  false,
		"resetPasswordAllowed": true,
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

func TestBuildProtocolMapperBodyWithConsentRequired(t *testing.T) {
	consent := true
	pm := config.ProtocolMapper{
		Name:            "mapper",
		Protocol:        "openid-connect",
		ProtocolMapper:  "oidc-audience-mapper",
		ConsentRequired: &consent,
		Config:          map[string]string{"key": "val"},
	}

	body := buildProtocolMapperBody(pm)

	if body["consentRequired"] != true {
		t.Errorf("expected consentRequired true, got %v", body["consentRequired"])
	}
	if body["protocol"] != "openid-connect" {
		t.Errorf("expected protocol openid-connect, got %v", body["protocol"])
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

func TestEnsureProtocolMapperInvalidID(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients/{uuid}/protocol-mappers/models": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": 123, "name": "mapper"}, // non-string id
			})
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	pm := config.ProtocolMapper{
		Name:           "mapper",
		ProtocolMapper: "oidc-audience-mapper",
	}

	p := New(c, &config.Config{})
	err := p.ensureProtocolMapper(context.Background(), "test-realm", "uuid-1", pm, "update")
	if err == nil {
		t.Fatal("expected error for non-string id")
	}
}

func TestBuildRealmBodyWithSslRequired(t *testing.T) {
	realm := config.Realm{
		Realm:       "test",
		SslRequired: "external",
	}

	body := buildRealmBody(realm, nil)

	if body["sslRequired"] != "external" {
		t.Errorf("expected sslRequired=external, got %v", body["sslRequired"])
	}
}

func TestBuildRealmBodyWithoutSslRequired(t *testing.T) {
	realm := config.Realm{
		Realm: "test",
	}

	body := buildRealmBody(realm, nil)

	if _, ok := body["sslRequired"]; ok {
		t.Error("sslRequired should not be set when empty")
	}
}

func TestEnsureMasterRealm(t *testing.T) {
	var mu sync.Mutex
	var updatedBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"realm":       "master",
				"sslRequired": "none",
			})
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
	mr := &config.MasterRealmConfig{
		SslRequired: "external",
	}

	p := New(c, &config.Config{MasterRealm: mr})
	if err := p.ensureMasterRealm(context.Background(), mr); err != nil {
		t.Fatalf("ensureMasterRealm: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if updatedBody["sslRequired"] != "external" {
		t.Errorf("expected sslRequired=external, got %v", updatedBody["sslRequired"])
	}
}

func TestEnsureMasterRealmNoChange(t *testing.T) {
	var updateCalled atomic.Bool

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"realm":       "master",
				"sslRequired": "external",
			})
		},
		"PUT /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			updateCalled.Store(true)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	mr := &config.MasterRealmConfig{
		SslRequired: "external",
	}

	p := New(c, &config.Config{MasterRealm: mr})
	if err := p.ensureMasterRealm(context.Background(), mr); err != nil {
		t.Fatalf("ensureMasterRealm: %v", err)
	}

	if updateCalled.Load() {
		t.Error("expected PUT not to be called when sslRequired already matches")
	}
}

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

func TestEnsureMasterRealmGetError(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	mr := &config.MasterRealmConfig{SslRequired: "external"}

	p := New(c, &config.Config{MasterRealm: mr})
	err := p.ensureMasterRealm(context.Background(), mr)
	if err == nil {
		t.Fatal("expected error when GetRealm fails")
	}
}

func TestEnsureMasterRealmUpdateError(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"realm":       "master",
				"sslRequired": "none",
			})
		},
		"PUT /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	mr := &config.MasterRealmConfig{SslRequired: "external"}

	p := New(c, &config.Config{MasterRealm: mr})
	err := p.ensureMasterRealm(context.Background(), mr)
	if err == nil {
		t.Fatal("expected error when UpdateRealm fails")
	}
}

func TestEnsureMasterRealmEmptySslRequired(t *testing.T) {
	var getCalled atomic.Bool

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			getCalled.Store(true)
			json.NewEncoder(w).Encode(map[string]any{"realm": "master"})
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	mr := &config.MasterRealmConfig{}

	p := New(c, &config.Config{MasterRealm: mr})
	if err := p.ensureMasterRealm(context.Background(), mr); err != nil {
		t.Fatalf("ensureMasterRealm: %v", err)
	}

	if getCalled.Load() {
		t.Error("expected GetRealm not to be called when sslRequired is empty")
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

func TestEnsureGroupCreate(t *testing.T) {
	var mu sync.Mutex
	var createdBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&createdBody)
			w.Header().Set("Location", r.URL.String()+"/group-uuid-1")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	g := config.Group{
		Name:       "engineering",
		Attributes: map[string][]string{"department": {"eng"}},
	}

	p := New(c, &config.Config{})
	if err := p.ensureGroup(context.Background(), "test-realm", "", g, "update"); err != nil {
		t.Fatalf("ensureGroup: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if createdBody["name"] != "engineering" {
		t.Errorf("expected name engineering, got %v", createdBody["name"])
	}
	if _, ok := createdBody["attributes"]; !ok {
		t.Error("missing attributes in create body")
	}
}

func TestEnsureGroupUpdate(t *testing.T) {
	var mu sync.Mutex
	var updatedBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "group-uuid-9", "name": "engineering"},
			})
		},
		"PUT /admin/realms/{realm}/groups/{id}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			if r.PathValue("id") != "group-uuid-9" {
				t.Errorf("expected group-uuid-9, got %s", r.PathValue("id"))
			}
			json.NewDecoder(r.Body).Decode(&updatedBody)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	g := config.Group{Name: "engineering", Attributes: map[string][]string{"tier": {"gold"}}}

	p := New(c, &config.Config{})
	if err := p.ensureGroup(context.Background(), "test-realm", "", g, "update"); err != nil {
		t.Fatalf("ensureGroup: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if updatedBody["id"] != "group-uuid-9" {
		t.Errorf("expected id group-uuid-9 in update body, got %v", updatedBody["id"])
	}
	attrs, ok := updatedBody["attributes"].(map[string]any)
	if !ok {
		t.Fatalf("expected attributes in update body, got %v", updatedBody["attributes"])
	}
	tier, ok := attrs["tier"].([]any)
	if !ok || len(tier) != 1 || tier[0] != "gold" {
		t.Errorf("expected tier=[gold] in update body, got %v", attrs["tier"])
	}
}

func TestEnsureGroupCreateStrategySkipsExisting(t *testing.T) {
	var updateCalled atomic.Bool

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "group-uuid-9", "name": "engineering"},
			})
		},
		"PUT /admin/realms/{realm}/groups/{id}": func(w http.ResponseWriter, r *http.Request) {
			updateCalled.Store(true)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	g := config.Group{Name: "engineering", Attributes: map[string][]string{"tier": {"should-not-update"}}}

	p := New(c, &config.Config{})
	if err := p.ensureGroup(context.Background(), "test-realm", "", g, "create"); err != nil {
		t.Fatalf("ensureGroup: %v", err)
	}

	if updateCalled.Load() {
		t.Error("expected PUT not to be called with strategy=create")
	}
}

func TestEnsureGroupWithSubGroups(t *testing.T) {
	var mu sync.Mutex
	var childParentID string
	var childBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", r.URL.String()+"/parent-uuid")
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/groups/{id}/children": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/groups/{id}/children": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			childParentID = r.PathValue("id")
			json.NewDecoder(r.Body).Decode(&childBody)
			w.Header().Set("Location", r.URL.String()+"/child-uuid")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	g := config.Group{
		Name: "parent",
		SubGroups: []config.Group{
			{Name: "backend"},
		},
	}

	p := New(c, &config.Config{})
	if err := p.ensureGroup(context.Background(), "test-realm", "", g, "update"); err != nil {
		t.Fatalf("ensureGroup: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if childParentID != "parent-uuid" {
		t.Errorf("expected subgroup created under parent-uuid, got %s", childParentID)
	}
	if childBody["name"] != "backend" {
		t.Errorf("expected subgroup name backend, got %v", childBody["name"])
	}
}

func TestEnsureGroupRealmRoles(t *testing.T) {
	var mu sync.Mutex
	var addedRoles []map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "group-uuid-9", "name": "engineering"},
			})
		},
		"PUT /admin/realms/{realm}/groups/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/groups/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"id": "role-1", "name": r.PathValue("name")})
		},
		"POST /admin/realms/{realm}/groups/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&addedRoles)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	g := config.Group{Name: "engineering", RealmRoles: []string{"developer"}}

	p := New(c, &config.Config{})
	if err := p.ensureGroup(context.Background(), "test-realm", "", g, "update"); err != nil {
		t.Fatalf("ensureGroup: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(addedRoles) != 1 || addedRoles[0]["name"] != "developer" {
		t.Errorf("expected developer role added, got %v", addedRoles)
	}
}

func TestEnsureGroupRealmRolesSkipsExisting(t *testing.T) {
	var addCalled atomic.Bool

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "group-uuid-9", "name": "engineering"},
			})
		},
		"PUT /admin/realms/{realm}/groups/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/groups/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "role-1", "name": "developer"}})
		},
		"POST /admin/realms/{realm}/groups/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			addCalled.Store(true)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	g := config.Group{Name: "engineering", RealmRoles: []string{"developer"}}

	p := New(c, &config.Config{})
	if err := p.ensureGroup(context.Background(), "test-realm", "", g, "update"); err != nil {
		t.Fatalf("ensureGroup: %v", err)
	}

	if addCalled.Load() {
		t.Error("expected POST not to be called when role already mapped")
	}
}

func TestEnsureGroupClientRoles(t *testing.T) {
	var mu sync.Mutex
	var addedRoles []map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "group-uuid-9", "name": "engineering"},
			})
		},
		"PUT /admin/realms/{realm}/groups/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "client-uuid", "clientId": "my-app"},
			})
		},
		"GET /admin/realms/{realm}/groups/{id}/role-mappings/clients/{clientUuid}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/clients/{uuid}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"id": "cr-1", "name": r.PathValue("name")})
		},
		"POST /admin/realms/{realm}/groups/{id}/role-mappings/clients/{clientUuid}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			if r.PathValue("clientUuid") != "client-uuid" {
				t.Errorf("expected client-uuid, got %s", r.PathValue("clientUuid"))
			}
			json.NewDecoder(r.Body).Decode(&addedRoles)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	g := config.Group{
		Name:        "engineering",
		ClientRoles: map[string][]string{"my-app": {"admin"}},
	}

	p := New(c, &config.Config{})
	if err := p.ensureGroup(context.Background(), "test-realm", "", g, "update"); err != nil {
		t.Fatalf("ensureGroup: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(addedRoles) != 1 || addedRoles[0]["name"] != "admin" {
		t.Errorf("expected admin client role added, got %v", addedRoles)
	}
}

func TestEnsureGroupClientRolesSkipsExisting(t *testing.T) {
	var addCalled atomic.Bool

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "group-uuid-9", "name": "engineering"},
			})
		},
		"PUT /admin/realms/{realm}/groups/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "client-uuid", "clientId": "my-app"},
			})
		},
		"GET /admin/realms/{realm}/groups/{id}/role-mappings/clients/{clientUuid}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "cr-1", "name": "admin"}})
		},
		"POST /admin/realms/{realm}/groups/{id}/role-mappings/clients/{clientUuid}": func(w http.ResponseWriter, r *http.Request) {
			addCalled.Store(true)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	g := config.Group{
		Name:        "engineering",
		ClientRoles: map[string][]string{"my-app": {"admin"}},
	}

	p := New(c, &config.Config{})
	if err := p.ensureGroup(context.Background(), "test-realm", "", g, "update"); err != nil {
		t.Fatalf("ensureGroup: %v", err)
	}

	if addCalled.Load() {
		t.Error("expected POST not to be called when client role already mapped")
	}
}

func TestEnsureGroupInvalidID(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": 12345, "name": "engineering"},
			})
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	g := config.Group{Name: "engineering"}

	p := New(c, &config.Config{})
	err := p.ensureGroup(context.Background(), "test-realm", "", g, "update")
	if err == nil {
		t.Fatal("expected error for invalid id type")
	}
}

func TestEnsureGroupRealmRoleNotFound(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "group-uuid-9", "name": "engineering"},
			})
		},
		"PUT /admin/realms/{realm}/groups/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/groups/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	g := config.Group{Name: "engineering", RealmRoles: []string{"missing-role"}}

	p := New(c, &config.Config{})
	err := p.ensureGroup(context.Background(), "test-realm", "", g, "update")
	if err == nil {
		t.Fatal("expected error for missing realm role")
	}
}

func TestEnsureGroupClientNotFound(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "group-uuid-9", "name": "engineering"},
			})
		},
		"PUT /admin/realms/{realm}/groups/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	g := config.Group{Name: "engineering", ClientRoles: map[string][]string{"missing-app": {"admin"}}}

	p := New(c, &config.Config{})
	err := p.ensureGroup(context.Background(), "test-realm", "", g, "update")
	if err == nil {
		t.Fatal("expected error for missing client")
	}
}

func TestEnsureGroupClientRoleNotFound(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "group-uuid-9", "name": "engineering"},
			})
		},
		"PUT /admin/realms/{realm}/groups/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "client-uuid", "clientId": "my-app"},
			})
		},
		"GET /admin/realms/{realm}/groups/{id}/role-mappings/clients/{clientUuid}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/clients/{uuid}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	g := config.Group{Name: "engineering", ClientRoles: map[string][]string{"my-app": {"missing-role"}}}

	p := New(c, &config.Config{})
	err := p.ensureGroup(context.Background(), "test-realm", "", g, "update")
	if err == nil {
		t.Fatal("expected error for missing client role")
	}
}

func TestEnsureGroupSubGroupUpdateStrategySkips(t *testing.T) {
	var subUpdateCalled atomic.Bool

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "parent-uuid", "name": "parent"},
			})
		},
		"PUT /admin/realms/{realm}/groups/{id}": func(w http.ResponseWriter, r *http.Request) {
			if r.PathValue("id") == "child-uuid" {
				subUpdateCalled.Store(true)
			}
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/groups/{id}/children": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "child-uuid", "name": "backend"},
			})
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	g := config.Group{
		Name:      "parent",
		SubGroups: []config.Group{{Name: "backend"}},
	}

	p := New(c, &config.Config{})
	if err := p.ensureGroup(context.Background(), "test-realm", "", g, "create"); err != nil {
		t.Fatalf("ensureGroup: %v", err)
	}

	if subUpdateCalled.Load() {
		t.Error("expected existing subgroup PUT not to be called with strategy=create")
	}
}

func TestEnsureGroupSubGroupUpdate(t *testing.T) {
	var mu sync.Mutex
	var subUpdated map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "parent-uuid", "name": "parent"},
			})
		},
		"PUT /admin/realms/{realm}/groups/{id}": func(w http.ResponseWriter, r *http.Request) {
			if r.PathValue("id") == "child-uuid" {
				mu.Lock()
				json.NewDecoder(r.Body).Decode(&subUpdated)
				mu.Unlock()
			}
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/groups/{id}/children": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "child-uuid", "name": "backend"},
			})
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	g := config.Group{
		Name:      "parent",
		SubGroups: []config.Group{{Name: "backend", Attributes: map[string][]string{"k": {"v"}}}},
	}

	p := New(c, &config.Config{})
	if err := p.ensureGroup(context.Background(), "test-realm", "", g, "update"); err != nil {
		t.Fatalf("ensureGroup: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if subUpdated["id"] != "child-uuid" {
		t.Errorf("expected existing subgroup to be updated, got body %v", subUpdated)
	}
}

func TestEnsureGroupDeeplyNestedSubGroups(t *testing.T) {
	var mu sync.Mutex
	created := map[string]string{} // name -> parentID

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", r.URL.String()+"/l1-uuid")
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/groups/{id}/children": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/groups/{id}/children": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			name, _ := body["name"].(string)
			parent := r.PathValue("id")
			mu.Lock()
			created[name] = parent
			mu.Unlock()
			w.Header().Set("Location", r.URL.String()+"/"+name+"-uuid")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	g := config.Group{
		Name: "l1",
		SubGroups: []config.Group{
			{Name: "l2", SubGroups: []config.Group{
				{Name: "l3"},
			}},
		},
	}

	p := New(c, &config.Config{})
	if err := p.ensureGroup(context.Background(), "test-realm", "", g, "update"); err != nil {
		t.Fatalf("ensureGroup: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if created["l2"] != "l1-uuid" {
		t.Errorf("expected l2 under l1-uuid, got %s", created["l2"])
	}
	if created["l3"] != "l2-uuid" {
		t.Errorf("expected l3 under l2-uuid, got %s", created["l3"])
	}
}

func TestBuildGroupBody(t *testing.T) {
	g := config.Group{
		Name:       "engineering",
		Attributes: map[string][]string{"department": {"eng"}},
	}
	body := buildGroupBody(g)
	if body["name"] != "engineering" {
		t.Errorf("expected name engineering, got %v", body["name"])
	}
	if _, ok := body["attributes"]; !ok {
		t.Error("expected attributes present")
	}

	bare := buildGroupBody(config.Group{Name: "ops"})
	if _, ok := bare["attributes"]; ok {
		t.Error("expected attributes omitted when empty")
	}
}

// --- User group membership tests ---

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

func TestBuildRealmBodyWithoutAttributesOmitsKey(t *testing.T) {
	realm := config.Realm{Realm: "test"}

	body := buildRealmBody(realm, nil)

	if _, ok := body["attributes"]; ok {
		t.Error("attributes should not be set when the config declares none")
	}
}

func TestBuildRealmBodyMergesExistingAttributes(t *testing.T) {
	realm := config.Realm{
		Realm:      "test",
		Attributes: map[string]string{"frontendUrl": "https://id.example.com"},
	}
	existing := map[string]any{
		"realm": "test",
		"attributes": map[string]any{
			"userProfileEnabled": "true",
			"frontendUrl":        "https://old.example.com",
		},
	}

	body := buildRealmBody(realm, existing)

	attrs, ok := body["attributes"].(map[string]string)
	if !ok {
		t.Fatalf("expected attributes map, got %T", body["attributes"])
	}
	if got := attrs["userProfileEnabled"]; got != "true" {
		t.Errorf("unmanaged attribute was dropped, got %q", got)
	}
	if got := attrs["frontendUrl"]; got != "https://id.example.com" {
		t.Errorf("configured attribute should win, got %q", got)
	}
}

func TestBuildRealmBodyAcrLoaMap(t *testing.T) {
	realm := config.Realm{
		Realm:     "test",
		AcrLoaMap: map[string]int{"gold": 2, "silver": 1},
	}

	body := buildRealmBody(realm, nil)

	attrs, ok := body["attributes"].(map[string]string)
	if !ok {
		t.Fatalf("expected attributes map, got %T", body["attributes"])
	}
	if got := attrs[acrLoaMapAttr]; got != `{"gold":2,"silver":1}` {
		t.Errorf("unexpected acr.loa.map: %q", got)
	}
}

func TestBuildRealmBodyAcrLoaMapWinsOverRawAttribute(t *testing.T) {
	realm := config.Realm{
		Realm:      "test",
		Attributes: map[string]string{acrLoaMapAttr: `{"bronze":0}`},
		AcrLoaMap:  map[string]int{"gold": 2},
	}

	body := buildRealmBody(realm, nil)

	attrs := body["attributes"].(map[string]string)
	if got := attrs[acrLoaMapAttr]; got != `{"gold":2}` {
		t.Errorf("typed acrLoaMap should win, got %q", got)
	}
}

func TestBuildRealmBodyOrganizationsEnabled(t *testing.T) {
	enabled := true
	realm := config.Realm{Realm: "test", OrganizationsEnabled: &enabled}

	body := buildRealmBody(realm, nil)

	if body["organizationsEnabled"] != true {
		t.Errorf("expected organizationsEnabled=true, got %v", body["organizationsEnabled"])
	}
}

func TestBuildRealmBodyOrganizationsEnabledUnsetOmitsKey(t *testing.T) {
	body := buildRealmBody(config.Realm{Realm: "test"}, nil)

	if _, ok := body["organizationsEnabled"]; ok {
		t.Error("organizationsEnabled should not be set when unset")
	}
}

func TestBuildClientAttributesAcrLoaMap(t *testing.T) {
	c := config.Client{
		ClientID:  "web",
		AcrLoaMap: map[string]int{"gold": 2},
	}

	attrs := buildClientAttributes(c, nil)

	if got := attrs[acrLoaMapAttr]; got != `{"gold":2}` {
		t.Errorf("unexpected acr.loa.map: %q", got)
	}
}

func TestBuildClientAttributesAcrLoaMapWinsOverRawAttribute(t *testing.T) {
	c := config.Client{
		ClientID:   "web",
		Attributes: map[string]string{acrLoaMapAttr: `{"bronze":0}`},
		AcrLoaMap:  map[string]int{"gold": 2},
	}

	attrs := buildClientAttributes(c, nil)

	if got := attrs[acrLoaMapAttr]; got != `{"gold":2}` {
		t.Errorf("typed acrLoaMap should win, got %q", got)
	}
}

func TestBuildAcrLoaMapAttributeEmpty(t *testing.T) {
	if got := buildAcrLoaMapAttribute(nil); got != "" {
		t.Errorf("expected empty string for nil map, got %q", got)
	}
	if got := buildAcrLoaMapAttribute(map[string]int{}); got != "" {
		t.Errorf("expected empty string for empty map, got %q", got)
	}
}

func TestMergeAttributesNilExisting(t *testing.T) {
	merged := mergeAttributes(nil, map[string]string{"a": "1"})

	if len(merged) != 1 || merged["a"] != "1" {
		t.Errorf("unexpected merge result: %v", merged)
	}
}

func TestMergeAttributesNonStringValues(t *testing.T) {
	existing := map[string]any{
		"attributes": map[string]any{
			"num":  float64(3),
			"null": nil,
		},
	}

	merged := mergeAttributes(existing, nil)

	if got := merged["num"]; got != "3" {
		t.Errorf("expected stringified number, got %q", got)
	}
	if _, ok := merged["null"]; ok {
		t.Error("nil attribute values should be skipped")
	}
}

func TestEnsureRealmUpdateSendsMergedAttributes(t *testing.T) {
	var mu sync.Mutex
	var updatedBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"realm":      "test",
				"attributes": map[string]any{"keptByKeycloak": "yes"},
			})
		},
		"PUT /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&updatedBody)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	realm := config.Realm{
		Realm:      "test",
		Attributes: map[string]string{"frontendUrl": "https://id.example.com"},
	}

	if err := p.ensureRealm(context.Background(), realm, "update"); err != nil {
		t.Fatalf("ensureRealm: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	attrs, ok := updatedBody["attributes"].(map[string]any)
	if !ok {
		t.Fatalf("expected attributes in update body, got %T", updatedBody["attributes"])
	}
	if attrs["keptByKeycloak"] != "yes" {
		t.Error("existing realm attribute was clobbered by the update")
	}
	if attrs["frontendUrl"] != "https://id.example.com" {
		t.Errorf("configured attribute missing, got %v", attrs["frontendUrl"])
	}
}

func TestBuildClientBodyMergesExistingAttributes(t *testing.T) {
	c := config.Client{
		ClientID:   "app",
		Attributes: map[string]string{"post.logout.redirect.uris": "+"},
	}
	existing := map[string]any{
		"clientId": "app",
		"attributes": map[string]any{
			"oauth2.device.authorization.grant.enabled": "true",
			"post.logout.redirect.uris":                 "-",
		},
	}

	body := buildClientBody(c, existing, nil)

	attrs, ok := body["attributes"].(map[string]string)
	if !ok {
		t.Fatalf("expected attributes map, got %T", body["attributes"])
	}
	if got := attrs["oauth2.device.authorization.grant.enabled"]; got != "true" {
		t.Errorf("unmanaged client attribute was dropped, got %q", got)
	}
	if got := attrs["post.logout.redirect.uris"]; got != "+" {
		t.Errorf("configured attribute should win, got %q", got)
	}
}

func TestBuildClientBodyUnmanagedAttributesOmitsKey(t *testing.T) {
	existing := map[string]any{
		"clientId":   "app",
		"attributes": map[string]any{"set.by.keycloak": "true"},
	}

	body := buildClientBody(config.Client{ClientID: "app"}, existing, nil)

	if _, ok := body["attributes"]; ok {
		t.Errorf("attributes must stay out of the body when unmanaged, got %v", body["attributes"])
	}
}

func TestEnsureClientUpdatePreservesOutOfBandAttributes(t *testing.T) {
	var mu sync.Mutex
	var updatedBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{
				"id":         "uuid-1",
				"clientId":   "app",
				"attributes": map[string]any{"setOutOfBand": "yes"},
			}})
		},
		"PUT /admin/realms/{realm}/clients/{uuid}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&updatedBody)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	c := config.Client{
		ClientID:   "app",
		Attributes: map[string]string{"post.logout.redirect.uris": "+"},
	}

	if _, err := p.ensureClient(context.Background(), "test-realm", c, "update"); err != nil {
		t.Fatalf("ensureClient: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	attrs, ok := updatedBody["attributes"].(map[string]any)
	if !ok {
		t.Fatalf("expected attributes in update body, got %T", updatedBody["attributes"])
	}
	if attrs["setOutOfBand"] != "yes" {
		t.Error("out-of-band client attribute was clobbered by the update")
	}
	if attrs["post.logout.redirect.uris"] != "+" {
		t.Errorf("configured attribute missing, got %v", attrs["post.logout.redirect.uris"])
	}
}

func TestBuildClientScopeBodyDefaultsProtocol(t *testing.T) {
	body := buildClientScopeBody(config.ClientScope{Name: "orders:read"})

	if body["protocol"] != defaultClientScopeProtocol {
		t.Errorf("expected protocol %q, got %v", defaultClientScopeProtocol, body["protocol"])
	}
	if _, ok := body["description"]; ok {
		t.Error("description should be omitted when empty")
	}
}

func TestBuildClientScopeBodyAllFields(t *testing.T) {
	body := buildClientScopeBody(config.ClientScope{
		Name:        "orders:read",
		Description: "Read orders",
		Protocol:    "saml",
		Attributes:  map[string]string{"include.in.token.scope": "true"},
	})

	if body["name"] != "orders:read" || body["protocol"] != "saml" || body["description"] != "Read orders" {
		t.Errorf("unexpected body: %v", body)
	}
	attrs, ok := body["attributes"].(map[string]string)
	if !ok || attrs["include.in.token.scope"] != "true" {
		t.Errorf("unexpected attributes: %v", body["attributes"])
	}
}

func TestEnsureClientScopeCreatesWhenMissing(t *testing.T) {
	var mu sync.Mutex
	var created map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/client-scopes": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "other-id", "name": "profile"}})
		},
		"POST /admin/realms/{realm}/client-scopes": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&created)
			w.Header().Set("Location", "http://kc/admin/realms/test/client-scopes/new-scope-id")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})

	scopes, err := p.loadClientScopeIndex(context.Background(), "test")
	if err != nil {
		t.Fatalf("loadClientScopeIndex: %v", err)
	}

	id, err := p.ensureClientScope(context.Background(), "test", config.ClientScope{Name: "orders:read"}, "update", scopes)
	if err != nil {
		t.Fatalf("ensureClientScope: %v", err)
	}
	if id != "new-scope-id" {
		t.Errorf("expected new-scope-id, got %q", id)
	}
	if scopes["orders:read"] != "new-scope-id" {
		t.Errorf("a newly created scope must be added to the index, got %v", scopes)
	}

	mu.Lock()
	defer mu.Unlock()
	if created["name"] != "orders:read" {
		t.Errorf("unexpected created body: %v", created)
	}
}

func TestEnsureClientScopeCreateStrategySkipsExisting(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/client-scopes": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "scope-1", "name": "orders:read"}})
		},
		"PUT /admin/realms/{realm}/client-scopes/{id}": func(w http.ResponseWriter, r *http.Request) {
			t.Error("update must not be called with strategy=create")
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})

	scopes, err := p.loadClientScopeIndex(context.Background(), "test")
	if err != nil {
		t.Fatalf("loadClientScopeIndex: %v", err)
	}

	id, err := p.ensureClientScope(context.Background(), "test", config.ClientScope{Name: "orders:read"}, "create", scopes)
	if err != nil {
		t.Fatalf("ensureClientScope: %v", err)
	}
	if id != "scope-1" {
		t.Errorf("expected existing id scope-1, got %q", id)
	}
}

func TestEnsureClientScopeAssignmentsAddsMissingScope(t *testing.T) {
	var mu sync.Mutex
	var assigned []string

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/client-scopes": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "scope-1", "name": "orders:read"},
				{"id": "scope-2", "name": "profile"},
			})
		},
		"GET /admin/realms/{realm}/clients/{uuid}/default-client-scopes": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "scope-2", "name": "profile"}})
		},
		"PUT /admin/realms/{realm}/clients/{uuid}/default-client-scopes/{scopeId}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			assigned = append(assigned, r.PathValue("scopeId"))
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	c := config.Client{ClientID: "app", DefaultClientScopes: []string{"orders:read", "profile"}}

	scopes, err := p.loadClientScopeIndex(context.Background(), "test")
	if err != nil {
		t.Fatalf("loadClientScopeIndex: %v", err)
	}

	if err := p.ensureClientScopeAssignments(context.Background(), "test", "uuid-1", c, scopes); err != nil {
		t.Fatalf("ensureClientScopeAssignments: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(assigned) != 1 || assigned[0] != "scope-1" {
		t.Errorf("expected only the missing scope to be assigned, got %v", assigned)
	}
}

func TestEnsureClientScopeAssignmentsWarnsOnUnknownScope(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/client-scopes": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/clients/{uuid}/default-client-scopes": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"PUT /admin/realms/{realm}/clients/{uuid}/default-client-scopes/{scopeId}": func(w http.ResponseWriter, r *http.Request) {
			t.Error("must not assign a scope that does not exist")
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	c := config.Client{ClientID: "app", DefaultClientScopes: []string{"does-not-exist"}}

	scopes, err := p.loadClientScopeIndex(context.Background(), "test")
	if err != nil {
		t.Fatalf("loadClientScopeIndex: %v", err)
	}

	if err := p.ensureClientScopeAssignments(context.Background(), "test", "uuid-1", c, scopes); err != nil {
		t.Fatalf("expected unknown scope to be skipped, got error: %v", err)
	}
}

func TestEnsureRealmClientScopeTypeNoneDoesNothing(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/default-default-client-scopes": func(w http.ResponseWriter, r *http.Request) {
			t.Error("must not read realm default scopes for type none")
			w.WriteHeader(http.StatusOK)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	for _, scopeType := range []string{"", "none"} {
		cs := config.ClientScope{Name: "orders:read", Type: scopeType}
		if err := p.ensureRealmClientScopeType(context.Background(), "test", "scope-1", cs); err != nil {
			t.Fatalf("type %q: %v", scopeType, err)
		}
	}
}

func TestEnsureRealmClientScopeTypeDefaultAssignsWhenMissing(t *testing.T) {
	var mu sync.Mutex
	var assigned []string

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/default-default-client-scopes": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "other", "name": "profile"}})
		},
		"PUT /admin/realms/{realm}/default-default-client-scopes/{scopeId}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			assigned = append(assigned, r.PathValue("scopeId"))
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	cs := config.ClientScope{Name: "orders:read", Type: "default"}

	if err := p.ensureRealmClientScopeType(context.Background(), "test", "scope-1", cs); err != nil {
		t.Fatalf("ensureRealmClientScopeType: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(assigned) != 1 || assigned[0] != "scope-1" {
		t.Errorf("expected scope-1 to be assigned, got %v", assigned)
	}
}

func TestEnsureRealmClientScopeTypeSkipsAlreadyAssigned(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/default-optional-client-scopes": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "scope-1", "name": "orders:read"}})
		},
		"PUT /admin/realms/{realm}/default-optional-client-scopes/{scopeId}": func(w http.ResponseWriter, r *http.Request) {
			t.Error("must not re-assign an already assigned scope")
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	cs := config.ClientScope{Name: "orders:read", Type: "optional"}

	if err := p.ensureRealmClientScopeType(context.Background(), "test", "scope-1", cs); err != nil {
		t.Fatalf("ensureRealmClientScopeType: %v", err)
	}
}

func TestEnsureClientScopeAssignmentsSkipsDuplicateNames(t *testing.T) {
	var mu sync.Mutex
	var assigned []string

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/client-scopes": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "scope-1", "name": "orders:read"}})
		},
		"GET /admin/realms/{realm}/clients/{uuid}/default-client-scopes": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"PUT /admin/realms/{realm}/clients/{uuid}/default-client-scopes/{scopeId}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			assigned = append(assigned, r.PathValue("scopeId"))
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	c := config.Client{
		ClientID:            "app",
		DefaultClientScopes: []string{"orders:read", "orders:read"},
	}

	scopes, err := p.loadClientScopeIndex(context.Background(), "test")
	if err != nil {
		t.Fatalf("loadClientScopeIndex: %v", err)
	}

	if err := p.ensureClientScopeAssignments(context.Background(), "test", "uuid-1", c, scopes); err != nil {
		t.Fatalf("ensureClientScopeAssignments: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(assigned) != 1 {
		t.Errorf("a repeated scope name must be assigned once, got %v", assigned)
	}
}

// TestProvisionRealmListsClientScopesOnce pins the fix for the O(scopes^2)
// listing: the realm's client scopes are listed once and indexed, rather than
// re-listed for every scope and again for every client's assignments.
func TestProvisionRealmListsClientScopesOnce(t *testing.T) {
	var mu sync.Mutex
	scopeListings := 0

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"realm": "test"})
		},
		"PUT /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/client-scopes": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			scopeListings++
			mu.Unlock()
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/client-scopes": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			name, _ := body["name"].(string)
			w.Header().Set("Location", "http://kc/admin/realms/test/client-scopes/id-"+name)
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "uuid-1", "clientId": "app"}})
		},
		"PUT /admin/realms/{realm}/clients/{uuid}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/clients/{uuid}/default-client-scopes": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"PUT /admin/realms/{realm}/clients/{uuid}/default-client-scopes/{scopeId}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	realm := config.Realm{
		Realm: "test",
		ClientScopes: []config.ClientScope{
			{Name: "scope-a"}, {Name: "scope-b"}, {Name: "scope-c"},
		},
		Clients: []config.Client{
			{ClientID: "app", DefaultClientScopes: []string{"scope-a", "scope-b"}},
		},
	}

	p := New(newTestClient(t, server.URL), &config.Config{})
	if err := p.provisionRealm(context.Background(), realm, "update"); err != nil {
		t.Fatalf("provisionRealm: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if scopeListings != 1 {
		t.Errorf("client scopes should be listed once per realm, got %d listings for 3 scopes and 1 client", scopeListings)
	}
}

func TestProvisionRealmSkipsClientScopeListingWhenUnused(t *testing.T) {
	var mu sync.Mutex
	scopeListings := 0

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"realm": "test"})
		},
		"PUT /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/client-scopes": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			scopeListings++
			mu.Unlock()
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "uuid-1", "clientId": "app"}})
		},
		"PUT /admin/realms/{realm}/clients/{uuid}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	realm := config.Realm{
		Realm:   "test",
		Clients: []config.Client{{ClientID: "app"}},
	}

	p := New(newTestClient(t, server.URL), &config.Config{})
	if err := p.provisionRealm(context.Background(), realm, "update"); err != nil {
		t.Fatalf("provisionRealm: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if scopeListings != 0 {
		t.Errorf("nothing resolves a scope by name, so no listing should happen; got %d", scopeListings)
	}
}

func TestBuildOrganizationBodyIncludesAliasOnlyOnCreate(t *testing.T) {
	o := config.Organization{Name: "acme", Alias: "acme-corp"}

	created := buildOrganizationBody(o, true)
	if created["alias"] != "acme-corp" {
		t.Errorf("expected alias on create, got %v", created["alias"])
	}

	updated := buildOrganizationBody(o, false)
	if _, ok := updated["alias"]; ok {
		t.Error("alias must be omitted on update; Keycloak treats it as immutable")
	}
}

func TestBuildOrganizationBodyDomains(t *testing.T) {
	verified := true
	o := config.Organization{
		Name: "acme",
		Domains: []config.OrganizationDomain{
			{Name: "acme.com", Verified: &verified},
			{Name: "acme.org"},
		},
	}

	body := buildOrganizationBody(o, true)

	domains, ok := body["domains"].([]map[string]any)
	if !ok || len(domains) != 2 {
		t.Fatalf("unexpected domains: %v", body["domains"])
	}
	if domains[0]["name"] != "acme.com" || domains[0]["verified"] != true {
		t.Errorf("unexpected first domain: %v", domains[0])
	}
	if _, ok := domains[1]["verified"]; ok {
		t.Error("verified should be omitted when unset")
	}
}

func TestEnsureOrganizationCreatesWhenMissing(t *testing.T) {
	var mu sync.Mutex
	var created map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&created)
			w.Header().Set("Location", "http://kc/admin/realms/test/organizations/org-1")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{Name: "acme", Domains: []config.OrganizationDomain{{Name: "acme.com"}}}

	if err := p.ensureOrganization(context.Background(), "test", o, "update"); err != nil {
		t.Fatalf("ensureOrganization: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if created["name"] != "acme" {
		t.Errorf("unexpected created body: %v", created)
	}
}

func TestEnsureOrganizationCreateStrategySkipsExisting(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "org-1", "name": "acme"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}": func(w http.ResponseWriter, r *http.Request) {
			t.Error("update must not be called with strategy=create")
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{Name: "acme"}

	if err := p.ensureOrganization(context.Background(), "test", o, "create"); err != nil {
		t.Fatalf("ensureOrganization: %v", err)
	}
}

func TestEnsureOrganizationMembersAddsMissing(t *testing.T) {
	var mu sync.Mutex
	var addedBodies []string

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "org-1", "name": "acme"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/members": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "u-1", "username": "alice"}})
		},
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("username") == "bob" {
				json.NewEncoder(w).Encode([]map[string]any{{"id": "u-2", "username": "bob"}})
				return
			}
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/organizations/{id}/members": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("reading member request body: %v", err)
			}
			addedBodies = append(addedBodies, string(body))
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{Name: "acme", Members: []string{"alice", "bob"}}

	if err := p.ensureOrganization(context.Background(), "test", o, "update"); err != nil {
		t.Fatalf("ensureOrganization: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(addedBodies) != 1 {
		t.Fatalf("expected only bob to be added, got %d calls: %v", len(addedBodies), addedBodies)
	}
	// The member endpoint takes the user ID as a bare JSON string.
	if addedBodies[0] != `"u-2"` {
		t.Errorf("expected quoted user id as body, got %s", addedBodies[0])
	}
}

func TestEnsureOrganizationMembersWarnsOnMissingUser(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "org-1", "name": "acme"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/members": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/organizations/{id}/members": func(w http.ResponseWriter, r *http.Request) {
			t.Error("must not add a member that does not exist")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{Name: "acme", Members: []string{"ghost"}}

	if err := p.ensureOrganization(context.Background(), "test", o, "update"); err != nil {
		t.Fatalf("missing user should be skipped, got error: %v", err)
	}
}

// orgGroupCalls records the organization-group creations a test server sees.
type orgGroupCalls struct {
	mu            sync.Mutex
	createdGroups []string
	createdSubs   []string
}

func TestEnsureOrganizationGroupCreatesTreeInOrder(t *testing.T) {
	calls := &orgGroupCalls{}

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "org-1", "name": "acme"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups/{gid}/children": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/organizations/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			name, _ := body["name"].(string)
			calls.mu.Lock()
			calls.createdGroups = append(calls.createdGroups, name)
			calls.mu.Unlock()
			w.Header().Set("Location", "http://kc/admin/realms/test/organizations/org-1/groups/g-"+name)
			w.WriteHeader(http.StatusCreated)
		},
		"POST /admin/realms/{realm}/organizations/{id}/groups/{gid}/children": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			name, _ := body["name"].(string)
			calls.mu.Lock()
			calls.createdSubs = append(calls.createdSubs, r.PathValue("gid")+"/"+name)
			calls.mu.Unlock()
			w.Header().Set("Location", "http://kc/admin/realms/test/organizations/org-1/groups/g-"+name)
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{
		Name: "acme",
		Groups: []config.OrganizationGroup{{
			Name: "engineering",
			SubGroups: []config.OrganizationGroup{
				{Name: "backend"},
				{Name: "frontend"},
			},
		}},
	}

	if err := p.ensureOrganization(context.Background(), "test", o, "update"); err != nil {
		t.Fatalf("ensureOrganization: %v", err)
	}

	calls.mu.Lock()
	defer calls.mu.Unlock()

	if len(calls.createdGroups) != 1 || calls.createdGroups[0] != "engineering" {
		t.Errorf("unexpected top-level groups: %v", calls.createdGroups)
	}
	want := []string{"g-engineering/backend", "g-engineering/frontend"}
	if len(calls.createdSubs) != 2 || calls.createdSubs[0] != want[0] || calls.createdSubs[1] != want[1] {
		t.Errorf("unexpected subgroups: %v, want %v", calls.createdSubs, want)
	}
}

func TestEnsureOrganizationGroupUpdatesExistingWithAttributes(t *testing.T) {
	var mu sync.Mutex
	var updated map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "org-1", "name": "acme"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "g-1", "name": "engineering"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}/groups/{gid}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&updated)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{
		Name: "acme",
		Groups: []config.OrganizationGroup{{
			Name:       "engineering",
			Attributes: map[string][]string{"tier": {"gold"}},
		}},
	}

	if err := p.ensureOrganization(context.Background(), "test", o, "update"); err != nil {
		t.Fatalf("ensureOrganization: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if updated["id"] != "g-1" {
		t.Errorf("update should carry the group id, got %v", updated["id"])
	}
	attrs, ok := updated["attributes"].(map[string]any)
	if !ok {
		t.Fatalf("expected attributes, got %T", updated["attributes"])
	}
	if tier, ok := attrs["tier"].([]any); !ok || len(tier) != 1 || tier[0] != "gold" {
		t.Errorf("unexpected attributes: %v", attrs)
	}
}

func TestEnsureOrganizationGroupSkipsNonOrgMember(t *testing.T) {
	var mu sync.Mutex
	var added []string

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "org-1", "name": "acme"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/members": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "u-1", "username": "alice"}})
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "g-1", "name": "engineering"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}/groups/{gid}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups/{gid}/members": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "u-1", "username": r.URL.Query().Get("username")}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}/groups/{gid}/members/{uid}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			added = append(added, r.PathValue("uid"))
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{
		Name: "acme",
		Groups: []config.OrganizationGroup{{
			Name: "engineering",
			// alice is an org member; bob is not, so Keycloak would answer 400.
			Members: []string{"alice", "bob"},
		}},
	}

	if err := p.ensureOrganization(context.Background(), "test", o, "update"); err != nil {
		t.Fatalf("ensureOrganization: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(added) != 1 {
		t.Fatalf("expected only the org member to be added, got %v", added)
	}
}

func TestEnsureOrganizationGroupSkipsExistingMembership(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "org-1", "name": "acme"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/members": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "u-1", "username": "alice"}})
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "g-1", "name": "engineering"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}/groups/{gid}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups/{gid}/members": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "u-1", "username": "alice"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}/groups/{gid}/members/{uid}": func(w http.ResponseWriter, r *http.Request) {
			t.Error("must not re-add an existing group member")
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{
		Name:   "acme",
		Groups: []config.OrganizationGroup{{Name: "engineering", Members: []string{"alice"}}},
	}

	if err := p.ensureOrganization(context.Background(), "test", o, "update"); err != nil {
		t.Fatalf("ensureOrganization: %v", err)
	}
}

func TestBuildOrganizationGroupBody(t *testing.T) {
	plain := buildOrganizationGroupBody(config.OrganizationGroup{Name: "engineering"})
	if plain["name"] != "engineering" {
		t.Errorf("unexpected name: %v", plain["name"])
	}
	if _, ok := plain["attributes"]; ok {
		t.Error("attributes should be omitted when empty")
	}

	withAttrs := buildOrganizationGroupBody(config.OrganizationGroup{
		Name:       "engineering",
		Attributes: map[string][]string{"tier": {"gold"}},
	})
	if _, ok := withAttrs["attributes"].(map[string][]string); !ok {
		t.Errorf("unexpected attributes type: %T", withAttrs["attributes"])
	}
}

// TestEnsureOrganizationGroupsFetchMembersOnce pins the fix for the repeated
// member reads: the organization's member listing is read once per
// organization and carried through the group recursion, and the group path
// resolves user ids from it rather than calling GetUsers per member.
func TestEnsureOrganizationGroupsFetchMembersOnce(t *testing.T) {
	var mu sync.Mutex
	orgMemberReads, userLookups := 0, 0

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "org-1", "name": "acme"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/members": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			orgMemberReads++
			mu.Unlock()
			json.NewEncoder(w).Encode([]map[string]any{{"id": "u-1", "username": "alice"}})
		},
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			userLookups++
			mu.Unlock()
			json.NewEncoder(w).Encode([]map[string]any{{"id": "u-1", "username": "alice"}})
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "g-1", "name": "engineering"}})
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups/{gid}/children": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "g-2", "name": "backend"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}/groups/{gid}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups/{gid}/members": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"PUT /admin/realms/{realm}/organizations/{id}/groups/{gid}/members/{uid}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{
		Name: "acme",
		Groups: []config.OrganizationGroup{{
			Name:    "engineering",
			Members: []string{"alice"},
			SubGroups: []config.OrganizationGroup{
				{Name: "backend", Members: []string{"alice"}},
			},
		}},
	}

	if err := p.ensureOrganization(context.Background(), "test", o, "update"); err != nil {
		t.Fatalf("ensureOrganization: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if orgMemberReads != 1 {
		t.Errorf("organization members should be read once for the whole tree, got %d reads", orgMemberReads)
	}
	if userLookups != 0 {
		t.Errorf("group membership must resolve ids from the member listing, got %d user lookups", userLookups)
	}
}

func TestEnsureOrganizationGroupsSkipMemberReadWhenNoneAssigned(t *testing.T) {
	var mu sync.Mutex
	orgMemberReads := 0

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "org-1", "name": "acme"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/members": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			orgMemberReads++
			mu.Unlock()
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "g-1", "name": "engineering"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}/groups/{gid}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{Name: "acme", Groups: []config.OrganizationGroup{{Name: "engineering"}}}

	if err := p.ensureOrganization(context.Background(), "test", o, "update"); err != nil {
		t.Fatalf("ensureOrganization: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if orgMemberReads != 0 {
		t.Errorf("no group assigns members, so the member listing should not be read; got %d", orgMemberReads)
	}
}

func TestEnsureAuthenticationFlowSkipsExistingWithoutMutating(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "f-1", "alias": "browser-step-up", "builtIn": false},
			})
		},
		"POST /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			t.Error("must not create a flow that already exists")
			w.WriteHeader(http.StatusCreated)
		},
		"POST /admin/realms/{realm}/authentication/flows/{alias}/executions/execution": func(w http.ResponseWriter, r *http.Request) {
			t.Error("must not touch executions of an existing flow")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	f := config.AuthenticationFlow{
		Alias:      "browser-step-up",
		Executions: []config.AuthenticationExecution{{Provider: "auth-cookie", Requirement: "REQUIRED"}},
	}

	if err := p.ensureAuthenticationFlow(context.Background(), "test", f); err != nil {
		t.Fatalf("ensureAuthenticationFlow: %v", err)
	}
}

func TestEnsureAuthenticationFlowRejectsBuiltIn(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "f-1", "alias": "browser", "builtIn": true},
			})
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	f := config.AuthenticationFlow{Alias: "browser"}

	err := p.ensureAuthenticationFlow(context.Background(), "test", f)
	if err == nil {
		t.Fatal("expected an error for a built-in flow")
	}
	if !strings.Contains(err.Error(), "copyFrom") {
		t.Errorf("error should point at copyFrom, got: %v", err)
	}
}

func TestEnsureAuthenticationFlowCopiesFromSource(t *testing.T) {
	var mu sync.Mutex
	var copied map[string]any
	var copiedFrom string

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "f-1", "alias": "browser", "builtIn": true},
			})
		},
		"POST /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			t.Error("copyFrom must copy, not create from scratch")
			w.WriteHeader(http.StatusCreated)
		},
		"POST /admin/realms/{realm}/authentication/flows/{alias}/copy": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			copiedFrom = r.PathValue("alias")
			json.NewDecoder(r.Body).Decode(&copied)
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	f := config.AuthenticationFlow{Alias: "browser-step-up", CopyFrom: "browser"}

	if err := p.ensureAuthenticationFlow(context.Background(), "test", f); err != nil {
		t.Fatalf("ensureAuthenticationFlow: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if copiedFrom != "browser" || copied["newName"] != "browser-step-up" {
		t.Errorf("unexpected copy: from=%q body=%v", copiedFrom, copied)
	}
}

func TestEnsureAuthenticationFlowMissingCopySourceErrors(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	f := config.AuthenticationFlow{Alias: "new-flow", CopyFrom: "does-not-exist"}

	if err := p.ensureAuthenticationFlow(context.Background(), "test", f); err == nil {
		t.Fatal("expected an error when copyFrom source is missing")
	}
}

func TestEnsureAuthenticationFlowCreatesExecutionsInDeclaredOrder(t *testing.T) {
	var mu sync.Mutex
	var order []string

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
		"POST /admin/realms/{realm}/authentication/flows/{alias}/executions/execution": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			provider, _ := body["provider"].(string)
			mu.Lock()
			defer mu.Unlock()
			order = append(order, r.PathValue("alias")+":"+provider)
			w.WriteHeader(http.StatusCreated)
		},
		"POST /admin/realms/{realm}/authentication/flows/{alias}/executions/flow": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			alias, _ := body["alias"].(string)
			mu.Lock()
			defer mu.Unlock()
			order = append(order, r.PathValue("alias")+":subflow="+alias)
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/authentication/flows/{alias}/executions": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	f := config.AuthenticationFlow{
		Alias: "step-up",
		Executions: []config.AuthenticationExecution{
			{Provider: "auth-cookie"},
			{Provider: "identity-provider-redirector"},
			{Subflow: "loa-gold", Executions: []config.AuthenticationExecution{
				{Provider: "conditional-level-of-authentication"},
				{Provider: "auth-otp-form"},
			}},
		},
	}

	if err := p.ensureAuthenticationFlow(context.Background(), "test", f); err != nil {
		t.Fatalf("ensureAuthenticationFlow: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	want := []string{
		"step-up:auth-cookie",
		"step-up:identity-provider-redirector",
		"step-up:subflow=loa-gold",
		"loa-gold:conditional-level-of-authentication",
		"loa-gold:auth-otp-form",
	}
	if len(order) != len(want) {
		t.Fatalf("expected %d calls, got %d: %v", len(want), len(order), order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("call %d: expected %q, got %q", i, want[i], order[i])
		}
	}
}

func TestEnsureAuthenticationFlowSetsRequirementAndConfig(t *testing.T) {
	var mu sync.Mutex
	var updated map[string]any
	var execConfig map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
		"POST /admin/realms/{realm}/authentication/flows/{alias}/executions/execution": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/authentication/flows/{alias}/executions": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "ex-1", "providerId": "conditional-level-of-authentication", "requirement": "DISABLED"},
			})
		},
		"PUT /admin/realms/{realm}/authentication/flows/{alias}/executions": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&updated)
			w.WriteHeader(http.StatusNoContent)
		},
		"POST /admin/realms/{realm}/authentication/executions/{id}/config": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&execConfig)
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	f := config.AuthenticationFlow{
		Alias: "step-up",
		Executions: []config.AuthenticationExecution{{
			Provider:    "conditional-level-of-authentication",
			Requirement: "REQUIRED",
			Config: map[string]string{
				"alias":               "gold-condition",
				"loa-condition-level": "2",
			},
		}},
	}

	if err := p.ensureAuthenticationFlow(context.Background(), "test", f); err != nil {
		t.Fatalf("ensureAuthenticationFlow: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if updated["requirement"] != "REQUIRED" {
		t.Errorf("expected requirement REQUIRED, got %v", updated["requirement"])
	}
	if updated["id"] != "ex-1" {
		t.Errorf("update should carry the execution id, got %v", updated["id"])
	}
	if execConfig["alias"] != "gold-condition" {
		t.Errorf("expected config alias gold-condition, got %v", execConfig["alias"])
	}
	cfgMap, ok := execConfig["config"].(map[string]any)
	if !ok || cfgMap["loa-condition-level"] != "2" {
		t.Errorf("unexpected execution config: %v", execConfig["config"])
	}
	if _, ok := cfgMap["alias"]; ok {
		t.Error("alias names the config and must not be a config entry")
	}
}

func TestEnsureAuthenticationBindingsNilDoesNothing(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			t.Error("must not update the realm when no bindings are configured")
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	if err := p.ensureAuthenticationBindings(context.Background(), "test", nil); err != nil {
		t.Fatalf("ensureAuthenticationBindings: %v", err)
	}
	if err := p.ensureAuthenticationBindings(context.Background(), "test", &config.AuthenticationBindings{}); err != nil {
		t.Fatalf("ensureAuthenticationBindings (empty): %v", err)
	}
}

func TestEnsureAuthenticationBindingsSendsAliases(t *testing.T) {
	var mu sync.Mutex
	var body map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	bindings := &config.AuthenticationBindings{
		BrowserFlow:     "browser-step-up",
		DirectGrantFlow: "direct grant",
	}

	if err := p.ensureAuthenticationBindings(context.Background(), "test", bindings); err != nil {
		t.Fatalf("ensureAuthenticationBindings: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if body["browserFlow"] != "browser-step-up" || body["directGrantFlow"] != "direct grant" {
		t.Errorf("unexpected bindings body: %v", body)
	}
	if _, ok := body["registrationFlow"]; ok {
		t.Error("unset bindings must be omitted")
	}
}

func TestResolveFlowBindingOverridesResolvesAliasToID(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "flow-uuid-1", "alias": "browser-step-up"},
			})
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	resolved, err := p.resolveFlowBindingOverrides(context.Background(), "test", "web",
		map[string]string{"browser": "browser-step-up"})
	if err != nil {
		t.Fatalf("resolveFlowBindingOverrides: %v", err)
	}

	// The client representation takes flow IDs here, not aliases.
	if resolved["browser"] != "flow-uuid-1" {
		t.Errorf("expected flow id, got %v", resolved["browser"])
	}
}

func TestResolveFlowBindingOverridesUnknownAliasErrors(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	_, err := p.resolveFlowBindingOverrides(context.Background(), "test", "web",
		map[string]string{"browser": "missing-flow"})
	if err == nil {
		t.Fatal("expected an error for an unknown flow alias")
	}
}

func TestResolveFlowBindingOverridesEmptySkipsLookup(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			t.Error("must not list flows when no overrides are configured")
			w.WriteHeader(http.StatusOK)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	resolved, err := p.resolveFlowBindingOverrides(context.Background(), "test", "web", nil)
	if err != nil {
		t.Fatalf("resolveFlowBindingOverrides: %v", err)
	}
	if resolved != nil {
		t.Errorf("expected nil, got %v", resolved)
	}
}

func TestEnsureClientScopeProtocolMapperCreatesWhenMissing(t *testing.T) {
	var mu sync.Mutex
	var created map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/client-scopes/{id}/protocol-mappers/models": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/client-scopes/{id}/protocol-mappers/models": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&created)
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	pm := config.ProtocolMapper{Name: "audience", Protocol: "openid-connect", ProtocolMapper: "oidc-audience-mapper"}

	if err := p.ensureClientScopeProtocolMapper(context.Background(), "test", "cs-1", "orders:read", pm, "update"); err != nil {
		t.Fatalf("ensureClientScopeProtocolMapper: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if created["name"] != "audience" {
		t.Errorf("unexpected created mapper: %v", created)
	}
}

func TestEnsureClientScopeProtocolMapperUpdatesExisting(t *testing.T) {
	var mu sync.Mutex
	var updated map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/client-scopes/{id}/protocol-mappers/models": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "m-1", "name": "audience"}})
		},
		"PUT /admin/realms/{realm}/client-scopes/{id}/protocol-mappers/models/{mapperId}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&updated)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	pm := config.ProtocolMapper{Name: "audience", Protocol: "openid-connect", ProtocolMapper: "oidc-audience-mapper"}

	if err := p.ensureClientScopeProtocolMapper(context.Background(), "test", "cs-1", "orders:read", pm, "update"); err != nil {
		t.Fatalf("ensureClientScopeProtocolMapper: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if updated["id"] != "m-1" {
		t.Errorf("update should carry the mapper id, got %v", updated["id"])
	}
}

func TestEnsureClientScopeProtocolMapperCreateStrategySkipsExisting(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/client-scopes/{id}/protocol-mappers/models": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "m-1", "name": "audience"}})
		},
		"PUT /admin/realms/{realm}/client-scopes/{id}/protocol-mappers/models/{mapperId}": func(w http.ResponseWriter, r *http.Request) {
			t.Error("update must not be called with strategy=create")
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	pm := config.ProtocolMapper{Name: "audience", Protocol: "openid-connect", ProtocolMapper: "oidc-audience-mapper"}

	if err := p.ensureClientScopeProtocolMapper(context.Background(), "test", "cs-1", "orders:read", pm, "create"); err != nil {
		t.Fatalf("ensureClientScopeProtocolMapper: %v", err)
	}
}

// TestEnsureAuthenticationFlowMissingCopySourceWarns covers the dry-run case:
// when the flow listing cannot show the copy source — a realm that does not
// exist yet has none of its built-in flows visible — the run must continue and
// let the copy call be the authority.
func TestEnsureAuthenticationFlowMissingCopySourceWarns(t *testing.T) {
	var mu sync.Mutex
	copied := false

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/authentication/flows/{alias}/copy": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			copied = true
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/authentication/flows/{alias}/executions": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	f := config.AuthenticationFlow{Alias: "browser-step-up", CopyFrom: "browser"}

	if err := p.ensureAuthenticationFlow(context.Background(), "test", f); err != nil {
		t.Fatalf("an unlistable copy source must not abort the run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !copied {
		t.Error("the copy should still be attempted")
	}
}
