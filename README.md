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
   2. **Clients** — created or updated (matched by `clientId`)
      - **Protocol mappers** — created or updated (matched by `name`)
      - **Client roles** — created or updated
   3. **Realm roles** — created or updated
   4. **Service account roles** — assigned (additive, after roles exist)
   5. **Groups** — created or updated (matched by `name`)
      - **Attributes** — set from config
      - **Realm/client role assignments** — granted if not already mapped (additive)
      - **Subgroups** — created or updated recursively
   6. **Users** — created or updated, passwords set, roles assigned, group memberships added (additive)

Groups run after roles so their realm/client role assignments resolve to
roles created earlier in the same run, and before users so group memberships
resolve to groups defined in the same config. Role assignments and group
memberships are additive: the provisioner grants any configured role or
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

`organizationsEnabled` toggles Keycloak Organizations for a realm:

```yaml
realms:
  - realm: "my-realm"
    organizationsEnabled: true
```

Requires Keycloak 26+, where the `organization` feature is enabled by default. Omitting the field leaves the realm's current setting untouched.

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
    image: keycloak/keycloak:26.0
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
