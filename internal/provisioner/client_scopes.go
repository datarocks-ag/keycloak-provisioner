package provisioner

import (
	"context"
	"fmt"
	"log/slog"

	"keycloak-provisioner/internal/client"
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

// ensureRealmClientScopeType assigns a scope to the realm's default or optional
// client scopes. Assignment is additive: a scope is added when missing and
// never removed, so switching a scope's type in config does not detach it from
// the type it previously had.
func (p *Provisioner) ensureRealmClientScopeType(ctx context.Context, realm, scopeID string, cs config.ClientScope) error {
	switch cs.Type {
	case "", "none":
		return nil
	case client.ClientScopeDefault, client.ClientScopeOptional:
	default:
		return fmt.Errorf("client scope %q: unknown type %q", cs.Name, cs.Type)
	}

	assigned, err := p.client.GetRealmClientScopes(ctx, realm, cs.Type)
	if err != nil {
		return err
	}

	if nameSet(assigned)[cs.Name] {
		slog.Debug("Client scope already assigned to realm", "realm", realm, "clientScope", cs.Name, "type", cs.Type)
		return nil
	}

	slog.Info("Assigning client scope to realm", "realm", realm, "clientScope", cs.Name, "type", cs.Type)

	return p.client.AddRealmClientScope(ctx, realm, scopeID, cs.Type)
}

// ensureClientScopeAssignments attaches the client's configured default and
// optional scopes.
//
// This is the only path that attaches them. The inline
// defaultClientScopes/optionalClientScopes fields of the client representation
// are ignored by Keycloak on update, and on create they replace the realm's
// default scopes rather than adding to them, so buildClientBody omits them
// entirely. Assignment here is additive — scopes already attached to the client
// are left alone, and none are ever detached.
func (p *Provisioner) ensureClientScopeAssignments(
	ctx context.Context,
	realm, clientUUID string,
	c config.Client,
	byName clientScopeIndex,
) error {
	if len(c.DefaultClientScopes) == 0 && len(c.OptionalClientScopes) == 0 {
		return nil
	}

	if err := p.assignClientScopes(ctx, realm, clientUUID, c.ClientID,
		client.ClientScopeDefault, c.DefaultClientScopes, byName); err != nil {
		return err
	}

	return p.assignClientScopes(ctx, realm, clientUUID, c.ClientID,
		client.ClientScopeOptional, c.OptionalClientScopes, byName)
}

func (p *Provisioner) assignClientScopes(
	ctx context.Context,
	realm, clientUUID, clientID, scopeType string,
	wanted []string,
	byName clientScopeIndex,
) error {
	if len(wanted) == 0 {
		return nil
	}

	assigned, err := p.client.GetClientScopeAssignments(ctx, realm, clientUUID, scopeType)
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

		if err := p.client.AddClientScopeAssignment(ctx, realm, clientUUID, scopeID, scopeType); err != nil {
			return err
		}

		// Keep the local view in step so a name repeated in the config does not
		// produce a second, redundant assignment.
		current[name] = true
	}

	return nil
}
