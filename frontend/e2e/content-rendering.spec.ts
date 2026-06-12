// @{"verifies": ["REQ-FECOURSE-612", "REQ-FECOURSE-613", "REQ-FECOURSE-614", "REQ-FECOURSE-615", "REQ-FECOURSE-616"]}
//
// content-rendering.spec.ts — Sprint 14 (updated Sprint 17 fast-follow)
// AI-free spec proving the markdown→KaTeX→DOMPurify rendering pipeline in
// IntakeChatView.
//
// REQ-FECOURSE-616 AMENDED (Sprint 17 fast-follow):
//   Student (user) messages are now rendered through the same sanitized
//   markdown→KaTeX→DOMPurify pipeline as agent messages.  The chat input is
//   now a <textarea> (Enter sends; Shift+Enter inserts newline).  Raw HTML
//   typed by a student is escaped to literal text by markdown-it html:false —
//   no <b> or other injected elements appear inside the user bubble.
//
// ARCHITECTURE (2 courses, 0 AI calls):
//
//   Course A (shared across suite A tests 1–4):
//     Created in Suite A beforeAll.  4 deterministic assistant messages are
//     seeded (one per rendering concern).  Each test navigates to the intake
//     page via the in-SPA pattern (click "Courses" nav → click course card),
//     which keeps the Pinia auth store in memory and forces a fresh
//     loadChatHistory() on each IntakeChatView mount.  Course A stays in
//     'intake' status throughout suite A; only Suite A afterAll archives it
//     via cleanupCourse().
//
//   Course B (suite B test 5 only):
//     Suite B beforeAll archives Course A (via archiveOpenCourses) then
//     creates Course B and seeds one student message.  Suite B afterAll
//     archives Course B via cleanupCourse().
//
// KICKOFF COLLISION RULE (see seed.ts):
//   markKickoffSent() is called immediately after course creation and BEFORE
//   any seedChatMessage() call to atomically prevent the background goroutine
//   from inserting its own opening message.
//
// COST: 0 Anthropic API calls.  No chat POST is ever sent.
//
// NAVIGATION STRATEGY:
//   In-SPA navigation (click "Courses" nav link, then click course card) is
//   used instead of hard page.goto() for intake pages.  This mirrors the
//   proven pattern in seed-smoke.spec.ts and avoids any router-guard redirect
//   that can occur during an SPA boot if the course exists but the route
//   guard evaluates before the auth state is restored.
//
// DOMPurify image-src note:
//   renderMarkdown.ts strips any img src that is not https?:// or data:image/.
//   Relative paths (e.g. /valory.svg) are stripped.  The image test uses
//   https://localhost/valory.svg so the src survives the afterSanitizeAttributes
//   hook.  /valory.svg is always served by nginx from public/.

import { test, expect, type Page } from '@playwright/test'
import { login, STUDENT_USER, STUDENT_PASS } from './helpers'
import { markKickoffSent, seedChatMessage, cleanupCourse } from './seed'

// ---------------------------------------------------------------------------
// archiveOpenCourses — withdraws all non-terminal courses for the student.
// Duplicated from seed-smoke.spec.ts per spec self-containment convention.
// ---------------------------------------------------------------------------
async function archiveOpenCourses(page: Page, bearerToken: string): Promise<void> {
  const csrf = (await page.context().cookies()).find(c => c.name === '__Host-csrf')?.value ?? ''
  const list = await page.request.get('/api/v1/courses', {
    headers: { Authorization: `Bearer ${bearerToken}` }
  })
  if (!list.ok()) return
  const body = await list.json() as { courses: { id: string; status: string }[] | null }
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
// createCourseViaUI — clicks the create modal, submits the topic, and waits
// for the router to push /courses/{id}/intake.  Returns the course UUID.
// Requires the page to already be on the /courses dashboard.
// ---------------------------------------------------------------------------
async function createCourseViaUI(page: Page, topic: string): Promise<string> {
  await page.click('button.create-button')
  await expect(page.locator('.modal')).toBeVisible()
  await page.locator('#course-topic').fill(topic)
  await expect(page.locator('button.submit-button')).toBeEnabled()
  await page.locator('button.submit-button').click()

  await expect(page).toHaveURL(/\/courses\/[0-9a-f-]+\/intake/, { timeout: 15_000 })
  const match = page.url().match(/\/courses\/([0-9a-f-]+)\/intake/)
  if (!match) throw new Error('content-rendering: could not extract course ID from intake URL')
  return match[1]
}

// ---------------------------------------------------------------------------
// navigateToIntake — performs the proven in-SPA navigation sequence:
//   1. Click the "Courses" nav link → lands on /courses (unmounts any current
//      view including IntakeChatView, closing the SSE connection).
//   2. Click the course card for the given topic → router pushes
//      /courses/{id}/intake, mounting IntakeChatView afresh and triggering a
//      new loadChatHistory() call.
// ---------------------------------------------------------------------------
async function navigateToIntake(page: Page, topic: string): Promise<void> {
  await page.click('nav.nav-links a[href="/courses"]')
  await expect(page).toHaveURL(/\/courses$/, { timeout: 10_000 })
  await page.locator('.course-card', { hasText: topic }).click()
  await expect(page).toHaveURL(/\/courses\/[0-9a-f-]+\/intake/, { timeout: 10_000 })
}

// ===========================================================================
// Suite A — agent-bubble rendering (markdown, LaTeX, image, XSS)
//
// One course (Course A) is shared across all four tests.  Messages are seeded
// once in beforeAll.  Tests navigate to the intake page independently so that
// a single assertion failure does not prevent the remaining tests from running.
// Course A is archived in afterAll — individual tests do NOT archive it.
// ===========================================================================
test.describe('AgentBubble rendering — shared Course A', () => {
  // Describe-level variables shared across beforeAll, afterAll, and tests.
  let courseId: string
  let topicA: string

  // ---------------------------------------------------------------------------
  // Message fixtures
  // ---------------------------------------------------------------------------

  // MARKDOWN_MSG: exercises h1 rendering, bold (strong), unordered list, and
  // fenced code block.  "Heading One" is the unique discriminator for the locator.
  const MARKDOWN_MSG = [
    '# Heading One',
    '',
    'This is **bold text** from the seed fixture.',
    '',
    '- Item Alpha',
    '- Item Beta',
    '',
    '```js',
    'const x = 1;',
    '```'
  ].join('\n')

  // LATEX_MSG: inline ($E = mc^2$) and display ($$\int_0^1 x^2\,dx$$) math.
  // "Inline:" is the unique discriminator.
  const LATEX_MSG = 'Inline: $E = mc^2$ and display: $$\\int_0^1 x^2\\,dx$$'

  // IMAGE_MSG: markdown image syntax.  Uses https:// so DOMPurify's src check
  // passes.  "Valory logo" is the alt text used as the locator discriminator.
  const IMAGE_MSG = '![Valory logo](https://localhost/valory.svg)'

  // XSS_MSG: three XSS attack vectors in a single message.
  //   1. <script> tag — DOMPurify removes all script elements.
  //   2. <img onerror> — afterSanitizeAttributes hook strips event handlers.
  //   3. [click](javascript:...) — afterSanitizeAttributes hook strips the href.
  // "click" is the unique discriminator used to locate the bubble.
  const XSS_MSG = [
    '<script>window.__pwned=1</script>',
    '<img src=x onerror="window.__pwned=2">',
    '[click](javascript:window.__pwned=3)'
  ].join(' ')

  test.beforeAll(async ({ browser }) => {
    // Open a dedicated setup page.  Each subsequent test uses its own { page }
    // fixture page within the same browser context.
    const setupPage = await browser.newPage()
    let setupToken: string | null = null

    setupPage.on('response', async response => {
      if (response.url().includes('/api/v1/auth/login') && response.status() === 200) {
        try {
          const body = await response.json() as { token?: string }
          if (body.token) setupToken = body.token
        } catch { /* ignore */ }
      }
    })

    await login(setupPage, STUDENT_USER, STUDENT_PASS)
    await expect(setupPage).toHaveURL(/\/courses/)
    if (!setupToken) throw new Error('Suite A beforeAll: bearer token not captured from login')

    // Archive any stale active course before creating Course A so the
    // single-active-course constraint does not block course creation.
    await archiveOpenCourses(setupPage, setupToken)

    topicA = `ContentRendering-A ${Date.now()}`
    courseId = await createCourseViaUI(setupPage, topicA)

    // markKickoffSent MUST be called before any seedChatMessage() call.
    // This atomically prevents the background kickoff goroutine from inserting
    // an assistant opening message that would interleave with our seeds.
    markKickoffSent(courseId)

    // Seed all four test messages.  They arrive in insertion order from the
    // history endpoint so each test can locate its bubble by content.
    seedChatMessage(courseId, 'assistant', MARKDOWN_MSG)
    seedChatMessage(courseId, 'assistant', LATEX_MSG)
    seedChatMessage(courseId, 'assistant', IMAGE_MSG)
    seedChatMessage(courseId, 'assistant', XSS_MSG)

    // setupPage is no longer needed; close it so its SSE connection does not
    // hold open the course event stream while tests are running.
    await setupPage.close()
  })

  test.afterAll(async () => {
    // Archive Course A.  cleanupCourse() uses psql as the postgres superuser
    // so no bearer token is required.  It is idempotent: if the course was
    // already archived by a previous failure path, this is a no-op.
    if (courseId) cleanupCourse(courseId)
  })

  // -------------------------------------------------------------------------
  // Test 1 — Markdown
  // -------------------------------------------------------------------------
  // @{"verifies": ["REQ-FECOURSE-612"]}
  test('ChatMarkdown_RendersFormattedAgentMessage', async ({ page }) => {
    await login(page, STUDENT_USER, STUDENT_PASS)
    await expect(page).toHaveURL(/\/courses/)

    // Navigate in-SPA to the intake page.  Course A is still in 'intake'
    // status so the router pushes /courses/{id}/intake.
    await navigateToIntake(page, topicA)

    // Wait for history to load.  The first agent bubble should be from the
    // MARKDOWN_MSG (it was seeded first).
    const agentBubbles = page.locator('.message.message--agent .message-bubble--markdown')
    await expect(agentBubbles.first()).toBeVisible({ timeout: 10_000 })

    // Locate the markdown bubble by the heading text "Heading One".
    const markdownBubble = agentBubbles.filter({ hasText: 'Heading One' })

    // REQ-FECOURSE-612: heading must be a <h1> DOM element, not "# Heading One".
    await expect(markdownBubble.locator('h1')).toBeVisible()

    // Bold text must be <strong>, not "**bold text**".
    await expect(markdownBubble.locator('strong')).toBeVisible()

    // List items must be <ul><li> elements, not "- Item Alpha".
    await expect(markdownBubble.locator('ul li')).toHaveCount(2)

    // Fenced code block must be <pre><code>.
    await expect(markdownBubble.locator('pre code')).toBeVisible()

    // Raw markdown syntax must NOT be present as visible text.
    await expect(markdownBubble).not.toContainText('# Heading One')
    await expect(markdownBubble).not.toContainText('**bold text**')
    await expect(markdownBubble).not.toContainText('- Item Alpha')
  })

  // -------------------------------------------------------------------------
  // Test 2 — LaTeX
  // -------------------------------------------------------------------------
  // @{"verifies": ["REQ-FECOURSE-613"]}
  test('ChatLaTeX_RendersTypesetMath', async ({ page }) => {
    await login(page, STUDENT_USER, STUDENT_PASS)
    await expect(page).toHaveURL(/\/courses/)

    await navigateToIntake(page, topicA)

    const agentBubbles = page.locator('.message.message--agent .message-bubble--markdown')
    await expect(agentBubbles.first()).toBeVisible({ timeout: 10_000 })

    // Locate the LaTeX bubble by "Inline:" which is unique to LATEX_MSG.
    const latexBubble = agentBubbles.filter({ hasText: 'Inline:' })

    // KaTeX generates .katex span elements for typeset math.
    const katexElements = latexBubble.locator('.katex')
    await expect(katexElements.first()).toBeVisible({ timeout: 5_000 })

    // REQ-FECOURSE-613: raw $...$ syntax must not appear as visible text.
    await expect(latexBubble).not.toContainText('$E = mc^2$')
    await expect(latexBubble).not.toContainText('$$')
  })

  // -------------------------------------------------------------------------
  // Test 3 — Image
  // -------------------------------------------------------------------------
  // @{"verifies": ["REQ-FECOURSE-614"]}
  test('ChatImage_RendersImgTag', async ({ page }) => {
    await login(page, STUDENT_USER, STUDENT_PASS)
    await expect(page).toHaveURL(/\/courses/)

    await navigateToIntake(page, topicA)

    const agentBubbles = page.locator('.message.message--agent .message-bubble--markdown')
    await expect(agentBubbles.first()).toBeVisible({ timeout: 10_000 })

    // Locate the image bubble via the rendered <img alt="Valory logo">.
    // DOMPurify preserves the alt attribute on img elements.
    const imageBubble = agentBubbles.filter({
      has: page.locator('img[alt="Valory logo"]')
    })

    // REQ-FECOURSE-614: an img element must exist and be visible.
    const img = imageBubble.locator('img[alt="Valory logo"]')
    await expect(img).toBeVisible({ timeout: 5_000 })

    // src must be the absolute https URL we seeded.
    // (Relative src values are stripped by the DOMPurify afterSanitizeAttributes hook.)
    await expect(img).toHaveAttribute('src', 'https://localhost/valory.svg')

    // The custom image renderer in renderMarkdown.ts adds loading="lazy".
    await expect(img).toHaveAttribute('loading', 'lazy')
  })

  // -------------------------------------------------------------------------
  // Test 4 — XSS
  // -------------------------------------------------------------------------
  // @{"verifies": ["REQ-FECOURSE-615"]}
  test('ChatXSS_ScriptAndEventHandlersInert', async ({ page }) => {
    await login(page, STUDENT_USER, STUDENT_PASS)
    await expect(page).toHaveURL(/\/courses/)

    await navigateToIntake(page, topicA)

    const agentBubbles = page.locator('.message.message--agent .message-bubble--markdown')
    await expect(agentBubbles.first()).toBeVisible({ timeout: 10_000 })

    // Locate the XSS bubble via the "click" link label unique to XSS_MSG.
    const xssBubble = agentBubbles.filter({ hasText: 'click' })
    await expect(xssBubble).toBeVisible({ timeout: 5_000 })

    // REQ-FECOURSE-615 primary assertion: window.__pwned must be undefined.
    // If any of the three XSS vectors had fired, __pwned would be 1, 2, or 3.
    const pwnedValue = await page.evaluate(
      () => (window as unknown as Record<string, unknown>).__pwned
    )
    expect(
      pwnedValue,
      'window.__pwned must be undefined — XSS payloads must not execute'
    ).toBeUndefined()

    // No <script> element must exist inside any agent bubble.
    // DOMPurify strips script elements unconditionally.
    const scriptCount = await xssBubble.locator('script').count()
    expect(scriptCount, 'No <script> element must exist inside the agent bubble').toBe(0)

    // The javascript: href on the [click] link must have been stripped.
    // The afterSanitizeAttributes hook in renderMarkdown.ts removes it.
    const links = xssBubble.locator('a')
    const linkCount = await links.count()
    for (let i = 0; i < linkCount; i++) {
      const href = await links.nth(i).getAttribute('href')
      expect(
        href === null || !href.toLowerCase().startsWith('javascript:'),
        `Link href must not be javascript: (got: ${href ?? 'null'})`
      ).toBe(true)
    }
  })
})

// ===========================================================================
// Suite B — student-bubble Markdown+LaTeX rendering (REQ-FECOURSE-616 AMENDED)
//
// REQ-FECOURSE-616 was amended: student messages are now rendered through the
// same sanitized markdown→KaTeX→DOMPurify pipeline as agent messages.  The
// key contract differences from the old plain-text behaviour are:
//
//   • **bold** → <strong> DOM element (Markdown rendered)
//   • $x^2$   → .katex DOM element (LaTeX typeset)
//   • <b>raw</b> typed literally by the student → rendered as literal text
//     "&lt;b&gt;raw&lt;/b&gt;" because markdown-it is configured with
//     html:false, so raw HTML is escaped before DOMPurify ever sees it.
//     No <b> DOM element must exist inside the user bubble.
//
// A separate course is required because the single-active-course constraint
// prevents creating Course B while Course A exists.  Suite A's afterAll runs
// before Suite B's beforeAll (Playwright runs describe blocks sequentially
// when workers=1), and Suite B's beforeAll also calls archiveOpenCourses() as
// a belt-and-suspenders guard.
//
// SELECTOR NOTE: user bubbles now carry class="message-bubble message-bubble--markdown"
// (same as agent bubbles).  Suite A tests are already scoped to .message--agent,
// so they are unaffected by the user bubble gaining the --markdown class.
// ===========================================================================
test.describe('StudentBubble Markdown+LaTeX rendering — Course B', () => {
  let courseId: string
  let topicB: string

  // STUDENT_MSG: exercises three distinct rendering paths for the amended contract:
  //   **bold**   → must render a <strong> element (Markdown processed)
  //   $x^2$      → must render a .katex element (LaTeX processed)
  //   <b>raw</b> → must appear as literal text with NO <b> element
  //                (html:false in markdown-it escapes the angle brackets)
  const STUDENT_MSG = '**bold** $x^2$ <b>raw</b>'

  test.beforeAll(async ({ browser }) => {
    const setupPage = await browser.newPage()
    let setupToken: string | null = null

    setupPage.on('response', async response => {
      if (response.url().includes('/api/v1/auth/login') && response.status() === 200) {
        try {
          const body = await response.json() as { token?: string }
          if (body.token) setupToken = body.token
        } catch { /* ignore */ }
      }
    })

    await login(setupPage, STUDENT_USER, STUDENT_PASS)
    await expect(setupPage).toHaveURL(/\/courses/)
    if (!setupToken) throw new Error('Suite B beforeAll: bearer token not captured from login')

    // Archive any stale active course (including Course A from Suite A).
    await archiveOpenCourses(setupPage, setupToken)

    topicB = `ContentRendering-B ${Date.now()}`
    courseId = await createCourseViaUI(setupPage, topicB)

    markKickoffSent(courseId)
    seedChatMessage(courseId, 'student', STUDENT_MSG)

    await setupPage.close()
  })

  test.afterAll(async () => {
    if (courseId) cleanupCourse(courseId)
  })

  // -------------------------------------------------------------------------
  // Test 5 — Student Markdown+LaTeX rendering with raw-HTML escape proof
  //
  // REQ-FECOURSE-616 AMENDED: student messages now pass through the sanitized
  // markdown→KaTeX→DOMPurify pipeline.  html:false in markdown-it means raw
  // HTML tags typed by the student are escaped to literals, not executed.
  // -------------------------------------------------------------------------
  // @{"verifies": ["REQ-FECOURSE-616"]}
  test('ChatStudentMessage_MarkdownRendersRawHtmlInert', async ({ page }) => {
    await login(page, STUDENT_USER, STUDENT_PASS)
    await expect(page).toHaveURL(/\/courses/)

    // Navigate in-SPA to Course B's intake page.
    await page.click('nav.nav-links a[href="/courses"]')
    await expect(page).toHaveURL(/\/courses$/, { timeout: 10_000 })
    await page.locator('.course-card', { hasText: topicB }).click()
    await expect(page).toHaveURL(/\/courses\/[0-9a-f-]+\/intake/, { timeout: 10_000 })

    // User bubbles have class="message-bubble message-bubble--markdown".
    // We scope to .message--user so we never accidentally match an agent bubble
    // (both roles now use the --markdown class).
    const userBubble = page.locator('.message.message--user .message-bubble--markdown')
    await expect(userBubble).toBeVisible({ timeout: 10_000 })

    // REQ-FECOURSE-616 (AMENDED): **bold** must be rendered as <strong>.
    // markdown-it converts **text** to <strong> even for student input.
    await expect(
      userBubble.locator('strong'),
      'User bubble must contain a <strong> element from **bold** Markdown'
    ).toBeVisible()

    // REQ-FECOURSE-616 (AMENDED): $x^2$ must be typeset by KaTeX.
    // The renderMarkdown pipeline processes LaTeX delimiters for student input.
    const katexElements = userBubble.locator('.katex')
    await expect(
      katexElements.first(),
      'User bubble must contain at least one .katex element from $x^2$ LaTeX'
    ).toBeVisible({ timeout: 5_000 })

    // REQ-FECOURSE-616 (AMENDED): raw HTML typed by the student (<b>raw</b>)
    // must appear as escaped literal text, NOT as a rendered <b> DOM element.
    // markdown-it html:false escapes < and > before DOMPurify is applied, so
    // the angle brackets survive as text content, not as markup.
    await expect(
      userBubble,
      'User bubble textContent must include the literal string <b>raw</b>'
    ).toContainText('<b>raw</b>')

    // No <b> DOM element must exist inside the user bubble.
    // html:false escapes the tag — it must not be parsed into the DOM.
    expect(
      await userBubble.locator('b').count(),
      'No <b> element must exist in user bubble — raw HTML must be escaped, not executed'
    ).toBe(0)
  })
})
