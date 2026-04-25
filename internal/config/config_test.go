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
