package provisioner

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"keycloak-provisioner/internal/config"
)

// TestEnsureIdentityProviderUpdateIsAFullReplace is the regression test that
// matters most here.
//
// Keycloak replaces the whole identity provider representation on update, so a
// field or config key left out of the body is deleted. Every other reconciler
// in this package sends a sparse body; if this one is ever "simplified" to
// match, the client secret and any config key the schema does not model are
// silently destroyed. The server's representation carries a masked secret and
// an unmanaged key, and both must survive.
func TestEnsureIdentityProviderUpdateIsAFullReplace(t *testing.T) {
	var mu sync.Mutex
	var updated map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/identity-provider/instances": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{
					"alias":      "corp",
					"providerId": "oidc",
					"internalId": "server-assigned-uuid",
					// Set by linking the provider to an organization.
					"organizationId": "org-uuid",
					"types":          []any{"something"},
					"displayName":    "Set out of band",
					"trustEmail":     true,
					"config": map[string]any{
						"clientId": "kc",
						// Keycloak masks a stored secret on read and reads the
						// mask back as "keep what you have".
						"clientSecret": "**********",
						// A key the YAML never mentions.
						"defaultScope": "openid email",
					},
				},
			})
		},
		"PUT /admin/realms/{realm}/identity-provider/instances/{alias}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&updated)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	enabled := true
	realm := config.Realm{
		Realm: "test-realm",
		IdentityProviders: []config.IdentityProvider{{
			Alias:      "corp",
			ProviderId: "oidc",
			Enabled:    &enabled,
			Config: map[string]string{
				"clientId": "kc-updated",
			},
		}},
	}

	p := New(newTestClient(t, server.URL), &config.Config{})
	if err := p.ensureIdentityProviders(context.Background(), realm, "update"); err != nil {
		t.Fatalf("ensureIdentityProviders: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if updated == nil {
		t.Fatal("no update was sent")
	}

	cfg, ok := updated["config"].(map[string]any)
	if !ok {
		t.Fatalf("config missing from update body: %v", updated)
	}

	// Configured values win.
	if cfg["clientId"] != "kc-updated" {
		t.Errorf("clientId: got %v, want kc-updated", cfg["clientId"])
	}
	// The masked secret must be carried forward, or Keycloak deletes it.
	if cfg["clientSecret"] != "**********" {
		t.Errorf("clientSecret not carried forward: got %v", cfg["clientSecret"])
	}
	// An unmanaged key must survive.
	if cfg["defaultScope"] != "openid email" {
		t.Errorf("unmanaged config key not preserved: got %v", cfg["defaultScope"])
	}
	// An unmanaged top-level field must survive too.
	if updated["displayName"] != "Set out of band" {
		t.Errorf("unmanaged displayName not preserved: got %v", updated["displayName"])
	}
	if updated["trustEmail"] != true {
		t.Errorf("unmanaged trustEmail not preserved: got %v", updated["trustEmail"])
	}
	// Configured flags are applied.
	if updated["enabled"] != true {
		t.Errorf("enabled: got %v", updated["enabled"])
	}
	// Server-owned fields must be dropped.
	for _, key := range []string{"internalId", "organizationId", "types"} {
		if _, present := updated[key]; present {
			t.Errorf("server-owned field %q must not be echoed back", key)
		}
	}
	// alias and providerId are always sent: a differing alias renames.
	if updated["alias"] != "corp" || updated["providerId"] != "oidc" {
		t.Errorf("alias/providerId must always be sent: %v / %v", updated["alias"], updated["providerId"])
	}
}

func TestEnsureIdentityProviderCreate(t *testing.T) {
	var mu sync.Mutex
	var created map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/identity-provider/instances": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/identity-provider/instances": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&created)
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	realm := config.Realm{
		Realm: "test-realm",
		IdentityProviders: []config.IdentityProvider{{
			Alias:       "corp",
			ProviderId:  "oidc",
			DisplayName: "Corporate",
			Config:      map[string]string{"clientId": "kc"},
		}},
	}

	p := New(newTestClient(t, server.URL), &config.Config{})
	if err := p.ensureIdentityProviders(context.Background(), realm, "update"); err != nil {
		t.Fatalf("ensureIdentityProviders: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if created["alias"] != "corp" || created["providerId"] != "oidc" {
		t.Errorf("unexpected create body: %v", created)
	}
	if created["displayName"] != "Corporate" {
		t.Errorf("displayName: got %v", created["displayName"])
	}
}

func TestEnsureIdentityProviderCreateStrategySkipsExisting(t *testing.T) {
	var mu sync.Mutex
	updates := 0

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/identity-provider/instances": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"alias": "corp", "providerId": "oidc"}})
		},
		"PUT /admin/realms/{realm}/identity-provider/instances/{alias}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			updates++
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	realm := config.Realm{
		Realm:             "test-realm",
		IdentityProviders: []config.IdentityProvider{{Alias: "corp", ProviderId: "oidc"}},
	}

	p := New(newTestClient(t, server.URL), &config.Config{})
	if err := p.ensureIdentityProviders(context.Background(), realm, "create"); err != nil {
		t.Fatalf("ensureIdentityProviders: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if updates != 0 {
		t.Errorf("strategy=create must not update an existing provider, got %d updates", updates)
	}
}

// TestEnsureIdentityProviderMapperConfigIsReplaced pins the deliberate
// asymmetry with the provider itself: a mapper's config comes from the config
// alone, so removing a key removes it from Keycloak.
func TestEnsureIdentityProviderMapperConfigIsReplaced(t *testing.T) {
	var mu sync.Mutex
	var updated map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/identity-provider/instances": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"alias": "corp", "providerId": "oidc"}})
		},
		"PUT /admin/realms/{realm}/identity-provider/instances/{alias}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/identity-provider/instances/{alias}/mappers": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{
				"id":     "m-1",
				"name":   "email",
				"config": map[string]any{"stale": "value"},
			}})
		},
		"PUT /admin/realms/{realm}/identity-provider/instances/{alias}/mappers/{mid}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&updated)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	realm := config.Realm{
		Realm: "test-realm",
		IdentityProviders: []config.IdentityProvider{{
			Alias:      "corp",
			ProviderId: "oidc",
			Mappers: []config.IdentityProviderMapper{{
				Name:                   "email",
				IdentityProviderMapper: "oidc-user-attribute-idp-mapper",
				Config:                 map[string]string{"claim": "email"},
			}},
		}},
	}

	p := New(newTestClient(t, server.URL), &config.Config{})
	if err := p.ensureIdentityProviders(context.Background(), realm, "update"); err != nil {
		t.Fatalf("ensureIdentityProviders: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	cfg, _ := updated["config"].(map[string]any)
	if cfg["claim"] != "email" {
		t.Errorf("mapper config not applied: %v", cfg)
	}
	if _, present := cfg["stale"]; present {
		t.Error("mapper config is a replace, not a merge: the stale key must be gone")
	}
	// Both are required: without the id Keycloak answers 500, without the
	// mapper type it answers 409.
	if updated["id"] != "m-1" {
		t.Errorf("update body must carry the mapper id, got %v", updated["id"])
	}
	if updated["identityProviderMapper"] != "oidc-user-attribute-idp-mapper" {
		t.Errorf("update body must carry the mapper type, got %v", updated["identityProviderMapper"])
	}
}

func TestEnsureOrganizationIdentityProvidersUnknownAliasFails(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations/{id}/identity-providers": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{Name: "acme", IdentityProviders: []string{"missing"}}
	known := identityProviderIndex{"other": {"alias": "other", "providerId": "oidc"}}

	err := p.ensureOrganizationIdentityProviders(context.Background(), "test-realm", "org-1", o, known)
	if err == nil {
		t.Fatal("expected an unknown alias to fail rather than warn and skip")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error should name the alias, got: %v", err)
	}
}

// TestEnsureOrganizationIdentityProvidersRejectsForeignProvider covers the
// case config validation cannot: the provider is already linked to an
// organization this config does not describe. Keycloak answers a bare 400
// naming neither side, but the listing carries organizationId, so the check
// happens locally.
func TestEnsureOrganizationIdentityProvidersRejectsForeignProvider(t *testing.T) {
	var mu sync.Mutex
	links := 0

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations/{id}/identity-providers": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/organizations/{id}/identity-providers": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			links++
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{Name: "acme", IdentityProviders: []string{"corp"}}
	known := identityProviderIndex{"corp": {
		"alias":          "corp",
		"providerId":     "oidc",
		"organizationId": "some-other-org",
	}}

	err := p.ensureOrganizationIdentityProviders(context.Background(), "test-realm", "org-1", o, known)
	if err == nil {
		t.Fatal("expected a provider owned by another organization to fail")
	}
	if !strings.Contains(err.Error(), "some-other-org") {
		t.Errorf("error should name the owning organization, got: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if links != 0 {
		t.Errorf("the failing request should not be sent at all, got %d", links)
	}
}

func TestEnsureOrganizationIdentityProvidersSkipsAlreadyLinked(t *testing.T) {
	var mu sync.Mutex
	links := 0

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations/{id}/identity-providers": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"alias": "corp"}})
		},
		"POST /admin/realms/{realm}/organizations/{id}/identity-providers": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			links++
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{Name: "acme", IdentityProviders: []string{"corp"}}
	known := identityProviderIndex{"corp": {"alias": "corp", "providerId": "oidc"}}

	if err := p.ensureOrganizationIdentityProviders(context.Background(), "test-realm", "org-1", o, known); err != nil {
		t.Fatalf("ensureOrganizationIdentityProviders: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if links != 0 {
		t.Errorf("an already-linked provider must not be re-linked, got %d calls", links)
	}
}

// TestOrganizationsShareOneIdentityProviderListing pins the fix for an N+1: the
// realm's providers are listed once and shared, not re-listed per organization.
// With three organizations the old shape issued four listings.
func TestOrganizationsShareOneIdentityProviderListing(t *testing.T) {
	var mu sync.Mutex
	listings := 0

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"realm": "test-realm"})
		},
		"PUT /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/identity-provider/instances": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			listings++
			mu.Unlock()
			json.NewEncoder(w).Encode([]map[string]any{{"alias": "corp", "providerId": "oidc"}})
		},
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "org-" + r.URL.Query().Get("search"), "name": r.URL.Query().Get("search")},
			})
		},
		"PUT /admin/realms/{realm}/organizations/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/identity-providers": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/organizations/{id}/identity-providers": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	org := func(name string) config.Organization {
		return config.Organization{
			Name:              name,
			Domains:           []config.OrganizationDomain{{Name: name + ".test"}},
			IdentityProviders: []string{"corp"},
		}
	}

	orgsEnabled := true

	cfg := &config.Config{Realms: []config.Realm{{
		Realm:                "test-realm",
		OrganizationsEnabled: &orgsEnabled,
		Organizations:        []config.Organization{org("a"), org("b"), org("c")},
	}}}

	if err := New(newTestClient(t, server.URL), cfg).Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if listings != 1 {
		t.Errorf("expected one identity provider listing for the realm, got %d", listings)
	}
}
