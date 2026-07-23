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
	Realm                string      `yaml:"realm"`
	DisplayName          string      `yaml:"displayName"`
	Enabled              *bool       `yaml:"enabled"`
	SslRequired          string      `yaml:"sslRequired"`
	LoginTheme           string      `yaml:"loginTheme"`
	RegistrationAllowed  *bool       `yaml:"registrationAllowed"`
	ResetPasswordAllowed *bool       `yaml:"resetPasswordAllowed"`
	Clients              []Client    `yaml:"clients"`
	Roles                []RealmRole `yaml:"roles"`
	Users                []User      `yaml:"users"`
	Groups               []Group     `yaml:"groups"`
	Strategy             string      `yaml:"strategy"`
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
	ProtocolMappers              []ProtocolMapper  `yaml:"protocolMappers"`
	ClientRoles                  []ClientRole      `yaml:"clientRoles"`
	ServiceAccountRoles          *UserRoles        `yaml:"serviceAccountRoles"`
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

// expandConfig walks the config and expands env vars in string fields.
// normalizeSslRequired lowercases the sslRequired value to match Keycloak's API.
func normalizeSslRequired(value string) string {
	return strings.ToLower(value)
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
			if len(c.Attributes) > 0 {
				expanded := make(map[string]string, len(c.Attributes))
				for k, v := range c.Attributes {
					expanded[expandEnvVars(k)] = expandEnvVars(v)
				}
				c.Attributes = expanded
			}
			for k := range c.ProtocolMappers {
				pm := &c.ProtocolMappers[k]
				pm.Name = expandEnvVars(pm.Name)
				pm.Protocol = expandEnvVars(pm.Protocol)
				pm.ProtocolMapper = expandEnvVars(pm.ProtocolMapper)
				for ck, cv := range pm.Config {
					pm.Config[ck] = expandEnvVars(cv)
				}
			}
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

		for k, v := range c.Attributes {
			if containsNullByte(k) || containsNullByte(v) {
				return fmt.Errorf("%s.attributes: contains null byte", prefix)
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
