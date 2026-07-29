import { defineConfig, devices } from '@playwright/test';

// Mobile-layout suite. Serves the production build via `vite preview` and stubs
// /api with Playwright route interception — no Go server, no DB — so it can put
// the UI into states that are awkward to arrange for real (pending, blocked, a
// 90-character unbroken word) and assert layout at the width people actually
// hold.
//
// It is NOT the end-to-end suite: playwright.stack.config.ts drives the real
// binary against a real PostgreSQL. Both run in the pre-commit gate
// (`./dev.sh e2e` and `./dev.sh e2e-stack`).
//
// TWO PROJECTS, AND THEY COST DIFFERENT AMOUNTS ON PURPOSE.
//   The phone is the product: everything runs at 360, the tightest width we
//   support, where the overflow and tap-target rules actually bite. Every other
//   width is a regression guard rather than a target, and a guard does not need
//   the whole suite — so desktop runs only what is *about* width, selected by
//   the `@wide` tag. This is a deliberate trade: four full projects (578 tests,
//   16 minutes serial in CI) became one full project plus a thin wide pass
//   (~165), because a phone-first site does not earn four copies of every
//   assertion. Measured at ~1.7s per test on both a workstation and a CI runner,
//   so the only things that move the clock are test count and worker count —
//   recording video for every test costs nothing measurable, and stays on.
//
// TAG A TEST `@wide` WHEN ITS CLAIM IS ABOUT WIDTH, not merely visible at one.
//   The bar: could it fail at 1440 while passing at 360? Cross-width ratio
//   claims, "nothing overflows", the never-scroll layout, and the permanent nav
//   drawer (≥960px) all qualify — the drawer branch and the desktop plane scale
//   exist *only* above phone width, so nothing but this project can see them
//   break. A test asserting a 44px tap target or "fits the smallest screen" does
//   not: those rules are width-gated inside the specs and skip themselves above
//   600px, so running them here spends time asserting nothing.
//
//   test('…', { tag: '@wide' }, async ({ page }) => …)   // one test
//   test.describe('…', { tag: '@wide' }, () => …)        // a whole group

const PORT = 4173;
const BASE_URL = `http://localhost:${PORT}`;

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: 0,
  // CI runners have 4 vCPUs and this suite is animation- and I/O-bound rather
  // than CPU-bound, so one worker left three quarters of the machine idle and
  // made this job the longest thing in the pipeline. Locally, Playwright's
  // default (cores/2) is already right.
  workers: process.env.CI ? 2 : undefined,
  reporter: [['list']],
  // THE DEFAULT PER-TEST TIMEOUT IS THIRTY SECONDS, and the thing that exceeds
  // it is never an assertion — it is `page.goto` plus the first paint of a lazy
  // route, queued behind every other worker doing the same.
  //
  // The mechanism is worth writing down because it is invisible from any single
  // test: `vite preview` is ONE Node process serving the whole suite, and every
  // view here is a separate chunk fetched over it. Locally Playwright runs
  // cores/2 workers — eight on a sixteen-core workstation, against CI's two — so
  // the workstation is the harsher environment for exactly this, which is why
  // the failure showed up in the commit hook rather than in the pipeline. A
  // fourth game's worth of route loads was enough to push one over.
  //
  // Raising it is not a retry and hides nothing: a test that genuinely hangs
  // still fails, and a passing one is unaffected, because a test ends when it
  // ends rather than when its ceiling allows.
  timeout: 60_000,
  // THE DEFAULT ASSERTION TIMEOUT IS FIVE SECONDS, and it is too short for the
  // same reason. Almost everything here is a synchronous DOM measurement that
  // resolves instantly, but a handful open a view and wait for it to appear —
  // `enterYard` is `goto` + a click + `expect(plane).toBeVisible()`. Under a
  // saturated machine one of those measured FOURTEEN seconds.
  //
  // That is the runbook's third known flake, and the sibling stack config
  // already answered it the same way for the same reason. It is a floor, not a
  // licence to wait: an assertion resolves the instant it is true, so a higher
  // ceiling costs nothing on a green run and only changes how long a genuinely
  // broken one takes to report itself.
  expect: { timeout: 15_000 },
  use: {
    baseURL: BASE_URL,
    // Kept for every test, pass or fail: a recording is the fastest way to see
    // what a mobile layout actually did. Measured at no cost in wall time.
    video: 'on',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      // The product width. Everything runs here.
      name: 'android-360',
      use: { ...devices['Desktop Chrome'], viewport: { width: 360, height: 800 }, isMobile: true, hasTouch: true },
    },
    {
      // Desktop, `@wide` tests only. This project exists because its ABSENCE hid
      // a real bug for as long as it was absent: the yard's plane is sized from
      // its container while an entity was a fixed 44px, so the same world drew
      // at 19.1% dot-to-plane on a 320px phone and 7.2% on a 1920px desktop, and
      // no project had ever opened it wider than a tablet. A suite that only
      // runs at phone widths cannot see a bug that only appears above them —
      // which is why this guard survives even though the phone is the priority.
      name: 'desktop-1440',
      grep: /@wide/,
      use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } },
    },
  ],
  webServer: {
    command: 'npm run build && npm run preview -- --port 4173 --strictPort',
    url: BASE_URL,
    // NEVER REUSE, and the runbook has a section on why. This suite serves the
    // built SPA out of a directory that the `web` step of the same pre-commit
    // run rebuilds moments earlier, so a server left alive by an earlier run is
    // serving a directory being rewritten underneath it. What that produces is
    // not a slow page, it is a BLANK one — a chunk that 404s while the app is
    // booting — and it presents as a sixty-second wait for a button on
    // whichever lazily-routed view the run happened to reach first, which is a
    // different test every time and looks exactly like flakiness.
    //
    // The cost of always starting fresh is nothing: the webServer command
    // rebuilds on every invocation anyway, so reuse only ever saved the process
    // spawn. The benefit is that a leftover server now fails loudly on the port
    // instead of quietly serving yesterday's bundle.
    reuseExistingServer: false,
    timeout: 180_000,
    stdout: 'ignore',
    stderr: 'pipe',
  },
});
