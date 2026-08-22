package config

import (
	"strings"
	"testing"
)

func TestIdentityProviderParsing(t *testing.T) {
	t.Setenv("TEST_IDP_SECRET", "s3cr3t")
	t.Setenv("TEST_IDP_ALIAS", "corporate")

	yaml := `
realms:
  - realm: "test"
    identityProviders:
      - alias: "${TEST_IDP_ALIAS}"
        displayName: "Corporate SSO"
        providerId: "oidc"
        enabled: true
        trustEmail: true
        hideOnLogin: false
        firstBrokerLoginFlowAlias: "first broker login"
        config:
          clientId: "kc"
          clientSecret: "${TEST_IDP_SECRET}"
        mappers:
          - name: "email"
            identityProviderMapper: "oidc-user-attribute-idp-mapper"
            config:
              claim: "email"
              user.attribute: "email"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idps := cfg.Realms[0].IdentityProviders
	if len(idps) != 1 {
		t.Fatalf("expected 1 identity provider, got %d", len(idps))
	}

	idp := idps[0]
	if idp.Alias != "corporate" {
		t.Errorf("alias not expanded: %q", idp.Alias)
	}
	if idp.Config["clientSecret"] != "s3cr3t" {
		t.Errorf("config value not expanded: %q", idp.Config["clientSecret"])
	}
	if idp.Enabled == nil || !*idp.Enabled {
		t.Error("expected enabled=true")
	}
	if idp.HideOnLogin == nil || *idp.HideOnLogin {
		t.Error("expected hideOnLogin=false")
	}
	// An unset flag must stay nil so the reconciler leaves the server's value
	// alone rather than asserting one.
	if idp.StoreToken != nil {
		t.Errorf("expected unset storeToken to stay nil, got %v", *idp.StoreToken)
	}
	if len(idp.Mappers) != 1 || idp.Mappers[0].Config["claim"] != "email" {
		t.Errorf("unexpected mappers: %v", idp.Mappers)
	}
}

func TestIdentityProviderValidation(t *testing.T) {
	tests := []struct {
		name  string
		block string
		want  string
	}{
		{
			"alias required",
			`- providerId: "oidc"`,
			"alias: is required",
		},
		{
			"alias rejects slash",
			`- alias: "a/b"
        providerId: "oidc"`,
			"must not contain '/'",
		},
		{
			"duplicate alias",
			`- alias: "dup"
        providerId: "oidc"
      - alias: "dup"
        providerId: "saml"`,
			"duplicate identity provider alias",
		},
		{
			"providerId required",
			`- alias: "corp"`,
			"providerId: is required",
		},
		{
			"mapper name required",
			`- alias: "corp"
        providerId: "oidc"
        mappers:
          - identityProviderMapper: "oidc-user-attribute-idp-mapper"`,
			"mappers[0].name: is required",
		},
		{
			"duplicate mapper name",
			`- alias: "corp"
        providerId: "oidc"
        mappers:
          - name: "m"
            identityProviderMapper: "oidc-user-attribute-idp-mapper"
          - name: "m"
            identityProviderMapper: "oidc-user-attribute-idp-mapper"`,
			"duplicate mapper name",
		},
		{
			"mapper type required",
			`- alias: "corp"
        providerId: "oidc"
        mappers:
          - name: "m"`,
			"identityProviderMapper: is required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			yaml := "realms:\n  - realm: \"test\"\n    identityProviders:\n      " + tc.block + "\n"

			path := writeTempConfig(t, yaml)
			_, err := Load(path)
			if err == nil {
				t.Fatalf("expected a validation error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected error to mention %q, got: %v", tc.want, err)
			}
		})
	}
}

// TestOrganizationIdentityProviderValidation covers the load-time rule that
// matters most: Keycloak allows a provider to belong to at most one
// organization and answers a bare 400, so two organizations claiming the same
// alias is caught here rather than partway through a run.
func TestOrganizationIdentityProviderValidation(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    organizationsEnabled: true
    identityProviders:
      - alias: "corp"
        providerId: "oidc"
    organizations:
      - name: "acme"
        domains:
          - name: "acme.test"
        identityProviders:
          - "corp"
      - name: "globex"
        domains:
          - name: "globex.test"
        identityProviders:
          - "corp"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected an error for one provider claimed by two organizations")
	}
	if !strings.Contains(err.Error(), "corp") {
		t.Errorf("error should name the alias, got: %v", err)
	}
}

func TestOrganizationIdentityProviderDuplicateWithinOneOrg(t *testing.T) {
	yaml := `
realms:
  - realm: "test"
    organizationsEnabled: true
    organizations:
      - name: "acme"
        domains:
          - name: "acme.test"
        identityProviders:
          - "corp"
          - "corp"
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected an error for a duplicate alias within one organization")
	}
}
