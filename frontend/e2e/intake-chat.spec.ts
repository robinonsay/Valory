// @{"verifies", ["REQ-FECOURSE-260", "REQ-FECOURSE-261", "REQ-FECOURSE-270", "REQ-FECOURSE-026", "REQ-FECOURSE-027"]}
//
// intake-chat.spec.ts — Sprint 11 intake-chat view assertions:
//
//   1. InputVisibility: after course creation lands on /intake, assert that
//      the .chat-input and .send-button bounding boxes are fully inside the
//      viewport WITHOUT any programmatic scrolling.  This is the regression
//      test for the headline demo bug (REQ-FECOURSE-270: input-bar height fix).
//
//   2. HistoryContract: via page.request (authenticated, CSRF-correct), GET
//      /api/v1/courses/{id}/chat/history for the just-created course and assert:
//        • status 200
//        • body.messages is an array (never null — guards against the Sprint 10
//          nil-slice marshalling bug class)
//      Also assert that an unauthenticated request returns 401 (not 200).
//
// COST RULE: no chat messages are sent — a chat POST is a synchronous Claude
// call.  The kickoff goroutine may fire ONE async Claude call per course
// creation; that is accepted and counted in the report.  This spec creates
// exactly ONE course and cleans it up via archiveOpenCourses.

import { test, expect, type Page } from '@playwright/test'
import { login, STUDENT_USER, STUDENT_PASS } from './helpers'

// archiveOpenCourses is duplicated from student-course.spec.ts rather than
// shared through helpers.ts to keep each spec self-contained.  The approach
// is identical: CSRF-correct withdrawal via page.request.
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

// @{"verifies", ["REQ-FECOURSE-270"]}
test('IntakeChat_InputAndSendButtonFullyInsideViewport_NoScrollRequired', async ({ page }) => {
  // Capture the bearer token from the login response for cleanup.
  let bearerToken: string | null = null
  page.on('response', async response => {
    if (response.url().includes('/api/v1/auth/login') && response.status() === 200) {
      try {
        const body = await response.json() as { token?: string }
        if (body.token) bearerToken = body.token
      } catch { /* ignore */ }
    }
  })

  await login(page, STUDENT_USER, STUDENT_PASS)
  await expect(page).toHaveURL(/\/courses/)
  expect(bearerToken, 'Bearer token must be captured from login').not.toBeNull()

  // Pre-clean: archive any stale active course so the 409 path is not hit.
  await archiveOpenCourses(page, bearerToken!)

  // Create a new course via the UI (mirrors student-course.spec.ts).
  await page.click('button.create-button')
  await expect(page.locator('.modal')).toBeVisible()

  const topic = `E2E Intake Viewport Test ${Date.now()}`
  await page.locator('#course-topic').fill(topic)
  await expect(page.locator('button.submit-button')).toBeEnabled()
  await page.locator('button.submit-button').click()

  // Wait for navigation to the intake page.
  await expect(page).toHaveURL(/\/courses\/[0-9a-f-]+\/intake/, { timeout: 15_000 })

  // ---- Input visibility regression (REQ-FECOURSE-270) -------------------

  // Playwright's boundingBox() returns the element's position relative to
  // the top-left corner of the page (not the viewport).  We compare against
  // the viewport dimensions to assert the elements are visible without
  // scrolling.  An element whose top edge is beyond the viewport height is
  // off-screen — the headline demo bug manifested exactly this way.
  const viewport = page.viewportSize()
  expect(viewport, 'Playwright must provide a viewport size').toBeTruthy()
  const { width: vpWidth, height: vpHeight } = viewport!

  // .chat-input: the text input inside .chat-input-area.
  const chatInput = page.locator('.chat-input')
  await expect(chatInput).toBeVisible()
  const inputBox = await chatInput.boundingBox()
  expect(inputBox, '.chat-input bounding box must be obtainable').toBeTruthy()

  // The bottom edge of the input must be <= viewport height (fully visible).
  expect(
    inputBox!.y + inputBox!.height,
    `.chat-input bottom edge (${inputBox!.y + inputBox!.height}px) must be within viewport height (${vpHeight}px)`
  ).toBeLessThanOrEqual(vpHeight)

  // The right edge must be within the viewport width.
  expect(
    inputBox!.x + inputBox!.width,
    `.chat-input right edge must be within viewport width`
  ).toBeLessThanOrEqual(vpWidth)

  // .send-button: the Send button to the right of the input.
  const sendButton = page.locator('.send-button')
  await expect(sendButton).toBeVisible()
  const sendBox = await sendButton.boundingBox()
  expect(sendBox, '.send-button bounding box must be obtainable').toBeTruthy()

  expect(
    sendBox!.y + sendBox!.height,
    `.send-button bottom edge (${sendBox!.y + sendBox!.height}px) must be within viewport height (${vpHeight}px)`
  ).toBeLessThanOrEqual(vpHeight)

  expect(
    sendBox!.x + sendBox!.width,
    `.send-button right edge must be within viewport width`
  ).toBeLessThanOrEqual(vpWidth)

  // ---- Cleanup -----------------------------------------------------------
  await archiveOpenCourses(page, bearerToken!)
})

// @{"verifies", ["REQ-FECOURSE-260", "REQ-FECOURSE-261", "REQ-FECOURSE-026", "REQ-FECOURSE-027"]}
test('IntakeChatHistory_GetHistoryEndpoint_Returns200WithMessagesArray', async ({ page }) => {
  // Capture the bearer token for direct API calls.
  let bearerToken: string | null = null
  page.on('response', async response => {
    if (response.url().includes('/api/v1/auth/login') && response.status() === 200) {
      try {
        const body = await response.json() as { token?: string }
        if (body.token) bearerToken = body.token
      } catch { /* ignore */ }
    }
  })

  await login(page, STUDENT_USER, STUDENT_PASS)
  await expect(page).toHaveURL(/\/courses/)
  expect(bearerToken, 'Bearer token must be captured from login').not.toBeNull()

  // Pre-clean stale courses.
  await archiveOpenCourses(page, bearerToken!)

  // Create a course so we have a valid course ID.
  await page.click('button.create-button')
  await expect(page.locator('.modal')).toBeVisible()

  const topic = `E2E History Contract Test ${Date.now()}`
  await page.locator('#course-topic').fill(topic)
  await page.locator('button.submit-button').click()

  await expect(page).toHaveURL(/\/courses\/[0-9a-f-]+\/intake/, { timeout: 15_000 })

  // Extract the course UUID from the URL.
  const url = page.url()
  const match = url.match(/\/courses\/([0-9a-f-]+)\/intake/)
  expect(match, 'Course ID must be extractable from intake URL').toBeTruthy()
  const courseId = match![1]

  // ---- History endpoint: owner-authenticated request (REQ-FECOURSE-260) -

  const histResp = await page.request.get(`/api/v1/courses/${courseId}/chat/history`, {
    headers: { Authorization: `Bearer ${bearerToken!}` }
  })

  expect(
    histResp.status(),
    'GET /chat/history for owned course must return 200'
  ).toBe(200)

  const histBody = await histResp.json() as { messages: unknown }

  // body.messages must be an array — never null.  The Sprint 10 production bug
  // class was nil slices marshalled as JSON null; this guards the new endpoint
  // against the same regression.  The array may be empty or contain messages
  // depending on whether the async kickoff has completed; we do not assert on
  // content (AI-generated, non-deterministic).
  expect(
    Array.isArray(histBody.messages),
    `body.messages must be an array, got: ${JSON.stringify(histBody.messages)}`
  ).toBe(true)

  // ---- History endpoint: unauthenticated request returns 401 -------------

  // An unauthenticated caller must not receive a 200.  The backend returns 401
  // when no bearer token is presented (auth middleware rejects before the
  // ownership check).
  const unauthedResp = await page.request.get(`/api/v1/courses/${courseId}/chat/history`)

  expect(
    unauthedResp.status(),
    'GET /chat/history without auth must not return 200'
  ).not.toBe(200)

  // The spec accepts 401 (no token) or 403 (wrong owner).  The backend returns
  // 401 when no Authorization header is present.
  expect(
    [401, 403],
    `GET /chat/history without auth must return 401 or 403, got ${unauthedResp.status()}`
  ).toContain(unauthedResp.status())

  // ---- Cleanup -----------------------------------------------------------
  await archiveOpenCourses(page, bearerToken!)
})
