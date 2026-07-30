import { expect, test } from '@playwright/test';
import { loginAs } from './fixtures';

/**
 * «СИМУЛЯТОР ФИНТЕХА» — full stack: the real Go binary, a real PostgreSQL, a real
 * session cookie and the real realtime hub.
 *
 * ONE CASE, AND IT IS THE ONE THE STUBBED SUITE CANNOT MAKE. Everything about
 * how this game LOOKS is asserted in `web/e2e/gamefintech.spec.ts`, where the test
 * is the server and can put the office into any state it likes. What no stub can
 * say is that a shift actually reached Postgres — that the room really opened,
 * that walking out really wrote a row, and that the row really comes back. So
 * this drives the whole loop through the UI and then asks the API.
 *
 * Note on API calls: always `page.request`, never the bare `request` fixture.
 * The fixture is a separate context with no cookies, so it would silently test
 * the anonymous path while looking like it tested the authenticated one.
 *
 * The harness shares one database, one server and six fixed accounts at
 * `workers: 1` — see playwright.stack.config.ts. Nothing here is scoped to a
 * test, so nothing here may assume it is the only shift in the table.
 */

/**
 * How long the shift has to last before the server keeps it.
 *
 * `MinShiftSeconds` in `internal/gamefintech`: a shift shorter than this is
 * dropped rather than written, so that a mis-tap does not fill the leaderboard
 * with nought-rouble entries. This is the one place in either Playwright suite
 * where elapsed wall time is the thing under test rather than something to be
 * waited out — the rule IS a duration, and there is no condition to poll that
 * would make it true sooner.
 */
const MIN_SHIFT_MS = 3_000;
/**
 * A little over the minimum, and deliberately NOT more.
 *
 * This test lives inside a window with a hard edge at each end, and the two are
 * not symmetrical:
 *
 *   * The FLOOR is `MinShiftSeconds` = 3.0 s, and it is one-sided-safe. A
 *     wall-clock wait can only ever overshoot, the shift was already running
 *     before the timer started (the POST and the first frame), and every
 *     millisecond a loaded runner adds to the click and the DELETE makes the
 *     shift longer. Load can only push us further inside this bound.
 *   * The CEILING is the bald man, and it is the one CI can actually break. Past
 *     it the shift ends as `promoted` and the assertion below reads «ТЕБЯ
 *     ПОВЫСИЛИ» instead.
 *
 * THE CEILING IS GUARANTEED BY A SERVER CONSTANT, not by geometry, and the
 * arithmetic here used to say otherwise. It claimed a fixed player spawn at
 * (8, 4) and `BossSpeed` 2.35 m/s giving 6.51 s of room; both numbers are gone.
 * The spawn is DRAWN now (`Office.spawnPoint`) and the walk speed is 4.0 m/s, so
 * neither the distance nor the time is a property of this test's setup.
 *
 * What replaces them is an inequality, and it is the reason `spawnHeadStart`
 * exists. `spawnPoint` resamples until the straight-line gap to the boss is at
 * least `spawnFromBoss = spawnHeadStart × BossSpeed + CatchRadius + PlayerRadius`,
 * where `spawnHeadStart = MinShiftSeconds + 0.5`. So *whatever* the draw returns,
 * he needs at least `MinShiftSeconds + 0.5` = 3.5 s to arrive — that constant is
 * defined to make a recordable shift always possible — and he has to walk round
 * the desks, so in practice it is several seconds more (the docs measure 10.35 s
 * worst case across the floor).
 *
 * Which makes the bound on SLACK_MS exact and independent of the room's shape:
 *
 *     SLACK_MS < (spawnHeadStart − MinShiftSeconds) × 1000 = 500 ms
 *
 * 300 leaves 200 ms of guaranteed runner latency and, in the overwhelmingly
 * common case, seconds. Raising it past 500 would make the test's correctness
 * depend on the boss taking the long way round, which is luck rather than a
 * guarantee. Standing still longer is not caution here — it is walking towards
 * the only thing that can fail.
 */
const SLACK_MS = 300;

test.describe('«СИМУЛЯТОР ФИНТЕХА»', () => {
  test.beforeEach(async ({ context }) => {
    await loginAs(context, 'user');
  });

  test('serves the whole office as one catalogue', async ({ page }) => {
    await page.goto('/app/game-fintech');
    const res = await page.request.get('/api/game-fintech/config');
    expect(res.status()).toBe(200);
    const config = (await res.json()) as {
      game_key: string;
      office: { w: number; h: number; desks: unknown[] };
      endings: { key: string }[];
      boss_lines: string[];
      player_lines: string[];
      personas: string[];
      claude_lines: string[];
      npcs: { key: string; name: string; lines: string[] }[];
    };
    // The load-bearing simplification: the office is STATIC and lives here, so
    // starting a shift sends no level and the client draws from this alone.
    //
    // AND THE KEY IS STILL `karen`, WHICH IS NOT A TYPO. The game was renamed to
    // «СИМУЛЯТОР ФИНТЕХА»; a `game_key` VALUE is data rather than a name, and it
    // is what art blobs in the shared store are keyed on, so it deliberately did
    // not move (migrations/014, and 007 before it). This line is the only place
    // in either suite that pins the literal, and the rename's own sweep rewrote
    // it to 'fintech' — which is how the mistake was caught, one commit before it
    // could reach production. Leave it.
    expect(config.game_key).toBe('karen');
    expect(config.office.w).toBeGreaterThan(0);
    expect(config.office.desks.length).toBeGreaterThan(0);
    expect(config.endings.map((e) => e.key).sort()).toEqual(['left', 'promoted']);
    // The balloons live here too, for the same reason: the frame carries an
    // INDEX ten times a second and the words are fetched once (ADR-037). Index 0
    // of each is what an omitted index means, so the two defaults are a contract
    // and not a preference.
    expect(config.player_lines[0]).toBe('Я КАРЕН');
    expect(config.boss_lines[0]).toBe('Я ЛЫСЫЙ');
    expect(config.boss_lines.length).toBeGreaterThan(1);
    // AND THE WHOLE CAST, because the owner logged in and could not see the two
    // non-players. Their names live here and only an INDEX rides the frame, so a
    // catalogue missing this array means two figures the client cannot draw.
    expect(config.npcs.length).toBeGreaterThan(1);
    for (const n of config.npcs) {
      expect(n.key).not.toBe('');
      expect(n.lines.length).toBeGreaterThan(0);
    }
    expect(config.claude_lines[0]).toBe('Я КЛОД');
    expect(config.personas[0]).toBe('Карен');
  });

  test('the office a real browser opens actually has Серега and Тёма in it', async ({ page }) => {
    // THE TEST THAT WAS MISSING. Every other assertion about the two of them was
    // made against a stubbed frame or against the office in Go — so «the server
    // sends them» and «the client can draw them» were both proved, and «they are on
    // screen when a real binary drives a real browser» was not. That is exactly the
    // gap a prod-only defect hides in.
    await page.goto('/app/game-fintech');
    await expect(page.getByTestId('fintech-splash')).toBeVisible();
    await page.getByTestId('fintech-start').click();
    await expect(page.getByTestId('fintech-play')).toBeVisible();

    const npcs = page.getByTestId('fintech-npc');
    await expect(npcs).toHaveCount(2);
    // Placed, rather than left stacked at the plane's default centre — which is what
    // an unwritten `--x` looks like and would read as one figure rather than two.
    await expect
      .poll(async () =>
        npcs.first().evaluate((el) => getComputedStyle(el).getPropertyValue('--x').trim()),
      )
      .not.toBe('');
    const boxes = await npcs.evaluateAll((els) =>
      els.map((el) => {
        const r = el.getBoundingClientRect();
        return { w: r.width, h: r.height, x: r.x };
      }),
    );
    for (const b of boxes) expect(b.w * b.h, 'a non-player has no box at all').toBeGreaterThan(0);
    expect(boxes[0].x, 'both of them are drawn in the same place').not.toBeCloseTo(boxes[1].x, 0);

    await page.getByTestId('fintech-quit').click();
  });

  test('the readouts name whoever the server drew you as', async ({ page }) => {
    // THE OTHER HALF OF THE SAME GAP THE NON-PLAYERS FELL INTO. That the draw varies is
    // proved over real HTTP in the integration suite; that the NAME reaches the screen
    // was proved nowhere, and for one deploy it did not — the served `personas` array
    // was nil, so the readout had no name to show and every shift looked identical
    // whatever the server had drawn.
    const res = await page.request.get('/api/game-fintech/config');
    const cast = ((await res.json()) as { personas: string[] }).personas;
    expect(cast.length).toBeGreaterThan(1);

    await page.goto('/app/game-fintech');
    await expect(page.getByTestId('fintech-splash')).toBeVisible();
    await page.getByTestId('fintech-start').click();
    await expect(page.getByTestId('fintech-play')).toBeVisible();

    const who = page.getByTestId('fintech-who');
    await expect(who).toHaveCount(1);
    const said = ((await who.textContent()) ?? '').trim();
    // One of the four, whichever was drawn — asserted as membership rather than as a
    // fixed name, because the whole point is that it is not fixed.
    expect(cast.some((n) => said.includes(n)), `the readout says «${said}»`).toBe(true);

    await page.getByTestId('fintech-quit').click();
  });

  test('a shift walked out of is written, and comes back from Postgres', async ({ page }) => {
    const before = await page.request.get('/api/game-fintech/shifts/me');
    expect(before.status()).toBe(200);
    const had = ((await before.json()) as { shifts: unknown[] }).shifts.length;

    await page.goto('/app/game-fintech');
    await expect(page.getByTestId('fintech-splash')).toBeVisible();
    await page.getByTestId('fintech-start').click();
    await expect(page.getByTestId('fintech-play')).toBeVisible();

    // The office answers over the real socket, which is what says the room was
    // opened by registering the handler rather than by anything in `realtime`
    // knowing this game exists.
    await expect(page.getByTestId('fintech-link')).toHaveCount(0);

    // Stand there long enough for the shift to be worth keeping. See MIN_SHIFT_MS.
    await page.waitForTimeout(MIN_SHIFT_MS + SLACK_MS);

    await page.getByTestId('fintech-quit').click();
    // The ending's words come from the served catalogue, so this is also the
    // assertion that the real content.go and the real client agree.
    //
    // If this ever reads «ТЕБЯ ПОВЫСИЛИ», the bald man arrived first: the runner
    // spent more than the ~200 ms of guaranteed slack between the clock-in and
    // this click, and probably a great deal more, since he normally has to walk
    // round the desks. Read SLACK_MS above before treating it as a bug in the
    // game — and if it recurs on an idle machine, the thing to change is
    // `spawnHeadStart`, not this timer.
    await expect(page.getByTestId('fintech-over-title')).toHaveText('ТЫ ПРОСТО УШЁЛ');

    // The row is written by a separate goroutine reading a buffered channel, so
    // it is polled rather than expected to be there the instant the DELETE
    // returns — bounded by expect.poll's own deadline, never by an attempt count.
    await expect
      .poll(
        async () => {
          const res = await page.request.get('/api/game-fintech/shifts/me');
          if (!res.ok()) return -1;
          return ((await res.json()) as { shifts: unknown[] }).shifts.length;
        },
        { message: 'the shift never reached game_fintech_shifts' },
      )
      .toBeGreaterThan(had);

    const after = (await (await page.request.get('/api/game-fintech/shifts/me')).json()) as {
      shifts: { cause: string; salary: number; seconds: number }[];
    };
    const mine = after.shifts[0];
    expect(mine.cause).toBe('left');
    expect(mine.seconds).toBeGreaterThanOrEqual(3);

    // And it is on the board, which is a different query over the same row.
    const top = (await (await page.request.get('/api/game-fintech/shifts/top')).json()) as {
      shifts: { name: string; salary: number }[];
    };
    expect(top.shifts.length).toBeGreaterThan(0);
  });
});
