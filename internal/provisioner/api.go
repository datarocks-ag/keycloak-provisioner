package provisioner

import "context"

// KeycloakAPI is the port the provisioner uses to interact with Keycloak.
// *client.Client is the production adapter; dryRunAPI decorates it, and
// fakeAPI substitutes for it in the dry-run tests.
//
// It is the union of the per-resource interfaces below. A new endpoint belongs
// in the interface that owns the resource, not here.
type KeycloakAPI interface {
	RealmAPI
	ClientAPI
	RealmRoleAPI
	ClientRoleAPI
	ProtocolMapperAPI
	ClientScopeAPI
	ClientScopeAssignmentAPI
	UserAPI
	RoleMappingAPI
	GroupMembershipAPI
	ServiceAccountAPI
	GroupAPI
	AuthenticationFlowAPI
	OrganizationAPI
	OrganizationGroupAPI
}

// dryRunAPI must satisfy the port. Asserting it here fails the build in the
// package that owns the contract, rather than at the wiring in main.
var _ KeycloakAPI = (*dryRunAPI)(nil)

// RealmAPI covers realms themselves.
type RealmAPI interface {
	GetRealm(ctx context.Context, name string) (map[string]any, error)
	CreateRealm(ctx context.Context, body map[string]any) error
	UpdateRealm(ctx context.Context, name string, body map[string]any) error
}

// ClientAPI covers clients.
type ClientAPI interface {
	GetClients(ctx context.Context, realm, clientID string) ([]map[string]any, error)
	CreateClient(ctx context.Context, realm string, body map[string]any) (string, error)
	UpdateClient(ctx context.Context, realm, uuid string, body map[string]any) error
}

// RealmRoleAPI covers realm-level roles.
type RealmRoleAPI interface {
	GetRealmRole(ctx context.Context, realm, name string) (map[string]any, error)
	CreateRealmRole(ctx context.Context, realm string, body map[string]any) error
	UpdateRealmRole(ctx context.Context, realm, name string, body map[string]any) error
}

// ClientRoleAPI covers roles defined on a client.
type ClientRoleAPI interface {
	GetClientRole(ctx context.Context, realm, clientUUID, name string) (map[string]any, error)
	CreateClientRole(ctx context.Context, realm, clientUUID string, body map[string]any) error
	UpdateClientRole(ctx context.Context, realm, clientUUID, name string, body map[string]any) error
}

// ProtocolMapperAPI covers protocol mappers. They hang off either a client or a
// client scope, which container selects; both expose the identical
// /protocol-mappers/models sub-resource.
type ProtocolMapperAPI interface {
	GetProtocolMappers(ctx context.Context, realm, container, containerID string) ([]map[string]any, error)
	CreateProtocolMapper(ctx context.Context, realm, container, containerID string, body map[string]any) error
	UpdateProtocolMapper(ctx context.Context, realm, container, containerID, mapperID string, body map[string]any) error
}

// ClientScopeAPI covers client scopes and their protocol mappers.
type ClientScopeAPI interface {
	GetClientScopes(ctx context.Context, realm string) ([]map[string]any, error)
	CreateClientScope(ctx context.Context, realm string, body map[string]any) (string, error)
	UpdateClientScope(ctx context.Context, realm, scopeID string, body map[string]any) error
}

// ClientScopeAssignmentAPI covers attaching client scopes, both as realm
// defaults and to an individual client. kind is "default" or "optional".
type ClientScopeAssignmentAPI interface {
	GetRealmClientScopes(ctx context.Context, realm, kind string) ([]map[string]any, error)
	AddRealmClientScope(ctx context.Context, realm, scopeID, kind string) error
	GetClientScopeAssignments(ctx context.Context, realm, clientUUID, kind string) ([]map[string]any, error)
	AddClientScopeAssignment(ctx context.Context, realm, clientUUID, scopeID, kind string) error
}

// UserAPI covers users and their credentials.
type UserAPI interface {
	GetUsers(ctx context.Context, realm, username string) ([]map[string]any, error)
	CreateUser(ctx context.Context, realm string, body map[string]any) (string, error)
	UpdateUser(ctx context.Context, realm, userID string, body map[string]any) error
	ResetUserPassword(ctx context.Context, realm, userID, password string, temporary bool) error
}

// RoleMappingAPI covers granting roles to users and groups.
type RoleMappingAPI interface {
	GetUserRealmRoleMappings(ctx context.Context, realm, userID string) ([]map[string]any, error)
	AddUserRealmRoleMappings(ctx context.Context, realm, userID string, roles []map[string]any) error
	GetUserClientRoleMappings(ctx context.Context, realm, userID, clientUUID string) ([]map[string]any, error)
	AddUserClientRoleMappings(ctx context.Context, realm, userID, clientUUID string, roles []map[string]any) error
}

// GroupMembershipAPI covers a user's group memberships.
type GroupMembershipAPI interface {
	GetUserGroups(ctx context.Context, realm, userID string) ([]map[string]any, error)
	AddUserToGroup(ctx context.Context, realm, userID, groupID string) error
}

// ServiceAccountAPI covers the user backing a service account.
type ServiceAccountAPI interface {
	GetServiceAccountUser(ctx context.Context, realm, clientUUID string) (map[string]any, error)
}

// GroupAPI covers groups, their subgroups and their role mappings.
type GroupAPI interface {
	// GetGroups searches one level: parentID is "" for top-level groups.
	GetGroups(ctx context.Context, realm, parentID, search string) ([]map[string]any, error)
	// CreateGroup creates at one level: parentID is "" for a top-level group.
	CreateGroup(ctx context.Context, realm, parentID string, body map[string]any) (string, error)
	UpdateGroup(ctx context.Context, realm, id string, body map[string]any) error
	GetGroupRealmRoleMappings(ctx context.Context, realm, groupID string) ([]map[string]any, error)
	AddGroupRealmRoleMappings(ctx context.Context, realm, groupID string, roles []map[string]any) error
	GetGroupClientRoleMappings(ctx context.Context, realm, groupID, clientUUID string) ([]map[string]any, error)
	AddGroupClientRoleMappings(ctx context.Context, realm, groupID, clientUUID string, roles []map[string]any) error
}

// AuthenticationFlowAPI covers authentication flows, their executions and
// the configuration attached to an execution.
type AuthenticationFlowAPI interface {
	GetAuthenticationFlows(ctx context.Context, realm string) ([]map[string]any, error)
	CreateAuthenticationFlow(ctx context.Context, realm string, body map[string]any) error
	CopyAuthenticationFlow(ctx context.Context, realm, sourceAlias, newName string) error
	GetAuthenticationFlowExecutions(ctx context.Context, realm, flowAlias string) ([]map[string]any, error)
	UpdateAuthenticationFlowExecution(ctx context.Context, realm, flowAlias string, body map[string]any) error
	CreateAuthenticationExecution(ctx context.Context, realm, flowAlias string, body map[string]any) error
	CreateAuthenticationSubflow(ctx context.Context, realm, flowAlias string, body map[string]any) error
	CreateAuthenticationExecutionConfig(ctx context.Context, realm, executionID string, body map[string]any) error
}

// OrganizationAPI covers organizations and their members.
type OrganizationAPI interface {
	GetOrganizations(ctx context.Context, realm, search string) ([]map[string]any, error)
	CreateOrganization(ctx context.Context, realm string, body map[string]any) (string, error)
	UpdateOrganization(ctx context.Context, realm, orgID string, body map[string]any) error
	GetOrganizationMembers(ctx context.Context, realm, orgID string) ([]map[string]any, error)
	AddOrganizationMember(ctx context.Context, realm, orgID, userID string) error
}

// OrganizationGroupAPI covers the groups an organization owns. They are a
// separate namespace from realm groups and cannot be reached through GroupAPI.
type OrganizationGroupAPI interface {
	// GetOrganizationGroups searches one level: parentID is "" for top level.
	GetOrganizationGroups(ctx context.Context, realm, orgID, parentID string) ([]map[string]any, error)
	// CreateOrganizationGroup creates at one level: parentID is "" for top level.
	CreateOrganizationGroup(ctx context.Context, realm, orgID, parentID string, body map[string]any) (string, error)
	UpdateOrganizationGroup(ctx context.Context, realm, orgID, groupID string, body map[string]any) error
	GetOrganizationGroupMembers(ctx context.Context, realm, orgID, groupID string) ([]map[string]any, error)
	AddOrganizationGroupMember(ctx context.Context, realm, orgID, groupID, userID string) error
}
