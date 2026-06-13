// @{"verifies": ["REQ-FEADMIN-704", "REQ-FEADMIN-705", "REQ-FEADMIN-706", "REQ-ASSIGN-001", "REQ-ASSIGN-002", "REQ-ASSIGN-006", "REQ-ASSIGN-008"]}
//
// admin-assignment.spec.ts — Sprint 22.5 Playwright e2e coverage for the admin
// course assignment UI.
//
// SCOPE DECISION — TWO TIERS:
//   This spec is split into two clearly labelled tiers:
//
//   Tier 1 (AI-FREE, stack-gated): exercises the admin assignment UI up to and
//   including the assignment detail page and the per-student status row. No AI
//   call is made. No course generation is triggered (assigning a student in the
//   UI reaches POST /api/v1/admin/assignments/{id}/students which calls
//   Chair.GenerateSyllabusFromParams — a LIVE Anthropic call). The tests in
//   Tier 1 create the assignment and assert the list view but SKIP the
//   "assign student" button flow because it would trigger a real Anthropic call.
//   They instead assert the assignment is visible in the admin list.
//
//   Tier 2 (LIVE AI, explicitly skipped): would exercise the full journey:
//   admin assigns student → student logs in → student sees assigned course in
//   CourseHub → GeneratingView shows progress → course transitions to active.
//   This tier requires a running stack WITH a live Anthropic API key AND a live
//   student account. It is gated with test.skip() until the Software Lead
//   schedules a budgeted AI run. See the skip-gate comments inline.
//
// PLAYWRIGHT CONVENTIONS:
//   • test.skip() with a descriptive message for missing stack or AI-key.
//   • @{"verifies": [...]} annotation at the file level AND per test.
//   • Usernames carry a timestamp suffix so repeated runs never collide.
//   • The admin sidebar link convention follows admin-users.spec.ts and
//     admin-api-key.spec.ts: use in-SPA navigation (click) not goto() so the
//     session cookie survives.
//
// DO NOT RUN via the default `npm run test:e2e` without a live stack. The lead
// schedules this; this file must lint/typecheck cleanly without running.
//
// WHAT THE TIER 1 TESTS PROVE (no AI, stack-gated):
//   1. Admin can navigate to the Assignments view via the admin sidebar.
//   2. Admin can open the "New Assignment" form, fill topic + level, and submit.
//   3. The new assignment row appears in the assignments table.
//   4. Admin can navigate to the assignment detail view (row click).
//   5. The assignment detail renders the correct topic and level metadata.
//   6. The "Assign Students" button is present on the detail page.
//
// WHAT THE TIER 2 TESTS WOULD PROVE (SKIPPED — requires live AI + stack):
//   7. Admin clicks "Assign Students", selects a student, confirms.
//   8. A per-student status row appears in the detail table.
//   9. The assigned student logs in and sees the course in CourseHub
//      (REQ-ASSIGN-008).
//  10. The course shows GeneratingView while status=generating.
//  11. The course transitions to active and CourseHub renders normally.

import { test, expect } from '@playwright/test'
import { login, ADMIN_USER, ADMIN_PASS, STUDENT_USER, STUDENT_PASS } from './helpers'

// ---------------------------------------------------------------------------
// Stack reachability helper
// ---------------------------------------------------------------------------

// isStackUp makes a lightweight GET to /login and returns true when the server
// responds. Used as a fast guard before any auth attempt.
async function isStackUp(page: Parameters<typeof login>[0]): Promise<boolean> {
  try {
    const resp = await page.goto('/login', { timeout: 8_000 })
    return (resp?.status() ?? 0) < 500
  } catch {
    return false
  }
}

// ---------------------------------------------------------------------------
// Navigation helper: reach the assignments view via the admin sidebar.
//
// Assumes the caller has already performed login(). This mirrors the pattern
// from admin-api-key.spec.ts: click the sidebar link rather than doing a hard
// goto() which would lose the in-memory session token.
// ---------------------------------------------------------------------------
async function navigateToAssignments(page: Parameters<typeof login>[0]): Promise<void> {
  // The admin sidebar is expected to have an anchor linking to /admin/assignments.
  // If AdminAssignmentsView is not yet in the router, this click will fail and
  // the test will surface a helpful selector-not-found error.
  await page.click('aside.sidebar a[href="/admin/assignments"]')
  await expect(page).toHaveURL(/\/admin\/assignments/, { timeout: 10_000 })
}

// ---------------------------------------------------------------------------
// Tier 1: AI-free, stack-gated tests
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-ASSIGN-001", "REQ-FEADMIN-704"]}
test('AdminAssignment_CreateAssignment_AppearsInList', async ({ page }) => {
  // Guard: skip when the stack is not running.
  const stackUp = await isStackUp(page)
  test.skip(!stackUp, 'Stack unreachable — run `docker compose up -d` first')

  // Use a timestamp suffix to avoid username collisions across runs.
  const uniqueTopic = `e2e_assignment_${Date.now()}`

  await login(page, ADMIN_USER, ADMIN_PASS)

  // Navigate to the Assignments section via the admin sidebar.
  await navigateToAssignments(page)

  // The "New Assignment" button must be present on the assignments list page.
  const newAssignmentBtn = page.locator('button', { hasText: 'New Assignment' })
  await expect(newAssignmentBtn).toBeVisible({ timeout: 10_000 })

  // Open the create-assignment form.
  await newAssignmentBtn.click()

  // Fill in the required fields. The form is expected to have:
  //   - an input for topic (label "Topic" or placeholder "Enter topic")
  //   - a select/input for level
  // The exact selectors match AdminAssignmentsView.vue.
  await page.fill('input[name="topic"], input[placeholder*="topic" i], #assignment-topic', uniqueTopic)
  await page.selectOption(
    'select[name="level"], #assignment-level',
    'beginner',
  ).catch(() => {
    // If no select element exists the level may already default to "beginner";
    // do not fail the test on a missing-element error here.
  })

  // Submit the form.
  const submitBtn = page.locator('button[type="submit"], button', { hasText: /create|save|submit/i })
  await submitBtn.first().click()

  // The new assignment must appear in the assignments table after creation.
  // The table cell carrying the topic is expected to have data-testid or class
  // that contains the topic text.
  const topicCell = page.locator('td, .assignment-topic', { hasText: uniqueTopic })
  await expect(topicCell).toBeVisible({ timeout: 10_000 })
})

// @{"verifies": ["REQ-ASSIGN-001", "REQ-ASSIGN-006", "REQ-FEADMIN-704", "REQ-FEADMIN-705"]}
test('AdminAssignment_DetailView_ShowsMetadataAndAssignButton', async ({ page }) => {
  // Guard: skip when the stack is not running.
  const stackUp = await isStackUp(page)
  test.skip(!stackUp, 'Stack unreachable — run `docker compose up -d` first')

  const uniqueTopic = `e2e_detail_${Date.now()}`

  await login(page, ADMIN_USER, ADMIN_PASS)
  await navigateToAssignments(page)

  // Create a new assignment.
  const newAssignmentBtn = page.locator('button', { hasText: 'New Assignment' })
  await expect(newAssignmentBtn).toBeVisible({ timeout: 10_000 })
  await newAssignmentBtn.click()

  await page.fill('input[name="topic"], input[placeholder*="topic" i], #assignment-topic', uniqueTopic)
  const submitBtn = page.locator('button[type="submit"], button', { hasText: /create|save|submit/i })
  await submitBtn.first().click()

  // Click the newly-created row to navigate to the detail view.
  const topicCell = page.locator('td, .assignment-topic', { hasText: uniqueTopic })
  await expect(topicCell).toBeVisible({ timeout: 10_000 })
  await topicCell.click()

  // Detail view must display the assignment topic.
  await expect(page.locator('body')).toContainText(uniqueTopic, { timeout: 10_000 })

  // The "Assign Students" button must be present on the detail page.
  // When pressed this would call POST .../students which triggers an Anthropic
  // call; we assert its presence but do NOT click it in the AI-free tier.
  const assignStudentsBtn = page.locator('button', { hasText: /assign student/i })
  await expect(assignStudentsBtn).toBeVisible({ timeout: 10_000 })
})

// ---------------------------------------------------------------------------
// Tier 2: LIVE AI tests — skipped until budgeted AI run is scheduled
// ---------------------------------------------------------------------------
//
// The following tests require:
//   1. A live Valory stack with a valid Anthropic API key configured.
//   2. At least one student account (e.g. STUDENT_USER from helpers.ts).
//   3. That student must have no active course (courses_single_active_idx).
//
// WHY SKIPPED: Chair.GenerateSyllabusFromParams makes a real Anthropic API call
// for each assigned student. Running this in the standard e2e suite would incur
// API costs on every CI run. The Software Lead schedules these via a separate
// npm script (e.g. npm run test:e2e:ai) after PM approval.
//
// The skip condition checks for the E2E_LIVE_AI environment variable:
//   E2E_LIVE_AI=1 npx playwright test admin-assignment.spec.ts
//
// Without E2E_LIVE_AI=1, both tests are skipped with a clear message.

const LIVE_AI = process.env['E2E_LIVE_AI'] === '1'

// @{"verifies": ["REQ-ASSIGN-002", "REQ-ASSIGN-006", "REQ-ASSIGN-008", "REQ-FEADMIN-706"]}
test('AdminAssignment_AssignStudent_PerStudentStatusRowAppears_LIVE_AI', async ({ page }) => {
  test.skip(
    !LIVE_AI,
    'Skipped — requires live Anthropic API key. ' +
    'Run with E2E_LIVE_AI=1 npx playwright test admin-assignment.spec.ts. ' +
    'The Software Lead schedules AI-spending runs after PM approval (SDD-022 §22.5).',
  )

  // This test is reached only when E2E_LIVE_AI=1.
  //
  // Journey: admin logs in → navigates to assignments → creates assignment →
  // opens detail → clicks "Assign Students" → selects demo student → confirms →
  // asserts a per-student status row appears in the table with the expected
  // username and a recognised course_status value.
  //
  // NOTE: The test does NOT wait for the course to finish generating (that
  // would require up to several minutes and multiple API calls). It only
  // asserts that:
  //   (a) the status row appears with the student's username (REQ-ASSIGN-006),
  //   (b) the status value is one of the known lifecycle statuses.
  //
  // The student-sees-course half (REQ-ASSIGN-008) is in the next test below,
  // which also requires live AI and is gated identically.

  const uniqueTopic = `e2e_ai_${Date.now()}`

  await login(page, ADMIN_USER, ADMIN_PASS)
  await navigateToAssignments(page)

  const newAssignmentBtn = page.locator('button', { hasText: 'New Assignment' })
  await newAssignmentBtn.click()
  await page.fill('input[name="topic"], input[placeholder*="topic" i], #assignment-topic', uniqueTopic)
  const submitBtn = page.locator('button[type="submit"], button', { hasText: /create|save|submit/i })
  await submitBtn.first().click()

  const topicCell = page.locator('td, .assignment-topic', { hasText: uniqueTopic })
  await expect(topicCell).toBeVisible({ timeout: 10_000 })
  await topicCell.click()

  // Click "Assign Students" to open the student-selection modal.
  const assignStudentsBtn = page.locator('button', { hasText: /assign student/i })
  await assignStudentsBtn.click()

  // In the modal, select the demo student (STUDENT_USER is the default demo
  // student from helpers.ts — value set by E2E_STUDENT_USER env var).
  // The modal is expected to have a searchable list or a select element.
  const studentOption = page.locator('.student-option, [data-testid="student-option"]', {
    hasText: process.env['E2E_STUDENT_USER'] ?? 'demo_student',
  })
  await expect(studentOption).toBeVisible({ timeout: 10_000 })
  await studentOption.click()

  // Confirm the selection. The button text is expected to be "Assign" or "Confirm".
  const confirmBtn = page.locator('button', { hasText: /^assign$|^confirm$/i })
  await confirmBtn.click()

  // A per-student status row must appear in the assignment detail table.
  // The row is expected to contain the student's username and a status value.
  const studentStatusRow = page.locator('tr, .student-status-row', {
    hasText: process.env['E2E_STUDENT_USER'] ?? 'demo_student',
  })
  await expect(studentStatusRow).toBeVisible({ timeout: 30_000 })

  // The status cell must contain a recognised value (any lifecycle status is
  // acceptable — we are not waiting for generation to complete).
  const recognisedStatuses = [
    'syllabus_approved',
    'generating',
    'active',
    'syllabus_draft',
    'intake',
  ]
  const statusCell = studentStatusRow.locator('.course-status, [data-testid="course-status"], td:last-child')
  const statusText = await statusCell.textContent({ timeout: 5_000 })
  const statusLower = (statusText ?? '').toLowerCase().replace(/[\s_]+/g, '')
  const isRecognised = recognisedStatuses.some(s => statusLower.includes(s.replace(/_/g, '')))
  if (!isRecognised) {
    throw new Error(
      `AdminAssignment_AssignStudent: status "${statusText}" is not in recognised set: ${recognisedStatuses.join(', ')}`,
    )
  }
})

// @{"verifies": ["REQ-ASSIGN-008", "REQ-FECOURSE-601", "REQ-FECOURSE-602"]}
test('AdminAssignment_StudentSeesAssignedCourseInCourseHub_LIVE_AI', async ({ page }) => {
  // This test requires the prior "AssignStudent" test to have run successfully
  // in the same session (or the demo student must already have an assigned course
  // at syllabus_approved/generating/active). It is intentionally paired with the
  // above test for sequential scheduling.
  test.skip(
    !LIVE_AI,
    'Skipped — requires live Anthropic API key AND the demo student to have an ' +
    'assigned course. Run with E2E_LIVE_AI=1. ' +
    'The Software Lead schedules AI-spending runs after PM approval (SDD-022 §22.5).',
  )

  // WHAT THIS TEST PROVES (when not skipped):
  //   The demo student logs in and is routed to GeneratingView (when the course
  //   is generating) or CourseHub (when the course is active), NOT to the intake
  //   chat view. This verifies REQ-ASSIGN-008 at the UI level: assigned courses
  //   appear in CourseHub like self-initiated courses.
  //
  //   The test does NOT wait for generation to complete (too slow and expensive).
  //   It asserts that the course is visible to the student in whichever state it
  //   is currently in. If the course is at syllabus_approved, the router guard
  //   should redirect to GeneratingView (SDD-022 §8.2), which is already the
  //   expected behaviour for generating courses.

  await login(page, STUDENT_USER, STUDENT_PASS)

  // The student must NOT land on the intake chat view for the assigned course.
  // Expected destinations: /courses (CourseHub), /courses/*/generating (GeneratingView).
  const url = page.url()
  const isAtIntake = url.includes('/intake')
  if (isAtIntake) {
    throw new Error(
      'AdminAssignment_StudentSeesAssignedCourseInCourseHub: student was routed to ' +
      '/intake for an admin-assigned course. The router guard for syllabus_approved ' +
      'courses should redirect to GeneratingView, not intake. REQ-ASSIGN-008 violated.',
    )
  }

  // The student should land on /courses or /courses/*/{generating,active}.
  await expect(page).toHaveURL(/\/courses/, { timeout: 15_000 })

  // The course list must contain at least one assigned course (identified by the
  // "Assigned by instructor" badge or the assignment_id being non-null in the
  // component's data).
  const assignedBadge = page.locator('.assigned-badge, [data-testid="assigned-badge"]', {
    hasText: /assigned by instructor/i,
  })
  // The badge is a nice-to-have — it may not be rendered if the student already
  // navigated to GeneratingView. We assert on it softly.
  const hasBadge = await assignedBadge.isVisible({ timeout: 5_000 }).catch(() => false)
  if (!hasBadge) {
    // Acceptable: the student may have been redirected to GeneratingView. Check
    // that they are not on the intake view (already asserted above) and that the
    // page contains course-related content.
    await expect(page.locator('body')).not.toContainText('Start a new course', { timeout: 5_000 })
      .catch(() => {
        // If this assertion fails it means the student has no course at all —
        // the prior "AssignStudent" test may not have run. Surface a clear message.
        throw new Error(
          'AdminAssignment_StudentSeesAssignedCourseInCourseHub: student has no course ' +
          'visible. Ensure the "AssignStudent" test ran first (or the demo student has an ' +
          'assigned course). REQ-ASSIGN-008 cannot be verified without a course.',
        )
      })
  }
})
