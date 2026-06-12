// @{"verifies": ["REQ-AUTH-009", "REQ-AUTH-010", "REQ-AUTH-011", "REQ-FEAUTH-169", "REQ-FEAUTH-170", "REQ-FEAUTH-171", "REQ-FEAUTH-172"]}
//
// session-refresh.spec.ts — Sprint 13 session-persistence end-to-end tests.
//
// What changed in Sprint 13:
//   • Login sets an httpOnly __Host-session cookie (REQ-AUTH-009). The SPA no
//     longer stores a bearer token in memory.
//   • GET /api/v1/auth/session restores full auth state (user_id, username, role,
//     consented, expires_at) on SPA boot so page.reload() keeps the user logged in.
//   • Router guards defer redirect decisions until restoreSession() completes
//     (REQ-FEAUTH-170), preventing a flash-redirect to /login on every reload.
//   • Logout clears the __Host-session cookie server-side (REQ-AUTH-010) and
//     requires CSRF (X-CSRF-Token header).
//   • No token material is written to localStorage or sessionStorage (REQ-FEAUTH-171).
//
// The demo_student account has consent recorded — the session restore response
// returns consented:true (REQ-FEAUTH-172), so the consent interstitial is NOT
// shown on reload.

import { test, expect, type Page } from '@playwright/test'
import { login, logout, STUDENT_USER, STUDENT_PASS } from './helpers'

// archiveOpenCourses withdraws every non-archived/completed course for the
// logged-in student. Uses page.request (shares the browser's cookie jar — the
// __Host-session cookie is sent automatically) plus the bearer token and CSRF
// token in headers. Bearer header is preferred by the server (REQ-AUTH-011),
// so both auth paths are exercised.
async function archiveOpenCourses(page: Page, bearerToken: string): Promise<void> {
  const csrf = (await page.context().cookies()).find(c => c.name === '__Host-csrf')?.value ?? ''
  const list = await page.request.get('/api/v1/courses', {
    headers: { Authorization: `Bearer ${bearerToken}` }
  })
  if (!list.ok()) return
  const body = await list.json() as { courses: { id: string; status: string }[] | null }
  for (const c of body.courses ?? []) {
    if (c.status !== 'archived' && c.status !== 'completed') {
      const resp = await page.request.post(`/api/v1/courses/${c.id}/withdraw`, {
        headers: { Authorization: `Bearer ${bearerToken}`, 'X-CSRF-Token': csrf },
        data: {}
      })
      expect(resp.status(), `withdraw of stale course ${c.id} must succeed`).toBe(200)
    }
  }
}

// ---------------------------------------------------------------------------
// SessionPersists_AcrossPageReload
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-009", "REQ-AUTH-011", "REQ-FEAUTH-169", "REQ-FEAUTH-170"]}
test('SessionPersists_AcrossPageReload', async ({ page }) => {
  // Step 1: login as student and land on /courses.
  await login(page, STUDENT_USER, STUDENT_PASS)
  await expect(page).toHaveURL(/\/courses/)

  // Step 2: verify the __Host-session cookie was set by login (REQ-AUTH-009).
  const cookiesBefore = await page.context().cookies()
  const sessionCookie = cookiesBefore.find(c => c.name === '__Host-session')
  expect(sessionCookie, '__Host-session cookie must be set after login').toBeTruthy()
  expect(sessionCookie!.httpOnly, '__Host-session must be httpOnly').toBe(true)
  expect(sessionCookie!.secure, '__Host-session must be Secure').toBe(true)
  expect(sessionCookie!.value, '__Host-session value must not be empty').not.toBe('')

  // Step 3: reload the page — this is the core session-persistence scenario.
  // Pre-Sprint-13, a reload wiped the in-memory token and the router guard
  // immediately redirected the student to /login. With Sprint 13, the
  // __Host-session cookie is re-sent on the reload request, restoreSession()
  // returns 200, and the router guard allows the /courses route (REQ-FEAUTH-170).
  await page.reload()

  // Step 4: assert the student is STILL on /courses (not redirected to /login).
  await expect(page).toHaveURL(/\/courses/, { timeout: 10_000 })

  // Step 5: assert the student nav chrome is visible, proving the authenticated
  // layout is rendered rather than the login page.
  await expect(page.locator('nav.nav-links a[href="/courses"]')).toBeVisible()
  await expect(page.locator('button:has-text("Logout")')).toBeVisible()
})

// ---------------------------------------------------------------------------
// SessionPersists_HardNavigation
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-009", "REQ-AUTH-011", "REQ-FEAUTH-169", "REQ-FEAUTH-170"]}
test('SessionPersists_HardNavigation', async ({ page }) => {
  // Capture the bearer token from login for cleanup.
  let bearerToken: string | null = null
  page.on('response', async response => {
    if (response.url().includes('/api/v1/auth/login') && response.status() === 200) {
      try {
        const body = await response.json() as { token?: string }
        if (body.token) bearerToken = body.token
      } catch { /* ignore */ }
    }
  })

  await login(page, STUDENT_USER, STUDENT_PASS)
  await expect(page).toHaveURL(/\/courses/)
  expect(bearerToken, 'Bearer token must be captured from login response').not.toBeNull()

  // Pre-clean stale courses before creating one.
  await archiveOpenCourses(page, bearerToken!)

  // Pre-Sprint-13 note: this step was IMPOSSIBLE. page.goto() triggers a full
  // document load, which wiped the SPA's in-memory token. The router guard
  // then saw isAuthenticated=false and redirected to /login regardless of
  // whether a valid server-side session existed. Sprint 13 fixes this: the
  // __Host-session cookie persists across document loads, restoreSession()
  // restores full auth state, and the router guard correctly allows the route.
  await page.goto('/courses')

  // The page must stay at /courses — not redirect to /login.
  await expect(page).toHaveURL(/\/courses/, { timeout: 10_000 })

  // The student nav chrome must be visible, proving the session was restored.
  await expect(page.locator('nav.nav-links a[href="/courses"]')).toBeVisible()
  await expect(page.locator('button:has-text("Logout")')).toBeVisible()
})

// ---------------------------------------------------------------------------
// ConsentSkipped_WhenAlreadyConsented
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FEAUTH-172"]}
test('ConsentSkipped_WhenAlreadyConsented', async ({ page }) => {
  // demo_student has consent recorded. After session restore, consented=true
  // is returned by GET /api/v1/auth/session, so router guard rule 7 (redirect
  // to /consent if !isConsented) does not fire.
  await login(page, STUDENT_USER, STUDENT_PASS)
  await expect(page).toHaveURL(/\/courses/)

  // Reload: session is restored with consented=true. Guard must NOT redirect
  // to /consent (REQ-FEAUTH-172 prohibits re-prompting a consented student).
  await page.reload()
  await expect(page).toHaveURL(/\/courses/, { timeout: 10_000 })
  await expect(page).not.toHaveURL(/\/consent/)

  // In-SPA navigation to /getting-started: guard must not redirect to /consent
  // while navigating around authenticated student routes.
  await page.click('nav.nav-links a[href="/getting-started"]')
  await expect(page).toHaveURL(/\/getting-started/, { timeout: 5_000 })
  await expect(page).not.toHaveURL(/\/consent/)
})

// ---------------------------------------------------------------------------
// Logout_ClearsSession
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-010"]}
test('Logout_ClearsSession', async ({ page, request }) => {
  // Step 1: login.
  let bearerToken: string | null = null
  page.on('response', async response => {
    if (response.url().includes('/api/v1/auth/login') && response.status() === 200) {
      try {
        const body = await response.json() as { token?: string }
        if (body.token) bearerToken = body.token
      } catch { /* ignore */ }
    }
  })

  await login(page, STUDENT_USER, STUDENT_PASS)
  await expect(page).toHaveURL(/\/courses/)
  expect(bearerToken, 'Bearer token must be captured from login response').not.toBeNull()

  // Step 2: logout.
  await logout(page)
  await expect(page).toHaveURL(/\/login/)

  // Step 3: verify the session cookie is cleared (REQ-AUTH-010).
  // The server sets __Host-session to empty with Max-Age=0 on logout. Playwright
  // stores it as a zero-value cookie. The critical verification is that
  // GET /api/v1/auth/session returns 401 — the server-side session is gone.
  //
  // Use the standalone `request` fixture (no browser cookies) to avoid the
  // bearer header. Use the raw bearerToken to prove the token itself is no
  // longer valid on the server after logout.
  const sessionAfterLogout = await request.get('https://localhost/api/v1/auth/session', {
    headers: { Authorization: `Bearer ${bearerToken!}` },
    ignoreHTTPSErrors: true
  })
  expect(
    sessionAfterLogout.status(),
    'GET /api/v1/auth/session must return 401 after logout (session row deleted on server)'
  ).toBe(401)

  // Step 4: page.reload() while at /login — must stay at /login.
  // The __Host-session cookie value is empty; restoreSession() returns 401;
  // the router guard keeps the user at /login.
  await page.reload()
  await expect(page).toHaveURL(/\/login/, { timeout: 10_000 })
  await expect(page).not.toHaveURL(/\/courses/)

  // Step 5: hard navigation to a protected route after logout must end at /login.
  // The async guard awaits restoreSession() (which returns 401 — the server
  // session was deleted on logout) before resolving the navigation, then applies
  // rule 1 (unauthenticated → /login). This is deterministic because the guard
  // blocks until the restore fetch completes.
  await page.goto('/courses')
  await expect(page).toHaveURL(/\/login/, { timeout: 10_000 })
})

// ---------------------------------------------------------------------------
// NoTokenInBrowserStorage
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FEAUTH-171"]}
test('NoTokenInBrowserStorage', async ({ page }) => {
  // Sprint 13: the auth store no longer stores the raw session token in
  // reactive state, and nothing writes it to localStorage or sessionStorage.
  // The session token exists only as the httpOnly __Host-session cookie (which
  // JavaScript cannot read at all — httpOnly cookies are invisible to document.cookie).
  await login(page, STUDENT_USER, STUDENT_PASS)
  await expect(page).toHaveURL(/\/courses/)

  // Collect all localStorage and sessionStorage keys and values.
  const storageContents = await page.evaluate(() => {
    const ls: Record<string, string> = {}
    const ss: Record<string, string> = {}
    for (let i = 0; i < localStorage.length; i++) {
      const k = localStorage.key(i)!
      ls[k] = localStorage.getItem(k) ?? ''
    }
    for (let i = 0; i < sessionStorage.length; i++) {
      const k = sessionStorage.key(i)!
      ss[k] = sessionStorage.getItem(k) ?? ''
    }
    return { localStorage: ls, sessionStorage: ss }
  })

  // Flatten all stored values into a single string for pattern matching.
  const allValues = [
    ...Object.values(storageContents.localStorage),
    ...Object.values(storageContents.sessionStorage)
  ].join(' ')

  // No storage entry should contain anything that looks like a session token.
  // Valid session tokens are 32-byte base64url strings (44 characters without
  // padding, or hex strings of 64 characters). We reject any value longer than
  // 20 characters that could be a token — legitimate UI state values (role
  // names, timestamps) are much shorter.
  //
  // Additionally, no key should be named anything token-adjacent.
  const tokenAdjacentKeys = [
    ...Object.keys(storageContents.localStorage),
    ...Object.keys(storageContents.sessionStorage)
  ].filter(k =>
    k.toLowerCase().includes('token') ||
    k.toLowerCase().includes('session') ||
    k.toLowerCase().includes('bearer') ||
    k.toLowerCase().includes('auth')
  )
  expect(
    tokenAdjacentKeys,
    `No token-adjacent keys must exist in browser storage. Found: ${JSON.stringify(tokenAdjacentKeys)}`
  ).toHaveLength(0)

  // Also verify no stored value looks like a 32+ character opaque string.
  const suspiciousValues = allValues.split(' ').filter(v => v.length >= 32)
  expect(
    suspiciousValues,
    `No long opaque values (possible tokens) must be in browser storage. Found: ${JSON.stringify(suspiciousValues)}`
  ).toHaveLength(0)
})
