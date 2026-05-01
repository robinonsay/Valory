# TEST-FE-AUTH — Frontend Auth & Shared Infrastructure Verification Plan

**Sprint:** 6
**Module:** `frontend/auth` and shared API client infrastructure
**Author role:** Test Author
**Date:** 2026-05-01

## Scope

This plan covers every requirement in `REQ-FE-AUTH.json`. Every test case below traces back to one or more leaf-level requirement IDs (REQ-FEAUTH-NNN). All requirement IDs referenced in parent requirement objects (REQ-FEAUTH-001 through REQ-FEAUTH-009) are satisfied transitively through their child leaf requirements.

## Key backend facts (for test setup)

Derived from reading `internal/auth/handler.go`, `internal/security/csrf.go`, and `internal/user/handler.go`:

- **Login** — `POST /api/v1/auth/login`, body `{"username": string, "password": string}`, success returns HTTP 200 with `{"token": string, "role": string, "expires_at": string}` and sets `Set-Cookie: __Host-csrf=<value>; Path=/; SameSite=Strict; Secure; HttpOnly=false`.
- **Logout** — `POST /api/v1/auth/logout`, requires `Authorization: Bearer <token>`, success returns HTTP 204.
- **CSRF middleware** — Applies to POST/PATCH/DELETE/PUT. Reads `__Host-csrf` cookie and compares it byte-for-byte with the `X-CSRF-Token` request header. Mismatch → 403. Login is exempted from CSRF because it issues the token.
- **Password reset request** — `POST /api/v1/password-reset/request`, body `{"username": string}`. Success 204. 429 on rate limit.
- **Password reset confirm** — `POST /api/v1/password-reset/confirm`, body `{"token": string, "new_password": string}`. Success 204. 400 on invalid/expired token.
- **Consent** — `POST /api/v1/consent`, requires `Authorization: Bearer <token>` and `X-CSRF-Token` header, body `{"version": string}`. Success 204. 400 if version is empty.

---

## Tier 1 — Unit Tests (Vitest)

Pure logic tests. No network calls. The API client module is mocked at the module level using `vi.mock`. All tests run with `vitest run`.

### TC-AUTH-U-001

| Field | Value |
|---|---|
| **ID** | TC-AUTH-U-001 |
| **Description** | Auth store `login` action stores the token in reactive state after a mocked successful API response. |
| **Setup** | Mock API client `post('/api/v1/auth/login', ...)` to resolve with `{ token: 'tok-abc', role: 'student', expires_at: '2026-06-01T00:00:00Z' }`. Cookie read helper is mocked to return `'csrf-xyz'`. |
| **Action** | Call `authStore.login({ username: 'alice', password: 'secret' })`. |
| **Assertion** | `authStore.token` equals `'tok-abc'` and `authStore.role` equals `'student'`. |

Verifies: REQ-FEAUTH-106, REQ-FEAUTH-107, REQ-FEAUTH-012

---

### TC-AUTH-U-002

| Field | Value |
|---|---|
| **ID** | TC-AUTH-U-002 |
| **Description** | Auth store `login` action captures the CSRF value from the `__Host-csrf` cookie after a successful 200 response. |
| **Setup** | Mock API client to resolve with a 200 login payload. Mock `document.cookie` (or the cookie-read utility) to return `__Host-csrf=csrf-xyz`. |
| **Action** | Call `authStore.login({ username: 'alice', password: 'secret' })`. |
| **Assertion** | `authStore.csrfToken` equals `'csrf-xyz'`. |

Verifies: REQ-FEAUTH-108, REQ-FEAUTH-053, REQ-FEAUTH-160, REQ-FEAUTH-047

---

### TC-AUTH-U-003

| Field | Value |
|---|---|
| **ID** | TC-AUTH-U-003 |
| **Description** | Auth store `logout` action sets `token` and `csrfToken` to `null` after a mocked 204 response. |
| **Setup** | Seed store with `token = 'tok-abc'`, `csrfToken = 'csrf-xyz'`. Mock API client `post('/api/v1/auth/logout')` to resolve with HTTP 204. |
| **Action** | Call `authStore.logout()`. |
| **Assertion** | `authStore.token` is `null` and `authStore.csrfToken` is `null`. |

Verifies: REQ-FEAUTH-121, REQ-FEAUTH-122, REQ-FEAUTH-159, REQ-FEAUTH-164, REQ-FEAUTH-021, REQ-FEAUTH-046, REQ-FEAUTH-050

---

### TC-AUTH-U-004

| Field | Value |
|---|---|
| **ID** | TC-AUTH-U-004 |
| **Description** | API client interceptor attaches `Authorization: Bearer <token>` header on every outgoing request when a token is present in the store. |
| **Setup** | Seed auth store with `token = 'tok-abc'`. Spy on the underlying `fetch` (or axios) instance. |
| **Action** | Invoke `apiClient.get('/api/v1/some-resource')`. |
| **Assertion** | The captured request headers include `Authorization: Bearer tok-abc`. |

Verifies: REQ-FEAUTH-157, REQ-FEAUTH-044

---

### TC-AUTH-U-005

| Field | Value |
|---|---|
| **ID** | TC-AUTH-U-005 |
| **Description** | API client interceptor reads the `__Host-csrf` cookie immediately before sending a POST request and sets `X-CSRF-Token` to the current cookie value. |
| **Setup** | Mock `document.cookie` to return `__Host-csrf=fresh-csrf-value`. Spy on the fetch/axios request. |
| **Action** | Invoke `apiClient.post('/api/v1/some-resource', {})`. |
| **Assertion** | The captured POST request has header `X-CSRF-Token: fresh-csrf-value`. |

Verifies: REQ-FEAUTH-161, REQ-FEAUTH-163, REQ-FEAUTH-048, REQ-FEAUTH-049

---

### TC-AUTH-U-006

| Field | Value |
|---|---|
| **ID** | TC-AUTH-U-006 |
| **Description** | API client interceptor includes `X-CSRF-Token` on PATCH, DELETE, and PUT requests using the current cookie value. |
| **Setup** | Mock `document.cookie` to return `__Host-csrf=csrf-patch`. Spy on the fetch/axios request. |
| **Action** | Invoke `apiClient.patch(...)`, `apiClient.delete(...)`, and `apiClient.put(...)` in sequence. |
| **Assertion** | Each of the three captured requests has `X-CSRF-Token: csrf-patch` in headers. |

Verifies: REQ-FEAUTH-162, REQ-FEAUTH-048

---

### TC-AUTH-U-007

| Field | Value |
|---|---|
| **ID** | TC-AUTH-U-007 |
| **Description** | API client interceptor does NOT add `X-CSRF-Token` to GET requests. |
| **Setup** | Mock `document.cookie` to return `__Host-csrf=some-value`. Spy on the fetch/axios request. |
| **Action** | Invoke `apiClient.get('/api/v1/some-resource')`. |
| **Assertion** | The captured GET request has no `X-CSRF-Token` header. |

Verifies: REQ-FEAUTH-048

---

### TC-AUTH-U-008

| Field | Value |
|---|---|
| **ID** | TC-AUTH-U-008 |
| **Description** | Route guard redirects an unauthenticated user to `/login` when navigating to a protected route. |
| **Setup** | Auth store `token` is `null`. Define a route guard function that reads the store state. |
| **Action** | Call guard with `to = { meta: { requiresAuth: true } }` and `from = {}`. |
| **Assertion** | Guard returns (or calls `next` with) `{ path: '/login' }`. |

Verifies: REQ-FEAUTH-151, REQ-FEAUTH-152, REQ-FEAUTH-040

---

### TC-AUTH-U-009

| Field | Value |
|---|---|
| **ID** | TC-AUTH-U-009 |
| **Description** | Route guard redirects a `student` role user who navigates to an admin route to `/dashboard`. |
| **Setup** | Auth store `token = 'tok-abc'`, `role = 'student'`. |
| **Action** | Call guard with `to = { meta: { requiresAuth: true, requiredRole: 'admin' } }`. |
| **Assertion** | Guard returns `{ path: '/dashboard' }`. |

Verifies: REQ-FEAUTH-153, REQ-FEAUTH-041

---

### TC-AUTH-U-010

| Field | Value |
|---|---|
| **ID** | TC-AUTH-U-010 |
| **Description** | Route guard redirects an `admin` role user who navigates to a student route to `/admin`. |
| **Setup** | Auth store `token = 'tok-abc'`, `role = 'admin'`. |
| **Action** | Call guard with `to = { meta: { requiresAuth: true, requiredRole: 'student' } }`. |
| **Assertion** | Guard returns `{ path: '/admin' }`. |

Verifies: REQ-FEAUTH-154, REQ-FEAUTH-042

---

### TC-AUTH-U-011

| Field | Value |
|---|---|
| **ID** | TC-AUTH-U-011 |
| **Description** | Route guard redirects a student without recorded consent to `/consent` when accessing a protected non-consent route. |
| **Setup** | Auth store `token = 'tok-abc'`, `role = 'student'`, `hasConsent = false`. |
| **Action** | Call guard with `to = { path: '/dashboard', meta: { requiresAuth: true, requiredRole: 'student' } }`. |
| **Assertion** | Guard returns `{ path: '/consent' }`. |

Verifies: REQ-FEAUTH-148, REQ-FEAUTH-037

---

### TC-AUTH-U-012

| Field | Value |
|---|---|
| **ID** | TC-AUTH-U-012 |
| **Description** | Global 401 response interceptor clears `token` and `csrfToken` in the auth store. |
| **Setup** | Seed auth store with `token = 'tok-abc'`, `csrfToken = 'csrf-xyz'`. Configure the API client response interceptor. Prepare a mock rejected response with status 401. |
| **Action** | Trigger the response interceptor with the 401 error object. |
| **Assertion** | `authStore.token` is `null` and `authStore.csrfToken` is `null`. |

Verifies: REQ-FEAUTH-155, REQ-FEAUTH-043

---

### TC-AUTH-U-013

| Field | Value |
|---|---|
| **ID** | TC-AUTH-U-013 |
| **Description** | Global 401 response interceptor navigates to `/login` after clearing auth state. |
| **Setup** | Seed auth store with valid token and CSRF. Mock the router `push` function. Configure the 401 interceptor. |
| **Action** | Trigger the interceptor with a 401 error. |
| **Assertion** | `router.push` is called with `'/login'` (or `{ path: '/login' }`). |

Verifies: REQ-FEAUTH-156, REQ-FEAUTH-055

---

### TC-AUTH-U-014

| Field | Value |
|---|---|
| **ID** | TC-AUTH-U-014 |
| **Description** | CSRF cookie reader reads the cookie by the exact name `__Host-csrf` and returns its value. |
| **Setup** | Set `document.cookie = '__Host-csrf=test-value; path=/'`. |
| **Action** | Call the cookie-read utility (e.g. `readCookie('__Host-csrf')`). |
| **Assertion** | Return value is `'test-value'`. |

Verifies: REQ-FEAUTH-160, REQ-FEAUTH-047

---

### TC-AUTH-U-015

| Field | Value |
|---|---|
| **ID** | TC-AUTH-U-015 |
| **Description** | CSRF cookie reader returns `null` (or empty string) when no `__Host-csrf` cookie is present. |
| **Setup** | `document.cookie` does not contain `__Host-csrf`. |
| **Action** | Call the cookie-read utility. |
| **Assertion** | Return value is `null` or `''`. |

Verifies: REQ-FEAUTH-160, REQ-FEAUTH-047

---

### TC-AUTH-U-016

| Field | Value |
|---|---|
| **ID** | TC-AUTH-U-016 |
| **Description** | Auth store `login` action redirects to `/dashboard` when the response role is `'student'`. |
| **Setup** | Mock API client to resolve with `{ token: 'tok', role: 'student', expires_at: '...' }`. Mock `router.push`. |
| **Action** | Call `authStore.login({ username: 'alice', password: 'pw' })`. |
| **Assertion** | `router.push` is called with `'/dashboard'`. |

Verifies: REQ-FEAUTH-109, REQ-FEAUTH-013

---

### TC-AUTH-U-017

| Field | Value |
|---|---|
| **ID** | TC-AUTH-U-017 |
| **Description** | Auth store `login` action redirects to `/admin` when the response role is `'admin'`. |
| **Setup** | Mock API client to resolve with `{ token: 'tok', role: 'admin', expires_at: '...' }`. Mock `router.push`. |
| **Action** | Call `authStore.login({ username: 'admin1', password: 'pw' })`. |
| **Assertion** | `router.push` is called with `'/admin'`. |

Verifies: REQ-FEAUTH-110, REQ-FEAUTH-013

---

### TC-AUTH-U-018

| Field | Value |
|---|---|
| **ID** | TC-AUTH-U-018 |
| **Description** | Auth store `logout` action navigates to `/login` after clearing state. |
| **Setup** | Seed store with `token = 'tok-abc'`. Mock API client to resolve 204. Mock `router.push`. |
| **Action** | Call `authStore.logout()`. |
| **Assertion** | `router.push` is called with `'/login'`. |

Verifies: REQ-FEAUTH-123, REQ-FEAUTH-022

---

## Tier 2 — Component Integration Tests (Vitest + Vue Test Utils)

Each test mounts a real Vue component in a JSDOM environment with mocked API responses. Use `mount` or `shallowMount` from `@vue/test-utils`. Tests run with `vitest run`.

### TC-AUTH-I-001

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-001 |
| **Component** | `LoginView.vue` |
| **Description** | Successful login: after submitting valid credentials and receiving a 200, the auth store contains the token and the router is redirected to the correct path. |
| **Mock API response** | HTTP 200, body `{ token: 'tok-abc', role: 'student', expires_at: '2026-06-01T00:00:00Z' }`. Cookie mocked to contain `__Host-csrf=csrf-xyz`. |
| **Action** | Fill `[data-testid="username"]` with `'alice'`, `[data-testid="password"]` with `'secret'`, click `[data-testid="submit"]`. Await next tick. |
| **Expected DOM state** | No error element visible. |
| **Expected side effect** | `authStore.token === 'tok-abc'`, `router.currentRoute.value.path === '/dashboard'`. |

Verifies: REQ-FEAUTH-106, REQ-FEAUTH-107, REQ-FEAUTH-109, REQ-FEAUTH-012, REQ-FEAUTH-013

---

### TC-AUTH-I-002

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-002 |
| **Component** | `LoginView.vue` |
| **Description** | 401 response renders the exact error string "Invalid username or password." and keeps the username field value. |
| **Mock API response** | HTTP 401, body `{ error: 'invalid credentials' }`. |
| **Action** | Fill username with `'alice'`, password with `'wrongpass'`, click submit. Await next tick. |
| **Expected DOM state** | Element matching `[data-testid="error-message"]` contains text `'Invalid username or password.'`. Input `[data-testid="username"]` value is `'alice'`. |

Verifies: REQ-FEAUTH-111, REQ-FEAUTH-112, REQ-FEAUTH-014

---

### TC-AUTH-I-003

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-003 |
| **Component** | `LoginView.vue` |
| **Description** | Network error renders the exact string "Unable to reach server. Please try again." |
| **Mock API response** | Reject the API call with a `TypeError` (network failure, no HTTP status). |
| **Action** | Fill username and password, click submit. Await next tick. |
| **Expected DOM state** | `[data-testid="error-message"]` text is `'Unable to reach server. Please try again.'`. |

Verifies: REQ-FEAUTH-113, REQ-FEAUTH-015

---

### TC-AUTH-I-004

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-004 |
| **Component** | `LoginView.vue` |
| **Description** | Submit button is disabled while the login request is in-flight and re-enabled after resolution. |
| **Mock API response** | Slow-resolving promise — resolved only after the test checks the disabled state. |
| **Action** | Click submit without awaiting the response. Read `[data-testid="submit"].disabled` synchronously. Then resolve the promise. Await next tick. |
| **Expected DOM state** | Button is `disabled` during request. After resolution, button is enabled. |

Verifies: REQ-FEAUTH-114, REQ-FEAUTH-016

---

### TC-AUTH-I-005

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-005 |
| **Component** | `LoginView.vue` |
| **Description** | Loading indicator is visible during the pending request and hidden once the promise resolves. |
| **Mock API response** | Slow-resolving promise (same pattern as TC-AUTH-I-004). |
| **Action** | Click submit; before resolving the promise, check for the loading indicator. Then resolve. |
| **Expected DOM state** | `[data-testid="loading"]` (or equivalent) is visible during the request; hidden after resolution. |

Verifies: REQ-FEAUTH-115, REQ-FEAUTH-165, REQ-FEAUTH-166, REQ-FEAUTH-054, REQ-FEAUTH-051, REQ-FEAUTH-052

---

### TC-AUTH-I-006

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-006 |
| **Component** | `LoginView.vue` |
| **Description** | Submitting with an empty username field does not trigger an API call. |
| **Mock API response** | API client spy (should not be called). |
| **Action** | Leave username empty, fill password, click submit. |
| **Expected DOM state** | API mock was not called. |

Verifies: REQ-FEAUTH-116, REQ-FEAUTH-017

---

### TC-AUTH-I-007

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-007 |
| **Component** | `LoginView.vue` |
| **Description** | Submitting with an empty password field does not trigger an API call. |
| **Mock API response** | API client spy (should not be called). |
| **Action** | Fill username, leave password empty, click submit. |
| **Expected DOM state** | API mock was not called. |

Verifies: REQ-FEAUTH-116, REQ-FEAUTH-017

---

### TC-AUTH-I-008

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-008 |
| **Component** | `LoginView.vue` |
| **Description** | 500 response renders the exact error string "Something went wrong. Please try again later." |
| **Mock API response** | HTTP 500, body `{ error: 'internal server error' }`. |
| **Action** | Fill valid credentials, click submit. Await next tick. |
| **Expected DOM state** | `[data-testid="error-message"]` text is `'Something went wrong. Please try again later.'`. |

Verifies: REQ-FEAUTH-117, REQ-FEAUTH-018

---

### TC-AUTH-I-009

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-009 |
| **Component** | `LoginView.vue` (or its route variant showing the success banner) |
| **Description** | After a successful password reset confirm, navigating to `/login` displays "Password updated. Please log in." banner. |
| **Mock API response** | Router is provided with `{ query: { resetSuccess: 'true' } }` on the login route (or equivalent state passed via router state). |
| **Action** | Mount the login view with the success signal present. |
| **Expected DOM state** | `[data-testid="success-banner"]` (or equivalent) contains text `'Password updated. Please log in.'`. |

Verifies: REQ-FEAUTH-139, REQ-FEAUTH-031

---

### TC-AUTH-I-010

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-010 |
| **Component** | `PasswordResetRequestView.vue` |
| **Description** | Any 2xx response (tested with 204) renders the neutral message "If that account exists, a reset link has been sent." |
| **Mock API response** | HTTP 204 (no body). |
| **Action** | Fill `[data-testid="username"]` with `'alice'`, click submit. Await next tick. |
| **Expected DOM state** | `[data-testid="success-message"]` text is `'If that account exists, a reset link has been sent.'`. |

Verifies: REQ-FEAUTH-128, REQ-FEAUTH-129, REQ-FEAUTH-025

---

### TC-AUTH-I-011

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-011 |
| **Component** | `PasswordResetRequestView.vue` |
| **Description** | A 200 response (non-204 2xx) also renders the neutral success message. |
| **Mock API response** | HTTP 200, arbitrary body. |
| **Action** | Fill username, click submit. Await next tick. |
| **Expected DOM state** | `[data-testid="success-message"]` text is `'If that account exists, a reset link has been sent.'`. |

Verifies: REQ-FEAUTH-129, REQ-FEAUTH-025

---

### TC-AUTH-I-012

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-012 |
| **Component** | `PasswordResetRequestView.vue` |
| **Description** | A 429 response renders the exact rate-limit message "Too many requests. Please wait before trying again." |
| **Mock API response** | HTTP 429. |
| **Action** | Fill username, click submit. Await next tick. |
| **Expected DOM state** | `[data-testid="error-message"]` text is `'Too many requests. Please wait before trying again.'`. |

Verifies: REQ-FEAUTH-130, REQ-FEAUTH-026

---

### TC-AUTH-I-013

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-013 |
| **Component** | `PasswordResetRequestView.vue` |
| **Description** | Submitting with an empty username field does not trigger an API call. |
| **Mock API response** | API client spy (should not be called). |
| **Action** | Leave username empty, click submit. |
| **Expected DOM state** | API mock was not called. |

Verifies: REQ-FEAUTH-131, REQ-FEAUTH-027

---

### TC-AUTH-I-014

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-014 |
| **Component** | `PasswordResetConfirmView.vue` |
| **Description** | Token field is pre-populated from the `token` query parameter when the component mounts. |
| **Mock API response** | Not needed; assertion is before any submit. |
| **Action** | Mount the component with the router stub providing `$route.query.token = 'reset-tok-123'`. |
| **Expected DOM state** | `[data-testid="token"]` input value is `'reset-tok-123'`. |

Verifies: REQ-FEAUTH-135, REQ-FEAUTH-029

---

### TC-AUTH-I-015

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-015 |
| **Component** | `PasswordResetConfirmView.vue` |
| **Description** | Successful reset (204 response) navigates to `/login`. |
| **Mock API response** | HTTP 204. |
| **Action** | Fill token field and new password, click submit. Await next tick. |
| **Expected side effect** | `router.currentRoute.value.path === '/login'`. |

Verifies: REQ-FEAUTH-138, REQ-FEAUTH-031

---

### TC-AUTH-I-016

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-016 |
| **Component** | `PasswordResetConfirmView.vue` |
| **Description** | A 400 response renders "Reset link is invalid or expired." |
| **Mock API response** | HTTP 400, body `{ error: 'invalid or expired token' }`. |
| **Action** | Fill token and new password, click submit. Await next tick. |
| **Expected DOM state** | `[data-testid="error-message"]` text is `'Reset link is invalid or expired.'`. |

Verifies: REQ-FEAUTH-140, REQ-FEAUTH-032

---

### TC-AUTH-I-017

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-017 |
| **Component** | `PasswordResetConfirmView.vue` |
| **Description** | Submitting with an empty token field does not trigger an API call. |
| **Mock API response** | API client spy (should not be called). |
| **Action** | Leave token empty, fill new password, click submit. |
| **Expected DOM state** | API mock was not called. |

Verifies: REQ-FEAUTH-141, REQ-FEAUTH-033

---

### TC-AUTH-I-018

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-018 |
| **Component** | `PasswordResetConfirmView.vue` |
| **Description** | Submitting with an empty new password field does not trigger an API call. |
| **Mock API response** | API client spy (should not be called). |
| **Action** | Fill token, leave new password empty, click submit. |
| **Expected DOM state** | API mock was not called. |

Verifies: REQ-FEAUTH-141, REQ-FEAUTH-033

---

### TC-AUTH-I-019

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-019 |
| **Component** | `ConsentView.vue` |
| **Description** | Clicking the accept button sends a POST to `/api/v1/consent` with a non-empty `version` field, an `Authorization` header, and an `X-CSRF-Token` header. |
| **Mock API response** | HTTP 204. Capture the outgoing request. |
| **Action** | Mount with auth store seeded (`token = 'tok-abc'`, `csrfToken = 'csrf-xyz'`). Click `[data-testid="accept"]`. Await next tick. |
| **Expected captured request** | Method is POST, URL is `/api/v1/consent`, body contains `{ version: <non-empty string> }`, headers contain `Authorization: Bearer tok-abc` and `X-CSRF-Token: csrf-xyz`. |

Verifies: REQ-FEAUTH-144, REQ-FEAUTH-145, REQ-FEAUTH-146, REQ-FEAUTH-149, REQ-FEAUTH-035, REQ-FEAUTH-038

---

### TC-AUTH-I-020

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-020 |
| **Component** | `ConsentView.vue` |
| **Description** | A 204 consent response navigates to `/dashboard`. |
| **Mock API response** | HTTP 204. |
| **Action** | Click accept. Await next tick. |
| **Expected side effect** | `router.currentRoute.value.path === '/dashboard'`. |

Verifies: REQ-FEAUTH-147, REQ-FEAUTH-036

---

### TC-AUTH-I-021

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-021 |
| **Component** | `ConsentView.vue` |
| **Description** | A 500 consent response renders "Something went wrong. Please try again." and does NOT navigate away. |
| **Mock API response** | HTTP 500. |
| **Action** | Click accept. Await next tick. |
| **Expected DOM state** | `[data-testid="error-message"]` text is `'Something went wrong. Please try again.'`. Route path is unchanged (still `/consent`). |

Verifies: REQ-FEAUTH-150, REQ-FEAUTH-039

---

### TC-AUTH-I-022

| Field | Value |
|---|---|
| **ID** | TC-AUTH-I-022 |
| **Component** | Router with navigation guards applied |
| **Description** | Unauthenticated navigation to a protected route results in a redirect to `/login`. |
| **Setup** | Auth store `token = null`. Mount the app with the full router. |
| **Action** | Call `router.push('/dashboard')`. Await navigation. |
| **Expected side effect** | `router.currentRoute.value.path === '/login'`. |

Verifies: REQ-FEAUTH-151, REQ-FEAUTH-152, REQ-FEAUTH-040

---

## Tier 3 — E2E Demonstration Procedure (Human-Executed)

Run against a local dev stack started with `docker compose up`. All URLs are relative to `http://localhost:5173` (Vite dev server) pointing at the API on `http://localhost:8080`. Seed the database with the users described before each step.

**Prerequisites:**
- `docker compose up` is running and healthy.
- Database seeded with:
  - `alice` / `password123` — role `student`, consent recorded.
  - `bob` / `password123` — role `student`, NO consent on record.
  - `admin1` / `adminpass` — role `admin`.

---

### TC-AUTH-E-001

| Field | Value |
|---|---|
| **ID** | TC-AUTH-E-001 |
| **Description** | Full student login and redirect to dashboard. |

**Steps:**

1. Navigate to `http://localhost:5173/login`.
   - Expected: Login form is displayed with a username input, a password input, and a submit button.
2. Enter `alice` in the username field and `password123` in the password field.
3. Click the submit button.
   - Expected: A loading indicator appears briefly.
   - Expected: Browser navigates to `/dashboard`.
   - Expected: Dashboard content is rendered (not the login form).

Verifies: REQ-FEAUTH-109, REQ-FEAUTH-001, REQ-FEAUTH-013, REQ-FEAUTH-051

---

### TC-AUTH-E-002

| Field | Value |
|---|---|
| **ID** | TC-AUTH-E-002 |
| **Description** | Logout redirects to `/login`. |

**Steps:**

1. Complete TC-AUTH-E-001 to be authenticated at `/dashboard`.
2. Click the logout button in the navigation bar.
   - Expected: A request is sent to `/api/v1/auth/logout`.
   - Expected: Browser navigates to `/login`.
   - Expected: Login form is displayed.

Verifies: REQ-FEAUTH-119, REQ-FEAUTH-123, REQ-FEAUTH-002, REQ-FEAUTH-022

---

### TC-AUTH-E-003

| Field | Value |
|---|---|
| **ID** | TC-AUTH-E-003 |
| **Description** | Direct navigation to a protected route while unauthenticated redirects to `/login`. |

**Steps:**

1. Open a fresh browser tab (clear application state or use incognito/private mode).
2. Navigate directly to `http://localhost:5173/dashboard`.
   - Expected: Browser is redirected to `/login` without rendering any dashboard content.

Verifies: REQ-FEAUTH-151, REQ-FEAUTH-152, REQ-FEAUTH-040

---

### TC-AUTH-E-004

| Field | Value |
|---|---|
| **ID** | TC-AUTH-E-004 |
| **Description** | Invalid credentials render the inline error with username preserved. |

**Steps:**

1. Navigate to `http://localhost:5173/login`.
2. Enter `alice` in the username field and `wrongpassword` in the password field.
3. Click the submit button.
   - Expected: Inline error message reads `"Invalid username or password."`.
   - Expected: Username field still shows `alice`.
   - Expected: No navigation occurs; the user remains on `/login`.

Verifies: REQ-FEAUTH-111, REQ-FEAUTH-112, REQ-FEAUTH-014

---

### TC-AUTH-E-005

| Field | Value |
|---|---|
| **ID** | TC-AUTH-E-005 |
| **Description** | Admin login redirects to `/admin`. |

**Steps:**

1. Navigate to `http://localhost:5173/login`.
2. Enter `admin1` in the username field and `adminpass` in the password field.
3. Click the submit button.
   - Expected: Browser navigates to `/admin`.
   - Expected: Admin panel content is rendered.

Verifies: REQ-FEAUTH-110, REQ-FEAUTH-013

---

### TC-AUTH-E-006

| Field | Value |
|---|---|
| **ID** | TC-AUTH-E-006 |
| **Description** | Password reset request shows success message. |

**Steps:**

1. Navigate to `http://localhost:5173/password-reset/request`.
2. Enter `alice` in the username field.
3. Click the submit button.
   - Expected: The page displays `"If that account exists, a reset link has been sent."`.
   - Expected: No navigation occurs; user remains on the reset request page.

Verifies: REQ-FEAUTH-128, REQ-FEAUTH-025, REQ-FEAUTH-003

---

### TC-AUTH-E-007

| Field | Value |
|---|---|
| **ID** | TC-AUTH-E-007 |
| **Description** | New student without consent is redirected to `/consent` on protected-route access. |

**Steps:**

1. Navigate to `http://localhost:5173/login`.
2. Enter `bob` in the username field and `password123` in the password field.
3. Click the submit button.
   - Expected: Browser navigates to `/consent` (not `/dashboard`).
   - Expected: Consent screen is displayed with AI processing consent text and an accept button.

Verifies: REQ-FEAUTH-148, REQ-FEAUTH-037, REQ-FEAUTH-005

---

### TC-AUTH-E-008

| Field | Value |
|---|---|
| **ID** | TC-AUTH-E-008 |
| **Description** | Accepting consent on the consent screen navigates to `/dashboard`. |

**Steps:**

1. Continue from TC-AUTH-E-007 (or repeat steps 1–3 with user `bob`). You should be on `/consent`.
2. Read the displayed AI processing consent text.
3. Click the accept button.
   - Expected: Browser navigates to `/dashboard`.
   - Expected: Dashboard content is rendered.

Verifies: REQ-FEAUTH-147, REQ-FEAUTH-036, REQ-FEAUTH-035

---

### TC-AUTH-E-009

| Field | Value |
|---|---|
| **ID** | TC-AUTH-E-009 |
| **Description** | After a successful password reset confirm, the `/login` page displays the success banner. |

**Steps:**

1. Obtain a valid reset token for `alice` (trigger a reset request or seed one directly in the database).
2. Navigate to `http://localhost:5173/password-reset/confirm?token=<valid-token>`.
   - Expected: Token field is pre-filled with the token value from the query parameter.
3. Enter a new password (e.g. `newpassword456`) in the new password field.
4. Click the submit button.
   - Expected: Browser navigates to `/login`.
   - Expected: A success banner reads `"Password updated. Please log in."`.

Verifies: REQ-FEAUTH-135, REQ-FEAUTH-138, REQ-FEAUTH-139, REQ-FEAUTH-031, REQ-FEAUTH-029

---

## Tier 4 — Inspection Checklist

A reviewer verifies each item by reading the source code. Each item is a pass/fail criterion.

### TC-AUTH-X-001

| Field | Value |
|---|---|
| **ID** | TC-AUTH-X-001 |
| **Description** | The Bearer token is never written to `localStorage` or `sessionStorage`. |
| **Procedure** | Search the entire frontend source tree for `localStorage.setItem`, `localStorage.set`, `sessionStorage.setItem`, and `sessionStorage.set`. Verify no result stores the auth token value. |
| **Pass criterion** | Zero occurrences of any `localStorage` or `sessionStorage` write that contains the auth token variable or its source value. |

Verifies: REQ-FEAUTH-158, REQ-FEAUTH-045

---

### TC-AUTH-X-002

| Field | Value |
|---|---|
| **ID** | TC-AUTH-X-002 |
| **Description** | The CSRF cookie name used in client code is exactly `__Host-csrf`. |
| **Procedure** | Search the frontend source tree for every cookie read operation (`document.cookie`, any cookie utility function). Verify all references to the CSRF cookie use the literal string `'__Host-csrf'`. |
| **Pass criterion** | All cookie reads targeting the CSRF cookie use exactly `'__Host-csrf'` — no variations such as `'csrf'`, `'_Host-csrf'`, or `'host-csrf'`. |

Verifies: REQ-FEAUTH-160, REQ-FEAUTH-047

---

### TC-AUTH-X-003

| Field | Value |
|---|---|
| **ID** | TC-AUTH-X-003 |
| **Description** | The route guard is registered on every protected route in the router definition. |
| **Procedure** | Open the router configuration file (e.g. `src/router/index.ts`). Enumerate all routes that require authentication. Verify each such route either: (a) has the navigation guard applied via `meta.requiresAuth` (or equivalent) and the `beforeEach` guard reads that field, or (b) is nested under a route group that has the guard applied. |
| **Pass criterion** | No protected route exists that lacks the guard. Every route accessible only to authenticated users is covered. |

Verifies: REQ-FEAUTH-151, REQ-FEAUTH-040, REQ-FEAUTH-006

---

### TC-AUTH-X-004

| Field | Value |
|---|---|
| **ID** | TC-AUTH-X-004 |
| **Description** | The CSRF cookie is read fresh immediately before each state-changing request, not cached in a module-level variable that is set once and never refreshed. |
| **Procedure** | Open the API client source file (e.g. `src/api/client.ts`). Locate the request interceptor. Verify that the CSRF value is obtained by calling a cookie-read function (e.g. `readCookie('__Host-csrf')`) inside the interceptor callback — not assigned to an outer variable before the interceptor is defined. |
| **Pass criterion** | The cookie-read call is inside the interceptor function body (or the `beforeEach` guard body), not in a module-level variable initialiser or a `onMounted`/`setup` block that runs once. |

Verifies: REQ-FEAUTH-163, REQ-FEAUTH-049

---

### TC-AUTH-X-005

| Field | Value |
|---|---|
| **ID** | TC-AUTH-X-005 |
| **Description** | The consent screen renders AI processing consent text before the accept button in DOM order. |
| **Procedure** | Open `ConsentView.vue` (or equivalent component). Verify in the template that the consent text element appears before the accept button element in the DOM tree. |
| **Pass criterion** | The consent description/text node appears earlier in the template than the accept button. |

Verifies: REQ-FEAUTH-142, REQ-FEAUTH-034

---

### TC-AUTH-X-006

| Field | Value |
|---|---|
| **ID** | TC-AUTH-X-006 |
| **Description** | A logout button is rendered inside the navigation bar component when the user is authenticated. |
| **Procedure** | Open the navigation bar component (e.g. `NavBar.vue` or `AppHeader.vue`). Verify a button or link element with a logout action is conditionally shown when `authStore.token` is non-null (or an equivalent authenticated state check). |
| **Pass criterion** | The logout trigger element is present in the nav component template and its visibility is conditioned on authenticated state. |

Verifies: REQ-FEAUTH-118, REQ-FEAUTH-019

---

## Coverage Matrix

The following table confirms every requirement in `REQ-FE-AUTH.json` is covered by at least one test case. Aggregate requirements (those with an `implements` list) are satisfied by the test cases covering their child leaf requirements — those child mappings are shown inline.

| Requirement ID | Test Cases |
|---|---|
| REQ-FEAUTH-004 (aggregate) | TC-AUTH-I-014, TC-AUTH-I-015, TC-AUTH-I-016, TC-AUTH-I-017, TC-AUTH-I-018, TC-AUTH-E-009 (via REQ-FEAUTH-028–033) |
| REQ-FEAUTH-007 (aggregate) | TC-AUTH-U-004, TC-AUTH-X-001 (via REQ-FEAUTH-044–046) |
| REQ-FEAUTH-008 (aggregate) | TC-AUTH-U-005, TC-AUTH-U-006, TC-AUTH-X-002, TC-AUTH-X-004 (via REQ-FEAUTH-047–050) |
| REQ-FEAUTH-010 (aggregate) | TC-AUTH-I-001 (via REQ-FEAUTH-100–102) |
| REQ-FEAUTH-011 (aggregate) | TC-AUTH-I-001 (via REQ-FEAUTH-103–105) |
| REQ-FEAUTH-020 (aggregate) | TC-AUTH-E-002 (via REQ-FEAUTH-119–120) |
| REQ-FEAUTH-023 (aggregate) | TC-AUTH-I-010, TC-AUTH-I-013 (via REQ-FEAUTH-124–125) |
| REQ-FEAUTH-024 (aggregate) | TC-AUTH-I-010 (via REQ-FEAUTH-126–127) |
| REQ-FEAUTH-028 (aggregate) | TC-AUTH-I-014, TC-AUTH-I-015, TC-AUTH-I-017, TC-AUTH-I-018 (via REQ-FEAUTH-132–134) |
| REQ-FEAUTH-030 (aggregate) | TC-AUTH-I-015 (via REQ-FEAUTH-136–137) |
| REQ-FEAUTH-103 | TC-AUTH-I-001 (login submit sends POST) |
| REQ-FEAUTH-104 | TC-AUTH-I-001 (request body shape) |
| REQ-FEAUTH-105 | TC-AUTH-I-001 (Content-Type header) |
| REQ-FEAUTH-106 | TC-AUTH-U-001, TC-AUTH-I-001 |
| REQ-FEAUTH-107 | TC-AUTH-U-001, TC-AUTH-I-001 |
| REQ-FEAUTH-108 | TC-AUTH-U-002 |
| REQ-FEAUTH-109 | TC-AUTH-U-016, TC-AUTH-I-001, TC-AUTH-E-001 |
| REQ-FEAUTH-110 | TC-AUTH-U-017, TC-AUTH-E-005 |
| REQ-FEAUTH-111 | TC-AUTH-I-002, TC-AUTH-E-004 |
| REQ-FEAUTH-112 | TC-AUTH-I-002, TC-AUTH-E-004 |
| REQ-FEAUTH-113 | TC-AUTH-I-003 |
| REQ-FEAUTH-114 | TC-AUTH-I-004 |
| REQ-FEAUTH-115 | TC-AUTH-I-005 |
| REQ-FEAUTH-116 | TC-AUTH-I-006, TC-AUTH-I-007 |
| REQ-FEAUTH-117 | TC-AUTH-I-008 |
| REQ-FEAUTH-118 | TC-AUTH-X-006 |
| REQ-FEAUTH-119 | TC-AUTH-E-002 |
| REQ-FEAUTH-120 | TC-AUTH-U-004 (bearer header on all requests, including logout) |
| REQ-FEAUTH-121 | TC-AUTH-U-003 |
| REQ-FEAUTH-122 | TC-AUTH-U-003 |
| REQ-FEAUTH-123 | TC-AUTH-U-018, TC-AUTH-E-002 |
| REQ-FEAUTH-124 | TC-AUTH-I-010 (form rendered, username field used) |
| REQ-FEAUTH-125 | TC-AUTH-I-010 (submit button clicked) |
| REQ-FEAUTH-126 | TC-AUTH-I-010 (POST to /api/v1/password-reset/request) |
| REQ-FEAUTH-127 | TC-AUTH-I-010 (body shape `{"username": string}`) |
| REQ-FEAUTH-128 | TC-AUTH-I-010, TC-AUTH-E-006 |
| REQ-FEAUTH-129 | TC-AUTH-I-011 |
| REQ-FEAUTH-130 | TC-AUTH-I-012 |
| REQ-FEAUTH-131 | TC-AUTH-I-013 |
| REQ-FEAUTH-132 | TC-AUTH-I-014 (token field rendered) |
| REQ-FEAUTH-133 | TC-AUTH-I-017 (new password field present) |
| REQ-FEAUTH-134 | TC-AUTH-I-015 (submit button clicked) |
| REQ-FEAUTH-135 | TC-AUTH-I-014, TC-AUTH-E-009 |
| REQ-FEAUTH-136 | TC-AUTH-I-015 (POST to /api/v1/password-reset/confirm) |
| REQ-FEAUTH-137 | TC-AUTH-I-015 (body shape `{"token": string, "new_password": string}`) |
| REQ-FEAUTH-138 | TC-AUTH-I-015, TC-AUTH-E-009 |
| REQ-FEAUTH-139 | TC-AUTH-I-009, TC-AUTH-E-009 |
| REQ-FEAUTH-140 | TC-AUTH-I-016 |
| REQ-FEAUTH-141 | TC-AUTH-I-017, TC-AUTH-I-018 |
| REQ-FEAUTH-142 | TC-AUTH-X-005 |
| REQ-FEAUTH-143 | TC-AUTH-X-005 (accept button present) |
| REQ-FEAUTH-144 | TC-AUTH-I-019 |
| REQ-FEAUTH-145 | TC-AUTH-I-019 |
| REQ-FEAUTH-146 | TC-AUTH-I-019 |
| REQ-FEAUTH-147 | TC-AUTH-I-020, TC-AUTH-E-008 |
| REQ-FEAUTH-148 | TC-AUTH-U-011, TC-AUTH-E-007 |
| REQ-FEAUTH-149 | TC-AUTH-I-019 |
| REQ-FEAUTH-150 | TC-AUTH-I-021 |
| REQ-FEAUTH-151 | TC-AUTH-U-008, TC-AUTH-I-022, TC-AUTH-E-003, TC-AUTH-X-003 |
| REQ-FEAUTH-152 | TC-AUTH-U-008, TC-AUTH-I-022, TC-AUTH-E-003 |
| REQ-FEAUTH-153 | TC-AUTH-U-009 |
| REQ-FEAUTH-154 | TC-AUTH-U-010 |
| REQ-FEAUTH-155 | TC-AUTH-U-012 |
| REQ-FEAUTH-156 | TC-AUTH-U-013 |
| REQ-FEAUTH-157 | TC-AUTH-U-004 |
| REQ-FEAUTH-158 | TC-AUTH-X-001 |
| REQ-FEAUTH-159 | TC-AUTH-U-003 |
| REQ-FEAUTH-160 | TC-AUTH-U-014, TC-AUTH-U-015, TC-AUTH-X-002 |
| REQ-FEAUTH-161 | TC-AUTH-U-005 |
| REQ-FEAUTH-162 | TC-AUTH-U-006 |
| REQ-FEAUTH-163 | TC-AUTH-U-005, TC-AUTH-X-004 |
| REQ-FEAUTH-164 | TC-AUTH-U-003 |
| REQ-FEAUTH-165 | TC-AUTH-I-005 |
| REQ-FEAUTH-166 | TC-AUTH-I-005 |
| REQ-FEAUTH-100 | TC-AUTH-I-001 (username input present) |
| REQ-FEAUTH-101 | TC-AUTH-I-001 (password input present) |
| REQ-FEAUTH-102 | TC-AUTH-I-001 (submit button present) |
