package provisioner

import (
	"context"
	"fmt"
	"log/slog"

	"keycloak-provisioner/internal/client"
	"keycloak-provisioner/internal/config"
)

// roleSubject identifies who a role mapping is granted to.
//
// Keycloak reaches users and groups through the same /role-mappings endpoints,
// and the reconcile is identical for both, so the subject is a value rather
// than a second copy of the function. name is the username or group name — the
// id is a UUID, which is not what anyone reading a log is looking for.
type roleSubject struct {
	kind string
	id   string
	name string
}

func userRoleSubject(userID, username string) roleSubject {
	return roleSubject{kind: client.RoleSubjectUsers, id: userID, name: username}
}

func groupRoleSubject(groupID, groupName string) roleSubject {
	return roleSubject{kind: client.RoleSubjectGroups, id: groupID, name: groupName}
}

// logArgs returns the attributes identifying this subject, plus whatever the
// caller adds, so every message below describes its subject the same way.
func (s roleSubject) logArgs(realm string, extra ...any) []any {
	return append([]any{"realm", realm, "subject", s.kind, "subjectName", s.name}, extra...)
}

// ensureRoleMappings grants any configured realm and client roles the subject
// does not already hold.
//
// Assignment is additive: a role already mapped is left alone and none is ever
// removed. A configured role that does not exist is an error rather than a
// warning — unlike a missing group or user, which can be drift, a named role
// that is absent means the config asks for something the realm cannot give.
func (p *Provisioner) ensureRoleMappings(
	ctx context.Context,
	realm string,
	subject roleSubject,
	realmRoles []string,
	clientRoles map[string][]string,
) error {
	if err := p.ensureRealmRoleMappings(ctx, realm, subject, realmRoles); err != nil {
		return err
	}

	return p.ensureClientRoleMappings(ctx, realm, subject, clientRoles)
}

func (p *Provisioner) ensureRealmRoleMappings(ctx context.Context, realm string, subject roleSubject, wanted []string) error {
	if len(wanted) == 0 {
		return nil
	}

	existing, err := p.client.GetRealmRoleMappings(ctx, realm, subject.kind, subject.id)
	if err != nil {
		return fmt.Errorf("getting realm role mappings for %s %q: %w", subject.kind, subject.name, err)
	}

	mapped := nameSet(existing)

	var toAdd []map[string]any

	for _, roleName := range wanted {
		if mapped[roleName] {
			slog.Debug("Realm role already assigned", subject.logArgs(realm, "role", roleName)...)
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

	slog.Info("Assigning realm roles", subject.logArgs(realm, "count", len(toAdd))...)

	if err := p.client.AddRealmRoleMappings(ctx, realm, subject.kind, subject.id, toAdd); err != nil {
		return fmt.Errorf("assigning realm roles to %s %q: %w", subject.kind, subject.name, err)
	}

	return nil
}

func (p *Provisioner) ensureClientRoleMappings(ctx context.Context, realm string, subject roleSubject, wanted map[string][]string) error {
	for clientID, roleNames := range wanted {
		if len(roleNames) == 0 {
			continue
		}

		clientUUID, err := p.resolveClientUUID(ctx, realm, clientID)
		if err != nil {
			return fmt.Errorf("resolving client for %s %q: %w", subject.kind, subject.name, err)
		}

		existing, err := p.client.GetClientRoleMappings(ctx, realm, subject.kind, subject.id, clientUUID)
		if err != nil {
			return fmt.Errorf("getting client role mappings for %s %q on client %q: %w", subject.kind, subject.name, clientID, err)
		}

		mapped := nameSet(existing)

		var toAdd []map[string]any

		for _, roleName := range roleNames {
			if mapped[roleName] {
				slog.Debug("Client role already assigned", subject.logArgs(realm, "client", clientID, "role", roleName)...)
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

		slog.Info("Assigning client roles", subject.logArgs(realm, "client", clientID, "count", len(toAdd))...)

		if err := p.client.AddClientRoleMappings(ctx, realm, subject.kind, subject.id, clientUUID, toAdd); err != nil {
			return fmt.Errorf("assigning client roles to %s %q on client %q: %w", subject.kind, subject.name, clientID, err)
		}
	}

	return nil
}

// roleRef reduces a role representation to what the role-mapping endpoints
// need. Keycloak accepts the whole representation, but echoing back fields it
// derived — composite, containerId — only invites one of them to be read as an
// instruction later.
func roleRef(role map[string]any) map[string]any {
	return map[string]any{"id": role["id"], "name": role["name"]}
}

// ensureUserRoles grants a user's configured roles. roles may be nil, which
// means the config said nothing about them and nothing is touched.
func (p *Provisioner) ensureUserRoles(ctx context.Context, realm, userID, username string, roles *config.UserRoles) error {
	if roles == nil {
		return nil
	}

	return p.ensureRoleMappings(ctx, realm, userRoleSubject(userID, username), roles.Realm, roles.Clients)
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
	return "", fmt.Errorf("client %q not found in realm %q", clientID, realm)
}
