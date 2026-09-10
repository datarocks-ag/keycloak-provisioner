package provisioner

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"keycloak-provisioner/internal/config"
)

// realmManagementClientID is the client that owns the authorization resource
// server behind fine-grained admin permissions. Every realm has one.
const realmManagementClientID = "realm-management"

// managementPolicyPrefix marks the client policies this provisioner owns.
//
// Ownership is what makes a configured client list authoritative: the
// provisioner replaces the clients on a policy it named itself, and never
// touches one it did not. The name has to be derivable from the config alone,
// so a run can find last run's policy without storing anything.
const managementPolicyPrefix = "keycloak-provisioner"

// affirmativeDecisionStrategy makes a scope permission grant when any one
// attached policy passes. Under Keycloak's UNANIMOUS default a permission
// carrying more than one policy grants only when every one of them passes, so a
// grant added alongside another policy would never take effect.
const affirmativeDecisionStrategy = "AFFIRMATIVE"

// managementPolicyName is the name of the policy granting one permission scope
// on one client. It is keyed by the target's clientId rather than its UUID so
// the policy is legible in the admin console; renaming a client's clientId
// therefore orphans its policy, which is the same trade every other name-keyed
// resource here makes.
func managementPolicyName(scope, targetClientID string) string {
	return fmt.Sprintf("%s.%s.%s", managementPolicyPrefix, scope, targetClientID)
}

// ensureManagementPermissions applies a client's fine-grained admin
// permissions: it turns them on, then grants each configured scope to the
// clients named for it.
//
// The reconcile is split between two ownership models, which is the whole
// design of this file:
//
//   - The policy for a (target, scope) pair is the provisioner's. Its client
//     list is replaced to match the config, so dropping a clientId withdraws
//     that grant on the next run.
//   - The permission's policy list is shared. The provisioner's policy is added
//     to whatever is already attached, so a policy created by hand or by
//     another tool keeps working.
//
// Nothing is deleted either way. A scope removed from the config stops being
// reconciled; its policy stays attached until someone removes it.
func (p *Provisioner) ensureManagementPermissions(
	ctx context.Context,
	realm, targetUUID string,
	c config.Client,
	clients clientUUIDIndex,
) error {
	mp := c.ManagementPermissions
	if mp == nil {
		return nil
	}

	scopePermissions, err := p.enableManagementPermissions(ctx, realm, targetUUID, c.ClientID)
	if err != nil {
		return err
	}

	// The resource server belongs to realm-management, not to the target: the
	// permissions and policies are all objects on that client.
	resourceServerUUID, err := p.resolveIndexedClientUUID(ctx, realm, clients, realmManagementClientID)
	if err != nil {
		return fmt.Errorf("resolving the %s client for client %q: %w", realmManagementClientID, c.ClientID, err)
	}

	// Sorted so a run's log and its dry-run report are reproducible; Go's map
	// iteration order is not.
	for _, scope := range sortedKeys(mp.Scopes) {
		permissionID, ok := scopePermissions[scope]
		if !ok {
			return fmt.Errorf("client %q: Keycloak offers no management permission scope %q (available: %s)",
				c.ClientID, scope, strings.Join(sortedKeys(scopePermissions), ", "))
		}

		if err := p.ensureManagementPermissionScope(
			ctx, realm, resourceServerUUID, permissionID, scope, c.ClientID, mp.Scopes[scope].Clients, clients,
		); err != nil {
			return err
		}
	}

	return nil
}

// enableManagementPermissions turns fine-grained permissions on for the client
// and returns the scope name to permission id map.
//
// Reading first is what keeps a settled realm free of writes: the PUT is
// idempotent and returns the same ids, but it is still a write, and a run that
// changes nothing should say so rather than do so.
func (p *Provisioner) enableManagementPermissions(ctx context.Context, realm, targetUUID, clientID string) (map[string]string, error) {
	current, err := p.client.GetClientManagementPermissions(ctx, realm, targetUUID)
	if err != nil {
		return nil, fmt.Errorf("reading management permissions for client %q: %w", clientID, err)
	}

	if enabled, _ := current["enabled"].(bool); enabled {
		slog.Debug("Management permissions already enabled", "realm", realm, "clientId", clientID)
		return scopePermissionIDs(current), nil
	}

	slog.Info("Enabling fine-grained management permissions", "realm", realm, "clientId", clientID)

	updated, err := p.client.SetClientManagementPermissions(ctx, realm, targetUUID, true)
	if err != nil {
		return nil, fmt.Errorf("enabling management permissions for client %q: %w", clientID, err)
	}

	return scopePermissionIDs(updated), nil
}

// ensureManagementPermissionScope grants one permission scope on one client to
// the named clients.
func (p *Provisioner) ensureManagementPermissionScope(
	ctx context.Context,
	realm, resourceServerUUID, permissionID, scope, targetClientID string,
	grantedClientIDs []string,
	clients clientUUIDIndex,
) error {
	grantedUUIDs := make([]string, 0, len(grantedClientIDs))

	for _, granted := range grantedClientIDs {
		uuid, err := p.resolveIndexedClientUUID(ctx, realm, clients, granted)
		if err != nil {
			return fmt.Errorf("resolving client %q granted %q on client %q: %w", granted, scope, targetClientID, err)
		}

		grantedUUIDs = append(grantedUUIDs, uuid)
	}

	policyID, err := p.ensureManagementPolicy(
		ctx, realm, resourceServerUUID, managementPolicyName(scope, targetClientID), scope, targetClientID, grantedClientIDs, grantedUUIDs,
	)
	if err != nil {
		return err
	}

	return p.attachManagementPolicy(ctx, realm, resourceServerUUID, permissionID, policyID, scope, targetClientID)
}

// ensureManagementPolicy creates or updates the client policy the provisioner
// owns for one (target, scope) pair, and returns its id.
//
// The client list is replaced rather than merged. That is the deliberate
// exception to this tool's additive rule, and it is safe only because the
// policy is named by the provisioner and reconciled by nobody else: a grant
// this config made can be withdrawn by editing this config, which a permission
// that only ever grows could not offer.
func (p *Provisioner) ensureManagementPolicy(
	ctx context.Context,
	realm, resourceServerUUID, name, scope, targetClientID string,
	grantedClientIDs, grantedUUIDs []string,
) (string, error) {
	existing, err := p.findManagementPolicy(ctx, realm, resourceServerUUID, name)
	if err != nil {
		return "", err
	}

	logArgs := []any{"realm", realm, "clientId", targetClientID, "scope", scope, "policy", name, "granted", strings.Join(grantedClientIDs, ", ")}

	if existing == nil {
		slog.Info("Creating management permission policy", logArgs...)

		id, err := p.client.CreateAuthzClientPolicy(ctx, realm, resourceServerUUID, map[string]any{
			"name":        name,
			"description": fmt.Sprintf("Managed by keycloak-provisioner: %s on %s", scope, targetClientID),
			"clients":     grantedUUIDs,
		})
		if err != nil {
			return "", fmt.Errorf("creating policy %q: %w", name, err)
		}

		return id, nil
	}

	id, ok := existing["id"].(string)
	if !ok || id == "" {
		return "", fmt.Errorf("policy %q: missing or invalid id in response", name)
	}

	if sameStringSet(stringsFrom(existing["clients"]), grantedUUIDs) {
		slog.Debug("Management permission policy already up to date", logArgs...)
		return id, nil
	}

	slog.Info("Updating management permission policy", logArgs...)

	// Merge over the current representation rather than sending a fresh one:
	// logic and decisionStrategy are the policy's own settings, and a sparse
	// body would reset them to Keycloak's defaults.
	body := make(map[string]any, len(existing)+1)
	for k, v := range existing {
		body[k] = v
	}

	body["clients"] = grantedUUIDs

	if err := p.client.UpdateAuthzClientPolicy(ctx, realm, resourceServerUUID, id, body); err != nil {
		return "", fmt.Errorf("updating policy %q: %w", name, err)
	}

	return id, nil
}

// findManagementPolicy returns the client policy with exactly this name, or nil.
//
// The search endpoint matches names as a substring, so a query for
// "keycloak-provisioner.view.app" also returns
// "keycloak-provisioner.view.app-admin". Filtering here is not an optimisation:
// without it the reconciler would adopt and rewrite a different client's policy.
func (p *Provisioner) findManagementPolicy(ctx context.Context, realm, resourceServerUUID, name string) (map[string]any, error) {
	matches, err := p.client.GetAuthzClientPolicies(ctx, realm, resourceServerUUID, name)
	if err != nil {
		return nil, fmt.Errorf("searching for policy %q: %w", name, err)
	}

	for _, m := range matches {
		if got, _ := m["name"].(string); got == name {
			return m, nil
		}
	}

	return nil, nil
}

// attachManagementPolicy adds the policy to the scope permission, leaving any
// other policy attached to it in place.
func (p *Provisioner) attachManagementPolicy(
	ctx context.Context,
	realm, resourceServerUUID, permissionID, policyID, scope, targetClientID string,
) error {
	associated, err := p.client.GetAuthzAssociatedPolicies(ctx, realm, resourceServerUUID, permissionID)
	if err != nil {
		return fmt.Errorf("reading policies attached to %q on client %q: %w", scope, targetClientID, err)
	}

	attached := make([]string, 0, len(associated)+1)

	for _, a := range associated {
		if id, ok := a["id"].(string); ok && id != "" {
			attached = append(attached, id)
		}
	}

	logArgs := []any{"realm", realm, "clientId", targetClientID, "scope", scope}

	alreadyAttached := false
	others := make([]string, 0, len(attached))

	for _, id := range attached {
		if id == policyID {
			alreadyAttached = true
			continue
		}

		others = append(others, id)
	}

	// Read the permission even when the policy is already attached. Its decision
	// strategy is part of what makes the grant effective, and it can be changed
	// out of band: a permission left at UNANIMOUS with a second policy attached
	// grants only when both pass, so a grant this config made would quietly stop
	// working. Returning early on the attachment alone would leave that
	// unrepaired for as long as nobody looked.
	permission, err := p.client.GetAuthzScopePermission(ctx, realm, resourceServerUUID, permissionID)
	if err != nil {
		return fmt.Errorf("reading the %q permission on client %q: %w", scope, targetClientID, err)
	}

	strategy, _ := permission["decisionStrategy"].(string)

	if alreadyAttached && strategy == affirmativeDecisionStrategy {
		slog.Debug("Management permission policy already attached", logArgs...)

		return nil
	}

	// The permission's representation omits its policies on read but replaces
	// them on write, so the full set has to be sent back or everything already
	// attached is silently detached.
	body := make(map[string]any, len(permission)+2)
	for k, v := range permission {
		body[k] = v
	}

	if alreadyAttached {
		body["policies"] = attached
	} else {
		body["policies"] = append(attached, policyID)
	}

	// Loosening only matters to report when something else is attached: with one
	// policy the two strategies decide identically.
	if strategy != affirmativeDecisionStrategy && len(others) > 0 {
		slog.Warn("Loosening the permission's decision strategy to AFFIRMATIVE; policies already attached now grant independently",
			append(logArgs, "was", strategy, "otherPolicies", len(others))...)
	}

	body["decisionStrategy"] = affirmativeDecisionStrategy

	if alreadyAttached {
		slog.Info("Correcting management permission decision strategy", append(logArgs, "was", strategy)...)
	} else {
		slog.Info("Attaching management permission policy", append(logArgs, "policy", policyID)...)
	}

	if err := p.client.UpdateAuthzScopePermission(ctx, realm, resourceServerUUID, permissionID, body); err != nil {
		return fmt.Errorf("attaching a policy to %q on client %q: %w", scope, targetClientID, err)
	}

	return nil
}

// scopePermissionIDs reads the scope name to permission id map out of a
// management permission representation. It is absent when permissions are
// disabled, which the caller has already ruled out.
func scopePermissionIDs(rep map[string]any) map[string]string {
	raw, _ := rep["scopePermissions"].(map[string]any)
	ids := make(map[string]string, len(raw))

	for scope, v := range raw {
		if id, ok := v.(string); ok {
			ids[scope] = id
		}
	}

	return ids
}

// stringsFrom reads a JSON array of strings out of an untyped representation.
func stringsFrom(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))

	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}

	return out
}

// sameStringSet reports whether two lists hold the same values, ignoring order
// and duplicates. Keycloak does not preserve the order of a policy's clients,
// so comparing the lists directly would rewrite the policy on every run.
//
// Comparing sets in both directions rather than deleting from one as the other
// is walked: deletion makes a repeated value in b look absent the second time
// it appears, which is the opposite of ignoring duplicates.
func sameStringSet(a, b []string) bool {
	inA := stringSet(a)
	inB := stringSet(b)

	if len(inA) != len(inB) {
		return false
	}

	for s := range inA {
		if !inB[s] {
			return false
		}
	}

	return true
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, s := range values {
		set[s] = true
	}

	return set
}

// sortedKeys returns a map's keys in order, so logs and dry-run output do not
// depend on Go's randomised map iteration.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}

// clientUUIDIndex maps a realm's clientIds to their UUIDs.
//
// It is seeded from the client loop, which already knows the UUID of every
// client the config declares, and filled in for anything else the first time it
// is asked for. Without it a grantee named by three scopes costs three
// identical lookups, and realm-management costs one per target client — the
// same re-listing the client scope and identity provider indexes exist to
// avoid.
type clientUUIDIndex map[string]string

// resolveIndexedClientUUID returns a client's UUID, consulting the index before
// asking Keycloak and remembering what it had to ask for.
//
// A nil index is valid and simply memoises nothing, so a caller that has no
// index — a test, or a path where one would not pay for itself — needs no
// special case.
func (p *Provisioner) resolveIndexedClientUUID(
	ctx context.Context,
	realm string,
	index clientUUIDIndex,
	clientID string,
) (string, error) {
	if uuid, ok := index[clientID]; ok {
		return uuid, nil
	}

	uuid, err := p.resolveClientUUID(ctx, realm, clientID)
	if err != nil {
		return "", err
	}

	if index != nil {
		index[clientID] = uuid
	}

	return uuid, nil
}
