import { execFileSync } from 'node:child_process';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
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
/** The catalogue the server actually served, so nothing here invents content. */
const CONFIG_URL = '/api/game-vanyagotchi/config';

/** Every dot's normalised x, read from the custom property the stylesheet uses. */
async function xs(page: Page): Promise<number[]> {
  return page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>('[data-test="peer"]')].map((el) =>
      Number.parseFloat(getComputedStyle(el).getPropertyValue('--x')),
    ),
  );
}

/**
 * The caller's OWN dot, normalised, read the same way.
 *
 * Read off the custom properties rather than off a bounding box because that is
 * where a position lives and nowhere else — a measured pixel offset would also
 * be measuring the plane's size, its border radius and whatever the CSS
 * transition happened to be doing at that instant.
 */
async function you(page: Page): Promise<{ x: number; y: number }> {
  return page.evaluate(() => {
    const el = document.querySelector<HTMLElement>('[data-test="peer"][data-you="1"]');
    // NaN rather than a throw, so a caller inside `expect.poll` retries across
    // the moment a reconnect takes the dot away instead of failing on it.
    if (!el) return { x: Number.NaN, y: Number.NaN };
    const style = getComputedStyle(el);
    return {
      x: Number.parseFloat(style.getPropertyValue('--x')),
      y: Number.parseFloat(style.getPropertyValue('--y')),
    };
  });
}

/** One skin as the catalogue describes it. Mirrored from the config response. */
interface WireSkin {
  key: string;
  emoji: string;
  image?: string;
}

/**
 * The skin a pet gets by default, read from the catalogue the server served.
 *
 * Read rather than written down so this file states no content of its own: a
 * face asserted against a hardcoded emoji would prove the client can draw an
 * emoji, and a face asserted against the served catalogue proves the key on the
 * wire was resolved against it.
 */
async function defaultSkin(page: Page): Promise<WireSkin> {
  const res = await page.request.get(CONFIG_URL);
  expect(res.status(), `GET ${CONFIG_URL}`).toBe(200);
  const cfg = (await res.json()) as { skins: WireSkin[]; default_skin: string };
  const skin = cfg.skins.find((s) => s.key === cfg.default_skin);
  expect(skin, `the catalogue has no skin for its own default ${cfg.default_skin}`).toBeDefined();
  return skin as WireSkin;
}

/** The plane's box, for turning a normalised target into a click. */
async function tapAt(page: Page, x: number, y: number): Promise<void> {
  const box = await page.locator('[data-test="plane"]').boundingBox();
  expect(box, 'the plane has no box to tap').not.toBeNull();
  await page.mouse.click((box?.x ?? 0) + (box?.width ?? 0) * x, (box?.y ?? 0) + (box?.height ?? 0) * y);
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

/**
 * Takes the server away and gives it back, exactly as a deploy does.
 *
 * Blocks until the replacement answers `/healthz`; roughly a second, since
 * nothing is rebuilt. Returns the new pid, which is what proves a restart
 * actually happened rather than the health check having been answered by the
 * process that was on its way out.
 */
function restartServer(): string {
  const script = join(
    dirname(fileURLToPath(import.meta.url)),
    '..',
    '..',
    'scripts',
    'e2e-stack-restart.sh',
  );
  return execFileSync('bash', [script], { encoding: 'utf8', timeout: 90_000 }).trim();
}

test('two open pages survive a restart of the binary', async ({ browser, baseURL }) => {
  // The reason this test exists: every production deploy is a restart of exactly
  // this shape, it happens several times a day, and until now nothing proved
  // that a page which was open across one ever came back. It is also the only
  // test in the suite that exercises the reconnect path end to end — the stubbed
  // suite fakes the socket, so it would pass against a client that gave up.
  test.setTimeout(120_000);
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const ctxA = await browser.newContext(PHONE);
  const ctxB = await browser.newContext(PHONE);
  try {
    await loginAs(ctxA, PLAYER_A);
    await loginAs(ctxB, PLAYER_B);
    const pageA = await enterYard(ctxA, base);
    const pageB = await enterYard(ctxB, base);
    await expect(dots(pageA)).toHaveCount(2);
    await expect(dots(pageB)).toHaveCount(2);

    const before = await pageA.locator('[data-test="peer"]').count();
    expect(before).toBe(2);

    restartServer();

    // Neither page was touched, and neither was reloaded. They have to notice
    // and recover on their own — including asking who they are again, since the
    // pseudonym is derived from a key that died with the old process.
    await expect(pageA.getByText('на связи')).toBeVisible({ timeout: 60_000 });
    await expect(pageB.getByText('на связи')).toBeVisible({ timeout: 60_000 });
    await expect(dots(pageA)).toHaveCount(2, { timeout: 30_000 });
    await expect(dots(pageB)).toHaveCount(2, { timeout: 30_000 });

    // Still in the yard, never bounced back to the intro.
    await expect(pageA.getByRole('button', { name: 'Во двор' })).toHaveCount(0);

    // And the socket is genuinely live again rather than merely labelled so: a
    // move made after the restart has to cross to the other player.
    const box = await pageA.locator('[data-test="plane"]').boundingBox();
    await pageA.mouse.click(
      (box?.x ?? 0) + (box?.width ?? 0) * 0.8,
      (box?.y ?? 0) + (box?.height ?? 0) * 0.5,
    );
    await expect
      .poll(async () => (await xs(pageB)).some((x) => Math.abs(x - 0.8) < 0.06), {
        message: 'a move made after the restart never reached the other player',
        timeout: 30_000,
      })
      .toBe(true);
  } finally {
    await ctxA.close();
    await ctxB.close();
  }
});

test('the same account on two devices is ONE Ваня', async ({ browser, baseURL }) => {
  // The bug this fixes, reproduced the way it was found: sign in twice and you
  // used to get two dots. One account is one Ваня, wherever it is connected
  // from, and a move from either device moves that one.
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const phone = await browser.newContext(PHONE);
  const laptop = await browser.newContext(PHONE);
  try {
    await loginAs(phone, PLAYER_A);
    await loginAs(laptop, PLAYER_A); // the SAME account
    const pagePhone = await enterYard(phone, base);
    const pageLaptop = await enterYard(laptop, base);

    await expect(dots(pagePhone)).toHaveCount(1);
    await expect(dots(pageLaptop)).toHaveCount(1);
    await expect(pagePhone.getByText('во дворе: 1')).toBeVisible();

    // A move from the laptop moves the Ваня the phone is watching.
    const box = await pageLaptop.locator('[data-test="plane"]').boundingBox();
    await pageLaptop.mouse.click(
      (box?.x ?? 0) + (box?.width ?? 0) * 0.2,
      (box?.y ?? 0) + (box?.height ?? 0) * 0.5,
    );
    await expect
      .poll(async () => (await xs(pagePhone)).some((x) => Math.abs(x - 0.2) < 0.06), {
        message: "the phone never saw the laptop's move",
        timeout: 15_000,
      })
      .toBe(true);

    // And each device knows which entity is its own.
    await expect(pagePhone.locator('[data-test="peer"][data-you="1"]')).toHaveCount(1);
    await expect(pageLaptop.locator('[data-test="peer"][data-you="1"]')).toHaveCount(1);
  } finally {
    await phone.close();
    await laptop.close();
  }
});

test("each player sees the OTHER's Ваня rather than an anonymous dot", async ({
  browser,
  baseURL,
}) => {
  // The yard used to be a field of identical circles: a roster entry was an id
  // and a pair of coordinates, and the only thing that distinguished two players
  // was the colour derived from their pseudonyms. It now carries what each
  // entity LOOKS like, for everybody rather than only for the caller, which is
  // the difference between one shared world and two private ones.
  //
  // Everything asserted below is read back from what the SERVER served: the face
  // is checked against the catalogue's own default skin, fetched over HTTP in
  // this test rather than written down in it. A fixture would only prove that
  // the client can draw a thing it was handed.
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const ctxA = await browser.newContext(PHONE);
  const ctxB = await browser.newContext(PHONE);
  try {
    await loginAs(ctxA, PLAYER_A);
    await loginAs(ctxB, PLAYER_B);
    const pageA = await enterYard(ctxA, base);
    const pageB = await enterYard(ctxB, base);

    await expect(dots(pageA)).toHaveCount(2);
    await expect(dots(pageB)).toHaveCount(2);
    // The hello has been answered, so "not me" identifies the other player
    // without either page having to know the other's pseudonym.
    await expect(pageA.locator('[data-test="peer"][data-you="1"]')).toHaveCount(1);
    await expect(pageB.locator('[data-test="peer"][data-you="1"]')).toHaveCount(1);

    const skin = await defaultSkin(pageA);

    for (const [page, who] of [
      [pageA, 'A'],
      [pageB, 'B'],
    ] as const) {
      const theirs = page.locator('[data-test="peer"]:not([data-you="1"])');
      await expect(theirs, `${who} should see exactly one other player`).toHaveCount(1);

      const theirFace = theirs.locator('[data-test="peer-face"]');
      await expect(theirFace, `${who} sees a faceless neighbour`).toHaveCount(1);
      // The art key on the wire resolved against the catalogue that came with
      // it. Both branches, because uploading a sprite for дядя Ваня is a
      // backend-only change and this test must not be what breaks when it lands.
      if (skin.image) {
        await expect(theirFace.locator('img.peer-sprite')).toHaveCount(1);
      } else {
        await expect(theirFace).toHaveText(skin.emoji);
      }
      // A pose the server decided. Not pinned to a value, because the shared
      // database means an earlier test may have left either Ваня in any state —
      // what is asserted is that it is one of the three the server can send,
      // which a missing or invented field would not be. The pose TRACKING a
      // pet's real condition is pinned end to end in the sibling pet spec.
      await expect(theirFace).toHaveAttribute('data-condition', /^(fine|poorly|dead)$/);

      // And the caller's own dot has one too — appearance is for everybody, not
      // a swap of who gets to be anonymous.
      await expect(
        page.locator('[data-test="peer"][data-you="1"] [data-test="peer-face"]'),
      ).toHaveCount(1);
    }
  } finally {
    await ctxA.close();
    await ctxB.close();
  }
});

test('a Ваня is still standing where you left him after a restart', async ({ browser, baseURL }) => {
  // THE test of this slice, and the one with the least competition: the flush it
  // exercises runs on shutdown and nowhere else, so outside a deploy nothing in
  // the world ever executes that path. Every production deploy is exactly this
  // shape, several times a day.
  //
  // Presence is in memory and dies with the process — that is the design, and
  // the sibling test above proves the reconnect. What is new is that a POSITION
  // outlives it: the server writes everybody still standing to Postgres as it
  // goes down, and puts them back when their client says hello to the
  // replacement. A restart is what makes that unambiguous. The old process's map
  // is gone, so a dot that comes back anywhere other than the middle of the yard
  // can only have been read out of the database.
  test.setTimeout(120_000);
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const context = await browser.newContext(PHONE);
  try {
    await loginAs(context, PLAYER_A);
    const page = await enterYard(context, base);
    await expect(dots(page)).toHaveCount(1);
    await expect(page.locator('[data-test="peer"][data-you="1"]')).toHaveCount(1);

    // A corner no other test in this suite walks to, so a pass cannot be
    // inherited from a position an earlier test happened to leave behind.
    await tapAt(page, 0.15, 0.85);
    // BOTH coordinates, and a window nothing else can occupy.
    //
    // This was a one-axis poll with a ±0.05 tolerance, and it made the test fail
    // for a reason that had nothing to do with the code: an earlier test in this
    // file taps at 20% across, which stores x = 0.1999999916618639 — a hair
    // under 0.2, and therefore 0.04999999 from the target, INSIDE the tolerance.
    // The poll was satisfied by the position left over from that test one
    // millisecond after the click, before any round trip could have happened,
    // and the guard below then caught its equally stale y. Distance from the
    // point actually tapped cannot be satisfied by anywhere else in the yard.
    await expect
      .poll(
        async () => {
          const p = await you(page);
          return Math.hypot(p.x - 0.15, p.y - 0.85);
        },
        { message: 'the move never came back from the server', timeout: 15_000 },
      )
      .toBeLessThan(0.02);

    const before = await you(page);
    // Guard against the assertion after the restart being satisfied by the
    // spawn point: the whole claim is "not back in the middle".
    expect(Math.abs(before.x - 0.5), `walked to x=${before.x}, which is the spawn`).toBeGreaterThan(
      0.2,
    );
    expect(Math.abs(before.y - 0.5), `walked to y=${before.y}, which is the spawn`).toBeGreaterThan(
      0.2,
    );

    restartServer();

    // The page was not touched and not reloaded: it notices, reconnects, and
    // says hello to a process that has never heard of it.
    await expect(page.getByText('на связи')).toBeVisible({ timeout: 60_000 });
    await expect(dots(page)).toHaveCount(1, { timeout: 30_000 });
    await expect(page.locator('[data-test="peer"][data-you="1"]')).toHaveCount(1, {
      timeout: 30_000,
    });

    await expect
      .poll(async () => (await you(page)).x, {
        message: 'the Ваня came back somewhere else — the position did not survive the restart',
        timeout: 30_000,
      })
      .toBeCloseTo(before.x, 1);
    expect((await you(page)).y, 'the y coordinate did not survive the restart').toBeCloseTo(
      before.y,
      1,
    );

    // And he is still a player rather than a frozen snapshot: a move made after
    // the restart still reaches the server and comes back.
    await tapAt(page, 0.6, 0.4);
    await expect
      .poll(async () => (await you(page)).x, { timeout: 20_000 })
      .toBeCloseTo(0.6, 1);
  } finally {
    await context.close();
  }
});
