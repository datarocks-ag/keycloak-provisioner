package provisioner

import (
	"context"
	"fmt"
	"log/slog"

	"keycloak-provisioner/internal/config"
)

func (p *Provisioner) ensureClient(ctx context.Context, realm string, c config.Client) (string, error) {
	existing, err := p.client.GetClients(ctx, realm, c.ClientID)
	if err != nil {
		return "", err
	}

	body := buildClientBody(c)

	if len(existing) == 0 {
		slog.Info("Creating client", "realm", realm, "clientId", c.ClientID)
		if err := p.client.CreateClient(ctx, realm, body); err != nil {
			return "", err
		}
		// Re-fetch to get the UUID
		created, err := p.client.GetClients(ctx, realm, c.ClientID)
		if err != nil {
			return "", fmt.Errorf("fetching created client UUID: %w", err)
		}
		if len(created) == 0 {
			return "", fmt.Errorf("client %q not found after creation", c.ClientID)
		}
		return created[0]["id"].(string), nil
	}

	uuid := existing[0]["id"].(string)
	slog.Info("Updating client", "realm", realm, "clientId", c.ClientID, "uuid", uuid)
	body["id"] = uuid
	if err := p.client.UpdateClient(ctx, realm, uuid, body); err != nil {
		return "", err
	}
	return uuid, nil
}

func buildClientBody(c config.Client) map[string]any {
	body := map[string]any{
		"clientId": c.ClientID,
	}

	if c.Secret != "" {
		body["secret"] = c.Secret
	}
	if c.Name != "" {
		body["name"] = c.Name
	}
	if c.Enabled != nil {
		body["enabled"] = *c.Enabled
	}
	if c.PublicClient != nil {
		body["publicClient"] = *c.PublicClient
	}
	if c.Protocol != "" {
		body["protocol"] = c.Protocol
	}
	if c.RootUrl != "" {
		body["rootUrl"] = c.RootUrl
	}
	if c.BaseUrl != "" {
		body["baseUrl"] = c.BaseUrl
	}
	if c.AdminUrl != "" {
		body["adminUrl"] = c.AdminUrl
	}
	if len(c.RedirectUris) > 0 {
		body["redirectUris"] = c.RedirectUris
	}
	if len(c.WebOrigins) > 0 {
		body["webOrigins"] = c.WebOrigins
	}
	if c.StandardFlowEnabled != nil {
		body["standardFlowEnabled"] = *c.StandardFlowEnabled
	}
	if c.DirectAccessGrantsEnabled != nil {
		body["directAccessGrantsEnabled"] = *c.DirectAccessGrantsEnabled
	}
	if c.ServiceAccountsEnabled != nil {
		body["serviceAccountsEnabled"] = *c.ServiceAccountsEnabled
	}
	if c.BearerOnly != nil {
		body["bearerOnly"] = *c.BearerOnly
	}
	if c.ConsentRequired != nil {
		body["consentRequired"] = *c.ConsentRequired
	}
	if c.FrontchannelLogout != nil {
		body["frontchannelLogout"] = *c.FrontchannelLogout
	}
	if len(c.DefaultClientScopes) > 0 {
		body["defaultClientScopes"] = c.DefaultClientScopes
	}
	if len(c.OptionalClientScopes) > 0 {
		body["optionalClientScopes"] = c.OptionalClientScopes
	}
	if len(c.Attributes) > 0 {
		body["attributes"] = c.Attributes
	}

	return body
}
