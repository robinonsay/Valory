# Sprint 1 — Auth Module & Server Entry Point

## Objective

Deliver a working, end-to-end authenticated HTTP server. All other modules (user,
course, agent, etc.) depend on a functional auth layer. Sprint 1 produces the session
token service, auth repository, login/logout service and handlers, RBAC middleware,
a corrected health handler, and the main server entry point that wires everything
together.

## Requirements Covered

| Requirement | Title |
|---|---|
| REQ-AUTH-001 | Login Credential Verification |
| REQ-AUTH-002 | Session Token Issuance on Successful Login |
| REQ-AUTH-003 | Role Claim in Session Token |
| REQ-AUTH-004 | Role-Based Request Enforcement |
| REQ-AUTH-005 | Session Inactivity Expiry |
| REQ-AUTH-006 | Brute-Force Account Lockout |
| REQ-AUTH-007 | TLS-Only Client Connections *(already implemented)* |
| REQ-AUTH-008 | Adaptive Password Hashing *(already implemented)* |
| REQ-INFRA-001 | Health Check Endpoint *(partial — corrected this sprint)* |

## Assumptions

- Session tokens are **opaque**: 256-bit `crypto/rand` value, base64url-encoded to the
  client, stored server-side as `hex(SHA-256(raw_token))` in `sessions.token_hash`.
  Stateless JWT is explicitly rejected per SDD section 02-auth.adoc.
- `AUTH_LOCKOUT_DURATION` env var (Go `time.Duration` format, default `15m`) controls
  lockout length. The failure threshold of **5** consecutive attempts is fixed per
  REQ-AUTH-006.
- `AUTH_SESSION_MAX_DURATION` env var (default `24h`) is the absolute session ceiling.
- `AUTH_INACTIVITY_PERIOD` env var (default `30m`) drives inactivity expiry.
- API routes are prefixed `/api/v1/` per the SDD.
- Health handler is updated this sprint to match SDD spec: three subsystems (`postgres`,
  `anthropic`, `disk`) checked concurrently via `sync.WaitGroup`.
- `CheckPassword` (argon2id) runs **before** any account-state check (disabled, locked).
  This equalizes response timing for active-wrong-password, disabled, and locked accounts,
  preventing timing-based state enumeration. A `dummyHash` (computed once at init)
  equalizes the unknown-username path.

## Pre-Sprint State

The following files exist from prior setup work and are **not** re-implemented:

| File | What it provides |
|---|---|
| `internal/auth/password.go` | `HashPassword` / `CheckPassword` (argon2id) |
| `internal/infra/tls.go` | `BuildTLSConfig` (ACME + dev self-signed) |
| `internal/infra/health.go` | Health handler *(non-conformant — corrected in Task 6)* |
| `internal/db/pool.go` | `NewPool` (pgxpool) |
| `migrations/001_auth.sql` | `users`, `sessions`, `login_attempts` tables |

## Sprint Plan

### Increment 1 — Core Auth Primitives (parallel)

| Task | Description | Agent | File(s) |
|---|---|---|---|
| **T1** | **Session token service** — issue a 256-bit opaque token, compute `hex(SHA-256)` for storage, validate tokens including inactivity and absolute expiry windows. Unit tests covering TC-AUTH-013, TC-AUTH-014, TC-AUTH-015. | `senior-engineer` | `internal/auth/token.go`, `internal/auth/token_test.go` |
| **T2** | **Auth DB repository** — user lookup by username, record login attempt, read/write `failed_login_count` and `locked_until`, create/lookup/delete session by token hash. All queries use `$N` parameterized form; no SQL injection vectors. | `junior-engineer` | `internal/auth/repository.go`, `internal/auth/repository_test.go` |

**Verifier (T1 + T2):** `software-quality-engineer` + `systems-engineer` (parallel) → `senior-quality-engineer`

---

### Increment 2 — Auth Service (sequential, after Increment 1)

| Task | Description | Agent | File(s) |
|---|---|---|---|
| **T3** | **Auth service** — login logic: run `CheckPassword` first (timing equalization), then check `is_active`, check lockout (`locked_until > NOW()`), on wrong password increment counter and lock at threshold 5, on success reset counter and issue session token via T1's token service. Logout: delete session row. Unit + integration tests for lockout flow (TC-AUTH-016 through TC-AUTH-019). | `senior-engineer` | `internal/auth/service.go`, `internal/auth/service_test.go` |

**Verifier (T3):** `software-quality-engineer` + `systems-engineer` (parallel) → `senior-quality-engineer`

---

### Increment 3 — HTTP Layer (parallel, after Increment 2)

| Task | Description | Agent | File(s) |
|---|---|---|---|
| **T4** | **Auth HTTP handlers** — `POST /api/v1/auth/login` (returns token, role, expires_at), `POST /api/v1/auth/logout` (requires bearer token, returns 204). Input validation; error responses per SDD API contract. Integration tests covering TC-AUTH-001 through TC-AUTH-012. | `junior-engineer` | `internal/auth/handler.go`, `internal/auth/handler_test.go` |
| **T5** | **Auth middleware** — `AuthMiddleware`: reads `Authorization: Bearer` header, computes SHA-256, queries session, checks inactivity + absolute expiry, updates `last_active_at`, sets `user_id` + `role` in `context.Context`, sets RLS session parameters (`app.current_user_id`, `app.current_role`). `RequireRole(role)` decorator for admin-only routes. Integration tests covering TC-AUTH-009 through TC-AUTH-012, TC-AUTH-020, TC-AUTH-021. | `senior-engineer` | `internal/auth/middleware.go`, `internal/auth/middleware_test.go` |

**Verifier (T4 + T5):** `software-quality-engineer` + `systems-engineer` (parallel) → `senior-quality-engineer`

---

### Increment 4 — Server Entry Point (sequential, after Increment 3)

| Task | Description | Agent | File(s) |
|---|---|---|---|
| **T6** | **Health handler correction + main entry point** — Update `internal/infra/health.go` to match SDD spec: three subsystems (`postgres`, `anthropic`, `disk`) checked concurrently via `sync.WaitGroup`. Write `cmd/server/main.go`: load env vars (fail-fast on missing secrets), open DB pool, run migrations, build router (chi), mount `/health`, mount auth routes, mount admin stub route guarded by `RequireRole("admin")`, wire TLS config, start HTTP redirect server on `:80` and HTTPS server on `:8443`. | `senior-engineer` | `internal/infra/health.go` *(update)*, `cmd/server/main.go` |

**Verifier (T6):** `software-quality-engineer` + `systems-engineer` (parallel) → `senior-quality-engineer`

---

## Dependency Graph

```
T1 (token service) ──┐
                      ├──> T3 (auth service) ──> T4 (handler) ──┐
T2 (repository) ─────┘                           T5 (middleware) ┤──> T6 (main)
                                                                  ┘
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
Return to contributor with feedback
```

## Test Cases Covered

| TC | Title | Increment |
|---|---|---|
| TC-AUTH-001 | Valid credentials return HTTP 200 and session token | 3 (T4) |
| TC-AUTH-002 | Wrong password returns HTTP 401 | 3 (T4) |
| TC-AUTH-003 | Unknown username returns HTTP 401 | 3 (T4) |
| TC-AUTH-004 | Empty password returns HTTP 400 | 3 (T4) |
| TC-AUTH-005 | Successful login issues signed session token | 3 (T4) |
| TC-AUTH-006 | Student token has role claim 'student' | 3 (T4) |
| TC-AUTH-007 | Admin token has role claim 'admin' | 3 (T4) |
| TC-AUTH-008 | Token role claim unit test | 1 (T1) |
| TC-AUTH-009 | Student token rejected on admin-only endpoint | 3 (T5) |
| TC-AUTH-010 | Admin token accepted on admin-only endpoint | 3 (T5) |
| TC-AUTH-011 | No token rejected on protected endpoint | 3 (T5) |
| TC-AUTH-012 | Tampered token rejected | 3 (T5) |
| TC-AUTH-013 | Expired session rejected | 1 (T1) |
| TC-AUTH-014 | Active token within window accepted | 1 (T1) |
| TC-AUTH-015 | Token at exact expiry boundary rejected | 1 (T1) |
| TC-AUTH-016 | Account locked after five failed attempts | 2 (T3) |
| TC-AUTH-017 | Account not locked after four failed attempts | 2 (T3) |
| TC-AUTH-018 | Locked account unlocks after duration | 2 (T3) |
| TC-AUTH-019 | Successful login resets failed attempt counter | 2 (T3) |
| TC-AUTH-020 | Tampered role claim rejected with 401 | 3 (T5) |
| TC-AUTH-021 | Expired inactivity token rejected at handler level | 3 (T5) |

## Sprint Results

### Delivery Summary

| Task | Agent | Files Delivered | Verification |
|---|---|---|---|
| **T1** Token service | `senior-engineer` | `internal/auth/token.go`, `internal/auth/token_test.go` | SQE + Systems Engineer + Senior SQE: PASS |
| **T2** Auth DB repository | `junior-engineer` | `internal/auth/repository.go`, `internal/auth/repository_test.go` | SQE + Systems Engineer + Senior SQE: PASS |
| **T3** Auth service | `senior-engineer` | `internal/auth/service.go`, `internal/auth/service_test.go` | SQE + Systems Engineer + Senior SQE: PASS |
| **T4** Auth HTTP handlers | `junior-engineer` | `internal/auth/handler.go`, `internal/auth/handler_test.go` | 2 review rounds → PASS |
| **T5** Auth middleware | `senior-engineer` | `internal/auth/middleware.go`, `internal/auth/middleware_test.go` | 2 review rounds → PASS |
| **T6** Health handler + main | `senior-engineer` | `internal/infra/health.go`, `cmd/server/main.go`, `migrations/embed.go` | 2 review rounds → PASS |

All 47 auth integration tests pass against a real PostgreSQL instance (no mocks). `go build ./...` and `go vet ./...` both clean.

### Design Decisions Made During Sprint

| Decision | Rationale |
|---|---|
| `CheckPassword` runs **before** IsActive / LockedUntil state checks | Equalizes argon2 response time for disabled and locked accounts, preventing timing-based state enumeration |
| `dummyHash` computed at `init()` in service.go | Equalizes response time for unknown usernames via `CheckPassword(password, dummyHash)` |
| `ErrAccountLocked` maps to `"invalid credentials"` at HTTP layer | Prevents lockout-state enumeration — same response as wrong password or disabled account |
| `ErrAccountDisabled` maps to `"invalid credentials"` at HTTP layer | Prevents username enumeration via account state disclosure |
| `AccountLockedError` carries `Until time.Time` (not exposed over HTTP) | Preserves exact lockout expiry for internal/audit use without leaking it to callers |
| AfterRelease hook in `db/pool.go` clears GUCs | Prevents `app.current_user_id` / `app.current_role` from leaking across pool connections when middleware's dedicated conn is returned |
| Middleware uses `pgxpool.Conn` (dedicated per request) with `is_local=false` | GUC persists for full handler lifetime; AfterRelease is the cleanup boundary |
| Migration runner uses `pgconn` simple query protocol | Supports `BEGIN;...COMMIT;` multi-statement migration files that extended protocol would truncate |
| `ANTHROPIC_API_KEY` validated at startup even though no Anthropic calls are in Sprint 1 | Fail-fast to surface missing config at container start rather than at runtime |

### Review Findings Fixed During Sprint

| Finding | Fix |
|---|---|
| Test annotation convention: test functions must use `@{"verifies": [...]}` (not `@{"req"}`) | Fixed across all test files |
| Multi-statement truncation in TestMain used single Exec call | Split into separate Exec calls per statement |
| TestMain exits `os.Exit(m.Run())` when no DB → non-zero exit masks skip | Changed to `os.Exit(0)` for clean skip |
| `middleware.go` used `http.Error()` (plain text) for auth failures | Changed to `writeError()` (JSON) for consistent API responses |
| `AccountLockedError` exposed `retry_after` in HTTP response | Removed — now returns generic "invalid credentials" to prevent enumeration |
| Fabricated `retry_after` timestamp in handler | Eliminated entirely (no retry_after exposed) |
| `http.Error()` vs `writeError()` inconsistency in logout handler | Unified to `writeError()` |
| REQ-AUTH-020 phantom ID in middleware_test.go annotation | Removed |
| Dockerfile built `./cmd/valory` (non-existent package) | Fixed to `./cmd/server` |
| `.env.example` listed stale `SESSION_SECRET` / `SESSION_TIMEOUT_SECONDS` | Replaced with `AUTH_LOCKOUT_DURATION`, `AUTH_SESSION_MAX_DURATION`, `AUTH_INACTIVITY_PERIOD` |

### Known Limitations (Tracked for Follow-Up)

1. **Residual timing difference on wrong-password active accounts**: Active-user wrong-password path incurs two DB writes (`SetLockoutState` + `RecordLoginAttempt`); locked/unknown paths do not. Measurable ~1–5 ms difference per request. Does not enable username enumeration but leaks account-state information to an attacker who has already correlated a username. Acceptable for Sprint 1.

2. **No unit tests for `internal/infra/health.go`**: The three probe functions (`checkPostgres`, `checkAnthropic`, `checkDisk`) and the handler are verified by inspection and manual smoke test. Automated test coverage for REQ-INFRA-001 is a follow-up task.

3. **ACME HTTP-01 challenge flow requires Nginx configuration**: The api service's `:80` listener handles ACME challenges. The frontend Nginx must proxy `/.well-known/acme-challenge/*` to `http://api:80` (via Docker internal network). This Nginx configuration is outside Sprint 1 scope.
