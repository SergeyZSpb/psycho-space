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
/** A request to stand somewhere. Mirrored from message.go. */
const TYPE_MOVE = 'vanyagotchi_move';
/** A verb frame on its way to the server. Mirrored from message.go. */
const TYPE_DO = 'vanyagotchi_do';
/**
 * A request to stand in another PLACE. Mirrored from message.go.
 *
 * Its own frame rather than a verb, and mirrored as one here for the same reason
 * every other constant in this block is: going somewhere moves no stat and is not
 * refused on a corpse, so folding it into `vanyagotchi_do` would have been a verb
 * that none of the verb rules applied to.
 */
const TYPE_GOTO = 'vanyagotchi_goto';
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
  /**
   * The world-object kind this verb leaves standing in the yard, or absent for a
   * verb that leaves nothing. `omitempty` on the wire, hence optional here.
   *
   * A KEY, resolved against `object_kinds` — which is the whole reason the splash
   * can state what «покакать» now does to the yard, and for how long, without a
   * word of it being typed into the SPA.
   */
  leaves?: string;
  /**
   * A stat this verb is gated on, and how much of it the pet needs, or absent
   * for a verb that can be pressed whenever.
   *
   * Both `omitempty` on the wire, hence optional here. Mirrored because the pair
   * is a RULE the player meets as a button that appears to do nothing: the
   * server answers «рано ещё» and applies nothing, and the splash is the only
   * place he can be told why in advance. Nothing in the client enforces it — the
   * button is not disabled and the verb is still sent — so this pair reaching
   * the SPA is entirely about what the cheatsheet says.
   */
  needs_stat?: string;
  needs_at_least?: number;
  /**
   * A world-object KIND this verb needs the pet to be standing beside, and the
   * kind it races other players for. Both `omitempty` on the wire.
   *
   * Unlike `needs_stat` above, `needs_near` IS enforced in the browser: the
   * button greys when he is not at the thing. The asymmetry is the point and it
   * is not inconsistency — a stat is interpolated here and read there, so the
   * two ends genuinely hold different numbers between frames, whereas a position
   * is not interpolated by this client at all. The roster states where everybody
   * is, five times a second, on the same frame that carries the store, so the
   * browser and the server read ONE number.
   *
   * The client must never look at the VALUE of either — it holds no content keys
   * — which is a property this suite pins directly.
   */
  needs_near?: string;
  contests?: string;
  /**
   * This verb is a SEARCH. Mirrored from the wire, where the server DERIVES it
   * from the kind the verb races for being hidden — a fact deliberately not
   * published, because saying which kinds are hidden would say that the key is.
   *
   * The client reads it for its PRESENCE and never compares a key, which is the
   * property this suite pins: a fixture can rename the verb to anything at all
   * and the yard must still find it.
   */
  needs_spot?: boolean;
}

/**
 * A kind of durable thing that can stand on the plane.
 *
 * Mirrored from internal/gamevanyagotchi/content.go like every other fixture in
 * this block. `lifetime_seconds` rather than a Go duration, because a nanosecond
 * count is not a number any client should have to know how to read; zero means
 * forever. `label` is optional and the shipped kind carries none — a caption over
 * a deposit would be one more thing to draw on a small screen.
 */
interface ObjectKindDef {
  key: string;
  art: string;
  label?: string;
  lifetime_seconds: number;
  /**
   * How many draws a freshly spawned one carries, for a kind that is drawn down
   * rather than won outright. `omitempty`, so every other kind omits it.
   *
   * Served — unlike the contest DISCIPLINE beside it in content.go, which is not
   * — because it is a number the player plays against rather than a mechanism:
   * «шесть на всех» is a rule of the game, and the splash derives that sentence
   * from here instead of somebody typing the six out and forgetting it after the
   * next retune.
   */
  stock?: number;
}

/**
 * One candidate hiding place, mirrored from internal/gamevanyagotchi/content.go.
 *
 * The list of them is deliberately PUBLIC and the answer deliberately is not:
 * every place a key might be under is served here, drawn on the plane and named
 * on the splash, while which one it is actually under is picked server-side at
 * spawn and published nowhere at all. Which is why there is no fixture in this
 * file for "the key" — it is not an entity on the roster any more, and a test
 * that gave itself one would be describing a server that no longer exists.
 */
interface HotspotDef {
  key: string;
  label: string;
  emoji: string;
  at: { x: number; y: number };
}

/**
 * A location, with the places you can search in it.
 *
 * `hotspots` is per location from the start rather than a flat list, because a
 * hiding place belongs to a place: the yard has its bushes and the лифт will
 * have its own, and the client filters by the pet's `location_key`.
 */
interface LocationDef {
  key: string;
  label: string;
  entry: { x: number; y: number };
  hotspots?: HotspotDef[];
}

interface ConfigFixture {
  game_key: string;
  title: string;
  stats: StatDef[];
  actions: ActionDef[];
  /** `image` is present only for a skin the asset store has a blob for. */
  skins: { key: string; label: string; emoji: string; gradient: string; image?: string }[];
  /**
   * What a durable thing standing in the yard can be. Served so the splash can
   * say what a verb leaves behind; nothing renders from it, because an object
   * arrives in the roster as an ordinary entity with an art key.
   */
  object_kinds: ObjectKindDef[];
  locations: LocationDef[];
  /**
   * How close «beside it» is, in plane widths — the one number a `needs_near`
   * gate turns on.
   *
   * Served so that the button this client greys and the verb the server refuses
   * turn on the SAME threshold rather than two that agree until somebody retunes
   * one. Mirrored with the shipped 0.12 for that reason and not because any
   * assertion depends on the value: what the tests below pin is that the client
   * uses whatever it is told.
   */
  arrive_within: number;
  /** The art key the beer store is drawn with, resolved against `skins`. */
  store_art?: string;
  /** The constant sign over the beer store. */
  store_label?: string;
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

/**
 * The third tally, and it arrived with the thing that can move it.
 *
 * Identical in shape to the other two, which is the point worth mirroring: a key
 * hunt is a contested race decided by a partial unique index in the database, and
 * the whole of its reward is still an ordinary stat with a rate of nought. It
 * cost no migration and no new kind of number, so a screen that renders a tally
 * needs nothing new to render this one either.
 */
const KEYS_FOUND: StatDef = {
  key: 'keys_found',
  label: 'найдено ключей',
  emoji: '🔑',
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

/**
 * The crate of beer, with the shipped stock and the shipped "forever".
 *
 * NAMED, unlike the deposit above, and the label is load-bearing rather than
 * decorative: the two sentences the splash derives from this kind are both about
 * WHICH thing to walk to, so a nameless crate yields no note at all rather than
 * «нужно стоять рядом: кое-что», which is a fetch quest and not a rule.
 */
const CRATE_KIND: ObjectKindDef = {
  key: 'beer_crate',
  art: 'obj_crate',
  label: 'ящик пива',
  lifetime_seconds: 0,
  stock: 6,
};

/**
 * The beer store as the roster publishes it: a place and a count.
 *
 * ONE SHARED FIELD on the frame rather than one per peer, because it is one fact
 * about the world — and a STRUCTURE rather than a kind key, which is what lets
 * this client gate a button while still holding no content at all. The
 * coordinates are the shipped pitch; what matters to the tests is only that they
 * are further than `arrive_within` from where a Ваня starts.
 */
const STORE = { x: 0.82, y: 0.22, left: CRATE_KIND.stock ?? 6 };

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
  // AND BEER NOW HAS TO COME FROM SOMEWHERE. Two preconditions that refuse for
  // different reasons on purpose: he can be at the crate and find it empty, or
  // hold the whole yard's beer at arm's length and be too far to reach it.
  // Telling the player which is the difference between walking over and waiting.
  needs_near: CRATE_KIND.key,
  contests: CRATE_KIND.key,
};

/**
 * What «покакать» leaves standing in the yard, with the shipped ten minutes.
 *
 * Unnamed on purpose, exactly as the catalogue has it: nobody needs telling what
 * it is, and a caption would be one more thing on a phone. Mirrored here so the
 * splash assertions see the shape the real server serves — without it the
 * cheatsheet would quietly stop mentioning a rule that everybody in the yard can
 * see, and this suite would agree with the omission.
 */
const RELIEF_KIND: ObjectKindDef = {
  key: 'relief',
  art: 'obj_relief',
  lifetime_seconds: 600,
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
  // And it stays where he left it, for everybody to walk past. A KEY rather than
  // anything describing the thing: the sentence the splash builds out of it comes
  // from the kind, one constant up.
  leaves: RELIEF_KIND.key,
  // And it is the one verb with a PRECONDITION: press it on an empty bladder and
  // the server answers «рано ещё» and applies nothing. Mirrored with the shipped
  // 15 because the splash's job is to say so in advance — a gate a player only
  // discovers by pressing a button that does nothing is a rule nobody was told.
  needs_stat: 'bladder',
  needs_at_least: 15,
  revives_fatal: false,
  starts_over: false,
};

/**
 * What is hidden in the yard for somebody to find, with the shipped "forever".
 *
 * Mirrored for the shape rather than for anything derived from it, and that is
 * exactly what makes it worth having here. A deposit's kind is reachable from the
 * verb that leaves it (`RELIEVE.leaves`), so the splash builds a whole sentence
 * out of it with nothing typed by hand. The searching verb leaves NOTHING — it
 * takes something — and it carries no stock either, so nothing about the hunt is
 * derived from this kind at all. A fixture that quietly dropped it would make
 * that look like an oversight rather than the shape of the catalogue.
 *
 * IT IS NEVER DRAWN. The key is hidden under one of the yard's hiding places and
 * the server publishes neither which one nor the key itself, so this kind
 * describes a thing no roster frame carries — which is why there is no
 * `{ id: 'obj-key' }` peer anywhere in either spec.
 *
 * `lifetime_seconds: 0` is the catalogue's word for forever: a hunt ends when it
 * is won and never by a timer.
 */
const KEY_KIND: ObjectKindDef = {
  key: 'key',
  art: 'obj_key',
  label: 'ключи',
  lifetime_seconds: 0,
};

/**
 * The contested verb: one key, one winner, and a loser who pays nothing.
 *
 * The tally is the only thing in `effects`, and it is the only thing there CAN
 * be — losing costs no stat at all, deliberately, so that another player turning
 * up is never bad news. That works out for free rather than by a special case: a
 * claim that loses the race is refused outright, and a refused batch writes
 * nothing. Which is why the fixture for the loser's half of this verb is not
 * here at all — it is a pose on the roster, in the sibling spec.
 *
 * `contests` WITH NO `needs_near` IS WHAT IDENTIFIES IT, and the pair is the
 * load-bearing part of this fixture rather than decoration. The client holds no
 * content keys and does not know this verb is called «claim»: it reads the
 * catalogue's shape instead, and the shape is a real distinction — «выпить пива»
 * races other players for the crate AND says where to stand, because a crate is
 * something you can see, while this races them for something HIDDEN, where
 * standing anywhere in particular is the question rather than the answer. So a
 * contested verb with no place to walk to is the verb you search with. Drop the
 * `contests` here and the yard stops offering hiding places altogether, which is
 * exactly what the tests below would then report.
 */
const CLAIM: ActionDef = {
  key: 'claim',
  label: 'искать ключи',
  emoji: '🔑',
  effects: [{ stat_key: 'keys_found', delta: 1 }],
  done: 'нашёл ключи',
  // A dead Ваня finds nothing.
  revives_fatal: false,
  starts_over: false,
  contests: 'key',
  // AND IT IS A SEARCH, which is what puts the hiding places on the plane and
  // takes this verb out of the action row. Derived server-side from the key
  // being a hidden kind; mirrored here because this suite serves its own
  // catalogue.
  needs_spot: true,
};

/**
 * The yard's hiding places, and the whole of what a browser is told about where
 * a key might be.
 *
 * Placed far enough apart that Playwright can click one without another being
 * the element under the cursor, and far enough from `ACROSS_THE_YARD` that a
 * Ваня standing there has genuinely not arrived at any of them — both are
 * properties the tests below lean on rather than incidental spacing.
 */
const BUSH: HotspotDef = { key: 'bush', label: 'куст', emoji: '🌳', at: { x: 0.28, y: 0.3 } };
const BIN: HotspotDef = { key: 'bin', label: 'мусорка', emoji: '🗑️', at: { x: 0.75, y: 0.62 } };
const DOOR: HotspotDef = { key: 'door', label: 'подъезд', emoji: '🚪', at: { x: 0.5, y: 0.1 } };
const HOTSPOTS = [BUSH, BIN, DOOR];

/**
 * The лес's own hiding places, which exist to be NOT the yard's.
 *
 * The whole point of a per-location hotspot list is that no place lends another
 * its bushes: a flat list would behave identically while there was one location
 * and would send a player walking across the двор to search something that is in
 * the лес. Deliberately overlapping the yard's coordinates — the stump sits
 * exactly where the bush does — so a test that filtered by POSITION rather than
 * by place would still pass and a test that reads the keys cannot.
 */
const STUMP: HotspotDef = { key: 'stump', label: 'пень', emoji: '🪵', at: { x: 0.28, y: 0.3 } };
const FIR: HotspotDef = { key: 'fir', label: 'ёлка', emoji: '🌲', at: { x: 0.75, y: 0.62 } };
const LES_HOTSPOTS = [STUMP, FIR];

/**
 * The world, as the catalogue serves it: four places, and not all of them with
 * something to search in.
 *
 * `lift` carries no hotspots ON PURPOSE. A location with no hunt in it is a
 * perfectly good location — you can still walk there and still meet people — and
 * it is what the splash's «мест, где искать» line has to leave out, so a fixture
 * without one could not tell the two counts apart.
 */
const YARD: LocationDef = {
  key: 'yard',
  label: 'двор',
  entry: { x: 0.5, y: 0.5 },
  hotspots: HOTSPOTS,
};
const LES: LocationDef = {
  key: 'les',
  label: 'лес',
  entry: { x: 0.5, y: 0.9 },
  hotspots: LES_HOTSPOTS,
};
const LIFT: LocationDef = { key: 'lift', label: 'лифт', entry: { x: 0.5, y: 0.5 } };
const ZABROSHKA: LocationDef = {
  key: 'zabroshka',
  label: 'заброшка',
  entry: { x: 0.2, y: 0.4 },
  hotspots: [{ key: 'hole', label: 'дыра', emoji: '🕳️', at: { x: 0.6, y: 0.4 } }],
};
const LOCATIONS = [YARD, LES, LIFT, ZABROSHKA];

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
  stats: [HP, BEER, BLADDER, BEERS_DRUNK, KEYS_FOUND, SHITS_TAKEN],
  actions: [DRINK, RELIEVE, CLAIM, REVIVE],
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
  object_kinds: [RELIEF_KIND, KEY_KIND, CRATE_KIND],
  locations: LOCATIONS,
  arrive_within: 0.12,
  // The store's picture, named ONCE in the catalogue rather than on the frame —
  // a constant has no business on a payload sent five times a second.
  store_art: CRATE_KIND.art,
  // The sign, derived server-side from the kind's own label so there is one
  // name for the thing rather than two that drift.
  store_label: CRATE_KIND.label,
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
   * Which place he is standing in. The yard unless a test says otherwise, which
   * is what a fresh Ваня gets — and what every assertion written before there
   * were four places assumed without having to say so.
   */
  locationKey?: string;
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
const DEFS: StatDef[] = [HP, BEER, BLADDER, BEERS_DRUNK, KEYS_FOUND, SHITS_TAKEN];

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
      location_key: opts.locationKey ?? YARD.key,
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
  /**
   * Every frame the page sent, parsed and in order — hellos, moves and verbs
   * alike.
   *
   * `asked` above is a projection of this and stays because most of the file
   * only cares which verbs were pressed. The key hunt is the one thing here that
   * cares about a frame's OTHER fields: which point a tap walked him to, and
   * which hiding place the claim named. Both are the message rather than the
   * verb, so neither is visible through `asked` at all.
   */
  sent: () => Record<string, unknown>[];
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
  const sent: Record<string, unknown>[] = [];

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
      // Recorded WHOLE and before anything is discriminated on, so a test can
      // read the fields a verb key does not carry — the point a move asked for,
      // the hiding place a claim named.
      sent.push(frame as Record<string, unknown>);
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
    sent: () => sent.map((frame) => ({ ...frame })),
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
  /**
   * Which PLACE this entity is standing in, or absent for the default one.
   *
   * Absent is the common case on the wire — most of the world is in the first
   * place most of the time, so the server omits the field rather than repeating
   * one key per entity five times a second — which is why every peer in this file
   * that says nothing is in the двор, exactly as it was before there were places.
   */
  loc?: string;
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
  return JSON.stringify({ t: TYPE_ROSTER, peers, here: { [YARD.key]: peers.length } });
}

/** A roster frame that counts several places at once, as a real one does. */
function rosterWorld(heads: Record<string, number>, ...peers: Peer[]): string {
  return JSON.stringify({ t: TYPE_ROSTER, peers, here: heads });
}

/**
 * The same frame, carrying the beer store — or deliberately carrying none.
 *
 * A separate builder rather than an optional first argument to `roster`, because
 * most of this file is about the pet rather than the yard and those frames
 * should keep saying nothing about a crate: a server that has not got one, and a
 * client being told so, is a real state and the tests that do not care about it
 * should exercise it rather than the other one.
 */
function rosterWithStore(
  store: { x: number; y: number; left: number } | undefined,
  ...peers: Peer[]
): string {
  return JSON.stringify({ t: TYPE_ROSTER, peers, here: { [YARD.key]: peers.length }, store });
}

/**
 * The unicast answer to a hello: which entity in the roster is you.
 *
 * The gate needs it. «Am I at the crate» is a question about ONE entity, and a
 * client that has not been told which one it is cannot answer it — which is why
 * `beside` reads a missing `youId` as "not beside anything" rather than
 * guessing. Pushing this is how a test says the handshake finished.
 */
function youAre(id: string): string {
  return JSON.stringify({ t: TYPE_YOU, id });
}

/** Where a Ваня has to stand to reach the crate: on it. */
const AT_THE_CRATE = { x: STORE.x, y: STORE.y };

/** And where he starts, which is most of a plane away from it. */
const ACROSS_THE_YARD = { x: 0.2, y: 0.8 };

/**
 * Walks the player's own Ваня to the crate, so that a verb gated on the place is
 * pressable at all.
 *
 * EVERY TEST THAT PRESSES «выпить пива» NEEDS THIS NOW, and that is the whole
 * shape of the iteration rather than an inconvenience of the harness: beer comes
 * out of a crate, so you have to be at the crate. It is two frames because the
 * gate is two questions — which entity am I, and where is it — and the client
 * refuses to guess at either.
 *
 * It waits for the button rather than for the frames, because what the caller
 * actually needs is a control it can click; asserting the enabling here also
 * means a test that merely wanted to press a button does not silently become a
 * test of the gate.
 */
async function standAtTheCrate(page: Page, socket: SocketHarness, id = 'me'): Promise<void> {
  await socket.push(youAre(id));
  await socket.push(rosterWithStore(STORE, { id, ...AT_THE_CRATE }));
  await expect(actionBtn(page, 'drink'), 'never got within reach of the crate').toBeEnabled();
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
 * Every action the ACTION ROW draws, so a layout assertion checks the whole row
 * rather than whichever two buttons it was written against.
 *
 * NOT EVERY ACTION IN THE CATALOGUE, and the one that is missing is the point.
 * «искать ключи» has no button any more: the yard's own hiding places are the
 * control, so a button beside them would be a second path to the same outcome.
 * It is still served, still in the cheatsheet, and still the verb the claim
 * carries — it is simply not something you press, which is why the row is three
 * wide and this list is derived from the row rather than from the catalogue.
 */
const ROW_ACTIONS = [DRINK, RELIEVE, REVIVE];
const ACTION_KEYS = ROW_ACTIONS.map((action) => action.key);

/** The death notice — the one line the screen writes rather than the catalogue. */
const DEATH_LINE = 'Ваня не выдержал. Откачай его.';

/** Loads the game and steps past the intro into the yard. */
async function enterYard(page: Page): Promise<void> {
  await page.goto('/app/game-vanyagotchi');
  await page.getByRole('button', { name: 'Во двор' }).click();
  await expect(plane(page)).toBeVisible();
}

test.describe('«Ванягоччи» — the pet on the yard screen', () => {
  test('the bars, the numbers and every action come from the catalogue', async ({ page }) => {
    // Nothing on this screen is spelled out in the SPA: the labels, the bounds
    // and the buttons' wording all arrive from GET /config, which is what makes
    // "adding a stat is a backend-only change" true rather than aspirational.
    await stubBackend(page, {
      config: CATALOGUE,
      state: () =>
        stateOf({ hp: 72, beer: 44, bladder: 18, beers_drunk: 12, keys_found: 5, shits_taken: 3 }),
    });
    await stubSocket(page);
    await enterYard(page);

    await expect(page.locator('[data-test="pet-stats"]')).toBeVisible();
    // SIX stats on the wire and THREE bars, which is the point: the three
    // lifetime tallies are not bars and must not be counted as though three more
    // tracks had appeared. The three that remain are the game — hp is the
    // consequence of what beer and the bladder do to him.
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
    // row iterates the catalogue rather than naming a verb, which is what makes
    // "adding a verb is a backend change" true rather than aspirational.
    //
    // THREE OF THE FOUR, and the exception is the one thing this row does decide
    // for itself. «искать ключи» is not pressed any more: the yard's hiding
    // places are the control, so the row leaves out the verb the plane already
    // offers rather than showing a second way to do one thing. It is left out by
    // the catalogue's SHAPE — a verb that races other players for something and
    // names no place to stand — and never by its key, which is what keeps the
    // browser holding no content at all; the sibling block below is where that
    // is pinned properly.
    await expect(page.locator('.actions .v-btn')).toHaveCount(3);
    await expect(actionBtn(page, 'drink')).toContainText('выпить пива');
    await expect(actionBtn(page, 'relieve')).toContainText('покакать');
    await expect(actionBtn(page, 'revive')).toContainText('восстать из мертвых');
    await expect(actionBtn(page, 'claim')).toHaveCount(0);
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
        stateOf({ hp: 61, beer: 33, bladder: 44, beers_drunk: 12, keys_found: 5, shits_taken: 3 }),
    });
    await stubSocket(page);
    await enterYard(page);

    await expect(page.locator('[data-test="pet-tallies"]')).toBeVisible();
    await expect(tallyValue(page, 'beers_drunk')).toHaveText('12');
    await expect(tallyValue(page, 'keys_found')).toHaveText('5');
    await expect(tallyValue(page, 'shits_taken')).toHaveText('3');
    // Named from the catalogue, like everything else on this screen.
    await expect(page.locator('[data-test="tally-beers_drunk"]')).toContainText('выпито пива');
    await expect(page.locator('[data-test="tally-keys_found"]')).toContainText('найдено ключей');
    await expect(page.locator('[data-test="tally-shits_taken"]')).toContainText('покакано раз');

    // And no track anywhere for any of them. The third one is the case worth
    // having: it is fed by a verb whose whole point is that it usually FAILS, so
    // a screen that drew it as a bar would show a race nobody wins as an empty
    // track next to two that fill.
    await expect(page.locator('[data-test="stat-beers_drunk"]')).toHaveCount(0);
    await expect(page.locator('[data-test="stat-keys_found"]')).toHaveCount(0);
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
      state: () =>
        stateOf({ hp: 65, beer: 60, bladder: 0, beers_drunk: 0, keys_found: 0, shits_taken: 0 }),
    });
    await stubSocket(page);
    await enterYard(page);

    await expect(tallyValue(page, 'beers_drunk')).toHaveText('0');
    await expect(tallyValue(page, 'keys_found')).toHaveText('0');
    await expect(tallyValue(page, 'shits_taken')).toHaveText('0');
    await expect(page.locator('[data-test="pet-tallies"] [data-trouble="1"]')).toHaveCount(0);
  });

  test('the screen still never scrolls now that the pet panel is on it', async ({ page }) => {
    // The layout rule this game inherited is literal: one flexible child, the
    // rest fixed, `overflow: hidden`. The panel below the plane keeps growing —
    // three bars, then a tally row, then a third and a fourth button, then a
    // third tally — so the plane has to give up the height rather than the panel
    // being pushed off, which is exactly the regression a growing pet panel is
    // most likely to cause. The state carries every tally so the row under test
    // is actually on the screen at its full width: a counter with no value is
    // skipped, and a layout test against a panel that quietly shed a row proves
    // nothing.
    await stubBackend(page, {
      config: CATALOGUE,
      state: () =>
        stateOf({ hp: 61, beer: 33, bladder: 44, beers_drunk: 12, keys_found: 5, shits_taken: 3 }),
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
    await expectOnScreen(page, page.locator('[data-test="status"]'), 'the status row');
  });

  test('the pet panel still fits the smallest screen we support', async ({ page }) => {
    // 320x568 is the floor — an iPhone SE in portrait, and the size at which the
    // fixed rows come closest to eating the plane entirely. Set before `goto`
    // rather than resized afterwards, the same way the sibling spec pins the
    // disclaimer, so the layout is built for this size rather than reflowed into
    // it. Three bars, THREE tallies and FOUR buttons is the tallest the panel
    // has ever been, and this is the assertion that decides whether the next
    // thing added to it fits — the panel is the tightest part of this screen and
    // the right answer to "it no longer fits" is to make the row smaller, never
    // to relax what is checked here. The tally row wrapping onto a second line
    // at this width is the DESIGNED failure rather than a defect: the plane
    // gives up another sixteen pixels, which is a better outcome than a label
    // truncated to something unreadable — so what this really pins is that the
    // plane can still afford to pay.
    await page.setViewportSize({ width: 320, height: 568 });
    await stubBackend(page, {
      config: CATALOGUE,
      state: () =>
        stateOf({
          hp: 61,
          beer: 33,
          bladder: 44,
          beers_drunk: 128,
          keys_found: 42,
          shits_taken: 64,
        }),
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
    await expectOnScreen(page, page.locator('[data-test="status"]'), 'the status row');
    // And the plane it is sharing the screen with has not been squeezed to
    // nothing to make room.
    const box = await plane(page).boundingBox();
    expect(box?.height ?? 0, 'the plane collapsed to make room for the panel').toBeGreaterThan(120);
  });

  test('every verb reads in full on the narrowest phones, without spilling out of its button', async ({
    page,
  }) => {
    // THE OWNER'S REPORT, ON A PHONE: the action labels did not fit. Four Russian
    // verbs share about 288px at 320, and Vuetify draws a button UPPERCASE,
    // letter-spaced, at 0.875rem, inside 16px of padding a side, with
    // `white-space: nowrap` — so «ВЫПИТЬ ПИВА» needed roughly three times the
    // ~66px it had. Nothing clipped it, which is why no existing assertion caught
    // it: `.v-btn` sets no `overflow`, so the text simply ran out of its button
    // and across the one beside it.
    //
    // MEASURED AS INK RATHER THAN AS A BOX, which is the only way to see that.
    // A `Range` over the content's own text nodes gives the union of the
    // rectangles the browser will actually paint glyphs into, so this fails for
    // BOTH failure modes at once — text spilling outside the button (what was
    // happening) and text cut off inside it (what a careless fix, an
    // `overflow: hidden` or a `-webkit-line-clamp`, would produce instead). A
    // check on the button's own box would notice neither.
    //
    // Both widths, because they fail differently: 320 is where the row is
    // tightest, and 360 is what most of the audience is actually holding.
    await stubBackend(page, {
      config: CATALOGUE,
      state: () =>
        stateOf({ hp: 61, beer: 33, bladder: 44, beers_drunk: 12, keys_found: 5, shits_taken: 3 }),
    });
    await stubSocket(page);

    for (const size of [
      { width: 320, height: 568 },
      { width: 360, height: 800 },
    ]) {
      // Sized before the page is built, then loaded fresh, rather than resized
      // afterwards — the discipline the rest of this file's layout assertions
      // follow, so what is measured is a layout built for this width instead of
      // one caught mid-reflow.
      await page.setViewportSize(size);
      await enterYard(page);
      const label = `${size.width}x${size.height}`;

      for (const action of ROW_ACTIONS) {
        const m = await page.evaluate((key) => {
          const btn = document.querySelector<HTMLElement>(`[data-test="action-${key}"]`);
          if (!btn) throw new Error(`no button for ${key}`);
          const content = btn.querySelector<HTMLElement>('.v-btn__content');
          if (!content) throw new Error(`no content box for ${key}`);
          // The union of the rectangles the text is painted into — the ink, not
          // the box that is supposed to hold it.
          const range = document.createRange();
          range.selectNodeContents(content);
          const ink = range.getBoundingClientRect();
          const box = btn.getBoundingClientRect();
          return {
            text: content.textContent?.replace(/\s+/g, ' ').trim() ?? '',
            overLeft: box.left - ink.left,
            overRight: ink.right - box.right,
            overTop: box.top - ink.top,
            overBottom: ink.bottom - box.bottom,
            width: box.width,
            height: box.height,
          };
        }, action.key);

        // The whole label really is in the DOM. Without this the rest would pass
        // against a client that had "fixed" the fit by shortening the words —
        // which it must never do: the labels are the SERVER's, out of the
        // catalogue, so an abbreviation invented here would be this screen making
        // up content and would go stale the moment a verb is renamed.
        expect(m.text, `the ${action.key} button is not showing its full label at ${label}`).toBe(
          `${action.emoji} ${action.label}`,
        );

        // A pixel of slack per edge, because a fractional layout size rounds and
        // a glyph's ink box is not its advance box. Anything past that is text
        // outside the button it belongs to.
        for (const [edge, over] of [
          ['left', m.overLeft],
          ['right', m.overRight],
          ['top', m.overTop],
          ['bottom', m.overBottom],
        ] as const) {
          expect(
            over,
            `«${m.text}» runs ${over.toFixed(1)}px past the ${edge} of its ${Math.round(m.width)}x${Math.round(m.height)} button at ${label}`,
          ).toBeLessThanOrEqual(1);
        }
      }

      // And the fix did not buy the fit by growing the panel: the screen still
      // does not scroll, and the plane still has a yard in it.
      await expectNoVerticalScroll(page, `vanyagotchi yard at ${label}`);
      await expectNoOverflow(page, `vanyagotchi yard at ${label}`);
      const box = await plane(page).boundingBox();
      expect(
        box?.height ?? 0,
        `the plane collapsed to ${Math.round(box?.height ?? 0)}px making room for the buttons at ${label}`,
      ).toBeGreaterThan(120);
    }
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
      // because this floor is enforced rather than requested. FOUR buttons now
      // share one row on a 320px screen, so the width is the half that could go
      // wrong — and it is the half that gets worse every time a verb is added.
      // The row is `auto-fit, minmax(64px, 1fr)`, so a fifth verb is the one that
      // wraps rather than the one that shrinks anybody below the floor.
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

    // Beer comes out of a crate, so he has to be at the crate before the button
    // will do anything at all.
    await standAtTheCrate(page, socket);
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

    await standAtTheCrate(page, socket);
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
    await expect(page.getByText('двор: 3')).toBeVisible();
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
    await expect(page.getByText('на связи')).toBeVisible();

    // AND THE PLACE CAPTION IS ABSENT RATHER THAN WRONG. Both halves of it come
    // off the catalogue — the name of the place, and through the place which of
    // the frame's head counts is ours — so with no catalogue there is nothing
    // true to write in it and nowhere for the sheet it opens to send him. «0»
    // over a yard with two people in it would be the worse answer.
    await expect(page.locator('[data-test="here"]')).toHaveCount(0);
    // The entities are drawn anyway, which is the whole claim of this test: the
    // frame says where everybody is standing and a client that has never seen the
    // catalogue can still draw them.
    await expect(page.locator('[data-peer="peer-a"]')).toBeVisible();

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
    // «Покакать» stopped being a private matter: it leaves a deposit standing
    // where you were, everybody in the yard walks past it, and it is gone ten
    // minutes later. BOTH HALVES OF THAT SENTENCE ARE DERIVED — the verb carries
    // the kind key, the kind carries the lifetime — so a player is told about it
    // without a word of it being typed into the SPA, and a retune in content.go
    // moves the line rather than leaving it lying about the yard.
    await expect(page.locator('[data-test="rule-action-relieve"]')).toContainText(
      'оставляет кое-что на земле: видно всем 10 минут',
    );
    // And only the verb that leaves something says so.
    await expect(page.locator('[data-test="rule-action-drink"]')).not.toContainText('оставляет');

    // The precondition, which is the one rule a player otherwise meets as a
    // button that does nothing: «покакать» on an empty bladder is refused with
    // «рано ещё» and applies not a single effect. DERIVED from the pair the
    // catalogue serves, so the threshold moving in content.go moves this line,
    // and it is stated BEFORE what the verb does — a rule about whether the
    // button works at all outranks a rule about what it works on.
    await expect(page.locator('[data-test="rule-action-relieve"]')).toContainText(
      'нужно накопить: мочевой пузырь от 15',
    );
    // And only the gated verb says it. «искать ключи» in particular is NOT
    // gated — losing a race costs nothing, so there is nothing to accumulate
    // first — and a cheatsheet that put a condition under it would be inventing
    // a rule the server does not have.
    await expect(page.locator('[data-test="rule-action-drink"]')).not.toContainText('нужно накопить');
    await expect(page.locator('[data-test="rule-action-claim"]')).not.toContainText('нужно накопить');

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

    // And the part no catalogue carries. The second line is the one the world
    // objects added: that not everything standing in the yard is somebody, and
    // that the things are left out of «во дворе». The exclusion is a server rule
    // — `props` in world.go appends them after the count is taken — and it is on
    // no wire at all, so the hand-maintained half of the cheatsheet is the only
    // place a player can be told.
    await expect(page.locator('[data-test="rules-prose"]')).toContainText('пока вкладка закрыта');
    await expect(page.locator('[data-test="rules-prose"]')).toContainText(
      'в счётчике народу не числится',
    );
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

test.describe('«Ванягоччи» — the beer store', () => {
  /** The one line the status row says about the crate. */
  const storeLine = (page: Page) => page.locator('[data-test="store"]');

  test('the drink is out of reach from across the yard, and the row says to walk', async ({
    page,
  }) => {
    // The whole of I9 in one assertion pair: beer comes out of a crate, so
    // distance stops being decorative. The button is greyed as a COURTESY — the
    // server refuses it regardless — and the row is what stops a greyed control
    // being a mystery, because "wait" and "walk over" are different instructions.
    await stubBackend(page, { config: CATALOGUE, state: () => stateOf({ beer: 30 }) });
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(youAre('me'));
    await socket.push(rosterWithStore(STORE, { id: 'me', ...ACROSS_THE_YARD }));

    await expect(actionBtn(page, 'drink')).toBeDisabled();
    await expect(storeLine(page)).toContainText('дойди');
    await expect(storeLine(page)).toContainText('6');
    // The verb never left the browser, which is the point of greying it.
    expect(socket.asked()).toEqual([]);
    await expectNoOverflow(page, 'vanyagotchi yard with the store out of reach');
  });

  test('walking to the crate is what makes the button work', async ({ page }) => {
    await stubBackend(page, { config: CATALOGUE, state: () => stateOf({ beer: 30 }) });
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(youAre('me'));
    await socket.push(rosterWithStore(STORE, { id: 'me', ...ACROSS_THE_YARD }));
    await expect(actionBtn(page, 'drink')).toBeDisabled();

    // He walks. Nothing else about the world changed — same crate, same stock —
    // so his position is the only thing that can have enabled it.
    await socket.push(rosterWithStore(STORE, { id: 'me', ...AT_THE_CRATE }));

    await expect(actionBtn(page, 'drink')).toBeEnabled();
    await expect(storeLine(page)).not.toContainText('дойди');
    await expect(storeLine(page)).toContainText('6');
  });

  test('an empty crate greys the button even standing on it, and says so', async ({ page }) => {
    // The other refusal, and deliberately NOT the same line: «пиво кончилось»
    // means wait, «далековато» means walk. One sentence covering both would tell
    // him to do neither.
    await stubBackend(page, { config: CATALOGUE, state: () => stateOf({ beer: 30 }) });
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(youAre('me'));
    await socket.push(rosterWithStore({ ...STORE, left: 0 }, { id: 'me', ...AT_THE_CRATE }));

    await expect(actionBtn(page, 'drink')).toBeDisabled();
    await expect(storeLine(page)).toContainText('пуст');
    await expect(storeLine(page)).not.toContainText('дойди');
  });

  test('a yard with no crate says nothing about one, and still refuses the drink', async ({
    page,
  }) => {
    // A real state rather than a defensive one: the frame omits the block
    // outright when the world holds no crate, and a server that predates the
    // field sends none either. The row must fall back to its usual two items.
    await stubBackend(page, { config: CATALOGUE, state: () => stateOf({ beer: 30 }) });
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(youAre('me'));
    await socket.push(rosterWithStore(undefined, { id: 'me', ...AT_THE_CRATE }));

    await expect(storeLine(page)).toHaveCount(0);
    await expect(actionBtn(page, 'drink')).toBeDisabled();
    await expectNoOverflow(page, 'vanyagotchi yard with no crate in it');
  });

  test('a verb that needs no place is never greyed by one', async ({ page }) => {
    // «покакать» is gated on a STAT, and that gate is deliberately not enforced
    // here at all. So the store being absent, empty, or across the yard must not
    // reach it — a version that greyed every button when the crate was out of
    // reach would pass every test above.
    await stubBackend(page, {
      config: CATALOGUE,
      state: () => stateOf({ beer: 30, bladder: 90 }),
    });
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(youAre('me'));
    await socket.push(rosterWithStore(undefined, { id: 'me', ...ACROSS_THE_YARD }));

    await expect(actionBtn(page, 'drink')).toBeDisabled();
    await expect(actionBtn(page, 'relieve')).toBeEnabled();
    await expect(actionBtn(page, 'revive')).toBeEnabled();
  });

  test('the gate turns on the served threshold rather than a number in the SPA', async ({
    page,
  }) => {
    // `arrive_within` is catalogue content, so retuning it in content.go must
    // move the client's idea of «beside it» with no client edit. Served as a
    // whole plane width here, which makes a Ваня standing across the yard near
    // enough — a client holding a hardcoded 0.12 would still grey the button.
    await stubBackend(page, {
      config: { ...CATALOGUE, arrive_within: 1.5 },
      state: () => stateOf({ beer: 30 }),
    });
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(youAre('me'));
    await socket.push(rosterWithStore(STORE, { id: 'me', ...ACROSS_THE_YARD }));

    await expect(actionBtn(page, 'drink')).toBeEnabled();
  });

  test('the splash says where to stand and how many are in it, from the catalogue', async ({
    page,
  }) => {
    // Both sentences are DERIVED — the verb names a kind, the kind carries its
    // label and its stock — so retuning `crateStock` changes what the player is
    // told with no client change. That is the property being pinned, and the
    // next test is what proves it rather than the number.
    await stubBackend(page, { config: CATALOGUE });
    await page.goto('/app/game-vanyagotchi');

    const row = page.locator('[data-test="rule-action-drink"]');
    await expect(row).toContainText('нужно стоять рядом: ящик пива');
    await expect(row).toContainText('ящик пива — 6 порций на всех');
    await expectNoOverflow(page, 'vanyagotchi splash with the beer store');
  });

  test('retuning the stock retunes the cheatsheet, with the numeral agreed', async ({ page }) => {
    await stubBackend(page, {
      config: {
        ...CATALOGUE,
        object_kinds: [RELIEF_KIND, KEY_KIND, { ...CRATE_KIND, stock: 2 }],
      },
    });
    await page.goto('/app/game-vanyagotchi');

    const row = page.locator('[data-test="rule-action-drink"]');
    await expect(row).toContainText('ящик пива — 2 порции на всех');
    await expect(row).not.toContainText('6 порций');
  });
});

test.describe('«Ванягоччи» — the key hunt', () => {
  /** One hiding place on the plane, by the key a claim would name. */
  const hotspot = (page: Page, key: string) => page.locator(`[data-spot="${key}"]`);
  /** All of them. */
  const hotspots = (page: Page) => page.locator('[data-test="hotspot"]');

  /** Every move the page asked for, in order. */
  const moves = (socket: SocketHarness) => socket.sent().filter((frame) => frame.t === TYPE_MOVE);
  /** Every verb frame, whole — so the hiding place it named can be read. */
  const claims = (socket: SocketHarness) => socket.sent().filter((frame) => frame.t === TYPE_DO);
  /** Every journey between places, whole — so the place it named can be read. */
  const journeys = (socket: SocketHarness) => socket.sent().filter((frame) => frame.t === TYPE_GOTO);

  /**
   * Opens the yard with a Ваня of our own standing a long way from anything.
   *
   * Two frames because the client refuses to guess at either half of "have I
   * arrived": which entity am I, and where is it. `ACROSS_THE_YARD` is further
   * than `arrive_within` from every hiding place in the fixture, so nothing has
   * been searched by accident before a test starts.
   */
  async function standInTheYard(
    page: Page,
    socket: SocketHarness,
    id = 'me',
    loc = YARD.key,
  ): Promise<void> {
    await socket.push(youAre(id));
    // The place is stated rather than left to the default, because the frame
    // carries the whole world and the browser draws one place of it: a Ваня put
    // in the двор while his pet is in the лес is simply not on the plane, which
    // is correct and would make every wait below time out for the wrong reason.
    await socket.push(
      rosterWorld({ [loc]: 1 }, { id, ...ACROSS_THE_YARD, ...(loc === YARD.key ? {} : { loc }) }),
    );
    await expect(dots(page)).toHaveCount(1);
  }

  /** Puts him exactly on a hiding place, which is what arriving looks like. */
  function standingAt(spot: HotspotDef, id = 'me') {
    return { id, x: spot.at.x, y: spot.at.y };
  }

  test('the yard draws a tap target at every hiding place the catalogue gave it', async ({
    page,
  }) => {
    // The list of places is deliberately public — it is drawn, and named on the
    // splash — while which one holds the key is not on any wire at all. So what
    // can be asserted here is exactly that: three places, at the coordinates the
    // catalogue put them, each big enough for a thumb.
    await stubBackend(page, { config: CATALOGUE, state: () => stateOf({ hp: 65 }) });
    const socket = await stubSocket(page);
    await enterYard(page);
    await standInTheYard(page, socket);

    await expect(hotspots(page)).toHaveCount(HOTSPOTS.length);
    for (const spot of HOTSPOTS) {
      const el = hotspot(page, spot.key);
      await expect(el).toBeVisible();
      await expect(el).toContainText(spot.emoji);
      // Named for anybody who is not looking at the emoji — a screen reader, or
      // a keyboard user tabbing round the yard.
      await expect(el).toHaveAttribute('aria-label', `искать: ${spot.label}`);
    }
  });

  test('a hiding place is placed where the catalogue put it, not where it happens to fit', async ({
    page,
  }) => {
    // The same `--x`/`--y` mapping the dots use, which is the whole reason
    // arrival works: a hiding place and a Ваня at the same coordinates are drawn
    // at the same point, so "he is standing on it" means the same thing to the
    // eye and to the gate.
    await stubBackend(page, { config: CATALOGUE, state: () => stateOf({ hp: 65 }) });
    const socket = await stubSocket(page);
    await enterYard(page);
    await standInTheYard(page, socket);

    const box = await plane(page).boundingBox();
    expect(box).not.toBeNull();
    for (const spot of HOTSPOTS) {
      const at = await hotspot(page, spot.key).boundingBox();
      expect(at, `no box for ${spot.key}`).not.toBeNull();
      const cx = (at?.x ?? 0) + (at?.width ?? 0) / 2 - (box?.x ?? 0);
      const cy = (at?.y ?? 0) + (at?.height ?? 0) / 2 - (box?.y ?? 0);
      // Two pixels of slack, because a fractional layout size rounds.
      expect(cx, `${spot.key} is at the wrong x`).toBeCloseTo(spot.at.x * (box?.width ?? 1), -0.5);
      expect(cy, `${spot.key} is at the wrong y`).toBeCloseTo(spot.at.y * (box?.height ?? 1), -0.5);
    }
  });

  test('every hiding place is a thumb-sized target at 360, and nothing overflows', async ({
    page,
  }) => {
    // THE first genuinely tappable thing inside this plane, which makes it the
    // first place the 44px rule really applies: a dot is `pointer-events: none`
    // and the plane takes every tap, so the world unit's 32px floor is a
    // legibility judgement and this is not. `--unit` bottoms out at 32 on a
    // phone, so the box has to be the larger of the two or the hunt would be
    // unplayable on exactly the device it is played on.
    await page.setViewportSize({ width: 360, height: 800 });
    await stubBackend(page, { config: CATALOGUE, state: () => stateOf({ hp: 65 }) });
    const socket = await stubSocket(page);
    await enterYard(page);
    await standInTheYard(page, socket);

    await expect(hotspots(page)).toHaveCount(HOTSPOTS.length);
    for (const spot of HOTSPOTS) {
      await expectTapTarget(hotspot(page, spot.key), `the ${spot.key} hiding place`);
    }
    await expectNoOverflow(page, 'vanyagotchi yard with hiding places at 360');
    await expectNoVerticalScroll(page, 'vanyagotchi yard with hiding places at 360');
  });

  test('a hiding place is not a person, and never lands in the head count', async ({ page }) => {
    // THE KEY IS NOT DRAWN, in the only sense a browser can be held to it: the
    // yard draws exactly the entities the roster listed and not one more, and
    // the hiding places are not among them. There is no `obj-key` peer in this
    // file because no server sends one — where the key is, is the answer, and
    // publishing it would make the hunt a one-line script — so what is checkable
    // here is that the client invents nothing in its place: no marker, no
    // pseudo-entity, and no hotspot quietly counted as somebody in the yard.
    await stubBackend(page, { config: CATALOGUE, state: () => stateOf({ hp: 65 }) });
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(youAre('me'));
    await socket.push(roster({ id: 'me', ...ACROSS_THE_YARD }, { id: 'сосед', x: 0.6, y: 0.5 }));

    await expect(hotspots(page)).toHaveCount(HOTSPOTS.length);
    // Two dots for two people, with three hiding places drawn among them.
    await expect(dots(page)).toHaveCount(2);
    await expect(page.getByText('двор: 2')).toBeVisible();
    // And a hiding place is not one of the things the yard counts as an entity.
    await expect(page.locator('[data-test="peer"][data-spot]')).toHaveCount(0);
  });

  test('tapping a hiding place walks him there, and does not also tap the ground', async ({
    page,
  }) => {
    // ONE MESSAGE FOR ONE GESTURE. The plane owns every pointerdown, so without
    // `.stop` on the hotspot a single finger would send two moves — the plane's,
    // to wherever the finger landed, and the hotspot's, to the catalogue's exact
    // point — and which of them won would come down to handler order. Worse, the
    // plane's would be the inaccurate one, and arrival is measured against the
    // hotspot's own coordinates: a tap a few pixels off centre could leave him a
    // hair outside `arrive_within` and have the claim refused «далековато» for a
    // reason nothing on screen explains.
    await stubBackend(page, { config: CATALOGUE, state: () => stateOf({ hp: 65 }) });
    const socket = await stubSocket(page);
    await enterYard(page);
    await standInTheYard(page, socket);

    await hotspot(page, BUSH.key).click();

    await expect.poll(() => moves(socket).length).toBe(1);
    const move = moves(socket)[0];
    // The catalogue's point EXACTLY, rather than close to it.
    expect(move.x).toBe(BUSH.at.x);
    expect(move.y).toBe(BUSH.at.y);
    // And nothing has been claimed yet: he has not gone anywhere.
    expect(claims(socket)).toEqual([]);
  });

  test('arriving is what searches the place, and it names the one that was tapped', async ({
    page,
  }) => {
    // The whole of I8d in one test. Tapping is a request to walk; the search is
    // sent when the roster says he got there, and it carries the hiding place so
    // the server can check it against where the key actually is. What the client
    // announces is a REQUEST — the server validates the spot against its own
    // placement and may answer «далековато» or «тут пусто» — so this pins the
    // shape of the message and never that it succeeds.
    await stubBackend(page, { config: CATALOGUE, state: () => stateOf({ hp: 65 }) });
    const socket = await stubSocket(page);
    await enterYard(page);
    await standInTheYard(page, socket);

    await hotspot(page, BIN.key).click();
    await expect.poll(() => moves(socket).length).toBe(1);
    // Still walking: a frame that puts him nearer but not there must not fire it.
    await socket.push(roster({ id: 'me', x: BIN.at.x - 0.3, y: BIN.at.y }));
    await expect(dots(page)).toHaveCount(1);
    expect(claims(socket)).toEqual([]);

    // He arrives.
    await socket.push(roster(standingAt(BIN)));

    await expect.poll(() => claims(socket).length).toBe(1);
    const claim = claims(socket)[0];
    expect(claim.verbs).toEqual([CLAIM.key]);
    expect(claim.spot).toBe(BIN.key);
  });

  test('a search is sent once, however many frames say he is standing there', async ({ page }) => {
    // The roster repeats five times a second, so "he is at the bin" is true on
    // every frame until he walks away. Re-sending on each of them would be this
    // client hammering a question it has already been answered — and «тут пусто»
    // is exactly the answer it would hammer, since the wrong place stays wrong.
    await stubBackend(page, { config: CATALOGUE, state: () => stateOf({ hp: 65 }) });
    const socket = await stubSocket(page);
    await enterYard(page);
    await standInTheYard(page, socket);

    await hotspot(page, BUSH.key).click();
    for (let i = 0; i < 6; i += 1) {
      // The yard grows by one each time, so every frame is provably DELIVERED
      // rather than merely sent — a "nothing more was claimed" assertion against
      // frames still sitting in the socket would pass for the wrong reason.
      await socket.push(
        roster(
          standingAt(BUSH),
          ...Array.from({ length: i }, (_, n) => ({ id: `peer-${n}`, x: 0.1 + n * 0.1, y: 0.9 })),
        ),
      );
      await expect(dots(page)).toHaveCount(i + 1);
    }

    expect(claims(socket)).toHaveLength(1);
  });

  test('a Ваня who never gets there never searches anything', async ({ page }) => {
    // THE reason distance costs something now. A long walk can end in «устал» —
    // he sits down where he gave up, and the server simply stops moving him — so
    // a search can fail by never arriving, which is what makes a far hiding place
    // a gamble rather than a longer wait. Nothing is claimed, and nothing is
    // queued up to be claimed later.
    await stubBackend(page, { config: CATALOGUE, state: () => stateOf({ hp: 65 }) });
    const socket = await stubSocket(page);
    await enterYard(page);
    await standInTheYard(page, socket);

    await hotspot(page, DOOR.key).click();
    await expect.poll(() => moves(socket).length).toBe(1);

    // He gets tired part way and stops, saying so — which is a roster frame like
    // any other, and the client is told nothing else about it.
    for (let i = 0; i < 4; i += 1) {
      await socket.push(
        roster(
          { id: 'me', x: 0.4, y: 0.55, say: 'нога отваливается' },
          ...Array.from({ length: i }, (_, n) => ({ id: `peer-${n}`, x: 0.1 + n * 0.1, y: 0.9 })),
        ),
      );
      await expect(dots(page)).toHaveCount(i + 1);
    }

    expect(claims(socket)).toEqual([]);
  });

  test('walking him somewhere else calls the search off', async ({ page }) => {
    // WHAT STOPS A STALE SEARCH FIRING MINUTES LATER. A tap on the ground cancels
    // the walk the search was riding on — that is the yard's oldest rule, and it
    // is why nobody can get stuck — so the intention on the end of it goes with
    // it. Left armed, the claim would go off the next time he happened to pass
    // within reach of a bush the player had stopped caring about: a search
    // nobody asked for, answered «тут пусто», at a moment that explains nothing.
    await stubBackend(page, { config: CATALOGUE, state: () => stateOf({ hp: 65 }) });
    const socket = await stubSocket(page);
    await enterYard(page);
    await standInTheYard(page, socket);

    await hotspot(page, BUSH.key).click();
    await expect.poll(() => moves(socket).length).toBe(1);

    // He changes his mind and taps the ground.
    const box = await plane(page).boundingBox();
    await page.mouse.click((box?.x ?? 0) + (box?.width ?? 0) * 0.85, (box?.y ?? 0) + (box?.height ?? 0) * 0.9);
    await expect.poll(() => moves(socket).length).toBe(2);

    // And then wanders past the bush anyway, which must now mean nothing at all.
    await socket.push(roster(standingAt(BUSH)));
    await socket.push(roster(standingAt(BUSH), { id: 'сосед', x: 0.9, y: 0.9 }));
    await expect(dots(page)).toHaveCount(2);

    expect(claims(socket)).toEqual([]);
  });

  test('tapping a second hiding place replaces the first rather than queueing it', async ({
    page,
  }) => {
    // A walk has one destination, so a search has one place. The yard already
    // plays by "a new tap always cancels the old", and arriving at the bin having
    // searched the bush would be the one shape that rule cannot produce.
    await stubBackend(page, { config: CATALOGUE, state: () => stateOf({ hp: 65 }) });
    const socket = await stubSocket(page);
    await enterYard(page);
    await standInTheYard(page, socket);

    await hotspot(page, BUSH.key).click();
    await expect.poll(() => moves(socket).length).toBe(1);
    await hotspot(page, BIN.key).click();
    await expect.poll(() => moves(socket).length).toBe(2);
    // The second tap walked him to the second place.
    expect(moves(socket)[1].x).toBe(BIN.at.x);

    await socket.push(roster(standingAt(BIN)));

    await expect.poll(() => claims(socket).length).toBe(1);
    expect(claims(socket)[0].spot).toBe(BIN.key);
  });

  test('the place he is walking to is the one the yard marks', async ({ page }) => {
    // A tap has to be visibly answered by something: the walk itself takes
    // seconds and the dot moves 220ms at a time, so without this the player
    // cannot tell a tap that registered from one that missed.
    await stubBackend(page, { config: CATALOGUE, state: () => stateOf({ hp: 65 }) });
    const socket = await stubSocket(page);
    await enterYard(page);
    await standInTheYard(page, socket);

    await hotspot(page, BUSH.key).click();
    await expect(hotspot(page, BUSH.key)).toHaveAttribute('data-seeking', '1');
    await expect(hotspot(page, BIN.key)).not.toHaveAttribute('data-seeking', '1');

    // And it stops being marked once the search has been made.
    await socket.push(roster(standingAt(BUSH)));
    await expect.poll(() => claims(socket).length).toBe(1);
    await expect(hotspot(page, BUSH.key)).not.toHaveAttribute('data-seeking', '1');
  });

  test('a catalogue with no verb to search with offers nothing to search', async ({ page }) => {
    // The hiding places are still served, and there is deliberately nothing to
    // tap: a bush that could only send a walk looks like the way to find keys and
    // is not. This is the older-server case — and the ambiguous-catalogue one,
    // where a second contested verb has left the shape identifying neither.
    await stubBackend(page, {
      config: catalogueOf(
        [HP, BEER, BLADDER, BEERS_DRUNK, KEYS_FOUND, SHITS_TAKEN],
        [DRINK, RELIEVE, REVIVE],
      ),
      state: () => stateOf({ hp: 65 }),
    });
    const socket = await stubSocket(page);
    await enterYard(page);
    await standInTheYard(page, socket);

    await expect(hotspots(page)).toHaveCount(0);
    // And the row is unfiltered, because there was nothing to leave out of it.
    await expect(page.locator('.actions .v-btn')).toHaveCount(3);
  });

  test('the yard offers the search, so the action row does not', async ({ page }) => {
    // NO SECOND PATH TO THE SAME OUTCOME. «искать ключи» used to be a button
    // pressable from anywhere, which is precisely what stopped the hunt being a
    // search — the key was drawn, so everybody could see it, and finding it was a
    // race to press. Removed rather than greyed: a control that can never be
    // enabled is not a control, it is a label taking a quarter of the tightest
    // row on the screen.
    await stubBackend(page, { config: CATALOGUE, state: () => stateOf({ hp: 65 }) });
    const socket = await stubSocket(page);
    await enterYard(page);
    await standInTheYard(page, socket);

    await expect(actionBtn(page, CLAIM.key)).toHaveCount(0);
    // The verb has not gone anywhere — it is still served, still the thing a
    // claim names — so this is the row declining to draw it rather than the
    // catalogue having dropped it.
    expect(CATALOGUE.actions.map((a) => a.key)).toContain(CLAIM.key);
    // And nothing in the yard can be made to send a bare claim: the only frames
    // this screen sends unprompted are the hello and a move.
    expect(claims(socket)).toEqual([]);
  });

  test('the splash says the search moved onto the plane, and counts the world', async ({
    page,
  }) => {
    // A rules change that did not reach the cheatsheet is a rules change nobody
    // playing knows about. Three facts, and only one of them hardcoded: that the
    // verb has no button is a property of this screen, while how many PLACES have
    // something to search in, what they are called, and how many hiding places
    // there are between them all come straight off the catalogue — so adding a
    // bush, or a whole location, in content.go moves these sentences on its own.
    await stubBackend(page, { config: CATALOGUE });
    await page.goto('/app/game-vanyagotchi');

    const row = page.locator('[data-test="rule-action-claim"]');
    await expect(row).toContainText('не кнопка');
    await expect(row).toContainText('тапни укрытие');
    // Three of the four places, because the лифт has nothing in it to search —
    // and the count of hiding places is the sum across all three, not the yard's.
    await expect(row).toContainText('искать можно в 3 местах: двор · лес · заброшка');
    await expect(row).toContainText('всего 6 укрытий');
    await expect(row).not.toContainText('лифт');
    // And the hand-written half, which is where everything the wire cannot say
    // lives: that the key is hidden, that there is ONE of it for the whole world
    // rather than one per place, and what happens when you look in the wrong one.
    const prose = page.locator('[data-test="rules-prose"]');
    await expect(prose).toContainText('не видно никому');
    await expect(prose).toContainText('тут пусто');
    await expect(prose).toContainText('на все места сразу');
    await expect(prose).not.toContainText('кто первым нажал');
    // The travel rule, which is the other thing no catalogue field could say:
    // that places exist, that you see only the people in your own, and where the
    // control that moves you between them is.
    await expect(prose).toContainText('ходить между ними можно как угодно');
    await expect(prose).toContainText('только тех, кто стоит там же');
    await expectNoOverflow(page, 'vanyagotchi splash with the key hunt');
  });

  test('a retuned world retunes the cheatsheet with it', async ({ page }) => {
    // THE assertion the derivation exists for: both counts are numbers the player
    // plays against, and they are exactly the sort somebody changes by feel one
    // evening. A world of one place with one bush in it must not still be
    // described as four places with six.
    await stubBackend(page, {
      config: {
        ...CATALOGUE,
        locations: [
          {
            key: 'yard',
            label: 'двор',
            entry: { x: 0.5, y: 0.5 },
            hotspots: [{ key: 'lift', label: 'лифт', emoji: '🛗', at: { x: 0.5, y: 0.5 } }],
          },
        ],
      },
    });
    await page.goto('/app/game-vanyagotchi');

    const row = page.locator('[data-test="rule-action-claim"]');
    await expect(row).toContainText('искать можно в 1 месте: двор');
    await expect(row).toContainText('всего 1 укрытие');
    await expect(row).not.toContainText('лес');
  });

  // -------------------------------------------------------------------------
  // The hunt across four places. Everything above this line is the yard's own
  // hiding places; what follows is that they are the YARD's — a per-location list
  // that no other place may borrow, which is the half of `hotspotsFor` that was
  // built a location early and is now load-bearing.
  // -------------------------------------------------------------------------

  test('the hiding places are the ones where he is standing, not the default ones', async ({
    page,
  }) => {
    // THE assertion the per-location filter exists for. The лес's stump sits at
    // exactly the coordinates the yard's bush does, deliberately: a client that
    // filtered by position, or flattened every location's list into one, would
    // draw something plausible at the right place and send a claim naming the
    // wrong one — which the server answers «тут пусто» for a reason nothing on
    // screen explains.
    await stubBackend(page, {
      config: CATALOGUE,
      state: () => stateOf({ hp: 65 }, { locationKey: LES.key }),
    });
    const socket = await stubSocket(page);
    await enterYard(page);
    await standInTheYard(page, socket, 'me', LES.key);

    await expect(hotspots(page)).toHaveCount(LES_HOTSPOTS.length);
    for (const spot of LES_HOTSPOTS) {
      await expect(hotspot(page, spot.key)).toBeVisible();
    }
    for (const spot of HOTSPOTS) {
      await expect(hotspot(page, spot.key), `${spot.key} belongs to the двор`).toHaveCount(0);
    }
  });

  test('a place with nothing to search in offers nothing to tap', async ({ page }) => {
    // A location need not have a hunt in it, and the honest answer is a plane
    // with no tap targets rather than the default location's borrowed ones.
    await stubBackend(page, {
      config: CATALOGUE,
      state: () => stateOf({ hp: 65 }, { locationKey: LIFT.key }),
    });
    const socket = await stubSocket(page);
    await enterYard(page);
    await standInTheYard(page, socket, 'me', LIFT.key);

    await expect(hotspots(page)).toHaveCount(0);
    // The verb is still served and still absent from the action row: the row's
    // rule is about the CATALOGUE having a searching verb, not about this place
    // having somewhere to use it.
    await expect(actionBtn(page, CLAIM.key)).toHaveCount(0);
  });

  test('travelling changes which places can be searched', async ({ page }) => {
    // The two halves of I10 meeting: a journey is asked for over the socket, the
    // server answers with the pet, and the hunt on the plane follows it. Without
    // this the yard would keep the бушes of the place he left and every claim
    // from the new one would name a hiding place that is not there.
    let where = YARD.key;
    await stubBackend(page, {
      config: CATALOGUE,
      state: () => stateOf({ hp: 65 }, { locationKey: where }),
    });
    const socket = await stubSocket(page);
    await enterYard(page);
    await standInTheYard(page, socket);
    await expect(hotspot(page, BUSH.key)).toBeVisible();

    await page.locator('[data-test="here"]').click();
    await page.locator(`[data-place="${LES.key}"]`).click();

    // ONE `vanyagotchi_goto`, naming the place — and no verb, because going
    // somewhere is neither something a corpse can be refused nor something that
    // moves a stat, which is why it is a frame of its own rather than an action.
    await expect.poll(() => journeys(socket).length).toBe(1);
    expect(journeys(socket)[0].location).toBe(LES.key);
    expect(socket.asked(), 'a journey is not a verb').toEqual([]);

    // The server folds it and pushes the pet back, which is the only thing that
    // moves him: nothing is written optimistically here, exactly as no tap moves
    // a dot before the roster says it did.
    where = LES.key;
    await socket.push(
      JSON.stringify({ t: TYPE_STATE, state: stateOf({ hp: 65 }, { locationKey: LES.key }) }),
    );

    await expect(hotspot(page, STUMP.key)).toBeVisible();
    await expect(hotspot(page, BUSH.key)).toHaveCount(0);
    await expect(hotspots(page)).toHaveCount(LES_HOTSPOTS.length);
  });

  test('travelling calls off a search he was walking to', async ({ page }) => {
    // The claim is armed against a hiding place in the place he is LEAVING, and
    // the coordinates it is waiting for exist in every location. Left armed it
    // would fire the moment he happened to stand near the same point somewhere
    // else — a search of a bush in another world entirely, answered «тут пусто»
    // at a moment that explains nothing.
    await stubBackend(page, { config: CATALOGUE, state: () => stateOf({ hp: 65 }) });
    const socket = await stubSocket(page);
    await enterYard(page);
    await standInTheYard(page, socket);

    await hotspot(page, BUSH.key).click();
    await expect.poll(() => moves(socket).length).toBe(1);
    await expect(hotspot(page, BUSH.key)).toHaveAttribute('data-seeking', '1');

    await page.locator('[data-test="here"]').click();
    await page.locator(`[data-place="${LES.key}"]`).click();
    await expect.poll(() => socket.sent().filter((f) => f.t === 'vanyagotchi_goto').length).toBe(1);

    // He then stands exactly where the bush was — the stump is at those very
    // coordinates — and nothing is claimed.
    await socket.push(roster(standingAt(BUSH)));
    await socket.push(roster(standingAt(BUSH), { id: 'сосед', x: 0.9, y: 0.9 }));
    await expect(dots(page)).toHaveCount(2);

    expect(claims(socket)).toEqual([]);
  });
});
