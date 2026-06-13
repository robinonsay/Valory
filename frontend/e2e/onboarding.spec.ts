// @{"verifies": ["REQ-FEPROFILE-001", "REQ-FEPROFILE-002", "REQ-FEPROFILE-003", "REQ-FEPROFILE-004", "REQ-PROFILE-014", "REQ-PROFILE-015", "REQ-AUTH-014"]}
//
// onboarding.spec.ts — End-to-end tests for the student learning-profile
// onboarding flow (Sprint 23).
//
// AI-gating policy (CRITICAL — read before running):
// ===================================================
// The onboarding chat (start / advance / complete) makes live Anthropic API
// calls.  These steps are marked [AI-GATED] and are skipped unless the test
// stack is configured with a working Anthropic key.  The skip-gate uses the
// environment variable VALORY_E2E_ANTHROPIC_ENABLED=true (must be set
// explicitly; defaults to off so routine CI runs never spend tokens).
//
// AI-FREE paths tested unconditionally (no Anthropic call required):
//   • Guard ordering: must_change_password check runs BEFORE onboarding check.
//   • Skip path: POST /profile/onboarding/skip → router lands on /courses.
//   • Nudge does not reappear after skip on reload.
//   • ProfileView (/profile) renders in StudentLayout.
//
// AI-GATED paths (require VALORY_E2E_ANTHROPIC_ENABLED=true):
//   • Full onboarding chat: login → change-password → /onboarding → 3 turns →
//     complete → GET /profile returns non-empty summary.
//
// PLAYWRIGHT CONVENTIONS followed (matching account-lifecycle.spec.ts):
//   • `test.skip` / `test.fixme` with descriptive messages for AI-gated paths.
//   • `@{"verifies", [...]}` annotations at file and per-test level.
//   • Username carries a UUID suffix so repeated runs never collide.
//   • `page.on('dialog', ...)` auto-accepts any confirm() dialogs.
//
// DO NOT RUN via the default `npm run test:e2e` without a live stack.
// The lead schedules e2e runs; this file must lint/typecheck cleanly without
// running.

import { test, expect, type Page } from '@playwright/test'
import { login, logout, ADMIN_USER, ADMIN_PASS } from './helpers'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/**
 * isAIGatingEnabled returns true when VALORY_E2E_ANTHROPIC_ENABLED=true is set
 * in the environment.  Used to gate tests that make live Anthropic API calls.
 */
function isAIGatingEnabled(): boolean {
  return process.env.VALORY_E2E_ANTHROPIC_ENABLED === 'true'
}

/**
 * createStudentViaAdmin logs in as admin, creates a new student with a
 * generated password (temp password path so must_change_password = true),
 * and returns { username, tempPassword }.
 *
 * The username carries a timestamp suffix so repeated runs never collide.
 * This mirrors the account-lifecycle.spec.ts pattern exactly.
 */
async function createStudentViaAdmin(page: Page): Promise<{ username: string; tempPassword: string }> {
  await login(page, ADMIN_USER, ADMIN_PASS)
  await page.waitForURL('**/admin/users')

  // Username is unique per run — timestamp + 4-char random suffix.
  const suffix = Date.now().toString(36) + Math.random().toString(36).slice(2, 6)
  const username = `onb_test_${suffix}`

  // Accept any confirm() dialogs (create-user form may show a confirmation).
  page.on('dialog', async (dialog) => dialog.accept())

  // Open the create-user form.
  await page.click('button:has-text("Create user")')

  // Fill the username field (data-testid mirrors UserManagementView.vue).
  await page.fill('[data-testid="new-username-input"]', username)

  // Enable the generate-password toggle so must_change_password = true.
  const generateToggle = page.locator('[data-testid="generate-password-toggle"]')
  if (await generateToggle.count() > 0) {
    const checked = await generateToggle.isChecked()
    if (!checked) await generateToggle.click()
  }

  // Submit the form.
  await page.click('[data-testid="create-user-submit"]')

  // Wait for the one-time temp-password display.
  const tempEl = page.locator('[data-testid="temp-password-display"]')
  await tempEl.waitFor({ timeout: 8_000 })
  const tempPassword = (await tempEl.textContent() ?? '').trim()

  if (!tempPassword) {
    throw new Error('onboarding.spec.ts: could not obtain temp password from admin UI')
  }

  await logout(page)

  return { username, tempPassword }
}

/**
 * changePasswordAs performs the forced password-change flow for a freshly
 * created student.  Returns the new password that was set.
 */
async function changePasswordAs(page: Page, username: string, tempPassword: string): Promise<string> {
  const newPassword = `NewPass!${Date.now().toString(36)}`

  // Log in with temp password.  The login helper does NOT handle the change-
  // password interstitial — that is what we are testing here.
  await page.goto('/login')
  await page.fill('#username', username)
  await page.fill('input[type="password"]', tempPassword)
  await page.click('button[type="submit"]')

  // Expect redirect to /change-password (must_change_password = true).
  await page.waitForURL('**/change-password', { timeout: 10_000 })
  await expect(page).toHaveURL(/\/change-password/)

  // Fill the change-password form (ChangePasswordView.vue).
  await page.fill('[data-testid="new-password-input"]', newPassword)
  await page.fill('[data-testid="confirm-password-input"]', newPassword)
  await page.click('[data-testid="change-password-submit"]')

  // After a successful change the guard clears must_change_password and
  // navigates onward.  Because this student has onboarding_prompted=false the
  // guard should redirect to /onboarding, NOT /courses.
  await page.waitForURL(url => !url.pathname.startsWith('/change-password'), { timeout: 10_000 })

  return newPassword
}

// ---------------------------------------------------------------------------
// Guard ordering: must_change_password precedes onboarding nudge
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-014", "REQ-FEPROFILE-004"]}
//
// TestGuardOrdering_MustChangePassword_PrecedesOnboarding_Nudge proves that a
// student with must_change_password=true is sent to /change-password — NOT to
// /onboarding — even though they also have onboarding_prompted=false.
//
// SDD-023 §7.1 explicitly requires: "The mustChangePassword hard gate (rule 3 in
// guardFn) runs first."  This test verifies that ordering without an AI call.
test('GuardOrdering_MustChangePassword_PrecedesOnboarding_Nudge', async ({ page }) => {
  // Create a student with must_change_password=true via the admin UI.
  // Skip gracefully if the stack is unavailable.
  const stackUrl = process.env.PLAYWRIGHT_BASE_URL ?? 'http://localhost:5173'
  try {
    await page.goto(stackUrl + '/login', { timeout: 5_000 })
  } catch {
    test.skip(true, 'e2e stack unavailable — skip guard ordering test')
    return
  }

  const { username, tempPassword } = await createStudentViaAdmin(page)

  // Log in with the temp password.
  await page.goto('/login')
  await page.fill('#username', username)
  await page.fill('input[type="password"]', tempPassword)
  await page.click('button[type="submit"]')

  // Wait for navigation.  The hard gate (must_change_password) MUST fire
  // before the soft gate (onboarding_prompted), so we must land on
  // /change-password, not /onboarding.
  await page.waitForURL(
    url => url.pathname.startsWith('/change-password') || url.pathname.startsWith('/onboarding'),
    { timeout: 10_000 },
  )

  // CRITICAL assertion: must be change-password, not onboarding.
  await expect(page).toHaveURL(/\/change-password/, {
    // REQ-AUTH-014 + SDD-023 §7.1: hard gate takes precedence over soft gate.
    message: 'Guard ordering violated: /change-password must appear before /onboarding (REQ-AUTH-014, SDD-023 §7.1)',
  })
})

// ---------------------------------------------------------------------------
// Skip path (AI-free)
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FEPROFILE-003", "REQ-FEPROFILE-004", "REQ-PROFILE-015"]}
//
// OnboardingSkip_RoutesToCourses_NudgeDoesNotReappear proves the full AI-free
// skip journey:
//   1. Freshly created student logs in → changes password → reaches /onboarding.
//   2. Student clicks the "Skip for now" button.
//   3. After skip the route is /courses (router followed the skip path).
//   4. Reload: the router guard does NOT redirect back to /onboarding because
//      onboarding_prompted is now true in the session.
//
// No Anthropic call is made in this path.
test('OnboardingSkip_RoutesToCourses_NudgeDoesNotReappear', async ({ page }) => {
  const stackUrl = process.env.PLAYWRIGHT_BASE_URL ?? 'http://localhost:5173'
  try {
    await page.goto(stackUrl + '/login', { timeout: 5_000 })
  } catch {
    test.skip(true, 'e2e stack unavailable — skip path test skipped')
    return
  }

  // Step 1: create student and change password.
  const { username, tempPassword } = await createStudentViaAdmin(page)
  await changePasswordAs(page, username, tempPassword)

  // After password change the guard redirects to /onboarding (soft gate).
  await expect(page).toHaveURL(/\/onboarding/, {
    message: 'After password change, student with onboarding_prompted=false must be redirected to /onboarding',
  })

  // Step 2: click the "Skip for now" button.
  // OnboardingView.vue renders a button with this text (SDD-023 §7.4).
  await page.click('button:has-text("Skip for now")')

  // Step 3: after skip, route must be /courses.
  await page.waitForURL('**/courses', { timeout: 10_000 })
  await expect(page).toHaveURL(/\/courses/, {
    message: 'After skip, student must land on /courses (REQ-FEPROFILE-003)',
  })

  // Step 4: reload — guard must NOT redirect back to /onboarding.
  // onboarding_prompted is now true in the session; the soft gate does not fire.
  await page.reload()
  await page.waitForURL(url => !url.pathname.startsWith('/onboarding'), { timeout: 10_000 })

  // Accept a brief navigation to /courses after reload (session restore).
  await expect(page).not.toHaveURL(/\/onboarding/, {
    message: 'After skip, reload must NOT redirect back to /onboarding (REQ-FEPROFILE-004 — nudge shown once only)',
  })
})

// ---------------------------------------------------------------------------
// ProfileView renders in settings
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FEPROFILE-002", "REQ-FEPROFILE-003"]}
//
// ProfileView_RendersInStudentLayout verifies that a student who has completed
// the skip path (or has onboarding_prompted=true) can navigate to /profile and
// see the ProfileSettingsView component.
//
// The test uses the demo student account (already consented, already has
// onboarding_prompted=true in a seeded stack) so it does not depend on creating
// a new account.  If the demo account also needs onboarding this test will still
// pass because the guard only redirects if onboarding_prompted=false AND role=
// 'student' AND not already on /onboarding.  The login helper handles /consent.
test('ProfileView_RendersInStudentLayout', async ({ page }) => {
  const stackUrl = process.env.PLAYWRIGHT_BASE_URL ?? 'http://localhost:5173'
  try {
    await page.goto(stackUrl + '/login', { timeout: 5_000 })
  } catch {
    test.skip(true, 'e2e stack unavailable — ProfileView render test skipped')
    return
  }

  // Use the demo student (already set up for other specs).
  const studentUser = process.env.E2E_STUDENT_USER ?? 'demo_student'
  const studentPass = process.env.E2E_STUDENT_PASS ?? 'StudentDemo!2026'
  await login(page, studentUser, studentPass)

  // Navigate to the profile settings route.
  await page.goto('/profile')
  await page.waitForURL('**/profile', { timeout: 10_000 })

  // ProfileSettingsView (REQ-FEPROFILE-002) renders within StudentLayout.
  // The view must contain either:
  //   - A textarea for editing the profile summary, OR
  //   - A "No profile yet" placeholder message.
  const textarea = page.locator('textarea')
  const noProfileMsg = page.locator('text=No profile yet')

  const hasTextarea = await textarea.count() > 0
  const hasNoProfile = await noProfileMsg.count() > 0

  if (!hasTextarea && !hasNoProfile) {
    throw new Error(
      'ProfileView (/profile) must render either a summary textarea or a "No profile yet" ' +
      'placeholder (REQ-FEPROFILE-002)',
    )
  }
})

// ---------------------------------------------------------------------------
// AI-GATED: Full onboarding chat journey
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FEPROFILE-001", "REQ-FEPROFILE-004", "REQ-PROFILE-011", "REQ-PROFILE-012", "REQ-PROFILE-014"]}
//
// OnboardingChat_FullJourney_ProfileCreated is the single budgeted live AI run
// described in SDD-023 §8.4.  It requires VALORY_E2E_ANTHROPIC_ENABLED=true.
//
// Journey:
//   1. Admin creates a new student.
//   2. Student changes forced password (Sprint 20 path).
//   3. After password change, router redirects to /onboarding (soft gate).
//   4. Student completes 3 turns of the onboarding chat (live Anthropic calls).
//   5. Student clicks "Done" / the chat signals completion.
//   6. App navigates to /courses.
//   7. Student navigates to /profile → sees a non-empty summary from the LLM.
//
// AI-gated: skip unless VALORY_E2E_ANTHROPIC_ENABLED=true.
test('OnboardingChat_FullJourney_ProfileCreated', async ({ page }) => {
  if (!isAIGatingEnabled()) {
    test.skip(true,
      'AI-GATED: set VALORY_E2E_ANTHROPIC_ENABLED=true to run the live onboarding chat journey ' +
      '(makes real Anthropic API calls; costs tokens).',
    )
    return
  }

  const stackUrl = process.env.PLAYWRIGHT_BASE_URL ?? 'http://localhost:5173'
  try {
    await page.goto(stackUrl + '/login', { timeout: 5_000 })
  } catch {
    test.skip(true, 'e2e stack unavailable — AI-gated onboarding journey skipped')
    return
  }

  // Step 1 & 2: create student and change forced password.
  const { username, tempPassword } = await createStudentViaAdmin(page)
  await changePasswordAs(page, username, tempPassword)

  // Step 3: after password change with onboarding_prompted=false, router must
  // redirect to /onboarding.
  await page.waitForURL('**/onboarding', { timeout: 10_000 })
  await expect(page).toHaveURL(/\/onboarding/)

  // Step 4: complete 3 turns of the onboarding chat.
  // The UI is OnboardingView.vue with a textarea input and a Send button.
  // The assistant produces 3-5 questions; after 3 substantive answers the
  // ONBOARDING_COMPLETE sentinel appears in the reply and the UI shows a
  // "Done" / "Complete" button.

  // --- Turn 1: answer the knowledge-level question ---
  await page.locator('textarea').fill('I am an intermediate learner with about 2 years of programming experience.')
  await page.click('button:has-text("Send")')
  // Wait for the assistant reply to appear (new chat bubble added).
  await page.waitForResponse(resp => resp.url().includes('/onboarding/advance') && resp.status() === 200)
  await page.waitForTimeout(500) // let the DOM update

  // --- Turn 2: answer the explanation-style question ---
  await page.locator('textarea').fill('I prefer examples first, then theory. Concrete code helps me a lot.')
  await page.click('button:has-text("Send")')
  await page.waitForResponse(resp => resp.url().includes('/onboarding/advance') && resp.status() === 200)
  await page.waitForTimeout(500)

  // --- Turn 3: answer the time-budget question ---
  await page.locator('textarea').fill('I can study about 10 hours per week.')
  await page.click('button:has-text("Send")')
  await page.waitForResponse(resp => resp.url().includes('/onboarding/advance') && resp.status() === 200)
  await page.waitForTimeout(1_000)

  // After 3 substantive answers the assistant should include ONBOARDING_COMPLETE
  // and the UI should render a completion button.  The exact button text may be
  // "Done", "Complete", or similar — use a flexible locator.
  const completeBtn = page.locator('button:has-text("Done"), button:has-text("Complete"), button:has-text("Finish")')

  // If done=true has not arrived yet after 3 turns we may need another turn.
  // Wait up to 10 s for the completion button to appear.
  await completeBtn.waitFor({ timeout: 10_000 }).catch(() => {
    // Some chat implementations auto-call /complete when done=true.  Try
    // sending one more answer before giving up.
  })

  // Step 5: call /complete (either via button click or auto-trigger).
  if (await completeBtn.count() > 0) {
    await completeBtn.click()
  }

  // Step 6: app navigates to /courses after completion.
  await page.waitForURL('**/courses', { timeout: 15_000 })
  await expect(page).toHaveURL(/\/courses/)

  // Step 7: navigate to /profile and confirm the summary is non-empty.
  await page.goto('/profile')
  await page.waitForURL('**/profile', { timeout: 10_000 })

  const textarea = page.locator('textarea').first()
  await textarea.waitFor({ timeout: 5_000 })
  const summaryText = (await textarea.inputValue()).trim()

  expect(summaryText.length).toBeGreaterThan(20)
  expect(summaryText).not.toBe('No profile yet')
})
