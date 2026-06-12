// Playwright configuration for Valory end-to-end test suite.
// The suite drives the real running stack (https://localhost with a self-signed
// cert) so ignoreHTTPSErrors must be true.  Workers is forced to 1 so that
// tests are serialized: the demo student account can only hold one active course
// at a time and sharing one backend prevents cross-test interference.

import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',

  // Run test files one at a time to avoid cross-test state conflicts on the
  // single demo student's single-active-course constraint.
  fullyParallel: false,
  workers: 1,

  // Fail the build if any test.only is accidentally committed.
  forbidOnly: !!process.env.CI,

  // Two retries on CI to absorb transient network latency; zero locally so
  // failures are visible immediately.
  retries: process.env.CI ? 2 : 0,

  // Report formats: human-readable list in the terminal and an HTML report
  // written to e2e-report/ (gitignored).
  reporter: [
    ['list'],
    ['html', { outputFolder: 'e2e-report', open: 'never' }]
  ],

  // Global timeout per test.  30 s is generous for demo-environment page loads.
  timeout: 30_000,

  // Wait up to 5 s for each expect() assertion to become true.
  expect: {
    timeout: 5_000
  },

  use: {
    // Base URL is overridable via E2E_BASE_URL so CI or a remote stack can be
    // targeted without editing config.
    baseURL: process.env.E2E_BASE_URL ?? 'https://localhost',

    // The stack runs behind nginx with a self-signed certificate.
    ignoreHTTPSErrors: true,

    // Capture screenshots / traces only on failure to keep artifact sizes small.
    screenshot: 'only-on-failure',
    trace: 'on-first-retry',

    // Use chromium headless shell (already installed at ~/Library/Caches/ms-playwright).
    ...devices['Desktop Chrome']
  },

  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] }
    }
  ]
})
