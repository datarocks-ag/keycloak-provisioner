# Onboard an organization

After this a customer company exists in your realm as a Keycloak organization, with its email
domain, its members, its own groups and its single sign-on provider, all from the config.

## When you need this

You run one realm for many customer companies. A new customer, ACME, signs up: its people should
be grouped under ACME, its `acme.com` email addresses should belong to it, and its employees
should sign in through ACME's own identity provider. Clicking this together in the admin console
for every customer and every environment does not scale, and the next environment never matches.

## What you need to know first

- **Organizations.** A Keycloak feature (26.0 and later, server feature `ORGANIZATION`, on by
  default) that models a customer company inside a realm: domains, members, groups and linked
  identity providers. It must be switched on per realm with `organizationsEnabled: true`. See
  [Organizations](https://www.keycloak.org/docs/latest/server_admin/) in the Server
  Administration Guide.
- **Domains are authoritative.** Once an organization declares `domains`, the configured list
  replaces the list on the server: a domain added in the admin console disappears on the next
  run. Leave `domains` out to stop managing them.
- **Organization groups.** Groups that belong to one organization (Keycloak 26.6 and later).
  They are separate from the realm's groups and cannot carry roles. A user must be a member of
  the organization before joining one of its groups.
- **Linked identity providers.** An organization can link providers by alias. The provider's
  `kc.org.domain` config key binds it to one of the organization's domains, and
  `kc.org.broker.redirect.mode.email-matches` sends users whose email matches that domain to the
  provider (Keycloak behaviour, not checked by the provisioner). A provider belongs to at most
  one organization.

The provisioner creates organizations last in a realm, after users and identity providers, so
an organization can name both from the same file.

## Steps

The names `orgs`, `ACME Corp`, `acme`, `acme.com`, `alice`, `dave` and `acme-sso` are
examples; replace them with your own. The snippets together make `org.yaml`.

### 1. Turn organizations on and declare the people

```yaml
realms:
  - realm: "orgs"
    enabled: true
    organizationsEnabled: true
    users:
      - username: "alice"
        enabled: true
        email: "alice@acme.com"
        emailVerified: true
        firstName: "Alice"
        lastName: "Acme"
      - username: "dave"
        enabled: true
        email: "dave@acme.com"
        emailVerified: true
        firstName: "Dave"
        lastName: "Acme"
```

### 2. Declare the organization's identity provider

The URLs here are placeholders for ACME's provider; see
[Broker logins through an external identity provider](connect-an-identity-provider.md) for a
provider you can sign in through. `kc.org.domain` must be one of the organization's domains,
spelled exactly the same, including case.

```yaml
    identityProviders:
      - alias: "acme-sso"
        displayName: "ACME single sign-on"
        providerId: "oidc"
        config:
          clientId: "keycloak"
          clientSecret: "${ACME_IDP_SECRET}"
          authorizationUrl: "https://sso.acme.com/oauth2/authorize"
          tokenUrl: "https://sso.acme.com/oauth2/token"
          kc.org.domain: "acme.com"
          kc.org.broker.redirect.mode.email-matches: "true"
```

### 3. Declare the organization

The organization is matched by `name`. `alias` is optional; Keycloak derives one when you leave
it out, and it cannot change once the organization exists. Members are usernames from the same
realm. Groups list their own members, who must also be organization members.

```yaml
    organizations:
      - name: "ACME Corp"
        alias: "acme"
        description: "ACME Corporation"
        redirectUrl: "https://app.example.com/acme"
        domains:
          - name: "acme.com"
            verified: true
        attributes:
          tier:
            - "gold"
        members:
          - "alice"
          - "dave"
        groups:
          - name: "engineering"
            members:
              - "alice"
            subGroups:
              - name: "backend"
                members:
                  - "dave"
        identityProviders:
          - "acme-sso"
```

`verified` is sent only when you declare it.

### 4. Run the provisioner

```console
$ ACME_IDP_SECRET=… \
  KEYCLOAK_URL=http://localhost:8080 KEYCLOAK_USER=admin KEYCLOAK_PASSWORD=… \
  keycloak-provisioner --config org.yaml
…
{"time":"…","level":"INFO","msg":"Starting provisioning"}
{"time":"…","level":"INFO","msg":"Creating realm","realm":"orgs"}
{"time":"…","level":"INFO","msg":"Creating user","realm":"orgs","username":"alice"}
{"time":"…","level":"INFO","msg":"Creating user","realm":"orgs","username":"dave"}
{"time":"…","level":"INFO","msg":"Creating identity provider","realm":"orgs","identityProvider":"acme-sso","providerId":"oidc"}
{"time":"…","level":"INFO","msg":"Creating organization","realm":"orgs","organization":"ACME Corp"}
{"time":"…","level":"INFO","msg":"Adding user to organization","realm":"orgs","organization":"ACME Corp","username":"alice"}
{"time":"…","level":"INFO","msg":"Adding user to organization","realm":"orgs","organization":"ACME Corp","username":"dave"}
{"time":"…","level":"INFO","msg":"Linking identity provider to organization","realm":"orgs","organization":"ACME Corp","identityProvider":"acme-sso"}
{"time":"…","level":"INFO","msg":"Creating organization group","realm":"orgs","group":"engineering"}
{"time":"…","level":"INFO","msg":"Adding user to organization group","realm":"orgs","organization":"ACME Corp","group":"engineering","username":"alice"}
{"time":"…","level":"INFO","msg":"Creating organization subgroup","realm":"orgs","group":"backend","parent":"2376a238-9fea-4bf6-aec6-3dad5b0c0be8"}
{"time":"…","level":"INFO","msg":"Adding user to organization group","realm":"orgs","organization":"ACME Corp","group":"backend","username":"dave"}
{"time":"…","level":"INFO","msg":"Provisioning complete"}
{"time":"…","level":"INFO","msg":"keycloak-provisioner finished successfully"}
```

## Check that it works

Read the organization back through the admin API (`$ADMIN_TOKEN` is an admin access token,
`<org-id>` the organization's id from the first command):

```console
$ curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
    "http://localhost:8080/admin/realms/orgs/organizations?search=ACME%20Corp&exact=true" \
    | jq -r '.[0].id'
063d157d-6d23-4ac0-b3ac-18028296eea0
$ curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
    http://localhost:8080/admin/realms/orgs/organizations/<org-id> \
    | jq '{name, alias, enabled, redirectUrl, domains, attributes}'
{
  "name": "ACME Corp",
  "alias": "acme",
  "enabled": true,
  "redirectUrl": "https://app.example.com/acme",
  "domains": [
    {
      "name": "acme.com",
      "verified": true
    }
  ],
  "attributes": {
    "tier": [
      "gold"
    ]
  }
}
$ curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
    http://localhost:8080/admin/realms/orgs/organizations/<org-id>/identity-providers \
    | jq '[.[] | {alias, organizationId, "kc.org.domain": .config["kc.org.domain"]}]'
[
  {
    "alias": "acme-sso",
    "organizationId": "063d157d-6d23-4ac0-b3ac-18028296eea0",
    "kc.org.domain": "acme.com"
  }
]
```

The organization's groups are not realm groups. The realm's group list stays empty, while the
organization lists its own:

```console
$ curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
    http://localhost:8080/admin/realms/orgs/groups | jq length
0
$ curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
    http://localhost:8080/admin/realms/orgs/organizations/<org-id>/groups | jq -c '[.[].name]'
["engineering"]
```

To see that domains are authoritative, add `acme.org` to the organization in the admin console
and run the provisioner again. Observed on 26.6.4 (2026-11-15), the domains before and after
the run were:

```
[{"name":"acme.org","verified":false},{"name":"acme.com","verified":true}]
[{"name":"acme.com","verified":true}]
```

## Pitfalls

- **A `kc.org.domain` that differs only in case is rejected at load.** Keycloak compares domains
  exactly, so the provisioner does too:

  ```
  {"time":"…","level":"ERROR","msg":"Failed to load configuration","error":"validating config: realms[0].identityProviders[0].config[kc.org.domain]: identity provider \"acme-sso\" has \"ACME.com\", which differs only in case from a domain of organization \"ACME Corp\" (acme.com); Keycloak compares them exactly"}
  ```

  A domain the organization does not have at all is rejected with
  `… has "acme.org", which is not a domain of organization "ACME Corp" (which has acme.com)`.
  Without this check the first run would pass and later runs fail: once the provider is linked,
  Keycloak itself refuses the update with
  `{"errorMessage":"Domain does not match any domain from the organization"}` (status 400,
  observed on 26.6.4 by writing `ACME.com` through the admin API). The provisioner checks only
  providers that an organization in the config links.
- **Organization groups reject roles at load.** Declaring `realmRoles` or `clientRoles` on one
  fails with
  `organization groups cannot carry role mappings; Keycloak stores them but they never reach a member's effective roles or any token claim. Assign the roles to the members directly, or use a realm group`.
  Give roles to the users or to a realm group; see [Grant roles through groups](grant-roles-through-groups.md).
- **`organizations` without `organizationsEnabled: true` is rejected at load** with
  `realms[0].organizations: requires organizationsEnabled: true on the realm`.
- **Missing users are skipped with a warning.** A member who does not exist logs
  `User not found, skipping organization membership`; a group member who is not in the
  organization logs `User is not a member of the organization, skipping group membership`. Both
  let the run continue.
- **A missing provider alias fails the run**, and so does a provider already linked to an
  organization the config does not describe; the error names the owning organization by id.
- **Changing `alias` fails the run.** The provisioner compares it with the stored alias and stops
  before the update, naming both.
- **Membership and links only grow.** Removing a member, group or provider from the config
  leaves it in Keycloak. Domains are the exception, as shown above.

## Further reading

- [Configuration reference](../configuration.md) for `organizations` and `identityProviders`.
- [Broker logins through an external identity provider](connect-an-identity-provider.md).
- [Deployment](../deployment.md) for the minimum Keycloak versions.
- [Server Administration Guide](https://www.keycloak.org/docs/latest/server_admin/) on
  organizations.
