package provisioner

import (
	"context"
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

	authFlows      map[string][]map[string]any // key: realm
	flowExecutions map[string][]map[string]any // key: realm/flowAlias

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

		authFlows:      make(map[string][]map[string]any),
		flowExecutions: make(map[string][]map[string]any),
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

func (f *fakeAPI) GetAuthenticationFlows(_ context.Context, realm string) ([]map[string]any, error) {
	return f.authFlows[realm], nil
}

func (f *fakeAPI) CreateAuthenticationFlow(context.Context, string, map[string]any) error {
	f.createCalls.inc()
	return nil
}

func (f *fakeAPI) CopyAuthenticationFlow(context.Context, string, string, string) error {
	f.createCalls.inc()
	return nil
}

func (f *fakeAPI) GetAuthenticationFlowExecutions(_ context.Context, realm, flowAlias string) ([]map[string]any, error) {
	return f.flowExecutions[realm+"/"+flowAlias], nil
}

func (f *fakeAPI) UpdateAuthenticationFlowExecution(context.Context, string, string, map[string]any) error {
	f.updateCalls.inc()
	return nil
}

func (f *fakeAPI) CreateAuthenticationExecution(context.Context, string, string, map[string]any) error {
	f.createCalls.inc()
	return nil
}

func (f *fakeAPI) CreateAuthenticationSubflow(context.Context, string, string, map[string]any) error {
	f.createCalls.inc()
	return nil
}

func (f *fakeAPI) CreateAuthenticationExecutionConfig(context.Context, string, string, map[string]any) error {
	f.createCalls.inc()
	return nil
}
