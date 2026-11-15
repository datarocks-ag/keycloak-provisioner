# Keep secrets out of the config file

After this your config file holds `${VAR}` references instead of client secrets and
passwords, so you can commit it or ship it as a Kubernetes ConfigMap, and the values come
from the environment at run time.

## When you need this

Your config declares a confidential client with a `secret`, a user with a `password`, a
seeded time-based one-time password (TOTP) secret, or an identity provider's client secret.
You want the config in version control and the secret values in a secret store, such as a
Kubernetes Secret, your CI's secret variables or an `.env` file that never leaves the host.

## What you need to know first

- **`${VAR}` expansion.** When the provisioner loads the config, it replaces every `${VAR}` in
  a string value with the environment variable `VAR`. This happens after the YAML is parsed,
  so a value containing `:`, `#` or quotes needs no escaping. Most string fields support it;
  the exceptions (such as `strategy` and client scope `type`) are listed in the
  [Configuration reference](../configuration.md). Background in
  [How it works](../concepts.md).
- **`$${VAR}` escape.** `$${VAR}` becomes the literal text `${VAR}`, for values where Keycloak
  itself expects that syntax.
- **An unset variable is left as it is.** The provisioner doesn't fail: an unset `${VAR}` stays
  the literal text `${VAR}`, and that text is what Keycloak stores, for example as a client
  secret. It logs a warning for each such variable when the config loads; 1.13.0 and earlier
  stay silent.
- **Environment only.** The provisioner reads secrets from environment variables only. There
  are no `_FILE` variants, and the image is built `FROM scratch` without a shell, so a Docker
  or Compose secret mounted as a file under `/run/secrets` can't be read; hand the value to
  the container as an environment variable instead. The admin credentials work the same way:
  `KEYCLOAK_USER` and `KEYCLOAK_PASSWORD` (see [Deployment](../deployment.md)).

## Steps

The example declares a confidential client `billing-api` in a realm `billing`.

### 1. Replace each secret with a `${VAR}` reference

`billing.yaml`:

```yaml
realms:
  - realm: "billing"
    enabled: true
    clients:
      - clientId: "billing-api"
        name: "$${client_billing_api}"
        publicClient: false
        serviceAccountsEnabled: true
        secret: "${BILLING_API_CLIENT_SECRET}"
```

`secret` comes from `BILLING_API_CLIENT_SECRET`. Only the `${NAME}` form with braces is
expanded: `$NAME` stays as it is, and so does a shell-style default such as
`${NAME:-fallback}`, because the provisioner looks up a variable literally named
`NAME:-fallback`.

### 2. Use `$${…}` where Keycloak expects `${…}`

Keycloak treats a client `name` of the form `${key}` as a message key and shows the
translated text in its consoles; its built-in clients are named that way, for example
`${client_account}` for `account` (Keycloak behaviour, not checked by the provisioner). The
example writes `$${client_billing_api}`, so Keycloak receives `${client_billing_api}` instead
of an expanded, and probably unset, variable.

### 3. Hand the values to the provisioner through the environment

On the command line:

```console
$ export BILLING_API_CLIENT_SECRET=<client-secret>
$ KEYCLOAK_URL=http://localhost:8080 KEYCLOAK_USER=admin KEYCLOAK_PASSWORD=<admin-password> \
    keycloak-provisioner --config billing.yaml
```

In Docker Compose, let Compose take the values from your shell or from an `.env` file next to
the Compose file, so the file itself holds no secrets. The `:?` form makes Compose refuse to
start when the variable is missing (Docker Compose behaviour, see
[Interpolation](https://docs.docker.com/reference/compose-file/interpolation/)), which closes
the unset-variable trap before the provisioner runs:

```yaml
  keycloak-provisioner:
    image: ghcr.io/datarocks-ag/keycloak-provisioner:1.13
    environment:
      KEYCLOAK_URL: http://keycloak:8080
      KEYCLOAK_USER: ${KEYCLOAK_USER:?set KEYCLOAK_USER}
      KEYCLOAK_PASSWORD: ${KEYCLOAK_PASSWORD:?set KEYCLOAK_PASSWORD}
      KEYCLOAK_CONFIG_PATH: /config.yaml
      BILLING_API_CLIENT_SECRET: ${BILLING_API_CLIENT_SECRET:?set BILLING_API_CLIENT_SECRET}
    volumes:
      - ./billing.yaml:/config.yaml:ro
```

These `${…}` are read by Compose, not by the provisioner: Compose substitutes them in the
Compose file before the container starts.

In Kubernetes, reference a Secret key from the container's `env`:

```yaml
          env:
            - name: BILLING_API_CLIENT_SECRET
              valueFrom: { secretKeyRef: { name: billing-api-client, key: secret } }
```

A `secretKeyRef` to a missing Secret or key keeps the container from starting unless it is
marked `optional` (Kubernetes behaviour, see
[Secrets as environment variables](https://kubernetes.io/docs/concepts/configuration/secret/#using-secrets-as-environment-variables)).
The full manifests are in [Run the provisioner as an init container](run-as-init-container.md).

## Check that it works

The log never shows secret values, so check what Keycloak stored. Get an admin token and read
the client:

```console
$ TOKEN=$(curl -s -d client_id=admin-cli -d username=admin -d password=<admin-password> \
    -d grant_type=password http://localhost:8080/realms/master/protocol/openid-connect/token | jq -r .access_token)
$ curl -s -H "Authorization: Bearer $TOKEN" \
    "http://localhost:8080/admin/realms/billing/clients?clientId=billing-api" | jq '.[0] | {clientId, name, secret}'
{
  "clientId": "billing-api",
  "name": "${client_billing_api}",
  "secret": "s3cr3t-from-env"
}
```

`secret` is the value of `BILLING_API_CLIENT_SECRET` (`s3cr3t-from-env` when this was
captured against Keycloak 26.6.4), and `name` kept its `${…}` thanks to the `$$` escape. Run
this against a development or staging server; on production, check the client's
**Credentials** tab in the admin console instead of printing the secret to a terminal.

## Pitfalls

- **An unset variable becomes the value.** The run succeeds and logs only a warning:

  ```json
  {"time":"…","level":"WARN","msg":"Environment variable is not set; ${BILLING_API_CLIENT_SECRET} is kept as literal text","variable":"BILLING_API_CLIENT_SECRET"}
  ```

  Captured with `BILLING_API_CLIENT_SECRET` unset, the same query then returns

  ```json
  {
    "clientId": "billing-api",
    "name": "${client_billing_api}",
    "secret": "${BILLING_API_CLIENT_SECRET}"
  }
  ```

  Anyone who reads the config now knows the client secret. The same happens to a user
  `password` or a TOTP `secret`. Guard against it outside the provisioner, with Compose's
  `${VAR:?…}` or a required `secretKeyRef` as in step 3, and rotate the secret if it happened
  (with the default `update` strategy, setting the variable and re-running replaces the
  client secret, because a declared secret is sent on every update).
- **An empty variable is not the same as an unset one.** `BILLING_API_CLIENT_SECRET=` expands
  to an empty string, and an empty client `secret` is not sent at all: an existing client
  keeps the secret it has, and Keycloak generates one for a new client (Keycloak behaviour,
  not checked by the provisioner).
- **Typos in the variable name** behave like an unset variable. Search the log for
  `Environment variable is not set`, and run the check above after the first deployment to
  each environment. A value where Keycloak itself expects `${…}`, such as a message key in a
  client `name`, warns too unless you write it as `$${…}`.
- **Not every field is expanded.** A `${VAR}` in `strategy`, a client scope `type`, an
  `acrLoaMap` key, a management permission scope name or a protocol mapper config key stays
  literal text. See the
  [Configuration reference](../configuration.md).
- **Secrets in the container environment** are visible to anyone who may inspect the
  container or pod spec. Restrict that access the same way as access to the Secret.

## Further reading

- [Configuration reference](../configuration.md): which fields support `${VAR}`.
- [How it works](../concepts.md): when expansion happens.
- [Deployment](../deployment.md): environment variables for the provisioner itself.
- [Docker Compose interpolation](https://docs.docker.com/reference/compose-file/interpolation/)
  and [Kubernetes Secrets](https://kubernetes.io/docs/concepts/configuration/secret/).
