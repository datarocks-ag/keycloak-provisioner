package provisioner

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"keycloak-provisioner/internal/config"
)

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

func TestEnsureProtocolMapperOnClientScopeCreatesWhenMissing(t *testing.T) {
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

	if err := p.ensureProtocolMapper(context.Background(), "test", clientScopeMapperTarget("cs-1", "orders:read"), pm, "update"); err != nil {
		t.Fatalf("ensureProtocolMapper on client scope: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if created["name"] != "audience" {
		t.Errorf("unexpected created mapper: %v", created)
	}
}

func TestEnsureProtocolMapperOnClientScopeUpdatesExisting(t *testing.T) {
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

	if err := p.ensureProtocolMapper(context.Background(), "test", clientScopeMapperTarget("cs-1", "orders:read"), pm, "update"); err != nil {
		t.Fatalf("ensureProtocolMapper on client scope: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if updated["id"] != "m-1" {
		t.Errorf("update should carry the mapper id, got %v", updated["id"])
	}
}

func TestEnsureProtocolMapperOnClientScopeCreateStrategySkipsExisting(t *testing.T) {
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

	if err := p.ensureProtocolMapper(context.Background(), "test", clientScopeMapperTarget("cs-1", "orders:read"), pm, "create"); err != nil {
		t.Fatalf("ensureProtocolMapper on client scope: %v", err)
	}
}
