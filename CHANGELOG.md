# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

_No unreleased changes._

## [1.5.0] — 2026-07-08

### Added

- Group provisioning as a realm-scoped resource: `name`, multivalued
  `attributes`, nested `subGroups`, and realm/client role assignments.
  Groups are reconciled idempotently under the existing create/update
  strategy and run after roles so their assignments resolve to roles
  created earlier in the same run. Role assignments are additive —
  configured roles not yet mapped are granted, existing mappings are
  never removed.
- Dry-run reporting for group creation, updates, and role mappings.
- Unit, validation, and integration coverage for groups; README and
  config example documentation.

## [1.4.0] — 2026-07-06

### Added

- `standardTokenExchangeEnabled` toggle on clients, mapping to
  Keycloak's `standard.token.exchange.enabled` attribute (Standard Token
  Exchange, RFC 8693, supported since Keycloak 26.2). The typed field is
  merged into the client attributes without mutating the config's own map
  and takes precedence over a manually-set raw attribute. Validation
  rejects the toggle on public and bearer-only clients, which cannot
  authenticate at the token endpoint.
- Unit, validation, and integration coverage for token exchange; README
  and config example documentation.

### Changed

- Integration-test Keycloak image bumped to 26.2 (required for Standard
  Token Exchange).
- GoReleaser now runs only on tag pushes and replaces existing artifacts.
- Bumped `github.com/stillya/testcontainers-keycloak`, and CI
  `actions/checkout` v6 → v7 and `codecov/codecov-action` v6 → v7.

## [1.3.0] — 2026-04-25

### Added

- `--dry-run` flag that logs every intended Keycloak mutation as
  `DRY-RUN: would …` without applying it. Pass-through reads still observe
  current state, and synthetic IDs allow the full provisioning sequence
  (clients in a new realm, role assignments to new users, service-account
  role mappings on new clients) to be reported in one run.
- `--version` flag prints the build version and exits 0.
- `--config <path>` flag overrides the `KEYCLOAK_CONFIG_PATH` environment
  variable.
- `$${VAR}` escape syntax in YAML config produces a literal `${VAR}` in
  the rendered output.
- Strict YAML loading: unknown fields are rejected at load time, and files
  containing more than one YAML document are rejected.
- New `provisioner.KeycloakAPI` interface (port) makes the orchestrator
  testable against any adapter and unlocks dry-run.
- Integration tests for master-realm user provisioning, dry-run
  non-mutation, and missing-role error paths.
- New `internal/cli` package extracts flag/env/logging handling from
  `main` for unit-testability.

### Changed

- `Provisioner.New` now accepts a `KeycloakAPI` interface instead of
  `*client.Client` (any compatible adapter works; the production client
  satisfies it).
- Master-realm users now honour the global `strategy` setting (previously
  hardcoded to `update`).
- `client.Connect` rejects URLs that lack a scheme/host or use a scheme
  other than `http`/`https`. Empty `access_token` responses now surface
  as an authentication error.
- Internal token handling consolidated under a single locked accessor
  to remove a benign read/refresh race.
- Bumped Go module dependencies and tool dependencies; no API changes.
- Bumped GitHub Actions to current majors (`codecov-action@v6`,
  `docker/build-push-action@v7`, `docker/login-action@v4`,
  `docker/metadata-action@v6`, `docker/setup-buildx-action@v4`).
- `Dockerfile` builder image bumped to `golang:1.26-alpine`.
- `.golangci.yml` enables `gosec`, `misspell`, `revive`, `unconvert`,
  plus `gofumpt` and `goimports` formatters with module-aware paths.

### Fixed

- Environment variable expansion now also runs on client `attributes`
  keys (previously only values were expanded).

## [1.2.1] — 2026-03-06

### Added

- `initialPassword` field on users — sets a temporary password that the
  user must change on first login. Only applied when the user is first
  created; ignored on subsequent runs. Mutually exclusive with
  `password`.

### Changed

- Bumped `go.opentelemetry.io/otel/sdk` from 1.38.0 to 1.40.0.

## [1.2.0] — 2026-03-01

### Fixed

- `sslRequired` value handling is now case-insensitive and normalises to
  the lowercase form Keycloak expects in its REST API.
- Integration test authentication flow against newer Keycloak images.

### Changed

- Bumped `actions/upload-artifact` from v6 to v7.

## [1.1.0] — 2026-02-25

### Added

- `sslRequired` setting on any realm (`external`, `all`, `none`).
- `masterRealm` configuration block — update-only, never created. Allows
  managing master-realm `sslRequired` and master-realm users.
- User provisioning per realm, including realm/client role assignment
  and password setting.
- Service-account role mapping for clients with
  `serviceAccountsEnabled: true`. Validation enforces that
  `serviceAccountsEnabled` is `true` whenever `serviceAccountRoles` is
  set.
- Error-path and edge-case test coverage for users, roles, master realm,
  and service accounts.
- README documentation for the new sections.

### Changed

- Bumped `codecov/codecov-action` v4 → v5,
  `goreleaser/goreleaser-action` v6 → v7, and
  `actions/upload-artifact` v4 → v6.

## [1.0.0] — 2026-02-20

Initial release.

### Added

- Idempotent provisioning of realms, clients, protocol mappers, realm
  roles, and client roles from a YAML config file.
- `${VAR}` environment-variable expansion in YAML string values.
- Configurable `strategy` (global and per-realm) — `update` (default)
  reconciles existing resources; `create` only fills in missing ones.
- Exponential-backoff retry on Keycloak connectivity for use as a
  Docker Compose init container without `wait-for-it` scripts.
- Structured JSON logging via `log/slog`.
- Multi-stage Dockerfile producing a `scratch`-based image.
- GoReleaser build for linux/darwin × amd64/arm64.
- GitHub Actions CI/CD: lint, unit tests, integration tests
  (testcontainers-based Keycloak), Trivy scan, GHCR publish, GoReleaser.
- LICENSE.

[Unreleased]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.5.0...HEAD
[1.5.0]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.4.0...v1.5.0
[1.4.0]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.3.0...v1.4.0
[1.3.0]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.2.1...v1.3.0
[1.2.1]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.2.0...v1.2.1
[1.2.0]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/datarocks-ag/keycloak-provisioner/releases/tag/v1.0.0
