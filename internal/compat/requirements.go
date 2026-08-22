package compat

import (
	"fmt"

	"keycloak-provisioner/internal/config"
)

// Requirement describes a capability this provisioner can configure, the
// oldest Keycloak that understands it, and how to tell whether a given config
// relies on it.
//
// Every MinVersion here must be verified against a real server or an upstream
// release note before it is added — a guessed floor is worse than none, since
// it refuses runs that would have worked. Capabilities old enough that no
// supported Keycloak lacks them are deliberately absent: this table exists to
// catch real mismatches, not to enumerate the API.
type Requirement struct {
	// Name is how the capability is described in errors and in the docs table.
	Name string
	// MinVersion is the oldest Keycloak that supports it, or "" when every
	// release in range does and only the feature flag matters.
	MinVersion string
	// Feature is the Keycloak server feature that must also be enabled, or ""
	// when the capability is not behind a feature flag. Version alone is not
	// always sufficient: a feature can be supported by the release and still
	// be switched off on the server.
	Feature string
	// Since records where the floor comes from, so a future reader can check
	// it rather than trust it.
	Since string
	// Uses returns the config paths that rely on this capability, empty when
	// the config does not use it at all.
	Uses func(cfg *config.Config) []string
}

// Requirements is the single source of truth for version gating. The docs
// table in README.md is generated from it; see TestCompatibilityTableMatchesDocs.
var Requirements = []Requirement{
	{
		Name:       "organizations",
		MinVersion: "26.0",
		Feature:    "ORGANIZATION",
		Since:      "Organizations became a supported feature in Keycloak 26.0",
		Uses:       usesOrganizations,
	},
	{
		Name:       "organization groups",
		MinVersion: "26.6",
		Feature:    "ORGANIZATION",
		Since:      "Organization groups were introduced in Keycloak 26.6.0",
		Uses:       usesOrganizationGroups,
	},
	{
		Name:       "standard token exchange",
		MinVersion: "26.2",
		Feature:    "TOKEN_EXCHANGE_STANDARD_V2",
		Since:      "Standard Token Exchange (RFC 8693) is supported from Keycloak 26.2. It is governed by TOKEN_EXCHANGE_STANDARD_V2, not by the legacy TOKEN_EXCHANGE preview feature, which is off by default and unrelated",
		Uses:       usesStandardTokenExchange,
	},
	{
		Name: "step-up authentication",
		// No version floor: acr.loa.map long predates any Keycloak this tool
		// meets. The feature can still be switched off, which is what matters.
		MinVersion: "",
		Feature:    "STEP_UP_AUTHENTICATION",
		Since:      "Step-up authentication predates the supported range; disabling STEP_UP_AUTHENTICATION removes the conditional-level-of-authentication authenticator and makes acr.loa.map inert",
		Uses:       usesStepUpAuthentication,
	},
}

// usesStepUpAuthentication reports the realms and clients that declare an
// ACR-to-LoA map. With the feature disabled the attribute is still written and
// provisioning still succeeds — it simply has no effect, which is worse than a
// failure because nothing says so.
func usesStepUpAuthentication(cfg *config.Config) []string {
	var paths []string

	for i, realm := range cfg.Realms {
		if len(realm.AcrLoaMap) > 0 {
			paths = append(paths, fmt.Sprintf("realms[%d].acrLoaMap", i))
		}

		for j, c := range realm.Clients {
			if len(c.AcrLoaMap) > 0 {
				paths = append(paths, fmt.Sprintf("realms[%d].clients[%d].acrLoaMap", i, j))
			}
		}
	}

	return paths
}

func usesOrganizations(cfg *config.Config) []string {
	var paths []string

	for i, realm := range cfg.Realms {
		if realm.OrganizationsEnabled != nil && *realm.OrganizationsEnabled {
			paths = append(paths, fmt.Sprintf("realms[%d].organizationsEnabled", i))
		}

		for j := range realm.Organizations {
			paths = append(paths, fmt.Sprintf("realms[%d].organizations[%d]", i, j))
		}
	}

	return paths
}

func usesOrganizationGroups(cfg *config.Config) []string {
	var paths []string

	for i, realm := range cfg.Realms {
		for j, org := range realm.Organizations {
			if len(org.Groups) > 0 {
				paths = append(paths, fmt.Sprintf("realms[%d].organizations[%d].groups", i, j))
			}
		}
	}

	return paths
}

// usesStandardTokenExchange counts a client only when it enables the feature.
// Setting the field to false writes standard.token.exchange.enabled=false,
// which an older Keycloak simply does not recognise — refusing the run would
// block a config that works. Config validation draws the same line: its
// confidential-client rule applies only when the value is true.
func usesStandardTokenExchange(cfg *config.Config) []string {
	var paths []string

	for i, realm := range cfg.Realms {
		for j, c := range realm.Clients {
			if c.StandardTokenExchangeEnabled != nil && *c.StandardTokenExchangeEnabled {
				paths = append(paths, fmt.Sprintf("realms[%d].clients[%d].standardTokenExchangeEnabled", i, j))
			}
		}
	}

	return paths
}
