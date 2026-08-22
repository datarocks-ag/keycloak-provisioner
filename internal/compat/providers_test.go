package compat

import (
	"context"
	"strings"
	"testing"

	"keycloak-provisioner/internal/config"
)

func testCapabilities() Capabilities {
	return Capabilities{
		Authenticators: map[string]bool{
			"auth-cookie":                         true,
			"auth-otp-form":                       true,
			"conditional-level-of-authentication": true,
			"registration-page-form":              true,
			"client-secret":                       true,
		},
		ProtocolMappers: map[string]map[string]bool{
			"openid-connect": {"oidc-audience-mapper": true, "oidc-acr-mapper": true},
			"saml":           {"saml-audience-mapper": true},
		},
	}
}

func flowConfig(executions ...config.AuthenticationExecution) *config.Config {
	return &config.Config{Realms: []config.Realm{{
		Realm: "test",
		AuthenticationFlows: []config.AuthenticationFlow{{
			Alias:      "browser-step-up",
			Executions: executions,
		}},
	}}}
}

func TestCheckCapabilitiesAcceptsKnownProviders(t *testing.T) {
	cfg := flowConfig(
		config.AuthenticationExecution{Provider: "auth-cookie"},
		config.AuthenticationExecution{Subflow: "loa-gold", Executions: []config.AuthenticationExecution{
			{Provider: "conditional-level-of-authentication"},
			{Provider: "auth-otp-form"},
		}},
	)

	if problems := CheckCapabilities(cfg, testCapabilities()); len(problems) != 0 {
		t.Errorf("expected no problems, got %v", problems)
	}
}

func TestCheckCapabilitiesRejectsUnknownAuthenticator(t *testing.T) {
	// The exact case a disabled feature produces: with STEP_UP_AUTHENTICATION
	// off, this authenticator disappears from the server's list.
	caps := testCapabilities()
	delete(caps.Authenticators, "conditional-level-of-authentication")

	cfg := flowConfig(config.AuthenticationExecution{Subflow: "loa", Executions: []config.AuthenticationExecution{
		{Provider: "conditional-level-of-authentication"},
	}})

	problems := CheckCapabilities(cfg, caps)
	if len(problems) != 1 {
		t.Fatalf("expected one problem, got %v", problems)
	}
	if problems[0].Capability != "conditional-level-of-authentication" {
		t.Errorf("unexpected capability: %q", problems[0].Capability)
	}
	if !strings.Contains(problems[0].Paths[0], "executions[0].executions[0].provider") {
		t.Errorf("path should point at the nested execution, got %v", problems[0].Paths)
	}
}

func TestCheckCapabilitiesSuggestsClosestName(t *testing.T) {
	cfg := flowConfig(config.AuthenticationExecution{Provider: "auth-cookei"})

	problems := CheckCapabilities(cfg, testCapabilities())
	if len(problems) != 1 {
		t.Fatalf("expected one problem, got %v", problems)
	}
	if !strings.Contains(problems[0].Reason, `did you mean "auth-cookie"`) {
		t.Errorf("a typo should suggest the real name, got %q", problems[0].Reason)
	}
}

func TestCheckCapabilitiesOmitsUselessSuggestion(t *testing.T) {
	cfg := flowConfig(config.AuthenticationExecution{Provider: "totally-different"})

	problems := CheckCapabilities(cfg, testCapabilities())
	if len(problems) != 1 {
		t.Fatalf("expected one problem, got %v", problems)
	}
	if strings.Contains(problems[0].Reason, "did you mean") {
		t.Errorf("an unrelated name should not get a suggestion, got %q", problems[0].Reason)
	}
}

func TestCheckCapabilitiesGroupsRepeatedProvider(t *testing.T) {
	cfg := flowConfig(
		config.AuthenticationExecution{Provider: "nope"},
		config.AuthenticationExecution{Subflow: "s", Executions: []config.AuthenticationExecution{{Provider: "nope"}}},
	)

	problems := CheckCapabilities(cfg, testCapabilities())
	if len(problems) != 1 {
		t.Fatalf("one missing provider used twice should be one problem, got %v", problems)
	}
	if len(problems[0].Paths) != 2 {
		t.Errorf("both uses should be named, got %v", problems[0].Paths)
	}
}

func TestCheckCapabilitiesSkipsWhenServerDidNotSay(t *testing.T) {
	// Nothing should be rejected on the strength of a question we could not ask.
	cfg := flowConfig(config.AuthenticationExecution{Provider: "anything-at-all"})

	if problems := CheckCapabilities(cfg, Capabilities{}); len(problems) != 0 {
		t.Errorf("an empty capability set must not reject anything, got %v", problems)
	}
}

func TestCheckCapabilitiesProtocolMappers(t *testing.T) {
	caps := testCapabilities()

	cfg := &config.Config{Realms: []config.Realm{{
		Realm: "test",
		ClientScopes: []config.ClientScope{{
			Name: "orders:read",
			ProtocolMappers: []config.ProtocolMapper{
				{Name: "ok", Protocol: "openid-connect", ProtocolMapper: "oidc-audience-mapper"},
				{Name: "bad", Protocol: "openid-connect", ProtocolMapper: "oidc-nonexistent-mapper"},
			},
		}},
		Clients: []config.Client{{
			ClientID: "web",
			ProtocolMappers: []config.ProtocolMapper{
				// A SAML mapper on an OIDC client: valid id, wrong protocol.
				{Name: "wrong-protocol", Protocol: "openid-connect", ProtocolMapper: "saml-audience-mapper"},
			},
		}},
	}}}

	problems := CheckCapabilities(cfg, caps)
	if len(problems) != 2 {
		t.Fatalf("expected two mapper problems, got %v", problems)
	}

	byName := map[string]Problem{}
	for _, p := range problems {
		byName[p.Capability] = p
	}

	if _, ok := byName["oidc-nonexistent-mapper"]; !ok {
		t.Errorf("unknown mapper type not reported: %v", problems)
	}
	if _, ok := byName["saml-audience-mapper"]; !ok {
		t.Errorf("a SAML mapper under openid-connect should be reported: %v", problems)
	}
}

func TestCheckCapabilitiesUnknownProtocolFallsBackToUnion(t *testing.T) {
	// An unrecognised protocol is validated against every mapper type rather
	// than reported twice; Keycloak rejects the protocol itself.
	cfg := &config.Config{Realms: []config.Realm{{
		Realm: "test",
		Clients: []config.Client{{
			ClientID: "web",
			ProtocolMappers: []config.ProtocolMapper{
				{Name: "m", Protocol: "made-up", ProtocolMapper: "saml-audience-mapper"},
			},
		}},
	}}}

	if problems := CheckCapabilities(cfg, testCapabilities()); len(problems) != 0 {
		t.Errorf("a known mapper under an unknown protocol should not be reported, got %v", problems)
	}
}

func TestReadCapabilities(t *testing.T) {
	reader := fakeServerInfo{
		doc: map[string]any{
			"systemInfo": map[string]any{"version": "26.6.4"},
			"protocolMapperTypes": map[string]any{
				"openid-connect": []any{map[string]any{"id": "oidc-audience-mapper"}},
			},
		},
		providers: map[string][]string{
			"authenticator-providers":        {"auth-cookie"},
			"form-providers":                 {"registration-page-form"},
			"form-action-providers":          {"registration-password-action"},
			"client-authenticator-providers": {"client-secret"},
		},
	}

	caps, err := ReadCapabilities(context.Background(), reader)
	if err != nil {
		t.Fatalf("ReadCapabilities: %v", err)
	}

	// Every kind contributes to one union, so a provider from any list counts.
	for _, want := range []string{"auth-cookie", "registration-page-form", "registration-password-action", "client-secret"} {
		if !caps.Authenticators[want] {
			t.Errorf("missing provider %q from the union: %v", want, caps.Authenticators)
		}
	}
	if !caps.ProtocolMappers["openid-connect"]["oidc-audience-mapper"] {
		t.Errorf("unexpected mapper types: %v", caps.ProtocolMappers)
	}
}

// TestSuggestUsesEditDistance pins the suggestion heuristic. A provider that is
// absent because a feature is switched off is not a typo, and suggesting an
// unrelated name that merely shares a prefix is worse than saying nothing.
func TestSuggestUsesEditDistance(t *testing.T) {
	known := map[string]bool{
		"auth-cookie":            true,
		"conditional-credential": true,
		"oidc-audience-mapper":   true,
		"registration-page-form": true,
	}

	tests := []struct {
		name string
		want string
	}{
		{"auth-cookei", "auth-cookie"},
		{"oidc-audiance-mapper", "oidc-audience-mapper"},
		// Shares the long prefix "conditional-" but is a different thing.
		{"conditional-level-of-authentication", ""},
		{"totally-unrelated-name", ""},
		{"", ""},
	}

	for _, tt := range tests {
		if got := suggest(tt.name, known); got != tt.want {
			t.Errorf("suggest(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestEditDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"auth-cookei", "auth-cookie", 2},
		{"", "abc", 3},
		{"AUTH-COOKIE", "auth-cookie", 0},
	}

	for _, tt := range tests {
		if got := editDistance(tt.a, tt.b); got != tt.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
