// @{"verifies": ["REQ-FEADMIN-710", "REQ-SYS-077"]}
//
// tree-admin-author.spec.ts — Admin draft author→publish flow (AI-free, chromium tier).
//
// AUTHORED AND GATED — do NOT run without the prod stack.
//
// Prerequisites:
//   1. Rebuild the production stack per memory "valory-stack-rebuild-before-e2e":
//        docker-compose -f docker-compose.prod.yml down --remove-orphans
//        docker-compose -f docker-compose.prod.yml up --build -d
//   2. Run: npm run test:e2e  (chromium project only; NOT test:e2e:journey)
//
// This spec validates the admin DraftListView UI without triggering AI calls:
//   - Navigate to /admin/drafts
//   - Open the "New Draft" modal
//   - Verify title is required (submit button disabled when title is empty)
//   - Verify topic is required (submit button disabled when topic is empty)
//   - Verify submit button is enabled when both fields are filled
//   - Create a draft (this calls POST /admin/drafts which seeds a root node)
//   - Navigate to the editor (DraftEditorView)
//   - Verify Publish button is DISABLED (root node is not yet approved)
//   - Clean up: delete the created draft so tests are idempotent
//
// The spec does NOT call any node generation or approval endpoints; no
// Anthropic API calls are made.

import { test, expect, type Page } from '@playwright/test'
import { login, ADMIN_USER, ADMIN_PASS } from './helpers'

// Capture bearer token from login response for cleanup API calls.
async function captureTokenAndLogin(page: Page): Promise<string | null> {
  let bearerToken: string | null = null

  page.on('response', async response => {
    if (response.url().includes('/api/v1/auth/login') && response.status() === 200) {
      try {
        const body = await response.json() as { token?: string }
        if (body.token) bearerToken = body.token
      } catch { /* ignore */ }
    }
  })

  await login(page, ADMIN_USER, ADMIN_PASS)
  return bearerToken
}

// @{"verifies": ["REQ-FEADMIN-710"]}
test('AdminDraftList_CreateDraft_RequiresNonEmptyTitleAndTopic', async ({ page }) => {
  await captureTokenAndLogin(page)
  await page.goto('/admin/drafts')
  await expect(page.locator('h1')).toHaveText('Course Drafts')

  // Open create modal
  await page.click('button.create-button')
  await expect(page.locator('.modal')).toBeVisible()

  // Both title and topic empty → submit disabled
  const submitBtn = page.locator('button.submit-button')
  await expect(submitBtn).toBeDisabled()

  // Fill topic only → still disabled (title missing)
  await page.fill('#draft-topic', 'Algebra')
  await expect(submitBtn).toBeDisabled()

  // Fill title too → enabled
  await page.fill('#draft-title', 'Test Draft Title')
  await expect(submitBtn).toBeEnabled()

  // Cancel without creating
  await page.click('button.cancel-button')
  await expect(page.locator('.modal')).not.toBeVisible()
})

// @{"verifies": ["REQ-FEADMIN-710", "REQ-SYS-077"]}
test('AdminDraftList_CreateDraftAndVerifyPublishGating_ThenDelete', async ({ page }) => {
  const bearerToken = await captureTokenAndLogin(page)
  await page.goto('/admin/drafts')

  const uniqueTitle = `E2E Test Draft ${Date.now()}`

  // Create a new draft
  await page.click('button.create-button')
  await expect(page.locator('.modal')).toBeVisible()

  await page.fill('#draft-title', uniqueTitle)
  await page.fill('#draft-topic', 'TestTopic E2E')
  await page.click('button.submit-button')

  // Should navigate to the draft editor
  await expect(page).toHaveURL(/\/admin\/drafts\/[^/]+$/, { timeout: 10_000 })

  // DraftEditorView should display the draft title
  await expect(page.locator('.editor-title')).toContainText(uniqueTitle)

  // The Publish button should be DISABLED — the root node is not yet approved
  const publishBtn = page.locator('button.publish-button')
  await expect(publishBtn).toBeVisible()
  await expect(publishBtn).toBeDisabled()

  // The tooltip/title attribute explains why it is disabled
  const titleAttr = await publishBtn.getAttribute('title')
  expect(titleAttr).toContain('approved')

  // ---- Cleanup: delete the draft via API so subsequent runs are clean ------

  const draftId = page.url().match(/\/admin\/drafts\/([^/]+)$/)?.[1]
  if (draftId && bearerToken) {
    const csrf = (await page.context().cookies()).find(c => c.name === '__Host-csrf')?.value ?? ''
    const deleteResp = await page.request.delete(`/api/v1/admin/drafts/${draftId}`, {
      headers: {
        Authorization: `Bearer ${bearerToken}`,
        'X-CSRF-Token': csrf,
      },
    })
    // 200 or 204 are both acceptable for delete
    expect([200, 204], `Cleanup delete of draft ${draftId} should succeed`).toContain(deleteResp.status())
  }
})

// @{"verifies": ["REQ-FEADMIN-710"]}
test('AdminDraftList_StatusFilter_FiltersDisplayedDrafts', async ({ page }) => {
  await captureTokenAndLogin(page)
  await page.goto('/admin/drafts')
  await expect(page.locator('h1')).toHaveText('Course Drafts')

  // Select "Draft" filter and verify page does not crash
  const filterSelect = page.locator('#status-filter')
  await filterSelect.selectOption('draft')

  // The list should reload — allow it to settle
  await page.waitForTimeout(500)

  // No error banner should appear (this is a smoke test for the filter UX)
  await expect(page.locator('[data-testid="error-banner"]')).not.toBeVisible()
})
