package provisioner

import (
	"context"
	"fmt"
	"log/slog"
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
func NewDryRunAdapter(inner KeycloakAPI) KeycloakAPI {
	return &dryRunAPI{
		inner:              inner,
		createdRealms:      make(map[string]bool),
		createdClients:     make(map[clientKey]string),
		createdSA:          make(map[saKey]string),
		createdRealmRoles:  make(map[realmRoleKey]string),
		createdClientRoles: make(map[clientRoleKey]string),
		createdGroups:      make(map[groupKey]string),
	}
}

type (
	clientKey     struct{ realm, clientID string }
	saKey         struct{ realm, clientUUID string }
	realmRoleKey  struct{ realm, name string }
	clientRoleKey struct{ realm, clientUUID, name string }
	groupKey      struct{ realm, parentID, name string } // parentID "" for top-level
)

type dryRunAPI struct {
	inner KeycloakAPI
	seq   atomic.Uint64

	mu                 sync.Mutex
	createdRealms      map[string]bool
	createdClients     map[clientKey]string
	createdSA          map[saKey]string
	createdRealmRoles  map[realmRoleKey]string  // -> synthetic role id
	createdClientRoles map[clientRoleKey]string // -> synthetic role id
	createdGroups      map[groupKey]string      // -> synthetic group id
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

func (d *dryRunAPI) GetUsers(ctx context.Context, realm, username string) ([]map[string]any, error) {
	if d.realmIsSynthetic(realm) {
		return nil, nil
	}
	return d.inner.GetUsers(ctx, realm, username)
}

func (d *dryRunAPI) CreateUser(_ context.Context, realm string, body map[string]any) (string, error) {
	slog.Info("DRY-RUN: would create user", "realm", realm, "username", body["username"])
	return d.newID("user"), nil
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

func (d *dryRunAPI) GetGroup(ctx context.Context, realm, id string) (map[string]any, error) {
	if isSyntheticID(id) || d.realmIsSynthetic(realm) {
		return nil, nil
	}
	return d.inner.GetGroup(ctx, realm, id)
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
