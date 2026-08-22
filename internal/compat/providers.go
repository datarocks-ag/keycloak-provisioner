package compat

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"keycloak-provisioner/internal/config"
)

// providerKinds are the authentication provider lists the server exposes. A
// declared authenticator is checked against the union of all of them rather
// than the one matching its flow type: which list a provider belongs to depends
// on how the flow is built, and a false rejection would block a config that
// works. The union still catches a typo or a provider the server does not have.
var providerKinds = []string{
	"authenticator-providers",
	"form-providers",
	"form-action-providers",
	"client-authenticator-providers",
}

// providerRealm is the realm queried for the provider lists. They are
// server-wide, and master is the one realm guaranteed to exist — the realms in
// the config may not yet.
const providerRealm = "master"

// Capabilities is what the server says it can actually do, as opposed to what
// its version implies. It is the more precise check of the two: a provider
// missing because a feature is switched off shows up here without anyone
// having to record the mapping.
type Capabilities struct {
	// Authenticators holds every authentication provider id the server offers.
	Authenticators map[string]bool
	// ProtocolMappers maps a protocol to the mapper type ids valid for it.
	ProtocolMappers map[string]map[string]bool
	// IdentityProviders holds every identity provider type id the server offers.
	IdentityProviders map[string]bool
	// IdentityProviderMappers holds every identity provider mapper type id the
	// server offers.
	IdentityProviderMappers map[string]bool
}

// known reports whether the capability lists were populated. An empty list
// means the server did not tell us, and nothing should be rejected on the
// strength of a question we could not ask.
func (c Capabilities) known() bool {
	return len(c.Authenticators) > 0
}

// ReadCapabilities collects the provider lists, taking the mapper types from
// the server info the caller already read.
func ReadCapabilities(ctx context.Context, reader ServerReader, info ServerInfo) (Capabilities, error) {
	caps := Capabilities{
		Authenticators:          map[string]bool{},
		ProtocolMappers:         info.ProtocolMappers,
		IdentityProviders:       info.IdentityProviders,
		IdentityProviderMappers: info.IdentityProviderMappers,
	}

	for _, kind := range providerKinds {
		providers, err := reader.GetAuthenticationProviders(ctx, providerRealm, kind)
		if err != nil {
			return Capabilities{}, fmt.Errorf("reading %s: %w", kind, err)
		}

		for _, p := range providers {
			if id, ok := p["id"].(string); ok {
				caps.Authenticators[id] = true
			}
		}
	}

	return caps, nil
}

// CheckCapabilities reports config that names an authenticator or protocol
// mapper the server does not offer. These fail partway through provisioning
// today, with an error from Keycloak that does not say which config entry
// caused it.
func CheckCapabilities(cfg *config.Config, caps Capabilities) []Problem {
	var problems []Problem

	problems = append(problems, checkAuthenticators(cfg, caps)...)
	problems = append(problems, checkProtocolMappers(cfg, caps)...)
	problems = append(problems, checkIdentityProviders(cfg, caps)...)
	problems = append(problems, checkIdentityProviderMappers(cfg, caps)...)

	return problems
}

func checkAuthenticators(cfg *config.Config, caps Capabilities) []Problem {
	if !caps.known() {
		return nil
	}

	// Group by provider so one missing authenticator used in three places is
	// one problem naming three paths, not three problems.
	paths := map[string][]string{}

	for i, realm := range cfg.Realms {
		for j, flow := range realm.AuthenticationFlows {
			prefix := fmt.Sprintf("realms[%d].authenticationFlows[%d]", i, j)
			collectUnknownAuthenticators(prefix+".executions", flow.Executions, caps, paths)
		}
	}

	return problemsFor(paths, caps.Authenticators, "authenticator")
}

func collectUnknownAuthenticators(prefix string, executions []config.AuthenticationExecution, caps Capabilities, paths map[string][]string) {
	for i, e := range executions {
		path := fmt.Sprintf("%s[%d]", prefix, i)

		if e.Provider != "" && !caps.Authenticators[e.Provider] {
			paths[e.Provider] = append(paths[e.Provider], path+".provider")
		}

		collectUnknownAuthenticators(path+".executions", e.Executions, caps, paths)
	}
}

func checkProtocolMappers(cfg *config.Config, caps Capabilities) []Problem {
	if len(caps.ProtocolMappers) == 0 {
		return nil
	}

	paths := map[string][]string{}

	for i, realm := range cfg.Realms {
		for j, cs := range realm.ClientScopes {
			prefix := fmt.Sprintf("realms[%d].clientScopes[%d]", i, j)
			collectUnknownMappers(prefix, cs.ProtocolMappers, caps, paths)
		}

		for j, c := range realm.Clients {
			prefix := fmt.Sprintf("realms[%d].clients[%d]", i, j)
			collectUnknownMappers(prefix, c.ProtocolMappers, caps, paths)
		}
	}

	return problemsFor(paths, allMapperTypes(caps), "protocol mapper type")
}

func collectUnknownMappers(prefix string, mappers []config.ProtocolMapper, caps Capabilities, paths map[string][]string) {
	for i, pm := range mappers {
		if pm.ProtocolMapper == "" {
			continue
		}

		// Validate against the mapper's own protocol when the server knows it,
		// which catches a SAML mapper on an OIDC client. An unrecognised
		// protocol falls back to the union rather than reporting twice.
		valid, known := caps.ProtocolMappers[pm.Protocol]
		if !known {
			valid = allMapperTypes(caps)
		}

		if !valid[pm.ProtocolMapper] {
			path := fmt.Sprintf("%s.protocolMappers[%d].protocolMapper", prefix, i)
			paths[pm.ProtocolMapper] = append(paths[pm.ProtocolMapper], path)
		}
	}
}

func allMapperTypes(caps Capabilities) map[string]bool {
	all := map[string]bool{}

	for _, ids := range caps.ProtocolMappers {
		for id := range ids {
			all[id] = true
		}
	}

	return all
}

// problemsFor turns a name-to-paths map into problems, sorted by name so the
// output is stable across runs. kind names what the entry is, for the message.
func problemsFor(paths map[string][]string, known map[string]bool, kind string) []Problem {
	if len(paths) == 0 {
		return nil
	}

	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)

	problems := make([]Problem, 0, len(names))

	for _, name := range names {
		reason := fmt.Sprintf("%s %q is not available on this server", kind, name)
		if closest := suggest(name, known); closest != "" {
			reason += fmt.Sprintf(" (did you mean %q?)", closest)
		}

		problems = append(problems, Problem{
			Capability: name,
			Paths:      paths[name],
			Reason:     reason,
		})
	}

	return problems
}

// suggest returns the closest known name, so a typo points at what was meant.
//
// It uses edit distance rather than a shared prefix: a provider that is absent
// because a feature is switched off is not a typo, and
// "conditional-level-of-authentication" shares a long prefix with
// "conditional-credential" without being anything like it. A wrong suggestion
// is worse than none, so the bar is deliberately tight.
func suggest(name string, known map[string]bool) string {
	limit := len(name) / 4
	if limit > 3 {
		limit = 3
	}
	if limit < 1 {
		return ""
	}

	best, bestDistance := "", limit+1

	for candidate := range known {
		// Length alone rules out most candidates before the expensive part.
		if abs(len(candidate)-len(name)) > limit {
			continue
		}

		if d := editDistance(name, candidate); d < bestDistance {
			best, bestDistance = candidate, d
		}
	}

	return best
}

// editDistance is the Levenshtein distance between two strings.
func editDistance(a, b string) int {
	a, b = strings.ToLower(a), strings.ToLower(b)

	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)

	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i

		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}

			curr[j] = min(min(curr[j-1]+1, prev[j]+1), prev[j-1]+cost)
		}

		prev, curr = curr, prev
	}

	return prev[len(b)]
}

func abs(n int) int {
	if n < 0 {
		return -n
	}

	return n
}

// checkIdentityProviders reports providerId values the server does not offer.
//
// The server's list reflects feature state as well as the build — "instagram"
// is absent unless INSTAGRAM_BROKER is enabled — so a missing entry means this
// server genuinely cannot create the provider, not merely that the id is
// unfamiliar.
func checkIdentityProviders(cfg *config.Config, caps Capabilities) []Problem {
	if len(caps.IdentityProviders) == 0 {
		return nil
	}

	paths := map[string][]string{}

	for i, realm := range cfg.Realms {
		for j, idp := range realm.IdentityProviders {
			if idp.ProviderId == "" || caps.IdentityProviders[idp.ProviderId] {
				continue
			}

			path := fmt.Sprintf("realms[%d].identityProviders[%d].providerId", i, j)
			paths[idp.ProviderId] = append(paths[idp.ProviderId], path)
		}
	}

	return problemsFor(paths, caps.IdentityProviders, "identity provider type")
}

// checkIdentityProviderMappers reports identityProviderMapper values the server
// does not offer.
//
// This check earns its place more than most: Keycloak accepts an unknown mapper
// type with 201 and then never applies it, so without it a typo is silent
// rather than merely late.
func checkIdentityProviderMappers(cfg *config.Config, caps Capabilities) []Problem {
	if len(caps.IdentityProviderMappers) == 0 {
		return nil
	}

	paths := map[string][]string{}

	for i, realm := range cfg.Realms {
		for j, idp := range realm.IdentityProviders {
			for k, m := range idp.Mappers {
				if m.IdentityProviderMapper == "" || caps.IdentityProviderMappers[m.IdentityProviderMapper] {
					continue
				}

				path := fmt.Sprintf("realms[%d].identityProviders[%d].mappers[%d].identityProviderMapper", i, j, k)
				paths[m.IdentityProviderMapper] = append(paths[m.IdentityProviderMapper], path)
			}
		}
	}

	return problemsFor(paths, caps.IdentityProviderMappers, "identity provider mapper type")
}
