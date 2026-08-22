package provisioner

import (
	"context"
	"fmt"
	"log/slog"

	"keycloak-provisioner/internal/config"
)

func (p *Provisioner) ensureUser(ctx context.Context, realm string, user config.User, strategy string) error {
	existing, err := p.client.GetUsers(ctx, realm, user.Username)
	if err != nil {
		return err
	}

	body := buildUserBody(user)

	var userID string
	created := false

	if len(existing) == 0 {
		id, err := p.createUser(ctx, realm, user, body)
		if err != nil {
			return err
		}

		userID = id
		created = true
	} else {
		id, ok := existing[0]["id"].(string)
		if !ok {
			return fmt.Errorf("user %q: missing or invalid id in response", user.Username)
		}
		userID = id

		// An id cannot be changed after creation, so a mismatch means the
		// server holds a different identity than the config asserts —
		// something else may already reference the existing one.
		if user.ID != "" && user.ID != userID {
			return fmt.Errorf(
				"user %q exists with id %s but the config declares %s; ids cannot be changed after creation",
				user.Username, userID, user.ID)
		}

		if strategy == "create" {
			slog.Info("Skipping existing user (strategy=create)", "realm", realm, "username", user.Username)
		} else {
			slog.Info("Updating user", "realm", realm, "username", user.Username)
			body["id"] = userID
			if err := p.client.UpdateUser(ctx, realm, userID, body); err != nil {
				return err
			}
		}
	}

	if user.Password != "" {
		slog.Info("Setting user password", "realm", realm, "username", user.Username)
		if err := p.client.ResetUserPassword(ctx, realm, userID, user.Password, false); err != nil {
			return err
		}
	} else if user.InitialPassword != "" && created {
		slog.Info("Setting initial (temporary) password", "realm", realm, "username", user.Username)
		if err := p.client.ResetUserPassword(ctx, realm, userID, user.InitialPassword, true); err != nil {
			return err
		}
	}

	if user.Roles != nil {
		if err := p.ensureUserRoles(ctx, realm, userID, user.Username, user.Roles); err != nil {
			return err
		}
	}

	if len(user.Groups) > 0 {
		if err := p.ensureUserGroups(ctx, realm, userID, user.Username, user.Groups); err != nil {
			return err
		}
	}

	return nil
}

// ensureUserGroups adds the user to any configured group they are not yet a
// member of. Groups are referenced by path (e.g. "/engineering/backend").
// Membership is additive — existing memberships are never removed. A group
// that does not exist is logged as a warning and skipped; it does not abort
// the run.
// createUser creates a user and returns its id.
//
// A user that declares an id goes through partial import: Keycloak's
// create-user endpoint accepts an id in the representation and silently
// discards it, generating its own instead. Import honours it, and with
// ifResourceExists=SKIP it creates only, so the rest of the reconcile —
// password, roles, groups — continues down the normal path either way.
func (p *Provisioner) createUser(ctx context.Context, realm string, user config.User, body map[string]any) (string, error) {
	if user.ID == "" {
		slog.Info("Creating user", "realm", realm, "username", user.Username)

		return p.client.CreateUser(ctx, realm, body)
	}

	slog.Info("Creating user with a fixed id", "realm", realm, "username", user.Username, "userID", user.ID)

	imported := make(map[string]any, len(body)+1)
	for k, v := range body {
		imported[k] = v
	}

	imported["id"] = user.ID

	if err := p.client.PartialImportUsers(ctx, realm, []map[string]any{imported}); err != nil {
		return "", fmt.Errorf("importing user %q with id %s: %w", user.Username, user.ID, err)
	}

	return user.ID, nil
}

func (p *Provisioner) ensureUserGroups(ctx context.Context, realm, userID, username string, groups []string) error {
	existing, err := p.client.GetUserGroups(ctx, realm, userID)
	if err != nil {
		return fmt.Errorf("getting group memberships for user %q: %w", username, err)
	}

	memberOf := make(map[string]bool, len(existing))
	for _, g := range existing {
		if path, ok := g["path"].(string); ok {
			memberOf[path] = true
		}
	}

	for _, path := range groups {
		normalized := normalizeGroupPath(path)
		if memberOf[normalized] {
			slog.Debug("User already member of group", "realm", realm, "username", username, "group", normalized)
			continue
		}

		groupID, err := p.resolveGroupPath(ctx, realm, normalized)
		if err != nil {
			return fmt.Errorf("resolving group %q for user %q: %w", normalized, username, err)
		}
		if groupID == "" {
			slog.Warn("Group not found, skipping membership", "realm", realm, "username", username, "group", normalized)
			continue
		}

		slog.Info("Adding user to group", "realm", realm, "username", username, "group", normalized)
		if err := p.client.AddUserToGroup(ctx, realm, userID, groupID); err != nil {
			return fmt.Errorf("adding user %q to group %q: %w", username, normalized, err)
		}
		// Mark as member so duplicate paths in the config (after
		// normalization) are not added again in the same run.
		memberOf[normalized] = true
	}

	return nil
}

func (p *Provisioner) ensureUserRoles(ctx context.Context, realm, userID, username string, roles *config.UserRoles) error {
	// Assign realm roles
	if len(roles.Realm) > 0 {
		existingRoles, err := p.client.GetUserRealmRoleMappings(ctx, realm, userID)
		if err != nil {
			return fmt.Errorf("getting realm role mappings for user %q: %w", username, err)
		}

		existingNames := make(map[string]bool, len(existingRoles))
		for _, r := range existingRoles {
			if name, ok := r["name"].(string); ok {
				existingNames[name] = true
			}
		}

		var toAdd []map[string]any
		for _, roleName := range roles.Realm {
			if existingNames[roleName] {
				slog.Debug("Realm role already assigned", "realm", realm, "username", username, "role", roleName)
				continue
			}
			role, err := p.client.GetRealmRole(ctx, realm, roleName)
			if err != nil {
				return fmt.Errorf("looking up realm role %q: %w", roleName, err)
			}
			if role == nil {
				return fmt.Errorf("realm role %q not found in realm %q", roleName, realm)
			}
			toAdd = append(toAdd, role)
		}

		if len(toAdd) > 0 {
			slog.Info("Assigning realm roles to user", "realm", realm, "username", username, "count", len(toAdd))
			if err := p.client.AddUserRealmRoleMappings(ctx, realm, userID, toAdd); err != nil {
				return fmt.Errorf("assigning realm roles to user %q: %w", username, err)
			}
		}
	}

	// Assign client roles
	for clientID, clientRoleNames := range roles.Clients {
		if len(clientRoleNames) == 0 {
			continue
		}

		clients, err := p.client.GetClients(ctx, realm, clientID)
		if err != nil {
			return fmt.Errorf("looking up client %q: %w", clientID, err)
		}
		if len(clients) == 0 {
			return fmt.Errorf("client %q not found in realm %q", clientID, realm)
		}
		clientUUID, ok := clients[0]["id"].(string)
		if !ok {
			return fmt.Errorf("client %q: missing or invalid id in response", clientID)
		}

		existingRoles, err := p.client.GetUserClientRoleMappings(ctx, realm, userID, clientUUID)
		if err != nil {
			return fmt.Errorf("getting client role mappings for user %q on client %q: %w", username, clientID, err)
		}

		existingNames := make(map[string]bool, len(existingRoles))
		for _, r := range existingRoles {
			if name, ok := r["name"].(string); ok {
				existingNames[name] = true
			}
		}

		var toAdd []map[string]any
		for _, roleName := range clientRoleNames {
			if existingNames[roleName] {
				slog.Debug("Client role already assigned", "realm", realm, "username", username, "client", clientID, "role", roleName)
				continue
			}
			role, err := p.client.GetClientRole(ctx, realm, clientUUID, roleName)
			if err != nil {
				return fmt.Errorf("looking up client role %q on client %q: %w", roleName, clientID, err)
			}
			if role == nil {
				return fmt.Errorf("client role %q not found on client %q in realm %q", roleName, clientID, realm)
			}
			toAdd = append(toAdd, role)
		}

		if len(toAdd) > 0 {
			slog.Info("Assigning client roles to user", "realm", realm, "username", username, "client", clientID, "count", len(toAdd))
			if err := p.client.AddUserClientRoleMappings(ctx, realm, userID, clientUUID, toAdd); err != nil {
				return fmt.Errorf("assigning client roles to user %q on client %q: %w", username, clientID, err)
			}
		}
	}

	return nil
}

func buildUserBody(user config.User) map[string]any {
	body := map[string]any{
		"username": user.Username,
	}

	if user.Enabled != nil {
		body["enabled"] = *user.Enabled
	}
	if user.Email != "" {
		body["email"] = user.Email
	}
	if user.FirstName != "" {
		body["firstName"] = user.FirstName
	}
	if user.LastName != "" {
		body["lastName"] = user.LastName
	}
	if user.EmailVerified != nil {
		body["emailVerified"] = *user.EmailVerified
	}

	return body
}
