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
// client scopes. A scope carrying the other type is moved: it is detached from
// that list first, because Keycloak rejects the conflicting assignment with 409
// rather than replacing it. Type "none" and the empty type leave both lists as
// they are, so a scope assigned by hand is not torn off.
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

	if err := p.detachRealmClientScope(ctx, realm, scopeID, cs); err != nil {
		return err
	}

	slog.Info("Assigning client scope to realm", "realm", realm, "clientScope", cs.Name, "type", cs.Type)

	return p.client.AddRealmClientScope(ctx, realm, scopeID, cs.Type)
}

// detachRealmClientScope removes the scope from the realm list it does not
// belong in, so the assignment that follows takes effect. It is a no-op when
// the scope is not on that list.
func (p *Provisioner) detachRealmClientScope(ctx context.Context, realm, scopeID string, cs config.ClientScope) error {
	other := otherClientScopeType(cs.Type)

	assigned, err := p.client.GetRealmClientScopes(ctx, realm, other)
	if err != nil {
		return err
	}

	if !nameSet(assigned)[cs.Name] {
		return nil
	}

	slog.Info("Detaching client scope from realm to change its type",
		"realm", realm, "clientScope", cs.Name, "type", other)

	return p.client.RemoveRealmClientScope(ctx, realm, scopeID, other)
}

// otherClientScopeType returns the assignment list a scope must leave to take
// the given type.
func otherClientScopeType(scopeType string) string {
	if scopeType == client.ClientScopeDefault {
		return client.ClientScopeOptional
	}

	return client.ClientScopeDefault
}

// ensureClientScopeAssignments attaches the client's configured default and
// optional scopes.
//
// This is the only path that attaches them. The inline
// defaultClientScopes/optionalClientScopes fields of the client representation
// are ignored by Keycloak on update, and on create they replace the realm's
// default scopes rather than adding to them, so buildClientBody omits them
// entirely. A configured scope currently attached with the other type is moved
// to the configured one; anything the config does not mention is left alone,
// including Keycloak's own defaults.
//
// Both lists are read up front, since moving a scope needs to know what the
// other one holds.
func (p *Provisioner) ensureClientScopeAssignments(
	ctx context.Context,
	realm, clientUUID string,
	c config.Client,
	byName clientScopeIndex,
) error {
	if len(c.DefaultClientScopes) == 0 && len(c.OptionalClientScopes) == 0 {
		return nil
	}

	current := make(map[string]map[string]bool, 2)

	for _, scopeType := range []string{client.ClientScopeDefault, client.ClientScopeOptional} {
		assigned, err := p.client.GetClientScopeAssignments(ctx, realm, clientUUID, scopeType)
		if err != nil {
			return err
		}

		current[scopeType] = nameSet(assigned)
	}

	if err := p.assignClientScopes(ctx, realm, clientUUID, c.ClientID,
		client.ClientScopeDefault, c.DefaultClientScopes, byName, current); err != nil {
		return err
	}

	return p.assignClientScopes(ctx, realm, clientUUID, c.ClientID,
		client.ClientScopeOptional, c.OptionalClientScopes, byName, current)
}

// assignClientScopes attaches wanted to the client with the given type.
// current holds the client's attached scope names for both types and is kept up
// to date as scopes move, so the second call sees what the first one did.
func (p *Provisioner) assignClientScopes(
	ctx context.Context,
	realm, clientUUID, clientID, scopeType string,
	wanted []string,
	byName clientScopeIndex,
	current map[string]map[string]bool,
) error {
	other := otherClientScopeType(scopeType)

	for _, name := range wanted {
		if current[scopeType][name] {
			slog.Debug("Client scope already assigned", "realm", realm, "clientId", clientID, "clientScope", name, "type", scopeType)
			continue
		}

		scopeID, ok := byName[name]
		if !ok {
			slog.Warn("Client scope not found, skipping assignment", "realm", realm, "clientId", clientID, "clientScope", name, "type", scopeType)
			continue
		}

		// Keycloak answers an assignment that conflicts with the existing one
		// with 204 and keeps the old type, so a scope changing type has to
		// leave the other list first.
		if current[other][name] {
			slog.Info("Detaching client scope to change its type",
				"realm", realm, "clientId", clientID, "clientScope", name, "type", other)

			if err := p.client.RemoveClientScopeAssignment(ctx, realm, clientUUID, scopeID, other); err != nil {
				return err
			}

			delete(current[other], name)
		}

		slog.Info("Assigning client scope", "realm", realm, "clientId", clientID, "clientScope", name, "type", scopeType)

		if err := p.client.AddClientScopeAssignment(ctx, realm, clientUUID, scopeID, scopeType); err != nil {
			return err
		}

		// Keep the local view in step so a name repeated in the config does not
		// produce a second, redundant assignment.
		current[scopeType][name] = true
	}

	return nil
}
