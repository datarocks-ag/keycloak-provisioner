package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
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

func TestValidationMissingProtocolMapperType(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    clients:
      - clientId: "app"
        protocolMappers:
          - name: "my-mapper"
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
            protocolMapper: "oidc-audience-mapper"
          - name: "dup"
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
	yaml := "realms:\n  - realm: \"test\"\n    clients:\n      - clientId: \"app\"\n        protocolMappers:\n          - name: \"m\\x00evil\"\n            protocolMapper: \"oidc-audience-mapper\"\n"
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for null byte in protocol mapper name")
	}
}
