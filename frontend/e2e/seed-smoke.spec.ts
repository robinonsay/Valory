// @{"verifies", ["REQ-FECOURSE-023", "REQ-FECOURSE-230", "REQ-FECOURSE-260", "REQ-FECOURSE-261"]}
//
// seed-smoke.spec.ts — Proves the DB seeding helpers work end-to-end.
//
// Flow:
//   1. Login and pre-clean any stale active course.
//   2. Create a course via the UI (no AI tokens: no chat POST sent).
//   3. Call markKickoffSent() to prevent the background kickoff goroutine from
//      inserting a duplicate assistant opening message.
//   4. Seed one deterministic assistant message via seedChatMessage().
//   5. Navigate in-SPA back to the course dashboard (to force unmount of
//      IntakeChatView), then click the course card to navigate back to the
//      intake page (re-mount triggers a fresh GET /chat/history).
//   6. Assert the seeded message is rendered as a .message--agent bubble.
//   7. Clean up via archiveOpenCourses (the CSRF-correct self-healing path).
//
// WHY in-SPA navigation instead of page.reload():
//   The auth token lives only in the Pinia store's in-memory state.  A hard
//   page.goto() or page.reload() would destroy that memory, clearing the token
//   and bouncing the router to /login.  We instead navigate within the SPA
//   using page.click() so the token survives.  IntakeChatView calls
//   loadChatHistory() on mount, so each re-mount produces a fresh history fetch.
//
// COST: zero AI calls.  markKickoffSent() prevents the kickoff goroutine from
// firing; the spec never sends a chat message.

import { test, expect, type Page } from '@playwright/test'
import { login, STUDENT_USER, STUDENT_PASS } from './helpers'
import { markKickoffSent, seedChatMessage, cleanupCourse } from './seed'

// Unique fixture content that will not collide with any real AI output.
// The marker string format makes it trivially searchable in CI logs.
const SEEDED_CONTENT = '[SEED-SMOKE] Hello from the seeding helper.'

// archiveOpenCourses is inlined here per existing spec convention: each spec
// is self-contained so the cleanup path is explicit rather than hidden in a
// shared import.
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

// @{"verifies", ["REQ-FECOURSE-023", "REQ-FECOURSE-230", "REQ-FECOURSE-260", "REQ-FECOURSE-261"]}
test('SeedSmoke_SeededAssistantMessageRendersOnIntakePage', async ({ page }) => {
  // ---- Capture bearer token from login ------------------------------------
  let bearerToken: string | null = null
  page.on('response', async response => {
    if (response.url().includes('/api/v1/auth/login') && response.status() === 200) {
      try {
        const body = await response.json() as { token?: string }
        if (body.token) bearerToken = body.token
      } catch { /* ignore parse errors */ }
    }
  })

  await login(page, STUDENT_USER, STUDENT_PASS)
  await expect(page).toHaveURL(/\/courses/)
  expect(bearerToken, 'Bearer token must be captured from login response').not.toBeNull()

  // ---- Pre-clean: archive any stale active course -------------------------
  await archiveOpenCourses(page, bearerToken!)

  // Everything from course creation onward runs inside try/finally so an
  // assertion failure can never strand an active course (the single-active-
  // course constraint would block every subsequent run).
  try {
  // ---- Create a new course via the UI -------------------------------------
  await page.click('button.create-button')
  await expect(page.locator('.modal')).toBeVisible()

  const topic = `SeedSmoke ${Date.now()}`
  await page.locator('#course-topic').fill(topic)
  await expect(page.locator('button.submit-button')).toBeEnabled()
  await page.locator('button.submit-button').click()

  await expect(page).toHaveURL(/\/courses\/[0-9a-f-]+\/intake/, { timeout: 15_000 })

  // Extract the course UUID from the URL for seeding and cleanup.
  const intakeUrl = page.url()
  const match = intakeUrl.match(/\/courses\/([0-9a-f-]+)\/intake/)
  expect(match, 'Course ID must be extractable from the intake URL').toBeTruthy()
  const courseId = match![1]

  // ---- Block the kickoff goroutine BEFORE seeding -------------------------
  // markKickoffSent sets intake_kickoff_sent=true and intake_kickoff_attempts=3
  // so the background goroutine cannot insert an assistant opening message
  // after we seed our own.  Must be called BEFORE seedChatMessage().
  markKickoffSent(courseId)

  // ---- Seed one deterministic assistant message directly into the DB ------
  seedChatMessage(courseId, 'assistant', SEEDED_CONTENT)

  // ---- Navigate to the course dashboard (in-SPA) --------------------------
  // Clicking the "Courses" nav link navigates within the SPA so the Pinia
  // auth store (and its in-memory bearer token) survives the navigation.
  // This unmounts IntakeChatView, which is required so that the next navigation
  // back to the intake URL produces a fresh mount and a fresh history fetch.
  await page.click('nav.nav-links a[href="/courses"]')
  await expect(page).toHaveURL(/\/courses$/, { timeout: 10_000 })

  // ---- Navigate back to the intake page by clicking the course card -------
  // CourseDashboardView renders a course card for every course.  Click the
  // card for our course (identified by its topic).  The router pushes
  // /courses/{id}/intake because the course status is still 'intake'.
  await page.locator('.course-card', { hasText: topic }).click()
  await expect(page).toHaveURL(/\/courses\/[0-9a-f-]+\/intake/, { timeout: 10_000 })

  // ---- Assert the seeded message appears as an agent bubble ---------------
  // IntakeChatView.vue renders assistant messages with CSS class "message--agent"
  // and the text content inside ".message-bubble".  The view calls
  // loadChatHistory() on mount and populates messages[] from the response.
  // We use a compound selector so we assert both alignment and content.
  const seededBubble = page.locator('.message.message--agent .message-bubble', {
    hasText: SEEDED_CONTENT
  })
  await expect(seededBubble).toBeVisible({ timeout: 10_000 })

  // ---- Cleanup: withdraw the course so the next run is unblocked ----------
  } finally {
    await archiveOpenCourses(page, bearerToken!)
  }
})
