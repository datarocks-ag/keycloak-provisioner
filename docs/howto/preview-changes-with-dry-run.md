# Preview changes with a dry run

After this you can see what the provisioner would create, update and assign on a Keycloak
server before it writes anything, and catch config errors that would otherwise stop a real
run halfway.

## When you need this

You changed the config of a Keycloak that is already in use, such as staging or production,
and want to know what the next run will touch before it touches it. Or a run failed halfway,
and you want to find the next error without writing more half-finished state.

## What you need to know first

- **Dry run.** `--dry-run` runs the whole provisioning sequence, but every call that would
  change Keycloak is logged as a `DRY-RUN: would …` line instead of being sent. Reads still go
  to the server, so the run sees what exists. See [How it works](../concepts.md).
- **It still connects.** A dry run logs in with `KEYCLOAK_USER` and `KEYCLOAK_PASSWORD` and
  runs the [compatibility check](../deployment.md), so it needs the same network access
  and credentials as a real run, and an account that may read everything the config touches.
- **It logs calls, not differences.** The provisioner updates an existing resource by sending
  the fields you declare, without comparing them first, so every existing resource shows a
  `would update` line, changed or not. Role assignments, group memberships and similar links
  are compared: they only show what is missing.
- **There is no environment variable for it.** Only the `--dry-run` flag turns it on
  ([CLI flags](../deployment.md)).

## Steps

The example adds a `support` role to an existing realm `shop` and grants it to Alice. The
config, `shop.yaml`:

```yaml
realms:
  - realm: "shop"
    displayName: "Shop (staging)"
    enabled: true
    roles:
      - name: "customer"
        description: "Can place orders"
      - name: "support"
        description: "Can see all orders"
    clients:
      - clientId: "shop-web"
        publicClient: true
        redirectUris:
          - "https://shop.example.com/*"
    users:
      - username: "alice"
        email: "alice@example.com"
        initialPassword: "change-me"
        roles:
          realm:
            - "customer"
            - "support"
```

### 1. Run the provisioner with `--dry-run`

Use the same environment as the real run and add the flag:

```console
$ KEYCLOAK_URL=http://localhost:8080 KEYCLOAK_USER=admin KEYCLOAK_PASSWORD=<admin-password> \
    keycloak-provisioner --config shop.yaml --dry-run
```

In Docker Compose, pass the flag to the service; the image's entrypoint is the provisioner,
so the arguments go straight to it:

```console
$ docker compose run --rm keycloak-provisioner --dry-run
```

The run says it is a dry run twice, at the start and before provisioning:

```json
{"time":"…","level":"INFO","msg":"Starting keycloak-provisioner","version":"dev","dryRun":true}
{"time":"…","level":"INFO","msg":"Dry-run mode enabled — mutations will be logged but not applied"}
```

### 2. Read the `DRY-RUN` lines

Each change comes as a pair: the line the real run also logs (`Creating realm role`), then
the `DRY-RUN: would …` line for the call that was skipped. Captured against Keycloak 26.6.4,
where `shop` already had the `customer` role, the client and Alice:

```json
{"time":"…","level":"INFO","msg":"Updating realm","realm":"shop"}
{"time":"…","level":"INFO","msg":"DRY-RUN: would update realm","realm":"shop"}
{"time":"…","level":"INFO","msg":"Updating client","realm":"shop","clientId":"shop-web","uuid":"7a08ba97-2c18-4344-aaeb-54e26d6bf0f5"}
{"time":"…","level":"INFO","msg":"DRY-RUN: would update client","realm":"shop","uuid":"7a08ba97-2c18-4344-aaeb-54e26d6bf0f5","clientId":"shop-web"}
{"time":"…","level":"INFO","msg":"Updating realm role","realm":"shop","role":"customer"}
{"time":"…","level":"INFO","msg":"DRY-RUN: would update realm role","realm":"shop","role":"customer"}
{"time":"…","level":"INFO","msg":"Creating realm role","realm":"shop","role":"support"}
{"time":"…","level":"INFO","msg":"DRY-RUN: would create realm role","realm":"shop","role":"support"}
{"time":"…","level":"INFO","msg":"Updating user","realm":"shop","username":"alice"}
{"time":"…","level":"INFO","msg":"DRY-RUN: would update user","realm":"shop","userID":"ff3903e5-cf69-4933-9292-0c43d8487f7c","username":"alice"}
{"time":"…","level":"INFO","msg":"Assigning realm roles","realm":"shop","subject":"users","subjectName":"alice","count":1}
{"time":"…","level":"INFO","msg":"DRY-RUN: would assign realm roles","realm":"shop","subject":"users","subjectID":"ff3903e5-cf69-4933-9292-0c43d8487f7c","roles":["support"]}
{"time":"…","level":"INFO","msg":"Provisioning complete"}
{"time":"…","level":"INFO","msg":"keycloak-provisioner finished successfully"}
```

Read it this way:

- `would create` lines are the resources that don't exist yet: here the `support` role.
- `would assign` lines list only what is missing: Alice already holds `customer`, so only
  `support` is listed.
- `would update` lines appear for every existing resource the config declares. The new
  `displayName` is in the `would update realm` call, but so is everything else declared on
  the realm; the line doesn't tell you whether anything differs. Compare the fields in the
  Keycloak admin console if it matters.

To see only the skipped calls, filter for them:

```console
$ keycloak-provisioner --config shop.yaml --dry-run | grep DRY-RUN
```

For a resource that doesn't exist yet, nested IDs are placeholders starting with `dryrun-`,
such as `"userID":"dryrun-user-3"` for a user that would be created.

### 3. Fix what the dry run rejects

A dry run fails on the same config errors as a real run, but before anything is written. A
user referencing a role that neither the config nor the server has, for example (captured):

```json
{"time":"…","level":"INFO","msg":"DRY-RUN: would create client","realm":"partial","clientId":"partial-web"}
{"time":"…","level":"INFO","msg":"DRY-RUN: would create user","realm":"partial","username":"dave"}
{"time":"…","level":"ERROR","msg":"Provisioning failed","error":"provisioning realm \"partial\": ensuring user \"dave\": realm role \"auditor\" not found in realm \"partial\""}
```

The exit code is `1`, as for a real run. A real run of the same config would have created the
realm, the client and the user, and then stopped. Fix the config and repeat the dry run until
it ends with `keycloak-provisioner finished successfully`.

### 4. Apply

Run the same command without `--dry-run`.

## Check that it works

Run the dry run again after applying. The `would create` and `would assign` lines are gone;
only the `would update` lines for the declared resources remain (captured):

```console
$ keycloak-provisioner --config shop.yaml --dry-run | grep DRY-RUN
{"time":"…","level":"INFO","msg":"DRY-RUN: would update realm","realm":"shop"}
{"time":"…","level":"INFO","msg":"DRY-RUN: would update client","realm":"shop","uuid":"7a08ba97-2c18-4344-aaeb-54e26d6bf0f5","clientId":"shop-web"}
{"time":"…","level":"INFO","msg":"DRY-RUN: would update realm role","realm":"shop","role":"customer"}
{"time":"…","level":"INFO","msg":"DRY-RUN: would update realm role","realm":"shop","role":"support"}
{"time":"…","level":"INFO","msg":"DRY-RUN: would update user","realm":"shop","userID":"ff3903e5-cf69-4933-9292-0c43d8487f7c","username":"alice"}
```

## Pitfalls

- **`would update` doesn't mean something changed.** See step 2. With `strategy: create`
  existing realms, clients, roles and users are logged as `Skipping existing … (strategy=create)`
  instead, but passwords, role assignments and the other links are still applied (see
  [Configuration reference](../configuration.md)).
- **The `Creating …` and `Updating …` lines are not proof that anything was written.** In a
  dry run they appear too; only the absence of `DRY-RUN` lines and `"dryRun":false` in the
  first line mark a real run.
- **New authentication flows are reported only partly.** For a flow that would be created,
  the dry run lists the flow and its executions, but not their requirements and execution
  config, because those are set by reading the created executions back.
- **Management permission scope names** on a client whose permissions would be enabled for
  the first time are checked against a fixed list in a dry run, and against the server in a
  real run, so a real run can still reject a scope name that passed the dry run.
- **A dry run needs read access.** It reads everything the config touches; an account that
  may not read a realm fails the dry run with `unexpected status 403`, as the real run would.
- **Secrets are not shown.** Client secrets and passwords never appear in the log, so a dry
  run can't tell you whether `${VAR}` references resolved. See
  [Keep secrets out of the config file](keep-secrets-out-of-the-config.md).

## Further reading

- [How it works](../concepts.md): the provisioning order and what an update sends.
- [Deployment](../deployment.md): CLI flags, exit codes, the compatibility check.
- [Configuration reference](../configuration.md): `strategy` and what each resource does on
  update.
