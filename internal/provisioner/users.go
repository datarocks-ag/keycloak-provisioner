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
		slog.Info("Creating user", "realm", realm, "username", user.Username)
		id, err := p.client.CreateUser(ctx, realm, body)
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
