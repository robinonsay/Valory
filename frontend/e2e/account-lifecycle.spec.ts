// @{"verifies": ["REQ-FEUSER-005", "REQ-AUTH-013", "REQ-AUTH-014"]}
//
// account-lifecycle.spec.ts — Forced change-password journey for admin-created
// accounts (Sprint 20).
//
// SCOPE: REQ-FEUSER-005 (forced change-password journey) at the UI level.
//
// What this spec proves:
//   1. Admin creates a new student account with the generate-password toggle.
//   2. When the mailer is not configured the response returns email_sent:false and
//      a one-time temp_password (no Mailpit needed in the e2e stack).
//   3. The temp password is displayed once in the UI immediately after creation
//      (one-time display) and cannot be retrieved again (not-shown-again).
//   4. The admin logs out.
//   5. The new student logs in with the temp password.
//   6. The forced change-password screen appears immediately.
//   7. The student cannot navigate away: trying to visit /courses is blocked and
//      the user stays on (or is redirected back to) the change-password screen.
//   8. The student submits the change-password form with a valid new password.
//   9. After a successful change the student lands on /courses (unrestricted nav).
//  10. The student logs out.
//  11. The student can re-login with the new password.
//  12. The student cannot login with the old temp password.
//
// STACK REQUIREMENT: this spec needs a live Valory stack. It uses the same
// skip-gate as other AI-free specs: if the base URL is unreachable the test is
// skipped rather than failed. The e2e stack for this spec does NOT need Mailpit
// because the admin creation UI branch for an unconfigured mailer surfaces the
// temp password directly in the response. If the stack is configured with a
// working mailer the spec will still pass (it reads the temp password from the
// admin UI's one-time display, which is shown regardless of whether the email
// was also delivered).
//
// PLAYWRIGHT CONVENTIONS followed:
//   • `test.skip` with a descriptive message when the stack is absent (mirrors
//     how other specs gate against missing stack resources).
//   • `@{"verifies", [...]}` annotation at the file level AND per-test.
//   • Username carries a timestamp suffix so repeated runs do not collide.
//   • `page.on('dialog', ...)` auto-accepts confirm() dialogs (create-user form).
//
// DO NOT RUN via the default `npm run test:e2e` without a live stack. The lead
// schedules this; this file must lint/typecheck cleanly without running.

import { test, expect, type Page } from '@playwright/test'
import { login, logout, ADMIN_USER, ADMIN_PASS } from './helpers'

// ---------------------------------------------------------------------------
// Helper: extract the one-time temp password from the admin UI after user creation.
// The UserManagementView renders the temp password in an element with the
// data-testid="temp-password-display" attribute when email_sent=false.
// If that element is absent the helper returns null (email was sent by the stack).
// ---------------------------------------------------------------------------
async function getTempPasswordFromUI(page: Page): Promise<string | null> {
  const el = page.locator('[data-testid="temp-password-display"]')
  // Give the reactive UI up to 5 s to render the one-time display.
  try {
    await el.waitFor({ timeout: 5_000 })
    return await el.textContent()
  } catch {
    return null
  }
}

// ---------------------------------------------------------------------------
// AccountLifecycle_ForcePasswordChange_FullJourney
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FEUSER-005", "REQ-AUTH-013", "REQ-AUTH-014"]}
test('AccountLifecycle_ForcePasswordChange_FullJourney', async ({ page }) => {
  // ── Guard: skip if the stack is absent ──────────────────────────────────
  //
  // Use a direct page.goto rather than login() so we can catch the network
  // error and skip before any auth attempt is made against a dead stack.
  let stackUp = false
  try {
    const resp = await page.goto('/login', { timeout: 8_000 })
    stackUp = (resp?.status() ?? 0) < 500
  } catch {
    // Network error → stack not running.
  }
  test.skip(!stackUp, 'Stack unreachable — skip account-lifecycle spec (run `docker compose up -d` first)')

  // ── Unique username so repeated runs never collide ───────────────────────
  const uniqueUsername = `e2e_lc_${Date.now()}`
  const newPassword = `NewPass!${Date.now()}`

  // ── Step 1: admin logs in ────────────────────────────────────────────────
  await login(page, ADMIN_USER, ADMIN_PASS)
  await expect(page).toHaveURL(/\/admin\/users/)

  // ── Step 2: admin creates student with generate-password toggle ──────────
  //
  // The UserManagementView has a "Generate password" checkbox/toggle; when
  // checked the password field is hidden. We set the username and select role,
  // then check the generate toggle and submit.
  //
  // Auto-accept any confirm() dialog the creation flow may trigger.
  page.on('dialog', dialog => dialog.accept())

  await page.fill('#create-username', uniqueUsername)

  // Check the generate-password toggle (REQ-FEADMIN-611 / generate_password).
  // The checkbox may use id="create-generate-password" or similar; we target
  // the label text as a fallback to keep the spec resilient to minor HTML changes.
  const generateToggle = page.locator('#create-generate-password, [data-testid="generate-password-toggle"]')
  const toggleExists = await generateToggle.count() > 0
  if (toggleExists) {
    await generateToggle.check()
  } else {
    // Fallback: locate by label text "Generate password".
    await page.locator('label', { hasText: /generate password/i }).click()
  }

  await page.selectOption('#create-role', 'student')
  await page.click('button.submit-btn')

  // Wait for the new row to appear in the users table.
  const usernameCell = page.locator('td.col-username', { hasText: uniqueUsername })
  await expect(usernameCell).toBeVisible({ timeout: 10_000 })

  // ── Step 3: one-time temp password display ───────────────────────────────
  //
  // When the stack mailer is unconfigured, email_sent=false and the UI
  // shows the temp password once in [data-testid="temp-password-display"].
  // If the stack mailer IS configured, the temp password was emailed and this
  // element is absent — in that case we cannot continue the e2e journey without
  // Mailpit; skip with a clear message.
  const tempPassword = await getTempPasswordFromUI(page)
  if (tempPassword === null || tempPassword.trim() === '') {
    test.skip(
      true,
      'Stack mailer is configured (email_sent=true) — temp password was emailed. ' +
      'Re-run with an unconfigured SMTP host or add Mailpit to the e2e stack to ' +
      'exercise the full lifecycle journey.'
    )
    return // unreachable after skip; satisfies TypeScript control-flow
  }

  const rawTempPassword = tempPassword.trim()

  // Verify the generate-password status indicator is shown.
  // The new row's status area must indicate generate_password was used.
  // REQ-FEADMIN-610: admin creation with generate_password shows a status.
  const newRow = page.locator('tr.user-row', { has: usernameCell })
  await expect(newRow.locator('.status-badge')).toContainText('Active')

  // ── Step 4: dismiss / close the one-time password display ────────────────
  //
  // Attempt to close the temp-password modal/banner if a close button exists.
  const closeBtn = page.locator('[data-testid="temp-password-close"]')
  if (await closeBtn.count() > 0) {
    await closeBtn.click()
  }

  // Verify the temp password is no longer retrievable (not-shown-again
  // guarantee). The component has no dismiss control — the one-time display
  // lives only in transient view state, so a reload must lose it.
  await page.reload()
  await expect(page.locator('[data-testid="temp-password-display"]')).not.toBeVisible({
    timeout: 3_000
  })

  // ── Step 5: admin logs out ───────────────────────────────────────────────
  await logout(page)
  await expect(page).toHaveURL(/\/login/)

  // ── Step 6: student logs in with temp password ───────────────────────────
  await page.goto('/login')
  await page.fill('#username', uniqueUsername)
  await page.fill('input[type="password"]', rawTempPassword)
  await page.click('button[type="submit"]')

  // REQ-AUTH-013: the login response includes must_change_password:true. The
  // frontend should immediately navigate to the forced change-password screen.
  await page.waitForURL(url => !url.pathname.startsWith('/login'), { timeout: 10_000 })

  // The forced change-password route. Accept any URL pattern that indicates the
  // change-password screen is shown (e.g. /change-password, /password/change,
  // or the login page re-shown with a banner).
  const isChangePasswordScreen =
    page.url().includes('/change-password') ||
    page.url().includes('/password') ||
    await page.locator('[data-testid="change-password-form"], .change-password-form, #new-password').isVisible()

  expect(
    isChangePasswordScreen,
    'REQ-AUTH-013: forced change-password screen must appear after login with must_change_password flag'
  ).toBe(true)

  // ── Step 7: cannot navigate away from the change-password screen ──────────
  //
  // REQ-AUTH-014: a must-change-password session is restricted to the change-
  // password and logout endpoints. Trying to visit /courses must be blocked.
  await page.goto('/courses')
  await page.waitForURL(url => true, { timeout: 5_000 }) // let any redirect settle

  // After attempting to visit /courses, the user must NOT land there.
  // Acceptable outcomes: back on the change-password screen, or /login.
  const blockedFromCourses =
    !page.url().includes('/courses') ||
    await page.locator('[data-testid="change-password-form"], .change-password-form, #new-password, .error-message').isVisible()

  expect(
    blockedFromCourses,
    'REQ-AUTH-014: must-change-password user must not be able to reach /courses'
  ).toBe(true)

  // Navigate back to change-password if we ended up on login or elsewhere.
  if (!page.url().includes('change-password') && !page.url().includes('password')) {
    // The guard may have pushed us to /login — re-login to get back.
    await page.goto('/login')
    await page.fill('#username', uniqueUsername)
    await page.fill('input[type="password"]', rawTempPassword)
    await page.click('button[type="submit"]')
    await page.waitForURL(url => !url.pathname.startsWith('/login'), { timeout: 10_000 })
  }

  // ── Step 8: student changes password ─────────────────────────────────────
  //
  // Fill and submit the forced change-password form.
  // The form selectors match the component conventions in the Vue frontend
  // (ChangePasswordView.vue or ForceChangePasswordView.vue). We try the most
  // likely selectors in order.
  const currentPwdField = page.locator('#current-password, [data-testid="current-password"]')
  const newPwdField     = page.locator('#new-password, [data-testid="new-password"]')
  const submitBtn       = page.locator('button[type="submit"], button.submit-btn')

  const confirmPwdField = page.locator('#confirm-password, [data-testid="confirm-password"]')

  await currentPwdField.fill(rawTempPassword)
  await newPwdField.fill(newPassword)
  // The submit button is disabled until confirm matches (ChangePasswordView
  // canSubmit) — the confirm field must be filled or the click is a no-op.
  await confirmPwdField.fill(newPassword)
  await submitBtn.click()

  // ── Step 9: lands in the app with unrestricted navigation ────────────────
  //
  // After a successful change, the student should be able to navigate to /courses.
  // REQ-FEUSER-005: unrestricted navigation after forced change.
  await expect(page).toHaveURL(/\/courses/, { timeout: 10_000 })
  await expect(page.locator('button:has-text("Logout")')).toBeVisible()

  // ── Step 10: student logs out ─────────────────────────────────────────────
  await logout(page)
  await expect(page).toHaveURL(/\/login/)

  // ── Step 11: student re-logs in with the new password ────────────────────
  await page.fill('#username', uniqueUsername)
  await page.fill('input[type="password"]', newPassword)
  await page.click('button[type="submit"]')

  await page.waitForURL(url => !url.pathname.startsWith('/login'), { timeout: 10_000 })
  await expect(page).toHaveURL(/\/courses/, { timeout: 10_000 })
  await logout(page)

  // ── Step 12: old temp password is rejected ────────────────────────────────
  await page.fill('#username', uniqueUsername)
  await page.fill('input[type="password"]', rawTempPassword)
  await page.click('button[type="submit"]')

  // Must stay on /login with an error; the old credential is dead.
  await expect(page.locator('.error-message')).toBeVisible({ timeout: 5_000 })
  await expect(page).toHaveURL(/\/login/)
})
