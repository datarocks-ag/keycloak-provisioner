# CLAUDE.md

## Project Overview

A Go CLI tool that idempotently provisions Keycloak resources (realms, clients, protocol mappers, realm roles, client roles) from a YAML config file via the Admin REST API. Designed as a Docker Compose init container.

## Build & Run

```bash
go build -o keycloak-provisioner ./cmd/keycloak-provisioner
make build          # same via Makefile
make test           # unit tests
make test-integration  # integration tests (requires Docker)
make lint           # golangci-lint
```

## Required Environment Variables

- `KEYCLOAK_USER` — Keycloak admin username
- `KEYCLOAK_PASSWORD` — Keycloak admin password

## Optional Environment Variables

- `KEYCLOAK_URL` (default: `http://localhost:8080`)
- `KEYCLOAK_CONFIG_PATH` (default: `./config.yaml`)
- `LOG_LEVEL` (default: `info`)

## Architecture

```
cmd/keycloak-provisioner/main.go       # CLI entrypoint, env vars, slog setup
internal/
  config/config.go                    # YAML config structs, loader, env var expansion, validation
  config/config_test.go               # Unit tests for config parsing
  client/client.go                    # HTTP client with retry + token management
  provisioner/
    provisioner.go                    # Top-level orchestrator
    realms.go                         # Idempotent realm create/update
    clients.go                        # Idempotent client create/update
    protocol_mappers.go               # Idempotent protocol mapper create/update
    realm_roles.go                    # Idempotent realm role create/update
    client_roles.go                   # Idempotent client role create/update
    provisioner_test.go               # Unit tests with httptest
    integration_test.go               # Integration tests with testcontainers-keycloak
```

## Dependencies

Go 1.25 module using:
- `gopkg.in/yaml.v3` — YAML config parsing
- `github.com/testcontainers/testcontainers-go` — integration tests
- `github.com/stillya/testcontainers-keycloak` — Keycloak testcontainer

No external Keycloak SDK — all HTTP via `net/http` + `encoding/json`.

## Key Design Decisions

- **Order**: Realms → Clients (+ protocol mappers + client roles) → Realm roles
- **Idempotency**: GET → 404? POST : PUT for all resources
- **Auth**: OAuth2 password grant to `admin-cli`, auto-refresh 30s before expiry, retry on 401
- **No SDK**: Raw `net/http` + `map[string]any` bodies (mirrors postgres-provisioner's raw SQL approach)
- **YAML naming**: camelCase fields matching Keycloak realm export format
- **Structured logging**: `log/slog` with JSON output
- **Connection retry**: Exponential backoff (1s–30s, 15 retries, 5min timeout)
