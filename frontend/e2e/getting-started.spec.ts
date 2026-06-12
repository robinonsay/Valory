// @{"verifies", ["REQ-FEONBOARD-001", "REQ-FEONBOARD-002", "REQ-FEONBOARD-003", "REQ-FEONBOARD-004", "REQ-FEONBOARD-010", "REQ-FEONBOARD-011", "REQ-FEONBOARD-012", "REQ-FEONBOARD-013", "REQ-FEONBOARD-014", "REQ-FEONBOARD-015", "REQ-FEONBOARD-016", "REQ-FEONBOARD-020", "REQ-FEONBOARD-021", "REQ-FEONBOARD-022", "REQ-FEONBOARD-023"]}
//
// getting-started.spec.ts — Getting Started view for each role:
//   • Student /getting-started renders inside StudentLayout (nav links visible)
//     and shows the 7 student steps, none of which contain admin-only content.
//   • Admin /admin/getting-started renders inside AdminLayout (sidebar visible)
//     and shows the 4 admin steps.

import { test, expect } from '@playwright/test'
import { login, ADMIN_USER, ADMIN_PASS, STUDENT_USER, STUDENT_PASS } from './helpers'

// ---------------------------------------------------------------------------
// Student Getting Started
// ---------------------------------------------------------------------------

// @{"verifies", ["REQ-FEONBOARD-001", "REQ-FEONBOARD-002", "REQ-FEONBOARD-003", "REQ-FEONBOARD-010", "REQ-FEONBOARD-011", "REQ-FEONBOARD-012", "REQ-FEONBOARD-013", "REQ-FEONBOARD-014", "REQ-FEONBOARD-015", "REQ-FEONBOARD-016"]}
test('StudentGettingStarted_ViewRendersInsideStudentLayoutWithSevenSteps', async ({ page }) => {
  await login(page, STUDENT_USER, STUDENT_PASS)
  // In-SPA navigation: the token lives only in memory (by requirement), so a
  // page.goto() hard reload would wipe the session and bounce to /login.
  await page.click('nav.nav-links a[href="/getting-started"]')
  await expect(page).toHaveURL(/\/getting-started/)

  // StudentLayout top bar must be present — "Courses" and "Getting Started"
  // nav links are rendered by StudentLayout.vue as RouterLink elements.
  await expect(page.locator('nav.nav-links a[href="/courses"]')).toBeVisible()
  await expect(page.locator('nav.nav-links a[href="/getting-started"]')).toBeVisible()

  // The heading identifies the student variant.
  await expect(page.locator('h1')).toContainText('Getting Started — Student Guide')

  // GettingStartedView.vue renders an ordered list with exactly 7 steps for
  // the student role.  Each step is an <li class="step">.
  const steps = page.locator('li.step')
  await expect(steps).toHaveCount(7)

  // Spot-check the first and last step headings to confirm the correct content
  // variant is displayed.
  await expect(steps.nth(0).locator('h2')).toContainText('Accept AI Consent')
  await expect(steps.nth(6).locator('h2')).toContainText('Track Grades and Badges')

  // Admin-only content must not appear.
  await expect(page.locator('text=Admin Guide')).toHaveCount(0)
})

// ---------------------------------------------------------------------------
// Admin Getting Started
// ---------------------------------------------------------------------------

// @{"verifies", ["REQ-FEONBOARD-002", "REQ-FEONBOARD-004", "REQ-FEONBOARD-020", "REQ-FEONBOARD-021", "REQ-FEONBOARD-022", "REQ-FEONBOARD-023"]}
test('AdminGettingStarted_ViewRendersInsideAdminLayoutWithFourSteps', async ({ page }) => {
  await login(page, ADMIN_USER, ADMIN_PASS)
  // In-SPA navigation (see student test above for why goto() is not usable).
  await page.click('aside.sidebar a[href="/admin/getting-started"]')
  await expect(page).toHaveURL(/\/admin\/getting-started/)

  // AdminLayout renders a sidebar with the navigation links defined in
  // AdminLayout.vue.  Assert that the sidebar nav is present and contains
  // the expected links.
  const sidebar = page.locator('aside.sidebar')
  await expect(sidebar).toBeVisible()
  await expect(sidebar.locator('a[href="/admin/users"]')).toBeVisible()
  await expect(sidebar.locator('a[href="/admin/audit"]')).toBeVisible()
  await expect(sidebar.locator('a[href="/admin/config"]')).toBeVisible()
  await expect(sidebar.locator('a[href="/admin/courses"]')).toBeVisible()
  await expect(sidebar.locator('a[href="/admin/getting-started"]')).toBeVisible()

  // The heading identifies the admin variant.
  await expect(page.locator('h1')).toContainText('Getting Started — Admin Guide')

  // GettingStartedView.vue renders exactly 4 steps for the admin role.
  const steps = page.locator('li.step')
  await expect(steps).toHaveCount(4)

  await expect(steps.nth(0).locator('h2')).toContainText('User Management')
  await expect(steps.nth(3).locator('h2')).toContainText('Course Oversight')

  // Student-only content must not appear.
  await expect(page.locator('text=Student Guide')).toHaveCount(0)
})
