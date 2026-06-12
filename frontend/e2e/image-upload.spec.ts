// @{"verifies": ["REQ-FECOURSE-622", "REQ-FECOURSE-623", "REQ-FECOURSE-624",
//                "REQ-AGENT-024", "REQ-AGENT-025",
//                "REQ-FECONTENT-220", "REQ-FECONTENT-221", "REQ-FECONTENT-222"]}
//
// image-upload.spec.ts — Sprint 15 Task 15.4 (updated in-sprint: endpoint added)
//
// AI-free e2e tests for image upload in chat (IntakeChatView) and homework
// submission (HomeworkSubmissionView).  Zero Anthropic API calls are made —
// no chat message is ever sent, and no homework submission is posted.
//
// COST RULE: Uploading an image to POST /api/v1/courses/{id}/images does NOT
// trigger any Claude call.  The backend stores bytes in the images table and
// returns {image_id, url}.  Only the POST /chat endpoint triggers Claude.
// Submitting homework triggers the grading runner asynchronously; to avoid
// any grading cost we NEVER POST to /submissions.
//
// TESTS:
//   1. ChatImageUpload_PreviewAndUploadRoundTrip
//        — attach a PNG via setInputFiles, assert preview thumbnail renders,
//          upload directly via page.request multipart (CSRF from cookie jar),
//          assert 201 + {image_id, url}, GET the image URL and assert 200 +
//          image/png + nosniff header, then remove the preview and assert it
//          disappears.  Verifies REQ-FECOURSE-622, REQ-FECOURSE-624, REQ-AGENT-024.
//
//   2. ChatImageValidation_WrongTypeRejected
//        — attempt to attach a text/plain file via setInputFiles and assert the
//          inline validation error is visible without any upload network call.
//          Also assert the backend returns 422 when a text payload is posted as
//          multipart via page.request.  Verifies REQ-FECOURSE-623, REQ-AGENT-024.
//
//   3. ImageAccess_OwnerOnly
//        — upload an image via page.request as demo_student, then GET the image
//          URL via a fresh unauthenticated context and assert 401.
//          Verifies REQ-AGENT-025.
//
//   4. HomeworkImageAttach_ControlAndCountFeedback
//        — seeds a course (intake → active) and a homework row via seedHomework.
//          Navigates to the submission view.  GET /api/v1/courses/{id}/homework/{hwId}
//          NOW EXISTS (endpoint was added in-sprint after Sprint 15 e2e found it
//          missing).  The view loads the homework title, shows the "Add Images"
//          control, and accepts up to 8 images.  The test:
//            a) asserts the seeded title renders (no error state);
//            b) asserts the "Add Images" button is visible and enabled;
//            c) attaches 2 PNG fixtures → 2 preview thumbnails + "2 / 8 images
//               attached" count feedback visible;
//            d) attempts to attach 7 more (total would be 9, cap is 8) →
//               count-limit error is visible;
//            e) NEVER posts a submission (grading runner consumes AI tokens
//               asynchronously via the background poller).
//          Fully verifies REQ-FECONTENT-220, REQ-FECONTENT-221, REQ-FECONTENT-222.

import { test, expect, type Page, type BrowserContext } from '@playwright/test'
import { login, STUDENT_USER, STUDENT_PASS } from './helpers'
import { markKickoffSent, setCourseStatus, cleanupCourse, seedHomework } from './seed'

// ---------------------------------------------------------------------------
// Tiny valid PNG fixture — generated in-test, no file on disk needed.
//
// This is the minimal binary representation of a 1×1 white PNG.  The bytes
// are hand-crafted from the PNG spec: signature + IHDR + IDAT + IEND.
// http.DetectContentType checks the first 512 bytes; the PNG magic bytes at
// offset 0 (\x89PNG) are sufficient for the server to accept it.
// ---------------------------------------------------------------------------
function makeTinyPng(): Buffer {
  // 1×1 white RGBA PNG (89 bytes — the smallest valid PNG that passes Go's
  // http.DetectContentType as image/png).
  const bytes = Buffer.from([
    // PNG signature
    0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
    // IHDR chunk (width=1, height=1, bit_depth=8, color_type=2 RGB)
    0x00, 0x00, 0x00, 0x0d, // chunk length
    0x49, 0x48, 0x44, 0x52, // "IHDR"
    0x00, 0x00, 0x00, 0x01, // width  = 1
    0x00, 0x00, 0x00, 0x01, // height = 1
    0x08, 0x02,             // bit depth=8, color type=2 (RGB)
    0x00, 0x00, 0x00,       // compression, filter, interlace
    0x90, 0x77, 0x53, 0xde, // CRC
    // IDAT chunk
    0x00, 0x00, 0x00, 0x0c, // chunk length = 12
    0x49, 0x44, 0x41, 0x54, // "IDAT"
    // zlib-compressed scanline: filter byte 0x00 + RGB pixel 0xff,0xff,0xff
    0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00, 0x00, 0x00, 0x02, 0x00, 0x01,
    0xe2, 0x21, 0xbc, 0x33, // CRC
    // IEND chunk
    0x00, 0x00, 0x00, 0x00, // length = 0
    0x49, 0x45, 0x4e, 0x44, // "IEND"
    0xae, 0x42, 0x60, 0x82  // CRC
  ])
  return bytes
}

// ---------------------------------------------------------------------------
// archiveOpenCourses — withdraws every non-terminal course owned by the
// logged-in student.  Uses page.request (shares cookie jar) so the __Host-csrf
// cookie is present and CSRF double-submit check passes.
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
// createCourseViaUI — opens the create-course modal and returns the UUID.
// ---------------------------------------------------------------------------
async function createCourseViaUI(page: Page, topic: string): Promise<string> {
  await page.click('button.create-button')
  await expect(page.locator('.modal')).toBeVisible()
  await page.locator('#course-topic').fill(topic)
  await expect(page.locator('button.submit-button')).toBeEnabled()
  await page.locator('button.submit-button').click()
  await expect(page).toHaveURL(/\/courses\/[0-9a-f-]+\/intake/, { timeout: 15_000 })
  const match = page.url().match(/\/courses\/([0-9a-f-]+)\/intake/)
  if (!match) throw new Error('image-upload: could not extract course ID from intake URL')
  return match[1]
}

// ---------------------------------------------------------------------------
// navigateToIntake — in-SPA navigation to the intake page.
// ---------------------------------------------------------------------------
async function navigateToIntake(page: Page, topic: string): Promise<void> {
  await page.click('nav.nav-links a[href="/courses"]')
  await expect(page).toHaveURL(/\/courses$/, { timeout: 10_000 })
  await page.locator('.course-card', { hasText: topic }).click()
  await expect(page).toHaveURL(/\/courses\/[0-9a-f-]+\/intake/, { timeout: 10_000 })
}

// ---------------------------------------------------------------------------
// getCsrfToken — reads the __Host-csrf cookie from the page's cookie jar.
// ---------------------------------------------------------------------------
async function getCsrfToken(page: Page): Promise<string> {
  const cookies = await page.context().cookies()
  return cookies.find(c => c.name === '__Host-csrf')?.value ?? ''
}

// ===========================================================================
// Test 1 — ChatImageUpload_PreviewAndUploadRoundTrip
// ===========================================================================

// @{"verifies": ["REQ-FECOURSE-622", "REQ-FECOURSE-624", "REQ-AGENT-024"]}
test('ChatImageUpload_PreviewAndUploadRoundTrip', async ({ page }) => {
  let bearerToken: string | null = null
  let courseId: string | null = null

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

  await archiveOpenCourses(page, bearerToken!)

  try {
    const topic = `ImageUpload-RoundTrip ${Date.now()}`
    courseId = await createCourseViaUI(page, topic)

    // Prevent the kickoff goroutine from inserting an AI message (no AI calls).
    markKickoffSent(courseId)

    // Navigate back to the intake page using in-SPA navigation so the Pinia
    // auth store (and bearer token) survive the navigation.
    await navigateToIntake(page, topic)

    // ---- Verify the attach button is present (REQ-FECOURSE-622) -------------

    const attachButton = page.locator('.attach-button')
    await expect(attachButton).toBeVisible()
    await expect(attachButton).toBeEnabled()

    // ---- Attach a tiny PNG via the hidden file input (REQ-FECOURSE-622) -----

    // The file input is hidden (display:none).  setInputFiles bypasses the
    // OS file picker and injects the file object directly.  We generate the
    // PNG bytes in-test so there is no fixture file dependency.
    const pngBytes = makeTinyPng()

    const fileInput = page.locator('input[type="file"][accept*="image/png"]').first()
    await fileInput.setInputFiles({
      name: 'test-image.png',
      mimeType: 'image/png',
      buffer: pngBytes
    })

    // ---- Assert preview thumbnail renders (REQ-FECOURSE-624) ----------------

    // IntakeChatView.vue renders .image-preview-item for each pending image.
    // Each item contains a .preview-img <img> element.
    const previewRow = page.locator('.image-preview-row')
    await expect(previewRow).toBeVisible({ timeout: 5_000 })

    const previewItem = previewRow.locator('.image-preview-item')
    await expect(previewItem).toHaveCount(1)

    const previewImg = previewItem.locator('.preview-img')
    await expect(previewImg).toBeVisible()

    // The src is a blob: URL created via URL.createObjectURL() — it must be
    // non-empty and start with "blob:" (the SPA-local object URL).
    const imgSrc = await previewImg.getAttribute('src')
    expect(imgSrc, 'Preview img src must be a blob: URL').toBeTruthy()
    expect(imgSrc!.startsWith('blob:'), 'Preview img src must start with blob:').toBe(true)

    // ---- Upload directly via page.request (REQ-AGENT-024) -------------------

    // We upload via page.request (which shares the browser context's cookie jar)
    // rather than clicking Send (which would trigger an AI chat call).  The CSRF
    // token is read from the __Host-csrf cookie — the same double-submit pattern
    // the frontend uses.
    const csrfToken = await getCsrfToken(page)
    expect(csrfToken, 'CSRF token must be present after login').not.toBe('')

    // Build a multipart body with the PNG bytes.
    const uploadResp = await page.request.post(`/api/v1/courses/${courseId}/images`, {
      headers: {
        'X-CSRF-Token': csrfToken
      },
      multipart: {
        image: {
          name: 'test-image.png',
          mimeType: 'image/png',
          buffer: pngBytes
        }
      }
    })

    // REQ-AGENT-024: successful upload returns 201 with {image_id, url}.
    expect(
      uploadResp.status(),
      `POST /images must return 201, got ${uploadResp.status()}`
    ).toBe(201)

    const uploadBody = await uploadResp.json() as { image_id?: string; url?: string }
    expect(uploadBody.image_id, 'Response must include image_id').toBeTruthy()
    expect(uploadBody.url, 'Response must include url').toBeTruthy()

    const imageId = uploadBody.image_id!
    const imageUrl = uploadBody.url!

    // url must be a same-origin /api/v1/images/{uuid} path.
    expect(
      imageUrl,
      'url must be /api/v1/images/<uuid>'
    ).toMatch(/^\/api\/v1\/images\/[0-9a-f-]{36}$/)

    // ---- GET the image and assert headers (REQ-AGENT-024) -------------------

    const getResp = await page.request.get(imageUrl)

    expect(
      getResp.status(),
      `GET ${imageUrl} must return 200`
    ).toBe(200)

    const contentType = getResp.headers()['content-type']
    expect(
      contentType,
      'Content-Type must be image/png'
    ).toContain('image/png')

    const noSniff = getResp.headers()['x-content-type-options']
    expect(
      noSniff,
      'X-Content-Type-Options must be nosniff'
    ).toBe('nosniff')

    // ---- Remove the preview (REQ-FECOURSE-624) --------------------------------

    // The ✕ remove button is inside .image-preview-item.  Clicking it calls
    // removeImage() which splices the pendingImages array and revokes the
    // object URL.  After removal, .image-preview-row must be gone (v-if on
    // pendingImages.length > 0).
    const removeButton = previewItem.locator('.remove-button')
    await expect(removeButton).toBeVisible()
    await removeButton.click()

    // The preview row disappears because pendingImages is empty.
    await expect(previewRow).not.toBeVisible({ timeout: 3_000 })

    // Verify the specific image item is also gone.
    expect(
      await previewItem.count(),
      'Preview item must be removed after clicking ✕'
    ).toBe(0)

    void imageId // suppress unused-variable warning — imageId is validated above
  } finally {
    if (courseId) cleanupCourse(courseId)
  }
})

// ===========================================================================
// Test 2 — ChatImageValidation_WrongTypeRejected
// ===========================================================================

// @{"verifies": ["REQ-FECOURSE-623", "REQ-AGENT-024"]}
test('ChatImageValidation_WrongTypeRejected', async ({ page }) => {
  let bearerToken: string | null = null
  let courseId: string | null = null

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

  await archiveOpenCourses(page, bearerToken!)

  try {
    const topic = `ImageUpload-Validation ${Date.now()}`
    courseId = await createCourseViaUI(page, topic)
    markKickoffSent(courseId)
    await navigateToIntake(page, topic)

    // ---- Client-side validation: text/plain file is rejected immediately ----

    // Count upload network requests before attaching the invalid file.  If the
    // client-side validation fires, no POST to /images should be initiated.
    let uploadRequestFired = false
    await page.route(`/api/v1/courses/${courseId}/images`, route => {
      uploadRequestFired = true
      // Fulfill with a dummy 422 so the test does not hang waiting for the
      // network — but the assertion below already checks the flag before any
      // route interception occurs.
      route.fulfill({ status: 422, body: '{"error":"UNSUPPORTED_MIME"}' })
    })

    const fileInput = page.locator('input[type="file"][accept*="image/png"]').first()

    // Provide a text/plain file payload.  The browser's mime type and the
    // frontend's validateImage() both check file.type; text/plain is not in
    // the allowed set (image/png, image/jpeg, image/gif, image/webp).
    await fileInput.setInputFiles({
      name: 'bad-file.txt',
      mimeType: 'text/plain',
      buffer: Buffer.from('this is not an image')
    })

    // REQ-FECOURSE-623: an inline validation error must be visible.
    // IntakeChatView.vue: <div v-if="imageValidationError" class="image-error">
    const imageError = page.locator('.image-error')
    await expect(imageError).toBeVisible({ timeout: 3_000 })
    await expect(imageError).toContainText(/type not supported|Allowed/i)

    // No preview item must appear (the file was rejected before being queued).
    const previewItem = page.locator('.image-preview-item')
    expect(
      await previewItem.count(),
      'No preview must appear for a rejected file type'
    ).toBe(0)

    // No upload network request must have been fired (client gated the upload).
    expect(
      uploadRequestFired,
      'No network upload must be initiated when client-side validation rejects the file'
    ).toBe(false)

    // ---- Backend validation: text payload POSTed as multipart → 422 ---------

    // Independently of the frontend, verify the backend also rejects a text body.
    // This asserts REQ-AGENT-024 (server-side MIME sniff rejection).
    const csrfToken = await getCsrfToken(page)
    const badUploadResp = await page.request.post(`/api/v1/courses/${courseId}/images`, {
      headers: { 'X-CSRF-Token': csrfToken },
      multipart: {
        image: {
          name: 'bad-file.txt',
          mimeType: 'text/plain',
          buffer: Buffer.from('this is not an image')
        }
      }
    })

    expect(
      badUploadResp.status(),
      `Backend must reject a non-image upload with 422, got ${badUploadResp.status()}`
    ).toBe(422)

    const errBody = await badUploadResp.json() as { error?: string }
    expect(
      errBody.error,
      'Error code must be UNSUPPORTED_MIME'
    ).toBe('UNSUPPORTED_MIME')

  } finally {
    if (courseId) cleanupCourse(courseId)
  }
})

// ===========================================================================
// Test 3 — ImageAccess_OwnerOnly
// ===========================================================================

// @{"verifies": ["REQ-AGENT-025"]}
test('ImageAccess_OwnerOnly', async ({ page, browser }) => {
  let bearerToken: string | null = null
  let courseId: string | null = null
  let imageUrl: string | null = null

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

  await archiveOpenCourses(page, bearerToken!)

  // A fresh unauthenticated context for the ownership test.  This context has
  // no cookies (no session), so requests from it are treated as anonymous.
  let anonContext: BrowserContext | null = null

  try {
    const topic = `ImageUpload-OwnerOnly ${Date.now()}`
    courseId = await createCourseViaUI(page, topic)
    markKickoffSent(courseId)

    // Upload an image via page.request as demo_student.
    const csrfToken = await getCsrfToken(page)
    const pngBytes = makeTinyPng()

    const uploadResp = await page.request.post(`/api/v1/courses/${courseId}/images`, {
      headers: { 'X-CSRF-Token': csrfToken },
      multipart: {
        image: {
          name: 'owner-test.png',
          mimeType: 'image/png',
          buffer: pngBytes
        }
      }
    })

    expect(
      uploadResp.status(),
      `Image upload must succeed with 201, got ${uploadResp.status()}`
    ).toBe(201)

    const uploadBody = await uploadResp.json() as { url?: string }
    imageUrl = uploadBody.url!
    expect(imageUrl, 'Upload response must include url').toBeTruthy()

    // ---- Unauthenticated access must return 401 (REQ-AGENT-025) -------------

    // Create a fresh browser context with no cookies (no session).
    // Using browser.newContext() gives an isolated cookie jar; the __Host-session
    // and __Host-csrf cookies from the main page are NOT present here.
    anonContext = await browser.newContext({ ignoreHTTPSErrors: true })
    const anonPage = await anonContext.newPage()

    const anonResp = await anonPage.request.get(`https://localhost${imageUrl}`)

    expect(
      anonResp.status(),
      `Unauthenticated GET of an image must return 401, got ${anonResp.status()}`
    ).toBe(401)

    await anonPage.close()

  } finally {
    if (anonContext) await anonContext.close()
    if (courseId) cleanupCourse(courseId)
  }
})

// ===========================================================================
// Test 4 — HomeworkImageAttach_ControlAndCountFeedback
// ===========================================================================
//
// ENDPOINT STATUS (updated in-sprint):
//   GET /api/v1/courses/{id}/homework/{hwId} is NOW IMPLEMENTED.
//   The backend mounts (via submission.Handler.Routes):
//     GET  /courses/{id}/homework/{homeworkId}          → returns {id, title, description, due_date?}
//     POST /courses/{id}/homework/{homeworkId}/submissions
//     GET  /courses/{id}/homework/{homeworkId}/submissions/latest
//
//   Sprint 15 e2e discovered the endpoint was missing; it was added in-sprint
//   before this test was updated.  REQ-FECONTENT-220/221/222 are now FULLY
//   verified end-to-end.
//
//   COST GUARD: No submission is ever posted.  Attaching images to the page
//   does NOT upload them (the upload only fires on form submit via onSubmit()).
//   No network calls are made to /submissions in this test.

// @{"verifies": ["REQ-FECONTENT-220", "REQ-FECONTENT-221", "REQ-FECONTENT-222"]}
test('HomeworkImageAttach_ControlAndCountFeedback', async ({ page }) => {
  let bearerToken: string | null = null
  let courseId: string | null = null
  let hwId: string | null = null

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

  await archiveOpenCourses(page, bearerToken!)

  try {
    // Create a course and immediately put it in active state so the router does
    // not bounce the student out of the homework URL.
    const topic = `HomeworkImageAttach ${Date.now()}`
    courseId = await createCourseViaUI(page, topic)
    markKickoffSent(courseId)

    // Advance the course to 'active' so the router guard allows /homework/{hwId}
    // navigation (the homework route has no status guard itself, but an intake-
    // status course will only have a card linking to /intake, not /hub or homework).
    setCourseStatus(courseId, 'active')

    // Seed a homework row via the DB helper (no AI call needed).
    // The seeded title 'E2E Test Homework' will be rendered as .homework-title
    // once the GET /homework/{hwId} endpoint returns it.
    const seededTitle = 'E2E Test Homework'
    hwId = seedHomework(courseId, seededTitle)
    expect(hwId, 'seedHomework must return a valid UUID').toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
    )

    // Navigate in-SPA to the homework submission page.  The homework route is:
    //   /courses/:id/homework/:hwId
    // We navigate via click to keep the Pinia token alive, then use
    // history.pushState + popstate to push the final homework URL without a
    // hard page.goto() that would destroy the in-memory auth token.
    await page.click('nav.nav-links a[href="/courses"]')
    await expect(page).toHaveURL(/\/courses$/, { timeout: 10_000 })

    // Click the course card — an active course links to /courses/{id}/hub.
    await page.locator('.course-card', { hasText: topic }).click()
    await page.waitForURL(
      url => url.pathname.includes(`/courses/${courseId}`),
      { timeout: 10_000 }
    )

    // Push the homework route via Vue router (popstate) so the in-memory token
    // is preserved.  Vue Router listens on popstate in web history mode.
    await page.evaluate(
      ([cId, hId]) => {
        window.history.pushState({}, '', `/courses/${cId}/homework/${hId}`)
        window.dispatchEvent(new PopStateEvent('popstate', { state: {} }))
      },
      [courseId, hwId]
    )

    await expect(page).toHaveURL(
      new RegExp(`/courses/${courseId}/homework/${hwId}`),
      { timeout: 10_000 }
    )

    // ---- REQ-FECONTENT-220: homework title renders; no error state ----------
    //
    // GET /api/v1/courses/{id}/homework/{hwId} now returns {id, title, description, due_date?}.
    // HomeworkSubmissionView.vue renders <h1 class="homework-title">{{ homework.title }}</h1>
    // inside the v-else-if="homework" branch.  The error state (.error-message showing
    // "Failed to load homework details") must NOT be visible.

    const homeworkTitle = page.locator('.homework-title')
    await expect(homeworkTitle).toBeVisible({ timeout: 8_000 })
    await expect(homeworkTitle).toContainText(seededTitle)

    // The error state must be absent — the endpoint now responds successfully.
    const errorDiv = page.locator('.error-message')
    expect(
      await errorDiv.count(),
      'Error state must not render when GET /homework/{hwId} returns 200'
    ).toBe(0)

    // ---- REQ-FECONTENT-220: "Add Images" control is visible and enabled -----
    //
    // HomeworkSubmissionView.vue renders:
    //   <button class="attach-images-button" :disabled="isUploading || pendingImages.length >= MAX_HOMEWORK_IMAGES">
    //     + Add Images
    //   </button>
    // With 0 images attached and no upload in progress, it must be enabled.

    const addImagesButton = page.locator('.attach-images-button')
    await expect(addImagesButton).toBeVisible()
    await expect(addImagesButton).toBeEnabled()

    // ---- REQ-FECONTENT-221/222: attach 2 PNG fixtures → thumbnails + count --
    //
    // The hidden file input for images has:
    //   accept="image/png,image/jpeg,image/gif,image/webp" multiple
    // setInputFiles can pass multiple files at once; we use two tiny PNGs.
    // After attaching, HomeworkSubmissionView renders:
    //   - One .image-preview-item per image (inside .image-preview-grid)
    //   - <p class="image-count-feedback">2 / 8 images attached</p>

    const pngBytes = makeTinyPng()
    const imageFileInput = page.locator('input[type="file"][accept*="image/png"][multiple]')

    await imageFileInput.setInputFiles([
      { name: 'hw-image-1.png', mimeType: 'image/png', buffer: pngBytes },
      { name: 'hw-image-2.png', mimeType: 'image/png', buffer: pngBytes }
    ])

    // Assert 2 preview thumbnails are visible.
    const previewItems = page.locator('.image-preview-item')
    await expect(previewItems).toHaveCount(2, { timeout: 5_000 })

    // Assert the count feedback reads "2 / 8 images attached".
    const countFeedback = page.locator('.image-count-feedback')
    await expect(countFeedback).toBeVisible()
    await expect(countFeedback).toContainText('2 / 8 images attached')

    // ---- REQ-FECONTENT-222: cap-limit error triggers when total exceeds 8 ---
    //
    // onImageFileSelect() loops over selected files and checks
    //   if (pendingImages.value.length >= MAX_HOMEWORK_IMAGES) { set error; break }
    // before each add.  With 2 images already attached, selecting 7 more:
    //   - adds files #3–#8 (6 adds bring total to 8)
    //   - on the 7th file the length is already 8, triggering the error message:
    //     "Maximum 8 images per submission."
    // The error element is:
    //   <p v-if="imageValidationError" class="error-message inline-error">
    //     {{ imageValidationError }}
    //   </p>

    await imageFileInput.setInputFiles([
      { name: 'hw-extra-1.png', mimeType: 'image/png', buffer: pngBytes },
      { name: 'hw-extra-2.png', mimeType: 'image/png', buffer: pngBytes },
      { name: 'hw-extra-3.png', mimeType: 'image/png', buffer: pngBytes },
      { name: 'hw-extra-4.png', mimeType: 'image/png', buffer: pngBytes },
      { name: 'hw-extra-5.png', mimeType: 'image/png', buffer: pngBytes },
      { name: 'hw-extra-6.png', mimeType: 'image/png', buffer: pngBytes },
      { name: 'hw-extra-7.png', mimeType: 'image/png', buffer: pngBytes }
    ])

    // The count-limit validation error must now be visible.
    const countLimitError = page.locator('.error-message.inline-error')
    await expect(countLimitError).toBeVisible({ timeout: 3_000 })
    await expect(countLimitError).toContainText(/Maximum 8 images/i)

    // Total attached must be capped at 8 (the 7 extra could only add 6 more).
    await expect(previewItems).toHaveCount(8)
    await expect(countFeedback).toContainText('8 / 8 images attached')

    // The "Add Images" button is now disabled (pendingImages.length >= MAX_HOMEWORK_IMAGES).
    await expect(addImagesButton).toBeDisabled()

    // COST GUARD: We do NOT click "Upload Submission".  No POST to /submissions
    // is made; the grading runner's background poller is never triggered.

  } finally {
    // Cleanup: archive the course so the single-active-course constraint does
    // not block subsequent test runs.
    if (courseId) cleanupCourse(courseId)
  }
})
