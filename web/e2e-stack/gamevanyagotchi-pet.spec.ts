import { execFileSync } from 'node:child_process';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test, type BrowserContext, type Page } from '@playwright/test';
import { loginAs, stack, type SeededAccount, type SeededKind } from './fixtures';
import { forgetWorldObjects, psql, uuid } from './vanyagotchi-db';

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
// A clean yard before every test. Objects are durable, shared and drawn as
// ordinary entities, so a deposit one test leaves behind is an extra thing on
// the plane that the next test's counts have no idea about — and it lasts ten
// minutes, which is longer than the whole suite.
test.beforeEach(() => {
  forgetWorldObjects();
});

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

/**
 * Takes the server away and gives it back, exactly as a deploy does.
 *
 * Copied from the sibling spec rather than imported: a game's fixtures are its
 * own, so deleting «Ванягоччи» stays a matter of deleting its files.
 *
 * Needed here for a reason `forgetPet` cannot cover. Deleting a pet's row does
 * not delete the SERVER's idea of where that account is standing — a placement
 * lives in memory, and since the yard started keeping absent Ваняs lying about
 * in it, a placement is no longer evicted when the reconnect grace runs out. So
 * an account whose row has been deleted still gets drawn wherever this process
 * last saw it, forever. A restart is the only thing that makes "has never stood
 * anywhere" true of both layers at once.
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

// ---------------------------------------------------------------------------

// The two routes this game has. There is deliberately no third: a verb travels
// over the socket as a `vanyagotchi_do` frame and is answered with STATE, so
// there is no action URL to name here and nothing in this file may post one.
const CONFIG_URL = '/api/game-vanyagotchi/config';
const STATE_URL = '/api/game-vanyagotchi/state';

/** The death notice, which is the screen's own line rather than the catalogue's. */
// What the DEATH SCREEN says. It used to be a line in the status row telling him
// to press a button; being dead is a screen now, and the screen carries the
// instruction as its only control rather than as a sentence.
const DEATH_LINE = 'Ваня не выдержал';
const deathScreen = (page: Page) => page.locator('[data-test="death"]');

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

/**
 * Makes the plane re-read a pet that was changed behind its back.
 *
 * Needed because of a deliberate performance property rather than a shortcut:
 * the 5 Hz broadcast is a render step that touches no database, so what it draws
 * comes from an in-memory cache filled on the two occasions a human causes — a
 * client saying hello on a fresh socket, and that account pressing a verb. A row
 * changed by this file's direct-DB setup is neither, so nothing would ever
 * notice it.
 *
 * A reload rather than an action, because an action would also change the very
 * stats being set up. It is also what a real player does constantly: a reload,
 * a lock screen, a tunnel — every one of them is a fresh socket and a fresh
 * hello.
 */
async function refreshTheYardsIdeaOf(page: Page): Promise<void> {
  await page.reload();
  await enterYardFrom(page);
}

/**
 * Everything the plane is drawing, which is NOT the same as the people in it.
 *
 * The roster carries the yard's two regulars — characters with no accounts,
 * evaluated closed-form on every tick — and everybody who is asleep in it, an
 * account whose owner is away lying where he last stood. Both are entities to
 * draw and neither is a player, so `PLAYER_ONLY` below is what a test means when
 * it says "the Ваняs who are here", and the head count comes off the wire.
 */
const dots = (page: Page) => page.locator('[data-test="peer"]');

/**
 * Everybody who is actually here: not a regular, and not asleep.
 *
 * A regular is recognised by the `npc-` id prefix the server mints for it, which
 * is the only handle it can have — it has no account, so no pseudonym and no
 * row. Written as one selector string because `positions` below has to hand the
 * same rule to `querySelectorAll` inside the page.
 */
const PLAYER_ONLY =
  '[data-test="peer"]:not([data-peer^="npc-"]):not([data-peer^="obj-"]):not(:has([data-test="peer-face"][data-condition="asleep"]))';

/** The caller's own dot, and everybody else's — the plane marks which is which. */
const yourDot = (page: Page) => page.locator('[data-test="peer"][data-you="1"]');
const playerDots = (page: Page) => page.locator(PLAYER_ONLY);
/**
 * The OTHER player's dot.
 *
 * "Not me" stopped being enough to find him: it now also matches the two
 * regulars and anybody asleep in the yard, so this narrows to the entities that
 * are people who are present.
 */
const theirDot = (page: Page) => playerDots(page).and(page.locator(':not([data-you="1"])'));
/** A dot's face, which now carries the condition the SERVER decided. */
const faceOf = (dot: ReturnType<typeof dots>) => dot.locator('[data-test="peer-face"]');

/** Every PLAYER's normalised position, read from the custom properties. Copied. */
async function positions(page: Page): Promise<{ x: number; y: number }[]> {
  return page.evaluate((selector) =>
    [...document.querySelectorAll<HTMLElement>(selector)].map((el) => {
      const style = getComputedStyle(el);
      return {
        x: Number.parseFloat(style.getPropertyValue('--x')),
        y: Number.parseFloat(style.getPropertyValue('--y')),
      };
    }),
  PLAYER_ONLY);
}

/** A stat's number as the screen is currently showing it. */
async function shown(page: Page, key: string): Promise<number> {
  const text = await page.locator(`[data-test="stat-value-${key}"]`).textContent();
  return Number.parseInt((text ?? '').trim(), 10);
}

const shownHp = (page: Page) => shown(page, 'hp');
const actionBtn = (page: Page, key: string) => page.locator(`[data-test="action-${key}"]`);

/** The crate, which is a control now as well as a picture. */
const crate = (page: Page) => page.locator('[data-test="shop"]');

/**
 * The thing you press to fire a verb, which is no longer always a button.
 *
 * Three of the four verbs left the controls: drinking is a tap on the crate,
 * searching is a tap on a hiding place, and reviving is a button on the screen a
 * death puts you on. The last of those still answers to `action-<key>`, because
 * the death screen's own CTA carries it; drinking answers to nothing of the sort,
 * because the crate is a picture of a crate.
 *
 * IT NAMES A KEY, WHICH THE CLIENT MAY NOT AND A TEST MAY. The SPA works out
 * which verb belongs to the crate from the catalogue's shape, precisely so a
 * second gated verb needs no deploy; this file is allowed to know which game it
 * is testing, and pretending otherwise would mean re-deriving the mapping here
 * from the same config the client reads — which would agree with the client by
 * construction and could never catch it being wrong.
 */
function control(page: Page, key: string) {
  return key === 'drink' ? crate(page) : actionBtn(page, key);
}

/**
 * Presses a verb until it actually comes off, and returns once it has.
 *
 * «покакать» carries a FailChance, so a share of presses are refused outright:
 * nothing is written — no stat, no event, no deposit, no tally — and instead of
 * the verb's own «полегчало» he says one of the catalogue's lines about backing
 * out. Re-pressing is exactly what a player does, and it is safe precisely
 * because the refusal is total: the bladder is as full as it was, so the next
 * press asks the identical question.
 *
 * It waits for the balloon to CLEAR before pressing again rather than sleeping a
 * fixed amount. That is the honest synchronisation — the line expires by
 * arithmetic against the server's clock — and it also spends more than the one
 * second the server bounds a verb to, so a retry is never dropped in silence for
 * being too quick.
 *
 * Scoped to HIS OWN Ваня, because the yard's regulars mutter to themselves and a
 * bare balloon locator would sooner or later read an NPC's line instead.
 */
async function pressUntilItLands(page: Page, key: string, done: string): Promise<void> {
  // EIGHT ATTEMPTS AND A SHORTER WINDOW EACH, which is a budget rather than a
  // patience setting. A quarter of presses are refused, so eight is one failure
  // in sixty-five thousand runs — and twelve attempts at fifteen seconds each is
  // over three minutes, which is longer than the test is allowed to take, so the
  // old numbers turned an unlucky run into a timeout with no diagnosis rather
  // than into the loud throw below.
  const say = yourDot(page).locator('[data-test="peer-say"]');
  for (let attempt = 0; attempt < 8; attempt += 1) {
    await control(page, key).click();
    // The server says SOMETHING about every press — the confirmation, or the
    // line he backs out with.
    await expect(say).toHaveCount(1, { timeout: 8_000 });
    if (((await say.first().textContent()) ?? '').trim() === done) return;
    await expect(say, 'the balloon never cleared, so the next press cannot be read').toHaveCount(0, {
      timeout: 8_000,
    });
  }
  throw new Error(`«${key}» never came off in 8 presses`);
}

/** The one line the status row says about the beer store, or nothing at all. */
const storeLine = (page: Page) => page.locator('[data-test="store"]');

/**
 * How many drinks the status row says are left in the crate.
 *
 * NaN when the row says something with no number in it — «ящик пуст» — or when
 * there is no store line at all, which is a yard with no crate in it. Both are
 * real states rather than parse failures, so they are reported as a value a
 * comparison will simply fail against rather than as a throw inside a poll.
 */
async function storeLeft(page: Page): Promise<number> {
  if ((await storeLine(page).count()) === 0) return Number.NaN;
  const text = (await storeLine(page).textContent()) ?? '';
  const found = text.match(/(\d+)/);
  return found ? Number(found[1]) : Number.NaN;
}

// ---------------------------------------------------------------------------
// Walking to the beer.
//
// Drinking stopped being something he can do from wherever he is standing.
// «Выпить пива» carries a `needs_near` for the crate, the crate stands at a
// fixed pitch, and the server refuses the verb from further away than
// `arrive_within`. Three tests below used to press the button from the spawn
// point and now have to walk first — which is the shape of the iteration rather
// than an inconvenience of the harness, and it is why these helpers exist.
//
// COPIED from the sibling full-stack spec rather than imported from it, for the
// reason that file's own header gives: a game's fixtures are its own, so the two
// specs deliberately duplicate and deleting «Ванягоччи» stays a matter of
// deleting its files.
// ---------------------------------------------------------------------------

/**
 * Turns a normalised target into a click on the plane. Copied from the sibling
 * spec, where a tap is what every movement test is made of.
 */
async function tapAt(page: Page, x: number, y: number): Promise<void> {
  const box = await page.locator('[data-test="plane"]').boundingBox();
  expect(box, 'the plane has no box to tap').not.toBeNull();
  await page.mouse.click(
    (box?.x ?? 0) + (box?.width ?? 0) * x,
    (box?.y ?? 0) + (box?.height ?? 0) * y,
  );
}

/**
 * The caller's OWN dot, normalised, read off the custom properties.
 *
 * Read from the properties rather than from a bounding box because that is where
 * a position lives and nowhere else — a measured pixel offset would also be
 * measuring the plane's size, its border radius and whatever the CSS transition
 * happened to be doing at that instant. NaN rather than a throw when the dot is
 * not there, so a caller inside `expect.poll` retries across a reconnect instead
 * of failing on it. Copied from the sibling spec.
 */
async function you(page: Page): Promise<{ x: number; y: number }> {
  return page.evaluate(() => {
    const el = document.querySelector<HTMLElement>('[data-test="peer"][data-you="1"]');
    if (!el) return { x: Number.NaN, y: Number.NaN };
    const style = getComputedStyle(el);
    return {
      x: Number.parseFloat(style.getPropertyValue('--x')),
      y: Number.parseFloat(style.getPropertyValue('--y')),
    };
  });
}

/**
 * Where the crate is standing RIGHT NOW, read off the yard.
 *
 * IT USED TO BE A CONSTANT MIRRORED FROM THE CATALOGUE, because the crate was
 * pinned to one pitch in двор and `ObjectKind.At` is `json:"-"`. Both halves of
 * that are gone: the crate is hidden in a random location the way a fresh key
 * is, so there is no fixed pitch to mirror, and the frame's `store` block is now
 * the only statement of where it is.
 *
 * Read off the ELEMENT rather than off the frame, because the element is what a
 * player can see and tap — a test that fetched the roster itself would agree
 * with the server about a crate the screen might be drawing somewhere else. The
 * custom properties are the same ones the stylesheet maps to pixels, so this is
 * the coordinate the yard is actually using.
 */
async function cratePitch(page: Page): Promise<{ x: number; y: number }> {
  return page.evaluate(() => {
    const el = document.querySelector('[data-test="shop"]') as HTMLElement | null;
    if (!el) throw new Error('there is no crate in this place to walk to');
    return {
      x: Number(el.style.getPropertyValue('--x')),
      y: Number(el.style.getPropertyValue('--y')),
    };
  });
}

/**
 * Somewhere emphatically not the crate: the far corner, about 0.95 plane-widths
 * from it and therefore some eight times the reach threshold.
 */
const ACROSS_THE_YARD = { x: 0.12, y: 0.86 };

/**
 * Walks his Ваня over to the beer and waits until the drink is pressable.
 *
 * IT WAITS ON THE BUTTON RATHER THAN ON A COORDINATE, and that is the point of
 * the helper rather than a shortcut. What every caller below actually wants is a
 * control it can click, and the button is enabled by exactly the arithmetic the
 * server will redo when the verb arrives — his own position against the store's,
 * compared with the served `arrive_within`. Waiting on the position instead
 * would be asserting the gate here, inside a fixture, and a test that merely
 * wanted to press a button would silently become a test of it.
 *
 * The retry is what tolerates the tiredness roll: a walk that ends in «устал»
 * only means asking again from where he sat down, and each attempt starts closer
 * than the last. It is robustness rather than an expectation — the crate stands
 * about 0.425 plane-widths from the spawn, which is INSIDE `tiredFrom` (0.45),
 * so a Ваня who has just arrived can always reach the beer in one tap and is
 * never stuck between the door and a drink. That is a deliberate property of
 * where the crate was placed (see the note over `cratePlace` in content.go); the
 * loop is here for the other case, a Ваня an earlier test walked into a corner.
 */
async function walkToTheCrate(page: Page): Promise<void> {
  // TRAVELLING IS PART OF WALKING TO IT NOW. The crate used to be pinned to
  // двор, so "walk to the crate" was always a walk; it is hidden in a random
  // location on every restock, so from here it may be four places away and there
  // is no crate on this plane to tap at all. Folded in rather than left to each
  // caller, because a caller that forgot would fail with "there is no crate in
  // this place" — which is true, unhelpful, and nothing to do with what it was
  // testing.
  // MORE ATTEMPTS AND SHORTER ONES THAN IT USED TO NEED, and the crate moving is
  // why. It used to stand 0.425 from the yard's spawn — inside `tiredFrom`, so a
  // walk to it never rolled a give-up and one tap always arrived. It is hidden at
  // a hotspot in a random location now, which can be most of a plane from that
  // location's entry, so giving up part way is the ORDINARY case rather than the
  // exceptional one. Each attempt starts from where he sat down, so the distance
  // left shrinks every time and is soon under the threshold at which he never
  // gives up at all — but that convergence needs room to happen in.
  //
  // AND THE LOCATION IS RE-CHECKED EVERY TIME ROUND, not once at the top. This
  // suite shares one world and one process: another test finishing its last beer
  // restocks the crate somewhere new, and it can do that between two taps of this
  // loop. `travelToTheCrate` returns immediately when the crate is already on
  // screen, so the common case costs one DOM count.
  //
  // MANY SHORT ATTEMPTS RATHER THAN A FEW LONG ONES, which is also how a player
  // behaves. A tap re-targets the walk from wherever he has got to, so tapping
  // again after two seconds is not a wasted press — it starts a shorter walk from
  // closer in, and the sequence converges on the crate whether he is walking, has
  // sat down tired, or is chasing a crate another test has just moved. Waiting out
  // a long window per attempt instead spends the whole test budget discovering
  // that one walk did not finish.
  const REACHES = 15;
  for (let attempt = 0; attempt < REACHES; attempt += 1) {
    await travelToTheCrate(page);
    const pitch = await cratePitch(page).catch(() => undefined);
    if (!pitch) continue;
    await tapAt(page, pitch.x, pitch.y);
    try {
      // KEYED ON THE LINE RATHER THAN ON A BUTTON, because the button is gone.
      // «дойди» is what the readout says while the crate is out of reach, and its
      // absence is the same fact the greyed button used to carry — the client
      // measures its own distance to the crate either way.
      await expect(storeLine(page)).not.toContainText('дойди', { timeout: 2_000 });
      return;
    } catch {
      // He sat down part way and said so. Ask again from where he is now.
    }
  }
  const at = await you(page);
  const pitch = await cratePitch(page).catch(() => undefined);
  throw new Error(
    `he never got within reach of the crate in ${REACHES} taps — he is at ` +
      `${at.x.toFixed(3)},${at.y.toFixed(3)} and it is at ` +
      (pitch ? `${pitch.x.toFixed(3)},${pitch.y.toFixed(3)}` : 'no place he can see'),
  );
}

/**
 * Puts him in whatever LOCATION the crate is in, from wherever he has ended up.
 *
 * Needed twice over. A revival relocates him — he wakes in a place the server
 * chooses, which is the whole mechanic, since death costs you the walk you had
 * done — and the crate itself moves, so "the crate's own location" is a question
 * with a different answer every few beers.
 *
 * Where it is comes off the yard's readout rather than off the catalogue, which
 * no longer says: `store_location` is gone because a static field could only
 * ever have named the place the shop happened to start in. That is also how a
 * player finds out — see the note in the body.
 */
async function travelToTheCrate(page: Page): Promise<void> {
  const res = await page.request.get(CONFIG_URL);
  expect(res.status(), `GET ${CONFIG_URL}`).toBe(200);
  const cfg = (await res.json()) as { locations?: { key: string; label: string }[] };
  // THE CRATE BEING DRAWN IS THE SIGNAL, not a field and not a coordinate. The
  // client only draws a shop for a crate in the place it is looking at, so its
  // presence is exactly the question "is the beer here" — the same question a
  // player answers by looking. `store_location` used to answer it off the
  // catalogue and is gone, because a static field could only name the place the
  // shop happened to start in.
  //
  // LOOPED, because two things can be true on the way: the first roster may not
  // have arrived yet, in which case the readout says nothing at all and there is
  // nowhere to travel to; and the crate can be drained by something else between
  // the read and the arrival, which moves it again. Four attempts is a great deal
  // more than either needs.
  for (let attempt = 0; attempt < 3; attempt += 1) {
    if (await crate(page).count()) return;
    const line = (await storeLine(page).textContent()) ?? '';
    // When it is somewhere else the line is that place's own name and nothing
    // else, which is what a player is told too.
    const away = (cfg.locations ?? []).find((l) => !!l.label && line.includes(l.label));
    if (!away) {
      await page.waitForTimeout(500);
      continue;
    }
    try {
      await travelToPlace(page, away.key, away.label);
      // The journey is answered with state and the yard redraws on the next
      // frame, so the shop is not on screen the instant `travelToPlace` returns.
      // Eight seconds, not twenty. This runs inside the walk's own retry loop, so
      // a generous window here is multiplied by every attempt and eats the whole
      // test budget discovering the same thing four times over.
      await expect(crate(page), `the crate was not in ${away.label} after all`).toBeVisible({
        timeout: 8_000,
      });
      return;
    } catch {
      // The journey did not take, or the crate had moved again by the time he
      // arrived — somebody else's last beer restocks it somewhere new, and this
      // suite shares one world. Both are answered the same way: look again at
      // where the yard says the beer is now, and go there.
    }
  }
  throw new Error('the yard never said where the beer is, in three attempts');
}


/**
 * Puts him back where the beer is out of reach, and waits until the screen
 * agrees.
 *
 * Keyed on the button for the same reason as its opposite. Note that it returns
 * IMMEDIATELY when he is already out of reach — a Ваня at the spawn point is
 * 0.425 away from a crate he has to be within 0.12 of — so this is a statement
 * about where he ends up rather than a promise that he walked.
 */
async function walkAwayFromTheCrate(page: Page): Promise<void> {
  for (let attempt = 0; attempt < 4; attempt += 1) {
    await tapAt(page, ACROSS_THE_YARD.x, ACROSS_THE_YARD.y);
    try {
      await expect(storeLine(page)).toContainText('дойди', { timeout: 15_000 });
      return;
    } catch {
      // As above: he gave up part way, which is a shorter walk to ask for again.
    }
  }
  const at = await you(page);
  throw new Error(
    `he would not leave the crate — he is at ${at.x.toFixed(3)},${at.y.toFixed(3)} and the ` +
      'the readout still says he is at it',
  );
}

// ---------------------------------------------------------------------------
// The crate, as Postgres holds it.
//
// The screen is the wrong place to prove a draw HAPPENED. A count on a frame is
// the server's cache repeating itself five times a second, and a server that
// decremented a number in memory and never wrote it down would satisfy every
// assertion made through the browser. So the tests below read the row.
//
// Whitebox, and the project's rule is real flows or direct DB setup — the same
// reasoning `setStats` above gives for its own.
// ---------------------------------------------------------------------------

/** One world object of the stocked kind, as the table holds it. */
interface CrateRow {
  id: string;
  remaining: number;
  x: number;
  y: number;
  exhausted: boolean;
}

/** Every crate the world has ever stood up, oldest first, live or used up. */
function crates(kind: string): CrateRow[] {
  const out = psql(
    `SELECT id::text, remaining, x, y, exhausted_at IS NOT NULL ` +
      `FROM game_vanyagotchi_world_objects ` +
      `WHERE kind = '${kind}' AND deleted_at IS NULL ORDER BY created_at`,
  );
  if (!out) return [];
  return out.split('\n').map((line) => {
    const [id, remaining, x, y, exhausted] = line.split('|');
    return {
      id,
      remaining: Number.parseInt(remaining, 10),
      x: Number.parseFloat(x),
      y: Number.parseFloat(y),
      exhausted: exhausted === 't',
    };
  });
}

/** The ones still standing — at most one, as a partial unique index. */
const liveCrates = (kind: string) => crates(kind).filter((crate) => !crate.exhausted);

/** One world-object kind as the catalogue serves it. */
interface WireObjectKind {
  key: string;
  label?: string;
  stock?: number;
}

/**
 * The kind of thing the yard draws beer out of, asked of the server.
 *
 * FOUND BY THE FACT THAT IT CARRIES A STOCK rather than by its key, so nothing
 * in this file writes «beer_crate» or its six down: retuning `crateStock`, or
 * renaming the kind, moves what these tests expect with no edit here — the same
 * discipline the sibling spec follows for the size of the cast. The key it
 * returns is then what the SQL above names, which is the one place a kind value
 * legitimately appears: a row's `kind` column holds it.
 */
async function stockedKind(page: Page): Promise<Required<WireObjectKind>> {
  const res = await page.request.get(CONFIG_URL);
  expect(res.status(), `GET ${CONFIG_URL}`).toBe(200);
  const cfg = (await res.json()) as { object_kinds?: WireObjectKind[] };
  const found = (cfg.object_kinds ?? []).find((kind) => (kind.stock ?? 0) > 0);
  expect(found, 'the catalogue serves nothing with a stock in it').toBeDefined();
  const kind = found as WireObjectKind;
  return { key: kind.key, label: kind.label ?? '', stock: kind.stock as number };
}

/** How close «beside it» is, as the server serves it. Never written down here. */
async function arriveWithin(page: Page): Promise<number> {
  const res = await page.request.get(CONFIG_URL);
  expect(res.status(), `GET ${CONFIG_URL}`).toBe(200);
  const cfg = (await res.json()) as { arrive_within?: number };
  expect(
    typeof cfg.arrive_within === 'number' && cfg.arrive_within > 0,
    'the catalogue serves a gate without the threshold it turns on',
  ).toBe(true);
  return cfg.arrive_within as number;
}

/** Everything lying about the plane, normalised — the `obj-` entities only. */
async function objectsOnThePlane(page: Page): Promise<{ x: number; y: number }[]> {
  return page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>('[data-test="peer"][data-peer^="obj-"]')].map(
      (el) => {
        const style = getComputedStyle(el);
        return {
          x: Number.parseFloat(style.getPropertyValue('--x')),
          y: Number.parseFloat(style.getPropertyValue('--y')),
        };
      },
    ),
  );
}

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
    // The crate IS the drink control now, so what has to be on screen for the
    // two verbs is a box and a button rather than two buttons — and the box is
    // wherever the crate has wandered to rather than reliably in the yard he
    // starts in, which is why he goes and finds it first.
    await travelToTheCrate(page);
    await expect(crate(page)).toBeVisible();
    await expect(actionBtn(page, 'relieve')).toBeVisible();
    await expect(actionBtn(page, 'drink'), 'the drink is a button again').toHaveCount(0);

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
    await expect(page.locator('[data-test="pet-line"]')).toHaveCount(0);
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
  // client sends a verb and never a value, so every number below was computed
  // and stored by the server.
  // A BIGGER BUDGET THAN A PINNED CRATE NEEDED. The beer used to stand 0.425 from
  // the spawn, inside `tiredFrom`, so reaching it was one tap that never rolled a
  // give-up. It is hidden at a hotspot in a random location now, so getting to it
  // can be a journey plus several walks, each of which may end early on purpose.
  test.setTimeout(120_000);
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

    // TO THE CRATE FIRST. Beer comes out of a crate now, so a drink is refused
    // from anywhere but beside it — this used to be a press from wherever he
    // happened to be standing, and the walk is the only thing about this test
    // that changed. What it is still about is the three numbers below.
    await walkToTheCrate(page);

    await crate(page).click();
    await expect(page.locator('[data-test="peer-say"]')).toHaveText('хорошо пошло');

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
  // A LONGER BUDGET THAN THE DEFAULT, because this verb can be refused for
  // nerves and each retry waits for the balloon to expire before pressing again.
  // The common case is one press; an unlucky run is four or five, and at four
  // seconds a balloon that is half a minute before the assertion is even
  // reached. Not a hang — a tail.
  test.setTimeout(120_000);
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

    await pressUntilItLands(page, 'relieve', 'полегчало');

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

test('a dead Ваня is a screen with one control on it, and that control raises him', async ({
  browser,
  baseURL,
}) => {
  // The refusal path, and it is now the DEFAULT rather than the exception. Beer
  // used to bring him round, which made dying almost invisible — the verb you
  // were pressing anyway quietly undid it. Death has its own verb now, so every
  // other verb is refused on a corpse and «восстать из мертвых» is the only way
  // out.
  //
  // A refusal has no status code to assert any more, and that is the design
  // rather than a gap: the socket owes no reply, so what the player is owed is
  // knowing what happened — and he is told the way he is told everything else,
  // as STATE. So the whole of the contract is observable on screen, and that is
  // where it is asserted: the line that says what to do appears over his own
  // Ваня, the global "something went wrong" modal never opens over a situation
  // the game is already explaining in words, and the pet is still dead
  // afterwards. The `ErrPetDead` sentinel behind that line is pinned by the Go
  // tests, which own it (internal/gamevanyagotchi/pet_test.go, and
  // test/integration/gamevanyagotchi_pet_test.go drives it through Service.Do).
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
    // Into the yard BEFORE the pet is put on the floor, and that ordering is
    // load-bearing now. A verb is a socket send with no reply, so one issued by
    // an earlier test can still be in flight when this one starts — and it
    // landed on this pet, drinking it back off the floor between the setup and
    // the assertion. Entering first, then killing him, closes that window: any
    // straggler has already been applied by the time hp goes to zero.
    await enterYardFrom(page);

    // TO THE CRATE WHILE HE IS STILL ALIVE, and this is the interesting half of
    // the test rather than a step of setup.
    //
    // A drink is gated on arriving now, so a corpse standing across the yard has
    // a drink button that is greyed for DISTANCE — and a button nobody can press
    // proves nothing at all about what a dead Ваня refuses. Walking him over
    // first is what keeps the refusal below reachable, and it makes this test say
    // something it could not say before: it now pins the ORDER of the two
    // refusals. Being dead outranks being far away — the check sits immediately
    // after `apply`, ahead of nothing and behind the fatal check, deliberately —
    // so a corpse standing AT the crate answers «он не встаёт» and not
    // «далековато». That ordering is the one ADR-043 records as load-bearing,
    // because the two lines are opposite instructions: one tells him which button
    // to press instead, the other would send him walking with a corpse.
    await walkToTheCrate(page);

    // On the floor. The death is not written by this UPDATE — it is recorded by
    // the first READ that observes hp at zero, which is the lazy-materialisation
    // shape the rest of this game uses too.
    setStats(seeded[PLAYER_A], { hp: 0, beer: 5, bladder: 95 });

    const dead = await readState(page);
    expect(dead.alive, 'a Ваня at zero health was still reported alive').toBe(false);
    expect((dead.pet as { died_at: string | null }).died_at, 'no death was recorded').not.toBeNull();

    // On screen, where the death has to read without anybody parsing a
    // number. The page is already in the yard, so it is nudged into re-reading
    // the row this test changed behind its back.
    await refreshTheYardsIdeaOf(page);
    await expect(deathScreen(page)).toContainText(DEATH_LINE);

    // He is still standing at the crate, which the reload did not move him from:
    // a placement is in memory and is only replaced by a stored position when
    // the yard has none or has only the provisional spawn it invents for a
    // connection it has not been greeted by. So he is IN REACH over a corpse,
    // and the refusal about to arrive can only be about the death.
    await expect(
      storeLine(page),
      'he is out of reach, so the refusal below would be about distance rather than death',
    ).not.toContainText('дойди', { timeout: 20_000 });

    // A BEER CANNOT EVEN BE ASKED FOR, and that is the change. This used to press
    // the crate over the corpse and read «он не встаёт» off the balloon, proving
    // the server refuses the verb that used to be a death's way out. The death
    // screen covers the plane now and takes every pointer event, so there is no
    // longer a way to ask — which is a stronger version of the same rule, and the
    // one the player actually meets: while he is dead the only control that
    // exists is the one that raises him.
    //
    // That the SERVER refuses it regardless is owned by the Go tests, and has to
    // be: it is a rule about a batch reaching `Do`, and a browser that cannot
    // send one can say nothing about it either way.
    // Asserted with `elementFromPoint` rather than with visibility, because both
    // controls are still DRAWN — the yard is deliberately left visible behind the
    // sheet, so the others walking about in it are what makes coming back feel
    // like something. What has changed is that a finger landing on either of them
    // lands on the death screen instead, which is the only form of "you cannot
    // press this" a covering overlay produces.
    for (const [what, selector] of [
      ['the crate', '[data-test="shop"]'],
      ['a verb button', '.verbs .verb'],
    ] as const) {
      const blocked = await page.evaluate((sel) => {
        const el = document.querySelector(sel);
        if (!el) return true; // not drawn at all is also not pressable
        const box = el.getBoundingClientRect();
        const hit = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2);
        return !!hit?.closest('[data-test="death"]');
      }, selector);
      expect(blocked, `${what} can be pressed over a corpse`).toBe(true);
    }
    expect((await readState(page)).alive, 'he came round on his own').toBe(false);

    // And the revival is a RESET: he comes back as a new Ваня rather than as the
    // old one plus a number, so health is the catalogue's starting value and not
    // whatever a drink would have added to zero.
    await actionBtn(page, 'revive').click();
    await expect(page.locator('.peer--you [data-test="peer-say"]')).toHaveText('воскрес');
    await expect.poll(() => shownHp(page)).toBeGreaterThanOrEqual(HP_START - 2);

    const revived = await readState(page);
    expect(revived.alive, 'the revival did not bring him round').toBe(true);
    expect(
      (revived.pet as { died_at: string | null }).died_at,
      'the death was still recorded after a revival',
    ).toBeNull();

    // THE SECOND HALF OF THIS TEST IS GONE, AND ITS ABSENCE IS THE FEATURE.
    //
    // It used to kill him again behind the page's back and press a verb, to show
    // that a refusal reaches a player who has NOT re-read — as a line over his own
    // Ваня rather than as the global error modal. That scenario cannot be staged
    // through this UI any more, and the reason is the death screen doing its job:
    // every path that leaves a verb pressable also tells the client he is dead.
    // Reaching the crate can mean a journey, and a journey is answered with
    // `vanyagotchi_state`; the yard's own button is gated on a stat, so un-greying
    // it means re-reading — and either read records the death and raises the
    // screen over both controls. A player simply cannot press a verb on a Ваня his
    // own screen knows is a corpse.
    //
    // What that leg protected is not lost. That the SERVER refuses every verb but
    // the revival on a corpse is owned by the Go tests, which is where a rule about
    // a batch reaching `Do` belongs; and that a refusal arrives as a balloon rather
    // than as the error modal is pinned by the shy refusal in the stubbed suite,
    // which needs no corpse to produce one.
    //
    // What is worth keeping is that the screen goes and stays gone: a revival that
    // cleared the row but left the sheet up would be a Ваня who is alive on the
    // wire and dead on the screen.
    await refreshTheYardsIdeaOf(page);
    await expect(deathScreen(page)).toHaveCount(0);
    await expect(actionBtn(page, 'relieve')).toBeVisible();
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

    // A walks over to the beer; B does not move and does not drink. That the two
    // Ваняs are in different parts of the yard is incidental to the claim here —
    // one pet per account — but it is worth noticing that A crossing the yard is
    // the only thing that happens to B at all.
    await walkToTheCrate(pageA);

    await crate(pageA).click();
    // Scoped to A's OWN Ваня, and this is the one test in the file where that
    // matters: B is a second real player standing in the same yard, and a Ваня
    // who is standing still is entitled to mutter on a schedule of his own
    // (idleSays, one slot in four). An unscoped balloon locator therefore
    // resolves to two elements whenever B happens to be talking, which is a
    // failure about the yard being alive rather than about what A's verb did.
    await expect(pageA.locator('.peer--you [data-test="peer-say"]')).toHaveText('хорошо пошло');
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
    // Compared against the CATALOGUE the same server just served, not against a
    // list typed in here. The state and the config are two views of one set of
    // stats, and a hand-written list only ever agrees with them until somebody
    // adds a stat — which is exactly what it did, twice, the day the lifetime
    // tallies arrived.
    const served = (await (await page.request.get(CONFIG_URL)).json()) as {
      stats: { key: string }[];
    };
    const want = served.stats.map((s) => s.key).sort();
    expect(want.length, 'the served catalogue has no stats at all').toBeGreaterThan(0);
    const keys = (first.stats as { key: string }[]).map((s) => s.key).sort();
    expect(keys).toEqual(want);
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
    // The bars come first and in this order, which is display order and is
    // therefore content: health is the consequence and the two needs that drive
    // it follow. The lifetime tallies come after them — they are not bars and
    // the screen does not draw them as one — so this asserts the PREFIX rather
    // than the whole list, and the tallies are checked as tallies below.
    expect(parsed.stats.map((s) => s.key).slice(0, 3)).toEqual(['hp', 'beer', 'bladder']);
    // The tallies, whichever they are: named by the catalogue rather than by
    // this list, because a counter is added the day a verb exists that can move
    // one and a hand-written pair goes stale the moment that happens — which it
    // did, when finding the keys arrived.
    const tallies = parsed.stats.filter((s) => (s as { counter?: boolean }).counter);
    expect(tallies.length, 'the catalogue serves no lifetime tallies at all').toBeGreaterThan(0);
    for (const t of tallies) {
      expect(wireStat(first, t.key).value, `${t.key} is not on the pet`).toBeGreaterThanOrEqual(0);
    }
    // The revival comes last, and it is the only action that starts him over —
    // asserted here because this is the one place the REAL server's catalogue is
    // read over the wire, and a stub cannot notice the shipped one changing.
    // The two the pet loop is made of come first and in this order, because
    // display order is content: the drink that keeps him alive, then the thing
    // it makes necessary. Asserted as a PREFIX — verbs about the wider world
    // arrive after them and a hand-written full list goes stale the day one
    // does, which it already has twice.
    expect(parsed.actions.map((a) => a.key).slice(0, 2)).toEqual(['drink', 'relieve']);
    expect(parsed.actions.map((a) => a.key)).toContain('revive');
    // An action carries a LIST of effects, and drinking is why: one press moves
    // three stats and keeps a tally.
    expect(parsed.actions[0].effects.map((e) => e.stat_key).sort()).toEqual([
      'beer',
      'beers_drunk',
      'bladder',
      'hp',
    ]);
    // And there is no third route. The buttons post nothing at all — a verb is a
    // socket frame — so what a press does to the right pet is proved by pressing
    // it, in the tests above, and not by a request from here.
  } finally {
    await context.close();
  }
});

test("a neighbour sees how ill your Ваня really is, without being told", async ({
  browser,
  baseURL,
}) => {
  // THE claim this iteration makes, end to end: one shared world.
  //
  // Everything else in this file is a player looking at his own pet. Here the
  // pet's condition is changed for ONE account, in the database, and asserted on
  // the OTHER account's screen — a browser that never fetched that pet, is not
  // allowed to, and has no way of computing anything about it. The pose can only
  // have been derived on the server, from stats only the server can see, and
  // published to everybody in the roster.
  //
  // That is also why a pose is not worked out in the client even for your own
  // Ваня. A locally-derived condition is derived from numbers its owner alone
  // has, so a dying Ваня would look ill to himself and perfectly well to the
  // player standing next to him — two worlds wearing one plane.
  test.setTimeout(150_000);
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

    // Both pets exist and are comfortably well before anybody looks at anybody:
    // every stat clear of its threshold, so hp rots at its own single point an
    // hour and nothing drifts over a threshold while the test runs.
    await readState(pageA);
    await readState(pageB);
    const WELL = { hp: 60, beer: BEER_EMPTY_AT + 30, bladder: BLADDER_FULL_AT - 60 };
    setStats(seeded[PLAYER_A], WELL);
    setStats(seeded[PLAYER_B], WELL);

    await enterYardFrom(pageA);
    await enterYardFrom(pageB);
    // The PLAYERS, not the entities: the yard's regulars are drawn alongside
    // them and are not people, so counting everything on the plane would say
    // four here and would say something different again the day a character is
    // added.
    await expect(playerDots(pageB)).toHaveCount(2, { timeout: 15_000 });
    await expect(yourDot(pageB)).toHaveCount(1, { timeout: 15_000 });

    // B is watching A's Ваня. B never asked for it and could not be told: the
    // pet endpoints are scoped to the caller's own account.
    const watched = faceOf(theirDot(pageB));
    await expect(watched, "B's neighbour started out looking unwell").toHaveAttribute(
      'data-condition',
      'fine',
      { timeout: 20_000 },
    );

    // A's health falls into the range the CATALOGUE calls trouble — the same
    // threshold that turns his own bar amber, so a rough-looking Ваня and an
    // amber bar are one moment rather than two numbers that drift apart.
    setStats(seeded[PLAYER_A], { ...WELL, hp: 10 });
    await refreshTheYardsIdeaOf(pageA);

    await expect(watched, "A's decline never reached B's screen").toHaveAttribute(
      'data-condition',
      'poorly',
      { timeout: 30_000 },
    );

    // And all the way to the floor.
    setStats(seeded[PLAYER_A], { ...WELL, hp: 0 });
    await refreshTheYardsIdeaOf(pageA);

    await expect(watched, "A's death never reached B's screen").toHaveAttribute(
      'data-condition',
      'dead',
      { timeout: 30_000 },
    );

    // B's own Ваня was never touched. The yard is showing two different
    // conditions at once, which is the assertion that separates "everybody is
    // drawn from the server" from "the screen painted its own state on all of
    // them" — the shortcut that passes every single-player test in this file.
    await expect(faceOf(yourDot(pageB)), "B's own Ваня was buried along with A's").toHaveAttribute(
      'data-condition',
      'fine',
    );
    // A agrees about his own, from the other end of the same socket.
    await expect(faceOf(yourDot(pageA))).toHaveAttribute('data-condition', 'dead', {
      timeout: 20_000,
    });
  } finally {
    await ctxA.close();
    await ctxB.close();
  }
});

test('a Ваня who has never stood anywhere arrives in the middle of the yard', async ({
  browser,
  baseURL,
}) => {
  // A stored position is nullable and starts null, because there is no honest
  // value to invent for a pet that has never been in the yard: 0.5 written into
  // the row at creation would be a position the player never chose, and the
  // moment the spawn point moves every one of those rows is a lie.
  //
  // So "the middle" is the SERVER's spawn constant applied at render time, and
  // the check below that the columns really are null is what stops this test
  // passing for the wrong reason — with a default of 0.5 in the schema, a
  // completely broken restore path would still put him in the centre.
  test.setTimeout(120_000);
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const seeded = await stack();
  forgetPet(seeded[PLAYER_A]);
  // Deleting the row is only half of "never stood anywhere": the running server
  // still holds this account's last placement in memory, and a placement is no
  // longer evicted when the reconnect grace expires — it becomes the sleeper
  // lying in the yard instead. Without the restart this test asserts the spawn
  // point against a position an earlier test walked to, and the assertion that
  // the columns are NULL passes while the plane draws him somewhere else
  // entirely. Restarting is what makes both layers agree that he is new.
  restartServer();

  const context = await browser.newContext(PHONE);
  try {
    await loginAs(context, PLAYER_A);
    const page = await context.newPage();
    await page.goto(`${base}/app/game-vanyagotchi`);

    // The first read creates the pet — lazily, which is the storage design.
    await readState(page);
    expect(
      psql(
        `SELECT x IS NULL AND y IS NULL FROM game_vanyagotchi_pets ` +
          `WHERE account_id = '${uuid(seeded[PLAYER_A].account_id)}' AND deleted_at IS NULL`,
      ),
      'a freshly created pet already had a stored position',
    ).toBe('t');

    await enterYardFrom(page);
    await expect(playerDots(page)).toHaveCount(1, { timeout: 15_000 });

    // He is drawn, and drawn in the middle. Read off the custom properties,
    // which are written from the frame — an unset property parses as NaN, so
    // this also fails if no position ever reached the DOM at all. `positions`
    // returns the players only, so index 0 is him rather than whichever regular
    // the server happened to append first.
    await expect
      .poll(async () => (await positions(page))[0]?.x, {
        message: 'a pet with no stored position never arrived on the plane',
        timeout: 15_000,
      })
      .toBeCloseTo(0.5, 2);
    expect((await positions(page))[0]?.y, 'he arrived off-centre vertically').toBeCloseTo(0.5, 2);

    // And he is a real entity rather than a placeholder: he has a face, and the
    // yard counts him.
    await expect(faceOf(yourDot(page))).toHaveCount(1);
    // One PERSON in the yard, whatever else is drawn in it. The count is the
    // server's own — published precisely so this screen never has to work out
    // which entity is somebody you could talk to.
    await expect
      .poll(() => page.locator('[data-test="here"]').textContent())
      .toMatch(/\b1\b/);
  } finally {
    await context.close();
  }
});

// ---------------------------------------------------------------------------
// The beer store.
//
// What the stubbed suite next door cannot say. It pushes a `store` block down a
// fake socket and checks that the client greys a button and writes the right
// sentence, which is the client's whole job and is worth pinning there — but it
// would pass just as happily against a server that never stood a crate up, never
// drew one down, and never wrote a row. Every test below is about the half that
// only Postgres and the real binary can answer: the crate is a ROW, the draw is
// a conditional UPDATE that cannot oversell it, the crate that empties is
// replaced in the same transaction, and a yard with nothing in it grows one back
// the moment somebody says hello.
// ---------------------------------------------------------------------------

test('the beer is behind a walk, and the draw that follows it reaches Postgres', async ({
  browser,
  baseURL,
}) => {
  // THE claim of the iteration: distance stopped being decorative. A drink used
  // to be a button you pressed from wherever you were standing; it is now a
  // journey with a button at the end of it, and both ends of that rule are
  // checked here against the real thing — the client greys the control from the
  // frame's own numbers, the server refuses the verb from its own placement map,
  // and the two are the same threshold because both read `arrive_within` off one
  // catalogue.
  //
  // And then the draw. The count falling by one on screen is not enough on its
  // own — a server that decremented a number in its cache would produce exactly
  // that — so the row is read straight out of the table either side of the
  // press. Both, because either alone is satisfied by a plausible bug: the row
  // without the screen would be a draw nobody is told about, and the screen
  // without the row would be a draw that evaporates on the next restart.
  test.setTimeout(120_000);
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const seeded = await stack();
  forgetPet(seeded[PLAYER_A]);

  const context = await browser.newContext(PHONE);
  try {
    await loginAs(context, PLAYER_A);
    const page = await context.newPage();
    await page.goto(`${base}/app/game-vanyagotchi`);

    // Low enough that a drink is not a clamped no-op, and comfortably clear of
    // every threshold so nothing drifts while the walk happens.
    await readState(page);
    setStats(seeded[PLAYER_A], { hp: 30, beer: 30, bladder: 10 });

    const kind = await stockedKind(page);
    const within = await arriveWithin(page);

    // The yard was emptied by `beforeEach`, so this hello is what stands the
    // crate up — the same path the last test in this file is entirely about.
    await enterYardFrom(page);
    await expect(storeLine(page), 'the yard never grew a crate to walk to').toBeVisible({
      timeout: 20_000,
    });

    // ACROSS THE YARD. Asserted three ways, because each of them is a different
    // claim: the geometry (he really is further away than the gate allows), the
    // control (it is greyed), and the words (the row tells him to walk rather
    // than to wait, which is the difference between the two refusals).
    // He goes to WHEREVER the beer is first, because it is no longer reliably in
    // the place he starts in — and then the pitch is read off the yard rather
    // than from a constant, because the crate no longer has a fixed one. Read
    // before he walks away, since the readout stops naming a pitch he cannot see.
    await travelToTheCrate(page);
    const pitch = await cratePitch(page);
    await walkAwayFromTheCrate(page);
    const far = await you(page);
    expect(
      Math.hypot(far.x - pitch.x, far.y - pitch.y),
      `he is standing at ${far.x.toFixed(3)},${far.y.toFixed(3)}, which is within reach of the ` +
        'crate — the greyed button below would be greyed for some other reason',
    ).toBeGreaterThan(within);
    await expect(storeLine(page)).toContainText('дойди');
    await expect(storeLine(page)).toContainText('дойди');

    // The count on the frame and the count in the table are the same number, and
    // they are compared BEFORE anybody drinks — so the falling count below is a
    // change to one thing rather than two numbers happening to end up equal.
    const before = liveCrates(kind.key);
    expect(before, 'the yard is holding something other than exactly one crate').toHaveLength(1);
    expect(
      await storeLeft(page),
      'the status row and the row in the table disagree about how much beer there is',
    ).toBe(before[0].remaining);

    // He walks. Nothing about the world changed — same crate, same stock — so
    // his position is the only thing that can have lit the button.
    await walkToTheCrate(page);
    const near = await you(page);
    expect(
      Math.hypot(near.x - pitch.x, near.y - pitch.y),
      `the drink lit up while he was standing at ${near.x.toFixed(3)},${near.y.toFixed(3)}, ` +
        'which is not at the crate',
    ).toBeLessThanOrEqual(within);
    await expect(storeLine(page)).not.toContainText('дойди');
    await expect(storeLine(page)).not.toContainText('дойди');

    await crate(page).click();
    await expect(page.locator('.peer--you [data-test="peer-say"]')).toHaveText('хорошо пошло');

    // On screen…
    await expect
      .poll(() => storeLeft(page), {
        message: 'the yard never noticed a beer leaving the crate',
        timeout: 20_000,
      })
      .toBe(before[0].remaining - 1);
    // …and in the table, which is the half a stub cannot reach. The SAME row,
    // drawn down rather than replaced: a crate is used up gradually, and only the
    // draw that reaches nought stands a new one up.
    const after = liveCrates(kind.key);
    expect(after, 'the drink left the yard with the wrong number of crates in it').toHaveLength(1);
    expect(after[0].id, 'the crate was replaced rather than drawn from').toBe(before[0].id);
    expect(after[0].remaining, 'the draw never reached Postgres').toBe(before[0].remaining - 1);

    // And the beer he drew is in him, which is what the whole walk was for.
    expect(await shown(page, 'beer'), 'the drink moved the crate but not the Ваня').toBeGreaterThanOrEqual(
      30 + DRINK_BEER - 2,
    );
  } finally {
    await context.close();
  }
});

test('the last beer exhausts the crate, and a fresh one is standing somewhere already', async ({
  browser,
  baseURL,
}) => {
  // The only moment in the crate's life that has any mechanism in it. Five of
  // the six draws are a decrement; the sixth exhausts the row and inserts its
  // replacement IN THE SAME TRANSACTION, against a partial unique index that
  // permits exactly one live crate — which is what makes «пиво кончилось» a
  // moment rather than a state anybody has to be dug out of.
  //
  // THE STOCK IS SET LOW WITH SQL FIRST, deliberately, and it is worth saying
  // why rather than draining it through the buttons. A verb is rate-limited to
  // one a second per account, so six real draws is six seconds of pressing plus
  // six round trips — and five of them would be testing the decrement this
  // file's previous test already pins, at the cost of a test that fails whenever
  // the machine is slow. What is interesting is the LAST draw, so the fixture
  // puts the crate one beer from empty and the test presses once. Whitebox, and
  // the project's rule is real flows or direct DB setup.
  test.setTimeout(120_000);
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const seeded = await stack();
  forgetPet(seeded[PLAYER_A]);

  const context = await browser.newContext(PHONE);
  try {
    await loginAs(context, PLAYER_A);
    const page = await context.newPage();
    await page.goto(`${base}/app/game-vanyagotchi`);

    await readState(page);
    const kind = await stockedKind(page);

    await enterYardFrom(page);
    await expect(storeLine(page)).toBeVisible({ timeout: 20_000 });
    // TO WHEREVER IT IS, before anything is read off the readout. The line only
    // carries a COUNT for a crate in this place; for one somewhere else it is the
    // other place's name, which is what a player is told and what `storeLeft`
    // honestly reports as NaN.
    await travelToTheCrate(page);

    const opened = liveCrates(kind.key);
    expect(opened, 'the hello did not leave exactly one crate to drain').toHaveLength(1);
    expect(opened[0].remaining, 'a fresh crate did not come out full').toBe(kind.stock);

    psql(
      `UPDATE game_vanyagotchi_world_objects SET remaining = 1, updated_at = now() ` +
        `WHERE id = '${opened[0].id}'`,
    );
    // The running process is still holding the count it read at the hello — the
    // world cache is refreshed when a human causes it to be, never on the tick —
    // so a row changed behind its back is a row nothing would notice. A reload is
    // a fresh socket and therefore a fresh hello, which is the honest way to make
    // it look again; it is also what a player does constantly.
    await refreshTheYardsIdeaOf(page);
    await expect
      .poll(() => storeLeft(page), {
        message: 'the yard never re-read a crate that had been drained behind its back',
        timeout: 20_000,
      })
      .toBe(1);

    await walkToTheCrate(page);
    await crate(page).click();
    await expect(page.locator('.peer--you [data-test="peer-say"]')).toHaveText('хорошо пошло');

    // AND THE REPLACEMENT IS SOMEWHERE ELSE, most of the time. The crate used to
    // restock in the same square, and this test used to end by reading the fresh
    // count off the same readout; a replacement is hidden in a random location
    // now, so the yard he is standing in usually has no crate in it at all and
    // the count is read from the database instead. Where it went is the server's
    // own test's business.
    // The row he drank from is used up, and says so in the column both
    // disciplines share.
    await expect
      .poll(() => crates(kind.key).find((crate) => crate.id === opened[0].id)?.exhausted, {
        message: 'the last beer left the crate standing with nothing in it',
        timeout: 20_000,
      })
      .toBe(true);
    const drained = crates(kind.key).find((crate) => crate.id === opened[0].id) as CrateRow;
    expect(drained.remaining, 'the exhausted crate still has beer in it').toBe(0);

    // And there is a new one — somewhere. WHERE is the thing that changed: the
    // crate used to be pinned to one pitch in двор and its replacement stood in
    // exactly the same square, which is what this used to assert. It is hidden
    // the way a fresh key is now, so what is left to pin is that there is exactly
    // ONE of them, that it is a new row rather than the old one refilled, and
    // that it came out full. Where it went is the server's own test's business.
    const live = liveCrates(kind.key);
    expect(live, 'the yard was left without a beer store').toHaveLength(1);
    expect(live[0].id, 'the exhausted crate was reused rather than replaced').not.toBe(opened[0].id);
    expect(live[0].remaining, 'the replacement did not come out full').toBe(kind.stock);

    // The yard is already telling everybody about it — no reload, no hello. The
    // verb refreshed the world cache on its own goroutine, so the very next frame
    // carries the fresh crate rather than an empty store somebody has to wait
    // out. What it says depends on where the replacement landed, and BOTH answers
    // are the frame having arrived: a full count if it is here, the name of
    // another place if it is not. What it must never still say is «пуст».
    await expect
      .poll(async () => (await storeLine(page).textContent()) ?? '', {
        message: 'the replacement crate never reached the frame',
        timeout: 20_000,
      })
      .not.toContain('пуст');
    // And a walk to it still ends at it, wherever it went — which is the property
    // that replaced "the replacement stands on the same pitch".
    await walkToTheCrate(page);
    await expect(storeLine(page)).not.toContainText('дойди', { timeout: 20_000 });
  } finally {
    await context.close();
  }
});

test('a hello puts a beer store back into an empty yard', async ({ browser, baseURL }) => {
  // `ensureWorld`, which is the only thing standing between this game and a yard
  // that is permanently out of beer.
  //
  // There is no timer anywhere in «Ванягоччи» and there is deliberately not
  // going to be one, so nothing sweeps the world and nothing schedules a
  // restock. What puts a missing singleton back is a human arriving: a hello is
  // a fresh socket, which is the human-paced moment this game is allowed to read
  // and write the world on the plane's behalf. That covers the three ways a yard
  // can end up bare — the first day of the game, a cold start, and a database
  // somebody emptied — with one mechanism rather than three.
  //
  // `forgetWorldObjects()` in `beforeEach` is what makes the precondition real:
  // it deletes EVERY world object, the crate included, so this test starts in a
  // yard with no beer in it at all.
  test.setTimeout(120_000);
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const seeded = await stack();
  forgetPet(seeded[PLAYER_A]);

  const context = await browser.newContext(PHONE);
  try {
    await loginAs(context, PLAYER_A);
    const page = await context.newPage();
    await page.goto(`${base}/app/game-vanyagotchi`);

    // Reading the catalogue is an ordinary HTTP GET and touches no world: the
    // config handler serves content and nothing else, which is what lets the
    // emptiness below be asserted after it.
    const kind = await stockedKind(page);
    expect(liveCrates(kind.key), 'the yard was not empty to start with').toHaveLength(0);

    // The splash screen is not a hello — the socket is opened when he goes out
    // into the yard and not on mount, so that the intro spends no connection and
    // puts nobody's dot in front of anybody. Which means the yard is still bare
    // at this point, and the button below is the thing that changes it.
    await enterYardFrom(page);

    await expect
      .poll(() => liveCrates(kind.key).length, {
        message: 'nobody stood a crate up when a player walked into an empty yard',
        timeout: 20_000,
      })
      .toBe(1);
    const [standing] = liveCrates(kind.key);
    expect(standing.remaining, 'the new crate did not come out full').toBe(kind.stock);
    // Somewhere on the plane, rather than at a pitch this file knows: the crate
    // moves, so a coordinate assertion here would be a mirror of a random draw.
    for (const axis of ['x', 'y'] as const) {
      expect(standing[axis], `the crate was stood outside the plane on ${axis}`).toBeGreaterThan(0);
      expect(standing[axis], `the crate was stood outside the plane on ${axis}`).toBeLessThan(1);
    }

    // It reached the screen, with the catalogue's own stock in it — after he has
    // gone to look at it. The readout only carries a COUNT for a crate in the
    // place he is standing in; for one somewhere else it is that place's name,
    // which is the whole of what the crate moving cost this assertion. Not
    // asserted against «дойди» either way: where he ends up standing is left over
    // from whatever this shared process last saw him do, and this test is about
    // the crate existing rather than about his feet.
    await expect(storeLine(page)).toBeVisible({ timeout: 20_000 });
    await travelToTheCrate(page);
    await expect(storeLine(page)).toContainText(String(kind.stock));

    // And it is drawn — as a SHOP rather than as an entity, which is the change
    // the owner's feedback bought. The crate used to arrive in the roster as an
    // ordinary `obj-` dot AND as the store block, and the entity half is what made
    // it a person-shaped circle: this client tells a thing from a person by the
    // presence of `expires`, and a crate never expires. Now there is one
    // representation, so the yard carries no object for it at all and the shop is
    // drawn from the block.
    await expect(page.locator('[data-test="shop"]')).toBeVisible({ timeout: 20_000 });
    await expect(page.locator('[data-test="shop-left"]')).toHaveText(String(kind.stock));
    const shopAt = await cratePitch(page);
    expect(
      (await objectsOnThePlane(page)).some(
        (at) => Math.hypot(at.x - shopAt.x, at.y - shopAt.y) < 0.01,
      ),
      'the crate is still drawn as an entity as well as as a shop',
    ).toBe(false);
  } finally {
    await context.close();
  }
});

/**
 * Presses a place in the travel sheet until he has actually arrived in it.
 *
 * Re-pressed rather than pressed once, because a goto is bounded to one a second
 * per account — on its own clock, since a rate-refused journey is silent and a
 * shared bound would make the control read as dead. Two journeys in quick
 * succession can therefore have the second dropped, and re-pressing is both what
 * a player does and what keeps this test from being a race against that bound.
 *
 * Arrival is read off the plane's own caption, which names the place he is in.
 * That caption is derived from the PET, which is exactly what makes it the right
 * thing to assert here: it is the reading that was wrong in production.
 */
async function travelToPlace(page: Page, key: string, label: string): Promise<void> {
  await expect
    .poll(
      async () => {
        if (!(await page.locator('[data-test="places"]').isVisible())) {
          await page.locator('[data-test="here"]').click();
        }
        await page.locator(`[data-place="${key}"]`).click();
        return (await page.locator('[data-test="here"]').textContent()) ?? '';
      },
      { timeout: 20_000, intervals: [1100, 1100, 1100, 1100] },
    )
    .toContain(label);
}

test('travelling moves the place he is looking at, and he can walk back', async ({
  browser,
  baseURL,
}) => {
  // THE REGRESSION. A goto writes `pets.location_key`, and the browser reads
  // which place it is LOOKING at off the pet — so a journey the server never
  // pushed the pet back for left the yard drawing the place he had left. Three
  // things went wrong at once and each is asserted below: his own Ваня was
  // filtered out of the plane as somebody standing elsewhere, the caption went
  // on naming the old place, and the sheet went on marking the old place as the
  // one he was in — which is the row that means "stay", so there was no row left
  // that would send him home. He was stuck.
  //
  // It is here rather than in the stubbed suite because the stubbed suite is
  // where it hid: that harness pushes the state frame FROM THE STUB, so it
  // asserts the client is right GIVEN a push and can never notice its absence.
  test.setTimeout(90_000);
  const base = baseURL ?? 'http://127.0.0.1:8081';
  const seeded = await stack();
  // A fresh pet, so he starts in the catalogue's default place rather than
  // wherever an earlier test in this shared database happened to leave him.
  forgetPet(seeded[PLAYER_A]);

  const context = await browser.newContext(PHONE);
  try {
    await loginAs(context, PLAYER_A);
    const page = await enterYard(context, base);
    await expect(yourDot(page)).toHaveCount(1);

    // Every place the catalogue serves, read off the sheet rather than named
    // here — the client is told what the world contains and this test holds no
    // content of its own either.
    await page.locator('[data-test="here"]').click();
    await expect(page.locator('[data-test="places"]')).toBeVisible();
    const rows = page.locator('[data-test="place"]');
    const count = await rows.count();
    expect(count, 'the travel sheet offers nowhere to go').toBeGreaterThan(1);

    const places: { key: string; label: string; here: boolean }[] = [];
    for (let i = 0; i < count; i++) {
      const row = rows.nth(i);
      places.push({
        key: (await row.getAttribute('data-place')) ?? '',
        label: ((await row.locator('.place-name').textContent()) ?? '').trim(),
        here: (await row.getAttribute('data-here')) === '1',
      });
    }
    const from = places.find((place) => place.here);
    const to = places.find((place) => !place.here);
    expect(from, 'the sheet marks no place as the one he is standing in').toBeTruthy();
    expect(to, 'every place is marked as the one he is standing in').toBeTruthy();

    await travelToPlace(page, to!.key, to!.label);

    // He is still drawn. Without the push the plane filters him out of a place
    // he is no longer in and reports «здесь никого» — an empty yard containing
    // nobody, not even yourself.
    await expect(yourDot(page)).toHaveCount(1);

    // And the sheet agrees about where he is, which is what makes the way back
    // reachable at all.
    await page.locator('[data-test="here"]').click();
    await expect(page.locator(`[data-place="${to!.key}"]`)).toHaveAttribute('data-here', '1');
    await expect(page.locator(`[data-place="${from!.key}"]`)).not.toHaveAttribute('data-here', '1');

    // Back again — the leg that was unrecoverable in production.
    await travelToPlace(page, from!.key, from!.label);
    await expect(yourDot(page)).toHaveCount(1);
    await page.locator('[data-test="here"]').click();
    await expect(page.locator(`[data-place="${from!.key}"]`)).toHaveAttribute('data-here', '1');
  } finally {
    await context.close();
  }
});
