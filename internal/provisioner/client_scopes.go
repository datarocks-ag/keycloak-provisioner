package provisioner

import (
	"context"
	"fmt"
	"log/slog"

	"keycloak-provisioner/internal/config"
)

// defaultClientScopeProtocol is what Keycloak assumes when a client scope does
// not name a protocol.
const defaultClientScopeProtocol = "openid-connect"

// clientScopeIndex maps client scope names to their UUIDs for one realm.
//
// The admin API offers no lookup by name, so scopes have to be listed and
// matched on "name". Listing once per realm and keeping the index up to date
// avoids re-listing for every scope and again for every client's assignments.
type clientScopeIndex map[string]string

// loadClientScopeIndex lists the realm's client scopes once and indexes them by
// name. Entries without both a name and an id are skipped rather than producing
// an unusable mapping.
func (p *Provisioner) loadClientScopeIndex(ctx context.Context, realm string) (clientScopeIndex, error) {
	scopes, err := p.client.GetClientScopes(ctx, realm)
	if err != nil {
		return nil, err
	}

	index := make(clientScopeIndex, len(scopes))

	for _, s := range scopes {
		name, nameOK := s["name"].(string)
		id, idOK := s["id"].(string)

		if nameOK && idOK {
			index[name] = id
		}
	}

	return index, nil
}

// realmNeedsClientScopeIndex reports whether anything in the realm resolves a
// client scope by name, so the listing is skipped for realms that do not.
func realmNeedsClientScopeIndex(realm config.Realm) bool {
	if len(realm.ClientScopes) > 0 {
		return true
	}

	for _, c := range realm.Clients {
		if len(c.DefaultClientScopes) > 0 || len(c.OptionalClientScopes) > 0 {
			return true
		}
	}

	return false
}

// ensureClientScope creates or updates a single client scope and returns its
// UUID. scopes is the realm's name-to-UUID index, which is updated in place
// when a scope is created so later lookups see it.
func (p *Provisioner) ensureClientScope(
	ctx context.Context,
	realm string,
	cs config.ClientScope,
	strategy string,
	scopes clientScopeIndex,
) (string, error) {
	body := buildClientScopeBody(cs)

	if id, ok := scopes[cs.Name]; ok {
		if strategy == "create" {
			slog.Info("Skipping existing client scope (strategy=create)", "realm", realm, "clientScope", cs.Name)
			return id, nil
		}

		slog.Info("Updating client scope", "realm", realm, "clientScope", cs.Name, "uuid", id)
		body["id"] = id

		if err := p.client.UpdateClientScope(ctx, realm, id, body); err != nil {
			return "", err
		}

		return id, nil
	}

	slog.Info("Creating client scope", "realm", realm, "clientScope", cs.Name)

	id, err := p.client.CreateClientScope(ctx, realm, body)
	if err != nil {
		return "", err
	}
	scopes[cs.Name] = id

	return id, nil
}

func buildClientScopeBody(cs config.ClientScope) map[string]any {
	protocol := cs.Protocol
	if protocol == "" {
		protocol = defaultClientScopeProtocol
	}

	body := map[string]any{
		"name":     cs.Name,
		"protocol": protocol,
	}

	if cs.Description != "" {
		body["description"] = cs.Description
	}
	if len(cs.Attributes) > 0 {
		body["attributes"] = cs.Attributes
	}

	return body
}

// ensureClientScopeProtocolMapper creates or updates one protocol mapper on a
// client scope. It mirrors ensureProtocolMapper, which does the same for the
// mappers attached directly to a client.
func (p *Provisioner) ensureClientScopeProtocolMapper(ctx context.Context, realm, scopeID, scopeName string, pm config.ProtocolMapper, strategy string) error {
	existing, err := p.client.GetClientScopeProtocolMappers(ctx, realm, scopeID)
	if err != nil {
		return err
	}

	body := buildProtocolMapperBody(pm)

	for _, m := range existing {
		name, ok := m["name"].(string)
		if !ok || name != pm.Name {
			continue
		}

		if strategy == "create" {
			slog.Info("Skipping existing client scope protocol mapper (strategy=create)", "realm", realm, "clientScope", scopeName, "mapper", pm.Name)
			return nil
		}

		id, ok := m["id"].(string)
		if !ok {
			return fmt.Errorf("client scope protocol mapper %q: missing or invalid id in response", pm.Name)
		}

		slog.Info("Updating client scope protocol mapper", "realm", realm, "clientScope", scopeName, "mapper", pm.Name)
		body["id"] = id

		return p.client.UpdateClientScopeProtocolMapper(ctx, realm, scopeID, id, body)
	}

	slog.Info("Creating client scope protocol mapper", "realm", realm, "clientScope", scopeName, "mapper", pm.Name)

	return p.client.CreateClientScopeProtocolMapper(ctx, realm, scopeID, body)
}

// ensureRealmClientScopeType assigns a scope to the realm's default or optional
// client scopes. Assignment is additive: a scope is added when missing and
// never removed, so switching a scope's type in config does not detach it from
// the type it previously had.
func (p *Provisioner) ensureRealmClientScopeType(ctx context.Context, realm, scopeID string, cs config.ClientScope) error {
	switch cs.Type {
	case "", "none":
		return nil
	case "default":
		return p.assignRealmClientScope(ctx, realm, scopeID, cs.Name, "default",
			p.client.GetRealmDefaultClientScopes, p.client.AddRealmDefaultClientScope)
	case "optional":
		return p.assignRealmClientScope(ctx, realm, scopeID, cs.Name, "optional",
			p.client.GetRealmOptionalClientScopes, p.client.AddRealmOptionalClientScope)
	default:
		return fmt.Errorf("client scope %q: unknown type %q", cs.Name, cs.Type)
	}
}

func (p *Provisioner) assignRealmClientScope(
	ctx context.Context,
	realm, scopeID, scopeName, scopeType string,
	list func(context.Context, string) ([]map[string]any, error),
	add func(context.Context, string, string) error,
) error {
	assigned, err := list(ctx, realm)
	if err != nil {
		return err
	}

	if nameSet(assigned)[scopeName] {
		slog.Debug("Client scope already assigned to realm", "realm", realm, "clientScope", scopeName, "type", scopeType)
		return nil
	}

	slog.Info("Assigning client scope to realm", "realm", realm, "clientScope", scopeName, "type", scopeType)

	return add(ctx, realm, scopeID)
}

// ensureClientScopeAssignments attaches the client's configured default and
// optional scopes.
//
// The inline defaultClientScopes/optionalClientScopes fields of the client
// representation are only honoured by Keycloak when a client is created, so
// they cannot pick up a scope added to an existing client. These explicit
// assignments cover the update path. Assignment is additive — scopes already
// attached to the client are left alone, and none are ever detached.
func (p *Provisioner) ensureClientScopeAssignments(
	ctx context.Context,
	realm, clientUUID string,
	c config.Client,
	byName clientScopeIndex,
) error {
	if len(c.DefaultClientScopes) == 0 && len(c.OptionalClientScopes) == 0 {
		return nil
	}

	if err := p.assignClientScopes(ctx, realm, clientUUID, c.ClientID, "default", c.DefaultClientScopes, byName,
		p.client.GetClientDefaultScopes, p.client.AddClientDefaultScope); err != nil {
		return err
	}

	return p.assignClientScopes(ctx, realm, clientUUID, c.ClientID, "optional", c.OptionalClientScopes, byName,
		p.client.GetClientOptionalScopes, p.client.AddClientOptionalScope)
}

func (p *Provisioner) assignClientScopes(
	ctx context.Context,
	realm, clientUUID, clientID, scopeType string,
	wanted []string,
	byName clientScopeIndex,
	list func(context.Context, string, string) ([]map[string]any, error),
	add func(context.Context, string, string, string) error,
) error {
	if len(wanted) == 0 {
		return nil
	}

	assigned, err := list(ctx, realm, clientUUID)
	if err != nil {
		return err
	}
	current := nameSet(assigned)

	for _, name := range wanted {
		if current[name] {
			slog.Debug("Client scope already assigned", "realm", realm, "clientId", clientID, "clientScope", name, "type", scopeType)
			continue
		}

		scopeID, ok := byName[name]
		if !ok {
			slog.Warn("Client scope not found, skipping assignment", "realm", realm, "clientId", clientID, "clientScope", name, "type", scopeType)
			continue
		}

		slog.Info("Assigning client scope", "realm", realm, "clientId", clientID, "clientScope", name, "type", scopeType)

		if err := add(ctx, realm, clientUUID, scopeID); err != nil {
			return err
		}

		// Keep the local view in step so a name repeated in the config does not
		// produce a second, redundant assignment.
		current[name] = true
	}

	return nil
}
