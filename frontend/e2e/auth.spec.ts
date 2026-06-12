// @{"verifies", ["REQ-FEAUTH-010", "REQ-FEAUTH-011", "REQ-FEAUTH-012", "REQ-FEAUTH-019", "REQ-FEAUTH-020", "REQ-FEAUTH-100", "REQ-FEAUTH-101", "REQ-FEAUTH-102", "REQ-FEAUTH-110", "REQ-FEAUTH-115", "REQ-FEAUTH-118", "REQ-FEAUTH-119", "REQ-FEAUTH-120", "REQ-FEAUTH-155", "REQ-FEAUTH-156"]}
//
// auth.spec.ts — Authentication flows:
//   • Admin login lands on /admin/users
//   • Student login lands on /courses
//   • Wrong password shows "Invalid credentials" and stays on /login
//   • Logout (both roles) redirects to /login
//   • After logout, navigating directly to a protected route redirects to /login
//     (proves the bearer token is truly cleared from memory-only SPA state)

import { test, expect } from '@playwright/test'
import { login, logout, ADMIN_USER, ADMIN_PASS, STUDENT_USER, STUDENT_PASS } from './helpers'

// ---------------------------------------------------------------------------
// Admin login
// ---------------------------------------------------------------------------

// @{"verifies", ["REQ-FEAUTH-010", "REQ-FEAUTH-100", "REQ-FEAUTH-101", "REQ-FEAUTH-102"]}
test('AdminLogin_ValidCredentials_LandsOnAdminUsers', async ({ page }) => {
  await login(page, ADMIN_USER, ADMIN_PASS)
  await expect(page).toHaveURL(/\/admin\/users/)
})

// ---------------------------------------------------------------------------
// Student login
// ---------------------------------------------------------------------------

// @{"verifies", ["REQ-FEAUTH-011", "REQ-FEAUTH-110", "REQ-FEAUTH-115"]}
test('StudentLogin_ValidCredentials_LandsOnCourses', async ({ page }) => {
  await login(page, STUDENT_USER, STUDENT_PASS)
  await expect(page).toHaveURL(/\/courses/)
})

// ---------------------------------------------------------------------------
// Wrong password — stays on /login and shows error
// ---------------------------------------------------------------------------

// @{"verifies", ["REQ-FEAUTH-012", "REQ-FEAUTH-120"]}
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

// @{"verifies", ["REQ-FEAUTH-019", "REQ-FEAUTH-020", "REQ-FEAUTH-118", "REQ-FEAUTH-119", "REQ-FEAUTH-155", "REQ-FEAUTH-156"]}
test('AdminLogout_AfterLogin_RedirectsToLoginAndProtectedRouteRedirectsToLogin', async ({ page }) => {
  await login(page, ADMIN_USER, ADMIN_PASS)
  await expect(page).toHaveURL(/\/admin\/users/)

  await logout(page)
  await expect(page).toHaveURL(/\/login/)

  // Navigating directly to a protected admin route must redirect back to
  // /login because the in-memory token has been cleared.
  await page.goto('/admin/users')
  await expect(page).toHaveURL(/\/login/)
})

// @{"verifies", ["REQ-FEAUTH-019", "REQ-FEAUTH-020", "REQ-FEAUTH-118", "REQ-FEAUTH-119", "REQ-FEAUTH-155", "REQ-FEAUTH-156"]}
test('StudentLogout_AfterLogin_RedirectsToLoginAndProtectedRouteRedirectsToLogin', async ({ page }) => {
  await login(page, STUDENT_USER, STUDENT_PASS)
  await expect(page).toHaveURL(/\/courses/)

  await logout(page)
  await expect(page).toHaveURL(/\/login/)

  // Navigating directly to the student home must redirect to /login because
  // the in-memory token has been cleared.
  await page.goto('/courses')
  await expect(page).toHaveURL(/\/login/)
})
