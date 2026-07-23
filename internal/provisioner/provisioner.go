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
// Master realm (if configured) -> For each realm: Realm -> Clients (+ protocol mappers + client roles) -> Realm roles -> Service account roles -> Groups -> Users
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

	// 2. Clients (+ protocol mappers + client roles)
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

		if c.ServiceAccountRoles != nil {
			saClients = append(saClients, clientInfo{uuid: clientUUID, clientID: c.ClientID, roles: c.ServiceAccountRoles})
		}
	}

	// 3. Realm roles
	for _, role := range realm.Roles {
		if err := p.ensureRealmRole(ctx, realm.Realm, role, strategy); err != nil {
			return fmt.Errorf("ensuring realm role %q: %w", role.Name, err)
		}
	}

	// 4. Service account roles (after realm+client roles exist)
	for _, sa := range saClients {
		if err := p.ensureServiceAccountRoles(ctx, realm.Realm, sa.uuid, sa.clientID, sa.roles); err != nil {
			return fmt.Errorf("ensuring service account roles for client %q: %w", sa.clientID, err)
		}
	}

	// 5. Groups (+ attributes + subgroups + realm/client role assignments).
	// Runs after roles so that role assignments resolve to existing roles,
	// and before users so users can join groups defined in the same config.
	for _, g := range realm.Groups {
		if err := p.ensureGroup(ctx, realm.Realm, "", g, strategy); err != nil {
			return fmt.Errorf("ensuring group %q: %w", g.Name, err)
		}
	}

	// 6. Users (after all roles and groups exist)
	for _, user := range realm.Users {
		if err := p.ensureUser(ctx, realm.Realm, user, strategy); err != nil {
			return fmt.Errorf("ensuring user %q: %w", user.Username, err)
		}
	}

	return nil
}
