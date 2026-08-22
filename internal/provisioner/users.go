package provisioner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"keycloak-provisioner/internal/config"
)

// Keycloak's own defaults for a TOTP credential.
const (
	defaultOTPDigits    = 6
	defaultOTPPeriod    = 30
	defaultOTPAlgorithm = "HmacSHA1"
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

	if err := p.ensureUserCredentials(ctx, realm, userID, user); err != nil {
		return err
	}

	return nil
}

// ensureUserCredentials seeds the credentials a user does not already hold.
//
// Adding is additive and never rotates: Keycloak's user update *appends* what
// the credentials array carries rather than reconciling it, so sending a
// credential the user already has produces a second one, and every run would
// add another. A credential of the same type and label is therefore left
// alone. Replacing a seeded secret means deleting the credential first, which
// the provisioner does not do — it removes nothing, anywhere.
func (p *Provisioner) ensureUserCredentials(ctx context.Context, realm, userID string, user config.User) error {
	if len(user.Credentials) == 0 {
		return nil
	}

	existing, err := p.client.GetUserCredentials(ctx, realm, userID)
	if err != nil {
		return fmt.Errorf("getting credentials for user %q: %w", user.Username, err)
	}

	held := make(map[string]bool, len(existing))

	for _, c := range existing {
		credType, _ := c["type"].(string)
		label, _ := c["userLabel"].(string)
		held[credType+"\x00"+label] = true
	}

	var toAdd []map[string]any

	for _, c := range user.Credentials {
		if held[c.Type+"\x00"+c.Label] {
			slog.Debug("Credential already present",
				"realm", realm, "username", user.Username, "type", c.Type, "label", c.Label)

			continue
		}

		slog.Info("Seeding user credential",
			"realm", realm, "username", user.Username, "type", c.Type, "label", c.Label)

		toAdd = append(toAdd, buildCredentialBody(c))
	}

	if len(toAdd) == 0 {
		return nil
	}

	// A user update carrying only credentials leaves the rest of the
	// representation alone, unlike an organization update.
	if err := p.client.UpdateUser(ctx, realm, userID, map[string]any{"credentials": toAdd}); err != nil {
		return fmt.Errorf("seeding credentials for user %q: %w", user.Username, err)
	}

	return nil
}

// buildCredentialBody renders a credential the way Keycloak stores one: the
// secret and the parameters go in two JSON strings rather than as fields.
func buildCredentialBody(c config.Credential) map[string]any {
	digits := c.Digits
	if digits == 0 {
		digits = defaultOTPDigits
	}

	period := c.Period
	if period == 0 {
		period = defaultOTPPeriod
	}

	algorithm := c.Algorithm
	if algorithm == "" {
		algorithm = defaultOTPAlgorithm
	}

	credentialData, _ := json.Marshal(map[string]any{
		"subType":   "totp",
		"digits":    digits,
		"counter":   0,
		"period":    period,
		"algorithm": algorithm,
	})
	secretData, _ := json.Marshal(map[string]any{"value": c.Secret})

	body := map[string]any{
		"type":           c.Type,
		"secretData":     string(secretData),
		"credentialData": string(credentialData),
	}

	if c.Label != "" {
		body["userLabel"] = c.Label
	}

	return body
}

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

// ensureUserGroups adds the user to any configured group they are not yet a
// member of. Groups are referenced by path (e.g. "/engineering/backend").
// Membership is additive — existing memberships are never removed. A group
// that does not exist is logged as a warning and skipped; it does not abort
// the run.
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
	// Sent only when declared. Keycloak replaces the list with whatever the
	// body carries and leaves it alone when the key is absent, so an omitted
	// block preserves what the user has and an empty one clears it.
	if user.RequiredActions != nil {
		body["requiredActions"] = user.RequiredActions
	}

	return body
}
