package provisioner

import (
	"context"
	"fmt"
	"log/slog"

	"keycloak-provisioner/internal/client"
	"keycloak-provisioner/internal/config"
)

// mapperTarget identifies what a protocol mapper hangs off.
//
// Keycloak attaches mappers to two kinds of container through the same
// /protocol-mappers/models endpoint, and the reconcile is identical for both,
// so the container is a value rather than a second copy of the function. name
// is the clientId or client scope name — the id alone is a UUID, which is not
// what anyone reading a log is looking for.
type mapperTarget struct {
	container string
	id        string
	name      string
}

func clientMapperTarget(clientUUID, clientID string) mapperTarget {
	return mapperTarget{container: client.MapperContainerClients, id: clientUUID, name: clientID}
}

func clientScopeMapperTarget(scopeID, scopeName string) mapperTarget {
	return mapperTarget{container: client.MapperContainerClientScopes, id: scopeID, name: scopeName}
}

// logArgs returns the attributes for one mapper message, so every message below
// describes its container the same way.
func (t mapperTarget) logArgs(realm, mapper string) []any {
	return []any{
		"realm", realm,
		"container", t.container,
		"containerId", t.id,
		"containerName", t.name,
		"mapper", mapper,
	}
}

// ensureProtocolMapper creates or updates one protocol mapper on a client or a
// client scope, matched by name.
func (p *Provisioner) ensureProtocolMapper(ctx context.Context, realm string, target mapperTarget, pm config.ProtocolMapper, strategy string) error {
	existing, err := p.client.GetProtocolMappers(ctx, realm, target.container, target.id)
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
			slog.Info("Skipping existing protocol mapper (strategy=create)", target.logArgs(realm, pm.Name)...)

			return nil
		}

		id, ok := m["id"].(string)
		if !ok {
			return fmt.Errorf("protocol mapper %q on %s %q: missing or invalid id in response", pm.Name, target.container, target.name)
		}

		slog.Info("Updating protocol mapper", target.logArgs(realm, pm.Name)...)
		body["id"] = id

		return p.client.UpdateProtocolMapper(ctx, realm, target.container, target.id, id, body)
	}

	slog.Info("Creating protocol mapper", target.logArgs(realm, pm.Name)...)

	return p.client.CreateProtocolMapper(ctx, realm, target.container, target.id, body)
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
