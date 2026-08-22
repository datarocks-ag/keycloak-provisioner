package provisioner

import (
	"context"
	"fmt"
	"log/slog"

	"keycloak-provisioner/internal/config"
)

// ensureOrganization creates or updates a single organization and reconciles
// its membership. Organizations are looked up by name; the alias Keycloak
// derives from the name is immutable once the organization exists, so it is
// only sent on create.
func (p *Provisioner) ensureOrganization(
	ctx context.Context,
	realm string,
	o config.Organization,
	strategy string,
	idps identityProviderIndex,
) error {
	existing, err := p.client.GetOrganizations(ctx, realm, o.Name)
	if err != nil {
		return err
	}

	var orgID string

	if len(existing) == 0 {
		slog.Info("Creating organization", "realm", realm, "organization", o.Name)

		orgID, err = p.client.CreateOrganization(ctx, realm, buildOrganizationCreateBody(o))
		if err != nil {
			return err
		}
	} else {
		orgID, err = organizationID(existing[0], o.Name)
		if err != nil {
			return err
		}

		if strategy == "create" {
			slog.Info("Skipping existing organization (strategy=create)", "realm", realm, "organization", o.Name)
		} else {
			slog.Info("Updating organization", "realm", realm, "organization", o.Name, "uuid", orgID)

			// The search listing omits attributes, so the merge reads the full
			// representation rather than reusing what the lookup returned.
			current, err := p.client.GetOrganization(ctx, realm, orgID)
			if err != nil {
				return fmt.Errorf("reading organization %q: %w", o.Name, err)
			}

			body, err := buildOrganizationUpdateBody(o, current)
			if err != nil {
				return err
			}

			body["id"] = orgID

			if err := p.client.UpdateOrganization(ctx, realm, orgID, body); err != nil {
				return err
			}
		}
	}

	if err := p.ensureOrganizationMembers(ctx, realm, orgID, o); err != nil {
		return err
	}

	if err := p.ensureOrganizationIdentityProviders(ctx, realm, orgID, o, idps); err != nil {
		return err
	}

	if len(o.Groups) == 0 {
		return nil
	}

	org := organizationContext{id: orgID, name: o.Name}

	// Read the organization's members once, after they have been reconciled,
	// and carry them through the group recursion. The listing already carries
	// each member's user id, so the group path needs no user lookups of its
	// own and no repeated member reads per subgroup. Groups that assign no
	// members need none of this, so the read is skipped entirely.
	if groupsAssignMembers(o.Groups) {
		members, err := p.client.GetOrganizationMembers(ctx, realm, orgID)
		if err != nil {
			return err
		}

		org.members = userIDsByUsername(members)
	}

	for _, g := range o.Groups {
		if err := p.ensureOrganizationGroup(ctx, realm, org, "", g, strategy); err != nil {
			return fmt.Errorf("group %q of organization %q: %w", g.Name, o.Name, err)
		}
	}

	return nil
}

// organizationContext carries the per-organization state that the group
// recursion needs, so it is resolved once rather than per group.
type organizationContext struct {
	id      string
	name    string
	members map[string]string // username -> user id; nil when no group assigns members
}

// groupsAssignMembers reports whether any group in the tree declares members.
func groupsAssignMembers(groups []config.OrganizationGroup) bool {
	for _, g := range groups {
		if len(g.Members) > 0 || groupsAssignMembers(g.SubGroups) {
			return true
		}
	}

	return false
}

// ensureOrganizationGroup reconciles one organization group and, recursively,
// its subgroups. parentID is empty for a top-level group.
//
// It mirrors ensureGroup for realm groups, with one difference: organization
// groups carry no role mappings, so there is nothing to reconcile beyond
// attributes, members and subgroups. Keycloak 26.7 does expose a role-mapping
// endpoint for them, but it is inert — the mapping reaches neither a member's
// effective roles nor any token claim — so config declaring one is rejected at
// load rather than written here.
func (p *Provisioner) ensureOrganizationGroup(
	ctx context.Context,
	realm string,
	org organizationContext,
	parentID string,
	g config.OrganizationGroup,
	strategy string,
) error {
	groupID, err := p.reconcileOrganizationGroup(ctx, realm, org.id, parentID, g, strategy)
	if err != nil {
		return err
	}

	if err := p.ensureOrganizationGroupMembers(ctx, realm, org, groupID, g); err != nil {
		return fmt.Errorf("members: %w", err)
	}

	for _, sub := range g.SubGroups {
		if err := p.ensureOrganizationGroup(ctx, realm, org, groupID, sub, strategy); err != nil {
			return fmt.Errorf("subgroup %q: %w", sub.Name, err)
		}
	}

	return nil
}

// reconcileOrganizationGroup creates or updates the group itself and returns its
// UUID. Creating a group whose name is already taken is a 409, so the group is
// looked up by name at its own level first.
func (p *Provisioner) reconcileOrganizationGroup(
	ctx context.Context,
	realm, orgID, parentID string,
	g config.OrganizationGroup,
	strategy string,
) (string, error) {
	existing, err := p.findOrganizationGroup(ctx, realm, orgID, parentID, g.Name)
	if err != nil {
		return "", err
	}

	body := buildOrganizationGroupBody(g)

	if existing == nil {
		if parentID == "" {
			slog.Info("Creating organization group", "realm", realm, "group", g.Name)
		} else {
			slog.Info("Creating organization subgroup", "realm", realm, "group", g.Name, "parent", parentID)
		}

		return p.client.CreateOrganizationGroup(ctx, realm, orgID, parentID, body)
	}

	groupID, ok := existing["id"].(string)
	if !ok {
		return "", fmt.Errorf("organization group %q: missing or invalid id in response", g.Name)
	}

	if strategy == "create" {
		// Only the group's own attributes are left untouched; the caller still
		// reconciles members and subgroups, which are additive children.
		slog.Info("Skipping existing organization group update (strategy=create)", "realm", realm, "group", g.Name)
		return groupID, nil
	}

	slog.Info("Updating organization group", "realm", realm, "group", g.Name, "uuid", groupID)
	body["id"] = groupID

	if err := p.client.UpdateOrganizationGroup(ctx, realm, orgID, groupID, body); err != nil {
		return "", err
	}

	return groupID, nil
}

// findOrganizationGroup looks up a group by exact name at the given level.
// Keycloak's ?search on the groups endpoint matches across the whole tree, so
// the children endpoint is used for nested levels rather than a search.
func (p *Provisioner) findOrganizationGroup(ctx context.Context, realm, orgID, parentID, name string) (map[string]any, error) {
	candidates, err := p.client.GetOrganizationGroups(ctx, realm, orgID, parentID)
	if err != nil {
		return nil, err
	}

	for _, c := range candidates {
		if n, ok := c["name"].(string); ok && n == name {
			return c, nil
		}
	}

	return nil, nil
}

func buildOrganizationGroupBody(g config.OrganizationGroup) map[string]any {
	body := map[string]any{
		"name": g.Name,
	}
	if len(g.Attributes) > 0 {
		body["attributes"] = g.Attributes
	}

	return body
}

// ensureOrganizationGroupMembers adds the configured users to an organization
// group. Membership is additive and never removed.
//
// A user has to be a member of the organization before it can join one of its
// groups — Keycloak answers 400 otherwise — so a member the organization does
// not have is warned about and skipped rather than failing the run. User ids
// come from the organization's member listing, which the caller resolved once.
func (p *Provisioner) ensureOrganizationGroupMembers(
	ctx context.Context,
	realm string,
	org organizationContext,
	groupID string,
	g config.OrganizationGroup,
) error {
	if len(g.Members) == 0 {
		return nil
	}

	groupMembers, err := p.client.GetOrganizationGroupMembers(ctx, realm, org.id, groupID)
	if err != nil {
		return err
	}
	inGroup := usernameSet(groupMembers)

	seen := make(map[string]bool, len(g.Members))

	for _, username := range g.Members {
		if seen[username] {
			continue
		}
		seen[username] = true

		if inGroup[username] {
			slog.Debug("User already a member of organization group", "realm", realm, "group", g.Name, "username", username)
			continue
		}

		userID, ok := org.members[username]
		if !ok {
			slog.Warn("User is not a member of the organization, skipping group membership",
				"realm", realm, "organization", org.name, "group", g.Name, "username", username)

			continue
		}

		slog.Info("Adding user to organization group", "realm", realm, "organization", org.name, "group", g.Name, "username", username)

		if err := p.client.AddOrganizationGroupMember(ctx, realm, org.id, groupID, userID); err != nil {
			return err
		}
	}

	return nil
}

// userIDsByUsername indexes a user listing by username. Entries without both a
// username and an id are skipped rather than producing an unusable mapping.
func userIDsByUsername(users []map[string]any) map[string]string {
	ids := make(map[string]string, len(users))

	for _, u := range users {
		username, nameOK := u["username"].(string)
		id, idOK := u["id"].(string)

		if nameOK && idOK {
			ids[username] = id
		}
	}

	return ids
}

func usernameSet(users []map[string]any) map[string]bool {
	set := make(map[string]bool, len(users))
	for _, u := range users {
		if username, ok := u["username"].(string); ok {
			set[username] = true
		}
	}

	return set
}

func organizationID(org map[string]any, name string) (string, error) {
	id, ok := org["id"].(string)
	if !ok {
		return "", fmt.Errorf("organization %q: missing or invalid id in response", name)
	}
	return id, nil
}

// buildOrganizationCreateBody builds the representation for a new organization.
// Nothing exists to preserve, so only what the config declares is sent and
// Keycloak fills in the rest — including deriving an alias when none is given.
func buildOrganizationCreateBody(o config.Organization) map[string]any {
	body := map[string]any{"name": o.Name}

	if o.Alias != "" {
		body["alias"] = o.Alias
	}

	// Sent as declared: there is no stored map to merge with yet.
	if len(o.Attributes) > 0 {
		body["attributes"] = o.Attributes
	}

	applyOrganizationConfig(body, o)

	return body
}

// buildOrganizationUpdateBody merges the configured organization over its
// current representation.
//
// Keycloak's organization update is not a sparse patch, and it is inconsistent
// about how it says so. Measured against 26.6:
//
//   - alias omitted is rejected with 400 "Cannot change the alias" — the
//     message reads as though something tried to change it, when in fact
//     nothing supplied it and the absence is read as setting it to null.
//   - domains and redirectUrl omitted are silently cleared.
//   - attributes omitted are preserved, but attributes supplied replace the
//     whole map rather than merging into it.
//
// Merging over the current representation is the one shape that satisfies all
// four, and it matches what identity providers already do.
func buildOrganizationUpdateBody(o config.Organization, current map[string]any) (map[string]any, error) {
	body := make(map[string]any, len(current)+6)

	for k, v := range current {
		body[k] = v
	}

	// The alias is immutable. Keycloak's own refusal names neither the
	// configured alias nor the stored one, so the mismatch is caught here.
	if stored, _ := current["alias"].(string); o.Alias != "" && stored != "" && o.Alias != stored {
		return nil, fmt.Errorf(
			"organization %q: alias is %q on the server but %q in the config, and Keycloak does not allow it to change; "+
				"either restore the configured value or remove the alias from the config",
			o.Name, stored, o.Alias)
	}

	body["name"] = o.Name

	applyOrganizationConfig(body, o)

	// Configured attributes merge over the stored ones rather than replacing
	// them, so a key set out of band survives — the same rule realm and client
	// attributes follow.
	if len(o.Attributes) > 0 {
		body["attributes"] = mergeMultiValueField(current, "attributes", o.Attributes)
	}

	return body, nil
}

// applyOrganizationConfig overlays the fields a config may declare that both
// paths treat identically. Attributes are not among them: create sends them as
// declared, update merges them over the stored map, so each caller sets them
// itself.
func applyOrganizationConfig(body map[string]any, o config.Organization) {
	if o.Enabled != nil {
		body["enabled"] = *o.Enabled
	}
	if o.Description != "" {
		body["description"] = o.Description
	}
	if o.RedirectUrl != "" {
		body["redirectUrl"] = o.RedirectUrl
	}
	if len(o.Domains) > 0 {
		domains := make([]map[string]any, 0, len(o.Domains))

		for _, d := range o.Domains {
			domain := map[string]any{"name": d.Name}
			if d.Verified != nil {
				domain["verified"] = *d.Verified
			}

			domains = append(domains, domain)
		}

		body["domains"] = domains
	}
}

// ensureOrganizationMembers adds the configured users to the organization.
// Membership is additive — existing members are never removed — and a username
// that cannot be resolved is warned about and skipped, matching how
// ensureUserGroups treats a missing group.
func (p *Provisioner) ensureOrganizationMembers(ctx context.Context, realm, orgID string, o config.Organization) error {
	if len(o.Members) == 0 {
		return nil
	}

	members, err := p.client.GetOrganizationMembers(ctx, realm, orgID)
	if err != nil {
		return err
	}

	current := usernameSet(members)

	seen := make(map[string]bool, len(o.Members))

	for _, username := range o.Members {
		if seen[username] {
			continue
		}
		seen[username] = true

		if current[username] {
			slog.Debug("User already a member of organization", "realm", realm, "organization", o.Name, "username", username)
			continue
		}

		users, err := p.client.GetUsers(ctx, realm, username)
		if err != nil {
			return err
		}
		if len(users) == 0 {
			slog.Warn("User not found, skipping organization membership", "realm", realm, "organization", o.Name, "username", username)
			continue
		}

		userID, ok := users[0]["id"].(string)
		if !ok {
			return fmt.Errorf("user %q: missing or invalid id in response", username)
		}

		slog.Info("Adding user to organization", "realm", realm, "organization", o.Name, "username", username)

		if err := p.client.AddOrganizationMember(ctx, realm, orgID, userID); err != nil {
			return err
		}
	}

	return nil
}

// ensureOrganizationIdentityProviders links the organization's configured
// identity providers. Linking is additive: a provider already linked is left
// alone and none is ever unlinked.
//
// Unlike a missing member, which is warned about and skipped, an alias that
// names no provider in the realm fails the run. A user absent from Keycloak is
// plausible drift in an environment the provisioner does not fully own; an
// alias that resolves to nothing is a config error, and Keycloak's own answer
// for it — a 400 naming neither the alias nor the organization — is not worth
// surfacing.
func (p *Provisioner) ensureOrganizationIdentityProviders(
	ctx context.Context,
	realm, orgID string,
	o config.Organization,
	known identityProviderIndex,
) error {
	if len(o.IdentityProviders) == 0 {
		return nil
	}

	linked, err := p.client.GetOrganizationIdentityProviders(ctx, realm, orgID)
	if err != nil {
		return err
	}

	current := make(map[string]bool, len(linked))

	for _, idp := range linked {
		if alias, ok := idp["alias"].(string); ok {
			current[alias] = true
		}
	}

	for _, alias := range o.IdentityProviders {
		if current[alias] {
			slog.Debug("Identity provider already linked to organization",
				"realm", realm, "organization", o.Name, "identityProvider", alias)

			continue
		}

		provider, ok := known[alias]
		if !ok {
			return fmt.Errorf("organization %q: identity provider %q does not exist in realm %q",
				o.Name, alias, realm)
		}

		// The listing carries the owning organization, so a provider claimed
		// elsewhere can be named here. Keycloak's own answer is a bare 400 that
		// mentions neither the provider nor either organization.
		//
		// Config validation already rejects two organizations in one config
		// claiming the same alias; this is the other case, where the provider
		// was linked to an organization the config does not describe.
		if owner, _ := provider["organizationId"].(string); owner != "" && owner != orgID {
			return fmt.Errorf(
				"organization %q: identity provider %q is already linked to organization %s; "+
					"Keycloak allows a provider to belong to at most one",
				o.Name, alias, owner)
		}

		slog.Info("Linking identity provider to organization",
			"realm", realm, "organization", o.Name, "identityProvider", alias)

		if err := p.client.AddOrganizationIdentityProvider(ctx, realm, orgID, alias); err != nil {
			return fmt.Errorf("linking identity provider %q to organization %q: %w", alias, o.Name, err)
		}

		current[alias] = true
	}

	return nil
}
