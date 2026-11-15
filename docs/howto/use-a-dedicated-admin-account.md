# Provision with a dedicated admin account

After this the provisioner logs in with an account of its own, created by the provisioner
itself, that may create realms and manage the realms you name, and nothing else in `master`.

## When you need this

So far the provisioner logs in as the bootstrap administrator, the account you created with
`KC_BOOTSTRAP_ADMIN_USERNAME`. That account can do everything on the server, and its password
now sits in every environment that runs the provisioner. Keycloak also treats the bootstrap
administrator as temporary and asks you to replace it with a permanent one (Keycloak
behaviour, not checked by the provisioner). You want an account for the provisioner that you
can rotate or disable on its own, and that can't touch realms it has no business with.

## What you need to know first

- **The provisioner logs in to `master`.** It uses `KEYCLOAK_USER` and `KEYCLOAK_PASSWORD` to
  log in to the `master` realm through the `admin-cli` client, so the account is a user of
  `master`. See [Connection retry](../deployment.md) for the login.
- **`masterRealm.users`** declares users of the `master` realm, with the same fields as users
  of any other realm, including `roles`. The provisioner can therefore create its own account.
  See the [Configuration reference](../configuration.md).
- **Admin roles in `master`.** The realm role `admin` allows everything; `create-realm` allows
  creating realms. Each realm also has a management client in `master` named
  `<realm>-realm` (`master-realm` for `master` itself) whose client roles, such as
  `manage-users`, grant admin rights on that one realm (Keycloak behaviour; see the
  [Server Administration Guide](https://www.keycloak.org/docs/latest/server_admin/)). The table
  of what the provisioner needs is in [Admin account permissions](../deployment.md).
- **Creating a realm grants rights on it.** When an account holding `create-realm` creates a
  realm, Keycloak gives it all roles of that realm's `<realm>-realm` client (Keycloak
  behaviour, observed on 26.6.4 and shown in [Check that it works](#check-that-it-works)).

## Steps

The example creates an account `provisioner` that may create new realms and manage the
existing realm `shop`.

### 1. Declare the account and create it as the bootstrap administrator

`provisioner-account.yaml`:

```yaml
masterRealm:
  users:
    - username: "provisioner"
      password: "${PROVISIONER_PASSWORD}"
      enabled: true
      roles:
        realm:
          - "create-realm"
        clients:
          master-realm:
            - "view-realm"
```

`create-realm` lets the account create new realms. `view-realm` on `master-realm` lets it read
the provider and required action lists the [compatibility check](../deployment.md) validates
against; without it the check skips that part (see [Pitfalls](#pitfalls)).

Run it once with the bootstrap administrator:

```console
$ export PROVISIONER_PASSWORD=<provisioner-password>
$ KEYCLOAK_URL=http://localhost:8080 KEYCLOAK_USER=admin KEYCLOAK_PASSWORD=<bootstrap-password> \
    keycloak-provisioner --config provisioner-account.yaml
```

Captured against Keycloak 26.6.4:

```json
{"time":"…","level":"INFO","msg":"Creating user","realm":"master","username":"provisioner"}
{"time":"…","level":"INFO","msg":"Setting user password","realm":"master","username":"provisioner"}
{"time":"…","level":"INFO","msg":"Assigning realm roles","realm":"master","subject":"users","subjectName":"provisioner","count":1}
{"time":"…","level":"INFO","msg":"Assigning client roles","realm":"master","subject":"users","subjectName":"provisioner","client":"master-realm","count":1}
{"time":"…","level":"INFO","msg":"Provisioning complete"}
{"time":"…","level":"INFO","msg":"keycloak-provisioner finished successfully"}
```

Keep this config separate from your application configs, and keep running it as the
bootstrap administrator (or another account holding `admin`): the new account may not create
users in `master` or grant roles there.

### 2. Grant roles on realms that already exist

The account gets rights on the realms it creates, but not on realms that existed before.
For each of those, add roles of its `<realm>-realm` client. For `shop`, which has realm
settings, a client, roles and users:

```yaml
masterRealm:
  users:
    - username: "provisioner"
      password: "${PROVISIONER_PASSWORD}"
      enabled: true
      roles:
        realm:
          - "create-realm"
        clients:
          master-realm:
            - "view-realm"
          shop-realm:
            - "manage-realm"
            - "manage-clients"
            - "manage-users"
```

Run it again as the bootstrap administrator. Only the new grants are added (captured):

```json
{"time":"…","level":"INFO","msg":"Updating user","realm":"master","username":"provisioner"}
{"time":"…","level":"INFO","msg":"Setting user password","realm":"master","username":"provisioner"}
{"time":"…","level":"INFO","msg":"Assigning client roles","realm":"master","subject":"users","subjectName":"provisioner","client":"shop-realm","count":3}
{"time":"…","level":"INFO","msg":"Provisioning complete"}
{"time":"…","level":"INFO","msg":"keycloak-provisioner finished successfully"}
```

These three roles covered realm settings, clients, roles and users of `shop` on
Keycloak 26.6.4. A realm that also declares identity providers, organizations or management
permissions needs the matching roles of `<realm>-realm`, such as
`manage-identity-providers` or `manage-authorization` (Keycloak behaviour, not checked by the
provisioner); a missing one stops the run with `unexpected status 403`.

### 3. Switch the provisioner to the new account

Set `KEYCLOAK_USER` and `KEYCLOAK_PASSWORD` to the new account wherever the provisioner runs
your application configs:

```console
$ KEYCLOAK_URL=http://localhost:8080 KEYCLOAK_USER=provisioner KEYCLOAK_PASSWORD=<provisioner-password> \
    keycloak-provisioner --config shop.yaml
```

Store the password as described in
[Keep secrets out of the config file](keep-secrets-out-of-the-config.md).

## Check that it works

Run your application config as the new account. Captured with this config, `shop.yaml`, on a
realm `shop` the bootstrap administrator had created earlier:

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

```json
{"time":"…","level":"INFO","msg":"Detected Keycloak version","version":"26.6.4"}
{"time":"…","level":"INFO","msg":"Starting provisioning"}
{"time":"…","level":"INFO","msg":"Updating realm","realm":"shop"}
{"time":"…","level":"INFO","msg":"Updating client","realm":"shop","clientId":"shop-web","uuid":"7a08ba97-2c18-4344-aaeb-54e26d6bf0f5"}
{"time":"…","level":"INFO","msg":"Updating realm role","realm":"shop","role":"customer"}
{"time":"…","level":"INFO","msg":"Updating realm role","realm":"shop","role":"support"}
{"time":"…","level":"INFO","msg":"Updating user","realm":"shop","username":"alice"}
{"time":"…","level":"INFO","msg":"Provisioning complete"}
{"time":"…","level":"INFO","msg":"keycloak-provisioner finished successfully"}
```

No `WARN` line between `Detected Keycloak version` and `Starting provisioning` means the
compatibility check ran in full. A new realm, here `warehouse`, is created the same way, and
afterwards the account holds all roles of `warehouse-realm`. To see the account's roles,
query its role mappings with the bootstrap administrator's token
(see [Keep secrets out of the config file](keep-secrets-out-of-the-config.md) for getting
one):

```console
$ USER_ID=$(curl -s -H "Authorization: Bearer $TOKEN" \
    "http://localhost:8080/admin/realms/master/users?username=provisioner&exact=true" | jq -r '.[0].id')
$ curl -s -H "Authorization: Bearer $TOKEN" \
    "http://localhost:8080/admin/realms/master/users/$USER_ID/role-mappings" \
    | jq '{realm: [.realmMappings[].name], clients: (.clientMappings | map_values([.mappings[].name] | sort))}'
{
  "realm": [
    "create-realm",
    "default-roles-master"
  ],
  "clients": {
    "warehouse-realm": [
      "create-client",
      "manage-authorization",
      "manage-clients",
      "manage-events",
      "manage-identity-providers",
      "manage-realm",
      "manage-users",
      "query-clients",
      "query-groups",
      "query-realms",
      "query-users",
      "view-authorization",
      "view-clients",
      "view-events",
      "view-identity-providers",
      "view-realm",
      "view-users"
    ],
    "master-realm": [
      "view-realm"
    ],
    "shop-realm": [
      "manage-clients",
      "manage-realm",
      "manage-users"
    ]
  }
}
```

## Pitfalls

- **Without `view-realm` on `master-realm`, validation is skipped.** An account holding only
  `create-realm` still provisions, but the compatibility check can't read the provider and
  required action lists. Captured:

  ```json
  {"time":"…","level":"WARN","msg":"Not permitted to read what the server offers; skipping provider and required action validation","error":"reading required actions: unexpected status 403: {\"error\":\"HTTP 403 Forbidden\"}"}
  ```

  A misspelled authenticator, protocol mapper, identity provider type or required action is
  then no longer caught before the run, and a misspelled required action is silently
  dropped by Keycloak.
- **A realm the account has no roles on fails at the first write.** Captured with
  `create-realm` only, against the existing `shop`:

  ```json
  {"time":"…","level":"ERROR","msg":"Provisioning failed","error":"provisioning realm \"shop\": ensuring realm: unexpected status 403: {\"error\":\"HTTP 403 Forbidden\"}"}
  ```

  Grant the roles of `shop-realm` as in step 2. A dry run fails the same way, at the first
  read it may not do.
- **The `<realm>-realm` client exists only once the realm does.** Granting roles on a realm
  that doesn't exist yet fails the account run (captured):

  ```json
  {"time":"…","level":"ERROR","msg":"Provisioning failed","error":"ensuring user \"provisioner\" in master realm: resolving client for users \"provisioner\": client \"inventory-realm\" not found in realm \"master\""}
  ```

  Let the new account create the realm instead; it then holds its roles already.
- **The password is reset on every run of the account config.** `password` is applied on
  every run, so change `PROVISIONER_PASSWORD` and the provisioner's `KEYCLOAK_PASSWORD`
  together. `initialPassword` is no alternative here: it is temporary, and Keycloak refuses a
  password-grant login for an account that must still change its password (Keycloak
  behaviour, not checked by the provisioner).
- **Changing `masterRealm.users` needs more than this account has.** The account run fails
  with `unexpected status 403` when the new account runs it. Creating users in `master` needs
  `manage-users` on `master-realm` (observed on 26.6.4); rather than granting that to the
  provisioner's own account, keep the account config with the bootstrap administrator.

## Further reading

- [Admin account permissions](../deployment.md) in the deployment guide.
- [Configuration reference](../configuration.md): `masterRealm` and user fields.
- [Keycloak Server Administration Guide](https://www.keycloak.org/docs/latest/server_admin/):
  admin roles and the `<realm>-realm` clients.
- [Keep secrets out of the config file](keep-secrets-out-of-the-config.md)
