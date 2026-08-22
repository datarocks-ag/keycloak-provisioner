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
