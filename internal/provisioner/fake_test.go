package provisioner

import (
	"context"
)

// fakeAPI is a KeycloakAPI test double for the dry-run adapter.
//
// The embedded KeycloakAPI is deliberately nil. Only the reads the adapter is
// expected to forward are implemented below; everything else — every mutation,
// and every read the adapter should have short-circuited — panics through the
// promoted nil method and names itself in the stack trace.
//
// That is a stronger assertion than the call counters it replaces, which could
// only be checked where a test remembered to look, and it needs no maintenance
// when the port grows: a new write method requires no stub here at all.
type fakeAPI struct {
	KeycloakAPI

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

	clientScopes           map[string][]map[string]any // key: realm
	scopeMappers           map[string][]map[string]any // key: realm/scopeID
	realmScopeAssignments  map[string][]map[string]any // key: realm/kind
	clientScopeAssignments map[string][]map[string]any // key: realm/clientUUID/kind

	organizations map[string][]map[string]any // key: realm
	orgMembers    map[string][]map[string]any // key: realm/orgID
	orgGroups     map[string][]map[string]any // key: realm/orgID
	orgSubGroups  map[string][]map[string]any // key: realm/orgID/parentID
	orgGroupMbrs  map[string][]map[string]any // key: realm/orgID/groupID

	authFlows      map[string][]map[string]any // key: realm
	flowExecutions map[string][]map[string]any // key: realm/flowAlias
}

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

		clientScopes:           make(map[string][]map[string]any),
		scopeMappers:           make(map[string][]map[string]any),
		realmScopeAssignments:  make(map[string][]map[string]any),
		clientScopeAssignments: make(map[string][]map[string]any),

		organizations: make(map[string][]map[string]any),
		orgMembers:    make(map[string][]map[string]any),
		orgGroups:     make(map[string][]map[string]any),
		orgSubGroups:  make(map[string][]map[string]any),
		orgGroupMbrs:  make(map[string][]map[string]any),

		authFlows:      make(map[string][]map[string]any),
		flowExecutions: make(map[string][]map[string]any),
	}
}

func (f *fakeAPI) GetRealm(_ context.Context, name string) (map[string]any, error) {
	return f.realms[name], nil
}

func (f *fakeAPI) GetClients(_ context.Context, realm, _ string) ([]map[string]any, error) {
	return f.clientsByRealm[realm], nil
}

func (f *fakeAPI) GetRealmRole(_ context.Context, realm, name string) (map[string]any, error) {
	return f.realmRoles[realm+"/"+name], nil
}

func (f *fakeAPI) GetClientRole(_ context.Context, realm, uuid, name string) (map[string]any, error) {
	return f.clientRoles[realm+"/"+uuid+"/"+name], nil
}

func (f *fakeAPI) GetProtocolMappers(_ context.Context, realm, uuid string) ([]map[string]any, error) {
	return f.protocolMappers[realm+"/"+uuid], nil
}

func (f *fakeAPI) GetUsers(_ context.Context, realm, _ string) ([]map[string]any, error) {
	return f.usersByRealm[realm], nil
}

func (f *fakeAPI) GetUserRealmRoleMappings(_ context.Context, realm, userID string) ([]map[string]any, error) {
	return f.realmRoleMaps[realm+"/"+userID], nil
}

func (f *fakeAPI) GetUserClientRoleMappings(_ context.Context, realm, userID, uuid string) ([]map[string]any, error) {
	return f.clientRoleMaps[realm+"/"+userID+"/"+uuid], nil
}

func (f *fakeAPI) GetUserGroups(_ context.Context, realm, userID string) ([]map[string]any, error) {
	return f.userGroups[realm+"/"+userID], nil
}

func (f *fakeAPI) GetServiceAccountUser(_ context.Context, realm, uuid string) (map[string]any, error) {
	return f.saUsers[realm+"/"+uuid], nil
}

func (f *fakeAPI) GetGroups(_ context.Context, realm, parentID, _ string) ([]map[string]any, error) {
	if parentID == "" {
		return f.groupsByRealm[realm], nil
	}

	return f.subGroupsByParent[realm+"/"+parentID], nil
}

func (f *fakeAPI) GetGroupRealmRoleMappings(_ context.Context, realm, groupID string) ([]map[string]any, error) {
	return f.groupRealmRoleMaps[realm+"/"+groupID], nil
}

func (f *fakeAPI) GetGroupClientRoleMappings(_ context.Context, realm, groupID, uuid string) ([]map[string]any, error) {
	return f.groupClientRoleMaps[realm+"/"+groupID+"/"+uuid], nil
}

func (f *fakeAPI) GetClientScopes(_ context.Context, realm string) ([]map[string]any, error) {
	return f.clientScopes[realm], nil
}

func (f *fakeAPI) GetClientScopeProtocolMappers(_ context.Context, realm, scopeID string) ([]map[string]any, error) {
	return f.scopeMappers[realm+"/"+scopeID], nil
}

func (f *fakeAPI) GetOrganizations(_ context.Context, realm, _ string) ([]map[string]any, error) {
	return f.organizations[realm], nil
}

func (f *fakeAPI) GetOrganizationMembers(_ context.Context, realm, orgID string) ([]map[string]any, error) {
	return f.orgMembers[realm+"/"+orgID], nil
}

func (f *fakeAPI) GetOrganizationGroups(_ context.Context, realm, orgID, parentID string) ([]map[string]any, error) {
	if parentID == "" {
		return f.orgGroups[realm+"/"+orgID], nil
	}

	return f.orgSubGroups[realm+"/"+orgID+"/"+parentID], nil
}

func (f *fakeAPI) GetOrganizationGroupMembers(_ context.Context, realm, orgID, groupID string) ([]map[string]any, error) {
	return f.orgGroupMbrs[realm+"/"+orgID+"/"+groupID], nil
}

func (f *fakeAPI) GetAuthenticationFlows(_ context.Context, realm string) ([]map[string]any, error) {
	return f.authFlows[realm], nil
}

func (f *fakeAPI) GetAuthenticationFlowExecutions(_ context.Context, realm, flowAlias string) ([]map[string]any, error) {
	return f.flowExecutions[realm+"/"+flowAlias], nil
}

func (f *fakeAPI) GetRealmClientScopes(_ context.Context, realm, kind string) ([]map[string]any, error) {
	return f.realmScopeAssignments[realm+"/"+kind], nil
}

func (f *fakeAPI) GetClientScopeAssignments(_ context.Context, realm, clientUUID, kind string) ([]map[string]any, error) {
	return f.clientScopeAssignments[realm+"/"+clientUUID+"/"+kind], nil
}
