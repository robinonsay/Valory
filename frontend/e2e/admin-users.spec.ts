// @{"verifies", ["REQ-FEADMIN-020", "REQ-FEADMIN-021", "REQ-FEADMIN-022", "REQ-FEADMIN-023", "REQ-FEADMIN-024", "REQ-FEADMIN-025", "REQ-FEADMIN-100", "REQ-FEADMIN-113", "REQ-FEADMIN-114", "REQ-FEADMIN-115", "REQ-FEADMIN-116", "REQ-FEADMIN-120", "REQ-FEADMIN-130", "REQ-FEADMIN-131", "REQ-FEADMIN-132", "REQ-FEADMIN-140", "REQ-FEADMIN-141", "REQ-FEADMIN-150", "REQ-FEADMIN-160"]}
//
// admin-users.spec.ts — User management CRUD flows:
//   • Create a student account with a unique timestamped username.
//   • Assert the new user row appears in the users table.
//   • Deactivate the user and assert the status badge changes to "Inactive".
//   • Delete the user (student-only action) and assert the row is removed.

import { test, expect } from '@playwright/test'
import { login, ADMIN_USER, ADMIN_PASS } from './helpers'

// @{"verifies", ["REQ-FEADMIN-021", "REQ-FEADMIN-022", "REQ-FEADMIN-023", "REQ-FEADMIN-025", "REQ-FEADMIN-113", "REQ-FEADMIN-114", "REQ-FEADMIN-115", "REQ-FEADMIN-116", "REQ-FEADMIN-130", "REQ-FEADMIN-131", "REQ-FEADMIN-132", "REQ-FEADMIN-140", "REQ-FEADMIN-141"]}
test('AdminUsers_CreateDeactivateDeleteStudent_AppearsAndDisappearsFromTable', async ({ page }) => {
  // Use a timestamp suffix so repeated runs do not collide on the unique-username
  // constraint.  The username carries the prefix "e2e_" to make test accounts
  // identifiable in the database.
  const uniqueUsername = `e2e_${Date.now()}`

  await login(page, ADMIN_USER, ADMIN_PASS)
  // Admin login already lands on /admin/users; goto() would hard-reload and
  // wipe the in-memory session.
  await expect(page).toHaveURL(/\/admin\/users/)

  // ---- Create user --------------------------------------------------------

  await page.fill('#create-username', uniqueUsername)
  await page.fill('#create-password', 'E2eTest!2026')
  await page.selectOption('#create-role', 'student')
  // Submit the create form.
  await page.click('button.submit-btn')

  // Wait for the new row to appear in the users table.  The username cell
  // carries class "col-username" (from UserManagementView.vue).
  const usernameCell = page.locator('td.col-username', { hasText: uniqueUsername })
  await expect(usernameCell).toBeVisible({ timeout: 10_000 })

  // The initial status should be "Active".
  const newRow = page.locator('tr.user-row', { has: usernameCell })
  await expect(newRow.locator('.status-badge')).toContainText('Active')

  // ---- Deactivate user ----------------------------------------------------

  // The Deactivate button is only rendered for active users (UserManagementView.vue:
  // v-if="user.is_active").  window.confirm is intercepted to auto-accept.
  page.on('dialog', dialog => dialog.accept())
  await newRow.locator('button.deactivate-btn').click()

  // After deactivation the status badge changes to "Inactive" reactively.
  await expect(newRow.locator('.status-badge')).toContainText('Inactive', { timeout: 10_000 })

  // ---- Delete user --------------------------------------------------------

  // The Delete button is rendered for student rows regardless of active state
  // (UserManagementView.vue: v-if="user.role === 'student'").
  await newRow.locator('button.delete-btn').click()

  // After deletion the row must disappear from the DOM.
  await expect(usernameCell).not.toBeVisible({ timeout: 10_000 })
})
