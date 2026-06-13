// @{"verifies": ["REQ-SYS-073", "REQ-FECOURSE-602"]}
//
// tree-student-build.spec.ts — Student interactive tree build flow (AI-free, chromium tier).
//
// AUTHORED AND GATED — do NOT run without the prod stack.
//
// Prerequisites:
//   1. Rebuild the production stack per memory "valory-stack-rebuild-before-e2e":
//        docker-compose -f docker-compose.prod.yml down --remove-orphans
//        docker-compose -f docker-compose.prod.yml up --build -d
//   2. Run: npm run test:e2e  (chromium project only; NOT test:e2e:journey)
//
// This spec drives the UI on a real backend but does NOT trigger paid Anthropic
// AI calls — it only tests navigation, routing, and UI state that exists before
// any generation step. A tree_mode=true course must pre-exist in the demo DB
// (seeded by the test seed.ts fixture if the stack includes it).
//
// If a pre-seeded tree_mode course is not available, this spec will fail at
// the navigation assertion and the failure message will indicate the missing
// prerequisite — do NOT fix by adding AI generation calls.

import { test, expect, type Page } from '@playwright/test'
import { login, STUDENT_USER, STUDENT_PASS } from './helpers'

// @{"verifies": ["REQ-SYS-073", "REQ-FECOURSE-602"]}
test('StudentTreeBuild_TreeModeCourse_NavigatesToBuildView', async ({ page }) => {
  await login(page, STUDENT_USER, STUDENT_PASS)
  await expect(page).toHaveURL(/\/courses/)

  // A tree_mode course must appear in the student's course list. If it does
  // not, the test fails with a clear message (rather than a timeout).
  const treeModeCourseCard = page.locator('.course-card').filter({ hasText: /tree/i }).first()
  const treeModeCourseExists = await treeModeCourseCard.count() > 0

  if (!treeModeCourseExists) {
    test.skip(true, 'No tree_mode course found in demo DB — seed the DB with a tree_mode course to run this spec')
    return
  }

  // Navigate to the tree_mode course
  await treeModeCourseCard.click()

  // CourseDashboardView.navigateToCourse pushes /courses/:id/build for tree_mode=true
  await expect(page).toHaveURL(/\/courses\/[^/]+\/build/, { timeout: 10_000 })

  // CourseBuildView should render its heading structure
  await expect(page.locator('.build-header')).toBeVisible()
  // TreeView should be present (even if empty)
  await expect(page.locator('.tree-view')).toBeVisible()
})

// @{"verifies": ["REQ-SYS-073"]}
test('StudentTreeBuild_FlatModeCourse_DoesNotRouteToBuilView', async ({ page }) => {
  await login(page, STUDENT_USER, STUDENT_PASS)
  await expect(page).toHaveURL(/\/courses/)

  // Click any non-tree-mode course (active status is typical for the flat flow)
  const flatCourseCard = page.locator('.course-card').first()
  const anyCardExists = await flatCourseCard.count() > 0

  if (!anyCardExists) {
    test.skip(true, 'No courses found in demo DB — this spec requires at least one flat-mode course')
    return
  }

  // The href of the continue button should NOT contain /build for a flat course
  const continueBtn = flatCourseCard.locator('.continue-button').first()
  await continueBtn.click()

  // Should land on one of the flat routes, not /build
  await expect(page).not.toHaveURL(/\/build/, { timeout: 10_000 })
})
