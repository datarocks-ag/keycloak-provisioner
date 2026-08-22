//go:build integration

package provisioner_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"keycloak-provisioner/internal/client"
	"keycloak-provisioner/internal/compat"
	"keycloak-provisioner/internal/config"
	"keycloak-provisioner/internal/provisioner"

	keycloak "github.com/stillya/testcontainers-keycloak"
	"github.com/testcontainers/testcontainers-go"
)

// keycloakImage is the version these tests target. 26.2+ is required for
// Standard Token Exchange (RFC 8693), 26+ for Organizations, and 26.6+ for
// organization groups, so 26.6 is the supported floor.
const keycloakImage = "keycloak/keycloak:26.6"

// The suite shares one Keycloak across every test that targets the supported
// version. Starting a container per test cost roughly ten seconds each and
// pushed the package past the ten-minute test timeout in CI, as well as
// straining the Docker daemon enough to produce spurious readiness failures.
//
// Sharing is safe because each test provisions its own uniquely named realm.
// The one piece of genuinely global state is the master realm, so a test that
// changes it has to put it back — see TestIntegrationSslRequired.
//
// Startup is lazy so a unit-test-only run under the integration build tag does
// not pay for a container it never uses; TestMain does the teardown.
var (
	sharedOnce      sync.Once
	sharedClient    *client.Client
	sharedTerminate func()
	sharedErr       error
)

func TestMain(m *testing.M) {
	code := m.Run()

	if sharedTerminate != nil {
		sharedTerminate()
	}

	os.Exit(code)
}

// setupKeycloak returns the Keycloak shared by the whole suite. The returned
// function is a no-op: the container outlives the test, and TestMain tears it
// down. It keeps the call signature that per-test containers used.
func setupKeycloak(t *testing.T) (*client.Client, func()) {
	t.Helper()

	sharedOnce.Do(func() {
		sharedClient, sharedTerminate, sharedErr = startKeycloak(keycloakImage)
	})

	if sharedErr != nil {
		t.Fatalf("failed to start the shared keycloak container: %v", sharedErr)
	}

	return sharedClient, func() {}
}

// setupKeycloakVersion starts a container of its own, for a test that needs a
// release other than the supported floor. Prefer setupKeycloak; this exists
// only for version-specific behaviour.
func setupKeycloakVersion(t *testing.T, image string) (*client.Client, func()) {
	t.Helper()

	kc, terminate, err := startKeycloak(image)
	if err != nil {
		t.Fatalf("failed to start keycloak container: %v", err)
	}

	return kc, terminate
}

func startKeycloak(image string) (*client.Client, func(), error) {
	ctx := context.Background()

	kcContainer, err := keycloak.Run(ctx,
		image,
		keycloak.WithAdminUsername("admin"),
		keycloak.WithAdminPassword("admin"),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("starting keycloak container: %w", err)
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
	if err != nil {
		return nil, nil, fmt.Errorf("disabling SSL on master realm: %w", err)
	}
	// Exec reports a non-zero status without returning an error, so this is a
	// separate case: wrapping a nil err with %w would render "%!w(<nil>)".
	if exitCode != 0 {
		return nil, nil, fmt.Errorf("disabling SSL on master realm: kcadm exited %d", exitCode)
	}

	baseURL, err := kcContainer.GetAuthServerURL(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("getting auth server URL: %w", err)
	}

	kc := client.New(baseURL, "admin", "admin")
	if err := kc.Connect(ctx); err != nil {
		return nil, nil, fmt.Errorf("connecting to keycloak: %w", err)
	}

	terminate := func() {
		if err := testcontainers.TerminateContainer(kcContainer); err != nil {
			log.Printf("failed to terminate container: %v", err)
		}
	}

	return kc, terminate, nil
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
    groups:
      - name: "engineering"
        attributes:
          department: ["engineering"]
        realmRoles: ["app-admin"]
        clientRoles:
          test-app: ["admin"]
        subGroups:
          - name: "backend"
    users:
      - username: "dev-user"
        enabled: true
        groups:
          - "engineering"
          - "/engineering/backend"
          - "does-not-exist"  # missing group: warned and skipped, must not fail the run
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

	// Verify group exists with its attribute
	groups, err := kc.GetGroups(ctx, "test-realm", "", "engineering")
	if err != nil {
		t.Fatalf("getting groups: %v", err)
	}
	if len(groups) == 0 {
		t.Fatal("group 'engineering' not found after provisioning")
	}
	groupUUID, ok := groups[0]["id"].(string)
	if !ok {
		t.Fatal("expected group 'id' to be a string")
	}

	// The group list returns a brief representation; fetch the full group for attributes.
	fullGroup, err := kc.GetGroup(ctx, "test-realm", groupUUID)
	if err != nil {
		t.Fatalf("getting full group: %v", err)
	}
	if attrs, ok := fullGroup["attributes"].(map[string]any); ok {
		if dept, ok := attrs["department"].([]any); !ok || len(dept) == 0 || dept[0] != "engineering" {
			t.Errorf("expected group attribute department=[engineering], got %v", attrs["department"])
		}
	} else {
		t.Error("expected group to have attributes")
	}

	// Verify subgroup exists
	subGroups, err := kc.GetGroups(ctx, "test-realm", groupUUID, "backend")
	if err != nil {
		t.Fatalf("getting subgroups: %v", err)
	}
	foundSub := false
	for _, sg := range subGroups {
		if sg["name"] == "backend" {
			foundSub = true
			break
		}
	}
	if !foundSub {
		t.Error("subgroup 'backend' not found")
	}

	// Verify realm role mapping
	realmMappings, err := kc.GetGroupRealmRoleMappings(ctx, "test-realm", groupUUID)
	if err != nil {
		t.Fatalf("getting group realm role mappings: %v", err)
	}
	foundRealmRole := false
	for _, m := range realmMappings {
		if m["name"] == "app-admin" {
			foundRealmRole = true
			break
		}
	}
	if !foundRealmRole {
		t.Error("realm role 'app-admin' not mapped to group 'engineering'")
	}

	// Verify client role mapping
	clientMappings, err := kc.GetGroupClientRoleMappings(ctx, "test-realm", groupUUID, clientUUID)
	if err != nil {
		t.Fatalf("getting group client role mappings: %v", err)
	}
	foundClientRole := false
	for _, m := range clientMappings {
		if m["name"] == "admin" {
			foundClientRole = true
			break
		}
	}
	if !foundClientRole {
		t.Error("client role 'admin' not mapped to group 'engineering'")
	}

	// Verify user group memberships (missing group was skipped with a warning)
	users, err := kc.GetUsers(ctx, "test-realm", "dev-user")
	if err != nil {
		t.Fatalf("getting users: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
	userUUID, ok := users[0]["id"].(string)
	if !ok {
		t.Fatal("expected user 'id' to be a string")
	}
	memberships, err := kc.GetUserGroups(ctx, "test-realm", userUUID)
	if err != nil {
		t.Fatalf("getting user groups: %v", err)
	}
	memberPaths := make(map[string]bool, len(memberships))
	for _, g := range memberships {
		if path, ok := g["path"].(string); ok {
			memberPaths[path] = true
		}
	}
	if !memberPaths["/engineering"] {
		t.Error("user 'dev-user' is not a member of group '/engineering'")
	}
	if !memberPaths["/engineering/backend"] {
		t.Error("user 'dev-user' is not a member of group '/engineering/backend'")
	}
	if len(memberPaths) != 2 {
		t.Errorf("expected exactly 2 group memberships, got %v", memberPaths)
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
    groups:
      - name: "idempotent-group"
        realmRoles: ["test-role"]
        subGroups:
          - name: "idempotent-subgroup"
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

	// After two runs, group state must be stable — no duplicate top-level group,
	// subgroup, or realm-role mapping. This exercises the create/skip idempotency
	// of ensureGroup, its subgroup recursion, and the additive role-mapping diff.
	groups, err := kc.GetGroups(ctx, "idempotent-realm", "", "idempotent-group")
	if err != nil {
		t.Fatalf("getting groups: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected exactly 1 top-level group after two runs, got %d", len(groups))
	}
	groupUUID, ok := groups[0]["id"].(string)
	if !ok {
		t.Fatal("expected group 'id' to be a string")
	}

	subGroups, err := kc.GetGroups(ctx, "idempotent-realm", groupUUID, "idempotent-subgroup")
	if err != nil {
		t.Fatalf("getting subgroups: %v", err)
	}
	subCount := 0
	for _, sg := range subGroups {
		if sg["name"] == "idempotent-subgroup" {
			subCount++
		}
	}
	if subCount != 1 {
		t.Errorf("expected exactly 1 'idempotent-subgroup' after two runs, got %d", subCount)
	}

	mappings, err := kc.GetGroupRealmRoleMappings(ctx, "idempotent-realm", groupUUID)
	if err != nil {
		t.Fatalf("getting group realm role mappings: %v", err)
	}
	roleCount := 0
	for _, m := range mappings {
		if m["name"] == "test-role" {
			roleCount++
		}
	}
	if roleCount != 1 {
		t.Errorf("expected 'test-role' mapped exactly once after two runs, got %d", roleCount)
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

	// This test ends with master at sslRequired=external, which blocks token
	// requests over the Docker bridge and would break every test sharing this
	// container afterwards. Put it back.
	t.Cleanup(func() {
		if err := kc.UpdateRealm(ctx, "master", map[string]any{
			"realm":       "master",
			"sslRequired": "none",
		}); err != nil {
			t.Errorf("restoring master sslRequired: %v", err)
		}
	})

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

func TestIntegrationRealmAttributesMerge(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	configYAML := `
realms:
  - realm: "attr-realm"
    enabled: true
    attributes:
      frontendUrl: "https://id.example.com"
    acrLoaMap:
      silver: 1
      gold: 2
`
	cfgPath := writeTestConfig(t, configYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	if err := provisioner.New(kc, cfg).Run(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// Simulate an external actor writing an attribute the config knows nothing
	// about, the way an operator or another tool would.
	realm, err := kc.GetRealm(ctx, "attr-realm")
	if err != nil {
		t.Fatalf("getting realm: %v", err)
	}
	attrs, ok := realm["attributes"].(map[string]any)
	if !ok {
		t.Fatalf("expected attributes map, got %T", realm["attributes"])
	}
	attrs["setOutOfBand"] = "yes"
	if err := kc.UpdateRealm(ctx, "attr-realm", map[string]any{
		"realm":      "attr-realm",
		"attributes": attrs,
	}); err != nil {
		t.Fatalf("setting out-of-band attribute: %v", err)
	}

	// Re-running must not drop it.
	if err := provisioner.New(kc, cfg).Run(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}

	realm, err = kc.GetRealm(ctx, "attr-realm")
	if err != nil {
		t.Fatalf("getting realm after second run: %v", err)
	}
	attrs, ok = realm["attributes"].(map[string]any)
	if !ok {
		t.Fatalf("expected attributes map, got %T", realm["attributes"])
	}

	if got := attrs["setOutOfBand"]; got != "yes" {
		t.Errorf("out-of-band attribute was clobbered, got %v", got)
	}
	if got := attrs["frontendUrl"]; got != "https://id.example.com" {
		t.Errorf("configured attribute missing, got %v", got)
	}
	if got := attrs["acr.loa.map"]; got != `{"gold":2,"silver":1}` {
		t.Errorf("unexpected acr.loa.map, got %v", got)
	}
}

func TestIntegrationClientAcrLoaMap(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	configYAML := `
realms:
  - realm: "acr-realm"
    enabled: true
    clients:
      - clientId: "acr-app"
        enabled: true
        acrLoaMap:
          gold: 2
`
	cfgPath := writeTestConfig(t, configYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := provisioner.New(kc, cfg).Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	clients, err := kc.GetClients(ctx, "acr-realm", "acr-app")
	if err != nil {
		t.Fatalf("getting client: %v", err)
	}
	if len(clients) == 0 {
		t.Fatal("client acr-app not found")
	}

	attrs, ok := clients[0]["attributes"].(map[string]any)
	if !ok {
		t.Fatalf("expected attributes map, got %T", clients[0]["attributes"])
	}
	if got := attrs["acr.loa.map"]; got != `{"gold":2}` {
		t.Errorf("unexpected acr.loa.map, got %v", got)
	}
}

func TestIntegrationRealmOrganizationsEnabled(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	configYAML := `
realms:
  - realm: "org-realm"
    enabled: true
    organizationsEnabled: true
`
	cfgPath := writeTestConfig(t, configYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := provisioner.New(kc, cfg).Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	realm, err := kc.GetRealm(ctx, "org-realm")
	if err != nil {
		t.Fatalf("getting realm: %v", err)
	}
	if realm["organizationsEnabled"] != true {
		t.Errorf("expected organizationsEnabled=true, got %v", realm["organizationsEnabled"])
	}
}

func TestIntegrationClientAttributesMerge(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	configYAML := `
realms:
  - realm: "client-attr-realm"
    enabled: true
    clients:
      - clientId: "attr-app"
        enabled: true
        attributes:
          "post.logout.redirect.uris": "+"
`
	cfgPath := writeTestConfig(t, configYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	if err := provisioner.New(kc, cfg).Run(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}

	clients, err := kc.GetClients(ctx, "client-attr-realm", "attr-app")
	if err != nil {
		t.Fatalf("getting client: %v", err)
	}
	if len(clients) == 0 {
		t.Fatal("client attr-app not found")
	}
	uuid, ok := clients[0]["id"].(string)
	if !ok || uuid == "" {
		t.Fatalf("client attr-app has no usable id: %v", clients[0]["id"])
	}

	// Simulate an external actor writing an attribute the config does not know.
	attrs, ok := clients[0]["attributes"].(map[string]any)
	if !ok {
		attrs = map[string]any{}
	}
	attrs["setOutOfBand"] = "yes"
	if err := kc.UpdateClient(ctx, "client-attr-realm", uuid, map[string]any{
		"id":         uuid,
		"clientId":   "attr-app",
		"attributes": attrs,
	}); err != nil {
		t.Fatalf("setting out-of-band attribute: %v", err)
	}

	// Re-running must not drop it.
	if err := provisioner.New(kc, cfg).Run(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}

	clients, err = kc.GetClients(ctx, "client-attr-realm", "attr-app")
	if err != nil {
		t.Fatalf("getting client after second run: %v", err)
	}
	if len(clients) == 0 {
		t.Fatal("client attr-app disappeared after the second run")
	}
	attrs, ok = clients[0]["attributes"].(map[string]any)
	if !ok {
		t.Fatalf("expected attributes map, got %T", clients[0]["attributes"])
	}

	if got := attrs["setOutOfBand"]; got != "yes" {
		t.Errorf("out-of-band client attribute was clobbered, got %v", got)
	}
	if got := attrs["post.logout.redirect.uris"]; got != "+" {
		t.Errorf("configured attribute missing, got %v", got)
	}
}

func TestIntegrationClientScopes(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	configYAML := `
realms:
  - realm: "scope-realm"
    enabled: true
    clientScopes:
      - name: "orders:read"
        description: "Read orders"
        type: "optional"
        attributes:
          "include.in.token.scope": "true"
          "display.on.consent.screen": "false"
        protocolMappers:
          - name: "orders-audience"
            protocol: "openid-connect"
            protocolMapper: "oidc-audience-mapper"
            config:
              "included.client.audience": "orders-api"
              "access.token.claim": "true"
    clients:
      - clientId: "orders-app"
        enabled: true
        optionalClientScopes:
          - "orders:read"
`
	cfgPath := writeTestConfig(t, configYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := provisioner.New(kc, cfg).Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	scopes, err := kc.GetClientScopes(ctx, "scope-realm")
	if err != nil {
		t.Fatalf("getting client scopes: %v", err)
	}

	var scopeID string
	for _, s := range scopes {
		if s["name"] == "orders:read" {
			scopeID, _ = s["id"].(string)
		}
	}
	if scopeID == "" {
		t.Fatal("client scope orders:read was not created")
	}

	mappers, err := kc.GetClientScopeProtocolMappers(ctx, "scope-realm", scopeID)
	if err != nil {
		t.Fatalf("getting scope protocol mappers: %v", err)
	}
	if len(mappers) != 1 || mappers[0]["name"] != "orders-audience" {
		t.Errorf("unexpected scope protocol mappers: %v", mappers)
	}

	realmOptional, err := kc.GetRealmOptionalClientScopes(ctx, "scope-realm")
	if err != nil {
		t.Fatalf("getting realm optional scopes: %v", err)
	}
	if !containsScopeNamed(realmOptional, "orders:read") {
		t.Errorf("scope was not assigned to realm optionals: %v", realmOptional)
	}

	clients, err := kc.GetClients(ctx, "scope-realm", "orders-app")
	if err != nil || len(clients) == 0 {
		t.Fatalf("getting client: %v", err)
	}
	uuid, _ := clients[0]["id"].(string)

	clientOptional, err := kc.GetClientOptionalScopes(ctx, "scope-realm", uuid)
	if err != nil {
		t.Fatalf("getting client optional scopes: %v", err)
	}
	if !containsScopeNamed(clientOptional, "orders:read") {
		t.Errorf("scope was not assigned to the client: %v", clientOptional)
	}
}

// TestIntegrationClientScopeAssignmentOnUpdate pins the behaviour that motivated
// explicit scope assignment: Keycloak honours the inline defaultClientScopes
// field of the client representation only when the client is created, so adding
// a scope to an existing client's config must go through the dedicated
// assignment endpoint to take effect.
func TestIntegrationClientScopeAssignmentOnUpdate(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	before := `
realms:
  - realm: "scope-update-realm"
    enabled: true
    clientScopes:
      - name: "late:scope"
    clients:
      - clientId: "late-app"
        enabled: true
`
	after := `
realms:
  - realm: "scope-update-realm"
    enabled: true
    clientScopes:
      - name: "late:scope"
    clients:
      - clientId: "late-app"
        enabled: true
        defaultClientScopes:
          - "late:scope"
`
	ctx := context.Background()

	cfg, err := config.Load(writeTestConfig(t, before))
	if err != nil {
		t.Fatal(err)
	}
	if err := provisioner.New(kc, cfg).Run(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}

	cfg, err = config.Load(writeTestConfig(t, after))
	if err != nil {
		t.Fatal(err)
	}
	if err := provisioner.New(kc, cfg).Run(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}

	clients, err := kc.GetClients(ctx, "scope-update-realm", "late-app")
	if err != nil || len(clients) == 0 {
		t.Fatalf("getting client: %v", err)
	}
	uuid, _ := clients[0]["id"].(string)

	assigned, err := kc.GetClientDefaultScopes(ctx, "scope-update-realm", uuid)
	if err != nil {
		t.Fatalf("getting client default scopes: %v", err)
	}
	if !containsScopeNamed(assigned, "late:scope") {
		t.Errorf("scope added to an existing client was not assigned: %v", assigned)
	}
}

func containsScopeNamed(scopes []map[string]any, name string) bool {
	for _, s := range scopes {
		if s["name"] == name {
			return true
		}
	}
	return false
}

func TestIntegrationOrganizations(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	configYAML := `
realms:
  - realm: "orgs-realm"
    enabled: true
    organizationsEnabled: true
    users:
      - username: "alice"
        password: "alice-pw"
        enabled: true
        email: "alice@acme.com"
    organizations:
      - name: "acme"
        alias: "acme"
        enabled: true
        description: "ACME Corp"
        redirectUrl: "https://acme.example.com"
        domains:
          - name: "acme.com"
            verified: true
        attributes:
          tier:
            - "gold"
        members:
          - "alice"
`
	cfgPath := writeTestConfig(t, configYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := provisioner.New(kc, cfg).Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	orgs, err := kc.GetOrganizations(ctx, "orgs-realm", "acme")
	if err != nil {
		t.Fatalf("getting organizations: %v", err)
	}
	if len(orgs) != 1 {
		t.Fatalf("expected 1 organization, got %d: %v", len(orgs), orgs)
	}

	org := orgs[0]
	orgID, _ := org["id"].(string)
	if org["name"] != "acme" {
		t.Errorf("unexpected organization name: %v", org["name"])
	}
	if org["description"] != "ACME Corp" {
		t.Errorf("unexpected description: %v", org["description"])
	}

	domains, ok := org["domains"].([]any)
	if !ok || len(domains) != 1 {
		t.Fatalf("unexpected domains: %v", org["domains"])
	}
	domain, _ := domains[0].(map[string]any)
	if domain["name"] != "acme.com" {
		t.Errorf("unexpected domain: %v", domain)
	}

	// The member endpoint takes the user ID as a bare JSON string rather than
	// an object, which is unique among the endpoints this client calls.
	members, err := kc.GetOrganizationMembers(ctx, "orgs-realm", orgID)
	if err != nil {
		t.Fatalf("getting organization members: %v", err)
	}
	found := false
	for _, m := range members {
		if m["username"] == "alice" {
			found = true
		}
	}
	if !found {
		t.Errorf("alice was not added as an organization member: %v", members)
	}

	// Re-running must not duplicate the membership or fail.
	if err := provisioner.New(kc, cfg).Run(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}

	members, err = kc.GetOrganizationMembers(ctx, "orgs-realm", orgID)
	if err != nil {
		t.Fatalf("getting organization members after second run: %v", err)
	}
	count := 0
	for _, m := range members {
		if m["username"] == "alice" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 membership for alice, got %d", count)
	}
}

func TestIntegrationOrganizationGroups(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	configYAML := `
realms:
  - realm: "orggroups-realm"
    enabled: true
    organizationsEnabled: true
    users:
      - username: "alice"
        password: "alice-pw"
        enabled: true
        email: "alice@acme.com"
      - username: "mallory"
        password: "mallory-pw"
        enabled: true
        email: "mallory@example.com"
    organizations:
      - name: "acme"
        alias: "acme"
        enabled: true
        domains:
          - name: "acme.com"
        members:
          - "alice"
        groups:
          - name: "engineering"
            attributes:
              tier:
                - "gold"
            members:
              - "alice"
              # mallory is not an organization member, so this is warned about
              # and skipped rather than failing the run.
              - "mallory"
            subGroups:
              - name: "backend"
                members:
                  - "alice"
`
	cfgPath := writeTestConfig(t, configYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := provisioner.New(kc, cfg).Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	orgs, err := kc.GetOrganizations(ctx, "orggroups-realm", "acme")
	if err != nil || len(orgs) == 0 {
		t.Fatalf("getting organization: %v", err)
	}
	orgID, _ := orgs[0]["id"].(string)

	groups, err := kc.GetOrganizationGroups(ctx, "orggroups-realm", orgID, "")
	if err != nil {
		t.Fatalf("getting organization groups: %v", err)
	}
	var engID string
	for _, g := range groups {
		if g["name"] == "engineering" {
			engID, _ = g["id"].(string)
		}
	}
	if engID == "" {
		t.Fatalf("group engineering not created: %v", groups)
	}

	// Organization groups must not leak into the realm's own groups.
	realmGroups, err := kc.GetGroups(ctx, "orggroups-realm", "", "engineering")
	if err != nil {
		t.Fatalf("getting realm groups: %v", err)
	}
	if len(realmGroups) != 0 {
		t.Errorf("organization group leaked into realm groups: %v", realmGroups)
	}

	children, err := kc.GetOrganizationGroups(ctx, "orggroups-realm", orgID, engID)
	if err != nil {
		t.Fatalf("getting subgroups: %v", err)
	}
	if len(children) != 1 || children[0]["name"] != "backend" {
		t.Errorf("unexpected subgroups: %v", children)
	}

	members, err := kc.GetOrganizationGroupMembers(ctx, "orggroups-realm", orgID, engID)
	if err != nil {
		t.Fatalf("getting group members: %v", err)
	}
	names := map[string]bool{}
	for _, m := range members {
		if u, ok := m["username"].(string); ok {
			names[u] = true
		}
	}
	if !names["alice"] {
		t.Errorf("alice was not added to the group: %v", members)
	}
	if names["mallory"] {
		t.Error("mallory is not an organization member and must not have been added")
	}

	// Second run must be a clean no-op.
	if err := provisioner.New(kc, cfg).Run(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}

	groups, err = kc.GetOrganizationGroups(ctx, "orggroups-realm", orgID, "")
	if err != nil {
		t.Fatalf("getting groups after second run: %v", err)
	}
	count := 0
	for _, g := range groups {
		if g["name"] == "engineering" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 engineering group after two runs, got %d", count)
	}

	children, err = kc.GetOrganizationGroups(ctx, "orggroups-realm", orgID, engID)
	if err != nil {
		t.Fatalf("getting subgroups after second run: %v", err)
	}
	if len(children) != 1 {
		t.Errorf("expected exactly 1 subgroup after two runs, got %d: %v", len(children), children)
	}
}

func TestIntegrationAuthenticationFlowStepUp(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	configYAML := `
realms:
  - realm: "stepup-realm"
    enabled: true
    acrLoaMap:
      silver: 1
      gold: 2
    authenticationFlows:
      - alias: "browser-step-up"
        description: "Browser flow with LoA step-up"
        copyFrom: "browser"
        executions:
          - subflow: "loa-gold"
            requirement: "CONDITIONAL"
            executions:
              - provider: "conditional-level-of-authentication"
                requirement: "REQUIRED"
                config:
                  alias: "gold-condition"
                  loa-condition-level: "2"
              - provider: "auth-otp-form"
                requirement: "REQUIRED"
    authenticationBindings:
      browserFlow: "browser-step-up"
    clients:
      - clientId: "stepup-app"
        enabled: true
        acrLoaMap:
          gold: 2
        authenticationFlowBindingOverrides:
          browser: "browser-step-up"
`
	cfgPath := writeTestConfig(t, configYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := provisioner.New(kc, cfg).Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	flows, err := kc.GetAuthenticationFlows(ctx, "stepup-realm")
	if err != nil {
		t.Fatalf("getting flows: %v", err)
	}
	var flowID string
	for _, f := range flows {
		if f["alias"] == "browser-step-up" {
			flowID, _ = f["id"].(string)
		}
	}
	if flowID == "" {
		t.Fatal("flow browser-step-up was not created")
	}

	executions, err := kc.GetAuthenticationFlowExecutions(ctx, "stepup-realm", "browser-step-up")
	if err != nil {
		t.Fatalf("getting flow executions: %v", err)
	}

	// The copied browser flow keeps its own executions; ours are appended after
	// them, in the order declared.
	subflowIdx, conditionIdx, otpIdx := -1, -1, -1
	for i, e := range executions {
		switch e["displayName"] {
		case "loa-gold":
			subflowIdx = i
		}
		switch e["providerId"] {
		case "conditional-level-of-authentication":
			conditionIdx = i
			if e["requirement"] != "REQUIRED" {
				t.Errorf("condition requirement: expected REQUIRED, got %v", e["requirement"])
			}
			if e["authenticationConfig"] == nil {
				t.Error("conditional-level-of-authentication has no config attached")
			}
		case "auth-otp-form":
			otpIdx = i
		}
	}

	if subflowIdx < 0 {
		t.Fatalf("subflow loa-gold not found: %v", executions)
	}
	if conditionIdx < 0 || otpIdx < 0 {
		t.Fatalf("nested executions not found: %v", executions)
	}
	if !(subflowIdx < conditionIdx && conditionIdx < otpIdx) {
		t.Errorf("executions are out of declared order: subflow=%d condition=%d otp=%d", subflowIdx, conditionIdx, otpIdx)
	}
	if executions[subflowIdx]["requirement"] != "CONDITIONAL" {
		t.Errorf("subflow requirement: expected CONDITIONAL, got %v", executions[subflowIdx]["requirement"])
	}

	realm, err := kc.GetRealm(ctx, "stepup-realm")
	if err != nil {
		t.Fatalf("getting realm: %v", err)
	}
	if realm["browserFlow"] != "browser-step-up" {
		t.Errorf("browserFlow binding: expected browser-step-up, got %v", realm["browserFlow"])
	}
	attrs, _ := realm["attributes"].(map[string]any)
	if got := attrs["acr.loa.map"]; got != `{"gold":2,"silver":1}` {
		t.Errorf("unexpected realm acr.loa.map: %v", got)
	}

	clients, err := kc.GetClients(ctx, "stepup-realm", "stepup-app")
	if err != nil || len(clients) == 0 {
		t.Fatalf("getting client: %v", err)
	}
	overrides, ok := clients[0]["authenticationFlowBindingOverrides"].(map[string]any)
	if !ok {
		t.Fatalf("expected flow binding overrides, got %T", clients[0]["authenticationFlowBindingOverrides"])
	}
	// The client representation stores flow IDs here, not aliases.
	if overrides["browser"] != flowID {
		t.Errorf("expected browser override to be flow id %q, got %v", flowID, overrides["browser"])
	}
}

// TestIntegrationAuthenticationFlowNotReconciled pins the create-only contract:
// an existing flow is left exactly as it is, so a change made outside the config
// survives a re-run. If flow reconciliation is ever added, this test is the one
// that should be rewritten deliberately rather than quietly deleted.
func TestIntegrationAuthenticationFlowNotReconciled(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	configYAML := `
realms:
  - realm: "flow-stable-realm"
    enabled: true
    authenticationFlows:
      - alias: "custom-flow"
        executions:
          - provider: "auth-cookie"
            requirement: "ALTERNATIVE"
`
	cfgPath := writeTestConfig(t, configYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := provisioner.New(kc, cfg).Run(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// Change the requirement out-of-band.
	executions, err := kc.GetAuthenticationFlowExecutions(ctx, "flow-stable-realm", "custom-flow")
	if err != nil || len(executions) == 0 {
		t.Fatalf("getting executions: %v", err)
	}
	execution := executions[0]
	execution["requirement"] = "DISABLED"
	if err := kc.UpdateAuthenticationFlowExecution(ctx, "flow-stable-realm", "custom-flow", execution); err != nil {
		t.Fatalf("changing requirement out-of-band: %v", err)
	}

	if err := provisioner.New(kc, cfg).Run(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}

	executions, err = kc.GetAuthenticationFlowExecutions(ctx, "flow-stable-realm", "custom-flow")
	if err != nil {
		t.Fatalf("getting executions after second run: %v", err)
	}
	if len(executions) != 1 {
		t.Fatalf("expected the flow to still have exactly 1 execution, got %d: %v", len(executions), executions)
	}
	if executions[0]["requirement"] != "DISABLED" {
		t.Errorf("flows are create-only: the out-of-band requirement should survive, got %v", executions[0]["requirement"])
	}
}

func TestIntegrationAuthenticationFlowRejectsBuiltIn(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	configYAML := `
realms:
  - realm: "builtin-realm"
    enabled: true
    authenticationFlows:
      - alias: "browser"
        executions:
          - provider: "auth-cookie"
`
	cfgPath := writeTestConfig(t, configYAML)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	err = provisioner.New(kc, cfg).Run(context.Background())
	if err == nil {
		t.Fatal("expected provisioning to fail for a built-in flow")
	}
	if !strings.Contains(err.Error(), "copyFrom") {
		t.Errorf("error should point at copyFrom, got: %v", err)
	}
}

// TestIntegrationCompatibilityCheckPasses confirms the gate does not stand in
// the way of a config the server genuinely supports.
func TestIntegrationCompatibilityCheckPasses(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	configYAML := `
realms:
  - realm: "compat-ok-realm"
    enabled: true
    organizationsEnabled: true
    organizations:
      - name: "acme"
        domains:
          - name: "acme.com"
        groups:
          - name: "engineering"
    clients:
      - clientId: "exchange-app"
        standardTokenExchangeEnabled: true
`
	cfg, err := config.Load(writeTestConfig(t, configYAML))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := compat.Verify(ctx, kc, cfg); err != nil {
		t.Fatalf("config should be supported by %s: %v", keycloakImage, err)
	}

	info, err := compat.ReadServerInfo(ctx, kc)
	if err != nil {
		t.Fatalf("ReadServerInfo: %v", err)
	}
	if !info.Parsed {
		t.Errorf("the server version should be parseable, got %q", info.RawVersion)
	}
	if !info.Features["ORGANIZATION"] {
		t.Errorf("ORGANIZATION should be enabled by default, features: %v", info.Features)
	}
}

// TestIntegrationCompatibilityCheckRejectsOldServer runs against a release that
// predates organization groups and asserts the gate refuses the config —
// against a real server rather than a fabricated version string.
func TestIntegrationCompatibilityCheckRejectsOldServer(t *testing.T) {
	// 26.2 has organizations but not organization groups, which arrived in 26.6.
	kc, cleanup := setupKeycloakVersion(t, "keycloak/keycloak:26.2")
	defer cleanup()

	configYAML := `
realms:
  - realm: "compat-old-realm"
    enabled: true
    organizationsEnabled: true
    organizations:
      - name: "acme"
        domains:
          - name: "acme.com"
        groups:
          - name: "engineering"
`
	cfg, err := config.Load(writeTestConfig(t, configYAML))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	err = compat.Verify(ctx, kc, cfg)
	if err == nil {
		t.Fatal("expected organization groups to be refused on 26.2")
	}
	for _, want := range []string{"organization groups", "26.6", "organizations[0].groups"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}

	// Organizations themselves are supported on 26.2, so a config without
	// groups must still pass — the gate has to be precise, not blanket.
	withoutGroups := `
realms:
  - realm: "compat-old-realm"
    enabled: true
    organizationsEnabled: true
    organizations:
      - name: "acme"
        domains:
          - name: "acme.com"
`
	cfg, err = config.Load(writeTestConfig(t, withoutGroups))
	if err != nil {
		t.Fatal(err)
	}
	if err := compat.Verify(ctx, kc, cfg); err != nil {
		t.Errorf("organizations without groups should be supported on 26.2: %v", err)
	}
}

// TestIntegrationCapabilityCheckRejectsUnknownProvider asks the real server
// what it offers and confirms a config naming something it does not have is
// refused, with the config path and a suggestion.
func TestIntegrationCapabilityCheckRejectsUnknownProvider(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	configYAML := `
realms:
  - realm: "capability-realm"
    enabled: true
    authenticationFlows:
      - alias: "typo-flow"
        executions:
          - provider: "auth-cookei"
    clients:
      - clientId: "web"
        protocolMappers:
          - name: "aud"
            protocol: "openid-connect"
            protocolMapper: "oidc-audiance-mapper"
`
	cfg, err := config.Load(writeTestConfig(t, configYAML))
	if err != nil {
		t.Fatal(err)
	}

	err = compat.Verify(context.Background(), kc, cfg)
	if err == nil {
		t.Fatal("expected the unknown authenticator and mapper to be refused")
	}

	for _, want := range []string{
		"auth-cookei",
		`did you mean "auth-cookie"`,
		"authenticationFlows[0].executions[0].provider",
		"oidc-audiance-mapper",
		`did you mean "oidc-audience-mapper"`,
		"clients[0].protocolMappers[0].protocolMapper",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

// TestIntegrationCapabilitiesMatchServer confirms the capability lists are read
// correctly from a real server, so the check is not silently comparing against
// nothing.
func TestIntegrationCapabilitiesMatchServer(t *testing.T) {
	kc, cleanup := setupKeycloak(t)
	defer cleanup()

	ctx := context.Background()

	info, err := compat.ReadServerInfo(ctx, kc)
	if err != nil {
		t.Fatalf("ReadServerInfo: %v", err)
	}

	caps, err := compat.ReadCapabilities(ctx, kc, info)
	if err != nil {
		t.Fatalf("ReadCapabilities: %v", err)
	}

	// Providers from several of the four lists, to prove they are unioned.
	for _, want := range []string{
		"auth-cookie",                         // authenticator-providers
		"conditional-level-of-authentication", // present while step-up is enabled
		"registration-page-form",              // form-providers
		"client-secret",                       // client-authenticator-providers
	} {
		if !caps.Authenticators[want] {
			t.Errorf("expected provider %q to be reported by the server", want)
		}
	}

	if !caps.ProtocolMappers["openid-connect"]["oidc-audience-mapper"] {
		t.Errorf("expected the OIDC audience mapper, got %d protocols", len(caps.ProtocolMappers))
	}
	if !caps.ProtocolMappers["saml"]["saml-audience-mapper"] {
		t.Error("expected SAML mapper types to be reported too")
	}
}
