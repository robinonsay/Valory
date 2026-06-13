# Runbook: TLS for Production Deployments

**Audience:** operators self-hosting a Valory instance and wanting to eliminate browser SSL warnings.

**Scope:** choosing and configuring one of the three supported production TLS modes: automatic ACME/Let's Encrypt certificates, operator-supplied (BYO) certificates, and plain-HTTP reverse-proxy mode. This runbook does not cover development deployments where the self-signed fallback is acceptable.

**Requirements satisfied:** REQ-INFRA-003, REQ-INFRA-004, REQ-INFRA-005, REQ-INFRA-006, REQ-INFRA-007, REQ-INFRA-008, REQ-SYS-056, REQ-SYS-057.

---

## Why browsers show an SSL warning

When neither `ACME_DOMAIN` nor the BYO cert variables (`VALORY_TLS_CERT_FILE` + `VALORY_TLS_KEY_FILE`) are set, the server falls back to a self-signed certificate generated at startup. The certificate is valid only for `localhost` / `127.0.0.1` and is not trusted by any browser's root store, producing the "Your connection is not private" warning on every access.

There is no way to suppress this warning in production without one of the three options below. The self-signed path exists only for local development convenience.

---

## Decision guide: which option fits your deployment?

| Scenario | Recommended option |
|---|---|
| The server is reachable on a **public domain name** (e.g. `valory.example.com`) and port 80 is open to the internet | **Option A — ACME/Let's Encrypt** |
| You have an **internal/private PKI** or your organisation issues its own CA-signed certificates, or you obtained a certificate from a commercial CA | **Option B — operator-supplied (BYO) certs** |
| You already run **Caddy, nginx, Traefik, or another TLS-terminating reverse proxy** in front of Valory | **Option C — reverse-proxy mode** |

The precedence order enforced at startup is: **BYO certs > ACME > self-signed fallback**. Reverse-proxy mode is mutually exclusive with all cert-based modes; combining them is a startup configuration error (the server aborts with a clear message — see REQ-INFRA-008 and the troubleshooting table below).

---

## Option A — ACME / Let's Encrypt

### How it works

Set `ACME_DOMAIN` to your public hostname. The server uses the `golang.org/x/crypto/acme/autocert` package to obtain and automatically renew a certificate from Let's Encrypt via the HTTP-01 challenge on port 80. The certificate is cached in `ACME_CACHE_DIR` so it survives container restarts without re-issuance.

### Prerequisites

1. **DNS A record** (or AAAA for IPv6) must point your domain to this server's public IP address before you start the container. Let's Encrypt's HTTP-01 validation is synchronous at first startup — if DNS is not resolving correctly the challenge fails and the server logs an error.

2. **Port 80 must be reachable from the internet.** Let's Encrypt dials your server on port 80 to retrieve the challenge token. Firewalls, AWS security groups, and similar network controls must allow inbound TCP port 80 from the internet.

   Note: in the default `docker-compose.yml`, port 80 on the host is bound by the `frontend` (Nginx) service, not the `api` service. The frontend's Nginx configuration must proxy ACME HTTP-01 challenge requests (paths beginning with `/.well-known/acme-challenge/`) to `http://api:80` on the Docker-internal network. The `api` container's port 80 is intentionally not exposed to the host. Review the frontend Nginx config to confirm this proxy rule is present before enabling ACME.

3. **Rate limits.** Let's Encrypt enforces issuance limits (currently 50 certificates per registered domain per week). Do not cycle `ACME_DOMAIN` values or destroy and recreate the cache volume repeatedly in testing; use the Let's Encrypt staging environment (`ACME_DOMAIN` alone does not have a staging toggle — use Option B with a staging-issued cert to test the full TLS path without consuming production rate limits).

### Configuration

Add to `.env`:

```
ACME_DOMAIN=valory.example.com
ACME_CACHE_DIR=/app/acme-cache
```

`ACME_CACHE_DIR` defaults to `/app/acme-cache` if unset. The value shown matches the volume mount already defined in `docker-compose.yml` and is the recommended path.

### Persisting the cache across container restarts

The `acme_cache` named volume in `docker-compose.yml` already covers this for Docker Compose deployments. If you deploy with a custom compose file or Kubernetes, ensure the directory at `ACME_CACHE_DIR` is backed by a persistent volume. Losing the cache does not corrupt data but forces re-issuance on the next restart, which consumes a rate-limit slot.

Verify the volume is mounted:

```bash
docker compose exec api ls /app/acme-cache
# After the first successful issuance this directory contains at least one file.
```

### What the startup log says

When ACME is active the server logs:

```
server: TLS mode: ACME (domain=valory.example.com, cacheDir=/app/acme-cache)
server: listening on :80 (HTTP redirect / ACME)
server: listening on :8443 (HTTPS)
```

The exact message text is determined by the implementation (REQ-INFRA-007 requires the startup log to state which mode is active); treat the above as illustrative of the required content.

---

## Option B — Operator-supplied (BYO) certificates

### How it works

Set both `VALORY_TLS_CERT_FILE` and `VALORY_TLS_KEY_FILE` to the absolute paths (inside the container) of your PEM-encoded certificate chain and private key. The server loads the pair at startup. **Both variables must be set together** — setting only one is a configuration error that aborts startup (REQ-INFRA-004). An unreadable file or an invalid cert/key pair also aborts startup with a clear error (REQ-INFRA-005).

### Certificate requirements

- PEM format. The certificate file may contain the full chain (leaf + intermediates); the key file contains only the private key.
- Any CA is acceptable: Let's Encrypt, a commercial CA, an internal CA, or a self-signed CA that you have imported into your browser's trust store.
- The certificate must cover the hostname(s) your users access (Subject Alternative Name). A certificate valid only for `localhost` produces the same browser warning as the self-signed fallback.

### Configuration

Add to `.env`:

```
VALORY_TLS_CERT_FILE=/run/secrets/tls/cert.pem
VALORY_TLS_KEY_FILE=/run/secrets/tls/key.pem
```

### Mounting cert files into the container

Add a `secrets` volume to the `api` service in your compose override or custom compose file:

```yaml
services:
  api:
    volumes:
      - uploads:/app/uploads
      - acme_cache:/app/acme-cache
      # Mount the directory containing your cert and key read-only.
      # The host path on the left must point to a directory containing
      # cert.pem and key.pem (or adjust the filenames to match VALORY_TLS_CERT_FILE
      # and VALORY_TLS_KEY_FILE).
      - /etc/valory/tls:/run/secrets/tls:ro
    environment:
      VALORY_TLS_CERT_FILE: /run/secrets/tls/cert.pem
      VALORY_TLS_KEY_FILE: /run/secrets/tls/key.pem
```

The `:ro` flag is a defence-in-depth measure: the application process only needs to read the files, not write them.

### Certificate renewal

The server loads the certificate **at startup only**. If you rotate the cert/key pair on disk, you must restart the `api` container to pick up the new files:

```bash
# Copy new cert and key into /etc/valory/tls/ on the host, then:
docker compose restart api
```

After restart, confirm the new certificate is in use:

```bash
# Replace valory.example.com with your hostname.
echo | openssl s_client -connect valory.example.com:443 -servername valory.example.com 2>/dev/null \
  | openssl x509 -noout -dates
```

If you use an automation tool (certbot, acme.sh, cert-manager) to renew certificates, add a post-renewal hook that runs `docker compose restart api`.

### Common failure: only one variable set

If `VALORY_TLS_CERT_FILE` is set without `VALORY_TLS_KEY_FILE`, or vice versa, the server aborts at startup. The log will contain a message identifying the missing variable (REQ-INFRA-004). Always set both or neither.

### What the startup log says

When BYO certs are active the server logs:

```
server: TLS mode: operator-supplied cert (cert=/run/secrets/tls/cert.pem, key=/run/secrets/tls/key.pem)
server: listening on :80 (HTTP redirect)
server: listening on :8443 (HTTPS)
```

The exact message text is determined by the implementation (REQ-INFRA-007 requires the startup log to state which mode is active); treat the above as illustrative of the required content.

---

## Option C — Behind a reverse proxy (plain-HTTP mode)

### How it works

Set `VALORY_BEHIND_PROXY=true`. The server binds only port 8443 but serves **plain HTTP** on it — no TLS at all. The reverse proxy (Caddy, nginx, Traefik, etc.) terminates TLS externally and forwards requests to Valory over the Docker-internal network.

### IMPORTANT — the proxy MUST serve HTTPS to browsers

Valory's session cookie carries the `Secure` attribute (REQ-SYS-056). Browsers will not send a `Secure` cookie over a plain HTTP connection. This means: **if end users reach the reverse proxy over plain HTTP, login will silently fail** — the browser will never send the session cookie back after it is set.

The reverse proxy must:
1. Obtain and serve a valid TLS certificate to browsers on port 443.
2. Forward requests to Valory's application port over the Docker-internal network (plain HTTP is fine on the internal leg because it never leaves the host).

There is no partial workaround: the `Secure` attribute is not configurable and is required by REQ-SYS-057. This is intentional — removing it would expose session tokens over plain HTTP connections.

### Configuration

Add to `.env`:

```
VALORY_BEHIND_PROXY=true
```

Do not set `ACME_DOMAIN`, `VALORY_TLS_CERT_FILE`, or `VALORY_TLS_KEY_FILE` alongside this variable. Combining `VALORY_BEHIND_PROXY=true` with any TLS cert variable or `ACME_DOMAIN` is a startup configuration error that aborts the server (REQ-INFRA-008).

### Port mapping in proxy mode

In reverse-proxy mode the `api` service no longer needs port 8443 exposed to the host if the proxy container is on the same Docker network. Update your compose file accordingly:

```yaml
services:
  api:
    # Remove or comment out the host port binding; the proxy reaches the api
    # via the Docker-internal network (api:8443).
    # ports:
    #   - "8443:8443"
    networks:
      - internal
  proxy:
    # ... your proxy service ...
    ports:
      - "80:80"
      - "443:443"
    networks:
      - internal

networks:
  internal:
```

### Minimal Caddyfile example

```caddyfile
valory.example.com {
    # Caddy automatically obtains and renews a Let's Encrypt certificate.
    # Forward all requests to the Valory api container on the internal network.
    reverse_proxy api:8443
}
```

Caddy handles TLS automatically using ACME. The internal leg (`api:8443`) is plain HTTP because `VALORY_BEHIND_PROXY=true` is set.

### Minimal nginx server-block example

```nginx
# HTTP → HTTPS redirect
server {
    listen 80;
    server_name valory.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    server_name valory.example.com;

    ssl_certificate     /etc/nginx/tls/cert.pem;
    ssl_certificate_key /etc/nginx/tls/key.pem;

    # Forward all requests to the Valory api container.
    # Plain HTTP on the internal leg is correct because VALORY_BEHIND_PROXY=true.
    location / {
        proxy_pass         http://api:8443;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Real-IP         $remote_addr;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
    }
}
```

### Compose healthcheck in proxy mode

The default `docker-compose.yml` healthcheck for the `api` service is:

```yaml
test: ["CMD", "curl", "-kf", "https://localhost:8443/health"]
```

This healthcheck uses HTTPS (`https://`) and the `-k` flag (skip cert verification). In reverse-proxy mode the `api` container serves **plain HTTP**, so `curl -kf https://localhost:8443/health` will fail with a protocol error (the server responds with plain HTTP bytes on a connection expecting a TLS handshake).

**Operators using reverse-proxy mode must update the healthcheck** to use plain HTTP:

```yaml
healthcheck:
  test: ["CMD", "curl", "-f", "http://localhost:8443/health"]
  interval: 10s
  timeout: 5s
  retries: 5
  start_period: 15s
```

Without this change `docker compose` will report the `api` container as unhealthy and the `frontend` service (which depends on `api: condition: service_healthy`) will never start.

---

## Quick-reference: all TLS-related environment variables

| Variable | Default | Mode | Notes |
|---|---|---|---|
| `ACME_DOMAIN` | _(unset)_ | ACME | Public hostname. When set, ACME mode is active (unless BYO certs are also set — BYO takes precedence). Must not be set with `VALORY_BEHIND_PROXY=true`. |
| `ACME_CACHE_DIR` | `/app/acme-cache` | ACME | Directory for ACME cert persistence. Must be on a persistent volume. No effect unless `ACME_DOMAIN` is also set. |
| `VALORY_TLS_CERT_FILE` | _(unset)_ | BYO | Absolute path (inside container) to PEM certificate chain. Must be set together with `VALORY_TLS_KEY_FILE`. Overrides ACME. Must not be set with `VALORY_BEHIND_PROXY=true`. |
| `VALORY_TLS_KEY_FILE` | _(unset)_ | BYO | Absolute path (inside container) to PEM private key. Must be set together with `VALORY_TLS_CERT_FILE`. Overrides ACME. Must not be set with `VALORY_BEHIND_PROXY=true`. |
| `VALORY_BEHIND_PROXY` | `false` | Proxy | Set to `true` to enable plain-HTTP mode for TLS-terminating reverse proxies. Mutually exclusive with all cert and ACME variables. |

**Precedence:** BYO certs (`VALORY_TLS_CERT_FILE` + `VALORY_TLS_KEY_FILE`) > ACME (`ACME_DOMAIN`) > self-signed dev fallback. `VALORY_BEHIND_PROXY=true` bypasses all cert loading entirely.

---

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| Browser shows "Your connection is not private" / untrusted cert warning | Server is using the self-signed dev fallback (no TLS mode configured, or `ACME_DOMAIN` unset and BYO vars not set) | Choose and configure one of Options A, B, or C above. |
| Server aborts at startup: message about `VALORY_TLS_CERT_FILE` and `VALORY_TLS_KEY_FILE` | Only one of the two BYO cert variables is set (REQ-INFRA-004) | Set both variables, or unset both to fall back to ACME or dev mode. |
| Server aborts at startup: message about invalid certificate pair | The cert and key PEM files exist but do not match (e.g. the key was generated for a different certificate), or the files are corrupt | Verify the pair matches by comparing public keys (works for RSA and ECDSA alike): `openssl x509 -noout -pubkey -in cert.pem \| md5sum` and `openssl pkey -pubout -in key.pem \| md5sum` — the hashes must match. Replace with a matching pair and restart. |
| Server aborts at startup: message about unreadable cert or key file | The file path is wrong, the file does not exist inside the container, or the container process lacks read permission | Confirm the volume mount in your compose file maps the host path to the container path specified in `VALORY_TLS_CERT_FILE`/`VALORY_TLS_KEY_FILE`. Check that the files exist on the host and are readable. |
| Server aborts at startup: message about conflicting TLS configuration | `VALORY_BEHIND_PROXY=true` is set alongside `ACME_DOMAIN`, `VALORY_TLS_CERT_FILE`, or `VALORY_TLS_KEY_FILE` (REQ-INFRA-008) | Proxy mode is mutually exclusive with all cert-based modes. Unset all cert and ACME variables when `VALORY_BEHIND_PROXY=true` is set. |
| ACME challenge fails at startup; server logs a certificate issuance error | DNS not yet resolving, or port 80 is not reachable from the internet | Confirm `dig valory.example.com` returns your server's public IP. Confirm that the frontend Nginx config proxies `/.well-known/acme-challenge/` to `http://api:80`. Confirm your firewall allows inbound TCP 80 from the internet. |
| `docker compose up` hangs; `frontend` container never becomes healthy | Healthcheck for `api` is failing in reverse-proxy mode because it probes `https://localhost:8443/health` but the server is serving plain HTTP | Update the `api` healthcheck in your compose file to use `http://localhost:8443/health` (see the healthcheck note in Option C above). |
| Login fails silently in reverse-proxy mode (page loads but user is not logged in after submitting credentials) | End users are reaching the reverse proxy over plain HTTP. The `__Host-session` cookie carries the `Secure` attribute and the browser refuses to send it over a non-HTTPS connection (REQ-SYS-057) | Ensure the reverse proxy terminates TLS and that all HTTP traffic to the proxy is redirected to HTTPS before it reaches Valory. See the proxy examples in Option C. |
| Certificate is not renewed after rotation; browser shows the old cert | BYO cert files on disk were updated but the `api` container was not restarted | Run `docker compose restart api`. The server loads the cert at startup only. Add a post-renewal hook to your cert automation tool. |
| `curl -kf https://localhost:8443/health` fails with a connection error from within the api container in proxy mode | Same as the healthcheck issue above — the server is on plain HTTP in proxy mode | Use `curl -f http://localhost:8443/health` (no `-k`, no `https://`) when probing from inside the container in proxy mode. |
