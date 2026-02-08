package provisioner

import (
	"context"
	"log/slog"

	"keycloak-provisioner/internal/config"
)

func (p *Provisioner) ensureRealmRole(ctx context.Context, realm string, role config.RealmRole) error {
	existing, err := p.client.GetRealmRole(ctx, realm, role.Name)
	if err != nil {
		return err
	}

	body := map[string]any{
		"name": role.Name,
	}
	if role.Description != "" {
		body["description"] = role.Description
	}

	if existing == nil {
		slog.Info("Creating realm role", "realm", realm, "role", role.Name)
		return p.client.CreateRealmRole(ctx, realm, body)
	}

	slog.Info("Updating realm role", "realm", realm, "role", role.Name)
	return p.client.UpdateRealmRole(ctx, realm, role.Name, body)
}
