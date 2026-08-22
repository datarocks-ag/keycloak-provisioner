package provisioner

import (
	"context"
	"fmt"
	"log/slog"

	"keycloak-provisioner/internal/client"
	"keycloak-provisioner/internal/config"
)

// scopeOwner identifies whose scope a role mapping widens.
//
// Keycloak reaches clients and client scopes through the same /scope-mappings
// endpoints, and the reconcile is identical for both, so the owner is a value
// rather than a second copy of the function — the same shape roleSubject has
// for users and groups. name is the clientId or scope name: the id is a UUID,
// which is not what anyone reading a log is looking for.
type scopeOwner struct {
	kind string
	id   string
	name string
}

func clientScopeOwner(clientUUID, clientID string) scopeOwner {
	return scopeOwner{kind: client.ScopeOwnerClients, id: clientUUID, name: clientID}
}

func clientScopeScopeOwner(scopeID, scopeName string) scopeOwner {
	return scopeOwner{kind: client.ScopeOwnerClientScopes, id: scopeID, name: scopeName}
}

// logArgs returns the attributes identifying this owner, plus whatever the
// caller adds, so every message below describes its owner the same way.
func (o scopeOwner) logArgs(realm string, extra ...any) []any {
	return append([]any{"realm", realm, "owner", o.kind, "ownerName", o.name}, extra...)
}

// ensureScopeMappings puts the configured realm and client roles into the
// owner's scope, which is what decides whether a role the subject already holds
// reaches the token once fullScopeAllowed is false.
//
// Assignment is additive, matching ensureRoleMappings: a role already in scope
// is left alone and none is ever removed, so a scope widened outside the config
// survives a run. A configured role that does not exist is an error — a named
// role that is absent means the config asks for something the realm cannot
// give, and silently leaving it out of scope would show up much later as a
// token missing a claim.
//
// roles may be nil, which means the config said nothing and nothing is touched.
func (p *Provisioner) ensureScopeMappings(ctx context.Context, realm string, owner scopeOwner, roles *config.UserRoles) error {
	if roles == nil {
		return nil
	}

	if err := p.ensureRealmScopeMappings(ctx, realm, owner, roles.Realm); err != nil {
		return err
	}

	return p.ensureClientScopeMappings(ctx, realm, owner, roles.Clients)
}

func (p *Provisioner) ensureRealmScopeMappings(ctx context.Context, realm string, owner scopeOwner, wanted []string) error {
	if len(wanted) == 0 {
		return nil
	}

	existing, err := p.client.GetRealmScopeMappings(ctx, realm, owner.kind, owner.id)
	if err != nil {
		return fmt.Errorf("getting realm scope mappings for %s %q: %w", owner.kind, owner.name, err)
	}

	inScope := nameSet(existing)

	var toAdd []map[string]any

	for _, roleName := range wanted {
		if inScope[roleName] {
			slog.Debug("Realm role already in scope", owner.logArgs(realm, "role", roleName)...)
			continue
		}

		role, err := p.client.GetRealmRole(ctx, realm, roleName)
		if err != nil {
			return fmt.Errorf("looking up realm role %q: %w", roleName, err)
		}
		if role == nil {
			return fmt.Errorf("realm role %q not found in realm %q", roleName, realm)
		}

		toAdd = append(toAdd, roleRef(role))
	}

	if len(toAdd) == 0 {
		return nil
	}

	slog.Info("Adding realm roles to scope", owner.logArgs(realm, "count", len(toAdd))...)

	if err := p.client.AddRealmScopeMappings(ctx, realm, owner.kind, owner.id, toAdd); err != nil {
		return fmt.Errorf("adding realm roles to the scope of %s %q: %w", owner.kind, owner.name, err)
	}

	return nil
}

func (p *Provisioner) ensureClientScopeMappings(ctx context.Context, realm string, owner scopeOwner, wanted map[string][]string) error {
	for clientID, roleNames := range wanted {
		if len(roleNames) == 0 {
			continue
		}

		clientUUID, err := p.resolveClientUUID(ctx, realm, clientID)
		if err != nil {
			return fmt.Errorf("resolving client for %s %q: %w", owner.kind, owner.name, err)
		}

		existing, err := p.client.GetClientScopeMappings(ctx, realm, owner.kind, owner.id, clientUUID)
		if err != nil {
			return fmt.Errorf("getting client scope mappings for %s %q on client %q: %w", owner.kind, owner.name, clientID, err)
		}

		inScope := nameSet(existing)

		var toAdd []map[string]any

		for _, roleName := range roleNames {
			if inScope[roleName] {
				slog.Debug("Client role already in scope", owner.logArgs(realm, "client", clientID, "role", roleName)...)
				continue
			}

			role, err := p.client.GetClientRole(ctx, realm, clientUUID, roleName)
			if err != nil {
				return fmt.Errorf("looking up client role %q on client %q: %w", roleName, clientID, err)
			}
			if role == nil {
				return fmt.Errorf("client role %q not found on client %q in realm %q", roleName, clientID, realm)
			}

			toAdd = append(toAdd, roleRef(role))
		}

		if len(toAdd) == 0 {
			continue
		}

		slog.Info("Adding client roles to scope", owner.logArgs(realm, "client", clientID, "count", len(toAdd))...)

		if err := p.client.AddClientScopeMappings(ctx, realm, owner.kind, owner.id, clientUUID, toAdd); err != nil {
			return fmt.Errorf("adding client roles to the scope of %s %q on client %q: %w", owner.kind, owner.name, clientID, err)
		}
	}

	return nil
}

// deferredScopeMapping is a scope mapping waiting for the roles it names to
// exist. A client's or client scope's own reconcile happens before realm roles
// and before the other clients' roles, so the mapping cannot be applied where
// it is declared.
type deferredScopeMapping struct {
	owner scopeOwner
	roles *config.UserRoles
}
