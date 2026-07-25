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
const HP_MAX = 100;
const HP_START = 65;
const HEAL_DELTA = 35;

// ---------------------------------------------------------------------------
// Direct database setup.
//
// Needed because of a real property of the shipped catalogue rather than for
// convenience: hp drains at three points an hour, so no amount of waiting
// produces an observable change inside a test's lifetime. A fresh pet does now
// start below its maximum — that was changed precisely so the first press of the
// only action is not a clamped no-op — but proving a value was WRITTEN, STORED
// and READ BACK across a reload needs a starting point far enough from the
// ceiling that the delta cannot be swallowed by clamping. So the row gets a low
// value first and the round trip is driven from there.
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

/** Writes a stat straight into the row the server decays from. */
function setStat(account: SeededAccount, key: string, value: number): void {
  const id = uuid(account.account_id);
  const updated = psql(
    `UPDATE game_vanyagotchi_pet_stats SET value = ${value}, as_of = now(), updated_at = now() ` +
      `WHERE stat_key = '${key}' AND pet_id IN ` +
      `(SELECT id FROM game_vanyagotchi_pets WHERE account_id = '${id}' AND deleted_at IS NULL) ` +
      `RETURNING pet_id`,
  );
  expect(updated, `no ${key} row to set — was the pet created first?`).not.toBe('');
}

// ---------------------------------------------------------------------------

const CONFIG_URL = '/api/game-vanyagotchi/config';
const STATE_URL = '/api/game-vanyagotchi/state';
const HEAL_URL = '/api/game-vanyagotchi/actions/heal';

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

/** The hp number as the screen is currently showing it. */
async function shownHp(page: Page): Promise<number> {
  const text = await page.locator('[data-test="stat-value-hp"]').textContent();
  return Number.parseInt((text ?? '').trim(), 10);
}

/** Creates the pet the way the app does — a plain read — and returns the state. */
async function readState(page: Page): Promise<Record<string, unknown>> {
  const res = await page.request.get(STATE_URL);
  expect(res.status(), `GET ${STATE_URL}`).toBe(200);
  return (await res.json()) as Record<string, unknown>;
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

    // Both catalogue stats, rendered from the config the server served — no
    // fixture anywhere in this file told the client what a stat is called.
    await expect(page.locator('[data-test="pet-stats"]')).toBeVisible();
    await expect(page.locator('[data-test="stat-hp"]')).toBeVisible();
    await expect(page.locator('[data-test="stat-bladder"]')).toBeVisible();
    await expect(page.locator('[data-test="action-heal"]')).toBeVisible();

    // A new Ваня starts BELOW the maximum — deliberately, so that the first
    // press of the only action there is has something to do — and has had no
    // time to decay from it yet.
    await expect.poll(() => shownHp(page)).toBeGreaterThanOrEqual(HP_START - 5);
    await expect.poll(() => shownHp(page)).toBeLessThanOrEqual(HP_START);
    expect(HP_START + HEAL_DELTA).toBeLessThanOrEqual(HP_MAX);
    await expect(page.locator('[data-test="stat-value-bladder"]')).toHaveText('0');
    // Alive, so no death line.
    await expect(page.locator('[data-test="pet-line"]')).toHaveText('');
  } finally {
    await context.close();
  }
});

test('the pet survives a reload — the first thing in this game that does', async ({
  browser,
  baseURL,
}) => {
  // THE point of the iteration. Everything this game had before now lived in the
  // server's memory and was gone the moment the page was: a reload put you back
  // in the middle of the yard with nothing to show for the session. A number
  // that is still there after a full page load is a different kind of thing, and
  // it can only be proved against a real database.
  test.setTimeout(90_000);
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const seeded = await stack();
  forgetPet(seeded[PLAYER_A]);

  const context = await browser.newContext(PHONE);
  try {
    await loginAs(context, PLAYER_A);
    const page = await context.newPage();
    await page.goto(`${base}/app/game-vanyagotchi`);

    // Create the pet through the real endpoint, then put him in a state an
    // action can visibly improve — see the note on direct setup above: a pet at
    // full health cannot demonstrate a heal, because +35 clamps to nothing.
    await readState(page);
    setStat(seeded[PLAYER_A], 'hp', 20);

    await enterYardFrom(page);
    await expect.poll(() => shownHp(page)).toBeLessThanOrEqual(21);

    await page.locator('[data-test="action-heal"]').click();
    await expect(page.locator('[data-test="pet-line"]')).toHaveText('полегчало');
    const healed = await shownHp(page);
    // 20 + 35, computed by the server and sent back — the client never says
    // what the value should become.
    expect(healed, `hp after healing: ${healed}`).toBeGreaterThanOrEqual(20 + HEAL_DELTA - 2);

    // A full page load: new document, new socket, new everything. The only place
    // the number can come back from is Postgres.
    await page.reload();
    await enterYardFrom(page);

    await expect
      .poll(() => shownHp(page), {
        message: 'the healed value did not survive the reload',
        timeout: 15_000,
      })
      // A range rather than the exact number, because hp decays while the test
      // runs — three points an hour, so a couple of points of slack is far more
      // than a test's lifetime and still nowhere near the 20 it started at.
      .toBeGreaterThanOrEqual(healed - 3);
    expect(await shownHp(page), 'hp climbed on its own across a reload').toBeLessThanOrEqual(
      healed + 1,
    );
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

    setStat(seeded[PLAYER_A], 'hp', 20);
    setStat(seeded[PLAYER_B], 'hp', 40);

    await enterYardFrom(pageA);
    await enterYardFrom(pageB);
    await expect.poll(() => shownHp(pageA)).toBeLessThanOrEqual(21);
    await expect.poll(() => shownHp(pageB)).toBeLessThanOrEqual(41);

    await pageA.locator('[data-test="action-heal"]').click();
    await expect(pageA.locator('[data-test="pet-line"]')).toHaveText('полегчало');
    expect(await shownHp(pageA)).toBeGreaterThanOrEqual(20 + HEAL_DELTA - 2);

    // B's Ваня is untouched — re-read from the server rather than trusting the
    // screen B has been holding, so this is about the database and not about
    // whether a component happened to re-render.
    await pageB.reload();
    await enterYardFrom(pageB);
    const hpB = await shownHp(pageB);
    expect(hpB, `B's hp moved when A healed: ${hpB}`).toBeLessThanOrEqual(41);
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

    // The catalogue's stats all came back, keyed — this is the contract the
    // client resolves its bars against.
    const keys = (first.stats as { key: string }[]).map((s) => s.key).sort();
    expect(keys).toEqual(['bladder', 'hp']);

    // And the catalogue itself is served, from the same session.
    const cfg = await page.request.get(CONFIG_URL);
    expect(cfg.status()).toBe(200);
    const parsed = (await cfg.json()) as { stats: { key: string }[]; actions: { key: string }[] };
    expect(parsed.stats.map((s) => s.key)).toEqual(['hp', 'bladder']);
    expect(parsed.actions.map((a) => a.key)).toEqual(['heal']);

    // The action route the buttons post to is real, and answers with a state.
    const acted = await page.request.post(HEAL_URL);
    expect(acted.status()).toBe(200);
    expect(((await acted.json()) as { pet: { id: string } }).pet.id).toBe(firstPet.id);
  } finally {
    await context.close();
  }
});
