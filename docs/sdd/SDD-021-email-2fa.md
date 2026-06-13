# SDD-021 — Email-Based Two-Factor Authentication

**Sprint:** 21
**Status:** DRAFT
**Author:** design-author
**Date:** 2026-06-12
**Implements tasks:** 21.1 (this document), 21.3 (backend), 21.4 (frontend), 21.5 (tests)
**Depends on:** SDD-019 (email subsystem, `email.Mailer`), SDD-020 (account lifecycle, `must_change_password` interposition pattern)

---

## 1. Overview

### Problem

Valory accounts are protected only by a password. An intercepted or guessed password gives an attacker a complete session. Admins require a second factor for higher-assurance deployments. Students benefit from the same protection when their institution requires it.

### Approach

A global admin toggle activates email-based OTP as a mandatory second factor for all users. The login flow becomes two phases: phase one exchanges credentials for a short-lived pending-2FA token; phase two exchanges a correct OTP for a full session. The pending token cannot access any application route. The OTP is delivered via the existing `email.Mailer` interface (Sprint 19). The toggle enable path is gated by two prerequisites that the backend enforces at write time: email must be configured (`Mailer.IsConfigured()`) and a test-send must have previously succeeded (a persisted timestamp). A break-glass environment variable allows an operator with shell access to recover from a sole-admin lockout without bypassing the password check.

This design follows the same middleware-interposition pattern established by `MustChangePasswordMiddleware` (Sprint 20) and the same rate-limit pattern established by `checkAndRecordEmailTestSend` (Sprint 19).

---

## 2. Requirements in scope

Requirements are authored separately in task 21.2. The IDs below are the plan; the requirements-author assigns the canonical JSON.

| ID | Statement |
|---|---|
| REQ-AUTH-015 | Two-phase login: password OK produces a pending-2FA state, not a full session |
| REQ-AUTH-016 | OTP: 6-digit numeric, hashed at rest (SHA-256), 10-minute TTL, single-use |
| REQ-AUTH-017 | OTP attempt cap: 5 failures invalidate the pending token; resend throttle: 1 per 60 s, 10 per 24 h |
| REQ-AUTH-018 | Global toggle `two_factor_enabled`: enabling blocked until email configured + test-send succeeded |
| REQ-AUTH-019 | Admin per-user 2FA reset endpoint |
| REQ-AUTH-020 | Break-glass `VALORY_2FA_BREAK_GLASS`: bypasses 2FA for admin only; never bypasses password; loudly audited |
| REQ-AUTH-021 | No-email-user behavior when 2FA is on |
| REQ-FEAUTH-200 | OTP entry screen: 6-digit input, resend with countdown, attempt-error, restart link |
| REQ-FEAUTH-201 | Auth store two-phase state: `pendingTwoFactor` flag not treated as authenticated |
| REQ-FEAUTH-202 | Router guard: pending-2FA route is `/login/verify`; all protected routes redirect there while pending |
| REQ-FEADMIN-700 | Admin 2FA toggle in SystemConfigView with prerequisite explanation |

Frontend requirement module prefix convention:
- `REQ-FEAUTH-2xx` continues from the existing `REQ-FEAUTH-171` sequence.
- `REQ-FEADMIN-7xx` continues from `REQ-FEADMIN-604`.

---

## 3. Data model

### Migration 018

File: `backend/auth/migrations/018_email_2fa.sql` (or wherever the project's migration runner picks up numbered files — confirm with the senior engineer; the SDD-019 migration established the `email_test_send_attempts` table so 018 is the next slot).

```sql
-- 018_email_2fa.sql

-- pending_2fa holds short-lived pre-session state between password-OK and OTP-verify.
-- A row exists only for the duration of the pending window (max 10 minutes).
-- The table is NOT the sessions table; the auth middleware explicitly rejects
-- pending tokens so they cannot access protected routes.
CREATE TABLE pending_2fa (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- pending_token_hash: SHA-256 of the raw pending token sent to the client.
    -- Same hashing approach as sessions.token_hash (see token.go:HashToken).
    pending_token_hash TEXT   NOT NULL UNIQUE,
    -- otp_hash: SHA-256 of the 6-digit OTP string (e.g. "042817").
    -- SHA-256 is intentional here — see Section 4.2 for justification.
    otp_hash      TEXT        NOT NULL,
    -- attempt_count increments on each wrong OTP; row is deleted at attempt_count = 5.
    attempt_count INT         NOT NULL DEFAULT 0,
    -- last_resend_at enables the 60-second resend throttle.
    last_resend_at TIMESTAMPTZ,
    -- resend_count_24h is a rolling counter reset to 0 when resend_window_start is
    -- more than 24 hours ago. Maintained in application code, not a DB trigger.
    resend_count_24h INT      NOT NULL DEFAULT 0,
    resend_window_start TIMESTAMPTZ,
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index for cleanup job and expiry checks.
CREATE INDEX idx_pending_2fa_expires_at ON pending_2fa (expires_at);
-- Index for user-level lookups (admin reset, no-email warning).
CREATE INDEX idx_pending_2fa_user_id ON pending_2fa (user_id);

-- otp_rate_limits tracks per-user OTP send events for the daily cap.
-- Kept separate from pending_2fa so the daily cap survives pending row deletion.
-- Rows older than 24 hours are pruned at send time (no cron required).
CREATE TABLE otp_rate_limits (
    id            BIGSERIAL   PRIMARY KEY,
    user_id       UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sent_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_otp_rate_limits_user_sent ON otp_rate_limits (user_id, sent_at);

-- system_config seed: add two_factor_enabled (off by default) and
-- email_test_send_verified_at (NULL until a test-send has succeeded).
-- The migration uses INSERT ... ON CONFLICT DO NOTHING so re-runs are safe.
INSERT INTO system_config (key, value) VALUES ('two_factor_enabled', 'false')
    ON CONFLICT (key) DO NOTHING;

-- email_test_send_verified_at stores the timestamp of the most recent successful
-- test-send. The 2FA toggle enable path reads this key and rejects the write
-- when it is empty or NULL. It is updated by the test-send handler on success.
INSERT INTO system_config (key, value) VALUES ('email_test_send_verified_at', '')
    ON CONFLICT (key) DO NOTHING;
```

### Rollback path

```sql
-- 018_email_2fa_rollback.sql
DROP TABLE IF EXISTS otp_rate_limits;
DROP TABLE IF EXISTS pending_2fa;
DELETE FROM system_config WHERE key IN ('two_factor_enabled', 'email_test_send_verified_at');
```

No existing table is altered, so rollback is clean. The `email_test_send_verified_at` key
value is ephemeral configuration — losing it on rollback means the admin must re-run a
test-send before re-enabling 2FA, which is acceptable.

### users table

No new column is needed. The email column (nullable, migration 001/002) is read at
challenge-issue time to determine deliverability. No `two_fa_enabled` per-user flag
exists — the feature is global by PM decision (resolved decision 3).

### system_config additions

| Key | Type | Default | Purpose |
|---|---|---|---|
| `two_factor_enabled` | `"true"` / `"false"` | `"false"` | Global toggle |
| `email_test_send_verified_at` | RFC 3339 timestamp string or `""` | `""` | Prerequisites gate |

Both keys are added to `allowedKeys` in `config_handler.go`. `two_factor_enabled` requires cross-key validation at write time (see Section 5).

---

## 4. Core design decisions

### 4.1 Pending-2FA state mechanism: separate table vs. sessions with status column

**Decision: separate `pending_2fa` table.**

A `status` column on `sessions` (`"pending"` / `"active"`) requires the auth middleware to check that column on every request. It also means the sessions table would hold credentials that are not yet authenticated, muddying the invariant that "a row in sessions = an authenticated session". A separate table keeps the invariant clean: the auth middleware only reads `sessions`; pending rows live in `pending_2fa` and are deleted as soon as a full session is issued or the row expires. Expired pending rows are cheap to prune by index scan on `expires_at`.

The separate table also makes the attempt-count and resend-throttle columns obvious and co-located with the entity they govern.

### 4.2 Pending token delivery: response body

**Decision: return the pending token in the response body JSON (`{"pending_token": "<raw>", "two_factor_required": true}`) as a short-lived bearer value, NOT in a cookie.**

Rationale: the pending token must not be usable as a session token and must not be confused with one. The existing auth middleware reads `__Host-session` cookie and `Authorization: Bearer`. A pending token delivered as a cookie named `__Host-session` would be treated as a session token by the middleware. Using a different cookie name would require middleware changes and still risks a browser sending it on every request.

The response-body approach is simpler: the frontend stores the pending token in Pinia reactive state (not localStorage, not a cookie), uses it only for the single `POST /api/v1/auth/2fa/verify` call, and discards it immediately after — whether successful or failed. Because it is in-memory only, a page reload loses it (intentional: the user must restart login, which is the correct UX for a TTL-gated step).

Security note: the pending token is 32 bytes of CSPRNG output encoded as base64url (same as `IssueToken` in token.go), hashed with SHA-256 before storage. It is short-lived (10 minutes), single-use (row deleted on successful verify), and cannot access any route behind `NewAuthMiddleware`.

### 4.3 OTP hashing: SHA-256 vs. argon2id

**Decision: SHA-256 (reusing `HashToken` from token.go).**

argon2id is designed to slow down offline brute-force against a stolen hash database. For a 6-digit OTP (1,000,000 possibilities) the offline attack is trivially fast regardless of the hash function: a dump of `pending_2fa` gives an attacker 10^6 guesses to try in microseconds with SHA-256 or milliseconds with argon2id. Neither prevents offline exhaustion of a 6-digit space.

The actual controls against OTP guessing are:
1. **Attempt cap** (5 failures → row deleted, login must restart).
2. **10-minute TTL** (row expires before a slow-hash advantage matters).
3. **Online rate** (each guess requires an HTTP round-trip through the API).

argon2id would add ~100 ms server latency per verify request (same parameters as password hashing) with no meaningful security gain, and would slow the resend path unnecessarily. SHA-256 is correct here. This is the same approach used for session tokens in the existing codebase.

If `pending_2fa` rows were ever to persist for longer periods or contain higher-entropy secrets, this decision should be revisited.

### 4.4 test-send success persistence

**Decision: write RFC 3339 timestamp to `system_config` key `email_test_send_verified_at` on every successful test-send.**

The `testEmailSend` handler in `config_handler.go` already runs a transaction for the audit entry. After a successful send (`sendOK == true`), the handler additionally upserts `email_test_send_verified_at` to `time.Now().UTC().Format(time.RFC3339)` within the same transaction. This is a one-line SQL upsert — no structural change to the handler, only a new statement in the success branch.

When the admin subsequently patches `two_factor_enabled = "true"`, `validateConfigValue` reads `email_test_send_verified_at` from `ConfigService`. If the value is empty or cannot be parsed as a timestamp, the validation rejects with a clear error: `"two_factor_enabled: email must be configured and a test-send must have succeeded before enabling 2FA"`.

`Mailer.IsConfigured()` is also checked at validation time (not just at send time) by calling the mailer through an interface passed into `validateConfigValue` or by checking the `smtp_host` key directly from the config map (the simpler approach — see Section 5).

### 4.5 No-email-user behavior

**Decision: require all active admin accounts to have a non-empty email before the toggle can be enabled; warn (but do not block) if active student accounts lack email.**

Rationale:
- Admins are the accounts most at risk from 2FA lockout. An admin without an email address cannot receive an OTP and cannot log in at all when 2FA is on. Blocking the toggle if any active admin lacks email is a hard safety guarantee.
- Students without email are a softer problem: a student account with no email cannot receive an OTP, but the system can present a clear error ("Contact your administrator — your account has no email address on file") rather than silently failing. The admin should fix the account before the student needs to log in.
- Blocking the toggle for ANY student without email is too strict: a large institution may have thousands of students, some created before email was a requirement. Requiring a bulk fix before enabling 2FA for any user would make the feature unusable.

The toggle validation (step in `patchConfig`) queries:
```sql
SELECT COUNT(*) FROM users WHERE is_active = true AND role = 'admin' AND (email IS NULL OR email = '')
```
If count > 0: reject with `"two_factor_enabled: all active admin accounts must have an email address before enabling 2FA"`.

For students the response from `GET /api/v1/admin/config` includes a diagnostic field `users_without_email_count` (student role only) so the admin UI can render a warning. This is advisory only and does not block the toggle.

### 4.6 Break-glass

**Decision: `VALORY_2FA_BREAK_GLASS` environment variable. When set to a non-empty value and the user being authenticated is an admin, phase-one login returns a full session token directly (skipping phase two). The password check always runs first.**

Mechanics:
1. `Service.Login` reads `os.Getenv("VALORY_2FA_BREAK_GLASS")` after a successful password check and before issuing a pending token.
2. The condition is: break-glass value is non-empty AND user role is `"admin"`.
3. When triggered: issue a full session (same path as non-2FA login), emit a WARN log `"SECURITY: VALORY_2FA_BREAK_GLASS used for admin %s"`, and append audit event `2fa.break_glass_used` with payload `{"username": "<name>"}` (no OTP, no token value in the payload).
4. The break-glass bypass is invisible to the client: the response shape is the same as a normal login response (no `pending_token` field).

Justification for env-var approach: an operator who has shell access to set the environment variable already has root/container access to the host. They could read the database directly, modify the binary, or extract the KEK. The env-var break-glass grants no privilege beyond what they already have; it simply provides a safe, logged path to recover a legitimate admin account without requiring database surgery. The approach is analogous to how the KEK (`VALORY_SECRET_KEY`) is delivered — env-only, no UI.

The break-glass value is intentionally not validated against a specific expected string (the mere presence of a non-empty value suffices). This matches the principle that the operator controls the environment. If a specific value were required, it would need to be stored somewhere and could be discovered; a "set = active" toggle is simpler and the audit trail provides accountability.

**Security tradeoff noted:** any process that can read the container's environment can activate break-glass. In a correctly secured deployment, only the operator has this access. The audit event is the accountability control.

---

## 5. API contract

All endpoints are under `/api/v1/auth`. Existing routes are unchanged.

### 5.1 POST /api/v1/auth/login (modified)

When `two_factor_enabled` is `"true"`, a successful password check now returns a pending-2FA response instead of a full session.

**Request:** unchanged (`{"username": "...", "password": "..."}`)

**Response when 2FA is OFF or break-glass is active:** unchanged (200 with `token`, `role`, `expires_at`, `must_change_password`).

**Response when 2FA is ON and password is correct:**

```json
HTTP 202 Accepted
{
  "two_factor_required": true,
  "pending_token": "<base64url, 43 chars>",
  "expires_at": "2026-06-12T10:30:00Z"
}
```

No `__Host-session` cookie is set. No CSRF cookie is set. These are only set on full session issuance.

**Error responses:** unchanged (401 invalid credentials, 403 account disabled/locked — same opaque message to avoid username enumeration).

**Implementation note:** `Service.Login` returns a new discriminated result type:

```go
type LoginResult struct {
    // Exactly one of the following is non-nil.
    Session     *Session      // 2FA off or break-glass: full session issued
    PendingTwoFactor *PendingTwoFactorResult // 2FA on: pending state
}

type PendingTwoFactorResult struct {
    PendingToken string    // raw token; client sends this back in 5.2
    ExpiresAt    time.Time
}
```

The handler switches on the result type to decide which response to emit. Status 202 is used (not 200) to signal to the frontend that more action is needed; the frontend checks for `two_factor_required: true` in the body.

### 5.2 POST /api/v1/auth/2fa/verify

Exchanges a valid OTP for a full session. No auth middleware is applied to this route; the pending token is the credential.

**Request:**

```json
{
  "pending_token": "<the token from 5.1 response>",
  "otp": "042817"
}
```

**Success (200):**

```json
{
  "token": "<session token>",
  "role": "student",
  "expires_at": "2026-06-13T10:20:00Z",
  "must_change_password": false
}
```

`__Host-session` cookie and `__Host-csrf` cookie are set identically to the normal login path.

**Error responses:**

| Condition | Status | Body |
|---|---|---|
| Missing/malformed fields | 400 | `{"error": "invalid request"}` |
| Unknown pending token | 401 | `{"error": "invalid or expired code"}` |
| Expired pending token | 401 | `{"error": "invalid or expired code"}` |
| Wrong OTP (attempt < 5) | 401 | `{"error": "invalid or expired code", "attempts_remaining": 3}` |
| Wrong OTP (attempt = 5) | 401 | `{"error": "too_many_attempts"}` — row deleted |
| 2FA globally disabled | 404 | `{"error": "not found"}` — route disabled when toggle is off |

`attempts_remaining` is included in wrong-OTP responses so the frontend can show the user how many tries are left. It is omitted from the final failure (row gone).

**Why 401 for unknown/expired token and wrong OTP:** both look identical to the client to prevent oracle attacks where an attacker probes whether a guessed pending token is valid before trying OTPs.

### 5.3 POST /api/v1/auth/2fa/resend

Resends the OTP for an active pending token. Rate-limited.

**Request:**

```json
{
  "pending_token": "<token from 5.1>"
}
```

**Success (200):**

```json
{
  "expires_at": "2026-06-12T10:30:00Z",
  "resend_available_at": null
}
```

A new OTP is generated; the old `otp_hash` in `pending_2fa` is replaced. `last_resend_at` and `resend_count_24h` are updated. `expires_at` is NOT extended (same original expiry).

**Error responses:**

| Condition | Status | Body |
|---|---|---|
| Unknown/expired pending token | 401 | `{"error": "invalid or expired code"}` |
| 60-second resend throttle | 429 | `{"error": "resend_throttled", "resend_available_at": "<RFC3339>"}` |
| Daily cap (10 sends) exceeded | 429 | `{"error": "resend_daily_cap_exceeded"}` |
| Mailer not configured | 503 | `{"error": "email not configured"}` |

`resend_available_at` in the 429 response gives the frontend the exact time at which resend becomes available, enabling an accurate countdown without client-side clock arithmetic.

### 5.4 GET /api/v1/admin/users/{id}/2fa/reset (admin only, requires auth middleware + RequireRole("admin"))

Resets 2FA state for a user: deletes any `pending_2fa` row for that user. This unblocks a user who hit the attempt cap or whose pending token expired in a bad state.

**Response (204 No Content):** success (including when no row existed — idempotent).

**Error (404):** user not found.

Audit event: `2fa.reset` with payload `{"target_user_id": "<uuid>"}`.

### 5.5 PATCH /api/v1/admin/config (modified — new key validation)

`two_factor_enabled` is added to `allowedKeys`. Setting it to `"true"` triggers cross-key validation:

1. `smtp_host` must be non-empty in current config (email is configured).
2. `email_test_send_verified_at` must be a parseable non-empty RFC 3339 timestamp.
3. No active admin accounts may have a null or empty email.

If any condition fails: `422 Unprocessable Entity` with `validation_errors` array.

Setting `two_factor_enabled` to `"false"` has no prerequisites and succeeds immediately.

Audit events appended by `patchConfig` (existing mechanism): `config.change` with `{"keys_changed": ["two_factor_enabled"]}`. A dedicated audit event `two_factor_enabled` or `two_factor_disabled` is appended as a SECOND entry in the same DB transaction to provide a semantically named record for security review (in addition to the generic `config.change`). The second entry uses the same `audit.Entry` structure with `Action: "2fa.config.enabled"` or `"2fa.config.disabled"`.

The `GET /api/v1/admin/config` response is extended to include:

```json
{
  "config": { "two_factor_enabled": "false", ... },
  "two_fa_prerequisites": {
    "email_configured": true,
    "test_send_verified": true,
    "admins_without_email": 0,
    "students_without_email": 12
  }
}
```

This lets the frontend render the correct disabled-with-reason state for the toggle.

### 5.6 POST /api/v1/admin/config/email/test (modified)

On `sendOK == true`, the handler additionally upserts `email_test_send_verified_at` to the current UTC timestamp in the same transaction as the audit entry:

```sql
UPDATE system_config SET value = $1, updated_by = $2, updated_at = NOW()
WHERE key = 'email_test_send_verified_at'
```

This is the only mechanism that sets `email_test_send_verified_at`. No separate endpoint exists.

---

## 6. Service layer additions

### 6.1 2FA service functions (new, in `internal/auth/service.go` or a new `internal/auth/twofactor.go`)

```go
// IssuePendingTwoFactor creates a pending_2fa row, generates and emails the OTP.
// Called from Service.Login when 2FA is globally enabled and the password check passed.
func (s *Service) IssuePendingTwoFactor(ctx context.Context, user *User, mailer email.Mailer) (*PendingTwoFactorResult, error)

// VerifyOTP looks up the pending_2fa row by pending_token_hash, checks the OTP,
// increments attempt_count, deletes the row on success or cap, issues a full session on success.
func (s *Service) VerifyOTP(ctx context.Context, pendingToken, otp string) (rawToken string, session *Session, err error)

// ResendOTP generates a new OTP, updates the pending_2fa row, sends the email.
func (s *Service) ResendOTP(ctx context.Context, pendingToken string, mailer email.Mailer) (expiresAt time.Time, resendAvailableAt *time.Time, err error)

// ResetUserTwoFactor deletes any pending_2fa row for userID (admin action).
func (s *Service) ResetUserTwoFactor(ctx context.Context, userID [16]byte) error
```

### 6.2 OTP generation

```go
// GenerateOTP returns a cryptographically random 6-digit decimal string ("000000"–"999999").
func GenerateOTP() (plain, hash string, err error) {
    // crypto/rand.Int generates a uniform random integer in [0, 1_000_000).
    // Format with %06d to preserve leading zeros.
    // Hash with HashToken (SHA-256) for storage.
}
```

The OTP is never logged. Only its hash is written to the database. The plain value is passed to `email.Mailer.Send` and then discarded.

### 6.3 Rate-limit enforcement

`VerifyOTP` uses a PostgreSQL advisory lock on the pending_2fa row's `id` (derived via XOR of UUID halves, same as `checkAndRecordEmailTestSend`) to prevent concurrent OTP-guess races. `ResendOTP` uses the same lock on the user's ID.

The 60-second resend throttle is checked by reading `last_resend_at` inside the lock. The 24-hour cap is maintained in `resend_count_24h` and `resend_window_start` in the pending_2fa row; when `resend_window_start` is more than 24 hours ago, both counters reset to 0 before incrementing.

The `otp_rate_limits` table records each send event for auditing/debugging. The daily cap decision itself is made in-process from the `pending_2fa` row to avoid a second query on the hot path.

### 6.4 Pending token expiry cleanup

No background goroutine. Expired rows are pruned opportunistically: `IssuePendingTwoFactor` runs `DELETE FROM pending_2fa WHERE user_id = $1` before inserting a new row (one pending state per user at a time), and `DELETE FROM pending_2fa WHERE expires_at < NOW() - INTERVAL '1 hour'` with a 1-hour grace period to avoid a full-table scan on every login. The grace period is generous: security impact is zero because the expiry check in `VerifyOTP` and `ResendOTP` is in-application, not purely based on row existence.

---

## 7. Middleware changes

### 7.1 Pending-2FA interposition middleware

A new middleware `RequireTwoFactorComplete` wraps all protected routes when `two_factor_enabled` is `"true"`. It checks the `__Host-session` cookie / Bearer token as normal (via `NewAuthMiddleware`) and additionally verifies that the session is not a pending token.

**The simpler approach:** since pending tokens are NOT stored in `sessions`, the existing `NewAuthMiddleware` already rejects them (no matching row). No new middleware is needed for protection. The middleware gap is in the opposite direction: the route `POST /api/v1/auth/2fa/verify` must NOT require auth middleware, and must check the pending token itself.

Routes `POST /api/v1/auth/2fa/verify` and `POST /api/v1/auth/2fa/resend` are mounted **outside** the auth middleware group, alongside the existing `POST /api/v1/auth/login`. They are public endpoints in the sense that they carry their own credential (the pending token).

This means no new middleware is required. The existing `NewAuthMiddleware` already rejects any token not in the `sessions` table. A pending token, stored only in `pending_2fa`, is not a valid session token.

### 7.2 ConfigService toggle reads

`Service.Login` reads the `two_factor_enabled` config key via a `ConfigReader` interface (narrow, like `email.ConfigLoader`) to decide whether to enter the 2FA path. The `ConfigService` implements this interface. At startup, `main.go` wires the interface.

---

## 8. Email template

The OTP email uses plain text only (no HTML, consistent with the existing welcome email):

```
Subject: Your Valory verification code

Your Valory verification code is: {OTP}

This code expires in 10 minutes and can only be used once.

If you did not attempt to sign in, contact your administrator.
```

The OTP value is substituted at send time. `sanitizeHeader` (existing, in `email/mailer.go`) is applied to the subject. The body is a static string template with `fmt.Sprintf`.

---

## 9. Audit events

All audit events use the existing `audit.Entry` and `audit.Repository.Append` mechanism. The `AdminID` field carries the acting admin's UUID for admin actions, and `uuid.Nil` (a zero UUID) for user-triggered events (OTP verify, challenge issue). This is consistent with how Sprint 20 audit events are structured for user self-service actions.

`otp` is added to `redactedKeys` in `audit/audit.go` as a defense-in-depth measure (no OTP value should appear in payloads, but the redaction list ensures it cannot accidentally).

| Event name | Action string | Trigger | Payload (no OTP values) |
|---|---|---|---|
| 2FA challenge issued | `2fa.challenge_issued` | Pending token created | `{"user_id": "<uuid>"}` |
| 2FA verify success | `2fa.verify_success` | OTP correct, session issued | `{"user_id": "<uuid>"}` |
| 2FA verify failed | `2fa.verify_failed` | Wrong OTP | `{"user_id": "<uuid>", "attempt": 3}` |
| 2FA locked | `2fa.locked` | Attempt cap reached | `{"user_id": "<uuid>"}` |
| 2FA resend | `2fa.resend` | OTP resent | `{"user_id": "<uuid>"}` |
| 2FA reset | `2fa.reset` | Admin clears pending state | `{"target_user_id": "<uuid>"}` |
| 2FA break-glass used | `2fa.break_glass_used` | Env var bypass | `{"username": "<name>"}` |
| 2FA enabled | `2fa.config.enabled` | Admin sets toggle true | `{}` |
| 2FA disabled | `2fa.config.disabled` | Admin sets toggle false | `{}` |

For the user-triggered events (`challenge_issued`, `verify_success`, `verify_failed`, `locked`, `resend`), `audit.Entry.AdminID` is set to `uuid.Nil`. The audit schema allows this (the column is not constrained to the `users` table). If a future schema change requires a non-null admin ID, these events can be refactored to a separate `security_events` table, but that is out of scope for Sprint 21.

---

## 10. Agent interactions

2FA is a synchronous HTTP protocol — no agent orchestration is involved. No multi-agent sequence diagram is needed.

---

## 11. Frontend design

### 11.1 Auth store additions (stores/auth.ts)

New state:

```typescript
// pendingTwoFactor holds the short-lived token between login phase 1 and phase 2.
// It is in-memory only: a page reload loses it and the user must restart login.
// pendingTwoFactor being non-null does NOT mean the user is authenticated.
const pendingTwoFactor = ref<{
  token: string
  expiresAt: number // epoch seconds
} | null>(null)
```

`isAuthenticated` (computed) remains `role.value !== null`. A user with `pendingTwoFactor !== null` and `role.value === null` is NOT authenticated. The router treats them as unauthenticated except for the `/login/verify` route.

New store actions:

```typescript
// setPendingTwoFactor is called by LoginView after a 202 response.
function setPendingTwoFactor(token: string, expiresAt: string): void

// clearPendingTwoFactor is called by OtpVerifyView on success, lockout, or cancel.
function clearPendingTwoFactor(): void

// verifyOtp calls POST /api/v1/auth/2fa/verify, on success calls login() to
// populate session state.
async function verifyOtp(otp: string): Promise<void>

// resendOtp calls POST /api/v1/auth/2fa/resend.
async function resendOtp(): Promise<{ resendAvailableAt: string | null }>
```

### 11.2 Router changes (router/index.ts)

New route:

```typescript
{
  path: '/login/verify',
  name: 'login-verify',
  component: OtpVerifyView,
  meta: {}  // no requiresAuth — the pending token is the credential
}
```

Guard rule added before existing rule 4 (authenticated user navigating to /login):

```
// 3a. User has a pending 2FA token: redirect to /login/verify
//     unless they are already there or navigating to /login (to cancel).
if (auth.pendingTwoFactor !== null && to.path !== '/login/verify' && to.path !== '/login') {
  return '/login/verify'
}
```

Guard rule modification: rule 4 (authenticated to /login) should also redirect away from /login/verify when fully authenticated:

```
if (auth.isAuthenticated && (to.path === '/login' || to.path === '/login/verify')) {
  ...
}
```

### 11.3 LoginView.vue changes

After a 202 response with `two_factor_required: true`:

```typescript
if (response.two_factor_required) {
  auth.setPendingTwoFactor(response.pending_token, response.expires_at)
  router.push('/login/verify')
  return
}
```

On any other non-200 status: existing error handling unchanged.

### 11.4 OtpVerifyView.vue (new)

Structure:

```
- Title: "Check your email"
- Subtitle: "Enter the 6-digit code sent to your email address."
- 6-digit input (type="text", inputmode="numeric", pattern="[0-9]{6}", maxlength="6",
  autocomplete="one-time-code")
- Submit button ("Verify")
- Error area:
  - Wrong code: "Incorrect code. {n} attempt(s) remaining."
  - Locked: "Too many incorrect attempts. Please sign in again." + "Return to sign in" link
  - Expired: "Your code has expired. Please sign in again." + "Return to sign in" link
- Resend section:
  - Countdown timer when resend is throttled: "Resend available in {N}s"
  - "Resend code" button (enabled when countdown reaches 0)
  - On resend success: brief "Code resent" confirmation
  - On resend daily-cap: "You have requested too many codes today. Contact your administrator."
- "Cancel and return to sign in" link (always visible)
```

The 6-digit input is a single `<input>` field, not six separate boxes. This is simpler to implement, accessible, and supports paste from email clients and SMS apps that offer autofill.

On successful verify: `auth.verifyOtp(otp)` → `auth.login()` → router navigates to role home (existing post-login logic reused).

On lockout (attempt cap): `auth.clearPendingTwoFactor()` → display locked message → user must go back to `/login`.

A page reload loses `pendingTwoFactor` (it is not persisted to localStorage or sessionStorage). When the user returns to `/login/verify` after a reload with no pending state, the guard redirects them to `/login`. This is the correct behavior.

### 11.5 Admin 2FA toggle in SystemConfigView.vue

The "Security" section of `SystemConfigView` gains a new subsection "Two-Factor Authentication" positioned between the session settings and the email settings:

```
Two-Factor Authentication
[Explanation text, matching Sprint-16 pattern: plain language, expandable]

2FA requires all users to enter a one-time code sent to their email address when signing in.

Before enabling:
  - Email must be configured and a test message must have been sent successfully.
  - All admin accounts must have an email address.

Status:
  - Email: Configured / Not configured
  - Test send: Verified {date} / Not yet verified
  - Admin accounts without email: 0 ✓ / {N} — please add emails before enabling

[Toggle: Enable two-factor authentication]
[Disabled with tooltip when prerequisites not met]
```

The toggle is a checkbox or switch bound to `config['two_factor_enabled']`. It is disabled (with an explanatory `title` attribute listing the missing prerequisites) when `two_fa_prerequisites.email_configured`, `two_fa_prerequisites.test_send_verified`, and `two_fa_prerequisites.admins_without_email === 0` are not all true.

When enabled, a confirmation dialog is shown: "Two-factor authentication will be required for all users on their next login. Make sure all admin accounts have email addresses. Continue?"

The `students_without_email` count is shown as an advisory warning (amber, not blocking).

---

## 12. Alternatives considered

### A. Per-user 2FA toggle instead of global

Rejected per resolved PM decision 3. The implementation would be identical except for a `users.two_fa_enabled` boolean and a skip condition in `Service.Login`. If the PM reverses this decision in a future sprint, the migration delta is additive (one column) and the service change is a one-line condition.

### B. TOTP (RFC 6238) instead of email OTP

TOTP would eliminate the email dependency and be more resilient to email delivery failures. However: (a) it requires a separate authenticator app that students may not have, (b) it requires QR code enrollment UI (significant frontend work), and (c) the PM request is specifically "email-based 2FA". TOTP could be added as an alternative factor in a future sprint without changing this design's foundation.

### C. Separate cookie for the pending token

A `__Host-pending` cookie would survive page reload. The tradeoff: it would persist in the browser and could be reused from a different tab after the 10-minute window (before the server expires it). The in-memory-only approach enforces that the user must restart login after a reload, which is the correct security posture for a TTL-gated credential.

### D. Sessions table with a `status` column

Rejected. See Section 4.1.

### E. argon2id for OTP hashing

Rejected. See Section 4.3.

---

## 13. Open questions

None — all design points resolved. The following are flagged for senior engineer awareness during implementation:

1. The `audit.Entry.AdminID` type is `uuid.UUID` (non-pointer). Setting it to `uuid.Nil` for user-triggered events is valid but differs from the existing convention where all audit entries are written by an admin action. The senior engineer should confirm `uuid.Nil` is acceptable to the existing `audit.Repository.Append` implementation or add a nullable admin ID path.

2. The `ConfigReader` interface injected into `Service` to read `two_factor_enabled` must not create a circular import (`auth` imports `admin`). The pattern used by `email.ConfigLoader` (a narrow `GetString(key string) string` interface defined in the consumer package) should be replicated.

3. The `GET /api/v1/admin/config` extension to include `two_fa_prerequisites` is a new top-level field alongside `config` and `updated_by`. The frontend currently reads only `response.config`. Adding a new field is backward-compatible but the frontend `ConfigResponse` type must be updated.

4. Route `POST /api/v1/auth/2fa/verify` and `POST /api/v1/auth/2fa/resend` must be mounted on the chi router BEFORE the auth middleware group, not inside it. The senior engineer should verify placement in `main.go` or the auth route registration file.

---

## 14. Acceptance criteria

### 14.1 End-to-end

- AC-1: With `two_factor_enabled = false`, login behaves exactly as before (no 2FA prompt, no pending token).
- AC-2: Enabling the toggle with email unconfigured returns 422 with a descriptive validation error.
- AC-3: Enabling the toggle without a verified test-send returns 422 with a descriptive validation error.
- AC-4: Enabling the toggle when any active admin has no email returns 422 with a descriptive validation error.
- AC-5: After enabling, a correct login + correct OTP issues a full session; the user reaches their role home page.
- AC-6: After enabling, a correct login + 5 wrong OTPs locks the pending state; subsequent verify attempts return `too_many_attempts`.
- AC-7: After enabling, a correct login + OTP after expiry returns `invalid or expired code`.
- AC-8: Resend within 60 seconds returns `resend_throttled` with `resend_available_at`.
- AC-9: Admin per-user reset clears the locked state; the user can log in again and receive a new OTP.
- AC-10: `VALORY_2FA_BREAK_GLASS` set in the environment: admin login with correct password returns a full session without prompting for OTP; audit log contains `2fa.break_glass_used`.
- AC-11: `VALORY_2FA_BREAK_GLASS` set: student login still requires OTP (break-glass is admin-only).
- AC-12: A user with no email address and 2FA on: login phase 1 succeeds (password OK), phase 2 send returns 503 with `{"error": "email not configured"}` (user has no email, not "SMTP not configured" — the distinction matters for the error message shown to the user). The handler detects empty email on the user record and returns a specific error before calling Mailer.Send.
- AC-13: Pending token cannot access `GET /api/v1/auth/session` or any protected endpoint (auth middleware rejects it with 401).
- AC-14: Page reload during pending-2FA state: user is redirected to `/login` (pending state lost); they must restart login.
- AC-15: No OTP value appears in any log line, audit payload, or HTTP response body at any point.

### 14.2 Per-task implementation checklists

#### Task 21.3 (backend, senior-engineer)

- [ ] Migration 018: create `pending_2fa`, `otp_rate_limits`; seed `two_factor_enabled` and `email_test_send_verified_at` in `system_config`.
- [ ] `internal/auth/twofactor.go`: `GenerateOTP`, `IssuePendingTwoFactor`, `VerifyOTP`, `ResendOTP`, `ResetUserTwoFactor`.
- [ ] `PendingTwoFARepository` (or methods on `auth.Repository`): `CreatePending`, `GetPendingByTokenHash`, `IncrementAttempt`, `DeletePending`, `DeletePendingByUserID`, `UpdateOTPHash`.
- [ ] `Service.Login` modified: read `two_factor_enabled` via `ConfigReader` interface; branch to `IssuePendingTwoFactor` or return `PendingTwoFactorResult`; check break-glass env var after password OK and before pending-token issuance.
- [ ] `Handler.login` modified: handle `LoginResult` discriminated type; return 202 for pending; return 200 for full session (unchanged path).
- [ ] New route `POST /api/v1/auth/2fa/verify` → `Handler.verifyOTP`.
- [ ] New route `POST /api/v1/auth/2fa/resend` → `Handler.resendOTP`.
- [ ] New admin route `DELETE /api/v1/admin/users/{id}/2fa/reset` → admin handler `resetUserTwoFactor`.
- [ ] `allowedKeys` in `config_handler.go`: add `two_factor_enabled`.
- [ ] `validateConfigValue` for `two_factor_enabled`: prerequisites check (email configured, test-send verified, no admin without email) when value is `"true"`.
- [ ] `testEmailSend` handler: upsert `email_test_send_verified_at` on success.
- [ ] `GET /api/v1/admin/config`: extend response with `two_fa_prerequisites` object.
- [ ] Audit: all events from Section 9 wired up; `otp` added to `audit.redactedKeys`.
- [ ] `WARN` log at startup when `VALORY_2FA_BREAK_GLASS` is non-empty: `"WARN: VALORY_2FA_BREAK_GLASS is set; admin 2FA bypass is active"`.
- [ ] Per-user no-email handling in `IssuePendingTwoFactor`: return a typed error when `user.Email` is empty; handler maps this to a distinct error message for the user (not the generic 503).
- [ ] All advisory locks for concurrent OTP operations use the XOR-of-UUID-halves derivation consistent with `checkAndRecordEmailTestSend`.

#### Task 21.4 (frontend, junior-engineer)

- [ ] `stores/auth.ts`: add `pendingTwoFactor` ref; `setPendingTwoFactor`, `clearPendingTwoFactor`, `verifyOtp`, `resendOtp` actions; `isAuthenticated` unchanged (does not include pending state).
- [ ] `router/index.ts`: add `/login/verify` route (no `requiresAuth`); add guard rule 3a (pending → redirect to /login/verify); update rule 4 to also redirect away from /login/verify when fully authenticated.
- [ ] `views/LoginView.vue`: handle 202 response; call `auth.setPendingTwoFactor`; `router.push('/login/verify')`.
- [ ] `views/OtpVerifyView.vue` (new): 6-digit input, submit, error display (attempts remaining, locked, expired), resend with countdown timer, cancel link.
- [ ] `views/admin/SystemConfigView.vue`: "Two-Factor Authentication" section with toggle, prerequisite status display, student-without-email advisory warning, confirmation dialog.
- [ ] `views/admin/systemConfig.ts` (or equivalent config constants): add `two_factor_enabled` to CONFIG_KEYS; add explanation text for EXPLANATIONS map.
- [ ] `stores/admin/systemConfig.ts` (if exists): extend `ConfigResponse` type with `two_fa_prerequisites`.
- [ ] The OTP input must have `autocomplete="one-time-code"` for mobile OS autofill.
- [ ] The resend countdown must be implemented with `setInterval` and cleared on component unmount to prevent memory leaks.
- [ ] No OTP value may be stored in Pinia state, localStorage, or sessionStorage.

#### Task 21.5 (tests, test-author)

**Unit tests (Go, `internal/auth` package, no Docker required):**

- [ ] `GenerateOTP`: output is always 6 decimal digits; leading zeros preserved.
- [ ] `HashToken` (existing) used for OTP hashing: round-trip test.
- [ ] OTP expiry: `VerifyOTP` with an expired pending row returns `invalid or expired code`.
- [ ] Single-use: `VerifyOTP` deletes the pending row on success; a second call returns 401.
- [ ] Attempt cap: 5 wrong OTPs delete the row; 6th attempt returns 401 (no row).
- [ ] `ResendOTP` 60-second throttle: second call within 60 s returns throttle error with `resend_available_at`.
- [ ] `ResendOTP` daily cap: 10th send within 24 h returns cap error.
- [ ] Break-glass: `Service.Login` with `VALORY_2FA_BREAK_GLASS` set + admin account returns full session, not pending.
- [ ] Break-glass: student account still returns pending even when env var is set.

**Integration tests (Go, real PostgreSQL via docker-compose.test.yml, Mailpit SMTP sink):**

- [ ] Full 2FA login: POST /auth/login → 202 + pending token → OTP arrives in Mailpit → extract 6-digit code from email body → POST /auth/2fa/verify → 200 + session cookie.
- [ ] Wrong code: POST /auth/2fa/verify with wrong OTP → 401 with `attempts_remaining`.
- [ ] Attempt cap integration: 5 wrong codes → 401 `too_many_attempts`; 6th verify → 401 (no row).
- [ ] Resend throttle integration: resend twice within 60 s → second gets 429 with `resend_available_at`.
- [ ] Toggle enable blocked without test-send: PATCH config `two_factor_enabled: true` before test-send → 422.
- [ ] Toggle enable blocked when admin has no email: PATCH config `two_factor_enabled: true` with admin having empty email → 422.
- [ ] Toggle enable succeeds after test-send and admin email present.
- [ ] Admin 2FA reset: user hits attempt cap → admin DELETE /admin/users/{id}/2fa/reset → 204; user can log in again and receive new OTP.
- [ ] Break-glass: `VALORY_2FA_BREAK_GLASS` set in test env → admin POST /auth/login → 200 (full session, no OTP required); audit log contains `2fa.break_glass_used`.
- [ ] No-email-user: user with empty email, 2FA on → POST /auth/login → 202 (password OK) → POST /auth/2fa/verify errors or POST /auth/2fa/resend returns informative error; the pending row exists but OTP was never sent (handler detected no email and returned error before calling Mailer.Send). Test verifies Mailpit received no message for this user.
- [ ] Pending token rejected by auth middleware: POST /auth/login → 202; use pending token as Bearer in GET /auth/session → 401.

**End-to-end (Playwright or equivalent, one budgeted run):**

- [ ] Full 2FA journey: enable toggle (after test-send) → student login → OTP entry → dashboard reached.
- [ ] Wrong code journey: two wrong codes → correct code → success.
- [ ] Resend journey: click "Resend code" → countdown shown → code arrives in Mailpit → verify.
- [ ] Lockout journey: 5 wrong codes → locked message → "Return to sign in" → new login starts fresh.

---

## 15. Requirement ID plan for task 21.2

The requirements-author should create the following files. Existing `REQ-AUTH-014` is the last used AUTH ID (confirmed from `internal/auth/requirements/REQ-AUTH-014.json`). Frontend FEAUTH IDs run to at least 171; FEADMIN IDs run to at least 604.

| File path | ID | Parent |
|---|---|---|
| `internal/auth/requirements/REQ-AUTH-015.json` | REQ-AUTH-015 | — |
| `internal/auth/requirements/REQ-AUTH-016.json` | REQ-AUTH-016 | REQ-AUTH-015 |
| `internal/auth/requirements/REQ-AUTH-017.json` | REQ-AUTH-017 | REQ-AUTH-015 |
| `internal/auth/requirements/REQ-AUTH-018.json` | REQ-AUTH-018 | REQ-AUTH-015 |
| `internal/auth/requirements/REQ-AUTH-019.json` | REQ-AUTH-019 | REQ-AUTH-015 |
| `internal/auth/requirements/REQ-AUTH-020.json` | REQ-AUTH-020 | REQ-AUTH-015 |
| `internal/auth/requirements/REQ-AUTH-021.json` | REQ-AUTH-021 | REQ-AUTH-015 |
| `frontend/src/requirements/REQ-FEAUTH-200.json` | REQ-FEAUTH-200 | REQ-AUTH-015 |
| `frontend/src/requirements/REQ-FEAUTH-201.json` | REQ-FEAUTH-201 | REQ-AUTH-015 |
| `frontend/src/requirements/REQ-FEAUTH-202.json` | REQ-FEAUTH-202 | REQ-AUTH-015 |
| `frontend/src/requirements/REQ-FEADMIN-700.json` | REQ-FEADMIN-700 | REQ-AUTH-018 |

All parent fields reference `REQ-AUTH-015` (the top-level 2FA requirement) except `REQ-FEADMIN-700` which references `REQ-AUTH-018` (the global toggle requirement).

---

## Revision note (software-lead) — audit actor resolution for tradeoff #3

The flagged concern that `audit.Entry.AdminID = uuid.Nil` might violate a
schema constraint is RESOLVED and not a risk: migration
`010_audit_admin_fk.sql` **removed** the `audit_log.admin_id` foreign key
(tamper-evidence is provided by the prev_hash/entry_hash chain, not FK
integrity). `admin_id` therefore accepts any UUID value with no referential
check.

Binding decision for 21.3: for the user-triggered 2FA events
(`2fa.challenge_issued`, `2fa.verify_success`, `2fa.verify_failed`,
`2fa.locked`), set `AdminID` to the SUBJECT USER's own id (the actor is the
user authenticating), with `TargetType="user"`, `TargetID=<userID>`. For
admin-triggered events (`2fa.reset`, `two_factor_enabled/disabled`) keep the
acting admin's id as `AdminID` and the affected user (if any) as the target.
For `2fa.break_glass_used`, set `AdminID` to the admin's id once resolved (the
break-glass still requires a valid admin username+password), `TargetType="user"`.
Never write OTP values or the break-glass env value into any payload.
