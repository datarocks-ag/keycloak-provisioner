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
	fullScope := false

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
		FullScopeAllowed:          &fullScope,
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
		"fullScopeAllowed":          false,
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
	// The scope lists must NOT be in the body: Keycloak reads them on create as
	// the client's complete scope list and detaches its own defaults, "roles"
	// included, so the client's tokens would carry no roles. They are attached
	// separately by ensureClientScopeAssignments.
	if _, ok := body["defaultClientScopes"]; ok {
		t.Error("defaultClientScopes must not be sent in the client body")
	}
	if _, ok := body["optionalClientScopes"]; ok {
		t.Error("optionalClientScopes must not be sent in the client body")
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

// TestBuildClientBodyFullScopeAllowed covers both directions of the flag and,
// more importantly, that leaving it unset sends no key at all. Keycloak's
// client update is a sparse merge for top-level booleans, so omitting the key
// preserves whatever the client already has rather than resetting it to the
// permissive default.
func TestBuildClientBodyFullScopeAllowed(t *testing.T) {
	on := true
	off := false

	tests := []struct {
		name    string
		configd *bool
		wantKey bool
		want    any
	}{
		{"unset sends no key", nil, false, nil},
		{"false is sent", &off, true, false},
		{"true is sent", &on, true, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := buildClientBody(config.Client{ClientID: "app", FullScopeAllowed: tc.configd}, nil, nil)

			got, ok := body["fullScopeAllowed"]
			if ok != tc.wantKey {
				t.Fatalf("fullScopeAllowed present = %v, want %v (body: %v)", ok, tc.wantKey, body)
			}
			if tc.wantKey && got != tc.want {
				t.Errorf("fullScopeAllowed = %v, want %v", got, tc.want)
			}
		})
	}
}
