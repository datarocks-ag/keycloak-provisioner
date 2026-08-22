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
	"os"
	"regexp"
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
	Strategy               string                  `yaml:"strategy"`
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
}

// OrganizationGroup is a group owned by an organization.
//
// Unlike a realm Group it has no realmRoles or clientRoles: Keycloak exposes
// no role-mapping endpoint for organization groups, and the realm group API
// refuses them outright.
type OrganizationGroup struct {
	Name       string              `yaml:"name"`
	Attributes map[string][]string `yaml:"attributes"` // Keycloak group attributes are multivalued
	// Members are usernames. A user must already be a member of the
	// organization before it can join one of its groups, so list them under
	// the organization's members as well. Membership is additive.
	Members   []string            `yaml:"members"`
	SubGroups []OrganizationGroup `yaml:"subGroups"`
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
	StandardTokenExchangeEnabled *bool             `yaml:"standardTokenExchangeEnabled"`
	BearerOnly                   *bool             `yaml:"bearerOnly"`
	ConsentRequired              *bool             `yaml:"consentRequired"`
	FrontchannelLogout           *bool             `yaml:"frontchannelLogout"`
	DefaultClientScopes          []string          `yaml:"defaultClientScopes"`
	OptionalClientScopes         []string          `yaml:"optionalClientScopes"`
	Attributes                   map[string]string `yaml:"attributes"`
	// AcrLoaMap maps ACR values to Levels of Authentication for this client.
	// It is marshalled into the client's acr.loa.map attribute and wins over an
	// acr.loa.map entry supplied through Attributes.
	AcrLoaMap map[string]int `yaml:"acrLoaMap"`
	// AuthenticationFlowBindingOverrides overrides realm flow bindings for
	// this client. Keys are binding names ("browser", "direct_grant") and
	// values are flow aliases, which the provisioner resolves to flow IDs.
	AuthenticationFlowBindingOverrides map[string]string `yaml:"authenticationFlowBindingOverrides"`
	ProtocolMappers                    []ProtocolMapper  `yaml:"protocolMappers"`
	ClientRoles                        []ClientRole      `yaml:"clientRoles"`
	ServiceAccountRoles                *UserRoles        `yaml:"serviceAccountRoles"`
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
	Username        string     `yaml:"username"`
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
}

// UserRoles defines realm and client role assignments for a user or service account.
type UserRoles struct {
	Realm   []string            `yaml:"realm"`
	Clients map[string][]string `yaml:"clients"`
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
	for k, v := range roles.Clients {
		for i := range v {
			roles.Clients[k][i] = expandEnvVars(v[i])
		}
	}
}

// expandUsers expands env vars in a slice of User structs.
func expandUsers(users []User) {
	for i := range users {
		u := &users[i]
		u.Username = expandEnvVars(u.Username)
		u.Password = expandEnvVars(u.Password)
		u.InitialPassword = expandEnvVars(u.InitialPassword)
		u.Email = expandEnvVars(u.Email)
		u.FirstName = expandEnvVars(u.FirstName)
		u.LastName = expandEnvVars(u.LastName)
		expandUserRoles(u.Roles)
		for j := range u.Groups {
			u.Groups[j] = expandEnvVars(u.Groups[j])
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
func expandMultiValueMap(m map[string][]string) map[string][]string {
	if len(m) == 0 {
		return m
	}

	expanded := make(map[string][]string, len(m))
	for k, values := range m {
		vs := make([]string, len(values))
		for i, v := range values {
			vs[i] = expandEnvVars(v)
		}
		expanded[expandEnvVars(k)] = vs
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

// expandConfig walks the config and expands env vars in string fields.
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
			c.Attributes = expandStringMap(c.Attributes)
			c.AuthenticationFlowBindingOverrides = expandStringMap(c.AuthenticationFlowBindingOverrides)
			expandProtocolMappers(c.ProtocolMappers)
			for k := range c.ClientRoles {
				c.ClientRoles[k].Name = expandEnvVars(c.ClientRoles[k].Name)
				c.ClientRoles[k].Description = expandEnvVars(c.ClientRoles[k].Description)
			}
			expandUserRoles(c.ServiceAccountRoles)
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

		for j := range r.Groups {
			expandGroup(&r.Groups[j])
		}
	}
}

// expandGroup recursively expands env vars in a group's string fields and subgroups.
func expandGroup(g *Group) {
	g.Name = expandEnvVars(g.Name)
	for k := range g.Attributes {
		for i := range g.Attributes[k] {
			g.Attributes[k][i] = expandEnvVars(g.Attributes[k][i])
		}
	}
	for i := range g.RealmRoles {
		g.RealmRoles[i] = expandEnvVars(g.RealmRoles[i])
	}
	renames := make(map[string]string)
	for clientID, roles := range g.ClientRoles {
		for i := range roles {
			roles[i] = expandEnvVars(roles[i])
		}
		if expanded := expandEnvVars(clientID); expanded != clientID {
			renames[clientID] = expanded
		}
	}
	// Apply renames after iterating to avoid mutating the map mid-range. When an
	// expanded client ID collides with an existing key (e.g. "${X}" expands to a
	// literal "app" key that is also present, or two vars expand to the same ID),
	// merge the role lists rather than silently dropping one side.
	for oldID, newID := range renames {
		if newID == oldID {
			continue
		}
		g.ClientRoles[newID] = append(g.ClientRoles[newID], g.ClientRoles[oldID]...)
		delete(g.ClientRoles, oldID)
	}
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

		if err := validateClients(i, r.Clients); err != nil {
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
		if names[u.Username] {
			return fmt.Errorf("%s.username: duplicate username %q", p, u.Username)
		}
		names[u.Username] = true

		if u.Password != "" && u.InitialPassword != "" {
			return fmt.Errorf("%s: password and initialPassword are mutually exclusive", p)
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
	for i, r := range roles.Realm {
		if containsNullByte(r) {
			return fmt.Errorf("%s.roles.realm[%d]: contains null byte", prefix, i)
		}
	}
	for clientID, clientRoles := range roles.Clients {
		if containsNullByte(clientID) {
			return fmt.Errorf("%s.roles.clients: client ID contains null byte", prefix)
		}
		for i, r := range clientRoles {
			if containsNullByte(r) {
				return fmt.Errorf("%s.roles.clients.%s[%d]: contains null byte", prefix, clientID, i)
			}
		}
	}
	return nil
}

func validateClients(realmIdx int, clients []Client) error {
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

		if err := validateAttributes(prefix+".attributes", c.Attributes); err != nil {
			return err
		}

		if err := validateAcrLoaMap(prefix+".acrLoaMap", c.AcrLoaMap); err != nil {
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
