package provisioner

import (
	"context"
	"fmt"
	"log/slog"

	"keycloak-provisioner/internal/client"
	"keycloak-provisioner/internal/config"
)

// Provisioner orchestrates idempotent Keycloak resource provisioning.
type Provisioner struct {
	client *client.Client
	cfg    *config.Config
}

// New creates a new Provisioner.
func New(client *client.Client, cfg *config.Config) *Provisioner {
	return &Provisioner{
		client: client,
		cfg:    cfg,
	}
}

// Run executes the full provisioning sequence:
// For each realm: Realm -> Clients (+ protocol mappers + client roles) -> Realm roles
func (p *Provisioner) Run(ctx context.Context) error {
	slog.Info("Starting provisioning")

	for _, realm := range p.cfg.Realms {
		if err := p.provisionRealm(ctx, realm); err != nil {
			return fmt.Errorf("provisioning realm %q: %w", realm.Realm, err)
		}
	}

	slog.Info("Provisioning complete")
	return nil
}

func (p *Provisioner) provisionRealm(ctx context.Context, realm config.Realm) error {
	// 1. Ensure realm
	if err := p.ensureRealm(ctx, realm); err != nil {
		return fmt.Errorf("ensuring realm: %w", err)
	}

	// 2. Clients (+ protocol mappers + client roles)
	for _, c := range realm.Clients {
		clientUUID, err := p.ensureClient(ctx, realm.Realm, c)
		if err != nil {
			return fmt.Errorf("ensuring client %q: %w", c.ClientID, err)
		}

		for _, pm := range c.ProtocolMappers {
			if err := p.ensureProtocolMapper(ctx, realm.Realm, clientUUID, pm); err != nil {
				return fmt.Errorf("ensuring protocol mapper %q for client %q: %w", pm.Name, c.ClientID, err)
			}
		}

		for _, cr := range c.ClientRoles {
			if err := p.ensureClientRole(ctx, realm.Realm, clientUUID, cr); err != nil {
				return fmt.Errorf("ensuring client role %q for client %q: %w", cr.Name, c.ClientID, err)
			}
		}
	}

	// 3. Realm roles
	for _, role := range realm.Roles {
		if err := p.ensureRealmRole(ctx, realm.Realm, role); err != nil {
			return fmt.Errorf("ensuring realm role %q: %w", role.Name, err)
		}
	}

	return nil
}
