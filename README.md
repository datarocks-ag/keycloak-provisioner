# keycloak-provisioner

[![CI](https://github.com/datarocks-ag/keycloak-provisioner/actions/workflows/ci.yaml/badge.svg)](https://github.com/datarocks-ag/keycloak-provisioner/actions/workflows/ci.yaml)
![coverage](https://raw.githubusercontent.com/datarocks-ag/keycloak-provisioner/badges/.badges/develop/coverage.svg)

A Go CLI tool that idempotently provisions Keycloak resources from a YAML config file. Designed as a Docker Compose init container.

## Features

- Idempotent provisioning of realms, clients, protocol mappers, realm roles, and client roles
- YAML config with `${VAR}` environment variable expansion
- Configurable strategy: `update` (default) or `create` (skip existing)
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

String values support `${VAR}` syntax. If the variable is set in the environment, it is replaced; if unset, the placeholder is preserved as-is.

```yaml
secret: "${MY_APP_CLIENT_SECRET}"    # replaced with env var value at load time
```

## Provisioning Order

For each realm:

1. **Realm** — created or updated
2. **Clients** — created or updated (matched by `clientId`)
   - **Protocol mappers** — created or updated (matched by `name`)
   - **Client roles** — created or updated
3. **Realm roles** — created or updated

## Config Example

See [config.example.yaml](config.example.yaml) for a full example. The YAML field names mirror Keycloak's realm JSON export format (camelCase), so you can copy-paste from an exported realm.

```yaml
realms:
  - realm: "my-realm"
    displayName: "My Realm"
    enabled: true
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
    roles:
      - name: "app-admin"
        description: "Application administrator"
```

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
