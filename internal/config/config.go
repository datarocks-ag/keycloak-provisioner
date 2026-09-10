// Package config loads, expands and validates the YAML configuration.
//
// Unknown fields are rejected at load time so a typo surfaces immediately,
// ${VAR} references are expanded from the environment, and every resource
// kind is validated before a single request reaches Keycloak.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// validStrategies is the allowlist of update strategy values.
var validStrategies = map[string]bool{
	"":       true, // inherits from parent/default
	"create": true, // only create if missing, skip if exists
	"update": true, // create or update (default behavior)
}

// EffectiveStrategy returns the first non-empty strategy from the given list,
// defaulting to "update" if all are empty.
func EffectiveStrategy(strategies ...string) string {
	for _, s := range strategies {
		if s != "" {
			return s
		}
	}
	return "update"
}

// validSslRequired is the allowlist of sslRequired values.
// Keycloak uses lowercase values in its REST API.
var validSslRequired = map[string]bool{
	"":         true,
	"external": true,
	"all":      true,
	"none":     true,
}

// Config is the top-level YAML configuration.
type Config struct {
	Strategy    string             `yaml:"strategy"`
	MasterRealm *MasterRealmConfig `yaml:"masterRealm"`
	Realms      []Realm            `yaml:"realms"`
}

// MasterRealmConfig allows limited configuration of the master realm.
// The master realm always exists, so it is update-only (never created).
type MasterRealmConfig struct {
	SslRequired string `yaml:"sslRequired"`
	Users       []User `yaml:"users"`
}

// Realm defines a Keycloak realm to provision.
type Realm struct {
	Realm                string `yaml:"realm"`
	DisplayName          string `yaml:"displayName"`
	Enabled              *bool  `yaml:"enabled"`
	SslRequired          string `yaml:"sslRequired"`
	LoginTheme           string `yaml:"loginTheme"`
	RegistrationAllowed  *bool  `yaml:"registrationAllowed"`
	ResetPasswordAllowed *bool  `yaml:"resetPasswordAllowed"`
	// LoginWithEmailAllowed lets users log in with their email address as well
	// as their username. Keycloak forces duplicateEmailsAllowed off while this
	// is on.
	LoginWithEmailAllowed *bool `yaml:"loginWithEmailAllowed"`
	// BruteForceProtected enables Keycloak's brute force detection, which
	// temporarily locks an account after repeated failed logins. The detection
	// thresholds keep their current values; only the toggle is configurable.
	BruteForceProtected *bool `yaml:"bruteForceProtected"`
	// OrganizationsEnabled toggles Keycloak Organizations for this realm.
	// Keycloak 26+. Leave unset to keep the realm's current value.
	OrganizationsEnabled *bool `yaml:"organizationsEnabled"`
	// Attributes are realm-level attributes. They are merged over the realm's
	// current attributes rather than replacing them, so keys managed outside
	// this config are preserved. Keys and values support ${VAR} expansion.
	Attributes map[string]string `yaml:"attributes"`
	// AcrLoaMap maps ACR values to Levels of Authentication for step-up
	// authentication. It is marshalled into the realm's acr.loa.map attribute
	// and wins over an acr.loa.map entry supplied through Attributes.
	AcrLoaMap map[string]int `yaml:"acrLoaMap"`
	// ClientScopes are provisioned before clients, so a client may reference a
	// scope defined in the same config.
	ClientScopes []ClientScope `yaml:"clientScopes"`
	Clients      []Client      `yaml:"clients"`
	Roles        []RealmRole   `yaml:"roles"`
	Users        []User        `yaml:"users"`
	Groups       []Group       `yaml:"groups"`
	// Organizations are provisioned last, so members can reference users
	// defined in the same config. Requires organizationsEnabled: true.
	Organizations []Organization `yaml:"organizations"`
	// AuthenticationFlows are created before clients so a client can bind to
	// a flow defined in the same config. Existing flows are never modified.
	AuthenticationFlows []AuthenticationFlow `yaml:"authenticationFlows"`
	// AuthenticationBindings binds realm-level flows by alias.
	AuthenticationBindings *AuthenticationBindings `yaml:"authenticationBindings"`
	// IdentityProviders are provisioned after groups and before
	// organizations, so an organization can associate a provider defined in
	// the same config.
	IdentityProviders []IdentityProvider `yaml:"identityProviders"`
	Strategy          string             `yaml:"strategy"`
}

// validFlowProviderIds is the allowlist of authentication flow types.
var validFlowProviderIds = map[string]bool{
	"":           true, // defaults to basic-flow
	"basic-flow": true,
	"form-flow":  true,
}

// validFlowBindingOverrides is the allowlist of client-level flow binding
// override keys. Keycloak accepts any key here and stores it verbatim without
// complaint, so a typo such as "directGrant" is silently inert — which is why
// this is validated up front rather than left to the server.
var validFlowBindingOverrides = map[string]bool{
	"browser":      true,
	"direct_grant": true,
}

// validFlowRequirements is the allowlist of execution requirement values.
var validFlowRequirements = map[string]bool{
	"":            true, // leave at Keycloak's default for the authenticator
	"REQUIRED":    true,
	"ALTERNATIVE": true,
	"DISABLED":    true,
	"CONDITIONAL": true,
}

// AuthenticationFlow defines a top-level Keycloak authentication flow.
//
// Flows are create-only: when a flow with this alias already exists it is left
// untouched, whatever the strategy. Reconciling an existing flow would mean
// diffing an ordered tree of executions and deleting the ones not configured,
// which the provisioner deliberately does not do. To change a flow, delete it
// in Keycloak or declare it under a new alias.
type AuthenticationFlow struct {
	Alias       string `yaml:"alias"`
	Description string `yaml:"description"`
	// ProviderId is "basic-flow" (default) or "form-flow".
	ProviderId string `yaml:"providerId"`
	// CopyFrom seeds the new flow from an existing one, which is how a
	// built-in flow such as "browser" should be customised. Keycloak's
	// built-in flows are never edited in place.
	CopyFrom   string                    `yaml:"copyFrom"`
	Executions []AuthenticationExecution `yaml:"executions"`
}

// AuthenticationExecution is one step of an authentication flow: either an
// authenticator (Provider) or a nested subflow (Subflow), never both.
// Executions are created in the order declared.
type AuthenticationExecution struct {
	// Provider is the authenticator provider id, e.g. "auth-cookie".
	Provider string `yaml:"provider"`
	// Subflow is the alias of a nested flow. Mutually exclusive with Provider.
	Subflow string `yaml:"subflow"`
	// ProviderId applies to subflows only: "basic-flow" (default) or "form-flow".
	ProviderId string `yaml:"providerId"`
	// Description is shown against the execution in the Keycloak console. It
	// applies to subflows; Keycloak ignores it on a plain authenticator.
	Description string `yaml:"description"`
	// Requirement is REQUIRED, ALTERNATIVE, DISABLED or CONDITIONAL.
	Requirement string `yaml:"requirement"`
	// Config is the authenticator configuration. The "alias" key names the
	// config; when absent, one is derived from the flow and provider.
	Config map[string]string `yaml:"config"`
	// Executions nests further steps under a subflow.
	Executions []AuthenticationExecution `yaml:"executions"`
}

// AuthenticationBindings binds flows to their realm-level roles. Each value is
// a flow alias, which may be a built-in flow or one declared in the same config.
type AuthenticationBindings struct {
	BrowserFlow              string `yaml:"browserFlow"`
	DirectGrantFlow          string `yaml:"directGrantFlow"`
	ResetCredentialsFlow     string `yaml:"resetCredentialsFlow"`
	RegistrationFlow         string `yaml:"registrationFlow"`
	ClientAuthenticationFlow string `yaml:"clientAuthenticationFlow"`
	DockerAuthenticationFlow string `yaml:"dockerAuthenticationFlow"`
	FirstBrokerLoginFlow     string `yaml:"firstBrokerLoginFlow"`
}

// Organization defines a Keycloak organization within a realm.
// Requires Keycloak 26+ and organizationsEnabled on the realm.
type Organization struct {
	Name string `yaml:"name"`
	// Alias defaults to the name when empty. It is immutable in Keycloak
	// once the organization exists.
	Alias       string               `yaml:"alias"`
	Enabled     *bool                `yaml:"enabled"`
	Description string               `yaml:"description"`
	RedirectUrl string               `yaml:"redirectUrl"`
	Domains     []OrganizationDomain `yaml:"domains"`
	Attributes  map[string][]string  `yaml:"attributes"` // Keycloak organization attributes are multivalued
	// Members are usernames of users in the same realm. Membership is
	// additive; members are never removed. A username that cannot be
	// resolved is logged as a warning and skipped.
	Members []string `yaml:"members"`
	// Groups are organization-scoped groups. They live in a namespace of
	// their own: they do not appear under the realm's groups, and Keycloak
	// refuses to manage them through the normal group API.
	Groups []OrganizationGroup `yaml:"groups"`
	// IdentityProviders are aliases of providers to associate with this
	// organization. The provider may be declared in the same realm or already
	// exist. Association is additive and never removed, and Keycloak allows a
	// provider to belong to at most one organization.
	IdentityProviders []string `yaml:"identityProviders"`
}

// OrganizationGroup is a group owned by an organization.
//
// Unlike a realm Group it carries no role mappings, and the realm group API
// refuses to manage it outright. Its realmRoles and clientRoles fields exist
// only so validation can reject them with an explanation — see the comment on
// those fields for why an outright omission would be the worse choice.
type OrganizationGroup struct {
	Name       string              `yaml:"name"`
	Attributes map[string][]string `yaml:"attributes"` // Keycloak group attributes are multivalued
	// Members are usernames. A user must already be a member of the
	// organization before it can join one of its groups, so list them under
	// the organization's members as well. Membership is additive.
	Members   []string            `yaml:"members"`
	SubGroups []OrganizationGroup `yaml:"subGroups"`

	// RealmRoles and ClientRoles are accepted by the parser only so that
	// validation can reject them with an explanation. Organization groups
	// cannot carry role mappings, but Keycloak's API does not say so: on 26.6
	// the role-mapping endpoint answers 404, while on 26.7 it answers 204 and
	// reads the role back even though the mapping never reaches a member's
	// effective roles or any token claim. Leaving these fields out would make
	// the parser reject them as unknown, which reads as "not implemented yet"
	// and invites someone to wire the endpoint up by hand and get silence.
	//
	// Validation rejects them when present at all, not merely when non-empty,
	// so "realmRoles: []" is refused too. Someone who writes that is reaching
	// for the feature, and the point is to answer them now rather than once
	// they add the first entry.
	RealmRoles  []string            `yaml:"realmRoles"`
	ClientRoles map[string][]string `yaml:"clientRoles"`
}

// IdentityProvider defines an identity provider (identity broker) in a realm.
//
// Unlike every other resource here, Keycloak replaces the whole representation
// on update: a field left out of the request is reset and the entire config map
// is discarded. The provisioner therefore merges over the server's current
// representation, so fields and config keys this struct does not model survive.
type IdentityProvider struct {
	Alias       string `yaml:"alias"`
	DisplayName string `yaml:"displayName"`
	// ProviderId is the broker type, e.g. "oidc", "saml", "google". It is
	// validated against the providers the server actually offers.
	ProviderId               string `yaml:"providerId"`
	Enabled                  *bool  `yaml:"enabled"`
	TrustEmail               *bool  `yaml:"trustEmail"`
	StoreToken               *bool  `yaml:"storeToken"`
	AddReadTokenRoleOnCreate *bool  `yaml:"addReadTokenRoleOnCreate"`
	LinkOnly                 *bool  `yaml:"linkOnly"`
	HideOnLogin              *bool  `yaml:"hideOnLogin"`
	// FirstBrokerLoginFlowAlias and PostBrokerLoginFlowAlias name flows that
	// must already exist. Keycloak answers 500, with no usable message, for an
	// alias it cannot resolve, so they are checked before the write.
	FirstBrokerLoginFlowAlias string `yaml:"firstBrokerLoginFlowAlias"`
	PostBrokerLoginFlowAlias  string `yaml:"postBrokerLoginFlowAlias"`
	// Config is the provider-specific configuration. Values support ${VAR}
	// expansion, which is how clientSecret should be supplied. Keys this
	// config does not mention are preserved rather than removed.
	Config  map[string]string        `yaml:"config"`
	Mappers []IdentityProviderMapper `yaml:"mappers"`
}

// IdentityProviderMapper maps claims or assertions from a broker onto the local
// user. Mappers are matched by name within their identity provider.
//
// Unlike the provider itself, a mapper's config is replaced rather than merged:
// it carries no masked secrets and no server-managed fields, so building it
// from the config alone is the more predictable behaviour.
type IdentityProviderMapper struct {
	Name string `yaml:"name"`
	// IdentityProviderMapper is the mapper type. Keycloak accepts an unknown
	// one with 201 and leaves it inert, so it is validated against the types
	// the server reports.
	IdentityProviderMapper string            `yaml:"identityProviderMapper"`
	Config                 map[string]string `yaml:"config"`
}

// OrganizationDomain is a domain owned by an organization.
type OrganizationDomain struct {
	Name     string `yaml:"name"`
	Verified *bool  `yaml:"verified"`
}

// validClientScopeTypes is the allowlist of realm-level client scope types.
var validClientScopeTypes = map[string]bool{
	"":         true, // not assigned at realm level
	"none":     true, // explicit form of the above
	"default":  true, // assigned to every new client
	"optional": true, // requestable via the scope parameter
}

// ClientScope defines a Keycloak client scope within a realm.
type ClientScope struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	// Protocol defaults to "openid-connect" when empty.
	Protocol string `yaml:"protocol"`
	// Type controls realm-level assignment: "default" adds the scope to every
	// newly created client, "optional" makes it requestable through the scope
	// parameter, and "none" (the default) assigns it nowhere. Realm-level
	// assignment is additive and never removed.
	Type            string            `yaml:"type"`
	Attributes      map[string]string `yaml:"attributes"`
	ProtocolMappers []ProtocolMapper  `yaml:"protocolMappers"`
	// ScopeMappings are the roles this scope carries into the token of any
	// client it is assigned to. It is the reusable half of the same mechanism
	// Client.ScopeMappings applies to one client: declare the roles once here
	// and attach the scope through defaultClientScopes or optionalClientScopes.
	//
	// Assignment is additive; see Client.ScopeMappings.
	ScopeMappings *UserRoles `yaml:"scopeMappings"`
}

// Client defines a Keycloak client to provision within a realm.
type Client struct {
	ClientID                  string   `yaml:"clientId"`
	Secret                    string   `yaml:"secret"`
	Name                      string   `yaml:"name"`
	Enabled                   *bool    `yaml:"enabled"`
	PublicClient              *bool    `yaml:"publicClient"`
	Protocol                  string   `yaml:"protocol"`
	RootUrl                   string   `yaml:"rootUrl"`
	BaseUrl                   string   `yaml:"baseUrl"`
	AdminUrl                  string   `yaml:"adminUrl"`
	RedirectUris              []string `yaml:"redirectUris"`
	WebOrigins                []string `yaml:"webOrigins"`
	StandardFlowEnabled       *bool    `yaml:"standardFlowEnabled"`
	DirectAccessGrantsEnabled *bool    `yaml:"directAccessGrantsEnabled"`
	ServiceAccountsEnabled    *bool    `yaml:"serviceAccountsEnabled"`
	// StandardTokenExchangeEnabled toggles OAuth 2.0 Token Exchange (RFC 8693)
	// for this client. Requires a confidential client. Keycloak 26.2+.
	StandardTokenExchangeEnabled *bool `yaml:"standardTokenExchangeEnabled"`
	// FullScopeAllowed controls whether the client's tokens carry every role
	// the subject holds, or only those reachable through its assigned client
	// scopes. Set it to false to scope a client down — it is the main control
	// over how broad an exchanged token can be, so it matters most alongside
	// StandardTokenExchangeEnabled.
	//
	// Leaving it unset sends no key, which means two different things. On a
	// client the provisioner creates, Keycloak applies its own default of true,
	// the permissive setting. On one that already exists, the client update is
	// a sparse merge for this flag, so whatever is stored is preserved — a
	// client set to false out of band is not widened. Declare it explicitly
	// wherever the scope matters rather than relying on either.
	FullScopeAllowed     *bool             `yaml:"fullScopeAllowed"`
	BearerOnly           *bool             `yaml:"bearerOnly"`
	ConsentRequired      *bool             `yaml:"consentRequired"`
	FrontchannelLogout   *bool             `yaml:"frontchannelLogout"`
	DefaultClientScopes  []string          `yaml:"defaultClientScopes"`
	OptionalClientScopes []string          `yaml:"optionalClientScopes"`
	Attributes           map[string]string `yaml:"attributes"`
	// AcrLoaMap maps ACR values to Levels of Authentication for this client.
	// It is marshalled into the client's acr.loa.map attribute and wins over an
	// acr.loa.map entry supplied through Attributes.
	AcrLoaMap map[string]int `yaml:"acrLoaMap"`
	// DefaultAcrValues are the ACR values Keycloak applies when a request does
	// not ask for one. Each must be a key of the effective ACR-to-LoA map,
	// either this client's AcrLoaMap or the realm's.
	//
	// Keycloak stores them in the default.acr.values attribute as a single
	// "##"-separated string — not a JSON array, despite the field being a list
	// everywhere it is presented. Writing one by hand through Attributes is
	// easy to get wrong, and the rejection quotes the ACR map rather than the
	// encoding, so it reads as the wrong problem. This field encodes it.
	DefaultAcrValues []string `yaml:"defaultAcrValues"`
	// AuthenticationFlowBindingOverrides overrides realm flow bindings for
	// this client. Keys are binding names ("browser", "direct_grant") and
	// values are flow aliases, which the provisioner resolves to flow IDs.
	AuthenticationFlowBindingOverrides map[string]string `yaml:"authenticationFlowBindingOverrides"`
	ProtocolMappers                    []ProtocolMapper  `yaml:"protocolMappers"`
	ClientRoles                        []ClientRole      `yaml:"clientRoles"`
	ServiceAccountRoles                *UserRoles        `yaml:"serviceAccountRoles"`
	// ScopeMappings are the roles this client's tokens may carry once
	// FullScopeAllowed is false. Without it, false leaves the client with scope
	// on nothing: a subject's roles are dropped from its tokens, and — for
	// token exchange — no audience is reachable, because Keycloak derives the
	// audiences a client may request from the roles in its scope.
	//
	// A role reaches the token only if the subject holds it *and* it is in
	// scope, so this narrows, never grants. Granting is what a user's or
	// group's roles do.
	//
	// Assignment is additive, like every other role assignment here: a mapping
	// present on the server but absent from the config is left alone, so this
	// cannot take back a scope widened out of band.
	ScopeMappings *UserRoles `yaml:"scopeMappings"`
	// ManagementPermissions declares Keycloak's fine-grained admin permissions
	// on this client — the v1 permission model, reached through
	// clients/{id}/management/permissions.
	//
	// It is the only way to grant a v1 token-exchange permission, which gates
	// impersonation (the exchange that accepts requested_subject). Declaring it
	// requires the ADMIN_FINE_GRAINED_AUTHZ server feature; see
	// internal/compat.
	ManagementPermissions *ManagementPermissions `yaml:"managementPermissions"`
}

// ManagementPermissions declares the fine-grained admin permissions on one
// client, and which other clients each permission scope is granted to.
//
// Keycloak models this as a scope permission per capability — view, manage,
// configure, the map-roles family and token-exchange — on the realm-management
// client's authorization resource server. Each scope permission is satisfied by
// the policies attached to it. This type is the declarative form of that: name
// a scope, list the clients allowed to exercise it.
type ManagementPermissions struct {
	// Enabled turns fine-grained permissions on for the client. It defaults to
	// true when omitted, because declaring the block at all is the intent.
	//
	// Setting it to false is rejected rather than applied: Keycloak deletes the
	// whole scope permission set, and the policies attached to it, when
	// fine-grained permissions are switched off on a client. This provisioner
	// does not delete. Remove the block to stop managing the client.
	Enabled *bool `yaml:"enabled"`
	// Scopes maps a permission scope name to who holds it. Scope names are not
	// checked against a hardcoded list — they are validated against the
	// scopePermissions map the server itself returns, so every scope the
	// running Keycloak offers works, and a typo is named rather than silently
	// ignored.
	Scopes map[string]ManagementPermissionScope `yaml:"scopes"`
}

// ManagementPermissionScope is who holds one permission scope on a client.
type ManagementPermissionScope struct {
	// Clients are the clientIds allowed to exercise this scope on the target
	// client, resolved to UUIDs when applied.
	//
	// The list is authoritative for the policy the provisioner owns: dropping a
	// clientId from it withdraws that grant on the next run. It cannot shrink
	// to nothing — see validateManagementPermissions.
	Clients []string `yaml:"clients"`
}

// ProtocolMapper defines a protocol mapper for a Keycloak client.
type ProtocolMapper struct {
	Name            string            `yaml:"name"`
	Protocol        string            `yaml:"protocol"`
	ProtocolMapper  string            `yaml:"protocolMapper"`
	ConsentRequired *bool             `yaml:"consentRequired"`
	Config          map[string]string `yaml:"config"`
}

// RealmRole defines a realm-level role.
type RealmRole struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// ClientRole defines a client-level role.
type ClientRole struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// User defines a Keycloak user to provision within a realm.
type User struct {
	Username string `yaml:"username"`
	// ID fixes the user's UUID so it is the same in every environment and can
	// be referenced without a lookup. It applies only when the user is created
	// and cannot be changed afterwards.
	//
	// Keycloak's create-user endpoint accepts an id and silently ignores it, so
	// a user declaring one is created through partial import instead, which
	// honours it. See ensureUser.
	ID              string     `yaml:"id"`
	Password        string     `yaml:"password"`
	InitialPassword string     `yaml:"initialPassword"`
	Enabled         *bool      `yaml:"enabled"`
	Email           string     `yaml:"email"`
	FirstName       string     `yaml:"firstName"`
	LastName        string     `yaml:"lastName"`
	EmailVerified   *bool      `yaml:"emailVerified"`
	Roles           *UserRoles `yaml:"roles"`
	// Groups lists group paths the user should be a member of, matching
	// Keycloak's path notation (e.g. "/engineering/backend"; the leading
	// slash is optional). Membership is additive — the provisioner never
	// removes a user from a group.
	Groups []string `yaml:"groups"`
	// RequiredActions are the actions Keycloak makes the user complete at
	// their next login, such as CONFIGURE_TOTP. Declaring the field replaces
	// whatever the user has; omitting it leaves them alone. An empty list is
	// therefore how to clear them.
	//
	// Keycloak accepts an unknown action with 204 and then silently drops it,
	// so the names are checked against the server before anything is written.
	RequiredActions []string `yaml:"requiredActions"`
	// Credentials seeds credentials the user cannot otherwise get from config,
	// so a realm rebuilds without a manual admin call. Adding is additive: a
	// credential of the same type is never replaced or removed.
	Credentials []Credential `yaml:"credentials"`
}

// Credential is a credential seeded on a user.
//
// Only TOTP is supported. Passwords already have their own fields, and the
// remaining Keycloak credential types are either device-bound (WebAuthn) or
// generated for one-time display (recovery codes), so neither belongs in a
// config file.
type Credential struct {
	// Type is the Keycloak credential type. Only "otp" is supported.
	Type string `yaml:"type"`
	// Secret is the shared secret, and its handling is the one thing worth
	// reading twice. Keycloak uses these characters directly as the HMAC key;
	// it does not base32-decode them. An authenticator app must therefore be
	// given base32(secret), which is exactly what Keycloak's own QR code shows
	// once the credential exists. Supply it through ${VAR}, never inline.
	Secret string `yaml:"secret"`
	// Label is shown against the credential in the account console. It also
	// distinguishes two credentials of the same type, which Keycloak allows.
	Label string `yaml:"label"`
	// Digits, Period and Algorithm default to Keycloak's own values: 6 digits,
	// a 30-second period and HmacSHA1. Change them only to match an existing
	// authenticator.
	Digits    int    `yaml:"digits"`
	Period    int    `yaml:"period"`
	Algorithm string `yaml:"algorithm"`
}

// UserRoles defines realm and client role assignments for a user or service account.
type UserRoles struct {
	Realm   []string            `yaml:"realm"`
	Clients map[string][]string `yaml:"clients"` // clientId -> client role names to grant; clientId keys support ${VAR} expansion
}

// Group defines a Keycloak group to provision within a realm. Groups may nest
// arbitrarily via SubGroups and may be granted realm and client roles.
type Group struct {
	Name        string              `yaml:"name"`
	Attributes  map[string][]string `yaml:"attributes"`  // Keycloak group attributes are multivalued
	RealmRoles  []string            `yaml:"realmRoles"`  // realm role names to grant the group
	ClientRoles map[string][]string `yaml:"clientRoles"` // clientId -> client role names to grant; clientId keys support ${VAR} expansion
	SubGroups   []Group             `yaml:"subGroups"`
}

// containsNullByte returns true if s contains a null byte (\x00).
func containsNullByte(s string) bool {
	return strings.ContainsRune(s, '\x00')
}

// envVarPattern matches `$${VAR}` (escaped, kept literal as `${VAR}`)
// or `${VAR}` (substituted).
var envVarPattern = regexp.MustCompile(`\$\$\{([^}]+)}|\$\{([^}]+)}`)

// expandEnvVars replaces ${VAR} references with their environment variable values.
// `$${VAR}` is an escape that yields the literal `${VAR}`.
// Unresolved `${VAR}` references are left as-is.
func expandEnvVars(s string) string {
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		groups := envVarPattern.FindStringSubmatch(match)
		if groups[1] != "" {
			return "${" + groups[1] + "}"
		}
		if val, ok := os.LookupEnv(groups[2]); ok {
			return val
		}
		return match
	})
}

// expandUserRoles expands env vars in a UserRoles struct.
func expandUserRoles(roles *UserRoles) {
	if roles == nil {
		return
	}
	for i := range roles.Realm {
		roles.Realm[i] = expandEnvVars(roles.Realm[i])
	}
	roles.Clients = expandMultiValueMap(roles.Clients)
}

// expandUsers expands env vars in a slice of User structs.
func expandUsers(users []User) {
	for i := range users {
		u := &users[i]
		u.Username = expandEnvVars(u.Username)
		u.ID = expandEnvVars(u.ID)
		u.Password = expandEnvVars(u.Password)
		u.InitialPassword = expandEnvVars(u.InitialPassword)
		u.Email = expandEnvVars(u.Email)
		u.FirstName = expandEnvVars(u.FirstName)
		u.LastName = expandEnvVars(u.LastName)
		expandUserRoles(u.Roles)
		for j := range u.Groups {
			u.Groups[j] = expandEnvVars(u.Groups[j])
		}
		for j := range u.RequiredActions {
			u.RequiredActions[j] = expandEnvVars(u.RequiredActions[j])
		}
		for j := range u.Credentials {
			c := &u.Credentials[j]
			c.Type = expandEnvVars(c.Type)
			c.Secret = expandEnvVars(c.Secret)
			c.Label = expandEnvVars(c.Label)
			c.Algorithm = expandEnvVars(c.Algorithm)
		}
	}
}

// expandAuthenticationExecutions expands env vars in an execution tree.
func expandAuthenticationExecutions(executions []AuthenticationExecution) {
	for i := range executions {
		e := &executions[i]
		e.Provider = expandEnvVars(e.Provider)
		e.Subflow = expandEnvVars(e.Subflow)
		e.ProviderId = expandEnvVars(e.ProviderId)
		e.Description = expandEnvVars(e.Description)
		e.Requirement = expandEnvVars(e.Requirement)
		e.Config = expandStringMap(e.Config)
		expandAuthenticationExecutions(e.Executions)
	}
}

// expandIdentityProvider expands env vars in an identity provider, its config
// and its mappers. Secrets reach the config through ${VAR} in Config, so no
// separate secret handling is needed.
func expandIdentityProvider(p *IdentityProvider) {
	p.Alias = expandEnvVars(p.Alias)
	p.DisplayName = expandEnvVars(p.DisplayName)
	p.ProviderId = expandEnvVars(p.ProviderId)
	p.FirstBrokerLoginFlowAlias = expandEnvVars(p.FirstBrokerLoginFlowAlias)
	p.PostBrokerLoginFlowAlias = expandEnvVars(p.PostBrokerLoginFlowAlias)
	p.Config = expandStringMap(p.Config)

	for i := range p.Mappers {
		m := &p.Mappers[i]
		m.Name = expandEnvVars(m.Name)
		m.IdentityProviderMapper = expandEnvVars(m.IdentityProviderMapper)
		m.Config = expandStringMap(m.Config)
	}
}

// expandManagementPermissions expands env vars in the clients each permission
// scope is granted to, so a grantee can be supplied per environment like every
// other name in the config.
//
// Scope names are not expanded: they are Keycloak's own vocabulary, not
// deployment-specific.
func expandManagementPermissions(mp *ManagementPermissions) {
	if mp == nil {
		return
	}

	for scope, granted := range mp.Scopes {
		for i := range granted.Clients {
			granted.Clients[i] = expandEnvVars(granted.Clients[i])
		}

		mp.Scopes[scope] = granted
	}
}

// expandAuthenticationBindings expands env vars in the realm flow bindings.
func expandAuthenticationBindings(b *AuthenticationBindings) {
	if b == nil {
		return
	}

	b.BrowserFlow = expandEnvVars(b.BrowserFlow)
	b.DirectGrantFlow = expandEnvVars(b.DirectGrantFlow)
	b.ResetCredentialsFlow = expandEnvVars(b.ResetCredentialsFlow)
	b.RegistrationFlow = expandEnvVars(b.RegistrationFlow)
	b.ClientAuthenticationFlow = expandEnvVars(b.ClientAuthenticationFlow)
	b.DockerAuthenticationFlow = expandEnvVars(b.DockerAuthenticationFlow)
	b.FirstBrokerLoginFlow = expandEnvVars(b.FirstBrokerLoginFlow)
}

// expandOrganization expands env vars in an organization and its domains,
// attributes and member list.
func expandOrganization(o *Organization) {
	o.Name = expandEnvVars(o.Name)
	o.Alias = expandEnvVars(o.Alias)
	o.Description = expandEnvVars(o.Description)
	o.RedirectUrl = expandEnvVars(o.RedirectUrl)

	for i := range o.Domains {
		o.Domains[i].Name = expandEnvVars(o.Domains[i].Name)
	}

	for i := range o.Members {
		o.Members[i] = expandEnvVars(o.Members[i])
	}

	for i := range o.IdentityProviders {
		o.IdentityProviders[i] = expandEnvVars(o.IdentityProviders[i])
	}

	for i := range o.Groups {
		expandOrganizationGroup(&o.Groups[i])
	}

	o.Attributes = expandMultiValueMap(o.Attributes)
}

// expandOrganizationGroup expands env vars in an organization group and, by
// recursion, its subgroups.
func expandOrganizationGroup(g *OrganizationGroup) {
	g.Name = expandEnvVars(g.Name)
	g.Attributes = expandMultiValueMap(g.Attributes)

	for i := range g.Members {
		g.Members[i] = expandEnvVars(g.Members[i])
	}

	for i := range g.SubGroups {
		expandOrganizationGroup(&g.SubGroups[i])
	}
}

// expandMultiValueMap returns a copy of m with env vars expanded in keys and
// in every value. It returns m unchanged when empty so a nil map stays nil.
//
// When two source keys expand to the same key — "${VAR}" expanding to a literal
// key the config also names, or two variables holding the same value — their
// value lists are concatenated rather than one side silently winning.
// Source keys are visited in sorted order so that merge yields the same result
// on every run, which map iteration order alone would not guarantee.
func expandMultiValueMap(m map[string][]string) map[string][]string {
	if len(m) == 0 {
		return m
	}

	expanded := make(map[string][]string, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		values := m[k]

		vs := make([]string, len(values))
		for i, v := range values {
			vs[i] = expandEnvVars(v)
		}

		key := expandEnvVars(k)
		expanded[key] = append(expanded[key], vs...)
	}

	return expanded
}

// expandProtocolMappers expands env vars in a protocol mapper list, which is
// shared between clients and client scopes.
func expandProtocolMappers(mappers []ProtocolMapper) {
	for i := range mappers {
		pm := &mappers[i]
		pm.Name = expandEnvVars(pm.Name)
		pm.Protocol = expandEnvVars(pm.Protocol)
		pm.ProtocolMapper = expandEnvVars(pm.ProtocolMapper)
		for ck, cv := range pm.Config {
			pm.Config[ck] = expandEnvVars(cv)
		}
	}
}

// normalizeSslRequired lowercases the sslRequired value to match Keycloak's API.
func normalizeSslRequired(value string) string {
	return strings.ToLower(value)
}

// expandStringMap returns a copy of m with env vars expanded in both keys and
// values. It returns m unchanged when empty so callers keep a nil map nil.
func expandStringMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return m
	}

	expanded := make(map[string]string, len(m))
	for k, v := range m {
		expanded[expandEnvVars(k)] = expandEnvVars(v)
	}

	return expanded
}

// expandConfig walks the config and expands env vars in string fields.
func expandConfig(cfg *Config) {
	if cfg.MasterRealm != nil {
		cfg.MasterRealm.SslRequired = normalizeSslRequired(expandEnvVars(cfg.MasterRealm.SslRequired))
		expandUsers(cfg.MasterRealm.Users)
	}

	for i := range cfg.Realms {
		r := &cfg.Realms[i]
		r.Realm = expandEnvVars(r.Realm)
		r.DisplayName = expandEnvVars(r.DisplayName)
		r.SslRequired = normalizeSslRequired(expandEnvVars(r.SslRequired))
		r.LoginTheme = expandEnvVars(r.LoginTheme)
		r.Attributes = expandStringMap(r.Attributes)

		for j := range r.ClientScopes {
			cs := &r.ClientScopes[j]
			cs.Name = expandEnvVars(cs.Name)
			cs.Description = expandEnvVars(cs.Description)
			cs.Protocol = expandEnvVars(cs.Protocol)
			cs.Attributes = expandStringMap(cs.Attributes)
			expandProtocolMappers(cs.ProtocolMappers)
			expandUserRoles(cs.ScopeMappings)
		}

		for j := range r.Clients {
			c := &r.Clients[j]
			c.ClientID = expandEnvVars(c.ClientID)
			c.Secret = expandEnvVars(c.Secret)
			c.Name = expandEnvVars(c.Name)
			c.Protocol = expandEnvVars(c.Protocol)
			c.RootUrl = expandEnvVars(c.RootUrl)
			c.BaseUrl = expandEnvVars(c.BaseUrl)
			c.AdminUrl = expandEnvVars(c.AdminUrl)
			for k := range c.RedirectUris {
				c.RedirectUris[k] = expandEnvVars(c.RedirectUris[k])
			}
			for k := range c.WebOrigins {
				c.WebOrigins[k] = expandEnvVars(c.WebOrigins[k])
			}
			for k := range c.DefaultClientScopes {
				c.DefaultClientScopes[k] = expandEnvVars(c.DefaultClientScopes[k])
			}
			for k := range c.OptionalClientScopes {
				c.OptionalClientScopes[k] = expandEnvVars(c.OptionalClientScopes[k])
			}
			for k := range c.DefaultAcrValues {
				c.DefaultAcrValues[k] = expandEnvVars(c.DefaultAcrValues[k])
			}
			c.Attributes = expandStringMap(c.Attributes)
			c.AuthenticationFlowBindingOverrides = expandStringMap(c.AuthenticationFlowBindingOverrides)
			expandProtocolMappers(c.ProtocolMappers)
			for k := range c.ClientRoles {
				c.ClientRoles[k].Name = expandEnvVars(c.ClientRoles[k].Name)
				c.ClientRoles[k].Description = expandEnvVars(c.ClientRoles[k].Description)
			}
			expandUserRoles(c.ServiceAccountRoles)
			expandUserRoles(c.ScopeMappings)
			expandManagementPermissions(c.ManagementPermissions)
		}

		for j := range r.Roles {
			r.Roles[j].Name = expandEnvVars(r.Roles[j].Name)
			r.Roles[j].Description = expandEnvVars(r.Roles[j].Description)
		}

		expandUsers(r.Users)

		for j := range r.Organizations {
			expandOrganization(&r.Organizations[j])
		}

		for j := range r.AuthenticationFlows {
			f := &r.AuthenticationFlows[j]
			f.Alias = expandEnvVars(f.Alias)
			f.Description = expandEnvVars(f.Description)
			f.ProviderId = expandEnvVars(f.ProviderId)
			f.CopyFrom = expandEnvVars(f.CopyFrom)
			expandAuthenticationExecutions(f.Executions)
		}

		expandAuthenticationBindings(r.AuthenticationBindings)

		for j := range r.IdentityProviders {
			expandIdentityProvider(&r.IdentityProviders[j])
		}

		for j := range r.Groups {
			expandGroup(&r.Groups[j])
		}
	}
}

// expandGroup recursively expands env vars in a group's string fields and subgroups.
func expandGroup(g *Group) {
	g.Name = expandEnvVars(g.Name)
	g.Attributes = expandMultiValueMap(g.Attributes)
	for i := range g.RealmRoles {
		g.RealmRoles[i] = expandEnvVars(g.RealmRoles[i])
	}
	g.ClientRoles = expandMultiValueMap(g.ClientRoles)
	for i := range g.SubGroups {
		expandGroup(&g.SubGroups[i])
	}
}

// Load reads and parses a YAML config file, expanding env vars and validating.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config YAML: %w", err)
	}
	// Reject trailing YAML documents (everything after the first `---`).
	var trailing yaml.Node
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("parsing config YAML: file contains multiple YAML documents; only one is supported")
		}
		return nil, fmt.Errorf("parsing config YAML: %w", err)
	}

	expandConfig(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

// scanNullBytes checks a set of named string values for null bytes.
func scanNullBytes(fields map[string]string) error {
	for path, value := range fields {
		if containsNullByte(value) {
			return fmt.Errorf("%s: contains null byte", path)
		}
	}
	return nil
}

// scanSliceNullBytes checks a slice of strings for null bytes.
func scanSliceNullBytes(prefix string, values []string) error {
	for i, v := range values {
		if containsNullByte(v) {
			return fmt.Errorf("%s[%d]: contains null byte", prefix, i)
		}
	}
	return nil
}

// validateAttributes checks an attribute map for empty keys and null bytes in
// keys or values. path is the config path of the map itself, e.g.
// "realms[0].attributes".
func validateAttributes(path string, attrs map[string]string) error {
	for k, v := range attrs {
		if k == "" {
			return fmt.Errorf("%s: attribute name is required", path)
		}
		if containsNullByte(k) || containsNullByte(v) {
			return fmt.Errorf("%s: contains null byte", path)
		}
	}

	return nil
}

// validateAcrLoaMap checks an ACR-to-LoA map for empty ACR names, null bytes
// and negative levels. Keycloak treats the level as a non-negative integer.
func validateAcrLoaMap(path string, m map[string]int) error {
	for acr, level := range m {
		if acr == "" {
			return fmt.Errorf("%s: acr value is required", path)
		}
		if containsNullByte(acr) {
			return fmt.Errorf("%s: contains null byte", path)
		}
		if level < 0 {
			return fmt.Errorf("%s: level for %q must not be negative", path, acr)
		}
	}

	return nil
}

// uuidPattern matches the canonical 8-4-4-4-12 hexadecimal form Keycloak uses
// for resource ids. A format check does not warrant promoting
// github.com/google/uuid from an indirect dependency to a direct one.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// validateStrategy returns an error if the strategy value is invalid.
func validateStrategy(path, value string) error {
	if !validStrategies[value] {
		return fmt.Errorf("%s: invalid strategy %q (must be \"create\" or \"update\")", path, value)
	}
	return nil
}

// validate checks the config for required fields and consistency.
func validate(cfg *Config) error {
	if err := validateStrategy("strategy", cfg.Strategy); err != nil {
		return err
	}

	if cfg.MasterRealm != nil {
		if err := validateMasterRealm(cfg.MasterRealm); err != nil {
			return err
		}
	}

	realmNames := make(map[string]bool)

	for i, r := range cfg.Realms {
		if err := validateStrategy(fmt.Sprintf("realms[%d].strategy", i), r.Strategy); err != nil {
			return err
		}
		if r.Realm == "" {
			return fmt.Errorf("realms[%d].realm: name is required", i)
		}
		if containsNullByte(r.Realm) {
			return fmt.Errorf("realms[%d].realm: contains null byte", i)
		}
		if r.Realm == "master" {
			return fmt.Errorf("realms[%d].realm: provisioning the \"master\" realm is not allowed; use masterRealm instead", i)
		}
		if realmNames[r.Realm] {
			return fmt.Errorf("realms[%d].realm: duplicate realm name %q", i, r.Realm)
		}
		realmNames[r.Realm] = true

		if !validSslRequired[r.SslRequired] {
			return fmt.Errorf("realms[%d].sslRequired: invalid value %q (must be \"external\", \"all\", or \"none\")", i, r.SslRequired)
		}

		if err := scanNullBytes(map[string]string{
			fmt.Sprintf("realms[%d].displayName", i): r.DisplayName,
			fmt.Sprintf("realms[%d].loginTheme", i):  r.LoginTheme,
		}); err != nil {
			return err
		}

		if err := validateAttributes(fmt.Sprintf("realms[%d].attributes", i), r.Attributes); err != nil {
			return err
		}

		if err := validateAcrLoaMap(fmt.Sprintf("realms[%d].acrLoaMap", i), r.AcrLoaMap); err != nil {
			return err
		}

		if err := validateClientScopes(i, r.ClientScopes); err != nil {
			return err
		}

		if err := validateClients(i, r.Clients, r.AcrLoaMap); err != nil {
			return err
		}

		if err := validateRealmRoles(i, r.Roles); err != nil {
			return err
		}

		if err := validateUsers(fmt.Sprintf("realms[%d]", i), r.Users); err != nil {
			return err
		}

		if err := validateGroups(fmt.Sprintf("realms[%d].groups", i), r.Groups); err != nil {
			return err
		}

		if err := validateOrganizations(i, r); err != nil {
			return err
		}

		if err := validateAuthenticationFlows(i, r.AuthenticationFlows); err != nil {
			return err
		}

		if err := validateIdentityProviders(i, r.IdentityProviders); err != nil {
			return err
		}

		// Last of the realm's checks: it reads both the organizations and the
		// identity providers, so both must already be known to be well-formed.
		if err := validateOrganizationIdentityProviderDomains(i, r); err != nil {
			return err
		}
	}

	return nil
}

func validateClientScopes(realmIdx int, scopes []ClientScope) error {
	names := make(map[string]bool)

	for i, cs := range scopes {
		prefix := fmt.Sprintf("realms[%d].clientScopes[%d]", realmIdx, i)

		if cs.Name == "" {
			return fmt.Errorf("%s.name: is required", prefix)
		}
		if containsNullByte(cs.Name) {
			return fmt.Errorf("%s.name: contains null byte", prefix)
		}
		if names[cs.Name] {
			return fmt.Errorf("%s.name: duplicate client scope name %q", prefix, cs.Name)
		}
		names[cs.Name] = true

		if !validClientScopeTypes[cs.Type] {
			return fmt.Errorf("%s.type: invalid value %q (must be \"default\", \"optional\", or \"none\")", prefix, cs.Type)
		}

		if err := scanNullBytes(map[string]string{
			prefix + ".description": cs.Description,
			prefix + ".protocol":    cs.Protocol,
		}); err != nil {
			return err
		}

		if err := validateAttributes(prefix+".attributes", cs.Attributes); err != nil {
			return err
		}

		if err := validateProtocolMappers(prefix, cs.ProtocolMappers); err != nil {
			return err
		}

		if err := validateRoleSet(prefix+".scopeMappings", cs.ScopeMappings); err != nil {
			return err
		}
	}

	return nil
}

func validateAuthenticationFlows(realmIdx int, flows []AuthenticationFlow) error {
	// Flow aliases are unique per realm in Keycloak, and subflow aliases share
	// that namespace, so they are checked together.
	aliases := make(map[string]bool)

	for i, f := range flows {
		prefix := fmt.Sprintf("realms[%d].authenticationFlows[%d]", realmIdx, i)

		if f.Alias == "" {
			return fmt.Errorf("%s.alias: is required", prefix)
		}
		if aliases[f.Alias] {
			return fmt.Errorf("%s.alias: duplicate flow alias %q", prefix, f.Alias)
		}
		aliases[f.Alias] = true

		if !validFlowProviderIds[f.ProviderId] {
			return fmt.Errorf("%s.providerId: invalid value %q (must be \"basic-flow\" or \"form-flow\")", prefix, f.ProviderId)
		}

		if err := scanNullBytes(map[string]string{
			prefix + ".alias":       f.Alias,
			prefix + ".description": f.Description,
			prefix + ".copyFrom":    f.CopyFrom,
		}); err != nil {
			return err
		}

		if err := validateAuthenticationExecutions(prefix+".executions", f.Executions, aliases); err != nil {
			return err
		}
	}

	return nil
}

// validateAuthenticationExecutions validates an execution tree recursively,
// following the same shape as validateGroups. aliases carries the realm-wide
// flow alias namespace so subflows cannot collide with each other or with a
// top-level flow.
func validateAuthenticationExecutions(prefix string, executions []AuthenticationExecution, aliases map[string]bool) error {
	for i, e := range executions {
		path := fmt.Sprintf("%s[%d]", prefix, i)

		switch {
		case e.Provider == "" && e.Subflow == "":
			return fmt.Errorf("%s: either provider or subflow is required", path)
		case e.Provider != "" && e.Subflow != "":
			return fmt.Errorf("%s: provider and subflow are mutually exclusive", path)
		}

		if !validFlowRequirements[e.Requirement] {
			return fmt.Errorf("%s.requirement: invalid value %q (must be REQUIRED, ALTERNATIVE, DISABLED, or CONDITIONAL)", path, e.Requirement)
		}

		if err := scanNullBytes(map[string]string{
			path + ".provider":    e.Provider,
			path + ".subflow":     e.Subflow,
			path + ".description": e.Description,
		}); err != nil {
			return err
		}

		if err := validateAttributes(path+".config", e.Config); err != nil {
			return err
		}

		if e.Provider != "" {
			if len(e.Executions) > 0 {
				return fmt.Errorf("%s: only a subflow can contain executions", path)
			}
			if e.ProviderId != "" {
				return fmt.Errorf("%s.providerId: only valid on a subflow", path)
			}
			continue
		}

		if aliases[e.Subflow] {
			return fmt.Errorf("%s.subflow: duplicate flow alias %q", path, e.Subflow)
		}
		aliases[e.Subflow] = true

		if !validFlowProviderIds[e.ProviderId] {
			return fmt.Errorf("%s.providerId: invalid value %q (must be \"basic-flow\" or \"form-flow\")", path, e.ProviderId)
		}

		if err := validateAuthenticationExecutions(path+".executions", e.Executions, aliases); err != nil {
			return err
		}
	}

	return nil
}

func validateIdentityProviders(realmIdx int, idps []IdentityProvider) error {
	aliases := make(map[string]bool)

	for i, idp := range idps {
		prefix := fmt.Sprintf("realms[%d].identityProviders[%d]", realmIdx, i)

		if idp.Alias == "" {
			return fmt.Errorf("%s.alias: is required", prefix)
		}
		// The alias is a URL path segment. Escaping would turn a slash into
		// %2F and address something other than what was written.
		if strings.Contains(idp.Alias, "/") {
			return fmt.Errorf("%s.alias: %q must not contain '/'", prefix, idp.Alias)
		}
		if aliases[idp.Alias] {
			return fmt.Errorf("%s.alias: duplicate identity provider alias %q", prefix, idp.Alias)
		}
		aliases[idp.Alias] = true

		if idp.ProviderId == "" {
			return fmt.Errorf("%s.providerId: is required", prefix)
		}

		if err := scanNullBytes(map[string]string{
			prefix + ".alias":                     idp.Alias,
			prefix + ".displayName":               idp.DisplayName,
			prefix + ".providerId":                idp.ProviderId,
			prefix + ".firstBrokerLoginFlowAlias": idp.FirstBrokerLoginFlowAlias,
			prefix + ".postBrokerLoginFlowAlias":  idp.PostBrokerLoginFlowAlias,
		}); err != nil {
			return err
		}

		if err := validateAttributes(prefix+".config", idp.Config); err != nil {
			return err
		}

		names := make(map[string]bool)

		for j, m := range idp.Mappers {
			mPrefix := fmt.Sprintf("%s.mappers[%d]", prefix, j)

			if m.Name == "" {
				return fmt.Errorf("%s.name: is required", mPrefix)
			}
			if names[m.Name] {
				return fmt.Errorf("%s.name: duplicate mapper name %q", mPrefix, m.Name)
			}
			names[m.Name] = true

			if m.IdentityProviderMapper == "" {
				return fmt.Errorf("%s.identityProviderMapper: is required", mPrefix)
			}

			if err := scanNullBytes(map[string]string{
				mPrefix + ".name":                   m.Name,
				mPrefix + ".identityProviderMapper": m.IdentityProviderMapper,
			}); err != nil {
				return err
			}

			if err := validateAttributes(mPrefix+".config", m.Config); err != nil {
				return err
			}
		}
	}

	return nil
}

func validateOrganizations(realmIdx int, r Realm) error {
	if len(r.Organizations) == 0 {
		return nil
	}

	// Mirrors the serviceAccountRoles/serviceAccountsEnabled rule: a feature
	// that cannot work without its realm-level toggle is rejected up front
	// rather than failing mid-run against Keycloak.
	if r.OrganizationsEnabled == nil || !*r.OrganizationsEnabled {
		return fmt.Errorf("realms[%d].organizations: requires organizationsEnabled: true on the realm", realmIdx)
	}

	names := make(map[string]bool)
	// Tracks which organization claims each identity provider alias, so a
	// second claim can name the first.
	linkedIdPs := make(map[string]string)

	for i, o := range r.Organizations {
		prefix := fmt.Sprintf("realms[%d].organizations[%d]", realmIdx, i)

		if o.Name == "" {
			return fmt.Errorf("%s.name: is required", prefix)
		}
		if names[o.Name] {
			return fmt.Errorf("%s.name: duplicate organization name %q", prefix, o.Name)
		}
		names[o.Name] = true

		if err := scanNullBytes(map[string]string{
			prefix + ".name":        o.Name,
			prefix + ".alias":       o.Alias,
			prefix + ".description": o.Description,
			prefix + ".redirectUrl": o.RedirectUrl,
		}); err != nil {
			return err
		}

		domains := make(map[string]bool)
		for j, d := range o.Domains {
			if d.Name == "" {
				return fmt.Errorf("%s.domains[%d].name: is required", prefix, j)
			}
			if containsNullByte(d.Name) {
				return fmt.Errorf("%s.domains[%d].name: contains null byte", prefix, j)
			}
			if domains[d.Name] {
				return fmt.Errorf("%s.domains[%d].name: duplicate domain %q", prefix, j, d.Name)
			}
			domains[d.Name] = true
		}

		if err := validateMultiValueAttributes(prefix+".attributes", o.Attributes); err != nil {
			return err
		}

		if err := validateMemberNames(prefix+".members", o.Members); err != nil {
			return err
		}

		if err := validateOrganizationGroups(prefix+".groups", o.Groups); err != nil {
			return err
		}

		if err := validateOrganizationIdentityProviders(prefix, o, linkedIdPs); err != nil {
			return err
		}
	}

	return nil
}

// orgDomainConfigKey binds an organization-linked identity provider to one of
// its organization's domains. Keycloak validates it — but only once the
// provider is actually linked, which is what makes it worth checking here.
const orgDomainConfigKey = "kc.org.domain"

// validateOrganizationIdentityProviderDomains checks that every identity
// provider setting kc.org.domain names a domain of the organization that links
// it.
//
// Keycloak applies this rule itself, but only to a provider already associated
// with an organization, and the provisioner creates providers before it makes
// the association. So on a realm being built from scratch the provider is
// unlinked when it is written, the rule does not apply, and a domain belonging
// to no organization is accepted with 201. The same config applied to a realm
// where the association already exists is rejected with
// "Domain does not match any domain from the organization".
//
// The result is a config that passes on first provision and fails on every run
// after it, with nothing changed in between — and in the meantime an
// organization whose provider matches no domain, so email-based redirection
// silently never fires. Checking here makes the outcome the same either way,
// before anything is written.
//
// A provider no organization in *this config* links is left alone: the
// association may already exist on the server or be managed elsewhere, and the
// config cannot tell. That is the one case this cannot cover.
//
// The domain comparison is exact, matching Keycloak's — see
// organizationHasDomain.
func validateOrganizationIdentityProviderDomains(realmIdx int, r Realm) error {
	if len(r.Organizations) == 0 {
		return nil
	}

	// Which organization claims each alias. Duplicate claims are already
	// rejected by validateOrganizationIdentityProviders, so the last write
	// cannot mask a conflict.
	owner := make(map[string]Organization)

	for _, o := range r.Organizations {
		for _, alias := range o.IdentityProviders {
			owner[alias] = o
		}
	}

	for i, idp := range r.IdentityProviders {
		domain, ok := idp.Config[orgDomainConfigKey]
		if !ok || domain == "" {
			continue
		}

		org, linked := owner[idp.Alias]
		if !linked {
			continue
		}

		if organizationHasDomain(org, domain) {
			continue
		}

		names := make([]string, 0, len(org.Domains))
		for _, d := range org.Domains {
			names = append(names, d.Name)
		}

		// The index alone identifies the provider positionally; the alias is what
		// the reader is looking for, and the point of checking here rather than
		// letting Keycloak answer is that its own 400 names nothing at all.
		path := fmt.Sprintf("realms[%d].identityProviders[%d].config[%s]", realmIdx, i, orgDomainConfigKey)

		if len(names) == 0 {
			return fmt.Errorf("%s: identity provider %q has %q, but organization %q declares no domains",
				path, idp.Alias, domain, org.Name)
		}

		if organizationHasDomainIgnoringCase(org, domain) {
			return fmt.Errorf("%s: identity provider %q has %q, which differs only in case from a domain of organization %q (%s); Keycloak compares them exactly",
				path, idp.Alias, domain, org.Name, strings.Join(names, ", "))
		}

		return fmt.Errorf("%s: identity provider %q has %q, which is not a domain of organization %q (which has %s)",
			path, idp.Alias, domain, org.Name, strings.Join(names, ", "))
	}

	return nil
}

// organizationHasDomain reports whether the organization declares this domain.
//
// The comparison is exact. Domain names are case-insensitive as names, so
// ignoring case is the intuitive choice and it is the wrong one: Keycloak
// compares the two strings literally and answers 400 for a difference of case
// alone, measured on 26.6. Accepting one here would wave through precisely the
// config this check exists to catch — good on first provision, refused on every
// run after. See TestIntegrationOrganizationDomainIsCaseSensitive.
func organizationHasDomain(o Organization, domain string) bool {
	for _, d := range o.Domains {
		if d.Name == domain {
			return true
		}
	}

	return false
}

// organizationHasDomainIgnoringCase reports whether the only thing separating
// the configured domain from one the organization has is capitalisation. It
// exists to say so in the error, because "acme.com is not a domain of acme"
// is a baffling thing to read next to a domains list containing ACME.com.
func organizationHasDomainIgnoringCase(o Organization, domain string) bool {
	for _, d := range o.Domains {
		if strings.EqualFold(d.Name, domain) {
			return true
		}
	}

	return false
}

// validateOrganizationIdentityProviders checks the aliases one organization
// associates, and records them in linkedIdPs so a provider claimed by two
// organizations is caught.
//
// Keycloak allows a provider to belong to at most one organization and answers
// 400 for a second claim, so catching it here turns a mid-run failure into a
// config error. An alias not declared under realms[i].identityProviders is
// deliberately allowed: it may already exist on the server, the same way
// members may name pre-existing users.
func validateOrganizationIdentityProviders(prefix string, o Organization, linkedIdPs map[string]string) error {
	seen := make(map[string]bool)

	for i, alias := range o.IdentityProviders {
		path := fmt.Sprintf("%s.identityProviders[%d]", prefix, i)

		if alias == "" {
			return fmt.Errorf("%s: identity provider alias is required", path)
		}
		if containsNullByte(alias) {
			return fmt.Errorf("%s: contains null byte", path)
		}
		if seen[alias] {
			return fmt.Errorf("%s: duplicate identity provider alias %q", path, alias)
		}
		seen[alias] = true

		if owner, taken := linkedIdPs[alias]; taken {
			return fmt.Errorf(
				"%s: identity provider %q is already associated with organization %q; Keycloak allows only one",
				path, alias, owner)
		}
		linkedIdPs[alias] = o.Name
	}

	return nil
}

// validateMultiValueAttributes checks a multivalued attribute map for empty
// keys and null bytes. It is shared by organizations and their groups.
func validateMultiValueAttributes(path string, attrs map[string][]string) error {
	for k, values := range attrs {
		if k == "" {
			return fmt.Errorf("%s: attribute name is required", path)
		}
		if containsNullByte(k) {
			return fmt.Errorf("%s: contains null byte", path)
		}
		if err := scanSliceNullBytes(fmt.Sprintf("%s[%s]", path, k), values); err != nil {
			return err
		}
	}

	return nil
}

func validateMemberNames(path string, members []string) error {
	for i, m := range members {
		if m == "" {
			return fmt.Errorf("%s[%d]: username is required", path, i)
		}
		if containsNullByte(m) {
			return fmt.Errorf("%s[%d]: contains null byte", path, i)
		}
	}

	return nil
}

// validateOrganizationGroups validates an organization group tree recursively,
// following the same shape as validateGroups: names must be unique among
// siblings, but may repeat at different levels.
func validateOrganizationGroups(prefix string, groups []OrganizationGroup) error {
	names := make(map[string]bool)

	for i, g := range groups {
		path := fmt.Sprintf("%s[%d]", prefix, i)

		if g.Name == "" {
			return fmt.Errorf("%s.name: is required", path)
		}
		if containsNullByte(g.Name) {
			return fmt.Errorf("%s.name: contains null byte", path)
		}
		if names[g.Name] {
			return fmt.Errorf("%s.name: duplicate group name %q", path, g.Name)
		}
		names[g.Name] = true

		if g.RealmRoles != nil || g.ClientRoles != nil {
			return fmt.Errorf("%s: organization groups cannot carry role mappings; "+
				"Keycloak stores them but they never reach a member's effective roles "+
				"or any token claim. Assign the roles to the members directly, or use a "+
				"realm group", path)
		}

		if err := validateMultiValueAttributes(path+".attributes", g.Attributes); err != nil {
			return err
		}

		if err := validateMemberNames(path+".members", g.Members); err != nil {
			return err
		}

		if err := validateOrganizationGroups(path+".subGroups", g.SubGroups); err != nil {
			return err
		}
	}

	return nil
}

func validateMasterRealm(mr *MasterRealmConfig) error {
	if !validSslRequired[mr.SslRequired] {
		return fmt.Errorf("masterRealm.sslRequired: invalid value %q (must be \"external\", \"all\", or \"none\")", mr.SslRequired)
	}
	if err := validateUsers("masterRealm", mr.Users); err != nil {
		return err
	}
	return nil
}

func validateUsers(prefix string, users []User) error {
	names := make(map[string]bool)

	for i, u := range users {
		p := fmt.Sprintf("%s.users[%d]", prefix, i)

		if u.Username == "" {
			return fmt.Errorf("%s.username: is required", p)
		}
		if containsNullByte(u.Username) {
			return fmt.Errorf("%s.username: contains null byte", p)
		}
		if u.ID != "" && !uuidPattern.MatchString(u.ID) {
			return fmt.Errorf("%s.id: %q is not a UUID; Keycloak ids are 8-4-4-4-12 hexadecimal", p, u.ID)
		}
		if names[u.Username] {
			return fmt.Errorf("%s.username: duplicate username %q", p, u.Username)
		}
		names[u.Username] = true

		if u.Password != "" && u.InitialPassword != "" {
			return fmt.Errorf("%s: password and initialPassword are mutually exclusive", p)
		}

		if err := validateRequiredActions(p, u.RequiredActions); err != nil {
			return err
		}

		if err := validateCredentials(p, u.Credentials); err != nil {
			return err
		}

		if err := scanNullBytes(map[string]string{
			p + ".password":        u.Password,
			p + ".initialPassword": u.InitialPassword,
			p + ".email":           u.Email,
			p + ".firstName":       u.FirstName,
			p + ".lastName":        u.LastName,
		}); err != nil {
			return err
		}

		if u.Roles != nil {
			if err := validateUserRoles(p, u.Roles); err != nil {
				return err
			}
		}

		for j, g := range u.Groups {
			if strings.Trim(g, "/") == "" {
				return fmt.Errorf("%s.groups[%d]: group path is required", p, j)
			}
		}
		if err := scanSliceNullBytes(p+".groups", u.Groups); err != nil {
			return err
		}
	}

	return nil
}

func validateUserRoles(prefix string, roles *UserRoles) error {
	return validateRoleSet(prefix+".roles", roles)
}

// validateRoleSet checks a UserRoles wherever it appears. path is where it sits
// in the config — "….roles" under a user, "….scopeMappings" on a client — so
// the message points at the key the reader wrote.
func validateRoleSet(path string, roles *UserRoles) error {
	if roles == nil {
		return nil
	}
	for i, r := range roles.Realm {
		if containsNullByte(r) {
			return fmt.Errorf("%s.realm[%d]: contains null byte", path, i)
		}
	}
	for clientID, clientRoles := range roles.Clients {
		if containsNullByte(clientID) {
			return fmt.Errorf("%s.clients: client ID contains null byte", path)
		}
		for i, r := range clientRoles {
			if containsNullByte(r) {
				return fmt.Errorf("%s.clients.%s[%d]: contains null byte", path, clientID, i)
			}
		}
	}
	return nil
}

func validateClients(realmIdx int, clients []Client, realmAcrLoaMap map[string]int) error {
	clientIDs := make(map[string]bool)

	for j, c := range clients {
		prefix := fmt.Sprintf("realms[%d].clients[%d]", realmIdx, j)

		if c.ClientID == "" {
			return fmt.Errorf("%s.clientId: is required", prefix)
		}
		if containsNullByte(c.ClientID) {
			return fmt.Errorf("%s.clientId: contains null byte", prefix)
		}
		if clientIDs[c.ClientID] {
			return fmt.Errorf("%s.clientId: duplicate client ID %q", prefix, c.ClientID)
		}
		clientIDs[c.ClientID] = true

		if err := scanNullBytes(map[string]string{
			prefix + ".secret":   c.Secret,
			prefix + ".name":     c.Name,
			prefix + ".protocol": c.Protocol,
			prefix + ".rootUrl":  c.RootUrl,
			prefix + ".baseUrl":  c.BaseUrl,
			prefix + ".adminUrl": c.AdminUrl,
		}); err != nil {
			return err
		}

		if err := scanSliceNullBytes(prefix+".redirectUris", c.RedirectUris); err != nil {
			return err
		}
		if err := scanSliceNullBytes(prefix+".webOrigins", c.WebOrigins); err != nil {
			return err
		}
		if err := scanSliceNullBytes(prefix+".defaultClientScopes", c.DefaultClientScopes); err != nil {
			return err
		}
		if err := scanSliceNullBytes(prefix+".optionalClientScopes", c.OptionalClientScopes); err != nil {
			return err
		}

		if err := validateDistinctClientScopes(prefix, c); err != nil {
			return err
		}

		if err := validateAttributes(prefix+".attributes", c.Attributes); err != nil {
			return err
		}

		if err := validateAcrLoaMap(prefix+".acrLoaMap", c.AcrLoaMap); err != nil {
			return err
		}

		if err := validateDefaultAcrValues(prefix, c, realmAcrLoaMap); err != nil {
			return err
		}

		for binding, alias := range c.AuthenticationFlowBindingOverrides {
			path := prefix + ".authenticationFlowBindingOverrides"
			if !validFlowBindingOverrides[binding] {
				return fmt.Errorf("%s: invalid binding %q (must be \"browser\" or \"direct_grant\")", path, binding)
			}
			if alias == "" {
				return fmt.Errorf("%s.%s: flow alias is required", path, binding)
			}
			if containsNullByte(alias) {
				return fmt.Errorf("%s.%s: contains null byte", path, binding)
			}
		}

		if err := validateProtocolMappers(prefix, c.ProtocolMappers); err != nil {
			return err
		}

		if err := validateClientRoles(prefix, c.ClientRoles); err != nil {
			return err
		}

		if err := validateRoleSet(prefix+".scopeMappings", c.ScopeMappings); err != nil {
			return err
		}

		if c.ServiceAccountRoles != nil {
			if c.ServiceAccountsEnabled == nil || !*c.ServiceAccountsEnabled {
				return fmt.Errorf("%s.serviceAccountRoles: serviceAccountsEnabled must be true when serviceAccountRoles is set", prefix)
			}
			if err := validateUserRoles(prefix+".serviceAccount", c.ServiceAccountRoles); err != nil {
				return err
			}
		}

		// Token exchange (RFC 8693) requires the requesting client to
		// authenticate at the token endpoint. Public clients cannot, and
		// bearer-only clients are barred from the token endpoint entirely.
		if c.StandardTokenExchangeEnabled != nil && *c.StandardTokenExchangeEnabled {
			if c.PublicClient != nil && *c.PublicClient {
				return fmt.Errorf("%s.standardTokenExchangeEnabled: requires a confidential client (publicClient must be false)", prefix)
			}
			if c.BearerOnly != nil && *c.BearerOnly {
				return fmt.Errorf("%s.standardTokenExchangeEnabled: not supported on a bearer-only client (it cannot call the token endpoint)", prefix)
			}
		}

		if err := validateManagementPermissions(prefix, c.ManagementPermissions); err != nil {
			return err
		}
	}

	return nil
}

// validateManagementPermissions checks a client's fine-grained admin
// permission block.
//
// Scope names are deliberately not checked against a list here: the set is the
// server's to define, and the reconciler validates each name against the
// scopePermissions map Keycloak returns. Only the shape is checked.
func validateManagementPermissions(clientPrefix string, mp *ManagementPermissions) error {
	if mp == nil {
		return nil
	}

	prefix := clientPrefix + ".managementPermissions"

	// Keycloak deletes every scope permission on the client, and the policies
	// attached to them, when fine-grained permissions are turned off. That is a
	// deletion this provisioner will not perform on a resource it may not own,
	// so the value is refused rather than applied.
	if mp.Enabled != nil && !*mp.Enabled {
		return fmt.Errorf("%s.enabled: must be true — Keycloak deletes the client's scope permissions and their policies when fine-grained permissions are disabled, which this provisioner does not do; remove the managementPermissions block to stop managing the client", prefix)
	}

	if len(mp.Scopes) == 0 {
		return fmt.Errorf("%s.scopes: at least one permission scope is required", prefix)
	}

	for name, scope := range mp.Scopes {
		scopePath := fmt.Sprintf("%s.scopes[%s]", prefix, name)

		if name == "" {
			return fmt.Errorf("%s.scopes: permission scope name is required", prefix)
		}
		if containsNullByte(name) {
			return fmt.Errorf("%s: contains null byte", scopePath)
		}

		// An empty list would mean "grant this to nobody", which can only be
		// applied by deleting the policy. Shrinking a list withdraws a grant;
		// emptying it is refused so the tool never deletes.
		if len(scope.Clients) == 0 {
			return fmt.Errorf("%s.clients: at least one client is required (an empty list would mean revoking the scope entirely, which this provisioner does not do; remove the scope to stop managing it)", scopePath)
		}

		seen := make(map[string]bool, len(scope.Clients))

		for k, clientID := range scope.Clients {
			path := fmt.Sprintf("%s.clients[%d]", scopePath, k)

			if clientID == "" {
				return fmt.Errorf("%s: clientId is required", path)
			}
			if containsNullByte(clientID) {
				return fmt.Errorf("%s: contains null byte", path)
			}
			if seen[clientID] {
				return fmt.Errorf("%s: duplicate client %q", path, clientID)
			}

			seen[clientID] = true
		}
	}

	return nil
}

// validateDistinctClientScopes rejects a scope named as both default and
// optional on one client. The two are mutually exclusive in Keycloak, and the
// provisioner moves a scope to the type the config asks for, so a scope in both
// lists would be detached and re-attached on every run.
func validateDistinctClientScopes(prefix string, c Client) error {
	defaults := make(map[string]bool, len(c.DefaultClientScopes))
	for _, name := range c.DefaultClientScopes {
		defaults[name] = true
	}

	for _, name := range c.OptionalClientScopes {
		if defaults[name] {
			return fmt.Errorf("%s: client scope %q is listed as both default and optional", prefix, name)
		}
	}

	return nil
}

func validateProtocolMappers(clientPrefix string, mappers []ProtocolMapper) error {
	names := make(map[string]bool)

	for k, pm := range mappers {
		prefix := fmt.Sprintf("%s.protocolMappers[%d]", clientPrefix, k)

		if pm.Name == "" {
			return fmt.Errorf("%s.name: is required", prefix)
		}
		if containsNullByte(pm.Name) {
			return fmt.Errorf("%s.name: contains null byte", prefix)
		}
		if pm.Protocol == "" {
			return fmt.Errorf("%s.protocol: is required", prefix)
		}
		if containsNullByte(pm.Protocol) {
			return fmt.Errorf("%s.protocol: contains null byte", prefix)
		}
		if pm.ProtocolMapper == "" {
			return fmt.Errorf("%s.protocolMapper: is required", prefix)
		}
		if containsNullByte(pm.ProtocolMapper) {
			return fmt.Errorf("%s.protocolMapper: contains null byte", prefix)
		}
		if names[pm.Name] {
			return fmt.Errorf("%s.name: duplicate protocol mapper name %q", prefix, pm.Name)
		}
		names[pm.Name] = true

		if err := scanNullBytes(map[string]string{
			prefix + ".protocol": pm.Protocol,
		}); err != nil {
			return err
		}

		for ck, cv := range pm.Config {
			if containsNullByte(ck) || containsNullByte(cv) {
				return fmt.Errorf("%s.config: contains null byte", prefix)
			}
		}
	}

	return nil
}

func validateClientRoles(clientPrefix string, roles []ClientRole) error {
	names := make(map[string]bool)

	for k, cr := range roles {
		prefix := fmt.Sprintf("%s.clientRoles[%d]", clientPrefix, k)

		if cr.Name == "" {
			return fmt.Errorf("%s.name: is required", prefix)
		}
		if containsNullByte(cr.Name) {
			return fmt.Errorf("%s.name: contains null byte", prefix)
		}
		if containsNullByte(cr.Description) {
			return fmt.Errorf("%s.description: contains null byte", prefix)
		}
		if names[cr.Name] {
			return fmt.Errorf("%s.name: duplicate client role name %q", prefix, cr.Name)
		}
		names[cr.Name] = true
	}

	return nil
}

func validateRealmRoles(realmIdx int, roles []RealmRole) error {
	names := make(map[string]bool)

	for j, r := range roles {
		prefix := fmt.Sprintf("realms[%d].roles[%d]", realmIdx, j)

		if r.Name == "" {
			return fmt.Errorf("%s.name: is required", prefix)
		}
		if containsNullByte(r.Name) {
			return fmt.Errorf("%s.name: contains null byte", prefix)
		}
		if containsNullByte(r.Description) {
			return fmt.Errorf("%s.description: contains null byte", prefix)
		}
		if names[r.Name] {
			return fmt.Errorf("%s.name: duplicate realm role name %q", prefix, r.Name)
		}
		names[r.Name] = true
	}

	return nil
}

// validateGroups recursively validates a level of groups. prefix is the config
// path to the slice (e.g. "realms[0].groups"). Sibling names must be unique.
func validateGroups(prefix string, groups []Group) error {
	names := make(map[string]bool)

	for i, g := range groups {
		path := fmt.Sprintf("%s[%d]", prefix, i)

		if g.Name == "" {
			return fmt.Errorf("%s.name: is required", path)
		}
		if containsNullByte(g.Name) {
			return fmt.Errorf("%s.name: contains null byte", path)
		}
		if names[g.Name] {
			return fmt.Errorf("%s.name: duplicate group name %q", path, g.Name)
		}
		names[g.Name] = true

		for k, values := range g.Attributes {
			if k == "" {
				return fmt.Errorf("%s.attributes: attribute key is required", path)
			}
			if containsNullByte(k) {
				return fmt.Errorf("%s.attributes: contains null byte", path)
			}
			if err := scanSliceNullBytes(fmt.Sprintf("%s.attributes[%q]", path, k), values); err != nil {
				return err
			}
		}

		for i, role := range g.RealmRoles {
			if role == "" {
				return fmt.Errorf("%s.realmRoles[%d]: is required", path, i)
			}
		}
		if err := scanSliceNullBytes(path+".realmRoles", g.RealmRoles); err != nil {
			return err
		}

		for clientID, roles := range g.ClientRoles {
			if clientID == "" {
				return fmt.Errorf("%s.clientRoles: client ID is required", path)
			}
			if containsNullByte(clientID) {
				return fmt.Errorf("%s.clientRoles: contains null byte", path)
			}
			for i, role := range roles {
				if role == "" {
					return fmt.Errorf("%s.clientRoles[%q][%d]: is required", path, clientID, i)
				}
			}
			if err := scanSliceNullBytes(fmt.Sprintf("%s.clientRoles[%q]", path, clientID), roles); err != nil {
				return err
			}
		}

		if err := validateGroups(path+".subGroups", g.SubGroups); err != nil {
			return err
		}
	}

	return nil
}

// credentialTypeOTP is the only credential type the provisioner seeds.
const credentialTypeOTP = "otp"

// validOTPAlgorithms are the HMAC algorithms Keycloak accepts for a TOTP
// credential, in the spelling it stores.
var validOTPAlgorithms = map[string]bool{
	"HmacSHA1":   true,
	"HmacSHA256": true,
	"HmacSHA512": true,
}

func validateRequiredActions(prefix string, actions []string) error {
	seen := make(map[string]bool, len(actions))

	for i, a := range actions {
		path := fmt.Sprintf("%s.requiredActions[%d]", prefix, i)

		if a == "" {
			return fmt.Errorf("%s: is empty", path)
		}
		if containsNullByte(a) {
			return fmt.Errorf("%s: contains null byte", path)
		}
		if seen[a] {
			return fmt.Errorf("%s: duplicate required action %q", path, a)
		}

		seen[a] = true
	}

	return nil
}

func validateCredentials(prefix string, creds []Credential) error {
	types := make(map[string]bool, len(creds))

	for i, c := range creds {
		path := fmt.Sprintf("%s.credentials[%d]", prefix, i)

		if c.Type == "" {
			return fmt.Errorf("%s.type: is required", path)
		}
		if c.Type != credentialTypeOTP {
			return fmt.Errorf("%s.type: %q is not supported; only %q is", path, c.Type, credentialTypeOTP)
		}

		// Two credentials of one type are only distinguishable by label, and
		// the reconciler matches on the pair to decide what already exists.
		key := c.Type + "\x00" + c.Label
		if types[key] {
			return fmt.Errorf("%s: duplicate credential of type %q with label %q", path, c.Type, c.Label)
		}

		types[key] = true

		if c.Secret == "" {
			return fmt.Errorf("%s.secret: is required", path)
		}

		if err := scanNullBytes(map[string]string{
			path + ".secret": c.Secret,
			path + ".label":  c.Label,
		}); err != nil {
			return err
		}

		if c.Digits < 0 {
			return fmt.Errorf("%s.digits: must not be negative", path)
		}
		if c.Period < 0 {
			return fmt.Errorf("%s.period: must not be negative", path)
		}
		if c.Algorithm != "" && !validOTPAlgorithms[c.Algorithm] {
			return fmt.Errorf("%s.algorithm: %q is not one of HmacSHA1, HmacSHA256, HmacSHA512", path, c.Algorithm)
		}
	}

	return nil
}

// acrValueSeparator is how Keycloak packs several ACR values into the single
// default.acr.values attribute.
const acrValueSeparator = "##"

// validateDefaultAcrValues checks a client's default ACR values against the
// ACR-to-LoA map they have to come from.
//
// The map may also be set on the server rather than in config, so a value is
// only rejected when a declared map disproves it — the same rule the broker
// flow aliases follow. Keycloak's own rejection quotes the map without naming
// the value or listing what it would accept.
func validateDefaultAcrValues(prefix string, c Client, realmAcrLoaMap map[string]int) error {
	if len(c.DefaultAcrValues) == 0 {
		return nil
	}

	seen := make(map[string]bool, len(c.DefaultAcrValues))

	for i, v := range c.DefaultAcrValues {
		path := fmt.Sprintf("%s.defaultAcrValues[%d]", prefix, i)

		if v == "" {
			return fmt.Errorf("%s: is empty", path)
		}
		if containsNullByte(v) {
			return fmt.Errorf("%s: contains null byte", path)
		}
		// Keycloak joins the values with this, so one containing it would come
		// back as two.
		if strings.Contains(v, acrValueSeparator) {
			return fmt.Errorf("%s: %q must not contain %q, which Keycloak uses to separate the values",
				path, v, acrValueSeparator)
		}
		if seen[v] {
			return fmt.Errorf("%s: duplicate ACR value %q", path, v)
		}

		seen[v] = true
	}

	known := make(map[string]bool, len(realmAcrLoaMap)+len(c.AcrLoaMap))
	for name := range realmAcrLoaMap {
		known[name] = true
	}

	for name := range c.AcrLoaMap {
		known[name] = true
	}

	// Nothing declared means nothing to check against: the map may exist on
	// the server already.
	if len(known) == 0 {
		return nil
	}

	for i, v := range c.DefaultAcrValues {
		if known[v] {
			continue
		}

		names := make([]string, 0, len(known))
		for name := range known {
			names = append(names, name)
		}

		sort.Strings(names)

		return fmt.Errorf(
			"%s.defaultAcrValues[%d]: %q is not in the acrLoaMap; the config declares %s",
			prefix, i, v, strings.Join(names, ", "))
	}

	return nil
}
