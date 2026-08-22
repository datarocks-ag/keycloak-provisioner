package provisioner

import (
	"context"
	"testing"
)

// fakeAPI is a minimal KeycloakAPI test double.
// All read methods return what the test loaded; all write methods record calls.
type fakeAPI struct {
	realms          map[string]map[string]any
	clientsByRealm  map[string][]map[string]any
	realmRoles      map[string]map[string]any   // key: realm/name
	clientRoles     map[string]map[string]any   // key: realm/uuid/name
	protocolMappers map[string][]map[string]any // key: realm/uuid
	usersByRealm    map[string][]map[string]any
	saUsers         map[string]map[string]any   // key: realm/clientUUID
	realmRoleMaps   map[string][]map[string]any // key: realm/userID
	clientRoleMaps  map[string][]map[string]any // key: realm/userID/clientUUID
	userGroups      map[string][]map[string]any // key: realm/userID

	groupsByRealm       map[string][]map[string]any // key: realm
	groupsByID          map[string]map[string]any   // key: realm/groupID
	subGroupsByParent   map[string][]map[string]any // key: realm/parentID
	groupRealmRoleMaps  map[string][]map[string]any // key: realm/groupID
	groupClientRoleMaps map[string][]map[string]any // key: realm/groupID/clientUUID

	clientScopes        map[string][]map[string]any // key: realm
	scopeMappers        map[string][]map[string]any // key: realm/scopeID
	realmDefaultScopes  map[string][]map[string]any // key: realm
	realmOptionalScopes map[string][]map[string]any // key: realm
	clientDefaultScopes map[string][]map[string]any // key: realm/clientUUID
	clientOptionalScope map[string][]map[string]any // key: realm/clientUUID

	organizations map[string][]map[string]any // key: realm
	orgMembers    map[string][]map[string]any // key: realm/orgID
	orgGroups     map[string][]map[string]any // key: realm/orgID
	orgSubGroups  map[string][]map[string]any // key: realm/orgID/parentID
	orgGroupMbrs  map[string][]map[string]any // key: realm/orgID/groupID

	createCalls atomicCounter
	updateCalls atomicCounter
}

type atomicCounter struct{ n int }

func (a *atomicCounter) inc() { a.n++ }

func newFakeAPI() *fakeAPI {
	return &fakeAPI{
		realms:          make(map[string]map[string]any),
		clientsByRealm:  make(map[string][]map[string]any),
		realmRoles:      make(map[string]map[string]any),
		clientRoles:     make(map[string]map[string]any),
		protocolMappers: make(map[string][]map[string]any),
		usersByRealm:    make(map[string][]map[string]any),
		saUsers:         make(map[string]map[string]any),
		realmRoleMaps:   make(map[string][]map[string]any),
		clientRoleMaps:  make(map[string][]map[string]any),
		userGroups:      make(map[string][]map[string]any),

		groupsByRealm:       make(map[string][]map[string]any),
		groupsByID:          make(map[string]map[string]any),
		subGroupsByParent:   make(map[string][]map[string]any),
		groupRealmRoleMaps:  make(map[string][]map[string]any),
		groupClientRoleMaps: make(map[string][]map[string]any),

		clientScopes:        make(map[string][]map[string]any),
		scopeMappers:        make(map[string][]map[string]any),
		realmDefaultScopes:  make(map[string][]map[string]any),
		realmOptionalScopes: make(map[string][]map[string]any),
		clientDefaultScopes: make(map[string][]map[string]any),
		clientOptionalScope: make(map[string][]map[string]any),

		organizations: make(map[string][]map[string]any),
		orgMembers:    make(map[string][]map[string]any),
		orgGroups:     make(map[string][]map[string]any),
		orgSubGroups:  make(map[string][]map[string]any),
		orgGroupMbrs:  make(map[string][]map[string]any),
	}
}

func (f *fakeAPI) GetRealm(_ context.Context, name string) (map[string]any, error) {
	return f.realms[name], nil
}

func (f *fakeAPI) CreateRealm(context.Context, map[string]any) error { f.createCalls.inc(); return nil }

func (f *fakeAPI) UpdateRealm(context.Context, string, map[string]any) error {
	f.updateCalls.inc()
	return nil
}

func (f *fakeAPI) GetClients(_ context.Context, realm, _ string) ([]map[string]any, error) {
	return f.clientsByRealm[realm], nil
}

func (f *fakeAPI) CreateClient(context.Context, string, map[string]any) (string, error) {
	f.createCalls.inc()
	return "real-uuid", nil
}

func (f *fakeAPI) UpdateClient(context.Context, string, string, map[string]any) error {
	f.updateCalls.inc()
	return nil
}

func (f *fakeAPI) GetRealmRole(_ context.Context, realm, name string) (map[string]any, error) {
	return f.realmRoles[realm+"/"+name], nil
}

func (f *fakeAPI) CreateRealmRole(context.Context, string, map[string]any) error {
	f.createCalls.inc()
	return nil
}

func (f *fakeAPI) UpdateRealmRole(context.Context, string, string, map[string]any) error {
	f.updateCalls.inc()
	return nil
}

func (f *fakeAPI) GetClientRole(_ context.Context, realm, uuid, name string) (map[string]any, error) {
	return f.clientRoles[realm+"/"+uuid+"/"+name], nil
}

func (f *fakeAPI) CreateClientRole(context.Context, string, string, map[string]any) error {
	f.createCalls.inc()
	return nil
}

func (f *fakeAPI) UpdateClientRole(context.Context, string, string, string, map[string]any) error {
	f.updateCalls.inc()
	return nil
}

func (f *fakeAPI) GetProtocolMappers(_ context.Context, realm, uuid string) ([]map[string]any, error) {
	return f.protocolMappers[realm+"/"+uuid], nil
}

func (f *fakeAPI) CreateProtocolMapper(context.Context, string, string, map[string]any) error {
	f.createCalls.inc()
	return nil
}

func (f *fakeAPI) UpdateProtocolMapper(context.Context, string, string, string, map[string]any) error {
	f.updateCalls.inc()
	return nil
}

func (f *fakeAPI) GetUsers(_ context.Context, realm, _ string) ([]map[string]any, error) {
	return f.usersByRealm[realm], nil
}

func (f *fakeAPI) CreateUser(context.Context, string, map[string]any) (string, error) {
	f.createCalls.inc()
	return "real-user-uuid", nil
}

func (f *fakeAPI) UpdateUser(context.Context, string, string, map[string]any) error {
	f.updateCalls.inc()
	return nil
}

func (f *fakeAPI) ResetUserPassword(context.Context, string, string, string, bool) error {
	f.updateCalls.inc()
	return nil
}

func (f *fakeAPI) GetUserRealmRoleMappings(_ context.Context, realm, userID string) ([]map[string]any, error) {
	return f.realmRoleMaps[realm+"/"+userID], nil
}

func (f *fakeAPI) AddUserRealmRoleMappings(context.Context, string, string, []map[string]any) error {
	f.updateCalls.inc()
	return nil
}

func (f *fakeAPI) GetUserClientRoleMappings(_ context.Context, realm, userID, uuid string) ([]map[string]any, error) {
	return f.clientRoleMaps[realm+"/"+userID+"/"+uuid], nil
}

func (f *fakeAPI) AddUserClientRoleMappings(context.Context, string, string, string, []map[string]any) error {
	f.updateCalls.inc()
	return nil
}

func (f *fakeAPI) GetUserGroups(_ context.Context, realm, userID string) ([]map[string]any, error) {
	return f.userGroups[realm+"/"+userID], nil
}

func (f *fakeAPI) AddUserToGroup(context.Context, string, string, string) error {
	f.updateCalls.inc()
	return nil
}

func (f *fakeAPI) GetServiceAccountUser(_ context.Context, realm, uuid string) (map[string]any, error) {
	return f.saUsers[realm+"/"+uuid], nil
}

func (f *fakeAPI) GetGroups(_ context.Context, realm, search string) ([]map[string]any, error) {
	return f.groupsByRealm[realm], nil
}

func (f *fakeAPI) GetGroup(_ context.Context, realm, id string) (map[string]any, error) {
	return f.groupsByID[realm+"/"+id], nil
}

func (f *fakeAPI) GetSubGroups(_ context.Context, realm, parentID, search string) ([]map[string]any, error) {
	return f.subGroupsByParent[realm+"/"+parentID], nil
}

func (f *fakeAPI) CreateGroup(context.Context, string, map[string]any) (string, error) {
	f.createCalls.inc()
	return "real-group-uuid", nil
}

func (f *fakeAPI) CreateSubGroup(context.Context, string, string, map[string]any) (string, error) {
	f.createCalls.inc()
	return "real-subgroup-uuid", nil
}

func (f *fakeAPI) UpdateGroup(context.Context, string, string, map[string]any) error {
	f.updateCalls.inc()
	return nil
}

func (f *fakeAPI) GetGroupRealmRoleMappings(_ context.Context, realm, groupID string) ([]map[string]any, error) {
	return f.groupRealmRoleMaps[realm+"/"+groupID], nil
}

func (f *fakeAPI) AddGroupRealmRoleMappings(context.Context, string, string, []map[string]any) error {
	f.updateCalls.inc()
	return nil
}

func (f *fakeAPI) GetGroupClientRoleMappings(_ context.Context, realm, groupID, uuid string) ([]map[string]any, error) {
	return f.groupClientRoleMaps[realm+"/"+groupID+"/"+uuid], nil
}

func (f *fakeAPI) AddGroupClientRoleMappings(context.Context, string, string, string, []map[string]any) error {
	f.updateCalls.inc()
	return nil
}

func TestDryRunSkipsAllMutations(t *testing.T) {
	inner := newFakeAPI()
	d := NewDryRunAdapter(inner)
	ctx := context.Background()

	if err := d.CreateRealm(ctx, map[string]any{"realm": "r"}); err != nil {
		t.Fatal(err)
	}
	if err := d.UpdateRealm(ctx, "r", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CreateClient(ctx, "r", map[string]any{"clientId": "c"}); err != nil {
		t.Fatal(err)
	}
	if err := d.UpdateClient(ctx, "r", "u", nil); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateRealmRole(ctx, "r", map[string]any{"name": "rr"}); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateClientRole(ctx, "r", "u", map[string]any{"name": "cr"}); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateProtocolMapper(ctx, "r", "u", map[string]any{"name": "m"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CreateUser(ctx, "r", map[string]any{"username": "u"}); err != nil {
		t.Fatal(err)
	}
	if err := d.ResetUserPassword(ctx, "r", "uid", "pw", false); err != nil {
		t.Fatal(err)
	}
	if err := d.AddUserRealmRoleMappings(ctx, "r", "uid", nil); err != nil {
		t.Fatal(err)
	}

	if inner.createCalls.n != 0 || inner.updateCalls.n != 0 {
		t.Errorf("inner API was called: creates=%d updates=%d", inner.createCalls.n, inner.updateCalls.n)
	}
}

func TestDryRunCreateClientReturnsSyntheticUUID(t *testing.T) {
	inner := newFakeAPI()
	d := NewDryRunAdapter(inner)
	ctx := context.Background()

	uuid, err := d.CreateClient(ctx, "r", map[string]any{"clientId": "c"})
	if err != nil {
		t.Fatal(err)
	}
	if !isSyntheticID(uuid) {
		t.Errorf("expected synthetic UUID, got %q", uuid)
	}

	// Subsequent GetClients on the same clientID should return the synthetic record.
	clients, err := d.GetClients(ctx, "r", "c")
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 1 || clients[0]["id"] != uuid {
		t.Errorf("expected synthetic record back, got %v", clients)
	}
}

func TestDryRunSyntheticClientShortCircuitsSubResources(t *testing.T) {
	inner := newFakeAPI()
	// Pre-populate a real client's protocol mappers so we can verify they
	// would be returned for non-synthetic UUIDs.
	inner.protocolMappers["r/real-uuid"] = []map[string]any{{"name": "existing"}}

	d := NewDryRunAdapter(inner)
	ctx := context.Background()

	uuid, _ := d.CreateClient(ctx, "r", map[string]any{"clientId": "c"})

	mappers, err := d.GetProtocolMappers(ctx, "r", uuid)
	if err != nil {
		t.Fatal(err)
	}
	if mappers != nil {
		t.Errorf("expected nil for synthetic client UUID, got %v", mappers)
	}

	role, err := d.GetClientRole(ctx, "r", uuid, "any")
	if err != nil {
		t.Fatal(err)
	}
	if role != nil {
		t.Errorf("expected nil client role for synthetic client UUID, got %v", role)
	}
}

func TestDryRunNewlyCreatedRealmRoleIsLookupable(t *testing.T) {
	inner := newFakeAPI()
	d := NewDryRunAdapter(inner)
	ctx := context.Background()

	if err := d.CreateRealmRole(ctx, "r", map[string]any{"name": "admin"}); err != nil {
		t.Fatal(err)
	}
	first, err := d.GetRealmRole(ctx, "r", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || first["name"] != "admin" {
		t.Errorf("expected synthetic role lookup to succeed, got %v", first)
	}

	// Repeated lookups must return a stable id so multiple users assigned the
	// same role get identical role records.
	second, _ := d.GetRealmRole(ctx, "r", "admin")
	if second["id"] != first["id"] {
		t.Errorf("synthetic realm role id not stable: %v vs %v", first["id"], second["id"])
	}
}

func TestDryRunNewlyCreatedClientRoleIsLookupable(t *testing.T) {
	inner := newFakeAPI()
	d := NewDryRunAdapter(inner)
	ctx := context.Background()

	if err := d.CreateClientRole(ctx, "r", "real-uuid", map[string]any{"name": "edit"}); err != nil {
		t.Fatal(err)
	}
	first, err := d.GetClientRole(ctx, "r", "real-uuid", "edit")
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || first["name"] != "edit" {
		t.Errorf("expected synthetic client role lookup to succeed, got %v", first)
	}

	second, _ := d.GetClientRole(ctx, "r", "real-uuid", "edit")
	if second["id"] != first["id"] {
		t.Errorf("synthetic client role id not stable: %v vs %v", first["id"], second["id"])
	}
}

func TestDryRunReadsPassThroughForRealResources(t *testing.T) {
	inner := newFakeAPI()
	inner.realms["existing"] = map[string]any{"realm": "existing", "sslRequired": "external"}
	d := NewDryRunAdapter(inner)

	got, err := d.GetRealm(context.Background(), "existing")
	if err != nil {
		t.Fatal(err)
	}
	if got["sslRequired"] != "external" {
		t.Errorf("expected pass-through, got %v", got)
	}
}

func TestDryRunSyntheticUserShortCircuitsRoleLookups(t *testing.T) {
	inner := newFakeAPI()
	d := NewDryRunAdapter(inner)
	ctx := context.Background()

	uid, _ := d.CreateUser(ctx, "r", map[string]any{"username": "u"})

	if mappings, _ := d.GetUserRealmRoleMappings(ctx, "r", uid); mappings != nil {
		t.Errorf("expected nil for synthetic user, got %v", mappings)
	}
	if mappings, _ := d.GetUserClientRoleMappings(ctx, "r", uid, "any"); mappings != nil {
		t.Errorf("expected nil for synthetic user, got %v", mappings)
	}
}

// Real (existing) user assigned a role on a newly-created (synthetic) client:
// GetUserClientRoleMappings must short-circuit so the dry-run can continue.
func TestDryRunSyntheticClientShortCircuitsUserRoleMappings(t *testing.T) {
	inner := newFakeAPI()
	d := NewDryRunAdapter(inner)
	ctx := context.Background()

	clientUUID, _ := d.CreateClient(ctx, "r", map[string]any{"clientId": "new-client"})

	if mappings, _ := d.GetUserClientRoleMappings(ctx, "r", "real-user-uuid", clientUUID); mappings != nil {
		t.Errorf("expected nil for synthetic client UUID, got %v", mappings)
	}
}

func TestDryRunSubResourcesShortCircuitedForCreatedRealm(t *testing.T) {
	inner := newFakeAPI()
	d := NewDryRunAdapter(inner)
	ctx := context.Background()

	if err := d.CreateRealm(ctx, map[string]any{"realm": "fresh"}); err != nil {
		t.Fatal(err)
	}

	if cs, _ := d.GetClients(ctx, "fresh", "anything"); cs != nil {
		t.Errorf("expected nil clients in synthetic realm, got %v", cs)
	}
	if r, _ := d.GetRealmRole(ctx, "fresh", "anything"); r != nil {
		t.Errorf("expected nil realm role in synthetic realm, got %v", r)
	}
	if u, _ := d.GetUsers(ctx, "fresh", "anything"); u != nil {
		t.Errorf("expected nil users in synthetic realm, got %v", u)
	}
}

func TestDryRunServiceAccountUserForSyntheticClient(t *testing.T) {
	inner := newFakeAPI()
	d := NewDryRunAdapter(inner)
	ctx := context.Background()

	uuid, _ := d.CreateClient(ctx, "r", map[string]any{"clientId": "svc"})

	sa, err := d.GetServiceAccountUser(ctx, "r", uuid)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := sa["id"].(string)
	if !isSyntheticID(id) {
		t.Errorf("expected synthetic SA user UUID, got %q", id)
	}

	// Stable across calls.
	sa2, _ := d.GetServiceAccountUser(ctx, "r", uuid)
	if sa2["id"] != id {
		t.Errorf("synthetic SA user UUID not stable: %q vs %q", id, sa2["id"])
	}
}

func (f *fakeAPI) GetClientScopes(_ context.Context, realm string) ([]map[string]any, error) {
	return f.clientScopes[realm], nil
}

func (f *fakeAPI) CreateClientScope(context.Context, string, map[string]any) (string, error) {
	f.createCalls.inc()
	return "real-scope-uuid", nil
}

func (f *fakeAPI) UpdateClientScope(context.Context, string, string, map[string]any) error {
	f.updateCalls.inc()
	return nil
}

func (f *fakeAPI) GetClientScopeProtocolMappers(_ context.Context, realm, scopeID string) ([]map[string]any, error) {
	return f.scopeMappers[realm+"/"+scopeID], nil
}

func (f *fakeAPI) CreateClientScopeProtocolMapper(context.Context, string, string, map[string]any) error {
	f.createCalls.inc()
	return nil
}

func (f *fakeAPI) UpdateClientScopeProtocolMapper(context.Context, string, string, string, map[string]any) error {
	f.updateCalls.inc()
	return nil
}

func (f *fakeAPI) GetRealmDefaultClientScopes(_ context.Context, realm string) ([]map[string]any, error) {
	return f.realmDefaultScopes[realm], nil
}

func (f *fakeAPI) AddRealmDefaultClientScope(context.Context, string, string) error {
	f.updateCalls.inc()
	return nil
}

func (f *fakeAPI) GetRealmOptionalClientScopes(_ context.Context, realm string) ([]map[string]any, error) {
	return f.realmOptionalScopes[realm], nil
}

func (f *fakeAPI) AddRealmOptionalClientScope(context.Context, string, string) error {
	f.updateCalls.inc()
	return nil
}

func (f *fakeAPI) GetClientDefaultScopes(_ context.Context, realm, clientUUID string) ([]map[string]any, error) {
	return f.clientDefaultScopes[realm+"/"+clientUUID], nil
}

func (f *fakeAPI) AddClientDefaultScope(context.Context, string, string, string) error {
	f.updateCalls.inc()
	return nil
}

func (f *fakeAPI) GetClientOptionalScopes(_ context.Context, realm, clientUUID string) ([]map[string]any, error) {
	return f.clientOptionalScope[realm+"/"+clientUUID], nil
}

func (f *fakeAPI) AddClientOptionalScope(context.Context, string, string, string) error {
	f.updateCalls.inc()
	return nil
}

func TestDryRunSkipsClientScopeMutations(t *testing.T) {
	inner := newFakeAPI()
	d := NewDryRunAdapter(inner)
	ctx := context.Background()

	scopeID, err := d.CreateClientScope(ctx, "r", map[string]any{"name": "orders:read"})
	if err != nil {
		t.Fatal(err)
	}
	if !isSyntheticID(scopeID) {
		t.Errorf("expected synthetic scope id, got %q", scopeID)
	}
	if err := d.UpdateClientScope(ctx, "r", scopeID, map[string]any{"name": "orders:read"}); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateClientScopeProtocolMapper(ctx, "r", scopeID, map[string]any{"name": "m"}); err != nil {
		t.Fatal(err)
	}
	if err := d.UpdateClientScopeProtocolMapper(ctx, "r", scopeID, "mid", map[string]any{"name": "m"}); err != nil {
		t.Fatal(err)
	}
	if err := d.AddRealmDefaultClientScope(ctx, "r", scopeID); err != nil {
		t.Fatal(err)
	}
	if err := d.AddRealmOptionalClientScope(ctx, "r", scopeID); err != nil {
		t.Fatal(err)
	}
	if err := d.AddClientDefaultScope(ctx, "r", "uuid", scopeID); err != nil {
		t.Fatal(err)
	}
	if err := d.AddClientOptionalScope(ctx, "r", "uuid", scopeID); err != nil {
		t.Fatal(err)
	}

	if inner.createCalls.n != 0 || inner.updateCalls.n != 0 {
		t.Errorf("inner API was called: creates=%d updates=%d", inner.createCalls.n, inner.updateCalls.n)
	}
}

func TestDryRunSyntheticClientScopeShortCircuitsMappers(t *testing.T) {
	inner := newFakeAPI()
	inner.scopeMappers["r/real-scope"] = []map[string]any{{"name": "existing"}}
	d := NewDryRunAdapter(inner)
	ctx := context.Background()

	scopeID, err := d.CreateClientScope(ctx, "r", map[string]any{"name": "s"})
	if err != nil {
		t.Fatal(err)
	}

	mappers, err := d.GetClientScopeProtocolMappers(ctx, "r", scopeID)
	if err != nil {
		t.Fatal(err)
	}
	if mappers != nil {
		t.Errorf("expected nil for synthetic scope, got %v", mappers)
	}

	mappers, err = d.GetClientScopeProtocolMappers(ctx, "r", "real-scope")
	if err != nil {
		t.Fatal(err)
	}
	if len(mappers) != 1 {
		t.Errorf("expected pass-through for real scope, got %v", mappers)
	}
}

// TestDryRunCreatedClientScopeIsDiscoverable pins the fix for the dry-run
// reporting gap: a scope created earlier in the same dry-run must be visible to
// the later assignment step, which looks scopes up by name.
func TestDryRunCreatedClientScopeIsDiscoverable(t *testing.T) {
	inner := newFakeAPI()
	inner.clientScopes["r"] = []map[string]any{{"id": "real-1", "name": "profile"}}
	d := NewDryRunAdapter(inner)
	ctx := context.Background()

	scopeID, err := d.CreateClientScope(ctx, "r", map[string]any{"name": "orders:read"})
	if err != nil {
		t.Fatal(err)
	}

	scopes, err := d.GetClientScopes(ctx, "r")
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]string{}
	for _, s := range scopes {
		name, _ := s["name"].(string)
		id, _ := s["id"].(string)
		byName[name] = id
	}

	if byName["profile"] != "real-1" {
		t.Errorf("real scope missing from dry-run listing: %v", scopes)
	}
	if byName["orders:read"] != scopeID {
		t.Errorf("scope created in this dry-run is not discoverable: %v", scopes)
	}
}

func TestDryRunCreatedClientScopeVisibleInSyntheticRealm(t *testing.T) {
	inner := newFakeAPI()
	d := NewDryRunAdapter(inner)
	ctx := context.Background()

	if err := d.CreateRealm(ctx, map[string]any{"realm": "r"}); err != nil {
		t.Fatal(err)
	}
	scopeID, err := d.CreateClientScope(ctx, "r", map[string]any{"name": "orders:read"})
	if err != nil {
		t.Fatal(err)
	}

	scopes, err := d.GetClientScopes(ctx, "r")
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 1 || scopes[0]["id"] != scopeID {
		t.Errorf("expected the synthetic scope inside a would-be-created realm, got %v", scopes)
	}
}

func (f *fakeAPI) GetOrganizations(_ context.Context, realm, _ string) ([]map[string]any, error) {
	return f.organizations[realm], nil
}

func (f *fakeAPI) CreateOrganization(context.Context, string, map[string]any) (string, error) {
	f.createCalls.inc()
	return "real-org-uuid", nil
}

func (f *fakeAPI) UpdateOrganization(context.Context, string, string, map[string]any) error {
	f.updateCalls.inc()
	return nil
}

func (f *fakeAPI) GetOrganizationMembers(_ context.Context, realm, orgID string) ([]map[string]any, error) {
	return f.orgMembers[realm+"/"+orgID], nil
}

func (f *fakeAPI) AddOrganizationMember(context.Context, string, string, string) error {
	f.updateCalls.inc()
	return nil
}

func (f *fakeAPI) GetOrganizationGroups(_ context.Context, realm, orgID string) ([]map[string]any, error) {
	return f.orgGroups[realm+"/"+orgID], nil
}

func (f *fakeAPI) GetOrganizationSubGroups(_ context.Context, realm, orgID, groupID string) ([]map[string]any, error) {
	return f.orgSubGroups[realm+"/"+orgID+"/"+groupID], nil
}

func (f *fakeAPI) CreateOrganizationGroup(context.Context, string, string, map[string]any) (string, error) {
	f.createCalls.inc()
	return "real-orggroup-uuid", nil
}

func (f *fakeAPI) CreateOrganizationSubGroup(context.Context, string, string, string, map[string]any) (string, error) {
	f.createCalls.inc()
	return "real-orgsubgroup-uuid", nil
}

func (f *fakeAPI) UpdateOrganizationGroup(context.Context, string, string, string, map[string]any) error {
	f.updateCalls.inc()
	return nil
}

func (f *fakeAPI) GetOrganizationGroupMembers(_ context.Context, realm, orgID, groupID string) ([]map[string]any, error) {
	return f.orgGroupMbrs[realm+"/"+orgID+"/"+groupID], nil
}

func (f *fakeAPI) AddOrganizationGroupMember(context.Context, string, string, string, string) error {
	f.updateCalls.inc()
	return nil
}
