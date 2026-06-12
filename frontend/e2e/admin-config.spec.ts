// @{"verifies": ["REQ-FEADMIN-040", "REQ-FEADMIN-041", "REQ-FEADMIN-042", "REQ-FEADMIN-043", "REQ-FEADMIN-044", "REQ-FEADMIN-045", "REQ-FEADMIN-210", "REQ-FEADMIN-215", "REQ-FEADMIN-240", "REQ-FEADMIN-250", "REQ-FEADMIN-300", "REQ-FEADMIN-301", "REQ-FEADMIN-302", "REQ-FEADMIN-303", "REQ-FEADMIN-304", "REQ-FEADMIN-305", "REQ-FEADMIN-306", "REQ-FEADMIN-307", "REQ-FEADMIN-308", "REQ-FEADMIN-309", "REQ-FEADMIN-310", "REQ-FEADMIN-311", "REQ-FEADMIN-312", "REQ-FEADMIN-320", "REQ-FEADMIN-321", "REQ-FEADMIN-322", "REQ-FEADMIN-323", "REQ-FEADMIN-324", "REQ-FEADMIN-325", "REQ-FEADMIN-330"]}
//
// admin-config.spec.ts — System configuration page:
//   • All 13 labeled config fields render with non-empty values.
//   • Changing agent_retry_limit 3→4, saving, reloading confirms persistence.
//   • Restoring original value and saving leaves the system in its original state.
//   • Setting homework_weight=0.9 (project_weight unchanged at 0.3) triggers the
//     client-side weight-sum validation error and prevents a save.

import { test, expect } from '@playwright/test'
import { login, ADMIN_USER, ADMIN_PASS } from './helpers'

// All 13 config keys and their labels as defined in systemConfig.ts.
// The test iterates over these to assert every field renders non-empty.
const CONFIG_LABELS: Record<string, string> = {
  agent_retry_limit: 'Agent Retry Limit',
  correction_loop_max_iterations: 'Correction Loop Max Iterations',
  per_student_token_limit: 'Per Student Token Limit',
  late_penalty_rate: 'Late Penalty Rate',
  homework_weight: 'Homework Weight',
  project_weight: 'Project Weight',
  session_inactivity_seconds: 'Session Inactivity (seconds)',
  account_lockout_seconds: 'Account Lockout (seconds)',
  max_upload_bytes: 'Max Upload Size (bytes)',
  content_generation_timeout_seconds: 'Content Generation Timeout (seconds)',
  audit_retention_days: 'Audit Retention (days)',
  notification_retention_days: 'Notification Retention (days)',
  consent_version: 'Consent Version'
}

// @{"verifies": ["REQ-FEADMIN-300", "REQ-FEADMIN-301", "REQ-FEADMIN-302", "REQ-FEADMIN-303", "REQ-FEADMIN-304", "REQ-FEADMIN-305", "REQ-FEADMIN-306", "REQ-FEADMIN-307", "REQ-FEADMIN-308", "REQ-FEADMIN-309", "REQ-FEADMIN-310", "REQ-FEADMIN-311", "REQ-FEADMIN-312"]}
test('AdminConfig_AllThirteenFieldsRenderWithNonEmptyValues', async ({ page }) => {
  await login(page, ADMIN_USER, ADMIN_PASS)
  // In-SPA navigation: the token lives only in memory, so a goto() hard
  // reload would wipe the session and bounce to /login.
  await page.click('aside.sidebar a[href="/admin/config"]')
  await expect(page).toHaveURL(/\/admin\/config/)

  // Wait for the loading spinner to disappear, confirming data has loaded.
  await expect(page.locator('.loading')).not.toBeVisible({ timeout: 10_000 })

  for (const [key, label] of Object.entries(CONFIG_LABELS)) {
    const input = page.locator(`#config-${key}`)
    await expect(input, `field ${label} (id config-${key}) must be visible`).toBeVisible()

    const value = await input.inputValue()
    expect(value, `field ${label} must have a non-empty value after load`).not.toBe('')
  }
})

// @{"verifies": ["REQ-FEADMIN-041", "REQ-FEADMIN-042", "REQ-FEADMIN-043", "REQ-FEADMIN-044", "REQ-FEADMIN-320", "REQ-FEADMIN-321", "REQ-FEADMIN-322", "REQ-FEADMIN-323", "REQ-FEADMIN-324"]}
test('AdminConfig_ChangeAgentRetryLimitAndSave_ValuePersistsAfterReload', async ({ page }) => {
  await login(page, ADMIN_USER, ADMIN_PASS)
  // In-SPA navigation: the token lives only in memory, so a goto() hard
  // reload would wipe the session and bounce to /login.
  await page.click('aside.sidebar a[href="/admin/config"]')
  await expect(page.locator('.loading')).not.toBeVisible({ timeout: 10_000 })

  const field = page.locator('#config-agent_retry_limit')
  const original = await field.inputValue()

  // Determine target value: if current is "3" set to "4", otherwise set to "3"
  // so the test is idempotent regardless of the current live value.
  const targetValue = original === '3' ? '4' : '3'
  const restoreValue = original

  // Change the value.
  await field.fill(targetValue)

  // The "You have unsaved changes" indicator must appear.
  await expect(page.locator('.unsaved-indicator')).toBeVisible()

  // Click Save.
  await page.click('button.save-button')

  // Wait for the "Configuration saved." success banner.
  await expect(page.locator('.success-banner')).toContainText('Configuration saved.', { timeout: 10_000 })

  // Confirm persistence: a hard reload wipes the in-memory session, so
  // reload, log back in, and navigate to Config again — the value is fetched
  // fresh from the API on mount, which proves it persisted server-side.
  await page.reload()
  await login(page, ADMIN_USER, ADMIN_PASS)
  await page.click('aside.sidebar a[href="/admin/config"]')
  await expect(page.locator('.loading')).not.toBeVisible({ timeout: 10_000 })
  await expect(page.locator('#config-agent_retry_limit')).toHaveValue(targetValue)

  // Restore original value so the stack is left in its pre-test state.
  await page.locator('#config-agent_retry_limit').fill(restoreValue)
  await page.click('button.save-button')
  await expect(page.locator('.success-banner')).toContainText('Configuration saved.', { timeout: 10_000 })
})

// @{"verifies": ["REQ-FEADMIN-043", "REQ-FEADMIN-322", "REQ-FEADMIN-323", "REQ-FEADMIN-324", "REQ-FEADMIN-325", "REQ-FEADMIN-330"]}
test('AdminConfig_InvalidWeightSum_ShowsClientSideErrorAndBlocksSave', async ({ page }) => {
  await login(page, ADMIN_USER, ADMIN_PASS)
  // In-SPA navigation: the token lives only in memory, so a goto() hard
  // reload would wipe the session and bounce to /login.
  await page.click('aside.sidebar a[href="/admin/config"]')
  await expect(page.locator('.loading')).not.toBeVisible({ timeout: 10_000 })

  // Read the current project_weight so we can restore it after the test.
  const originalProjectWeight = await page.locator('#config-project_weight').inputValue()
  const originalHomeworkWeight = await page.locator('#config-homework_weight').inputValue()

  // Set homework_weight=0.9 while leaving project_weight at its current value
  // (which, in a properly configured system, is 0.3).  Their sum will be
  // 0.9 + 0.3 = 1.2, which violates the must-equal-1.0 rule in validateWeights().
  await page.locator('#config-homework_weight').fill('0.9')

  // Attempt to save.
  await page.click('button.save-button')

  // The client-side weight validation error must appear in .weight-error.
  // This is rendered by the v-if="weightError" block in SystemConfigView.vue.
  const weightError = page.locator('.weight-error')
  await expect(weightError).toBeVisible()
  await expect(weightError).toContainText('homework_weight + project_weight must equal 1.0')

  // The success banner must NOT appear, confirming the save was blocked.
  await expect(page.locator('.success-banner')).not.toBeVisible()

  // Restore original values so the stack is left in its pre-test state.
  await page.locator('#config-homework_weight').fill(originalHomeworkWeight)
  await page.locator('#config-project_weight').fill(originalProjectWeight)
  await page.click('button.save-button')
  // If the restored values already equal the persisted values changedFields will
  // be empty and no network request is made — the success banner may or may not
  // appear.  Either way the weight error must be gone.
  await expect(weightError).not.toBeVisible()
})
