import { expect, test, type BrowserContext, type Page } from '@playwright/test';
import { loginAs } from './fixtures';

// «Ванягоччи» against the real stack: two browsers, two seeded accounts, one Go
// binary, one real WebSocket each. Nothing is stubbed — a pass here means the
// upgrade, the auth check, the origin check, the hub, the game's inbound handler
// and its 5 Hz broadcast all agree with the client.
//
// This is the test the whole iteration exists to make possible, and it is the
// one that would have caught every wiring mistake the unit tests cannot see:
// the stubbed suite fakes the socket, so it would pass just as happily against a
// server that never registered the handler at all.
//
// `loginAs` is imported rather than copied because the seeded-account harness is
// platform, not a game's property — the same reason a game shares `apiFetch`.

/** The two accounts the stack seeds as *approved*; anything else is refused. */
const PLAYER_A = 'user' as const;
const PLAYER_B = 'superadmin' as const;

/**
 * The project's viewport, restated. `browser.newContext()` does not inherit the
 * project's `use` block, and a desktop-sized context would quietly stop
 * exercising the phone layout this game is built for.
 */
const PHONE = {
  viewport: { width: 390, height: 844 },
  isMobile: true,
  hasTouch: true,
} as const;

/** Opens a logged-in phone-sized page already standing in the yard. */
async function enterYard(context: BrowserContext, baseURL: string): Promise<Page> {
  const page = await context.newPage();
  await page.goto(`${baseURL}/app/game-vanyagotchi`);
  await page.getByRole('button', { name: 'Во двор' }).click();
  await expect(page.locator('[data-test="plane"]')).toBeVisible();
  return page;
}

const dots = (page: Page) => page.locator('[data-test="peer"]');

/** Every dot's normalised x, read from the custom property the stylesheet uses. */
async function xs(page: Page): Promise<number[]> {
  return page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>('[data-test="peer"]')].map((el) =>
      Number.parseFloat(getComputedStyle(el).getPropertyValue('--x')),
    ),
  );
}

test('two players share one plane, and a move crosses between them', async ({
  browser,
  baseURL,
}) => {
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const ctxA = await browser.newContext(PHONE);
  const ctxB = await browser.newContext(PHONE);
  try {
    await loginAs(ctxA, PLAYER_A);
    await loginAs(ctxB, PLAYER_B);

    const pageA = await enterYard(ctxA, base);
    const pageB = await enterYard(ctxB, base);

    // Each sees both of them. The server builds this from its own connection
    // list every tick, so it is proof the two sockets really are in one room.
    await expect(dots(pageA)).toHaveCount(2);
    await expect(dots(pageB)).toHaveCount(2);
    await expect(pageA.getByText('во дворе: 2')).toBeVisible();

    // A taps a quarter across. Nothing local happens; the position is only real
    // once the server has clamped it and sent it back to everybody.
    const box = await pageA.locator('[data-test="plane"]').boundingBox();
    expect(box).not.toBeNull();
    await pageA.mouse.click(
      (box?.x ?? 0) + (box?.width ?? 0) * 0.25,
      (box?.y ?? 0) + (box?.height ?? 0) * 0.5,
    );

    // B learns about it. Both start at the spawn point (0.5), so a dot arriving
    // near 0.25 on B's screen can only have come through the server.
    await expect
      .poll(async () => (await xs(pageB)).some((x) => Math.abs(x - 0.25) < 0.06), {
        message: "B never saw A's move arrive",
        timeout: 10_000,
      })
      .toBe(true);

    // And A sees it too — from the broadcast, not from a local guess.
    await expect
      .poll(async () => (await xs(pageA)).some((x) => Math.abs(x - 0.25) < 0.06), {
        timeout: 10_000,
      })
      .toBe(true);

    // B leaves. A's plane empties down to one without anything telling the game
    // that B went — the roster is rebuilt from the hub on every tick.
    await ctxB.close();
    await expect(dots(pageA)).toHaveCount(1, { timeout: 10_000 });
    await expect(pageA.getByText('во дворе: 1')).toBeVisible();
  } finally {
    await ctxA.close();
    await ctxB.close().catch(() => undefined);
  }
});

test('the yard survives leaving the route and coming back', async ({ browser, baseURL }) => {
  // The socket is owned at module scope precisely so a route change does not
  // cost a handshake and a slot against the per-account connection cap. This
  // drives the churn as a user would: client-side navigation away and back, with
  // no page load in between to hide a reconnect.
  //
  // Deliberately at a desktop width. Below Vuetify's `md` the nav is a temporary
  // overlay that has to be opened first, and the shell peeks it open on load —
  // so clicking a nav link on a phone races the peek's own auto-close for the
  // drawer's state. Widening makes the drawer permanent, which removes a source
  // of flakiness that has nothing to do with what this test is about. The phone
  // layout is covered by the stubbed suite and by the two-player test above.
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  try {
    await loginAs(context, PLAYER_A);
    const page = await enterYard(context, base);
    await expect(dots(page)).toHaveCount(1);

    const nav = page.locator('.v-navigation-drawer');
    await nav.getByRole('link', { name: 'Вишлист' }).click();
    await expect(page).toHaveURL(/\/app\/wishlist/);
    await expect(page.locator('[data-test="plane"]')).toHaveCount(0);

    await nav.getByRole('link', { name: 'Ванягоччи' }).click();
    await page.getByRole('button', { name: 'Во двор' }).click();
    await expect(dots(page)).toHaveCount(1, { timeout: 10_000 });
    await expect(page.getByText('на связи')).toBeVisible();
  } finally {
    await context.close();
  }
});
