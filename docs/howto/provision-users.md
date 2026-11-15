# Provision users with passwords, roles and groups

After this you can declare users in the config with a password, realm and client roles, group
memberships and, where other systems need it, a fixed user id.

## When you need this

A test or demo realm needs the same users in every environment: an operator who can sign in
straight away, a colleague who must choose their own password on first login, both with the
roles your application checks and in the groups your team uses. Clicking them together in the
admin console does not survive a rebuild of the realm; declaring them in the config does.

## What you need to know first

- **Users are matched by `username`.** A user that does not exist is created; one that exists
  is updated (or left alone under `strategy: create`). Nothing is ever deleted. See
  [How it works](../concepts.md).
- **`password` or `initialPassword`, never both.** `password` is a permanent password that the
  provisioner sets again on every run. `initialPassword` is a temporary password set only when
  the user is created; Keycloak then makes the user change it at first login.
- **Roles and groups are additive.** The provisioner grants what the config lists and never
  takes a role or a group membership away. A realm role is defined on the realm; a client role
  belongs to one client. See the
  [Keycloak Server Administration Guide](https://www.keycloak.org/docs/latest/server_admin/).
- **Group paths.** A group is named by its path, such as `/staff/support` for the subgroup
  `support` of `staff`. The leading slash is optional.

## Steps

The names `users`, `shop-web`, `shop-user`, `orders-viewer`, `staff`, `alice` and `bob` are
examples; replace them with your own. The complete file is the sum of the three snippets below
(`users.yaml`).

### 1. Declare the roles, the client and the groups the users need

A user can only receive a role or join a group that exists. Roles that do not exist fail the
run, so declare them in the same realm. Groups are provisioned before users, so a group
declared here is there by the time the users are added to it.

```yaml
realms:
  - realm: "users"
    enabled: true

    roles:
      - name: "shop-user"
        description: "Can sign in to the shop"

    clients:
      - clientId: "shop-web"
        publicClient: true
        directAccessGrantsEnabled: true   # only so you can test logins with curl below
        clientRoles:
          - name: "orders-viewer"
            description: "Can view orders"

    groups:
      - name: "staff"
        subGroups:
          - name: "support"
```

### 2. Declare the users

`alice` gets a permanent password, a realm role, a client role, a group membership and a fixed
id. `bob` gets a temporary password and a realm role. Both carry `firstName` and `lastName`,
because Keycloak's default user profile requires them before a user can complete a login.

```yaml
    users:
      - username: "alice"
        id: "6f1c2a7e-0b5d-4c1e-9a3f-2d8e4b7c9a10"
        password: "${ALICE_PASSWORD}"
        enabled: true
        email: "alice@example.com"
        emailVerified: true
        firstName: "Alice"
        lastName: "Example"
        roles:
          realm:
            - "default-roles-users"   # see Pitfalls: a fixed id skips the default roles
            - "shop-user"
          clients:
            shop-web:                    # the clientId, not the client's internal UUID
              - "orders-viewer"
        groups:
          - "/staff/support"

      - username: "bob"
        initialPassword: "${BOB_INITIAL_PASSWORD}"
        enabled: true
        email: "bob@example.com"
        firstName: "Bob"
        lastName: "Example"
        roles:
          realm:
            - "shop-user"
```

The passwords come from environment variables, so they stay out of the file; see
[Keep secrets out of the config file](keep-secrets-out-of-the-config.md).

`id` is optional. Set it only when another system stores the user's id and must find the same
user in every environment. It must be a UUID (`8-4-4-4-12` hexadecimal digits) and applies only
when the user is created; you cannot change it afterwards. Generate it deterministically, for
example as a UUIDv5 of a fixed namespace and the username, rather than by hand.

`requiredActions` makes Keycloak ask the user for something at their next login, for example
to set up a one-time password. [Require or seed TOTP for users](require-totp-for-users.md)
shows how.

### 3. Run the provisioner

```console
$ ALICE_PASSWORD='alice-S3cret!' BOB_INITIAL_PASSWORD='change-me-1' \
  KEYCLOAK_URL=http://localhost:8080 KEYCLOAK_USER=admin KEYCLOAK_PASSWORD=<admin-password> \
  keycloak-provisioner --config users.yaml
…
{"time":"…","level":"INFO","msg":"Creating group","realm":"users","group":"staff"}
{"time":"…","level":"INFO","msg":"Creating subgroup","realm":"users","group":"support","parent":"bbd38a2b-9ef2-48dd-90aa-f04ccff17117"}
{"time":"…","level":"INFO","msg":"Creating user with a fixed id","realm":"users","username":"alice","userID":"6f1c2a7e-0b5d-4c1e-9a3f-2d8e4b7c9a10"}
{"time":"…","level":"INFO","msg":"Setting user password","realm":"users","username":"alice"}
{"time":"…","level":"INFO","msg":"Assigning realm roles","realm":"users","subject":"users","subjectName":"alice","count":2}
{"time":"…","level":"INFO","msg":"Assigning client roles","realm":"users","subject":"users","subjectName":"alice","client":"shop-web","count":1}
{"time":"…","level":"INFO","msg":"Adding user to group","realm":"users","username":"alice","group":"/staff/support"}
{"time":"…","level":"INFO","msg":"Creating user","realm":"users","username":"bob"}
{"time":"…","level":"INFO","msg":"Setting initial (temporary) password","realm":"users","username":"bob"}
{"time":"…","level":"INFO","msg":"Assigning realm roles","realm":"users","subject":"users","subjectName":"bob","count":1}
{"time":"…","level":"INFO","msg":"Provisioning complete"}
{"time":"…","level":"INFO","msg":"keycloak-provisioner finished successfully"}
```

On the next run, `alice` gets `Setting user password` again, because a `password` is reset on
every run, while `bob`'s temporary password is left alone:

```console
{"time":"…","level":"INFO","msg":"Updating user","realm":"users","username":"alice"}
{"time":"…","level":"INFO","msg":"Setting user password","realm":"users","username":"alice"}
{"time":"…","level":"INFO","msg":"Updating user","realm":"users","username":"bob"}
```

## Check that it works

Sign in as `alice` with the password grant and decode the access token's payload. The `jq`
filter turns the token's base64url-encoded middle part back into JSON (tested with jq 1.8.2):

```console
$ curl -s -d client_id=shop-web -d username=alice -d 'password=alice-S3cret!' \
    -d grant_type=password \
    http://localhost:8080/realms/users/protocol/openid-connect/token \
  | jq -r .access_token \
  | jq -R 'split(".")[1] | gsub("-";"+") | gsub("_";"/") | @base64d | fromjson
           | {sub, preferred_username, realm_access, resource_access}'
{
  "sub": "6f1c2a7e-0b5d-4c1e-9a3f-2d8e4b7c9a10",
  "preferred_username": "alice",
  "realm_access": {
    "roles": [
      "default-roles-users",
      "offline_access",
      "shop-user",
      "uma_authorization"
    ]
  },
  "resource_access": {
    "shop-web": {
      "roles": [
        "orders-viewer"
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

`sub` is the fixed id, and the realm and client roles are in the token. `bob` cannot sign in
with the password grant yet, because his temporary password has to be changed in the browser
first:

```console
$ curl -s -d client_id=shop-web -d username=bob -d 'password=change-me-1' \
    -d grant_type=password \
    http://localhost:8080/realms/users/protocol/openid-connect/token
{"error":"invalid_grant","error_description":"Account is not fully set up"}
```

Group memberships are not in the token by default, so read them through the admin API:

```console
$ ADMIN_TOKEN=$(curl -s -d client_id=admin-cli -d username=admin -d 'password=<admin-password>' \
    -d grant_type=password \
    http://localhost:8080/realms/master/protocol/openid-connect/token | jq -r .access_token)
$ curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
    http://localhost:8080/admin/realms/users/users/6f1c2a7e-0b5d-4c1e-9a3f-2d8e4b7c9a10/groups \
  | jq -c '[.[].path]'
["/staff/support"]
$ curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
    "http://localhost:8080/admin/realms/users/users?username=bob&exact=true" \
  | jq -c '.[] | {username, requiredActions}'
{"username":"bob","requiredActions":["UPDATE_PASSWORD"]}
```

Run these against a test Keycloak only: the password grant needs `directAccessGrantsEnabled` on
the client, which you would not normally enable for a browser application.

## Pitfalls

- **A user with a fixed `id` does not get the realm's default roles.** The provisioner creates
  such a user through Keycloak's partial import, which keeps the id. On Keycloak 26.6.4, a user
  created that way had no `default-roles-<realm>` mapping, while a user created without an `id`
  had it (Keycloak behaviour, observed on 26.6.4). Without it the user lacks `offline_access`,
  `uma_authorization` and the `account` client roles. List `default-roles-<realm>` under
  `roles.realm`, as `alice` does above.
- **`Account is not fully set up` on login.** Either the user still has a required action, as
  `bob` does, or the user has no `firstName` or `lastName`. A user declared without them was
  created fine but could not sign in on Keycloak 26.6.4 (Keycloak behaviour, observed on 26.6.4).
- **A misspelled group is a warning, not an error.** The run continues and the user is not a
  member:
  `{"level":"WARN","msg":"Group not found, skipping membership","realm":"users","username":"carol","group":"/staff/suport"}`.
  Search the log for `WARN` after a run.
- **A missing role stops the run half-way.** The user is already created when its roles are
  granted, so a typo leaves a user without roles and fails the run with exit code 1:
  `{"level":"ERROR","msg":"Provisioning failed","error":"provisioning realm \"users\": ensuring user \"dave\": realm role \"shop-admin\" not found in realm \"users\""}`.
  Fix the name and run again.
- **An `id` cannot be changed or added later.** If the user already exists under another id,
  the run fails:
  `user "alice" exists with id 6f1c2a7e-0b5d-4c1e-9a3f-2d8e4b7c9a10 but the config declares 00000000-0000-4000-8000-000000000001; ids cannot be changed after creation`.
  To give an existing user a fixed id, delete the user in Keycloak and run again.
- **Keep ids unique and never reuse an id another user has.** The provisioner checks that an
  `id` is a UUID but not that it is unique, neither within the config nor in the realm.
- **Removing a role or a group from the config changes nothing in Keycloak.** Roles and
  memberships are only ever added. Remove them in the admin console or through the admin API.
- **`initialPassword` does nothing for an existing user.** It is set only by the run that
  creates the user. To reset a password, use `password`, or reset it in Keycloak.

## Further reading

- [Configuration reference](../configuration.md) for every user field.
- [Grant roles through groups](grant-roles-through-groups.md) to grant roles once per group
  instead of per user.
- [Require or seed TOTP for users](require-totp-for-users.md) for `requiredActions` and seeded
  one-time password credentials.
- [How it works](../concepts.md) for matching, strategies and why nothing is removed.
- [Managing users](https://www.keycloak.org/docs/latest/server_admin/) in the Keycloak Server
  Administration Guide.
