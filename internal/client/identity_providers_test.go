package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// --- Identity Provider tests ---

func TestIdentityProviderEndpoints(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	bodies := map[string]string{}

	record := func(key string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)

			mu.Lock()
			seen[key] = r.URL.Path
			bodies[key] = string(raw)
			mu.Unlock()

			switch r.Method {
			case http.MethodPost:
				w.WriteHeader(http.StatusCreated)
			case http.MethodPut:
				w.WriteHeader(http.StatusNoContent)
			}
		}
	}

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/identity-provider/instances": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen["list"] = r.URL.Path
			// realmOnly must not be set: it hides org-linked providers.
			seen["listQuery"] = r.URL.RawQuery
			mu.Unlock()
			json.NewEncoder(w).Encode([]map[string]any{{"alias": "corp", "providerId": "oidc"}})
		},
		"POST /admin/realms/{realm}/identity-provider/instances":                      record("create"),
		"PUT /admin/realms/{realm}/identity-provider/instances/{alias}":               record("update"),
		"POST /admin/realms/{realm}/identity-provider/instances/{alias}/mappers":      record("createMapper"),
		"PUT /admin/realms/{realm}/identity-provider/instances/{alias}/mappers/{mid}": record("updateMapper"),
		"GET /admin/realms/{realm}/identity-provider/instances/{alias}/mappers": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen["listMappers"] = r.URL.Path
			mu.Unlock()
			json.NewEncoder(w).Encode([]map[string]any{{"id": "m-1", "name": "email"}})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	ctx := context.Background()

	providers, err := c.GetIdentityProviders(ctx, "test")
	if err != nil {
		t.Fatalf("GetIdentityProviders: %v", err)
	}
	if len(providers) != 1 || providers[0]["alias"] != "corp" {
		t.Errorf("unexpected providers: %v", providers)
	}

	if err := c.CreateIdentityProvider(ctx, "test", map[string]any{"alias": "corp"}); err != nil {
		t.Fatalf("CreateIdentityProvider: %v", err)
	}
	if err := c.UpdateIdentityProvider(ctx, "test", "corp", map[string]any{"alias": "corp"}); err != nil {
		t.Fatalf("UpdateIdentityProvider: %v", err)
	}

	mappers, err := c.GetIdentityProviderMappers(ctx, "test", "corp")
	if err != nil {
		t.Fatalf("GetIdentityProviderMappers: %v", err)
	}
	if len(mappers) != 1 || mappers[0]["id"] != "m-1" {
		t.Errorf("unexpected mappers: %v", mappers)
	}

	if err := c.CreateIdentityProviderMapper(ctx, "test", "corp", map[string]any{"name": "email"}); err != nil {
		t.Fatalf("CreateIdentityProviderMapper: %v", err)
	}
	if err := c.UpdateIdentityProviderMapper(ctx, "test", "corp", "m-1", map[string]any{"name": "email"}); err != nil {
		t.Fatalf("UpdateIdentityProviderMapper: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	wantPaths := map[string]string{
		"list":         "/admin/realms/test/identity-provider/instances",
		"create":       "/admin/realms/test/identity-provider/instances",
		"update":       "/admin/realms/test/identity-provider/instances/corp",
		"listMappers":  "/admin/realms/test/identity-provider/instances/corp/mappers",
		"createMapper": "/admin/realms/test/identity-provider/instances/corp/mappers",
		"updateMapper": "/admin/realms/test/identity-provider/instances/corp/mappers/m-1",
	}

	for key, want := range wantPaths {
		if seen[key] != want {
			t.Errorf("%s path: got %q, want %q", key, seen[key], want)
		}
	}

	if seen["listQuery"] != "" {
		t.Errorf("listing must not filter: got query %q; realmOnly hides org-linked providers", seen["listQuery"])
	}
}

func TestIdentityProviderAliasIsEscaped(t *testing.T) {
	var mu sync.Mutex
	var gotPath string

	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}/identity-provider/instances/{alias}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			gotPath = r.URL.EscapedPath()
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	if err := c.UpdateIdentityProvider(context.Background(), "test", "a b", map[string]any{}); err != nil {
		t.Fatalf("UpdateIdentityProvider: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/admin/realms/test/identity-provider/instances/a%20b" {
		t.Errorf("alias not escaped: %q", gotPath)
	}
}

// TestAddOrganizationIdentityProviderSendsBareString pins the oddity: the body
// is a bare JSON string, not an object. Keycloak rejects an object with 400.
func TestAddOrganizationIdentityProviderSendsBareString(t *testing.T) {
	var mu sync.Mutex
	var gotBody, gotPath string

	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/organizations/{id}/identity-providers": func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)

			mu.Lock()
			gotBody = string(raw)
			gotPath = r.URL.Path
			mu.Unlock()

			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	if err := c.AddOrganizationIdentityProvider(context.Background(), "test", "org-1", "corp"); err != nil {
		t.Fatalf("AddOrganizationIdentityProvider: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if gotPath != "/admin/realms/test/organizations/org-1/identity-providers" {
		t.Errorf("unexpected path: %q", gotPath)
	}
	if got := strings.TrimSpace(gotBody); got != `"corp"` {
		t.Errorf("body must be a bare JSON string, got %s", got)
	}
}

func TestGetOrganizationIdentityProviders(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations/{id}/identity-providers": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"alias": "corp"}})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)

	linked, err := c.GetOrganizationIdentityProviders(context.Background(), "test", "org-1")
	if err != nil {
		t.Fatalf("GetOrganizationIdentityProviders: %v", err)
	}
	if len(linked) != 1 || linked[0]["alias"] != "corp" {
		t.Errorf("unexpected linked providers: %v", linked)
	}
}

func TestIdentityProviderErrorStatuses(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/identity-provider/instances": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("duplicate alias"))
		},
		"POST /admin/realms/{realm}/identity-provider/instances/{alias}/mappers": func(w http.ResponseWriter, r *http.Request) {
			// A duplicate mapper name is a 400, not a 409.
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("duplicate name"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	ctx := context.Background()

	if err := c.CreateIdentityProvider(ctx, "test", map[string]any{"alias": "corp"}); err == nil {
		t.Error("expected an error for a duplicate alias")
	}
	if err := c.CreateIdentityProviderMapper(ctx, "test", "corp", map[string]any{"name": "m"}); err == nil {
		t.Error("expected an error for a duplicate mapper name")
	}
}
