// @{"verifies": ["REQ-FECOURSE-028", "REQ-FECOURSE-056", "REQ-FECOURSE-057",
//                "REQ-FECOURSE-221", "REQ-FECOURSE-223", "REQ-FECOURSE-225",
//                "REQ-FECOURSE-490", "REQ-FECOURSE-491", "REQ-FECOURSE-590",
//                "REQ-FECOURSE-629", "REQ-FECONTENT-225"]}
//
// processing-indicators.spec.ts — Sprint 12 task 12.3 (updated Sprint 17 fast-follow)
//
// AI-free tier specs for processing indicators and error paths shipped in
// Sprint 11 and the Sprint 11 fast-follow.  Every spec in this file incurs
// zero intentional Claude API cost:
//
//   - Course creation fires one async kickoff call per run (unavoidable and
//     accepted; counted in the per-run total).
//   - markKickoffSent() blocks the kickoff goroutine for specs that seed chat.
//   - No real chat messages are sent; page.route() is used to fulfill or abort
//     HTTP requests at the browser network layer.
//
// BOUNDED-WAIT HINT — NOT e2e'd here:
//   The 120 s (intake) and 180 s (syllabus) bounded-wait timeouts are already
//   covered by Vitest unit tests in:
//     frontend/src/views/IntakeChatView.test.ts
//       (test: "shows bounded-wait hint after 120 seconds with no messages")
//     frontend/src/views/SyllabusView.test.ts
//       (test: "shows longer-than-expected message after 180s of drafting polling")
//   Running those limits in an e2e spec would require 2–3 minute waits per
//   run; unit tests with fake timers are the right tool for those behaviors.
//
// NAVIGATION PATTERN (why we avoid page.goto):
//   The auth token lives only in the Pinia store's in-memory state.  A hard
//   page.goto() or page.reload() clears that memory and bounces the router to
//   /login.  We navigate within the SPA using page.click() / page.locator()
//   so the token survives.  See seed-smoke.spec.ts for the canonical example.

import { test, expect, type Page } from '@playwright/test'
import { login, STUDENT_USER, STUDENT_PASS } from './helpers'
import {
  markKickoffSent,
  seedSyllabus,
  setCourseStatus,
  cleanupCourse
} from './seed'

// ---------------------------------------------------------------------------
// Shared utility: archiveOpenCourses
// ---------------------------------------------------------------------------
//
// Withdraws every non-archived course owned by the logged-in student so the
// single-active-course constraint never blocks a run.  Uses page.request (not
// the standalone fixture) so the __Host-csrf cookie is attached automatically.

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

// ---------------------------------------------------------------------------
// Shared setup: login + pre-clean helper
// ---------------------------------------------------------------------------
//
// Returns the bearer token captured from the login response.  Called inside
// each test because Playwright shares no state across test functions.

async function loginAndClean(page: Page): Promise<string> {
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

  await archiveOpenCourses(page, bearerToken!)
  return bearerToken!
}

// ---------------------------------------------------------------------------
// Shared setup: create a course via the UI and return its ID
// ---------------------------------------------------------------------------

async function createCourseViaUI(page: Page, topicSuffix: string): Promise<string> {
  await page.click('button.create-button')
  await expect(page.locator('.modal')).toBeVisible()

  const topic = `E2E ProcessingIndicators ${topicSuffix} ${Date.now()}`
  await page.locator('#course-topic').fill(topic)
  await expect(page.locator('button.submit-button')).toBeEnabled()
  await page.locator('button.submit-button').click()

  await expect(page).toHaveURL(/\/courses\/[0-9a-f-]+\/intake/, { timeout: 15_000 })

  const match = page.url().match(/\/courses\/([0-9a-f-]+)\/intake/)
  expect(match, 'Course ID must be extractable from the intake URL').toBeTruthy()
  return match![1]
}

// ---------------------------------------------------------------------------
// Test 1: IntakePreparingIndicator_VisibleWhileHistoryEmpty
// ---------------------------------------------------------------------------
//
// @{"verifies": ["REQ-FECOURSE-028"]}
//
// Verifies REQ-FECOURSE-028: the frontend shall indicate that course
// preparation is in progress while the intake conversation is empty.
//
// Strategy:
//   1. Create course via UI → land on intake page.
//   2. Immediately call markKickoffSent() to prevent the kickoff goroutine
//      from inserting a message (so history stays empty).
//   3. Assert that the ".preparing-label" text indicator is visible within
//      ~3 s of landing.  The component calls loadChatHistory() on mount;
//      an empty response triggers startHistoryPolling() which sets
//      isHistoryPolling=true and renders the preparing indicator.
//
// The kickoff Claude call always takes longer than 3 s on the real API, so
// even without markKickoffSent the history would be empty at first paint.
// markKickoffSent is called as a defence-in-depth measure to ensure we do
// not observe a race if the server is unusually fast.
//
// AI cost per run: 1 course creation kickoff call (accepted).

// @{"verifies": ["REQ-FECOURSE-028"]}
test('IntakePreparingIndicator_VisibleWhileHistoryEmpty', async ({ page }) => {
  const bearerToken = await loginAndClean(page)
  let courseId: string | null = null

  try {
    courseId = await createCourseViaUI(page, 'Preparing')

    // Block the kickoff goroutine immediately to keep history empty.
    // This must be called BEFORE the component's polling can see a message.
    markKickoffSent(courseId)

    // The intake page has already loaded (we just navigated there via the
    // submit button redirect).  The component calls loadChatHistory() on
    // mount.  An empty response triggers startHistoryPolling() which sets
    // isHistoryPolling=true — rendering the preparing indicator:
    //   <div v-if="isHistoryPolling && messages.length === 0" class="message message--agent">
    //     <div class="message-bubble typing-indicator">...</div>
    //     <div class="preparing-label">Your professor is preparing your course…</div>
    //   </div>

    await expect(
      page.locator('.preparing-label'),
      'preparing-label must be visible while history is empty'
    ).toBeVisible({ timeout: 3_000 })

    // Also confirm the typing indicator dots are rendered alongside the label.
    await expect(
      page.locator('.message.message--agent .typing-indicator'),
      'typing-indicator must be visible alongside preparing label'
    ).toBeVisible({ timeout: 3_000 })
  } finally {
    if (courseId) await archiveOpenCourses(page, bearerToken)
  }
})

// ---------------------------------------------------------------------------
// Test 2: IntakeSendFailure_ErrorBannerShownAndDismissible
// ---------------------------------------------------------------------------
//
// @{"verifies": ["REQ-FECOURSE-225", "REQ-FECOURSE-221", "REQ-FECOURSE-223"]}
//
// Verifies:
//   REQ-FECOURSE-225: display an error message and re-enable input when the
//     chat POST returns a non-200 status.
//   REQ-FECOURSE-223: append the student message optimistically on submit.
//   REQ-FECOURSE-221: input disabled while POST in-flight; re-enabled after.
//
// Strategy: use page.route() to FULFILL every POST to the chat endpoint with
// a 500 Internal Server Error.  The response is delivered synchronously by
// Playwright's network interception so no real server call is made and no AI
// cost is incurred.  The fetch() call returns a non-2xx response, the
// api/client.ts wrapper creates ApiError(500, ...), and the view's catch
// branch displays "Failed to send message. Please check your connection and
// try again."
//
// WHY fulfill(500) not abort: route.abort() throws a TypeError (network
// error) in the browser's fetch, which is caught by the `else` branch in
// sendMessage() → "An unexpected error occurred".  We want the ApiError
// branch to exercise REQ-FECOURSE-225 properly.
//
// markKickoffSent is called after course creation per the README rule.

// @{"verifies": ["REQ-FECOURSE-225", "REQ-FECOURSE-221", "REQ-FECOURSE-223"]}
test('IntakeSendFailure_ErrorBannerShownAndDismissible', async ({ page }) => {
  const bearerToken = await loginAndClean(page)
  let courseId: string | null = null

  try {
    courseId = await createCourseViaUI(page, 'SendFailure')

    // Block the kickoff goroutine.  Per the README rule, this must be called
    // before any assertion that depends on a deterministic message count.
    markKickoffSent(courseId)

    // Intercept POST requests to the chat endpoint and fulfill with 500.
    // The response body is valid JSON so that response.json() succeeds in the
    // client wrapper; the 500 status causes ApiError(500, ...) to be thrown.
    await page.route('**/api/v1/courses/*/chat', route =>
      route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'internal server error' })
      })
    )

    // The chat input starts enabled (no POST in-flight yet).
    const chatInput = page.locator('.chat-input')
    const sendButton = page.locator('.send-button')

    await expect(chatInput).toBeEnabled()
    await expect(sendButton).toBeEnabled()

    const MESSAGE_TEXT = 'Hello, this message will fail to send.'
    await chatInput.fill(MESSAGE_TEXT)
    await sendButton.click()

    // REQ-FECOURSE-223: optimistic user bubble must appear immediately
    // (before the POST response is processed).
    await expect(
      page.locator('.message.message--user .message-bubble', { hasText: MESSAGE_TEXT }),
      'optimistic user message bubble must be visible after Send'
    ).toBeVisible({ timeout: 5_000 })

    // REQ-FECOURSE-225: error banner must appear after the 500 POST response.
    await expect(
      page.locator('.send-error'),
      '.send-error banner must be visible after POST failure'
    ).toBeVisible({ timeout: 5_000 })

    // The banner must contain the ApiError error message from IntakeChatView.
    // When error instanceof ApiError, sendErrorMessage =
    // 'Failed to send message. Please check your connection and try again.'
    await expect(
      page.locator('.send-error'),
      'error banner must contain the send-failure message'
    ).toContainText('Failed to send message', { timeout: 5_000 })

    // REQ-FECOURSE-221 / REQ-FECOURSE-225: input must be re-enabled after
    // the error resolves (POST is no longer in-flight; retry is allowed).
    await expect(
      chatInput,
      '.chat-input must be re-enabled after POST failure'
    ).toBeEnabled({ timeout: 5_000 })

    await expect(
      sendButton,
      '.send-button must be re-enabled after POST failure'
    ).toBeEnabled({ timeout: 5_000 })

    // The optimistic message must still be present (not removed on error).
    await expect(
      page.locator('.message.message--user .message-bubble', { hasText: MESSAGE_TEXT }),
      'optimistic user message bubble must remain after error (retry available)'
    ).toBeVisible()

    // Dismiss the error banner and verify it disappears.
    await page.locator('.error-dismiss').click()
    await expect(
      page.locator('.send-error'),
      '.send-error banner must be hidden after dismiss'
    ).not.toBeVisible({ timeout: 5_000 })
  } finally {
    if (courseId) await archiveOpenCourses(page, bearerToken)
  }
})

// ---------------------------------------------------------------------------
// Test 3: SyllabusDrafting_IndicatorThenAutoRender
// ---------------------------------------------------------------------------
//
// @{"verifies": ["REQ-FECOURSE-056", "REQ-FECOURSE-057",
//                "REQ-FECOURSE-490", "REQ-FECOURSE-491",
//                "REQ-FECOURSE-629", "REQ-FECONTENT-225"]}
//
// Verifies:
//   REQ-FECOURSE-056 / REQ-FECOURSE-490: drafting indicator shown while
//     syllabus is not yet available (404 from GET /syllabus).
//   REQ-FECOURSE-057 / REQ-FECOURSE-491: frontend polls and auto-renders
//     the syllabus once it becomes available (no manual reload needed).
//   REQ-FECOURSE-629 / REQ-FECONTENT-225: syllabus AsciiDoc content is
//     rendered as HTML (via renderAsciidoc) — DOM shows real heading
//     elements (h1/h2), not raw AsciiDoc markup in a <pre> block.
//
// Strategy:
//   1. Create course → markKickoffSent → setCourseStatus('syllabus_draft').
//   2. Navigate in-SPA to /courses/{id}/syllabus:
//      - Click "Courses" nav link → dashboard fetches fresh course list.
//      - Click course card → CourseDashboardView.navigateToCourse() pushes
//        /courses/{id}/syllabus because status=syllabus_draft.
//   3. Wait for dashboard to show the course card with "syllabus_draft" badge
//      before clicking, to ensure the fresh fetch has completed.
//   4. Assert .drafting-indicator is visible and .error-message is NOT.
//   5. Seed a real AsciiDoc syllabus via seedSyllabus().
//   6. Wait up to 12 s for .syllabus-content to appear automatically (4 s
//      poll interval; allow 3 intervals to absorb jitter).  No manual reload.
//   7. Assert that the AsciiDoc heading (== Week 1) rendered as an <h2>
//      element inside .syllabus-rendered — NOT as raw "== Week 1" text.
//
// SyllabusView polls every 4000 ms.  On 404, isDrafting=true and polling
// starts.  Once the syllabus row exists (seeded via psql), the next poll
// returns 200 and syllabusHtml is set via renderAsciidoc(), revealing
// .syllabus-content > .syllabus-rendered.
//
// DOM structure (post Sprint 17):
//   .syllabus-content
//     .syllabus-rendered.adoc-content   ← v-html="syllabusHtml"
//       h1                              ← from "= [SEED] ..." AsciiDoc title
//       div.sect1
//         h2                            ← from "== Week 1" section heading
//         ...
//
// AI cost: 1 kickoff call from course creation (accepted).

// @{"verifies": ["REQ-FECOURSE-056", "REQ-FECOURSE-057", "REQ-FECOURSE-490", "REQ-FECOURSE-491", "REQ-FECOURSE-629", "REQ-FECONTENT-225"]}
test('SyllabusDrafting_IndicatorThenAutoRender', async ({ page }) => {
  const bearerToken = await loginAndClean(page)
  let courseId: string | null = null

  try {
    courseId = await createCourseViaUI(page, 'Drafting')

    // Block kickoff goroutine.  This spec seeds no chat messages but follows
    // the rule as defence-in-depth when navigating away from the intake page.
    markKickoffSent(courseId)

    // Set course status to syllabus_draft so the syllabus view's initial
    // GET /syllabus returns 404 (no syllabus row yet) and starts polling.
    setCourseStatus(courseId, 'syllabus_draft')

    // Navigate in-SPA to the syllabus view using the two-step approach:
    //   1. Click "Courses" nav link → dashboard mounts, fetches fresh courses.
    //   2. Wait for the course card to show the updated "syllabus_draft" badge
    //      (confirms the dashboard fetch completed with the new status).
    //   3. Click the course card → router pushes /courses/{id}/syllabus.
    await page.click('nav.nav-links a[href="/courses"]')
    await expect(page).toHaveURL(/\/courses$/, { timeout: 10_000 })

    // Wait for the course card with "syllabus_draft" status to appear.
    // CourseDashboardView renders a .status-badge with the course status text.
    // This confirms that the dashboard's fetchCourses() has completed and the
    // store holds the updated syllabus_draft status before we click.
    await expect(
      page.locator('.course-card .status-badge', { hasText: 'syllabus_draft' }),
      'course card must show syllabus_draft status before clicking'
    ).toBeVisible({ timeout: 10_000 })

    // Click the course card.  With status=syllabus_draft, navigateToCourse()
    // maps to /courses/{id}/syllabus.
    await page.locator('.course-card').first().click()
    await expect(page).toHaveURL(/\/courses\/[0-9a-f-]+\/syllabus/, { timeout: 10_000 })

    // ---- Assert drafting indicator is visible --------------------------------
    //
    // SyllabusView.vue renders when isDrafting===true:
    //   <div v-if="isDrafting && !isDraftingWaitExceeded" class="drafting-indicator">
    //     <div class="typing-indicator"><span/><span/><span/></div>
    //     <p>Your professor is drafting your syllabus…</p>
    //   </div>
    //
    // fetchSyllabus() calls GET /syllabus, gets 404, sets isDrafting=true.

    await expect(
      page.locator('.drafting-indicator'),
      '.drafting-indicator must be visible while no syllabus exists'
    ).toBeVisible({ timeout: 5_000 })

    await expect(
      page.locator('.drafting-indicator'),
      'drafting indicator must say professor is drafting the syllabus'
    ).toContainText('drafting your syllabus', { timeout: 5_000 })

    // Assert the error-message banner is NOT present (this is a drafting
    // state, not an error state — REQ-FECOURSE-490 distinguishes the two).
    await expect(
      page.locator('.error-message'),
      '.error-message must NOT be present while in drafting state'
    ).not.toBeVisible()

    // ---- Seed the syllabus row -----------------------------------------------
    //
    // Real AsciiDoc fixture.  Asciidoctor converts:
    //   "= [SEED] Test Syllabus"  → <h1> document title
    //   "== Week 1"               → <h2> section heading (inside div.sect1)
    //   "== Week 2"               → second <h2> section heading
    // The fixture uses AsciiDoc section syntax so that the h2 assertion below
    // proves the AsciiDoc-to-HTML pipeline fired (REQ-FECOURSE-629).
    const SYLLABUS_FIXTURE = [
      '= [SEED] Test Syllabus',
      '',
      '== Week 1',
      '',
      'Introduction to the subject matter.',
      '',
      '== Week 2',
      '',
      'Deep dive into the core concepts.'
    ].join('\n')

    seedSyllabus(courseId, SYLLABUS_FIXTURE)

    // ---- Wait for auto-render (no manual reload) ----------------------------
    //
    // SyllabusView polls every 4000 ms.  The syllabus row now exists, so the
    // next GET /syllabus returns 200 and calls renderAsciidoc() which sets
    // syllabusHtml, revealing .syllabus-content > .syllabus-rendered.
    // Allow 12 s (3 poll intervals) to absorb jitter.
    //
    // REQ-FECOURSE-629 / REQ-FECONTENT-225: the rendered container is
    // .syllabus-rendered (v-html="syllabusHtml"), NOT the old .syllabus-text
    // pre (which was a raw-text block).

    await expect(
      page.locator('.syllabus-content'),
      '.syllabus-content wrapper must appear automatically after seedSyllabus (polling)'
    ).toBeVisible({ timeout: 12_000 })

    await expect(
      page.locator('.syllabus-rendered'),
      '.syllabus-rendered container must be visible once AsciiDoc is rendered'
    ).toBeVisible({ timeout: 5_000 })

    // REQ-FECOURSE-629 / REQ-FECONTENT-225: AsciiDoc "== Week 1" must produce
    // an <h2> DOM element inside .syllabus-rendered — not the raw text "== Week 1".
    // Asciidoctor wraps sections in <div class="sect1"><h2>...</h2>...</div>.
    await expect(
      page.locator('.syllabus-rendered h2').first(),
      '.syllabus-rendered must contain at least one h2 element from the == section heading'
    ).toBeVisible({ timeout: 5_000 })

    // The raw AsciiDoc markup must NOT appear as visible text.
    // If Asciidoctor ran, "== Week 1" is in an h2, not as literal "==" text.
    await expect(
      page.locator('.syllabus-rendered'),
      'raw "== Week 1" AsciiDoc markup must not be visible as text (must be rendered to h2)'
    ).not.toContainText('== Week 1')

    // Assert the drafting indicator is gone once content is available.
    await expect(
      page.locator('.drafting-indicator'),
      '.drafting-indicator must disappear once syllabus is rendered'
    ).not.toBeVisible()
  } finally {
    if (courseId) {
      cleanupCourse(courseId)
      await archiveOpenCourses(page, bearerToken)
    }
  }
})

// ---------------------------------------------------------------------------
// Test 4: SyllabusFetchFailure_NonDraftErrorStillShown
// ---------------------------------------------------------------------------
//
// @{"verifies": ["REQ-FECOURSE-490", "REQ-FECOURSE-590"]}
//
// Verifies:
//   REQ-FECOURSE-490: when a non-404 error occurs (e.g. 500), the error
//     banner is shown — NOT the drafting indicator.
//   REQ-FECOURSE-590: fetch failures surface a clear error message.
//
// Strategy: create a course in syllabus_draft status, use page.route() to
// fulfill GET /syllabus with status 500 before navigating to the syllabus
// view.  SyllabusView.fetchSyllabus() catches any non-404 ApiError and sets
// fetchError, rendering .error-message.
//
// WHY 500 and not abort: same as Test 2 — we want the ApiError branch.
//
// AI cost: 1 kickoff call from course creation (accepted).

// @{"verifies": ["REQ-FECOURSE-490", "REQ-FECOURSE-590"]}
test('SyllabusFetchFailure_NonDraftErrorStillShown', async ({ page }) => {
  const bearerToken = await loginAndClean(page)
  let courseId: string | null = null

  try {
    courseId = await createCourseViaUI(page, 'FetchFailure')

    markKickoffSent(courseId)
    setCourseStatus(courseId, 'syllabus_draft')

    // Intercept GET /syllabus and return a 500 Internal Server Error.
    // SyllabusView.fetchSyllabus() branches on status code:
    //   - 404  → isDrafting = true  (show drafting indicator)
    //   - other → fetchError = '...' (show error banner)
    await page.route('**/api/v1/courses/*/syllabus', route =>
      route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'internal server error' })
      })
    )

    // Navigate in-SPA to the syllabus view.
    await page.click('nav.nav-links a[href="/courses"]')
    await expect(page).toHaveURL(/\/courses$/, { timeout: 10_000 })

    // Wait for the course card to show the updated status before clicking.
    await expect(
      page.locator('.course-card .status-badge', { hasText: 'syllabus_draft' }),
      'course card must show syllabus_draft status before clicking'
    ).toBeVisible({ timeout: 10_000 })

    await page.locator('.course-card').first().click()
    await expect(page).toHaveURL(/\/courses\/[0-9a-f-]+\/syllabus/, { timeout: 10_000 })

    // The error banner must be visible.
    // SyllabusView sets fetchError.value = 'Failed to load syllabus...'
    // and renders it in <div v-if="fetchError" class="error-message">.
    await expect(
      page.locator('.error-message'),
      '.error-message banner must be visible on a 500 fetch failure'
    ).toBeVisible({ timeout: 5_000 })

    await expect(
      page.locator('.error-message'),
      'error banner must contain the fetch-failure message'
    ).toContainText('Failed to load syllabus', { timeout: 5_000 })

    // The drafting indicator must NOT be shown on a non-404 error.
    await expect(
      page.locator('.drafting-indicator'),
      '.drafting-indicator must NOT appear on a non-404 fetch error'
    ).not.toBeVisible()
  } finally {
    if (courseId) {
      cleanupCourse(courseId)
      await archiveOpenCourses(page, bearerToken)
    }
  }
})
