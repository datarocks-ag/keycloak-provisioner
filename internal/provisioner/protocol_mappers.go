package provisioner

import (
	"context"
	"log/slog"

	"keycloak-provisioner/internal/config"
)

func (p *Provisioner) ensureProtocolMapper(ctx context.Context, realm, clientUUID string, pm config.ProtocolMapper) error {
	existing, err := p.client.GetProtocolMappers(ctx, realm, clientUUID)
	if err != nil {
		return err
	}

	body := buildProtocolMapperBody(pm)

	// Find existing mapper by name
	for _, m := range existing {
		if name, ok := m["name"].(string); ok && name == pm.Name {
			id, ok := m["id"].(string)
			if !ok {
				continue
			}
			slog.Info("Updating protocol mapper", "realm", realm, "clientUUID", clientUUID, "mapper", pm.Name)
			body["id"] = id
			return p.client.UpdateProtocolMapper(ctx, realm, clientUUID, id, body)
		}
	}

	slog.Info("Creating protocol mapper", "realm", realm, "clientUUID", clientUUID, "mapper", pm.Name)
	return p.client.CreateProtocolMapper(ctx, realm, clientUUID, body)
}

func buildProtocolMapperBody(pm config.ProtocolMapper) map[string]any {
	body := map[string]any{
		"name":           pm.Name,
		"protocolMapper": pm.ProtocolMapper,
	}

	if pm.Protocol != "" {
		body["protocol"] = pm.Protocol
	}
	if pm.ConsentRequired != nil {
		body["consentRequired"] = *pm.ConsentRequired
	}
	if len(pm.Config) > 0 {
		body["config"] = pm.Config
	}

	return body
}
