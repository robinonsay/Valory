// @{"verifies": ["REQ-FECOURSE-001", "REQ-FECOURSE-002", "REQ-FECOURSE-003", "REQ-FECOURSE-010", "REQ-FECOURSE-011", "REQ-FECOURSE-012", "REQ-FECOURSE-300", "REQ-FECOURSE-301", "REQ-COURSE-001", "REQ-COURSE-003"]}
//
// student-course.spec.ts — Course creation and cleanup:
//   • New course modal opens when "New course" is clicked.
//   • Create button is disabled while the topic input is empty.
//   • Typing a topic and submitting navigates to /courses/<uuid>/intake.
//   • The test does NOT send any intake chat messages — no AI tokens are consumed.
//   • Cleanup via POST /api/v1/courses/:id/withdraw (REQ-COURSE-003) so the
//     single-active-course constraint does not block the next run.
//
// If the student account already has an active course (e.g. a previous failed
// run did not clean up), the modal will show the "You already have an active
// course" 409 error.  The test detects and documents this case explicitly
// rather than silently passing.

import { test, expect, type Page } from '@playwright/test'
import { login, STUDENT_USER, STUDENT_PASS } from './helpers'

// archiveOpenCourses withdraws every non-archived course owned by the
// logged-in student so the single-active-course constraint can never block a
// run. It uses page.request (NOT the standalone request fixture) because
// page.request shares the browser context's cookie jar — the __Host-csrf
// cookie set at login is attached automatically, and we mirror it into the
// X-CSRF-Token header to satisfy the double-submit CSRF check. The standalone
// fixture has no cookies, so its withdraw calls 403'd (csrf_token_missing)
// and silently leaked an active course into the next run.
async function archiveOpenCourses(page: Page, bearerToken: string): Promise<void> {
  const csrf = (await page.context().cookies()).find(c => c.name === '__Host-csrf')?.value ?? ''
  const list = await page.request.get('/api/v1/courses', {
    headers: { Authorization: `Bearer ${bearerToken}` }
  })
  if (!list.ok()) return
  const body = await list.json() as { courses: { id: string; status: string }[] | null }
  for (const c of body.courses ?? []) {
    if (c.status !== 'archived' && c.status !== 'completed') {
      const resp = await page.request.post(`/api/v1/courses/${c.id}/withdraw`, {
        headers: { Authorization: `Bearer ${bearerToken}`, 'X-CSRF-Token': csrf },
        data: {}
      })
      expect(resp.status(), `withdraw of stale course ${c.id} must succeed`).toBe(200)
    }
  }
}

// @{"verifies": ["REQ-FECOURSE-001", "REQ-FECOURSE-010", "REQ-FECOURSE-011", "REQ-FECOURSE-012", "REQ-FECOURSE-300", "REQ-FECOURSE-301", "REQ-COURSE-001", "REQ-COURSE-003"]}
test('StudentCourse_CreateNewCourse_NavigatesToIntakeAndCleansUp', async ({ page, request }) => {
  // Intercept the login response to capture the bearer token.  We need it to
  // call the withdraw endpoint directly via page.request without going through
  // the UI (intake chat is off-limits because it would trigger AI agent calls).
  let bearerToken: string | null = null

  page.on('response', async response => {
    if (response.url().includes('/api/v1/auth/login') && response.status() === 200) {
      try {
        const body = await response.json() as { token?: string }
        if (body.token) {
          bearerToken = body.token
        }
      } catch {
        // Ignore parse errors
      }
    }
  })

  await login(page, STUDENT_USER, STUDENT_PASS)
  await expect(page).toHaveURL(/\/courses/)

  // Self-heal: archive anything a previous failed run left behind BEFORE
  // creating, then reload the dashboard state by re-navigating via the nav.
  expect(bearerToken, 'Bearer token must have been captured from login response').not.toBeNull()
  await archiveOpenCourses(page, bearerToken!)

  // ---- Open the New course modal ------------------------------------------

  await page.click('button.create-button')
  await expect(page.locator('.modal')).toBeVisible()

  // ---- Verify Create button is disabled with empty topic ------------------

  const createBtn = page.locator('button.submit-button')
  // The modal form input id is "course-topic" (CourseDashboardView.vue).
  const topicInput = page.locator('#course-topic')
  await expect(topicInput).toBeVisible()

  // When the input is empty the submit button must be disabled.
  // CourseDashboardView.vue: :disabled="isCreating || newCourseTopic.trim() === ''"
  await expect(createBtn).toBeDisabled()

  // ---- Type a topic -------------------------------------------------------

  const topic = `E2E Demo Topic ${Date.now()}`
  await topicInput.fill(topic)

  // With a non-empty topic the button must become enabled.
  await expect(createBtn).toBeEnabled()

  // ---- Submit and assert navigation to intake -----------------------------

  await createBtn.click()

  // Check for the 409 "already active course" error first (stale cleanup from a
  // previous run).  If it appears, document the situation and fail clearly.
  const errorDiv = page.locator('.modal .error-message')
  const is409 = await errorDiv.isVisible().catch(() => false)
  if (is409) {
    const errorText = await errorDiv.textContent()
    throw new Error(
      `CLEANUP REQUIRED: The demo_student account already has an active course. ` +
      `The previous test run did not clean up.  Delete or withdraw the active ` +
      `course manually via psql or the admin Course Oversight view, then re-run. ` +
      `Error from server: "${errorText?.trim()}"`
    )
  }

  // Successful creation: the router pushes /courses/<uuid>/intake.
  await expect(page).toHaveURL(/\/courses\/[0-9a-f-]+\/intake/, { timeout: 15_000 })

  // Extract the course UUID from the URL for cleanup.
  const url = page.url()
  const match = url.match(/\/courses\/([0-9a-f-]+)\/intake/)
  expect(match, 'Could not extract course ID from URL for cleanup').toBeTruthy()
  const courseId = match![1]

  // ---- Cleanup: withdraw the course via the API ---------------------------
  // Same CSRF-correct path as the pre-clean; asserts success so a cleanup
  // regression fails loudly instead of leaking state into the next run.
  await archiveOpenCourses(page, bearerToken!)
})
