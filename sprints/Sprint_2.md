# Sprint 2 — User Module, Audit Module, Config Service & Security Middleware

## Objective

Deliver the user account lifecycle (create, modify, deactivate/activate, delete, password reset), a tamper-evident SHA-256 hash-chained audit log for all admin actions, a system configuration service consumed by security controls, and the cross-cutting security middleware (CSRF double-submit cookie, password-reset rate limiter, AI consent gate). After this sprint every admin action is logged, accounts can be managed over the API, and the security perimeter established in Sprint 1 is extended with CSRF protection and consent enforcement.

## Requirements Covered

| Requirement | Title |
|---|---|
| REQ-USER-001 | Admin Creates User Account |
| REQ-USER-002 | Admin Modifies User Account |
| REQ-USER-003 | Admin Deactivates User Account |
| REQ-USER-004 | Deactivated Account Login Rejection |
| REQ-USER-005 | Out-of-Band Password Reset Delivery |
| REQ-USER-006 | Password Reset Token Single-Use Enforcement |
| REQ-USER-007 | Student Personal Data Permanent Deletion |
| REQ-AUDIT-001 | Tamper-Evident Admin Action Log Entry |
| REQ-AUDIT-002 | Audit Log Integrity Verifiability |
| REQ-SECURITY-003 | Password-Reset Request Rate Limiting |
| REQ-SECURITY-004 | CSRF Token Validation on State-Changing Requests |
| REQ-SECURITY-005 | Student Consent Before AI Data Transmission |
| REQ-SYS-003 | Admin Account Provisioning |
| REQ-SYS-004 | Admin Account Deactivation |
| REQ-SYS-025 | Student Account Data Deletion |
| REQ-SYS-026 | Password Reset |
| REQ-SYS-038 | Password-Reset Rate Limiting |
| REQ-SYS-042 | Admin Action Audit Logging |
| REQ-SYS-046 | AI Processing Consent |
| REQ-SYS-048 | CSRF Protection |

ConfigService is seeded and the in-memory map is operational, but the admin `PATCH /api/v1/admin/config` HTTP endpoint is deferred to Sprint 3 (it requires the admin module and a `config.change` audit entry type that depends on config infrastructure being stable first).

## Assumptions

- **Migration 002** includes: `password_reset_tokens`, `password_reset_attempts`, `student_consent`, `audit_log`, `system_config` tables with seed defaults, and the `valory_app` database role setup. The `agent_token_usage` table (references `courses.id`) is deferred to the sprint that creates the `courses` table.
- **RLS policies** for student-owned tables (`courses`, `lesson_content`, `section_feedback`, `submissions`, `grades`, `chat_messages`, `notifications`) are added in the migrations that create those tables in future sprints. Only tables that exist after migration 002 are eligible for RLS now; none of the new Sprint 2 tables require RLS.
- **TC-SECURITY-016** (replayed CSRF token rejected on second use) conflicts with the SDD double-submit cookie design, which is not single-use — the `__Host-csrf` cookie persists for the session and is compared against the `X-CSRF-Token` header on each request. The SDD is authoritative; TC-SECURITY-016 will not be implemented as specified. A design note is appended to the contributor's task.
- **`TerminateStudentOperations`** (REQ-AGENT-013) is expressed as an interface in the user service; the real implementation arrives with the agent module in a future sprint. Sprint 2 wires a no-op that returns immediately so deletion logic can be exercised end-to-end.
- **Password reset token lifetime** is configured via `PASSWORD_RESET_TOKEN_TTL` env var (Go `time.Duration` format, default `1h`) per the SDD. It is NOT drawn from `system_config`.
- **`email` column on `users`**: TC-USER-006 and password-reset delivery require an email address. Migration 002 adds an `email TEXT UNIQUE` column to `users` (nullable; existing rows get NULL). Handlers that create/modify users may supply an optional `email` field.
- **ConfigService** is injected into the auth middleware via a `ConsentVersionProvider` interface (`GetString(key string) string`) to avoid a circular `auth → admin` package dependency.
- **Admin reactivation** (`POST /api/v1/admin/users/:id/activate`) is included alongside deactivation per the SDD lifecycle diagram and the `user.activate` audit action table.
- The `users` table `email` column added in migration 002 is used for out-of-band password-reset delivery. Sprint 1 auth tests are unaffected (email is nullable).

## Pre-Sprint State

| File | What it provides |
|---|---|
| `internal/auth/middleware.go` | `AuthMiddleware`, `RequireRole` |
| `internal/auth/repository.go` | Session CRUD, user lookup by username |
| `internal/auth/service.go` | Login / logout logic |
| `internal/auth/handler.go` | `POST /api/v1/auth/login`, `POST /api/v1/auth/logout` |
| `internal/db/pool.go` | `NewPool` (pgxpool) |
| `migrations/001_auth.sql` | `users`, `sessions`, `login_attempts` tables |
| `migrations/embed.go` | Embedded migration FS |
| `cmd/server/main.go` | Router wiring for auth routes, TLS config, health check |

## Sprint Plan

### Increment 1 — Database Migration (sequential)

| Task | Description | Agent | File(s) |
|---|---|---|---|
| **T1** | **DB Migration 002** — Add `email TEXT` column to `users`; create `password_reset_tokens`, `password_reset_attempts`, `student_consent`, `audit_log`, `system_config` tables with all indexes per the SDD data models; seed default `system_config` rows (all 13 keys) with `INSERT … ON CONFLICT DO NOTHING`; create `valory_app` role with `NOBYPASSRLS NOSUPERUSER NOCREATEDB NOCREATEROLE NOLOGIN NOINHERIT`; grant `CONNECT`, `USAGE ON SCHEMA public`, `SELECT/INSERT/UPDATE/DELETE ON ALL TABLES`, `USAGE/SELECT ON ALL SEQUENCES`. | `junior-engineer` | `migrations/002_user_security_audit.sql` |

**Verifier (T1):** `software-quality-engineer` + `systems-engineer` (parallel) → `senior-quality-engineer`

---

### Increment 2 — Parallel Foundation (parallel, all after T1)

| Task | Description | Agent | File(s) |
|---|---|---|---|
| **T2** | **Audit logic** — `internal/audit` package: `Entry` struct, `computeEntryHash(prevHash, createdAt, adminID, action, targetID, payloadJSON string) string` (SHA-256, RFC 3339 Nano UTC, canonical JSON, hex encoding), genesis hash constant, `verifyChain(rows []AuditRow) (bool, int64)` streaming in batches of 1000, `redactedKeys` compile-time list (`anthropic_api_key`). Unit tests covering hash computation, genesis entry, chain verification (intact chain passes; altered entry fails; deleted middle entry fails). No DB dependency in this file. | `senior-engineer` | `internal/audit/audit.go`, `internal/audit/audit_test.go` |
| **T3** | **Audit repository** — `AuditRepository`: `Append(ctx, tx pgx.Tx, e Entry) error` (SELECT FOR UPDATE last entry_hash, compute new hash, INSERT); `ListPaginated(ctx, pool, limit int, before int64) ([]AuditRow, error)`; `StreamAll(ctx, pool) ([]AuditRow, error)` (batch fetch for verify). Parameterised queries only; no SQL injection vectors. Integration tests against real PostgreSQL using TestMain. | `junior-engineer` | `internal/audit/repository.go`, `internal/audit/repository_test.go` |
| **T4** | **Config service** — `internal/admin/config.go`: `ConfigService` struct with `sync.RWMutex`-protected `map[string]string`; `NewConfigService(db *pgxpool.Pool) *ConfigService`; `Load(ctx) error` (SELECT all rows from `system_config`); `GetString(key string) string`; `GetInt64(key string) int64`; `GetFloat64(key string) float64`. Export `ConsentVersionProvider` interface (`GetString(key string) string`) for injection into auth middleware without creating an `auth → admin` import cycle. Unit tests for all accessors. Integration test: Load reads seeded defaults from a real `system_config` table. | `junior-engineer` | `internal/admin/config.go`, `internal/admin/config_test.go` |
| **T5** | **CSRF middleware** — `internal/security/csrf.go`: `CSRFMiddleware(next http.Handler) http.Handler` per SDD section 13. Skips GET/HEAD/OPTIONS; reads `__Host-csrf` cookie; compares cookie value to `X-CSRF-Token` header using `hmac.Equal` (constant-time); returns 403 JSON `{"error":"csrf_token_mismatch"}` on failure. Also export `SetCSRFCookie(w http.ResponseWriter, token string)` so the login handler can call it. Unit tests: missing cookie → 403; wrong header → 403; matching values → passes through; GET skipped. **Design note:** The SDD uses a double-submit cookie, not a single-use token — TC-SECURITY-016's single-use assertion is out of scope and should not be implemented. | `junior-engineer` | `internal/security/csrf.go`, `internal/security/csrf_test.go` |
| **T6** | **Password-reset rate limiter** — `internal/security/ratelimit.go`: `CheckPasswordResetRateLimit(ctx, db *pgxpool.Pool, userID uuid.UUID) error` — counts `password_reset_attempts` in the last hour; returns `ErrRateLimitExceeded` when `count >= 3`; `RecordPasswordResetAttempt(ctx, db, userID)` inserts a row. Export `ErrRateLimitExceeded` sentinel. Integration tests: first 3 calls succeed; 4th returns `ErrRateLimitExceeded`; counts are per-user (user A exhausted does not block user B). | `junior-engineer` | `internal/security/ratelimit.go`, `internal/security/ratelimit_test.go` |
| **T7** | **User repository** — `internal/user/repository.go`: `UserRepository` with pgxpool. Methods: `CreateUser(ctx, username, email, passwordHash, role string) (UserRow, error)`; `GetUserByID(ctx, id uuid.UUID) (UserRow, error)`; `GetUserByUsername(ctx, username string) (UserRow, error)`; `UpdateUser(ctx, id uuid.UUID, fields UpdateFields) (UserRow, error)` (partial update, only non-nil fields); `SetActive(ctx, id uuid.UUID, active bool) error` (also deletes all sessions in same tx); `DeleteStudent(ctx, id uuid.UUID) error` (cascading hard-delete transaction per SDD order — submissions, grades, chat_messages, courses, sessions, password_reset_tokens, users); `CreatePasswordResetToken(ctx, userID uuid.UUID, tokenHash string, expiresAt time.Time) error`; `GetValidResetToken(ctx, tokenHash string) (ResetTokenRow, error)` (used_at IS NULL AND expires_at > NOW()); `MarkResetTokenUsed(ctx, id uuid.UUID) error` (sets used_at = NOW()); `UpdatePasswordHash(ctx, userID uuid.UUID, newHash string) error`; `UpsertConsent(ctx, studentID uuid.UUID, version string) error`; `GetConsentVersion(ctx, studentID uuid.UUID) (string, error)`. Parameterised queries only. Integration tests against real PostgreSQL using TestMain. | `junior-engineer` | `internal/user/repository.go`, `internal/user/repository_test.go` |
| **T8** | **Email transport** — `internal/user/email.go`: `EmailTransport` interface with `SendPasswordReset(ctx, toAddress, rawToken string) error`; `SMTPTransport` struct (Host, Port, From, Password) implementing the interface via SMTP STARTTLS dial; `NoOpTransport` that writes `[password-reset] to=<addr> token=<tok>` to a supplied `io.Writer` (used when `SMTP_HOST` is absent and for tests). Constructor: `NewEmailTransport(host string, port int, from, password string, log io.Writer) EmailTransport`. Unit tests: `NoOpTransport` writes expected output; `SMTPTransport` returns an error when the dial fails (use a non-routable address). | `junior-engineer` | `internal/user/email.go`, `internal/user/email_test.go` |

**Verifier (T2–T8):** `software-quality-engineer` + `systems-engineer` (parallel) → `senior-quality-engineer`

---

### Increment 3 — Services & Middleware Extension (parallel, after T2 + T3 + T4 + T7 + T8)

| Task | Description | Agent | File(s) |
|---|---|---|---|
| **T9** | **User service** — `internal/user/service.go`: `UserService` struct holding `*UserRepository`, `*audit.AuditRepository`, `EmailTransport`, `ConsentVersionProvider`, `AgentTerminator` interface (Sprint 2 wires a no-op). Methods: `CreateUser(ctx, adminID, username, email, role, password string) (UserRow, error)` — hashes password with argon2id (reuse `auth.HashPassword`), inserts user, calls `audit.Append` (`user.create` action) in same transaction; `ModifyUser(ctx, adminID, id, fields)` — partial update + audit `user.modify`; `DeactivateUser(ctx, adminID, id)` — sets `is_active=false`, deletes sessions, audit `user.deactivate`; `ActivateUser(ctx, adminID, id)` — sets `is_active=true`, audit `user.activate`; `DeleteStudent(ctx, adminID, id)` — calls `agentTerminator.TerminateStudentOperations(ctx, id)` first (returns 409 if drain times out), then cascading delete transaction + post-delete best-effort disk cleanup, audit `user.delete`; `RequestPasswordReset(ctx, username string) error` — looks up user (no error if not found; always returns nil to prevent enumeration), calls `security.CheckPasswordResetRateLimit`, generates 256-bit token, stores SHA-256 hash, calls `EmailTransport.SendPasswordReset`; `ConfirmPasswordReset(ctx, rawToken, newPassword string) error` — SHA-256 token lookup, validates unused + not expired, updates password hash, marks token used, deletes all user sessions; `RecordConsent(ctx, studentID uuid.UUID, version string) error`. Integration tests cover: create → audit entry exists; deactivate → sessions deleted; delete student → all cascade tables empty; password reset token single-use rejection; rate-limit violation from service perspective. | `senior-engineer` | `internal/user/service.go`, `internal/user/service_test.go` |
| **T11** | **Audit HTTP handlers** — `internal/audit/handler.go`: `AuditHandler` holding `*AuditRepository`. `GET /api/v1/admin/audit?limit=N&before=M` — validates `limit` 1–200 (default 50); cursor-based pagination via `before` BIGSERIAL ID; returns JSON per SDD contract. `GET /api/v1/admin/audit/verify` — streams all rows via `StreamAll`, runs `verifyChain`, returns `{"valid":true}` or `{"valid":false,"first_broken_id":N}`; always HTTP 200. Both routes require `RequireRole("admin")`. Integration tests: pagination returns correct page; verify returns valid on unmodified log; verify returns broken ID after direct DB row alteration. | `junior-engineer` | `internal/audit/handler.go`, `internal/audit/handler_test.go` |
| **T12** | **Auth middleware consent gate extension** — Extend `internal/auth/middleware.go`: add `consentProvider ConsentVersionProvider` field to `AuthMiddleware` (constructor updated to accept it, `nil`-safe so tests that pass `nil` skip the gate); in `AuthMiddleware.ServeHTTP`, after session validation and before passing to the next handler, for `role == "student"` sessions: call `consentProvider.GetString("consent_version")`; query `student_consent` for this `student_id`; if no row or stored version `< current`, return HTTP 403 JSON `{"error":"CONSENT_REQUIRED","current_version":"<v>"}` without touching the next handler. Also update the login handler (`internal/auth/handler.go`) to call `security.SetCSRFCookie` after successful login so the CSRF cookie is set at the login response. Update `internal/auth/middleware_test.go` with new test cases: student without consent row → 403; student with matching version → passes; student with stale version → 403; admin role skips consent gate; nil consentProvider skips gate. | `senior-engineer` | `internal/auth/middleware.go` *(update)*, `internal/auth/handler.go` *(update)*, `internal/auth/middleware_test.go` *(update)* |

**Verifier (T9 + T11 + T12):** `software-quality-engineer` + `systems-engineer` (parallel) → `senior-quality-engineer`

---

### Increment 4 — User HTTP Handlers (sequential, after T9 + T5 + T6)

| Task | Description | Agent | File(s) |
|---|---|---|---|
| **T10** | **User HTTP handlers** — `internal/user/handler.go`: `UserHandler` holding `*UserService`. Routes (all `/api/v1/admin/…` require `RequireRole("admin")`): `POST /api/v1/admin/users` → CreateUser (201 or 400/409); `PATCH /api/v1/admin/users/:id` → ModifyUser (200 or 400/404/409); `POST /api/v1/admin/users/:id/deactivate` → DeactivateUser (204 or 404/409); `POST /api/v1/admin/users/:id/activate` → ActivateUser (204 or 404/409); `DELETE /api/v1/admin/students/:id` → DeleteStudent (204 or 404/409/422 for admin-role target). Public (no auth required): `POST /api/v1/auth/password-reset/request` → RequestPasswordReset (always 202); `POST /api/v1/auth/password-reset/confirm` → ConfirmPasswordReset (204 or 400/401). Authenticated student: `POST /api/v1/consent` → RecordConsent (200). Extract `adminID` from context (set by AuthMiddleware) for all admin operations. Integration tests covering TC-USER-001 through TC-USER-025 (TC-USER-025 tests isolation at the user-deletion layer; cross-module student data is stubbed). | `junior-engineer` | `internal/user/handler.go`, `internal/user/handler_test.go` |

**Verifier (T10):** `software-quality-engineer` + `systems-engineer` (parallel) → `senior-quality-engineer`

---

### Increment 5 — Server Wiring (sequential, after T10 + T11 + T12)

| Task | Description | Agent | File(s) |
|---|---|---|---|
| **T13** | **Main entry-point update** — Update `cmd/server/main.go`: (1) load and validate additional env vars (`SMTP_HOST`, `SMTP_PORT`, `SMTP_FROM`, `SMTP_PASSWORD` — non-fatal if absent, substitutes `NoOpTransport`; `PASSWORD_RESET_TOKEN_TTL` — defaults `1h`); (2) instantiate `ConfigService`, call `Load(ctx)`, fail-fast if it returns error; (3) instantiate `AuditRepository`; (4) instantiate `UserRepository` and `EmailTransport` (no-op if `SMTP_HOST` empty); (5) instantiate `UserService` with no-op `AgentTerminator`; (6) instantiate `UserHandler` and `AuditHandler`; (7) mount `CSRFMiddleware` globally on the router (before all routes except `/health`); (8) pass `ConfigService` (as `ConsentVersionProvider`) to `AuthMiddleware` constructor; (9) mount user admin routes under `RequireRole("admin")`; (10) mount password-reset public routes; (11) mount consent route under `AuthMiddleware`; (12) mount audit routes under `RequireRole("admin")`. Smoke test: `go build ./...` and `go vet ./...` pass clean. | `junior-engineer` | `cmd/server/main.go` *(update)* |

**Verifier (T13):** `software-quality-engineer` + `systems-engineer` (parallel) → `senior-quality-engineer`

---

## Dependency Graph

```
T1 (migration 002)
 ├─> T2 (audit logic)       ┐
 ├─> T3 (audit repository)  ├─> T11 (audit handler) ┐
 ├─> T4 (config service)    ├─> T9 (user service)   ├─> T13 (main wiring)
 ├─> T5 (CSRF middleware)   ├─>                     │
 ├─> T6 (rate limiter)      ├─> T12 (consent gate)  ┤
 ├─> T7 (user repository)   ┘                       │
 └─> T8 (email transport)       T10 (user handler) ─┘
                                  ^ depends on T9, T5, T6
```

## Review Pipeline

For each increment:

```
Contributor completes task
        |
        v
SQE + Systems Engineer (parallel review)
        |
   Pass? ──yes──> Senior SQE ──pass──> next increment
        |
        no
        v
Return to contributor with feedback → Review again
```

## Test Cases Covered

### User module (TC-USER-*)

| TC | Title | Task |
|---|---|---|
| TC-USER-001 | Admin successfully creates a new student account | T10 |
| TC-USER-002 | Admin successfully creates a new admin account | T10 |
| TC-USER-003 | Non-admin user rejected when creating an account | T10 |
| TC-USER-004 | Admin rejected when creating duplicate username | T10 |
| TC-USER-005 | Admin rejected when missing required field | T10 |
| TC-USER-006 | Admin successfully modifies student account email | T10 |
| TC-USER-007 | Admin successfully modifies user account username | T10 |
| TC-USER-008 | Admin modification of non-existent account → 404 | T10 |
| TC-USER-009 | Account modification with empty body rejected | T10 |
| TC-USER-010 | Admin successfully deactivates an active account | T10 |
| TC-USER-011 | Deactivation of already-deactivated account → 409 | T10 |
| TC-USER-012 | Non-admin cannot deactivate an account | T10 |
| TC-USER-013 | Deactivated account login rejected | T10 |
| TC-USER-014 | Deactivated admin account login rejected | T10 |
| TC-USER-015 | Password reset token delivered to registered contact | T10 |
| TC-USER-016 | Password reset request for unknown username → 202 (no enumeration) | T10 |
| TC-USER-017 | Reset token not returned in API response | T10 |
| TC-USER-018 | Password reset token usable exactly once | T10 |
| TC-USER-019 | Expired password reset token rejected | T10 |
| TC-USER-020 | Already-used reset token rejected (unit) | T9 |
| TC-USER-021 | Authorized deletion removes all student personal data | T10 |
| TC-USER-022 | Deletion request for non-existent student → 404 | T10 |
| TC-USER-023 | Non-admin cannot delete a student account | T10 |
| TC-USER-024 | Deletion of student with no associated records | T10 |
| TC-USER-025 | Deleting Student A does not corrupt Student B records | T10 (DB isolation only; course/submission stubs) |

### Audit module (TC-AUDIT-*)

| TC | Title | Task |
|---|---|---|
| TC-AUDIT-001 | Admin create-user action produces tamper-evident log entry | T11 |
| TC-AUDIT-002 | Admin modify-user action produces tamper-evident log entry | T11 |
| TC-AUDIT-003 | Admin deactivate-user action produces tamper-evident log entry | T11 |
| TC-AUDIT-004 | Admin policy-change action produces tamper-evident log entry | Deferred to Sprint 3 (admin config endpoint) |
| TC-AUDIT-005 | Non-admin action does not produce audit log entry | T11 |
| TC-AUDIT-006 | Unmodified audit log passes integrity verification | T11 |
| TC-AUDIT-007 | Altered audit log entry fails integrity verification | T11 |
| TC-AUDIT-008 | Deleted audit log entry detected by integrity verification | T11 |

### Security module (TC-SECURITY-*)

| TC | Title | Task |
|---|---|---|
| TC-SECURITY-001–TC-SECURITY-005 | Per-student data isolation (course/submission/grade/chat) | Deferred — tables don't exist until Sprint 3+ |
| TC-SECURITY-006 | Data isolation enforced at the database query level | T7 (user repo isolation test) |
| TC-SECURITY-007 | Three password-reset requests within one hour succeed | T6 |
| TC-SECURITY-008 | Fourth password-reset request within one hour rejected | T6 |
| TC-SECURITY-009 | Password-reset rate limit resets after one hour | T6 |
| TC-SECURITY-010 | Rate-limit counter is per-account | T6 |
| TC-SECURITY-011 | State-changing request with valid CSRF token accepted | T5 |
| TC-SECURITY-012 | State-changing request with missing CSRF token rejected | T5 |
| TC-SECURITY-013 | State-changing request with invalid CSRF token rejected | T5 |
| TC-SECURITY-014 | GET request not blocked when CSRF token absent | T5 |
| TC-SECURITY-015 | CSRF validation unit test rejects mismatched token | T5 |
| TC-SECURITY-016 | Replayed CSRF token rejected *(single-use)* | **Out of scope** — SDD uses double-submit cookie pattern (not single-use); this TC reflects a mismatched assumption |
| TC-SECURITY-017 | CSRF token from Student A rejected with Student B's auth | T5 (the double-submit cookie is session-independent; cross-session CSRF injection is prevented by SameSite=Strict + __Host- binding) |
| TC-SECURITY-018–TC-SECURITY-019 | Cross-student course submission/chat isolation | Deferred — submission/course tables don't exist until Sprint 3+ |
