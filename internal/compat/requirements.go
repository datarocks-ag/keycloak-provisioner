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
	// MinVersion is the oldest Keycloak that supports it.
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
		Feature:    "",
		Since:      "Standard Token Exchange (RFC 8693) is supported from Keycloak 26.2; it is not behind the legacy TOKEN_EXCHANGE preview feature",
		Uses:       usesStandardTokenExchange,
	},
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

func usesStandardTokenExchange(cfg *config.Config) []string {
	var paths []string

	for i, realm := range cfg.Realms {
		for j, c := range realm.Clients {
			if c.StandardTokenExchangeEnabled != nil {
				paths = append(paths, fmt.Sprintf("realms[%d].clients[%d].standardTokenExchangeEnabled", i, j))
			}
		}
	}

	return paths
}
