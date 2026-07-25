import { execFileSync } from 'node:child_process';
import { expect, test, type BrowserContext, type Page } from '@playwright/test';
import { loginAs, stack, type SeededAccount, type SeededKind } from './fixtures';

// «Ванягоччи» — the pet, against the real stack: the real Go binary, the real
// catalogue, and a real PostgreSQL. Nothing is stubbed.
//
// The sibling full-stack spec proves the plane, which is presence: it lives in
// memory and is gone the moment the process is. This file proves the opposite
// half — the first thing this game has ever had that OUTLIVES a reload — and
// that is why it needs the real database rather than a route stub. The mobile
// suite fakes both endpoints, so it would pass just as happily against a server
// that never wrote a row.
//
// It is also the only place the COUPLING is exercised end to end. Health is no
// longer a chore of its own: it is what an empty beer and a full bladder do to
// him, and the rate the client draws between fetches is computed on the server
// from both. A stubbed suite can only assert that the client believes a number;
// this one asserts the server produced it.
//
// `loginAs` is imported rather than copied because the seeded-account harness is
// platform, not a game's property — the same reason the sibling gives.

/** The two accounts the stack seeds as *approved*; anything else is refused. */
const PLAYER_A: SeededKind = 'user';
const PLAYER_B: SeededKind = 'superadmin';

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

/** The catalogue's own numbers, mirrored from internal/gamevanyagotchi/content.go. */
const HP_START = 65;
const BEER_START = 60;
/** What one drink puts back, per stat. Three of them, which is the whole joke. */
const DRINK_HP = 15;
const DRINK_BEER = 40;
const DRINK_BLADDER = 25;
/** hp's own rot, and what EACH unmet need adds to it. */
const HP_DECAY_PER_HOUR = 1;
const HP_PENALTY_PER_HOUR = 6;
/** The thresholds those penalties fire at — also the drivers' own warning marks. */
const BEER_EMPTY_AT = 20;
const BLADDER_FULL_AT = 80;

// ---------------------------------------------------------------------------
// Direct database setup.
//
// Needed because of a real property of the shipped catalogue rather than for
// convenience: nothing decays fast enough to produce an observable change inside
// a test's lifetime — hp loses one point an hour with both needs met, thirteen
// with neither. A fresh pet does now start below its maximum — that was changed
// precisely so the first press of an action is not a clamped no-op — but proving
// a value was WRITTEN, STORED and READ BACK across a reload needs a starting
// point far enough from the ceiling that the delta cannot be swallowed by
// clamping. So the rows get low values first and the round trip is driven from
// there.
//
// Whitebox, and deliberately so — the project's rule is real flows or direct DB
// setup, and inventing a "set hp" endpoint to make a test possible is exactly
// the test-only production path that rule exists to forbid.
//
// The container name is fixed in docker-compose.yml (`psycho-pg-e2e`), so this
// needs no compose-project resolution; the suite already requires Docker to run
// at all.
// ---------------------------------------------------------------------------

const DB_CONTAINER = 'psycho-pg-e2e';

/** Runs one statement against the e2e database and returns its unaligned output. */
function psql(sql: string): string {
  try {
    return execFileSync(
      'docker',
      ['exec', '-i', DB_CONTAINER, 'psql', '-U', 'psychospace', '-d', 'psychospace', '-At', '-c', sql],
      { encoding: 'utf8', timeout: 30_000 },
    ).trim();
  } catch (err) {
    throw new Error(
      `psql against ${DB_CONTAINER} failed — is the e2e stack up? (scripts/e2e-stack.sh)\n${String(err)}`,
    );
  }
}

/**
 * Guards an id before it is spliced into SQL.
 *
 * The values come from the seed file rather than from anything user-supplied, so
 * this is not a security control — it is a guard against a malformed seed file
 * turning into a confusing SQL syntax error three lines later.
 */
function uuid(value: string): string {
  expect(value, 'expected a UUID from the seed file').toMatch(
    /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i,
  );
  return value;
}

/**
 * Removes an account's pet entirely, so the next read creates a genuinely new
 * one.
 *
 * The suite shares one database and the pet is per-account and permanent, so
 * "freshly seeded" stops being true the moment any earlier test has opened the
 * yard. Resetting here is what makes each test below independent of the order
 * the files happen to run in.
 */
function forgetPet(account: SeededAccount): void {
  const id = uuid(account.account_id);
  psql(
    `DELETE FROM game_vanyagotchi_pet_stats WHERE pet_id IN ` +
      `(SELECT id FROM game_vanyagotchi_pets WHERE account_id = '${id}')`,
  );
  psql(`DELETE FROM game_vanyagotchi_pets WHERE account_id = '${id}'`);
}

/**
 * Writes stats straight into the rows the server decays from.
 *
 * Every named stat is stamped with the SAME instant, which is not tidiness: the
 * coupled decay is only exact because every write re-stamps every stat, so a
 * fixture that moved one row and left another's `as_of` behind would set up a
 * window the server's own arithmetic does not assume exists.
 */
function setStats(account: SeededAccount, values: Record<string, number>): void {
  const id = uuid(account.account_id);
  const cases = Object.entries(values)
    .map(([key, value]) => `WHEN '${key}' THEN ${value}`)
    .join(' ');
  const keys = Object.keys(values)
    .map((key) => `'${key}'`)
    .join(', ');
  const updated = psql(
    `UPDATE game_vanyagotchi_pet_stats SET value = CASE stat_key ${cases} END, ` +
      `as_of = now(), updated_at = now() ` +
      `WHERE stat_key IN (${keys}) AND pet_id IN ` +
      `(SELECT id FROM game_vanyagotchi_pets WHERE account_id = '${id}' AND deleted_at IS NULL) ` +
      `RETURNING stat_key`,
  );
  expect(
    // `psql -At` prints the command tag ("UPDATE 3") after the returned rows,
    // which is not one of them.
    updated
      .split('\n')
      .filter((line) => line && !/^[A-Z]+ \d+$/.test(line))
      .sort(),
    'wrong rows were set — was the pet created first?',
  ).toEqual(Object.keys(values).sort());
}

// ---------------------------------------------------------------------------

const CONFIG_URL = '/api/game-vanyagotchi/config';
const STATE_URL = '/api/game-vanyagotchi/state';
const DRINK_URL = '/api/game-vanyagotchi/actions/drink';
const RELIEVE_URL = '/api/game-vanyagotchi/actions/relieve';

/** The death notice, which is the screen's own line rather than the catalogue's. */
const DEATH_LINE = 'Ваня не выдержал. Откачай его.';

/** Opens a logged-in phone-sized page already standing in the yard. */
async function enterYard(context: BrowserContext, baseURL: string): Promise<Page> {
  const page = await context.newPage();
  await page.goto(`${baseURL}/app/game-vanyagotchi`);
  await enterYardFrom(page);
  return page;
}

/** Steps an already-loaded intro screen into the yard. Also used after a reload. */
async function enterYardFrom(page: Page): Promise<void> {
  await page.getByRole('button', { name: 'Во двор' }).click();
  await expect(page.locator('[data-test="plane"]')).toBeVisible();
}

/** A stat's number as the screen is currently showing it. */
async function shown(page: Page, key: string): Promise<number> {
  const text = await page.locator(`[data-test="stat-value-${key}"]`).textContent();
  return Number.parseInt((text ?? '').trim(), 10);
}

const shownHp = (page: Page) => shown(page, 'hp');
const actionBtn = (page: Page, key: string) => page.locator(`[data-test="action-${key}"]`);

/** Creates the pet the way the app does — a plain read — and returns the state. */
async function readState(page: Page): Promise<Record<string, unknown>> {
  const res = await page.request.get(STATE_URL);
  expect(res.status(), `GET ${STATE_URL}`).toBe(200);
  return (await res.json()) as Record<string, unknown>;
}

/** One stat as it came off the wire, by key. */
interface WireStat {
  key: string;
  value: number;
  as_of: string;
  rate_per_hour: number;
}

function wireStat(state: Record<string, unknown>, key: string): WireStat {
  const found = (state.stats as WireStat[]).find((s) => s.key === key);
  expect(found, `no ${key} in the state`).toBeDefined();
  return found as WireStat;
}

test('a freshly seeded account gets a Ваня the first time it opens the yard', async ({
  browser,
  baseURL,
}) => {
  // Lazy creation is the whole storage design: no migration backfills anything,
  // no signup hook makes a pet, and the first read that notices there is none
  // writes one. Deleting the row first is what makes "first time" true in a
  // suite that shares one database.
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const seeded = await stack();
  forgetPet(seeded[PLAYER_A]);

  const context = await browser.newContext(PHONE);
  try {
    await loginAs(context, PLAYER_A);
    const page = await enterYard(context, base);

    // All three catalogue stats and both verbs, rendered from the config the
    // server served — no fixture anywhere in this file told the client what a
    // stat is called or what a button says.
    await expect(page.locator('[data-test="pet-stats"]')).toBeVisible();
    await expect(page.locator('[data-test="stat-hp"]')).toBeVisible();
    await expect(page.locator('[data-test="stat-beer"]')).toBeVisible();
    await expect(page.locator('[data-test="stat-bladder"]')).toBeVisible();
    await expect(actionBtn(page, 'drink')).toBeVisible();
    await expect(actionBtn(page, 'relieve')).toBeVisible();

    // A new Ваня starts BELOW the maximum — deliberately, so that the first
    // press of an action has something to do — and has had no time to decay
    // from it yet.
    await expect.poll(() => shownHp(page)).toBeGreaterThanOrEqual(HP_START - 5);
    await expect.poll(() => shownHp(page)).toBeLessThanOrEqual(HP_START);
    await expect.poll(() => shown(page, 'beer')).toBeGreaterThanOrEqual(BEER_START - 5);
    await expect.poll(() => shown(page, 'beer')).toBeLessThanOrEqual(BEER_START);
    // Empty, and it FILLS from here rather than draining — the one stat whose
    // rate is negative.
    await expect(page.locator('[data-test="stat-value-bladder"]')).toHaveText('0');
    // Alive, so no death line.
    await expect(page.locator('[data-test="pet-line"]')).toHaveText('');
  } finally {
    await context.close();
  }
});

test('one drink moves three stats, and all three survive a reload', async ({ browser, baseURL }) => {
  // THE point of the iteration. Everything this game had before now lived in the
  // server's memory and was gone the moment the page was: a reload put you back
  // in the middle of the yard with nothing to show for the session. Numbers that
  // are still there after a full page load are a different kind of thing, and it
  // can only be proved against a real database.
  //
  // Three of them from ONE press, which is what the action's `effects` slice
  // bought: drinking tops him up, cheers him up and fills his bladder. The
  // client posts a verb and never a value, so every number below was computed
  // and stored by the server.
  test.setTimeout(90_000);
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const seeded = await stack();
  forgetPet(seeded[PLAYER_A]);

  const context = await browser.newContext(PHONE);
  try {
    await loginAs(context, PLAYER_A);
    const page = await context.newPage();
    await page.goto(`${base}/app/game-vanyagotchi`);

    // Create the pet through the real endpoint, then put it in a state a drink
    // can visibly improve — see the note on direct setup above. Every value is
    // comfortably clear of a threshold, so hp is rotting at its own rate and
    // nothing drifts noticeably while the test runs.
    await readState(page);
    setStats(seeded[PLAYER_A], { hp: 20, beer: 30, bladder: 10 });

    await enterYardFrom(page);
    await expect.poll(() => shownHp(page)).toBeLessThanOrEqual(21);

    await actionBtn(page, 'drink').click();
    await expect(page.locator('[data-test="pet-line"]')).toHaveText('хорошо пошло');

    const after = {
      hp: await shownHp(page),
      beer: await shown(page, 'beer'),
      bladder: await shown(page, 'bladder'),
    };
    // 20+15, 30+40, 10+25 — each computed by the server and sent back. The
    // bladder is the one that proves the effects are a LIST rather than a single
    // stat: a client or a server that applied only the first would leave it at
    // 10 and everything else would still look right.
    expect(after.hp, `hp after a drink: ${after.hp}`).toBeGreaterThanOrEqual(20 + DRINK_HP - 2);
    expect(after.beer, `beer after a drink: ${after.beer}`).toBeGreaterThanOrEqual(
      30 + DRINK_BEER - 2,
    );
    expect(after.bladder, `bladder after a drink: ${after.bladder}`).toBeGreaterThanOrEqual(
      10 + DRINK_BLADDER - 2,
    );

    // A full page load: new document, new socket, new everything. The only place
    // the numbers can come back from is Postgres.
    await page.reload();
    await enterYardFrom(page);

    await expect
      .poll(() => shownHp(page), {
        message: 'the drunk hp did not survive the reload',
        timeout: 15_000,
      })
      // A range rather than the exact number, because everything decays while
      // the test runs — a point an hour here, so a couple of points of slack is
      // far more than a test's lifetime and still nowhere near the 20 it
      // started at.
      .toBeGreaterThanOrEqual(after.hp - 3);
    expect(await shownHp(page), 'hp climbed on its own across a reload').toBeLessThanOrEqual(
      after.hp + 1,
    );
    expect(await shown(page, 'beer'), 'the beer did not survive the reload').toBeGreaterThanOrEqual(
      after.beer - 3,
    );
    // The bladder fills rather than drains, so time moves it the other way — and
    // that asymmetry is itself worth pinning: a server that treated every rate
    // as a drain would send this one back LOWER.
    expect(
      await shown(page, 'bladder'),
      'the bladder did not survive the reload',
    ).toBeGreaterThanOrEqual(after.bladder - 1);
  } finally {
    await context.close();
  }
});

test('relieving himself empties the bladder and leaves the rest alone', async ({
  browser,
  baseURL,
}) => {
  // The second verb, and the other half of the loop drinking creates. Its effect
  // is a delta larger than the whole scale, so "reset" is the clamp doing its
  // job rather than a mechanism of its own — which only the real server can be
  // shown to do.
  test.setTimeout(90_000);
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const seeded = await stack();
  forgetPet(seeded[PLAYER_A]);

  const context = await browser.newContext(PHONE);
  try {
    await loginAs(context, PLAYER_A);
    const page = await context.newPage();
    await page.goto(`${base}/app/game-vanyagotchi`);

    await readState(page);
    // A bladder past its warning mark, so the bar is visibly in trouble before
    // the press and visibly not after it.
    setStats(seeded[PLAYER_A], { hp: 50, beer: 40, bladder: 90 });

    await enterYardFrom(page);
    await expect.poll(() => shown(page, 'bladder')).toBeGreaterThanOrEqual(90);
    await expect(page.locator('[data-test="stat-bladder"][data-trouble="1"]')).toHaveCount(1);

    await actionBtn(page, 'relieve').click();
    await expect(page.locator('[data-test="pet-line"]')).toHaveText('полегчало');

    await expect.poll(() => shown(page, 'bladder')).toBe(0);
    await expect(page.locator('[data-test="stat-bladder"][data-trouble="1"]')).toHaveCount(0);
    // And nothing else moved: relieving himself is one effect, not a general
    // reset, so a bug that wrote every stat back at its starting value would
    // show up here rather than nowhere.
    expect(await shownHp(page), 'relieving moved his health').toBeLessThanOrEqual(50);
    expect(await shownHp(page)).toBeGreaterThanOrEqual(48);
    expect(await shown(page, 'beer'), 'relieving moved his beer').toBeLessThanOrEqual(40);
    expect(await shown(page, 'beer')).toBeGreaterThanOrEqual(38);

    // It persisted, rather than only having been drawn.
    await page.reload();
    await enterYardFrom(page);
    await expect.poll(() => shown(page, 'bladder'), { timeout: 15_000 }).toBeLessThanOrEqual(1);
  } finally {
    await context.close();
  }
});

test('a dead Ваня refuses the toilet and accepts a beer', async ({ browser, baseURL }) => {
  // The refusal path, which only became REACHABLE with the second verb: until
  // there was an action that cannot revive him, every request succeeded and the
  // 409 was a branch nobody could reach. A dead Ваня does not go to the toilet.
  //
  // Both halves matter. The refusal must be a 409 with the stable code rather
  // than a 500, and the screen must answer it with the line that says what to do
  // — not with the global "something went wrong" modal for a situation the game
  // is already explaining in words.
  test.setTimeout(90_000);
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const seeded = await stack();
  forgetPet(seeded[PLAYER_A]);

  const context = await browser.newContext(PHONE);
  try {
    await loginAs(context, PLAYER_A);
    const page = await context.newPage();
    await page.goto(`${base}/app/game-vanyagotchi`);

    await readState(page);
    // On the floor. The death is not written by this UPDATE — it is recorded by
    // the first READ that observes hp at zero, which is the lazy-materialisation
    // shape the rest of this game uses too.
    setStats(seeded[PLAYER_A], { hp: 0, beer: 5, bladder: 95 });

    const dead = await readState(page);
    expect(dead.alive, 'a Ваня at zero health was still reported alive').toBe(false);
    expect((dead.pet as { died_at: string | null }).died_at, 'no death was recorded').not.toBeNull();

    // The wire contract, asserted directly: 409 and the stable code, not a 500
    // and not a silent success.
    const refused = await page.request.post(RELIEVE_URL);
    expect(refused.status(), 'relieving a corpse should be refused with a 409').toBe(409);
    expect((await refused.json()) as { error: string }).toMatchObject({ error: 'pet_dead' });

    // And now on screen, where the death has to read without anybody parsing a
    // number.
    await enterYardFrom(page);
    await expect(page.locator('[data-test="pet-line"]')).toHaveText(DEATH_LINE);

    // The beer is the way back, and it is in character that it is. `revives`
    // requires the action to actually lift the fatal stat off its floor, so this
    // is a real revival rather than a flag being cleared.
    await actionBtn(page, 'drink').click();
    await expect(page.locator('[data-test="pet-line"]')).toHaveText('хорошо пошло');
    await expect.poll(() => shownHp(page)).toBeGreaterThanOrEqual(DRINK_HP - 2);

    const revived = await readState(page);
    expect(revived.alive, 'a beer did not bring him round').toBe(true);
    expect(
      (revived.pet as { died_at: string | null }).died_at,
      'the death was still recorded after a revival',
    ).toBeNull();

    // Now kill him again BEHIND THE PAGE'S BACK, which is the honest way a
    // refusal reaches a player: nothing re-reads between actions, so the first
    // the client hears of a death is the server turning an action down.
    //
    // Killing him a second time rather than pressing relieve while the screen
    // already said he was dead, because that assertion would have been vacuous —
    // the line was ALREADY the death notice, so it would have passed however the
    // refusal was handled. Here the line has to CHANGE, from the drink's own
    // `done` text to the notice, and only the handled 409 can do that.
    setStats(seeded[PLAYER_A], { hp: 0 });

    await actionBtn(page, 'relieve').click();
    // The refusal is answered by the line that says what to do about it — and
    // the global "something went wrong" modal never opens over a situation the
    // game is already explaining in words.
    await expect(page.locator('[data-test="pet-line"]')).toHaveText(DEATH_LINE);
    await expect(page.getByText('Ой, ошибка')).toHaveCount(0);
    expect((await readState(page)).alive, 'he should still be dead').toBe(false);
  } finally {
    await context.close();
  }
});

test('the health rate on the wire is the coupled one, not the catalogue rate', async ({
  browser,
  baseURL,
}) => {
  // The number this whole client change rests on. The bar creeps between fetches
  // by drawing a straight line from `rate_per_hour`, and the entire reason that
  // field exists is that hp's real drain is NOT its catalogue rate: it is one an
  // hour with both needs met and thirteen with neither. The browser is
  // deliberately incapable of working that out — it would need the thresholds,
  // the onset arithmetic and every driver's trajectory — so if the server ever
  // stopped computing it, the bars would silently understate how ill he is and
  // no stubbed test could notice.
  test.setTimeout(90_000);
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const seeded = await stack();
  forgetPet(seeded[PLAYER_A]);

  const context = await browser.newContext(PHONE);
  try {
    await loginAs(context, PLAYER_A);
    const page = await context.newPage();
    await page.goto(`${base}/app/game-vanyagotchi`);

    // A well-kept Ваня: both drivers comfortably clear of their thresholds.
    await readState(page);
    setStats(seeded[PLAYER_A], { hp: 60, beer: BEER_EMPTY_AT + 30, bladder: BLADDER_FULL_AT - 30 });
    const healthy = await readState(page);
    expect(wireStat(healthy, 'hp').rate_per_hour, 'a healthy Ваня should rot at his own rate').toBe(
      HP_DECAY_PER_HOUR,
    );
    // The drivers are never themselves penalised — the dependency graph is one
    // layer deep on purpose — so their rates are the catalogue's, sign and all.
    expect(wireStat(healthy, 'beer').rate_per_hour).toBeGreaterThan(0);
    expect(wireStat(healthy, 'bladder').rate_per_hour, 'the bladder should FILL').toBeLessThan(0);

    // One need unmet: the beer has run dry.
    setStats(seeded[PLAYER_A], { hp: 60, beer: 1, bladder: BLADDER_FULL_AT - 30 });
    expect(wireStat(await readState(page), 'hp').rate_per_hour, 'an empty beer should cost him').toBe(
      HP_DECAY_PER_HOUR + HP_PENALTY_PER_HOUR,
    );

    // Both unmet: dry AND desperate. This is the state that kills a full Ваня in
    // under eight hours, and it is the whole causal story of the game.
    setStats(seeded[PLAYER_A], { hp: 60, beer: 1, bladder: 99 });
    const suffering = await readState(page);
    expect(
      wireStat(suffering, 'hp').rate_per_hour,
      'two unmet needs should stack on his health',
    ).toBe(HP_DECAY_PER_HOUR + 2 * HP_PENALTY_PER_HOUR);
    // And the penalties themselves are on the wire as content, so a screen could
    // one day explain WHY the bar is falling that fast.
    const cfg = (await (await page.request.get(CONFIG_URL)).json()) as {
      stats: { key: string; penalties?: { when_key: string; rate_per_hour: number }[] }[];
    };
    const hp = cfg.stats.find((s) => s.key === 'hp');
    expect(hp?.penalties?.map((p) => p.when_key).sort()).toEqual(['beer', 'bladder']);
  } finally {
    await context.close();
  }
});

test('two accounts have two Ваняs, and an action on one does not touch the other', async ({
  browser,
  baseURL,
}) => {
  // One pet per account is a partial unique index in the schema, and the read
  // path is scoped by the caller's own account id. Both are invisible to a
  // single-player test — a service that ignored the account entirely and kept
  // one global pet would pass every other test in this file.
  test.setTimeout(90_000);
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const seeded = await stack();
  forgetPet(seeded[PLAYER_A]);
  forgetPet(seeded[PLAYER_B]);

  const ctxA = await browser.newContext(PHONE);
  const ctxB = await browser.newContext(PHONE);
  try {
    await loginAs(ctxA, PLAYER_A);
    await loginAs(ctxB, PLAYER_B);
    const pageA = await ctxA.newPage();
    const pageB = await ctxB.newPage();
    await pageA.goto(`${base}/app/game-vanyagotchi`);
    await pageB.goto(`${base}/app/game-vanyagotchi`);

    // Two pets, and two DIFFERENT starting numbers, so a shared row would show
    // up as the wrong value rather than as a coincidence.
    const stateA = await readState(pageA);
    const stateB = await readState(pageB);
    const petA = (stateA.pet as { id: string }).id;
    const petB = (stateB.pet as { id: string }).id;
    expect(petA, 'two accounts were handed the same pet').not.toBe(petB);

    setStats(seeded[PLAYER_A], { hp: 20, beer: 30, bladder: 10 });
    setStats(seeded[PLAYER_B], { hp: 40, beer: 30, bladder: 10 });

    await enterYardFrom(pageA);
    await enterYardFrom(pageB);
    await expect.poll(() => shownHp(pageA)).toBeLessThanOrEqual(21);
    await expect.poll(() => shownHp(pageB)).toBeLessThanOrEqual(41);

    await actionBtn(pageA, 'drink').click();
    await expect(pageA.locator('[data-test="pet-line"]')).toHaveText('хорошо пошло');
    expect(await shownHp(pageA)).toBeGreaterThanOrEqual(20 + DRINK_HP - 2);

    // B's Ваня is untouched — re-read from the server rather than trusting the
    // screen B has been holding, so this is about the database and not about
    // whether a component happened to re-render.
    await pageB.reload();
    await enterYardFrom(pageB);
    const hpB = await shownHp(pageB);
    expect(hpB, `B's hp moved when A drank: ${hpB}`).toBeLessThanOrEqual(41);
    expect(hpB, `B's hp was lost entirely: ${hpB}`).toBeGreaterThanOrEqual(37);
  } finally {
    await ctxA.close();
    await ctxB.close();
  }
});

test('reading the state twice returns the same pet, and a server clock with it', async ({
  browser,
  baseURL,
}) => {
  // GET /state is a read that writes: it creates the pet on first sight. That
  // makes idempotency a real question rather than a pedantic one — a second read
  // that made a second pet would be a new Ваня every page load, and the partial
  // unique index plus ON CONFLICT DO NOTHING is what stops it.
  //
  // `server_now` matters for a reason the UI makes visible: the bars are
  // interpolated between fetches from the SERVER's clock, so a phone that is
  // three minutes fast would otherwise draw three minutes of decay that has not
  // happened.
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const seeded = await stack();
  forgetPet(seeded[PLAYER_A]);

  const context = await browser.newContext(PHONE);
  try {
    await loginAs(context, PLAYER_A);
    const page = await context.newPage();
    await page.goto(`${base}/app/game-vanyagotchi`);

    const first = await readState(page);
    const second = await readState(page);

    const firstPet = first.pet as { id: string };
    const secondPet = second.pet as { id: string };
    expect(firstPet.id).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i,
    );
    expect(secondPet.id, 'a second read created a second Ваня').toBe(firstPet.id);
    // And the database agrees there is exactly one.
    expect(
      psql(
        `SELECT count(*) FROM game_vanyagotchi_pets ` +
          `WHERE account_id = '${uuid(seeded[PLAYER_A].account_id)}' AND deleted_at IS NULL`,
      ),
      'more than one living pet for one account',
    ).toBe('1');

    for (const state of [first, second]) {
      const now = Date.parse(String(state.server_now));
      expect(Number.isNaN(now), `server_now is not a timestamp: ${state.server_now}`).toBe(false);
      // Within a few minutes of this machine's clock — enough to catch a zero
      // value or a wrong unit, loose enough not to care about skew.
      expect(Math.abs(now - Date.now())).toBeLessThan(5 * 60_000);
      expect(state.alive).toBe(true);
    }

    // The catalogue's stats all came back, keyed, each with the rate it is
    // actually under — this is the contract the client resolves its bars
    // against, and every field of it is load-bearing.
    const keys = (first.stats as { key: string }[]).map((s) => s.key).sort();
    expect(keys).toEqual(['beer', 'bladder', 'hp']);
    for (const key of keys) {
      const stat = wireStat(first, key);
      expect(Number.isFinite(stat.rate_per_hour), `${key} has no usable rate`).toBe(true);
      expect(Number.isNaN(Date.parse(stat.as_of)), `${key} has no usable as_of`).toBe(false);
    }

    // And the catalogue itself is served, from the same session — in display
    // order, which is content too.
    const cfg = await page.request.get(CONFIG_URL);
    expect(cfg.status()).toBe(200);
    const parsed = (await cfg.json()) as {
      stats: { key: string }[];
      actions: { key: string; effects: { stat_key: string; delta: number }[] }[];
    };
    expect(parsed.stats.map((s) => s.key)).toEqual(['hp', 'beer', 'bladder']);
    expect(parsed.actions.map((a) => a.key)).toEqual(['drink', 'relieve']);
    // An action carries a LIST of effects now, and drinking is why: one press
    // moves three stats.
    expect(parsed.actions[0].effects.map((e) => e.stat_key).sort()).toEqual([
      'beer',
      'bladder',
      'hp',
    ]);

    // Both action routes the buttons post to are real, and answer with a state.
    for (const url of [DRINK_URL, RELIEVE_URL]) {
      const acted = await page.request.post(url);
      expect(acted.status(), `POST ${url}`).toBe(200);
      expect(((await acted.json()) as { pet: { id: string } }).pet.id).toBe(firstPet.id);
    }
  } finally {
    await context.close();
  }
});
