package provisioner

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"keycloak-provisioner/internal/config"
)

// TestBuildCredentialBodyShape pins how Keycloak stores a TOTP credential: the
// secret and the parameters are JSON *strings*, not nested objects, and the
// secret is carried verbatim — Keycloak uses those characters as the HMAC key
// rather than base32-decoding them.
func TestBuildCredentialBodyShape(t *testing.T) {
	body := buildCredentialBody(config.Credential{
		Type:   "otp",
		Secret: "SEEDEDSECRET",
		Label:  "seeded",
	})

	if body["type"] != "otp" || body["userLabel"] != "seeded" {
		t.Errorf("unexpected body: %v", body)
	}

	var secret map[string]any
	if err := json.Unmarshal([]byte(body["secretData"].(string)), &secret); err != nil {
		t.Fatalf("secretData must be a JSON string: %v", err)
	}
	if secret["value"] != "SEEDEDSECRET" {
		t.Errorf("secret must be carried verbatim, got %v", secret["value"])
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(body["credentialData"].(string)), &data); err != nil {
		t.Fatalf("credentialData must be a JSON string: %v", err)
	}
	for key, want := range map[string]any{
		"subType": "totp", "digits": float64(6), "period": float64(30), "algorithm": "HmacSHA1",
	} {
		if data[key] != want {
			t.Errorf("credentialData[%q] = %v, want %v", key, data[key], want)
		}
	}
}

func TestBuildCredentialBodyHonoursOverrides(t *testing.T) {
	body := buildCredentialBody(config.Credential{
		Type: "otp", Secret: "s", Digits: 8, Period: 60, Algorithm: "HmacSHA256",
	})

	var data map[string]any
	if err := json.Unmarshal([]byte(body["credentialData"].(string)), &data); err != nil {
		t.Fatal(err)
	}
	if data["digits"] != float64(8) || data["period"] != float64(60) || data["algorithm"] != "HmacSHA256" {
		t.Errorf("overrides not applied: %v", data)
	}
	if _, ok := body["userLabel"]; ok {
		t.Error("an unset label must be omitted rather than sent empty")
	}
}

// TestEnsureUserCredentialsSkipsExisting is the guard that matters. A user
// update *appends* what its credentials array carries, so re-sending one the
// user already holds leaves two, and every run would add another.
func TestEnsureUserCredentialsSkipsExisting(t *testing.T) {
	var mu sync.Mutex
	updates := 0

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users/{id}/credentials": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "c-1", "type": "password"},
				{"id": "c-2", "type": "otp", "userLabel": "seeded"},
			})
		},
		"PUT /admin/realms/{realm}/users/{id}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			updates++
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	user := config.User{
		Username:    "bob",
		Credentials: []config.Credential{{Type: "otp", Label: "seeded", Secret: "s"}},
	}

	if err := p.ensureUserCredentials(context.Background(), "test-realm", "u-1", user); err != nil {
		t.Fatalf("ensureUserCredentials: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if updates != 0 {
		t.Errorf("a credential the user already holds must not be re-sent, got %d updates", updates)
	}
}

// TestEnsureUserCredentialsAddsMissing covers the other half, including that a
// second credential of the same type is distinguished by label.
func TestEnsureUserCredentialsAddsMissing(t *testing.T) {
	var mu sync.Mutex
	var sent map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users/{id}/credentials": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "c-2", "type": "otp", "userLabel": "phone"},
			})
		},
		"PUT /admin/realms/{realm}/users/{id}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&sent)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	user := config.User{
		Username: "bob",
		Credentials: []config.Credential{
			{Type: "otp", Label: "phone", Secret: "a"},
			{Type: "otp", Label: "tablet", Secret: "b"},
		},
	}

	if err := p.ensureUserCredentials(context.Background(), "test-realm", "u-1", user); err != nil {
		t.Fatalf("ensureUserCredentials: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	creds, ok := sent["credentials"].([]any)
	if !ok || len(creds) != 1 {
		t.Fatalf("expected only the missing credential to be sent, got %v", sent["credentials"])
	}
	if creds[0].(map[string]any)["userLabel"] != "tablet" {
		t.Errorf("wrong credential sent: %v", creds[0])
	}
	// Only credentials — a user update carrying other fields would risk
	// overwriting what the reconcile already settled.
	if len(sent) != 1 {
		t.Errorf("the update must carry credentials alone, got keys %v", sent)
	}
}

func TestBuildUserBodyRequiredActions(t *testing.T) {
	// Declared: sent, replacing whatever the user has.
	body := buildUserBody(config.User{Username: "bob", RequiredActions: []string{"CONFIGURE_TOTP"}})
	if got, ok := body["requiredActions"].([]string); !ok || len(got) != 1 {
		t.Errorf("expected requiredActions to be sent, got %v", body["requiredActions"])
	}

	// Declared empty: sent, which is how the config clears them.
	body = buildUserBody(config.User{Username: "bob", RequiredActions: []string{}})
	if _, ok := body["requiredActions"]; !ok {
		t.Error("an explicitly empty list must be sent so it clears")
	}

	// Undeclared: omitted, so Keycloak leaves them alone.
	body = buildUserBody(config.User{Username: "bob"})
	if _, ok := body["requiredActions"]; ok {
		t.Error("an undeclared requiredActions must be omitted")
	}
}
