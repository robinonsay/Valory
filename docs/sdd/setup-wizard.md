# SDD — First-Run Administrator Setup Wizard + SMTP Send-Timeout Hardening

| Field | Value |
|---|---|
| Document ID | SDD-SETUP-001 |
| Status | Draft |
| Author | Design Author |
| Date | 2026-06-13 |
| Module(s) | `internal/setup`, `frontend/src/views/SetupView.vue`, `frontend/src/stores/setup.ts`, `internal/email/mailer.go` |

---

## 1. Overview

A fresh Valory installation contains zero users. There is no credential with which an operator can authenticate, so every authenticated route is unreachable. The setup wizard solves this bootstrap problem with two mechanisms:

1. **Backend** — two unauthenticated API endpoints (`GET /api/v1/setup/status` and `POST /api/v1/setup`) that are wired outside every middleware gate. The POST is made race-safe by executing the insert inside a transaction that first acquires a fixed PostgreSQL advisory lock and re-checks the user count before touching any row.

2. **Frontend** — a Pinia store that checks setup status at SPA boot (before the router guard runs) and a `/setup` route with a two-step wizard. The existing router guard gains one new top-level rule (rule 0) that redirects all traffic to `/setup` while `needsSetup` is true, and redirects `/setup` to `/login` once it is false.

The SMTP timeout hardening is a targeted change to `internal/email/mailer.go`: it wraps the `Send` method body in a `context.WithTimeout` deadline and sets a concrete `net.Dialer.Timeout` on every dial call so that a non-responsive relay cannot block the HTTP request goroutine indefinitely.

No new database tables are required. The setup endpoints read and write only the existing `users` and `audit_log` tables.

### 1.1 Residual risk — setup re-opening after total user deletion

`needs_setup` is defined as `COUNT(*) FROM users == 0`, so in principle deleting every user row would re-open the public, unauthenticated setup endpoint and allow a new admin to be created. In the current system this window is **not reachable**: there is no admin-delete path (only student deletion via `DeleteStudent`, plus deactivation — which preserves the row), so at least one user row always remains once setup has completed. This is a documented, accepted residual risk for this sprint, not a code defect.

If a future feature ever adds the ability to hard-delete an admin (or otherwise empty the `users` table), this endpoint must be hardened first. The recommended mitigation is a one-time, immutable `setup_completed` marker (e.g. a write-once `system_config` row) consulted by `needs_setup` in addition to the user count, so setup can never re-open regardless of row count.

---

## 2. Requirements in Scope

| Requirement | Title |
|---|---|
| REQ-SYS-071 | First-Run Administrator Setup Wizard |
| REQ-SETUP-001 | Setup Status Endpoint |
| REQ-SETUP-002 | Setup Status Reflects Zero-User Condition |
| REQ-SETUP-003 | Create First Administrator Endpoint |
| REQ-SETUP-004 | First Administrator Role Assignment |
| REQ-SETUP-005 | Atomic One-Shot Setup Guard |
| REQ-SETUP-006 | Setup Password Minimum Length Enforcement |
| REQ-SETUP-007 | Setup Blank Username Rejection |
| REQ-SETUP-008 | First Admin Password Stored as Hash Only |
| REQ-SETUP-009 | Setup Audit Entry Written on First Admin Creation |
| REQ-SETUP-010 | Setup Audit Actor Is the Newly Created Admin |
| REQ-SETUP-011 | Setup Endpoint Exempt from CSRF Protection |
| REQ-FESETUP-001 | Boot-Time Setup Status Check |
| REQ-FESETUP-002 | All Navigation Redirected to Setup When Setup Is Required |
| REQ-FESETUP-003 | Setup Route Redirects to Login When Setup Is Not Required |
| REQ-FESETUP-004 | Setup Wizard Welcome Step |
| REQ-FESETUP-005 | Setup Wizard Admin Account Form |
| REQ-FESETUP-006 | Setup Wizard Client-Side Validation |
| REQ-FESETUP-007 | Setup Wizard Success Confirmation and Login Redirect |
| REQ-EMAIL-012 | SMTP Dial Timeout |
| REQ-EMAIL-013 | SMTP Send Overall Deadline |

---

## 3. Data Model

No new tables are required. The setup endpoints operate on existing tables only.

### 3.1 Users table (existing, `migrations/001_auth.sql`)

The `POST /api/v1/setup` handler inserts exactly one row using the following columns. All other columns use their `DEFAULT` values.

| Column | Value written by setup |
|---|---|
| `id` | `gen_random_uuid()` (DEFAULT) |
| `username` | operator-supplied, trimmed |
| `password_hash` | argon2id hash via `auth.HashPassword` |
| `role` | `'admin'` |
| `is_active` | `true` (DEFAULT) |
| `must_change_password` | `false` (DEFAULT — the admin chooses their own password) |

The `email` column is `NULL` when the operator omits the optional email field. The existing column is `TEXT` and nullable; no schema change is needed.

### 3.2 Audit log (existing, `internal/audit`)

One row is appended to `audit_log` within the same transaction as the `users` insert:

| Field | Value |
|---|---|
| `admin_id` | UUID of the newly created admin (from `RETURNING id`) |
| `action` | `"setup.initial_admin"` |
| `target_type` | `"user"` |
| `target_id` | UUID of the newly created admin |
| `payload` | `{"username": "<username>"}` |

### 3.3 Migration plan

No schema migration is required for the setup feature. The SMTP timeout hardening is a code-only change. If a future requirement adds a dedicated `setup_complete` flag to a config table, a new numbered migration must be authored at that time with a `BEGIN` / `COMMIT` wrapper and a `schema_migrations` insert, following the pattern established in `migrations/001_auth.sql`.

---

## 4. API Contract

### 4.1 GET /api/v1/setup/status

**Purpose** — machine-readable signal for the frontend boot sequence and any external health/bootstrap tooling.

| Property | Value |
|---|---|
| Method | `GET` |
| Path | `/api/v1/setup/status` |
| Authentication | None |
| CSRF | None |
| Rate-limiting | None (fast DB read; no advisory lock needed) |

**Response — 200 OK (always)**

```json
{ "needs_setup": true }
```

or

```json
{ "needs_setup": false }
```

`needs_setup` is `true` if and only if `SELECT COUNT(*) FROM users` returns 0. The query runs outside any transaction; Read Committed isolation is sufficient here because a stale read that reports `needs_setup: true` when an admin was just created can only result in an extra redirect to `/setup`, which the wizard's own 409 guard will immediately correct.

**Error responses**

| Status | Condition |
|---|---|
| 500 | Database query fails |

500 body:
```json
{ "error": "internal_error" }
```

### 4.2 POST /api/v1/setup

**Purpose** — create the first administrator account atomically.

| Property | Value |
|---|---|
| Method | `POST` |
| Path | `/api/v1/setup` |
| Authentication | None |
| CSRF | None — no session exists at first run (REQ-SETUP-011) |
| Content-Type | `application/json` |

**Request body**

```json
{
  "username": "admin",
  "email": "admin@example.com",
  "password": "secretpass123"
}
```

| Field | Type | Required | Constraints |
|---|---|---|---|
| `username` | string | Yes | Non-blank after `strings.TrimSpace`; max 255 characters (enforced by DB `TEXT` column; no explicit cap needed beyond what the DB rejects) |
| `email` | string | No | When present, passed as-is to the `email` column; no format validation at this layer (format validation is intentionally deferred to avoid blocking setup on regex edge cases) |
| `password` | string | Yes | Length ≥ 8 characters after UTF-8 byte count |

The password field is consumed only for hashing. It must never appear in any response body, log line, or audit payload.

**Response — 201 Created**

```json
{
  "username": "admin",
  "role": "admin"
}
```

Only `username` and `role` are returned. No `id`, no `email`, no `password_hash`.

**Error responses**

| Status | Error body `"error"` value | Condition |
|---|---|---|
| 400 | `"username_required"` | Username is blank or whitespace-only after trim |
| 400 | `"password_too_short"` | Password length < 8 characters |
| 400 | `"invalid_json"` | Request body is not valid JSON or missing required fields |
| 409 | `"already_configured"` | At least one user already exists (checked inside the advisory-lock transaction) |
| 500 | `"internal_error"` | Hash failure, DB error, or audit append failure |

All error bodies follow the shape:
```json
{ "error": "<error_code>" }
```

---

## 5. Backend Package Design — `internal/setup`

### 5.1 File layout

```
internal/setup/
  handler.go     — HTTP layer: decode request, call service, write response
  service.go     — Business logic: validation, transaction orchestration
  repository.go  — DB layer: CountUsers, CreateAdmin
```

### 5.2 Advisory lock key

```go
// setupAdvisoryLockKey serializes concurrent POST /api/v1/setup calls.
// The value 0x56414C53455455 encodes "VALSETUP" in ASCII hex.
// It must be unique among all advisory locks taken by this application.
// The audit chain lock (0x56414C41554449, "VALAUDI") is a different key.
const setupAdvisoryLockKey = int64(0x56414C53455455)
```

### 5.3 `repository.go` — function signatures

```go
package setup

import (
    "context"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
    pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository

// CountUsers returns the total number of rows in the users table.
// Used by GetStatus (outside a transaction) and by CreateAdmin (inside the
// advisory-lock transaction as the re-check step).
func (r *Repository) CountUsers(ctx context.Context, tx pgx.Tx) (int64, error)

// CreateAdmin inserts a new user row with role='admin' and returns the
// generated UUID. Must be called inside an open transaction that has already
// acquired setupAdvisoryLockKey.
func (r *Repository) CreateAdmin(
    ctx context.Context,
    tx pgx.Tx,
    username string,
    email *string,
    passwordHash string,
) (uuid.UUID, error)
```

`CountUsers` accepts a `pgx.Tx` parameter that may be nil. When nil, the function queries against `r.pool` directly. When non-nil, it queries against the transaction, enabling the in-transaction re-check without a separate signature.

### 5.4 `service.go` — function signatures

```go
package setup

import (
    "context"
    "errors"

    "github.com/valory/valory/internal/audit"
    "github.com/valory/valory/internal/auth"
)

var (
    ErrAlreadyConfigured = errors.New("setup: system already has at least one user")
    ErrUsernameMissing   = errors.New("setup: username is blank or whitespace-only")
    ErrPasswordTooShort  = errors.New("setup: password must be at least 8 characters")
)

type Service struct {
    repo      *Repository
    auditRepo *audit.Repository
    pool      *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool, repo *Repository, auditRepo *audit.Repository) *Service

// NeedsSetup returns true iff the users table is empty. Runs a plain pool
// query — no transaction, no advisory lock.
func (s *Service) NeedsSetup(ctx context.Context) (bool, error)

// CreateFirstAdmin validates input, acquires the advisory lock, re-checks
// the user count, inserts the admin row and audit entry, then commits.
// Returns ErrAlreadyConfigured, ErrUsernameMissing, or ErrPasswordTooShort
// on the respective validation failures.
func (s *Service) CreateFirstAdmin(
    ctx context.Context,
    username string,
    email *string,
    password string,
) (AdminCreatedResult, error)

type AdminCreatedResult struct {
    Username string
    Role     string
}
```

### 5.5 Race-safe transaction algorithm — exact statement order

`CreateFirstAdmin` executes the following steps in order. Any error at any step rolls back the transaction and surfaces the error to the caller.

```
1. Validate input (before opening the transaction — fail fast without touching DB)
   a. username = strings.TrimSpace(username)
   b. if username == ""  → return ErrUsernameMissing
   c. if len(password) < 8  → return ErrPasswordTooShort   (byte length, not rune count)

2. passwordHash, err = auth.HashPassword(password)
   if err != nil → return err

3. tx, err = pool.Begin(ctx)
   defer tx.Rollback(ctx)   // no-op after Commit

4. tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", setupAdvisoryLockKey)
   // Blocks until no concurrent setup transaction holds the lock.
   // The lock is automatically released at COMMIT or ROLLBACK.

5. count, err = repo.CountUsers(ctx, tx)
   if count > 0 → tx.Rollback; return ErrAlreadyConfigured

6. adminID, err = repo.CreateAdmin(ctx, tx, username, email, passwordHash)
   // INSERT INTO users ... RETURNING id

7. auditRepo.Append(ctx, tx, audit.Entry{
       AdminID:    adminID,
       Action:     "setup.initial_admin",
       TargetType: "user",
       TargetID:   &adminID,
       Payload:    map[string]any{"username": username},
   })
   // audit.Append acquires auditChainLockID (0x56414C41554449) inside this
   // same transaction. PostgreSQL allows a single transaction to hold multiple
   // different advisory locks simultaneously, so this does not deadlock.

8. tx.Commit(ctx)
   return AdminCreatedResult{Username: username, Role: "admin"}, nil
```

### 5.6 `handler.go` — function signatures

```go
package setup

import (
    "net/http"

    "github.com/go-chi/chi/v5"
)

type Handler struct {
    svc *Service
}

func NewHandler(svc *Service) *Handler

// Routes registers the two setup endpoints on the provided router.
// The router must already be a public (unauthenticated, no-CSRF) group.
func (h *Handler) Routes(r chi.Router)

// handleGetStatus serves GET /api/v1/setup/status
func (h *Handler) handleGetStatus(w http.ResponseWriter, r *http.Request)

// handlePost serves POST /api/v1/setup
func (h *Handler) handlePost(w http.ResponseWriter, r *http.Request)
```

`handlePost` maps service errors to HTTP status codes as follows:

| Error | HTTP status | Body `"error"` |
|---|---|---|
| `ErrUsernameMissing` | 400 | `"username_required"` |
| `ErrPasswordTooShort` | 400 | `"password_too_short"` |
| JSON decode failure | 400 | `"invalid_json"` |
| `ErrAlreadyConfigured` | 409 | `"already_configured"` |
| anything else | 500 | `"internal_error"` |

### 5.7 Wiring in `cmd/server/main.go`

The setup handler is constructed after the pool and audit repository, and before the router is built. It is mounted inside a new `r.Route("/setup", ...)` block that sits alongside the existing `/auth` and `/password-reset` public groups — outside every middleware group that carries authentication or CSRF middleware.

```go
// Construct (after pool and auditRepo are initialised)
setupRepo    := setup.NewRepository(pool)
setupSvc     := setup.NewService(pool, setupRepo, auditRepo)
setupHandler := setup.NewHandler(setupSvc)

// Mount (inside r.Route("/api/v1", ...) but outside all middleware groups)
// @{"req": ["REQ-SETUP-001", "REQ-SETUP-003", "REQ-SETUP-011", "REQ-SYS-071"]}
// Setup routes are public: no auth, no CSRF. They must remain reachable on
// a fresh install before any user account exists.
r.Route("/setup", func(r chi.Router) {
    setupHandler.Routes(r)
})
```

The `Routes` method registers:
- `r.Get("/status", h.handleGetStatus)` → `GET /api/v1/setup/status`
- `r.Post("/", h.handlePost)` → `POST /api/v1/setup`

---

## 6. Frontend Design

### 6.1 Setup Pinia store — `frontend/src/stores/setup.ts`

```typescript
// @{"req": ["REQ-FESETUP-001", "REQ-FESETUP-002", "REQ-FESETUP-003", "REQ-SYS-071"]}
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { get } from '@/api/client'

interface SetupStatusResponse {
  needs_setup: boolean
}

export const useSetupStore = defineStore('setup', () => {
  const needsSetup = ref<boolean | null>(null) // null = not yet checked

  // setupPromise is set once checkSetupStatus() is first called and resolves
  // when the fetch completes. Subsequent calls return the same promise so the
  // router guard's await never fires a second network request (mirrors the
  // auth store's restorePromise pattern).
  let setupPromise: Promise<void> | null = null

  function checkSetupStatus(): Promise<void> {
    if (setupPromise !== null) return setupPromise
    setupPromise = (async () => {
      try {
        const data = await get<SetupStatusResponse>('/api/v1/setup/status')
        needsSetup.value = data.needs_setup
      } catch {
        // Network or server error: treat as setup not required to avoid
        // blocking the app indefinitely. The POST /api/v1/setup endpoint will
        // return 409 if an admin actually exists.
        needsSetup.value = false
      }
    })()
    return setupPromise
  }

  function getSetupPromise(): Promise<void> | null {
    return setupPromise
  }

  // markComplete sets needsSetup to false after a successful POST /api/v1/setup
  // so the guard does not redirect back to /setup before the router navigates
  // to /login.
  function markComplete(): void {
    needsSetup.value = false
  }

  return { needsSetup, checkSetupStatus, getSetupPromise, markComplete }
})
```

### 6.2 App.vue boot integration

`App.vue` calls `setup.checkSetupStatus()` in `onMounted`, alongside the existing `auth.restoreSession()` call. Both are fire-and-forget (no await needed here — the router guard awaits them via their stored promises).

```typescript
// @{"req": ["REQ-FESETUP-001", "REQ-SYS-071"]}
import { onMounted } from 'vue'
import { RouterView } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useSetupStore } from '@/stores/setup'

const auth = useAuthStore()
const setup = useSetupStore()

onMounted(() => {
  auth.restoreSession()
  setup.checkSetupStatus()
})
```

### 6.3 Router — new route record

Add a `/setup` route to the `routes` array in `frontend/src/router/index.ts`, before the catch-all `/:pathMatch(.*)*` record:

```typescript
// @{"req": ["REQ-FESETUP-001", "REQ-FESETUP-002", "REQ-FESETUP-003", "REQ-SYS-071"]}
{
  path: '/setup',
  name: 'setup',
  component: () => import('@/views/SetupView.vue'), // lazy-loaded
  meta: {} // no requiresAuth — deliberately public
},
```

No `requiresAuth` meta key; the guard's new rule 0 controls access in both directions.

### 6.4 Router guard — new rule 0

The `guardFn` signature gains two new parameters: `needsSetup: boolean | null` and `setupPromise: Promise<void> | null`. The guard awaits `setupPromise` immediately after it awaits `auth.restorePromise`, then applies rule 0 before all existing rules.

#### Updated `guardFn` signature

```typescript
export async function guardFn(
  to: RouteLocationNormalized,
  auth: {
    restorePromise?: Promise<void> | null
    isAuthenticated: boolean
    isAdmin: boolean
    isStudent: boolean
    isConsented: boolean
    isExpired: boolean
    mustChangePassword: boolean
    onboardingPrompted: boolean
    pendingTwoFactor: { token: string; expiresAt: number } | null
    logout: () => void
  },
  setupState: {                               // NEW parameter
    setupPromise?: Promise<void> | null
    needsSetup: boolean | null
  }
): Promise<RouteLocationRaw | undefined>
```

#### Rule 0 — inserted before existing rule 1a

```typescript
// Await setup status before any routing decision — mirrors the auth
// restorePromise pattern (REQ-FESETUP-001).
if (setupState.setupPromise) {
  await setupState.setupPromise
}

// @{"req": ["REQ-FESETUP-002", "REQ-FESETUP-003"]}
// Rule 0a: setup required — hard-redirect everything to /setup.
if (setupState.needsSetup === true && to.path !== '/setup') {
  return '/setup'
}
// Rule 0b: setup complete — /setup is not reachable.
if (setupState.needsSetup === false && to.path === '/setup') {
  return '/login'
}
// Rule 0c: needsSetup is null (check failed) — allow navigation to proceed;
// the /setup route will not be redirected to, and /setup itself will not
// redirect away. This edge case is rare and self-corrects on next navigation.
```

#### Updated `router.beforeEach` call

```typescript
router.beforeEach(async (to, _from) => {
  const auth = useAuthStore()
  const setup = useSetupStore()
  const redirect = await guardFn(
    to,
    {
      restorePromise: auth.getRestorePromise(),
      isAuthenticated: auth.isAuthenticated,
      isAdmin: auth.isAdmin,
      isStudent: auth.isStudent,
      isConsented: auth.isConsented,
      isExpired: auth.isExpired,
      mustChangePassword: auth.mustChangePassword,
      onboardingPrompted: auth.onboardingPrompted,
      pendingTwoFactor: auth.pendingTwoFactor,
      logout: auth.logout,
    },
    {
      setupPromise: setup.getSetupPromise(),
      needsSetup: setup.needsSetup,
    }
  )
  return redirect ?? true
})
```

**Rule ordering justification**: Rule 0 must execute before rule 1a (2FA pending redirect) because on a fresh install there is no session, no pending 2FA token, and no authentication state — all subsequent rules assume at least one user exists. Running the setup check first avoids any guard logic depending on absent auth state.

### 6.5 API client method

No new wrapper function is needed. The setup store uses the existing `get<T>` from `@/api/client`. The wizard form uses the existing `post<T>` function:

```typescript
// In SetupView.vue (see section 6.6)
import { post } from '@/api/client'

interface SetupRequest {
  username: string
  email?: string
  password: string
}

interface SetupResponse {
  username: string
  role: string
}

// Called on form submit (after client-side validation passes)
const result = await post<SetupResponse>('/api/v1/setup', payload)
```

`post<T>` will attempt to attach the `__Host-csrf` cookie value as `X-CSRF-Token` if the cookie exists, but on a fresh install the cookie is not set (no session → no CSRF cookie), so the header is simply omitted. The server does not check it for this endpoint (REQ-SETUP-011).

### 6.6 SetupView.vue — two-step wizard

**File**: `frontend/src/views/SetupView.vue`

The component is a single-file Vue 3 component using `<script setup lang="ts">`.

#### State

```typescript
const step = ref<'welcome' | 'form' | 'success'>('welcome')
const username = ref('')
const email = ref('')
const password = ref('')
const confirmPassword = ref('')
const validationErrors = ref<Record<string, string>>({})
const serverError = ref<string | null>(null)
const submitting = ref(false)
```

#### Step 1 — welcome (REQ-FESETUP-004)

Displayed when `step.value === 'welcome'`. Content:
- Heading: "Welcome to Valory"
- Explanation paragraph: "No administrator account exists yet. This wizard creates the first administrator account. You will use these credentials to log in and manage the platform."
- A "Get Started" button that sets `step.value = 'form'`.

#### Step 2 — form (REQ-FESETUP-005, REQ-FESETUP-006)

Displayed when `step.value === 'form'`. Fields:

| Field | Input type | Required | Validation |
|---|---|---|---|
| Username | `text` | Yes | Non-blank after trim |
| Email | `email` | No | No client-side format validation; empty string omitted from request payload |
| Password | `password` | Yes | Length ≥ 8 (byte length matches server rule) |
| Confirm password | `password` | Yes | Must equal `password` field |

Client-side validation runs on submit before `post` is called. Errors are displayed inline adjacent to the relevant field. The submit button is disabled while `submitting` is true.

**Validation function** (runs entirely client-side, no network call):

```typescript
function validate(): boolean {
  validationErrors.value = {}
  if (username.value.trim() === '') {
    validationErrors.value.username = 'Username is required.'
  }
  if (password.value.length < 8) {
    validationErrors.value.password = 'Password must be at least 8 characters.'
  }
  if (password.value !== confirmPassword.value) {
    validationErrors.value.confirmPassword = 'Passwords do not match.'
  }
  return Object.keys(validationErrors.value).length === 0
}
```

**Submit handler**:

```typescript
async function submit(): Promise<void> {
  if (!validate()) return
  submitting.value = true
  serverError.value = null
  try {
    const payload: SetupRequest = { username: username.value.trim(), password: password.value }
    if (email.value.trim() !== '') payload.email = email.value.trim()
    await post<SetupResponse>('/api/v1/setup', payload)
    setup.markComplete()          // update store so guard rule 0b fires correctly
    step.value = 'success'
  } catch (err) {
    if (err instanceof ApiError) {
      if (err.status === 409) {
        serverError.value = 'An administrator account already exists. Please log in.'
      } else if (err.status === 400) {
        serverError.value = 'Invalid input. Please check your entries and try again.'
      } else {
        serverError.value = 'An unexpected error occurred. Please try again.'
      }
    } else {
      serverError.value = 'Network error. Please check your connection.'
    }
  } finally {
    submitting.value = false
  }
}
```

#### Step 3 — success (REQ-FESETUP-007)

Displayed when `step.value === 'success'`. Content:
- Heading: "Setup Complete"
- Body: "Your administrator account has been created. You can now log in."
- The component calls `router.push('/login')` in a `watch` on `step`:

```typescript
watch(step, (val) => {
  if (val === 'success') {
    // Short delay so the operator sees the confirmation message before redirect.
    setTimeout(() => router.push({ name: 'login', query: { username: username.value } }), 1500)
  }
})
```

The `username` query parameter is optional — `LoginView.vue` may pre-fill the username field if it reads `route.query.username`. The backend does not use it.

---

## 7. SMTP Send-Timeout Hardening — `internal/email/mailer.go`

### 7.1 Problem

All three dial paths (`sendSTARTTLS`, `sendImplicitTLS`, `sendPlain`) use `net.Dialer{}` or `tls.Dialer{NetDialer: &net.Dialer{}}` with no `Timeout` field, so a TCP SYN to a non-responsive SMTP server blocks the goroutine indefinitely. Additionally, the `Send` method has no overall deadline: even after a successful TCP connection, a throttled server can stall the DATA phase indefinitely. The calling HTTP handler goroutine is held open for the duration (REQ-EMAIL-012, REQ-EMAIL-013).

### 7.2 Solution

Two changes to `mailer.go`:

**Change 1 — overall send deadline (REQ-EMAIL-013)**

Wrap the body of `Send` with a 30-second `context.WithTimeout`. The 30-second value provides a generous margin over the 10-second operational target stated in REQ-EMAIL-013, accommodating slow but non-hung relays on high-latency links. The context passed to the three send helpers is the derived (deadline-bounded) context, not the original caller context.

```go
const smtpSendTimeout = 15 * time.Second

func (m *SMTPMailer) Send(ctx context.Context, to, subject, body string) error {
    if err := ctx.Err(); err != nil {
        return err
    }

    // @{"req": ["REQ-EMAIL-013"]}
    // Bound the entire send operation so a slow SMTP server cannot hold the
    // HTTP request goroutine open past smtpSendTimeout.
    ctx, cancel := context.WithTimeout(ctx, smtpSendTimeout)
    defer cancel()

    cfg := m.resolve(ctx)
    // ... remainder unchanged ...
}
```

**Change 2 — dial timeout (REQ-EMAIL-012)**

Replace `&net.Dialer{}` with `&net.Dialer{Timeout: smtpDialTimeout}` in all three dial paths. Set the same `Timeout` on the `tls.Dialer.NetDialer` in `sendImplicitTLS`. The dial timeout is 10 seconds — the operational target stated in REQ-EMAIL-012. Because the dial timeout is a subset of the overall send timeout (10 s < 15 s), the overall deadline always fires after the dial timeout under normal failure conditions, giving the two constants independent roles.

```go
const smtpDialTimeout = 10 * time.Second
```

**`sendSTARTTLS` — before and after**

```go
// Before
netConn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)

// After (REQ-EMAIL-012)
netConn, err := (&net.Dialer{Timeout: smtpDialTimeout}).DialContext(ctx, "tcp", addr)
```

**`sendImplicitTLS` — before and after**

```go
// Before
dialer := &tls.Dialer{
    NetDialer: &net.Dialer{},
    Config:    &tls.Config{ServerName: cfg.host},
}

// After (REQ-EMAIL-012)
dialer := &tls.Dialer{
    NetDialer: &net.Dialer{Timeout: smtpDialTimeout},
    Config:    &tls.Config{ServerName: cfg.host},
}
```

**`sendPlain` — before and after**

```go
// Before
netConn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)

// After (REQ-EMAIL-012)
netConn, err := (&net.Dialer{Timeout: smtpDialTimeout}).DialContext(ctx, "tcp", addr)
```

**Write/read deadline on net.Conn** — after dialing, set a deadline on the raw connection so SMTP protocol steps (EHLO, STARTTLS, AUTH, MAIL, RCPT, DATA) cannot block past the overall context deadline:

```go
// In sendSTARTTLS and sendPlain, immediately after dial succeeds:
if deadline, ok := ctx.Deadline(); ok {
    _ = netConn.SetDeadline(deadline)
}
```

For `sendImplicitTLS` the connection is a `*tls.Conn` which implements `net.Conn`; the same pattern applies using `tlsConn`:

```go
if deadline, ok := ctx.Deadline(); ok {
    _ = tlsConn.SetDeadline(deadline)
}
```

This ensures that even if the SMTP server accepts the TCP connection but then stalls in the DATA phase, the connection-level deadline (derived from `context.WithTimeout`) will trigger a `i/o timeout` error and unblock the goroutine.

### 7.3 Constants placement

Both constants are declared at package scope in `mailer.go`, alongside the existing `sanitizeHeader` function:

```go
const (
    // smtpDialTimeout bounds the TCP/TLS dial phase (REQ-EMAIL-012).
    smtpDialTimeout = 10 * time.Second
    // smtpSendTimeout bounds the entire Send operation including dial,
    // auth, and data transfer (REQ-EMAIL-013).
    smtpSendTimeout = 15 * time.Second
)
```

---

## 8. Alternatives Considered

### 8.1 Seed row in the migration instead of a setup endpoint

Adding a default admin row in a migration (e.g. with a well-known password) is common in simpler frameworks. This approach was rejected because:
- It produces a known default credential in the database, a class-A security vulnerability in any deployment where the operator forgets to change it.
- It creates an awkward "change default password" prompt rather than a genuine first-run experience.
- It does not satisfy REQ-SETUP-008 (argon2id hash chosen by the admin, not hard-coded).

### 8.2 Dedicated `setup_complete` boolean in a config table

Using a separate `SELECT setup_complete FROM system_config` flag instead of `COUNT(*) FROM users` as the setup signal was considered. It was rejected because:
- It introduces a new table or row, requiring a migration for a feature that adds no persistent state of its own.
- It can desync from reality (e.g., all users manually deleted via psql while the flag remains true).
- The user count is the authoritative source of truth; using it directly means the signal is always accurate.

### 8.3 Redirect `/setup` to `/login` after POST instead of a success step

Redirecting immediately on 201 without a success confirmation screen was considered. It was rejected because REQ-FESETUP-007 explicitly requires a success confirmation before navigation, and an immediate redirect provides no feedback to the operator that the account was created.

### 8.4 SMTP deadline via `net.Conn.SetDeadline` only (no `context.WithTimeout`)

Using only `SetDeadline` on the `net.Conn` without a `context.WithTimeout` wrapping the full `Send` call was considered. It was rejected because:
- The `resolve()` call (which reads config and the SMTP password from the secret provider) happens before the dial and is not bounded by a conn deadline.
- `context.WithTimeout` is the idiomatic Go mechanism for bounding the lifetime of a multi-step operation; `SetDeadline` on the conn alone does not cancel the context that future steps might consult.
- Using both provides defense in depth: the context deadline cancels any context-aware code; the conn deadline terminates blocked I/O.

---

## 9. Open Questions

| ID | Question | Owner |
|---|---|---|
| OQ-1 | Should `POST /api/v1/setup` accept a non-empty `email` field but apply a format validation regex? The current design defers format validation to the DB column (no format constraint exists) and relies on SMTP delivery failure as the eventual signal. PM should confirm whether a format check is required at setup time. | PM |
| OQ-2 | The wizard does not send a welcome email to the newly created admin (no email template exists for setup, and the admin may not have provided an email). If a welcome email is desired, a template must be authored and the setup service must be wired to the mailer. | PM |
| OQ-3 | RESOLVED (Lead): `smtpSendTimeout` is set to **15 seconds**. An interactive login may wait on the 2FA OTP send, so 30 s was judged too long; 15 s bounds the wait while leaving headroom for dial + STARTTLS + auth + data. REQ-EMAIL-013 deliberately leaves the concrete value as an implementation detail. | Lead |
| OQ-4 | The router guard change adds `setupState` as a third parameter to the exported `guardFn`. Existing unit tests for `guardFn` must be updated to pass a third argument. The implementation engineer must enumerate all affected test files. | Contributor |

---

## 10. Requirements Traceability

| Requirement | Satisfied by |
|---|---|
| REQ-SYS-071 | Entire feature: both API endpoints, the advisory-lock guard, the frontend wizard flow |
| REQ-SETUP-001 | `GET /api/v1/setup/status` → `{"needs_setup": bool}` (section 4.1) |
| REQ-SETUP-002 | `NeedsSetup()` queries `COUNT(*) FROM users`; returns `true` iff count is 0 (sections 4.1, 5.4) |
| REQ-SETUP-003 | `POST /api/v1/setup` public endpoint, no auth (section 4.2) |
| REQ-SETUP-004 | `CreateAdmin` inserts with `role='admin'` hardcoded (sections 5.3, 5.5) |
| REQ-SETUP-005 | Advisory lock `setupAdvisoryLockKey` + in-transaction re-check of `CountUsers` before insert (section 5.5) |
| REQ-SETUP-006 | `len(password) < 8` check in step 1 of the transaction algorithm (section 5.5) |
| REQ-SETUP-007 | `strings.TrimSpace(username) == ""` check in step 1 of the transaction algorithm (section 5.5) |
| REQ-SETUP-008 | `auth.HashPassword` called in step 2; raw password never written to DB or returned in response (sections 4.2, 5.5) |
| REQ-SETUP-009 | `auditRepo.Append` with `action: "setup.initial_admin"` in step 7 of the transaction (section 5.5) |
| REQ-SETUP-010 | `AdminID: adminID` (the RETURNING id from the users insert) used as the audit actor (section 5.5) |
| REQ-SETUP-011 | Setup routes mounted outside all CSRF middleware groups; `POST /api/v1/setup` requires no CSRF token (sections 4.2, 5.7) |
| REQ-FESETUP-001 | `setup.checkSetupStatus()` called in `App.vue` `onMounted`; `guardFn` awaits `setupPromise` (sections 6.2, 6.4) |
| REQ-FESETUP-002 | Guard rule 0a: `needsSetup === true && to.path !== '/setup'` → redirect `/setup` (section 6.4) |
| REQ-FESETUP-003 | Guard rule 0b: `needsSetup === false && to.path === '/setup'` → redirect `/login` (section 6.4) |
| REQ-FESETUP-004 | `SetupView.vue` step 1 (`'welcome'`): heading + explanation + "Get Started" button (section 6.6) |
| REQ-FESETUP-005 | `SetupView.vue` step 2 (`'form'`): username, optional email, password, confirm fields (section 6.6) |
| REQ-FESETUP-006 | `validate()` function: non-blank username, password ≥ 8, password === confirmPassword (section 6.6) |
| REQ-FESETUP-007 | `SetupView.vue` step 3 (`'success'`): confirmation message + `setTimeout` redirect to `/login` (section 6.6) |
| REQ-EMAIL-012 | `net.Dialer{Timeout: smtpDialTimeout}` (10 s) in all three dial paths (section 7.2) |
| REQ-EMAIL-013 | `context.WithTimeout(ctx, smtpSendTimeout)` (15 s) wrapping the full `Send` body; `net.Conn.SetDeadline` applied after dial (section 7.2) |
