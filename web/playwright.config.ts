import { defineConfig, devices } from '@playwright/test';

// Mobile-responsiveness suite. Serves the production build via `vite preview`
// and stubs /api with Playwright route interception — no Go server, no DB — so
// it can put the UI into states that are awkward to arrange for real (pending,
// blocked, a 90-character unbroken word) and assert layout at phone widths.
//
// It is NOT the end-to-end suite: playwright.stack.config.ts drives the real
// binary against a real PostgreSQL. Both run in the pre-commit gate
// (`./dev.sh e2e` and `./dev.sh e2e-stack`).

const PORT = 4173;
const BASE_URL = `http://localhost:${PORT}`;

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: [['list']],
  use: {
    baseURL: BASE_URL,
    // Kept for every test, pass or fail: a recording is the fastest way to see
    // what a mobile layout actually did.
    video: 'on',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'android-360',
      use: { ...devices['Desktop Chrome'], viewport: { width: 360, height: 800 }, isMobile: true, hasTouch: true },
    },
    {
      name: 'iphone-390',
      use: { ...devices['Desktop Chrome'], viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true },
    },
    {
      // Tablet sanity check — no tap-target rules here, just no-overflow / no-regression.
      name: 'tablet-768',
      use: { ...devices['Desktop Chrome'], viewport: { width: 768, height: 1024 } },
    },
    {
      // Desktop. Added because its ABSENCE hid a real bug for as long as it was
      // absent: the yard's plane is sized from its container while an entity was
      // a fixed 44px, so the same world drew at 19.1% dot-to-plane on a 320px
      // phone and 7.2% on a 1920px desktop — and no project here had ever opened
      // it wider than a tablet. A suite that only runs at phone widths cannot see
      // a bug that only appears above them.
      // The width-gated mobile rules (tap targets, the 44px floor) skip here by
      // design — `isMobile()` in the specs is a viewport-width test — so this
      // project's job is the ungated half: no horizontal overflow, the never-
      // scroll layout, and the cross-viewport ratio assertions.
      name: 'desktop-1440',
      use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } },
    },
  ],
  webServer: {
    command: 'npm run build && npm run preview -- --port 4173 --strictPort',
    url: BASE_URL,
    reuseExistingServer: !process.env.CI,
    timeout: 180_000,
    stdout: 'ignore',
    stderr: 'pipe',
  },
});
