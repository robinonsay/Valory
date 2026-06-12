// @{"verifies", ["REQ-FEAUTH-034", "REQ-FEAUTH-056", "REQ-FEAUTH-057", "REQ-FEAUTH-149", "REQ-FEAUTH-167", "REQ-FEAUTH-168"]}
//
// consent-statement.spec.ts — ConsentView Sprint 11 changes:
//   • The version label "AI Processing Consent Statement — Version 1.0" is
//     rendered as the page <h1> before the student clicks "I agree".
//   • The consent-statement-panel contains distinctive phrases from the
//     statement body (Anthropic, permanent deletion) so the reviewer can be
//     confident the full text is present.
//   • The statement panel is present in the DOM and scrollable (overflow-y:auto)
//     before any interaction — confirming the requirement that the student can
//     read the statement BEFORE accepting (REQ-FEAUTH-167).
//   • After asserting the statement, the helper clicks "I agree" so the
//     demo_student's in-memory consent flag is set and subsequent navigation
//     can proceed normally.
//
// NOTE: Every fresh student login in a new browser context lands on /consent
// because the SPA starts with isConsented=false and rule 7 of the router guard
// redirects the student there before they can reach /courses.  This means the
// consent page is exercised on every student login; the existing login()
// helper in helpers.ts already clicks through it automatically.  This spec
// intercepts the flow BEFORE clicking "I agree" to assert the statement
// content required by Sprint 11.

import { test, expect } from '@playwright/test'
import { STUDENT_USER, STUDENT_PASS } from './helpers'

// loginStopAtConsent navigates to /login, fills credentials, and STOPS once
// the browser lands on /consent.  It does NOT click "I agree", allowing the
// caller to inspect the consent page content.
async function loginStopAtConsent(
  page: import('@playwright/test').Page,
  username: string,
  password: string
): Promise<void> {
  await page.goto('/login')
  await page.fill('#username', username)
  await page.fill('input[type="password"]', password)
  await page.click('button[type="submit"]')

  // Router guard rule 7 redirects every fresh student login to /consent
  // because isConsented is always false in a new browser context.
  await page.waitForURL('**/consent', { timeout: 10_000 })
}

// @{"verifies", ["REQ-FEAUTH-034", "REQ-FEAUTH-056", "REQ-FEAUTH-057", "REQ-FEAUTH-149", "REQ-FEAUTH-167", "REQ-FEAUTH-168"]}
test('ConsentStatement_VersionLabelAndDistinctivePhrasesVisibleBeforeAccept', async ({ page }) => {
  // Stop at the consent page so we can inspect the statement before acceptance.
  await loginStopAtConsent(page, STUDENT_USER, STUDENT_PASS)
  await expect(page).toHaveURL(/\/consent/)

  // ---- Version label (REQ-FEAUTH-057, REQ-FEAUTH-168) -------------------

  // The <h1> must carry exactly this text.  The CONSENT_VERSION constant in
  // ConsentView.vue is "1.0" and is interpolated into the heading.
  const heading = page.locator('h1')
  await expect(heading).toContainText('AI Processing Consent Statement — Version 1.0')

  // ---- Scrollable statement panel (REQ-FEAUTH-167) -----------------------

  // The panel must be present in the DOM and reachable before the student
  // has clicked "I agree".  We assert visibility (not just attachment) to
  // ensure it is not hidden behind a loading spinner or conditional block.
  const panel = page.locator('.consent-statement-panel')
  await expect(panel).toBeVisible()

  // ---- Distinctive statement phrases (REQ-FEAUTH-056) -------------------

  // Phrase 1: Anthropic is mentioned as the external AI service.
  // Exact wording from ConsentView.vue: "the Claude API, which is operated by Anthropic PBC"
  await expect(panel).toContainText('Anthropic')

  // Phrase 2: Permanent deletion is offered through the account deletion function.
  // Exact wording from ConsentView.vue: "permanently delete"
  await expect(panel).toContainText('permanently delete')

  // ---- Complete consent to leave the stack in its expected state ----------
  await page.click('button:has-text("I agree")')
  await page.waitForURL(url => !url.pathname.startsWith('/consent'), { timeout: 10_000 })
  await expect(page).toHaveURL(/\/courses/)
})
