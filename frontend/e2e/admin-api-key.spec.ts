// @{"verifies": ["REQ-FEADMIN-500", "REQ-FEADMIN-501", "REQ-FEADMIN-502", "REQ-FEADMIN-503",
//                "REQ-FEADMIN-504", "REQ-FEADMIN-510", "REQ-FEADMIN-511", "REQ-FEADMIN-512",
//                "REQ-FEADMIN-515", "REQ-ADMIN-005", "REQ-ADMIN-007", "REQ-ADMIN-008",
//                "REQ-SECURITY-008"]}
//
// admin-api-key.spec.ts — Sprint 16 Task 16.4
//
// AI-free e2e tests for the API Keys admin UI section in SystemConfigView and
// the config-field explanation toggle system.  Zero Anthropic API calls are made.
//
// COST RULE: These tests NEVER touch anthropic_api_key.  A fake Anthropic key
// entered via the UI would break live AI features for the duration of the test;
// brave_api_key is used instead because a fake Brave key silently disables web
// search but does not affect any running feature.  All test mutations to
// brave_api_key are restored in finally-blocks via DELETE /api/v1/admin/secrets.
//
// TESTS:
//   1. AdminSecrets_SectionRendersWithStatuses
//        — login as admin → navigate to /admin/config via sidebar → the "API Keys"
//          h2 heading and both key rows (Anthropic API Key, Brave Search API Key)
//          are visible. Each row has a status badge whose text is one of the three
//          valid strings: "Configured (••••xxxx)", "Set via environment", or
//          "Not configured". The password input for each key is type=password and
//          initially empty. Verifies REQ-FEADMIN-500, REQ-FEADMIN-501, REQ-FEADMIN-502.
//
//   2. AdminSecrets_SaveRoundTrip_NeverEchoed
//        — enter fake value 'e2e-test-brave-key-9999' in the brave_api_key password
//          input → click Save → badge flips to "Configured (••••9999)" → full page
//          text and the raw GET /api/v1/admin/secrets response body do NOT contain
//          the full value → click Clear → confirm dialog → badge returns to
//          "Not configured" or "Set via environment". Verifies REQ-FEADMIN-503,
//          REQ-FEADMIN-504, REQ-ADMIN-007, REQ-SECURITY-008.
//
//   3. AdminSecrets_AuditRedaction
//        — after a save+clear cycle for brave_api_key, GET /api/v1/audit and assert
//          that secret.set and secret.clear entries exist for brave_api_key and
//          that the raw response text does NOT contain the fake value string.
//          Verifies REQ-SECURITY-008.
//
//   4. AdminConfig_ExplanationsVisible
//        — config fields each have a "?" toggle button.  Expand three fields:
//          agent_retry_limit (an agent key), audit_retention_days (which carries
//          the honest "No automated purge" disclosure), and session_inactivity_seconds
//          (which carries the env-override disclosure "has no effect on the running
//          server").  Assert the explanation text renders. Also assert brave_api_key
//          explanation text is always visible in its .secret-explanation block
//          (not behind a toggle).  Verifies REQ-FEADMIN-510, REQ-FEADMIN-511,
//          REQ-FEADMIN-512, REQ-FEADMIN-515.

import { test, expect } from '@playwright/test'
import { login, ADMIN_USER, ADMIN_PASS } from './helpers'

// FAKE_BRAVE_VALUE is a clearly synthetic key used in round-trip tests.
// It must never match a real Brave Search API key format.
const FAKE_BRAVE_VALUE = 'e2e-test-brave-key-9999'
// The last 4 characters of FAKE_BRAVE_VALUE — shown in the badge after save.
const FAKE_BRAVE_LAST4 = '9999'

// VALID_BADGE_TEXTS is the set of strings that a status badge may display.
// Any other text is a product bug.
const VALID_BADGE_TEXTS = [
  `Configured (••••${FAKE_BRAVE_LAST4})`, // only after our own save
  'Set via environment',
  'Not configured'
]

// navigateToConfig performs a full admin login and SPA navigation to /admin/config.
// Uses in-SPA sidebar click (not goto()) to preserve the session cookie.
async function navigateToConfig(page: Parameters<typeof login>[0]): Promise<void> {
  await login(page, ADMIN_USER, ADMIN_PASS)
  // The sidebar link must be clicked — goto() to /admin/config after a page.reload()
  // would lose the in-memory session token (pre-Sprint-13 pattern); the cookie
  // survives, but a hard navigation resets SPA state. Sidebar click is safer
  // and matches how admins actually reach this page.
  await page.click('aside.sidebar a[href="/admin/config"]')
  await expect(page).toHaveURL(/\/admin\/config/)
  // Wait for both the config form and the secrets card to finish loading.
  await expect(page.locator('.loading')).not.toBeVisible({ timeout: 10_000 })
  await expect(page.locator('.secrets-card')).toBeVisible({ timeout: 10_000 })
}

// deleteBraveKeyViaAPI sends DELETE /api/v1/admin/secrets/brave_api_key using
// page.request (which carries the __Host-session cookie automatically).  A
// CSRF token is read from the cookie jar.  This is used in finally-blocks to
// restore stack state regardless of test outcome.
async function deleteBraveKeyViaAPI(page: Parameters<typeof login>[0]): Promise<void> {
  const csrf = (await page.context().cookies()).find(c => c.name === '__Host-csrf')?.value ?? ''
  const resp = await page.request.delete('/api/v1/admin/secrets/brave_api_key', {
    headers: { 'X-CSRF-Token': csrf }
  })
  // 204 = deleted, 400 = unknown name (should not happen), anything else is
  // unexpected but should not mask the original test failure.
  if (resp.status() !== 204 && resp.status() !== 400) {
    console.warn(`[finally] DELETE brave_api_key returned HTTP ${resp.status()}`)
  }
}

// ---------------------------------------------------------------------------
// Test 1 — AdminSecrets_SectionRendersWithStatuses
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FEADMIN-500", "REQ-FEADMIN-501", "REQ-FEADMIN-502"]}
test('AdminSecrets_SectionRendersWithStatuses', async ({ page }) => {
  await navigateToConfig(page)

  // The "API Keys" section heading must be visible.
  await expect(page.locator('.secrets-card h2')).toContainText('API Keys')

  // Both known secret labels must appear in secret-header elements.
  const anthropicLabel = page.locator('.secret-field .secret-header label', {
    hasText: 'Anthropic API Key'
  })
  const braveLabel = page.locator('.secret-field .secret-header label', {
    hasText: 'Brave Search API Key'
  })
  await expect(anthropicLabel).toBeVisible()
  await expect(braveLabel).toBeVisible()

  // Each key row must have exactly one status badge whose text is one of the
  // three valid strings.  The actual state depends on the running stack — the
  // test does NOT assume which state is active.
  const badgeLocators = page.locator('.secret-badge')
  const badgeCount = await badgeLocators.count()
  expect(badgeCount, 'Expected two status badges (one per managed secret)').toBe(2)

  for (let i = 0; i < badgeCount; i++) {
    const badgeText = (await badgeLocators.nth(i).textContent())?.trim() ?? ''
    const isValid =
      badgeText === 'Set via environment' ||
      badgeText === 'Not configured' ||
      /^Configured \(••••.{1,4}\)$/.test(badgeText)
    expect(
      isValid,
      `Badge ${i} text "${badgeText}" is not one of the three valid states`
    ).toBe(true)
  }

  // Each password input must be type=password and have an empty value on load.
  // REQ-FEADMIN-502: the browser must never pre-fill a managed-secret input.
  const secretInputs = page.locator('input.secret-input[type="password"]')
  const inputCount = await secretInputs.count()
  expect(inputCount, 'Expected two password inputs (one per managed secret)').toBe(2)

  for (let i = 0; i < inputCount; i++) {
    const value = await secretInputs.nth(i).inputValue()
    expect(
      value,
      `Password input ${i} must be empty on load (REQ-FEADMIN-502 — never echo secret values)`
    ).toBe('')
  }
})

// ---------------------------------------------------------------------------
// Test 2 — AdminSecrets_SaveRoundTrip_NeverEchoed
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FEADMIN-503", "REQ-FEADMIN-504", "REQ-ADMIN-007", "REQ-SECURITY-008"]}
test('AdminSecrets_SaveRoundTrip_NeverEchoed', async ({ page }) => {
  await navigateToConfig(page)

  // Ensure cleanup regardless of assertion failures.
  try {
    // Locate the brave_api_key row by finding a secret-field that contains the
    // "Brave Search API Key" label.  All interactions are scoped to this row.
    const braveRow = page.locator('.secret-field', {
      has: page.locator('label', { hasText: 'Brave Search API Key' })
    })
    await expect(braveRow).toBeVisible()

    // Record the badge text before the save so we can assert it changes.
    const badgeBefore = braveRow.locator('.secret-badge')
    const textBefore = (await badgeBefore.textContent())?.trim() ?? ''

    // Enter the fake brave key in the password input.
    const secretInput = braveRow.locator('input.secret-input[type="password"]')
    await secretInput.fill(FAKE_BRAVE_VALUE)

    // Click the Save button (within the brave row specifically).
    const saveBtn = braveRow.locator('button.save-secret-button')
    await saveBtn.click()

    // Wait for the badge to flip to "Configured (••••9999)".
    // REQ-FEADMIN-503: after a successful PUT the badge must show last4.
    await expect(badgeBefore).toContainText(`Configured (••••${FAKE_BRAVE_LAST4})`, {
      timeout: 10_000
    })

    // REQ-SECURITY-008: the full secret value must NEVER appear in the page DOM.
    const fullPageText = await page.content()
    expect(
      fullPageText,
      'The full fake key value must not appear anywhere in the page source'
    ).not.toContain(FAKE_BRAVE_VALUE)

    // REQ-ADMIN-007: GET /api/v1/admin/secrets must NEVER return plaintext values.
    // page.request carries the __Host-session cookie automatically.
    const listResp = await page.request.get('/api/v1/admin/secrets')
    expect(listResp.ok(), 'GET /api/v1/admin/secrets must return 200').toBe(true)
    const listBody = await listResp.text()
    expect(
      listBody,
      'GET /api/v1/admin/secrets response body must not contain the full key value'
    ).not.toContain(FAKE_BRAVE_VALUE)

    // The input must be cleared after a successful save (frontend wipes it so the
    // value is not sitting in the DOM in plaintext).
    const inputValueAfterSave = await secretInput.inputValue()
    expect(
      inputValueAfterSave,
      'Password input must be empty after successful save (value must not persist in DOM)'
    ).toBe('')

    // REQ-FEADMIN-504: the Clear button must be present only when source === "managed".
    // After our save, brave_api_key is managed, so Clear must be visible.
    const clearBtn = braveRow.locator('button.delete-secret-button')
    await expect(clearBtn).toBeVisible()

    // Click Clear. The deleteSecret handler calls window.confirm; accept it.
    page.on('dialog', dialog => dialog.accept())
    await clearBtn.click()

    // After clearing, the badge must return to a non-configured state.
    // The exact state depends on whether BRAVE_API_KEY env var is set on the
    // running stack; accept either "Not configured" or "Set via environment".
    await expect(badgeBefore).not.toContainText(`Configured (••••${FAKE_BRAVE_LAST4})`, {
      timeout: 10_000
    })
    const textAfterClear = (await badgeBefore.textContent())?.trim() ?? ''
    const isValidAfterClear =
      textAfterClear === 'Not configured' || textAfterClear === 'Set via environment'
    expect(
      isValidAfterClear,
      `After Clear, badge must be "Not configured" or "Set via environment", got: "${textAfterClear}"`
    ).toBe(true)

    // The Clear button must disappear now that source is no longer "managed".
    await expect(clearBtn).not.toBeVisible()

    // Sanity: the badge text must have actually changed from the pre-test state
    // unless it was already configured (unlikely in a clean test environment,
    // but possible if brave_api_key is set via an env var on this stack).
    // We only assert the change-and-restore cycle completed without error.
    void textBefore // used only to ensure no regression via a future linter
  } finally {
    // Always restore: delete the managed row even if assertions above failed.
    // This prevents a stale brave_api_key from affecting subsequent test runs.
    await deleteBraveKeyViaAPI(page)
  }
})

// ---------------------------------------------------------------------------
// Test 3 — AdminSecrets_AuditRedaction
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-SECURITY-008"]}
test('AdminSecrets_AuditRedaction', async ({ page }) => {
  await navigateToConfig(page)

  // Perform a save+clear cycle directly via the API to produce audit entries
  // without depending on UI state from a previous test.
  const csrf = (await page.context().cookies()).find(c => c.name === '__Host-csrf')?.value ?? ''

  try {
    // PUT brave_api_key via API (same endpoint the UI calls).
    const putResp = await page.request.put('/api/v1/admin/secrets/brave_api_key', {
      headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' },
      data: { value: FAKE_BRAVE_VALUE }
    })
    expect(
      putResp.ok(),
      `PUT /api/v1/admin/secrets/brave_api_key must succeed; got HTTP ${putResp.status()}`
    ).toBe(true)

    // DELETE brave_api_key via API to generate a secret.clear audit entry.
    const deleteResp = await page.request.delete('/api/v1/admin/secrets/brave_api_key', {
      headers: { 'X-CSRF-Token': csrf }
    })
    expect(
      deleteResp.status(),
      `DELETE /api/v1/admin/secrets/brave_api_key must return 204; got HTTP ${deleteResp.status()}`
    ).toBe(204)

    // Fetch recent audit log entries.  Limit 50 covers any reasonably active
    // demo environment; the save and clear we just performed are at the top.
    const auditResp = await page.request.get('/api/v1/audit?limit=50')
    expect(
      auditResp.ok(),
      `GET /api/v1/audit must return 200; got HTTP ${auditResp.status()}`
    ).toBe(true)

    const auditText = await auditResp.text()

    // REQ-SECURITY-008: the audit log must NEVER contain plaintext secret values.
    expect(
      auditText,
      'Audit log response body must not contain the full fake key value'
    ).not.toContain(FAKE_BRAVE_VALUE)

    // Parse the response to assert structural completeness.
    const auditBody = JSON.parse(auditText) as { entries: { action: string; target_type: string; payload: Record<string, unknown> }[] }

    // Find secret.set and secret.clear entries for brave_api_key.
    const setEntry = auditBody.entries.find(
      e => e.action === 'secret.set' && e.payload?.['name'] === 'brave_api_key'
    )
    const clearEntry = auditBody.entries.find(
      e => e.action === 'secret.clear' && e.payload?.['name'] === 'brave_api_key'
    )

    expect(
      setEntry,
      'Audit log must contain a secret.set entry for brave_api_key'
    ).toBeDefined()
    expect(
      clearEntry,
      'Audit log must contain a secret.clear entry for brave_api_key'
    ).toBeDefined()

    // Verify the payload for the set entry contains only the name — no value,
    // no last4, no ciphertext (REQ-SECURITY-008).
    if (setEntry) {
      const payloadKeys = Object.keys(setEntry.payload)
      expect(
        payloadKeys,
        'secret.set audit payload must contain only "name" — no value or last4'
      ).toEqual(['name'])
    }

    // Same check for clear entry.
    if (clearEntry) {
      const payloadKeys = Object.keys(clearEntry.payload)
      expect(
        payloadKeys,
        'secret.clear audit payload must contain only "name" — no value or last4'
      ).toEqual(['name'])
    }
  } finally {
    // Belt-and-suspenders cleanup: if the PUT succeeded but the DELETE didn't,
    // clean up so the next run starts fresh.
    await deleteBraveKeyViaAPI(page)
  }
})

// ---------------------------------------------------------------------------
// Test 4 — AdminConfig_ExplanationsVisible
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FEADMIN-510", "REQ-FEADMIN-511", "REQ-FEADMIN-512", "REQ-FEADMIN-515"]}
test('AdminConfig_ExplanationsVisible', async ({ page }) => {
  await navigateToConfig(page)

  // ---- 4a. Secret explanations are ALWAYS visible (no toggle required) -----
  // The .secret-explanation blocks are rendered unconditionally in the Vue
  // template (no v-if toggle — they are always shown below the input).

  // brave_api_key explanation must mention "silently skipped" for the honest
  // disclosure about absent Brave key behavior (REQ-FEADMIN-516).
  const braveRow = page.locator('.secret-field', {
    has: page.locator('label', { hasText: 'Brave Search API Key' })
  })
  const braveExplanation = braveRow.locator('.secret-explanation')
  await expect(braveExplanation).toBeVisible()
  await expect(braveExplanation).toContainText('silently skipped')

  // ---- 4b. Config field explanations require a "?" button toggle -----------
  // Explanations for CONFIG_KEYS fields are hidden until the "?" button is
  // clicked (v-if="expandedExplanations[key]").

  // agent_retry_limit: an agent-orchestration key.
  // REQ-FEADMIN-511: explanation describes what the setting controls.
  const agentRetryToggle = page.locator(
    '.form-field-with-explanation:has(#config-agent_retry_limit) .explanation-toggle'
  )
  await expect(agentRetryToggle).toBeVisible()
  // Explanation block must be hidden before toggle click.
  const agentRetryExplanationBlock = page.locator(
    '.form-field-with-explanation:has(#config-agent_retry_limit) .explanation-block'
  )
  await expect(agentRetryExplanationBlock).not.toBeVisible()
  // Click "?" to expand.
  await agentRetryToggle.click()
  // Explanation must appear and contain meaningful content.
  await expect(agentRetryExplanationBlock).toBeVisible()
  await expect(agentRetryExplanationBlock).toContainText('AI agent retries')

  // audit_retention_days: REQ-FEADMIN-512 requires the "No automated purge"
  // honest disclosure so admins understand this setting is currently inert.
  const auditRetentionToggle = page.locator(
    '.form-field-with-explanation:has(#config-audit_retention_days) .explanation-toggle'
  )
  await expect(auditRetentionToggle).toBeVisible()
  const auditRetentionExplanationBlock = page.locator(
    '.form-field-with-explanation:has(#config-audit_retention_days) .explanation-block'
  )
  await expect(auditRetentionExplanationBlock).not.toBeVisible()
  await auditRetentionToggle.click()
  await expect(auditRetentionExplanationBlock).toBeVisible()
  // REQ-FEADMIN-512: must include the honest "No automated purge" disclosure.
  await expect(auditRetentionExplanationBlock).toContainText('No automated purge')

  // session_inactivity_seconds: REQ-FEADMIN-515 requires the env-override
  // disclosure explaining this config key is currently inert on the server.
  const sessionInactivityToggle = page.locator(
    '.form-field-with-explanation:has(#config-session_inactivity_seconds) .explanation-toggle'
  )
  await expect(sessionInactivityToggle).toBeVisible()
  const sessionInactivityExplanationBlock = page.locator(
    '.form-field-with-explanation:has(#config-session_inactivity_seconds) .explanation-block'
  )
  await expect(sessionInactivityExplanationBlock).not.toBeVisible()
  await sessionInactivityToggle.click()
  await expect(sessionInactivityExplanationBlock).toBeVisible()
  // REQ-FEADMIN-515: must disclose that this key "has no effect on the running server".
  await expect(sessionInactivityExplanationBlock).toContainText('has no effect on the running server')

  // ---- 4c. Toggle collapse works (click again hides the block) -------------
  // REQ-FEADMIN-510: the toggle must be bidirectional.
  await agentRetryToggle.click()
  await expect(agentRetryExplanationBlock).not.toBeVisible()
})
