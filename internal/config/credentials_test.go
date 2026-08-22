package config

import (
	"strings"
	"testing"
)

func TestUserCredentialsAndRequiredActionsParsing(t *testing.T) {
	t.Setenv("TEST_TOTP_SECRET", "SEEDEDSECRET")

	yaml := `
realms:
  - realm: "test"
    users:
      - username: "bob"
        requiredActions:
          - "CONFIGURE_TOTP"
        credentials:
          - type: "otp"
            label: "seeded"
            secret: "${TEST_TOTP_SECRET}"
      - username: "carol"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	bob := cfg.Realms[0].Users[0]
	if len(bob.RequiredActions) != 1 || bob.RequiredActions[0] != "CONFIGURE_TOTP" {
		t.Errorf("unexpected requiredActions: %v", bob.RequiredActions)
	}
	if len(bob.Credentials) != 1 || bob.Credentials[0].Secret != "SEEDEDSECRET" {
		t.Errorf("secret not expanded: %v", bob.Credentials)
	}

	// Nil rather than empty: the reconciler sends the field only when the
	// config declares it, since Keycloak replaces the list with whatever the
	// body carries.
	if cfg.Realms[0].Users[1].RequiredActions != nil {
		t.Error("an undeclared requiredActions must stay nil so the update omits it")
	}
}

func TestUserCredentialsValidation(t *testing.T) {
	tests := []struct {
		name  string
		block string
		want  string
	}{
		{
			"type required",
			`- secret: "s"`,
			"type: is required",
		},
		{
			"unsupported type",
			`- type: "password"
            secret: "s"`,
			"is not supported",
		},
		{
			"secret required",
			`- type: "otp"`,
			"secret: is required",
		},
		{
			"unknown algorithm",
			`- type: "otp"
            secret: "s"
            algorithm: "HmacMD5"`,
			"HmacSHA1, HmacSHA256, HmacSHA512",
		},
		{
			"negative digits",
			`- type: "otp"
            secret: "s"
            digits: -1`,
			"digits: must not be negative",
		},
		{
			"duplicate type and label",
			`- type: "otp"
            secret: "a"
          - type: "otp"
            secret: "b"`,
			"duplicate credential",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			yaml := "realms:\n  - realm: \"test\"\n    users:\n      - username: \"bob\"\n        credentials:\n          " + tc.block + "\n"

			path := writeTempConfig(t, yaml)
			if _, err := Load(path); err == nil {
				t.Fatalf("expected an error mentioning %q", tc.want)
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected %q, got: %v", tc.want, err)
			}
		})
	}
}

// TestUserCredentialsDistinctLabelsAllowed is the control: Keycloak permits two
// OTP credentials on one user, so the duplicate rule keys on type *and* label.
func TestUserCredentialsDistinctLabelsAllowed(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    users:
      - username: "bob"
        credentials:
          - type: "otp"
            label: "phone"
            secret: "a"
          - type: "otp"
            label: "tablet"
            secret: "b"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err != nil {
		t.Fatalf("two labelled credentials of one type must be allowed: %v", err)
	}
}

func TestRequiredActionsValidation(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    users:
      - username: "bob"
        requiredActions:
          - "CONFIGURE_TOTP"
          - "CONFIGURE_TOTP"
`
	path := writeTempConfig(t, yaml)
	if _, err := Load(path); err == nil {
		t.Fatal("expected duplicate required actions to be rejected")
	} else if !strings.Contains(err.Error(), "duplicate required action") {
		t.Errorf("unexpected error: %v", err)
	}
}
