# Require or seed TOTP for users

After this you can make users enrol a one-time password at their next login, or give a test
user a known one-time password secret so that automated tests can sign in with a second factor.

## When you need this

Your realm uses a second factor, and you have two kinds of users. People should set up their
own authenticator app the first time they sign in. Test automation cannot scan a QR code, so it
needs a user whose secret is already known and from which it can compute the current code. The
provisioner covers both, since 1.9.0 (2026-08-22).

## What you need to know first

- **Time-based one-time password (TOTP).** A six-digit code computed from a shared secret and
  the current time, valid for a 30-second period by default. Keycloak stores it as a credential
  of type `otp`. See the
  [Keycloak Server Administration Guide](https://www.keycloak.org/docs/latest/server_admin/).
- **Required action.** Something Keycloak makes a user do at their next login, before the login
  completes. `CONFIGURE_TOTP` asks the user to set up an authenticator app.
- **`requiredActions` replaces.** When a user declares the field, the user's required actions
  become exactly that list; an empty list clears them. Without the field, the user's actions are
  left alone. Under `strategy: create` the list is applied only when the user is created.
- **Seeded credentials are added, never rotated.** The provisioner adds a credential whose
  `type` and `label` the user does not hold yet. A credential with the same pair is left alone,
  even if the secret in the config changed. Nothing is removed.

## Steps

The names `totp`, `e2e-tests`, `bob` and `tester` are examples; replace them with your own.

### 1. Require TOTP enrolment for people

List `CONFIGURE_TOTP` under the user's `requiredActions`. Before writing anything, the
provisioner checks each action name against the required actions registered in the `master`
realm, because Keycloak accepts an unknown name and silently drops it.

```yaml
realms:
  - realm: "totp"
    enabled: true

    clients:
      - clientId: "e2e-tests"
        publicClient: true
        directAccessGrantsEnabled: true   # the tests sign in with the password grant

    users:
      - username: "bob"
        password: "${BOB_PASSWORD}"
        enabled: true
        email: "bob@example.com"
        firstName: "Bob"
        lastName: "Example"
        requiredActions:
          - "CONFIGURE_TOTP"
```

### 2. Seed a known secret for test automation

Add a `credentials` entry of type `otp` to the test user, in the same `users` list. `type` and
`secret` are required, and `otp` is the only type the provisioner accepts. Pass the secret
through an environment variable: an unset variable is not an error, and the literal text
`${TESTER_TOTP_SECRET}` would become the secret.

```yaml
      - username: "tester"
        password: "${TESTER_PASSWORD}"
        enabled: true
        email: "tester@example.com"
        firstName: "Test"
        lastName: "Automation"
        credentials:
          - type: "otp"
            label: "seeded"
            secret: "${TESTER_TOTP_SECRET}"
```

`digits`, `period` and `algorithm` are optional and default to `6`, `30` and `HmacSHA1`.
`algorithm` also accepts `HmacSHA256` and `HmacSHA512`. Change them only to match an existing
authenticator. `label` tells two credentials of the same type apart, and the provisioner uses
it to recognise a credential it already seeded, so keep it stable.

### 3. Run the provisioner

The two snippets together make `totp.yaml`.

```console
$ BOB_PASSWORD='bob-S3cret!' TESTER_PASSWORD='tester-S3cret!' TESTER_TOTP_SECRET='JBSWY3DPEHPK3PXP' \
  KEYCLOAK_URL=http://localhost:8080 KEYCLOAK_USER=admin KEYCLOAK_PASSWORD=<admin-password> \
  keycloak-provisioner --config totp.yaml
…
{"time":"…","level":"INFO","msg":"Creating user","realm":"totp","username":"bob"}
{"time":"…","level":"INFO","msg":"Setting user password","realm":"totp","username":"bob"}
{"time":"…","level":"INFO","msg":"Creating user","realm":"totp","username":"tester"}
{"time":"…","level":"INFO","msg":"Setting user password","realm":"totp","username":"tester"}
{"time":"…","level":"INFO","msg":"Seeding user credential","realm":"totp","username":"tester","type":"otp","label":"seeded"}
{"time":"…","level":"INFO","msg":"Provisioning complete"}
{"time":"…","level":"INFO","msg":"keycloak-provisioner finished successfully"}
```

A second run does not seed again. With `LOG_LEVEL=debug` it says so:

```console
{"time":"…","level":"DEBUG","msg":"Credential already present","realm":"totp","username":"tester","type":"otp","label":"seeded"}
```

### 4. Compute codes from the secret as Keycloak does

Keycloak uses the characters of `secret` directly as the key for the code; it does not
base32-decode them. An authenticator app or a TOTP library that expects a base32 secret must
therefore be given the base32 encoding of `secret`. With `oathtool`:

```console
$ oathtool --totp -b "$(printf %s "$TESTER_TOTP_SECRET" | base32)"
010184
```

This was checked against Keycloak 26.6.4 with the secret `JBSWY3DPEHPK3PXP` in the same 30-second
period: the code computed from the raw characters (`010184`) was accepted, the code computed
from the base32-decoded bytes (`554673`) was rejected with `Invalid user credentials`.

## Check that it works

Sign in as `tester` with the password grant and the current code in the `totp` parameter. Run
this against a test realm only; the password grant must be enabled on the client.

```console
$ code=$(oathtool --totp -b "$(printf %s "$TESTER_TOTP_SECRET" | base32)")
$ curl -s -d client_id=e2e-tests -d username=tester -d 'password=tester-S3cret!' \
    -d totp="$code" -d grant_type=password \
    http://localhost:8080/realms/totp/protocol/openid-connect/token \
  | jq -r .access_token \
  | jq -R 'split(".")[1] | gsub("-";"+") | gsub("_";"/") | @base64d | fromjson
           | {preferred_username, acr}'
{
  "preferred_username": "tester",
  "acr": "1"
}
```

Without the code, the same request fails, because a user with an `otp` credential must give it
in Keycloak's default direct grant flow:

```console
$ curl -s -d client_id=e2e-tests -d username=tester -d 'password=tester-S3cret!' \
    -d grant_type=password \
    http://localhost:8080/realms/totp/protocol/openid-connect/token
{"error":"invalid_grant","error_description":"Invalid user credentials"}
```

`bob` cannot complete a password-grant login until he has set up TOTP in the browser:

```console
$ curl -s -d client_id=e2e-tests -d username=bob -d 'password=bob-S3cret!' \
    -d grant_type=password \
    http://localhost:8080/realms/totp/protocol/openid-connect/token
{"error":"invalid_grant","error_description":"Account is not fully set up"}
```

To see the seeded credential, ask the admin API (`$ADMIN_TOKEN` as in
[Provision users with passwords, roles and groups](provision-users.md)):

```console
$ USER_ID=$(curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
    "http://localhost:8080/admin/realms/totp/users?username=tester&exact=true" | jq -r '.[0].id')
$ curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
    "http://localhost:8080/admin/realms/totp/users/$USER_ID/credentials" \
  | jq -c '.[] | select(.type == "otp") | {type, userLabel, credentialData}'
{"type":"otp","userLabel":"seeded","credentialData":"{\"algorithm\":\"HmacSHA1\",\"counter\":0,\"digits\":6,\"period\":30,\"subType\":\"totp\"}"}
```

## Pitfalls

- **`Invalid user credentials` although the password is right.** The user has an `otp`
  credential and the request has no code, or a wrong one. A code computed from the
  base32-decoded secret is wrong; see step 4.
- **A code works once per period.** Keycloak 26.6.4 rejected a second login with the same code
  in the same 30-second period with `Invalid user credentials` (Keycloak behaviour, observed on
  26.6.4). A test that signs in twice must wait for the next period.
- **Changing `secret` in the config changes nothing.** The credential with that `type` and
  `label` already exists, so it is skipped. Delete the credential in Keycloak (account console or
  admin API) and run again, or seed under a new `label`; the old credential then stays as well.
- **A misspelled action stops the run before anything is written.** The check also suggests the
  name you probably meant, and the run exits with code 1:
  `{"level":"ERROR","msg":"Unsupported by this Keycloak server","capability":"CONFIGURE_TOPT","reason":"required action \"CONFIGURE_TOPT\" is not available on this server (did you mean \"CONFIGURE_TOTP\"?)","usedAt":"realms[0].users[0].requiredActions[0]"}`.
  The check is skipped with `--skip-version-check`, and skipped with a warning when the admin
  account may not read the server's required actions; a typo then goes unnoticed.
- **`requiredActions: []` clears every required action**, including ones Keycloak or an
  administrator set, such as `UPDATE_PASSWORD` from an `initialPassword`. Leave the field out if
  you do not want to manage the list.
- **`Account is not fully set up` for a seeded user.** Usually unrelated to the credential: a
  pending required action, or a missing `firstName` or `lastName`.

## Further reading

- [Configuration reference](../configuration.md) for every user and credential field.
- [Provision users with passwords, roles and groups](provision-users.md) for the rest of a
  user's declaration.
- [Keep secrets out of the config file](keep-secrets-out-of-the-config.md) for passing the
  secret through the environment.
- [Add step-up authentication to a client](add-step-up-authentication.md) to require the second
  factor for one client only.
- [Keycloak Server Administration Guide](https://www.keycloak.org/docs/latest/server_admin/) on
  one-time password policies and required actions.
