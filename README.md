# keycloak-provisioner

[![CI](https://github.com/datarocks-ag/keycloak-provisioner/actions/workflows/ci.yaml/badge.svg)](https://github.com/datarocks-ag/keycloak-provisioner/actions/workflows/ci.yaml)
![coverage](https://raw.githubusercontent.com/datarocks-ag/keycloak-provisioner/badges/.badges/develop/coverage.svg)

A Go CLI tool that idempotently provisions Keycloak resources from a YAML config file. Designed as a Docker Compose init container.

Release notes are maintained in [CHANGELOG.md](CHANGELOG.md).

## Features

- Idempotent provisioning of realms, clients, protocol mappers, realm roles, client roles, users, service account roles, and groups (with nested subgroups and role assignments)
- Master realm configuration (SSL, users) without full provisioning
- `sslRequired` setting on any realm (`external`, `all`, `none`)
- Realm attributes, merged over Keycloak's current values so unmanaged keys are never dropped
- Client scopes as a first-class resource, with their own protocol mappers and realm-level assignment
- Role scope mappings on a client or a client scope, the other half of `fullScopeAllowed: false`
- Organizations with domains and additive membership (Keycloak 26+), including organization-scoped groups (Keycloak 26.6+)
- Authentication flows with nested subflows and execution config, plus realm and client flow bindings
- Identity providers (identity brokering) with mappers, merged over the server's representation so secrets and unmanaged config keys survive, and linkable to organizations
- Step-up authentication support via `acrLoaMap` on realms and clients, plus `defaultAcrValues` on a client
- User management with password setting, realm/client role assignment, group membership, required actions, and seeded TOTP credentials
- Service account role mapping for machine-to-machine clients
- YAML config with `${VAR}` environment variable expansion (and `$${VAR}` escape for literals)
- Configurable strategy: `update` (default) or `create` (skip existing)
- `--dry-run` mode that logs all intended changes without applying them
- Pre-flight compatibility check: refuses a config the Keycloak server is too old for, or whose server feature is disabled, before anything is mutated
- Exponential backoff retry for Keycloak connectivity
- Structured JSON logging via `log/slog`
- No external Keycloak SDK — pure `net/http`

## Quick Start

```bash
docker compose up
```

This starts Keycloak and runs the provisioner with the example config.

## Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `KEYCLOAK_USER` | yes | — | Keycloak admin username |
| `KEYCLOAK_PASSWORD` | yes | — | Keycloak admin password |
| `KEYCLOAK_URL` | no | `http://localhost:8080` | Keycloak base URL |
| `KEYCLOAK_CONFIG_PATH` | no | `./config.yaml` | Path to YAML config |
| `LOG_LEVEL` | no | `info` | Log level (debug/info/warn/error) |

**Security note:** Use HTTPS in production. The provisioner logs a warning when using plain HTTP.

## Command-line Flags

| Flag | Description |
|---|---|
| `--config <path>` | Path to YAML config; overrides `KEYCLOAK_CONFIG_PATH` |
| `--dry-run` | Log all intended changes without applying them |
| `--skip-version-check` | Skip the pre-flight Keycloak compatibility check |
| `--version` | Print version and exit |

## Strategy

Control whether existing resources are updated or skipped using the `strategy` field:

- `update` (default) — create resources if missing, update if they already exist
- `create` — create resources if missing, skip if they already exist

Strategy can be set globally or per realm. Per-realm strategy overrides the global setting.

```yaml
strategy: "create"              # global: skip existing resources

realms:
  - realm: "my-realm"
    strategy: "update"          # override: always reconcile this realm
```

## Environment Variable Expansion

String values support `${VAR}` syntax. If the variable is set in the environment, it is replaced; if unset, the placeholder is preserved as-is. Use `$${VAR}` to keep a literal `${VAR}` in the rendered config.

```yaml
secret: "${MY_APP_CLIENT_SECRET}"     # replaced with env var value at load time
literal: "$${MY_APP_CLIENT_SECRET}"   # rendered as the literal string ${MY_APP_CLIENT_SECRET}
```

Map **keys** are expanded as well as values, everywhere a config block is a map:
realm, client and client-scope `attributes`; a client's
`authenticationFlowBindingOverrides`; protocol mapper and authentication
execution `config`; group and organization `attributes`; and the clientId keys
under a group's `clientRoles`, a user's `roles.clients` and a client's
`serviceAccountRoles.clients`. If two keys expand to the same name, their values
are merged rather than one replacing the other.

Unknown YAML fields are rejected at load time so typos surface immediately.

## Dry-Run

```bash
keycloak-provisioner --dry-run
```

In dry-run mode, every mutating call logs a `DRY-RUN:` message and is skipped. Read calls pass through to Keycloak so drift between current state and config is still observed. Resources that would be created return synthetic IDs internally so the full intended sequence (clients inside a new realm, role assignments to new users, service-account role mappings on new clients) is reported in one run.

## Provisioning Order

1. **Master realm** (if configured) — update `sslRequired`, provision users
2. For each realm:
   1. **Realm** — created or updated
   2. **Authentication flows** — created if missing (matched by `alias`), never modified
      - **Executions and subflows** — appended in declared order, requirements and config set
   3. **Realm authentication bindings** — applied as a second realm update, after flows exist
   4. **Client scopes** — created or updated (matched by `name`)
      - **Protocol mappers** — created or updated (matched by `name`)
      - **Realm-level assignment** — added if `type` is `default` or `optional`, moved if the scope currently carries the other type
   5. **Clients** — created or updated (matched by `clientId`)
      - **Protocol mappers** — created or updated (matched by `name`)
      - **Client roles** — created or updated
      - **Client scope assignment** — configured default/optional scopes attached, moved if attached with the other type
   6. **Realm roles** — created or updated
   7. **Service account roles** — assigned (additive, after roles exist)
      - **Scope mappings** — roles declared on a client or client scope added to its scope (additive, after roles exist)
   8. **Fine-grained management permissions** — enabled per client, one policy per scope created or updated, attached to the scope permission (after every client exists)
   9. **Groups** — created or updated (matched by `name`)
      - **Attributes** — set from config
      - **Realm/client role assignments** — granted if not already mapped (additive)
      - **Subgroups** — created or updated recursively
   10. **Users** — created or updated, passwords set, roles assigned, group memberships added (additive)
   11. **Identity providers** — created or updated (matched by `alias`), merged over the server's representation
       - **Mappers** — created or updated (matched by `name`); their config is replaced, not merged
   12. **Organizations** — created or updated (matched by `name`), domains set, members added, identity providers linked (all additive)

Authentication flows run before clients so a client can bind to a flow defined
in the same config, and before the realm bindings that reference them. Client
scopes run before clients so a client can reference a scope defined in the same
config. Groups run after roles so their realm/client role assignments resolve to
roles created earlier in the same run, and before users so group memberships
resolve to groups defined in the same config. Identity providers run after
authentication flows, whose aliases their broker login fields reference, and
after roles and groups, which a hardcoded-role or hardcoded-group mapper names.
Scope mappings run with the service account roles rather than where they are
declared: they name roles on any client in the realm, and realm roles, none of
which exist while the owning client or client scope is being reconciled.
Fine-grained management permissions run after the whole client loop rather than
inside it: a permission names *another* client as the grantee, and either client
may be declared first. Organizations run last so their members resolve to users created in the same run
and their identity provider links resolve to providers created just above. Role assignments, group
memberships and organization memberships are additive: the provisioner grants any configured role or
membership that is not yet present, and never removes existing ones.

## Config Example

See [config.example.yaml](config.example.yaml) for a full example. The YAML field names mirror Keycloak's realm JSON export format (camelCase), so you can copy-paste from an exported realm.

```yaml
# Optional: configure the master realm (update-only, never created)
masterRealm:
  sslRequired: external
  users:
    - username: "admin-new"
      password: "${ADMIN_PASSWORD}"
      enabled: true
      roles:
        realm:
          - admin

realms:
  - realm: "my-realm"
    displayName: "My Realm"
    enabled: true
    sslRequired: "external"
    clients:
      - clientId: "my-app"
        secret: "${MY_APP_CLIENT_SECRET}"
        enabled: true
        protocol: "openid-connect"
        redirectUris:
          - "https://myapp.example.com/*"
        protocolMappers:
          - name: "audience-mapper"
            protocolMapper: "oidc-audience-mapper"
            config:
              "included.client.audience": "my-app"
        clientRoles:
          - name: "admin"

      # Service account with role assignments
      - clientId: "my-service"
        serviceAccountsEnabled: true
        serviceAccountRoles:
          realm:
            - app-admin
          clients:
            my-app:
              - admin

    roles:
      - name: "app-admin"
        description: "Application administrator"

    users:
      - username: "service-admin"
        password: "${SERVICE_ADMIN_PASSWORD}"
        enabled: true
        email: "admin@example.com"
        firstName: "Service"
        lastName: "Admin"
        emailVerified: true
        roles:
          realm:
            - app-admin
          clients:
            my-app:
              - admin
        groups:
          - "/engineering/backend"

    groups:
      - name: "engineering"
        attributes:
          department:
            - "engineering"
        realmRoles:
          - "app-admin"
        clientRoles:
          my-app:
            - "admin"
        subGroups:
          - name: "backend"
```

## Realm Fields

Every field is optional except `realm`. A field left out is not sent, so Keycloak keeps its current value.

| Field | Type | Description |
|---|---|---|
| `realm` | string | **Required.** Realm name. `master` is rejected here — use `masterRealm`. |
| `displayName` | string | Human-readable name shown in the console |
| `enabled` | bool | Whether the realm is enabled |
| `sslRequired` | string | `external`, `all`, or `none` (see below) |
| `loginTheme` | string | Login theme name |
| `registrationAllowed` | bool | Whether self-registration is open |
| `resetPasswordAllowed` | bool | Whether users may reset their own password |
| `loginWithEmailAllowed` | bool | Whether users may log in with their email address |
| `bruteForceProtected` | bool | Enable brute force detection (temporary lockout after repeated failed logins) |
| `organizationsEnabled` | bool | Enable Keycloak Organizations (26+) |
| `attributes` | map | Realm attributes, merged over the current ones |
| `acrLoaMap` | map | ACR value to Level of Authentication (see Step-Up Authentication) |
| `strategy` | string | `update` or `create`, overriding the global setting for this realm |

## Client Fields

| Field | Type | Description |
|---|---|---|
| `clientId` | string | **Required.** Client identifier |
| `secret` | string | Client secret for a confidential client |
| `name` | string | Human-readable name |
| `enabled` | bool | Whether the client is enabled |
| `publicClient` | bool | Public rather than confidential |
| `protocol` | string | `openid-connect` or `saml` |
| `rootUrl` | string | Root URL that relative URLs below are resolved against |
| `baseUrl` | string | Default URL to redirect to after login |
| `adminUrl` | string | URL Keycloak calls for backchannel requests such as logout |
| `redirectUris` | list | Valid redirect URIs |
| `webOrigins` | list | Allowed CORS origins |
| `standardFlowEnabled` | bool | Authorization Code flow |
| `directAccessGrantsEnabled` | bool | Resource Owner Password Credentials grant |
| `serviceAccountsEnabled` | bool | Client Credentials grant, required for `serviceAccountRoles` |
| `standardTokenExchangeEnabled` | bool | Standard Token Exchange, RFC 8693 (see below) |
| `bearerOnly` | bool | Client only validates tokens and never initiates login |
| `consentRequired` | bool | Require user consent |
| `frontchannelLogout` | bool | Use front-channel rather than back-channel logout |
| `defaultClientScopes` | list | Scopes always applied (see Client Scopes) |
| `optionalClientScopes` | list | Scopes requestable via the `scope` parameter |
| `attributes` | map | Client attributes, merged over the current ones |
| `acrLoaMap` | map | ACR value to Level of Authentication for this client |
| `defaultAcrValues` | list | ACR values applied when a request asks for none (see Step-Up Authentication) |
| `authenticationFlowBindingOverrides` | map | Override realm flow bindings (see Authentication Flows) |
| `protocolMappers` | list | Protocol mappers on this client |
| `clientRoles` | list | Roles defined on this client |
| `serviceAccountRoles` | object | Roles granted to the service account |
| `fullScopeAllowed` | bool | Whether tokens carry every role the subject holds (see Token Exchange) |
| `scopeMappings` | object | Roles in this client's scope (see Role Scope Mappings) |
| `managementPermissions` | object | Fine-grained admin permissions on this client (see Fine-Grained Admin Permissions) |

## Master Realm

The `masterRealm` section configures the built-in master realm. Since the master realm always exists, it is update-only — the provisioner will never attempt to create it. This section is deliberately separate from the `realms` list to prevent accidentally applying full provisioning to master.

Supported fields:

- `sslRequired` — set the SSL mode (`external`, `all`, `none`)
- `users` — create/update users in the master realm (same schema as realm users)

## SSL Required

Set `sslRequired` on any realm (including master) to control whether Keycloak requires SSL:

| Value | Meaning |
|---|---|
| `external` | SSL required for external requests (recommended for production) |
| `all` | SSL required for all requests |
| `none` | SSL not required (development only) |

## Users

Users can be provisioned in any realm (including master via `masterRealm.users`). Each user supports:

| Field | Type | Description |
|---|---|---|
| `username` | string | **Required.** Username |
| `id` | string | Fix the user's UUID so it is the same in every environment (see below) |
| `password` | string | Permanent password (set on every run via reset-password API). Mutually exclusive with `initialPassword`. |
| `initialPassword` | string | Temporary password — only set when the user is first created. The user must change it on first login. Ignored on subsequent runs if the user already exists. Mutually exclusive with `password`. |
| `enabled` | bool | Whether the user is enabled |
| `email` | string | Email address |
| `firstName` | string | First name |
| `lastName` | string | Last name |
| `emailVerified` | bool | Whether the email is marked as verified |
| `roles` | object | Role assignments (see below) |
| `groups` | list | Group memberships by path (see below) |
| `requiredActions` | list | Actions Keycloak makes the user complete at next login (see below) |
| `credentials` | list | Credentials to seed, currently TOTP only (see below) |

### Required Actions

```yaml
users:
  - username: "bob"
    requiredActions:
      - "CONFIGURE_TOTP"
```

Declaring the field **replaces** whatever the user has; omitting it leaves them
alone, and an empty list is how to clear them.

Action names are checked against the server before anything is written. Keycloak
accepts an unknown action with `204` and then silently drops it, so a typo would
otherwise look applied while the user is never asked to do anything.

### Seeding a TOTP Credential

`requiredActions: [CONFIGURE_TOTP]` makes the *user* enrol. The alternative is to
seed a known secret, so the realm rebuilds from config with no manual step and an
automated test can compute valid codes:

```yaml
users:
  - username: "bob"
    credentials:
      - type: "otp"
        label: "seeded"
        secret: "${BOB_TOTP_SECRET}"
```

`type` must be `otp` — it is the only credential a config can usefully carry.
Passwords have their own fields, while WebAuthn is device-bound and recovery
codes are generated for one-time display.

**The secret is not base32.** This is the part worth reading twice. Keycloak uses
the characters of `secret` *directly* as the HMAC key; it does not base32-decode
them. An authenticator app must therefore be given `base32(secret)` — which is
exactly what Keycloak's own QR code shows once the credential exists. Computing
codes from the base32-decoded bytes produces codes Keycloak rejects, and the
failure looks like bad seeding rather than a bad client.

`digits`, `period` and `algorithm` default to Keycloak's own `6`, `30` and
`HmacSHA1`. Change them only to match an existing authenticator; `algorithm`
accepts `HmacSHA1`, `HmacSHA256` or `HmacSHA512`.

**Seeding is additive and never rotates.** Keycloak's user update *appends* the
credentials it is given rather than reconciling them, so a credential of the same
type and label is left alone — otherwise every run would add another. Replacing a
seeded secret means deleting the credential first, which the provisioner does not
do: it removes nothing, anywhere.

Two errors are worth recognising, because neither says what is wrong:

- **`Invalid user credentials`** on a password-only login once a TOTP credential
  exists. The password is correct; a factor is missing.
- **`Account is not fully set up`**. Usually nothing to do with credentials —
  Keycloak's declarative user profile requires `firstName` and `lastName`, and a
  user missing them cannot complete a login.

### Predictable User IDs

Setting `id` gives a user the same UUID in every environment, so other systems can
reference it without a lookup:

```yaml
users:
  - username: "alice"
    id: "11111111-2222-3333-4444-555555555555"
```

Generate them deterministically rather than by hand — a UUIDv5 over a fixed namespace plus
the username gives the same id for `alice` everywhere, with no table to maintain.

Two things to know:

- **It applies only when the user is created.** Keycloak does not allow an id to change
  afterwards. If a user with that username already exists under a different id, the run
  fails and names both, rather than provisioning against an identity other systems may
  already reference differently.
- **Such a user is created through Keycloak's partial import**, not the usual create call.
  Keycloak's create-user endpoint accepts an `id` in the representation and silently
  discards it, generating its own; import honours it. Everything after creation — password,
  roles, group memberships — goes down the normal path either way, and the import is
  configured to skip a username that already exists, so re-runs stay idempotent.

Users are still matched by `username`, so adding an `id` to an existing config changes
nothing for users that already exist.

### User Role Assignment

Roles are assigned additively — existing role mappings are never removed. Both realm roles and client roles are supported:

```yaml
roles:
  realm:
    - app-admin          # realm-level role
  clients:
    my-app:              # client ID
      - admin            # client-level role
```

The referenced roles and clients must already exist (either defined earlier in the config or pre-existing in Keycloak).

### User Group Membership

Users can be added to groups by path, using Keycloak's path notation (the leading slash is optional). Nested groups are addressed by their full path:

```yaml
groups:
  - "engineering"              # top-level group
  - "/engineering/backend"     # nested group
```

Memberships are additive — the provisioner never removes a user from a group. The referenced groups must exist (either defined in the same config's `groups` section, which is provisioned before users, or pre-existing in Keycloak). A group that cannot be found is logged as a warning and skipped; it does not abort the run.

## Service Account Roles

Clients with `serviceAccountsEnabled: true` can have roles assigned to their service account user via `serviceAccountRoles`. This uses the same `roles` schema as users:

```yaml
clients:
  - clientId: "my-service"
    serviceAccountsEnabled: true
    serviceAccountRoles:
      realm:
        - app-admin
      clients:
        another-client:
          - some-role
```

Validation enforces that `serviceAccountsEnabled` is `true` when `serviceAccountRoles` is set.

## Token Exchange (RFC 8693)

Set `standardTokenExchangeEnabled: true` on a client to enable Keycloak's [Standard Token Exchange](https://www.keycloak.org/securing-apps/token-exchange) — the OAuth 2.0 Token Exchange grant (`urn:ietf:params:oauth:grant-type:token-exchange`, RFC 8693). This lets the client exchange its access token for a token targeting another client in the same realm.

```yaml
clients:
  - clientId: "exchange-service"
    publicClient: false
    secret: "${EXCHANGE_SERVICE_SECRET}"
    standardTokenExchangeEnabled: true
```

Requirements:

- **Confidential client.** The requesting client must authenticate its exchange requests at the token endpoint. `publicClient` must not be `true` (omitting it is fine — Keycloak defaults clients to confidential), and the client must not be `bearerOnly`. Validation rejects both cases.
- **Keycloak 26.2+.** Standard Token Exchange is a supported feature from 26.2 onward (earlier versions only had the non-standard preview feature).

Under the hood this sets the `standard.token.exchange.enabled` client attribute. Setting the typed field takes precedence over the same key set manually in `attributes`.

Setting `standardTokenExchangeEnabled: false` explicitly disables the feature — the attribute is written as `false`, correcting drift if it was enabled out-of-band. Omitting the field leaves the attribute unmanaged (existing values in Keycloak are left untouched).

### Narrowing what an exchanged token carries

Enabling exchange says *who* may exchange; `fullScopeAllowed` says *how much* the
resulting token carries. Keycloak sets it to `true` on every client it creates,
which means the token carries every role the subject holds — not just the ones
reachable through the client's assigned scopes. For an exchange client that is
usually far wider than intended:

```yaml
clients:
  - clientId: "exchange-service"
    publicClient: false
    secret: "${EXCHANGE_SERVICE_SECRET}"
    standardTokenExchangeEnabled: true
    fullScopeAllowed: false          # carry only roles reachable via assigned scopes
```

With `fullScopeAllowed: false`, roles reach the token only through the client's
scope, so the client's token scope is what the config says it is rather than
whatever the subject happens to hold. Declaring the flag alone narrows the client
to nothing: `scopeMappings` is what puts roles back. See
[Role scope mappings](#role-scope-mappings).

Omitting the field leaves it unmanaged. Keycloak's client update is a sparse merge
for this flag, so a client already set to `false` out-of-band is not widened by a
run that does not declare it — but a client the provisioner *creates* without the
field gets Keycloak's permissive `true`. Declare it explicitly on clients where the
scope matters.

## Fine-Grained Admin Permissions

`managementPermissions` declares Keycloak's **v1** fine-grained admin permissions on a client: which other clients are allowed to exercise a given admin capability on it.

```yaml
clients:
  - clientId: "sandbox-router"
    managementPermissions:
      scopes:
        token-exchange:
          clients: ["sandbox-bff"]
  - clientId: "sandbox-bff"
```

This is the only way to express a v1 **token-exchange** permission, which is what gates impersonation — the exchange that accepts `requested_subject`. Without it that permission has to be applied by hand after every run, and the realm cannot be rebuilt from its configuration.

### Requires a server feature

The API behind this is off by default. Start Keycloak with:

```
--features=admin-fine-grained-authz:v1
```

Without it the admin API answers `501` with `{"error": "Feature not enabled"}`, naming neither the feature nor the flag. The provisioner checks for it before writing anything and refuses the run with a message that names the flag — see [Keycloak Compatibility](#keycloak-compatibility).

Note that enabling v1 **switches the v2 permission model off**: Keycloak reports `ADMIN_FINE_GRAINED_AUTHZ` and `ADMIN_FINE_GRAINED_AUTHZ_V2` as separate features and only one is active. This is a realm-wide consequence of a per-client setting, so it is worth deciding deliberately.

### Scopes

Any scope the running Keycloak offers can be used as a key. Scope names are validated against the `scopePermissions` map the server itself returns, so a typo is reported by name rather than silently ignored, and no upgrade is needed when Keycloak adds one. On 26.6 and 26.7 the set is:

`view`, `manage`, `configure`, `map-roles`, `map-roles-client-scope`, `map-roles-composite`, `token-exchange`

### What is authoritative and what is additive

This is the one place the provisioner replaces state rather than only adding to it, and the split is deliberate:

- **The policy is the provisioner's.** For each `(client, scope)` pair it owns a client policy named `keycloak-provisioner.<scope>.<clientId>`, and rewrites its client list to match the config. **Removing a `clientId` from the list withdraws that grant on the next run.** A permission that could only grow would leave impersonation rights that no edit could take back, which would defeat the point of declaring them.
- **The permission's policy list is shared.** The provisioner's policy is added alongside whatever is already attached, so a policy created by hand or by another tool keeps working.

Nothing is deleted. Removing a scope from the config stops it being reconciled but leaves the existing grant in place — to revoke everything, remove the clients from the list rather than the scope from the config. For the same reason `enabled: false` is rejected at validation: Keycloak deletes the client's whole scope permission set, and the policies attached to it, when fine-grained permissions are switched off.

The permission's `decisionStrategy` is set to `AFFIRMATIVE`, so each attached policy grants independently. Under Keycloak's `UNANIMOUS` default a permission carrying more than one policy grants only when *every* policy passes, which would silently make the configured grant ineffective. If the provisioner loosens an existing `UNANIMOUS` permission that already had policies attached, it logs a warning saying so.

### Using this for impersonation

The `token-exchange` scope permission is one of four things v1 impersonation needs — the exchange that accepts `requested_subject`. All four are required; the following was verified end to end against Keycloak 26.6 and 26.7.2 by decoding the returned token and confirming its `sub` is the impersonated user, not the service account.

1. **Both server features**, not just this one: `--features=token-exchange:v1,admin-fine-grained-authz:v1`. They are independent flags, and `admin-fine-grained-authz:v1` does not enable the v1 exchange provider.
2. **The `token-exchange` permission on the audience client** — the client being exchanged *to*, not the requesting one. That is what this section configures.
3. **`realm-management`'s `impersonation` role held by the identity in the subject token** — see below. This is the one that is easy to put in the wrong place.

Removing either of 2 or 3 fails the exchange.

#### The trap: the role belongs to the operator, not to the client

The natural assumption is that the *requesting client* needs permission to impersonate, so the role goes on its service account. That is wrong for the ordinary flow, and it fails in a way that looks like everything is configured.

Keycloak checks the identity the `subject_token` represents. In a real impersonation that is the **operator** — the human whose session the exchange is performed under — so the operator's own account needs the role. Measured on 26.7.2 against a realm with a service-account grant already in place, removing one thing at a time:

| removed | exchange |
|---|---|
| — (all present) | succeeds |
| `token-exchange` permission on the audience client | **refused** |
| `impersonation` on the requesting client's **service account** | succeeds — not required |
| that role in the client's **scope mappings** | succeeds — not required |
| `impersonation` on the **operator's user account** | **refused** |

So a realm can grant the role to the client's service account, verify it is present there, and still be refused — which is exactly what the role's absence on the operator looks like:

```yaml
users:
  - username: "bob"          # an operator who may impersonate
    roles:
      clients:
        realm-management: ["impersonation"]
```

The service-account grant matters only when a client exchanges **its own** token — then the service account *is* the identity in the subject token, and the usual rule applies: with `fullScopeAllowed: false` the role must also be in the client's [scope mappings](#role-scope-mappings) or it never reaches the token.

Note what this role is: `realm-management:impersonation` lets its holder impersonate through Keycloak's admin API as well, not only through the exchange. Granting it to operator accounts is a real privilege decision, not a formality.

#### Telling the two failures apart

The error distinguishes a missing feature from a missing grant, which is worth knowing before changing anything:

| error | meaning |
|---|---|
| `invalid_request: Parameter 'requested_subject' is not supported for standard token exchange` | `token-exchange:v1` is **not** enabled — the request reached the v2 provider, which has no impersonation at all |
| `access_denied: Client not allowed to exchange` | v1 **is** active; one of the grants above is missing or is not reaching the token |

## Client Scopes

Client scopes are defined per realm under `clientScopes` and provisioned before clients, so a client can reference a scope declared in the same config.

```yaml
realms:
  - realm: "my-realm"
    clientScopes:
      - name: "orders:read"
        description: "Read access to orders"
        protocol: "openid-connect"   # default when omitted
        type: "optional"             # "default", "optional", or "none" (default)
        attributes:
          "include.in.token.scope": "true"
          "display.on.consent.screen": "false"
        protocolMappers:
          - name: "orders-audience"
            protocol: "openid-connect"
            protocolMapper: "oidc-audience-mapper"
            config:
              "included.client.audience": "orders-api"

    clients:
      - clientId: "orders-app"
        optionalClientScopes:
          - "orders:read"
```

`type` controls realm-level assignment: `default` adds the scope to every newly created client, `optional` makes it requestable through the `scope` parameter, and `none` (the default) assigns it nowhere. Changing `type` between `default` and `optional` moves the scope: it is detached from the list it is on before it is added to the other. Setting `type` to `none` does not detach it from either — it only stops the provisioner from managing the realm-level assignment.

Scopes are matched by `name`. Protocol mappers on a scope follow the same rules as protocol mappers on a client.

`defaultClientScopes` and `optionalClientScopes` on a client attach existing scopes to that client. Configured scopes that are not yet attached are added, and a configured scope currently attached with the other type is moved — listing an optional scope under `defaultClientScopes` detaches it from the optional list first, because Keycloak answers the conflicting assignment with 204 and would otherwise keep the old type. Scopes the config does not mention are never detached, including Keycloak's own default scopes, which a client declaring a scope keeps. A referenced scope that does not exist is logged as a warning and skipped, and naming the same scope in both lists is rejected at load time — the two types are mutually exclusive.

## Role Scope Mappings

`scopeMappings` declares which roles are in scope for a client, or for a client
scope. It is the other half of `fullScopeAllowed: false`: that flag decides
whether a client's tokens carry every role the subject holds, and this decides
which roles they carry instead.

```yaml
realms:
  - realm: "my-realm"
    clients:
      - clientId: "bff"
        fullScopeAllowed: false
        scopeMappings:
          realm:
            - "app-admin"
          clients:
            sandbox-ledger:
              - "reader"
            sandbox-document:
              - "reader"
```

A role reaches the token only if the subject holds it **and** it is in the
client's scope, so `scopeMappings` narrows — it never grants. Granting is what a
user's or group's `roles` do. Both halves are needed: a client whose scope
includes `sandbox-ledger`'s `reader` still issues tokens without it to a user who
was never given that role.

This is also what makes an audience reachable. Keycloak derives the audiences a
client may request from the roles in its scope, so with `fullScopeAllowed: false`
and no scope mappings, a token exchange fails with `Requested audience not
available` for every downstream client. Naming that client's roles here — and no
others — is how an exchange is scoped to exactly the audiences it should reach.

The same field on a client scope declares the roles once for every client the
scope is attached to:

```yaml
realms:
  - realm: "my-realm"
    clientScopes:
      - name: "ledger-access"
        scopeMappings:
          clients:
            sandbox-ledger:
              - "reader"
    clients:
      - clientId: "bff"
        fullScopeAllowed: false
        defaultClientScopes:
          - "ledger-access"
```

Keycloak treats a role reached this way exactly like one mapped on the client
itself. Which to use is a question of reuse: on the client for a grant that
belongs to that client alone, on a client scope for one several clients share.

Assignment is additive, like every other role assignment here: a role already in
scope is left alone, and one present on the server but absent from the config is
never removed. This cannot take back a scope that was widened out of band — to
narrow a client that already has too much in scope, remove the mapping in
Keycloak. A role named here that does not exist fails the run rather than being
skipped, since a role silently left out of scope surfaces much later as a token
missing a claim.

## Realm Attributes

Keycloak stores a number of realm settings as free-form attributes. Declare them under `attributes` on any realm:

```yaml
realms:
  - realm: "my-realm"
    attributes:
      frontendUrl: "https://id.example.com"
      userProfileEnabled: "true"
```

Both keys and values support `${VAR}` expansion.

Attributes are **merged**, not replaced. Keycloak's realm update replaces the whole attribute map, and several realm settings live there, so the provisioner reads the realm's current attributes and merges the configured keys over them. Keys you do not declare are preserved; nothing is ever removed. Omitting the `attributes` block entirely leaves realm attributes untouched.

Client `attributes` are merged the same way: Keycloak replaces the whole attribute map on a client update, so the provisioner sends the union of the client's current attributes and the configured ones. A client that declares no `attributes`, `acrLoaMap`, `defaultAcrValues`, or `standardTokenExchangeEnabled` sends no attributes at all.

## Authentication Flows

Flows are declared per realm and created before clients, so a client can bind to a flow defined in the same config.

```yaml
realms:
  - realm: "my-realm"
    authenticationFlows:
      - alias: "browser-step-up"
        description: "Browser flow with LoA step-up"
        providerId: "basic-flow"   # or "form-flow"; default basic-flow
        copyFrom: "browser"        # seed from an existing flow
        executions:
          - subflow: "loa-gold"
            requirement: "CONDITIONAL"
            providerId: "basic-flow"           # subflow type; default basic-flow
            description: "Require gold LoA"    # shown against the subflow
            executions:
              - provider: "conditional-level-of-authentication"
                requirement: "REQUIRED"
                config:
                  alias: "gold-condition"      # names the config itself
                  loa-condition-level: "2"
              - provider: "auth-otp-form"
                requirement: "REQUIRED"

    authenticationBindings:
      browserFlow: "browser-step-up"

    clients:
      - clientId: "web"
        authenticationFlowBindingOverrides:
          browser: "browser-step-up"
```

Each execution is either a `provider` (an authenticator) or a `subflow` (a nested flow), never both. `requirement` is `REQUIRED`, `ALTERNATIVE`, `DISABLED`, or `CONDITIONAL`. Executions are created in the order declared — Keycloak appends each one, so the declared order is the resulting order.

Two fields apply to subflows only. `providerId` is the subflow's own type, `basic-flow` (the default) or `form-flow` — distinct from the `providerId` on the flow itself. `description` is shown against the subflow in the console; Keycloak ignores it on a plain authenticator.

**Flows are create-only.** A flow whose alias already exists is left untouched, whatever the `strategy`, and the skip is logged at INFO so an edit that does not take effect is visible in the log. Reconciling an existing flow would mean diffing an ordered tree whose entries have no stable name and deleting the executions that are not configured — the provisioner does not remove anything anywhere else, and does not start here. To change a flow, delete it in the Keycloak console or declare it under a new alias.

Declaring a built-in Keycloak flow (`browser`, `direct grant`, …) is rejected with an error pointing at `copyFrom`, since editing built-ins in place is how a realm becomes hard to recover. Use `copyFrom` to derive your own flow from one instead.

`authenticationBindings` points the realm's flow bindings at aliases — `browserFlow`, `directGrantFlow`, `resetCredentialsFlow`, `registrationFlow`, `clientAuthenticationFlow`, `dockerAuthenticationFlow`, `firstBrokerLoginFlow`. It is applied as a second realm update, after the flows exist. Unset bindings are left alone.

Clients can override realm bindings with `authenticationFlowBindingOverrides`, keyed by `browser` or `direct_grant`. Values are flow **aliases**; the provisioner resolves them to the flow IDs Keycloak stores on the client. An alias that does not resolve is an error rather than a silent skip, since a client bound to the wrong flow is a security-relevant misconfiguration.

## Step-Up Authentication (`acr.loa.map`)

`acrLoaMap` maps ACR values to Levels of Authentication, which is what Keycloak uses to decide whether a session already satisfies a requested authentication level. It is available on both realms and clients:

```yaml
realms:
  - realm: "my-realm"
    acrLoaMap:
      silver: 1
      gold: 2
    clients:
      - clientId: "web"
        acrLoaMap:
          gold: 2
        defaultAcrValues:
          - "silver"
```

Under the hood this sets the `acr.loa.map` attribute as a JSON object. Setting the typed field takes precedence over the same key set manually in `attributes`, matching how `standardTokenExchangeEnabled` behaves. Levels must not be negative, and map keys are written in sorted order so repeated runs produce identical values.

### Default ACR values

`defaultAcrValues` is what Keycloak applies when a request asks for no particular ACR. Use the typed field rather than the attribute, because the encoding is not what the shape suggests:

- Keycloak stores the values in `default.acr.values` as one **`##`-separated string** — `gold##silver` — not as a JSON array.
- A JSON array is rejected, and the message is about the ACR-to-LoA map rather than about encoding, so it reads as the wrong problem: `Default ACR values need to contain values specified in the ACR-To-Loa mapping or number levels from set realm browser flow`.
- Bare level numbers are rejected too, despite that message mentioning them.

Every value must be a key of the effective map — this client's `acrLoaMap` or the realm's. The config checks that itself when a map is declared, naming the offending value and listing what is available, since Keycloak's refusal names neither. When no map is declared anywhere the check is skipped, because the map may already exist on the server.

The LoA map on its own does not create a step-up flow — it only names the levels. A browser flow containing a `conditional-level-of-authentication` subflow is what actually enforces them.

## Organizations

`organizationsEnabled` toggles Keycloak Organizations for a realm, and `organizations` provisions them:

```yaml
realms:
  - realm: "my-realm"
    organizationsEnabled: true
    organizations:
      - name: "acme"
        alias: "acme"              # optional; Keycloak derives one when omitted
        enabled: true
        description: "ACME Corp"
        redirectUrl: "https://acme.example.com"
        domains:
          - name: "acme.com"
            verified: true
          - name: "acme.org"
        attributes:
          tier:
            - "gold"
        members:
          - "alice"
```

Requires Keycloak 26+, where the `organization` feature is enabled by default. Organization groups need **Keycloak 26.6+**, which is the version the integration tests and compose stack target. Omitting `organizationsEnabled` leaves the realm's current setting untouched; declaring `organizations` without setting it to `true` is rejected at config load time, the same way `serviceAccountRoles` requires `serviceAccountsEnabled`.

Organizations are matched by `name` and provisioned last, after users, so `members` can reference users defined in the same config. Membership is additive — members are never removed, and a username that cannot be resolved is logged as a warning and skipped.

### Organization groups

Organizations can own groups, declared under `groups` on the organization:

```yaml
organizations:
  - name: "acme"
    members:
      - "alice"
    groups:
      - name: "engineering"
        attributes:
          tier:
            - "gold"
        members:
          - "alice"
        subGroups:
          - name: "backend"
```

These are **not** realm groups. They live in a namespace of their own: they never appear under the realm's `groups`, and Keycloak refuses to manage them through the normal group API (`Cannot manage organization related group via non Organization API`). The realm-level `groups` section and a user's `groups` list therefore cannot reach them, and vice versa.

Two further differences from realm groups:

- **No role mappings**, and the API is a trap. `realmRoles` and `clientRoles` are
  rejected at config load with an explanation, rather than left out of the schema
  and refused as unknown fields, because the underlying endpoint misleads. On Keycloak 26.6,
  `POST /organizations/{org}/groups/{group}/role-mappings/realm` answers **404**.
  On 26.7 it answers **204**, and the subsequent `GET` returns the role — but the
  mapping has no effect: a member's composite realm roles are unchanged,
  `GET /roles/{name}/users` stays empty, and the role reaches no token claim.
  Reading the API alone, it looks supported, so the provisioner names the trap
  instead of leaving it to be rediscovered.
- **Members must already belong to the organization.** Keycloak rejects adding a non-member with `User is not member of the organization`, so list the user under the organization's `members` as well. A group member the organization does not have is logged as a warning and skipped rather than failing the run.

Group names must be unique among siblings but may repeat at different levels, matching realm groups. Subgroups nest to any depth, and both group creation and membership are additive.

`alias` is immutable once the organization exists. On create it is sent only when declared, so omitting it lets Keycloak derive one rather than copying `name`. Declaring an alias that differs from the stored one is rejected before the request is sent, naming both — Keycloak's own refusal names neither.

### Updates merge over the server's representation

Like identity providers, an organization update is not a sparse patch, and Keycloak is inconsistent about how it says so. Measured against 26.6:

| left out of the update | what happens |
| --- | --- |
| `alias` | **400 `Cannot change the alias`** — the message reads as though something tried to change it; in fact nothing supplied it, and the absence is read as setting it to null |
| `domains` | **silently cleared** |
| `redirectUrl` | **silently cleared** |
| `attributes` | preserved — but supplying any *replaces* the whole map |

So the provisioner reads the organization's current representation and merges the config over it. A domain, redirect URL or attribute set outside the config survives a run that does not mention it, and configured attributes merge over stored ones rather than replacing them — the same rule realm and client attributes follow.

The read is a separate request per organization on the update path, because the search listing used to find it by name omits `attributes`.

Organizations can link identity providers by alias — see
[Identity Providers](#identity-providers) below. A provider must already exist,
either declared in the same config or already present in the realm, and belongs
to at most one organization.

## Identity Providers

Identity providers (identity brokering) are declared per realm under
`identityProviders` and provisioned before organizations, so an organization can
link a provider declared in the same config.

```yaml
realms:
  - realm: "my-realm"
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

`alias` identifies the provider and is a URL path segment, so it must not
contain `/`. `providerId` is the broker type (`oidc`, `saml`, `google`, …) and
is validated against the types the server actually offers. Secrets belong in
`config` via `${VAR}`; nothing is ever written to the config file.

### Updates are a merge, because Keycloak's are a replace

This is the one resource where Keycloak replaces the **whole** representation on
update: a field or config key left out of the request is deleted, not left
alone. The provisioner therefore reads the current representation and merges the
config over it, rather than sending the sparse body it uses everywhere else.

Two consequences worth knowing:

- **A stored client secret survives.** Keycloak returns it masked as
  `**********` and reads that mask back as "keep what you have", so a provider
  whose secret was set out of band keeps it across runs. Omitting the key would
  delete it.
- **Config keys the schema does not model survive.** Anything set out of band —
  by an operator, or by a Keycloak version that knows a key this provisioner
  does not — is carried forward.

The server-derived `internalId`, `organizationId` and `types` are dropped rather
than echoed back.

Mapper `config`, by contrast, is **replaced** from the config alone. A mapper has
neither a masked secret nor unmodelled server fields, so removing a key from the
config removes it from Keycloak — which is not true one level up. The asymmetry
is deliberate.

### Validation

`providerId` and `identityProviderMapper` are checked against the server before
anything is written, using the server info the compatibility check already
reads — so this costs no extra request. The check is worth more than most:
Keycloak accepts an **unknown mapper type with 201** and then silently never
applies it, so a typo here is invisible rather than merely late. The provider
list also reflects feature state, not just the build: `instagram` is absent
unless `INSTAGRAM_BROKER` is enabled.

`firstBrokerLoginFlowAlias` and `postBrokerLoginFlowAlias` are checked against
the realm's flow listing and **warn** rather than fail — the listing can
legitimately be incomplete, such as in a dry run against a realm that does not
exist yet. Keycloak answers a bare 500 for an alias it cannot resolve, so the
warning names the flow the error would not.

### Linking to an organization

```yaml
    organizations:
      - name: "acme"
        domains:
          - name: "acme.test"
        identityProviders:
          - "corporate"
```

Linking is additive and a provider belongs to at most one organization, so two
organizations claiming the same alias is rejected at config load rather than
failing partway through a run. A provider already linked to an organization the
config does not describe is caught too, before the request is sent: the realm's
provider listing carries the owning organization, where Keycloak's own answer is
a bare 400 naming neither side.

Unlike a missing member, which is warned about and skipped, an alias naming no
provider in the realm **fails**: a missing user is plausible drift, a missing
alias is a config error.

The realm's providers are listed once and shared across every organization, so
a realm with many organizations costs one listing, not one per organization.

### Not supported

Deleting, unlinking or renaming anything, consistent with the rest of the
provisioner. Per-provider config-key validation is not possible —
`GET /identity-provider/providers/oidc` returns an empty `configProperties` — and
per-type mapper validation needs the instance to exist, so it cannot run
pre-flight.

## Keycloak Compatibility

Some configuration only works on newer Keycloak releases, and some of it also depends on a server feature that can be switched off. Before provisioning anything, the tool reads the server version and feature list and refuses a config the server cannot apply — so a run either does the whole job or changes nothing, rather than failing halfway with a raw Keycloak error.

<!-- BEGIN COMPATIBILITY TABLE -->

| Config | Minimum Keycloak | Server feature |
|---|---|---|
| organizations | 26.0 | `ORGANIZATION` |
| organization groups | 26.6 | `ORGANIZATION` |
| standard token exchange | 26.2 | `TOKEN_EXCHANGE_STANDARD_V2` |
| client management permissions | any | `ADMIN_FINE_GRAINED_AUTHZ` |
| step-up authentication | any | `STEP_UP_AUTHENTICATION` |

<!-- END COMPATIBILITY TABLE -->

`any` in the version column means every Keycloak in range supports it and only the feature flag matters. Anything not listed at all works everywhere and is never checked.

The check covers two different failure modes. Most of these would make provisioning **fail** partway through. Two would not: with `TOKEN_EXCHANGE_STANDARD_V2` or `STEP_UP_AUTHENTICATION` switched off, Keycloak still accepts and stores the settings — they are simply inert. That is arguably worse than a failure, because nothing tells you, so the check treats it the same way. The table is generated from the requirement registry in `internal/compat/requirements.go`, and a test fails if the two drift apart — run `go test ./internal/compat -update` to regenerate it.

A config that uses none of these is never blocked, whatever the server version.

```
ERROR Unsupported by this Keycloak server  capability=organization groups
      reason="requires Keycloak 26.6 or newer, server is 26.2.0"
      usedAt=realms[0].organizations[0].groups
```

Two cases are deliberately **not** treated as failures:

- **A version string the tool cannot parse.** Custom and nightly builds report shapes this tool should not be the judge of, so version comparisons are skipped with a warning and feature checks still apply. (`999.0.0-SNAPSHOT` parses fine and counts as newer than any release.)
- **A feature the server does not mention at all.** That means the release predates it, which the version comparison already covers.

### Provider validation

Alongside the table, the check asks the server which authenticators and protocol mapper types it actually offers, and refuses config naming anything else. That covers more than a table can: a provider missing because a feature is switched off, one that does not exist in this release, or simply a typo.

```
ERROR Unsupported by this Keycloak server  capability=auth-cookei
      reason="authenticator \"auth-cookei\" is not available on this server (did you mean \"auth-cookie\"?)"
      usedAt=realms[0].authenticationFlows[0].executions[0].provider
```

Validated:

- `provider` on every authentication flow execution, against the union of the server's authenticator, form, form-action and client-authenticator providers. The union rather than the list matching the flow type, because a false rejection would block a config that works.
- `protocolMapper` on clients and client scopes, against the mapper types the server reports for that protocol — which also catches a SAML mapper on an OIDC client.

Suggestions use edit distance, so a genuine typo gets one and a provider that is merely absent does not: `conditional-level-of-authentication` shares a long prefix with `conditional-credential` without being anything like it, and a wrong suggestion is worse than none.

If the server does not report its providers, nothing is rejected on the strength of a question that could not be asked.

### Permissions

Provider validation needs more rights than provisioning does. An account holding only `create-realm` can read the server info — so version and feature checks work — but is refused the provider lists with 403.

A check the account is **not permitted** to perform is skipped with a warning, never treated as a failure; provisioning that works must keep working:

```
WARN  Could not read the server's provider lists; skipping provider validation
      error="reading authenticator-providers: unexpected status 403: ..."
```

The two halves degrade independently, so losing the provider lists still leaves the version and feature checks in place.

Only 401 and 403 are treated this way. A network failure, a 5xx or an unreadable response means something is actually wrong and fails the run, rather than quietly disabling the check.

Pass `--skip-version-check` to bypass all of this.

## Connection Retry

On startup, the tool retries connecting to Keycloak with exponential backoff (1s initial, 30s cap, 15 retries, 5min total timeout). This handles Docker Compose startup ordering without requiring `wait-for-it` scripts.

## Development

```bash
make build            # Build binary
make test             # Run unit tests
make test-integration # Run integration tests (requires Docker)
make cover            # Run unit tests with a coverage report
make lint             # Run golangci-lint
make vet              # Run go vet
make fmt              # Format with gofumpt and goimports
make mod-tidy         # Tidy go.mod and go.sum
make docker           # Build Docker image
make clean            # Remove build artifacts
```

## Docker Compose Usage

```yaml
services:
  keycloak:
    image: keycloak/keycloak:26.6
    command: ["start-dev"]
    environment:
      KC_HEALTH_ENABLED: "true"
      KEYCLOAK_ADMIN: admin
      KEYCLOAK_ADMIN_PASSWORD: adminpass
    ports: ["8080:8080", "9000:9000"]
    healthcheck:
      test: ["CMD-SHELL", "{ printf 'GET /health/ready HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n' >&3; grep -q '200 OK' <&3; } 3<>/dev/tcp/localhost/9000"]
      interval: 5s
      timeout: 5s
      retries: 20

  keycloak-provisioner:
    image: ghcr.io/datarocks-ag/keycloak-provisioner:latest
    depends_on:
      keycloak: { condition: service_healthy }
    environment:
      KEYCLOAK_USER: admin
      KEYCLOAK_PASSWORD: adminpass
      KEYCLOAK_URL: http://keycloak:8080
      KEYCLOAK_CONFIG_PATH: /config.yaml
      MY_APP_CLIENT_SECRET: my-secret-value
    volumes:
      - ./config.example.yaml:/config.yaml:ro

  app:
    image: your-app
    depends_on:
      keycloak-provisioner:
        condition: service_completed_successfully
```

## Container Image

```bash
docker pull ghcr.io/datarocks-ag/keycloak-provisioner:latest
```
