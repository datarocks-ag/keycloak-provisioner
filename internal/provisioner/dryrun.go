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
)

type dryRunAPI struct {
	inner KeycloakAPI
	seq   atomic.Uint64

	mu                 sync.Mutex
	createdRealms      map[string]bool
	createdClients     map[clientKey]string
	createdSA          map[saKey]string
	createdRealmRoles  map[realmRoleKey]string   // -> synthetic role id
	createdClientRoles map[clientRoleKey]string  // -> synthetic role id
	createdGroups      map[groupKey]string       // -> synthetic group id
	createdScopes      map[clientScopeKey]string // -> synthetic client scope id
	createdFlows       map[flowKey]string        // -> synthetic authentication flow id
	createdUsers       map[userKey]string        // -> synthetic user id
	createdOrgMembers  map[orgMemberKey]bool     // organization memberships added this run
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
		return nil, nil
	}
	return d.inner.GetClients(ctx, realm, clientID)
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

func (d *dryRunAPI) GetProtocolMappers(ctx context.Context, realm, clientUUID string) ([]map[string]any, error) {
	if isSyntheticID(clientUUID) || d.realmIsSynthetic(realm) {
		return nil, nil
	}
	return d.inner.GetProtocolMappers(ctx, realm, clientUUID)
}

func (d *dryRunAPI) CreateProtocolMapper(_ context.Context, realm, clientUUID string, body map[string]any) error {
	slog.Info("DRY-RUN: would create protocol mapper", "realm", realm, "clientUUID", clientUUID, "mapper", body["name"])
	return nil
}

func (d *dryRunAPI) UpdateProtocolMapper(_ context.Context, realm, clientUUID, mapperID string, body map[string]any) error {
	slog.Info("DRY-RUN: would update protocol mapper", "realm", realm, "clientUUID", clientUUID, "mapperId", mapperID, "mapper", body["name"])
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

func (d *dryRunAPI) UpdateUser(_ context.Context, realm, userID string, body map[string]any) error {
	slog.Info("DRY-RUN: would update user", "realm", realm, "userID", userID, "username", body["username"])
	return nil
}

func (d *dryRunAPI) ResetUserPassword(_ context.Context, realm, userID string, _ string, temporary bool) error {
	slog.Info("DRY-RUN: would reset user password", "realm", realm, "userID", userID, "temporary", temporary)
	return nil
}

// Role mappings.

func (d *dryRunAPI) GetUserRealmRoleMappings(ctx context.Context, realm, userID string) ([]map[string]any, error) {
	if isSyntheticID(userID) || d.realmIsSynthetic(realm) {
		return nil, nil
	}
	return d.inner.GetUserRealmRoleMappings(ctx, realm, userID)
}

func (d *dryRunAPI) AddUserRealmRoleMappings(_ context.Context, realm, userID string, roles []map[string]any) error {
	slog.Info("DRY-RUN: would assign realm roles", "realm", realm, "userID", userID, "roles", roleNames(roles))
	return nil
}

func (d *dryRunAPI) GetUserClientRoleMappings(ctx context.Context, realm, userID, clientUUID string) ([]map[string]any, error) {
	if isSyntheticID(userID) || isSyntheticID(clientUUID) || d.realmIsSynthetic(realm) {
		return nil, nil
	}
	return d.inner.GetUserClientRoleMappings(ctx, realm, userID, clientUUID)
}

func (d *dryRunAPI) AddUserClientRoleMappings(_ context.Context, realm, userID, clientUUID string, roles []map[string]any) error {
	slog.Info("DRY-RUN: would assign client roles", "realm", realm, "userID", userID, "clientUUID", clientUUID, "roles", roleNames(roles))
	return nil
}

// Group memberships.

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

func (d *dryRunAPI) GetGroups(ctx context.Context, realm, search string) ([]map[string]any, error) {
	d.mu.Lock()
	id, found := d.createdGroups[groupKey{realm, "", search}]
	d.mu.Unlock()
	if found {
		return []map[string]any{{"id": id, "name": search}}, nil
	}
	if d.realmIsSynthetic(realm) {
		return nil, nil
	}
	return d.inner.GetGroups(ctx, realm, search)
}

func (d *dryRunAPI) GetSubGroups(ctx context.Context, realm, parentID, search string) ([]map[string]any, error) {
	d.mu.Lock()
	id, found := d.createdGroups[groupKey{realm, parentID, search}]
	d.mu.Unlock()
	if found {
		return []map[string]any{{"id": id, "name": search}}, nil
	}
	if isSyntheticID(parentID) || d.realmIsSynthetic(realm) {
		return nil, nil
	}
	return d.inner.GetSubGroups(ctx, realm, parentID, search)
}

func (d *dryRunAPI) CreateGroup(_ context.Context, realm string, body map[string]any) (string, error) {
	name, _ := body["name"].(string)
	slog.Info("DRY-RUN: would create group", "realm", realm, "group", name)
	id := d.newID("group")
	d.mu.Lock()
	d.createdGroups[groupKey{realm, "", name}] = id
	d.mu.Unlock()
	return id, nil
}

func (d *dryRunAPI) CreateSubGroup(_ context.Context, realm, parentID string, body map[string]any) (string, error) {
	name, _ := body["name"].(string)
	slog.Info("DRY-RUN: would create subgroup", "realm", realm, "group", name, "parent", parentID)
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

func (d *dryRunAPI) GetGroupRealmRoleMappings(ctx context.Context, realm, groupID string) ([]map[string]any, error) {
	if isSyntheticID(groupID) || d.realmIsSynthetic(realm) {
		return nil, nil
	}
	return d.inner.GetGroupRealmRoleMappings(ctx, realm, groupID)
}

func (d *dryRunAPI) AddGroupRealmRoleMappings(_ context.Context, realm, groupID string, roles []map[string]any) error {
	slog.Info("DRY-RUN: would assign realm roles to group", "realm", realm, "groupID", groupID, "roles", roleNames(roles))
	return nil
}

func (d *dryRunAPI) GetGroupClientRoleMappings(ctx context.Context, realm, groupID, clientUUID string) ([]map[string]any, error) {
	if isSyntheticID(groupID) || isSyntheticID(clientUUID) || d.realmIsSynthetic(realm) {
		return nil, nil
	}
	return d.inner.GetGroupClientRoleMappings(ctx, realm, groupID, clientUUID)
}

func (d *dryRunAPI) AddGroupClientRoleMappings(_ context.Context, realm, groupID, clientUUID string, roles []map[string]any) error {
	slog.Info("DRY-RUN: would assign client roles to group", "realm", realm, "groupID", groupID, "clientUUID", clientUUID, "roles", roleNames(roles))
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

func (d *dryRunAPI) GetClientScopeProtocolMappers(ctx context.Context, realm, scopeID string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) || isSyntheticID(scopeID) {
		return nil, nil
	}
	return d.inner.GetClientScopeProtocolMappers(ctx, realm, scopeID)
}

func (d *dryRunAPI) CreateClientScopeProtocolMapper(_ context.Context, realm, scopeID string, body map[string]any) error {
	name, _ := body["name"].(string)
	slog.Info("DRY-RUN: would create client scope protocol mapper", "realm", realm, "scopeUUID", scopeID, "mapper", name)
	return nil
}

func (d *dryRunAPI) UpdateClientScopeProtocolMapper(_ context.Context, realm, scopeID, mapperID string, body map[string]any) error {
	name, _ := body["name"].(string)
	slog.Info("DRY-RUN: would update client scope protocol mapper", "realm", realm, "scopeUUID", scopeID, "mapper", name, "uuid", mapperID)
	return nil
}

// Client scope assignment.

func (d *dryRunAPI) GetRealmDefaultClientScopes(ctx context.Context, realm string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) {
		return nil, nil
	}
	return d.inner.GetRealmDefaultClientScopes(ctx, realm)
}

func (d *dryRunAPI) AddRealmDefaultClientScope(_ context.Context, realm, scopeID string) error {
	slog.Info("DRY-RUN: would assign client scope to realm defaults", "realm", realm, "scopeUUID", scopeID)
	return nil
}

func (d *dryRunAPI) GetRealmOptionalClientScopes(ctx context.Context, realm string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) {
		return nil, nil
	}
	return d.inner.GetRealmOptionalClientScopes(ctx, realm)
}

func (d *dryRunAPI) AddRealmOptionalClientScope(_ context.Context, realm, scopeID string) error {
	slog.Info("DRY-RUN: would assign client scope to realm optionals", "realm", realm, "scopeUUID", scopeID)
	return nil
}

func (d *dryRunAPI) GetClientDefaultScopes(ctx context.Context, realm, clientUUID string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) || isSyntheticID(clientUUID) {
		return nil, nil
	}
	return d.inner.GetClientDefaultScopes(ctx, realm, clientUUID)
}

func (d *dryRunAPI) AddClientDefaultScope(_ context.Context, realm, clientUUID, scopeID string) error {
	slog.Info("DRY-RUN: would assign default client scope", "realm", realm, "clientUUID", clientUUID, "scopeUUID", scopeID)
	return nil
}

func (d *dryRunAPI) GetClientOptionalScopes(ctx context.Context, realm, clientUUID string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) || isSyntheticID(clientUUID) {
		return nil, nil
	}
	return d.inner.GetClientOptionalScopes(ctx, realm, clientUUID)
}

func (d *dryRunAPI) AddClientOptionalScope(_ context.Context, realm, clientUUID, scopeID string) error {
	slog.Info("DRY-RUN: would assign optional client scope", "realm", realm, "clientUUID", clientUUID, "scopeUUID", scopeID)
	return nil
}

// Organizations.

func (d *dryRunAPI) GetOrganizations(ctx context.Context, realm, search string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) {
		return nil, nil
	}
	return d.inner.GetOrganizations(ctx, realm, search)
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

func (d *dryRunAPI) GetOrganizationGroups(ctx context.Context, realm, orgID string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) || isSyntheticID(orgID) {
		return nil, nil
	}
	return d.inner.GetOrganizationGroups(ctx, realm, orgID)
}

func (d *dryRunAPI) GetOrganizationSubGroups(ctx context.Context, realm, orgID, groupID string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) || isSyntheticID(orgID) || isSyntheticID(groupID) {
		return nil, nil
	}
	return d.inner.GetOrganizationSubGroups(ctx, realm, orgID, groupID)
}

func (d *dryRunAPI) CreateOrganizationGroup(_ context.Context, realm, orgID string, body map[string]any) (string, error) {
	name, _ := body["name"].(string)
	slog.Info("DRY-RUN: would create organization group", "realm", realm, "orgUUID", orgID, "group", name)
	return d.newID("orggroup"), nil
}

func (d *dryRunAPI) CreateOrganizationSubGroup(_ context.Context, realm, orgID, parentID string, body map[string]any) (string, error) {
	name, _ := body["name"].(string)
	slog.Info("DRY-RUN: would create organization subgroup", "realm", realm, "orgUUID", orgID, "group", name, "parent", parentID)
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
