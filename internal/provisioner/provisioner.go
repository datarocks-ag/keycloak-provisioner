// Package provisioner reconciles a config against a Keycloak server.
//
// Reconciliation is idempotent and additive: resources are created when
// absent and updated in place otherwise, and nothing is ever deleted. What
// a config does not mention is left alone.
package provisioner

import (
	"context"
	"fmt"
	"log/slog"

	"keycloak-provisioner/internal/config"
)

// Provisioner orchestrates idempotent Keycloak resource provisioning.
type Provisioner struct {
	client KeycloakAPI
	cfg    *config.Config
}

// New creates a new Provisioner. The api argument is the Keycloak adapter to
// use; *client.Client is the production implementation.
func New(api KeycloakAPI, cfg *config.Config) *Provisioner {
	return &Provisioner{
		client: api,
		cfg:    cfg,
	}
}

// Run executes the full provisioning sequence:
// Master realm (if configured) -> For each realm: Realm -> Authentication flows -> Client scopes -> Clients (+ protocol mappers + client roles + client scope assignment) -> Realm roles -> Service account roles -> Groups -> Users -> Organizations
func (p *Provisioner) Run(ctx context.Context) error {
	slog.Info("Starting provisioning")

	if p.cfg.MasterRealm != nil {
		if err := p.ensureMasterRealm(ctx, p.cfg.MasterRealm); err != nil {
			return fmt.Errorf("provisioning master realm: %w", err)
		}

		masterStrategy := config.EffectiveStrategy(p.cfg.Strategy)
		for _, user := range p.cfg.MasterRealm.Users {
			if err := p.ensureUser(ctx, "master", user, masterStrategy); err != nil {
				return fmt.Errorf("ensuring user %q in master realm: %w", user.Username, err)
			}
		}
	}

	for _, realm := range p.cfg.Realms {
		strategy := config.EffectiveStrategy(realm.Strategy, p.cfg.Strategy)
		if err := p.provisionRealm(ctx, realm, strategy); err != nil {
			return fmt.Errorf("provisioning realm %q: %w", realm.Realm, err)
		}
	}

	slog.Info("Provisioning complete")
	return nil
}

func (p *Provisioner) provisionRealm(ctx context.Context, realm config.Realm, strategy string) error {
	// 1. Ensure realm
	if err := p.ensureRealm(ctx, realm, strategy); err != nil {
		return fmt.Errorf("ensuring realm: %w", err)
	}

	// 2. Authentication flows (+ executions + execution config). Created
	// before clients so a client can bind to a flow defined here, and before
	// the realm bindings below, which reference them by alias.
	for _, f := range realm.AuthenticationFlows {
		if err := p.ensureAuthenticationFlow(ctx, realm.Realm, f); err != nil {
			return fmt.Errorf("ensuring authentication flow %q: %w", f.Alias, err)
		}
	}

	// 3. Realm authentication bindings. A second realm update, because the
	// flows they point at have to exist first.
	if err := p.ensureAuthenticationBindings(ctx, realm.Realm, realm.AuthenticationBindings); err != nil {
		return fmt.Errorf("ensuring authentication bindings: %w", err)
	}

	// 4. Client scopes (+ their protocol mappers + realm-level assignment).
	// Runs before clients so a client can reference a scope defined here.
	//
	// The realm's scopes are listed once and indexed by name: both the scope
	// reconciler and the per-client assignment resolve names against that
	// index, so neither re-lists per scope or per client.
	var clientScopes clientScopeIndex

	if realmNeedsClientScopeIndex(realm) {
		var err error

		clientScopes, err = p.loadClientScopeIndex(ctx, realm.Realm)
		if err != nil {
			return fmt.Errorf("listing client scopes: %w", err)
		}
	}

	for _, cs := range realm.ClientScopes {
		scopeID, err := p.ensureClientScope(ctx, realm.Realm, cs, strategy, clientScopes)
		if err != nil {
			return fmt.Errorf("ensuring client scope %q: %w", cs.Name, err)
		}

		for _, pm := range cs.ProtocolMappers {
			if err := p.ensureClientScopeProtocolMapper(ctx, realm.Realm, scopeID, cs.Name, pm, strategy); err != nil {
				return fmt.Errorf("ensuring protocol mapper %q for client scope %q: %w", pm.Name, cs.Name, err)
			}
		}

		if err := p.ensureRealmClientScopeType(ctx, realm.Realm, scopeID, cs); err != nil {
			return fmt.Errorf("assigning client scope %q to realm: %w", cs.Name, err)
		}
	}

	// 5. Clients (+ protocol mappers + client roles + client scope assignment)
	// Track client UUIDs for service account role assignment later
	type clientInfo struct {
		uuid     string
		clientID string
		roles    *config.UserRoles
	}
	var saClients []clientInfo

	for _, c := range realm.Clients {
		clientUUID, err := p.ensureClient(ctx, realm.Realm, c, strategy)
		if err != nil {
			return fmt.Errorf("ensuring client %q: %w", c.ClientID, err)
		}

		for _, pm := range c.ProtocolMappers {
			if err := p.ensureProtocolMapper(ctx, realm.Realm, clientUUID, pm, strategy); err != nil {
				return fmt.Errorf("ensuring protocol mapper %q for client %q: %w", pm.Name, c.ClientID, err)
			}
		}

		for _, cr := range c.ClientRoles {
			if err := p.ensureClientRole(ctx, realm.Realm, clientUUID, cr, strategy); err != nil {
				return fmt.Errorf("ensuring client role %q for client %q: %w", cr.Name, c.ClientID, err)
			}
		}

		if err := p.ensureClientScopeAssignments(ctx, realm.Realm, clientUUID, c, clientScopes); err != nil {
			return fmt.Errorf("ensuring client scope assignments for client %q: %w", c.ClientID, err)
		}

		if c.ServiceAccountRoles != nil {
			saClients = append(saClients, clientInfo{uuid: clientUUID, clientID: c.ClientID, roles: c.ServiceAccountRoles})
		}
	}

	// 6. Realm roles
	for _, role := range realm.Roles {
		if err := p.ensureRealmRole(ctx, realm.Realm, role, strategy); err != nil {
			return fmt.Errorf("ensuring realm role %q: %w", role.Name, err)
		}
	}

	// 7. Service account roles (after realm+client roles exist)
	for _, sa := range saClients {
		if err := p.ensureServiceAccountRoles(ctx, realm.Realm, sa.uuid, sa.clientID, sa.roles); err != nil {
			return fmt.Errorf("ensuring service account roles for client %q: %w", sa.clientID, err)
		}
	}

	// 8. Groups (+ attributes + subgroups + realm/client role assignments).
	// Runs after roles so that role assignments resolve to existing roles,
	// and before users so users can join groups defined in the same config.
	for _, g := range realm.Groups {
		if err := p.ensureGroup(ctx, realm.Realm, "", g, strategy); err != nil {
			return fmt.Errorf("ensuring group %q: %w", g.Name, err)
		}
	}

	// 9. Users (after all roles and groups exist)
	for _, user := range realm.Users {
		if err := p.ensureUser(ctx, realm.Realm, user, strategy); err != nil {
			return fmt.Errorf("ensuring user %q: %w", user.Username, err)
		}
	}

	// 10. Organizations (+ domains + members). Last, so members resolve to
	// users created in the same run.
	for _, org := range realm.Organizations {
		if err := p.ensureOrganization(ctx, realm.Realm, org, strategy); err != nil {
			return fmt.Errorf("ensuring organization %q: %w", org.Name, err)
		}
	}

	return nil
}
