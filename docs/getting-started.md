# Getting started

This guide takes you from an empty Keycloak to a realm an application can log users into.
Along the way you meet everything you need day to day: a realm, a confidential client, a
realm role, a group, a user, secrets from the environment, dry-run, and re-running the
provisioner without side effects.

**The project:** you're building `shop`, a small web shop. Its API needs a realm of its own,
an OpenID Connect (OIDC) client with a secret, an administrator role, a `staff` group, and one
user, Alice, who holds the role. Everything runs in Docker Compose.

You'll need Docker with Compose v2, `curl` and `jq`, and a checkout of this repository. Each
step builds on the previous one, so keep the files you create.

## Step 1: Start Keycloak

The repository's [`docker-compose.yaml`](../docker-compose.yaml) defines two services:

- `keycloak` runs `keycloak/keycloak:26.6` in development mode (`start-dev`) on port 8080,
  with the management port 9000 for health checks. The bootstrap administrator is `admin`
  with the password `adminpass`, set through `KC_BOOTSTRAP_ADMIN_USERNAME` and
  `KC_BOOTSTRAP_ADMIN_PASSWORD`.
- `keycloak-provisioner` runs `ghcr.io/datarocks-ag/keycloak-provisioner:latest`. It logs in
  as that administrator (`KEYCLOAK_USER`, `KEYCLOAK_PASSWORD`), talks to
  `http://keycloak:8080`, and only starts once Keycloak reports healthy.

The Keycloak image ships without `curl`, so the health check opens a TCP connection to
`/health/ready` on the management port with bash's `/dev/tcp` and looks for `200 OK`:

```yaml
    healthcheck:
      # The image ships without curl, so probe the management port with bash.
      test: ["CMD-SHELL", "{ printf 'GET /health/ready HTTP/1.1\\r\\nHost: localhost\\r\\nConnection: close\\r\\n\\r\\n' >&3; grep -q '200 OK' <&3; } 3<>/dev/tcp/localhost/9000"]
```

Start Keycloak and wait until it is healthy:

```console
$ docker compose up -d --wait keycloak
```

`docker compose up` without a service name would also run the provisioner with
[`config.example.yaml`](../config.example.yaml). You'll run it with your own config instead.

## Step 2: Write a config

Create `shop.yaml` next to `docker-compose.yaml`:

```yaml
realms:
  - realm: shop
    displayName: Shop
    enabled: true
    sslRequired: external

    roles:
      - name: shop-admin
        description: Manages the shop catalogue

    clients:
      - clientId: shop-api
        enabled: true
        protocol: openid-connect
        publicClient: false
        secret: ${SHOP_API_SECRET}
        directAccessGrantsEnabled: true
        redirectUris:
          - "http://localhost:3000/*"

    groups:
      - name: staff

    users:
      - username: alice
        password: ${ALICE_PASSWORD}
        enabled: true
        email: alice@example.com
        emailVerified: true
        firstName: Alice
        lastName: Example
        roles:
          realm:
            - shop-admin
        groups:
          - /staff
```

A few things to notice:

- **Secrets aren't in the file.** `${SHOP_API_SECRET}` and `${ALICE_PASSWORD}` are read from
  the environment when the config is loaded. A file without secrets can be committed and
  reviewed. If a variable is not set, the placeholder stays as literal text, with no
  warning, so the client secret would become the string `${SHOP_API_SECRET}`. Always set
  the variables you reference. [Keep secrets out of the config file](howto/keep-secrets-out-of-the-config.md)
  covers this in depth.
- **`publicClient: false` makes `shop-api` confidential**: it authenticates with its
  `secret`. `directAccessGrantsEnabled: true` lets you request a token with a username and
  password in Step 6. A real web application would use the browser login (standard flow)
  instead.
- **Users refer to groups by path** (`/staff`) and to realm roles by name. The provisioner
  creates roles and groups before users, so both can be declared in the same file.
- **Unknown fields are rejected.** The decoder is strict, so a typo fails the run before
  anything is sent to Keycloak. With `clientID` instead of `clientId`:

  ```json
  {"level":"ERROR","msg":"Failed to load configuration","error":"parsing config YAML: yaml: unmarshal errors:\n  line 12: field clientID not found in type config.Client"}
  ```

The [Configuration reference](configuration.md) lists every field.

## Step 3: Preview with a dry run

Before changing anything, ask the provisioner what it would do. `--dry-run` connects, logs
in, and reads the current state, but only **logs** each change instead of making it.

The Compose service already mounts `config.example.yaml`. Mount `shop.yaml` alongside it,
pass the two secrets, and point `--config` at the new file:

```console
$ docker compose run --rm \
    -v "$PWD/shop.yaml:/shop.yaml:ro" \
    -e SHOP_API_SECRET=shop-api-secret \
    -e ALICE_PASSWORD=alice-password \
    keycloak-provisioner --config /shop.yaml --dry-run
…
{"level":"INFO","msg":"Detected Keycloak version","version":"26.6.4"}
{"level":"INFO","msg":"Dry-run mode enabled — mutations will be logged but not applied"}
{"level":"INFO","msg":"Starting provisioning"}
{"level":"INFO","msg":"Creating realm","realm":"shop"}
{"level":"INFO","msg":"DRY-RUN: would create realm","realm":"shop"}
{"level":"INFO","msg":"Creating client","realm":"shop","clientId":"shop-api"}
{"level":"INFO","msg":"DRY-RUN: would create client","realm":"shop","clientId":"shop-api"}
{"level":"INFO","msg":"Creating realm role","realm":"shop","role":"shop-admin"}
{"level":"INFO","msg":"DRY-RUN: would create realm role","realm":"shop","role":"shop-admin"}
{"level":"INFO","msg":"Creating group","realm":"shop","group":"staff"}
{"level":"INFO","msg":"DRY-RUN: would create group","realm":"shop","group":"staff"}
{"level":"INFO","msg":"Creating user","realm":"shop","username":"alice"}
{"level":"INFO","msg":"DRY-RUN: would create user","realm":"shop","username":"alice"}
{"level":"INFO","msg":"Setting user password","realm":"shop","username":"alice"}
{"level":"INFO","msg":"DRY-RUN: would reset user password","realm":"shop","userID":"dryrun-user-4","temporary":false}
{"level":"INFO","msg":"Assigning realm roles","realm":"shop","subject":"users","subjectName":"alice","count":1}
{"level":"INFO","msg":"DRY-RUN: would assign realm roles","realm":"shop","subject":"users","subjectID":"dryrun-user-4","roles":["shop-admin"]}
{"level":"INFO","msg":"Adding user to group","realm":"shop","username":"alice","group":"/staff"}
{"level":"INFO","msg":"DRY-RUN: would add user to group","realm":"shop","userID":"dryrun-user-4","groupID":"dryrun-group-3"}
{"level":"INFO","msg":"Provisioning complete"}
{"level":"INFO","msg":"keycloak-provisioner finished successfully"}
```

Log lines are JSON. Here the `time` field is left out and the start-up lines are shortened
to `…`. Among those start-up lines is a warning, `Keycloak URL uses plain HTTP — credentials
will be sent unencrypted`: fine for this local setup, but use HTTPS anywhere else
([Connect to Keycloak over HTTPS](howto/connect-over-https.md)).

The realm doesn't exist yet, so the user and group get stand-in ids starting with
`dryrun-`. The dry run still reports the role assignment and the group membership that
depend on them, so you see the whole run at once.

## Step 4: Apply it

Run the same command without `--dry-run`:

```console
$ docker compose run --rm \
    -v "$PWD/shop.yaml:/shop.yaml:ro" \
    -e SHOP_API_SECRET=shop-api-secret \
    -e ALICE_PASSWORD=alice-password \
    keycloak-provisioner --config /shop.yaml
…
{"level":"INFO","msg":"Starting provisioning"}
{"level":"INFO","msg":"Creating realm","realm":"shop"}
{"level":"INFO","msg":"Creating client","realm":"shop","clientId":"shop-api"}
{"level":"INFO","msg":"Creating realm role","realm":"shop","role":"shop-admin"}
{"level":"INFO","msg":"Creating group","realm":"shop","group":"staff"}
{"level":"INFO","msg":"Creating user","realm":"shop","username":"alice"}
{"level":"INFO","msg":"Setting user password","realm":"shop","username":"alice"}
{"level":"INFO","msg":"Assigning realm roles","realm":"shop","subject":"users","subjectName":"alice","count":1}
{"level":"INFO","msg":"Adding user to group","realm":"shop","username":"alice","group":"/staff"}
{"level":"INFO","msg":"Provisioning complete"}
{"level":"INFO","msg":"keycloak-provisioner finished successfully"}
```

The provisioner exits with code `0`. Notice the order: the realm, then clients, then roles,
then groups, then users. Each step only refers to things an earlier step created.
[How it works](concepts.md) explains the full order and why.

## Step 5: Run it again

Run the same command a second time:

```console
$ docker compose run --rm … keycloak-provisioner --config /shop.yaml
…
{"level":"INFO","msg":"Starting provisioning"}
{"level":"INFO","msg":"Updating realm","realm":"shop"}
{"level":"INFO","msg":"Updating client","realm":"shop","clientId":"shop-api","uuid":"b110ef66-209f-4f8b-a8c8-36b9c2282b58"}
{"level":"INFO","msg":"Updating realm role","realm":"shop","role":"shop-admin"}
{"level":"INFO","msg":"Updating group","realm":"shop","group":"staff","uuid":"1830fdbc-18ea-4797-a6ef-726d3927b514"}
{"level":"INFO","msg":"Updating user","realm":"shop","username":"alice"}
{"level":"INFO","msg":"Setting user password","realm":"shop","username":"alice"}
{"level":"INFO","msg":"Provisioning complete"}
{"level":"INFO","msg":"keycloak-provisioner finished successfully"}
```

Nothing was created twice and nothing failed because it already exists. Existing resources
are *updated* to match the config, and the role assignment and group membership are left
alone because Alice already has them. This is what makes the provisioner safe to run on
every deploy: it converges Keycloak to the config.

Two things worth knowing from this output:

- **`password` is set on every run.** If Alice changes her password in Keycloak, the next
  run resets it. For a password the user should own, use `initialPassword`, which is only
  set when the user is created ([Provision users](howto/provision-users.md)).
- **Removing something from the config doesn't remove it from Keycloak.** Delete the
  `groups: [/staff]` line and Alice stays in `staff`. The provisioner adds, it doesn't take
  away. [How it works](concepts.md) lists the few exceptions.

## Step 6: Check the result

Log in as Alice through `shop-api` and look at the realm roles in her access token:

```console
$ curl -s \
    -d client_id=shop-api -d client_secret=shop-api-secret \
    -d username=alice -d password=alice-password \
    -d grant_type=password \
    http://localhost:8080/realms/shop/protocol/openid-connect/token \
  | jq -r '.access_token | split(".")[1] | gsub("-";"+") | gsub("_";"/") | @base64d | fromjson
           | {preferred_username, azp, realm_access}'
{
  "preferred_username": "alice",
  "azp": "shop-api",
  "realm_access": {
    "roles": [
      "offline_access",
      "uma_authorization",
      "default-roles-shop",
      "shop-admin"
    ]
  }
}
```

The token was issued, so the client secret matches, and `shop-admin` is among Alice's
roles. The other three are the realm's default roles, which Keycloak gives every user. With
a wrong `client_secret`, Keycloak answers
`{"error":"unauthorized_client","error_description":"Invalid client or Invalid client credentials"}`.

To check the group membership, read it through the admin API with the bootstrap
administrator:

```console
$ ADMIN_TOKEN=$(curl -s -d client_id=admin-cli -d username=admin -d password=adminpass \
    -d grant_type=password \
    http://localhost:8080/realms/master/protocol/openid-connect/token | jq -r .access_token)
$ ALICE_ID=$(curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
    "http://localhost:8080/admin/realms/shop/users?username=alice&exact=true" | jq -r '.[0].id')
$ curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
    "http://localhost:8080/admin/realms/shop/users/$ALICE_ID/groups" | jq -c '[.[].path]'
["/staff"]
```

You can also open the admin console at `http://localhost:8080/admin` and sign in as
`admin` / `adminpass`.

When you're done, `docker compose down` stops and removes the containers.

## Where you are now

You have:

- a realm with a confidential client whose secret comes from the environment,
- a role, a group and a user who holds both,
- a config you can preview with `--dry-run` and re-run as often as you like.

## Next steps

- **Understand what a run does.** [How it works](concepts.md) explains the order of steps,
  what `strategy: create` still changes, what is never deleted, and how far the pre-flight
  check protects you.
- **Look up a field.** [Configuration reference](configuration.md) lists every field the
  config accepts.
- **Deploy it.** [Deployment](deployment.md) covers the image, Kubernetes, connection retry
  and Keycloak version compatibility.
- **Answer the questions that come up later.** [FAQ](faq.md).
- **Common tasks:**
  - [Provision users with passwords, roles and groups](howto/provision-users.md)
  - [Grant roles through groups](howto/grant-roles-through-groups.md)
  - [Set up a machine-to-machine client](howto/set-up-a-service-account-client.md)
  - [Preview changes with a dry run](howto/preview-changes-with-dry-run.md)
  - [Keep secrets out of the config file](howto/keep-secrets-out-of-the-config.md)
  - [Run the provisioner as an init container](howto/run-as-init-container.md)
