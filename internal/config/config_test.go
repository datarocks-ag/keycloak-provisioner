package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	return path
}

func TestLoadValidConfig(t *testing.T) {
	yaml := `
realms:
  - realm: "my-realm"
    displayName: "My Realm"
    enabled: true
    clients:
      - clientId: "my-app"
        secret: "mysecret"
        enabled: true
        publicClient: false
        protocol: "openid-connect"
        redirectUris:
          - "https://myapp.example.com/*"
        protocolMappers:
          - name: "audience-mapper"
            protocol: "openid-connect"
            protocolMapper: "oidc-audience-mapper"
            config:
              "included.client.audience": "my-app"
        clientRoles:
          - name: "admin"
            description: "Administrator"
    roles:
      - name: "app-admin"
        description: "Application administrator"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Realms) != 1 {
		t.Fatalf("expected 1 realm, got %d", len(cfg.Realms))
	}
	if cfg.Realms[0].Realm != "my-realm" {
		t.Errorf("expected realm name my-realm, got %s", cfg.Realms[0].Realm)
	}
	if cfg.Realms[0].Enabled == nil || !*cfg.Realms[0].Enabled {
		t.Error("expected enabled=true")
	}
	if len(cfg.Realms[0].Clients) != 1 {
		t.Fatalf("expected 1 client, got %d", len(cfg.Realms[0].Clients))
	}
	if cfg.Realms[0].Clients[0].ClientID != "my-app" {
		t.Errorf("expected client ID my-app, got %s", cfg.Realms[0].Clients[0].ClientID)
	}
	if len(cfg.Realms[0].Clients[0].ProtocolMappers) != 1 {
		t.Fatalf("expected 1 protocol mapper, got %d", len(cfg.Realms[0].Clients[0].ProtocolMappers))
	}
	if len(cfg.Realms[0].Clients[0].ClientRoles) != 1 {
		t.Fatalf("expected 1 client role, got %d", len(cfg.Realms[0].Clients[0].ClientRoles))
	}
	if len(cfg.Realms[0].Roles) != 1 {
		t.Fatalf("expected 1 realm role, got %d", len(cfg.Realms[0].Roles))
	}
}

func TestEnvVarExpansion(t *testing.T) {
	t.Setenv("TEST_KC_SECRET", "env_secret")
	t.Setenv("TEST_KC_REALM", "env-realm")

	yaml := `
realms:
  - realm: "${TEST_KC_REALM}"
    clients:
      - clientId: "app"
        secret: "${TEST_KC_SECRET}"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Realms[0].Realm != "env-realm" {
		t.Errorf("expected env-realm, got %s", cfg.Realms[0].Realm)
	}
	if cfg.Realms[0].Clients[0].Secret != "env_secret" {
		t.Errorf("expected env_secret, got %s", cfg.Realms[0].Clients[0].Secret)
	}
}

func TestUnsetEnvVarPreserved(t *testing.T) {
	os.Unsetenv("TOTALLY_UNSET_VAR")

	result := expandEnvVars("${TOTALLY_UNSET_VAR}")
	if result != "${TOTALLY_UNSET_VAR}" {
		t.Errorf("expected unresolved var to be preserved, got %s", result)
	}
}

func TestEmptyConfig(t *testing.T) {
	yaml := `{}`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error for empty config: %v", err)
	}
	if len(cfg.Realms) != 0 {
		t.Error("expected empty realms")
	}
}

func TestValidationMissingRealmName(t *testing.T) {
	yaml := `
realms:
  - displayName: "No Name"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing realm name")
	}
}

func TestValidationMasterRealm(t *testing.T) {
	yaml := `
realms:
  - realm: "master"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for master realm")
	}
}

func TestValidationDuplicateRealmName(t *testing.T) {
	yaml := `
realms:
  - realm: "dup"
  - realm: "dup"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for duplicate realm name")
	}
}

func TestValidationMissingClientID(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clients:
      - secret: "s"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing client ID")
	}
}

func TestValidationDuplicateClientID(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "dup"
      - clientId: "dup"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for duplicate client ID")
	}
}

func TestValidationMissingProtocolMapperName(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "app"
        protocolMappers:
          - protocolMapper: "oidc-audience-mapper"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing protocol mapper name")
	}
}

func TestValidationMissingProtocolMapperProtocol(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "app"
        protocolMappers:
          - name: "my-mapper"
            protocolMapper: "oidc-audience-mapper"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing protocol mapper protocol")
	}
}

func TestValidationMissingProtocolMapperType(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "app"
        protocolMappers:
          - name: "my-mapper"
            protocol: "openid-connect"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing protocolMapper type")
	}
}

func TestValidationDuplicateProtocolMapperName(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "app"
        protocolMappers:
          - name: "dup"
            protocol: "openid-connect"
            protocolMapper: "oidc-audience-mapper"
          - name: "dup"
            protocol: "openid-connect"
            protocolMapper: "oidc-usermodel-attribute-mapper"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for duplicate protocol mapper name")
	}
}

func TestValidationMissingRealmRoleName(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    roles:
      - description: "no name"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing realm role name")
	}
}

func TestValidationDuplicateRealmRoleName(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    roles:
      - name: "dup"
      - name: "dup"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for duplicate realm role name")
	}
}

func TestValidationMissingClientRoleName(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "app"
        clientRoles:
          - description: "no name"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing client role name")
	}
}

func TestValidationDuplicateClientRoleName(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "app"
        clientRoles:
          - name: "dup"
          - name: "dup"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for duplicate client role name")
	}
}

func TestValidationNullByteInRealmName(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\\x00evil\"\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in realm name")
	}
}

func TestValidationNullByteInClientID(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    clients:\n      - clientId: \"app\\x00evil\"\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in client ID")
	}
}

func TestValidationInvalidGlobalStrategy(t *testing.T) {
	yaml := `
strategy: "invalid"
realms:
  - realm: "test"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for invalid global strategy")
	}
}

func TestValidationInvalidRealmStrategy(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    strategy: "invalid"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for invalid realm strategy")
	}
}

func TestValidStrategies(t *testing.T) {
	for _, strategy := range []string{"create", "update"} {
		t.Run(strategy, func(t *testing.T) {
			yaml := `
strategy: "` + strategy + `"
realms:
  - realm: "test"
    strategy: "` + strategy + `"
`
			path := writeTempConfig(t, yaml)
			_, err := Load(path)
			if err != nil {
				t.Fatalf("unexpected error for strategy %q: %v", strategy, err)
			}
		})
	}
}

func TestEffectiveStrategy(t *testing.T) {
	tests := []struct {
		name       string
		strategies []string
		want       string
	}{
		{"all empty defaults to update", []string{"", ""}, "update"},
		{"first wins", []string{"create", "update"}, "create"},
		{"fallback to second", []string{"", "create"}, "create"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EffectiveStrategy(tt.strategies...)
			if got != tt.want {
				t.Errorf("EffectiveStrategy(%v) = %q, want %q", tt.strategies, got, tt.want)
			}
		})
	}
}

func TestValidationNullByteInProtocolMapperName(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    clients:\n      - clientId: \"app\"\n        protocolMappers:\n          - name: \"m\\x00evil\"\n            protocol: \"openid-connect\"\n            protocolMapper: \"oidc-audience-mapper\"\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in protocol mapper name")
	}
}

func TestLoadFileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	path := writeTempConfig(t, "{{{{invalid yaml")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestValidationNullByteInRealmRoleDescription(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    roles:\n      - name: \"admin\"\n        description: \"desc\\x00evil\"\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in realm role description")
	}
}

func TestValidationNullByteInClientRoleDescription(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    clients:\n      - clientId: \"app\"\n        clientRoles:\n          - name: \"admin\"\n            description: \"desc\\x00evil\"\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in client role description")
	}
}

func TestValidationNullByteInAttributes(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    clients:\n      - clientId: \"app\"\n        attributes:\n          key: \"val\\x00evil\"\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in attributes")
	}
}

func TestValidationNullByteInRedirectUris(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    clients:\n      - clientId: \"app\"\n        redirectUris:\n          - \"http://evil\\x00.com\"\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in redirectUris")
	}
}

func TestValidationNullByteInWebOrigins(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    clients:\n      - clientId: \"app\"\n        webOrigins:\n          - \"http://evil\\x00.com\"\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in webOrigins")
	}
}

func TestValidationNullByteInDefaultClientScopes(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    clients:\n      - clientId: \"app\"\n        defaultClientScopes:\n          - \"scope\\x00evil\"\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in defaultClientScopes")
	}
}

func TestValidationNullByteInOptionalClientScopes(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    clients:\n      - clientId: \"app\"\n        optionalClientScopes:\n          - \"scope\\x00evil\"\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in optionalClientScopes")
	}
}

func TestValidationNullByteInProtocolMapperProtocol(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    clients:\n      - clientId: \"app\"\n        protocolMappers:\n          - name: \"mapper\"\n            protocol: \"proto\\x00evil\"\n            protocolMapper: \"oidc-audience-mapper\"\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in protocol mapper protocol")
	}
}

func TestValidationNullByteInProtocolMapperType(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    clients:\n      - clientId: \"app\"\n        protocolMappers:\n          - name: \"mapper\"\n            protocol: \"openid-connect\"\n            protocolMapper: \"oidc\\x00evil\"\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in protocolMapper type")
	}
}

func TestValidationNullByteInProtocolMapperConfig(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    clients:\n      - clientId: \"app\"\n        protocolMappers:\n          - name: \"mapper\"\n            protocol: \"openid-connect\"\n            protocolMapper: \"oidc-audience-mapper\"\n            config:\n              key: \"val\\x00evil\"\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in protocol mapper config")
	}
}

func TestValidationNullByteInDisplayName(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    displayName: \"name\\x00evil\"\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in displayName")
	}
}

func TestValidationNullByteInLoginTheme(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    loginTheme: \"theme\\x00evil\"\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in loginTheme")
	}
}

func TestValidationNullByteInClientSecret(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    clients:\n      - clientId: \"app\"\n        secret: \"sec\\x00evil\"\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in client secret")
	}
}

func TestValidationSslRequired(t *testing.T) {
	for _, val := range []string{"external", "all", "none"} {
		t.Run("valid_"+val, func(t *testing.T) {
			yaml := `
realms:
  - realm: "test"
    sslRequired: "` + val + `"
`
			path := writeTempConfig(t, yaml)
			_, err := Load(path)
			if err != nil {
				t.Fatalf("unexpected error for sslRequired=%q: %v", val, err)
			}
		})
	}

	t.Run("invalid", func(t *testing.T) {
		yaml := `
realms:
  - realm: "test"
    sslRequired: "invalid"
`
		path := writeTempConfig(t, yaml)
		_, err := Load(path)
		if err == nil {
			t.Fatal("expected validation error for invalid sslRequired")
		}
	})
}

func TestSslRequiredNormalizedToLowercase(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"external", "external"},
		{"all", "all"},
		{"none", "none"},
		{"EXTERNAL", "external"},
		{"External", "external"},
		{"ALL", "all"},
		{"NONE", "none"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			yaml := `
realms:
  - realm: "test"
    sslRequired: "` + tt.input + `"
`
			path := writeTempConfig(t, yaml)
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Realms[0].SslRequired != tt.want {
				t.Errorf("expected sslRequired=%q, got %q", tt.want, cfg.Realms[0].SslRequired)
			}
		})
	}

	t.Run("masterRealm", func(t *testing.T) {
		yaml := `
masterRealm:
  sslRequired: External
`
		path := writeTempConfig(t, yaml)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.MasterRealm.SslRequired != "external" {
			t.Errorf("expected sslRequired=external, got %q", cfg.MasterRealm.SslRequired)
		}
	})
}

func TestMasterRealmConfig(t *testing.T) {
	t.Setenv("TEST_ADMIN_PW", "secret123")
	yaml := `
masterRealm:
  sslRequired: external
  users:
    - username: admin-new
      password: "${TEST_ADMIN_PW}"
      enabled: true
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MasterRealm == nil {
		t.Fatal("expected masterRealm to be set")
	}
	if cfg.MasterRealm.SslRequired != "external" {
		t.Errorf("expected sslRequired=external, got %s", cfg.MasterRealm.SslRequired)
	}
	if len(cfg.MasterRealm.Users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(cfg.MasterRealm.Users))
	}
	if cfg.MasterRealm.Users[0].Password != "secret123" {
		t.Errorf("expected password secret123, got %s", cfg.MasterRealm.Users[0].Password)
	}
}

func TestMasterRealmInvalidSslRequired(t *testing.T) {
	yaml := `
masterRealm:
  sslRequired: "invalid"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for invalid masterRealm sslRequired")
	}
}

func TestValidationUserMissingUsername(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    users:
      - password: "pw"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing username")
	}
}

func TestValidationDuplicateUsername(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    users:
      - username: "dup"
      - username: "dup"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for duplicate username")
	}
}

func TestValidationNullByteInUsername(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    users:\n      - username: \"user\\x00evil\"\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in username")
	}
}

func TestValidationServiceAccountRolesWithoutServiceAccountsEnabled(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "app"
        serviceAccountRoles:
          realm:
            - admin
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error when serviceAccountRoles set without serviceAccountsEnabled")
	}
}

func TestValidationServiceAccountRolesWithServiceAccountsEnabled(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "app"
        serviceAccountsEnabled: true
        serviceAccountRoles:
          realm:
            - admin
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidationStandardTokenExchangeRequiresConfidentialClient(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "app"
        publicClient: true
        standardTokenExchangeEnabled: true
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for token exchange on a public client")
	}
	if !strings.Contains(err.Error(), "standardTokenExchangeEnabled") {
		t.Errorf("expected error to mention standardTokenExchangeEnabled, got: %v", err)
	}
}

func TestValidationStandardTokenExchangeOnConfidentialClient(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "app"
        publicClient: false
        standardTokenExchangeEnabled: true
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidationStandardTokenExchangeRejectsBearerOnly(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "app"
        bearerOnly: true
        standardTokenExchangeEnabled: true
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for token exchange on a bearer-only client")
	}
	if !strings.Contains(err.Error(), "bearer-only") {
		t.Errorf("expected error to mention bearer-only, got: %v", err)
	}
}

func TestValidationStandardTokenExchangeDefaultsConfidential(t *testing.T) {
	// publicClient omitted defaults to confidential in Keycloak, so this is valid.
	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "app"
        standardTokenExchangeEnabled: true
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUserWithRoles(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    users:
      - username: "testuser"
        password: "pw"
        enabled: true
        email: "test@example.com"
        firstName: "Test"
        lastName: "User"
        emailVerified: true
        roles:
          realm:
            - admin
          clients:
            my-app:
              - editor
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	u := cfg.Realms[0].Users[0]
	if u.Username != "testuser" {
		t.Errorf("expected testuser, got %s", u.Username)
	}
	if u.Roles == nil {
		t.Fatal("expected roles to be set")
	}
	if len(u.Roles.Realm) != 1 || u.Roles.Realm[0] != "admin" {
		t.Errorf("expected realm role [admin], got %v", u.Roles.Realm)
	}
	if len(u.Roles.Clients["my-app"]) != 1 || u.Roles.Clients["my-app"][0] != "editor" {
		t.Errorf("expected client role [editor], got %v", u.Roles.Clients["my-app"])
	}
}

func TestEnvVarExpansionInSlicesAndMaps(t *testing.T) {
	t.Setenv("TEST_REDIRECT", "https://app.example.com/*")
	t.Setenv("TEST_ORIGIN", "https://app.example.com")
	t.Setenv("TEST_SCOPE", "openid")
	t.Setenv("TEST_ATTR_VAL", "attr_value")
	t.Setenv("TEST_PM_CONFIG", "my-audience")
	t.Setenv("TEST_CR_NAME", "admin-role")
	t.Setenv("TEST_CR_DESC", "Admin role desc")
	t.Setenv("TEST_RR_NAME", "realm-admin")
	t.Setenv("TEST_RR_DESC", "Realm admin desc")

	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "app"
        protocol: "openid-connect"
        redirectUris:
          - "${TEST_REDIRECT}"
        webOrigins:
          - "${TEST_ORIGIN}"
        defaultClientScopes:
          - "${TEST_SCOPE}"
        optionalClientScopes:
          - "${TEST_SCOPE}"
        attributes:
          key: "${TEST_ATTR_VAL}"
        protocolMappers:
          - name: "mapper"
            protocol: "openid-connect"
            protocolMapper: "oidc-audience-mapper"
            config:
              "included.client.audience": "${TEST_PM_CONFIG}"
        clientRoles:
          - name: "${TEST_CR_NAME}"
            description: "${TEST_CR_DESC}"
    roles:
      - name: "${TEST_RR_NAME}"
        description: "${TEST_RR_DESC}"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	c := cfg.Realms[0].Clients[0]
	if c.RedirectUris[0] != "https://app.example.com/*" {
		t.Errorf("redirectUris not expanded: %s", c.RedirectUris[0])
	}
	if c.WebOrigins[0] != "https://app.example.com" {
		t.Errorf("webOrigins not expanded: %s", c.WebOrigins[0])
	}
	if c.DefaultClientScopes[0] != "openid" {
		t.Errorf("defaultClientScopes not expanded: %s", c.DefaultClientScopes[0])
	}
	if c.OptionalClientScopes[0] != "openid" {
		t.Errorf("optionalClientScopes not expanded: %s", c.OptionalClientScopes[0])
	}
	if c.Attributes["key"] != "attr_value" {
		t.Errorf("attributes not expanded: %s", c.Attributes["key"])
	}
	if c.ProtocolMappers[0].Config["included.client.audience"] != "my-audience" {
		t.Errorf("pm config not expanded: %s", c.ProtocolMappers[0].Config["included.client.audience"])
	}
	if c.ClientRoles[0].Name != "admin-role" {
		t.Errorf("client role name not expanded: %s", c.ClientRoles[0].Name)
	}
	if c.ClientRoles[0].Description != "Admin role desc" {
		t.Errorf("client role description not expanded: %s", c.ClientRoles[0].Description)
	}

	r := cfg.Realms[0]
	if r.Roles[0].Name != "realm-admin" {
		t.Errorf("realm role name not expanded: %s", r.Roles[0].Name)
	}
	if r.Roles[0].Description != "Realm admin desc" {
		t.Errorf("realm role description not expanded: %s", r.Roles[0].Description)
	}
}

func TestLoadRejectsMultipleDocuments(t *testing.T) {
	yaml := `
realms:
  - realm: "first"
---
realms:
  - realm: "second"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for multi-document YAML")
	}
	if !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Errorf("expected multi-document error, got: %v", err)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	yaml := `
realms:
  - realm: "ok"
    badTypo: "should fail"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unknown YAML field")
	}
	if !strings.Contains(err.Error(), "badTypo") {
		t.Errorf("expected error to mention unknown field, got: %v", err)
	}
}

func TestExpandEnvVarsEscape(t *testing.T) {
	t.Setenv("EVE_CLIENT_ID", "real-client")

	yaml := `
realms:
  - realm: "r"
    clients:
      - clientId: "$${EVE_CLIENT_ID}"
        secret: "${EVE_CLIENT_ID}"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c := cfg.Realms[0].Clients[0]
	if c.ClientID != "${EVE_CLIENT_ID}" {
		t.Errorf("escape not honoured: got %q want %q", c.ClientID, "${EVE_CLIENT_ID}")
	}
	if c.Secret != "real-client" {
		t.Errorf("expansion broken: got %q want %q", c.Secret, "real-client")
	}
}

func TestExpandEnvVarsAttributeKeys(t *testing.T) {
	t.Setenv("ATTR_KEY", "post.logout.redirect.uris")
	t.Setenv("ATTR_VAL", "+")

	yaml := `
realms:
  - realm: "r"
    clients:
      - clientId: "app"
        attributes:
          "${ATTR_KEY}": "${ATTR_VAL}"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	attrs := cfg.Realms[0].Clients[0].Attributes
	if attrs["post.logout.redirect.uris"] != "+" {
		t.Errorf("attribute key not expanded: %v", attrs)
	}
}

func TestValidGroups(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    groups:
      - name: "engineering"
        attributes:
          department: ["eng"]
        realmRoles: ["developer"]
        clientRoles:
          my-app: ["admin"]
        subGroups:
          - name: "backend"
          - name: "frontend"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	g := cfg.Realms[0].Groups[0]
	if g.Name != "engineering" {
		t.Errorf("expected engineering, got %s", g.Name)
	}
	if len(g.SubGroups) != 2 {
		t.Fatalf("expected 2 subgroups, got %d", len(g.SubGroups))
	}
	if g.Attributes["department"][0] != "eng" {
		t.Errorf("unexpected attribute: %v", g.Attributes)
	}
}

func TestValidationMissingGroupName(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    groups:
      - attributes:
          department: ["eng"]
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing group name")
	}
}

func TestValidationDuplicateGroupName(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    groups:
      - name: "dup"
      - name: "dup"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for duplicate group name")
	}
}

func TestValidationDuplicateSubGroupName(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    groups:
      - name: "engineering"
        subGroups:
          - name: "dup"
          - name: "dup"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for duplicate subgroup name")
	}
}

func TestValidationMissingSubGroupName(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    groups:
      - name: "engineering"
        subGroups:
          - realmRoles: ["developer"]
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing subgroup name")
	}
}

func TestValidationNullByteInGroupName(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    groups:\n      - name: \"grp\x00evil\"\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in group name")
	}
}

func TestEnvVarExpansionInGroups(t *testing.T) {
	t.Setenv("TEST_GROUP_NAME", "engineering")
	t.Setenv("TEST_GROUP_ATTR", "eng")
	t.Setenv("TEST_GROUP_ROLE", "developer")
	t.Setenv("TEST_GROUP_CLIENT_ROLE", "admin")
	t.Setenv("TEST_SUBGROUP_NAME", "backend")

	yaml := `
realms:
  - realm: "test"
    groups:
      - name: "${TEST_GROUP_NAME}"
        attributes:
          department: ["${TEST_GROUP_ATTR}"]
        realmRoles: ["${TEST_GROUP_ROLE}"]
        clientRoles:
          my-app: ["${TEST_GROUP_CLIENT_ROLE}"]
        subGroups:
          - name: "${TEST_SUBGROUP_NAME}"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	g := cfg.Realms[0].Groups[0]
	if g.Name != "engineering" {
		t.Errorf("group name not expanded: %s", g.Name)
	}
	if g.Attributes["department"][0] != "eng" {
		t.Errorf("group attribute not expanded: %v", g.Attributes)
	}
	if g.RealmRoles[0] != "developer" {
		t.Errorf("group realm role not expanded: %s", g.RealmRoles[0])
	}
	if g.ClientRoles["my-app"][0] != "admin" {
		t.Errorf("group client role not expanded: %v", g.ClientRoles)
	}
	if g.SubGroups[0].Name != "backend" {
		t.Errorf("subgroup name not expanded: %s", g.SubGroups[0].Name)
	}
}

func TestEnvVarExpansionInGroupClientRoleKeys(t *testing.T) {
	t.Setenv("TEST_CLIENT_ID", "resolved-app")

	yaml := `
realms:
  - realm: "test"
    groups:
      - name: "engineering"
        clientRoles:
          "${TEST_CLIENT_ID}": ["admin"]
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	g := cfg.Realms[0].Groups[0]
	if roles, ok := g.ClientRoles["resolved-app"]; !ok || len(roles) != 1 || roles[0] != "admin" {
		t.Errorf("expected clientRoles re-keyed to resolved-app: got %v", g.ClientRoles)
	}
	if _, ok := g.ClientRoles["${TEST_CLIENT_ID}"]; ok {
		t.Error("expected original templated key to be removed")
	}
}

func TestEnvVarExpansionInGroupClientRoleKeyCollisionMerges(t *testing.T) {
	t.Setenv("TEST_CLIENT_ID", "app")

	yaml := `
realms:
  - realm: "test"
    groups:
      - name: "engineering"
        clientRoles:
          "${TEST_CLIENT_ID}": ["admin"]
          "app": ["user"]
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	roles := cfg.Realms[0].Groups[0].ClientRoles["app"]
	if len(roles) != 2 {
		t.Fatalf("expected merged roles for colliding key, got %v", roles)
	}
	has := map[string]bool{}
	for _, r := range roles {
		has[r] = true
	}
	if !has["admin"] || !has["user"] {
		t.Errorf("expected both admin and user after merge, got %v", roles)
	}
}

func TestValidationNullByteInGroupAttributeValue(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    groups:\n      - name: \"g\"\n        attributes:\n          dept: [\"e\x00vil\"]\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in group attribute value")
	}
}

func TestValidationNullByteInGroupRealmRole(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    groups:\n      - name: \"g\"\n        realmRoles: [\"dev\x00\"]\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in group realm role")
	}
}

func TestValidationNullByteInGroupClientRole(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    groups:\n      - name: \"g\"\n        clientRoles:\n          app: [\"adm\x00in\"]\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in group client role")
	}
}

func TestValidationEmptyGroupRealmRole(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    groups:
      - name: "g"
        realmRoles: [""]
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for empty group realm role")
	}
}

func TestValidationEmptyGroupClientRole(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    groups:
      - name: "g"
        clientRoles:
          app: [""]
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for empty group client role")
	}
}

func TestValidationEmptyGroupAttributeKey(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    groups:
      - name: "g"
        attributes:
          "": ["v"]
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for empty group attribute key")
	}
}

func TestUserWithGroups(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    users:
      - username: "alice"
        groups:
          - "engineering"
          - "/engineering/backend"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	groups := cfg.Realms[0].Users[0].Groups
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0] != "engineering" || groups[1] != "/engineering/backend" {
		t.Errorf("unexpected groups: %v", groups)
	}
}

func TestEnvVarExpansionInUserGroups(t *testing.T) {
	t.Setenv("TEAM_GROUP", "engineering")
	yaml := `
realms:
  - realm: "test"
    users:
      - username: "alice"
        groups:
          - "/${TEAM_GROUP}/backend"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := cfg.Realms[0].Users[0].Groups[0]; got != "/engineering/backend" {
		t.Errorf("expected /engineering/backend, got %q", got)
	}
}

func TestValidationEmptyUserGroup(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    users:
      - username: "alice"
        groups:
          - "/"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for empty group path")
	}
}

func TestValidationNullByteInUserGroup(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    users:\n      - username: \"alice\"\n        groups:\n          - \"eng\\x00evil\"\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in group path")
	}
}

func TestRealmAttributes(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    attributes:
      frontendUrl: "https://id.example.com"
      userProfileEnabled: "true"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	attrs := cfg.Realms[0].Attributes
	if len(attrs) != 2 {
		t.Fatalf("expected 2 attributes, got %d", len(attrs))
	}
	if got := attrs["frontendUrl"]; got != "https://id.example.com" {
		t.Errorf("expected https://id.example.com, got %q", got)
	}
}

func TestEnvVarExpansionInRealmAttributes(t *testing.T) {
	t.Setenv("ATTR_NAME", "frontendUrl")
	t.Setenv("PUBLIC_URL", "https://id.example.com")
	yaml := `
realms:
  - realm: "test"
    attributes:
      ${ATTR_NAME}: "${PUBLIC_URL}"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := cfg.Realms[0].Attributes["frontendUrl"]; got != "https://id.example.com" {
		t.Errorf("expected expanded key and value, got %q", got)
	}
}

func TestValidationNullByteInRealmAttributes(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    attributes:\n      key: \"val\\x00evil\"\n"
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for null byte in realm attribute")
	}
}

func TestValidationEmptyRealmAttributeName(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    attributes:
      "": "value"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for empty realm attribute name")
	}
}

func TestValidationEmptyClientAttributeName(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "web"
        attributes:
          "": "value"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for empty client attribute name")
	}
}

func TestRealmAcrLoaMap(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    acrLoaMap:
      silver: 1
      gold: 2
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	m := cfg.Realms[0].AcrLoaMap
	if m["silver"] != 1 || m["gold"] != 2 {
		t.Errorf("unexpected acrLoaMap: %v", m)
	}
}

func TestClientAcrLoaMap(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "web"
        acrLoaMap:
          gold: 2
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := cfg.Realms[0].Clients[0].AcrLoaMap["gold"]; got != 2 {
		t.Errorf("expected gold=2, got %d", got)
	}
}

func TestValidationNegativeAcrLoaLevel(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    acrLoaMap:
      gold: -1
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for negative LoA level")
	}
}

func TestValidationNullByteInAcrValue(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    acrLoaMap:\n      \"go\\x00ld\": 2\n"
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for null byte in acr value")
	}
}

func TestValidationEmptyAcrValue(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    acrLoaMap:
      "": 1
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for empty acr value")
	}
}

func TestRealmOrganizationsEnabled(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    organizationsEnabled: true
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	enabled := cfg.Realms[0].OrganizationsEnabled
	if enabled == nil || !*enabled {
		t.Errorf("expected organizationsEnabled=true, got %v", enabled)
	}
}

func TestRealmOrganizationsEnabledUnsetStaysNil(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Realms[0].OrganizationsEnabled != nil {
		t.Error("expected organizationsEnabled to stay nil when unset")
	}
}

func TestOrganizations(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    organizationsEnabled: true
    organizations:
      - name: "acme"
        alias: "acme-corp"
        enabled: true
        description: "ACME Corp"
        redirectUrl: "https://acme.example.com"
        domains:
          - name: "acme.com"
            verified: true
          - name: "acme.org"
        attributes:
          tier:
            - "gold"
        members:
          - "alice"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	orgs := cfg.Realms[0].Organizations
	if len(orgs) != 1 {
		t.Fatalf("expected 1 organization, got %d", len(orgs))
	}
	o := orgs[0]
	if o.Name != "acme" || o.Alias != "acme-corp" {
		t.Errorf("unexpected name/alias: %q/%q", o.Name, o.Alias)
	}
	if len(o.Domains) != 2 || o.Domains[0].Name != "acme.com" {
		t.Errorf("unexpected domains: %v", o.Domains)
	}
	if o.Domains[0].Verified == nil || !*o.Domains[0].Verified {
		t.Error("expected acme.com to be verified")
	}
	if o.Domains[1].Verified != nil {
		t.Error("expected acme.org verified to stay nil")
	}
	if len(o.Members) != 1 || o.Members[0] != "alice" {
		t.Errorf("unexpected members: %v", o.Members)
	}
}

func TestValidationOrganizationsRequireOrganizationsEnabled(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    organizations:
      - name: "acme"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error when organizationsEnabled is unset")
	}
	if !strings.Contains(err.Error(), "organizationsEnabled") {
		t.Errorf("error should mention organizationsEnabled, got: %v", err)
	}
}

func TestValidationOrganizationsRejectedWhenExplicitlyDisabled(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    organizationsEnabled: false
    organizations:
      - name: "acme"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error when organizationsEnabled is false")
	}
}

func TestValidationMissingOrganizationName(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    organizationsEnabled: true
    organizations:
      - description: "no name"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for missing organization name")
	}
}

func TestValidationDuplicateOrganizationName(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    organizationsEnabled: true
    organizations:
      - name: "dup"
      - name: "dup"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for duplicate organization name")
	}
}

func TestValidationDuplicateOrganizationDomain(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    organizationsEnabled: true
    organizations:
      - name: "acme"
        domains:
          - name: "acme.com"
          - name: "acme.com"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for duplicate domain")
	}
}

func TestValidationEmptyOrganizationMember(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    organizationsEnabled: true
    organizations:
      - name: "acme"
        members:
          - ""
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for empty member username")
	}
}

func TestEnvVarExpansionInOrganizations(t *testing.T) {
	t.Setenv("ORG_NAME", "acme")
	t.Setenv("ORG_DOMAIN", "acme.com")
	t.Setenv("ORG_MEMBER", "alice")
	yaml := `
realms:
  - realm: "test"
    organizationsEnabled: true
    organizations:
      - name: "${ORG_NAME}"
        domains:
          - name: "${ORG_DOMAIN}"
        members:
          - "${ORG_MEMBER}"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	o := cfg.Realms[0].Organizations[0]
	if o.Name != "acme" || o.Domains[0].Name != "acme.com" || o.Members[0] != "alice" {
		t.Errorf("expansion failed: %+v", o)
	}
}

func TestOrganizationGroups(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    organizationsEnabled: true
    organizations:
      - name: "acme"
        members:
          - "alice"
        groups:
          - name: "engineering"
            attributes:
              tier:
                - "gold"
            members:
              - "alice"
            subGroups:
              - name: "backend"
                members:
                  - "alice"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	groups := cfg.Realms[0].Organizations[0].Groups
	if len(groups) != 1 || groups[0].Name != "engineering" {
		t.Fatalf("unexpected groups: %+v", groups)
	}
	if got := groups[0].Attributes["tier"]; len(got) != 1 || got[0] != "gold" {
		t.Errorf("unexpected attributes: %v", groups[0].Attributes)
	}
	if len(groups[0].Members) != 1 || groups[0].Members[0] != "alice" {
		t.Errorf("unexpected members: %v", groups[0].Members)
	}
	if len(groups[0].SubGroups) != 1 || groups[0].SubGroups[0].Name != "backend" {
		t.Errorf("unexpected subgroups: %+v", groups[0].SubGroups)
	}
}

func TestValidationMissingOrganizationGroupName(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    organizationsEnabled: true
    organizations:
      - name: "acme"
        groups:
          - members: ["alice"]
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for missing organization group name")
	}
}

func TestValidationDuplicateOrganizationGroupName(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    organizationsEnabled: true
    organizations:
      - name: "acme"
        groups:
          - name: "dup"
          - name: "dup"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for duplicate organization group name")
	}
}

func TestOrganizationSubGroupNameMayRepeatAcrossLevels(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    organizationsEnabled: true
    organizations:
      - name: "acme"
        groups:
          - name: "team"
            subGroups:
              - name: "team"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err != nil {
		t.Fatalf("names may repeat at different levels: %v", err)
	}
}

func TestValidationDuplicateOrganizationSubGroupName(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    organizationsEnabled: true
    organizations:
      - name: "acme"
        groups:
          - name: "team"
            subGroups:
              - name: "dup"
              - name: "dup"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for duplicate sibling subgroup name")
	}
}

func TestValidationEmptyOrganizationGroupMember(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    organizationsEnabled: true
    organizations:
      - name: "acme"
        groups:
          - name: "engineering"
            members:
              - ""
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for empty group member username")
	}
}

func TestEnvVarExpansionInOrganizationGroups(t *testing.T) {
	t.Setenv("OG_NAME", "engineering")
	t.Setenv("OG_TIER", "gold")
	t.Setenv("OG_MEMBER", "alice")
	yaml := `
realms:
  - realm: "test"
    organizationsEnabled: true
    organizations:
      - name: "acme"
        groups:
          - name: "${OG_NAME}"
            attributes:
              tier:
                - "${OG_TIER}"
            members:
              - "${OG_MEMBER}"
            subGroups:
              - name: "${OG_NAME}-sub"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	g := cfg.Realms[0].Organizations[0].Groups[0]
	if g.Name != "engineering" || g.Attributes["tier"][0] != "gold" || g.Members[0] != "alice" {
		t.Errorf("expansion failed: %+v", g)
	}
	if g.SubGroups[0].Name != "engineering-sub" {
		t.Errorf("subgroup expansion failed: %q", g.SubGroups[0].Name)
	}
}

func TestAuthenticationFlows(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    authenticationFlows:
      - alias: "browser-step-up"
        description: "Step-up browser flow"
        copyFrom: "browser"
        executions:
          - provider: "auth-cookie"
            requirement: "ALTERNATIVE"
          - subflow: "loa-gold"
            requirement: "CONDITIONAL"
            providerId: "basic-flow"
            executions:
              - provider: "conditional-level-of-authentication"
                requirement: "REQUIRED"
                config:
                  alias: "gold-condition"
                  loa-condition-level: "2"
    authenticationBindings:
      browserFlow: "browser-step-up"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	flows := cfg.Realms[0].AuthenticationFlows
	if len(flows) != 1 || flows[0].Alias != "browser-step-up" || flows[0].CopyFrom != "browser" {
		t.Fatalf("unexpected flows: %+v", flows)
	}
	if len(flows[0].Executions) != 2 {
		t.Fatalf("expected 2 executions, got %d", len(flows[0].Executions))
	}

	sub := flows[0].Executions[1]
	if sub.Subflow != "loa-gold" || sub.Requirement != "CONDITIONAL" {
		t.Errorf("unexpected subflow: %+v", sub)
	}
	if len(sub.Executions) != 1 || sub.Executions[0].Config["loa-condition-level"] != "2" {
		t.Errorf("unexpected nested execution: %+v", sub.Executions)
	}

	if cfg.Realms[0].AuthenticationBindings == nil ||
		cfg.Realms[0].AuthenticationBindings.BrowserFlow != "browser-step-up" {
		t.Errorf("unexpected bindings: %+v", cfg.Realms[0].AuthenticationBindings)
	}
}

func TestValidationMissingFlowAlias(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    authenticationFlows:
      - description: "no alias"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for missing flow alias")
	}
}

func TestValidationDuplicateFlowAlias(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    authenticationFlows:
      - alias: "dup"
      - alias: "dup"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for duplicate flow alias")
	}
}

func TestValidationSubflowAliasCollidesWithFlow(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    authenticationFlows:
      - alias: "top"
        executions:
          - subflow: "top"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for subflow alias colliding with a flow alias")
	}
}

func TestValidationExecutionRequiresProviderOrSubflow(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    authenticationFlows:
      - alias: "f"
        executions:
          - requirement: "REQUIRED"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for execution without provider or subflow")
	}
}

func TestValidationExecutionProviderAndSubflowMutuallyExclusive(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    authenticationFlows:
      - alias: "f"
        executions:
          - provider: "auth-cookie"
            subflow: "nested"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for provider and subflow together")
	}
}

func TestValidationInvalidExecutionRequirement(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    authenticationFlows:
      - alias: "f"
        executions:
          - provider: "auth-cookie"
            requirement: "MAYBE"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for invalid requirement")
	}
}

func TestValidationInvalidFlowProviderId(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    authenticationFlows:
      - alias: "f"
        providerId: "weird-flow"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for invalid providerId")
	}
}

func TestValidationProviderExecutionCannotNest(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    authenticationFlows:
      - alias: "f"
        executions:
          - provider: "auth-cookie"
            executions:
              - provider: "auth-otp-form"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for nested executions under a provider")
	}
}

func TestEnvVarExpansionInAuthenticationFlows(t *testing.T) {
	t.Setenv("FLOW_ALIAS", "browser-step-up")
	t.Setenv("LOA_LEVEL", "3")
	yaml := `
realms:
  - realm: "test"
    authenticationFlows:
      - alias: "${FLOW_ALIAS}"
        executions:
          - provider: "conditional-level-of-authentication"
            config:
              loa-condition-level: "${LOA_LEVEL}"
    authenticationBindings:
      browserFlow: "${FLOW_ALIAS}"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	f := cfg.Realms[0].AuthenticationFlows[0]
	if f.Alias != "browser-step-up" {
		t.Errorf("expected expanded alias, got %q", f.Alias)
	}
	if got := f.Executions[0].Config["loa-condition-level"]; got != "3" {
		t.Errorf("expected expanded config value, got %q", got)
	}
	if got := cfg.Realms[0].AuthenticationBindings.BrowserFlow; got != "browser-step-up" {
		t.Errorf("expected expanded binding, got %q", got)
	}
}

func TestClientAuthenticationFlowBindingOverrides(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "web"
        authenticationFlowBindingOverrides:
          browser: "browser-step-up"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := cfg.Realms[0].Clients[0].AuthenticationFlowBindingOverrides["browser"]
	if got != "browser-step-up" {
		t.Errorf("expected browser-step-up, got %q", got)
	}
}

func TestValidationMissingClientScopeName(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clientScopes:
      - description: "no name"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for missing client scope name")
	}
}

func TestValidationDuplicateClientScopeName(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clientScopes:
      - name: "dup"
      - name: "dup"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for duplicate client scope name")
	}
}

func TestValidationInvalidClientScopeType(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clientScopes:
      - name: "orders:read"
        type: "mandatory"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for invalid client scope type")
	}
}

func TestValidationClientScopeProtocolMapperReused(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clientScopes:
      - name: "orders:read"
        protocolMappers:
          - name: "no-protocol"
            protocolMapper: "oidc-audience-mapper"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected client scope protocol mappers to reuse mapper validation")
	}
}

func TestValidationNullByteInClientScopeAttributes(t *testing.T) {
	yaml := "realms:\n  - realm: \"test\"\n    clientScopes:\n      - name: \"s\"\n        attributes:\n          k: \"v\\x00evil\"\n"
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for null byte in client scope attribute")
	}
}

func TestClientScopeTypeNoneAccepted(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clientScopes:
      - name: "orders:read"
        type: "none"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err != nil {
		t.Fatalf("type none should be valid: %v", err)
	}
}

func TestValidationInvalidFlowBindingOverrideKey(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "web"
        authenticationFlowBindingOverrides:
          directGrant: "my-flow"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for an invalid binding override key")
	}
	if !strings.Contains(err.Error(), "direct_grant") {
		t.Errorf("error should name the valid keys, got: %v", err)
	}
}

func TestValidationEmptyFlowBindingOverrideAlias(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "web"
        authenticationFlowBindingOverrides:
          browser: ""
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for an empty flow alias")
	}
}

func TestValidFlowBindingOverrideKeys(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "web"
        authenticationFlowBindingOverrides:
          browser: "browser-step-up"
          direct_grant: "direct-grant-flow"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err != nil {
		t.Fatalf("browser and direct_grant must both be accepted: %v", err)
	}
}

func TestUserWithFixedID(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    users:
      - username: "alice"
        id: "11111111-2222-3333-4444-555555555555"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := cfg.Realms[0].Users[0].ID; got != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("unexpected id: %q", got)
	}
}

func TestValidationUserIDMustBeUUID(t *testing.T) {
	for _, id := range []string{"not-a-uuid", "1111", "11111111-2222-3333-4444-5555555555", "zzzzzzzz-2222-3333-4444-555555555555"} {
		yaml := `
realms:
  - realm: "test"
    users:
      - username: "alice"
        id: "` + id + `"
`
		path := writeTempConfig(t, yaml)
		if _, err := Load(path); err == nil {
			t.Errorf("expected %q to be rejected as a user id", id)
		}
	}
}

func TestUserIDAcceptsUppercaseUUID(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    users:
      - username: "alice"
        id: "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err != nil {
		t.Fatalf("an uppercase UUID should be accepted: %v", err)
	}
}

func TestUserIDOptional(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    users:
      - username: "alice"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Realms[0].Users[0].ID != "" {
		t.Error("id should default to empty")
	}
}

func TestEnvVarExpansionInUserID(t *testing.T) {
	t.Setenv("ALICE_ID", "11111111-2222-3333-4444-555555555555")
	yaml := `
realms:
  - realm: "test"
    users:
      - username: "alice"
        id: "${ALICE_ID}"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Realms[0].Users[0].ID; got != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("expected the expanded id, got %q", got)
	}
}

func TestEnvVarExpansionInUserClientRoleKeys(t *testing.T) {
	t.Setenv("TEST_CLIENT_ID", "resolved-app")

	yaml := `
realms:
  - realm: "test"
    users:
      - username: "alice"
        roles:
          clients:
            "${TEST_CLIENT_ID}": ["admin"]
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	clients := cfg.Realms[0].Users[0].Roles.Clients
	if roles, ok := clients["resolved-app"]; !ok || len(roles) != 1 || roles[0] != "admin" {
		t.Errorf("expected user roles.clients re-keyed to resolved-app: got %v", clients)
	}
	if _, ok := clients["${TEST_CLIENT_ID}"]; ok {
		t.Error("expected original templated key to be removed")
	}
}

func TestEnvVarExpansionInServiceAccountClientRoleKeys(t *testing.T) {
	t.Setenv("TEST_CLIENT_ID", "resolved-app")

	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "worker"
        serviceAccountsEnabled: true
        serviceAccountRoles:
          clients:
            "${TEST_CLIENT_ID}": ["admin"]
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	clients := cfg.Realms[0].Clients[0].ServiceAccountRoles.Clients
	if roles, ok := clients["resolved-app"]; !ok || len(roles) != 1 || roles[0] != "admin" {
		t.Errorf("expected serviceAccountRoles.clients re-keyed to resolved-app: got %v", clients)
	}
	if _, ok := clients["${TEST_CLIENT_ID}"]; ok {
		t.Error("expected original templated key to be removed")
	}
}

func TestEnvVarExpansionInGroupAttributeKeys(t *testing.T) {
	t.Setenv("TEST_ATTR_KEY", "resolved.attr")

	yaml := `
realms:
  - realm: "test"
    groups:
      - name: "engineering"
        attributes:
          "${TEST_ATTR_KEY}": ["value"]
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	attrs := cfg.Realms[0].Groups[0].Attributes
	if values, ok := attrs["resolved.attr"]; !ok || len(values) != 1 || values[0] != "value" {
		t.Errorf("expected group attributes re-keyed to resolved.attr: got %v", attrs)
	}
	if _, ok := attrs["${TEST_ATTR_KEY}"]; ok {
		t.Error("expected original templated key to be removed")
	}
}

func TestEnvVarExpansionInOrganizationAttributeKeyCollisionMerges(t *testing.T) {
	t.Setenv("TEST_ATTR_KEY", "tier")

	yaml := `
realms:
  - realm: "test"
    organizationsEnabled: true
    organizations:
      - name: "acme"
        domains:
          - name: "acme.test"
        attributes:
          "${TEST_ATTR_KEY}": ["gold"]
          "tier": ["silver"]
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	attrs := cfg.Realms[0].Organizations[0].Attributes
	if len(attrs["tier"]) != 2 {
		t.Errorf("expected colliding organization attribute keys to merge into 2 values, got %v", attrs)
	}
}

func TestValidationOrganizationGroupRejectsRealmRoles(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    organizationsEnabled: true
    organizations:
      - name: "acme"
        domains:
          - name: "acme.test"
        groups:
          - name: "engineers"
            realmRoles:
              - "org-inherited-role"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected organization group realmRoles to be rejected")
	}
	if !strings.Contains(err.Error(), "cannot carry role mappings") {
		t.Errorf("error should explain the trap, got: %v", err)
	}
	if !strings.Contains(err.Error(), "token claim") {
		t.Errorf("error should say the mapping never reaches a token claim, got: %v", err)
	}
}

func TestValidationOrganizationGroupRejectsClientRoles(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    organizationsEnabled: true
    organizations:
      - name: "acme"
        domains:
          - name: "acme.test"
        groups:
          - name: "engineers"
            subGroups:
              - name: "backend"
                clientRoles:
                  "app": ["admin"]
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected organization subgroup clientRoles to be rejected")
	}
	if !strings.Contains(err.Error(), "cannot carry role mappings") {
		t.Errorf("error should explain the trap, got: %v", err)
	}
	if !strings.Contains(err.Error(), "subGroups[0]") {
		t.Errorf("error should name the offending subgroup, got: %v", err)
	}
}

// TestValidationOrganizationGroupRejectsEmptyRoleFields covers presence rather
// than content: someone writing "realmRoles: []" is reaching for the feature,
// so the explanation is due then, not once they add the first entry.
func TestValidationOrganizationGroupRejectsEmptyRoleFields(t *testing.T) {
	for _, field := range []string{"realmRoles: []", "clientRoles: {}"} {
		t.Run(field, func(t *testing.T) {
			yaml := `
realms:
  - realm: "test"
    organizationsEnabled: true
    organizations:
      - name: "acme"
        domains:
          - name: "acme.test"
        groups:
          - name: "engineers"
            ` + field + "\n"

			path := writeTempConfig(t, yaml)
			_, err := Load(path)
			if err == nil {
				t.Fatalf("expected %q to be rejected", field)
			}
			if !strings.Contains(err.Error(), "cannot carry role mappings") {
				t.Errorf("error should explain the trap, got: %v", err)
			}
		})
	}
}

// TestValidationOrganizationGroupAcceptsAbsentRoleFields is the control: a
// group that never mentions the fields must still load.
func TestValidationOrganizationGroupAcceptsAbsentRoleFields(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    organizationsEnabled: true
    organizations:
      - name: "acme"
        domains:
          - name: "acme.test"
        groups:
          - name: "engineers"
            attributes:
              tier:
                - "gold"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err != nil {
		t.Fatalf("a group without role fields must load: %v", err)
	}
}

func TestClientFullScopeAllowedParsing(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "scoped"
        fullScopeAllowed: false
      - clientId: "wide"
        fullScopeAllowed: true
      - clientId: "unset"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	clients := cfg.Realms[0].Clients
	if clients[0].FullScopeAllowed == nil || *clients[0].FullScopeAllowed {
		t.Errorf("expected scoped client to have fullScopeAllowed=false, got %v", clients[0].FullScopeAllowed)
	}
	if clients[1].FullScopeAllowed == nil || !*clients[1].FullScopeAllowed {
		t.Errorf("expected wide client to have fullScopeAllowed=true, got %v", clients[1].FullScopeAllowed)
	}
	// Unset must stay nil so the provisioner omits the key and Keycloak's own
	// default applies, rather than the provisioner asserting one.
	if clients[2].FullScopeAllowed != nil {
		t.Errorf("expected unset fullScopeAllowed to stay nil, got %v", *clients[2].FullScopeAllowed)
	}
}

func TestClientDefaultAcrValuesParsing(t *testing.T) {
	t.Setenv("TEST_ACR", "gold")

	yaml := `
realms:
  - realm: "test"
    acrLoaMap:
      gold: 2
      silver: 1
    clients:
      - clientId: "app"
        defaultAcrValues:
          - "${TEST_ACR}"
          - "silver"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := cfg.Realms[0].Clients[0].DefaultAcrValues
	if len(got) != 2 || got[0] != "gold" || got[1] != "silver" {
		t.Errorf("unexpected defaultAcrValues: %v", got)
	}
}

func TestClientDefaultAcrValuesValidation(t *testing.T) {
	tests := []struct {
		name  string
		realm string
		want  string
	}{
		{
			"value not in the declared map",
			`    acrLoaMap:
      gold: 2
    clients:
      - clientId: "app"
        defaultAcrValues:
          - "standard"`,
			`"standard" is not in the acrLoaMap; the config declares gold`,
		},
		{
			"separator inside a value",
			`    acrLoaMap:
      gold: 2
    clients:
      - clientId: "app"
        defaultAcrValues:
          - "go##ld"`,
			`must not contain "##"`,
		},
		{
			"duplicate value",
			`    acrLoaMap:
      gold: 2
    clients:
      - clientId: "app"
        defaultAcrValues:
          - "gold"
          - "gold"`,
			"duplicate ACR value",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			yaml := "realms:\n  - realm: \"test\"\n" + tc.realm + "\n"

			path := writeTempConfig(t, yaml)
			if _, err := Load(path); err == nil {
				t.Fatalf("expected an error mentioning %q", tc.want)
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected %q, got: %v", tc.want, err)
			}
		})
	}
}

// TestClientDefaultAcrValuesAcceptsClientMapAndUndeclared covers the two cases
// that must not fail: the value comes from the client's own map, or no map is
// declared anywhere and the server may already have one.
func TestClientDefaultAcrValuesAcceptsClientMapAndUndeclared(t *testing.T) {
	for name, realm := range map[string]string{
		"client's own map": `    clients:
      - clientId: "app"
        acrLoaMap:
          gold: 2
        defaultAcrValues:
          - "gold"`,
		"no map declared anywhere": `    clients:
      - clientId: "app"
        defaultAcrValues:
          - "set-on-the-server"`,
	} {
		t.Run(name, func(t *testing.T) {
			path := writeTempConfig(t, "realms:\n  - realm: \"test\"\n"+realm+"\n")
			if _, err := Load(path); err != nil {
				t.Errorf("must be accepted: %v", err)
			}
		})
	}
}
