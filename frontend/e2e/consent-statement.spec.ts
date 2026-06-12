// @{"verifies": ["REQ-FEAUTH-034", "REQ-FEAUTH-056", "REQ-FEAUTH-057", "REQ-FEAUTH-149", "REQ-FEAUTH-167", "REQ-FEAUTH-168"]}
//
// consent-statement.spec.ts — the REAL first-login consent journey.
//
// Sprint 13's async router guard applies rules deterministically: consented
// students are bounced OFF /consent, unconsented students are forced ONTO it.
// (The pre-Sprint-13 version of this spec visited /consent as a consented
// student — that only worked by exploiting the guard race that Sprint 13
// fixed.)  To test the statement content the way a student actually sees it,
// the spec revokes demo_student's consent in the DB, logs in manually (the
// shared login() helper would auto-click "I agree" before we could assert),
// verifies the statement, and accepts — which restores the consent record.
// A finally-block restores consent via the API if any assertion fails, since
// other specs (ConsentSkipped_WhenAlreadyConsented) rely on it.

import { test, expect } from '@playwright/test'
import { STUDENT_USER, STUDENT_PASS } from './helpers'
import { revokeConsent } from './seed'

// @{"verifies": ["REQ-FEAUTH-034", "REQ-FEAUTH-056", "REQ-FEAUTH-057", "REQ-FEAUTH-149", "REQ-FEAUTH-167", "REQ-FEAUTH-168"]}
test('ConsentStatement_VersionLabelAndDistinctivePhrasesVisibleBeforeAccept', async ({ page }) => {
  // Make demo_student unconsented so the guard forces the consent interstitial.
  revokeConsent(STUDENT_USER)

  let accepted = false
  try {
    // Manual login (NOT the shared helper, which auto-accepts consent).
    await page.goto('/login')
    await page.fill('#username', STUDENT_USER)
    await page.fill('input[type="password"]', STUDENT_PASS)
    await page.click('button[type="submit"]')

    // The guard must route the unconsented student to /consent (REQ-FEAUTH-037).
    await expect(page).toHaveURL(/\/consent/, { timeout: 10_000 })

    // ---- Version label (REQ-FEAUTH-057, REQ-FEAUTH-168) -------------------
    const heading = page.locator('h1')
    await expect(heading).toContainText('AI Processing Consent Statement — Version 1.0')

    // ---- Scrollable statement panel, readable BEFORE accepting (167) ------
    const panel = page.locator('.consent-statement-panel')
    await expect(panel).toBeVisible()

    // ---- Distinctive statement phrases (REQ-FEAUTH-056) -------------------
    await expect(panel).toContainText('Anthropic')
    await expect(panel).toContainText('permanently delete')

    // ---- Accept: restores the consent record server-side ------------------
    await page.click('button:has-text("I agree")')
    await page.waitForURL(url => !url.pathname.startsWith('/consent'), { timeout: 10_000 })
    await expect(page).toHaveURL(/\/courses/)
    accepted = true
  } finally {
    if (!accepted) {
      // Self-heal: restore consent via the API (cookie-auth via the shared
      // jar; CSRF token from the __Host-csrf cookie) so later specs that
      // depend on demo_student being consented are not poisoned.
      const csrf =
        (await page.context().cookies()).find(c => c.name === '__Host-csrf')?.value ?? ''
      await page.request
        .post('/api/v1/consent', {
          headers: { 'X-CSRF-Token': csrf },
          data: { version: '1.0' }
        })
        .catch(() => {
          // Best-effort: if even this fails the next consent-spec run will
          // revoke+re-accept anyway; only same-run specs could be affected.
        })
    }
  }
})
