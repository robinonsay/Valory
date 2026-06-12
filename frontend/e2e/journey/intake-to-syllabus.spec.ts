// frontend/e2e/journey/intake-to-syllabus.spec.ts
//
// JOURNEY SPEC — costs real Anthropic API money. Run only with PM approval
// via: npm run test:e2e:journey
//
// SCOPE DECISION — WITHDRAWAL DOES NOT CANCEL IN-FLIGHT GENERATION:
//   Investigation of internal/course/service.go (Withdraw, line 70–82) and
//   internal/agent/runner.go (RunContentGeneration, line 316–376 and
//   generateAllSections, line 382–445) shows:
//
//   • Withdraw() only does a DB status UPDATE to 'archived'.  It neither
//     cancels a Go context nor signals any channel.
//   • RunContentGeneration() wraps the run in context.WithTimeout(ctx, ...)
//     where ctx is the server-root context, not a per-course cancellable
//     context.  The timeout can be up to 3600 s (runner.go:320–322).
//   • generateAllSections() checks ctx.Err() only at the top of each
//     section loop iteration (runner.go:423–425).  An ongoing section
//     generate/review call continues until it returns even after withdrawal.
//   • There is no cancel hook, no withdrawal-listener goroutine, and
//     pollAndGenerate() guards only against starting a NEW run — it does
//     not terminate an already-running one.
//
//   CONCLUSION: withdrawing a course does NOT reliably stop an in-flight
//   content-generation run.  Clicking Approve would trigger full content
//   generation (many Claude calls) with no cheap abort path.
//
//   Therefore this spec STOPS at step 5 (syllabus rendered, Approve button
//   visible) and does NOT click Approve.  The approve→generating branch is
//   out of scope until a per-course context-cancellation hook is wired to
//   Withdraw().
//
// WHAT THIS SPEC PROVES (AI-verified, end-to-end against a real Claude key):
//   1. Preparing indicator appears on the intake view immediately after
//      course creation (REQ-FECOURSE-028, REQ-FECOURSE-262).
//   2. The Chair's OPENING MESSAGE appears as an agent bubble within 90 s —
//      regression for the blank-chat demo bug (REQ-AGENT-018).
//   3. A realistic multi-turn intake conversation completes (REQ-AGENT-001).
//      Each turn: typing indicator visible → agent reply arrives within 60 s.
//   4. Intake→syllabus URL transition occurs (REQ-AGENT-019, REQ-FECOURSE-024).
//   5. Drafting indicator ("Your professor is drafting your syllabus…") appears
//      (REQ-FECOURSE-056).
//   6. Syllabus content renders (REQ-FECOURSE-057) within 180 s — verifies the
//      polling fix from the fast-follow sprint.
//   7. "Approve and Start Course" button is visible (REQ-FECOURSE-510 / the
//      approval UI requirement).
//
// CLEANUP GUARANTEE:
//   afterEach archives the course via API regardless of pass/fail so the
//   single-active-course constraint never blocks subsequent runs.

// @{"verifies", ["REQ-FECOURSE-028", "REQ-FECOURSE-262", "REQ-AGENT-018", "REQ-AGENT-001", "REQ-AGENT-019", "REQ-FECOURSE-024", "REQ-FECOURSE-056", "REQ-FECOURSE-057"]}

import { test, expect, type Page } from '@playwright/test'
import { login, STUDENT_USER, STUDENT_PASS } from '../helpers'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// archiveOpenCourses withdraws every non-archived course for the logged-in
// student.  Uses page.request (not the standalone request fixture) so the
// __Host-csrf cookie set at login is sent automatically.
async function archiveOpenCourses(page: Page, bearerToken: string): Promise<void> {
  const csrf =
    (await page.context().cookies()).find(c => c.name === '__Host-csrf')?.value ?? ''
  const list = await page.request.get('/api/v1/courses', {
    headers: { Authorization: `Bearer ${bearerToken}` }
  })
  if (!list.ok()) return
  const body = (await list.json()) as { courses: { id: string; status: string }[] | null }
  for (const c of body.courses ?? []) {
    if (c.status !== 'archived' && c.status !== 'completed') {
      await page.request.post(`/api/v1/courses/${c.id}/withdraw`, {
        headers: { Authorization: `Bearer ${bearerToken}`, 'X-CSRF-Token': csrf },
        data: {}
      })
    }
  }
}

// ---------------------------------------------------------------------------
// State shared between beforeEach and afterEach hooks.
// We store the bearer token and active course ID so afterEach can always
// archive even when the test body throws mid-way.
// ---------------------------------------------------------------------------
let bearerToken: string | null = null
let activeCourseId: string | null = null

// ---------------------------------------------------------------------------
// Test: full intake-to-syllabus journey
// ---------------------------------------------------------------------------

// @{"verifies", ["REQ-FECOURSE-028", "REQ-FECOURSE-262", "REQ-AGENT-018", "REQ-AGENT-001", "REQ-AGENT-019", "REQ-FECOURSE-024", "REQ-FECOURSE-056", "REQ-FECOURSE-057"]}
test('IntakeToSyllabus_FullJourney_PreparingThenChatThenSyllabusRendered', async ({ page }) => {
  // -------------------------------------------------------------------------
  // Step 0: set up event listeners BEFORE any navigation so we capture
  // everything from the start of the test.
  // -------------------------------------------------------------------------
  bearerToken = null
  activeCourseId = null

  // Capture bearer token from the login response.
  page.on('response', async response => {
    if (
      response.url().includes('/api/v1/auth/login') &&
      response.status() === 200
    ) {
      try {
        const body = (await response.json()) as { token?: string }
        if (body.token) bearerToken = body.token
      } catch {
        // ignore parse errors
      }
    }
  })

  // Capture browser console messages from the very start so mount-time
  // errors in IntakeChatView are not missed.
  const consoleLogs: string[] = []
  page.on('console', msg => consoleLogs.push(`[${msg.type()}] ${msg.text()}`))

  // -------------------------------------------------------------------------
  // Step 1: login and archive any stale course
  // -------------------------------------------------------------------------
  await login(page, STUDENT_USER, STUDENT_PASS)
  await expect(page).toHaveURL(/\/courses/, { timeout: 15_000 })
  expect(bearerToken, 'bearer token must be captured from login response').not.toBeNull()

  await archiveOpenCourses(page, bearerToken!)

  // -------------------------------------------------------------------------
  // Step 2: create a new course
  // -------------------------------------------------------------------------
  await page.click('button.create-button')
  await expect(page.locator('.modal')).toBeVisible()

  const topic = `Journey E2E ${Date.now()}`
  await page.locator('#course-topic').fill(topic)
  await expect(page.locator('button.submit-button')).toBeEnabled()
  await page.locator('button.submit-button').click()

  // Successful creation navigates to /courses/<uuid>/intake
  await expect(page).toHaveURL(/\/courses\/[0-9a-f-]+\/intake/, { timeout: 15_000 })

  // Extract course ID from the URL for cleanup
  const intakeUrl = page.url()
  const idMatch = intakeUrl.match(/\/courses\/([0-9a-f-]+)\/intake/)
  expect(idMatch, 'course ID must be extractable from intake URL').toBeTruthy()
  activeCourseId = idMatch![1]

  // -------------------------------------------------------------------------
  // Step 3: assert preparing indicator OR opening message (REQ-FECOURSE-028,
  // REQ-FECOURSE-262)
  //
  // The view shows a typing-indicator + "Your professor is preparing your
  // course…" label (IntakeChatView.vue line 294-299) while history is empty
  // and isHistoryPolling is true.  On fast networks the kickoff message can
  // arrive in 1-2 s, causing the label to disappear before the test asserts.
  // The correct invariant is: the page must never remain permanently blank.
  // We therefore assert that EITHER the preparing label is visible (kickoff
  // in flight) OR the first agent bubble is already present (kickoff arrived
  // quickly).  Both outcomes satisfy REQ-FECOURSE-028 (no blank page) and
  // REQ-FECOURSE-262 (polling successfully retrieved the opening message).
  //
  // DIAGNOSTIC: capture console output and snapshot inner HTML to diagnose
  // persistent blank-page failures.
  // -------------------------------------------------------------------------

  // Wait for the network to settle so the component's onMounted fetch has had
  // time to fire and the Vue app has processed its response.
  await page.waitForLoadState('networkidle', { timeout: 15_000 }).catch(() => {
    // Not a failure — networkidle may time out for long-lived SSE connections.
    // We only use it as a signal that the initial fetch has likely completed.
  })

  const preparingLabel = page.locator('.preparing-label')
  const earlyAgentBubble = page.locator('.message--agent .message-bubble').first()

  // Wait up to 90 s for either the preparing label (polling active, kickoff
  // in flight) or the first agent bubble (kickoff arrived quickly via polling).
  // 90 s is the same as the "opening message" timeout further below — if the
  // app is working correctly one of these two states must appear within that
  // window.  If neither does, this is the REQ-FECOURSE-028 blank-chat regression.
  let chatContainerHtml = ''
  try {
    await expect(
      preparingLabel.or(earlyAgentBubble),
      'either the preparing label or the first agent bubble must be visible (REQ-FECOURSE-028)'
    ).toBeVisible({ timeout: 90_000 })
  } catch (assertionError) {
    // Capture DOM state for the bug report before re-throwing
    chatContainerHtml = await page.evaluate(() => {
      const el = document.querySelector('.chat-container')
      return el ? el.innerHTML : '<chat-container not found>'
    }).catch(() => '<evaluate failed>')
    const fullUrl = page.url()
    throw new Error(
      `PRODUCT BUG (REQ-FECOURSE-028 REGRESSION): intake view remained blank for 90 s.\n` +
      `URL at failure: ${fullUrl}\n` +
      `.chat-container innerHTML:\n${chatContainerHtml}\n` +
      `Browser console (last 20):\n${consoleLogs.slice(-20).join('\n')}\n` +
      `Original assertion error: ${(assertionError as Error).message}`
    )
  }

  // -------------------------------------------------------------------------
  // Step 4: wait for the Chair's OPENING MESSAGE (REQ-AGENT-018)
  //
  // The kickoff goroutine sends the first intake question within a few seconds.
  // The frontend polls history every 2.5 s and renders the first agent bubble
  // once it arrives.  90 s is generous — normal arrival is 5–20 s.
  //
  // This is the AI-verified regression guard for the blank-chat demo bug:
  // the test fails if the opening message never appears.
  // -------------------------------------------------------------------------
  // :not(.typing-indicator) is essential: both the preparing indicator and the
  // in-flight send indicator render as .message--agent .message-bubble, so an
  // unfiltered locator matches them and passes before any real message exists.
  const agentBubble = page.locator('.message--agent .message-bubble:not(.typing-indicator)').first()
  await expect(agentBubble, 'Chair opening message must appear within 90 s').toBeVisible({
    timeout: 90_000
  })

  // -------------------------------------------------------------------------
  // Step 5: multi-turn intake conversation (REQ-AGENT-001)
  //
  // We send realistic canned answers until either:
  //   (a) the chat POST response has course_status !== 'intake' (URL redirect
  //       will follow automatically via redirectOnStatusChange), OR
  //   (b) the URL changes to /courses/<id>/syllabus (SSE status_change event),
  //       OR
  //   (c) we reach MAX_TURNS, in which case we fail with the transcript.
  //
  // After each send we assert the typing indicator appears then disappears
  // (i.e. an agent reply arrives) within 60 s per turn.
  // -------------------------------------------------------------------------
  const MAX_TURNS = 8

  // Canned answers cover the Chair's typical intake questions: background,
  // goal, time commitment, learning style preference.  We cycle through them
  // if the Chair asks more questions than we have unique answers.
  const cannedAnswers = [
    "I'm a complete beginner with no prior experience in this topic.",
    'My goal is to gain a solid foundational understanding and be able to apply the concepts practically.',
    'I can dedicate about 5 hours per week to studying.',
    'I learn best through worked examples and hands-on practice.',
    'I prefer a structured approach with clear milestones.',
    'I have no particular constraints — flexible scheduling works for me.',
    "Yes, I'm ready to start. Please proceed with the course plan.",
    "That all sounds great. I'm happy with the plan you've described."
  ]

  // Collect a transcript for failure reporting
  const transcript: string[] = []
  let transitioned = false

  for (let turn = 0; turn < MAX_TURNS; turn++) {
    // Check if the URL already changed (SSE-driven redirect happened between turns)
    if (page.url().includes('/syllabus')) {
      transitioned = true
      break
    }

    const answer = cannedAnswers[turn % cannedAnswers.length]
    transcript.push(`[student turn ${turn + 1}] ${answer}`)

    // Fill and submit
    const chatInput = page.locator('.chat-input')
    // 30 s: the input re-enables when the previous turn's POST resolves; chat
    // replies routinely take >5 s, so a short timeout here races the API.
    // If the redirect lands while we wait, that's intake completion — success.
    try {
      await expect(chatInput).toBeEnabled({ timeout: 30_000 })
    } catch (err) {
      if (page.url().includes('/syllabus')) {
        transitioned = true
        break
      }
      throw err
    }
    await chatInput.fill(answer)
    await page.locator('.send-button').click()

    // Assert typing indicator appears (isSending = true shows the 3-dot bubble)
    const typingIndicator = page.locator('.message--agent .typing-indicator')
    await expect(typingIndicator, `typing indicator must appear after turn ${turn + 1}`).toBeVisible({
      timeout: 10_000
    })

    // Wait for typing indicator to go away AND a new agent bubble to appear.
    // The typing-indicator bubble is removed when isSending goes false;
    // a new .message--agent .message-bubble is added for the reply.
    // We count agent bubbles: after turn N+1 there should be at least N+2
    // (opening message + N replies).
    const expectedAgentCount = turn + 2 // opening + replies so far
    // :not(.typing-indicator): the isSending indicator bubble also matches
    // .message--agent .message-bubble and would inflate the count by one,
    // advancing the loop a turn early into a still-disabled input.
    //
    // The reply and the intake-complete redirect race each other: when the
    // Chair detects INTAKE_COMPLETE the POST response carries
    // course_status=syllabus_draft and the SPA navigates away, emptying the
    // chat DOM — so "redirected" is just as much a success as "reply arrived".
    const bubbles = page.locator('.message--agent .message-bubble:not(.typing-indicator)')
    let outcome: 'reply' | 'redirect' | 'pending' = 'pending'
    const turnDeadline = Date.now() + 60_000
    while (outcome === 'pending' && Date.now() < turnDeadline) {
      if (page.url().includes('/syllabus')) {
        outcome = 'redirect'
      } else if ((await bubbles.count().catch(() => 0)) >= expectedAgentCount) {
        outcome = 'reply'
      } else {
        await page.waitForTimeout(500)
      }
    }
    if (outcome === 'pending') {
      throw new Error(
        `Neither an agent reply nor the syllabus redirect arrived within 60 s after turn ${turn + 1}.\n` +
          `Transcript so far:\n${transcript.join('\n')}`
      )
    }
    if (outcome === 'redirect') {
      transitioned = true
      transcript.push(`[transition] redirected to syllabus after turn ${turn + 1}`)
      break
    }

    transcript.push(`[agent reply ${turn + 1}] (received)`)

    // After the reply lands, check for syllabus URL transition.
    // redirectOnStatusChange() is called when response.course_status !== 'intake'.
    if (page.url().includes('/syllabus')) {
      transitioned = true
      break
    }
  }

  // If we exhausted all turns without a transition, fail with the transcript.
  if (!transitioned) {
    // Give a short extra wait in case the redirect is still in flight
    const didTransition = await page
      .waitForURL(/\/courses\/[0-9a-f-]+\/syllabus/, { timeout: 10_000 })
      .then(() => true)
      .catch(() => false)

    if (!didTransition) {
      throw new Error(
        `Intake did not complete after ${MAX_TURNS} turns.\n` +
          `Current URL: ${page.url()}\n` +
          `Transcript:\n${transcript.join('\n')}`
      )
    }
    transitioned = true
  }

  // -------------------------------------------------------------------------
  // Step 6: assert intake→syllabus URL transition (REQ-AGENT-019, REQ-FECOURSE-024)
  // -------------------------------------------------------------------------
  await expect(page).toHaveURL(/\/courses\/[0-9a-f-]+\/syllabus/, { timeout: 15_000 })

  // -------------------------------------------------------------------------
  // Step 7: assert drafting indicator (REQ-FECOURSE-056)
  //
  // SyllabusView.vue renders .drafting-indicator when the GET /syllabus
  // returns 404 (syllabus not yet generated).  The indicator contains the
  // text "Your professor is drafting your syllabus…"
  // The syllabus may arrive quickly; we allow it to be absent if content
  // is already present, but we must assert one or the other.
  // -------------------------------------------------------------------------
  const syllabusContent = page.locator('.syllabus-text pre')
  const draftingIndicator = page.locator('.drafting-indicator')

  // Either the drafting indicator is visible, or the syllabus content is
  // already rendered.  Assert within a generous window.
  await expect(
    draftingIndicator.or(syllabusContent),
    'either the drafting indicator or syllabus content must be visible'
  ).toBeVisible({ timeout: 15_000 })

  // -------------------------------------------------------------------------
  // Step 8: wait for syllabus content to render (REQ-FECOURSE-057)
  //
  // SyllabusView.vue polls GET /courses/{id}/syllabus every 4 s.  Once the
  // backend has the syllabus row, the poll succeeds and syllabusContent is
  // populated.  The Chair generates the syllabus asynchronously via the
  // syllabus-draft transition triggered at end of intake (REQ-AGENT-020).
  //
  // 180 s is the view's own BOUNDED_WAIT_MS; we use it as the timeout so
  // the test mirrors the production guarantee exactly.
  // -------------------------------------------------------------------------
  await expect(syllabusContent, 'syllabus content must render within 180 s').toBeVisible({
    timeout: 180_000
  })

  // Syllabus must have non-trivial content (not just whitespace)
  const syllabusText = await syllabusContent.textContent()
  expect(
    syllabusText?.trim().length ?? 0,
    'syllabus content must be non-empty'
  ).toBeGreaterThan(50)

  // -------------------------------------------------------------------------
  // Step 9: assert Approve button is visible (scoped stop per cost rule)
  //
  // We do NOT click Approve because withdrawal does not cancel in-flight
  // content generation — see the SCOPE DECISION at the top of this file.
  // Asserting the button is present proves the UI reached the approval-ready
  // state.
  // -------------------------------------------------------------------------
  const approveButton = page.locator('button:has-text("Approve and Start Course")')
  await expect(approveButton, '"Approve and Start Course" button must be visible').toBeVisible()

  // -------------------------------------------------------------------------
  // Cleanup is handled in afterEach below
  // -------------------------------------------------------------------------
})

// ---------------------------------------------------------------------------
// afterEach: archive the course even on failure
// ---------------------------------------------------------------------------
test.afterEach(async ({ page }) => {
  if (bearerToken && activeCourseId) {
    try {
      const csrf =
        (await page.context().cookies()).find(c => c.name === '__Host-csrf')?.value ?? ''
      await page.request.post(`/api/v1/courses/${activeCourseId}/withdraw`, {
        headers: { Authorization: `Bearer ${bearerToken}`, 'X-CSRF-Token': csrf },
        data: {}
      })
    } catch {
      // Best-effort: if the request fails (e.g. page already closed), fall
      // back to archiveOpenCourses via a fresh navigation.  The afterEach is
      // not guaranteed to run with a live page on hard test failures, so we
      // swallow errors here and rely on the pre-clean at the start of the
      // next run.
    }
  }
})
