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
  // NO RETRIES ON PURPOSE. A retry turns a flaky test green and hides the fact
  // that it was flaky, which is the opposite of what this suite is for — the
  // failures it has produced were real defects in the tests every time.
  retries: 0,
  reporter: [['list']],
  // THE DEFAULT IS FIVE SECONDS AND THAT WAS TOO SHORT HERE, in a way peculiar
  // to this game. «Ванягоччи» is synchronised by wall-clock server mechanics —
  // a 5 Hz broadcast, a one-second verb rate limit, and above all a speech
  // balloon that lives exactly FOUR seconds. An implicit five-second assertion
  // against a four-second balloon leaves one second of slack for a websocket
  // round trip and a render, which a loaded CI runner does not always have. This
  // is the suite's floor, not a licence to wait: an assertion resolves the
  // moment it is true, so a longer ceiling costs nothing on a green run and only
  // changes how long a genuinely broken one takes to say so.
  expect: { timeout: 15_000 },
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
