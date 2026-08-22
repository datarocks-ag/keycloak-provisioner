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
