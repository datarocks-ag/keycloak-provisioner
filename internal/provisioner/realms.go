package provisioner

import (
	"context"
	"log/slog"

	"keycloak-provisioner/internal/config"
)

func (p *Provisioner) ensureRealm(ctx context.Context, realm config.Realm, strategy string) error {
	existing, err := p.client.GetRealm(ctx, realm.Realm)
	if err != nil {
		return err
	}

	body := buildRealmBody(realm)

	if existing == nil {
		slog.Info("Creating realm", "realm", realm.Realm)
		return p.client.CreateRealm(ctx, body)
	}

	if strategy == "create" {
		slog.Info("Skipping existing realm (strategy=create)", "realm", realm.Realm)
		return nil
	}

	slog.Info("Updating realm", "realm", realm.Realm)
	return p.client.UpdateRealm(ctx, realm.Realm, body)
}

func buildRealmBody(realm config.Realm) map[string]any {
	body := map[string]any{
		"realm": realm.Realm,
	}

	if realm.DisplayName != "" {
		body["displayName"] = realm.DisplayName
	}
	if realm.Enabled != nil {
		body["enabled"] = *realm.Enabled
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

	return body
}
