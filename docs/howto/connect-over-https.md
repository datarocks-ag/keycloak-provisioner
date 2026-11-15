# Connect to Keycloak over HTTPS

After this the provisioner talks to Keycloak over HTTPS (HTTP over TLS, Transport Layer
Security), with the server certificate checked against a certificate authority (CA) you
trust, including a private one.

## When you need this

The provisioner sends the admin password to Keycloak to log in and then carries an admin
access token on every request. Over plain HTTP both cross the network readable by anyone on
the path, which is acceptable inside one Docker network on your laptop and nowhere else. Your
production Keycloak is served over HTTPS, possibly with a certificate from your
organisation's own CA.

## What you need to know first

- **`KEYCLOAK_URL`** is the Keycloak base URL. It must start with `http://` or `https://` and
  name a host; the provisioner appends `/realms/master/protocol/openid-connect/token` to log
  in and `/admin/realms/…` for everything else. See
  [Environment variables](../deployment.md).
- **Certificate verification is always on.** The provisioner has no option to skip it and no
  option to present a client certificate. It uses Go's standard HTTP client, which checks the
  server certificate against the trusted CAs and the host name in the URL.
- **Trusted CAs.** On Linux, Go reads the trusted CAs from a bundle file, such as
  `/etc/ssl/certs/ca-certificates.crt` (or the file named in the `SSL_CERT_FILE` environment
  variable, when set), plus every file in a certificate directory such as `/etc/ssl/certs`
  (or the directories in `SSL_CERT_DIR`). This is Go standard-library behaviour, not a
  provisioner setting; see [crypto/x509 `SystemCertPool`](https://pkg.go.dev/crypto/x509#SystemCertPool).
  The container image
  is built `FROM scratch` and contains only the binary and the Alpine CA bundle at
  `/etc/ssl/certs/ca-certificates.crt` (see [`Dockerfile`](../../Dockerfile)), so
  certificates from public CAs verify without mounting anything.

## Steps

The examples use `https://keycloak.example.com` as the server and `ca.crt` as your private
CA certificate in PEM format; replace both with your own.

### 1. Point `KEYCLOAK_URL` at the HTTPS address

```console
$ export KEYCLOAK_URL=https://keycloak.example.com
```

Use the host name the certificate is issued for, not an IP address, and include the path
prefix if your Keycloak is served under one (for example `https://example.com/auth`). A
trailing `/` is removed.

If the certificate comes from a public CA, you are done: skip to
[Check that it works](#check-that-it-works).

### 2. Trust your private CA, if Keycloak uses one

Choose one of two ways. Both are Go standard-library behaviour.

**Point `SSL_CERT_FILE` at your CA file.** Mount the file and set the variable. In the
image, Go reads that file instead of the bundle file, but still reads the directory
`/etc/ssl/certs`, which holds the bundle; the public CAs therefore stay trusted alongside
yours. In Docker Compose:

```yaml
  keycloak-provisioner:
    image: ghcr.io/datarocks-ag/keycloak-provisioner:1.14
    environment:
      KEYCLOAK_URL: https://keycloak.example.com
      SSL_CERT_FILE: /certs/ca.crt
      # … KEYCLOAK_USER, KEYCLOAK_PASSWORD, KEYCLOAK_CONFIG_PATH as before
    volumes:
      - ./config.yaml:/config.yaml:ro
      - ./certs/ca.crt:/certs/ca.crt:ro
```

**Or replace the bundle in the image.** Mount a bundle at
`/etc/ssl/certs/ca-certificates.crt`. This replaces the public CAs that ship with the image,
so Keycloak's certificate must chain to a CA in your bundle. In Kubernetes, from a
ConfigMap:

```console
$ kubectl create configmap keycloak-ca --from-file=ca-certificates.crt=./certs/ca-bundle.crt
```

```yaml
          volumeMounts:
            - { name: keycloak-ca, mountPath: /etc/ssl/certs/ca-certificates.crt, subPath: ca-certificates.crt, readOnly: true }
      volumes:
        - name: keycloak-ca
          configMap: { name: keycloak-ca }
```

The macOS binary from the releases page verifies against the macOS keychain, so a CA you
trust there works without either option. Setting `SSL_CERT_FILE` replaces the keychain with
that file (Go standard-library behaviour since Go 1.27, which the releases are built with).

## Check that it works

Run a dry run, which logs in and reads but changes nothing:

```console
$ keycloak-provisioner --config ./config.yaml --dry-run
```

With `https://` there is no plain-HTTP warning, and a `Connected to Keycloak` line means the
TLS handshake and the certificate check passed (expected from the code, not captured):

```json
{"time":"…","level":"INFO","msg":"Connecting to Keycloak","url":"https://keycloak.example.com"}
{"time":"…","level":"INFO","msg":"Connected to Keycloak","url":"https://keycloak.example.com"}
```

For comparison, an `http://` URL logs this warning before connecting (captured against
Keycloak 26.6.4):

```json
{"time":"…","level":"WARN","msg":"Keycloak URL uses plain HTTP — credentials will be sent unencrypted","url":"http://localhost:8080"}
```

## Pitfalls

- **TLS errors are retried, not reported at once.** A failed handshake is logged as
  `Keycloak not ready, retrying`, and the provisioner keeps trying for up to five minutes
  (see [Connection retry](../deployment.md)). Read the `error` field of the first warning.
- **HTTPS against an HTTP port.** Captured against a Keycloak that only serves HTTP:

  ```json
  {"time":"…","level":"WARN","msg":"Keycloak not ready, retrying","attempt":1,"max_retries":15,"delay":1000000000,"error":"token request: Post \"https://localhost:8080/realms/master/protocol/openid-connect/token\": http: server gave HTTP response to HTTPS client"}
  ```

- **Unknown CA.** `x509: certificate signed by unknown authority` in the `error` field means
  the CA is not trusted: `SSL_CERT_FILE` points to the wrong file, the file isn't mounted, or
  it holds the wrong certificate (Go error text, not captured).
- **Host name mismatch.** An error starting `x509: certificate is valid for …` means
  `KEYCLOAK_URL` uses a name the certificate doesn't carry, often an IP address or an internal service name (Go
  error text, not captured).
- **A URL without scheme fails at once.** Captured:

  ```json
  {"time":"…","level":"ERROR","msg":"Failed to connect to Keycloak","error":"invalid Keycloak URL \"keycloak.example.com\": must include scheme and host"}
  ```

  `ftp://…` and other schemes fail with `scheme must be http or https`.
- **Plain HTTP to a remote Keycloak may be refused by Keycloak itself.** With the realm
  setting `sslRequired: external`, Keycloak accepts plain HTTP only from private addresses
  and refuses the login otherwise (Keycloak behaviour, not checked by the provisioner). The
  provisioner retries that like any failed login.

## Further reading

- [Deployment](../deployment.md): environment variables and connection retry.
- [crypto/x509](https://pkg.go.dev/crypto/x509): how Go finds the trusted CAs.
- [Configuring TLS](https://www.keycloak.org/server/enabletls) in the Keycloak server guide.
- [Keep secrets out of the config file](keep-secrets-out-of-the-config.md) for the admin
  credentials that go with the connection.
