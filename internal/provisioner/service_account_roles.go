package provisioner

import (
	"context"
	"fmt"
	"log/slog"

	"keycloak-provisioner/internal/config"
)

func (p *Provisioner) ensureServiceAccountRoles(ctx context.Context, realm, clientUUID, clientID string, roles *config.UserRoles) error {
	saUser, err := p.client.GetServiceAccountUser(ctx, realm, clientUUID)
	if err != nil {
		return fmt.Errorf("getting service account user for client %q: %w", clientID, err)
	}

	userID, ok := saUser["id"].(string)
	if !ok {
		return fmt.Errorf("service account user for client %q: missing or invalid id in response", clientID)
	}

	username := fmt.Sprintf("service-account-%s", clientID)
	slog.Info("Ensuring service account roles", "realm", realm, "clientId", clientID, "serviceAccountUserId", userID)

	return p.ensureUserRoles(ctx, realm, userID, username, roles)
}
