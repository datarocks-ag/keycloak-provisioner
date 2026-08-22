package provisioner

import (
	"context"
	"testing"

	"keycloak-provisioner/internal/client"
)

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
	if err := d.CreateProtocolMapper(ctx, "r", client.MapperContainerClients, "u", map[string]any{"name": "m"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CreateUser(ctx, "r", map[string]any{"username": "u"}); err != nil {
		t.Fatal(err)
	}
	if err := d.ResetUserPassword(ctx, "r", "uid", "pw", false); err != nil {
		t.Fatal(err)
	}
	if err := d.AddRealmRoleMappings(ctx, "r", client.RoleSubjectUsers, "uid", nil); err != nil {
		t.Fatal(err)
	}

	// Reaching here is the assertion: fakeAPI embeds a nil KeycloakAPI, so any
	// call the adapter forwarded instead of skipping would have panicked above.
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

	mappers, err := d.GetProtocolMappers(ctx, "r", client.MapperContainerClients, uuid)
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

	if mappings, _ := d.GetRealmRoleMappings(ctx, "r", client.RoleSubjectUsers, uid); mappings != nil {
		t.Errorf("expected nil for synthetic user, got %v", mappings)
	}
	if mappings, _ := d.GetClientRoleMappings(ctx, "r", client.RoleSubjectUsers, uid, "any"); mappings != nil {
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

	if mappings, _ := d.GetClientRoleMappings(ctx, "r", client.RoleSubjectUsers, "real-user-uuid", clientUUID); mappings != nil {
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
	if err := d.CreateProtocolMapper(ctx, "r", client.MapperContainerClientScopes, scopeID, map[string]any{"name": "m"}); err != nil {
		t.Fatal(err)
	}
	if err := d.UpdateProtocolMapper(ctx, "r", client.MapperContainerClientScopes, scopeID, "mid", map[string]any{"name": "m"}); err != nil {
		t.Fatal(err)
	}
	if err := d.AddRealmClientScope(ctx, "r", scopeID, client.ClientScopeDefault); err != nil {
		t.Fatal(err)
	}
	if err := d.AddRealmClientScope(ctx, "r", scopeID, client.ClientScopeOptional); err != nil {
		t.Fatal(err)
	}
	if err := d.AddClientScopeAssignment(ctx, "r", "uuid", scopeID, client.ClientScopeDefault); err != nil {
		t.Fatal(err)
	}
	if err := d.AddClientScopeAssignment(ctx, "r", "uuid", scopeID, client.ClientScopeOptional); err != nil {
		t.Fatal(err)
	}

	// Reaching here is the assertion: fakeAPI embeds a nil KeycloakAPI, so any
	// call the adapter forwarded instead of skipping would have panicked above.
}

func TestDryRunSyntheticClientScopeShortCircuitsMappers(t *testing.T) {
	inner := newFakeAPI()
	inner.protocolMappers["r/client-scopes/real-scope"] = []map[string]any{{"name": "existing"}}
	d := NewDryRunAdapter(inner)
	ctx := context.Background()

	scopeID, err := d.CreateClientScope(ctx, "r", map[string]any{"name": "s"})
	if err != nil {
		t.Fatal(err)
	}

	mappers, err := d.GetProtocolMappers(ctx, "r", client.MapperContainerClientScopes, scopeID)
	if err != nil {
		t.Fatal(err)
	}
	if mappers != nil {
		t.Errorf("expected nil for synthetic scope, got %v", mappers)
	}

	mappers, err = d.GetProtocolMappers(ctx, "r", client.MapperContainerClientScopes, "real-scope")
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

func TestDryRunSkipsAuthenticationFlowMutations(t *testing.T) {
	inner := newFakeAPI()
	d := NewDryRunAdapter(inner)
	ctx := context.Background()

	if err := d.CreateAuthenticationFlow(ctx, "r", map[string]any{"alias": "f"}); err != nil {
		t.Fatal(err)
	}
	if err := d.CopyAuthenticationFlow(ctx, "r", "browser", "f"); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateAuthenticationExecution(ctx, "r", "f", map[string]any{"provider": "auth-cookie"}); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateAuthenticationSubflow(ctx, "r", "f", map[string]any{"alias": "sub"}); err != nil {
		t.Fatal(err)
	}
	if err := d.UpdateAuthenticationFlowExecution(ctx, "r", "f", map[string]any{"requirement": "REQUIRED"}); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateAuthenticationExecutionConfig(ctx, "r", "ex-1", map[string]any{"alias": "cfg"}); err != nil {
		t.Fatal(err)
	}

	// Reaching here is the assertion: fakeAPI embeds a nil KeycloakAPI, so any
	// call the adapter forwarded instead of skipping would have panicked above.
}

func TestDryRunFlowReadsShortCircuitForSyntheticRealm(t *testing.T) {
	inner := newFakeAPI()
	inner.authFlows["r"] = []map[string]any{{"alias": "browser"}}
	d := NewDryRunAdapter(inner)
	ctx := context.Background()

	if _, err := d.GetAuthenticationFlows(ctx, "r"); err != nil {
		t.Fatal(err)
	}

	if err := d.CreateRealm(ctx, map[string]any{"realm": "r"}); err != nil {
		t.Fatal(err)
	}

	flows, err := d.GetAuthenticationFlows(ctx, "r")
	if err != nil {
		t.Fatal(err)
	}
	if flows != nil {
		t.Errorf("expected nil for a would-be-created realm, got %v", flows)
	}
}

// TestDryRunCreatedFlowIsDiscoverable pins the fix for dry-run aborting on
// authentication flows: a flow created in this run must be visible to the
// client binding-override lookup that runs later.
func TestDryRunCreatedFlowIsDiscoverable(t *testing.T) {
	inner := newFakeAPI()
	inner.authFlows["r"] = []map[string]any{{"id": "real-1", "alias": "browser", "builtIn": true}}
	d := NewDryRunAdapter(inner)
	ctx := context.Background()

	if err := d.CopyAuthenticationFlow(ctx, "r", "browser", "browser-step-up"); err != nil {
		t.Fatal(err)
	}

	flows, err := d.GetAuthenticationFlows(ctx, "r")
	if err != nil {
		t.Fatal(err)
	}

	byAlias := map[string]string{}
	for _, f := range flows {
		alias, _ := f["alias"].(string)
		id, _ := f["id"].(string)
		byAlias[alias] = id
	}

	if byAlias["browser"] != "real-1" {
		t.Errorf("real flow missing from dry-run listing: %v", flows)
	}
	if !isSyntheticID(byAlias["browser-step-up"]) {
		t.Errorf("flow copied in this dry-run is not discoverable: %v", flows)
	}
}

// TestDryRunExecutionsNotReadForCreatedFlow pins the other half: reading the
// executions of a flow that was only "created" in this dry-run must not reach
// the server, which would 404 and abort the whole run.
func TestDryRunExecutionsNotReadForCreatedFlow(t *testing.T) {
	inner := newFakeAPI()
	inner.flowExecutions["r/existing-flow"] = []map[string]any{{"id": "ex-1", "providerId": "auth-cookie"}}
	d := NewDryRunAdapter(inner)
	ctx := context.Background()

	if err := d.CreateAuthenticationFlow(ctx, "r", map[string]any{"alias": "new-flow"}); err != nil {
		t.Fatal(err)
	}

	executions, err := d.GetAuthenticationFlowExecutions(ctx, "r", "new-flow")
	if err != nil {
		t.Fatal(err)
	}
	if executions != nil {
		t.Errorf("expected no executions for a would-be-created flow, got %v", executions)
	}

	// A flow that really exists still passes through.
	executions, err = d.GetAuthenticationFlowExecutions(ctx, "r", "existing-flow")
	if err != nil {
		t.Fatal(err)
	}
	if len(executions) != 1 {
		t.Errorf("expected pass-through for a real flow, got %v", executions)
	}
}

// TestDryRunSubflowIsTrackedAsFlow covers the case that actually broke the
// smoke test: a subflow is itself a flow that later executions are added to, so
// it has to be tracked like a top-level one.
func TestDryRunSubflowIsTrackedAsFlow(t *testing.T) {
	inner := newFakeAPI()
	d := NewDryRunAdapter(inner)
	ctx := context.Background()

	if err := d.CreateAuthenticationSubflow(ctx, "r", "parent-flow", map[string]any{"alias": "loa-gold"}); err != nil {
		t.Fatal(err)
	}

	executions, err := d.GetAuthenticationFlowExecutions(ctx, "r", "loa-gold")
	if err != nil {
		t.Fatal(err)
	}
	if executions != nil {
		t.Errorf("expected no executions for a would-be-created subflow, got %v", executions)
	}
}
