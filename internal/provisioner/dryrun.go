package provisioner

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

const dryRunIDPrefix = "dryrun-"

// NewDryRunAdapter wraps a KeycloakAPI so that mutating operations are logged
// at INFO with a "DRY-RUN" marker and skipped. Read operations pass through.
//
// Resources that "would be created" return synthetic UUIDs prefixed with
// "dryrun-". Subsequent reads on those UUIDs (or on resources nested inside
// a would-be-created realm) short-circuit so dependent operations can
// continue, allowing the full intended provisioning sequence to be reported
// in one run.
//
// Caveats:
//   - Drift between current state and config IS visible (real reads pass through).
//   - Roles created in the same dry-run are tracked, so user role assignments
//     against newly-created roles do not error.
//   - Errors from the wrapped API propagate only for operations that still call it
//     (pass-through reads); skipped mutating operations always return nil.
//   - Authentication flows report their creates and the executions that would be
//     added. Requirements and execution config are set by reading the created
//     execution back, which is not possible for a flow that does not exist yet,
//     so those steps are not reported for a would-be-created flow.
func NewDryRunAdapter(inner KeycloakAPI) KeycloakAPI {
	return &dryRunAPI{
		inner:              inner,
		createdRealms:      make(map[string]bool),
		createdClients:     make(map[clientKey]string),
		createdSA:          make(map[saKey]string),
		createdRealmRoles:  make(map[realmRoleKey]string),
		createdClientRoles: make(map[clientRoleKey]string),
		createdGroups:      make(map[groupKey]string),
		createdScopes:      make(map[clientScopeKey]string),
		createdFlows:       make(map[flowKey]string),
		createdUsers:       make(map[userKey]string),
		createdOrgMembers:  make(map[orgMemberKey]bool),
		createdIdPs:        make(map[idpKey]map[string]any),
		createdPolicies:    make(map[policyKey]map[string]any),
	}
}

type (
	clientKey      struct{ realm, clientID string }
	saKey          struct{ realm, clientUUID string }
	realmRoleKey   struct{ realm, name string }
	clientRoleKey  struct{ realm, clientUUID, name string }
	groupKey       struct{ realm, parentID, name string } // parentID "" for top-level
	clientScopeKey struct{ realm, name string }
	flowKey        struct{ realm, alias string }
	userKey        struct{ realm, username string }
	orgMemberKey   struct{ realm, orgID, userID string }
	idpKey         struct{ realm, alias string }
	policyKey      struct{ realm, resourceServerUUID, name string }
)

type dryRunAPI struct {
	inner KeycloakAPI
	seq   atomic.Uint64

	mu                 sync.Mutex
	createdRealms      map[string]bool
	createdClients     map[clientKey]string
	createdSA          map[saKey]string
	createdRealmRoles  map[realmRoleKey]string      // -> synthetic role id
	createdClientRoles map[clientRoleKey]string     // -> synthetic role id
	createdGroups      map[groupKey]string          // -> synthetic group id
	createdScopes      map[clientScopeKey]string    // -> synthetic client scope id
	createdFlows       map[flowKey]string           // -> synthetic authentication flow id
	createdUsers       map[userKey]string           // -> synthetic user id
	createdOrgMembers  map[orgMemberKey]bool        // organization memberships added this run
	createdIdPs        map[idpKey]map[string]any    // -> the representation that would have been created
	createdPolicies    map[policyKey]map[string]any // management permission policies created this run
}

func (d *dryRunAPI) realmIsSynthetic(realm string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.createdRealms[realm]
}

func (d *dryRunAPI) newID(kind string) string {
	return fmt.Sprintf("%s%s-%d", dryRunIDPrefix, kind, d.seq.Add(1))
}

func isSyntheticID(s string) bool {
	return strings.HasPrefix(s, dryRunIDPrefix)
}

// Realms.

func (d *dryRunAPI) GetRealm(ctx context.Context, name string) (map[string]any, error) {
	return d.inner.GetRealm(ctx, name)
}

func (d *dryRunAPI) CreateRealm(_ context.Context, body map[string]any) error {
	name, _ := body["realm"].(string)
	slog.Info("DRY-RUN: would create realm", "realm", name)
	d.mu.Lock()
	d.createdRealms[name] = true
	d.mu.Unlock()
	return nil
}

func (d *dryRunAPI) UpdateRealm(_ context.Context, name string, _ map[string]any) error {
	slog.Info("DRY-RUN: would update realm", "realm", name)
	return nil
}

// Clients.

func (d *dryRunAPI) GetClients(ctx context.Context, realm, clientID string) ([]map[string]any, error) {
	d.mu.Lock()
	uuid, found := d.createdClients[clientKey{realm, clientID}]
	d.mu.Unlock()
	if found {
		return []map[string]any{{"id": uuid, "clientId": clientID}}, nil
	}
	if d.realmIsSynthetic(realm) {
		// Keycloak creates realm-management with every realm, so it exists in
		// a realm that would be created here. Without this, fine-grained
		// management permissions could not be reported for a new realm: the
		// resource server they hang off would look absent.
		if clientID == realmManagementClientID {
			return []map[string]any{{"id": d.syntheticRealmManagement(realm), "clientId": clientID}}, nil
		}

		return nil, nil
	}
	return d.inner.GetClients(ctx, realm, clientID)
}

// syntheticRealmManagement returns a stable synthetic UUID for a realm's
// realm-management client, so repeated lookups within one dry-run agree.
func (d *dryRunAPI) syntheticRealmManagement(realm string) string {
	d.mu.Lock()
	defer d.mu.Unlock()

	key := clientKey{realm, realmManagementClientID}
	if uuid, ok := d.createdClients[key]; ok {
		return uuid
	}

	uuid := d.newID("client")
	d.createdClients[key] = uuid

	return uuid
}

func (d *dryRunAPI) CreateClient(_ context.Context, realm string, body map[string]any) (string, error) {
	clientID, _ := body["clientId"].(string)
	slog.Info("DRY-RUN: would create client", "realm", realm, "clientId", clientID)
	uuid := d.newID("client")
	d.mu.Lock()
	d.createdClients[clientKey{realm, clientID}] = uuid
	d.mu.Unlock()
	return uuid, nil
}

func (d *dryRunAPI) UpdateClient(_ context.Context, realm, uuid string, body map[string]any) error {
	slog.Info("DRY-RUN: would update client", "realm", realm, "uuid", uuid, "clientId", body["clientId"])
	return nil
}

// Realm roles.

func (d *dryRunAPI) GetRealmRole(ctx context.Context, realm, name string) (map[string]any, error) {
	d.mu.Lock()
	id, found := d.createdRealmRoles[realmRoleKey{realm, name}]
	d.mu.Unlock()
	if found {
		return map[string]any{"name": name, "id": id}, nil
	}
	if d.realmIsSynthetic(realm) {
		return nil, nil
	}
	return d.inner.GetRealmRole(ctx, realm, name)
}

func (d *dryRunAPI) CreateRealmRole(_ context.Context, realm string, body map[string]any) error {
	name, _ := body["name"].(string)
	slog.Info("DRY-RUN: would create realm role", "realm", realm, "role", name)
	id := d.newID("realm-role")
	d.mu.Lock()
	d.createdRealmRoles[realmRoleKey{realm, name}] = id
	d.mu.Unlock()
	return nil
}

func (d *dryRunAPI) UpdateRealmRole(_ context.Context, realm, name string, _ map[string]any) error {
	slog.Info("DRY-RUN: would update realm role", "realm", realm, "role", name)
	return nil
}

// Client roles.

func (d *dryRunAPI) GetClientRole(ctx context.Context, realm, clientUUID, name string) (map[string]any, error) {
	d.mu.Lock()
	id, found := d.createdClientRoles[clientRoleKey{realm, clientUUID, name}]
	d.mu.Unlock()
	if found {
		return map[string]any{"name": name, "id": id, "containerId": clientUUID}, nil
	}
	if isSyntheticID(clientUUID) || d.realmIsSynthetic(realm) {
		return nil, nil
	}
	return d.inner.GetClientRole(ctx, realm, clientUUID, name)
}

func (d *dryRunAPI) CreateClientRole(_ context.Context, realm, clientUUID string, body map[string]any) error {
	name, _ := body["name"].(string)
	slog.Info("DRY-RUN: would create client role", "realm", realm, "clientUUID", clientUUID, "role", name)
	id := d.newID("client-role")
	d.mu.Lock()
	d.createdClientRoles[clientRoleKey{realm, clientUUID, name}] = id
	d.mu.Unlock()
	return nil
}

func (d *dryRunAPI) UpdateClientRole(_ context.Context, realm, clientUUID, name string, _ map[string]any) error {
	slog.Info("DRY-RUN: would update client role", "realm", realm, "clientUUID", clientUUID, "role", name)
	return nil
}

// Protocol mappers.

func (d *dryRunAPI) GetProtocolMappers(ctx context.Context, realm, container, containerID string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) || isSyntheticID(containerID) {
		return nil, nil
	}

	return d.inner.GetProtocolMappers(ctx, realm, container, containerID)
}

func (d *dryRunAPI) CreateProtocolMapper(_ context.Context, realm, container, containerID string, body map[string]any) error {
	slog.Info("DRY-RUN: would create protocol mapper",
		"realm", realm, "container", container, "containerID", containerID, "mapper", body["name"])

	return nil
}

func (d *dryRunAPI) UpdateProtocolMapper(_ context.Context, realm, container, containerID, mapperID string, body map[string]any) error {
	slog.Info("DRY-RUN: would update protocol mapper",
		"realm", realm, "container", container, "containerID", containerID, "mapper", body["name"], "uuid", mapperID)

	return nil
}

// Users.

// GetUsers reports the realm's real users plus any created earlier in this
// dry-run. Without the synthetic ones, a step that resolves a username — adding
// a member to an organization, for instance — would treat a user created
// moments ago as missing and silently drop that part of the report.
func (d *dryRunAPI) GetUsers(ctx context.Context, realm, username string) ([]map[string]any, error) {
	var users []map[string]any

	if !d.realmIsSynthetic(realm) {
		var err error

		users, err = d.inner.GetUsers(ctx, realm, username)
		if err != nil {
			return nil, err
		}
	}

	if id, ok := d.syntheticUser(realm, username); ok {
		users = append(users, map[string]any{"id": id, "username": username})
	}

	return users, nil
}

// syntheticUser returns the id of a user that would have been created in this
// run, if any.
func (d *dryRunAPI) syntheticUser(realm, username string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	id, ok := d.createdUsers[userKey{realm, username}]

	return id, ok
}

func (d *dryRunAPI) CreateUser(_ context.Context, realm string, body map[string]any) (string, error) {
	username, _ := body["username"].(string)
	slog.Info("DRY-RUN: would create user", "realm", realm, "username", username)

	id := d.newID("user")

	d.mu.Lock()
	d.createdUsers[userKey{realm, username}] = id
	d.mu.Unlock()

	return id, nil
}

// PartialImportUsers is how a user with a chosen id is created, so the id it
// would get is the one the config asked for, not a synthetic one. Recording it
// keeps later steps that resolve the username — organization membership, for
// instance — reporting correctly.
func (d *dryRunAPI) PartialImportUsers(_ context.Context, realm string, users []map[string]any) error {
	for _, u := range users {
		username, _ := u["username"].(string)
		id, _ := u["id"].(string)

		slog.Info("DRY-RUN: would import user", "realm", realm, "username", username, "userID", id)

		if username != "" && id != "" {
			d.mu.Lock()
			d.createdUsers[userKey{realm, username}] = id
			d.mu.Unlock()
		}
	}

	return nil
}

// UpdateUser reports a credential-only update as what it is. Seeding a
// credential goes through the user update, and "would update user" would tell
// a reader nothing about the one thing that is actually changing.
func (d *dryRunAPI) UpdateUser(_ context.Context, realm, userID string, body map[string]any) error {
	if creds, ok := body["credentials"].([]map[string]any); ok && len(body) == 1 {
		slog.Info("DRY-RUN: would seed user credentials",
			"realm", realm, "userID", userID, "credentials", credentialLabels(creds))

		return nil
	}

	slog.Info("DRY-RUN: would update user", "realm", realm, "userID", userID, "username", body["username"])

	return nil
}

// credentialLabels names the credentials in a seeding update, preferring the
// label since a user may hold more than one of a type.
func credentialLabels(creds []map[string]any) []string {
	out := make([]string, 0, len(creds))

	for _, c := range creds {
		credType, _ := c["type"].(string)

		if label, ok := c["userLabel"].(string); ok && label != "" {
			out = append(out, credType+"/"+label)
			continue
		}

		out = append(out, credType)
	}

	return out
}

func (d *dryRunAPI) ResetUserPassword(_ context.Context, realm, userID string, _ string, temporary bool) error {
	slog.Info("DRY-RUN: would reset user password", "realm", realm, "userID", userID, "temporary", temporary)
	return nil
}

// Group memberships.

// GetUserCredentials short-circuits for a user that does not exist yet: there
// are no credentials to compare against, so every configured one is reported
// as a create.
func (d *dryRunAPI) GetUserCredentials(ctx context.Context, realm, userID string) ([]map[string]any, error) {
	if isSyntheticID(userID) || d.realmIsSynthetic(realm) {
		return nil, nil
	}
	return d.inner.GetUserCredentials(ctx, realm, userID)
}

func (d *dryRunAPI) GetRealmScopeMappings(ctx context.Context, realm, owner, ownerID string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) || isSyntheticID(ownerID) {
		return nil, nil
	}

	return d.inner.GetRealmScopeMappings(ctx, realm, owner, ownerID)
}

func (d *dryRunAPI) AddRealmScopeMappings(_ context.Context, realm, owner, ownerID string, roles []map[string]any) error {
	slog.Info("DRY-RUN: would add realm roles to scope",
		"realm", realm, "owner", owner, "ownerID", ownerID, "roles", roleNames(roles))

	return nil
}

func (d *dryRunAPI) GetClientScopeMappings(ctx context.Context, realm, owner, ownerID, clientUUID string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) || isSyntheticID(ownerID) || isSyntheticID(clientUUID) {
		return nil, nil
	}

	return d.inner.GetClientScopeMappings(ctx, realm, owner, ownerID, clientUUID)
}

func (d *dryRunAPI) AddClientScopeMappings(_ context.Context, realm, owner, ownerID, clientUUID string, roles []map[string]any) error {
	slog.Info("DRY-RUN: would add client roles to scope",
		"realm", realm, "owner", owner, "ownerID", ownerID, "clientUUID", clientUUID, "roles", roleNames(roles))

	return nil
}

func (d *dryRunAPI) GetUserGroups(ctx context.Context, realm, userID string) ([]map[string]any, error) {
	if isSyntheticID(userID) || d.realmIsSynthetic(realm) {
		return nil, nil
	}
	return d.inner.GetUserGroups(ctx, realm, userID)
}

func (d *dryRunAPI) AddUserToGroup(_ context.Context, realm, userID, groupID string) error {
	slog.Info("DRY-RUN: would add user to group", "realm", realm, "userID", userID, "groupID", groupID)
	return nil
}

// Role mappings.

func (d *dryRunAPI) GetRealmRoleMappings(ctx context.Context, realm, subject, subjectID string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) || isSyntheticID(subjectID) {
		return nil, nil
	}

	return d.inner.GetRealmRoleMappings(ctx, realm, subject, subjectID)
}

func (d *dryRunAPI) AddRealmRoleMappings(_ context.Context, realm, subject, subjectID string, roles []map[string]any) error {
	slog.Info("DRY-RUN: would assign realm roles",
		"realm", realm, "subject", subject, "subjectID", subjectID, "roles", roleNames(roles))

	return nil
}

func (d *dryRunAPI) GetClientRoleMappings(ctx context.Context, realm, subject, subjectID, clientUUID string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) || isSyntheticID(subjectID) || isSyntheticID(clientUUID) {
		return nil, nil
	}

	return d.inner.GetClientRoleMappings(ctx, realm, subject, subjectID, clientUUID)
}

func (d *dryRunAPI) AddClientRoleMappings(_ context.Context, realm, subject, subjectID, clientUUID string, roles []map[string]any) error {
	slog.Info("DRY-RUN: would assign client roles",
		"realm", realm, "subject", subject, "subjectID", subjectID, "clientUUID", clientUUID, "roles", roleNames(roles))

	return nil
}

// Service accounts.

func (d *dryRunAPI) GetServiceAccountUser(ctx context.Context, realm, clientUUID string) (map[string]any, error) {
	if isSyntheticID(clientUUID) {
		key := saKey{realm, clientUUID}
		d.mu.Lock()
		defer d.mu.Unlock()
		if id, ok := d.createdSA[key]; ok {
			return map[string]any{"id": id}, nil
		}
		id := d.newID("sa-user")
		d.createdSA[key] = id
		return map[string]any{"id": id}, nil
	}
	return d.inner.GetServiceAccountUser(ctx, realm, clientUUID)
}

// Groups.

// GetGroups reports a group created earlier in this dry-run when one matches,
// so a later step — a user joining a group, or a subgroup being nested under
// it — resolves it instead of treating it as missing.
//
// The lookup is keyed on parentID, which is "" at the top level, so one method
// serves both levels exactly as the two it replaced did.
func (d *dryRunAPI) GetGroups(ctx context.Context, realm, parentID, search string) ([]map[string]any, error) {
	d.mu.Lock()
	id, found := d.createdGroups[groupKey{realm, parentID, search}]
	d.mu.Unlock()

	if found {
		return []map[string]any{{"id": id, "name": search}}, nil
	}

	if isSyntheticID(parentID) || d.realmIsSynthetic(realm) {
		return nil, nil
	}

	return d.inner.GetGroups(ctx, realm, parentID, search)
}

func (d *dryRunAPI) CreateGroup(_ context.Context, realm, parentID string, body map[string]any) (string, error) {
	name, _ := body["name"].(string)
	if parentID == "" {
		slog.Info("DRY-RUN: would create group", "realm", realm, "group", name)
	} else {
		slog.Info("DRY-RUN: would create subgroup", "realm", realm, "group", name, "parent", parentID)
	}

	id := d.newID("group")

	d.mu.Lock()
	d.createdGroups[groupKey{realm, parentID, name}] = id
	d.mu.Unlock()

	return id, nil
}

func (d *dryRunAPI) UpdateGroup(_ context.Context, realm, id string, body map[string]any) error {
	slog.Info("DRY-RUN: would update group", "realm", realm, "groupID", id, "group", body["name"])
	return nil
}

func roleNames(roles []map[string]any) []string {
	out := make([]string, 0, len(roles))
	for _, r := range roles {
		if n, ok := r["name"].(string); ok {
			out = append(out, n)
		}
	}
	return out
}

// Client scopes.

// GetClientScopes reports the realm's real client scopes plus the ones that
// would have been created earlier in this dry-run. Without the synthetic ones,
// ensureClientScopeAssignments would treat a scope created moments ago as
// missing and log a spurious "not found, skipping assignment".
func (d *dryRunAPI) GetClientScopes(ctx context.Context, realm string) ([]map[string]any, error) {
	var scopes []map[string]any

	if !d.realmIsSynthetic(realm) {
		var err error

		scopes, err = d.inner.GetClientScopes(ctx, realm)
		if err != nil {
			return nil, err
		}
	}

	return append(scopes, d.syntheticClientScopes(realm)...), nil
}

// syntheticClientScopes returns the scopes created so far in this dry-run for
// the given realm, sorted by name so output is stable across runs.
func (d *dryRunAPI) syntheticClientScopes(realm string) []map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()

	names := make([]string, 0, len(d.createdScopes))
	for key := range d.createdScopes {
		if key.realm == realm {
			names = append(names, key.name)
		}
	}
	sort.Strings(names)

	scopes := make([]map[string]any, 0, len(names))
	for _, name := range names {
		scopes = append(scopes, map[string]any{
			"id":   d.createdScopes[clientScopeKey{realm, name}],
			"name": name,
		})
	}

	return scopes
}

func (d *dryRunAPI) CreateClientScope(_ context.Context, realm string, body map[string]any) (string, error) {
	name, _ := body["name"].(string)
	slog.Info("DRY-RUN: would create client scope", "realm", realm, "clientScope", name)
	id := d.newID("clientscope")
	d.mu.Lock()
	d.createdScopes[clientScopeKey{realm, name}] = id
	d.mu.Unlock()
	return id, nil
}

func (d *dryRunAPI) UpdateClientScope(_ context.Context, realm, scopeID string, body map[string]any) error {
	name, _ := body["name"].(string)
	slog.Info("DRY-RUN: would update client scope", "realm", realm, "clientScope", name, "uuid", scopeID)
	return nil
}

// Client scope assignment.

func (d *dryRunAPI) GetRealmClientScopes(ctx context.Context, realm, kind string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) {
		return nil, nil
	}

	return d.inner.GetRealmClientScopes(ctx, realm, kind)
}

func (d *dryRunAPI) AddRealmClientScope(_ context.Context, realm, scopeID, kind string) error {
	slog.Info("DRY-RUN: would assign client scope to realm", "realm", realm, "scopeUUID", scopeID, "type", kind)
	return nil
}

func (d *dryRunAPI) RemoveRealmClientScope(_ context.Context, realm, scopeID, kind string) error {
	slog.Info("DRY-RUN: would detach client scope from realm", "realm", realm, "scopeUUID", scopeID, "type", kind)
	return nil
}

func (d *dryRunAPI) GetClientScopeAssignments(ctx context.Context, realm, clientUUID, kind string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) || isSyntheticID(clientUUID) {
		return nil, nil
	}

	return d.inner.GetClientScopeAssignments(ctx, realm, clientUUID, kind)
}

func (d *dryRunAPI) AddClientScopeAssignment(_ context.Context, realm, clientUUID, scopeID, kind string) error {
	slog.Info("DRY-RUN: would assign client scope",
		"realm", realm, "clientUUID", clientUUID, "scopeUUID", scopeID, "type", kind)

	return nil
}

func (d *dryRunAPI) RemoveClientScopeAssignment(_ context.Context, realm, clientUUID, scopeID, kind string) error {
	slog.Info("DRY-RUN: would detach client scope",
		"realm", realm, "clientUUID", clientUUID, "scopeUUID", scopeID, "type", kind)

	return nil
}

// Organizations.

func (d *dryRunAPI) GetOrganizations(ctx context.Context, realm, search string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) {
		return nil, nil
	}
	return d.inner.GetOrganizations(ctx, realm, search)
}

// GetOrganization short-circuits for an organization that does not exist yet:
// there is no representation to merge over, and the reconciler treats a nil
// result as "nothing to preserve".
func (d *dryRunAPI) GetOrganization(ctx context.Context, realm, orgID string) (map[string]any, error) {
	if d.realmIsSynthetic(realm) || isSyntheticID(orgID) {
		return nil, nil
	}
	return d.inner.GetOrganization(ctx, realm, orgID)
}

// CreateOrganization returns a synthetic id so the organization's groups and
// members can still be reported. Unlike client scopes there is no bookkeeping
// to keep: an organization is looked up once per run, so nothing rediscovers it
// by name later.
func (d *dryRunAPI) CreateOrganization(_ context.Context, realm string, body map[string]any) (string, error) {
	name, _ := body["name"].(string)
	slog.Info("DRY-RUN: would create organization", "realm", realm, "organization", name)

	return d.newID("organization"), nil
}

func (d *dryRunAPI) UpdateOrganization(_ context.Context, realm, orgID string, body map[string]any) error {
	name, _ := body["name"].(string)
	slog.Info("DRY-RUN: would update organization", "realm", realm, "organization", name, "uuid", orgID)
	return nil
}

// GetOrganizationMembers reports the organization's real members plus any added
// earlier in this dry-run, so organization group membership — which resolves
// its users against this listing — is reported rather than skipped.
//
// A synthetic member can only be named when the user was also created in this
// run, since that is the only place a username is known for an id. One that
// cannot be named is left out: the caller matches on username, so an entry
// without one would not help it.
func (d *dryRunAPI) GetOrganizationMembers(ctx context.Context, realm, orgID string) ([]map[string]any, error) {
	var members []map[string]any

	if !d.realmIsSynthetic(realm) && !isSyntheticID(orgID) {
		var err error

		members, err = d.inner.GetOrganizationMembers(ctx, realm, orgID)
		if err != nil {
			return nil, err
		}
	}

	return append(members, d.syntheticOrgMembers(realm, orgID)...), nil
}

// syntheticOrgMembers returns the memberships added so far in this dry-run for
// the given organization, sorted by username so output is stable.
func (d *dryRunAPI) syntheticOrgMembers(realm, orgID string) []map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()

	byID := make(map[string]string, len(d.createdUsers))
	for key, id := range d.createdUsers {
		if key.realm == realm {
			byID[id] = key.username
		}
	}

	usernames := make([]string, 0, len(d.createdOrgMembers))

	for key := range d.createdOrgMembers {
		if key.realm != realm || key.orgID != orgID {
			continue
		}

		if username, ok := byID[key.userID]; ok {
			usernames = append(usernames, username)
		}
	}
	sort.Strings(usernames)

	members := make([]map[string]any, 0, len(usernames))
	for _, username := range usernames {
		members = append(members, map[string]any{
			"id":       d.createdUsers[userKey{realm, username}],
			"username": username,
		})
	}

	return members
}

func (d *dryRunAPI) AddOrganizationMember(_ context.Context, realm, orgID, userID string) error {
	slog.Info("DRY-RUN: would add user to organization", "realm", realm, "orgUUID", orgID, "userID", userID)

	d.mu.Lock()
	d.createdOrgMembers[orgMemberKey{realm, orgID, userID}] = true
	d.mu.Unlock()

	return nil
}

// Organization groups.

func (d *dryRunAPI) GetOrganizationGroups(ctx context.Context, realm, orgID, parentID string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) || isSyntheticID(orgID) || isSyntheticID(parentID) {
		return nil, nil
	}

	return d.inner.GetOrganizationGroups(ctx, realm, orgID, parentID)
}

func (d *dryRunAPI) CreateOrganizationGroup(_ context.Context, realm, orgID, parentID string, body map[string]any) (string, error) {
	name, _ := body["name"].(string)
	if parentID == "" {
		slog.Info("DRY-RUN: would create organization group", "realm", realm, "orgUUID", orgID, "group", name)
	} else {
		slog.Info("DRY-RUN: would create organization subgroup",
			"realm", realm, "orgUUID", orgID, "group", name, "parent", parentID)
	}

	return d.newID("orggroup"), nil
}

func (d *dryRunAPI) UpdateOrganizationGroup(_ context.Context, realm, orgID, groupID string, body map[string]any) error {
	name, _ := body["name"].(string)
	slog.Info("DRY-RUN: would update organization group", "realm", realm, "orgUUID", orgID, "group", name, "uuid", groupID)
	return nil
}

func (d *dryRunAPI) GetOrganizationGroupMembers(ctx context.Context, realm, orgID, groupID string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) || isSyntheticID(orgID) || isSyntheticID(groupID) {
		return nil, nil
	}
	return d.inner.GetOrganizationGroupMembers(ctx, realm, orgID, groupID)
}

func (d *dryRunAPI) AddOrganizationGroupMember(_ context.Context, realm, orgID, groupID, userID string) error {
	slog.Info("DRY-RUN: would add user to organization group", "realm", realm, "orgUUID", orgID, "groupUUID", groupID, "userID", userID)
	return nil
}

// Authentication flows.

// GetAuthenticationFlows reports the realm's real flows plus the ones that
// would have been created earlier in this dry-run, so a client binding override
// can resolve a flow declared in the same config.
func (d *dryRunAPI) GetAuthenticationFlows(ctx context.Context, realm string) ([]map[string]any, error) {
	var flows []map[string]any

	if !d.realmIsSynthetic(realm) {
		var err error

		flows, err = d.inner.GetAuthenticationFlows(ctx, realm)
		if err != nil {
			return nil, err
		}
	}

	return append(flows, d.syntheticFlows(realm)...), nil
}

// syntheticFlows returns the flows created so far in this dry-run for the given
// realm, sorted by alias so output is stable across runs.
func (d *dryRunAPI) syntheticFlows(realm string) []map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()

	aliases := make([]string, 0, len(d.createdFlows))
	for key := range d.createdFlows {
		if key.realm == realm {
			aliases = append(aliases, key.alias)
		}
	}
	sort.Strings(aliases)

	flows := make([]map[string]any, 0, len(aliases))
	for _, alias := range aliases {
		flows = append(flows, map[string]any{
			"id":      d.createdFlows[flowKey{realm, alias}],
			"alias":   alias,
			"builtIn": false,
		})
	}

	return flows
}

// flowIsSynthetic reports whether the flow would only have been created in this
// dry-run, and therefore does not exist on the server to be read back.
func (d *dryRunAPI) flowIsSynthetic(realm, alias string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, ok := d.createdFlows[flowKey{realm, alias}]

	return ok
}

func (d *dryRunAPI) recordFlow(realm, alias string) {
	id := d.newID("authflow")

	d.mu.Lock()
	defer d.mu.Unlock()
	d.createdFlows[flowKey{realm, alias}] = id
}

func (d *dryRunAPI) CreateAuthenticationFlow(_ context.Context, realm string, body map[string]any) error {
	alias, _ := body["alias"].(string)
	slog.Info("DRY-RUN: would create authentication flow", "realm", realm, "flow", alias)
	d.recordFlow(realm, alias)

	return nil
}

func (d *dryRunAPI) CopyAuthenticationFlow(_ context.Context, realm, sourceAlias, newName string) error {
	slog.Info("DRY-RUN: would copy authentication flow", "realm", realm, "from", sourceAlias, "flow", newName)
	d.recordFlow(realm, newName)

	return nil
}

// GetAuthenticationFlowExecutions returns nothing for a flow that would only
// have been created in this run, so the requirement and config steps that read
// an execution back are skipped and only the creates are reported. Without the
// flowIsSynthetic check this would ask the server for a flow it never created
// and abort the whole dry-run on the resulting 404.
func (d *dryRunAPI) GetAuthenticationFlowExecutions(ctx context.Context, realm, flowAlias string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) || d.flowIsSynthetic(realm, flowAlias) {
		return nil, nil
	}

	return d.inner.GetAuthenticationFlowExecutions(ctx, realm, flowAlias)
}

func (d *dryRunAPI) UpdateAuthenticationFlowExecution(_ context.Context, realm, flowAlias string, body map[string]any) error {
	requirement, _ := body["requirement"].(string)
	slog.Info("DRY-RUN: would update authentication execution", "realm", realm, "flow", flowAlias, "requirement", requirement)
	return nil
}

func (d *dryRunAPI) CreateAuthenticationExecution(_ context.Context, realm, flowAlias string, body map[string]any) error {
	provider, _ := body["provider"].(string)
	slog.Info("DRY-RUN: would add authentication execution", "realm", realm, "flow", flowAlias, "provider", provider)
	return nil
}

func (d *dryRunAPI) CreateAuthenticationSubflow(_ context.Context, realm, flowAlias string, body map[string]any) error {
	alias, _ := body["alias"].(string)
	slog.Info("DRY-RUN: would add authentication subflow", "realm", realm, "flow", flowAlias, "subflow", alias)

	// A subflow is itself a flow that the executions below it are added to, so
	// it has to be tracked too — otherwise the recursion reads it back from a
	// server that never created it.
	d.recordFlow(realm, alias)

	return nil
}

func (d *dryRunAPI) CreateAuthenticationExecutionConfig(_ context.Context, realm, executionID string, body map[string]any) error {
	alias, _ := body["alias"].(string)
	slog.Info("DRY-RUN: would set authentication execution config", "realm", realm, "executionID", executionID, "configAlias", alias)
	return nil
}

// Identity providers.
//
// An identity provider is keyed by its alias rather than a generated id, so
// there is no synthetic id to hand back. What the bookkeeping has to preserve
// instead is visibility: a provider created in this run must still show up in
// the realm's listing, because both the mapper step and the organization link
// resolve an alias against it.

// GetIdentityProviders reports the realm's real providers plus any that would
// have been created earlier in this run, so a provider and the organization
// that links it can be declared in the same config.
func (d *dryRunAPI) GetIdentityProviders(ctx context.Context, realm string) ([]map[string]any, error) {
	var providers []map[string]any

	if !d.realmIsSynthetic(realm) {
		var err error

		providers, err = d.inner.GetIdentityProviders(ctx, realm)
		if err != nil {
			return nil, err
		}
	}

	return append(providers, d.syntheticIdentityProviders(realm)...), nil
}

// syntheticIdentityProviders returns the providers created in this run for one
// realm, in alias order so the reported sequence does not depend on map
// iteration.
func (d *dryRunAPI) syntheticIdentityProviders(realm string) []map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()

	aliases := make([]string, 0, len(d.createdIdPs))

	for k := range d.createdIdPs {
		if k.realm == realm {
			aliases = append(aliases, k.alias)
		}
	}

	sort.Strings(aliases)

	providers := make([]map[string]any, 0, len(aliases))
	for _, alias := range aliases {
		providers = append(providers, d.createdIdPs[idpKey{realm, alias}])
	}

	return providers
}

func (d *dryRunAPI) CreateIdentityProvider(_ context.Context, realm string, body map[string]any) error {
	alias, _ := body["alias"].(string)
	providerID, _ := body["providerId"].(string)
	slog.Info("DRY-RUN: would create identity provider", "realm", realm, "identityProvider", alias, "providerId", providerID)

	d.mu.Lock()
	d.createdIdPs[idpKey{realm, alias}] = body
	d.mu.Unlock()

	return nil
}

func (d *dryRunAPI) UpdateIdentityProvider(_ context.Context, realm, alias string, body map[string]any) error {
	providerID, _ := body["providerId"].(string)
	slog.Info("DRY-RUN: would update identity provider", "realm", realm, "identityProvider", alias, "providerId", providerID)
	return nil
}

// GetIdentityProviderMappers short-circuits for a provider that does not exist
// yet, so its mappers are reported as creates rather than failing the read.
func (d *dryRunAPI) GetIdentityProviderMappers(ctx context.Context, realm, alias string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) || d.identityProviderIsSynthetic(realm, alias) {
		return nil, nil
	}
	return d.inner.GetIdentityProviderMappers(ctx, realm, alias)
}

func (d *dryRunAPI) identityProviderIsSynthetic(realm, alias string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.createdIdPs[idpKey{realm, alias}]

	return ok
}

func (d *dryRunAPI) CreateIdentityProviderMapper(_ context.Context, realm, alias string, body map[string]any) error {
	name, _ := body["name"].(string)
	slog.Info("DRY-RUN: would create identity provider mapper", "realm", realm, "identityProvider", alias, "mapper", name)
	return nil
}

func (d *dryRunAPI) UpdateIdentityProviderMapper(_ context.Context, realm, alias, mapperID string, body map[string]any) error {
	name, _ := body["name"].(string)
	slog.Info("DRY-RUN: would update identity provider mapper", "realm", realm, "identityProvider", alias, "mapper", name, "uuid", mapperID)
	return nil
}

func (d *dryRunAPI) GetOrganizationIdentityProviders(ctx context.Context, realm, orgID string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) || isSyntheticID(orgID) {
		return nil, nil
	}
	return d.inner.GetOrganizationIdentityProviders(ctx, realm, orgID)
}

func (d *dryRunAPI) AddOrganizationIdentityProvider(_ context.Context, realm, orgID, alias string) error {
	slog.Info("DRY-RUN: would link identity provider to organization", "realm", realm, "organizationUuid", orgID, "identityProvider", alias)
	return nil
}

// Fine-grained management permissions.

// managementPermissionScopes are the scope names Keycloak creates when
// fine-grained permissions are enabled on a client. Measured on 26.6 and
// 26.7.2; identical on both.
//
// The reconciler normally validates configured scope names against what the
// server returns. This list is the one place that cannot: a client that would
// be created has no permissions to read, so a dry-run has to say what enabling
// them would produce. It is only ever used for that synthetic case.
var managementPermissionScopes = []string{
	"view", "manage", "configure",
	"map-roles", "map-roles-client-scope", "map-roles-composite",
	"token-exchange",
}

func (d *dryRunAPI) GetClientManagementPermissions(ctx context.Context, realm, clientUUID string) (map[string]any, error) {
	if d.realmIsSynthetic(realm) || isSyntheticID(clientUUID) {
		return map[string]any{"enabled": false}, nil
	}

	return d.inner.GetClientManagementPermissions(ctx, realm, clientUUID)
}

func (d *dryRunAPI) SetClientManagementPermissions(_ context.Context, realm, clientUUID string, enabled bool) (map[string]any, error) {
	slog.Info("DRY-RUN: would enable fine-grained management permissions", "realm", realm, "clientUUID", clientUUID)

	scopePermissions := make(map[string]any, len(managementPermissionScopes))
	for _, scope := range managementPermissionScopes {
		scopePermissions[scope] = d.newID("scopeperm")
	}

	return map[string]any{
		"enabled":          enabled,
		"resource":         d.newID("resource"),
		"scopePermissions": scopePermissions,
	}, nil
}

func (d *dryRunAPI) GetAuthzClientPolicies(ctx context.Context, realm, resourceServerUUID, name string) ([]map[string]any, error) {
	d.mu.Lock()
	policy, found := d.createdPolicies[policyKey{realm, resourceServerUUID, name}]
	d.mu.Unlock()

	if found {
		return []map[string]any{policy}, nil
	}

	if d.realmIsSynthetic(realm) || isSyntheticID(resourceServerUUID) {
		return nil, nil
	}

	return d.inner.GetAuthzClientPolicies(ctx, realm, resourceServerUUID, name)
}

func (d *dryRunAPI) CreateAuthzClientPolicy(_ context.Context, realm, resourceServerUUID string, body map[string]any) (string, error) {
	name, _ := body["name"].(string)
	slog.Info("DRY-RUN: would create management permission policy", "realm", realm, "policy", name)

	id := d.newID("policy")

	// Remember it so a second scope reconciled in the same run sees the policy
	// this one would have created, rather than reporting a duplicate create.
	policy := map[string]any{"id": id, "name": name, "type": "client"}
	if clients, ok := body["clients"]; ok {
		policy["clients"] = clients
	}

	d.mu.Lock()
	d.createdPolicies[policyKey{realm, resourceServerUUID, name}] = policy
	d.mu.Unlock()

	return id, nil
}

func (d *dryRunAPI) UpdateAuthzClientPolicy(_ context.Context, realm, resourceServerUUID, policyID string, body map[string]any) error {
	slog.Info("DRY-RUN: would update management permission policy",
		"realm", realm, "policy", body["name"], "uuid", policyID)
	return nil
}

func (d *dryRunAPI) GetAuthzScopePermission(ctx context.Context, realm, resourceServerUUID, permissionID string) (map[string]any, error) {
	if d.realmIsSynthetic(realm) || isSyntheticID(resourceServerUUID) || isSyntheticID(permissionID) {
		return map[string]any{"id": permissionID, "type": "scope", "decisionStrategy": "UNANIMOUS"}, nil
	}

	return d.inner.GetAuthzScopePermission(ctx, realm, resourceServerUUID, permissionID)
}

func (d *dryRunAPI) GetAuthzAssociatedPolicies(ctx context.Context, realm, resourceServerUUID, permissionID string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) || isSyntheticID(resourceServerUUID) || isSyntheticID(permissionID) {
		return nil, nil
	}

	return d.inner.GetAuthzAssociatedPolicies(ctx, realm, resourceServerUUID, permissionID)
}

func (d *dryRunAPI) UpdateAuthzScopePermission(_ context.Context, realm, resourceServerUUID, permissionID string, body map[string]any) error {
	slog.Info("DRY-RUN: would attach policies to management permission",
		"realm", realm, "permission", permissionID, "policies", body["policies"])
	return nil
}
