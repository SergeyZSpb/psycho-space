import { expect, test, type Locator, type Page } from '@playwright/test';

// «Ванягоччи» — the pet, at phone widths, with the backend faked.
//
// The sibling file covers the shared plane; this one covers everything the pet
// added to the same screen: the bars, the action row, the status line and the
// face on your own dot. They are separate files rather than one because the two
// halves of this game keep deliberately different company — presence lives in
// memory and dies with the process, the pet lives in Postgres and does not — and
// a failure should say which half broke without anyone having to read the
// stack trace.
//
// Everything is stubbed inside this file, and the helpers below are COPIES
// rather than imports, exactly as the sibling spec's header explains: games
// share no fixtures, so deleting this game means deleting its own files and
// nothing else (ARCHITECTURE ADR-028). That is only true if no other spec has to
// be edited on the way out.
//
// The catalogue fixtures further down are also copies rather than anything read
// from the app's own types, and for the same reason the sibling mirrors the wire
// constants: a change to the shape the server sends should fail here rather than
// silently follow along.

/** Mirrored from internal/gamevanyagotchi/message.go. */
const TYPE_ROSTER = 'vanyagotchi_roster';
const TYPE_YOU = 'vanyagotchi_you';

/** The routes the SPA calls, spelled out rather than built from a helper. */
const CONFIG_PATH = '/api/game-vanyagotchi/config';
const STATE_PATH = '/api/game-vanyagotchi/state';
const DRINK_PATH = '/api/game-vanyagotchi/actions/drink';
const RELIEVE_PATH = '/api/game-vanyagotchi/actions/relieve';

// ---------------------------------------------------------------------------
// Wire fixtures. Local mirrors of internal/gamevanyagotchi/{content,decay,pet}.go.
// ---------------------------------------------------------------------------

/** Extra drain one stat suffers while ANOTHER sits in a bad range. */
interface PenaltyDef {
  when_key: string;
  threshold: number;
  above: boolean;
  rate_per_hour: number;
}

interface StatDef {
  key: string;
  label: string;
  emoji: string;
  min: number;
  max: number;
  start: number;
  /** Signed: positive drains towards `min`, negative fills towards `max`. */
  decay_per_hour: number;
  good_high: boolean;
  warn_at: number;
  fatal: boolean;
  penalties?: PenaltyDef[];
}

/** One stat moved by one amount — an action moves a slice of these. */
interface EffectDef {
  stat_key: string;
  delta: number;
}

interface ActionDef {
  key: string;
  label: string;
  emoji: string;
  effects: EffectDef[];
  done: string;
  revives_fatal: boolean;
}

interface ConfigFixture {
  game_key: string;
  title: string;
  stats: StatDef[];
  actions: ActionDef[];
  /** `image` is present only for a skin the asset store has a blob for. */
  skins: { key: string; label: string; emoji: string; gradient: string; image?: string }[];
  locations: { key: string; label: string; entry: { x: number; y: number } }[];
  default_skin: string;
  default_location: string;
}

interface StateFixture {
  pet: {
    id: string;
    name: string | null;
    skin_key: string;
    location_key: string;
    died_at: string | null;
    created_at: string;
  };
  stats: { key: string; value: number; as_of: string; rate_per_hour: number }[];
  alive: boolean;
  server_now: string;
}

/**
 * The three shipped stats, with the shipped rates.
 *
 * Health is the CONSEQUENCE of the other two rather than a chore of its own: it
 * barely rots by itself, and what kills him is an empty beer or a full bladder,
 * six points an hour each. None of that arithmetic is the client's — it is here
 * because this file mirrors the wire, and because a screen that never receives a
 * penalty must still show one taking effect through `rate_per_hour`.
 */
const HP: StatDef = {
  key: 'hp',
  label: 'здоровье',
  emoji: '❤️',
  min: 0,
  max: 100,
  start: 65,
  decay_per_hour: 1,
  good_high: true,
  warn_at: 30,
  fatal: true,
  penalties: [
    { when_key: 'beer', threshold: 20, above: false, rate_per_hour: 6 },
    { when_key: 'bladder', threshold: 80, above: true, rate_per_hour: 6 },
  ],
};

const BEER: StatDef = {
  key: 'beer',
  label: 'пиво',
  emoji: '🍺',
  min: 0,
  max: 100,
  start: 60,
  decay_per_hour: 4,
  good_high: true,
  warn_at: 20,
  fatal: false,
};

const BLADDER: StatDef = {
  key: 'bladder',
  label: 'мочевой пузырь',
  emoji: '🚽',
  min: 0,
  max: 100,
  start: 0,
  // Negative because it FILLS. One signed rate, no second code path.
  decay_per_hour: -5,
  good_high: false,
  warn_at: 80,
  fatal: false,
};

/** Three stats in one press, and the third one is the joke. */
const DRINK: ActionDef = {
  key: 'drink',
  label: 'выпить пива',
  emoji: '🍺',
  effects: [
    { stat_key: 'beer', delta: 40 },
    { stat_key: 'hp', delta: 15 },
    { stat_key: 'bladder', delta: 25 },
  ],
  done: 'хорошо пошло',
  revives_fatal: true,
};

/** The other half of the loop drinking creates — and the verb a corpse is refused. */
const RELIEVE: ActionDef = {
  key: 'relieve',
  label: 'покакать',
  emoji: '💩',
  // A delta larger than the whole scale: the clamp is how the catalogue says
  // "reset", with no mechanism of its own.
  effects: [{ stat_key: 'bladder', delta: -100 }],
  done: 'полегчало',
  revives_fatal: false,
};

/** The shipped skin: an emoji over a gradient, because no sprite exists yet. */
const SKIN_VANYA = 'vanya';
const VANYA_EMOJI = '🫃';

/**
 * A skin key with a picture behind it, which the shipped catalogue does not have
 * yet.
 *
 * Invented here on purpose. `Config` decorates a skin with an art URL whenever
 * the shared asset store holds a blob for it, so the sprite branch is a live
 * path with no live data — exactly the shape that rots unnoticed. A data URI
 * rather than a file, so the picture is part of the fixture and the test needs
 * no network of its own.
 */
const SKIN_PAINTED = 'vanya-drawn';
const PAINTED_IMAGE =
  'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==';

/**
 * What a dot draws for a key the catalogue does not describe. Mirrored from
 * src/lib/vanyagotchiPlane.ts — a silhouette rather than a blank, because the
 * entity is real and standing there and the only thing missing is this client's
 * idea of what it looks like.
 */
const UNKNOWN_ART = '👤';

/** The catalogue as shipped today, plus one skin that carries a picture. */
const CATALOGUE: ConfigFixture = {
  game_key: 'vanyagotchi',
  title: 'Ванягоччи',
  stats: [HP, BEER, BLADDER],
  actions: [DRINK, RELIEVE],
  skins: [
    {
      key: SKIN_VANYA,
      label: 'дядя Ваня',
      emoji: VANYA_EMOJI,
      gradient: 'linear-gradient(160deg, #6b4a2f, #2f4a6b)',
    },
    {
      key: SKIN_PAINTED,
      label: 'нарисованный Ваня',
      emoji: VANYA_EMOJI,
      gradient: 'linear-gradient(160deg, #6b4a2f, #2f4a6b)',
      image: PAINTED_IMAGE,
    },
  ],
  locations: [{ key: 'yard', label: 'двор', entry: { x: 0.5, y: 0.5 } }],
  default_skin: SKIN_VANYA,
  default_location: 'yard',
};

/** Convenience: a catalogue with the stats and actions replaced wholesale. */
function catalogueOf(stats: StatDef[], actions: ActionDef[]): ConfigFixture {
  return { ...CATALOGUE, stats, actions };
}

interface StateOptions {
  alive?: boolean;
  diedAt?: string | null;
  petId?: string;
  /**
   * The drain each stat is under at the moment of the read, penalties folded in.
   *
   * Defaults to the catalogue's own rate, which is the uncoupled case; a test
   * about a Ваня who is actually dying overrides hp with the penalised figure,
   * because that is what the server would really have sent.
   */
  rates?: Record<string, number>;
}

/** Every stat fixture defined at module scope, so a state can carry a real rate. */
const DEFS: StatDef[] = [HP, BEER, BLADDER];

/**
 * A state stamped at the moment the request is answered.
 *
 * Stamped rather than fixed, because the client decays every value from `as_of`
 * towards `server_now`: a fixture built once at module load would be minutes old
 * by the time a test read it, and the number on screen would then be a function
 * of how long the suite had been running. With both stamped now the decay term
 * is zero and the assertion is about the value that was sent.
 */
function stateOf(values: Record<string, number>, opts: StateOptions = {}): StateFixture {
  const now = new Date().toISOString();
  return {
    pet: {
      id: opts.petId ?? 'pet-1',
      name: null,
      skin_key: 'vanya',
      location_key: 'yard',
      died_at: opts.diedAt ?? null,
      created_at: now,
    },
    stats: Object.entries(values).map(([key, value]) => ({
      key,
      value,
      as_of: now,
      rate_per_hour:
        opts.rates?.[key] ?? DEFS.find((d) => d.key === key)?.decay_per_hour ?? 0,
    })),
    alive: opts.alive ?? true,
    server_now: now,
  };
}

// ---------------------------------------------------------------------------
// Stubs.
// ---------------------------------------------------------------------------

/**
 * A refused action, as the backend would answer it: a stable machine code and
 * the status that goes with it. `pet_dead` is a 409 — the request is perfectly
 * well formed and would have worked on a living Ваня.
 */
interface Refusal {
  status: number;
  code: string;
}

/** What the pet endpoints should answer. `'fail'` serves a 500. */
interface PetStub {
  config?: ConfigFixture | 'fail';
  /** Called per request, so every answer is stamped with a fresh clock. */
  state?: (() => StateFixture) | 'fail';
  /**
   * What POST /actions/{key} answers with, given the verb — so one stub can
   * refuse one action and honour another, which is the whole point now that not
   * every action can be applied to a corpse. Defaults to whatever `state` says.
   */
  acted?: (action: string) => StateFixture | Refusal;
  /**
   * Held open, if set, before any action is answered. A test that needs to
   * observe the in-flight state waits on this rather than on a timeout — the
   * disabled button is otherwise a state one Vue flush wide.
   */
  hold?: Promise<void>;
}

/** The calls the page actually made, for tests that care how many. */
interface PetCalls {
  posts: string[];
  states: number;
}

/**
 * Stubs the HTTP the app shell needs, plus the pet endpoints.
 *
 * Copied from the sibling spec and extended; the catch-all still answers `{}`,
 * which is what an unconfigured catalogue looks like to the client — no stats,
 * so no bars — and is why the plane-only tests over there are unaffected by any
 * of this.
 */
async function stubBackend(page: Page, pet: PetStub = {}): Promise<PetCalls> {
  const calls: PetCalls = { posts: [], states: 0 };
  await page.addInitScript(() => {
    try {
      localStorage.setItem('ps-theme', 'dark');
      localStorage.setItem('ps-cookie-consent', '1');
    } catch {
      /* ignore */
    }
  });
  await page.route('**/api/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    const json = (body: unknown, status = 200) =>
      route.fulfill({
        status,
        contentType: 'application/json',
        headers: { 'X-Trace-Id': 'e2e-trace-id' },
        body: JSON.stringify(body),
      });
    const boom = () => json({ error: 'internal', trace_id: 'e2e-trace-id' }, 500);

    if (path === '/api/auth/me') {
      return json({
        account: {
          id: 'acc-1',
          display_name: 'Тест Пользователь',
          avatar_url: '',
          role: 'user',
          status: 'approved',
        },
      });
    }
    if (path === CONFIG_PATH) {
      if (pet.config === 'fail') return boom();
      return json(pet.config ?? {});
    }
    if (path === STATE_PATH) {
      calls.states += 1;
      if (pet.state === 'fail') return boom();
      return json(pet.state ? pet.state() : {});
    }
    if (path.startsWith('/api/game-vanyagotchi/actions/')) {
      calls.posts.push(path);
      if (pet.hold) await pet.hold;
      const verb = path.slice(path.lastIndexOf('/') + 1);
      if (pet.acted) {
        const answer = pet.acted(verb);
        if ('code' in answer) {
          return json({ error: answer.code, trace_id: 'e2e-trace-id' }, answer.status);
        }
        return json(answer);
      }
      if (pet.state === 'fail' || !pet.state) return boom();
      return json(pet.state());
    }
    return json({});
  });
  return calls;
}

/** Everything the socket handler hands back to the test. Copied and trimmed. */
interface SocketHarness {
  /** Pushes a frame to the page. Resolves once a socket exists to push it to. */
  push: (payload: string) => Promise<void>;
}

/**
 * Intercepts the WebSocket. Must be installed before `goto`, and the pattern
 * needs the trailing `*` — the client appends `?room=yard`, and a glob without
 * it does not match a query string.
 */
async function stubSocket(page: Page): Promise<SocketHarness> {
  let ws: { send: (m: string) => void } | undefined;
  let resolveReady: () => void;
  const ready = new Promise<void>((r) => {
    resolveReady = r;
  });

  await page.routeWebSocket('**/api/realtime*', (route) => {
    ws = route;
    resolveReady();
  });

  return {
    async push(payload: string) {
      await ready;
      ws?.send(payload);
    },
  };
}

/**
 * One entry of a roster frame. `art`, `label` and `pose` are optional because
 * they are optional on the wire — `label` is omitted outright for a Ваня with no
 * name — and every combination has to draw somebody.
 */
interface Peer {
  id: string;
  x: number;
  y: number;
  art?: string;
  label?: string;
  pose?: string;
}

/**
 * A roster frame, carrying the head count the server puts on one.
 *
 * `here` is how many PEOPLE are in the yard, which stopped being `peers.length`
 * once the roster began carrying the yard's regulars and everybody asleep in it
 * as well. Every peer this file builds is a person, so the two agree — but the
 * field is sent rather than omitted, because omitting it would silently route
 * every frame in this file through the client's legacy fallback and leave the
 * path a live server actually uses untested here.
 */
function roster(...peers: Peer[]): string {
  return JSON.stringify({ t: TYPE_ROSTER, peers, here: peers.length });
}

/** Copied, not imported — see the header. */
async function expectNoOverflow(page: Page, label: string): Promise<void> {
  const diff = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(diff, `horizontal overflow on "${label}": ${diff}px`).toBeLessThanOrEqual(0);
}

/** The play screen must never scroll vertically either — that is the whole layout rule. */
async function expectNoVerticalScroll(page: Page, label: string): Promise<void> {
  const diff = await page.evaluate(
    () => document.documentElement.scrollHeight - document.documentElement.clientHeight,
  );
  expect(diff, `vertical scroll on "${label}": ${diff}px`).toBeLessThanOrEqual(1);
}

/** Asserts an element's bottom edge is inside the viewport. */
async function expectOnScreen(page: Page, loc: Locator, label: string): Promise<void> {
  await expect(loc, `${label} should be visible`).toBeVisible();
  const box = await loc.boundingBox();
  expect(box, `${label} has no bounding box`).not.toBeNull();
  const bottom = (box?.y ?? 0) + (box?.height ?? 0);
  const height = page.viewportSize()?.height ?? 0;
  expect(bottom, `${label} is pushed off the bottom: ${bottom} > ${height}`).toBeLessThanOrEqual(
    height,
  );
}

function isMobile(page: Page): boolean {
  const vp = page.viewportSize();
  return !!vp && vp.width <= 600;
}

async function expectTapTarget(loc: Locator, label: string): Promise<void> {
  await expect(loc, `${label} should be visible`).toBeVisible();
  const box = await loc.boundingBox();
  expect(box, `${label} has no bounding box`).not.toBeNull();
  if (box) {
    const min = Math.round(Math.min(box.width, box.height));
    expect(
      min,
      `${label} tap target too small: ${Math.round(box.width)}x${Math.round(box.height)}`,
    ).toBeGreaterThanOrEqual(44);
  }
}

/**
 * How much of a stat's track its fill occupies, 0..1.
 *
 * Measured rather than read off the inline style, because what is being checked
 * is that the bar was scaled against the CATALOGUE's own bounds — 7 of 10 is
 * most of the track, 7 of a hardcoded 100 is a sliver — and a percentage string
 * would prove only that some number was written.
 */
function barFraction(page: Page, key: string): Promise<number> {
  return page.locator(`[data-test="stat-${key}"] .stat-fill`).evaluate((el) => {
    const fill = el as HTMLElement;
    const track = fill.parentElement as HTMLElement;
    return fill.getBoundingClientRect().width / track.getBoundingClientRect().width;
  });
}

const plane = (page: Page) => page.locator('[data-test="plane"]');
const dots = (page: Page) => page.locator('[data-test="peer"]');
/** One entity's face. On every dot, not only on the caller's own. */
const face = (page: Page, id: string) =>
  page.locator(`[data-peer="${id}"] [data-test="peer-face"]`);
const petLine = (page: Page) => page.locator('[data-test="pet-line"]');
const statValue = (page: Page, key: string) => page.locator(`[data-test="stat-value-${key}"]`);
const actionBtn = (page: Page, key: string) => page.locator(`[data-test="action-${key}"]`);

/** The death notice — the one line the screen writes rather than the catalogue. */
const DEATH_LINE = 'Ваня не выдержал. Откачай его.';

/** Loads the game and steps past the intro into the yard. */
async function enterYard(page: Page): Promise<void> {
  await page.goto('/app/game-vanyagotchi');
  await page.getByRole('button', { name: 'Во двор' }).click();
  await expect(plane(page)).toBeVisible();
}

test.describe('«Ванягоччи» — the pet on the yard screen', () => {
  test('the bars, the numbers and both actions come from the catalogue', async ({ page }) => {
    // Nothing on this screen is spelled out in the SPA: the labels, the bounds
    // and the buttons' wording all arrive from GET /config, which is what makes
    // "adding a stat is a backend-only change" true rather than aspirational.
    await stubBackend(page, {
      config: CATALOGUE,
      state: () => stateOf({ hp: 72, beer: 44, bladder: 18 }),
    });
    await stubSocket(page);
    await enterYard(page);

    await expect(page.locator('[data-test="pet-stats"]')).toBeVisible();
    // Three bars now, and the third is the one that turned health into a
    // consequence: hp is what beer and the bladder do to him.
    await expect(page.locator('.stats .stat')).toHaveCount(3);
    await expect(page.locator('[data-test="stat-hp"]')).toBeVisible();
    await expect(page.locator('[data-test="stat-beer"]')).toBeVisible();
    await expect(page.locator('[data-test="stat-bladder"]')).toBeVisible();

    // Rounded, and decayed to now — with `as_of` stamped at the response the
    // decay term is zero, so these are exactly the numbers that were sent.
    await expect(statValue(page, 'hp')).toHaveText('72');
    await expect(statValue(page, 'beer')).toHaveText('44');
    await expect(statValue(page, 'bladder')).toHaveText('18');

    // One button per catalogue action, labelled from it, emoji and all — the
    // row iterates the catalogue rather than naming a verb.
    await expect(page.locator('.actions .v-btn')).toHaveCount(2);
    await expect(actionBtn(page, 'drink')).toContainText('выпить пива');
    await expect(actionBtn(page, 'relieve')).toContainText('покакать');
  });

  test('the screen still never scrolls now that the pet panel is on it', async ({ page }) => {
    // The layout rule this game inherited is literal: one flexible child, the
    // rest fixed, `overflow: hidden`. The panel below the plane grew a third bar
    // and a second button, so the plane has to give up the height rather than
    // the panel being pushed off — which is exactly the regression a growing pet
    // panel is most likely to cause.
    await stubBackend(page, {
      config: CATALOGUE,
      state: () => stateOf({ hp: 61, beer: 33, bladder: 44 }),
    });
    const socket = await stubSocket(page);
    await enterYard(page);
    await expect(page.locator('[data-test="pet-stats"]')).toBeVisible();

    // A crowded yard as well as an empty one: dots are absolutely positioned
    // inside the plane, but a layout mistake would show up here first.
    await socket.push(
      roster(
        ...Array.from({ length: 12 }, (_, i) => ({
          id: `peer-${i}`,
          x: (i % 4) / 3,
          y: Math.floor(i / 4) / 2,
        })),
      ),
    );
    await expect(dots(page)).toHaveCount(12);

    await expectNoOverflow(page, 'vanyagotchi yard with the pet panel');
    await expectNoVerticalScroll(page, 'vanyagotchi yard with the pet panel');
    await expectOnScreen(page, actionBtn(page, 'drink'), 'the drink button');
    await expectOnScreen(page, actionBtn(page, 'relieve'), 'the relieve button');
    await expectOnScreen(page, page.getByText(/во дворе:/), 'the status row');
  });

  test('the pet panel still fits the smallest screen we support', async ({ page }) => {
    // 320x568 is the floor — an iPhone SE in portrait, and the size at which the
    // fixed rows come closest to eating the plane entirely. Set before `goto`
    // rather than resized afterwards, the same way the sibling spec pins the
    // disclaimer, so the layout is built for this size rather than reflowed into
    // it. Three bars and two buttons is the tallest the panel has ever been.
    await page.setViewportSize({ width: 320, height: 568 });
    await stubBackend(page, {
      config: CATALOGUE,
      state: () => stateOf({ hp: 61, beer: 33, bladder: 44 }),
    });
    await stubSocket(page);
    await enterYard(page);
    await expect(page.locator('[data-test="pet-stats"]')).toBeVisible();

    await expectNoOverflow(page, 'vanyagotchi yard at 320x568');
    await expectNoVerticalScroll(page, 'vanyagotchi yard at 320x568');
    await expectOnScreen(page, actionBtn(page, 'drink'), 'the drink button');
    await expectOnScreen(page, actionBtn(page, 'relieve'), 'the relieve button');
    await expectOnScreen(page, page.getByText(/во дворе:/), 'the status row');
    // And the plane it is sharing the screen with has not been squeezed to
    // nothing to make room.
    const box = await plane(page).boundingBox();
    expect(box?.height ?? 0, 'the plane collapsed to make room for the panel').toBeGreaterThan(120);
  });

  test('both actions are thumb-sized targets', async ({ page }) => {
    await stubBackend(page, {
      config: CATALOGUE,
      state: () => stateOf({ hp: 61, beer: 33, bladder: 44 }),
    });
    await stubSocket(page);
    await enterYard(page);

    if (isMobile(page)) {
      // Vuetify's default button is 36px tall; the view overrides it precisely
      // because this floor is enforced rather than requested. Two buttons now
      // share one row, so the width is the half that could go wrong.
      await expectTapTarget(actionBtn(page, 'drink'), 'drink action');
      await expectTapTarget(actionBtn(page, 'relieve'), 'relieve action');
    }
  });

  test('drinking posts once and moves all three bars from the answer', async ({ page }) => {
    // The client sends a VERB and never a value, and the numbers it then shows
    // are the server's own recomputed ones — not a local application of the
    // catalogue's `effects`. Stubbing the POST with an answer no local sum
    // produces is what tells the two apart, and drinking is the case where it
    // matters most: one press moves three stats at once.
    let acted = false;
    const calls = await stubBackend(page, {
      config: CATALOGUE,
      state: () =>
        acted
          ? stateOf({ hp: 61, beer: 88, bladder: 47 })
          : stateOf({ hp: 26, beer: 4, bladder: 12 }),
      acted: () => {
        acted = true;
        // Deliberately not 26+15, 4+40, 12+25: a client applying the effects
        // itself would land on 41/44/37 and a client that trusted the server
        // lands here, so the two cannot both pass.
        return stateOf({ hp: 61, beer: 88, bladder: 47 });
      },
    });
    await stubSocket(page);
    await enterYard(page);
    await expect(statValue(page, 'hp')).toHaveText('26');
    await expect(statValue(page, 'beer')).toHaveText('4');

    await actionBtn(page, 'drink').click();

    await expect(statValue(page, 'hp')).toHaveText('61');
    await expect(statValue(page, 'beer')).toHaveText('88');
    await expect(statValue(page, 'bladder')).toHaveText('47');
    // The line under the bars is the action's own `done` text, straight from the
    // catalogue — another string the SPA does not know.
    await expect(petLine(page)).toHaveText('хорошо пошло');
    // Exactly one POST, to the verb that was pressed.
    expect(calls.posts).toEqual([DRINK_PATH]);
    // The bar followed the number rather than staying where it was. Polled
    // rather than measured once: the fill animates its width over 400 ms, so a
    // single reading taken the instant the number changed catches it part-way
    // along and says nothing about where it was heading.
    await expect
      .poll(() => barFraction(page, 'beer'), {
        message: 'the beer bar did not follow its number',
      })
      .toBeGreaterThan(0.5);
  });

  test('relieving posts to its own verb and empties the bladder', async ({ page }) => {
    // The second verb, and the proof the row is really iterating the catalogue:
    // the path is built from the action's own key, so pressing the second button
    // must reach `/actions/relieve` and nothing else.
    let acted = false;
    const calls = await stubBackend(page, {
      config: CATALOGUE,
      state: () =>
        acted ? stateOf({ hp: 70, beer: 50, bladder: 0 }) : stateOf({ hp: 70, beer: 50, bladder: 92 }),
      acted: () => {
        acted = true;
        // Clamped to the floor by the server: the catalogue's −100 is larger
        // than the whole scale, which is how it says "reset".
        return stateOf({ hp: 70, beer: 50, bladder: 0 });
      },
    });
    await stubSocket(page);
    await enterYard(page);
    await expect(statValue(page, 'bladder')).toHaveText('92');
    await expect(page.locator('[data-test="stat-bladder"][data-trouble="1"]')).toHaveCount(1);

    await actionBtn(page, 'relieve').click();

    await expect(statValue(page, 'bladder')).toHaveText('0');
    await expect(petLine(page)).toHaveText('полегчало');
    expect(calls.posts).toEqual([RELIEVE_PATH]);
    // And the bar stopped reading as trouble, because 0 is the good end of a
    // stat whose `good_high` is false.
    await expect(page.locator('[data-test="stat-bladder"][data-trouble="1"]')).toHaveCount(0);
  });

  test('a refused action shows the death line rather than the global error modal', async ({
    page,
  }) => {
    // THIS PATH IS NEWLY REACHABLE. Until relieving himself existed, every verb
    // in the catalogue could revive, so the server had no way to refuse one and
    // the branch that handles a 409 was dead code kept on faith. A dead Ваня
    // does not go to the toilet, so now it is live — and what the player must
    // see is the screen's own instruction to bring him round, NOT the generic
    // "something went wrong" modal for a situation the game is already
    // explaining in words.
    //
    // He is alive to begin with and dies while the page is holding a stale
    // state, which is the honest way this happens: nothing re-reads between
    // actions, so the first the client hears of it is the refusal.
    let dead = false;
    const calls = await stubBackend(page, {
      config: CATALOGUE,
      state: () =>
        dead
          ? stateOf(
              { hp: 0, beer: 0, bladder: 96 },
              // A truthful rate for a Ваня with both needs unmet: 1 of his own
              // plus 6 for the beer plus 6 for the bladder.
              { alive: false, diedAt: '2026-07-25T03:00:00Z', rates: { hp: 13 } },
            )
          : stateOf({ hp: 4, beer: 1, bladder: 96 }, { rates: { hp: 13 } }),
      acted: (verb) => {
        if (verb === 'relieve') {
          dead = true;
          return { status: 409, code: 'pet_dead' };
        }
        return stateOf({ hp: 15, beer: 40, bladder: 96 });
      },
    });
    await stubSocket(page);
    await enterYard(page);

    // A press that succeeds first, so the line has something on it that the
    // refusal has to clear — otherwise "the death notice appeared" would be
    // indistinguishable from "the line never changed".
    await actionBtn(page, 'drink').click();
    await expect(petLine(page)).toHaveText('хорошо пошло');

    const statesBefore = calls.states;
    await actionBtn(page, 'relieve').click();

    // The refusal is handled: the line says what to do about it, and the modal
    // never opens.
    await expect(petLine(page)).toHaveText(DEATH_LINE);
    await expect(page.getByText('Ой, ошибка')).toHaveCount(0);
    // And it re-read the world rather than merely swapping a string, which is
    // the only reason the screen now knows he is dead at all.
    expect(calls.states, 'the refusal did not re-read the state').toBeGreaterThan(statesBefore);
    expect(calls.posts).toEqual([DRINK_PATH, RELIEVE_PATH]);

    // Still playable: the verb that CAN revive him is still there to press.
    await expect(actionBtn(page, 'drink')).toBeEnabled();
  });

  test('an action in flight cannot be fired twice', async ({ page }) => {
    // A double-fire is a real bug on a touchscreen, and the guard is a flag the
    // whole row shares. Proved by holding the response open rather than by
    // racing two clicks: the disabled state is otherwise one Vue flush wide and
    // an assertion about it would be a coin toss.
    let release!: () => void;
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    const calls = await stubBackend(page, {
      config: CATALOGUE,
      state: () => stateOf({ hp: 40, beer: 30, bladder: 20 }),
      acted: () => stateOf({ hp: 55, beer: 70, bladder: 45 }),
      hold: held,
    });
    await stubSocket(page);
    await enterYard(page);
    await expect(statValue(page, 'hp')).toHaveText('40');

    await actionBtn(page, 'drink').click();

    // Both buttons, not just the pressed one: one request is in flight and the
    // answer is a whole new world, so relieving mid-drink would be acting on a
    // state that is already gone.
    await expect(actionBtn(page, 'drink')).toBeDisabled();
    await expect(actionBtn(page, 'relieve')).toBeDisabled();

    release();
    await expect(statValue(page, 'hp')).toHaveText('55');
    await expect(actionBtn(page, 'drink')).toBeEnabled();
    expect(calls.posts).toEqual([DRINK_PATH]);
  });

  test('a stat in its warning range says so, and one outside does not', async ({ page }) => {
    // Which values count as trouble is catalogue data — `warn_at` plus which end
    // of the scale is the happy one — so the stylesheet is told rather than
    // knowing that thirty is a bad amount of health. Both directions are checked
    // in one go: hp is in trouble when it is LOW, bladder when it is HIGH.
    await stubBackend(page, {
      config: CATALOGUE,
      // hp 20 < warn_at 30 (good_high) -> trouble.
      // beer 55 > warn_at 20 (good_high) -> fine.
      // bladder 10, warn_at 80, good_high false -> comfortably fine.
      state: () => stateOf({ hp: 20, beer: 55, bladder: 10 }),
    });
    await stubSocket(page);
    await enterYard(page);

    await expect(page.locator('[data-test="stat-hp"][data-trouble="1"]')).toHaveCount(1);
    await expect(page.locator('[data-test="stat-beer"][data-trouble="1"]')).toHaveCount(0);
    await expect(page.locator('[data-test="stat-bladder"][data-trouble="1"]')).toHaveCount(0);
  });

  test('a full bladder is trouble even though a full health bar is not', async ({ page }) => {
    // The other half of the rule above. A single-direction implementation — "low
    // is bad" — passes the test above and fails this one, which is the whole
    // reason `good_high` is on the wire. An empty beer is the same shape as low
    // health, and is here because it is the OTHER thing that kills him.
    await stubBackend(page, {
      config: CATALOGUE,
      state: () => stateOf({ hp: 95, beer: 3, bladder: 88 }),
    });
    await stubSocket(page);
    await enterYard(page);

    await expect(page.locator('[data-test="stat-bladder"][data-trouble="1"]')).toHaveCount(1);
    await expect(page.locator('[data-test="stat-beer"][data-trouble="1"]')).toHaveCount(1);
    await expect(page.locator('[data-test="stat-hp"][data-trouble="1"]')).toHaveCount(0);
  });

  test('a dead Ваня is legible from the dot as well as from the line', async ({ page }) => {
    // Death has to read without anybody parsing a number: the line says what to
    // do about it, and the face on the dot says it at a glance.
    //
    // The pose comes off the WIRE and not out of this screen's own pet state,
    // which is why the socket has to speak before there is anything to look at.
    // That is not an implementation detail: the yard shows everybody ONE world,
    // and a condition worked out locally could only ever be worked out from
    // numbers its owner alone can see — so a dying Ваня would look ill to
    // himself and perfectly well to the player standing next to him.
    await stubBackend(page, {
      config: CATALOGUE,
      state: () =>
        stateOf(
          { hp: 0, beer: 0, bladder: 90 },
          { alive: false, diedAt: '2026-07-24T03:00:00Z', rates: { hp: 13 } },
        ),
    });
    const socket = await stubSocket(page);
    await enterYard(page);

    await expect(petLine(page)).toHaveText(DEATH_LINE);

    await socket.push(JSON.stringify({ t: TYPE_YOU, id: 'me' }));
    await socket.push(
      roster(
        { id: 'me', x: 0.5, y: 0.5, art: SKIN_VANYA, pose: 'dead' },
        { id: 'other', x: 0.2, y: 0.2, art: SKIN_VANYA, pose: 'fine' },
      ),
    );
    await expect(dots(page)).toHaveCount(2);

    // Both of them have a face, because appearance is sent for everybody.
    await expect(page.locator('[data-test="peer-face"]')).toHaveCount(2);
    await expect(face(page, 'me')).toHaveAttribute('data-condition', 'dead');
    // And the neighbour keeps his own condition: a screen that painted its own
    // pet's state across the yard would have buried him too.
    await expect(face(page, 'other')).toHaveAttribute('data-condition', 'fine');
  });

  test('a skin key resolves against the catalogue, and an unknown one still draws somebody', async ({
    page,
  }) => {
    // THE property this iteration is bought for, and the reason the wire carries
    // a catalogue KEY rather than a picture: a skin — or, later, an NPC that is
    // not a Ваня at all — can be added on the server with no client deploy. A
    // client that refused to render what it had not heard of would have to ship
    // in lockstep with the server, which is precisely the coupling being avoided.
    //
    // All three branches in one frame, because they are one decision: picture if
    // the catalogue has one, its emoji if not, and a silhouette if the catalogue
    // has never heard of the key. The third is the one with no other coverage —
    // nothing else in the suite can produce a key the config does not define.
    await stubBackend(page, {
      config: CATALOGUE,
      state: () => stateOf({ hp: 61, beer: 33, bladder: 44 }),
    });
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(
      roster(
        { id: 'plain', x: 0.2, y: 0.2, art: SKIN_VANYA, pose: 'fine' },
        { id: 'painted', x: 0.5, y: 0.5, art: SKIN_PAINTED, pose: 'fine' },
        { id: 'npc', x: 0.8, y: 0.8, art: 'npc-babushka-2027', pose: 'poorly' },
      ),
    );
    await expect(dots(page)).toHaveCount(3);

    // A key the catalogue describes with an emoji and no blob: the emoji, and
    // it is the CATALOGUE's emoji rather than anything this screen knows.
    await expect(face(page, 'plain')).toHaveText(VANYA_EMOJI);
    await expect(face(page, 'plain').locator('img')).toHaveCount(0);

    // A key with a picture behind it: the picture wins, and the emoji is not
    // drawn underneath it.
    const sprite = face(page, 'painted').locator('img.peer-sprite');
    await expect(sprite).toHaveCount(1);
    await expect(sprite).toHaveAttribute('src', PAINTED_IMAGE);
    await expect(face(page, 'painted')).toHaveText('');

    // A key from a server newer than this client: a placeholder, and — the part
    // that matters — a dot that is still on the plane, still counted, still
    // carrying its own pose. Invisible would be the wrong answer.
    await expect(face(page, 'npc')).toHaveText(UNKNOWN_ART);
    await expect(face(page, 'npc')).toHaveAttribute('data-condition', 'poorly');
    await expect(page.locator('[data-peer="npc"]')).toBeVisible();
    await expect(page.getByText('во дворе: 3')).toBeVisible();
  });

  test('a catalogue key the SPA has never heard of renders anyway', async ({ page }) => {
    // THE property of this iteration, and the one worth a test of its own:
    // adding a stat or a verb is meant to be a backend change with no frontend
    // deploy. `mood` and `feed` do not exist anywhere in the Go catalogue or in
    // the SPA — if either had been hardcoded, nothing below would appear. Note
    // `feed` moves TWO stats, one of which the catalogue does not define: the
    // client posts a verb and never applies an effect, so it does not care.
    const MOOD: StatDef = {
      key: 'mood',
      label: 'настроение',
      emoji: '🙂',
      min: 0,
      max: 10,
      start: 5,
      decay_per_hour: 1,
      good_high: true,
      warn_at: 3,
      fatal: false,
    };
    const FEED: ActionDef = {
      key: 'feed',
      label: 'накормить',
      emoji: '🥟',
      effects: [
        { stat_key: 'mood', delta: 2 },
        { stat_key: 'nonexistent', delta: 99 },
      ],
      done: 'поел',
      revives_fatal: false,
    };
    await stubBackend(page, {
      config: catalogueOf([MOOD], [FEED]),
      state: () => stateOf({ mood: 7 }, { rates: { mood: MOOD.decay_per_hour } }),
    });
    await stubSocket(page);
    await enterYard(page);

    await expect(page.locator('[data-test="stat-mood"]')).toBeVisible();
    await expect(statValue(page, 'mood')).toHaveText('7');
    await expect(actionBtn(page, 'feed')).toContainText('накормить');
    // The shipped keys are gone, because the catalogue no longer mentions them.
    await expect(page.locator('[data-test="stat-hp"]')).toHaveCount(0);
    await expect(page.locator('[data-test="stat-bladder"]')).toHaveCount(0);
    await expect(actionBtn(page, 'drink')).toHaveCount(0);
    await expect(actionBtn(page, 'relieve')).toHaveCount(0);
    // And the bar is scaled against THIS stat's bounds (0..10), not against a
    // hardcoded 0..100: 7 of 10 is most of the track, 7 of 100 would be a sliver.
    await expect
      .poll(() => barFraction(page, 'mood'), {
        message: 'the bar was not scaled against the catalogue bounds',
      })
      .toBeGreaterThan(0.5);
  });

  test('a pet that will not load costs the bars and nothing else', async ({ page }) => {
    // The plane is the point of this screen and it runs on the socket, so a
    // broken pet must not take the yard down with it — and above all must not
    // pop the global error modal over a working world. The failure is
    // deliberately silent until the player actually presses something.
    await stubBackend(page, { config: 'fail', state: 'fail' });
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(roster({ id: 'peer-a', x: 0.3, y: 0.3 }, { id: 'peer-b', x: 0.7, y: 0.7 }));
    await expect(dots(page)).toHaveCount(2);
    await expect(page.getByText('во дворе: 2')).toBeVisible();
    await expect(page.getByText('на связи')).toBeVisible();

    // No bars, no buttons — and no modal.
    await expect(page.locator('[data-test="pet-stats"]')).toHaveCount(0);
    await expect(actionBtn(page, 'drink')).toHaveCount(0);
    await expect(page.getByText('Ой, ошибка')).toHaveCount(0);
    // The line is still there, and empty: it is a fixed-height row so the plane
    // above does not resize when text comes and goes.
    await expect(petLine(page)).toHaveText('');

    await expectNoOverflow(page, 'vanyagotchi yard with no pet');
    await expectNoVerticalScroll(page, 'vanyagotchi yard with no pet');
  });


  test('the splash still carries both the lore and the disclaimer at 320x568', async ({ page }) => {
    // The disclaimer is a requirement rather than decoration, and the lore line
    // that now sits above it is the one thing on that screen most likely to push
    // it off a short phone — the two prose blocks are the only shrinkable
    // children there, and the disclaimer is deliberately not one of them.
    await page.setViewportSize({ width: 320, height: 568 });
    await stubBackend(page, {
      config: CATALOGUE,
      state: () => stateOf({ hp: 65, beer: 60, bladder: 0 }),
    });
    await stubSocket(page);
    await page.goto('/app/game-vanyagotchi');

    const disclaimer = page.getByText(
      'Все персонажи вымышлены; любые совпадения с реальными людьми случайны.',
    );
    await expect(disclaimer).toBeVisible();
    await expect(page.getByText('Ваня — офигенный чел')).toBeVisible();
    await expect(page.getByText('постоянно теряет ключи')).toBeVisible();

    await expectOnScreen(page, disclaimer, 'the fiction disclaimer');
    await expectOnScreen(page, page.getByRole('button', { name: 'Во двор' }), 'the enter-yard CTA');
    await expectNoOverflow(page, 'vanyagotchi splash at 320x568');
    await expectNoVerticalScroll(page, 'vanyagotchi splash at 320x568');
  });
});
