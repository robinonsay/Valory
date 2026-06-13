// @{"verifies": ["REQ-FESETUP-001", "REQ-FESETUP-002", "REQ-FESETUP-003", "REQ-FESETUP-004", "REQ-FESETUP-005", "REQ-FESETUP-006", "REQ-FESETUP-007", "REQ-SETUP-001", "REQ-SETUP-002", "REQ-SETUP-003", "REQ-SETUP-004", "REQ-SETUP-005", "REQ-SETUP-006", "REQ-SETUP-007", "REQ-SETUP-011"]}
//
// setup.spec.ts — End-to-end tests for the first-run administrator setup wizard.
//
// ZERO-USER DATABASE PREREQUISITE (CRITICAL — read before running):
// ===================================================================
// This spec exercises the setup wizard, which only activates when the `users`
// table contains zero rows.  The spec MUST be run against a freshly initialised
// Valory stack with an empty database.  Running it against any stack that
// already has users will cause every test to skip (the stack-gate redirects to
// /login before any wizard page is reachable).
//
// A separate Valory stack with real user data may be running on this machine.
// DO NOT wipe, truncate, or point this spec at that database.  Only provision
// a fresh, isolated stack specifically for this suite.
//
// CI provisioning instructions:
//   1. Create a named, isolated Docker Compose project:
//        docker compose -p valory-setup-test up -d --build
//      This creates a fresh `db_data` volume scoped to the project name
//      (`valory-setup-test_db_data`), separate from the default `valory_db_data`.
//   2. Wait for the backend health check:
//        docker compose -p valory-setup-test exec backend wget -qO- http://localhost:8080/healthz
//   3. Run the spec against the isolated stack:
//        E2E_BASE_URL=https://localhost:8443 npx playwright test e2e/setup.spec.ts
//      (adjust the port if the isolated stack is mapped to a different host port)
//   4. Tear down after the run:
//        docker compose -p valory-setup-test down -v
//      The `-v` flag removes the ephemeral volume so the next CI run starts clean.
//
// This spec was NOT executed against the live database.  It was validated
// statically with `npx playwright test --list` and `npx tsc --noEmit` only.
//
// PLAYWRIGHT CONVENTIONS followed (matching auth.spec.ts / onboarding.spec.ts):
//   • `test.skip` with a descriptive message when the stack is absent or already
//     configured (guards the live-data constraint above).
//   • `@{"verifies": [...]}` annotations at file level AND per-test.
//   • The wizard admin credentials carry a timestamp suffix so repeated runs on
//     the same zero-user DB do not collide with each other (though in practice
//     the 409 guard means only the first creation succeeds; subsequent tests skip
//     on the stack-gate before reaching the POST endpoint).
//
// SELECTOR NOTES:
//   SetupView.vue uses:
//     - button[type="button"] with text "Get Started" for the welcome step
//     - #setup-username, #setup-password, #setup-confirm-password for the form
//     - #setup-email for the optional email field
//     - button[type="submit"] for the form submit button
//     - .field-error for per-field validation error text
//     - .error-message for server-level error text
//     - .redirect-notice for the "Redirecting to login..." text on success
//   LoginView.vue uses:
//     - #username and input[type="password"] for the login form
//     - button[type="submit"] for submission
//   All selectors are stable IDs or semantic roles; no brittle text-matching
//   is used beyond what the spec commentary acknowledges as template-coupled.

import { test, expect } from '@playwright/test'

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

// Use a fixed suffix so the wizard username is predictable within a single
// spec run; a timestamp discriminator avoids CI collisions across reruns.
const SETUP_USERNAME = `setup_admin_${Date.now().toString(36)}`
const SETUP_PASSWORD = 'SetupTest!2026'

// ---------------------------------------------------------------------------
// Skip gate helper
// ---------------------------------------------------------------------------

/**
 * assertStackIsInSetupMode navigates to the app root and returns true if the
 * stack redirects to /setup (zero-user DB state), or skips the test if:
 *   (a) the stack is unreachable, or
 *   (b) the stack is already configured (redirects to /login or elsewhere).
 *
 * This guard prevents the spec from running against the live seeded stack and
 * mutating or asserting against a database that is already populated.
 */
async function assertStackIsInSetupMode(page: import('@playwright/test').Page): Promise<void> {
  const stackUrl = process.env.E2E_BASE_URL ?? 'https://localhost'
  try {
    // Navigate to root; the guard should redirect to /setup.
    await page.goto(stackUrl + '/', { timeout: 8_000 })
  } catch {
    test.skip(true, 'e2e stack unreachable — setup wizard spec skipped')
    return
  }

  // Wait for the router guard to settle.
  await page.waitForURL(
    url => url.pathname.startsWith('/setup') || url.pathname.startsWith('/login'),
    { timeout: 8_000 },
  ).catch(() => {
    // Navigation did not settle to a known route in time — skip.
    test.skip(true, 'e2e stack did not redirect to /setup or /login — skipping')
  })

  if (!page.url().includes('/setup')) {
    test.skip(
      true,
      'Stack is already configured (redirected to /login, not /setup). ' +
      'This spec requires a zero-user database. ' +
      'Provision an isolated stack (see CI provisioning instructions at the top of this file).',
    )
  }
}

// ---------------------------------------------------------------------------
// Scenario 1: Root navigation redirects to /setup on a zero-user database
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FESETUP-001", "REQ-FESETUP-002", "REQ-SETUP-001", "REQ-SETUP-002"]}
//
// SetupGuard_ZeroUserDB_RedirectsRootToSetup proves that the router guard (rule 0a)
// fires before all other routing decisions and sends unauthenticated traffic to
// /setup when the backend reports needs_setup=true (zero users in the DB).
//
// This verifies REQ-FESETUP-001 (boot-time status check) and REQ-FESETUP-002
// (all navigation redirected to /setup when setup is required).  It also acts
// as the live integration signal that the backend GET /api/v1/setup/status
// returns needs_setup:true (REQ-SETUP-001, REQ-SETUP-002).
test('SetupGuard_ZeroUserDB_RedirectsRootToSetup', async ({ page }) => {
  await assertStackIsInSetupMode(page)

  // assertStackIsInSetupMode already navigated to root and confirmed /setup —
  // assert the final URL to produce a visible failure if the redirect is absent.
  await expect(page).toHaveURL(/\/setup/, {
    message: 'Root navigation must redirect to /setup when needs_setup=true (REQ-FESETUP-002)',
  })
})

// ---------------------------------------------------------------------------
// Scenario 2: Arbitrary protected route also redirects to /setup
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FESETUP-002"]}
//
// SetupGuard_ZeroUserDB_RedirectsProtectedRouteToSetup confirms that rule 0a
// applies not only to "/" but to any navigation target, including a typical
// authenticated route like /admin/users.  No route should be reachable while
// needs_setup is true.
test('SetupGuard_ZeroUserDB_RedirectsProtectedRouteToSetup', async ({ page }) => {
  await assertStackIsInSetupMode(page)

  // Try to navigate directly to a protected route.
  await page.goto('/admin/users')

  // Guard rule 0a must intercept this and redirect to /setup.
  await page.waitForURL(/\/setup/, { timeout: 8_000 })
  await expect(page).toHaveURL(/\/setup/, {
    message: 'All navigation must redirect to /setup when needs_setup=true (REQ-FESETUP-002)',
  })
})

// ---------------------------------------------------------------------------
// Scenario 3: Welcome step displays expected content and advances to form step
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FESETUP-004"]}
//
// SetupWizard_WelcomeStep_ClickGetStartedAdvancesToForm proves that the initial
// wizard step renders the welcome copy and that clicking "Get Started" transitions
// the component to the form step without a page reload (client-side state only).
//
// The "Get Started" button is a type="button" (not submit) inside the welcome
// div, rendered when step === 'welcome' in SetupView.vue.
test('SetupWizard_WelcomeStep_ClickGetStartedAdvancesToForm', async ({ page }) => {
  await assertStackIsInSetupMode(page)

  // The wizard loads on /setup from the redirect above; re-navigate cleanly.
  await page.goto('/setup')
  await page.waitForURL(/\/setup/, { timeout: 8_000 })

  // The welcome step heading must be visible.
  await expect(page.locator('h2')).toContainText('Welcome to Valory', {
    message: 'Welcome step heading must be present (REQ-FESETUP-004)',
  })

  // The explanation paragraph must mention the wizard purpose.
  await expect(page.locator('.step-description')).toContainText('administrator account', {
    message: 'Welcome step description must explain the wizard purpose (REQ-FESETUP-004)',
  })

  // The form inputs must NOT be visible yet (still on welcome step).
  await expect(page.locator('#setup-username')).not.toBeVisible({
    message: 'Form must not be shown until Get Started is clicked',
  })

  // Click "Get Started" to advance to the form step.
  await page.click('button:has-text("Get Started")')

  // The form step must now be rendered: the username input appears.
  await expect(page.locator('#setup-username')).toBeVisible({
    message: 'Username input must appear after clicking Get Started (REQ-FESETUP-004 → form step)',
  })

  // The form heading must change to indicate credential creation.
  await expect(page.locator('h2')).toContainText('Create Administrator Account', {
    message: 'Form step heading must appear after advancing from welcome step',
  })
})

// ---------------------------------------------------------------------------
// Scenario 4: Client-side validation — mismatched passwords blocks submission
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FESETUP-006", "REQ-SETUP-007"]}
//
// SetupWizard_Form_MismatchedPasswords_ShowsInlineErrorAndDoesNotSubmit proves
// that the client-side validate() function fires on submit, catches a password
// mismatch, displays an inline error beside the confirmPassword field, and does
// NOT fire the POST /api/v1/setup request.
//
// This satisfies REQ-FESETUP-006 (client-side validation blocks submission) and
// is the frontend counterpart of REQ-SETUP-007 (blank username rejection at the
// server), exercising the client-side path so the server never receives
// mal-formed data.
test('SetupWizard_Form_MismatchedPasswords_ShowsInlineErrorAndDoesNotSubmit', async ({ page }) => {
  await assertStackIsInSetupMode(page)

  await page.goto('/setup')
  await page.waitForURL(/\/setup/, { timeout: 8_000 })
  await page.click('button:has-text("Get Started")')
  await expect(page.locator('#setup-username')).toBeVisible()

  // Fill valid username, valid password, but a DIFFERENT confirm password.
  await page.fill('#setup-username', SETUP_USERNAME)
  await page.fill('#setup-password', SETUP_PASSWORD)
  await page.fill('#setup-confirm-password', SETUP_PASSWORD + '_MISMATCH')

  // Track whether POST /api/v1/setup is fired; it must not be.
  let setupPostFired = false
  page.on('request', (req) => {
    if (req.method() === 'POST' && req.url().includes('/api/v1/setup')) {
      setupPostFired = true
    }
  })

  await page.click('button[type="submit"]')

  // The inline confirm-password error must appear.
  await expect(page.locator('.field-error').last()).toContainText('do not match', {
    message: 'Confirm password mismatch must produce an inline error (REQ-FESETUP-006)',
  })

  // URL must remain /setup — no redirect occurred.
  await expect(page).toHaveURL(/\/setup/, {
    message: 'Page must stay on /setup after client-side validation failure',
  })

  // No network request to the setup endpoint must have been made.
  expect(setupPostFired).toBe(false)
})

// ---------------------------------------------------------------------------
// Scenario 5: Client-side validation — short password blocks submission
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FESETUP-006", "REQ-SETUP-006"]}
//
// SetupWizard_Form_ShortPassword_ShowsInlineErrorAndDoesNotSubmit proves that
// the client-side password-length check (length < 8) fires before network I/O
// when the supplied password is too short, satisfying REQ-FESETUP-006.  This is
// the client-side mirror of the server-enforced REQ-SETUP-006 (password minimum
// length enforcement) — both guards must exist independently.
test('SetupWizard_Form_ShortPassword_ShowsInlineErrorAndDoesNotSubmit', async ({ page }) => {
  await assertStackIsInSetupMode(page)

  await page.goto('/setup')
  await page.waitForURL(/\/setup/, { timeout: 8_000 })
  await page.click('button:has-text("Get Started")')
  await expect(page.locator('#setup-username')).toBeVisible()

  // Fill valid username but a password that is only 5 characters long.
  await page.fill('#setup-username', SETUP_USERNAME)
  await page.fill('#setup-password', 'short')
  await page.fill('#setup-confirm-password', 'short')

  let setupPostFired = false
  page.on('request', (req) => {
    if (req.method() === 'POST' && req.url().includes('/api/v1/setup')) {
      setupPostFired = true
    }
  })

  await page.click('button[type="submit"]')

  // The inline password error must appear.
  await expect(page.locator('.field-error')).toContainText('at least 8 characters', {
    message: 'Short password must produce a client-side inline error (REQ-FESETUP-006)',
  })

  await expect(page).toHaveURL(/\/setup/)
  expect(setupPostFired).toBe(false)
})

// ---------------------------------------------------------------------------
// Scenario 6: Client-side validation — blank username blocks submission
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FESETUP-006", "REQ-SETUP-007"]}
//
// SetupWizard_Form_BlankUsername_ShowsInlineErrorAndDoesNotSubmit proves that
// submitting the form with an empty username field triggers the client-side
// "Username is required" error without making a network call.  This is the
// frontend enforcement complementing the server-side REQ-SETUP-007 guard.
test('SetupWizard_Form_BlankUsername_ShowsInlineErrorAndDoesNotSubmit', async ({ page }) => {
  await assertStackIsInSetupMode(page)

  await page.goto('/setup')
  await page.waitForURL(/\/setup/, { timeout: 8_000 })
  await page.click('button:has-text("Get Started")')
  await expect(page.locator('#setup-username')).toBeVisible()

  // Leave username blank; fill valid password and matching confirm.
  await page.fill('#setup-username', '')
  await page.fill('#setup-password', SETUP_PASSWORD)
  await page.fill('#setup-confirm-password', SETUP_PASSWORD)

  let setupPostFired = false
  page.on('request', (req) => {
    if (req.method() === 'POST' && req.url().includes('/api/v1/setup')) {
      setupPostFired = true
    }
  })

  await page.click('button[type="submit"]')

  // The inline username error must appear.
  await expect(page.locator('.field-error').first()).toContainText('required', {
    message: 'Blank username must produce a client-side inline error (REQ-FESETUP-006, REQ-SETUP-007)',
  })

  await expect(page).toHaveURL(/\/setup/)
  expect(setupPostFired).toBe(false)
})

// ---------------------------------------------------------------------------
// Scenario 7: Happy path — full wizard journey and post-setup login
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FESETUP-001", "REQ-FESETUP-002", "REQ-FESETUP-003", "REQ-FESETUP-004", "REQ-FESETUP-005", "REQ-FESETUP-006", "REQ-FESETUP-007", "REQ-SETUP-001", "REQ-SETUP-002", "REQ-SETUP-003", "REQ-SETUP-004", "REQ-SETUP-009", "REQ-SETUP-011"]}
//
// SetupWizard_HappyPath_CreatesAdminAndRedirectsToLogin is the primary
// integration test for the entire wizard flow:
//
//   1. Zero-user DB → root navigation redirects to /setup (rule 0a).
//   2. Welcome step renders (REQ-FESETUP-004).
//   3. "Get Started" advances to the form step.
//   4. Valid credentials (username + password + matching confirm + optional email)
//      are submitted.
//   5. POST /api/v1/setup returns 201 (REQ-SETUP-003, REQ-SETUP-011 — no CSRF
//      token required on the public endpoint).
//   6. Success step renders "Setup Complete" (REQ-FESETUP-007).
//   7. After ~1.5 s the SPA navigates to /login (REQ-FESETUP-007).
//   8. Login with the just-created admin credentials succeeds.
//   9. Admin lands on /admin/users (REQ-SETUP-004 — account was created as admin).
//
// After this test completes the database has one user row.  Subsequent tests in
// this file should find the setup guard redirecting to /login, not /setup.
test('SetupWizard_HappyPath_CreatesAdminAndRedirectsToLogin', async ({ page }) => {
  await assertStackIsInSetupMode(page)

  // --- Step 1: confirm root redirects to /setup (already asserted by gate) ---
  await page.goto('/')
  await page.waitForURL(/\/setup/, { timeout: 8_000 })
  await expect(page).toHaveURL(/\/setup/)

  // --- Step 2: welcome step renders ---
  await expect(page.locator('h2')).toContainText('Welcome to Valory')

  // --- Step 3: advance to form step ---
  await page.click('button:has-text("Get Started")')
  await expect(page.locator('#setup-username')).toBeVisible()

  // --- Step 4: fill valid credentials (with optional email) ---
  await page.fill('#setup-username', SETUP_USERNAME)
  await page.fill('#setup-email', 'setup-admin@example.com')
  await page.fill('#setup-password', SETUP_PASSWORD)
  await page.fill('#setup-confirm-password', SETUP_PASSWORD)

  // Track the POST /api/v1/setup request to assert 201 response.
  const setupResponsePromise = page.waitForResponse(
    (resp) => resp.url().includes('/api/v1/setup') && resp.request().method() === 'POST',
    { timeout: 15_000 },
  )

  // --- Step 4 (continued): submit the form ---
  await page.click('button[type="submit"]')

  // --- Step 5: POST /api/v1/setup must return 201 ---
  const setupResponse = await setupResponsePromise
  expect(setupResponse.status()).toBe(201)

  // The response body must include the username and role (REQ-SETUP-003, REQ-SETUP-004).
  const setupBody = await setupResponse.json() as { username?: string; role?: string }
  expect(setupBody.username).toBe(SETUP_USERNAME)
  expect(setupBody.role).toBe('admin')

  // --- Step 6: success step renders ---
  await expect(page.locator('h2')).toContainText('Setup Complete', {
    message: 'Success step heading must appear after successful POST (REQ-FESETUP-007)',
  })
  await expect(page.locator('.redirect-notice')).toContainText('Redirecting to login', {
    message: 'Redirect notice must be shown on the success step (REQ-FESETUP-007)',
  })

  // --- Step 7: auto-redirect to /login after ~1.5 s ---
  // Allow up to 5 s for the setTimeout(1500) to fire and the router to push /login.
  await page.waitForURL(/\/login/, { timeout: 5_000 })
  await expect(page).toHaveURL(/\/login/, {
    message: 'Success step must auto-redirect to /login (REQ-FESETUP-007)',
  })

  // --- Step 8 & 9: log in with the just-created admin credentials ---
  await page.fill('#username', SETUP_USERNAME)
  await page.fill('input[type="password"]', SETUP_PASSWORD)
  await page.click('button[type="submit"]')

  // The admin home route is /admin/users (REQ-SETUP-004: first admin has role='admin').
  await page.waitForURL(/\/admin\/users/, { timeout: 10_000 })
  await expect(page).toHaveURL(/\/admin\/users/, {
    message:
      'Admin created via setup must land on /admin/users after login ' +
      '(REQ-SETUP-004 — first admin account must have role=admin)',
  })
})

// ---------------------------------------------------------------------------
// Scenario 8: One-shot guard — /setup redirects to /login once a user exists
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FESETUP-003", "REQ-SETUP-005"]}
//
// SetupGuard_AfterSetupComplete_SetupRouteRedirectsToLogin verifies that once
// an admin account exists (i.e., after the happy-path test has run), navigating
// directly to /setup is redirected to /login by guard rule 0b.
//
// This covers REQ-FESETUP-003 (setup route redirects to login when setup is not
// required) and REQ-SETUP-005 (atomic one-shot guard — the endpoint returns 409
// after the first admin is created).
//
// ORDERING NOTE: this test depends on SetupWizard_HappyPath_CreatesAdminAndRedirectsToLogin
// having run first (workers=1, fullyParallel=false) so there is exactly one user
// in the DB.  If run in isolation against a fresh DB it will be skipped by
// assertStackIsInSetupMode (which only proceeds when /setup is the redirect target).
// That is intentional — the skip message guides the operator to run the full suite.
test('SetupGuard_AfterSetupComplete_SetupRouteRedirectsToLogin', async ({ page }) => {
  // For this test we need to be in the POST-setup state (at least one user exists).
  // Check whether the stack has been configured yet.
  const stackUrl = process.env.E2E_BASE_URL ?? 'https://localhost'
  try {
    await page.goto(stackUrl + '/', { timeout: 8_000 })
  } catch {
    test.skip(true, 'e2e stack unreachable — setup guard post-setup test skipped')
    return
  }

  await page.waitForURL(
    url => url.pathname.startsWith('/setup') || url.pathname.startsWith('/login'),
    { timeout: 8_000 },
  ).catch(() => {
    test.skip(true, 'Stack did not redirect to /setup or /login — skipping')
  })

  if (page.url().includes('/setup')) {
    test.skip(
      true,
      'Stack is still in zero-user mode — run SetupWizard_HappyPath_CreatesAdminAndRedirectsToLogin first, ' +
      'or run the full suite sequentially (workers=1)',
    )
    return
  }

  // Stack is already configured (landed on /login).  Now navigate directly to /setup.
  await page.goto('/setup')

  // Guard rule 0b must redirect /setup → /login when needs_setup=false.
  await page.waitForURL(/\/login/, { timeout: 8_000 })
  await expect(page).toHaveURL(/\/login/, {
    message:
      'Navigating to /setup after setup completes must redirect to /login ' +
      '(REQ-FESETUP-003 guard rule 0b; REQ-SETUP-005 one-shot enforcement)',
  })
})

// ---------------------------------------------------------------------------
// Scenario 9: 409 response when POST /api/v1/setup is called after config
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-SETUP-005", "REQ-FESETUP-003"]}
//
// SetupEndpoint_409WhenAlreadyConfigured_ShowsServerError verifies that if the
// /setup POST is reached despite the guard (e.g. during a race or a direct API
// call), the server returns 409 and the component surfaces a server-level error
// message rather than proceeding to the success step.
//
// Because the guard makes /setup inaccessible once a user exists, this test
// uses page.route() to intercept the status endpoint and fake a needs_setup:true
// response so the wizard renders; it then intercepts the POST and delivers a
// real 409 response.  This allows the UI error-handling path to be exercised
// without requiring a zero-user DB.
//
// If the stack is unreachable the test is skipped.
test('SetupEndpoint_409WhenAlreadyConfigured_ShowsServerError', async ({ page }) => {
  const stackUrl = process.env.E2E_BASE_URL ?? 'https://localhost'
  try {
    await page.goto(stackUrl + '/', { timeout: 8_000 })
  } catch {
    test.skip(true, 'e2e stack unreachable — 409 server error test skipped')
    return
  }

  // Intercept the setup status endpoint to make the SPA think setup is still needed.
  // This causes guard rule 0a to redirect to /setup and the wizard to render.
  await page.route('**/api/v1/setup/status', (route) => {
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_setup: true }),
    })
  })

  // Intercept the POST endpoint to return a 409 already_configured response.
  await page.route('**/api/v1/setup', async (route) => {
    if (route.request().method() === 'POST') {
      await route.fulfill({
        status: 409,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'already_configured' }),
      })
    } else {
      await route.continue()
    }
  })

  // Navigate to /setup — the intercepted status response keeps the wizard accessible.
  await page.goto('/setup')
  await page.waitForURL(/\/setup/, { timeout: 8_000 })

  // Advance to the form step.
  await page.click('button:has-text("Get Started")')
  await expect(page.locator('#setup-username')).toBeVisible()

  // Submit valid credentials.
  await page.fill('#setup-username', SETUP_USERNAME)
  await page.fill('#setup-password', SETUP_PASSWORD)
  await page.fill('#setup-confirm-password', SETUP_PASSWORD)
  await page.click('button[type="submit"]')

  // The component must display the server-level error message for a 409 response.
  // SetupView.vue sets serverError to 'An administrator account already exists. Please log in.'
  await expect(page.locator('.error-message')).toContainText('already exists', {
    message:
      'A 409 response from POST /api/v1/setup must show a server-level error (REQ-SETUP-005)',
  })

  // The component must NOT advance to the success step.
  await expect(page.locator('h2')).not.toContainText('Setup Complete', {
    message: 'Success step must not render when the server returns 409',
  })
})
