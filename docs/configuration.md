# Configuration reference

The complete reference for the YAML config: every field, its type, what happens to it on a
later run, and which Keycloak version or server feature it needs. For a guided introduction,
start with [Getting started](getting-started.md); for the model behind it, read
[How it works](concepts.md). A commented example with most fields is in
[config.example.yaml](../config.example.yaml).

- [A minimal config](#a-minimal-config)
- [How updates are applied](#how-updates-are-applied)
- [Top level](#top-level) and [master realm](#master-realm)
- [Realms](#realms): [attributes](#realm-attributes)
- [Client scopes](#client-scopes)
- [Clients](#clients): [client roles](#client-roles), [client scope assignment](#client-scope-assignment),
  [token exchange and `fullScopeAllowed`](#token-exchange-and-fullscopeallowed),
  [role scope mappings](#role-scope-mappings), [service account roles](#service-account-roles),
  [management permissions](#management-permissions)
- [Protocol mappers](#protocol-mappers)
- [Realm roles](#realm-roles)
- [Groups](#groups)
- [Users](#users): [role assignments](#role-assignments), [group membership](#group-membership),
  [required actions](#required-actions), [credentials](#credentials), [fixed ids](#fixed-user-ids)
- [Authentication flows](#authentication-flows): [executions](#executions),
  [realm bindings](#realm-flow-bindings), [client overrides](#client-flow-binding-overrides)
- [Step-up: `acrLoaMap` and `defaultAcrValues`](#step-up-acrloamap-and-defaultacrvalues)
- [Identity providers](#identity-providers): [mappers](#identity-provider-mappers)
- [Organizations](#organizations): [domains](#organization-domains), [groups](#organization-groups),
  [identity provider links](#organization-identity-providers)
- [Environment variable expansion](#environment-variable-expansion)
- [Validation](#validation)
- [Keycloak versions and features](#keycloak-versions-and-features)

## A minimal config

One realm with a realm role, a public client and a user who holds the role:

```yaml
realms:
  - realm: "shop"
    enabled: true
    roles:
      - name: "shop-admin"
    clients:
      - clientId: "shop-web"
        publicClient: true
        redirectUris:
          - "https://shop.example.com/*"
    users:
      - username: "alice"
        email: "alice@example.com"
        firstName: "Alice"
        lastName: "Example"
        password: "${ALICE_PASSWORD}"
        roles:
          realm:
            - "shop-admin"
```

```bash
ALICE_PASSWORD='<password>' KEYCLOAK_URL=http://localhost:8080 KEYCLOAK_USER=admin \
  KEYCLOAK_PASSWORD='<admin-password>' keycloak-provisioner --config config.yaml
```

Output of the first run against Keycloak 26.6.4 (`time` trimmed):

```
{"level":"INFO","msg":"Detected Keycloak version","version":"26.6.4"}
{"level":"INFO","msg":"Starting provisioning"}
{"level":"INFO","msg":"Creating realm","realm":"shop"}
{"level":"INFO","msg":"Creating client","realm":"shop","clientId":"shop-web"}
{"level":"INFO","msg":"Creating realm role","realm":"shop","role":"shop-admin"}
{"level":"INFO","msg":"Creating user","realm":"shop","username":"alice"}
{"level":"INFO","msg":"Setting user password","realm":"shop","username":"alice"}
{"level":"INFO","msg":"Assigning realm roles","realm":"shop","subject":"users","subjectName":"alice","count":1}
{"level":"INFO","msg":"Provisioning complete"}
{"level":"INFO","msg":"keycloak-provisioner finished successfully"}
```

A second run logs `Updating realm`, `Updating client`, `Updating realm role`, `Updating user`
and `Setting user password`, and no `Assigning realm roles`: the role is already mapped.

The field names follow Keycloak's admin representation (camelCase), but the schema is **not**
a realm export. The decoder rejects unknown fields, and several shapes differ: realm `roles` is
a list of `{name, description}`, and a user's roles are `roles: {realm, clients}` rather than
`realmRoles` / `clientRoles`. An exported realm will not load as-is:

```
parsing config YAML: yaml: unmarshal errors:
  line 3: field id not found in type config.Realm
```

## How updates are applied

Every resource is matched by a natural key (realm name, `clientId`, role `name`, `username`,
group path, flow `alias`, …) and created when it is missing. What happens to an existing
resource depends on the field. The **On update** column in the tables below uses these terms:

| Term | Meaning |
|---|---|
| **set** | Written on create and, with `strategy: update`, on every run. A field you leave out is not sent, so Keycloak keeps its current value. |
| **merged** | A map merged over the server's current map: your keys win, keys you don't declare are kept. Applied like **set**. |
| **replaced** | Sent as declared and replaces what the server holds — entries you drop are removed. Applied like **set**. |
| **create-only** | Used only when the resource is created. |
| **additive** | Missing entries are added on every run, **whatever the strategy**; entries the config doesn't name are never removed. |
| **every run** | Written on every run, whatever the strategy. |

Nothing is ever deleted: no realm, client, role, user, group, scope, flow, provider or
organization, and no role mapping or membership. The **replaced** fields, `requiredActions: []`, moving a client scope
between default and optional, and the [management permission](#management-permissions) client
lists are the only ways a run removes a value. See [How it works](concepts.md) for the model.

### What `strategy: create` still changes

`strategy: create` skips only the update of an existing resource's own representation. On an
existing resource, it still:

- resets a user's `password` (every run) and adds user roles, groups and credentials,
- adds group role mappings and creates missing subgroups,
- applies the realm's `authenticationBindings`,
- moves a client scope's realm-level `type` between default and optional,
- attaches client scopes to clients, adds service account roles and scope mappings, and
  reconciles management permissions,
- creates missing identity provider mappers,
- adds organization members, links identity providers, creates missing organization groups and
  adds their members.

Authentication flows are create-only under either strategy.

## Top level

```yaml
strategy: update     # optional: "update" (default) or "create"
masterRealm: {...}   # optional, see Master realm
realms: [...]        # optional
```

| Field | Type | Default | Description |
|---|---|---|---|
| `strategy` | string | `update` | Default for every realm. `update` creates or updates; `create` creates missing resources and skips existing ones (see [above](#what-strategy-create-still-changes)). Any other value is rejected. Not expanded from `${VAR}`. |
| `masterRealm` | object | — | Limited settings for the built-in `master` realm. |
| `realms` | list | — | Realms to provision. |

The file must hold exactly one YAML document; a second one after `---` is rejected with
`file contains multiple YAML documents; only one is supported`.

## Master realm

The master realm always exists, so this block never creates it; it only updates what you declare.
It has no `strategy` of its own: its users follow the top-level `strategy`.

```yaml
masterRealm:
  sslRequired: external
  users:
    - username: "ops-admin"
      password: "${OPS_ADMIN_PASSWORD}"
      roles:
        realm:
          - admin
```

| Field | Type | On update | Description |
|---|---|---|---|
| `sslRequired` | string | every run | `external`, `all` or `none` (case-insensitive). Written whenever it differs from the current value, whatever the strategy. |
| `users` | list | — | Users in the master realm, same fields as [realm users](#users). |

`realms` cannot contain `master`: `realms[0].realm: provisioning the "master" realm is not
allowed; use masterRealm instead`.

## Realms

```yaml
realms:
  - realm: "shop"                 # required
    displayName: "Shop"
    enabled: true
    sslRequired: external
    strategy: update
    attributes: {...}
    acrLoaMap: {...}
    clientScopes: [...]
    clients: [...]
    roles: [...]
    groups: [...]
    users: [...]
    authenticationFlows: [...]
    authenticationBindings: {...}
    identityProviders: [...]
    organizationsEnabled: true
    organizations: [...]
```

Every field except `realm` is optional.

| Field | Type | On update | Description |
|---|---|---|---|
| `realm` | string | key | **Required.** Realm name, unique in the file. `master` is rejected. |
| `displayName` | string | set | Name shown in the console and login pages. |
| `enabled` | bool | set | Whether the realm is enabled. Sent only when declared; a realm created without it came out disabled on Keycloak 26.6.4 (`Realm not enabled` on login), so declare `enabled: true`. |
| `sslRequired` | string | set | `external`, `all` or `none`, case-insensitive. |
| `loginTheme` | string | set | Login theme name. |
| `registrationAllowed` | bool | set | Self-registration. |
| `resetPasswordAllowed` | bool | set | "Forgot password" link. |
| `loginWithEmailAllowed` | bool | set | Log in with email address as well as username. |
| `bruteForceProtected` | bool | set | Brute force detection on or off; the thresholds keep their current values. |
| `organizationsEnabled` | bool | set | Keycloak Organizations. Must be `true` when `organizations` is declared. Keycloak 26.0+, `ORGANIZATION` feature. |
| `attributes` | map of string | merged | Realm attributes, see [Realm attributes](#realm-attributes). |
| `acrLoaMap` | map of int | merged (as an attribute) | ACR value to Level of Authentication, see [Step-up](#step-up-acrloamap-and-defaultacrvalues). `STEP_UP_AUTHENTICATION` feature. |
| `strategy` | string | — | `update` or `create`, overrides the top-level `strategy` for this realm and everything in it. Not expanded from `${VAR}`. |
| `clientScopes` | list | — | [Client scopes](#client-scopes). |
| `clients` | list | — | [Clients](#clients). |
| `roles` | list | — | [Realm roles](#realm-roles). |
| `groups` | list | — | [Groups](#groups). |
| `users` | list | — | [Users](#users). |
| `authenticationFlows` | list | — | [Authentication flows](#authentication-flows). |
| `authenticationBindings` | object | — | [Realm flow bindings](#realm-flow-bindings). |
| `identityProviders` | list | — | [Identity providers](#identity-providers). |
| `organizations` | list | — | [Organizations](#organizations). |

With `strategy: create`, an existing realm's own fields (the scalar rows above, `attributes` and
`acrLoaMap`) are not updated; the list fields are still reconciled resource by resource.

### Realm attributes

```yaml
realms:
  - realm: "shop"
    attributes:
      frontendUrl: "https://id.example.com"
```

Keycloak replaces a realm's whole attribute map on update and keeps several settings there, so
the provisioner reads the current attributes and sends the union with your keys on top. Keys you
don't declare are kept; an attribute is never removed. Omitting `attributes` leaves them alone.
Keys and values support `${VAR}` expansion; an empty key is rejected
(`realms[0].attributes: attribute name is required`).

## Client scopes

Provisioned before clients, so a client can reference a scope declared in the same realm.
Matched by `name`.

```yaml
realms:
  - realm: "shop"
    clients:
      - clientId: "orders-api"
        bearerOnly: true
        clientRoles:
          - name: "reader"
    clientScopes:
      - name: "orders:read"
        description: "Read access to orders"
        type: optional
        attributes:
          "include.in.token.scope": "true"
        protocolMappers:
          - name: "orders-audience"
            protocol: "openid-connect"
            protocolMapper: "oidc-audience-mapper"
            config:
              "included.client.audience": "orders-api"
        scopeMappings:
          clients:
            orders-api: ["reader"]
```

| Field | Type | On update | Description |
|---|---|---|---|
| `name` | string | key | **Required**, unique per realm. |
| `description` | string | set | |
| `protocol` | string | set | Defaults to `openid-connect`, which is sent on every create and update when omitted. |
| `type` | string | additive | Realm-level assignment: `default` (added to every new client), `optional` (requestable through the `scope` parameter), `none` or empty (not managed). See below. Not expanded from `${VAR}`. |
| `attributes` | map of string | set as declared | Sent as declared, not merged. On Keycloak 26.6.4 a key you drop from the config stayed on the scope (observed), so you can't remove an attribute through the config. |
| `protocolMappers` | list | — | [Protocol mappers](#protocol-mappers) on the scope. |
| `scopeMappings` | object | additive | Roles the scope carries into the tokens of every client it is assigned to, see [Role scope mappings](#role-scope-mappings). |

**`type`:** a scope already on the other realm list is moved — detached from it, then added —
on every run, whatever the strategy. `none` (or no `type`) doesn't detach it from either list; it
only stops the provisioner managing the assignment. Any other value is rejected:
`realms[0].clientScopes[0].type: invalid value "Default" (must be "default", "optional", or "none")`.

To attach scopes to a client, see [Client scope assignment](#client-scope-assignment). Walk-through:
[Add claims and audiences with client scopes](howto/add-claims-with-client-scopes.md).

## Clients

Matched by `clientId`.

```yaml
realms:
  - realm: "shop"
    clients:
      - clientId: "shop-web"
        name: "Shop web app"
        publicClient: true
        rootUrl: "https://shop.example.com"
        redirectUris: ["/*"]
        webOrigins: ["+"]
      - clientId: "orders-worker"
        secret: "${ORDERS_WORKER_SECRET}"
        publicClient: false
        standardFlowEnabled: false
        serviceAccountsEnabled: true
```

| Field | Type | On update | Description |
|---|---|---|---|
| `clientId` | string | key | **Required**, unique per realm. |
| `secret` | string | set | Secret of a confidential client. Supply it through `${VAR}`. |
| `name` | string | set | Display name. |
| `enabled` | bool | set | |
| `publicClient` | bool | set | `true` for a public client; unset or `false` for confidential. |
| `protocol` | string | set | `openid-connect` or `saml`. |
| `rootUrl` | string | set | Base for relative URLs below. |
| `baseUrl` | string | set | Default URL after login. |
| `adminUrl` | string | set | URL Keycloak calls for back-channel requests. |
| `redirectUris` | list | replaced | Valid redirect URIs. Sent only when non-empty. |
| `webOrigins` | list | replaced | Allowed CORS origins. Sent only when non-empty. |
| `standardFlowEnabled` | bool | set | Authorization code flow. |
| `directAccessGrantsEnabled` | bool | set | Resource owner password credentials grant. |
| `serviceAccountsEnabled` | bool | set | Client credentials grant. Must be `true` when `serviceAccountRoles` is set. |
| `standardTokenExchangeEnabled` | bool | set | Standard token exchange (RFC 8693), see [below](#token-exchange-and-fullscopeallowed). `true` needs Keycloak 26.2+ and `TOKEN_EXCHANGE_STANDARD_V2`. |
| `fullScopeAllowed` | bool | set | Whether tokens carry every role the subject holds, see [below](#token-exchange-and-fullscopeallowed). |
| `bearerOnly` | bool | set | Client only validates tokens. |
| `consentRequired` | bool | set | |
| `frontchannelLogout` | bool | set | |
| `attributes` | map of string | merged | Client attributes, merged over the current ones like [realm attributes](#realm-attributes). |
| `acrLoaMap` | map of int | merged (as an attribute) | See [Step-up](#step-up-acrloamap-and-defaultacrvalues). `STEP_UP_AUTHENTICATION` feature. |
| `defaultAcrValues` | list | merged (as an attribute) | See [Step-up](#step-up-acrloamap-and-defaultacrvalues). |
| `authenticationFlowBindingOverrides` | map | set | See [Client flow binding overrides](#client-flow-binding-overrides). |
| `defaultClientScopes` | list | additive | See [Client scope assignment](#client-scope-assignment). |
| `optionalClientScopes` | list | additive | See [Client scope assignment](#client-scope-assignment). |
| `protocolMappers` | list | — | [Protocol mappers](#protocol-mappers). |
| `clientRoles` | list | — | [Client roles](#client-roles). |
| `serviceAccountRoles` | object | additive | [Service account roles](#service-account-roles). |
| `scopeMappings` | object | additive | [Role scope mappings](#role-scope-mappings). |
| `managementPermissions` | object | see section | [Management permissions](#management-permissions). `ADMIN_FINE_GRAINED_AUTHZ` feature. |

`attributes`, `acrLoaMap`, `defaultAcrValues` and `standardTokenExchangeEnabled` all end up in the
client's attribute map. A typed field wins over the same key under `attributes`. A client that
declares none of the four sends no attributes at all.

### Client roles

```yaml
    clients:
      - clientId: "orders-api"
        clientRoles:
          - name: "reader"
            description: "Read orders"
```

| Field | Type | On update | Description |
|---|---|---|---|
| `name` | string | key | **Required**, unique per client. |
| `description` | string | set | |

### Client scope assignment

```yaml
    clients:
      - clientId: "shop-web"
        defaultClientScopes: ["orders:read"]
        optionalClientScopes: ["orders:write"]
```

Each named scope that isn't attached yet is attached with that type; a scope attached with the
other type is moved. This runs on every run, whatever the strategy. Scopes the config doesn't name
— including Keycloak's own defaults such as `roles` — stay attached.

- A scope may be a [client scope](#client-scopes) declared in the same realm or one that already
  exists. A scope that doesn't exist is logged as a warning (`Client scope not found, skipping
  assignment`) and skipped.
- Naming one scope in both lists is rejected: `realms[0].clients[0]: client scope "orders:read"
  is listed as both default and optional`.

### Token exchange and `fullScopeAllowed`

```yaml
    clients:
      - clientId: "exchange-service"
        publicClient: false
        secret: "${EXCHANGE_SERVICE_SECRET}"
        standardTokenExchangeEnabled: true
        fullScopeAllowed: false
```

- **`standardTokenExchangeEnabled`** writes the client attribute
  `standard.token.exchange.enabled` (`true` or `false`); unset leaves the attribute alone. `true`
  requires a confidential client: `publicClient: true` and `bearerOnly: true` are both rejected.
  The pre-flight check refuses `true` on Keycloak older than 26.2 or with
  `TOKEN_EXCHANGE_STANDARD_V2` disabled; `false` is never checked.
- **`fullScopeAllowed`** unset sends nothing: a client the provisioner creates gets Keycloak's
  default `true`, an existing client keeps its value. With `false`, roles reach the token only
  through the client's scope — its [`scopeMappings`](#role-scope-mappings) and those of its
  assigned client scopes — so declare those too.

Both flags are corrected on an existing client only with `strategy: update`. Walk-through:
[Let a client exchange tokens for another audience](howto/set-up-token-exchange.md).

### Role scope mappings

`scopeMappings` names the roles in a client's scope, or in a client scope's scope (then they apply
to every client the scope is assigned to). It narrows what a token carries; it never grants a role.
Same shape as a [user's `roles`](#role-assignments):

```yaml
realms:
  - realm: "shop"
    roles:
      - name: "app-admin"
    clients:
      - clientId: "orders-api"
        clientRoles:
          - name: "reader"
      - clientId: "bff"
        fullScopeAllowed: false
        scopeMappings:
          realm: ["app-admin"]
          clients:
            orders-api: ["reader"]
```

| Field | Type | Description |
|---|---|---|
| `realm` | list | Realm role names. |
| `clients` | map | `clientId` → client role names. |

**Additive**, whatever the strategy, applied after all roles in the realm exist. A role or client
that doesn't exist fails the run: `realm role "app-admin" not found in realm "shop"`,
`client role "reader" not found on client "orders-api" in realm "shop"`.

### Service account roles

```yaml
realms:
  - realm: "shop"
    roles:
      - name: "app-admin"
    clients:
      - clientId: "orders-worker"
        serviceAccountsEnabled: true
        serviceAccountRoles:
          realm: ["app-admin"]
```

Same shape as [`scopeMappings`](#role-scope-mappings): `realm` (list) and `clients` (map of
`clientId` → role names). **Additive**, whatever the strategy, applied after all roles exist; a
missing role or client fails the run as above. Requires `serviceAccountsEnabled: true`:
`realms[0].clients[0].serviceAccountRoles: serviceAccountsEnabled must be true when
serviceAccountRoles is set`. Walk-through: [Set up a machine-to-machine client](howto/set-up-a-service-account-client.md).

### Management permissions

Keycloak's v1 fine-grained admin permissions on this client: which other clients may exercise a
permission scope on it.

```yaml
    clients:
      - clientId: "router"
        publicClient: false
        managementPermissions:
          scopes:
            token-exchange:
              clients: ["worker"]
      - clientId: "worker"
        publicClient: false
```

| Field | Type | Description |
|---|---|---|
| `enabled` | bool | Defaults to `true`. `false` is rejected, because Keycloak would delete the client's scope permissions and their policies. |
| `scopes` | map | **At least one.** Permission scope name → `{clients: [...]}`. |
| `scopes.<name>.clients` | list | **At least one** `clientId`, no duplicates. The grantee need not be declared in the config. |

- **Requires** the `ADMIN_FINE_GRAINED_AUTHZ` server feature: start Keycloak with
  `--features=admin-fine-grained-authz:v1`.
- **Scope names** are checked against the scopes the server reports for the client, during the
  run (after earlier resources were written): `client "router": Keycloak offers no management
  permission scope "token-exchang" (available: configure, manage, map-roles, …)`. Keycloak 26.6.4
  reports `configure`, `manage`, `map-roles`, `map-roles-client-scope`, `map-roles-composite`,
  `token-exchange`, `view`. A dry run against a client that doesn't exist yet uses that list.
- **The client list is authoritative.** For each scope the provisioner owns a client policy named
  `keycloak-provisioner.<scope>.<clientId>` and sets its clients to the configured list, so
  dropping a `clientId` withdraws that grant on the next run. The policy is added to the scope
  permission alongside any others. Renaming the target client orphans its policy.
- **Decision strategy** of the scope permission is set to `AFFIRMATIVE`; a warning is logged when
  that loosens a permission that already had other policies.
- **Removing the block** or a scope stops managing it; permissions and policies stay.
- Applied after every client in the realm exists, whatever the strategy.

Walk-through: [Allow a client to impersonate users](howto/allow-impersonation.md).

## Protocol mappers

On a client or a client scope, under `protocolMappers`. Matched by `name` within the owner.

```yaml
    clients:
      - clientId: "shop-web"
        protocolMappers:
          - name: "tenant"
            protocol: "openid-connect"
            protocolMapper: "oidc-hardcoded-claim-mapper"
            config:
              "claim.name": "tenant"
              "claim.value": "acme"
              "access.token.claim": "true"
```

| Field | Type | On update | Description |
|---|---|---|---|
| `name` | string | key | **Required**, unique per client or client scope. |
| `protocol` | string | set | **Required**: `openid-connect` or `saml`. |
| `protocolMapper` | string | set | **Required**. Mapper type, checked against the types the server offers for that protocol. |
| `consentRequired` | bool | set | |
| `config` | map of string | replaced | Mapper configuration. A key you remove from the config is removed from Keycloak. Values support `${VAR}`; keys don't. |

## Realm roles

```yaml
    roles:
      - name: "app-admin"
        description: "Application administrator"
```

| Field | Type | On update | Description |
|---|---|---|---|
| `name` | string | key | **Required**, unique per realm. |
| `description` | string | set | |

Realm roles are created after clients and before groups and users. Composite roles are not
supported.

## Groups

Matched by `name` at their level of the tree.

```yaml
realms:
  - realm: "shop"
    roles:
      - name: "app-admin"
    clients:
      - clientId: "orders-api"
        clientRoles:
          - name: "reader"
    groups:
      - name: "engineering"
        attributes:
          department: ["engineering"]
        realmRoles: ["app-admin"]
        clientRoles:
          orders-api: ["reader"]
        subGroups:
          - name: "backend"
```

| Field | Type | On update | Description |
|---|---|---|---|
| `name` | string | key | **Required**, unique among siblings; may repeat at different levels. |
| `attributes` | map of list | replaced | Multivalued attributes. Sent only when non-empty; then attributes you don't declare are removed. |
| `realmRoles` | list | additive | Realm roles granted to the group. |
| `clientRoles` | map | additive | `clientId` → client roles granted to the group. |
| `subGroups` | list | — | Nested groups, same fields, any depth. |

Role mappings and subgroups are reconciled whatever the strategy; a missing role or client fails
the run. Walk-through: [Grant roles through groups](howto/grant-roles-through-groups.md).

## Users

In a realm under `users`, or in the master realm under `masterRealm.users`. Matched by exact
`username`.

```yaml
realms:
  - realm: "shop"
    roles:
      - name: "app-admin"
    groups:
      - name: "engineering"
        subGroups:
          - name: "backend"
    users:
      - username: "alice"
        email: "alice@example.com"
        firstName: "Alice"
        lastName: "Example"
        emailVerified: true
        initialPassword: "${ALICE_INITIAL_PASSWORD}"
        roles:
          realm: ["app-admin"]
        groups: ["/engineering/backend"]
        requiredActions: ["CONFIGURE_TOTP"]
```

| Field | Type | On update | Description |
|---|---|---|---|
| `username` | string | key | **Required**, unique per realm. |
| `id` | string | create-only | Fixed UUID, see [Fixed user ids](#fixed-user-ids). |
| `password` | string | every run | Permanent password, reset on every run whatever the strategy. Mutually exclusive with `initialPassword`. |
| `initialPassword` | string | create-only | Temporary password set when the user is created; the user must change it at first login. |
| `enabled` | bool | set | Sent only when declared; a user created without it came out disabled on Keycloak 26.6.4 (`Account disabled` on login), so declare `enabled: true`. |
| `email` | string | set | |
| `firstName` | string | set | |
| `lastName` | string | set | |
| `emailVerified` | bool | set | |
| `roles` | object | additive | See [Role assignments](#role-assignments). |
| `groups` | list | additive | See [Group membership](#group-membership). |
| `requiredActions` | list | replaced | See [Required actions](#required-actions). |
| `credentials` | list | additive | See [Credentials](#credentials). |

Walk-through: [Provision users with passwords, roles and groups](howto/provision-users.md).

### Role assignments

```yaml
        roles:
          realm: ["app-admin"]
          clients:
            orders-api: ["reader"]
```

`realm` is a list of realm role names, `clients` a map of `clientId` → client role names. Roles
that aren't mapped yet are added on every run, whatever the strategy; existing mappings are never
removed. Roles may be declared in the same config or exist already. A missing role or client
fails the run: `realm role "app-admin" not found in realm "shop"`, `client "orders-api" not found
in realm "shop"`.

### Group membership

`groups` lists group paths: `"/engineering/backend"` or `"engineering/backend"` (the leading slash
is optional). Memberships that are missing are added on every run, whatever the strategy; none is
ever removed. A group that doesn't exist is logged as a warning (`Group not found, skipping
membership`) and skipped. An empty path (`""` or `"/"`) is rejected. Organization groups can't be
reached this way, see [Organization groups](#organization-groups).

### Required actions

```yaml
        requiredActions: ["CONFIGURE_TOTP", "UPDATE_PASSWORD"]
```

Actions Keycloak makes the user complete at the next login.

- **Declared** (on create, or on update with `strategy: update`): the list replaces the user's
  current actions. `requiredActions: []` clears them. **Omitted:** left alone.
- **Names are checked** before anything is written, against the actions registered in the
  **master** realm. The check is skipped with a warning when the admin account may not read them
  (401/403), and with `--skip-version-check`.
- Empty and duplicate entries are rejected: `….requiredActions[0]: is empty`,
  `….requiredActions[1]: duplicate required action "CONFIGURE_TOTP"`.

Walk-through: [Require or seed TOTP for users](howto/require-totp-for-users.md).

### Credentials

Seeds a time-based one-time password (TOTP) credential, so a realm rebuilds without anyone
enrolling by hand:

```yaml
        credentials:
          - type: "otp"
            label: "seeded"
            secret: "${BOB_TOTP_SECRET}"
```

| Field | Type | Default | Description |
|---|---|---|---|
| `type` | string | — | **Required**. Only `otp` is supported: `… .type: "password" is not supported; only "otp" is`. |
| `secret` | string | — | **Required**. The shared secret. Supply it through `${VAR}`. |
| `label` | string | empty | Shown in the account console; distinguishes two credentials of one type. |
| `digits` | int | 6 | Must not be negative; `0` means the default. |
| `period` | int | 30 | Seconds. Must not be negative; `0` means the default. |
| `algorithm` | string | `HmacSHA1` | `HmacSHA1`, `HmacSHA256` or `HmacSHA512`. |

- **Additive, never rotated.** Credentials are matched by `(type, label)`; one the user already
  holds is left alone, even if the secret changed. To replace a secret, delete the credential in
  Keycloak first.
- `type` + `label` must be unique per user: `duplicate credential of type "otp" with label "seeded"`.
- An unset `${BOB_TOTP_SECRET}` is not an error: the literal text `${BOB_TOTP_SECRET}` becomes the
  secret (see [expansion](#environment-variable-expansion)).
- Keycloak uses the characters of `secret` directly as the HMAC key; an authenticator app must be
  given their base32 encoding (Keycloak behaviour, not checked by the provisioner). The how-to
  above covers this.

### Fixed user ids

```yaml
        id: "6f1c1d2e-0a4b-4c3d-9e8f-7a6b5c4d3e2f"
```

Gives the user the same UUID in every environment.

- Must be 8-4-4-4-12 hexadecimal: `….id: "abc" is not a UUID; Keycloak ids are 8-4-4-4-12 hexadecimal`.
- Applies only when the user is created; such a user is created through Keycloak's partial import.
- Users are still matched by `username`. If the user exists under another id, the run fails:
  `user "alice" exists with id <server-id> but the config declares <config-id>; ids cannot be
  changed after creation`.
- Keep ids unique and never reuse an id another user already has: the provisioner doesn't check
  for that.

## Authentication flows

Top-level flows, created before clients so a client can bind to one declared in the same realm.

```yaml
realms:
  - realm: "shop"
    enabled: true
    acrLoaMap:
      silver: 1
      gold: 2
    authenticationFlows:
      - alias: "browser-step-up"
        description: "Browser login with a second factor for gold"
        executions:
          - provider: "auth-cookie"
            requirement: "ALTERNATIVE"
          - subflow: "browser-step-up-levels"
            requirement: "ALTERNATIVE"
            executions:
              - subflow: "browser-step-up-silver"
                requirement: "CONDITIONAL"
                executions:
                  - provider: "conditional-level-of-authentication"
                    requirement: "REQUIRED"
                    config:
                      alias: "silver-condition"
                      loa-condition-level: "1"
                      loa-max-age: "36000"
                  - provider: "auth-username-password-form"
                    requirement: "REQUIRED"
              - subflow: "browser-step-up-gold"
                requirement: "CONDITIONAL"
                executions:
                  - provider: "conditional-level-of-authentication"
                    requirement: "REQUIRED"
                    config:
                      alias: "gold-condition"
                      loa-condition-level: "2"
                      loa-max-age: "300"
                  - provider: "auth-otp-form"
                    requirement: "REQUIRED"
    authenticationBindings:
      browserFlow: "browser-step-up"
```

| Field | Type | Description |
|---|---|---|
| `alias` | string | **Required**. Unique per realm, shared with subflow aliases. |
| `description` | string | Ignored with `copyFrom`. |
| `providerId` | string | `basic-flow` (default) or `form-flow`. Ignored with `copyFrom`. |
| `copyFrom` | string | Alias of an existing flow (built-in or not) to copy. Declared `executions` are appended at the top level after the copied ones. |
| `executions` | list | Steps in order, see [Executions](#executions). |

- **Don't add `REQUIRED` or `CONDITIONAL` steps to a `copyFrom` flow.** They land at the top
  level, beside the copied `ALTERNATIVE` steps (cookie, the login form). Keycloak ignores
  `ALTERNATIVE` steps on a level that also has `REQUIRED` or `CONDITIONAL` ones, so nobody can log
  in through the flow. Observed on 26.6.4: a client bound to `copyFrom: "browser"` plus a
  `CONDITIONAL` subflow showed `Invalid username or password.` on the first page of every login.
  Build such a flow from scratch, as in the example above and in
  [Add step-up authentication to a client](howto/add-step-up-authentication.md).
- **Create-only, whatever the strategy.** A flow whose alias exists is skipped
  (`Skipping existing authentication flow`). To change one, delete it in Keycloak or use a new alias.
- **Built-in flows** are refused during the run, not at load: `authentication flow "browser" is a
  built-in Keycloak flow and will not be modified; declare a new alias with copyFrom: "browser" to
  customise it`.

Walk-through: [Add step-up authentication to a client](howto/add-step-up-authentication.md).

### Executions

Each execution is an authenticator (`provider`) or a nested flow (`subflow`), never both. They are
created in the declared order.

| Field | Applies to | Description |
|---|---|---|
| `provider` | authenticator | Authenticator id, e.g. `auth-cookie`. Checked against the server's authenticators. |
| `subflow` | subflow | Alias of the nested flow. |
| `providerId` | subflow | `basic-flow` (default) or `form-flow`. Rejected on an authenticator. |
| `description` | subflow | Shown against the subflow; not sent for an authenticator. |
| `requirement` | both | `REQUIRED`, `ALTERNATIVE`, `DISABLED` or `CONDITIONAL`. Empty keeps Keycloak's default for the authenticator. |
| `config` | both | Authenticator config. The `alias` key names the config itself; without it the name is `<parent-flow-alias>-<provider-or-subflow>`. |
| `executions` | subflow | Nested steps. Rejected on an authenticator. |

### Realm flow bindings

```yaml
    authenticationBindings:
      browserFlow: "browser-step-up"
```

Fields: `browserFlow`, `directGrantFlow`, `resetCredentialsFlow`, `registrationFlow`,
`clientAuthenticationFlow`, `dockerAuthenticationFlow`, `firstBrokerLoginFlow`. Each value is a flow
alias — built-in or declared in the same realm. Applied on every run, whatever the strategy, as a
second realm update after the flows exist. Unset bindings are left alone. The provisioner doesn't
check that the alias exists.

### Client flow binding overrides

```yaml
    clients:
      - clientId: "shop-web"
        authenticationFlowBindingOverrides:
          browser: "browser-step-up"
```

Keys are `browser` or `direct_grant` only (`invalid binding "directGrant" (must be "browser" or
"direct_grant")`); values are flow aliases, resolved to flow ids. An alias that doesn't exist fails
the run: `client "shop-web": authentication flow "browser-step-up" not found in realm "shop"`.

## Step-up: `acrLoaMap` and `defaultAcrValues`

```yaml
realms:
  - realm: "shop"
    acrLoaMap:
      silver: 1
      gold: 2
    clients:
      - clientId: "shop-web"
        publicClient: true
        defaultAcrValues: ["silver"]
```

| Field | On | Type | Description |
|---|---|---|---|
| `acrLoaMap` | realm, client | map of int | ACR value → Level of Authentication. Written as JSON to the `acr.loa.map` attribute (keys sorted) and wins over that key under `attributes`. Levels ≥ 0; empty ACR name rejected; an empty map leaves the attribute unmanaged. Keys are not expanded. Needs the `STEP_UP_AUTHENTICATION` feature. |
| `defaultAcrValues` | client | list | ACR values applied when a request asks for none. Written to `default.acr.values` as one `##`-separated string. No empty values, no `##`, no duplicates. |

Every `defaultAcrValues` entry must be a key of the realm's or the client's `acrLoaMap` **in the
config**; when neither declares one, the check is skipped (the map may be on the server):
`realms[0].clients[0].defaultAcrValues[0]: "platinum" is not in the acrLoaMap; the config declares gold, silver`.

When the client has its own `acrLoaMap`, list the default values in it too: on Keycloak 26.6.4 a
value found only in the realm's map passed validation and was then refused with `Default ACR values
need to contain values specified in the ACR-To-Loa mapping or number levels from set realm browser
flow`, failing the run at that client.

The map only names levels. A flow with a `CONDITIONAL` subflow containing the
`conditional-level-of-authentication` authenticator enforces them, see
[Authentication flows](#authentication-flows).

## Identity providers

Identity brokering. Created after users and before organizations. Matched by `alias`.

```yaml
realms:
  - realm: "shop"
    identityProviders:
      - alias: "corporate"
        displayName: "Corporate SSO"
        providerId: "oidc"
        enabled: true
        trustEmail: true
        firstBrokerLoginFlowAlias: "first broker login"
        config:
          clientId: "keycloak-broker"
          clientSecret: "${CORPORATE_IDP_SECRET}"
          authorizationUrl: "https://idp.example.com/authorize"
          tokenUrl: "https://idp.example.com/token"
        mappers:
          - name: "email"
            identityProviderMapper: "oidc-user-attribute-idp-mapper"
            config:
              claim: "email"
              user.attribute: "email"
```

| Field | Type | On update | Description |
|---|---|---|---|
| `alias` | string | key | **Required**, unique per realm, no `/`. |
| `providerId` | string | set (always sent) | **Required**. Broker type (`oidc`, `saml`, `google`, …), checked against the types the server offers. |
| `displayName` | string | set | |
| `enabled` | bool | set | |
| `trustEmail` | bool | set | |
| `storeToken` | bool | set | Store the external tokens. |
| `addReadTokenRoleOnCreate` | bool | set | Grant new users the role to read stored tokens. |
| `linkOnly` | bool | set | Only link existing accounts; no login through this provider. |
| `hideOnLogin` | bool | set | Hide the provider's button on the login page. |
| `firstBrokerLoginFlowAlias` | string | set | Flow alias. Not found in the realm → warning `Broker login flow not visible in the current flow listing`, not an error. |
| `postBrokerLoginFlowAlias` | string | set | As above. |
| `config` | map of string | merged | Provider-specific config. Keys you don't declare are kept; a stored secret survives (Keycloak returns it masked). |
| `mappers` | list | — | [Mappers](#identity-provider-mappers). |

Keycloak replaces the whole provider on update, so the provisioner sends the server's current
representation with your fields on top, minus the server-owned `internalId`, `organizationId` and
`types`. An unset boolean keeps the server's value.

Walk-through: [Broker logins through an external identity provider](howto/connect-an-identity-provider.md).

### Identity provider mappers

| Field | Type | On update | Description |
|---|---|---|---|
| `name` | string | key | **Required**, unique per provider. |
| `identityProviderMapper` | string | set | **Required**. Mapper type, checked against the types the server offers. |
| `config` | map of string | replaced | Built from the config alone; a key you remove is removed from Keycloak. |

A missing mapper is created whatever the strategy.

## Organizations

Requires `organizationsEnabled: true` on the realm (`realms[0].organizations: requires
organizationsEnabled: true on the realm`), Keycloak 26.0+ and the `ORGANIZATION` feature.
Provisioned last, so members and providers declared in the same realm exist. Matched by `name`.

```yaml
realms:
  - realm: "shop"
    organizationsEnabled: true
    users:
      - username: "alice"
    organizations:
      - name: "acme"
        description: "ACME Corp"
        redirectUrl: "https://acme.example.com"
        domains:
          - name: "acme.com"
            verified: true
        attributes:
          tier: ["gold"]
        members: ["alice"]
```

| Field | Type | On update | Description |
|---|---|---|---|
| `name` | string | key | **Required**, unique per realm. |
| `alias` | string | create-only | Sent only on create; Keycloak derives one when omitted. Immutable: a different alias on an existing organization fails with `organization "acme": alias is "acme" on the server but "acme-corp" in the config, and Keycloak does not allow it to change; …`. |
| `enabled` | bool | set | |
| `description` | string | set | |
| `redirectUrl` | string | set | |
| `domains` | list | replaced | See [Organization domains](#organization-domains). |
| `attributes` | map of list | merged | On create sent as declared; on update merged per key over the stored map (a declared key's values replace that key's values). |
| `members` | list | additive | Usernames. A username that doesn't exist → warning `User not found, skipping organization membership`. |
| `groups` | list | — | [Organization groups](#organization-groups). Keycloak 26.6+. |
| `identityProviders` | list | additive | [Linked providers](#organization-identity-providers). |

Keycloak's organization update is not a sparse patch, so the provisioner sends the full current
representation with your fields on top; a field you don't declare keeps its server value.

Walk-through: [Onboard an organization](howto/onboard-an-organization.md).

### Organization domains

| Field | Type | Description |
|---|---|---|
| `name` | string | **Required**, unique per organization. |
| `verified` | bool | Sent only when set. |

Once you declare `domains`, the list is **authoritative**: it replaces the organization's domains, so
a domain you remove from the config is removed from Keycloak (captured on 26.6.4). Omit `domains`
to leave them alone.

### Organization groups

Groups owned by an organization. They live apart from realm groups: realm `groups` and a user's
`groups` can't reach them. Keycloak 26.6+.

```yaml
    organizations:
      - name: "acme"
        members: ["alice"]
        groups:
          - name: "staff"
            members: ["alice"]
            subGroups:
              - name: "admins"
```

| Field | Type | On update | Description |
|---|---|---|---|
| `name` | string | key | **Required**, unique among siblings. |
| `attributes` | map of list | set as declared | Sent as declared, not merged. |
| `members` | list | additive | Usernames. Each must also be a member of the organization; otherwise warning `User is not a member of the organization, skipping group membership`. |
| `subGroups` | list | — | Nested groups, any depth. |

`realmRoles` and `clientRoles` are rejected, even when empty: `organization groups cannot carry role
mappings; …`. Grant roles to the members directly, or use a realm group.

### Organization identity providers

```yaml
realms:
  - realm: "shop"
    organizationsEnabled: true
    identityProviders:
      - alias: "corporate"
        providerId: "oidc"
        config:
          clientId: "keycloak-broker"
          clientSecret: "${CORPORATE_IDP_SECRET}"
          authorizationUrl: "https://idp.example.com/authorize"
          tokenUrl: "https://idp.example.com/token"
          kc.org.domain: "acme.com"
          kc.org.broker.redirect.mode.email-matches: "true"
    organizations:
      - name: "acme"
        domains:
          - name: "acme.com"
        identityProviders: ["corporate"]
```

- Aliases of providers declared in the realm or already present. Linking is additive; nothing is
  unlinked.
- An alias that doesn't exist fails the run: `organization "acme": identity provider "corporate"
  does not exist in realm "shop"`.
- A provider belongs to at most one organization. Two organizations in the config claiming one
  alias are rejected at load; a provider already linked to another organization fails the run,
  naming that organization by id.
- When a linked provider sets `kc.org.domain` in its `config`, it must be exactly (case-sensitively)
  one of the linking organization's `domains`. Checked at load; a provider no organization in the
  config links isn't checked.

## Environment variable expansion

String values support `${VAR}`, resolved from the environment when the config is loaded, before
validation:

| Syntax | Result |
|---|---|
| `${VAR}` | Value of `VAR`, also when it is set to an empty string. |
| `${VAR}` with `VAR` unset | **Left as the literal text `${VAR}`, without a warning.** |
| `$${VAR}` | The literal text `${VAR}`. |

```yaml
secret: "${SHOP_WEB_SECRET}"        # value of SHOP_WEB_SECRET
literal: "$${SHOP_WEB_SECRET}"      # the text ${SHOP_WEB_SECRET}
```

- **An unset variable is not an error.** A forgotten `${CLIENT_SECRET}` becomes the client
  secret, password or TOTP secret `${CLIENT_SECRET}`. Check your environment before a run; see
  [Keep secrets out of the config file](howto/keep-secrets-out-of-the-config.md).
- There are no defaults (`${VAR:-x}` is looked up as a variable named `VAR:-x`, so it stays
  literal) and no `*_FILE` variants.
- **Not expanded:** top-level and realm `strategy`, client scope `type`, `acrLoaMap` keys,
  `managementPermissions` scope names, protocol mapper `config` keys (their values are expanded).
  Integers and booleans are never expanded.
- **Map keys are expanded** in: realm, client and client scope `attributes`; client
  `authenticationFlowBindingOverrides`; execution `config`; identity provider `config` and mapper
  `config`; group, organization and organization group `attributes`; group `clientRoles`; the
  `clients` keys of a user's `roles`, `serviceAccountRoles` and `scopeMappings`.
- **Two keys expanding to the same name:** in list-valued maps (group and organization
  `attributes`, `clientRoles`, `roles.clients`, …) their lists are concatenated. In string-valued
  maps (`attributes` of realms, clients and scopes, `config`) one value wins, and which one is not
  predictable.
- `sslRequired` is lowercased after expansion.

## Validation

The whole file is checked when it is loaded, before the provisioner connects. Errors name the
config path, for example `validating config: realms[0].clients[0].protocolMappers[0].protocol: is required`.
The checks:

- **YAML shape:** unknown fields (`field <name> not found in type config.<Type>`), wrong types, and
  more than one YAML document.
- **Required fields:** realm `realm`; `clientId`; role, group, scope, mapper, organization and
  domain `name`; `username`; flow `alias`; execution `provider` or `subflow`; identity provider
  `alias` and `providerId`; mapper `protocol`, `protocolMapper` and `identityProviderMapper`;
  credential `type` and `secret`.
- **Uniqueness:** realm names; `clientId` per realm; realm roles; client roles per client;
  client scopes; usernames; group and organization group names among siblings; flow and subflow
  aliases per realm; identity provider aliases; mapper names per owner; organization names;
  domains per organization; credential `type`+`label` per user; required actions per user;
  `defaultAcrValues` per client; grantee clients per management permission scope.
- **Allowed values:** `strategy`; `sslRequired`; client scope `type`; flow and subflow
  `providerId`; execution `requirement`; client binding override keys; credential `type` and
  `algorithm`.
- **Consistency:** `master` only under `masterRealm`; `password` and `initialPassword` mutually
  exclusive; `serviceAccountRoles` needs `serviceAccountsEnabled: true`;
  `standardTokenExchangeEnabled: true` needs a confidential, non-bearer-only client;
  `organizations` need `organizationsEnabled: true`; a scope not both default and optional; an
  authenticator has no `executions` or `providerId`; `managementPermissions` not `enabled: false`
  and with non-empty `scopes` and `clients`; `defaultAcrValues` in a declared `acrLoaMap`; no role
  mappings on organization groups; one organization per identity provider; `kc.org.domain` among
  the linking organization's domains.
- **Formats:** user `id` is a UUID; identity provider `alias` contains no `/`; `acrLoaMap` levels
  ≥ 0; credential `digits` and `period` ≥ 0; `defaultAcrValues` without `##`; no empty attribute
  keys, group paths or required actions; no null bytes in any string.

Validation stops at the first error. A second set of checks runs after connecting, before anything
is written: the [Keycloak version and features](#keycloak-versions-and-features) the config needs,
and the names Keycloak must recognise — execution `provider`, `protocolMapper`, identity provider
`providerId`, `identityProviderMapper` and `requiredActions`. Those provider lists are read from
the master realm; the check is skipped with a warning when the admin account may not read them,
and entirely with `--skip-version-check`. See [Deployment](deployment.md).

## Keycloak versions and features

What the pre-flight check requires; the generated table is in [Deployment](deployment.md).

| Config | Minimum Keycloak | Server feature |
|---|---|---|
| `organizationsEnabled: true`, `organizations` | 26.0 | `ORGANIZATION` |
| organization `groups` | 26.6 | `ORGANIZATION` |
| client `standardTokenExchangeEnabled: true` | 26.2 | `TOKEN_EXCHANGE_STANDARD_V2` |
| client `managementPermissions` | any | `ADMIN_FINE_GRAINED_AUTHZ` (`--features=admin-fine-grained-authz:v1`) |
| realm or client `acrLoaMap` (non-empty) | any | `STEP_UP_AUTHENTICATION` |

No other field is checked against the Keycloak version or features.
