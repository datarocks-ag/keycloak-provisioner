# Deployment

The provisioner is a short-lived process. It logs in to Keycloak, makes the server match the
config, and exits: `0` on success, non-zero on any error. Run it wherever your application is
deployed, as the step that has to succeed before the application starts.

## Container image

Images are published to `ghcr.io/datarocks-ag/keycloak-provisioner` for `linux/amd64` and
`linux/arm64`:

| Tag | Points to | Use it for |
|---|---|---|
| `1.14.0` | exactly that release | production: reproducible |
| `1.14` | latest patch release of 1.14 | production: picks up fixes |
| `latest` | latest release | trying things out |
| `develop` | the `develop` branch | testing unreleased changes |
| `sha-<commit>` | a single commit (short hash) | debugging |

Release tags (`v1.14.0` in Git) produce the version tags and `latest`; every push to
`develop` produces `develop` and a `sha-<commit>` tag (`.github/workflows/release.yaml`).
The documentation on the `develop` branch may describe features that aren't released yet;
the [changelog](../CHANGELOG.md) lists them under *Unreleased*.

The image is built `FROM scratch` (see [`Dockerfile`](../Dockerfile)): it contains the static
binary at `/keycloak-provisioner`, which is the entrypoint, and the Alpine CA certificate
bundle at `/etc/ssl/certs/ca-certificates.crt`, and nothing else. There is no shell to `exec`
into, so a container can't run wrapper scripts. Its working directory is `/`, so the default
config path `./config.yaml` resolves to `/config.yaml`.

`--version` prints the version the image was built from, which is the output of
`git describe --tags` (`v1.14.0` for a release image, `v1.14.0-<n>-g<commit>` for a `develop`
image), or `dev` for a local `make build` or `make docker`:

```console
$ docker run --rm ghcr.io/datarocks-ag/keycloak-provisioner:1.14.0 --version
v1.14.0
```

## Standalone binary

Every release has binaries for Linux and macOS (`amd64`, `arm64`) as `tar.gz` archives, with a
`checksums.txt`, on the
[releases page](https://github.com/datarocks-ag/keycloak-provisioner/releases)
(see [`.goreleaser.yaml`](../.goreleaser.yaml)). Their `--version` prints the release
number without the `v` (`1.14.0`):

```console
$ export KEYCLOAK_URL=https://id.example.com KEYCLOAK_USER=<admin-user> KEYCLOAK_PASSWORD=<admin-password>
$ keycloak-provisioner --config ./config.yaml --dry-run
$ keycloak-provisioner --config ./config.yaml
```

`go install` isn't supported: the module path (`keycloak-provisioner`) is not a URL.

## Docker Compose

The provisioner runs between Keycloak and the application:

```yaml
services:
  keycloak:
    image: keycloak/keycloak:26.8
    command: ["start-dev"]
    environment:
      KC_HEALTH_ENABLED: "true"
      KC_BOOTSTRAP_ADMIN_USERNAME: admin
      KC_BOOTSTRAP_ADMIN_PASSWORD: adminpass
    ports: ["8080:8080", "9000:9000"]
    healthcheck:
      # The image ships without curl, so probe the management port with bash.
      test: ["CMD-SHELL", "{ printf 'GET /health/ready HTTP/1.1\\r\\nHost: localhost\\r\\nConnection: close\\r\\n\\r\\n' >&3; grep -q '200 OK' <&3; } 3<>/dev/tcp/localhost/9000"]
      interval: 5s
      timeout: 5s
      retries: 20

  keycloak-provisioner:
    image: ghcr.io/datarocks-ag/keycloak-provisioner:1.14
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
    image: your-registry/your-app
    depends_on:
      keycloak-provisioner:
        condition: service_completed_successfully
```

The first two services are the repository's [`docker-compose.yaml`](../docker-compose.yaml).

- The Keycloak image has no `curl`, so the health check opens a TCP connection to
  `/health/ready` on the management port (9000, enabled by `KC_HEALTH_ENABLED`) with bash's
  `/dev/tcp` and looks for `200 OK`.
- `KC_BOOTSTRAP_ADMIN_USERNAME` and `KC_BOOTSTRAP_ADMIN_PASSWORD` create the first
  administrator. Keycloak 26 logs the older `KEYCLOAK_ADMIN` as deprecated
  (Keycloak behaviour, not checked by the provisioner).
- `service_healthy` isn't strictly needed (the provisioner [retries](#connection-retry) until
  Keycloak answers), but it keeps the logs free of retry warnings.
- `service_completed_successfully` keeps the application from starting when provisioning
  failed.
- Don't set `restart: always` on the provisioner. It is supposed to exit.

Outside local development, don't write passwords into the Compose file: see
[Keep secrets out of the config file](howto/keep-secrets-out-of-the-config.md), and
[Provision with a dedicated admin account](howto/use-a-dedicated-admin-account.md) for an
account other than the bootstrap administrator.

## Kubernetes

The provisioner needs nothing beyond the environment variables and the config file, so it
fits a Kubernetes Job or an init container. The manifests below are examples; they use only
the variables and flags listed on this page. Ship the config as a ConfigMap and the
credentials as a Secret:

```console
$ kubectl create configmap shop-keycloak-config --from-file=config.yaml
$ kubectl create secret generic keycloak-provisioner-admin \
    --from-literal=username=<admin-user> --from-literal=password=<admin-password>
$ kubectl create secret generic shop-api-client --from-literal=secret=<client-secret>
```

### As a Job

A Job fits deploy pipelines and Helm hooks (`helm.sh/hook: pre-install,pre-upgrade`). Each
deploy runs it once, and the application rollout waits for it:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: shop-keycloak-provisioner
spec:
  backoffLimit: 2
  ttlSecondsAfterFinished: 3600
  template:
    spec:
      restartPolicy: Never
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
      containers:
        - name: keycloak-provisioner
          image: ghcr.io/datarocks-ag/keycloak-provisioner:1.14
          env:
            - { name: KEYCLOAK_URL, value: "https://keycloak.example.com" }
            - name: KEYCLOAK_USER
              valueFrom: { secretKeyRef: { name: keycloak-provisioner-admin, key: username } }
            - name: KEYCLOAK_PASSWORD
              valueFrom: { secretKeyRef: { name: keycloak-provisioner-admin, key: password } }
            - name: SHOP_API_CLIENT_SECRET
              valueFrom: { secretKeyRef: { name: shop-api-client, key: secret } }
            - { name: KEYCLOAK_CONFIG_PATH, value: /config/config.yaml }
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities: { drop: [ALL] }
          volumeMounts:
            - { name: config, mountPath: /config, readOnly: true }
      volumes:
        - name: config
          configMap: { name: shop-keycloak-config }
```

`SHOP_API_CLIENT_SECRET` stands for whatever `${VAR}` references your config contains.

### As an init container

To provision whenever the application's pod starts, add the same container as an
`initContainer` of the application's Deployment; the application container only starts after
it exits with `0`. Every replica runs it on every start, and the provisioner takes no lock, so
for more than one replica prefer the Job.
[Run the provisioner as an init container](howto/run-as-init-container.md) walks through
both Compose and Kubernetes.

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `KEYCLOAK_USER` | **required** | Username of the admin account the provisioner logs in as, in the `master` realm |
| `KEYCLOAK_PASSWORD` | **required** | Its password |
| `KEYCLOAK_URL` | `http://localhost:8080` | Keycloak base URL; must include `http://` or `https://` and a host |
| `KEYCLOAK_CONFIG_PATH` | `./config.yaml` | Path to the YAML config; `--config` overrides it |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` (or `warning`) or `error`; any other value means `info` |

An empty variable counts as unset: `KEYCLOAK_USER=""` is reported as missing, and an empty
`KEYCLOAK_URL` falls back to the default (`internal/cli/cli.go`). `KEYCLOAK_USER` and
`KEYCLOAK_PASSWORD` are not needed for `--version`.

There are no `_FILE` variants: the provisioner reads its credentials from the environment
only. Variables referenced in the config (`${MY_APP_CLIENT_SECRET}`) come from the same
environment; see [Keep secrets out of the config file](howto/keep-secrets-out-of-the-config.md).

Use an `https://` URL in production. With `http://` the provisioner logs a warning, because
the admin password and the access token cross the network unencrypted; see
[Connect to Keycloak over HTTPS](howto/connect-over-https.md).

## CLI flags

| Flag | Description |
|---|---|
| `--config <path>` | Path to the YAML config; overrides `KEYCLOAK_CONFIG_PATH` |
| `--dry-run` | Log every change instead of making it ([Preview changes with a dry run](howto/preview-changes-with-dry-run.md)) |
| `--skip-version-check` | Skip the whole [compatibility check](#keycloak-compatibility) |
| `--version` | Print the version and exit |

`--dry-run` has no environment variable. `-h` prints the flag list.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Provisioning finished, or `-h` / `--version` |
| `1` | The config failed to load or validate, the connection failed, the compatibility check refused the config, or provisioning failed |
| `2` | Unknown flag, or `KEYCLOAK_USER` / `KEYCLOAK_PASSWORD` missing |

Exit code `2` comes before logging is set up, so its message is plain text on standard error:

```console
$ keycloak-provisioner --config ./config.yaml
required environment variable KEYCLOAK_USER is not set
$ echo $?
2
```

## Logs

Everything after argument parsing is logged to standard output as JSON lines, one object per
event, with `time`, `level` and `msg` plus fields for the event. A successful run ends with
these two lines:

```json
{"time":"…","level":"INFO","msg":"Provisioning complete"}
{"time":"…","level":"INFO","msg":"keycloak-provisioner finished successfully"}
```

A failed run ends with an `ERROR` line whose `error` field carries the cause, for example:

```json
{"time":"…","level":"ERROR","msg":"Provisioning failed","error":"provisioning realm \"partial\": ensuring user \"dave\": realm role \"auditor\" not found in realm \"partial\""}
```

Log lines name the resources, never the passwords or client secrets sent for them; an
`error` field can contain the body of Keycloak's error response. Set `LOG_LEVEL=debug` to see
token refreshes as well. Durations, such as the `delay` of a retry, are
logged in nanoseconds (`1000000000` is one second).

## Connection retry

At startup, the provisioner logs in with exponential backoff, so it can start alongside
Keycloak without wait scripts:

- Up to 16 attempts (`max_retries` 15 after the first), waiting 1, 2, 4, 8 and 16 seconds,
  then 30 seconds between each further attempt.
- At most 5 minutes in total; then the run fails with `connection timeout …`. With these
  waits the limit is reached after about 14 attempts.
- Each HTTP request times out after 30 seconds.
- **Every** failed login is retried, including a wrong password: Keycloak's
  `invalid_grant` answer looks the same to the provisioner as a server that isn't ready.

Each retry is a warning carrying the error, without credentials. Captured against
Keycloak 26.6.4 with a wrong password, stopped after 8 seconds:

```json
{"time":"…","level":"WARN","msg":"Keycloak not ready, retrying","attempt":1,"max_retries":15,"delay":1000000000,"error":"token request failed with status 400: {\"error\":\"invalid_grant\",\"error_description\":\"Invalid user credentials\"}"}
{"time":"…","level":"WARN","msg":"Keycloak not ready, retrying","attempt":2,"max_retries":15,"delay":2000000000,"error":"token request failed with status 400: {\"error\":\"invalid_grant\",\"error_description\":\"Invalid user credentials\"}"}
{"time":"…","level":"WARN","msg":"Keycloak not ready, retrying","attempt":3,"max_retries":15,"delay":4000000000,"error":"token request failed with status 400: {\"error\":\"invalid_grant\",\"error_description\":\"Invalid user credentials\"}"}
```

Read the `error` field of the first warning instead of waiting five minutes for the run to
end. A `KEYCLOAK_URL` without scheme or host is not retried; it fails at once.

The provisioner logs in with the OAuth 2.0 password grant, as client `admin-cli` in the
`master` realm. It refreshes the access token 30 seconds before it expires and, when Keycloak
answers a request with `401` mid-run, logs in again once and retries that request. There is
no client-credentials login.

## Keycloak compatibility

Some configuration only works on newer Keycloak releases, and some of it also depends on a
server feature that can be switched off. Right after logging in, and before writing anything,
the provisioner reads the server version and feature list and refuses a config the server
can't apply:

<!-- BEGIN COMPATIBILITY TABLE -->

| Config | Minimum Keycloak | Server feature |
|---|---|---|
| organizations | 26.0 | `ORGANIZATION` |
| organization groups | 26.6 | `ORGANIZATION` |
| standard token exchange | 26.2 | `TOKEN_EXCHANGE_STANDARD_V2` |
| client management permissions | any | `ADMIN_FINE_GRAINED_AUTHZ` |
| step-up authentication | any | `STEP_UP_AUTHENTICATION` |

<!-- END COMPATIBILITY TABLE -->

`any` in the version column means every Keycloak release supports it and only the feature
flag matters. Anything not listed is never checked against the version. A config that uses
none of these rows is never blocked, whatever the server version.

The check refuses a feature only when the server reports it as disabled. For
`ADMIN_FINE_GRAINED_AUTHZ` the error also names the start option to use
(`--features=admin-fine-grained-authz:v1`). It covers two kinds of failure. Most rows would
make provisioning fail partway through. Two would not: with `TOKEN_EXCHANGE_STANDARD_V2` or
`STEP_UP_AUTHENTICATION` switched off, Keycloak still accepts and stores the settings, but
they have no effect (Keycloak behaviour, not checked by the provisioner). The check treats
both the same way.

Two cases are deliberately not failures:

- **A version string the provisioner can't parse.** Version comparisons are skipped with the
  warning `Could not parse the Keycloak version; skipping version checks`; feature checks
  still apply. Qualifiers are ignored, so `999.0.0-SNAPSHOT` (a nightly build) counts as
  newer than any release.
- **A feature the server does not mention at all.** That means the release predates it, which
  the version comparison already covers.

The table is generated from `internal/compat/requirements.go`; contributors regenerate it
with `go test ./internal/compat -update`.

### Provider and required action validation

Alongside the table, the check asks the server what it actually offers and refuses config
naming anything else. That catches a provider missing because a feature is off, one that
doesn't exist in this release, or a typo. It validates:

- `provider` on every authentication flow execution, against the union of the server's
  authenticator, form, form-action and client-authenticator providers;
- `protocolMapper` on clients and client scopes, against the mapper types the server reports
  for that protocol, which also catches a SAML mapper on an OpenID Connect client;
- an identity provider's `providerId` and its mappers' types;
- users' `requiredActions`, against the required actions registered in the `master` realm.

A typo gets a suggestion. Captured against Keycloak 26.6.4; nothing was written:

```json
{"time":"…","level":"ERROR","msg":"Unsupported by this Keycloak server","capability":"auth-cookei","reason":"authenticator \"auth-cookei\" is not available on this server (did you mean \"auth-cookie\"?)","usedAt":"realms[0].authenticationFlows[0].executions[0].provider"}
{"time":"…","level":"ERROR","msg":"Keycloak compatibility check failed","error":"config is not supported by this Keycloak server:\n  auth-cookei: authenticator \"auth-cookei\" is not available on this server (did you mean \"auth-cookie\"?) (used at realms[0].authenticationFlows[0].executions[0].provider)"}
```

### What the check does not cover

The check only refuses version, feature, provider and required action mismatches. It does
not make a run all-or-nothing: any other error, such as a role a user references that
neither the config nor the server has, a `403` from Keycloak, or a conflict with a
concurrent run, stops the run where it happens, and what was written before stays. Captured:
the client below was created, then the user step failed.

```json
{"time":"…","level":"INFO","msg":"Creating realm","realm":"partial"}
{"time":"…","level":"INFO","msg":"Creating client","realm":"partial","clientId":"partial-web"}
{"time":"…","level":"INFO","msg":"Creating user","realm":"partial","username":"dave"}
{"time":"…","level":"ERROR","msg":"Provisioning failed","error":"provisioning realm \"partial\": ensuring user \"dave\": realm role \"auditor\" not found in realm \"partial\""}
```

Fix the config and run again: the provisioner picks up from what exists. A
[dry run](howto/preview-changes-with-dry-run.md) reports most of these errors before
anything is written.

Pass `--skip-version-check` to bypass the whole check, for example for a custom Keycloak
build. The run then logs `Skipping the Keycloak compatibility check (--skip-version-check)`.

## Admin account permissions

The provisioner logs in to the `master` realm and calls the Admin REST API, so the account in
`KEYCLOAK_USER` needs admin roles there. The simplest choice is the `admin` realm role of
`master`, which the bootstrap administrator holds. A narrower account works too:

| What the provisioner does | Roles the account needs in `master` |
|---|---|
| Create new realms | the `create-realm` realm role; Keycloak gives the account the admin roles of each realm it creates (Keycloak behaviour, observed on 26.6.4) |
| Manage a realm that already exists | roles of that realm's management client `<realm>-realm` in `master`; `manage-realm`, `manage-clients` and `manage-users` were enough for realm settings, clients, roles and users on 26.6.4 |
| Create users under `masterRealm` | `manage-users` of the `master-realm` client (observed on 26.6.4) |
| Validate providers and required actions | `view-realm` of the `master-realm` client |

Other resources need the matching `manage-…` role of `<realm>-realm`, such as
`manage-identity-providers` for identity providers (Keycloak behaviour, not checked by the
provisioner). The roles are described in
the [Keycloak Server Administration Guide](https://www.keycloak.org/docs/latest/server_admin/).

Two things degrade instead of failing when the account may not read them:

- Without `view-realm` on `master-realm`, provider and required action validation is skipped
  with a warning, and a misspelled provider or required action goes unnoticed:

  ```json
  {"time":"…","level":"WARN","msg":"Not permitted to read what the server offers; skipping provider and required action validation","error":"reading required actions: unexpected status 403: {\"error\":\"HTTP 403 Forbidden\"}"}
  ```

- If the server info is refused, version and feature checks are skipped with
  `Not permitted to read the Keycloak server info; skipping version and feature checks`.

Only `401` and `403` are tolerated this way. A network failure, a `5xx` or an unreadable
response fails the run. Anything else the account may not do fails the run with Keycloak's
`unexpected status 403`. [Provision with a dedicated admin account](howto/use-a-dedicated-admin-account.md)
sets such an account up with the provisioner itself.

## Next steps

- [Run the provisioner as an init container](howto/run-as-init-container.md)
- [Keep secrets out of the config file](howto/keep-secrets-out-of-the-config.md) before going
  to production.
- [Connect to Keycloak over HTTPS](howto/connect-over-https.md)
- [FAQ](faq.md) for operations questions.
