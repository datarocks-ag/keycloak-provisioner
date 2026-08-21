package provisioner

import (
	"context"
	"fmt"
	"log/slog"

	"keycloak-provisioner/internal/config"
)

func (p *Provisioner) ensureRealm(ctx context.Context, realm config.Realm, strategy string) error {
	existing, err := p.client.GetRealm(ctx, realm.Realm)
	if err != nil {
		return err
	}

	if existing == nil {
		slog.Info("Creating realm", "realm", realm.Realm)
		return p.client.CreateRealm(ctx, buildRealmBody(realm, nil))
	}

	if strategy == "create" {
		slog.Info("Skipping existing realm (strategy=create)", "realm", realm.Realm)
		return nil
	}

	slog.Info("Updating realm", "realm", realm.Realm)

	return p.client.UpdateRealm(ctx, realm.Realm, buildRealmBody(realm, existing))
}

// buildRealmBody builds the realm representation to send to Keycloak. existing
// is the realm's current representation, or nil when the realm is being
// created; it is only read to merge attributes (see mergeAttributes).
func buildRealmBody(realm config.Realm, existing map[string]any) map[string]any {
	body := map[string]any{
		"realm": realm.Realm,
	}

	if realm.DisplayName != "" {
		body["displayName"] = realm.DisplayName
	}
	if realm.Enabled != nil {
		body["enabled"] = *realm.Enabled
	}
	if realm.SslRequired != "" {
		body["sslRequired"] = realm.SslRequired
	}
	if realm.LoginTheme != "" {
		body["loginTheme"] = realm.LoginTheme
	}
	if realm.RegistrationAllowed != nil {
		body["registrationAllowed"] = *realm.RegistrationAllowed
	}
	if realm.ResetPasswordAllowed != nil {
		body["resetPasswordAllowed"] = *realm.ResetPasswordAllowed
	}
	if realm.OrganizationsEnabled != nil {
		body["organizationsEnabled"] = *realm.OrganizationsEnabled
	}
	if attrs := buildRealmAttributes(realm, existing); len(attrs) > 0 {
		body["attributes"] = attrs
	}

	return body
}

// buildRealmAttributes merges the configured realm attributes over the realm's
// current attributes. Keycloak replaces the whole attribute map on update and
// stores several realm settings there, so sending only the configured keys
// would silently drop everything else.
//
// It returns nil when the config declares no attributes, so realms without an
// attributes block keep Keycloak's existing behaviour untouched.
func buildRealmAttributes(realm config.Realm, existing map[string]any) map[string]string {
	acrLoaMap := buildAcrLoaMapAttribute(realm.AcrLoaMap)
	if len(realm.Attributes) == 0 && acrLoaMap == "" {
		return nil
	}

	attrs := mergeAttributes(existing, realm.Attributes)
	if acrLoaMap != "" {
		attrs[acrLoaMapAttr] = acrLoaMap
	}

	return attrs
}

func (p *Provisioner) ensureMasterRealm(ctx context.Context, mr *config.MasterRealmConfig) error {
	if mr.SslRequired != "" {
		existing, err := p.client.GetRealm(ctx, "master")
		if err != nil {
			return fmt.Errorf("getting master realm: %w", err)
		}
		if existing == nil {
			return fmt.Errorf("master realm not found (unexpected)")
		}

		currentSsl, _ := existing["sslRequired"].(string)
		if currentSsl != mr.SslRequired {
			slog.Info("Updating master realm sslRequired", "from", currentSsl, "to", mr.SslRequired)
			body := map[string]any{
				"realm":       "master",
				"sslRequired": mr.SslRequired,
			}
			if err := p.client.UpdateRealm(ctx, "master", body); err != nil {
				return fmt.Errorf("updating master realm: %w", err)
			}
		} else {
			slog.Info("Master realm sslRequired already set", "value", currentSsl)
		}
	}

	return nil
}
