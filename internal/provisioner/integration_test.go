//go:build integration

package provisioner_test

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"testing"

	keycloak "github.com/stillya/testcontainers-keycloak"
	"github.com/testcontainers/testcontainers-go"

	"keycloak-provisioner/internal/client"
	"keycloak-provisioner/internal/config"
	"keycloak-provisioner/internal/provisioner"
)

func setupKeycloak(t *testing.T) (*client.Client, func()) {
	t.Helper()
	ctx := context.Background()

	kcContainer, err := keycloak.Run(ctx,
		"keycloak/keycloak:26.0",
		keycloak.WithAdminUsername("admin"),
		keycloak.WithAdminPassword("admin"),
	)
	if err != nil {
		t.Fatalf("failed to start keycloak container: %v", err)
	}

	baseURL, err := kcContainer.GetAuthServerURL(ctx)
	if err != nil {
		t.Fatalf("failed to get auth server URL: %v", err)
	}

	kc := client.New(baseURL, "admin", "admin")
	if err := kc.Connect(ctx); err != nil {
		t.Fatalf("failed to connect to keycloak: %v", err)
	}

	cleanup := func() {
		if err := testcontainers.TerminateContainer(kcContainer); err != nil {
			log.Printf("failed to terminate container: %v", err)
		}
	}

	return kc, cleanup
}

func writeTestConfig(t *testing.T, yaml string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestIntegrationFullProvisioning(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	configYAML := `
realms:
  - realm: "test-realm"
    displayName: "Test Realm"
    enabled: true
    clients:
      - clientId: "test-app"
        secret: "test-secret"
        enabled: true
        publicClient: false
        protocol: "openid-connect"
        redirectUris:
          - "https://example.com/*"
        protocolMappers:
          - name: "audience-mapper"
            protocol: "openid-connect"
            protocolMapper: "oidc-audience-mapper"
            config:
              "included.client.audience": "test-app"
              "id.token.claim": "true"
              "access.token.claim": "true"
        clientRoles:
          - name: "admin"
            description: "Administrator"
          - name: "user"
            description: "Regular user"
    roles:
      - name: "app-admin"
        description: "Application administrator"
      - name: "app-user"
        description: "Application user"
`
	cfgPath := writeTestConfig(t, configYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	ctx := context.Background()

	// First run
	p := provisioner.New(kc, cfg)
	if err := p.Run(ctx); err != nil {
		t.Fatalf("first provisioning run failed: %v", err)
	}

	// Verify realm exists
	realm, err := kc.GetRealm(ctx, "test-realm")
	if err != nil {
		t.Fatalf("getting realm: %v", err)
	}
	if realm == nil {
		t.Fatal("realm not found after provisioning")
	}
	if realm["displayName"] != "Test Realm" {
		t.Errorf("expected displayName 'Test Realm', got %v", realm["displayName"])
	}

	// Verify client exists
	clients, err := kc.GetClients(ctx, "test-realm", "test-app")
	if err != nil {
		t.Fatalf("getting clients: %v", err)
	}
	if len(clients) == 0 {
		t.Fatal("client not found after provisioning")
	}

	// Verify realm roles
	role, err := kc.GetRealmRole(ctx, "test-realm", "app-admin")
	if err != nil {
		t.Fatalf("getting realm role: %v", err)
	}
	if role == nil {
		t.Error("realm role 'app-admin' not found")
	}

	// Verify client roles
	clientUUID, ok := clients[0]["id"].(string)
	if !ok {
		t.Fatal("expected client 'id' to be a string")
	}
	clientRole, err := kc.GetClientRole(ctx, "test-realm", clientUUID, "admin")
	if err != nil {
		t.Fatalf("getting client role: %v", err)
	}
	if clientRole == nil {
		t.Error("client role 'admin' not found")
	}

	// Verify protocol mappers
	mappers, err := kc.GetProtocolMappers(ctx, "test-realm", clientUUID)
	if err != nil {
		t.Fatalf("getting protocol mappers: %v", err)
	}
	found := false
	for _, m := range mappers {
		if m["name"] == "audience-mapper" {
			found = true
			break
		}
	}
	if !found {
		t.Error("protocol mapper 'audience-mapper' not found")
	}
}

func TestIntegrationIdempotency(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	configYAML := `
realms:
  - realm: "idempotent-realm"
    enabled: true
    clients:
      - clientId: "idempotent-app"
        enabled: true
    roles:
      - name: "test-role"
`
	cfgPath := writeTestConfig(t, configYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// First run
	p := provisioner.New(kc, cfg)
	if err := p.Run(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// Second run (idempotent)
	p2 := provisioner.New(kc, cfg)
	if err := p2.Run(ctx); err != nil {
		t.Fatalf("second (idempotent) run: %v", err)
	}
}

func TestIntegrationEmptyConfig(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	cfgPath := writeTestConfig(t, "{}")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	p := provisioner.New(kc, cfg)
	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("empty config should succeed: %v", err)
	}
}
