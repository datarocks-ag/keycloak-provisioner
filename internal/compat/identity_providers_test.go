package compat

import (
	"strings"
	"testing"

	"keycloak-provisioner/internal/config"
)

func idpConfig(idps ...config.IdentityProvider) *config.Config {
	return &config.Config{Realms: []config.Realm{{
		Realm:             "test",
		IdentityProviders: idps,
	}}}
}

func TestCheckCapabilitiesAcceptsKnownIdentityProviders(t *testing.T) {
	cfg := idpConfig(config.IdentityProvider{
		Alias:      "corp",
		ProviderId: "oidc",
		Mappers: []config.IdentityProviderMapper{
			{Name: "email", IdentityProviderMapper: "oidc-user-attribute-idp-mapper"},
		},
	})

	if problems := CheckCapabilities(cfg, testCapabilities()); len(problems) != 0 {
		t.Errorf("expected no problems, got %v", problems)
	}
}

// TestCheckCapabilitiesRejectsFeatureGatedProvider covers the real case: the
// server's list reflects feature state, so "instagram" is absent unless
// INSTAGRAM_BROKER is enabled. Verified on 26.6.
func TestCheckCapabilitiesRejectsFeatureGatedProvider(t *testing.T) {
	cfg := idpConfig(config.IdentityProvider{Alias: "ig", ProviderId: "instagram"})

	problems := CheckCapabilities(cfg, testCapabilities())
	if len(problems) != 1 {
		t.Fatalf("expected one problem, got %v", problems)
	}
	if problems[0].Capability != "instagram" {
		t.Errorf("unexpected capability: %q", problems[0].Capability)
	}
	if !strings.Contains(problems[0].Paths[0], "identityProviders[0].providerId") {
		t.Errorf("unexpected path: %v", problems[0].Paths)
	}
}

// TestCheckCapabilitiesRejectsUnknownIdentityProviderMapper is the one that
// earns its keep: Keycloak accepts an unknown mapper type with 201 and then
// never applies it, so nothing downstream would ever report this.
func TestCheckCapabilitiesRejectsUnknownIdentityProviderMapper(t *testing.T) {
	cfg := idpConfig(config.IdentityProvider{
		Alias:      "corp",
		ProviderId: "oidc",
		Mappers: []config.IdentityProviderMapper{
			{Name: "typo", IdentityProviderMapper: "oidc-user-attribute-idp-mappr"},
		},
	})

	problems := CheckCapabilities(cfg, testCapabilities())
	if len(problems) != 1 {
		t.Fatalf("expected one problem, got %v", problems)
	}
	if !strings.Contains(problems[0].Paths[0], "identityProviders[0].mappers[0].identityProviderMapper") {
		t.Errorf("unexpected path: %v", problems[0].Paths)
	}
	// The suggestion machinery should catch a single-character typo.
	if !strings.Contains(problems[0].Reason, "oidc-user-attribute-idp-mapper") {
		t.Errorf("expected a suggestion naming the real mapper, got: %s", problems[0].Reason)
	}
}

// TestCheckCapabilitiesSkipsIdentityProvidersWhenServerSilent guards the rule
// that nothing is rejected on the strength of a question the server did not
// answer.
func TestCheckCapabilitiesSkipsIdentityProvidersWhenServerSilent(t *testing.T) {
	caps := testCapabilities()
	caps.IdentityProviders = nil
	caps.IdentityProviderMappers = nil

	cfg := idpConfig(config.IdentityProvider{
		Alias:      "corp",
		ProviderId: "totally-made-up",
		Mappers: []config.IdentityProviderMapper{
			{Name: "m", IdentityProviderMapper: "also-made-up"},
		},
	})

	if problems := CheckCapabilities(cfg, caps); len(problems) != 0 {
		t.Errorf("expected no problems when the server did not report its providers, got %v", problems)
	}
}

func TestReadServerInfoParsesIdentityProviders(t *testing.T) {
	raw := map[string]any{
		"systemInfo": map[string]any{"version": "26.6.0"},
		"identityProviders": []any{
			map[string]any{"id": "oidc", "name": "OpenID Connect v1.0"},
			map[string]any{"id": "saml", "name": "SAML v2.0"},
			map[string]any{"name": "no id here"},
		},
		"providers": map[string]any{
			"identity-provider-mapper": map[string]any{
				"providers": map[string]any{
					"oidc-user-attribute-idp-mapper": map[string]any{"order": 0},
					"hardcoded-role-idp-mapper":      map[string]any{"order": 0},
				},
			},
		},
	}

	info, err := ReadServerInfo(t.Context(), fakeServerInfo{doc: raw})
	if err != nil {
		t.Fatalf("ReadServerInfo: %v", err)
	}

	if !info.IdentityProviders["oidc"] || !info.IdentityProviders["saml"] {
		t.Errorf("expected oidc and saml, got %v", info.IdentityProviders)
	}
	if len(info.IdentityProviders) != 2 {
		t.Errorf("an entry without an id must be skipped, got %v", info.IdentityProviders)
	}
	if !info.IdentityProviderMappers["hardcoded-role-idp-mapper"] {
		t.Errorf("expected the mapper types, got %v", info.IdentityProviderMappers)
	}
}

func TestReadServerInfoToleratesMissingIdentityProviderKeys(t *testing.T) {
	info, err := ReadServerInfo(t.Context(), fakeServerInfo{doc: map[string]any{
		"systemInfo": map[string]any{"version": "26.6.0"},
	}})
	if err != nil {
		t.Fatalf("ReadServerInfo: %v", err)
	}

	if len(info.IdentityProviders) != 0 || len(info.IdentityProviderMappers) != 0 {
		t.Errorf("expected empty maps, got %v / %v", info.IdentityProviders, info.IdentityProviderMappers)
	}
}
