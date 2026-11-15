# keycloak-provisioner

[![CI](https://github.com/datarocks-ag/keycloak-provisioner/actions/workflows/ci.yaml/badge.svg)](https://github.com/datarocks-ag/keycloak-provisioner/actions/workflows/ci.yaml)
![coverage](https://raw.githubusercontent.com/datarocks-ag/keycloak-provisioner/badges/.badges/develop/coverage.svg)

Declarative, idempotent provisioning for Keycloak. Describe the realms, clients, roles, users,
groups, client scopes, authentication flows, organizations and identity providers your services
need in one YAML file; the provisioner creates what is missing, updates what it manages, and
exits.

It is built to run **before your application starts**: as a Docker Compose service your app
`depends_on`, or as a Kubernetes init container or Job.

## Features

- Idempotent provisioning of realms, clients, protocol mappers, realm and client roles, users,
  groups with nested subgroups, and service account roles
- Master realm settings (`sslRequired`, users) without provisioning the master realm itself
- Realm and client attributes merged over Keycloak's current values, so keys you don't manage
  are kept
- Client scopes with their own protocol mappers, realm-level default/optional assignment, and
  role scope mappings for `fullScopeAllowed: false`
- Standard token exchange, and fine-grained admin permissions for impersonation
- Authentication flows with nested subflows and execution config, realm and client flow
  bindings, and step-up authentication via `acrLoaMap` and `defaultAcrValues`
- Organizations with domains, members and organization groups, linked to identity providers
- Identity providers with mappers, merged over the server's representation so secrets and
  unmanaged config keys survive
- Users with passwords, role assignments, group membership, required actions and seeded
  time-based one-time password (TOTP) credentials
- `${VAR}` environment variable expansion, with `$${VAR}` for a literal
- `strategy: update` (default) or `create`, which leaves the settings of existing resources
  alone
- `--dry-run`, which logs every change instead of making it
- A pre-flight check that refuses a config the Keycloak server is too old for, or whose server
  feature or provider is missing, before anything is written
- Retries while Keycloak starts, JSON logs, and a single static binary in a `scratch` image

## Quick start

```bash
docker compose up
```

This starts Keycloak 26.6 and runs the provisioner with [config.example.yaml](config.example.yaml).
[Getting started](docs/getting-started.md) walks you through your own first config.

## Documentation

Read in this order if you're new; jump straight to a how-to if you know what you need.

| | |
|---|---|
| **[Getting started](docs/getting-started.md)** | A guided tour: from an empty Keycloak to a realm with a client, a role, a group and a user, checked with a real token. |
| **[How it works](docs/concepts.md)** | The model behind a run: what is created, updated or never touched, the order of steps, `strategy`, dry-run, the pre-flight check. |
| **How-to guides** | Task-focused stories: |
| &nbsp;&nbsp;[Run the provisioner as an init container](docs/howto/run-as-init-container.md) | Docker Compose and Kubernetes; start the application only after a successful run. |
| &nbsp;&nbsp;[Preview changes with a dry run](docs/howto/preview-changes-with-dry-run.md) | Read `DRY-RUN: would …` lines before changing a live Keycloak. |
| &nbsp;&nbsp;[Keep secrets out of the config file](docs/howto/keep-secrets-out-of-the-config.md) | `${VAR}` for client secrets and passwords, and the unset-variable trap. |
| &nbsp;&nbsp;[Connect to Keycloak over HTTPS](docs/howto/connect-over-https.md) | An `https` URL and a private certificate authority. |
| &nbsp;&nbsp;[Provision with a dedicated admin account](docs/howto/use-a-dedicated-admin-account.md) | A master-realm account instead of the bootstrap admin, and what a restricted one loses. |
| &nbsp;&nbsp;[Provision users with passwords, roles and groups](docs/howto/provision-users.md) | `password` or `initialPassword`, role assignments, group paths, fixed ids. |
| &nbsp;&nbsp;[Require or seed TOTP for users](docs/howto/require-totp-for-users.md) | Make users enrol a second factor, or seed a known one for test automation. |
| &nbsp;&nbsp;[Grant roles through groups](docs/howto/grant-roles-through-groups.md) | Assign roles to a group once; members and subgroup members inherit them. |
| &nbsp;&nbsp;[Set up a machine-to-machine client](docs/howto/set-up-a-service-account-client.md) | A confidential client with a service account and the roles it needs. |
| &nbsp;&nbsp;[Add claims and audiences with client scopes](docs/howto/add-claims-with-client-scopes.md) | Client scopes with protocol mappers, assigned as default or optional. |
| &nbsp;&nbsp;[Let a client exchange tokens for another audience](docs/howto/set-up-token-exchange.md) | Standard token exchange, narrowed with `fullScopeAllowed: false` and scope mappings. |
| &nbsp;&nbsp;[Allow a client to impersonate users](docs/howto/allow-impersonation.md) | Fine-grained admin permissions (v1) and `requested_subject` token exchange. |
| &nbsp;&nbsp;[Add step-up authentication to a client](docs/howto/add-step-up-authentication.md) | A level-of-authentication browser flow, `acrLoaMap` and `defaultAcrValues`. |
| &nbsp;&nbsp;[Broker logins through an external identity provider](docs/howto/connect-an-identity-provider.md) | An OpenID Connect identity provider with mappers and a secret from the environment. |
| &nbsp;&nbsp;[Onboard an organization](docs/howto/onboard-an-organization.md) | Domains, members, organization groups and a linked identity provider. |
| **[Deployment](docs/deployment.md)** | Container image, Docker Compose, Kubernetes, environment variables, CLI flags, exit codes, Keycloak compatibility, required admin permissions. |
| **[Configuration reference](docs/configuration.md)** | Every field of the YAML file, with its update behaviour and validation. |
| **[FAQ](docs/faq.md)** | Questions and error messages people run into after their first runs. |
| **[Changelog](CHANGELOG.md)** | What changed in each release. |

A complete, commented config lives in [config.example.yaml](config.example.yaml), and
[docker-compose.yaml](docker-compose.yaml) runs it against a local Keycloak.

## Development

```bash
make build            # Build binary
make test             # Run unit tests (with -race)
make test-integration # Run unit and integration tests (requires Docker)
make cover            # Run unit tests with a coverage report
make lint             # Run golangci-lint (includes gofumpt and goimports)
make vet              # Run go vet
make fmt              # Run go fmt
make mod-tidy         # Tidy go.mod and go.sum
make docker           # Build a local Docker image (reports version "dev")
make clean            # Remove build artifacts
```

The Keycloak compatibility table in [docs/deployment.md](docs/deployment.md) is generated from
`internal/compat/requirements.go`; regenerate it with `go test ./internal/compat -update`.
