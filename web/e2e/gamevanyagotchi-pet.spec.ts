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
/** A verb frame on its way to the server. Mirrored from message.go. */
const TYPE_DO = 'vanyagotchi_do';
/** The owner's own pet, pushed after it changes. */
const TYPE_STATE = 'vanyagotchi_state';

/**
 * The routes the SPA calls, spelled out rather than built from a helper.
 *
 * Two of them, and that is the whole list: a verb is a socket frame, so there is
 * no action route here to name. The stub still answers the action PREFIX, but as
 * a tripwire rather than as a route — see `stubBackend`.
 */
const CONFIG_PATH = '/api/game-vanyagotchi/config';
const STATE_PATH = '/api/game-vanyagotchi/state';

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
  /** A LIFETIME TALLY rather than a bar — never drawn as a track, never trouble. */
  counter: boolean;
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
  /**
   * Puts every non-counter stat back to its catalogue `start` and IGNORES
   * `effects`. Mirrored because it is the field that makes an action with an
   * empty effects list meaningful.
   */
  starts_over: boolean;
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
 * The three shipped BARS, with the shipped rates.
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
  counter: false,
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
  counter: false,
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
  counter: false,
  fatal: false,
};

/**
 * The two shipped LIFETIME TALLIES, and they are ordinary stats with a rate of
 * nought — which is the whole trick, and the reason they cost no migration.
 *
 * Mirrored with the shipped numbers because two of them are the trap this
 * screen has to avoid: `max` is a million, so anything that drew one as a bar
 * would draw an empty track that still looks empty after a year of drinking,
 * and `warn_at` sits on the floor so nothing ever reports a total as trouble.
 */
const BEERS_DRUNK: StatDef = {
  key: 'beers_drunk',
  label: 'выпито пива',
  emoji: '🍻',
  min: 0,
  max: 1_000_000,
  start: 0,
  decay_per_hour: 0,
  good_high: true,
  warn_at: 0,
  counter: true,
  fatal: false,
};

const SHITS_TAKEN: StatDef = {
  key: 'shits_taken',
  label: 'покакано раз',
  emoji: '🧻',
  min: 0,
  max: 1_000_000,
  start: 0,
  decay_per_hour: 0,
  good_high: true,
  warn_at: 0,
  counter: true,
  fatal: false,
};

/** Four stats in one press, and the joke is the third — the fourth just counts. */
const DRINK: ActionDef = {
  key: 'drink',
  label: 'выпить пива',
  emoji: '🍺',
  effects: [
    { stat_key: 'beer', delta: 40 },
    { stat_key: 'hp', delta: 15 },
    { stat_key: 'bladder', delta: 25 },
    { stat_key: 'beers_drunk', delta: 1 },
  ],
  done: 'хорошо пошло',
  // NO LONGER THE WAY BACK FROM A DEATH. Beer used to revive, which made dying
  // almost invisible; a corpse now refuses this like every other verb.
  revives_fatal: false,
  starts_over: false,
};

/** The other half of the loop drinking creates — and the verb a corpse is refused. */
const RELIEVE: ActionDef = {
  key: 'relieve',
  label: 'покакать',
  emoji: '💩',
  // A delta larger than the whole scale: the clamp is how the catalogue says
  // "reset", with no mechanism of its own.
  effects: [
    { stat_key: 'bladder', delta: -100 },
    { stat_key: 'shits_taken', delta: 1 },
  ],
  done: 'полегчало',
  revives_fatal: false,
  starts_over: false,
};

/**
 * The ONLY way back from a death, and the only action with no effects at all.
 *
 * `starts_over` ignores the effects list outright and puts every bar back to its
 * catalogue `start`, sparing the tallies — so listing deltas here would be a
 * second description of an outcome, and the one that did nothing would be the
 * one somebody edited.
 */
const REVIVE: ActionDef = {
  key: 'revive',
  label: 'восстать из мертвых',
  emoji: '🧟',
  effects: [],
  done: 'воскрес',
  revives_fatal: true,
  starts_over: true,
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
  stats: [HP, BEER, BLADDER, BEERS_DRUNK, SHITS_TAKEN],
  actions: [DRINK, RELIEVE, REVIVE],
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
const DEFS: StatDef[] = [HP, BEER, BLADDER, BEERS_DRUNK, SHITS_TAKEN];

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
   * What a VERB does, given its key — so one stub can refuse one action and
   * honour another, which is the whole point now that not every action can be
   * applied to a corpse. Defaults to whatever `state` says.
   *
   * Read by the socket fake, which is where verbs actually go, and by the HTTP
   * tripwire, which exists only so that a verb going anywhere else fails loudly.
   * A `Refusal` is not sent back at all: the socket owes no reply, so the server
   * answers a refused verb with a line over the player's own Ваня instead.
   */
  acted?: (action: string) => StateFixture | Refusal;
}

/**
 * The calls the page actually made, for tests that care how many.
 *
 * One field, and it counts the requests that must NEVER happen rather than the
 * ones that should — see the tripwire in `stubBackend`. There was a `states`
 * counter beside it, incremented on every read and asserted by nothing; a
 * counter nobody reads cannot fail, so it was not coverage.
 */
interface PetCalls {
  posts: string[];
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
  stubbedPet = pet;
  const calls: PetCalls = { posts: [] };
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
      if (pet.state === 'fail') return boom();
      return json(pet.state ? pet.state() : {});
    }
    // A TRIPWIRE, not a route. `POST /api/game-vanyagotchi/actions/{verb}` NO
    // LONGER EXISTS on the server — a verb travels over the socket as a
    // `vanyagotchi_do` frame — so nothing the SPA does should ever land here.
    //
    // The branch exists precisely so that it never fires: every action test
    // below asserts `calls.posts` is EMPTY, which is what proves the SPA sends
    // no HTTP verb. Without it a regression — the client quietly going back to
    // HTTP — would be caught by nothing here and would 404 in production
    // instead, where the failure is a button that silently does nothing.
    //
    // So do not delete it because the route is gone. Gone is the point.
    if (path.startsWith('/api/game-vanyagotchi/actions/')) {
      calls.posts.push(path);
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
  /** The verb batches the page actually sent, in order. */
  asked: () => string[][];
}

/**
 * Intercepts the WebSocket. Must be installed before `goto`, and the pattern
 * needs the trailing `*` — the client appends `?room=yard`, and a glob without
 * it does not match a query string.
 */
// The world the last stubBackend described, so the socket fake can answer a
// verb the same way the HTTP fake answers a read. A test states its world once
// and both halves of the app agree about it — which is exactly the property the
// real server has, both paths going through one Service.Do.
let stubbedPet: PetStub = {};

async function stubSocket(page: Page, pet: PetStub = stubbedPet): Promise<SocketHarness> {
  let ws: { send: (m: string) => void } | undefined;
  let resolveReady: () => void;
  const ready = new Promise<void>((r) => {
    resolveReady = r;
  });
  const asked: string[][] = [];

  await page.routeWebSocket('**/api/realtime*', (route) => {
    ws = route;
    // Verbs travel over the socket now, so the fake has to behave the way the
    // server does: apply them and PUSH the resulting state back. It is not a
    // reply — there is no correlation and nothing waits for it — which is why
    // this is a plain send rather than anything resembling a response.
    route.onMessage((raw) => {
      let frame: { t?: string; verbs?: unknown };
      try {
        frame = JSON.parse(typeof raw === 'string' ? raw : String(raw));
      } catch {
        return;
      }
      if (frame?.t !== TYPE_DO || !Array.isArray(frame.verbs)) return;
      const verbs = frame.verbs as string[];
      asked.push(verbs);

      // The same stub that answers the HTTP read, so a test describes its world
      // once and both paths agree about it.
      const verb = verbs[verbs.length - 1];
      const answer = pet.acted
        ? pet.acted(verb)
        : typeof pet.state === 'function'
          ? pet.state()
          : undefined;
      // A REFUSAL IS NOT SENT BACK, and nor is a broken read. The socket owes no
      // reply at all: the server's answer to a refused verb is a line over the
      // player's own Ваня, which a test pushes as a roster frame, so the shape
      // `stubBackend` would have turned into a 409 becomes silence here.
      //
      // Discriminated on `code`, which is what a `Refusal` carries and a state
      // does not — the same discriminator the HTTP branch uses. It used to look
      // for a `refusal` field that nothing has ever set, so a refused verb was
      // answered with a `vanyagotchi_state` frame carrying a status code where
      // the pet should be: a frame the real server cannot produce, in the one
      // test whose comment says the fake sends nothing.
      if (!answer || 'code' in answer) return;
      route.send(JSON.stringify({ t: TYPE_STATE, state: answer }));
    });
    resolveReady();
  });

  return {
    async push(payload: string) {
      await ready;
      ws?.send(payload);
    },
    asked: () => asked.map((v) => [...v]),
  };
}

/**
 * One entry of a roster frame. `art`, `label`, `pose` and `say` are optional
 * because they are optional on the wire — `label` is omitted outright for a Ваня
 * with no name — and every combination has to draw somebody.
 *
 * `say` is the line the SERVER decided to put over this entity's head: what a
 * verb did, or why it was refused, or idle muttering. It belongs here because it
 * is how a verb is answered — there is no reply channel — so a test that presses
 * one describes the answer by pushing a roster frame carrying it.
 */
interface Peer {
  id: string;
  x: number;
  y: number;
  art?: string;
  label?: string;
  pose?: string;
  say?: string;
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
/** A lifetime tally on the yard screen: a number in its own row, never a bar. */
const tallyValue = (page: Page, key: string) => page.locator(`[data-test="tally-value-${key}"]`);
const actionBtn = (page: Page, key: string) => page.locator(`[data-test="action-${key}"]`);

/**
 * Every action the shipped catalogue carries, so a layout assertion checks the
 * whole row rather than whichever two buttons it was written against.
 */
const ACTION_KEYS = [DRINK.key, RELIEVE.key, REVIVE.key];

/** The death notice — the one line the screen writes rather than the catalogue. */
const DEATH_LINE = 'Ваня не выдержал. Откачай его.';

/** Loads the game and steps past the intro into the yard. */
async function enterYard(page: Page): Promise<void> {
  await page.goto('/app/game-vanyagotchi');
  await page.getByRole('button', { name: 'Во двор' }).click();
  await expect(plane(page)).toBeVisible();
}

test.describe('«Ванягоччи» — the pet on the yard screen', () => {
  test('the bars, the numbers and all three actions come from the catalogue', async ({ page }) => {
    // Nothing on this screen is spelled out in the SPA: the labels, the bounds
    // and the buttons' wording all arrive from GET /config, which is what makes
    // "adding a stat is a backend-only change" true rather than aspirational.
    await stubBackend(page, {
      config: CATALOGUE,
      state: () =>
        stateOf({ hp: 72, beer: 44, bladder: 18, beers_drunk: 12, shits_taken: 3 }),
    });
    await stubSocket(page);
    await enterYard(page);

    await expect(page.locator('[data-test="pet-stats"]')).toBeVisible();
    // FIVE stats on the wire and THREE bars, which is the point: the two
    // lifetime tallies are not bars and must not be counted as though a fourth
    // and fifth track had appeared. The three that remain are the game — hp is
    // the consequence of what beer and the bladder do to him.
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
    // row iterates the catalogue rather than naming a verb. Three of them now,
    // and the third arrived as a catalogue entry with no client change, which is
    // the property this assertion is really about.
    await expect(page.locator('.actions .v-btn')).toHaveCount(3);
    await expect(actionBtn(page, 'drink')).toContainText('выпить пива');
    await expect(actionBtn(page, 'relieve')).toContainText('покакать');
    await expect(actionBtn(page, 'revive')).toContainText('восстать из мертвых');
  });

  test('a lifetime tally is a number, not a bar', async ({ page }) => {
    // A counter's scale runs to a million so that the clamp has something to
    // clamp to. Drawn as a bar, «выпито пива: 12» is an empty track that will
    // still be an empty track after a year of drinking — the player would read
    // it as "nearly none" when it means "twelve". So a counter gets a tally row
    // of its own, and a bar row is exactly what must NOT exist for it.
    await stubBackend(page, {
      config: CATALOGUE,
      state: () =>
        stateOf({ hp: 61, beer: 33, bladder: 44, beers_drunk: 12, shits_taken: 3 }),
    });
    await stubSocket(page);
    await enterYard(page);

    await expect(page.locator('[data-test="pet-tallies"]')).toBeVisible();
    await expect(tallyValue(page, 'beers_drunk')).toHaveText('12');
    await expect(tallyValue(page, 'shits_taken')).toHaveText('3');
    // Named from the catalogue, like everything else on this screen.
    await expect(page.locator('[data-test="tally-beers_drunk"]')).toContainText('выпито пива');
    await expect(page.locator('[data-test="tally-shits_taken"]')).toContainText('покакано раз');

    // And no track anywhere for either of them.
    await expect(page.locator('[data-test="stat-beers_drunk"]')).toHaveCount(0);
    await expect(page.locator('[data-test="stat-shits_taken"]')).toHaveCount(0);
    // Nor is a total ever trouble: `warn_at` sits on the floor and the value is
    // above it, which under the ordinary bar rule (`good_high`, value < warn_at)
    // would be fine — but a screen that ran the rule at all on a stat sitting
    // exactly AT nought would paint a fresh player's tally amber on day one.
    await expect(page.locator('[data-test="pet-tallies"] [data-trouble="1"]')).toHaveCount(0);
  });

  test('a tally that has never moved still reads as zero rather than as trouble', async ({
    page,
  }) => {
    // The day-one case, and the one a bar rule gets wrong: a brand-new Ваня has
    // drunk nothing, so both totals sit exactly on `warn_at`. Nothing about that
    // is a warning, and the row still has to appear — a counter that hid itself
    // until it moved would make the first beer look like a new feature.
    await stubBackend(page, {
      config: CATALOGUE,
      state: () => stateOf({ hp: 65, beer: 60, bladder: 0, beers_drunk: 0, shits_taken: 0 }),
    });
    await stubSocket(page);
    await enterYard(page);

    await expect(tallyValue(page, 'beers_drunk')).toHaveText('0');
    await expect(tallyValue(page, 'shits_taken')).toHaveText('0');
    await expect(page.locator('[data-test="pet-tallies"] [data-trouble="1"]')).toHaveCount(0);
  });

  test('the screen still never scrolls now that the pet panel is on it', async ({ page }) => {
    // The layout rule this game inherited is literal: one flexible child, the
    // rest fixed, `overflow: hidden`. The panel below the plane keeps growing —
    // three bars, then a tally row, then a third button — so the plane has to
    // give up the height rather than the panel being pushed off, which is
    // exactly the regression a growing pet panel is most likely to cause. The
    // state carries the tallies so the row under test is actually on the screen:
    // a counter with no value is skipped, and a layout test against a panel that
    // quietly shed a row proves nothing.
    await stubBackend(page, {
      config: CATALOGUE,
      state: () =>
        stateOf({ hp: 61, beer: 33, bladder: 44, beers_drunk: 12, shits_taken: 3 }),
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
    for (const key of ACTION_KEYS) {
      await expectOnScreen(page, actionBtn(page, key), `the ${key} button`);
    }
    await expectOnScreen(page, page.locator('[data-test="pet-tallies"]'), 'the tally row');
    await expectOnScreen(page, page.getByText(/во дворе:/), 'the status row');
  });

  test('the pet panel still fits the smallest screen we support', async ({ page }) => {
    // 320x568 is the floor — an iPhone SE in portrait, and the size at which the
    // fixed rows come closest to eating the plane entirely. Set before `goto`
    // rather than resized afterwards, the same way the sibling spec pins the
    // disclaimer, so the layout is built for this size rather than reflowed into
    // it. Three bars, a tally row and THREE buttons is the tallest the panel has
    // ever been, and this is the assertion that decides whether the next thing
    // added to it fits — the panel is the tightest part of this screen and the
    // right answer to "it no longer fits" is to make the row smaller, never to
    // relax what is checked here.
    await page.setViewportSize({ width: 320, height: 568 });
    await stubBackend(page, {
      config: CATALOGUE,
      state: () =>
        stateOf({ hp: 61, beer: 33, bladder: 44, beers_drunk: 128, shits_taken: 64 }),
    });
    await stubSocket(page);
    await enterYard(page);
    await expect(page.locator('[data-test="pet-stats"]')).toBeVisible();
    await expect(page.locator('[data-test="pet-tallies"]')).toBeVisible();

    await expectNoOverflow(page, 'vanyagotchi yard at 320x568');
    await expectNoVerticalScroll(page, 'vanyagotchi yard at 320x568');
    for (const key of ACTION_KEYS) {
      await expectOnScreen(page, actionBtn(page, key), `the ${key} button`);
    }
    await expectOnScreen(page, page.locator('[data-test="pet-tallies"]'), 'the tally row');
    await expectOnScreen(page, page.getByText(/во дворе:/), 'the status row');
    // And the plane it is sharing the screen with has not been squeezed to
    // nothing to make room.
    const box = await plane(page).boundingBox();
    expect(box?.height ?? 0, 'the plane collapsed to make room for the panel').toBeGreaterThan(120);
  });

  test('every action is a thumb-sized target', async ({ page }) => {
    await stubBackend(page, {
      config: CATALOGUE,
      state: () =>
        stateOf({ hp: 61, beer: 33, bladder: 44, beers_drunk: 12, shits_taken: 3 }),
    });
    await stubSocket(page);
    await enterYard(page);

    if (isMobile(page)) {
      // Vuetify's default button is 36px tall; the view overrides it precisely
      // because this floor is enforced rather than requested. THREE buttons now
      // share one row on a 320px screen, so the width is the half that could go
      // wrong — and it is the half that gets worse every time a verb is added.
      for (const key of ACTION_KEYS) {
        await expectTapTarget(actionBtn(page, key), `${key} action`);
      }
    }
  });

  test('drinking posts once and moves all three bars and the tally from the answer', async ({
    page,
  }) => {
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
          ? stateOf({ hp: 61, beer: 88, bladder: 47, beers_drunk: 7 })
          : stateOf({ hp: 26, beer: 4, bladder: 12, beers_drunk: 6 }),
      acted: () => {
        acted = true;
        // Deliberately not 26+15, 4+40, 12+25: a client applying the effects
        // itself would land on 41/44/37 and a client that trusted the server
        // lands here, so the two cannot both pass. The tally is the one effect
        // where the two would AGREE — 6+1 either way — so it is checked for a
        // different property: that a counter is redrawn from the answer at all,
        // rather than being read once and left where the first fetch put it.
        return stateOf({ hp: 61, beer: 88, bladder: 47, beers_drunk: 7 });
      },
    });
    const socket = await stubSocket(page);
    await enterYard(page);
    await expect(statValue(page, 'hp')).toHaveText('26');
    await expect(statValue(page, 'beer')).toHaveText('4');
    await expect(tallyValue(page, 'beers_drunk')).toHaveText('6');

    await actionBtn(page, 'drink').click();

    await expect(statValue(page, 'hp')).toHaveText('61');
    await expect(statValue(page, 'beer')).toHaveText('88');
    await expect(statValue(page, 'bladder')).toHaveText('47');
    await expect(tallyValue(page, 'beers_drunk')).toHaveText('7');
    // What the verb DID is the server's to say, and it says it in the BALLOON —
    // where the rest of the yard reads it too — rather than in a caption only
    // its owner sees. The catalogue's own `done` string, which the SPA does not
    // know and could not have invented.
    await socket.push(roster({ id: 'me', x: 0.5, y: 0.5, say: 'хорошо пошло' }));
    await expect(page.locator('[data-test="peer-say"]')).toHaveText('хорошо пошло');
    // Exactly one verb, over the SOCKET — and no HTTP at all, which is the
    // whole of the move: the action endpoint is gone.
    expect(socket.asked()).toEqual([['drink']]);
    expect(calls.posts).toEqual([]);
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

  test('relieving sends its own verb and empties the bladder', async ({ page }) => {
    // The second verb, and the proof the row is really iterating the catalogue:
    // the frame carries the action's own key, so pressing the second button
    // must send `relieve` and nothing else.
    let acted = false;
    const calls = await stubBackend(page, {
      config: CATALOGUE,
      state: () =>
        acted
          ? stateOf({ hp: 70, beer: 50, bladder: 0, shits_taken: 5 })
          : stateOf({ hp: 70, beer: 50, bladder: 92, shits_taken: 4 }),
      acted: () => {
        acted = true;
        // Clamped to the floor by the server: the catalogue's −100 is larger
        // than the whole scale, which is how it says "reset". The tally beside
        // it goes the other way, up by one, which is the point of the pair — the
        // bar resets and the total never does.
        return stateOf({ hp: 70, beer: 50, bladder: 0, shits_taken: 5 });
      },
    });
    const socket = await stubSocket(page);
    await enterYard(page);
    await expect(statValue(page, 'bladder')).toHaveText('92');
    await expect(tallyValue(page, 'shits_taken')).toHaveText('4');
    await expect(page.locator('[data-test="stat-bladder"][data-trouble="1"]')).toHaveCount(1);

    await actionBtn(page, 'relieve').click();

    await expect(statValue(page, 'bladder')).toHaveText('0');
    await expect(tallyValue(page, 'shits_taken')).toHaveText('5');
    await socket.push(roster({ id: 'me', x: 0.5, y: 0.5, say: 'полегчало' }));
    await expect(page.locator('[data-test="peer-say"]')).toHaveText('полегчало');
    expect(socket.asked()).toEqual([['relieve']]);
    expect(calls.posts).toEqual([]);
    // And the bar stopped reading as trouble, because 0 is the good end of a
    // stat whose `good_high` is false.
    await expect(page.locator('[data-test="stat-bladder"][data-trouble="1"]')).toHaveCount(0);
  });

  test('a refusal reaches the player as a line, never as the global modal', async ({ page }) => {
    // A VERB OWES NO REPLY, so a refusal cannot be an error code the client
    // catches — it arrives the way everything else in this game does, as STATE:
    // a line over the player's own Ваня that the whole yard can read. What must
    // never happen is the generic "something went wrong" modal appearing over a
    // situation the game is already explaining in words.
    await stubBackend(page, {
      config: CATALOGUE,
      state: () =>
        stateOf(
          { hp: 0, beer: 0, bladder: 96 },
          // A truthful rate for a Ваня with both needs unmet: 1 of his own plus
          // 6 for the beer plus 6 for the bladder.
          { alive: false, diedAt: '2026-07-25T03:00:00Z', rates: { hp: 13 } },
        ),
      // A corpse is refused EVERY verb but the one that carries
      // `revives_fatal`, which is now exactly one of them — beer stopped
      // reviving, so drinking is refused here too. The socket fake sends nothing
      // back for a refusal, because the server owes no reply.
      acted: (verb) =>
        verb === 'revive'
          ? stateOf({ hp: 65, beer: 60, bladder: 0 })
          : { status: 409, code: 'pet_dead' },
    });
    const socket = await stubSocket(page);
    await enterYard(page);
    await actionBtn(page, 'relieve').click();
    expect(socket.asked(), 'the verb never left the page').toEqual([['relieve']]);

    // The server's answer, in the world rather than in a response body.
    await socket.push(roster({ id: 'me', x: 0.5, y: 0.5, pose: 'dead', say: 'он не встаёт' }));
    await expect(page.locator('[data-test="peer-say"]')).toHaveText('он не встаёт');
    await expect(page.getByText('Ой, ошибка')).toHaveCount(0);

    // Still playable: the ONE verb that can revive him is still there to press,
    // and it is a verb of its own rather than a side effect of the button the
    // player was pressing anyway.
    await expect(actionBtn(page, 'revive')).toBeEnabled();
  });

  test('reviving starts him over on the catalogue values, and spares the tallies', async ({
    page,
  }) => {
    // THE new verb, end to end. `starts_over` means the server ignores the
    // action's (empty) effects and puts every bar back to its catalogue `start`
    // — so the answer is 65/60/0 rather than the numbers he died holding plus
    // anything. The counters are exempt, which is what makes them lifetime
    // totals: a Ваня who comes back having drunk nothing would be a lie about
    // the past rather than a fresh beginning.
    let revived = false;
    const calls = await stubBackend(page, {
      config: CATALOGUE,
      state: () =>
        revived
          ? stateOf({ hp: 65, beer: 60, bladder: 0, beers_drunk: 41, shits_taken: 9 })
          : stateOf(
              { hp: 0, beer: 0, bladder: 96, beers_drunk: 41, shits_taken: 9 },
              { alive: false, diedAt: '2026-07-25T03:00:00Z', rates: { hp: 13 } },
            ),
      acted: () => {
        revived = true;
        return stateOf({ hp: 65, beer: 60, bladder: 0, beers_drunk: 41, shits_taken: 9 });
      },
    });
    const socket = await stubSocket(page);
    await enterYard(page);
    await expect(petLine(page)).toHaveText(DEATH_LINE);
    await expect(statValue(page, 'hp')).toHaveText('0');

    await actionBtn(page, 'revive').click();

    await expect(statValue(page, 'hp')).toHaveText('65');
    await expect(statValue(page, 'beer')).toHaveText('60');
    await expect(statValue(page, 'bladder')).toHaveText('0');
    // Forty-one beers ago is still forty-one beers ago.
    await expect(tallyValue(page, 'beers_drunk')).toHaveText('41');
    await expect(tallyValue(page, 'shits_taken')).toHaveText('9');
    // And the death notice is gone, because he is not dead any more.
    await expect(petLine(page)).toHaveText('');
    expect(socket.asked()).toEqual([['revive']]);
    expect(calls.posts).toEqual([]);
  });

  test('a double-tap sends one verb, not two', async ({ page }) => {
    // A double-fire is a real bug on a touchscreen. There is no request to hold
    // open any more — the verb is a socket send and returns immediately — so the
    // guard is a short cooldown rather than an in-flight flag, and what is worth
    // asserting is the only thing that matters: the server heard one verb.
    const calls = await stubBackend(page, {
      config: CATALOGUE,
      state: () => stateOf({ hp: 40, beer: 30, bladder: 20 }),
      acted: () => stateOf({ hp: 55, beer: 70, bladder: 45 }),
    });
    const socket = await stubSocket(page);
    await enterYard(page);
    await expect(statValue(page, 'hp')).toHaveText('40');

    const drink = actionBtn(page, 'drink');
    await drink.click();
    await drink.click({ force: true });
    await drink.click({ force: true });

    // The state still arrives, from the one verb that got through.
    await expect(statValue(page, 'hp')).toHaveText('55');
    expect(socket.asked(), 'a double-tap became more than one verb').toEqual([['drink']]);
    // And nothing went over HTTP at all — the action endpoint is gone.
    expect(calls.posts).toEqual([]);
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
      counter: false,
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
      starts_over: false,
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
    await expect(actionBtn(page, 'revive')).toHaveCount(0);
    // No tally row either, because this catalogue declares no counter — the row
    // is driven by the `counter` flag rather than by the screen knowing that a
    // game called Ванягоччи happens to count beers.
    await expect(page.locator('[data-test="pet-tallies"]')).toHaveCount(0);
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
    await expect(page.locator('[data-test="pet-tallies"]')).toHaveCount(0);
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

  // -------------------------------------------------------------------------
  // The splash is a RULES CHEATSHEET, and its point is that it cannot go stale.
  // -------------------------------------------------------------------------

  test('the splash teaches the rules it was served', async ({ page }) => {
    await stubBackend(page, {
      config: CATALOGUE,
      state: () => stateOf({ hp: 65, beer: 60, bladder: 0 }),
    });
    await stubSocket(page);
    await page.goto('/app/game-vanyagotchi');

    const rules = page.locator('[data-test="rules"]');
    await expect(rules).toBeVisible();

    // A draining stat reads as falling and a FILLING one as rising, which is the
    // sign flip between the catalogue's drain and what a player watches the bar
    // do. Printed straight through, this would tell them the bladder empties
    // itself and health climbs.
    await expect(page.locator('[data-test="rule-stat-hp"]')).toContainText('старт 65, −1 в час');
    await expect(page.locator('[data-test="rule-stat-bladder"]')).toContainText('старт 0, +5 в час');

    // The causal half of the game: what actually kills him is neglect of the
    // other two, and the cheatsheet has to say so or the bars are a mystery.
    await expect(page.locator('[data-test="rule-stat-hp"]')).toContainText('ещё −6 в час, пока пиво ≤ 20');
    await expect(page.locator('[data-test="rule-stat-hp"]')).toContainText(
      'ещё −6 в час, пока мочевой пузырь ≥ 80',
    );
    await expect(page.locator('[data-test="rule-stat-hp"]')).toContainText('помер');

    // Actions, with the reset idiom spelled out as the bound it lands on. Beer
    // no longer revives — a corpse refuses it like everything else — so the note
    // under it has to have followed the catalogue rather than staying cheerful.
    await expect(page.locator('[data-test="rule-action-drink"]')).toContainText('пиво +40');
    await expect(page.locator('[data-test="rule-action-drink"]')).toContainText('выпито пива +1');
    await expect(page.locator('[data-test="rule-action-drink"]')).toContainText('мёртвому нельзя');
    await expect(page.locator('[data-test="rule-action-drink"]')).not.toContainText(
      'поднимает мёртвого',
    );
    await expect(page.locator('[data-test="rule-action-relieve"]')).toContainText('мочевой пузырь → 0');
    await expect(page.locator('[data-test="rule-action-relieve"]')).toContainText('мёртвому нельзя');

    // The verb that is the whole of the death rule. Its effects list is EMPTY on
    // the wire, because `starts_over` ignores it — so a cheatsheet that rendered
    // it the ordinary way would tell the player that the one button he needs
    // when дядя Ваня is dead moves nothing at all. What it says instead is
    // derived from the catalogue's own `start` values.
    const revive = page.locator('[data-test="rule-action-revive"]');
    await expect(revive).toContainText('восстать из мертвых');
    await expect(revive).toContainText('всё заново: здоровье → 65 · пиво → 60 · мочевой пузырь → 0');
    await expect(revive).toContainText('единственный способ поднять мёртвого');
    // The tallies are NOT in the reset, because a total death wiped would not be
    // a lifetime total.
    await expect(revive).not.toContainText('выпито пива');

    // A counter is not a thing that ticks, so it is not in the section that
    // promises everything under it does. «выпито пива — старт 0, сам не
    // меняется» is a drift line saying there is no drift, sitting next to the
    // stats that actually kill him.
    await expect(page.locator('[data-test="rules-stats"]')).not.toContainText('выпито пива');
    await expect(page.locator('[data-test="rule-stat-beers_drunk"]')).toHaveCount(0);
    await expect(page.locator('[data-test="rule-stat-shits_taken"]')).toHaveCount(0);
    // It has a section of its own instead, saying the one thing about a tally
    // that nothing else on this screen says.
    await expect(page.locator('[data-test="rule-counter-beers_drunk"]')).toContainText(
      'выпито пива',
    );
    await expect(page.locator('[data-test="rule-counter-beers_drunk"]')).toContainText(
      'даже начав заново, его не обнулишь',
    );
    await expect(page.locator('[data-test="rule-counter-shits_taken"]')).toContainText(
      'покакано раз',
    );

    // And the part no catalogue carries.
    await expect(page.locator('[data-test="rules-prose"]')).toContainText('пока вкладка закрыта');
  });

  test('a retuned catalogue retunes the cheatsheet with it', async ({ page }) => {
    // THE assertion this screen exists for. The rules are DERIVED from the
    // served catalogue rather than typed into the template, so moving a number
    // in internal/gamevanyagotchi/content.go moves what the player is told with
    // no client change at all. Nothing else here checks that: every other
    // assertion would pass equally well against a hand-written cheatsheet, and
    // a hand-written one would be wrong the first afternoon somebody retuned a
    // constant — silently, because nothing compares the two.
    await stubBackend(page, {
      config: catalogueOf(
        [
          { ...HP, start: 42, decay_per_hour: 9, penalties: [] },
          { ...BEER, label: 'самогон', start: 11 },
          BLADDER,
        ],
        [{ ...DRINK, effects: [{ stat_key: 'beer', delta: 7 }] }, REVIVE],
      ),
      state: () => stateOf({ hp: 42, beer: 60, bladder: 0 }),
    });
    await stubSocket(page);
    await page.goto('/app/game-vanyagotchi');

    await expect(page.locator('[data-test="rule-stat-hp"]')).toContainText('старт 42, −9 в час');
    await expect(page.locator('[data-test="rule-action-drink"]')).toContainText('самогон +7');
    // What a revival lands on is derived from the same `start` values, so
    // retuning what дядя Ваня comes back with retunes the line that promises it.
    // This is the assertion that would catch somebody typing 65/60/0 into the
    // cheatsheet the first time the derived version looked inconvenient.
    await expect(page.locator('[data-test="rule-action-revive"]')).toContainText(
      'всё заново: здоровье → 42 · самогон → 11 · мочевой пузырь → 0',
    );
    // The shipped numbers are gone, so this is the cheatsheet reading its input
    // rather than reciting what happens to be true today.
    await expect(page.locator('[data-test="rule-stat-hp"]')).not.toContainText('старт 65');
    await expect(page.locator('[data-test="rule-action-drink"]')).not.toContainText('+40');
    await expect(page.locator('[data-test="rule-action-revive"]')).not.toContainText('→ 65');
  });

  test('a catalogue that never arrives costs the derived rows and nothing else', async ({
    page,
  }) => {
    // The config fetch is deliberately allowed to fail — the yard runs on the
    // socket — so the splash must degrade to the prose rather than render an
    // empty box or refuse to open.
    await stubBackend(page, {
      config: 'fail',
      state: () => stateOf({ hp: 65, beer: 60, bladder: 0 }),
    });
    await stubSocket(page);
    await page.goto('/app/game-vanyagotchi');

    await expect(page.locator('[data-test="rules-prose"]')).toBeVisible();
    await expect(page.locator('[data-test="rules-stats"]')).toHaveCount(0);
    await expect(page.locator('[data-test="rules-counters"]')).toHaveCount(0);
    await expect(page.locator('[data-test="rules-actions"]')).toHaveCount(0);
    // Still playable: the CTA is what matters and it is still there.
    await expect(page.getByRole('button', { name: 'Во двор' })).toBeVisible();
    await expectNoOverflow(page, 'vanyagotchi splash with no catalogue');
  });
});
