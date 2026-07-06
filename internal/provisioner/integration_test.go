//go:build integration

package provisioner_test

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"keycloak-provisioner/internal/client"
	"keycloak-provisioner/internal/config"
	"keycloak-provisioner/internal/provisioner"

	keycloak "github.com/stillya/testcontainers-keycloak"
	"github.com/testcontainers/testcontainers-go"
)

func setupKeycloak(t *testing.T) (*client.Client, func()) {
	t.Helper()
	ctx := context.Background()

	kcContainer, err := keycloak.Run(ctx,
		// 26.2+ is required for Standard Token Exchange (RFC 8693).
		"keycloak/keycloak:26.2",
		keycloak.WithAdminUsername("admin"),
		keycloak.WithAdminPassword("admin"),
	)
	if err != nil {
		t.Fatalf("failed to start keycloak container: %v", err)
	}

	// Keycloak defaults master realm sslRequired=EXTERNAL, which blocks
	// token requests over HTTP from outside the container (Docker bridge).
	// Use kcadm.sh inside the container (where localhost is allowed) to
	// disable SSL so our HTTP-based client can authenticate.
	exitCode, _, err := kcContainer.Exec(ctx, []string{
		"/opt/keycloak/bin/kcadm.sh", "update", "realms/master",
		"-s", "sslRequired=NONE",
		"--server", "http://localhost:8080",
		"--realm", "master",
		"--user", "admin",
		"--password", "admin",
	})
	if err != nil || exitCode != 0 {
		t.Fatalf("failed to disable SSL on master realm: exit=%d err=%v", exitCode, err)
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
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
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

func TestIntegrationUpdatePath(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	ctx := context.Background()

	// First run: create resources
	initialYAML := `
realms:
  - realm: "update-realm"
    displayName: "Original Name"
    enabled: true
    clients:
      - clientId: "update-app"
        secret: "secret-v1"
        enabled: true
        protocol: "openid-connect"
        protocolMappers:
          - name: "aud-mapper"
            protocol: "openid-connect"
            protocolMapper: "oidc-audience-mapper"
            config:
              "included.client.audience": "update-app"
              "access.token.claim": "false"
        clientRoles:
          - name: "editor"
            description: "Original editor"
    roles:
      - name: "realm-editor"
        description: "Original realm editor"
`
	cfgPath := writeTestConfig(t, initialYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	p := provisioner.New(kc, cfg)
	if err := p.Run(ctx); err != nil {
		t.Fatalf("initial run: %v", err)
	}

	// Second run: update resources
	updatedYAML := `
realms:
  - realm: "update-realm"
    displayName: "Updated Name"
    enabled: true
    clients:
      - clientId: "update-app"
        secret: "secret-v2"
        enabled: true
        protocol: "openid-connect"
        protocolMappers:
          - name: "aud-mapper"
            protocol: "openid-connect"
            protocolMapper: "oidc-audience-mapper"
            config:
              "included.client.audience": "update-app"
              "access.token.claim": "true"
        clientRoles:
          - name: "editor"
            description: "Updated editor"
    roles:
      - name: "realm-editor"
        description: "Updated realm editor"
`
	cfgPath2 := writeTestConfig(t, updatedYAML)
	cfg2, err := config.Load(cfgPath2)
	if err != nil {
		t.Fatal(err)
	}

	p2 := provisioner.New(kc, cfg2)
	if err := p2.Run(ctx); err != nil {
		t.Fatalf("update run: %v", err)
	}

	// Verify realm was updated
	realm, err := kc.GetRealm(ctx, "update-realm")
	if err != nil {
		t.Fatalf("getting realm: %v", err)
	}
	if realm["displayName"] != "Updated Name" {
		t.Errorf("expected displayName 'Updated Name', got %v", realm["displayName"])
	}

	// Verify realm role was updated
	role, err := kc.GetRealmRole(ctx, "update-realm", "realm-editor")
	if err != nil {
		t.Fatalf("getting realm role: %v", err)
	}
	if role["description"] != "Updated realm editor" {
		t.Errorf("expected description 'Updated realm editor', got %v", role["description"])
	}

	// Verify client role was updated
	clients, err := kc.GetClients(ctx, "update-realm", "update-app")
	if err != nil {
		t.Fatalf("getting clients: %v", err)
	}
	if len(clients) == 0 {
		t.Fatal("client not found")
	}
	clientUUID, ok := clients[0]["id"].(string)
	if !ok {
		t.Fatal("expected client 'id' to be a string")
	}

	clientRole, err := kc.GetClientRole(ctx, "update-realm", clientUUID, "editor")
	if err != nil {
		t.Fatalf("getting client role: %v", err)
	}
	if clientRole["description"] != "Updated editor" {
		t.Errorf("expected description 'Updated editor', got %v", clientRole["description"])
	}

	// Verify protocol mapper was updated
	mappers, err := kc.GetProtocolMappers(ctx, "update-realm", clientUUID)
	if err != nil {
		t.Fatalf("getting protocol mappers: %v", err)
	}
	for _, m := range mappers {
		if m["name"] == "aud-mapper" {
			cfg, ok := m["config"].(map[string]any)
			if !ok {
				t.Fatal("expected mapper config to be a map")
			}
			if cfg["access.token.claim"] != "true" {
				t.Errorf("expected access.token.claim 'true', got %v", cfg["access.token.claim"])
			}
			return
		}
	}
	t.Error("protocol mapper 'aud-mapper' not found after update")
}

func TestIntegrationCreateStrategy(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	ctx := context.Background()

	// First run: create resources with default strategy
	initialYAML := `
realms:
  - realm: "strategy-realm"
    displayName: "Original"
    enabled: true
    clients:
      - clientId: "strategy-app"
        enabled: true
        protocol: "openid-connect"
        clientRoles:
          - name: "viewer"
            description: "Original viewer"
    roles:
      - name: "strategy-role"
        description: "Original role"
`
	cfgPath := writeTestConfig(t, initialYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	p := provisioner.New(kc, cfg)
	if err := p.Run(ctx); err != nil {
		t.Fatalf("initial run: %v", err)
	}

	// Second run: strategy=create should skip existing resources
	createYAML := `
strategy: "create"
realms:
  - realm: "strategy-realm"
    displayName: "Should Not Change"
    enabled: true
    clients:
      - clientId: "strategy-app"
        enabled: true
        protocol: "openid-connect"
        clientRoles:
          - name: "viewer"
            description: "Should not change"
    roles:
      - name: "strategy-role"
        description: "Should not change"
`
	cfgPath2 := writeTestConfig(t, createYAML)
	cfg2, err := config.Load(cfgPath2)
	if err != nil {
		t.Fatal(err)
	}

	p2 := provisioner.New(kc, cfg2)
	if err := p2.Run(ctx); err != nil {
		t.Fatalf("create-strategy run: %v", err)
	}

	// Verify realm was NOT updated
	realm, err := kc.GetRealm(ctx, "strategy-realm")
	if err != nil {
		t.Fatalf("getting realm: %v", err)
	}
	if realm["displayName"] != "Original" {
		t.Errorf("expected displayName to stay 'Original', got %v", realm["displayName"])
	}

	// Verify realm role was NOT updated
	role, err := kc.GetRealmRole(ctx, "strategy-realm", "strategy-role")
	if err != nil {
		t.Fatalf("getting realm role: %v", err)
	}
	if role["description"] != "Original role" {
		t.Errorf("expected description to stay 'Original role', got %v", role["description"])
	}

	// Verify client role was NOT updated
	clients, err := kc.GetClients(ctx, "strategy-realm", "strategy-app")
	if err != nil {
		t.Fatalf("getting clients: %v", err)
	}
	if len(clients) == 0 {
		t.Fatal("client not found")
	}
	clientUUID, ok := clients[0]["id"].(string)
	if !ok {
		t.Fatal("expected client 'id' to be a string")
	}

	clientRole, err := kc.GetClientRole(ctx, "strategy-realm", clientUUID, "viewer")
	if err != nil {
		t.Fatalf("getting client role: %v", err)
	}
	if clientRole["description"] != "Original viewer" {
		t.Errorf("expected description to stay 'Original viewer', got %v", clientRole["description"])
	}
}

func TestIntegrationMultipleClients(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	ctx := context.Background()

	configYAML := `
realms:
  - realm: "multi-client-realm"
    enabled: true
    clients:
      - clientId: "frontend-app"
        enabled: true
        publicClient: true
        protocol: "openid-connect"
        redirectUris:
          - "http://localhost:3000/*"
        clientRoles:
          - name: "user"
      - clientId: "backend-api"
        enabled: true
        publicClient: false
        protocol: "openid-connect"
        secret: "backend-secret"
        serviceAccountsEnabled: true
        clientRoles:
          - name: "service"
            description: "Service account role"
        protocolMappers:
          - name: "backend-audience"
            protocol: "openid-connect"
            protocolMapper: "oidc-audience-mapper"
            config:
              "included.client.audience": "backend-api"
              "access.token.claim": "true"
      - clientId: "admin-cli-ext"
        enabled: true
        protocol: "openid-connect"
        bearerOnly: true
    roles:
      - name: "global-admin"
        description: "Global admin across all clients"
`
	cfgPath := writeTestConfig(t, configYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	p := provisioner.New(kc, cfg)
	if err := p.Run(ctx); err != nil {
		t.Fatalf("provisioning: %v", err)
	}

	// Verify all three clients exist
	for _, clientID := range []string{"frontend-app", "backend-api", "admin-cli-ext"} {
		clients, err := kc.GetClients(ctx, "multi-client-realm", clientID)
		if err != nil {
			t.Fatalf("getting client %s: %v", clientID, err)
		}
		if len(clients) == 0 {
			t.Errorf("client %s not found", clientID)
		}
	}

	// Verify backend-api has its protocol mapper and client role
	backendClients, err := kc.GetClients(ctx, "multi-client-realm", "backend-api")
	if err != nil {
		t.Fatalf("getting backend-api: %v", err)
	}
	backendUUID, ok := backendClients[0]["id"].(string)
	if !ok {
		t.Fatal("expected client 'id' to be a string")
	}

	mappers, err := kc.GetProtocolMappers(ctx, "multi-client-realm", backendUUID)
	if err != nil {
		t.Fatalf("getting protocol mappers: %v", err)
	}
	found := false
	for _, m := range mappers {
		if m["name"] == "backend-audience" {
			found = true
			break
		}
	}
	if !found {
		t.Error("protocol mapper 'backend-audience' not found on backend-api")
	}

	serviceRole, err := kc.GetClientRole(ctx, "multi-client-realm", backendUUID, "service")
	if err != nil {
		t.Fatalf("getting client role: %v", err)
	}
	if serviceRole == nil {
		t.Error("client role 'service' not found on backend-api")
	}

	// Verify frontend-app has its client role
	frontendClients, err := kc.GetClients(ctx, "multi-client-realm", "frontend-app")
	if err != nil {
		t.Fatalf("getting frontend-app: %v", err)
	}
	frontendUUID, ok := frontendClients[0]["id"].(string)
	if !ok {
		t.Fatal("expected client 'id' to be a string")
	}

	userRole, err := kc.GetClientRole(ctx, "multi-client-realm", frontendUUID, "user")
	if err != nil {
		t.Fatalf("getting client role: %v", err)
	}
	if userRole == nil {
		t.Error("client role 'user' not found on frontend-app")
	}

	// Verify realm role
	globalAdmin, err := kc.GetRealmRole(ctx, "multi-client-realm", "global-admin")
	if err != nil {
		t.Fatalf("getting realm role: %v", err)
	}
	if globalAdmin == nil {
		t.Error("realm role 'global-admin' not found")
	}
}

func TestIntegrationRealmStrategyOverride(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	ctx := context.Background()

	// First run: create two realms
	initialYAML := `
realms:
  - realm: "override-update"
    displayName: "Will Update"
    enabled: true
    roles:
      - name: "role-a"
        description: "Original A"
  - realm: "override-create"
    displayName: "Will Not Update"
    enabled: true
    roles:
      - name: "role-b"
        description: "Original B"
`
	cfgPath := writeTestConfig(t, initialYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	p := provisioner.New(kc, cfg)
	if err := p.Run(ctx); err != nil {
		t.Fatalf("initial run: %v", err)
	}

	// Second run: global strategy=update, but one realm overrides to create
	overrideYAML := `
strategy: "update"
realms:
  - realm: "override-update"
    displayName: "Updated Name"
    enabled: true
    roles:
      - name: "role-a"
        description: "Updated A"
  - realm: "override-create"
    strategy: "create"
    displayName: "Should Stay Original"
    enabled: true
    roles:
      - name: "role-b"
        description: "Should Stay Original B"
`
	cfgPath2 := writeTestConfig(t, overrideYAML)
	cfg2, err := config.Load(cfgPath2)
	if err != nil {
		t.Fatal(err)
	}

	p2 := provisioner.New(kc, cfg2)
	if err := p2.Run(ctx); err != nil {
		t.Fatalf("override run: %v", err)
	}

	// Verify the update realm was updated
	realm1, err := kc.GetRealm(ctx, "override-update")
	if err != nil {
		t.Fatalf("getting realm: %v", err)
	}
	if realm1["displayName"] != "Updated Name" {
		t.Errorf("expected 'Updated Name', got %v", realm1["displayName"])
	}

	role1, err := kc.GetRealmRole(ctx, "override-update", "role-a")
	if err != nil {
		t.Fatalf("getting role: %v", err)
	}
	if role1["description"] != "Updated A" {
		t.Errorf("expected 'Updated A', got %v", role1["description"])
	}

	// Verify the create realm was NOT updated
	realm2, err := kc.GetRealm(ctx, "override-create")
	if err != nil {
		t.Fatalf("getting realm: %v", err)
	}
	if realm2["displayName"] != "Will Not Update" {
		t.Errorf("expected 'Will Not Update', got %v", realm2["displayName"])
	}

	role2, err := kc.GetRealmRole(ctx, "override-create", "role-b")
	if err != nil {
		t.Fatalf("getting role: %v", err)
	}
	if role2["description"] != "Original B" {
		t.Errorf("expected 'Original B', got %v", role2["description"])
	}
}

func TestIntegrationSslRequired(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	ctx := context.Background()

	// Test 1: Set sslRequired on a realm using lowercase config value
	configYAML := `
masterRealm:
  sslRequired: none
realms:
  - realm: "ssl-test-realm"
    enabled: true
    sslRequired: none
`
	cfgPath := writeTestConfig(t, configYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Verify normalization happened during Load (lowercase)
	if cfg.MasterRealm.SslRequired != "none" {
		t.Fatalf("expected config normalized to none, got %q", cfg.MasterRealm.SslRequired)
	}
	if cfg.Realms[0].SslRequired != "none" {
		t.Fatalf("expected config normalized to none, got %q", cfg.Realms[0].SslRequired)
	}

	p := provisioner.New(kc, cfg)
	if err := p.Run(ctx); err != nil {
		t.Fatalf("provisioning run failed: %v", err)
	}

	// Verify master realm sslRequired was set (Keycloak returns lowercase)
	master, err := kc.GetRealm(ctx, "master")
	if err != nil {
		t.Fatalf("getting master realm: %v", err)
	}
	if master["sslRequired"] != "none" {
		t.Errorf("expected master sslRequired=none, got %v", master["sslRequired"])
	}

	// Verify realm sslRequired was set
	realm, err := kc.GetRealm(ctx, "ssl-test-realm")
	if err != nil {
		t.Fatalf("getting realm: %v", err)
	}
	if realm == nil {
		t.Fatal("realm not found after provisioning")
	}
	if realm["sslRequired"] != "none" {
		t.Errorf("expected sslRequired=none, got %v", realm["sslRequired"])
	}

	// Test 2: Idempotent re-run with same value should not error
	p2 := provisioner.New(kc, cfg)
	if err := p2.Run(ctx); err != nil {
		t.Fatalf("idempotent re-run failed: %v", err)
	}

	// Test 3: Change sslRequired to a different value
	configYAML2 := `
masterRealm:
  sslRequired: external
realms:
  - realm: "ssl-test-realm"
    enabled: true
    sslRequired: external
`
	cfgPath2 := writeTestConfig(t, configYAML2)
	cfg2, err := config.Load(cfgPath2)
	if err != nil {
		t.Fatalf("failed to load updated config: %v", err)
	}

	p3 := provisioner.New(kc, cfg2)
	if err := p3.Run(ctx); err != nil {
		t.Fatalf("update run failed: %v", err)
	}

	master2, err := kc.GetRealm(ctx, "master")
	if err != nil {
		t.Fatalf("getting master realm: %v", err)
	}
	if master2["sslRequired"] != "external" {
		t.Errorf("expected master sslRequired=external, got %v", master2["sslRequired"])
	}

	realm2, err := kc.GetRealm(ctx, "ssl-test-realm")
	if err != nil {
		t.Fatalf("getting realm: %v", err)
	}
	if realm2["sslRequired"] != "external" {
		t.Errorf("expected sslRequired=external, got %v", realm2["sslRequired"])
	}
}

func TestIntegrationCreateStrategyNewResourcesStillCreated(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	ctx := context.Background()

	// First run: create realm with one client
	initialYAML := `
realms:
  - realm: "additive-realm"
    enabled: true
    clients:
      - clientId: "existing-app"
        enabled: true
        protocol: "openid-connect"
`
	cfgPath := writeTestConfig(t, initialYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	p := provisioner.New(kc, cfg)
	if err := p.Run(ctx); err != nil {
		t.Fatalf("initial run: %v", err)
	}

	// Second run: strategy=create, add a new client and new role
	// Existing resources should be skipped, new ones should be created
	additiveYAML := `
strategy: "create"
realms:
  - realm: "additive-realm"
    enabled: true
    clients:
      - clientId: "existing-app"
        enabled: true
        protocol: "openid-connect"
      - clientId: "new-app"
        enabled: true
        protocol: "openid-connect"
    roles:
      - name: "new-role"
        description: "Brand new role"
`
	cfgPath2 := writeTestConfig(t, additiveYAML)
	cfg2, err := config.Load(cfgPath2)
	if err != nil {
		t.Fatal(err)
	}

	p2 := provisioner.New(kc, cfg2)
	if err := p2.Run(ctx); err != nil {
		t.Fatalf("additive run: %v", err)
	}

	// Verify the new client was created
	newClients, err := kc.GetClients(ctx, "additive-realm", "new-app")
	if err != nil {
		t.Fatalf("getting new client: %v", err)
	}
	if len(newClients) == 0 {
		t.Error("new client 'new-app' should have been created even with strategy=create")
	}

	// Verify the new role was created
	newRole, err := kc.GetRealmRole(ctx, "additive-realm", "new-role")
	if err != nil {
		t.Fatalf("getting new role: %v", err)
	}
	if newRole == nil {
		t.Error("new role 'new-role' should have been created even with strategy=create")
	}
}

func TestIntegrationMasterRealmUserProvisioning(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	configYAML := `
masterRealm:
  users:
    - username: "extra-master-admin"
      password: "secret-pw"
      enabled: true
      email: "extra@example.com"
      firstName: "Extra"
      lastName: "Admin"
      emailVerified: true
`
	cfgPath := writeTestConfig(t, configYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := provisioner.New(kc, cfg).Run(ctx); err != nil {
		t.Fatalf("provisioning master user: %v", err)
	}

	users, err := kc.GetUsers(ctx, "master", "extra-master-admin")
	if err != nil {
		t.Fatalf("looking up master user: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 master user, got %d", len(users))
	}
	if users[0]["email"] != "extra@example.com" {
		t.Errorf("unexpected email: %v", users[0]["email"])
	}
}

func TestIntegrationMissingRoleErrorsOnUserAssignment(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	// app-user references a realm role that is NOT defined in the config and
	// does not exist in Keycloak. This must surface as a clear error.
	configYAML := `
realms:
  - realm: "missing-role-realm"
    enabled: true
    users:
      - username: "broken-user"
        enabled: true
        roles:
          realm:
            - does-not-exist
`
	cfgPath := writeTestConfig(t, configYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	err = provisioner.New(kc, cfg).Run(context.Background())
	if err == nil {
		t.Fatal("expected provisioning error for non-existent role")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("expected error to mention missing role, got: %v", err)
	}
}

func TestIntegrationStandardTokenExchange(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	ctx := context.Background()

	configYAML := `
realms:
  - realm: "token-exchange-realm"
    enabled: true
    clients:
      - clientId: "exchange-service"
        enabled: true
        publicClient: false
        secret: "exchange-secret"
        protocol: "openid-connect"
        standardTokenExchangeEnabled: true
`
	cfgPath := writeTestConfig(t, configYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// First run creates the client with token exchange enabled.
	if err := provisioner.New(kc, cfg).Run(ctx); err != nil {
		t.Fatalf("provisioning run failed: %v", err)
	}

	assertTokenExchangeEnabled := func() {
		t.Helper()
		clients, err := kc.GetClients(ctx, "token-exchange-realm", "exchange-service")
		if err != nil {
			t.Fatalf("getting client: %v", err)
		}
		if len(clients) == 0 {
			t.Fatal("client not found after provisioning")
		}
		attrs, ok := clients[0]["attributes"].(map[string]any)
		if !ok {
			t.Fatalf("expected client attributes map, got %T", clients[0]["attributes"])
		}
		if attrs["standard.token.exchange.enabled"] != "true" {
			t.Errorf("expected standard.token.exchange.enabled=true, got %v", attrs["standard.token.exchange.enabled"])
		}
	}

	assertTokenExchangeEnabled()

	// Second run is idempotent and keeps the attribute set.
	if err := provisioner.New(kc, cfg).Run(ctx); err != nil {
		t.Fatalf("idempotent re-run failed: %v", err)
	}
	assertTokenExchangeEnabled()
}

func TestIntegrationDryRunDoesNotMutate(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	configYAML := `
realms:
  - realm: "dryrun-realm"
    displayName: "Dry Run"
    enabled: true
    clients:
      - clientId: "dryrun-app"
        enabled: true
    roles:
      - name: "dryrun-role"
`
	cfgPath := writeTestConfig(t, configYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	api := provisioner.NewDryRunAdapter(kc)
	if err := provisioner.New(api, cfg).Run(ctx); err != nil {
		t.Fatalf("dry-run: %v", err)
	}

	// Realm must NOT exist after dry-run.
	got, err := kc.GetRealm(ctx, "dryrun-realm")
	if err != nil {
		t.Fatalf("looking up realm: %v", err)
	}
	if got != nil {
		t.Errorf("dry-run created the realm — got %v", got)
	}
}
