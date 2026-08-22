# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `id` on a user, fixing its UUID so it is the same in every
  environment and can be referenced without a lookup. It applies only
  when the user is created: Keycloak does not allow an id to change
  afterwards, so a username that already exists under a different id
  fails the run and names both rather than provisioning against a
  different identity.

  Such a user is created through Keycloak's partial import rather than
  the usual create call. The create-user endpoint accepts an `id` in the
  representation and silently discards it, generating its own — verified
  against 26.6. Import honours it, and is configured to skip an existing
  username so re-runs stay idempotent. Everything after creation —
  password, roles, group memberships — is unchanged.

### Changed

- `realmRoles` and `clientRoles` on an organization group are now rejected at
  config load with an explanation, instead of being rejected as unknown fields.
  Keycloak 26.7 accepts the corresponding role-mapping call and reads the role
  back, but the mapping never reaches a member's effective roles or any token
  claim. An unknown-field error reads as "not implemented yet", which invites
  wiring the endpoint up by hand; naming the trap does not.

### Fixed

- `${VAR}` references are now expanded in the **keys** of `roles.clients` under a
  user and under a client's `serviceAccountRoles`, and in the keys of a group's
  `attributes`. Only the values were expanded, so a templated clientId reached
  Keycloak literally and provisioning failed with
  `client "${VAR}" not found in realm`. Group `clientRoles` keys were already
  expanded, which is why the gap went unnoticed.
- When two keys of a multivalued map expand to the same key, their value lists
  are merged instead of one silently overwriting the other, and the merge is now
  order-independent. Organization and organization-group attributes previously
  dropped one side, nondeterministically.
- Corrected the 1.7.0 note claiming Keycloak exposes no role-mapping endpoint
  for organization groups. It does not on 26.6, but 26.7 exposes one that is
  accepted and inert — which is worse, and now documented as such.
- `fullScopeAllowed` on a client. Keycloak defaults it to `true`, which puts
  every role the subject holds into the client's tokens regardless of its
  assigned scopes; setting it to `false` limits them to roles reachable through
  `defaultClientScopes` and `optionalClientScopes`. It is the main control over
  how broad an exchanged token can be, so it pairs with
  `standardTokenExchangeEnabled`. Omitting it leaves the flag unmanaged.

## [1.7.0] — 2026-08-22

Extends the config schema to cover realm attributes, client scopes,
organizations and authentication flows, and adds a pre-flight check that
refuses a config the Keycloak server cannot apply.

No configuration written for 1.6.0 needs changing. Three things do behave
differently on the first run after upgrading:

- **Client scopes listed on a client are now actually attached.** Keycloak
  ignores the inline `defaultClientScopes` and `optionalClientScopes` fields
  when a client is *updated*, so a scope added to an existing client's config
  was silently dropped on every run. It is applied now, which means the first
  run after upgrading will attach scopes that were configured but never took
  effect.
- **Runs can now be refused before they start.** The compatibility check
  rejects a config the server cannot apply, including two cases that
  previously exited 0 while doing nothing useful: `standardTokenExchangeEnabled`
  against a server with `TOKEN_EXCHANGE_STANDARD_V2` disabled, and `acrLoaMap`
  with `STEP_UP_AUTHENTICATION` disabled. Pass `--skip-version-check` to
  bypass it.
- **An empty attribute name is now a config error.** Previously it was passed
  through to Keycloak, where it meant nothing.

### Added

- Realm attributes: an `attributes` map on any realm, with `${VAR}`
  expansion in both keys and values. Attributes are merged over the
  realm's current attributes rather than replacing them, so keys managed
  outside the config are preserved. Omitting the block leaves realm
  attributes untouched.
- `acrLoaMap` on realms and clients, mapping ACR values to Levels of
  Authentication for step-up authentication. Marshalled into the
  `acr.loa.map` attribute with sorted keys for stable output; the typed
  field wins over the same key set manually in `attributes`.
- `organizationsEnabled` on realms, toggling Keycloak Organizations
  (Keycloak 26+).
- Client scopes as a first-class resource: a `clientScopes` list on any
  realm, with `description`, `protocol`, `attributes`, and their own
  `protocolMappers`. Scopes are matched by name and provisioned before
  clients, so a client can reference a scope defined in the same config.
  The `type` field (`default`, `optional`, or `none`) assigns the scope
  at realm level; assignment is additive and never removed.
- Organizations: an `organizations` list on any realm, with `alias`,
  `description`, `redirectUrl`, multivalued `attributes`, `domains`
  (each with an optional `verified` flag), and `members` given as
  usernames. Organizations are provisioned last so members resolve to
  users created in the same run. Membership is additive; an unresolvable
  username is logged as a warning and skipped. Declaring organizations
  without `organizationsEnabled: true` is rejected at config load time.
  Requires Keycloak 26+ for organizations themselves, and Keycloak 26.6+
  for the organization groups below, which is the version the
  integration tests and compose stack now target. Linking identity
  providers to organizations is not supported.
- Organization groups: a `groups` tree on any organization, with
  multivalued `attributes`, additive `members`, and nested `subGroups`.
  These are organization-scoped and separate from realm groups — they do
  not appear under the realm's groups, and Keycloak refuses to manage
  them through the normal group API. They support no role mappings — see
  the README for why the endpoint that appears to offer them does not —
  and a member must already belong to the organization or it is logged
  as a warning and skipped.
- Authentication flows: an `authenticationFlows` list on any realm, with
  nested subflows, per-execution `requirement`, authenticator `config`,
  and `copyFrom` to seed a flow from an existing one. Executions are
  created in the order declared. Flows are create-only — a flow whose
  alias already exists is left untouched whatever the strategy, and the
  skip is logged at INFO. Declaring a built-in Keycloak flow is rejected
  with an error pointing at `copyFrom`.
- `authenticationBindings` on realms, pointing `browserFlow`,
  `directGrantFlow` and the other realm flow bindings at a flow alias.
  Applied as a second realm update, after the flows exist.
- `authenticationFlowBindingOverrides` on clients, given as flow aliases
  and resolved to the flow IDs Keycloak stores on the client. An alias
  that does not resolve is an error rather than a silent skip.
- Pre-flight Keycloak compatibility check. Before anything is
  provisioned, the tool reads the server version and feature list and
  refuses a config the server cannot apply, so a run either does the
  whole job or changes nothing instead of failing partway with a raw
  Keycloak error. The error names each unsupported capability, the
  version it needs, and the config paths that use it. The check also
  covers server feature flags, not just versions: a capability can be
  supported by the release and still be switched off on the server,
  which a version comparison alone cannot catch. Two cases are
  deliberately not failures — a version string the tool cannot parse
  (custom and nightly builds), where comparisons are skipped with a
  warning and feature checks still apply, and a feature the server does
  not report at all, which means the release predates it and is already
  covered by the version comparison.
- `--skip-version-check` to bypass the compatibility check.
- A published compatibility table in the README, generated from the
  requirement registry in `internal/compat/requirements.go`. A test
  fails if the two drift apart; `go test ./internal/compat -update`
  regenerates it.
- The compatibility check also covers two settings that Keycloak accepts
  and stores but silently ignores when the governing feature is off,
  which is worse than a failure because nothing reports it:
  `standardTokenExchangeEnabled` needs `TOKEN_EXCHANGE_STANDARD_V2`
  (distinct from the legacy `TOKEN_EXCHANGE` preview, which is off by
  default and unrelated), and `acrLoaMap` needs `STEP_UP_AUTHENTICATION`.
  Step-up has no version floor — it predates the supported range — so a
  requirement can now gate on a feature alone.
- Provider validation. The check now also asks the server which
  authenticators and protocol mapper types it offers and refuses config
  naming anything else, which catches more than a version table can: a
  provider missing because a feature is disabled, one absent from this
  release, or a typo. Authenticators are checked against the union of the
  server's four provider lists; protocol mappers against the types
  reported for their protocol, which also catches a SAML mapper on an
  OIDC client. Errors name the config path and, for a likely typo,
  suggest the closest real name by edit distance — a provider that is
  merely absent gets no suggestion, since a wrong one is worse than none.
  Nothing is rejected when the server does not report its providers.

### Fixed

- The compatibility check no longer fails a run it is not permitted to
  perform. Provider validation needs more rights than provisioning does:
  an account holding only `create-realm` can read the server info, so
  version and feature checks work, but is refused the provider lists with
  403 — and aborting there broke setups that provision perfectly well.
  A check the account is not permitted to perform is now skipped with a
  warning, and the two degrade independently. Only 401 and 403 are
  treated this way; a network failure, a 5xx or an unreadable response
  still fails the run rather than quietly disabling the check.

- A client scope added to an existing client's `defaultClientScopes` or
  `optionalClientScopes` is now actually attached. Keycloak honours those
  inline fields only when a client is created, so the assignment was
  silently dropped on every subsequent run. The provisioner now attaches
  scopes through the dedicated assignment endpoints, additively — a
  referenced scope that does not exist is logged as a warning and
  skipped.
- Client attributes are no longer replaced on update. Keycloak replaces
  the whole attribute map when a client is updated, so a run would drop
  any attribute not declared in the config — including attributes set
  out-of-band and those Keycloak defaults itself. The provisioner now
  merges the configured attributes over the client's current ones.
  Clients that declare no `attributes`, `acrLoaMap`, or
  `standardTokenExchangeEnabled` still send no attributes at all.

### Changed

- Integration tests share one Keycloak container instead of starting a
  fresh one per test, cutting the suite from roughly five minutes to
  about thirty seconds. The per-test containers had pushed the package
  past the ten-minute Go test timeout in CI and strained the Docker
  daemon enough to cause spurious readiness failures. Each test already
  provisions its own uniquely named realm; the master realm is the one
  piece of shared state, so the test that changes it now restores it.
  The container starts lazily, so a unit-test-only run under the
  integration build tag does not pay for one. Verified order-independent
  with `-shuffle=on`.

- Empty attribute names are now rejected at config load time for both
  realm and client `attributes` maps. Previously an empty key was passed
  through to Keycloak.

## [1.6.0] — 2026-07-23

### Added

- Group membership for users: a `groups` list of group paths (e.g.
  `/engineering/backend`; leading slash optional) on any user, including
  master realm users. Memberships are additive — users are never removed
  from groups. A referenced group that does not exist is logged as a
  warning and skipped without aborting the run.
- Dry-run reporting for group membership additions.
- Unit, validation, and integration coverage for group memberships;
  README and config example documentation.

### Changed

- Groups are now provisioned before users (previously after) so user
  group memberships resolve to groups defined in the same config.

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

[Unreleased]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.7.0...HEAD
[1.7.0]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.6.0...v1.7.0
[1.6.0]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.5.0...v1.6.0
[1.5.0]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.4.0...v1.5.0
[1.4.0]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.3.0...v1.4.0
[1.3.0]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.2.1...v1.3.0
[1.2.1]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.2.0...v1.2.1
[1.2.0]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/datarocks-ag/keycloak-provisioner/releases/tag/v1.0.0
