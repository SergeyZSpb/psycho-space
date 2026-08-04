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
 * Picks a movement key whose walk has somewhere to go from where the spawn
 * landed, plus the assertion that goes with it.
 *
 * A spawn is DRAWN, so no key is safe to hardcode: the draw keeps its distance
 * from the лысый at the far wall, which puts most spawns in the band at the other
 * end, where "up" is a wall. It takes the plane coordinates the figure is drawn
 * with — both 0..1 across the office — and the served catalogue, which carries
 * the office's size, everything solid in it and the player's radius.
 *
 * `KeyW` is −Y and `KeyS` is +Y, because the office's +Y points DOWN and so does
 * the screen's; there is no axis flip anywhere in this client, and asserting the
 * direction is half of what this test is for.
 *
 * IT MEASURES ALL FOUR DIRECTIONS AND TAKES THE ROOMIEST. The earlier version
 * took the first of one axis's two that had a clear 0.8 m and threw when neither
 * did — survivable while the floor was three desks, and reachable now that the
 * layout is stored and can be denser: a spawn in a gap between two solids has
 * neither of one axis's directions clear while the other axis is wide open.
 * Choosing by clearance also fails INFORMATIVELY, because the message carries
 * what every direction actually had.
 *
 * `only` NARROWS IT TO ONE AXIS, and the caller needs that for the second leg:
 * proving the axes are neither swapped nor flipped takes one vertical walk and
 * one horizontal one, so the second call asks for whatever the first did not use
 * — still the roomier of that axis's two rather than the first that qualifies.
 */
function clearKey(
  cfg: {
    office: {
      w: number;
      h: number;
      player_radius: number;
      solids: { x: number; y: number; w: number; h: number }[];
    };
    move: { walk_speed: number };
  },
  from: { x: number; y: number },
  only?: 'vertical' | 'horizontal',
): {
  key: string;
  axis: 'vertical' | 'horizontal';
  moved: (a: { x: number; y: number }, b: { x: number; y: number }) => boolean;
} {
  const { w, h, player_radius: r, solids } = cfg.office;
  // How far is worth measuring. The poll asks for 0.01 of the plane, which is
  // about eleven centimetres, so eighty of them is seven times the margin — and
  // demanding a whole half-second of walking (3.2 m) rejects most of the floor,
  // which is how the first version of this helper threw instead of choosing.
  const reach = 0.8;
  const step = 0.05;
  const free = (mx: number, my: number) => {
    if (mx < r || my < r || mx > w - r || my > h - r) return false;
    return !solids.some(
      (d) => mx > d.x - r && mx < d.x + d.w + r && my > d.y - r && my < d.y + d.h + r,
    );
  };
  /** How far this direction is walkable before something is in the way, in metres. */
  const clearance = (dx: number, dy: number) => {
    let s = 0;
    while (s + step <= reach + 1e-9 && free(from.x * w + dx * (s + step), from.y * h + dy * (s + step))) {
      s += step;
    }
    return s;
  };
  const options = [
    { key: 'KeyW', axis: 'vertical', dx: 0, dy: -1, moved: (a: { y: number }, b: { y: number }) => b.y < a.y - 0.01 },
    { key: 'KeyS', axis: 'vertical', dx: 0, dy: 1, moved: (a: { y: number }, b: { y: number }) => b.y > a.y + 0.01 },
    { key: 'KeyD', axis: 'horizontal', dx: 1, dy: 0, moved: (a: { x: number }, b: { x: number }) => b.x > a.x + 0.01 },
    { key: 'KeyA', axis: 'horizontal', dx: -1, dy: 0, moved: (a: { x: number }, b: { x: number }) => b.x < a.x - 0.01 },
  ] as const;
  const measured = options
    .filter((o) => !only || o.axis === only)
    .map((o) => ({ ...o, room: clearance(o.dx, o.dy) }));
  const best = measured.reduce((a, b) => (b.room > a.room ? b : a));
  // A tenth of a metre is ten times the 0.01 of the plane the poll asks for, so a
  // direction that clears it is a direction the assertion can see.
  if (best.room < 0.1) {
    throw new Error(
      `nowhere to walk ${only ?? 'at all'} from ${from.x}/${from.y}: ${measured
        .map((m) => `${m.key} ${m.room.toFixed(2)}m`)
        .join(', ')}`,
    );
  }
  return { key: best.key, axis: best.axis, moved: best.moved };
}
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
      office: { id: string; w: number; h: number; solids: { kind: string }[]; windows: unknown[] };
      endings: { key: string }[];
      boss_lines: string[];
      player_lines: string[];
      personas: string[];
      claude_lines: string[];
      npcs: { key: string; name: string; lines: string[] }[];
      renovation: { hour_utc: number };
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
    expect(config.office.solids.length).toBeGreaterThan(0);
    // AND THE LAYOUT HAS AN IDENTITY, which is the whole of how a client knows the
    // geometry it cached is still the geometry everybody is standing in: the office
    // is stored and rebuildable now, and every shift response names the layout it
    // belongs to. A content hash, so the shape is pinned rather than the value —
    // two identical floors have the same id on every machine and after every
    // deploy, which is what makes it a cache key rather than a row id.
    expect(config.office.id).toMatch(/^[0-9a-f]{16}$/);
    // Every solid says what it is, because the kind is the only thing that decides
    // how it is drawn. Not pinned to a fixed set: the wire carries a plain string
    // precisely so the office can grow a fourth kind without a client deploy.
    for (const s of config.office.solids) expect(s.kind).not.toBe('');
    // THREE ENDINGS, and the third is the one nobody in the office chooses: an
    // admin rebuilt the floor underneath everybody. Pinned as a set rather than
    // as a count, because the client renders whatever is served and the only
    // thing worth asserting is that the server really publishes all of them —
    // the words themselves are `content.go`'s to change.
    expect(config.endings.map((e) => e.key).sort()).toEqual(['left', 'promoted', 'renovated']);
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
    // AND WHEN THE BUILDING REBUILDS THE OFFICE, which is the one rule of this
    // game a player cannot discover by playing. The splash derives both the UTC
    // and the Moscow wording from this number, so an hour that never reached the
    // wire would be a screen that says nothing about being thrown out at
    // midnight — pinned as a real hour rather than as the literal 21, which is
    // `content.go`'s to retune.
    expect(config.renovation.hour_utc).toBeGreaterThanOrEqual(0);
    expect(config.renovation.hour_utc).toBeLessThanOrEqual(23);
  });

  test('the office a real browser opens has the whole cast in it, and names you', async ({
    page,
  }) => {
    // TWO CLAIMS, ONE OFFICE VISIT, and the reason is the suite's own budget rather
    // than tidiness. This runs at `workers: 1` against one server behind the same
    // per-IP rate limiter production uses, so every extra login-navigate-start-quit
    // cycle spends part of a shared allowance — and three separate tests here pushed
    // the run over it, which surfaced as a 429 on `/api/auth/me` in an unrelated yard
    // spec twenty tests later. Denser tests, not a raised limit: the limiter is doing
    // its job.
    //
    // THE GAP BOTH HALVES FELL INTO. Every other assertion about the non-players and
    // about the persona was made against a stubbed frame or against the office in Go —
    // so «the server sends it» and «the client can draw it» were both proved, and «it
    // is on screen when a real binary drives a real browser» was not. It was not, for
    // one deploy: the served `npcs` and `personas` arrays were both nil.
    const res = await page.request.get('/api/game-fintech/config');
    const cast = (await res.json()) as { personas: string[]; npcs: { key: string }[] };
    expect(cast.personas.length).toBeGreaterThan(1);
    expect(cast.npcs.length).toBeGreaterThan(1);

    await page.goto('/app/game-fintech');
    await expect(page.getByTestId('fintech-splash')).toBeVisible();
    await page.getByTestId('fintech-start').click();
    await expect(page.getByTestId('fintech-play')).toBeVisible();

    // Both non-players are there, placed apart rather than stacked at the plane's
    // default centre — which is what an unwritten `--x` looks like and would read as
    // one figure rather than two.
    const npcs = page.getByTestId('fintech-npc');
    await expect(npcs).toHaveCount(cast.npcs.length);
    await expect
      .poll(async () =>
        npcs.first().evaluate((el) => getComputedStyle(el).getPropertyValue('--x').trim()),
      )
      .not.toBe('');
    const boxes = await npcs.evaluateAll((els) =>
      els.map((el) => {
        const r = el.getBoundingClientRect();
        return { area: r.width * r.height, x: r.x };
      }),
    );
    for (const b of boxes) expect(b.area, 'a non-player has no box at all').toBeGreaterThan(0);
    expect(boxes[0].x, 'both of them are drawn in the same place').not.toBeCloseTo(boxes[1].x, 0);

    // AND THE SHIFT'S OWN CLOCK, which is the second scored dimension and reaches
    // the screen without a byte on the snapshot: the ready frame said which office
    // tick this shift began on and every frame says which tick the office is on. A
    // real socket against a real office is the only place that subtraction is
    // proved end to end.
    await expect(page.getByTestId('fintech-hud-alive')).toBeVisible();
    await expect
      .poll(async () => (await page.getByTestId('fintech-hud-alive').textContent())?.trim())
      .toMatch(/\d+:\d\d/);
    // And the office's tempo, derived from the same tick against the served ramp.
    await expect(page.getByTestId('fintech-hud-tempo')).toContainText('×1');

    await page.getByTestId('fintech-quit').click();

    // And the ENDING names whoever the server drew you as — asserted as membership
    // of the served cast rather than as a fixed name, because the whole point is
    // that it is not fixed. That the draw VARIES is proved over real HTTP in the
    // integration suite, where forty shifts cost no browser. It is read here rather
    // than on the play HUD because the persona no longer has a cell there: it
    // changes nothing about the game and it was the widest readout in the strip.
    const said = ((await page.getByTestId('fintech-over-who').textContent()) ?? '').trim();
    expect(
      cast.personas.some((n) => said.includes(n)),
      `the ending says «${said}»`,
    ).toBe(true);
  });

  test('WASD and space play the game, against the real office', async ({ page }) => {
    // DESKTOP CONTROLS, END TO END. The stubbed suite deliberately makes no claim about
    // input — frames are emitted from the render loop, which a browser pauses for a
    // backgrounded tab — so the axes are unit-tested and the WIRE is proved here, where
    // there is one worker, the page is visible, and the office on the other end is real.
    await page.goto('/app/game-fintech');
    await expect(page.getByTestId('fintech-splash')).toBeVisible();
    await page.getByTestId('fintech-start').click();
    await expect(page.getByTestId('fintech-play')).toBeVisible();

    const me = page.getByTestId('fintech-me');
    const at = () =>
      me.evaluate((el) => ({
        x: parseFloat(getComputedStyle(el).getPropertyValue('--x')),
        y: parseFloat(getComputedStyle(el).getPropertyValue('--y')),
      }));
    // Wait for a real position before measuring one: the first frame is what seeds the
    // predictor, and reading before it arrives measures the plane's default centre.
    await expect.poll(async () => Number.isFinite((await at()).y)).toBe(true);
    const before = await at();

    // THE KEYS ARE CHOSEN FROM WHERE THE SPAWN LANDED, and that is not fussiness —
    // it is how this test failed in CI. A spawn is DRAWN (Office.spawnPoint), and
    // the draw keeps its distance from the лысый, who stands at the far wall — so
    // a spawn is usually in the band at the OPPOSITE end, where "up" is a wall a
    // few centimetres behind you. Pressing W there moves nobody, and a test that
    // reads "he did not move" cannot tell that from a broken key handler.
    //
    // So: one vertical key and one horizontal key, each picked because the walk
    // has somewhere to go. The claim — that WASD reaches the real office and that
    // the axes are not swapped or flipped — is unchanged.
    const cfg = await (await page.request.get('/api/game-fintech/config')).json();
    const first = clearKey(cfg, before);

    // Held, not tapped: a walk is a state, and the emitter samples the axes at the
    // served rate rather than per key event.
    await page.keyboard.down(first.key);
    await expect.poll(async () => first.moved(before, await at()), { timeout: 5_000 }).toBe(true);
    await page.keyboard.up(first.key);

    // And then the OTHER axis, so the mapping is proved rather than just
    // "something moved" — measured from where the first walk actually ended,
    // which is also a second draw at finding room to walk in.
    const mid = await at();
    const second = clearKey(cfg, mid, first.axis === 'vertical' ? 'horizontal' : 'vertical');
    await page.keyboard.down(second.key);
    await expect.poll(async () => second.moved(mid, await at()), { timeout: 5_000 }).toBe(true);
    await page.keyboard.up(second.key);

    // Space dashes, and the readout says so — which is the office answering, since the
    // cooldown is on the snapshot rather than run locally.
    await expect(page.getByTestId('fintech-hud-dash')).toContainText('ГОТОВ');
    await page.keyboard.press('Space');
    await expect(page.getByTestId('fintech-hud-dash')).not.toContainText('ГОТОВ');

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

    // And it is on BOTH boards, which are two different queries over the same row —
    // the game scores money and length, and one response carries each, because the
    // splash draws them side by side.
    const top = (await (await page.request.get('/api/game-fintech/shifts/top')).json()) as {
      salary: { name: string; salary: number; seconds: number }[];
      seconds: { name: string; salary: number; seconds: number }[];
    };
    expect(top.salary.length).toBeGreaterThan(0);
    expect(top.seconds.length).toBeGreaterThan(0);
    // Every row carries both numbers wherever it appears, so the two read as one
    // scoreboard rather than as two lists of unrelated figures.
    expect(top.seconds[0].seconds).toBeGreaterThan(0);
    expect(top.salary[0].seconds).toBeGreaterThan(0);
  });
});
