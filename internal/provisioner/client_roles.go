package provisioner

import (
	"context"
	"log/slog"

	"keycloak-provisioner/internal/config"
)

func (p *Provisioner) ensureClientRole(ctx context.Context, realm, clientUUID string, role config.ClientRole) error {
	existing, err := p.client.GetClientRole(ctx, realm, clientUUID, role.Name)
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
		slog.Info("Creating client role", "realm", realm, "clientUUID", clientUUID, "role", role.Name)
		return p.client.CreateClientRole(ctx, realm, clientUUID, body)
	}

	slog.Info("Updating client role", "realm", realm, "clientUUID", clientUUID, "role", role.Name)
	return p.client.UpdateClientRole(ctx, realm, clientUUID, role.Name, body)
}
