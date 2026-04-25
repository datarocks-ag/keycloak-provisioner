# keycloak-provisioner

[![CI](https://github.com/datarocks-ag/keycloak-provisioner/actions/workflows/ci.yaml/badge.svg)](https://github.com/datarocks-ag/keycloak-provisioner/actions/workflows/ci.yaml)
![coverage](https://raw.githubusercontent.com/datarocks-ag/keycloak-provisioner/badges/.badges/develop/coverage.svg)

A Go CLI tool that idempotently provisions Keycloak resources from a YAML config file. Designed as a Docker Compose init container.

## Features

- Idempotent provisioning of realms, clients, protocol mappers, realm roles, client roles, users, and service account roles
- Master realm configuration (SSL, users) without full provisioning
- `sslRequired` setting on any realm (`external`, `all`, `none`)
- User management with password setting and realm/client role assignment
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
   5. **Users** — created or updated, passwords set, roles assigned (additive)

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
