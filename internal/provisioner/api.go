package provisioner

import "context"

// KeycloakAPI is the port the provisioner uses to interact with Keycloak.
// *client.Client is the production adapter; tests and dry-run mode wrap or
// substitute their own implementations.
type KeycloakAPI interface {
	// Realms
	GetRealm(ctx context.Context, name string) (map[string]any, error)
	CreateRealm(ctx context.Context, body map[string]any) error
	UpdateRealm(ctx context.Context, name string, body map[string]any) error

	// Clients
	GetClients(ctx context.Context, realm, clientID string) ([]map[string]any, error)
	CreateClient(ctx context.Context, realm string, body map[string]any) (string, error)
	UpdateClient(ctx context.Context, realm, uuid string, body map[string]any) error

	// Realm roles
	GetRealmRole(ctx context.Context, realm, name string) (map[string]any, error)
	CreateRealmRole(ctx context.Context, realm string, body map[string]any) error
	UpdateRealmRole(ctx context.Context, realm, name string, body map[string]any) error

	// Client roles
	GetClientRole(ctx context.Context, realm, clientUUID, name string) (map[string]any, error)
	CreateClientRole(ctx context.Context, realm, clientUUID string, body map[string]any) error
	UpdateClientRole(ctx context.Context, realm, clientUUID, name string, body map[string]any) error

	// Protocol mappers
	GetProtocolMappers(ctx context.Context, realm, clientUUID string) ([]map[string]any, error)
	CreateProtocolMapper(ctx context.Context, realm, clientUUID string, body map[string]any) error
	UpdateProtocolMapper(ctx context.Context, realm, clientUUID, mapperID string, body map[string]any) error

	// Client scopes
	GetClientScopes(ctx context.Context, realm string) ([]map[string]any, error)
	CreateClientScope(ctx context.Context, realm string, body map[string]any) (string, error)
	UpdateClientScope(ctx context.Context, realm, scopeID string, body map[string]any) error
	GetClientScopeProtocolMappers(ctx context.Context, realm, scopeID string) ([]map[string]any, error)
	CreateClientScopeProtocolMapper(ctx context.Context, realm, scopeID string, body map[string]any) error
	UpdateClientScopeProtocolMapper(ctx context.Context, realm, scopeID, mapperID string, body map[string]any) error

	// Client scope assignment
	GetRealmDefaultClientScopes(ctx context.Context, realm string) ([]map[string]any, error)
	AddRealmDefaultClientScope(ctx context.Context, realm, scopeID string) error
	GetRealmOptionalClientScopes(ctx context.Context, realm string) ([]map[string]any, error)
	AddRealmOptionalClientScope(ctx context.Context, realm, scopeID string) error
	GetClientDefaultScopes(ctx context.Context, realm, clientUUID string) ([]map[string]any, error)
	AddClientDefaultScope(ctx context.Context, realm, clientUUID, scopeID string) error
	GetClientOptionalScopes(ctx context.Context, realm, clientUUID string) ([]map[string]any, error)
	AddClientOptionalScope(ctx context.Context, realm, clientUUID, scopeID string) error

	// Users
	GetUsers(ctx context.Context, realm, username string) ([]map[string]any, error)
	CreateUser(ctx context.Context, realm string, body map[string]any) (string, error)
	UpdateUser(ctx context.Context, realm, userID string, body map[string]any) error
	ResetUserPassword(ctx context.Context, realm, userID, password string, temporary bool) error

	// Role mappings
	GetUserRealmRoleMappings(ctx context.Context, realm, userID string) ([]map[string]any, error)
	AddUserRealmRoleMappings(ctx context.Context, realm, userID string, roles []map[string]any) error
	GetUserClientRoleMappings(ctx context.Context, realm, userID, clientUUID string) ([]map[string]any, error)
	AddUserClientRoleMappings(ctx context.Context, realm, userID, clientUUID string, roles []map[string]any) error

	// Group memberships
	GetUserGroups(ctx context.Context, realm, userID string) ([]map[string]any, error)
	AddUserToGroup(ctx context.Context, realm, userID, groupID string) error

	// Service accounts
	GetServiceAccountUser(ctx context.Context, realm, clientUUID string) (map[string]any, error)

	// Groups
	GetGroups(ctx context.Context, realm, search string) ([]map[string]any, error)
	GetGroup(ctx context.Context, realm, id string) (map[string]any, error)
	GetSubGroups(ctx context.Context, realm, parentID, search string) ([]map[string]any, error)
	CreateGroup(ctx context.Context, realm string, body map[string]any) (string, error)
	CreateSubGroup(ctx context.Context, realm, parentID string, body map[string]any) (string, error)
	UpdateGroup(ctx context.Context, realm, id string, body map[string]any) error
	GetGroupRealmRoleMappings(ctx context.Context, realm, groupID string) ([]map[string]any, error)
	AddGroupRealmRoleMappings(ctx context.Context, realm, groupID string, roles []map[string]any) error
	GetGroupClientRoleMappings(ctx context.Context, realm, groupID, clientUUID string) ([]map[string]any, error)
	AddGroupClientRoleMappings(ctx context.Context, realm, groupID, clientUUID string, roles []map[string]any) error

	// Authentication flows
	GetAuthenticationFlows(ctx context.Context, realm string) ([]map[string]any, error)
	CreateAuthenticationFlow(ctx context.Context, realm string, body map[string]any) error
	CopyAuthenticationFlow(ctx context.Context, realm, sourceAlias, newName string) error
	GetAuthenticationFlowExecutions(ctx context.Context, realm, flowAlias string) ([]map[string]any, error)
	UpdateAuthenticationFlowExecution(ctx context.Context, realm, flowAlias string, body map[string]any) error
	CreateAuthenticationExecution(ctx context.Context, realm, flowAlias string, body map[string]any) error
	CreateAuthenticationSubflow(ctx context.Context, realm, flowAlias string, body map[string]any) error
	CreateAuthenticationExecutionConfig(ctx context.Context, realm, executionID string, body map[string]any) error

	// Organizations
	GetOrganizations(ctx context.Context, realm, search string) ([]map[string]any, error)
	CreateOrganization(ctx context.Context, realm string, body map[string]any) (string, error)
	UpdateOrganization(ctx context.Context, realm, orgID string, body map[string]any) error
	GetOrganizationMembers(ctx context.Context, realm, orgID string) ([]map[string]any, error)
	AddOrganizationMember(ctx context.Context, realm, orgID, userID string) error

	// Organization groups
	GetOrganizationGroups(ctx context.Context, realm, orgID string) ([]map[string]any, error)
	GetOrganizationSubGroups(ctx context.Context, realm, orgID, groupID string) ([]map[string]any, error)
	CreateOrganizationGroup(ctx context.Context, realm, orgID string, body map[string]any) (string, error)
	CreateOrganizationSubGroup(ctx context.Context, realm, orgID, parentID string, body map[string]any) (string, error)
	UpdateOrganizationGroup(ctx context.Context, realm, orgID, groupID string, body map[string]any) error
	GetOrganizationGroupMembers(ctx context.Context, realm, orgID, groupID string) ([]map[string]any, error)
	AddOrganizationGroupMember(ctx context.Context, realm, orgID, groupID, userID string) error
}
