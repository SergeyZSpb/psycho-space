import { defineConfig, devices } from '@playwright/test';

// FULL-STACK e2e: the browser drives the real Go binary (serving the embedded
// SPA and /api) against a real PostgreSQL. Nothing is stubbed — a passing run
// means the SPA, the HTTP API, the session cookie, and the SQL all agree.
//
// The sibling config (playwright.config.ts) intercepts /api in the browser and
// checks responsive layout instead; the two are complementary and both run in
// the pre-commit gate.
//
// scripts/e2e-stack.sh brings the stack up, seeds the accounts, and writes
// their session cookies to e2e-stack/.stack.json.

const PORT = Number(process.env.E2E_PORT ?? 8081);
const BASE_URL = `http://127.0.0.1:${PORT}`;

export default defineConfig({
  testDir: './e2e-stack',
  // Serial on purpose: these tests share one database, so a parallel worker
  // creating or deleting an idea would make another worker's list assertions
  // flaky for reasons that have nothing to do with the code under test.
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: [['list']],
  use: {
    baseURL: BASE_URL,
    // Videos are kept for every test, pass or fail — a recording of the real
    // stack is the cheapest way to see what a run actually did.
    video: 'on',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  // ONE project on purpose. Every project would replay the whole suite against
  // the same database, so the first one to approve the seeded pending account
  // leaves the next with nothing to approve. Viewport coverage is the stubbed
  // suite's job; this suite is about behaviour, and it runs at a phone width.
  projects: [
    {
      name: 'stack-mobile-390',
      use: { ...devices['Desktop Chrome'], viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true },
    },
  ],
  webServer: {
    command: 'bash ../scripts/e2e-stack.sh',
    url: `${BASE_URL}/healthz`,
    // Cold start compiles the SPA and the Go binary and pulls the Postgres image.
    timeout: 300_000,
    reuseExistingServer: !process.env.CI,
    stdout: 'pipe',
    stderr: 'pipe',
  },
});
