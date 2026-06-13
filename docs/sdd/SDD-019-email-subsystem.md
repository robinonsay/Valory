# SDD-019 — Admin-Configurable Email Subsystem

**Sprint:** 19  
**Status:** DRAFT  
**Author:** design-author  
**Date:** 2026-06-12  
**Implements tasks:** 19.1 (this document), 19.3 (backend), 19.4 (frontend), 19.5 (tests)  
**Foundation for:** Sprint 20 (account lifecycle / welcome email), Sprint 21 (2FA OTP email)

---

## 1. Overview

### Problem

`internal/user.SMTPTransport` is hardwired at process startup from four environment
variables (`SMTP_HOST/PORT/FROM/PASSWORD`). It mandates STARTTLS and AUTH. This fails
on zero-configuration loopback relays (postfix, exim) that self-hosters commonly run
free-of-charge. It cannot be changed without a container restart. There is no test-send
mechanism and no admin visibility into email state.

### Approach

1. Introduce a new `internal/email` package that owns a minimal `Mailer` interface and
   a configurable SMTP implementation. The interface is designed so Sprint 20 (welcome
   email) and Sprint 21 (2FA OTP) plug in without interface changes.
2. Store non-secret SMTP settings in `system_config` (the existing admin-controlled
   key-value table). Store the SMTP password via the Sprint-16 `managed_secrets`
   subsystem (AES-256-GCM, write-only masked in the UI).
3. Read config and secret per-send rather than at startup — sends are infrequent
   and this gives zero-staleness without a 30-second cache window.
4. Retain full env-var fallback per CLAUDE.md convention.
5. Add a rate-limited, audited admin test-send endpoint.
6. Add Mailpit to `docker-compose.test.yml` as the integration test mail sink.

---

## 2. Requirements in scope

These IDs are targets for task 19.2 (requirements-author). The module prefix is
`EMAIL` for backend concerns and `FEADMIN` (continuing the existing series) for
frontend concerns.

| ID | Title | Verification |
|---|---|---|
| REQ-EMAIL-001 | Mailer interface | Inspection |
| REQ-EMAIL-002 | SMTP encryption modes (none / starttls / tls) | Test |
| REQ-EMAIL-003 | Optional AUTH (empty username disables) | Test |
| REQ-EMAIL-004 | Config precedence (admin UI > env vars) | Test |
| REQ-EMAIL-005 | Password in managed_secrets; env fallback | Test |
| REQ-EMAIL-006 | IsConfigured() | Test |
| REQ-EMAIL-007 | Send failures must not block caller | Inspection |
| REQ-EMAIL-008 | Test-send endpoint | Demonstration |
| REQ-EMAIL-009 | Test-send rate limit (5 req/min per admin) | Test |
| REQ-EMAIL-010 | Audit event for test-send (name-only, no secret) | Test |
| REQ-EMAIL-011 | SMTP error surfaces to admin; password never echoed | Inspection |
| REQ-FEADMIN-600 | Email section in SystemConfigView | Demonstration |
| REQ-FEADMIN-601 | Encryption dropdown | Demonstration |
| REQ-FEADMIN-602 | SMTP password write-only masked badge | Inspection |
| REQ-FEADMIN-603 | Test-send button with to-address input | Demonstration |
| REQ-FEADMIN-604 | Plain-language EXPLANATIONS for all email keys | Inspection |

Existing requirement REQ-USER-005 (password-reset email) is satisfied transitively:
the refactored call site still satisfies the same observable behavior.

---

## 3. Data model

### 3.1 system_config additions (migration 016)

Five new rows are added to `system_config`. The table schema itself does not change;
only new seed rows are inserted.

```
key                  | type   | constraint         | default | notes
---------------------|--------|--------------------|---------|------
smtp_host            | text   | non-empty when set |         | empty = "not configured"
smtp_port            | text   | integer 1–65535    | 587     | stored as text per existing table convention
smtp_from            | text   | non-empty when set |         | RFC 5321 envelope sender
smtp_username        | text   |                    |         | empty = no AUTH
smtp_encryption      | text   | none|starttls|tls  | starttls|
```

Rationale for keeping `smtp_port` as text: every other `system_config` value is
stored as text and parsed in Go. Introducing a dedicated integer column type would
require a schema change that the existing migration pattern does not support cleanly.

### 3.2 managed_secrets addition

`smtp_password` is added to the `knownSecrets` allowlist in `internal/admin/secrets.go`.
No schema migration required — the `managed_secrets` table accepts any `name` that
passes the allowlist check.

Env fallback variable: `SMTP_PASSWORD` (the existing variable read in `main.go`).

### 3.3 Migration 016

File: `migrations/016_email_config.sql`

```sql
-- migrations/016_email_config.sql
-- Sprint 19 — Admin-configurable email subsystem.
-- REQ-EMAIL-001..REQ-EMAIL-011
-- Adds SMTP config keys to system_config seed rows.
-- Rollback: DELETE FROM system_config WHERE key IN
--   ('smtp_host','smtp_port','smtp_from','smtp_username','smtp_encryption');
BEGIN;

INSERT INTO schema_migrations (version) VALUES ('016_email_config')
    ON CONFLICT (version) DO NOTHING;

-- Seed rows use INSERT ... ON CONFLICT DO NOTHING so re-running the migration
-- on an existing DB with admin-set values is a no-op.
INSERT INTO system_config (key, value) VALUES
    ('smtp_host',       ''),
    ('smtp_port',       '587'),
    ('smtp_from',       ''),
    ('smtp_username',   ''),
    ('smtp_encryption', 'starttls')
ON CONFLICT (key) DO NOTHING;

COMMIT;
```

Rollback path: the `DELETE` statement in the comment above removes the seed rows.
Existing data is unaffected. The `managed_secrets` row for `smtp_password`, if present,
can be removed via the admin UI (DELETE /api/v1/admin/secrets/smtp_password).

### 3.4 Config key validation rules (backend)

Added to `validateConfigValue` in `internal/admin/config_handler.go` and to the
`allowedKeys` map:

| Key | Rule |
|---|---|
| `smtp_host` | any non-negative-length string (empty is valid = not configured) |
| `smtp_port` | integer in range 1–65535 |
| `smtp_from` | any string (validated at send time, not at config time) |
| `smtp_username` | any string (empty = no AUTH) |
| `smtp_encryption` | one of `none`, `starttls`, `tls` |

`smtp_host` being empty is explicitly allowed because it is the "not configured" state.
Operators who clear the host to disable email do not see a validation error.

---

## 4. `internal/email` package

### 4.1 Directory

`internal/email/` — new package. No sub-packages.

### 4.2 Mailer interface

```go
// Mailer is the single outbound-email abstraction used across the application.
// Implementations must be safe for concurrent use.
type Mailer interface {
    // Send delivers a single message. subject and body are plain text.
    // body may contain a pre-formatted RFC 2822 body, or callers may pass
    // the MIME-structured body they construct (the implementation does not
    // add MIME headers; callers who need HTML must build the body themselves).
    Send(ctx context.Context, to, subject, body string) error

    // IsConfigured returns true when the Mailer has enough configuration to
    // attempt a send. Callers use this to surface "no email sent — email not
    // configured" notices without attempting a send.
    IsConfigured() bool
}
```

Interface design rationale:

- `Send(ctx, to, subject, body string)` covers password-reset (Sprint 19), welcome
  email (Sprint 20), and 2FA OTP (Sprint 21) without redesign. None of these require
  CC/BCC, attachments, or HTML at this time. If HTML is needed in a future sprint a
  `SendHTML(ctx, to, subject, plainBody, htmlBody string)` method can be added to a
  wider interface or the body convention can expand — neither change breaks the current
  interface consumers.
- `IsConfigured()` is a pure read and can be called by Sprint 20's account-creation
  path to decide whether to show the "no email sent" admin notice, and by Sprint 21 to
  gate the 2FA toggle, without attempting a send.
- No `Message` struct abstraction. Keeping the method flat avoids speculative
  abstraction. A struct can be introduced when a third call site with genuinely
  different field needs appears.

### 4.3 ConfigLoader interface (internal to the package)

```go
// ConfigLoader is satisfied by *admin.ConfigService.
// The email package depends on this narrow interface, not the full ConfigService,
// to avoid a circular import.
type ConfigLoader interface {
    GetString(key string) string
}

// SecretGetter is satisfied by *admin.SecretProvider.
type SecretGetter interface {
    Get(ctx context.Context, name string) string
}
```

### 4.4 SMTPMailer

```go
type SMTPMailer struct {
    config  ConfigLoader
    secrets SecretGetter
    // env vars are captured at construction time to preserve the env-fallback
    // semantics described in section 5.2 without needing to call os.Getenv at
    // send time on every request.
    envHost       string
    envPort       int
    envFrom       string
    envUsername   string
    envPassword   string
    envEncryption string
}
```

`NewSMTPMailer(config ConfigLoader, secrets SecretGetter, envVars SMTPEnvVars) *SMTPMailer`

`SMTPEnvVars` is a plain struct holding the five values read from the environment in
`main.go` at startup. This makes the env-capture explicit and testable.

### 4.5 Config resolution (per-send)

Config is resolved on every `Send` / `IsConfigured` call:

```
resolved_host       = config.GetString("smtp_host")        || envVars.Host
resolved_port       = config.GetString("smtp_port")        || envVars.Port (default 587)
resolved_from       = config.GetString("smtp_from")        || envVars.From
resolved_username   = config.GetString("smtp_username")    || envVars.Username
resolved_encryption = config.GetString("smtp_encryption")  || envVars.Encryption (default "starttls")
resolved_password   = secrets.Get(ctx, "smtp_password")    (already handles env fallback via SecretProvider)
```

Admin config wins over env var for every field. `secrets.Get` internally applies the
managed > env fallback, so SMTP_PASSWORD env is the fallback when no managed secret row
exists and `VALORY_SECRET_KEY` is absent.

Rationale for read-per-send: email sends are rare (user-triggered, not on every
request). Reading config per-send provides zero-staleness — a misconfiguration fix or
credential rotation is visible on the very next send with no TTL window. The 30-second
TTL cache in `SecretProvider` already provides DB-call amortization for the password
fetch. This is simpler than adding a second cache layer in the mailer.

### 4.6 Encryption mode behavior

| `smtp_encryption` value | Behavior |
|---|---|
| `none` | Plain TCP SMTP. No TLS anywhere on the connection. Appropriate for loopback relays (postfix/exim on localhost) or internal relays on trusted networks. **Operator risk**: credentials and message content travel in plaintext on the network segment between Valory and the relay. This is acceptable for loopback (127.0.0.1) deployments; operators using `none` over a routable network segment accept the exposure. The admin UI explanation states this explicitly. |
| `starttls` (default) | TCP connection upgraded to TLS via SMTP STARTTLS. The existing `SMTPTransport.SendPasswordReset` behavior. This is the default and the safe choice for external providers (Gmail, SES, Postmark, etc.). |
| `tls` | Implicit TLS (SMTPS): TLS wraps the connection from the first byte, typically on port 465. Required by some providers. Uses `tls.Dial` before handing off to the SMTP client. |

### 4.7 AUTH behavior

When `resolved_username` is empty: no `smtp.Auth` object is created and `conn.Auth` is
not called. The connection proceeds unauthenticated. This enables loopback postfix/exim
deployments that do not require authentication.

When `resolved_username` is non-empty: `smtp.PlainAuth("", username, password, host)`
is used, identical to the existing `SMTPTransport`.

### 4.8 IsConfigured()

Returns `true` when `resolved_host != ""`. No connection is attempted.

### 4.9 NoOpMailer

```go
// NoOpMailer logs sends to an io.Writer instead of connecting to SMTP.
// Used when IsConfigured() == false, or in development when no SMTP host is set.
type NoOpMailer struct {
    Out io.Writer
}
```

`Send` writes `[email] to=<to> subject=<subject> (noop — not configured)` to `Out`.
`IsConfigured()` returns `false`.

### 4.10 Wiring in main.go

```go
// Construct the mailer from the existing env reads + the admin config + secrets.
smtpEnv := email.SMTPEnvVars{
    Host:       smtpHost,
    Port:       smtpPort,
    From:       smtpFrom,
    Username:   "", // no existing SMTP_USERNAME env var — defaulted to empty
    Encryption: "starttls",
    // Password resolution is handled by secretProvider internally.
}
mailer := email.NewSMTPMailer(configSvc, secretProvider, smtpEnv)
```

The `smtpPassword` local variable read in `main.go` is removed; password resolution
moves entirely to `SecretProvider.Get(ctx, "smtp_password")` which already handles
the `SMTP_PASSWORD` env fallback via the `strings.ToUpper(name)` convention in
`resolve()`.

---

## 5. Refactoring `internal/user`

### 5.1 EmailTransport interface — stays in place, adapts

The existing `EmailTransport` interface in `internal/user/email.go`:

```go
type EmailTransport interface {
    SendPasswordReset(ctx context.Context, toAddress, rawToken string) error
}
```

This interface remains. Removing it would break `user.Service` and tests in a
single commit with other changes. Instead, a thin adapter satisfies it:

```go
// mailerAdapter wraps email.Mailer so it satisfies the legacy EmailTransport
// interface. This eliminates the SMTPTransport/NoOpTransport pair from the
// user package without touching user.Service or user.Handler.
type mailerAdapter struct {
    m      email.Mailer
    fromAddr string
}

func (a *mailerAdapter) SendPasswordReset(ctx context.Context, toAddress, rawToken string) error {
    subject := "Password Reset"
    body := fmt.Sprintf(
        "Your password reset token: %s\r\n\r\nThis token expires in 1 hour.\r\n",
        rawToken,
    )
    return a.m.Send(ctx, toAddress, subject, body)
}
```

`mailerAdapter` lives in `internal/user/email.go` alongside the existing interface.
`user.SMTPTransport` and `user.NoOpTransport` are deleted. `user.NewEmailTransport` is
replaced by `user.NewMailerAdapter(m email.Mailer) EmailTransport`.

`main.go` wiring change:

```go
// Before (Sprint 16):
emailTransport := user.NewEmailTransport(smtpHost, smtpPort, smtpFrom, smtpPassword, log.Writer())

// After (Sprint 19):
emailTransport := user.NewMailerAdapter(mailer)
```

Churn assessment: one line in `main.go`, two structs deleted from `internal/user/email.go`,
one adapter struct added. `user.Service`, `user.Handler`, and all tests that inject a
`MockEmailTransport` or `NoOpTransport` via the interface are unaffected.

### 5.2 Startup warning

After wiring, `main.go` logs:

```
if !mailer.IsConfigured() {
    log.Printf("server: WARN: SMTP is not configured; email features will be unavailable")
}
```

This mirrors the existing Anthropic key warning pattern.

---

## 6. Admin test-send endpoint

### 6.1 Route

```
POST /api/v1/admin/config/email/test
```

Mounted inside the existing `r.Route("/admin/config", ...)` group in `main.go`, which
already applies `auth.RequireRole("admin")` and `security.CSRFMiddleware`.

### 6.2 Handler location

`internal/admin/config_handler.go` — added as a new method on `AdminConfigHandler`.
`AdminConfigHandler` receives `email.Mailer` as a new constructor parameter.

### 6.3 Request / response

Request body (max 1 KiB):
```json
{ "to": "admin@example.com" }
```

Validation: `to` must be non-empty. No RFC 5322 regex enforcement at this layer —
SMTP error messages provide sufficient feedback for operator use.

Success response `200 OK`:
```json
{ "ok": true, "message": "Test email sent successfully." }
```

SMTP error response `502 Bad Gateway` (SMTP dial/auth/send error):
```json
{ "ok": false, "smtp_error": "dial tcp 127.0.0.1:587: connection refused" }
```

Not-configured response `503 Service Unavailable`:
```json
{ "ok": false, "smtp_error": "email is not configured" }
```

Rate limit response `429 Too Many Requests`:
```json
{ "error": "test-send rate limit exceeded; try again in 60 seconds" }
```

### 6.4 SMTP error sanitization

The raw `error.Error()` string from the dial/auth/send is included in `smtp_error`
with one rule: the string must not contain the SMTP password. The implementation
scans the resolved password value from the mailer and replaces any occurrence in the
error string with `[REDACTED]` before writing the response. In practice SMTP library
errors (`net/smtp`) never echo the password back — they carry TCP errors, SMTP reply
codes, and server banners — but the sanitization is a defense-in-depth measure.
Connection refused, authentication failure responses from the SMTP server, and banner
text are all valuable operator debugging information and are included as-is after the
redaction pass.

### 6.5 Rate limit

5 requests per admin per 60-second window, stored in a new `email_test_send_attempts`
table (see migration 016 extension below). The implementation mirrors
`security.CheckAndRecordPasswordReset` using a pg advisory lock keyed on the admin
user ID.

Migration 016 extension:

```sql
CREATE TABLE IF NOT EXISTS email_test_send_attempts (
    admin_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    attempted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS email_test_attempts_idx
    ON email_test_send_attempts (admin_id, attempted_at);
```

No RLS required — this table is only ever read/written by the admin handler under the
`valory_app` role.

### 6.6 Audit event

Action: `email.test_send`  
Target type: `system_config`  
Target ID: null  
Payload: `{"to": "<address>", "ok": true|false}`

The payload never includes the SMTP password, last4, or any secret value.

---

## 7. Agent interactions

No agent orchestration is involved. Email is a synchronous operation called from HTTP
handlers and the future account-lifecycle service.

Sequence for a password-reset send (post-refactor, behavior unchanged):

```
Client                   user.Handler          user.Service          email.Mailer (SMTPMailer)
  |                           |                     |                         |
  |-- POST /password-reset --> |                     |                         |
  |                           |-- RequestPasswordReset(ctx, username) -------> |
  |                           |                     |-- Send(ctx, to, subj, body) -->
  |                           |                     |     [resolves config+secret per-send]
  |                           |                     |<-- err (ignored, anti-enum) ----
  |<-- 200 OK (always) ----   |                     |                         |
```

The anti-enumeration contract in `user.Service.RequestPasswordReset` is preserved:
send errors are not returned to the caller.

---

## 8. Failure posture

### 8.1 Send failures never block the caller

`user.Service.RequestPasswordReset` already discards the send error (anti-enumeration).
Sprint 20's welcome-email path must follow the same pattern: the account is created
regardless of send outcome. The caller receives a flag indicating whether email was sent,
not an error that prevents the operation.

Specifically, for Sprint 20 the `CreateUser` response will include:
```json
{ "user": {...}, "email_sent": true|false, "email_error": "smtp_error_string_or_null" }
```
This is Sprint 20 design — recorded here as a constraint the mailer must support.

### 8.2 Logging on send failure

When `SMTPMailer.Send` returns an error, the caller logs:
```
log.Printf("email: send failed to=%s subject=%q: %v", to, subject, err)
```
The `to` address is logged. The error string is logged after the same redaction pass
used in the test-send endpoint (password never in logs).

### 8.3 IsConfigured() — Sprint 20 / 21 consumers

```go
if !mailer.IsConfigured() {
    // Sprint 20: include in CreateUser response: email_sent=false, email_error="not configured"
    // Sprint 21: block 2FA enable toggle with UI message
}
```

---

## 9. Frontend — SystemConfigView "Email" section

### 9.1 Section placement

A new "Email" card (`<div class="email-card">`) is inserted in `SystemConfigView.vue`
between the "API Keys" card and the existing config fields, following the same visual
pattern as the `secrets-card`.

### 9.2 Config fields rendered in the Email section

The five SMTP keys (`smtp_host`, `smtp_port`, `smtp_from`, `smtp_username`,
`smtp_encryption`) are added to `CONFIG_KEYS` in `systemConfig.ts`. They are rendered
in the Email card rather than in the general field list, so the component template
applies conditional rendering:

```ts
export const EMAIL_CONFIG_KEYS = [
  'smtp_host', 'smtp_port', 'smtp_from', 'smtp_username', 'smtp_encryption'
] as const
```

Non-email keys continue to render in the existing loop over `CONFIG_KEYS`.

### 9.3 smtp_encryption field

A `<select>` dropdown replaces the generic `<input type="text">` for
`smtp_encryption`. Options: `none` ("None — plaintext SMTP"), `starttls`
("STARTTLS — upgrade after connect (recommended)"), `tls` ("TLS — implicit TLS
(port 465)"). The value bound to `formValues['smtp_encryption']`.

### 9.4 smtp_password — managed secret

`smtp_password` is added to the `knownSecrets` list surfaced by the existing
GET /api/v1/admin/secrets endpoint. The `SystemConfigView` Email card renders the
`smtp_password` entry from `secretsStore.secrets` using the identical write-only
masked pattern already used for `anthropic_api_key` and `brave_api_key`:

- Status badge: `Configured (••••<last4>)` / `Set via environment` / `Not configured`
- Password input: `type="password"`, placeholder "Enter new value to update",
  `autocomplete="new-password"`
- Save / Clear buttons

No changes to `systemConfig.ts` `useSystemConfigStore` are required — the store
already fetches all secrets from GET /api/v1/admin/secrets and the `smtp_password`
row will appear automatically once the backend allowlist is updated.

### 9.5 Test-send UI

Within the Email card, below the SMTP fields:

```
[ Test email to: _____________________ ] [ Send test email ]
[ status message area ]
```

State variables added to `SystemConfigView.vue`:
- `testEmailTo: ref('')`
- `testEmailSending: ref(false)`
- `testEmailResult: ref<{ok: boolean, message: string} | null>(null)`

`sendTestEmail()` calls `POST /api/v1/admin/config/email/test` with `{ to: testEmailTo.value }`.
On success: green banner "Test email sent successfully."
On SMTP error: red banner "Test failed: <smtp_error value from response>."
On 429: yellow banner "Rate limited — try again in 60 seconds."

The SMTP error string from the backend is rendered verbatim in the banner. It never
contains the password (sanitized by the backend).

### 9.6 EXPLANATIONS additions (systemConfig.ts)

```ts
'smtp_host':
  'The hostname or IP address of your SMTP server. Leave empty to disable email. ' +
  'Examples: smtp.gmail.com (Gmail with an App Password), ' +
  'email-smtp.us-east-1.amazonaws.com (Amazon SES), ' +
  'localhost (local postfix/exim relay — no auth needed, use encryption: none).',

'smtp_port':
  'The SMTP server port. Common values: 587 (STARTTLS — recommended for external ' +
  'providers), 465 (implicit TLS / SMTPS), 25 (unauthenticated local relay). ' +
  'Default: 587.',

'smtp_from':
  'The envelope sender address shown in the From: header of all outbound emails ' +
  '(e.g. noreply@yourschool.edu). Must be an address your SMTP server is authorized ' +
  'to send from. Leave empty if SMTP host is not configured.',

'smtp_username':
  'The username for SMTP authentication. Leave empty to skip authentication entirely — ' +
  'required for localhost relays (postfix/exim) that do not need credentials. ' +
  'For Gmail, enter your full Gmail address and use an App Password (not your account password).',

'smtp_encryption':
  'How the connection to the SMTP server is secured. ' +
  'STARTTLS (recommended): starts a plain connection then upgrades it to TLS — ' +
  'use this for most external providers on port 587. ' +
  'TLS: wraps the entire connection in TLS from the start (implicit TLS) — ' +
  'use this for providers that require port 465. ' +
  'None: no encryption — only safe for a loopback relay on localhost. ' +
  'Using None over a routable network sends credentials and message content in plaintext.',

'smtp_password':
  'The SMTP password or app-specific password for authentication. ' +
  'If set here, this value takes precedence over the SMTP_PASSWORD environment variable. ' +
  'Leave the username empty if your relay does not require authentication. ' +
  'For Gmail: generate an App Password in your Google Account security settings — ' +
  'do NOT use your Gmail account password here. ' +
  'Changes take effect immediately on the next email send.',
```

---

## 10. Mailpit in docker-compose.test.yml

### 10.1 Service definition

```yaml
  mailpit:
    image: axllent/mailpit:latest
    ports:
      - "11025:1025"   # SMTP sink — Go integration tests send here
      - "18025:8025"   # Mailpit REST API — Go tests assert delivery here
    environment:
      MP_MAX_MESSAGES: 500
      MP_DATABASE: ""   # in-memory; tests start with a clean slate per run
    healthcheck:
      test: ["CMD", "wget", "--quiet", "--spider", "http://localhost:8025/api/v1/info"]
      interval: 5s
      timeout: 3s
      retries: 10
```

Host ports `11025` and `18025` are chosen to avoid collisions with common local
SMTP (25, 587) and the postgres-test port (55432).

### 10.2 Integration test usage

Test environment variables (set when running `go test ./...` against the test compose
stack):

```
SMTP_HOST=localhost
SMTP_PORT=11025
SMTP_FROM=test@valory.test
SMTP_ENCRYPTION=none
# SMTP_USERNAME left empty — Mailpit accepts unauthenticated connections
MAILPIT_API=http://localhost:18025
```

Go integration tests assert delivery via the Mailpit REST API:

```
GET http://localhost:18025/api/v1/messages
```

Response shape (Mailpit v1 API):
```json
{
  "messages": [
    {
      "ID": "...",
      "From": { "Address": "test@valory.test" },
      "To": [{ "Address": "student@example.com" }],
      "Subject": "Password Reset",
      "Created": "...",
      ...
    }
  ],
  "total": 1
}
```

The test helper `internal/testutil/mailpit.go` wraps these API calls:

```go
func WaitForMessage(t *testing.T, apiBase, toAddr, subject string) MailpitMessage
func ClearMessages(t *testing.T, apiBase string)
```

`WaitForMessage` polls with a 5-second timeout and 100ms interval. `ClearMessages`
calls `DELETE /api/v1/messages` before each test case to ensure isolation.

### 10.3 E2E test assertions

The e2e test for the config round-trip (task 19.5) asserts:
1. Setting SMTP config via `PATCH /api/v1/admin/config` and sending a test email
   results in a message appearing in Mailpit.
2. `GET /api/v1/admin/secrets` never returns a `value` field for `smtp_password`.
3. `PATCH /api/v1/admin/config` with `smtp_password` as a key returns `400 Bad Request`
   (password is not a config key; it routes through secrets).

---

## 11. Alternatives considered

### 11.1 Single config-cache read vs per-send read

**Considered:** cache the resolved SMTP config in `SMTPMailer` with a 30-second TTL,
mirroring `SecretProvider`.

**Rejected:** The email subsystem has exactly one periodic send path (password reset)
plus admin test-sends. None are on a hot path. The per-send read from `ConfigService`
is an O(1) map lookup under a read lock — negligible overhead. Adding a second TTL
cache would make the system harder to reason about and would mean a misconfiguration
fix is not reflected for up to 30 seconds. `SecretProvider` already caches the
password independently, so the DB is not hit per-send for the secret.

### 11.2 New `EmailConfig` table vs system_config rows

**Considered:** a dedicated `email_config` table with typed columns.

**Rejected:** all other admin-configurable values live in `system_config`. A new table
would require a wider schema migration, a new repository type, and a separate UI
section that doesn't fit the existing pattern. The five SMTP keys are no more complex
than the existing 15 keys.

### 11.3 smtp_password in system_config vs managed_secrets

**Considered:** storing smtp_password as a `system_config` key.

**Rejected:** `system_config` values are returned in plaintext by GET /api/v1/admin/config
and stored unencrypted in the database. The Sprint-16 managed_secrets subsystem exists
precisely for write-only encrypted values. `smtp_password` is a credential and must use
that subsystem.

### 11.4 Replacing EmailTransport interface entirely

**Considered:** deleting `internal/user.EmailTransport` and injecting `email.Mailer`
directly into `user.Service`.

**Rejected:** it requires changes to `user.Service`, its constructor, all tests that
mock `EmailTransport`, and `main.go` wiring — a wide diff in a non-feature area.
The adapter pattern achieves the same behavior with a two-line change in `main.go` and
zero changes to `user.Service` or its tests. Sprint 20 may be a natural cleanup point.

### 11.5 Rate limiting via in-process token bucket

**Considered:** an in-memory per-admin token bucket for the test-send rate limit.

**Rejected:** would not survive a server restart (operator spams test-send, restarts
container to bypass the limit). The DB advisory lock pattern used for password resets
is already proven in the codebase and persists across restarts.

---

## 12. Open questions

None for this sprint. The following are forward-looking notes for downstream sprints:

- **Sprint 20 welcome-email body:** the format (HTML vs text, branding, login URL
  injection) is a Sprint 20 design concern. The `Mailer.Send` body parameter accepts
  any string; callers own body construction.
- **Sprint 21 OTP email body:** same as above.
- **smtp_username env fallback:** there is no existing `SMTP_USERNAME` env variable.
  The `SMTPEnvVars.Username` field defaults to empty at construction time. If operators
  currently rely on `SMTP_PASSWORD` env without a username (because the existing
  `SMTPTransport` uses `s.From` as the username), the Sprint 19 backend engineer must
  add a `SMTP_USERNAME` env var read in `main.go` and document the migration path for
  existing deployments.

---

## 13. Acceptance criteria

The following must all be true for Sprint 19 to close.

### Email subsystem (AC-EMAIL)

- AC-EMAIL-01: `POST /api/v1/admin/config/email/test` with valid `to` and correctly
  configured SMTP delivers a message to Mailpit captured by an integration test.
- AC-EMAIL-02: With `smtp_encryption=none` and an empty `smtp_username`, the mailer
  connects to Mailpit on port 11025 without TLS and without sending AUTH.
- AC-EMAIL-03: With `smtp_encryption=starttls` pointing at a STARTTLS-capable test
  server, the mailer upgrades the connection.
- AC-EMAIL-04: `IsConfigured()` returns false when `smtp_host` is empty in both config
  and env. Returns true when either source provides a non-empty host.
- AC-EMAIL-05: A send failure (dial error, auth error, SMTP reject) does not propagate
  through `user.Service.RequestPasswordReset` — the endpoint returns 200.
- AC-EMAIL-06: The SMTP password value never appears in any HTTP response body, log
  line, or audit payload in any test scenario.
- AC-EMAIL-07: `GET /api/v1/admin/secrets` lists `smtp_password` with `source: "none"`
  before it is configured, and `source: "managed"` with a masked `last4` after a PUT.
- AC-EMAIL-08: `PATCH /api/v1/admin/config` with the five SMTP keys succeeds and the
  values are reflected in a subsequent GET.
- AC-EMAIL-09: `PATCH /api/v1/admin/config` with `smtp_encryption=invalid` returns 422.
- AC-EMAIL-10: `PATCH /api/v1/admin/config` with `smtp_port=99999` returns 422.
- AC-EMAIL-11: Test-send rate limit: 6th request within 60 seconds returns 429.
- AC-EMAIL-12: Test-send audit event appears in `GET /api/v1/audit` with action
  `email.test_send` and no secret value in the payload.

### Frontend (AC-FE)

- AC-FE-01: SystemConfigView renders an "Email" section with five text/select fields
  and the `smtp_password` managed-secret widget.
- AC-FE-02: `smtp_encryption` renders as a `<select>` with three options.
- AC-FE-03: `smtp_password` renders with the Sprint-16 write-only masked badge pattern.
- AC-FE-04: "Send test email" button is present; entering an address and clicking it
  calls the test-send endpoint and shows the result in a status banner.
- AC-FE-05: EXPLANATION text is visible (via toggle) for all five config keys and for
  `smtp_password`.

---

## 14. Per-task implementation checklists

### Task 19.3 — Backend (senior-engineer)

- [ ] Add `smtp_password` to `knownSecrets` in `internal/admin/secrets.go`
- [ ] Add `envVarName` mapping for `smtp_password` → `SMTP_PASSWORD` in
      `internal/admin/secrets_handler.go`
- [ ] Create `internal/email/` package with:
  - [ ] `mailer.go`: `Mailer` interface, `ConfigLoader` interface, `SecretGetter`
        interface, `SMTPEnvVars` struct, `SMTPMailer` struct and methods, `NoOpMailer`
        struct and methods, `NewSMTPMailer` constructor
  - [ ] SMTP dial logic for `none`, `starttls`, `tls` encryption modes
  - [ ] AUTH-optional logic (empty username skips AUTH)
  - [ ] Password redaction helper for error strings
- [ ] Write migration `migrations/016_email_config.sql` (seed rows + rate-limit table)
- [ ] Update `internal/admin/config_handler.go`:
  - [ ] Add five SMTP keys to `allowedKeys`
  - [ ] Add validation cases for the five keys in `validateConfigValue`
  - [ ] Add `Mailer` field to `AdminConfigHandler`; update `NewConfigHandler` signature
  - [ ] Add `testEmailSend` handler method
  - [ ] Register `POST /test` route in `Routes()`
  - [ ] Add `email_test_send` rate limit helper (mirrors `security.CheckAndRecordPasswordReset`)
- [ ] Update `internal/user/email.go`:
  - [ ] Delete `SMTPTransport`, `NoOpTransport`, `NewEmailTransport`
  - [ ] Add `mailerAdapter` struct and `NewMailerAdapter` constructor
- [ ] Update `cmd/server/main.go`:
  - [ ] Read `SMTP_USERNAME` env var (new)
  - [ ] Construct `email.SMTPEnvVars` from env reads
  - [ ] Construct `email.NewSMTPMailer(configSvc, secretProvider, smtpEnv)`
  - [ ] Wire `user.NewMailerAdapter(mailer)` → `emailTransport`
  - [ ] Pass `mailer` to `admin.NewConfigHandler`
  - [ ] Log startup WARN when `!mailer.IsConfigured()`
  - [ ] Remove `smtpPassword` local variable (now inside SecretProvider)
- [ ] Add `@{"req": [...]}` annotations to all new functions/types

### Task 19.4 — Frontend (junior-engineer)

- [ ] Update `systemConfig.ts`:
  - [ ] Add five SMTP keys to `CONFIG_KEYS`
  - [ ] Define `EMAIL_CONFIG_KEYS` constant
  - [ ] Add labels, hints, and EXPLANATIONS for all five keys and `smtp_password`
  - [ ] Add validation: `smtp_port` must be integer 1–65535 (client-side hint, not a
        blocking guard — server validates)
- [ ] Update `SystemConfigView.vue`:
  - [ ] Add Email card section with conditional rendering for `EMAIL_CONFIG_KEYS`
  - [ ] Render `smtp_encryption` as a `<select>` dropdown
  - [ ] Render `smtp_password` from `secretsStore.secrets` using the existing
        managed-secret widget pattern
  - [ ] Add test-send UI: `testEmailTo` input + "Send test email" button +
        status banner
  - [ ] Add `sendTestEmail()` function calling `POST .../email/test`
  - [ ] Handle 200, 502, 503, 429 response shapes
  - [ ] Add `@{"req": [...]}` annotations for new REQ-FEADMIN-600..604 IDs

### Task 19.5 — Tests (test-author)

- [ ] Add Mailpit service to `docker-compose.test.yml` per section 10.1
- [ ] Create `internal/testutil/mailpit.go` with `WaitForMessage` and `ClearMessages`
- [ ] Unit tests for `internal/email`:
  - [ ] `IsConfigured()` returns false when host empty, true otherwise
  - [ ] Password redaction strips the password from error strings
  - [ ] `SMTPEnvVars` fallback: config empty → env var used
  - [ ] Config precedence: config non-empty → env var ignored
- [ ] Integration tests (require Mailpit container):
  - [ ] `none` encryption: message delivered, no TLS negotiation (verified by
        Mailpit API)
  - [ ] Password-reset flow end-to-end: request → Mailpit captures email with
        correct subject
  - [ ] Test-send endpoint: 200 + message in Mailpit
  - [ ] Test-send rate limit: 6th call within window returns 429
  - [ ] Secret never echoed: PUT smtp_password → GET /admin/secrets confirms no
        `value` field; test-send SMTP error response contains `[REDACTED]` not the
        actual password value
- [ ] Config round-trip test: PATCH five SMTP keys → GET returns same values
- [ ] Validation tests: smtp_port out of range → 422; smtp_encryption invalid → 422
- [ ] Audit test: test-send audit entry present, payload has no secret value
- [ ] Vitest unit tests for frontend: sendTestEmail success / SMTP error / 429 paths

---

## Revision note (software-lead)

Migration number corrected 014 → 016: `014_anthropic_base_url.sql` already
exists, and `015_professor_max_tokens.sql` is reserved by the in-flight
(unreviewed) max-tokens feature in the working tree. The email migration is
`migrations/016_email_config.sql`.

## Revision note 2 (software-lead, folding test-author finding)

Mailpit healthcheck corrected curl -> wget: the axllent/mailpit image is
Alpine-based and ships wget, not curl. docker-compose.test.yml uses the wget
form.
