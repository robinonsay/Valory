// @{"verifies": ["REQ-FEAUTH-100", "REQ-FEAUTH-101", "REQ-FEAUTH-102", "REQ-FEAUTH-118"]}
//
// branding.spec.ts — Sprint 13 branding assertions.
//
// Branding has no formal requirement of its own (no REQ-BRAND-* IDs were
// authored for Sprint 13). Tests are traced to the closest layout/login
// requirements they overlap with.
//
// What this spec verifies:
//   • The login page shows an <img alt="Valory"> whose src contains "valory.svg".
//   • The document title is "Valory — AI Professor" on the login page.
//   • After student login, the nav header shows the Valory logo (REQ-FEAUTH-118:
//     nav bar for authenticated users).
//   • After admin login, the sidebar header shows the Valory logo (REQ-FEAUTH-118).
//   • The document title is "Valory — AI Professor" on authenticated pages.

import { test, expect } from '@playwright/test'
import { login, ADMIN_USER, ADMIN_PASS, STUDENT_USER, STUDENT_PASS } from './helpers'

// ---------------------------------------------------------------------------
// Login page branding
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FEAUTH-100", "REQ-FEAUTH-101", "REQ-FEAUTH-102"]}
// Branding note: no formal branding requirement exists; traced to login form
// field requirements as these share the same LoginView component.
test('LoginPage_ShowsValoryLogo_AndCorrectTitle', async ({ page }) => {
  await page.goto('/login')

  // The login page title must identify the product (set in index.html).
  await expect(page).toHaveTitle('Valory — AI Professor')

  // The login form must show the Valory logo image (LoginView.vue logo-section).
  const logo = page.locator('img[alt="Valory"]')
  await expect(logo).toBeVisible()

  // The logo src must reference the valory.svg asset.
  const src = await logo.getAttribute('src')
  expect(src, 'Logo src must contain valory.svg').toMatch(/valory\.svg/)
})

// ---------------------------------------------------------------------------
// Authenticated student nav — logo visible
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FEAUTH-118"]}
// Branding note: no formal branding requirement exists; traced to the nav-bar
// requirement for authenticated users (REQ-FEAUTH-118 specifies a logout button
// in the navigation bar; the logo lives in the same nav bar component).
test('StudentNav_ShowsValoryLogoAfterLogin', async ({ page }) => {
  await login(page, STUDENT_USER, STUDENT_PASS)
  await expect(page).toHaveURL(/\/courses/)

  // StudentLayout.vue renders a brand link with the Valory logo in the top bar.
  // The logo is inside an <a class="brand-link"> → <img alt="Valory">.
  const navLogo = page.locator('header .brand-link img[alt="Valory"]')
  await expect(navLogo).toBeVisible()

  const src = await navLogo.getAttribute('src')
  expect(src, 'Nav logo src must contain valory.svg').toMatch(/valory\.svg/)

  // The document title is set once in index.html and is unchanged by navigation.
  await expect(page).toHaveTitle('Valory — AI Professor')
})

// ---------------------------------------------------------------------------
// Authenticated admin sidebar — logo visible
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-FEAUTH-118"]}
// Branding note: no formal branding requirement; traced to REQ-FEAUTH-118 as
// the brand link in the admin sidebar is part of the same authenticated chrome.
test('AdminSidebar_ShowsValoryLogoAfterLogin', async ({ page }) => {
  await login(page, ADMIN_USER, ADMIN_PASS)
  await expect(page).toHaveURL(/\/admin\/users/)

  // AdminLayout.vue renders a brand link inside <aside class="sidebar"> with
  // class "sidebar-brand". The logo is <img alt="Valory" class="sidebar-logo">.
  const sidebarLogo = page.locator('aside.sidebar .sidebar-brand img[alt="Valory"]')
  await expect(sidebarLogo).toBeVisible()

  const src = await sidebarLogo.getAttribute('src')
  expect(src, 'Sidebar logo src must contain valory.svg').toMatch(/valory\.svg/)

  // Document title unchanged after admin login.
  await expect(page).toHaveTitle('Valory — AI Professor')
})
