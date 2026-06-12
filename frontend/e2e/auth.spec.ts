// @{"verifies": ["REQ-FEAUTH-010", "REQ-FEAUTH-011", "REQ-FEAUTH-012", "REQ-FEAUTH-019", "REQ-FEAUTH-020", "REQ-FEAUTH-100", "REQ-FEAUTH-101", "REQ-FEAUTH-102", "REQ-FEAUTH-110", "REQ-FEAUTH-115", "REQ-FEAUTH-118", "REQ-FEAUTH-119", "REQ-FEAUTH-120", "REQ-FEAUTH-155", "REQ-FEAUTH-156"]}
//
// auth.spec.ts — Authentication flows:
//   • Admin login lands on /admin/users
//   • Student login lands on /courses
//   • Wrong password shows "Invalid credentials" and stays on /login
//   • Logout (both roles) redirects to /login
//
// Sprint 13 note: the pre-Sprint-13 tests also verified that after logout,
// page.goto('/protected') redirected to /login. That assertion proved the
// in-memory bearer token was cleared. With Sprint 13 session persistence,
// auth state is cookie-based. The async router guard awaits restoreSession()
// before deciding — there is no longer any race between the guard and the
// restore fetch. Post-logout hard-navigation redirect and cookie-clearance
// verification are covered in session-refresh.spec.ts (Logout_ClearsSession).

import { test, expect } from '@playwright/test'
import { login, logout, ADMIN_USER, ADMIN_PASS, STUDENT_USER, STUDENT_PASS } from './helpers'

// ---------------------------------------------------------------------------
// Admin login
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FEAUTH-010", "REQ-FEAUTH-100", "REQ-FEAUTH-101", "REQ-FEAUTH-102"]}
test('AdminLogin_ValidCredentials_LandsOnAdminUsers', async ({ page }) => {
  await login(page, ADMIN_USER, ADMIN_PASS)
  await expect(page).toHaveURL(/\/admin\/users/)
})

// ---------------------------------------------------------------------------
// Student login
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FEAUTH-011", "REQ-FEAUTH-110", "REQ-FEAUTH-115"]}
test('StudentLogin_ValidCredentials_LandsOnCourses', async ({ page }) => {
  await login(page, STUDENT_USER, STUDENT_PASS)
  await expect(page).toHaveURL(/\/courses/)
})

// ---------------------------------------------------------------------------
// Wrong password — stays on /login and shows error
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FEAUTH-012", "REQ-FEAUTH-120"]}
test('Login_WrongPassword_ShowsInvalidCredentialsAndStaysOnLogin', async ({ page }) => {
  await page.goto('/login')
  await page.fill('#username', ADMIN_USER)
  await page.fill('input[type="password"]', 'definitely-wrong-password')
  await page.click('button[type="submit"]')

  // The error div in LoginView.vue carries class "error-message" and text
  // "Invalid credentials" on a 401 response.
  await expect(page.locator('.error-message')).toContainText('Invalid credentials')
  await expect(page).toHaveURL(/\/login/)
})

// ---------------------------------------------------------------------------
// Admin logout — redirects to /login; protected route bounces back
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FEAUTH-019", "REQ-FEAUTH-020", "REQ-FEAUTH-118", "REQ-FEAUTH-119", "REQ-FEAUTH-155", "REQ-FEAUTH-156"]}
test('AdminLogout_AfterLogin_RedirectsToLogin', async ({ page }) => {
  await login(page, ADMIN_USER, ADMIN_PASS)
  await expect(page).toHaveURL(/\/admin\/users/)

  await logout(page)
  // Logout must redirect to /login.  Sprint 13: session cookie is cleared
  // server-side (REQ-AUTH-010); the client-side state is also cleared
  // (REQ-FEAUTH-155, REQ-FEAUTH-156).  Session-cookie clearance and
  // post-logout page.goto() redirect are verified in session-refresh.spec.ts.
  await expect(page).toHaveURL(/\/login/)
})

// @{"verifies": ["REQ-FEAUTH-019", "REQ-FEAUTH-020", "REQ-FEAUTH-118", "REQ-FEAUTH-119", "REQ-FEAUTH-155", "REQ-FEAUTH-156"]}
test('StudentLogout_AfterLogin_RedirectsToLogin', async ({ page }) => {
  await login(page, STUDENT_USER, STUDENT_PASS)
  await expect(page).toHaveURL(/\/courses/)

  await logout(page)
  // Logout must redirect to /login.  See note above; full session-clearing
  // verification is in session-refresh.spec.ts.
  await expect(page).toHaveURL(/\/login/)
})
