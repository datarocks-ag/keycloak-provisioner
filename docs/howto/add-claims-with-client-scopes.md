# Add claims and audiences with client scopes

After this you can declare client scopes that add an audience and a custom claim to access
tokens, and attach them to a client as default or optional scopes.

## When you need this

Your orders API accepts only tokens whose `aud` (audience) claim names it, and your services
expect a `tenant` claim. You could put protocol mappers on every client, but then each client
repeats them. A client scope holds the mappers once; each client that should get the claims
lists the scope, and a client that only sometimes needs the audience can request it.

## What you need to know first

- **Client scope.** A named set of protocol mappers (and role scope mappings) that Keycloak
  applies to the tokens of every client the scope is attached to. See the
  [Keycloak Server Administration Guide](https://www.keycloak.org/docs/latest/server_admin/).
- **Protocol mapper.** A rule that puts a claim into a token. `oidc-audience-mapper` adds a
  client to `aud`; `oidc-hardcoded-claim-mapper` adds a fixed claim. The `config` keys are
  Keycloak's own and are passed through as written.
- **Default and optional.** A *default* scope is applied to every token the client gets. An
  *optional* scope is applied only when the token request names it in the `scope` parameter.
- **Two places to assign.** On the client, `defaultClientScopes` and `optionalClientScopes`
  attach a scope to that client. On the scope, `type: default` or `type: optional` adds it to the
  realm's lists, which Keycloak uses for clients created afterwards. See the
  [Configuration reference](../configuration.md).

## Steps

The names `scopes`, `orders:read`, `tenant`, `orders-api`, `orders-app` and `alice` are
examples; replace them with your own. The three snippets together make `scopes.yaml`.

### 1. Declare the client scopes with their mappers

`orders:read` adds `orders-api` to the audience of access tokens. Its `type: optional` also puts
it on the realm's optional list. `tenant` adds a fixed `tenant` claim and stays off the realm
lists (`type` defaults to `none`). Every mapper needs `name`, `protocol` and `protocolMapper`;
the provisioner checks before writing anything that the server offers that mapper type.

```yaml
realms:
  - realm: "scopes"
    enabled: true

    clientScopes:
      - name: "orders:read"
        description: "Read access to the orders API"
        type: "optional"
        attributes:
          "include.in.token.scope": "true"
        protocolMappers:
          - name: "orders-audience"
            protocol: "openid-connect"
            protocolMapper: "oidc-audience-mapper"
            config:
              "included.client.audience": "orders-api"
              "access.token.claim": "true"
              "id.token.claim": "false"

      - name: "tenant"
        description: "Adds the tenant claim"
        protocolMappers:
          - name: "tenant-claim"
            protocol: "openid-connect"
            protocolMapper: "oidc-hardcoded-claim-mapper"
            config:
              "claim.name": "tenant"
              "claim.value": "acme"
              "jsonType.label": "String"
              "access.token.claim": "true"
              "id.token.claim": "true"
              "userinfo.token.claim": "true"
```

### 2. Attach the scopes to the client

`orders-app` always gets the `tenant` claim and gets the audience only when it asks for
`orders:read`. The provisioner creates client scopes before clients, so a client can name a
scope from the same file. The API itself is a client too: the audience mapper names it by
`clientId`.

```yaml
    clients:
      - clientId: "orders-api"
        publicClient: false
        secret: "${ORDERS_API_SECRET}"
        standardFlowEnabled: false

      - clientId: "orders-app"
        publicClient: true
        directAccessGrantsEnabled: true   # only so you can test logins with curl below
        defaultClientScopes:
          - "tenant"
        optionalClientScopes:
          - "orders:read"
```

The lists only add: Keycloak's built-in scopes such as `profile`, `email` and `roles` stay
attached. A scope you list on the wrong side is moved to the side you list it on.

### 3. Declare a user to test with

```yaml
    users:
      - username: "alice"
        password: "${ALICE_PASSWORD}"
        enabled: true
        email: "alice@example.com"
        firstName: "Alice"
        lastName: "Example"
```

### 4. Run the provisioner

```console
$ ORDERS_API_SECRET='<orders-api-secret>' ALICE_PASSWORD='alice-S3cret!' \
  KEYCLOAK_URL=http://localhost:8080 KEYCLOAK_USER=admin KEYCLOAK_PASSWORD=<admin-password> \
  keycloak-provisioner --config scopes.yaml
…
{"time":"…","level":"INFO","msg":"Creating client scope","realm":"scopes","clientScope":"orders:read"}
{"time":"…","level":"INFO","msg":"Creating protocol mapper","realm":"scopes","container":"client-scopes","containerId":"af8b6518-de02-4939-816f-6da41f77c5bb","containerName":"orders:read","mapper":"orders-audience"}
{"time":"…","level":"INFO","msg":"Assigning client scope to realm","realm":"scopes","clientScope":"orders:read","type":"optional"}
{"time":"…","level":"INFO","msg":"Creating client scope","realm":"scopes","clientScope":"tenant"}
{"time":"…","level":"INFO","msg":"Creating protocol mapper","realm":"scopes","container":"client-scopes","containerId":"a3b42b8e-4469-4957-a35f-f4808ba2460c","containerName":"tenant","mapper":"tenant-claim"}
{"time":"…","level":"INFO","msg":"Creating client","realm":"scopes","clientId":"orders-api"}
{"time":"…","level":"INFO","msg":"Creating client","realm":"scopes","clientId":"orders-app"}
{"time":"…","level":"INFO","msg":"Assigning client scope","realm":"scopes","clientId":"orders-app","clientScope":"tenant","type":"default"}
{"time":"…","level":"INFO","msg":"Creating user","realm":"scopes","username":"alice"}
…
{"time":"…","level":"INFO","msg":"keycloak-provisioner finished successfully"}
```

There is no `Assigning client scope` line for `orders:read`: `orders-app` was created after the
scope joined the realm's optional list, so Keycloak had already attached it as optional.

## Check that it works

Sign in as `alice` without asking for a scope, then with `scope=orders:read`, and decode the
access token's payload each time. The `jq` filter turns the token's base64url-encoded middle
part back into JSON (tested with jq 1.8.2). Run this against a test realm only; the password
grant must be enabled on the client.

```console
$ curl -s -d client_id=orders-app -d username=alice -d 'password=alice-S3cret!' \
    -d grant_type=password \
    http://localhost:8080/realms/scopes/protocol/openid-connect/token \
  | jq -r .access_token \
  | jq -R 'split(".")[1] | gsub("-";"+") | gsub("_";"/") | @base64d | fromjson | {aud, scope, tenant}'
{
  "aud": "account",
  "scope": "tenant profile email",
  "tenant": "acme"
}
$ curl -s -d client_id=orders-app -d username=alice -d 'password=alice-S3cret!' \
    -d grant_type=password -d 'scope=orders:read' \
    http://localhost:8080/realms/scopes/protocol/openid-connect/token \
  | jq -r .access_token \
  | jq -R 'split(".")[1] | gsub("-";"+") | gsub("_";"/") | @base64d | fromjson | {aud, scope, tenant}'
{
  "aud": [
    "orders-api",
    "account"
  ],
  "scope": "tenant profile email orders:read",
  "tenant": "acme"
}
```

The `tenant` claim is in both tokens, `orders-api` is in `aud` only when requested. To see
what is attached to the client, ask the admin API (`$ADMIN_TOKEN` as in
[Provision users with passwords, roles and groups](provision-users.md)):

```console
$ KC=http://localhost:8080/admin/realms/scopes
$ CLIENT_UUID=$(curl -s -H "Authorization: Bearer $ADMIN_TOKEN" "$KC/clients?clientId=orders-app" | jq -r '.[0].id')
$ for t in default optional; do
    echo "$t: $(curl -s -H "Authorization: Bearer $ADMIN_TOKEN" "$KC/clients/$CLIENT_UUID/$t-client-scopes" | jq -c '[.[].name]|sort')"
  done
default: ["acr","basic","email","profile","roles","tenant","web-origins"]
optional: ["address","microprofile-jwt","offline_access","orders:read","organization","phone"]
```

## Pitfalls

- **A misspelled scope on a client is only a warning.** The run continues without it:
  `{"level":"WARN","msg":"Client scope not found, skipping assignment","realm":"scopes","clientId":"orders-app","clientScope":"orders:write","type":"default"}`.
  Search the log for `WARN` after a run.
- **Moving a scope between default and optional is logged as two steps.** Listing `orders:read`
  under `defaultClientScopes` instead detaches it first:
  `Detaching client scope to change its type` (`"type":"optional"`), then
  `Assigning client scope` (`"type":"default"`). Changing a scope's own `type` does the same on
  the realm lists: `Detaching client scope from realm to change its type`, then
  `Assigning client scope to realm`.
- **A scope in both lists is rejected.** The config fails to load:
  `validating config: realms[0].clients[1]: client scope "orders:read" is listed as both default and optional`.
- **Removing a scope from the config detaches nothing.** Neither the client lists nor the realm
  lists ever lose an entry, and `type: none` only stops the provisioner from managing the realm
  assignment. Detach a scope in Keycloak.
- **The realm `type` does not reach existing clients.** It decides what Keycloak attaches to
  clients created later; switching `orders:read` to `type: default` left it optional on the
  existing `orders-api` (Keycloak behaviour, observed on 26.6.4). For a client that already exists, list the scope in its
  `defaultClientScopes` or `optionalClientScopes`.
- **`strategy: create` leaves existing scopes and mappers as they are.** A changed mapper
  `config` is not applied to an existing mapper; the realm and client assignments are still
  applied.

## Further reading

- [Configuration reference](../configuration.md) for every client scope, protocol mapper and
  client field.
- [Set up a machine-to-machine client](set-up-a-service-account-client.md) for clients that get
  tokens without a user.
- [Let a client exchange tokens for another audience](set-up-token-exchange.md), which relies
  on audiences.
- [Client scopes](https://www.keycloak.org/docs/latest/server_admin/) and protocol mappers in
  the Keycloak Server Administration Guide.
