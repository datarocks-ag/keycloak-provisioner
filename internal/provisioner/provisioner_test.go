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

	body := buildClientBody(c)

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

	body := buildRealmBody(realm)

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

	body := buildRealmBody(realm)

	if body["sslRequired"] != "external" {
		t.Errorf("expected sslRequired=external, got %v", body["sslRequired"])
	}
}

func TestBuildRealmBodyWithoutSslRequired(t *testing.T) {
	realm := config.Realm{
		Realm: "test",
	}

	body := buildRealmBody(realm)

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
