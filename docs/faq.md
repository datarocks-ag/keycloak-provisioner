# FAQ

After reading this page you can answer the questions that come up once the provisioner runs
for real. The provisioner reads one YAML config and brings Keycloak realms, clients, roles,
groups, users, flows, identity providers and organizations in line with it on every run; it
creates and updates, and it never deletes. [How it works](concepts.md) explains a run in
full, and the [configuration reference](configuration.md) lists every field.

The output on this page was captured against Keycloak 26.6.4 on 2026-11-15, in test realms
named `faq` and `tx`. Log lines are trimmed to the relevant fields: real lines also
carry `time`, and `…` marks start-up lines left out. Commands use
`KEYCLOAK_URL=http://localhost:8080`; point it at your own server.

- [Day-to-day behaviour](#day-to-day-behaviour)
  - [I removed a client, role or user from the config. Why does it still exist?](#i-removed-a-client-role-or-user-from-the-config-why-does-it-still-exist)
  - [I edited an authentication flow and nothing changed. Why?](#i-edited-an-authentication-flow-and-nothing-changed-why)
  - [What does `strategy: create` actually skip?](#what-does-strategy-create-actually-skip)
  - [Why did a user's password change back after a run?](#why-did-a-users-password-change-back-after-a-run)
  - [Why does a dry run show "would update" for things that haven't changed?](#why-does-a-dry-run-show-would-update-for-things-that-havent-changed)
  - [Can I check a config without a running Keycloak?](#can-i-check-a-config-without-a-running-keycloak)
  - [Will a run wipe attributes I set by hand?](#will-a-run-wipe-attributes-i-set-by-hand)
  - [Can I split the config across several files?](#can-i-split-the-config-across-several-files)
- [Configuration errors](#configuration-errors)
  - [My `${VAR}` showed up literally as a password or secret. Why?](#my-var-showed-up-literally-as-a-password-or-secret-why)
  - [Why does loading fail with "field … not found in type …"?](#why-does-loading-fail-with-field--not-found-in-type-)
  - [Why do I get "provisioning the "master" realm is not allowed; use masterRealm instead"?](#why-do-i-get-provisioning-the-master-realm-is-not-allowed-use-masterrealm-instead)
  - [Why do I get "serviceAccountsEnabled must be true when serviceAccountRoles is set"?](#why-do-i-get-serviceaccountsenabled-must-be-true-when-serviceaccountroles-is-set)
  - [Why do I get "organizations: requires organizationsEnabled: true on the realm"?](#why-do-i-get-organizations-requires-organizationsenabled-true-on-the-realm)
  - [Why do I get "client scope … is listed as both default and optional"?](#why-do-i-get-client-scope--is-listed-as-both-default-and-optional)
  - [Why do I get "standardTokenExchangeEnabled: requires a confidential client …"?](#why-do-i-get-standardtokenexchangeenabled-requires-a-confidential-client-)
  - [How do I stop managing `managementPermissions` when `enabled: false` is refused?](#how-do-i-stop-managing-managementpermissions-when-enabled-false-is-refused)
  - [Why do I get "… is not in the acrLoaMap; the config declares …"?](#why-do-i-get--is-not-in-the-acrloamap-the-config-declares-)
  - [Why do I get "… differs only in case from a domain of organization …"?](#why-do-i-get--differs-only-in-case-from-a-domain-of-organization-)
  - [Why can't organization groups carry roles?](#why-cant-organization-groups-carry-roles)
- [Errors during a run](#errors-during-a-run)
  - [Why does a run fail with "realm role … not found in realm …" or "client role … not found on client …"?](#why-does-a-run-fail-with-realm-role--not-found-in-realm--or-client-role--not-found-on-client-)
  - [A user wasn't added to a group, but the run succeeded. Why?](#a-user-wasnt-added-to-a-group-but-the-run-succeeded-why)
  - [A client is missing a scope I listed. Why?](#a-client-is-missing-a-scope-i-listed-why)
  - [Why does a run fail with "user … exists with id … but the config declares …"?](#why-does-a-run-fail-with-user--exists-with-id--but-the-config-declares-)
  - [Why does a run fail with "authentication flow "browser" is a built-in Keycloak flow …"?](#why-does-a-run-fail-with-authentication-flow-browser-is-a-built-in-keycloak-flow-)
  - [Why does a run fail with "organization …: alias is … on the server but … in the config"?](#why-does-a-run-fail-with-organization--alias-is--on-the-server-but--in-the-config)
  - [Why does a run fail with "identity provider … is already linked to organization …"?](#why-does-a-run-fail-with-identity-provider--is-already-linked-to-organization-)
  - [Why do I see "User is not a member of the organization, skipping group membership"?](#why-do-i-see-user-is-not-a-member-of-the-organization-skipping-group-membership)
- [Keycloak compatibility](#keycloak-compatibility)
  - [The run stops with "Unsupported by this Keycloak server". What now?](#the-run-stops-with-unsupported-by-this-keycloak-server-what-now)
  - [Why do I get "requires the … server feature, which is disabled …"?](#why-do-i-get-requires-the--server-feature-which-is-disabled-)
  - [Why do I get "authenticator … is not available on this server (did you mean …)"?](#why-do-i-get-authenticator--is-not-available-on-this-server-did-you-mean-)
  - [Is "Not permitted to read what the server offers …" a problem?](#is-not-permitted-to-read-what-the-server-offers--a-problem)
  - [When should I use `--skip-version-check`?](#when-should-i-use---skip-version-check)
- [Connection and credentials](#connection-and-credentials)
  - [Why does a wrong admin password take about five minutes to fail?](#why-does-a-wrong-admin-password-take-about-five-minutes-to-fail)
  - [Which account does it log in as? Can I use client credentials?](#which-account-does-it-log-in-as-can-i-use-client-credentials)
  - [Why do I get "required environment variable KEYCLOAK_USER is not set"?](#why-do-i-get-required-environment-variable-keycloak_user-is-not-set)
  - [Why do I get "invalid Keycloak URL …"?](#why-do-i-get-invalid-keycloak-url-)
  - [What do the exit codes mean?](#what-do-the-exit-codes-mean)
  - [I set `LOG_LEVEL=trace` and see only info. Why?](#i-set-log_leveltrace-and-see-only-info-why)
- [Tokens](#tokens)
  - [Why does a token exchange fail with "Requested audience not available"?](#why-does-a-token-exchange-fail-with-requested-audience-not-available)
  - [Why do a client's tokens have no `resource_access`?](#why-do-a-clients-tokens-have-no-resource_access)

## Day-to-day behaviour

### I removed a client, role or user from the config. Why does it still exist?

Because the provisioner never deletes anything. Removing an entry from the config only stops
the provisioner from managing it.

Deleting a resource in Keycloak can lock out a service or drop every token that refers to
it, so a missing line must never be able to do that. Whatever the config doesn't mention is
left as it is, and that holds for every resource kind, including role mappings and group
memberships. If you want the resource gone, delete it yourself in the Keycloak Admin
Console after taking it out of the config; otherwise the next run creates it again.

You can see that a removed client is still there with a read-only call to the admin API
(safe anywhere: read-only):

```console
$ curl -s -H "Authorization: Bearer <admin-token>" \
    "http://localhost:8080/admin/realms/<realm>/clients?clientId=<client-id>" | jq -r '.[].clientId'
<client-id>
```

Further reading: [How it works](concepts.md)

### I edited an authentication flow and nothing changed. Why?

Because authentication flows are create-only: once a flow with that alias exists, the
provisioner skips it, whatever the `strategy`.

Updating a flow would mean diffing an ordered tree of executions that have no stable names
and deleting the executions that aren't configured, so the provisioner leaves existing flows
alone. The run says so. Here the first run created `browser-otp` with the one-time password
step `REQUIRED`; the config was then changed to `ALTERNATIVE` and run again:

```yaml
realms:
  - realm: faq
    authenticationFlows:
      - alias: browser-otp
        copyFrom: browser
        executions:
          - provider: auth-otp-form
            requirement: ALTERNATIVE
```

```json
…
{"level":"INFO","msg":"Starting provisioning"}
{"level":"INFO","msg":"Updating realm","realm":"faq"}
{"level":"INFO","msg":"Skipping existing authentication flow","realm":"faq","flow":"browser-otp"}
{"level":"INFO","msg":"Provisioning complete"}
{"level":"INFO","msg":"keycloak-provisioner finished successfully"}
```

To roll out a different flow, declare it under a new alias and point the binding
(`authenticationBindings` on the realm, or `authenticationFlowBindingOverrides` on a client)
at the new alias.

Further reading: [Configuration reference](configuration.md),
[Add step-up authentication to a client](howto/add-step-up-authentication.md)

### What does `strategy: create` actually skip?

Only the update of the resource's own settings, such as a realm's display name, a client's
redirect URIs or a user's email. Everything attached to an existing resource is still
reconciled.

With `strategy: create`, an existing realm, client scope, client, protocol mapper, role,
group, user, identity provider, identity provider mapper or organization isn't updated. The
run still:

- sets a user's `password` on every run, and adds the user's roles, groups and credentials;
- adds a group's role mappings and subgroups, and an organization's members and subgroups;
- attaches a client's `defaultClientScopes` and `optionalClientScopes`, and applies
  `serviceAccountRoles`, `scopeMappings` and `managementPermissions`;
- applies the realm's `authenticationBindings`;
- creates missing identity provider mappers.

`strategy` is set at the top level or per realm, and the realm value wins. `masterRealm` has
no `strategy` of its own: its users follow the top-level one. In this run `alice` already
existed; her profile is skipped, her password is set anyway:

```yaml
strategy: create
realms:
  - realm: faq
    users:
      - username: alice
        password: "Initial-1"
        firstName: Alice
```

```json
…
{"level":"INFO","msg":"Starting provisioning"}
{"level":"INFO","msg":"Skipping existing realm (strategy=create)","realm":"faq"}
{"level":"INFO","msg":"Skipping existing user (strategy=create)","realm":"faq","username":"alice"}
{"level":"INFO","msg":"Setting user password","realm":"faq","username":"alice"}
{"level":"INFO","msg":"Provisioning complete"}
{"level":"INFO","msg":"keycloak-provisioner finished successfully"}
```

Further reading: [How it works](concepts.md)

### Why did a user's password change back after a run?

Because `password` is set on every run, under either strategy. If the user or an
administrator changes it in Keycloak, the next run sets it back to the configured value.

For a password the user is meant to choose, use `initialPassword` instead. It is set only
when the provisioner creates the user, and Keycloak marks it temporary, so the user has to
change it at first login. The two fields are mutually exclusive. A run on an existing user
shows the difference: `alice` has `password`, `bob` has `initialPassword`:

```json
…
{"level":"INFO","msg":"Updating user","realm":"faq","username":"alice"}
{"level":"INFO","msg":"Setting user password","realm":"faq","username":"alice"}
{"level":"INFO","msg":"Updating user","realm":"faq","username":"bob"}
…
```

Further reading: [Provision users with passwords, roles and groups](howto/provision-users.md)

### Why does a dry run show "would update" for things that haven't changed?

Because a dry run logs the calls a run would make, not a comparison. A real run sends an
update for every existing realm, client, role, group, user, identity provider and
organization (under the default `strategy: update`), so the dry run reports each one.

Role assignments, group memberships, scope assignments and other attached items are
compared with the server first and only appear when something is missing. A dry run of an
unchanged config, safe anywhere because it changes nothing:

```console
$ KEYCLOAK_URL=http://localhost:8080 KEYCLOAK_USER=admin KEYCLOAK_PASSWORD=<password> \
    keycloak-provisioner --config config.yaml --dry-run
…
{"level":"INFO","msg":"Dry-run mode enabled — mutations will be logged but not applied"}
{"level":"INFO","msg":"Starting provisioning"}
{"level":"INFO","msg":"Updating realm","realm":"faq"}
{"level":"INFO","msg":"DRY-RUN: would update realm","realm":"faq"}
{"level":"INFO","msg":"Updating client","realm":"faq","clientId":"shop-web","uuid":"d3f395c4-a3a6-41d9-b3fe-61a4c81cea08"}
{"level":"INFO","msg":"DRY-RUN: would update client","realm":"faq","uuid":"d3f395c4-a3a6-41d9-b3fe-61a4c81cea08","clientId":"shop-web"}
{"level":"INFO","msg":"Updating user","realm":"faq","username":"alice"}
{"level":"INFO","msg":"DRY-RUN: would update user","realm":"faq","userID":"8e92ea18-64b6-4b42-953e-dca5f6a5e774","username":"alice"}
{"level":"INFO","msg":"Setting user password","realm":"faq","username":"alice"}
{"level":"INFO","msg":"DRY-RUN: would reset user password","realm":"faq","userID":"8e92ea18-64b6-4b42-953e-dca5f6a5e774","temporary":false}
…
{"level":"INFO","msg":"Provisioning complete"}
```

`alice` already has her role and group, so no `Assigning realm roles` or `Adding user to
group` line appears.

Further reading: [Preview changes with a dry run](howto/preview-changes-with-dry-run.md)

### Can I check a config without a running Keycloak?

Partly. The provisioner loads and validates the config before it connects, so a run against
no server tells you whether the config itself is valid.

`KEYCLOAK_USER` and `KEYCLOAK_PASSWORD` are read before the config, so set them to any
value. Point `KEYCLOAK_URL` at a port where nothing listens; this is safe anywhere. An invalid
config ends with `Failed to load configuration` and exit code `1` right after
`Loading configuration`. A valid one gets as far as connecting; stop it with Ctrl-C once you
see the retry warning:

```console
$ KEYCLOAK_URL=http://127.0.0.1:9 KEYCLOAK_USER=check KEYCLOAK_PASSWORD=check \
    keycloak-provisioner --config config.yaml
{"level":"INFO","msg":"Starting keycloak-provisioner","version":"dev","dryRun":false}
{"level":"INFO","msg":"Loading configuration","path":"config.yaml"}
{"level":"INFO","msg":"Configuration loaded","realms":1}
{"level":"INFO","msg":"Connecting to Keycloak","url":"http://127.0.0.1:9"}
{"level":"WARN","msg":"Keycloak URL uses plain HTTP — credentials will be sent unencrypted","url":"http://127.0.0.1:9"}
{"level":"WARN","msg":"Keycloak not ready, retrying","attempt":1,"max_retries":15,"delay":1000000000,"error":"token request: Post \"http://127.0.0.1:9/realms/master/protocol/openid-connect/token\": dial tcp 127.0.0.1:9: connect: connection refused"}
```

This covers the YAML and every rule the config can check on its own. It doesn't cover what
only the server knows: the compatibility check (version, server features, authenticator and
mapper names) and whether roles, groups and scopes the config refers to exist. For those,
use `--dry-run` against a real server.

Further reading: [Configuration reference](configuration.md),
[Preview changes with a dry run](howto/preview-changes-with-dry-run.md)

### Will a run wipe attributes I set by hand?

Not on realms, clients and organizations: there, the configured attributes are merged over
the ones Keycloak already has, and keys the config doesn't mention are kept. A key the
config does declare is set to the configured value on every run.

Client scopes, groups and organization groups are different: when the config declares
`attributes` for one of them, the provisioner sends exactly that map, so hand-set keys on
that scope or group are replaced. Without an `attributes` block nothing is sent and Keycloak
26.6.4 kept the existing keys in the test below.

The test set `supportContact` on the realm `faq` by hand, then ran a config that declares
only `frontendUrl` (on a test realm; the `PUT` below replaces the realm's attributes):

```console
$ curl -s -X PUT -H "Authorization: Bearer <admin-token>" -H 'Content-Type: application/json' \
    -d '{"attributes":{"supportContact":"ops@example.com"}}' \
    http://localhost:8080/admin/realms/faq
$ curl -s -H "Authorization: Bearer <admin-token>" http://localhost:8080/admin/realms/faq \
    | jq -c '.attributes | {frontendUrl, supportContact}'
{"frontendUrl":null,"supportContact":"ops@example.com"}
$ KEYCLOAK_URL=http://localhost:8080 KEYCLOAK_USER=admin KEYCLOAK_PASSWORD=<password> \
    keycloak-provisioner --config config.yaml
…
$ curl -s -H "Authorization: Bearer <admin-token>" http://localhost:8080/admin/realms/faq \
    | jq -c '.attributes | {frontendUrl, supportContact}'
{"frontendUrl":"https://id.example.com","supportContact":"ops@example.com"}
```

The hand-written `PUT` itself dropped `frontendUrl`, because Keycloak replaces the whole
attribute map on update (Keycloak behaviour, not checked by the provisioner). That is why
the provisioner merges instead.

Further reading: [Configuration reference](configuration.md)

### Can I split the config across several files?

No. A run reads one file (`--config`, or `KEYCLOAK_CONFIG_PATH`, default `./config.yaml`),
and that file must hold exactly one YAML document.

A file with a second document after `---` is refused before anything is sent to Keycloak:

```json
{"level":"ERROR","msg":"Failed to load configuration","error":"parsing config YAML: file contains multiple YAML documents; only one is supported"}
```

If you want separate files, give each realm its own file and run the provisioner once per
file. Keep each realm in exactly one file: two files that declare the same realm both update
it, and the last run wins for the fields they both set.

Further reading: [Configuration reference](configuration.md),
[Deployment](deployment.md)

## Configuration errors

### My `${VAR}` showed up literally as a password or secret. Why?

Because a `${VAR}` whose environment variable isn't set is left in the config as written,
without an error or a warning. That text then becomes the password or secret.

Expansion happens when the config loads. `$${VAR}` is the escape for a literal `${VAR}`. In
this test (on a test realm) `DAVE_PASSWORD` was not set, the run succeeded, and the literal
string logs in:

```yaml
realms:
  - realm: faq
    enabled: true
    users:
      - username: dave
        enabled: true
        email: dave@example.com
        firstName: Dave
        lastName: Example
        password: "${DAVE_PASSWORD}"
```

```console
$ curl -s -d client_id=admin-cli -d username=dave --data-urlencode 'password=${DAVE_PASSWORD}' \
    -d grant_type=password http://localhost:8080/realms/faq/protocol/openid-connect/token \
    | jq -c '{token_type}'
{"token_type":"Bearer"}
```

Check that every variable is set in the environment the provisioner runs in, for example in
the container's `env` section, not only in your shell.

Further reading: [Keep secrets out of the config file](howto/keep-secrets-out-of-the-config.md)

### Why does loading fail with "field … not found in type …"?

Because the config contains a field the provisioner doesn't know. The parser is strict, so a
typo or a field copied from a Keycloak realm export is refused instead of being ignored.

The message gives the line and the Go type the field was found in; `config.Client` means a
client entry. Here `redirectUri` should be `redirectUris` (safe anywhere: fails before
connecting):

```yaml
realms:
  - realm: shop
    clients:
      - clientId: shop-web
        redirectUri: "https://shop.example.com/*"
```

```json
{"level":"ERROR","msg":"Failed to load configuration","error":"parsing config YAML: yaml: unmarshal errors:\n  line 5: field redirectUri not found in type config.Client"}
```

A realm export doesn't load either: its field names and shapes differ from this config (for
example, user roles are `roles: {realm: […], clients: {…}}`, not `realmRoles`).

Further reading: [Configuration reference](configuration.md)

### Why do I get "provisioning the "master" realm is not allowed; use masterRealm instead"?

Because the `master` realm can't be listed under `realms`. It always exists and holds the
administrator accounts, so the provisioner manages only a small part of it, through the
top-level `masterRealm` block: `sslRequired` and `users`.

```json
{"level":"ERROR","msg":"Failed to load configuration","error":"validating config: realms[0].realm: provisioning the \"master\" realm is not allowed; use masterRealm instead"}
```

Move what you need into `masterRealm`:

```yaml
masterRealm:
  sslRequired: external
  users:
    - username: ops-admin
      password: "${OPS_ADMIN_PASSWORD}"
      roles:
        realm: [admin]
```

Further reading: [Configuration reference](configuration.md)

### Why do I get "serviceAccountsEnabled must be true when serviceAccountRoles is set"?

Because only a client with a service account can hold service account roles, and the config
doesn't turn the service account on.

```json
{"level":"ERROR","msg":"Failed to load configuration","error":"validating config: realms[0].clients[0].serviceAccountRoles: serviceAccountsEnabled must be true when serviceAccountRoles is set"}
```

Add `serviceAccountsEnabled: true` to the client:

```yaml
realms:
  - realm: shop
    clients:
      - clientId: billing-worker
        serviceAccountsEnabled: true
        serviceAccountRoles:
          realm: [offline_access]
```

Further reading: [Set up a machine-to-machine client](howto/set-up-a-service-account-client.md)

### Why do I get "organizations: requires organizationsEnabled: true on the realm"?

Because the realm declares `organizations` but not `organizationsEnabled: true`, and Keycloak
only accepts organizations on a realm that has them switched on.

```json
{"level":"ERROR","msg":"Failed to load configuration","error":"validating config: realms[0].organizations: requires organizationsEnabled: true on the realm"}
```

Set it on the same realm, even if you already switched it on in the Admin Console:

```yaml
realms:
  - realm: shop
    organizationsEnabled: true
    organizations:
      - name: acme
        domains:
          - name: acme.example
```

Further reading: [Onboard an organization](howto/onboard-an-organization.md)

### Why do I get "client scope … is listed as both default and optional"?

Because a client scope can be attached to a client as default or as optional, not both, and
the client names it in `defaultClientScopes` and in `optionalClientScopes`. This is rejected
since 1.12.0 (2026-08-31).

```json
{"level":"ERROR","msg":"Failed to load configuration","error":"validating config: realms[0].clients[0]: client scope \"email\" is listed as both default and optional"}
```

Keep the scope in one of the two lists.

Further reading: [Add claims and audiences with client scopes](howto/add-claims-with-client-scopes.md)

### Why do I get "standardTokenExchangeEnabled: requires a confidential client …"?

Because only a confidential client, one with a secret, can exchange tokens, and this client
is public.

```json
{"level":"ERROR","msg":"Failed to load configuration","error":"validating config: realms[0].clients[0].standardTokenExchangeEnabled: requires a confidential client (publicClient must be false)"}
```

Set `publicClient: false` and give the client a `secret`, or enable token exchange on a
separate confidential client, such as your backend.

Further reading: [Let a client exchange tokens for another audience](howto/set-up-token-exchange.md)

### How do I stop managing `managementPermissions` when `enabled: false` is refused?

Remove the `managementPermissions` block from the client. The provisioner then stops managing
the client's fine-grained permissions and leaves what exists in Keycloak as it is.

`enabled: false` is refused because Keycloak deletes the client's scope permissions and the
policies attached to them when fine-grained permissions are switched off, and the provisioner
doesn't delete:

```json
{"level":"ERROR","msg":"Failed to load configuration","error":"validating config: realms[0].clients[0].managementPermissions.enabled: must be true — Keycloak deletes the client's scope permissions and their policies when fine-grained permissions are disabled, which this provisioner does not do; remove the managementPermissions block to stop managing the client"}
```

Further reading: [Allow a client to impersonate users](howto/allow-impersonation.md),
[Configuration reference](configuration.md)

### Why do I get "… is not in the acrLoaMap; the config declares …"?

Because a client's `defaultAcrValues` names an authentication context class reference (ACR)
value that neither the realm's nor the client's `acrLoaMap` in the config declares.

Only the config counts: a map that exists on the server but isn't in the config isn't
consulted. When the config declares no `acrLoaMap` at all, the check is skipped.

```yaml
realms:
  - realm: shop
    acrLoaMap:
      silver: 1
      gold: 2
    clients:
      - clientId: shop-web
        defaultAcrValues: [platinum]
```

```json
{"level":"ERROR","msg":"Failed to load configuration","error":"validating config: realms[0].clients[0].defaultAcrValues[0]: \"platinum\" is not in the acrLoaMap; the config declares gold, silver"}
```

Add the value to `acrLoaMap`, or use one of the values the message lists.

Further reading: [Add step-up authentication to a client](howto/add-step-up-authentication.md)

### Why do I get "… differs only in case from a domain of organization …"?

Because the identity provider's `kc.org.domain` and the organization's domain differ in upper
and lower case, and Keycloak compares the two exactly. The provisioner's tests measured this
on Keycloak 26.6: such a provider is accepted when first created and refused on every run
after.

```yaml
realms:
  - realm: shop
    organizationsEnabled: true
    identityProviders:
      - alias: acme-sso
        providerId: oidc
        config:
          kc.org.domain: Acme.example
    organizations:
      - name: acme
        domains:
          - name: acme.example
        identityProviders: [acme-sso]
```

```json
{"level":"ERROR","msg":"Failed to load configuration","error":"validating config: realms[0].identityProviders[0].config[kc.org.domain]: identity provider \"acme-sso\" has \"Acme.example\", which differs only in case from a domain of organization \"acme\" (acme.example); Keycloak compares them exactly"}
```

Write the domain the same way in both places.

Further reading: [Broker logins through an external identity provider](howto/connect-an-identity-provider.md),
[Onboard an organization](howto/onboard-an-organization.md)

### Why can't organization groups carry roles?

Because the roles would never take effect, so the provisioner refuses `realmRoles` and
`clientRoles` on an organization group, even as an empty list.

Keycloak can store a role mapping on an organization group, but it never reaches a member's
effective roles or any token claim (Keycloak behaviour, not checked by the provisioner; the
reasons are recorded in the provisioner's code).

```json
{"level":"ERROR","msg":"Failed to load configuration","error":"validating config: realms[0].organizations[0].groups[0]: organization groups cannot carry role mappings; Keycloak stores them but they never reach a member's effective roles or any token claim. Assign the roles to the members directly, or use a realm group"}
```

Grant the roles through a realm group, or to the users directly:

```yaml
realms:
  - realm: shop
    organizationsEnabled: true
    roles:
      - name: buyer
    groups:
      - name: purchasing
        realmRoles: [buyer]
    organizations:
      - name: acme
        domains:
          - name: acme.example
        groups:
          - name: purchasing
```

Further reading: [Grant roles through groups](howto/grant-roles-through-groups.md),
[Onboard an organization](howto/onboard-an-organization.md)

## Errors during a run

### Why does a run fail with "realm role … not found in realm …" or "client role … not found on client …"?

Because a user, group, service account or scope mapping refers to a role that doesn't exist
in Keycloak and isn't declared in the config.

The run stops at that point, and what it wrote before stays: here `carol` was created and
then the run failed on her role (on a test realm):

```yaml
realms:
  - realm: faq
    users:
      - username: carol
        roles:
          realm: [auditor]
```

```json
…
{"level":"INFO","msg":"Creating user","realm":"faq","username":"carol"}
{"level":"ERROR","msg":"Provisioning failed","error":"provisioning realm \"faq\": ensuring user \"carol\": realm role \"auditor\" not found in realm \"faq\""}
```

For a client role the message names the client:

```json
{"level":"ERROR","msg":"Provisioning failed","error":"provisioning realm \"faq\": ensuring user \"carol\": client role \"admin\" not found on client \"shop-web\" in realm \"faq\""}
```

Declare the role in the same config: realm roles under the realm's `roles`, client roles
under the client's `clientRoles`. Roles are created before users, groups and service accounts
are assigned them, so the next run succeeds and continues where this one stopped.

Further reading: [How it works](concepts.md),
[Provision users with passwords, roles and groups](howto/provision-users.md)

### A user wasn't added to a group, but the run succeeded. Why?

Because the group path doesn't resolve to an existing group. A missing group is a warning,
not an error, so the run goes on.

Look for the warning in the log:

```yaml
realms:
  - realm: faq
    users:
      - username: carol
        groups: [/support]
```

```json
…
{"level":"WARN","msg":"Group not found, skipping membership","realm":"faq","username":"carol","group":"/support"}
{"level":"INFO","msg":"Provisioning complete"}
```

Declare the group under the realm's `groups` (groups are created before users), or fix the
path. A path names the parent groups too: `/engineering/backend` for the subgroup `backend`.

Further reading: [Grant roles through groups](howto/grant-roles-through-groups.md)

### A client is missing a scope I listed. Why?

Because the client scope doesn't exist in the realm. Like a missing group, a missing scope is
a warning and the run goes on.

```yaml
realms:
  - realm: faq
    clients:
      - clientId: shop-web
        publicClient: true
        defaultClientScopes: [orders:write]
```

```json
…
{"level":"WARN","msg":"Client scope not found, skipping assignment","realm":"faq","clientId":"shop-web","clientScope":"orders:write","type":"default"}
{"level":"INFO","msg":"Provisioning complete"}
```

Declare the scope under the realm's `clientScopes`; scopes are created before clients.

Further reading: [Add claims and audiences with client scopes](howto/add-claims-with-client-scopes.md)

### Why does a run fail with "user … exists with id … but the config declares …"?

Because the config gives the user a fixed `id`, and the user with that username already
exists in Keycloak under a different id. Keycloak can't change a user's id after creation.

```yaml
realms:
  - realm: faq
    users:
      - username: bob
        id: 3f2b8c1e-5d4a-4e6f-9a7b-1c2d3e4f5a6b
```

```json
…
{"level":"ERROR","msg":"Provisioning failed","error":"provisioning realm \"faq\": ensuring user \"bob\": user \"bob\" exists with id fa6f5e62-9998-4bec-9433-a6ff9fe95a0b but the config declares 3f2b8c1e-5d4a-4e6f-9a7b-1c2d3e4f5a6b; ids cannot be changed after creation"}
```

Put the id from the message into the config, or remove `id` from the user. Keep ids unique
and never reuse an id another user already has.

Further reading: [Provision users with passwords, roles and groups](howto/provision-users.md)

### Why does a run fail with "authentication flow "browser" is a built-in Keycloak flow …"?

Because the config declares a flow whose alias belongs to one of Keycloak's built-in flows,
and the provisioner never modifies those.

```json
…
{"level":"ERROR","msg":"Provisioning failed","error":"provisioning realm \"faq\": ensuring authentication flow \"browser\": authentication flow \"browser\" is a built-in Keycloak flow and will not be modified; declare a new alias with copyFrom: \"browser\" to customise it"}
```

Give your flow its own alias. `copyFrom: browser`, as the message suggests, gives you an exact
copy:

```yaml
realms:
  - realm: faq
    authenticationFlows:
      - alias: browser-copy
        copyFrom: browser
```

Don't add `REQUIRED` or `CONDITIONAL` steps to such a copy: they land beside the copied
`ALTERNATIVE` login steps, and Keycloak then ignores those, so nobody can log in through the flow.
To add a second factor or step-up, build the flow from scratch instead, as
[Add step-up authentication to a client](howto/add-step-up-authentication.md) does. Then bind the
new alias with `authenticationBindings` or a client's `authenticationFlowBindingOverrides`.

Further reading: [Add step-up authentication to a client](howto/add-step-up-authentication.md),
[Configuration reference](configuration.md)

### Why does a run fail with "organization …: alias is … on the server but … in the config"?

Because an organization's alias can't change once it exists, and the config declares a
different one than Keycloak holds.

```json
…
{"level":"ERROR","msg":"Provisioning failed","error":"provisioning realm \"faq\": ensuring organization \"acme\": organization \"acme\": alias is \"acme\" on the server but \"acme-corp\" in the config, and Keycloak does not allow it to change; either restore the configured value or remove the alias from the config"}
```

Set `alias` back to the value on the server, or remove `alias` from the config.

Further reading: [Onboard an organization](howto/onboard-an-organization.md)

### Why does a run fail with "identity provider … is already linked to organization …"?

Because Keycloak links an identity provider to at most one organization, and the provider is
already linked to an organization this config doesn't describe. The message names that
organization by its id.

```json
…
{"level":"ERROR","msg":"Provisioning failed","error":"provisioning realm \"faq\": ensuring organization \"globex\": organization \"globex\": identity provider \"acme-sso\" is already linked to organization c6cbd4df-4e95-4772-a06f-7b7640b41802; Keycloak allows a provider to belong to at most one"}
```

Look the id up to see which organization holds the provider (safe anywhere: read-only):

```console
$ curl -s -H "Authorization: Bearer <admin-token>" \
    http://localhost:8080/admin/realms/faq/organizations/c6cbd4df-4e95-4772-a06f-7b7640b41802 \
    | jq '{name, alias}'
{
  "name": "acme",
  "alias": "acme"
}
```

Then either link a separate identity provider to the new organization, or decide which of
the two organizations should own this one.

Further reading: [Broker logins through an external identity provider](howto/connect-an-identity-provider.md),
[Onboard an organization](howto/onboard-an-organization.md)

### Why do I see "User is not a member of the organization, skipping group membership"?

Because an organization group lists a user who isn't a member of the organization. Only
members can join its groups, so the provisioner skips that user and the run goes on.

```yaml
realms:
  - realm: faq
    organizationsEnabled: true
    organizations:
      - name: acme
        domains:
          - name: acme.example
        members: [alice]
        groups:
          - name: purchasing
            members: [alice, bob]
```

```json
…
{"level":"INFO","msg":"Adding user to organization group","realm":"faq","organization":"acme","group":"purchasing","username":"alice"}
{"level":"WARN","msg":"User is not a member of the organization, skipping group membership","realm":"faq","organization":"acme","group":"purchasing","username":"bob"}
{"level":"INFO","msg":"Provisioning complete"}
```

Add the user to the organization's `members` as well.

Further reading: [Onboard an organization](howto/onboard-an-organization.md)

## Keycloak compatibility

### The run stops with "Unsupported by this Keycloak server". What now?

Read the `reason`: the server can't apply part of the config. The check runs after login and
before the first change, so nothing has been written yet.

There are three kinds of reason:

- `requires Keycloak <version> or newer, server is <version>`: upgrade Keycloak, or drop the
  feature from the config. [Deployment](deployment.md) has the table of minimum versions.
- `requires the <FEATURE> server feature, which is disabled on this server`: start Keycloak
  with that feature enabled (see the next question).
- `… is not available on this server`: a name in the config, usually a typo (see the question
  after next).

`usedAt` lists the config paths that need the capability. One `Unsupported` line is logged
per problem, followed by a summary:

```json
{"level":"ERROR","msg":"Keycloak compatibility check failed","error":"config is not supported by this Keycloak server:\n  auth-otp-from: authenticator \"auth-otp-from\" is not available on this server (did you mean \"auth-otp-form\"?) (used at realms[0].authenticationFlows[0].executions[0].provider)"}
```

Further reading: [Deployment](deployment.md)

### Why do I get "requires the … server feature, which is disabled …"?

Because the config uses something that Keycloak only offers with a server feature switched
on, and this server has it switched off.

For fine-grained admin permissions (`managementPermissions`) the message also names the flag
to start Keycloak with. Expected output, written from the code:

```json
{"level":"ERROR","msg":"Unsupported by this Keycloak server","capability":"client management permissions","reason":"requires the ADMIN_FINE_GRAINED_AUTHZ server feature, which is disabled on this server; start Keycloak with --features=admin-fine-grained-authz:v1","usedAt":"realms[0].clients[0].managementPermissions"}
```

Enable the feature with Keycloak's `--features` option and restart it, or remove what needs
it from the config. The provisioner only reports a feature as disabled when the server says
so explicitly; when it can't read the server info, it skips this check.

Further reading: [Deployment](deployment.md)

### Why do I get "authenticator … is not available on this server (did you mean …)"?

Because a flow execution names an authenticator that this Keycloak doesn't offer, most often
a typo. The provisioner suggests the closest name it found (safe anywhere: refused before any
change):

```yaml
realms:
  - realm: faq
    authenticationFlows:
      - alias: browser-otp
        copyFrom: browser
        executions:
          - provider: auth-otp-from
            requirement: REQUIRED
```

```json
…
{"level":"INFO","msg":"Detected Keycloak version","version":"26.6.4"}
{"level":"ERROR","msg":"Unsupported by this Keycloak server","capability":"auth-otp-from","reason":"authenticator \"auth-otp-from\" is not available on this server (did you mean \"auth-otp-form\"?)","usedAt":"realms[0].authenticationFlows[0].executions[0].provider"}
```

The same check covers protocol mapper types, identity provider types, identity provider
mapper types and required actions. A provider can also be missing because a server feature
is off or because it comes from an extension that isn't installed.

Further reading: [Configuration reference](configuration.md)

### Is "Not permitted to read what the server offers …" a problem?

Not for the run itself, but you lose part of the safety net. The admin account may not read
the server's provider lists, so the provisioner skips checking authenticators, protocol
mapper types, identity provider and mapper types and required actions, and carries on.

Expected output, written from the code:

```json
{"level":"WARN","msg":"Not permitted to read what the server offers; skipping provider and required action validation","error":"reading required actions: unexpected status 403…"}
```

A typo in one of those names then fails partway through the run, with Keycloak's error
instead of the provisioner's. The similar warning `Not permitted to read the Keycloak server
info; skipping version and feature checks` means the version and feature checks were skipped.
Both are normal for an account that holds only the rights it needs to provision. Run a dry
run with a full administrator once if you want the checks.

Further reading: [Provision with a dedicated admin account](howto/use-a-dedicated-admin-account.md)

### When should I use `--skip-version-check`?

Only when the check is wrong about your server, for example a custom Keycloak build whose
version or provider list doesn't tell the truth. It skips the whole pre-flight check:
version, server features, provider names and required actions.

Whatever the check would have caught then fails in the middle of the run, after earlier
changes are written. A dry run doesn't catch it either, because it doesn't send the changes.
Here the authenticator typo from the previous questions passes a dry run with the check
skipped:

```console
$ KEYCLOAK_URL=http://localhost:8080 KEYCLOAK_USER=admin KEYCLOAK_PASSWORD=<password> \
    keycloak-provisioner --config config.yaml --dry-run --skip-version-check
…
{"level":"WARN","msg":"Skipping the Keycloak compatibility check (--skip-version-check)"}
{"level":"INFO","msg":"Dry-run mode enabled — mutations will be logged but not applied"}
…
{"level":"INFO","msg":"DRY-RUN: would add authentication execution","realm":"faq","flow":"browser-otp","provider":"auth-otp-from"}
{"level":"INFO","msg":"Provisioning complete"}
{"level":"INFO","msg":"keycloak-provisioner finished successfully"}
```

Further reading: [Deployment](deployment.md)

## Connection and credentials

### Why does a wrong admin password take about five minutes to fail?

Because the provisioner treats every failed login as "Keycloak not ready yet" and retries,
for up to five minutes, before it gives up.

Retrying is what lets the provisioner start alongside Keycloak, but the login response doesn't
tell a starting server apart from wrong credentials. The delays are 1, 2, 4, 8, 16 seconds and
then 30 seconds each time. The `error` field shows the real cause; here Keycloak answers
`Invalid user credentials` (captured with `timeout 8`, safe anywhere):

```json
…
{"level":"INFO","msg":"Connecting to Keycloak","url":"http://localhost:8080"}
{"level":"WARN","msg":"Keycloak not ready, retrying","attempt":1,"max_retries":15,"delay":1000000000,"error":"token request failed with status 400: {\"error\":\"invalid_grant\",\"error_description\":\"Invalid user credentials\"}"}
{"level":"WARN","msg":"Keycloak not ready, retrying","attempt":2,"max_retries":15,"delay":2000000000,"error":"token request failed with status 400: {\"error\":\"invalid_grant\",\"error_description\":\"Invalid user credentials\"}"}
{"level":"WARN","msg":"Keycloak not ready, retrying","attempt":3,"max_retries":15,"delay":4000000000,"error":"token request failed with status 400: {\"error\":\"invalid_grant\",\"error_description\":\"Invalid user credentials\"}"}
```

`delay` is in nanoseconds. Read the first retry line rather than waiting: if it says
`invalid_grant`, fix `KEYCLOAK_USER` or `KEYCLOAK_PASSWORD`. After five minutes the run ends
with exit code `1`; expected last line, written from the code:

```json
{"level":"ERROR","msg":"Failed to connect to Keycloak","error":"connection timeout: context deadline exceeded"}
```

Further reading: [Deployment](deployment.md)

### Which account does it log in as? Can I use client credentials?

It logs in as the user in `KEYCLOAK_USER`, a user of the `master` realm, with the password
in `KEYCLOAK_PASSWORD`. Client credentials aren't supported.

The provisioner asks the `master` realm's token endpoint for a token with the password grant,
through Keycloak's built-in `admin-cli` client. It refreshes the token before it expires and
logs in again once if Keycloak answers `401` mid-run. There is no setting for another realm,
another client or a client secret. You can test the account by hand the same way (safe
anywhere: it only issues a token):

```console
$ curl -s -d client_id=admin-cli -d username=<admin-user> -d password=<password> \
    -d grant_type=password http://localhost:8080/realms/master/protocol/openid-connect/token \
    | jq -r .token_type
Bearer
```

The account doesn't have to be the bootstrap `admin`: a dedicated account with only the
roles it needs works too.

Further reading: [Provision with a dedicated admin account](howto/use-a-dedicated-admin-account.md)

### Why do I get "required environment variable KEYCLOAK_USER is not set"?

Because `KEYCLOAK_USER` and `KEYCLOAK_PASSWORD` are mandatory and one is unset or empty. The
provisioner checks them before it reads the config, prints the message as plain text on
standard error, and exits with code `2`:

```console
$ KEYCLOAK_PASSWORD=<password> keycloak-provisioner --config config.yaml
required environment variable KEYCLOAK_USER is not set
$ echo $?
2
```

There are no `_FILE` variants; pass the value itself, for example from a Kubernetes secret
through `env`. `--version` is the only invocation that works without them. The other
variables have defaults: `KEYCLOAK_URL` is `http://localhost:8080`, `KEYCLOAK_CONFIG_PATH`
is `./config.yaml` and `LOG_LEVEL` is `info`.

Further reading: [Deployment](deployment.md)

### Why do I get "invalid Keycloak URL …"?

Because `KEYCLOAK_URL` has no scheme or no host. It must be a full URL starting with
`http://` or `https://`. This fails at once, without retrying:

```console
$ KEYCLOAK_URL=keycloak:8080 KEYCLOAK_USER=admin KEYCLOAK_PASSWORD=<password> \
    keycloak-provisioner --config config.yaml
…
{"level":"INFO","msg":"Connecting to Keycloak","url":"keycloak:8080"}
{"level":"ERROR","msg":"Failed to connect to Keycloak","error":"invalid Keycloak URL \"keycloak:8080\": must include scheme and host"}
```

Use `KEYCLOAK_URL=http://keycloak:8080`, or `https://…` outside a local setup. With plain
`http` the provisioner logs the warning `Keycloak URL uses plain HTTP — credentials will be
sent unencrypted`.

Further reading: [Connect to Keycloak over HTTPS](howto/connect-over-https.md)

### What do the exit codes mean?

`0`: the run finished, or `--help` / `--version` printed. `1`: the config didn't load, the
connection failed, the compatibility check refused the config, or provisioning failed. `2`: a
flag is wrong, or `KEYCLOAK_USER` or `KEYCLOAK_PASSWORD` is missing.

With `1`, the last log line says what happened; its `msg` is one of `Failed to load
configuration`, `Failed to connect to Keycloak`, `Keycloak compatibility check failed` or
`Provisioning failed`. With `2`, the message is plain text on standard error:

```console
$ keycloak-provisioner --dryrun
flag provided but not defined: -dryrun
Usage of keycloak-provisioner:
…
$ echo $?
2
```

Kubernetes Jobs, init containers and Compose's `service_completed_successfully` rely on these
codes.

Further reading: [Deployment](deployment.md),
[Run the provisioner as an init container](howto/run-as-init-container.md)

### I set `LOG_LEVEL=trace` and see only info. Why?

Because `trace` isn't a level the provisioner knows, and an unknown value falls back to
`info` without a warning. The accepted values are `debug`, `info`, `warn` (or `warning`) and
`error`, in lower case: `DEBUG` falls back to `info` too.

`debug` adds the lines for things that were already in place (captured on a test realm):

```console
$ LOG_LEVEL=debug KEYCLOAK_URL=http://localhost:8080 KEYCLOAK_USER=admin KEYCLOAK_PASSWORD=<password> \
    keycloak-provisioner --config config.yaml
…
{"level":"DEBUG","msg":"Realm role already assigned","realm":"faq","subject":"users","subjectName":"alice","role":"viewer"}
{"level":"DEBUG","msg":"User already member of group","realm":"faq","username":"alice","group":"/staff"}
{"level":"DEBUG","msg":"User already a member of organization","realm":"faq","organization":"acme","username":"alice"}
…
```

With `LOG_LEVEL=trace` the same run printed none of these lines.

Further reading: [Deployment](deployment.md)

## Tokens

### Why does a token exchange fail with "Requested audience not available"?

Because nothing in scope of the exchanging client gives it access to the audience it asks
for. Keycloak only offers an audience when the client's scope holds a role of the target
client that the user also has (Keycloak behaviour, not checked by the provisioner; captured
below on 26.6.4).

In this test realm `shop-bff` exchanges `erin`'s token for the audience `orders-api`. The first
config gives `erin` no role on `orders-api`, and the exchange fails:

```console
$ curl -s -d client_id=shop-bff -d client_secret=bff-secret \
    -d grant_type=urn:ietf:params:oauth:grant-type:token-exchange \
    -d subject_token=<erin-access-token> \
    -d subject_token_type=urn:ietf:params:oauth:token-type:access_token \
    -d audience=orders-api http://localhost:8080/realms/tx/protocol/openid-connect/token
{"error":"invalid_request","error_description":"Requested audience not available: orders-api"}
```

With the role granted, the exchange returns a token for `orders-api`:

```yaml
realms:
  - realm: tx
    enabled: true
    clients:
      - clientId: orders-api
        secret: orders-secret
        publicClient: false
        clientRoles:
          - name: reader
      - clientId: shop-bff
        secret: bff-secret
        publicClient: false
        directAccessGrantsEnabled: true
        standardTokenExchangeEnabled: true
    users:
      - username: erin
        enabled: true
        email: erin@example.com
        firstName: Erin
        lastName: Example
        password: "Erin-pass-1"
        roles:
          clients:
            orders-api: [reader]
```

The decoded result has `{"aud":"orders-api","resource_access":{"orders-api":{"roles":["reader"]}}}`.

If the exchanging client has `fullScopeAllowed: false`, granting the role isn't enough: the
same exchange fails again until the client's `scopeMappings` names the role
(`clients: {orders-api: [reader]}`). That was also captured on the test realm.

Further reading: [Let a client exchange tokens for another audience](howto/set-up-token-exchange.md)

### Why do a client's tokens have no `resource_access`?

Usually because the client lost Keycloak's built-in `roles` client scope, which is what adds
`resource_access` to its tokens.

Before 1.10.0 (2026-08-22), a client created with `defaultClientScopes` got exactly that
list and lost Keycloak's own default scopes, `roles` among them. Since 1.10.0 the provisioner
attaches the configured scopes in addition to Keycloak's defaults, and it only adds, so a
client created by an older version keeps its narrowed set. A client created now keeps the
defaults; here `reports` declares only `email` (the `curl` is read-only, safe anywhere):

```yaml
realms:
  - realm: tx
    clients:
      - clientId: reports
        secret: reports-secret
        publicClient: false
        defaultClientScopes: [email]
```

```console
$ curl -s -H "Authorization: Bearer <admin-token>" \
    http://localhost:8080/admin/realms/tx/clients/<client-uuid>/default-client-scopes \
    | jq -r '[.[].name] | sort | join(", ")'
acr, basic, email, profile, roles, web-origins
```

If `roles` is missing from that list, the client was created before 1.10.0; the changelog
entry for 1.10.0 describes the options. If `roles` is there and `resource_access` is still
missing, check whether the client has `fullScopeAllowed: false` without `scopeMappings`, which
leaves it with no roles in scope.

Further reading: [CHANGELOG 1.10.0](../CHANGELOG.md),
[Add claims and audiences with client scopes](howto/add-claims-with-client-scopes.md)
