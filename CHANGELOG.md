# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

_No unreleased changes._

## [1.10.0] — 2026-08-22

Makes `fullScopeAllowed: false` usable. It shipped in 1.8.0 as a switch with
nothing behind it: it limits a client's tokens to the roles in its scope, and
nothing in the config could put a role there. Measured against 26.6 the result
is not a narrower token but a client that reaches nothing — Keycloak derives the
audiences a client may request from the roles in its scope, so every token
exchange fails with `Requested audience not available`. `scopeMappings` is the
missing half.

Two things to know before upgrading:

- **A client declaring `defaultClientScopes` was losing Keycloak's own default
  scopes**, `roles` among them, so its tokens carried no `resource_access` at
  all. That is fixed for clients created from now on, but the reconciler only
  adds: a client provisioned by an earlier version keeps its narrowed set. Check
  any client that declares either scope list, and either name the missing
  built-ins (`roles`, `basic`, `profile`, `email`, `web-origins`, `acr`) in
  `defaultClientScopes` or recreate the client.
- **`fullScopeAllowed: false` still needs `scopeMappings` to be useful.** The
  flag alone leaves a client with scope on nothing; naming the roles it should
  reach is what makes an audience reachable. Nothing about an existing config
  changes on its own — both fields are opt-in.

### Added

- `scopeMappings` on a client and on a client scope: the roles that are in that
  client's — or that scope's — scope. It is the other half of
  `fullScopeAllowed: false`, which until now could be declared but not used.
  Setting the flag alone narrows a client to nothing, and measured against 26.6
  that is total: Keycloak derives the audiences a client may request from the
  roles in its scope, so an exchange fails with `Requested audience not
  available` for every downstream client. Naming a client's roles here, and no
  others, is what scopes an exchange to exactly the audiences it should reach.

  A role reaches the token only if the subject holds it *and* it is in scope, so
  this narrows and never grants. Assignment is additive, like every other role
  assignment: a mapping present on the server but absent from the config is left
  alone, so this cannot take back a scope widened out of band. A role named here
  that does not exist fails the run.

  Declared on a client scope, the roles apply to every client the scope is
  attached to; Keycloak treats a role reached that way exactly like one mapped
  on the client itself.

### Fixed

- A client declaring `defaultClientScopes` or `optionalClientScopes` no longer
  loses Keycloak's own default scopes.

  Keycloak reads either list in a client representation as the client's
  *complete* scope list, so a client naming one scope was created with only that
  one — without `roles`, the scope that emits `resource_access`, and without
  `basic`, `profile`, `email`, `web-origins` and `acr`. Its tokens then carried
  no roles at all, whatever else was configured, which reads as a role or scope
  mapping problem and is neither.

  The provisioner now leaves both lists out of the client body and attaches them
  through the same additive assignment the update path already used, matching
  what the README documented all along: scopes are added, and nothing is ever
  detached. A client provisioned before this fix keeps its narrowed set — the
  reconciler adds, so re-running restores nothing. Attach the missing built-ins
  by naming them in `defaultClientScopes`, or recreate the client.

## [1.9.0] — 2026-08-22

Closes the gaps that stopped a realm rebuilding from config alone: enrolling a
TOTP credential and setting a user's required actions both needed an admin call
by hand, so wiping a realm left any MFA setup unusable until someone re-ran one.

Nothing written for 1.8.1 needs changing — every addition is a new optional
field. Two things are worth knowing before using them:

- **A seeded TOTP secret is not base32.** Keycloak uses the characters of
  `secret` directly as the HMAC key rather than base32-decoding them, so an
  authenticator app must be given `base32(secret)` — which is what Keycloak's
  own QR code shows once the credential exists. Computing codes from the decoded
  bytes produces codes Keycloak rejects, and the failure looks like bad seeding
  rather than a bad client.
- **`defaultAcrValues` supersedes writing `default.acr.values` by hand**, and
  the README example for doing it by hand was wrong: Keycloak stores the values
  as one `##`-separated string and rejects a JSON array. If you copied that
  example, switch to the typed field.

Neither changes the behaviour of an existing config, since both fields are new.

### Added

- `requiredActions` on a user, listing what Keycloak makes them complete at
  their next login. Declaring the field replaces the user's current list;
  omitting it leaves them alone, so an empty list is how to clear them. Master
  realm users get this too, since they reuse the same type.
- `credentials` on a user, seeding a TOTP secret so a realm rebuilds from config
  without an out-of-band admin call. `otp` is the only supported type:
  passwords already have their own fields, WebAuthn is device-bound, and
  recovery codes are generated for one-time display.

  **The secret is not base32.** Keycloak uses the characters of `secret`
  directly as the HMAC key rather than base32-decoding them, so an authenticator
  app must be given `base32(secret)` — which is what Keycloak's own QR code
  shows once the credential exists.

  Seeding is additive and never rotates. Keycloak's user update *appends* the
  credentials it is given rather than reconciling them, so a credential of the
  same type and label is left alone; otherwise every run would add another.
- Server-driven validation of required action names, alongside the existing
  provider checks. Keycloak accepts an unknown action with `204` and then drops
  it silently, so a typo would look applied while the user is never asked to do
  anything.
- `defaultAcrValues` on a client, the ACR values Keycloak applies when a request
  asks for none. It was already reachable through the generic `attributes` map,
  but the encoding is not what the field's shape suggests: Keycloak stores the
  values in `default.acr.values` as one `##`-separated string, and rejects both
  a JSON array and bare level numbers. Worse, the rejection quotes the
  ACR-to-LoA map rather than the encoding, so it reads as the wrong problem.

  Values are also checked against the effective `acrLoaMap` at config load when
  one is declared, naming the offending value and listing what is available —
  Keycloak's own refusal names neither. The check is skipped when no map is
  declared anywhere, since the server may already have one.

  The README example previously wrote that attribute as a JSON array, which
  Keycloak rejects. It now uses the typed field.

### Changed

- Dry-run reports a credential-only user update as
  `would seed user credentials`, naming each credential, rather than the generic
  `would update user` it would otherwise share with every other user change.

## [1.8.1] — 2026-08-22

Fixes organization updates, which have never worked since organizations shipped
in 1.7.0.

Any second run against a realm whose organizations already exist aborted on the
first one:

```
ERROR Provisioning failed error="provisioning realm \"sandbox\": ensuring
organization \"Acme\": unexpected status 400: {\"errorMessage\":\"Cannot change
the alias\"}"
```

Creation was unaffected, and `strategy: create` skips the update entirely, which
is why it did not show up for everyone.

**No data was lost.** Keycloak rejected the whole request, so nothing was
applied — including the fields an accepted sparse update would have cleared. The
practical effect was narrower and quieter than the error suggests: changes to an
existing organization were never applied at all, and the run failed instead of
reporting that.

### Fixed

- Organization updates merge over the server's current representation instead of
  sending a sparse body.

  `400 Cannot change the alias` inverts the cause. Keycloak requires `alias` to
  be **present** on an update and reads its absence as an attempt to set it to
  null; the provisioner left it out precisely because the alias is immutable.
  Nothing tried to change it — nothing supplied it.

  Two further defects sat behind that one, unreachable while the run could not
  get past it. Measured against 26.6, omitting a field from an organization
  update does three different things: `domains` and `redirectUrl` are **silently
  cleared**, while `attributes` survive being omitted but are **replaced whole**
  when supplied. Merging is the one shape that satisfies all four, and it is
  what identity provider updates already do. A domain, redirect URL or attribute
  set outside the config now survives a run that does not mention it.

  Declaring an alias that differs from the stored one is rejected before the
  request, naming both values. Keycloak's refusal names neither.

## [1.8.0] — 2026-08-22

Adds identity provider support, predictable user ids and `fullScopeAllowed`,
and fixes two ways provisioning could fail on its second run.

No configuration written for 1.7.0 needs changing. Three things behave
differently after upgrading:

- **Some log messages changed.** Anything that greps or parses the
  provisioner's output needs updating — the entries under "Changed" name every
  old and new string. Nothing was lost: each distinction that left a message
  moved into a structured attribute, which is easier to match on than prose.
- **Runs that used to fail on their second pass now succeed.** An organization
  with more than 10 members, or an organization group with more than 10
  subgroups, hit a Keycloak listing default that hid what already existed; the
  provisioner then tried to create it again and Keycloak refused with a 409.
  Both listings now ask for everything.
- **`${VAR}` in a role map key is expanded.** Previously only the values were,
  so a templated clientId under a user's `roles.clients` or a client's
  `serviceAccountRoles.clients` reached Keycloak literally and the run failed
  with `client "${VAR}" not found`. Such a config now resolves and provisions.
  A key meant to contain a literal `${...}` needs the `$${VAR}` escape.

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
- Identity providers: an `identityProviders` list on any realm, with the usual
  flags, broker login flow aliases, provider `config`, and `mappers`. Secrets
  come through `${VAR}` in `config`.
- `identityProviders` on an organization, linking providers by alias. Linking is
  additive, and a provider belongs to at most one organization — two
  organizations claiming the same alias is rejected at config load rather than
  failing partway through a run.
- Server-driven validation of `providerId` and `identityProviderMapper`, taken
  from the server info the compatibility check already reads, so it costs no
  extra request. Keycloak accepts an unknown mapper type with 201 and then never
  applies it, so this turns a silent misconfiguration into a pre-flight error.
  The provider list reflects feature state: `instagram` is absent unless
  `INSTAGRAM_BROKER` is enabled.
- `fullScopeAllowed` on a client. Keycloak defaults it to `true`, which puts
  every role the subject holds into the client's tokens regardless of its
  assigned scopes; setting it to `false` limits them to roles reachable through
  `defaultClientScopes` and `optionalClientScopes`. It is the main control over
  how broad an exchanged token can be, so it pairs with
  `standardTokenExchangeEnabled`. Omitting it leaves the flag unmanaged.

### Changed

- Eight dry-run log messages were removed when the port methods were
  consolidated. Each case now shares the message of its counterpart, with the
  distinction moved out of the message text and into a structured attribute, so
  anything matching on the old strings needs updating. No information was lost.

  | was | now | discriminator |
  | --- | --- | --- |
  | `would assign realm roles to group` | `would assign realm roles` | `subject=groups` |
  | `would assign client roles to group` | `would assign client roles` | `subject=groups` |
  | `would assign default client scope` | `would assign client scope` | `type=default` |
  | `would assign optional client scope` | `would assign client scope` | `type=optional` |
  | `would assign client scope to realm defaults` | `would assign client scope to realm` | `type=default` |
  | `would assign client scope to realm optionals` | `would assign client scope to realm` | `type=optional` |
  | `would create client scope protocol mapper` | `would create protocol mapper` | `container=client-scopes` |
  | `would update client scope protocol mapper` | `would update protocol mapper` | `container=client-scopes` |

  The port collapse itself left the non-dry-run messages alone; the reconciler
  changes below came later.
- The two protocol mapper reconcilers were merged into one. A mapper on a
  client scope now logs `Creating protocol mapper` rather than
  `Creating client scope protocol mapper` (likewise updating and skipping), with
  the container carried in `container`, `containerId` and `containerName`
  attributes. Mappers on a client gained the same attributes in place of the
  bare `clientUUID`, and `containerName` is the clientId, which is more use in a
  log than the UUID. This matches how the dry-run adapter already reports them.
- The user and group role reconcilers were merged into one. Four log messages
  become two: `Assigning realm roles to user` and `Granting realm roles to
  group` are both now `Assigning realm roles`, and likewise for client roles,
  with the subject carried in `subject` (`users` or `groups`) and `subjectName`
  attributes. The `Realm role already assigned` and `Client role already
  assigned` debug lines are unchanged, and groups now emit them too.
- Failures assigning roles now carry more context. Group role failures name the
  group and the role and wrap the underlying error, where the group path
  returned these bare; and a client that cannot be resolved names the realm and
  the subject as well as the clientId, where each path previously reported only
  part of that.
- Roles are sent to the role-mapping endpoints as `{id, name}` rather than the
  whole representation Keycloak returned. Groups already did this; users and
  service accounts now do too, so nothing echoes back a server-derived field
  like `composite` or `containerId`.
- `realmRoles` and `clientRoles` on an organization group are now rejected at
  config load with an explanation, instead of being rejected as unknown fields.
  Keycloak 26.7 accepts the corresponding role-mapping call and reads the role
  back, but the mapping never reaches a member's effective roles or any token
  claim. An unknown-field error reads as "not implemented yet", which invites
  wiring the endpoint up by hand; naming the trap does not.
- Identity provider updates merge over the server's current representation
  rather than sending a sparse body. Keycloak replaces the whole representation
  for this resource, so a stored client secret — returned masked as
  `**********` — and any config key the schema does not model would otherwise be
  deleted. Mapper config is deliberately the opposite: replaced from the config
  alone, since a mapper has neither a masked secret nor unmodelled fields.
- `mergeAttributes` is now a thin wrapper over `mergeStringMapField`, which
  takes the field name, so the identity provider config merge reuses it instead
  of duplicating its coercion loop.

### Fixed

- Provisioning an organization group with more than 10 subgroups is idempotent
  again. Keycloak defaults the group children endpoint to 10 results, and
  organization groups are listed whole and matched by name in the provisioner
  rather than filtered by Keycloak, so past the tenth subgroup the second run
  tried to recreate what already existed and failed with
  `409 Sibling group with the given name already exists`. Realm groups were
  never affected: their lookup asks Keycloak for an exact name, so the default
  never truncates the answer.
- Provisioning an organization with more than 10 members is idempotent again.
  Keycloak defaults `GET /organizations/{id}/members` to 10 results, so the
  provisioner's view of who was already a member was truncated: it re-added
  members 11 and beyond on every run and the second run failed with
  `409 User is already a member of the organization`. The listing now asks for
  all of them. Among the listings this client uses, only this one is capped —
  organization groups, their members, organization identity providers, a user's
  groups, and the realm-level listings were all verified to return in full.
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

[Unreleased]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.9.0...HEAD
[1.9.0]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.8.1...v1.9.0
[1.8.1]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.8.0...v1.8.1
[1.8.0]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.7.0...v1.8.0
[1.7.0]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.6.0...v1.7.0
[1.6.0]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.5.0...v1.6.0
[1.5.0]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.4.0...v1.5.0
[1.4.0]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.3.0...v1.4.0
[1.3.0]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.2.1...v1.3.0
[1.2.1]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.2.0...v1.2.1
[1.2.0]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/datarocks-ag/keycloak-provisioner/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/datarocks-ag/keycloak-provisioner/releases/tag/v1.0.0
