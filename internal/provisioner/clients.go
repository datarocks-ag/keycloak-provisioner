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

	if len(existing) == 0 {
		slog.Info("Creating client", "realm", realm, "clientId", c.ClientID)
		uuid, err := p.client.CreateClient(ctx, realm, buildClientBody(c, nil))
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

	body := buildClientBody(c, existing[0])
	body["id"] = uuid

	if err := p.client.UpdateClient(ctx, realm, uuid, body); err != nil {
		return "", err
	}
	return uuid, nil
}

// buildClientBody builds the client representation to send to Keycloak.
// existing is the client's current representation, or nil when the client is
// being created; it is only read to merge attributes (see mergeAttributes).
func buildClientBody(c config.Client, existing map[string]any) map[string]any {
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
	if attrs := buildClientAttributes(c, existing); len(attrs) > 0 {
		body["attributes"] = attrs
	}

	return body
}

// standardTokenExchangeAttr is the Keycloak client attribute that enables
// OAuth 2.0 Token Exchange (RFC 8693). Keycloak 26.2+.
const standardTokenExchangeAttr = "standard.token.exchange.enabled"

// buildClientAttributes merges the client's configured attributes, and the
// attributes derived from typed fields, over the client's current attributes.
// It returns a fresh map so the config's own Attributes map is never mutated.
// Typed fields win over raw attributes, which win over current values.
//
// Keycloak replaces the whole attribute map on update, so the union has to be
// sent; otherwise attributes set out-of-band, or defaulted by Keycloak itself,
// would be dropped on every run.
//
// It returns nil when the config manages no attributes at all, leaving the
// client's attributes out of the request entirely.
func buildClientAttributes(c config.Client, existing map[string]any) map[string]string {
	acrLoaMap := buildAcrLoaMapAttribute(c.AcrLoaMap)
	if len(c.Attributes) == 0 && c.StandardTokenExchangeEnabled == nil && acrLoaMap == "" {
		return nil
	}

	attrs := mergeAttributes(existing, c.Attributes)
	if c.StandardTokenExchangeEnabled != nil {
		attrs[standardTokenExchangeAttr] = strconv.FormatBool(*c.StandardTokenExchangeEnabled)
	}
	if acrLoaMap != "" {
		attrs[acrLoaMapAttr] = acrLoaMap
	}

	return attrs
}
