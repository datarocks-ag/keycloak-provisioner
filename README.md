# keycloak-provisioner

A Go CLI tool that idempotently provisions Keycloak resources from a YAML config file. Designed as a Docker Compose init container.

## Features

- Idempotent provisioning of realms, clients, protocol mappers, realm roles, and client roles
- YAML config with `${VAR}` environment variable expansion
- Exponential backoff retry for Keycloak connectivity
- Structured JSON logging via `log/slog`
- No external Keycloak SDK — pure `net/http`

## Quick Start

```bash
docker compose up
```

This starts Keycloak and runs the provisioner with the example config.

## Environment Variables

| Variable | Required | Default |
|---|---|---|
| `KEYCLOAK_USER` | yes | — |
| `KEYCLOAK_PASSWORD` | yes | — |
| `KEYCLOAK_URL` | no | `http://localhost:8080` |
| `KEYCLOAK_CONFIG_PATH` | no | `./config.yaml` |
| `LOG_LEVEL` | no | `info` |

## Config Example

See [config.example.yaml](config.example.yaml) for a full example. The YAML field names mirror Keycloak's realm JSON export format (camelCase).

```yaml
realms:
  - realm: "my-realm"
    enabled: true
    clients:
      - clientId: "my-app"
        secret: "${MY_APP_CLIENT_SECRET}"
        enabled: true
        protocol: "openid-connect"
    roles:
      - name: "app-admin"
```

## Container Image

```bash
docker pull ghcr.io/datarocks-ag/keycloak-provisioner:latest
```
