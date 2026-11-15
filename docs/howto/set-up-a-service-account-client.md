# Set up a machine-to-machine client

After this you can declare a client that a backend job or service uses to get its own access
token with the client credentials grant, carrying the realm and client roles you gave it.

## When you need this

A nightly export job calls your reports API. No person signs in, so there is no user whose
token the job could use. The job needs an identity of its own, a secret to prove it, and
exactly the roles the API checks, the same in every environment.

## What you need to know first

- **Confidential client.** A client that holds a secret and authenticates with it at the token
  endpoint (`publicClient: false`). Only a confidential client can use the client credentials
  grant.
- **Client credentials grant.** The OAuth 2.0 flow in which a client exchanges its own id and
  secret for an access token, with no user involved. See the
  [Keycloak Server Administration Guide](https://www.keycloak.org/docs/latest/server_admin/).
- **Service account.** With `serviceAccountsEnabled: true`, Keycloak gives the client a hidden
  user named `service-account-<clientId>`. Tokens from the client credentials grant are issued
  for that user, so its roles are the roles in the token.
- **`serviceAccountRoles`.** The provisioner grants these roles to the service account user. It
  takes the same `realm` and `clients` lists as a user's `roles`, and like them it only adds.
  See the [Configuration reference](../configuration.md).

## Steps

The names `m2m`, `reports-api`, `reports-read`, `batch-job` and `nightly-export` are
examples; replace them with your own. The two snippets together make `m2m.yaml`.

### 1. Declare the roles the job needs

The API the job calls is a client with a client role; the realm role is for checks across
applications. Both must exist before the service account can get them, so declare them in the
same realm. The provisioner assigns service account roles after all roles exist, so the order
in the file does not matter.

```yaml
realms:
  - realm: "m2m"
    enabled: true

    roles:
      - name: "batch-job"
        description: "Machine callers that run scheduled jobs"

    clients:
      - clientId: "reports-api"
        publicClient: false
        secret: "${REPORTS_API_SECRET}"
        standardFlowEnabled: false
        clientRoles:
          - name: "reports-read"
```

### 2. Declare the machine client with its service account roles

The client is confidential, has a service account, and turns off the flows a person would use.
`secret` fixes the client secret so the job's deployment can be given the same value; pass it
through an environment variable.

```yaml
      - clientId: "nightly-export"
        name: "Nightly export job"
        publicClient: false
        secret: "${NIGHTLY_EXPORT_SECRET}"
        standardFlowEnabled: false
        directAccessGrantsEnabled: false
        serviceAccountsEnabled: true
        serviceAccountRoles:
          realm:
            - "batch-job"
          clients:
            reports-api:            # the clientId of the client that owns the role
              - "reports-read"
```

`serviceAccountRoles` requires `serviceAccountsEnabled: true` on the same client; without it
the config is rejected when it loads.

### 3. Run the provisioner

```console
$ REPORTS_API_SECRET='<reports-api-secret>' NIGHTLY_EXPORT_SECRET='export-S3cret' \
  KEYCLOAK_URL=http://localhost:8080 KEYCLOAK_USER=admin KEYCLOAK_PASSWORD=<admin-password> \
  keycloak-provisioner --config m2m.yaml
…
{"time":"…","level":"INFO","msg":"Creating client","realm":"m2m","clientId":"reports-api"}
{"time":"…","level":"INFO","msg":"Creating client role","realm":"m2m","clientUUID":"4391a510-6abc-4793-bd11-3e93a46d46e9","role":"reports-read"}
{"time":"…","level":"INFO","msg":"Creating client","realm":"m2m","clientId":"nightly-export"}
{"time":"…","level":"INFO","msg":"Creating realm role","realm":"m2m","role":"batch-job"}
{"time":"…","level":"INFO","msg":"Ensuring service account roles","realm":"m2m","clientId":"nightly-export","serviceAccountUserId":"219d3893-d8e8-44d6-ac54-af9a2b2490d9"}
{"time":"…","level":"INFO","msg":"Assigning realm roles","realm":"m2m","subject":"users","subjectName":"service-account-nightly-export","count":1}
{"time":"…","level":"INFO","msg":"Assigning client roles","realm":"m2m","subject":"users","subjectName":"service-account-nightly-export","client":"reports-api","count":1}
{"time":"…","level":"INFO","msg":"Provisioning complete"}
{"time":"…","level":"INFO","msg":"keycloak-provisioner finished successfully"}
```

## Check that it works

Request a token the way the job will, with the client id and secret as HTTP basic
authentication:

```console
$ curl -s -u 'nightly-export:export-S3cret' -d grant_type=client_credentials \
    http://localhost:8080/realms/m2m/protocol/openid-connect/token \
  | jq '{token_type, expires_in, scope, refresh_token}'
{
  "token_type": "Bearer",
  "expires_in": 300,
  "scope": "email profile",
  "refresh_token": null
}
```

There is no refresh token: the job asks for a new access token when the old one expires. Decode
the access token's payload to see the roles. The `jq` filter turns the token's base64url-encoded
middle part back into JSON (tested with jq 1.8.2):

```console
$ curl -s -u 'nightly-export:export-S3cret' -d grant_type=client_credentials \
    http://localhost:8080/realms/m2m/protocol/openid-connect/token \
  | jq -r .access_token \
  | jq -R 'split(".")[1] | gsub("-";"+") | gsub("_";"/") | @base64d | fromjson
           | {azp, aud, preferred_username, realm_access, resource_access}'
{
  "azp": "nightly-export",
  "aud": [
    "reports-api",
    "account"
  ],
  "preferred_username": "service-account-nightly-export",
  "realm_access": {
    "roles": [
      "default-roles-m2m",
      "offline_access",
      "uma_authorization",
      "batch-job"
    ]
  },
  "resource_access": {
    "reports-api": {
      "roles": [
        "reports-read"
      ]
    },
    "account": {
      "roles": [
        "manage-account",
        "manage-account-links",
        "view-profile"
      ]
    }
  }
}
```

`batch-job` and `reports-read` are there. Keycloak also put `reports-api` into `aud`, because
the token carries one of its client roles (Keycloak behaviour, observed on 26.6.4). To control
the audience explicitly, see [Add claims and audiences with client scopes](add-claims-with-client-scopes.md).

## Pitfalls

- **`unauthorized_client` with `Invalid client or Invalid client credentials`.** The secret is
  wrong, or the variable holding it was not set when the provisioner ran: an unset `${VAR}` is
  left as the literal text and becomes the secret.
- **`unauthorized_client` with `Client not enabled to retrieve service account`.** You requested
  a token for a client without `serviceAccountsEnabled: true`, such as `reports-api` above.
- **A missing role stops the run.** Service account roles are assigned after clients and roles
  are written, so the run fails part-way, with exit code 1:
  `{"level":"ERROR","msg":"Provisioning failed","error":"provisioning realm \"m2m\": ensuring service account roles for client \"nightly-export\": client role \"reports-reader\" not found on client \"reports-api\" in realm \"m2m\""}`.
- **`serviceAccountsEnabled must be true when serviceAccountRoles is set`.** The config fails to
  load:
  `validating config: realms[0].clients[1].serviceAccountRoles: serviceAccountsEnabled must be true when serviceAccountRoles is set`.
- **Removing a role from `serviceAccountRoles` changes nothing in Keycloak.** Roles are only
  ever added, under every `strategy`. Remove them in Keycloak, from the role mappings of the
  `service-account-<clientId>` user.

## Further reading

- [Configuration reference](../configuration.md) for every client field.
- [Keep secrets out of the config file](keep-secrets-out-of-the-config.md) for passing the
  client secret through the environment.
- [Add claims and audiences with client scopes](add-claims-with-client-scopes.md) to add an
  audience or claims to the job's tokens.
- [Let a client exchange tokens for another audience](set-up-token-exchange.md) when a service
  calls another one on behalf of a user instead.
- [Service accounts](https://www.keycloak.org/docs/latest/server_admin/) in the Keycloak Server
  Administration Guide.
