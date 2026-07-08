package provisioner

import (
	"context"
	"fmt"
	"log/slog"

	"keycloak-provisioner/internal/config"
)

// ensureGroup reconciles a single group (and, recursively, its subgroups) within
// a realm. parentID is empty for top-level groups; for subgroups it is the UUID
// of the parent group. Role mappings and subgroups are always descended into,
// regardless of strategy — only the group's own attributes are gated by strategy.
func (p *Provisioner) ensureGroup(ctx context.Context, realm, parentID string, g config.Group, strategy string) error {
	uuid, err := p.reconcileGroup(ctx, realm, parentID, g, strategy)
	if err != nil {
		return err
	}

	if err := p.ensureGroupRealmRoles(ctx, realm, uuid, g); err != nil {
		return fmt.Errorf("realm roles for group %q: %w", g.Name, err)
	}
	if err := p.ensureGroupClientRoles(ctx, realm, uuid, g); err != nil {
		return fmt.Errorf("client roles for group %q: %w", g.Name, err)
	}

	for _, sub := range g.SubGroups {
		if err := p.ensureGroup(ctx, realm, uuid, sub, strategy); err != nil {
			return fmt.Errorf("subgroup %q of %q: %w", sub.Name, g.Name, err)
		}
	}

	return nil
}

// reconcileGroup creates or updates the group itself and returns its UUID.
func (p *Provisioner) reconcileGroup(ctx context.Context, realm, parentID string, g config.Group, strategy string) (string, error) {
	existing, err := p.findGroup(ctx, realm, parentID, g.Name)
	if err != nil {
		return "", err
	}

	body := buildGroupBody(g)

	if existing == nil {
		if parentID == "" {
			slog.Info("Creating group", "realm", realm, "group", g.Name)
			return p.client.CreateGroup(ctx, realm, body)
		}
		slog.Info("Creating subgroup", "realm", realm, "group", g.Name, "parent", parentID)
		return p.client.CreateSubGroup(ctx, realm, parentID, body)
	}

	uuid, ok := existing["id"].(string)
	if !ok {
		return "", fmt.Errorf("group %q: missing or invalid id in response", g.Name)
	}

	if strategy == "create" {
		// Only the group's own attributes are left untouched; the caller still
		// reconciles role mappings and subgroups (they are additive children).
		slog.Info("Skipping existing group update (strategy=create)", "realm", realm, "group", g.Name)
		return uuid, nil
	}

	slog.Info("Updating group", "realm", realm, "group", g.Name, "uuid", uuid)
	body["id"] = uuid
	if err := p.client.UpdateGroup(ctx, realm, uuid, body); err != nil {
		return "", err
	}
	return uuid, nil
}

// findGroup looks up an existing group by exact name at the given level, or nil.
func (p *Provisioner) findGroup(ctx context.Context, realm, parentID, name string) (map[string]any, error) {
	var candidates []map[string]any
	var err error
	if parentID == "" {
		candidates, err = p.client.GetGroups(ctx, realm, name)
	} else {
		candidates, err = p.client.GetSubGroups(ctx, realm, parentID, name)
	}
	if err != nil {
		return nil, err
	}

	for _, c := range candidates {
		if n, ok := c["name"].(string); ok && n == name {
			return c, nil
		}
	}
	return nil, nil
}

func buildGroupBody(g config.Group) map[string]any {
	body := map[string]any{
		"name": g.Name,
	}
	if len(g.Attributes) > 0 {
		body["attributes"] = g.Attributes
	}
	return body
}

// ensureGroupRealmRoles grants any configured realm roles not already mapped to the group.
func (p *Provisioner) ensureGroupRealmRoles(ctx context.Context, realm, groupID string, g config.Group) error {
	if len(g.RealmRoles) == 0 {
		return nil
	}

	existing, err := p.client.GetGroupRealmRoleMappings(ctx, realm, groupID)
	if err != nil {
		return err
	}
	mapped := nameSet(existing)

	var toAdd []map[string]any
	for _, roleName := range g.RealmRoles {
		if mapped[roleName] {
			continue
		}
		role, err := p.client.GetRealmRole(ctx, realm, roleName)
		if err != nil {
			return err
		}
		if role == nil {
			return fmt.Errorf("realm role %q not found", roleName)
		}
		toAdd = append(toAdd, map[string]any{"id": role["id"], "name": role["name"]})
	}

	if len(toAdd) == 0 {
		return nil
	}
	slog.Info("Granting realm roles to group", "realm", realm, "group", g.Name, "count", len(toAdd))
	return p.client.AddGroupRealmRoleMappings(ctx, realm, groupID, toAdd)
}

// ensureGroupClientRoles grants any configured client roles not already mapped to the group.
func (p *Provisioner) ensureGroupClientRoles(ctx context.Context, realm, groupID string, g config.Group) error {
	for clientID, roleNames := range g.ClientRoles {
		if len(roleNames) == 0 {
			continue
		}

		clientUUID, err := p.resolveClientUUID(ctx, realm, clientID)
		if err != nil {
			return err
		}

		existing, err := p.client.GetGroupClientRoleMappings(ctx, realm, groupID, clientUUID)
		if err != nil {
			return err
		}
		mapped := nameSet(existing)

		var toAdd []map[string]any
		for _, roleName := range roleNames {
			if mapped[roleName] {
				continue
			}
			role, err := p.client.GetClientRole(ctx, realm, clientUUID, roleName)
			if err != nil {
				return err
			}
			if role == nil {
				return fmt.Errorf("client role %q not found on client %q", roleName, clientID)
			}
			toAdd = append(toAdd, map[string]any{"id": role["id"], "name": role["name"]})
		}

		if len(toAdd) == 0 {
			continue
		}
		slog.Info("Granting client roles to group", "realm", realm, "group", g.Name, "client", clientID, "count", len(toAdd))
		if err := p.client.AddGroupClientRoleMappings(ctx, realm, groupID, clientUUID, toAdd); err != nil {
			return err
		}
	}

	return nil
}

// resolveClientUUID returns the internal UUID of the client with the given clientId.
func (p *Provisioner) resolveClientUUID(ctx context.Context, realm, clientID string) (string, error) {
	clients, err := p.client.GetClients(ctx, realm, clientID)
	if err != nil {
		return "", err
	}
	for _, c := range clients {
		if id, ok := c["clientId"].(string); ok && id == clientID {
			uuid, ok := c["id"].(string)
			if !ok {
				return "", fmt.Errorf("client %q: missing or invalid id in response", clientID)
			}
			return uuid, nil
		}
	}
	return "", fmt.Errorf("client %q not found", clientID)
}

// nameSet returns the set of "name" values present in a list of role representations.
func nameSet(roles []map[string]any) map[string]bool {
	set := make(map[string]bool, len(roles))
	for _, r := range roles {
		if n, ok := r["name"].(string); ok {
			set[n] = true
		}
	}
	return set
}
