// Shared helpers for the Valory e2e suite.
//
// login() performs a full browser login and handles the one-time consent
// interstitial that appears for new accounts before they reach their role home.
// logout() clicks the Logout button and waits for the /login redirect.

import { type Page, expect } from '@playwright/test'

/**
 * Demo account credentials with environment-variable overrides.
 * E2E_ADMIN_USER / E2E_ADMIN_PASS override the admin credentials.
 * E2E_STUDENT_USER / E2E_STUDENT_PASS override the student credentials.
 */
export const ADMIN_USER = process.env.E2E_ADMIN_USER ?? 'admin'
export const ADMIN_PASS = process.env.E2E_ADMIN_PASS ?? 'ValoryDemo!2026'
export const STUDENT_USER = process.env.E2E_STUDENT_USER ?? 'demo_student'
export const STUDENT_PASS = process.env.E2E_STUDENT_PASS ?? 'StudentDemo!2026'

/**
 * login navigates to /login, fills the credentials form and submits.
 * If the server redirects to /consent (first-login agreement), the helper
 * clicks "I agree" automatically before returning to the caller.
 * The caller should assert the final destination URL themselves.
 */
export async function login(page: Page, username: string, password: string): Promise<void> {
  await page.goto('/login')

  // Fill the login form using the exact selectors present in LoginView.vue.
  await page.fill('#username', username)
  await page.fill('input[type="password"]', password)
  await page.click('button[type="submit"]')

  // Wait for navigation away from /login (either to /consent or role home).
  await page.waitForURL(url => !url.pathname.startsWith('/login'), { timeout: 10_000 })

  // Handle the one-time consent interstitial.  The button text in
  // ConsentView.vue is "I agree".
  if (page.url().includes('/consent')) {
    await page.click('button:has-text("I agree")')
    // After consent the app pushes /courses.
    await page.waitForURL(url => !url.pathname.startsWith('/consent'), { timeout: 10_000 })
  }
}

/**
 * logout clicks the Logout button (present in both StudentLayout and
 * AdminLayout) and waits for the redirect to /login.
 */
export async function logout(page: Page): Promise<void> {
  // Both layouts render a single "Logout" button.
  await page.click('button:has-text("Logout")')
  await page.waitForURL('**/login', { timeout: 10_000 })
  await expect(page).toHaveURL(/\/login/)
}
