import { execFileSync } from 'node:child_process';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test, type BrowserContext, type Page } from '@playwright/test';
import { loginAs, type SeededAccount } from './fixtures';
import { forgetWorldObjects, psql, uuid } from './vanyagotchi-db';

// A clean yard before every test. Objects are durable, shared and drawn as
// ordinary entities, so a deposit one test leaves behind is an extra thing on
// the plane that the next test's counts have no idea about — and it lasts ten
// minutes, which is longer than the whole suite.
test.beforeEach(() => {
  forgetWorldObjects();
});

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
  return enterYardWith(context, baseURL, async () => undefined);
}

/**
 * The same, with a chance to set the page up BEFORE it navigates.
 *
 * Anything installed with `addInitScript` has to be in place before the first
 * document loads, so a recorder cannot be attached by a test that already has a
 * page in the yard.
 */
async function enterYardWith(
  context: BrowserContext,
  baseURL: string,
  prepare: (page: Page) => Promise<void>,
): Promise<Page> {
  const page = await context.newPage();
  await prepare(page);
  await page.goto(`${baseURL}/app/game-vanyagotchi`);
  await page.getByRole('button', { name: 'Во двор' }).click();
  await expect(page.locator('[data-test="plane"]')).toBeVisible();
  return page;
}

/**
 * Everything the plane is drawing — which STOPPED being the people in the yard.
 *
 * The roster now also carries the yard's regulars, who have no accounts, and
 * everybody who is asleep in it, who has an account but is not here. So a count
 * of these is a count of things on screen and nothing more; the locators below
 * are how a test says which kind it means, and `here` is how it counts people.
 */
const dots = (page: Page) => page.locator('[data-test="peer"]');

/**
 * The yard's regulars.
 *
 * Identified by the `npc-` id prefix the server mints for them, which is the
 * only handle there is: they have no account, so they have no pseudonym, no row
 * and nothing else to be recognised by.
 */
const npcDots = (page: Page) => page.locator('[data-test="peer"][data-peer^="npc-"]');

/** Everybody lying about the yard: an account whose owner is not here. */
const sleeperDots = (page: Page) =>
  page.locator(
    '[data-test="peer"]:not([data-peer^="npc-"]):has([data-test="peer-face"][data-condition="asleep"])',
  );

/**
 * The players who are actually here — the thing `dots` used to mean.
 *
 * A dead sleeper is drawn `dead` rather than `asleep` (death outranks sleep, so
 * that his owner is not shown a peaceful nap where the one thing he needs to
 * come back for is), which means this locator would count one. Nothing in this
 * suite leaves a dead pet lying about, and where the distinction could matter a
 * test reads `here` — the server's own count — instead of counting these.
 */
const playerDots = (page: Page) =>
  page.locator(
    '[data-test="peer"]:not([data-peer^="npc-"]):not(:has([data-test="peer-face"][data-condition="asleep"]))',
  );

/** The catalogue the server actually served, so nothing here invents content. */
const CONFIG_URL = '/api/game-vanyagotchi/config';

/**
 * How many PEOPLE the server says are in the yard.
 *
 * Read off the status row rather than counted from the plane, because counting
 * the plane is precisely the thing that stopped working: the server counts the
 * humans and publishes the number so that no client has to hold an opinion about
 * which entity is a real person.
 */
async function here(page: Page): Promise<number> {
  const text = (await page.locator('.hud-count').textContent()) ?? '';
  const found = text.match(/(\d+)/);
  return found ? Number(found[1]) : Number.NaN;
}

/** Every PLAYER's normalised x, read from the custom property the stylesheet uses. */
async function xs(page: Page): Promise<number[]> {
  return page.evaluate(() =>
    [
      ...document.querySelectorAll<HTMLElement>(
        '[data-test="peer"]:not([data-peer^="npc-"]):not(:has([data-test="peer-face"][data-condition="asleep"]))',
      ),
    ].map((el) => Number.parseFloat(getComputedStyle(el).getPropertyValue('--x'))),
  );
}

/** One named entity's normalised position, or NaN when it is not on the plane. */
async function peerAt(page: Page, id: string): Promise<{ x: number; y: number }> {
  return page.evaluate((peerId) => {
    const el = document.querySelector<HTMLElement>(`[data-peer="${peerId}"]`);
    if (!el) return { x: Number.NaN, y: Number.NaN };
    const style = getComputedStyle(el);
    return {
      x: Number.parseFloat(style.getPropertyValue('--x')),
      y: Number.parseFloat(style.getPropertyValue('--y')),
    };
  }, id);
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

// ---------------------------------------------------------------------------
// Walking.
//
// A tap used to BE the position: the server clamped it and broadcast it, and the
// dot slid over 220 ms whatever the distance. He now WALKS, at about a fifth of
// the yard a second — so the far corner is some seven seconds away, and distance
// finally means something.
//
// Two consequences for every test in this file that moves anybody. Arrival is no
// longer immediate, so nothing may assume the position one tick after a tap. And
// a LONG walk may not finish at all: the server rolls once, at accept time, on
// whether he gives up part way and sits down saying «устал». That roll is a hash
// of the account, the destination and the instant, so it is unpredictable from
// here by design — which makes it something to be tolerated rather than
// arranged. `walkTo` is what tolerates it.
// ---------------------------------------------------------------------------

/**
 * The distance below which a walk is always completed. Mirrored from `tiredFrom`
 * in internal/gamevanyagotchi/content.go, and only used to REASON about a
 * fixture, never to make one pass.
 */
const NEVER_TIRES_WITHIN = 0.45;

/**
 * Everything he might say when he gives up part way. Mirrored from `tiredSays`
 * in internal/gamevanyagotchi/content.go.
 *
 * Mirrored rather than read back, which is the exception here and is worth
 * justifying. The pool is not served anywhere: `/api/game-vanyagotchi/config`
 * publishes stats, actions, skins, the cast and the locations, and no lines at
 * all — a phrase reaches a client only inside a roster frame, one at a time, as
 * a thing somebody has just said. So there is nothing to read the pool back
 * from, and the alternatives are both worse. Asserting one fixed word stopped
 * being true when the pool replaced the single constant; asserting merely that
 * he said SOMETHING is vacuous, and now actively wrong — see below.
 *
 * The cost of the mirror is bounded and visible: add a line on the server
 * without adding it here and the give-up test fails the first time the server
 * picks it, which is a loud, correct failure rather than a silent one.
 */
const TIRED_SAYS = [
  'устал',
  'нога отваливается',
  'спина болит',
  'перекур',
  'чёт я сдох',
  'дыхалки нет',
  'ноги не идут',
  'колено щёлкает',
  'надо было такси',
  'я не спортсмен',
];

/**
 * Everything a Ваня STANDING STILL might mutter to himself. Mirrored from
 * `idleSays` in the same file, for the same reason and at the same cost.
 *
 * This pool is why "he said something" is no longer evidence of anything. A
 * balloon over a Ваня's head used to mean exactly one thing — he had given up on
 * a walk — and it now also means he has been standing outside for a while and
 * has an opinion about it. The two are told apart by WHICH POOL the line came
 * from and by nothing else, so both pools have to be written down here for
 * either assertion to mean what it says.
 */
const IDLE_SAYS = [
  'где ключи',
  'я вообще норм',
  'кто взял мою зажигалку',
  'так, где я',
  'пивка бы',
  'мамка звонила',
  'курить хочется',
  'да я в порядке',
  'чё стоим',
  'вроде дождь собирается',
  'я на пять минут вышел',
  'кладмен мудак',
  'зачем я вышел',
  'телефон сел',
  'холодно чёт',
];

/**
 * Lines that are in BOTH pools, which must be none.
 *
 * The load-bearing guard under every "which pool was that line from" assertion
 * in this file. If a line ever appears in both, membership stops distinguishing
 * a Ваня who gave up from a Ваня who is bored, and the give-up test below would
 * go on passing while proving nothing. Asserted where the reasoning is used
 * rather than at import time, so the failure names the test it invalidates.
 */
const POOL_OVERLAP = TIRED_SAYS.filter((line) => IDLE_SAYS.includes(line));

/**
 * Waits until your own Ваня has either reached a point or stopped moving.
 *
 * Answers true when he arrived. Polling rather than sleeping on a guess,
 * because the journey's length depends on where he happened to be standing — and
 * "he stopped somewhere else" is a real outcome the server is entitled to
 * produce, not a timeout.
 *
 * Stillness is only believed after a beat: for the first moment after a tap the
 * dot is legitimately still at its old position, and counting that as "stopped"
 * would call every walk a failure before it began.
 */
async function settleNear(
  page: Page,
  x: number,
  y: number,
  tolerance = 0.02,
): Promise<boolean> {
  const startedAt = Date.now();
  const deadline = startedAt + 30_000;
  // Four identical reads 150 ms apart is 600 ms of not moving — three broadcast
  // ticks, so it cannot be an unlucky gap between two frames.
  const STILL_ENOUGH = 4;
  let last = { x: Number.NaN, y: Number.NaN };
  let still = 0;
  while (Date.now() < deadline) {
    const at = await you(page);
    if (Number.isFinite(at.x) && Math.hypot(at.x - x, at.y - y) <= tolerance) return true;
    if (Number.isFinite(at.x) && at.x === last.x && at.y === last.y) {
      still += 1;
      if (still >= STILL_ENOUGH && Date.now() - startedAt > 1_500) return false;
    } else {
      still = 0;
      last = at;
    }
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error(`he neither arrived at ${x},${y} nor stopped walking within 30s`);
}

/**
 * Walks your Ваня to a point and waits until he is standing on it.
 *
 * Asks again when he gives up part way, which is why this is a loop rather than
 * a tap: each attempt starts from where he sat down, so the distance left
 * shrinks every time and is soon under the one below which he never gives up at
 * all. That makes "put him here" deterministic without anybody having to predict
 * the server's roll — and without a test ever wanting the roll to be turned off,
 * which would be the same as not testing it.
 */
async function walkTo(page: Page, x: number, y: number): Promise<void> {
  for (let attempt = 0; attempt < 5; attempt += 1) {
    await tapAt(page, x, y);
    if (await settleNear(page, x, y)) return;
  }
  const at = await you(page);
  throw new Error(`he never made it to ${x},${y}; he gave up at ${at.x},${at.y}`);
}

/** One recorded moment of one entity, as the browser was told to draw it. */
interface Step {
  id: string;
  x: number;
  y: number;
}

/**
 * One line, WHOSE head it appeared over, and WHEN.
 *
 * It used to be the text alone, which was enough while a balloon could only mean
 * one thing. Two things changed it. A line is now either a give-up or idle
 * muttering, so an assertion has to be able to say whose it was — a Ваня
 * standing about saying he fancies a beer is not evidence that anybody got
 * tired. And idle muttering is SHARED, so proving that needs two recordings of
 * the same balloon to be recognisable as the same balloon, which takes an id and
 * an instant.
 *
 * `at` is the page's own `Date.now()` at the moment the DOM grew the balloon.
 * Both browsers in a two-player test run on this machine off this clock, so two
 * timestamps are directly comparable — and recording it in the page removes the
 * race entirely: the test never has to be quick enough to catch a balloon that
 * is only up for four seconds.
 */
interface Said {
  id: string;
  text: string;
  at: number;
}

/**
 * Records every position the plane is ever told to draw, from before the app
 * boots.
 *
 * The plane redraws five times a second and a walk is over in a few, so polling
 * from the test can only ever sample it — and "was he ever between here and
 * there" is a question about the whole journey rather than about any instant. A
 * MutationObserver installed before boot sees every write, so the assertion can
 * be about the recorded TRAJECTORY instead of about being quick enough.
 *
 * It watches the `style` attribute because that is where a position lives: the
 * frames are applied by writing custom properties onto the element and nothing
 * else, deliberately, so that a frame costs no Vue render.
 */
async function recordTrail(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const steps: Step[] = [];
    const said: Said[] = [];
    (window as unknown as { __trail: Step[] }).__trail = steps;
    (window as unknown as { __said: Said[] }).__said = said;
    new MutationObserver((records) => {
      for (const record of records) {
        // A line over somebody's head is on the wire for a few seconds and then
        // gone, so catching it by polling is a coin toss. Recorded on arrival,
        // with whose head it is over: the balloon is a child of its entity's
        // dot, so the id is the nearest `data-peer` above it.
        for (const node of record.addedNodes) {
          if (!(node instanceof HTMLElement)) continue;
          const bubble = node.matches?.('[data-test="peer-say"]')
            ? node
            : node.querySelector?.('[data-test="peer-say"]');
          if (bubble) {
            said.push({
              id: bubble.closest('[data-peer]')?.getAttribute('data-peer') ?? '',
              text: bubble.textContent ?? '',
              at: Date.now(),
            });
          }
        }
        const el = record.target;
        if (!(el instanceof HTMLElement)) continue;
        const id = el.getAttribute('data-peer');
        if (!id) continue;
        const x = Number.parseFloat(el.style.getPropertyValue('--x'));
        const y = Number.parseFloat(el.style.getPropertyValue('--y'));
        if (!Number.isFinite(x) || !Number.isFinite(y)) continue;
        const previous = steps[steps.length - 1];
        // Consecutive identical writes are the standing case — five a second
        // saying the same thing — and would drown the journey in noise.
        if (previous && previous.id === id && previous.x === x && previous.y === y) continue;
        steps.push({ id, x, y });
      }
    }).observe(document, {
      // THE DOCUMENT, not `document.documentElement`. An init script runs before
      // the page's own scripts and, on this stack, before the `<html>` element
      // exists at all — so `documentElement` is null here and `observe` throws
      // on it, leaving a recorder that silently records nothing and a test that
      // passes for want of any evidence. A Document is a Node and covers every
      // descendant, so this attaches whatever stage the parse has reached.
      subtree: true,
      childList: true,
      attributes: true,
      attributeFilter: ['style'],
    });
  });
}

/** Everything recorded so far, oldest first. */
function trail(page: Page): Promise<Step[]> {
  return page.evaluate(() => (window as unknown as { __trail: Step[] }).__trail ?? []);
}

/** Every line anybody has been seen to say, with whose head it was over. */
function saidSoFar(page: Page): Promise<Said[]> {
  return page.evaluate(() => (window as unknown as { __said: Said[] }).__said ?? []);
}

/** Forgets the recording, so the next assertion is about one journey. */
async function forgetTrail(page: Page): Promise<void> {
  await page.evaluate(() => {
    (window as unknown as { __trail: Step[] }).__trail.length = 0;
    (window as unknown as { __said: Said[] }).__said.length = 0;
  });
}

/** The id the server told this page is its own. */
async function youId(page: Page): Promise<string> {
  const id = await page
    .locator('[data-test="peer"][data-you="1"]')
    .getAttribute('data-peer');
  expect(id, 'the page never learned which Ваня is its own').toBeTruthy();
  return id as string;
}

/** How far a point lies off the straight line from `from` to `to`. */
function offLine(
  from: { x: number; y: number },
  to: { x: number; y: number },
  at: { x: number; y: number },
): number {
  const dx = to.x - from.x;
  const dy = to.y - from.y;
  const length = Math.hypot(dx, dy);
  if (length === 0) return Math.hypot(at.x - from.x, at.y - from.y);
  return Math.abs(dx * (at.y - from.y) - dy * (at.x - from.x)) / length;
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
    await expect(playerDots(pageA)).toHaveCount(2);
    await expect(playerDots(pageB)).toHaveCount(2);
    await expect(pageA.getByText('во дворе: 2')).toBeVisible();
    // And the yard's regulars are there alongside them, on both screens — which
    // is also what says the count above is the server's own rather than a tally
    // of everything drawn. How many there are comes from the server; casting a
    // new one must not be an edit to this file.
    const cast = await npcCount(pageA);
    expect(cast, 'the server serves no cast at all').toBeGreaterThan(0);
    await expect(npcDots(pageA)).toHaveCount(cast);
    await expect(npcDots(pageB)).toHaveCount(cast);

    // A walks a quarter across. Nothing local happens; the position is only real
    // once the server has clamped it and sent it back to everybody.
    //
    // Through `walkTo` rather than a bare click, because a tap stopped being a
    // teleport: he takes seconds to cross the yard, and a long walk may end with
    // the server sitting him down part way. Asking again from where he sat down
    // is how a test gets him somewhere definite without wanting that roll turned
    // off — which would be the same as not testing it.
    await walkTo(pageA, 0.25, 0.5);

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
    await expect(playerDots(pageA)).toHaveCount(1, { timeout: 10_000 });
    await expect(pageA.getByText('во дворе: 1')).toBeVisible();
    // B is inside the reconnect grace, so he is not drawn at all — a grace is
    // about REMEMBERING where somebody was, not about showing him. He becomes a
    // sleeper only once it runs out.
    await expect(sleeperDots(pageA)).toHaveCount(0);
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
    await expect(playerDots(page)).toHaveCount(1);

    const nav = page.locator('.v-navigation-drawer');
    await nav.getByRole('link', { name: 'Вишлист' }).click();
    await expect(page).toHaveURL(/\/app\/wishlist/);
    await expect(page.locator('[data-test="plane"]')).toHaveCount(0);

    await nav.getByRole('link', { name: 'Ванягоччи' }).click();
    await page.getByRole('button', { name: 'Во двор' }).click();
    await expect(playerDots(page)).toHaveCount(1, { timeout: 10_000 });
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
    await expect(playerDots(pageA)).toHaveCount(2);
    await expect(playerDots(pageB)).toHaveCount(2);

    const before = await playerDots(pageA).count();
    expect(before).toBe(2);

    restartServer();

    // Neither page was touched, and neither was reloaded. They have to notice
    // and recover on their own — including asking who they are again, since the
    // pseudonym is derived from a key that died with the old process.
    await expect(pageA.getByText('на связи')).toBeVisible({ timeout: 60_000 });
    await expect(pageB.getByText('на связи')).toBeVisible({ timeout: 60_000 });
    await expect(playerDots(pageA)).toHaveCount(2, { timeout: 30_000 });
    await expect(playerDots(pageB)).toHaveCount(2, { timeout: 30_000 });
    // The regulars came back with the world — all of them, however many the
    // catalogue holds today. They are evaluated closed-form from a FIXED world
    // epoch rather than from process start, so a deploy does not teleport the
    // cast — which is only observable across a restart.
    await expect(npcDots(pageA)).toHaveCount(await npcCount(pageA), { timeout: 30_000 });

    // Still in the yard, never bounced back to the intro.
    await expect(pageA.getByRole('button', { name: 'Во двор' })).toHaveCount(0);

    // And the socket is genuinely live again rather than merely labelled so: a
    // move made after the restart has to cross to the other player.
    await walkTo(pageA, 0.8, 0.5);
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

    await expect(playerDots(pagePhone)).toHaveCount(1);
    await expect(playerDots(pageLaptop)).toHaveCount(1);
    await expect(pagePhone.getByText('во дворе: 1')).toBeVisible();

    // A move from the laptop moves the Ваня the phone is watching.
    await walkTo(pageLaptop, 0.2, 0.5);
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

    await expect(playerDots(pageA)).toHaveCount(2);
    await expect(playerDots(pageB)).toHaveCount(2);
    // The hello has been answered, so "not me" identifies the other player
    // without either page having to know the other's pseudonym.
    await expect(pageA.locator('[data-test="peer"][data-you="1"]')).toHaveCount(1);
    await expect(pageB.locator('[data-test="peer"][data-you="1"]')).toHaveCount(1);

    const skin = await defaultSkin(pageA);

    for (const [page, who] of [
      [pageA, 'A'],
      [pageB, 'B'],
    ] as const) {
      // "Not me" stopped identifying the other player: it now also matches the
      // two regulars and anybody asleep in the yard. What is wanted here is the
      // other PERSON, so the regulars and the sleepers are excluded by name.
      const theirs = playerDots(page).and(page.locator(':not([data-you="1"])'));
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
      // FOUR poses now, not three. `asleep` joined them when the yard started
      // keeping absent Ваняs lying about in it — and while the neighbour asserted
      // here is connected and so cannot be one, pinning the set to the three that
      // existed before would make this the test that fails when somebody's
      // socket blips rather than when anything is wrong.
      await expect(theirFace).toHaveAttribute('data-condition', /^(fine|poorly|dead|asleep)$/);

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
    await expect(playerDots(page)).toHaveCount(1);
    await expect(page.locator('[data-test="peer"][data-you="1"]')).toHaveCount(1);

    // A corner no other test in this suite walks to, so a pass cannot be
    // inherited from a position an earlier test happened to leave behind.
    //
    // `walkTo` rather than a bare tap: the journey from wherever he is standing
    // is long enough that the server may decide he has had enough and sit him
    // down part way, and the point of this test is the restart rather than the
    // roll. Each retry starts from where he sat down, so he gets there.
    await walkTo(page, 0.15, 0.85);
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
    await expect(playerDots(page)).toHaveCount(1, { timeout: 30_000 });
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
    await walkTo(page, 0.6, 0.4);
    expect((await you(page)).x).toBeCloseTo(0.6, 1);
  } finally {
    await context.close();
  }
});

/** One regular as the catalogue describes it. Mirrored from the config response. */
interface WireNPC {
  key: string;
  label: string;
  art: string;
}

/** The whole catalogue, as the server served it to this page. */
async function catalogue(page: Page): Promise<{ skins: WireSkin[]; npcs?: WireNPC[] }> {
  const res = await page.request.get(CONFIG_URL);
  expect(res.status(), `GET ${CONFIG_URL}`).toBe(200);
  return (await res.json()) as { skins: WireSkin[]; npcs?: WireNPC[] };
}

/**
 * How many regulars the server actually has, asked of the server.
 *
 * No test writes the number down. Casting a new character is a content change —
 * a catalogue entry and an art key, no client, no migration, no route — and the
 * whole point of that is that it costs nothing to do. A suite that hardcodes the
 * size of the cast turns it into a suite-wide edit, and the edit is invisible
 * until CI goes red for a reason that has nothing to do with the change.
 */
async function npcCount(page: Page): Promise<number> {
  return ((await catalogue(page)).npcs ?? []).length;
}

/**
 * The cap a name is cut to before it is drawn, in code points. Mirrored from
 * `LABEL_MAX` in src/lib/vanyagotchiPlane.ts.
 *
 * Needed here because a regular's name comes off the SERVER's catalogue and one
 * of them is longer than the cap, so a test comparing the drawn label with the
 * served one has to apply the same rule the client does. Cut by code point, not
 * by `slice`, because halving a surrogate pair is how this field grows a
 * replacement character.
 */
const LABEL_MAX = 16;

function capLabel(raw: string): string {
  const chars = [...raw.trim()];
  if (chars.length <= LABEL_MAX) return raw.trim();
  return `${chars.slice(0, LABEL_MAX - 1).join('')}…`;
}

test('the yard has regulars, and both players see the same ones', async ({ browser, baseURL }) => {
  // The yard used to be as empty as the number of friends who happened to be
  // online, which for five to thirty people is almost always nobody. It now has
  // characters in it who are not players at all.
  //
  // Everything asserted is read back from what the SERVER served — the cast, its
  // names and its art all come from `/api/game-vanyagotchi/config` in this test
  // rather than being written down in it. A hardcoded «Тунг Тунг Сахур» would
  // only prove that the client can draw a string this file already knew.
  //
  // The claim that matters is that they are SHARED: two independent browsers,
  // two accounts, two sockets, and the same characters on both. An NPC faked
  // client-side would satisfy every single-page assertion here.
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const ctxA = await browser.newContext(PHONE);
  const ctxB = await browser.newContext(PHONE);
  try {
    await loginAs(ctxA, PLAYER_A);
    await loginAs(ctxB, PLAYER_B);
    const pageA = await enterYard(ctxA, base);
    const pageB = await enterYard(ctxB, base);

    const cfg = await catalogue(pageA);
    const npcs = cfg.npcs ?? [];
    expect(npcs.length, 'the server serves no cast at all').toBeGreaterThanOrEqual(2);

    for (const [page, who] of [
      [pageA, 'A'],
      [pageB, 'B'],
    ] as const) {
      await expect(npcDots(page), `${who} sees the wrong number of regulars`).toHaveCount(
        npcs.length,
        { timeout: 15_000 },
      );

      for (const npc of npcs) {
        // The id the server mints is `npc-` plus the catalogue key — the only
        // handle a character without an account can have.
        const dot = page.locator(`[data-peer="npc-${npc.key}"]`);
        await expect(dot, `${who} cannot see ${npc.key}`).toHaveCount(1);

        // Its art resolved against the same catalogue, exactly as a pet's skin
        // does. That is the whole reason a character costs the browser nothing:
        // to the client it is one more entity in the roster.
        const skin = cfg.skins.find((s) => s.key === npc.art);
        expect(skin, `the catalogue has no skin for ${npc.key}'s art key ${npc.art}`).toBeDefined();
        const npcFace = dot.locator('[data-test="peer-face"]');
        await expect(npcFace, `${who} sees a faceless ${npc.key}`).toHaveCount(1);
        if (skin?.image) {
          await expect(npcFace.locator('img.peer-sprite')).toHaveCount(1);
        } else {
          await expect(npcFace, `${who} drew the placeholder for ${npc.key}`).toHaveText(
            skin?.emoji ?? '',
          );
        }

        // And its name, cut the way every name on the plane is cut.
        await expect(dot.locator('[data-test="peer-label"]')).toHaveText(capLabel(npc.label));
      }
    }

    // THE point of the head count. Two of everything drawn are people, and the
    // number on screen is two — on both screens. Counting the plane would say
    // two plus the whole cast, which is the bug the field exists to foreclose.
    await expect(playerDots(pageA)).toHaveCount(2);
    await expect(pageA.getByText('во дворе: 2')).toBeVisible();
    await expect(pageB.getByText('во дворе: 2')).toBeVisible();
    expect(await here(pageA), 'the regulars were counted as people').toBe(2);
    expect(await dots(pageA).count()).toBe(2 + npcs.length);
  } finally {
    await ctxA.close();
    await ctxB.close();
  }
});

test('a regular is in the same place on both screens at the same moment', async ({
  browser,
  baseURL,
}) => {
  // THE closed-form claim, and the one no stub can make.
  //
  // An NPC's position is `pattern(params, now − epoch)` — a pure function of the
  // clock, evaluated fresh on every broadcast, stored nowhere. The whole payoff
  // is that it cannot drift: a tick that is late, early, skipped or duplicated
  // still produces the same world, so two players watching from two browsers see
  // one character rather than two copies slowly disagreeing. Nothing about that
  // is observable from a single page, and a velocity that were integrated per
  // tick instead would pass every other test in this file.
  //
  // The sampling is PAIRED — both pages read in the same `Promise.all` — because
  // the claim is about a moment rather than about an average. And it is followed
  // by a RESTART, because agreement between two pages watching one broadcast is
  // the easy half: see the comment above that leg for what it adds.
  test.setTimeout(150_000);
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const ctxA = await browser.newContext(PHONE);
  const ctxB = await browser.newContext(PHONE);
  try {
    await loginAs(ctxA, PLAYER_A);
    await loginAs(ctxB, PLAYER_B);
    const pageA = await enterYard(ctxA, base);
    const pageB = await enterYard(ctxB, base);

    const npcs = (await catalogue(pageA)).npcs ?? [];
    expect(npcs.length, 'the server serves no cast at all').toBeGreaterThanOrEqual(1);
    await expect(npcDots(pageA)).toHaveCount(npcs.length, { timeout: 15_000 });
    await expect(npcDots(pageB)).toHaveCount(npcs.length, { timeout: 15_000 });

    // One that MOVES. A character standing still would agree with itself on two
    // screens for entirely uninteresting reasons, so the assertion that the
    // agreement is non-trivial is made against the same samples below.
    const wanderer = `npc-${npcs[0].key}`;

    // A frame is published to everybody at once, so at worst the two pages are
    // one tick apart. The wanderer covers well under 0.01 of the plane in a 200
    // ms tick, so this is a couple of ticks of slack — and still far tighter
    // than the distance he is required to have covered by the time we stop.
    const TICK_SLACK = 0.03;
    // How far he has to have gone before the agreement above counts for
    // anything. A character standing still agrees with itself for free.
    const MIN_TRAVELLED = TICK_SLACK * 2;

    // Sampled until he HAS moved that far, rather than for a fixed few seconds.
    // His path is the sum of two sinusoids whose periods are deliberately
    // incommensurable, so his speed varies from nothing to about a fortieth of
    // the plane a second — a fixed window that happened to land on a turning
    // point would fail for a reason that has nothing to do with the claim.
    const paired: { a: { x: number; y: number }; b: { x: number; y: number } }[] = [];
    let travelled = 0;
    const deadline = Date.now() + 40_000;
    while (Date.now() < deadline) {
      const [a, b] = await Promise.all([peerAt(pageA, wanderer), peerAt(pageB, wanderer)]);
      if (Number.isFinite(a.x) && Number.isFinite(b.x)) {
        const previous = paired[paired.length - 1];
        // Total path length rather than net displacement: he is going nowhere in
        // particular, so he can end up where he started having covered ground.
        if (previous) travelled += Math.hypot(a.x - previous.a.x, a.y - previous.a.y);
        paired.push({ a, b });
        // The agreement is asserted as we go, so a disagreement fails on the
        // sample that had it rather than being averaged away.
        const apart = Math.hypot(a.x - b.x, a.y - b.y);
        expect(
          apart,
          `sample ${paired.length}: the two players are watching ${wanderer} in different ` +
            `places — (${a.x.toFixed(3)},${a.y.toFixed(3)}) vs (${b.x.toFixed(3)},${b.y.toFixed(3)})`,
        ).toBeLessThan(TICK_SLACK);
      }
      if (paired.length >= 12 && travelled > MIN_TRAVELLED) break;
      await new Promise((resolve) => setTimeout(resolve, 250));
    }

    expect(paired.length, 'the wanderer was never on both planes at once').toBeGreaterThanOrEqual(12);
    expect(
      travelled,
      `${wanderer} moved only ${travelled.toFixed(3)} of a plane in 40 seconds — ` +
        'a stationary character agrees with itself for free',
    ).toBeGreaterThan(MIN_TRAVELLED);

    // WHAT THIS TEST DOES NOT REACH, stated so nobody reads more into it.
    //
    // Both pages agree largely because they are shown ONE broadcast frame, so
    // the agreement above proves the regulars are SERVER-AUTHORITATIVE and
    // shared — a cast generated in the browser would drift between two tabs
    // within seconds — but it does not prove they are evaluated closed-form.
    //
    // The property it cannot reach is that the world's clock is a FIXED epoch
    // rather than process start: get that wrong and the whole cast snaps back to
    // its t = 0 pose on every deploy. Deciding it from here needs two restarts
    // and a sample taken at a known moment after each, and the moment is not
    // knowable — the page reconnects on its own schedule, and the few seconds of
    // slack in that is more movement than the difference being looked for. An
    // attempt at it passed against a server deliberately broken to use process
    // start, which is worse than no test. It belongs in a Go unit test asserting
    // `worldEpoch` is a literal date; the existing one compares each frame
    // against `worldEpoch` itself and so would follow the same change.
  } finally {
    await ctxA.close();
    await ctxB.close();
  }
});

test('a tap is a walk across the yard, not a teleport', async ({ browser, baseURL }) => {
  // Before this the position WAS the tap: the server clamped it and broadcast
  // it, and the dot slid over 220 ms whatever the distance — so the far side of
  // the plane was 220 ms away and distance meant nothing. It has to mean
  // something, because the errands this game is heading for are races to ARRIVE.
  //
  // "Not a teleport" is a claim about a JOURNEY, not about any instant, so it is
  // asserted against the recorded trajectory: a teleport writes one position and
  // is done, and a walk writes a stream of them along the straight line between
  // where he was and where he is going.
  test.setTimeout(120_000);
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const context = await browser.newContext(PHONE);
  try {
    await loginAs(context, PLAYER_A);
    const page = await enterYardWith(context, base, recordTrail);
    await expect(playerDots(page)).toHaveCount(1);
    await expect(page.locator('[data-test="peer"][data-you="1"]')).toHaveCount(1);
    const me = await youId(page);

    // Put him against one wall first, so the journey below is the width of the
    // yard rather than however far he happens to be from the far side.
    await walkTo(page, 0.08, 0.5);
    const from = await you(page);

    const to = { x: 0.92, y: 0.5 };
    await forgetTrail(page);
    await tapAt(page, to.x, to.y);
    const arrived = await settleNear(page, to.x, to.y);

    const journey = (await trail(page)).filter((step) => step.id === me);
    expect(journey.length, 'nothing about his position was ever written').toBeGreaterThan(0);

    // He was NOT already there when the first frame after the tap arrived. This
    // is the assertion a teleport fails outright.
    const first = journey[0];
    expect(
      Math.hypot(first.x - to.x, first.y - to.y),
      `the very first frame after the tap already had him at the destination ` +
        `(${first.x.toFixed(3)},${first.y.toFixed(3)}) — that is a teleport`,
    ).toBeGreaterThan(0.1);

    // And he was seen in a stream of places strictly between the two ends,
    // rather than at one end and then the other.
    const between = journey.filter(
      (step) =>
        Math.hypot(step.x - from.x, step.y - from.y) > 0.05 &&
        Math.hypot(step.x - to.x, step.y - to.y) > 0.05,
    );
    expect(
      between.length,
      `he was drawn at ${journey.length} places and only ${between.length} of them were ` +
        'part-way across; a walk at a fifth of a plane a second should be a couple of dozen',
    ).toBeGreaterThanOrEqual(5);

    // Every one of them on the straight line between the two ends — so this is a
    // walk rather than a dot wandering about on its way.
    for (const step of between) {
      expect(
        offLine(from, to, step),
        `he strayed ${offLine(from, to, step).toFixed(3)} off the line to where he was asked to go`,
      ).toBeLessThan(0.03);
    }

    // The walk had to be long enough that giving up was on the table, or the
    // branch below is never taken and this test quietly stops covering it.
    expect(
      Math.hypot(to.x - from.x, to.y - from.y),
      'the fixture is now a short hop, which can never be given up on',
    ).toBeGreaterThan(NEVER_TIRES_WITHIN);

    if (arrived) {
      const at = await you(page);
      expect(at.x).toBeCloseTo(to.x, 1);
      expect(at.y).toBeCloseTo(to.y, 1);
    } else {
      // The other honest outcome: the server decided he had had enough and sat
      // him down part way, saying so. The roll is a hash of the account, the
      // destination and the instant — unpredictable from here on purpose — so
      // this branch is tolerated rather than arranged, and asserting where he
      // stopped is what stops "gave up" being an excuse for "went nowhere".
      const at = await you(page);
      expect(
        offLine(from, to, at),
        'he gave up somewhere that is not on the way to where he was going',
      ).toBeLessThan(0.03);
      const progress =
        Math.hypot(at.x - from.x, at.y - from.y) / Math.hypot(to.x - from.x, to.y - from.y);
      expect(progress, 'he gave up before setting off, which reads as an ignored tap').toBeGreaterThan(
        0.2,
      );
      expect(progress, 'he "gave up" within a whisker of arriving').toBeLessThan(0.95);
      // And he said so. Recorded on arrival in the DOM rather than polled for:
      // the line is on the wire for a few seconds only, so catching it by
      // sampling would be a coin toss.
      //
      // What is asserted is that ONE OF THE GIVING-UP LINES appeared over HIS
      // OWN head. Both halves are load-bearing, and the reason is the balloon
      // stopped meaning one thing.
      //
      // A Ваня who is standing still now mutters to himself out of a second,
      // separate pool. So "he said something" would be satisfied by a Ваня
      // remarking that he fancies a beer, which is not evidence that anybody got
      // tired — and the window for exactly that is real rather than theoretical:
      // giving up stops counting as giving up after `tiredFor`, at which point
      // he is standing still and entitled to mutter, while `settleNear` may
      // still be confirming that he has stopped. Membership of `tiredSays` is
      // what tells the two apart, and it only tells them apart while the pools
      // are disjoint — which is what the guard immediately below is for.
      //
      // A single fixed word is what this used to assert, and it is now wrong:
      // which line he uses is decided by the same hash that decided he would sit
      // down, so it is unpredictable from here by design.
      expect(
        POOL_OVERLAP,
        'a line is in both phrase pools, so "he said one of the giving-up lines" no longer ' +
          'distinguishes a Ваня who gave up from one who is merely bored',
      ).toHaveLength(0);
      const heard = (await saidSoFar(page)).filter((line) => line.id === me);
      const complaints = heard.filter((line) => TIRED_SAYS.includes(line.text));
      expect(
        complaints.length,
        'he stopped short of where he was sent without a word about it — over his own head ' +
          `this journey we saw ${JSON.stringify(heard.map((line) => line.text))}, and none of ` +
          'it is one of the giving-up lines',
      ).toBeGreaterThan(0);
    }
  } finally {
    await context.close();
  }
});

// ---------------------------------------------------------------------------
// Putting an inherited Ваня back on his feet — a DIRECT DATABASE WRITE, and
// deliberately not a request and not a button press.
//
// Two tests below cannot say anything about a POSE until they know the pet is
// not dead: death outranks sleep — a dead Ваня is drawn dead however long his
// owner has been away — and a dead Ваня never mutters. This suite shares one
// database and a pet is permanent, so an earlier file may well have left this
// account's Ваня on the floor, and neither test would then be failing about
// anything it is meant to be about.
//
// This used to be `POST /api/game-vanyagotchi/actions/drink`. THAT ROUTE NO
// LONGER EXISTS — a verb travels over the socket as a `vanyagotchi_do` frame and
// is answered with state — so do not "restore" the request. It is a write rather
// than a press of the drink button for three reasons:
//
//   * A press TALKS. It puts the action's own line over his head for four
//     seconds, on every screen in the yard — and the chatter test's entire claim
//     is about WHICH balloon appeared and which pool it came from, so a fixture
//     that talks is a fixture that lies to the test. A write leaves the yard
//     silent.
//   * A press can be dropped in silence. A verb is rate-limited to one a second
//     per account and owes no reply at all, so a fixture that spends one has no
//     way of knowing whether it landed; a write either happens or throws.
//
// It writes the SNAPSHOT — game_vanyagotchi_pet_stats, which migration 009 keeps
// authoritative for a read — so this is the same state a verb would have left,
// arrived at without pressing anything.
//
// Whitebox, and the project's rule is real flows or direct DB setup — the same
// reasoning the sibling pet spec gives for its own direct setup. The container
// name is fixed in docker-compose.yml and this suite already requires Docker.
// ---------------------------------------------------------------------------

const STATE_URL = '/api/game-vanyagotchi/state';

/**
 * Where a revived Ваня is put down, mirrored from
 * internal/gamevanyagotchi/content.go.
 *
 * Every shipped stat, comfortably clear of its own warning mark, so the pose is
 * `fine` and nothing drifts over a threshold while a test runs. All of them
 * rather than only the fatal one, and stamped with ONE instant, because the
 * coupled decay assumes every stat's `as_of` moved together — a fixture that
 * lifted hp and left the drivers behind would set up a window the server's
 * arithmetic does not expect. The count assertion below is what makes a stat
 * added to the catalogue fail loudly here instead.
 */
const ALIVE_STATS = { hp: 60, beer: 60, bladder: 10 };

/**
 * Makes sure this account's Ваня is alive and well, in the database AND in the
 * running process's idea of him.
 *
 * Read, write, read. The reads are the app's own creation path — a plain
 * `GET /state`, which creates the pet lazily on first sight and re-caches it on
 * every sight — and neither is a stand-in for the verb that used to be posted
 * here. Both are load-bearing:
 *
 *   * The FIRST one makes the row exist. There is nothing to revive until it
 *     does, and the sleeper test needs it so that his position has somewhere to
 *     be written down when he leaves.
 *   * The SECOND one is why this fixture works wherever it is called from. The
 *     pose the plane draws comes from an in-memory cache that a query never
 *     refreshes — it is filled when a client says hello on a fresh socket and
 *     whenever that account's pet is read — so a row changed behind the
 *     process's back is a row nothing would ever notice. Reading it back leaves
 *     exactly what the POST left, which is what makes this a replacement for it
 *     rather than an approximation.
 *
 * The write between them is two statements, and both are needed: a pose is drawn
 * `dead` while `died_at` is set OR while the fatal stat sits on its floor, and
 * the next read would re-record the death from the second even with the first
 * cleared.
 */
async function reviveInDb(
  context: BrowserContext,
  base: string,
  account: SeededAccount,
): Promise<void> {
  const read = async () => {
    const res = await context.request.get(`${base}${STATE_URL}`);
    expect(res.status(), `GET ${STATE_URL}`).toBe(200);
  };
  await read();

  const id = uuid(account.account_id);
  const pets = psql(
    `UPDATE game_vanyagotchi_pets SET died_at = NULL, updated_at = now() ` +
      `WHERE account_id = '${id}' AND deleted_at IS NULL`,
  );
  expect(pets, 'no living pet to revive — was the read above answered?').toBe('UPDATE 1');

  const cases = Object.entries(ALIVE_STATS)
    .map(([key, value]) => `WHEN '${key}' THEN ${value}`)
    .join(' ');
  const owned =
    `(SELECT id FROM game_vanyagotchi_pets WHERE account_id = '${id}' AND deleted_at IS NULL)`;
  // EVERY row is re-stamped, but only the three bars are given a value: a
  // `CASE` with no `ELSE` yields NULL, so `COALESCE` leaves everything else
  // holding exactly what it held. That matters in both directions. The shared
  // `as_of` is the invariant the coupled decay rests on, so a fixture that
  // moved three rows and left the others behind would set up a window the
  // server's arithmetic does not assume exists — and the rows it must not
  // touch are the LIFETIME TALLIES, which a fixture zeroing them would quietly
  // rewrite the player's history to make a pose assertion pass.
  const stats = psql(
    `UPDATE game_vanyagotchi_pet_stats ` +
      `SET value = COALESCE(CASE stat_key ${cases} END, value), ` +
      `as_of = now(), updated_at = now() ` +
      `WHERE pet_id IN ${owned} RETURNING stat_key`,
  );
  const touched = stats
    .split('\n')
    .filter((line) => line && !/^[A-Z]+ \d+$/.test(line));
  expect(
    touched.length,
    'the fixture re-stamped fewer rows than the pet has; the shared as_of is the whole invariant',
  ).toBeGreaterThanOrEqual(Object.keys(ALIVE_STATS).length);
  expect(
    touched.sort(),
    'a stat this fixture names is not one the pet has',
  ).toEqual(expect.arrayContaining(Object.keys(ALIVE_STATS).sort()));

  await read();
}

test('a Ваня whose owner has gone is asleep in the yard where he stood', async ({
  browser,
  baseURL,
}) => {
  // What stops a solo visit being an empty field. With five to thirty friends
  // the yard is almost never occupied by two people at once, but it is never
  // empty either, because everybody else is lying about in it.
  //
  // Driven through a RESTART rather than by waiting out the two-minute reconnect
  // grace. That is not a shortcut around the clock — it is the other, and
  // harder, half of the feature: after a deploy the new process has never heard
  // of anybody, so the sleepers have to be read back out of Postgres on the
  // first hello. Without that the first visitor after every deploy finds the
  // bare field the sleepers exist to prevent, and no amount of waiting would
  // exercise it. The grace-expiry path is covered in the Go tests, which own the
  // clock, and by the stubbed suite, which owns the wire.
  test.setTimeout(150_000);
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const ctxA = await browser.newContext(PHONE);
  const ctxB = await browser.newContext(PHONE);
  try {
    await loginAs(ctxA, PLAYER_A);
    const accB = await loginAs(ctxB, PLAYER_B);
    const pageA = await enterYard(ctxA, base);
    const pageB = await enterYard(ctxB, base);
    await expect(playerDots(pageA)).toHaveCount(2, { timeout: 15_000 });

    // B is alive. Death outranks sleep — a dead Ваня is drawn dead however long
    // his owner has been away, because hiding that behind a peaceful nap would
    // hide the one thing his owner needs to come back for — so a pet left dead
    // by an earlier file would make this assert the wrong pose for the right
    // reason. It would also make him miss the sleeper count below entirely:
    // `playerDots` counts a Ваня drawn dead and not one drawn asleep.
    //
    // A database write, not a request and not a button press — see `reviveInDb`.
    await reviveInDb(ctxB, base, accB);

    // Somewhere no other test in this file walks to, so the position asserted
    // after the restart cannot be one left lying around by an earlier one.
    await walkTo(pageB, 0.86, 0.14);
    const stood = await you(pageB);
    expect(Math.abs(stood.x - 0.5), 'he is standing on the spawn point').toBeGreaterThan(0.2);

    // A can see him there while he is still connected.
    await expect
      .poll(async () => (await xs(pageA)).some((x) => Math.abs(x - stood.x) < 0.05), {
        message: "A never saw B walk over",
        timeout: 20_000,
      })
      .toBe(true);

    // B goes. The server writes down where he was standing on the next tick.
    await ctxB.close();
    await expect(playerDots(pageA)).toHaveCount(1, { timeout: 20_000 });

    restartServer();

    // A's page was not touched and not reloaded: it notices, reconnects, and
    // says hello to a process that has never heard of it — and that hello is
    // what makes the new process read the yard's sleepers out of the database.
    await expect(pageA.getByText('на связи')).toBeVisible({ timeout: 60_000 });
    await expect(playerDots(pageA)).toHaveCount(1, { timeout: 30_000 });

    // There he is, lying where he stood.
    await expect(sleeperDots(pageA), 'the yard came back empty of everybody who had left').toHaveCount(
      1,
      { timeout: 30_000 },
    );
    const sleeper = sleeperDots(pageA).first();
    await expect(sleeper.locator('[data-test="peer-face"]')).toHaveAttribute(
      'data-condition',
      'asleep',
    );
    const sleeperId = await sleeper.getAttribute('data-peer');
    expect(sleeperId, 'the sleeper has no id').toBeTruthy();
    const restingAt = await peerAt(pageA, sleeperId as string);
    expect(
      Math.hypot(restingAt.x - stood.x, restingAt.y - stood.y),
      `he went to sleep at ${restingAt.x.toFixed(3)},${restingAt.y.toFixed(3)} rather than ` +
        `where he was standing, ${stood.x.toFixed(3)},${stood.y.toFixed(3)}`,
    ).toBeLessThan(0.05);

    // He is emphatically NOT one of the people in the yard, which is the whole
    // reason the head count stopped being the length of the roster: A is alone
    // in a yard with the sleeper and the whole cast drawn in it.
    expect(await here(pageA), 'a sleeper was counted as somebody who is here').toBe(1);
    await expect(pageA.getByText('во дворе: 1')).toBeVisible();
    // And the regulars are still there alongside him.
    await expect(npcDots(pageA)).toHaveCount(await npcCount(pageA));
  } finally {
    await ctxA.close();
    await ctxB.close().catch(() => undefined);
  }
});

// ---------------------------------------------------------------------------
// Idle chatter.
//
// The yard is mostly people doing nothing, so a Ваня standing still is now
// allowed an opinion about it. Time is cut into twelve-second slots from the
// world's fixed epoch; hashing (account, slot) decides whether that slot
// produces a remark and which one, and it is shown for the first four seconds.
// Nothing is stored, nothing is scheduled, and every process computes the same
// answer — which is the property this suite exists to check and no unit test
// can: two browsers, two accounts, two sockets, one set of words.
// ---------------------------------------------------------------------------

/**
 * How far apart two sightings of a balloon may be and still be the same slot.
 *
 * Reasoned from `idlePeriod` (12 s) and `idleSayFor` (4 s) in content.go: a
 * slot's talking window is four seconds long and the next one opens eight
 * seconds after this one shuts, so two sightings less than eight seconds apart
 * cannot straddle a slot boundary. Five is that with room to spare.
 *
 * Used to decide which two recordings are of the SAME balloon, never to make
 * anything pass. Both pages are told by the same broadcast and in practice
 * record a few hundred milliseconds apart; the slack is there so the pairing
 * cannot be defeated by a slow tick, not because simultaneity is being claimed.
 */
const SAME_SLOT_MS = 5_000;

/**
 * How long to wait for somebody to say something. Twelve and a half slots.
 *
 * A given Ваня speaks in a quarter of his slots, so with two of them standing
 * about the chance that a slot produces nothing at all is about 0.56 — and the
 * chance of a whole run of them producing nothing is that to the twelfth, which
 * is a thousand to one. Generous on purpose: the alternative to waiting is a
 * flaky test, and this one is only reached once per suite.
 */
const CHATTER_TIMEOUT_MS = 150_000;

test('a Ваня standing still mutters, and both screens hear the same words', async ({
  browser,
  baseURL,
}) => {
  // THE claim, and the only one worth two browsers: the words are the SERVER's.
  //
  // Nothing here proves it by being clever — it proves it by the words being
  // unguessable. The phrase pool is not served anywhere: the config publishes
  // stats, actions, skins, the cast and the locations, and no lines at all. So a
  // client cannot know a single one of the fifteen except by being handed it in
  // a roster frame, and two independently-launched browsers cannot agree on a
  // line neither of them could have invented. Together with the balloon being
  // over the SAME entity on both screens, that is idle chatter being computed
  // once, on the server, for everybody.
  //
  // Standing still is the precondition rather than an accident: a walking Ваня
  // says nothing at all, and a Ваня who has given up draws from a different pool
  // — which is why the line is asserted to be one of the IDLE ones rather than
  // merely to be a line.
  test.setTimeout(240_000);
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const ctxA = await browser.newContext(PHONE);
  const ctxB = await browser.newContext(PHONE);
  try {
    // The guard the pool-membership assertion at the end rests on. Stated here
    // as well as in the give-up test because both tests are only meaningful
    // while a line belongs to exactly one pool.
    expect(
      POOL_OVERLAP,
      'a line is in both phrase pools, so "he said one of the idle lines" no longer ' +
        'distinguishes a Ваня who is bored from one who has just given up on a walk',
    ).toHaveLength(0);

    const accA = await loginAs(ctxA, PLAYER_A);
    const accB = await loginAs(ctxB, PLAYER_B);
    // Recording from before boot, on BOTH pages. A remark is up for four seconds
    // in twelve, so polling the DOM for one is a coin toss that has to be won
    // twice at once; a recorder installed ahead of the app catches every balloon
    // with an id and an instant, and the pairing below is then arithmetic rather
    // than a race.
    const pageA = await enterYardWith(ctxA, base, recordTrail);
    const pageB = await enterYardWith(ctxB, base, recordTrail);
    await expect(playerDots(pageA)).toHaveCount(2, { timeout: 15_000 });
    await expect(playerDots(pageB)).toHaveCount(2, { timeout: 15_000 });

    // Both are alive, because a DEAD Ваня never says anything and an earlier
    // file may well have left one that way.
    //
    // A database write, not a request and not a button press — see `reviveInDb`.
    // The distinction matters more here than anywhere else in the file: a press
    // puts the action's own `done` line over that Ваня's head for four seconds,
    // on BOTH screens, and every assertion at the end of this test is about which
    // balloon appeared and which pool it came from — so a fixture that talks is a
    // fixture that lies to the test. A write leaves the yard silent.
    await reviveInDb(ctxA, base, accA);
    await reviveInDb(ctxB, base, accB);

    // Confirmed on the plane rather than in the database: it is the SERVER's pose
    // that decides whether anybody mutters, and nothing below can happen while
    // somebody is drawn dead.
    await expect(
      playerDots(pageA).locator('[data-test="peer-face"][data-condition="dead"]'),
      'somebody in the yard is dead, and a dead Ваня has nothing to add',
    ).toHaveCount(0, { timeout: 15_000 });

    // NOBODY TAPS. That is deliberate, and it is the stronger version of this
    // test rather than the lazier one.
    //
    // Two reasons. It is what almost every Ваня in the yard is doing almost all
    // of the time — standing where he was left, having walked nowhere this
    // process — so it is the case the feature actually has to serve. And it is
    // the case that was BROKEN when this test was first written: "standing
    // still" is the server's `arrived`, a property of a JOURNEY, and somebody
    // holding a zero-length placement reported neither walking nor arrived, so
    // the muttering reached nobody. An earlier draft of this test worked around
    // it with a short hop each. The server was fixed instead
    // (`walk.progress` now treats a journey of no length as complete), and the
    // hop was removed so that a regression puts this test red rather than
    // quietly walking around the hole again.
    //
    // It also removes the last way a give-up line could enter the recording:
    // nobody walked, so nobody can have given up, so every balloon below is
    // unambiguously idle muttering.

    // Wait for one balloon that BOTH pages saw over the same entity within the
    // same slot. Matched on the id and the instant rather than read at one
    // moment from both screens: a four-second window read twice in sequence is a
    // race nobody needs to run, and what the claim is actually about is the slot
    // — the same remark, not the same millisecond.
    const deadline = Date.now() + CHATTER_TIMEOUT_MS;
    let shared: { id: string; onA: string; onB: string } | undefined;
    while (!shared && Date.now() < deadline) {
      const [heardByA, heardByB] = await Promise.all([saidSoFar(pageA), saidSoFar(pageB)]);
      for (const mine of heardByA) {
        const theirs = heardByB.find(
          (line) => line.id === mine.id && Math.abs(line.at - mine.at) < SAME_SLOT_MS,
        );
        if (theirs) {
          shared = { id: mine.id, onA: mine.text, onB: theirs.text };
          break;
        }
      }
      if (!shared) await new Promise((resolve) => setTimeout(resolve, 500));
    }

    expect(
      shared,
      `nobody in the yard said a word on both screens in ${CHATTER_TIMEOUT_MS / 1000}s — ` +
        `A heard ${JSON.stringify((await saidSoFar(pageA)).map((line) => line.text))} and ` +
        `B heard ${JSON.stringify((await saidSoFar(pageB)).map((line) => line.text))}`,
    ).toBeDefined();
    const seen = shared as { id: string; onA: string; onB: string };

    // It was over a PLAYER. The regulars are drawn by the same code path and are
    // deliberately silent — they have no account to hash, and giving them lines
    // would make the yard's ambient noise come from the scenery.
    expect(seen.id, 'a regular said something; the cast does not talk').not.toMatch(/^npc-/);

    // THE assertion. Two browsers, one set of words.
    expect(
      seen.onB,
      `A saw ${seen.id} say «${seen.onA}» and B saw the same Ваня say «${seen.onB}» — ` +
        'the line is being decided per client rather than once by the server',
    ).toBe(seen.onA);

    // And it is idle muttering rather than anything else a balloon can be. The
    // pool is not served to anybody, so a client that agreed with another client
    // on a line from it could only have been told the line.
    expect(
      IDLE_SAYS,
      `${seen.id} said «${seen.onA}», which is not in the idle pool and is not anything ` +
        'this test can account for — nobody tapped at all, so nobody can have given up on a walk',
    ).toContain(seen.onA);
  } finally {
    await ctxA.close();
    await ctxB.close();
  }
});
