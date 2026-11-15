# Broker logins through an external identity provider

After this your realm offers "Sign in with Corporate SSO", creates a local user on the first
login, and keeps the provider's client secret out of the config file.

## When you need this

Your users already have accounts at a corporate OpenID Connect (OIDC) provider, and your
applications trust only your Keycloak realm. Instead of creating passwords in Keycloak, you
want Keycloak to send users to the corporate provider, accept who it says they are, and copy a
few attributes across. The provider's settings should come from the config like the rest of the
realm, so every environment gets the same setup.

## What you need to know first

- **Identity brokering.** Keycloak acts as a client of another identity provider (IdP): the
  browser is sent to the IdP to sign in, and Keycloak then exchanges the returned code for
  tokens itself. See
  [Identity brokering](https://www.keycloak.org/docs/latest/server_admin/) in the Server
  Administration Guide.
- **First broker login flow.** The authentication flow Keycloak runs the first time someone
  signs in through a provider, to create or link the local user. `firstBrokerLoginFlowAlias`
  names it; every realm has one called `first broker login`.
- **Identity provider mappers.** Copy claims from the provider's tokens onto the local user. A
  mapper's type (`identityProviderMapper`) must be one the server offers.
- **Updates merge.** Keycloak replaces an identity provider's whole representation on update,
  so the provisioner reads the current one and merges the config over it. Settings and config
  keys you do not declare survive, and so does the stored client secret, which Keycloak returns
  masked as `**********`. A mapper's `config` is the exception: it is replaced from the config.
  See [Configuration reference](../configuration.md).

## Steps

The example uses a second realm, `upstream`, on the same Keycloak as a stand-in for the
corporate provider, so you can try it end to end. In production you declare only the second
realm, `broker`, and take the URLs and client credentials from your provider. The names
are examples; the snippets together make `idp.yaml`.

### 1. Register your Keycloak at the provider

The provider needs a client for your realm, whose redirect URI is the broker endpoint of your
realm: `<keycloak-url>/realms/<realm>/broker/<alias>/endpoint`. Here the stand-in realm
declares it, with one user to sign in with:

```yaml
realms:
  # Stands in for the external identity provider. In production this is
  # your corporate IdP, and only the second realm is yours.
  - realm: "upstream"
    enabled: true
    clients:
      - clientId: "broker"
        publicClient: false
        secret: "${UPSTREAM_CLIENT_SECRET}"
        redirectUris:
          - "http://localhost:8080/realms/broker/broker/upstream/endpoint"
    users:
      - username: "carol"
        enabled: true
        password: "${CAROL_PASSWORD}"
        email: "carol@corp.example.com"
        emailVerified: true
        firstName: "Carol"
        lastName: "Upstream"
```

### 2. Declare the identity provider

`alias` is part of the broker URL, so it must not contain `/`. Pass the client secret through
an environment variable; see [Keep secrets out of the config file](keep-secrets-out-of-the-config.md).

```yaml
  - realm: "broker"
    enabled: true
    identityProviders:
      - alias: "upstream"
        displayName: "Corporate SSO"
        providerId: "oidc"
        enabled: true
        trustEmail: true
        firstBrokerLoginFlowAlias: "first broker login"
        config:
          clientId: "broker"
          clientSecret: "${UPSTREAM_CLIENT_SECRET}"
          clientAuthMethod: "client_secret_post"
          issuer: "http://localhost:8080/realms/upstream"
          authorizationUrl: "http://localhost:8080/realms/upstream/protocol/openid-connect/auth"
          tokenUrl: "http://localhost:8080/realms/upstream/protocol/openid-connect/token"
          jwksUrl: "http://localhost:8080/realms/upstream/protocol/openid-connect/certs"
          useJwksUrl: "true"
          validateSignature: "true"
          defaultScope: "openid email profile"
          syncMode: "IMPORT"
```

The URLs are read by two different parties. The browser opens `authorizationUrl`; Keycloak
itself calls `tokenUrl` and `jwksUrl`. `issuer` must equal the `iss` claim in the provider's
tokens. With a provider on the same Keycloak, that is the URL the browser used. On 26.6.4, an
`issuer` that did not match ended the login on Keycloak's error page with
`Unexpected error when authenticating with identity provider`.

`config` keys are passed to Keycloak as they are: the provisioner checks `providerId` against
the server but cannot check the keys.

### 3. Add mappers

This mapper copies a `department` claim, when the provider sends one, into a user attribute:

```yaml
        mappers:
          - name: "department"
            identityProviderMapper: "oidc-user-attribute-idp-mapper"
            config:
              claim: "department"
              user.attribute: "department"
              syncMode: "INHERIT"
```

### 4. Declare the client your application uses

Nothing on the client refers to the provider; the provider belongs to the realm.

```yaml
    clients:
      - clientId: "web"
        publicClient: false
        secret: "${WEB_CLIENT_SECRET}"
        redirectUris:
          - "http://localhost:3000/*"
```

### 5. Run the provisioner

The provisioner creates identity providers after users and before organizations. For the
`broker` realm it logs:

```console
$ UPSTREAM_CLIENT_SECRET=… CAROL_PASSWORD=… WEB_CLIENT_SECRET=… \
  KEYCLOAK_URL=http://localhost:8080 KEYCLOAK_USER=admin KEYCLOAK_PASSWORD=… \
  keycloak-provisioner --config idp.yaml
…
{"time":"…","level":"INFO","msg":"Creating realm","realm":"broker"}
{"time":"…","level":"INFO","msg":"Creating client","realm":"broker","clientId":"web"}
{"time":"…","level":"INFO","msg":"Creating identity provider","realm":"broker","identityProvider":"upstream","providerId":"oidc"}
{"time":"…","level":"INFO","msg":"Creating identity provider mapper","realm":"broker","identityProvider":"upstream","mapper":"department"}
{"time":"…","level":"INFO","msg":"Provisioning complete"}
{"time":"…","level":"INFO","msg":"keycloak-provisioner finished successfully"}
```

Later runs log `Updating identity provider` and `Updating identity provider mapper` instead.

## Check that it works

Read the provider back through the admin API (`$ADMIN_TOKEN` is an admin access token). The
secret comes back masked:

```console
$ curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
    http://localhost:8080/admin/realms/broker/identity-provider/instances/upstream \
    | jq '{alias, providerId, enabled, trustEmail, firstBrokerLoginFlowAlias,
           clientId: .config.clientId, clientSecret: .config.clientSecret}'
{
  "alias": "upstream",
  "providerId": "oidc",
  "enabled": true,
  "trustEmail": true,
  "firstBrokerLoginFlowAlias": "first broker login",
  "clientId": "broker",
  "clientSecret": "**********"
}
```

Then sign in to the `web` client in a browser with `&kc_idp_hint=upstream` added to the
authorization URL, which skips Keycloak's own login page, and sign in as `carol`. On 26.6.4,
driven over HTTP rather than with a browser, the ID token afterwards had
`"preferred_username": "carol"` and `"email": "carol@corp.example.com"`, and the new local user
was linked to the provider:

```console
$ curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
    http://localhost:8080/admin/realms/broker/users/<carol-user-id>/federated-identity
[
  {
    "identityProvider": "upstream",
    "userId": "3a063428-e255-4d90-bff5-9f7e88cdac4b",
    "userName": "carol"
  }
]
```

## Pitfalls

- **An unset secret variable is written as text.** If `UPSTREAM_CLIENT_SECRET` is not set, the
  provisioner leaves `${UPSTREAM_CLIENT_SECRET}` in the value and sends that text to Keycloak
  as the secret. The run logs a `WARN` line naming the variable; 1.13.0 and earlier don't.
- **Removing `clientSecret` from the config keeps the stored secret.** The merge carries the
  masked value forward. A run with the key removed still logged `Updating identity provider`,
  and brokered logins kept working. Removing any other `config` key does not delete it either;
  change it in Keycloak.
- **A mapper type typo stops the run before anything is written:**

  ```
  {"time":"…","level":"ERROR","msg":"Unsupported by this Keycloak server","capability":"oidc-user-atribute-idp-mapper","reason":"identity provider mapper type \"oidc-user-atribute-idp-mapper\" is not available on this server (did you mean \"oidc-user-attribute-idp-mapper\"?)","usedAt":"realms[1].identityProviders[0].mappers[0].identityProviderMapper"}
  ```

  This check, and the one for `providerId`, is skipped when the admin account may not read what
  the server offers.
- **A wrong broker flow alias warns, then Keycloak fails.** The provisioner warns about an alias
  it cannot see in the realm's flows, and Keycloak then rejects the write without saying why:

  ```
  {"time":"…","level":"WARN","msg":"Broker login flow not visible in the current flow listing","realm":"broker","flow":"first-broker-login","identityProviders":["upstream"]}
  {"time":"…","level":"ERROR","msg":"Provisioning failed","error":"provisioning realm \"broker\": ensuring identity providers: ensuring identity provider \"upstream\": unexpected status 500: {\"error\":\"unknown_error\",\"error_description\":\"For more on this error consult the server log.\"}"}
  ```

  The built-in flow's alias has spaces: `first broker login`.
- **`strategy: create` skips existing providers and mappers.** Only missing ones are created.
- **Nothing is deleted.** A provider or mapper removed from the config stays in Keycloak.

## Further reading

- [Configuration reference](../configuration.md) for `identityProviders`.
- [Onboard an organization](onboard-an-organization.md) to link a provider to an organization.
- [Server Administration Guide](https://www.keycloak.org/docs/latest/server_admin/) on identity
  brokering and OIDC providers.
