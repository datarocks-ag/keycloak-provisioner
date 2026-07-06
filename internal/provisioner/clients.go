package provisioner

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"keycloak-provisioner/internal/config"
)

func (p *Provisioner) ensureClient(ctx context.Context, realm string, c config.Client, strategy string) (string, error) {
	existing, err := p.client.GetClients(ctx, realm, c.ClientID)
	if err != nil {
		return "", err
	}

	body := buildClientBody(c)

	if len(existing) == 0 {
		slog.Info("Creating client", "realm", realm, "clientId", c.ClientID)
		uuid, err := p.client.CreateClient(ctx, realm, body)
		if err != nil {
			return "", err
		}
		return uuid, nil
	}

	uuid, ok := existing[0]["id"].(string)
	if !ok {
		return "", fmt.Errorf("client %q: missing or invalid id in response", c.ClientID)
	}

	if strategy == "create" {
		slog.Info("Skipping existing client (strategy=create)", "realm", realm, "clientId", c.ClientID)
		return uuid, nil
	}

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
	if attrs := buildClientAttributes(c); len(attrs) > 0 {
		body["attributes"] = attrs
	}

	return body
}

// standardTokenExchangeAttr is the Keycloak client attribute that enables
// OAuth 2.0 Token Exchange (RFC 8693). Keycloak 26.2+.
const standardTokenExchangeAttr = "standard.token.exchange.enabled"

// buildClientAttributes merges the client's raw attributes with attributes
// derived from typed fields. It returns a fresh map so the config's own
// Attributes map is never mutated. Typed fields win over raw attributes.
func buildClientAttributes(c config.Client) map[string]string {
	attrs := make(map[string]string, len(c.Attributes)+1)
	for k, v := range c.Attributes {
		attrs[k] = v
	}
	if c.StandardTokenExchangeEnabled != nil {
		attrs[standardTokenExchangeAttr] = strconv.FormatBool(*c.StandardTokenExchangeEnabled)
	}
	return attrs
}
