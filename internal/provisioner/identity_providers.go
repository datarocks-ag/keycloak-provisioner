package provisioner

import (
	"context"
	"fmt"
	"log/slog"

	"keycloak-provisioner/internal/config"
)

// serverOwnedIdentityProviderFields are keys Keycloak derives and returns in an
// identity provider representation but that must not be echoed back. internalId
// and organizationId are server-assigned identity, and types is computed.
// Sending them back either has no effect or reassigns the provider, so the
// merge drops them.
var serverOwnedIdentityProviderFields = []string{"internalId", "organizationId", "types"}

// ensureIdentityProviders reconciles a realm's identity providers and their
// mappers.
//
// The realm's providers are listed once. The listing carries each provider's
// full representation, which is what the update path merges over, so no
// per-alias re-read is needed.
func (p *Provisioner) ensureIdentityProviders(ctx context.Context, realm config.Realm, strategy string) error {
	if len(realm.IdentityProviders) == 0 {
		return nil
	}

	existing, err := p.client.GetIdentityProviders(ctx, realm.Realm)
	if err != nil {
		return fmt.Errorf("listing identity providers: %w", err)
	}

	byAlias := indexIdentityProvidersByAlias(existing)

	if err := p.warnUnknownBrokerFlows(ctx, realm); err != nil {
		return err
	}

	for _, idp := range realm.IdentityProviders {
		if err := p.ensureIdentityProvider(ctx, realm.Realm, idp, strategy, byAlias[idp.Alias]); err != nil {
			return fmt.Errorf("ensuring identity provider %q: %w", idp.Alias, err)
		}
	}

	return nil
}

// indexIdentityProvidersByAlias indexes a provider listing by alias. Entries
// without an alias are skipped rather than producing an unusable mapping.
func indexIdentityProvidersByAlias(providers []map[string]any) map[string]map[string]any {
	index := make(map[string]map[string]any, len(providers))

	for _, p := range providers {
		if alias, ok := p["alias"].(string); ok {
			index[alias] = p
		}
	}

	return index
}

// warnUnknownBrokerFlows reports broker login flow aliases that are not in the
// realm's flow listing.
//
// Keycloak answers a 500 with no usable message when it cannot resolve one of
// these aliases, so naming the flow up front is the difference between a clear
// error and an opaque one. It warns rather than fails: the listing can
// legitimately be incomplete — in a dry run against a realm that does not exist
// yet, none of its built-in flows are visible — so the server stays the
// authority, exactly as copyFrom does.
func (p *Provisioner) warnUnknownBrokerFlows(ctx context.Context, realm config.Realm) error {
	referenced := make(map[string][]string)

	for _, idp := range realm.IdentityProviders {
		for _, alias := range []string{idp.FirstBrokerLoginFlowAlias, idp.PostBrokerLoginFlowAlias} {
			if alias != "" {
				referenced[alias] = append(referenced[alias], idp.Alias)
			}
		}
	}

	if len(referenced) == 0 {
		return nil
	}

	flows, err := p.client.GetAuthenticationFlows(ctx, realm.Realm)
	if err != nil {
		return fmt.Errorf("listing authentication flows: %w", err)
	}

	for alias, users := range referenced {
		if findFlowByAlias(flows, alias) == nil {
			slog.Warn("Broker login flow not visible in the current flow listing",
				"realm", realm.Realm, "flow", alias, "identityProviders", users)
		}
	}

	return nil
}

// ensureIdentityProvider creates or updates one provider and its mappers.
// existing is the provider's current representation, or nil when it is absent.
func (p *Provisioner) ensureIdentityProvider(
	ctx context.Context,
	realm string,
	idp config.IdentityProvider,
	strategy string,
	existing map[string]any,
) error {
	switch {
	case existing != nil && strategy == "create":
		slog.Info("Skipping existing identity provider (strategy=create)",
			"realm", realm, "identityProvider", idp.Alias)

	case existing != nil:
		slog.Info("Updating identity provider", "realm", realm, "identityProvider", idp.Alias)

		if err := p.client.UpdateIdentityProvider(ctx, realm, idp.Alias, buildIdentityProviderBody(idp, existing)); err != nil {
			return err
		}

	default:
		slog.Info("Creating identity provider",
			"realm", realm, "identityProvider", idp.Alias, "providerId", idp.ProviderId)

		if err := p.client.CreateIdentityProvider(ctx, realm, buildIdentityProviderBody(idp, nil)); err != nil {
			return err
		}
	}

	return p.ensureIdentityProviderMappers(ctx, realm, idp, strategy)
}

// buildIdentityProviderBody merges the configured provider over its current
// representation.
//
// Keycloak's update is a full replace, not a sparse merge: a field or config key
// left out of the body is removed, which is why this starts from existing rather
// than building a fresh sparse body as the other reconcilers do. existing is nil
// on create.
//
// alias and providerId are always sent. A body alias differing from the one in
// the path renames the provider, and providerId is required on create.
func buildIdentityProviderBody(idp config.IdentityProvider, existing map[string]any) map[string]any {
	flags := identityProviderFlags(idp)
	body := make(map[string]any, len(existing)+len(flags)+4)

	for k, v := range existing {
		body[k] = v
	}

	for _, k := range serverOwnedIdentityProviderFields {
		delete(body, k)
	}

	body["alias"] = idp.Alias
	body["providerId"] = idp.ProviderId

	if idp.DisplayName != "" {
		body["displayName"] = idp.DisplayName
	}
	if idp.FirstBrokerLoginFlowAlias != "" {
		body["firstBrokerLoginFlowAlias"] = idp.FirstBrokerLoginFlowAlias
	}
	if idp.PostBrokerLoginFlowAlias != "" {
		body["postBrokerLoginFlowAlias"] = idp.PostBrokerLoginFlowAlias
	}

	for key, configured := range flags {
		if configured != nil {
			body[key] = *configured
		}
	}

	if cfg := mergeStringMapField(existing, "config", idp.Config); len(cfg) > 0 {
		body["config"] = cfg
	}

	return body
}

// identityProviderFlags maps the provider's boolean fields to their Keycloak
// keys. A nil pointer means the field was not configured, so the server's
// current value is left in place by the merge above.
func identityProviderFlags(idp config.IdentityProvider) map[string]*bool {
	return map[string]*bool{
		"enabled":                  idp.Enabled,
		"trustEmail":               idp.TrustEmail,
		"storeToken":               idp.StoreToken,
		"addReadTokenRoleOnCreate": idp.AddReadTokenRoleOnCreate,
		"linkOnly":                 idp.LinkOnly,
		"hideOnLogin":              idp.HideOnLogin,
	}
}

// ensureIdentityProviderMappers creates or updates the provider's mappers.
func (p *Provisioner) ensureIdentityProviderMappers(
	ctx context.Context,
	realm string,
	idp config.IdentityProvider,
	strategy string,
) error {
	if len(idp.Mappers) == 0 {
		return nil
	}

	existing, err := p.client.GetIdentityProviderMappers(ctx, realm, idp.Alias)
	if err != nil {
		return err
	}

	byName := make(map[string]map[string]any, len(existing))

	for _, m := range existing {
		if name, ok := m["name"].(string); ok {
			byName[name] = m
		}
	}

	for _, m := range idp.Mappers {
		if err := p.ensureIdentityProviderMapper(ctx, realm, idp.Alias, m, strategy, byName[m.Name]); err != nil {
			return fmt.Errorf("ensuring mapper %q: %w", m.Name, err)
		}
	}

	return nil
}

func (p *Provisioner) ensureIdentityProviderMapper(
	ctx context.Context,
	realm, alias string,
	m config.IdentityProviderMapper,
	strategy string,
	existing map[string]any,
) error {
	body := buildIdentityProviderMapperBody(alias, m)

	if existing == nil {
		slog.Info("Creating identity provider mapper",
			"realm", realm, "identityProvider", alias, "mapper", m.Name)

		return p.client.CreateIdentityProviderMapper(ctx, realm, alias, body)
	}

	if strategy == "create" {
		slog.Info("Skipping existing identity provider mapper (strategy=create)",
			"realm", realm, "identityProvider", alias, "mapper", m.Name)

		return nil
	}

	id, ok := existing["id"].(string)
	if !ok {
		return fmt.Errorf("identity provider mapper %q: missing or invalid id in response", m.Name)
	}

	slog.Info("Updating identity provider mapper",
		"realm", realm, "identityProvider", alias, "mapper", m.Name)

	body["id"] = id

	return p.client.UpdateIdentityProviderMapper(ctx, realm, alias, id, body)
}

// buildIdentityProviderMapperBody builds a mapper representation.
//
// Unlike the provider itself, a mapper's config is taken from the config alone
// rather than merged over the server's copy. A mapper has neither a masked
// secret nor fields the schema does not model, so a plain replace is more
// predictable: removing a key from the config removes it from Keycloak, which
// is not true one level up.
func buildIdentityProviderMapperBody(alias string, m config.IdentityProviderMapper) map[string]any {
	body := map[string]any{
		"name":                   m.Name,
		"identityProviderAlias":  alias,
		"identityProviderMapper": m.IdentityProviderMapper,
	}

	if len(m.Config) > 0 {
		body["config"] = m.Config
	}

	return body
}
