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
- Organizations with domains and additive membership (Keycloak 26+), including organization-scoped groups (Keycloak 26.6+)
- Step-up authentication support via `acrLoaMap` on realms and clients (`acr.loa.map`)
- User management with password setting, realm/client role assignment, and group membership
- Service account role mapping for machine-to-machine clients
- YAML config with `${VAR}` environment variable expansion (and `$${VAR}` escape for literals)
- Configurable strategy: `update` (default) or `create` (skip existing)
- `--dry-run` mode that logs all intended changes without applying them
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
   2. **Client scopes** — created or updated (matched by `name`)
      - **Protocol mappers** — created or updated (matched by `name`)
      - **Realm-level assignment** — added if `type` is `default` or `optional` (additive)
   3. **Clients** — created or updated (matched by `clientId`)
      - **Protocol mappers** — created or updated (matched by `name`)
      - **Client roles** — created or updated
      - **Client scope assignment** — configured default/optional scopes attached (additive)
   4. **Realm roles** — created or updated
   5. **Service account roles** — assigned (additive, after roles exist)
   6. **Groups** — created or updated (matched by `name`)
      - **Attributes** — set from config
      - **Realm/client role assignments** — granted if not already mapped (additive)
      - **Subgroups** — created or updated recursively
   7. **Users** — created or updated, passwords set, roles assigned, group memberships added (additive)
   8. **Organizations** — created or updated (matched by `name`), domains set, members added (additive)

Client scopes run before clients so a client can reference a scope defined in
the same config. Groups run after roles so their realm/client role assignments resolve to
roles created earlier in the same run, and before users so group memberships
resolve to groups defined in the same config. Organizations run last so their
members resolve to users created in the same run. Role assignments, group
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
| `password` | string | Permanent password (set on every run via reset-password API). Mutually exclusive with `initialPassword`. |
| `initialPassword` | string | Temporary password — only set when the user is first created. The user must change it on first login. Ignored on subsequent runs if the user already exists. Mutually exclusive with `password`. |
| `enabled` | bool | Whether the user is enabled |
| `email` | string | Email address |
| `firstName` | string | First name |
| `lastName` | string | Last name |
| `emailVerified` | bool | Whether the email is marked as verified |
| `roles` | object | Role assignments (see below) |
| `groups` | list | Group memberships by path (see below) |

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

`type` controls realm-level assignment: `default` adds the scope to every newly created client, `optional` makes it requestable through the `scope` parameter, and `none` (the default) assigns it nowhere. Realm-level assignment is additive — changing a scope's `type` adds the new assignment but does not remove the old one.

Scopes are matched by `name`. Protocol mappers on a scope follow the same rules as protocol mappers on a client.

`defaultClientScopes` and `optionalClientScopes` on a client attach existing scopes to that client. Assignment is additive: configured scopes that are not yet attached are added, and nothing is ever detached. A referenced scope that does not exist is logged as a warning and skipped.

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

Client `attributes` are merged the same way: Keycloak replaces the whole attribute map on a client update, so the provisioner sends the union of the client's current attributes and the configured ones. A client that declares no `attributes`, `acrLoaMap`, or `standardTokenExchangeEnabled` sends no attributes at all.

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
        attributes:
          "default.acr.values": '["silver"]'
```

Under the hood this sets the `acr.loa.map` attribute as a JSON object. Setting the typed field takes precedence over the same key set manually in `attributes`, matching how `standardTokenExchangeEnabled` behaves. Levels must not be negative, and map keys are written in sorted order so repeated runs produce identical values.

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

- **No role mappings.** Keycloak exposes no role-mapping endpoint for organization groups, so `realmRoles` and `clientRoles` are not available on them.
- **Members must already belong to the organization.** Keycloak rejects adding a non-member with `User is not member of the organization`, so list the user under the organization's `members` as well. A group member the organization does not have is logged as a warning and skipped rather than failing the run.

Group names must be unique among siblings but may repeat at different levels, matching realm groups. Subgroups nest to any depth, and both group creation and membership are additive.

`alias` is only sent when the organization is created, since Keycloak treats it as immutable afterwards. Omitting it does not mean the provisioner copies `name` — the field is left out of the request entirely and Keycloak derives its own value.

Linking identity providers to organizations is not supported: the provisioner has no identity provider support to link.

## Connection Retry

On startup, the tool retries connecting to Keycloak with exponential backoff (1s initial, 30s cap, 15 retries, 5min total timeout). This handles Docker Compose startup ordering without requiring `wait-for-it` scripts.

## Development

```bash
make build            # Build binary
make test             # Run unit tests
make test-integration # Run integration tests (requires Docker)
make lint             # Run golangci-lint
make vet              # Run go vet
make docker           # Build Docker image
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
