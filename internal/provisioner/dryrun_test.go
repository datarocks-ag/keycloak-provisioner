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

func (f *fakeAPI) GetServiceAccountUser(_ context.Context, realm, uuid string) (map[string]any, error) {
	return f.saUsers[realm+"/"+uuid], nil
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
	role, err := d.GetRealmRole(ctx, "r", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if role == nil || role["name"] != "admin" {
		t.Errorf("expected synthetic role lookup to succeed, got %v", role)
	}
}

func TestDryRunNewlyCreatedClientRoleIsLookupable(t *testing.T) {
	inner := newFakeAPI()
	d := NewDryRunAdapter(inner)
	ctx := context.Background()

	if err := d.CreateClientRole(ctx, "r", "real-uuid", map[string]any{"name": "edit"}); err != nil {
		t.Fatal(err)
	}
	role, err := d.GetClientRole(ctx, "r", "real-uuid", "edit")
	if err != nil {
		t.Fatal(err)
	}
	if role == nil || role["name"] != "edit" {
		t.Errorf("expected synthetic client role lookup to succeed, got %v", role)
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
