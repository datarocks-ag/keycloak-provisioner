package provisioner

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

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

	if err := p.ensureRoleMappings(ctx, realm, groupRoleSubject(uuid, g.Name), g.RealmRoles, g.ClientRoles); err != nil {
		return fmt.Errorf("roles for group %q: %w", g.Name, err)
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
		} else {
			slog.Info("Creating subgroup", "realm", realm, "group", g.Name, "parent", parentID)
		}

		return p.client.CreateGroup(ctx, realm, parentID, body)
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
	candidates, err := p.client.GetGroups(ctx, realm, parentID, name)
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

// normalizeGroupPath returns the path in Keycloak's canonical form: a single
// leading slash, no trailing slash (e.g. "engineering/backend/" -> "/engineering/backend").
func normalizeGroupPath(path string) string {
	return "/" + strings.Trim(path, "/")
}

// resolveGroupPath resolves a normalized group path to the group's UUID by
// walking the group tree one level per path segment. Returns "" (and no
// error) when the path does not resolve to an existing group.
func (p *Provisioner) resolveGroupPath(ctx context.Context, realm, path string) (string, error) {
	parentID := ""
	for _, name := range strings.Split(strings.Trim(path, "/"), "/") {
		g, err := p.findGroup(ctx, realm, parentID, name)
		if err != nil {
			return "", err
		}
		if g == nil {
			return "", nil
		}
		id, ok := g["id"].(string)
		if !ok {
			return "", fmt.Errorf("group %q: missing or invalid id in response", name)
		}
		parentID = id
	}
	return parentID, nil
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
