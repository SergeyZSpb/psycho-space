import { describe, expect, it } from 'vitest';

import type {
  VanyagotchiAction,
  VanyagotchiConfig,
  VanyagotchiObjectKind,
  VanyagotchiStat,
} from '../api/types';
import { YARD_PROSE, buildRules } from '../lib/vanyagotchiRules';

// The splash cheatsheet is derived from the served catalogue rather than typed
// out, so that retuning a constant in internal/gamevanyagotchi/content.go
// changes what the player is told with no client edit. These tests are what
// makes that claim checkable: they pin the DERIVATION, not the numbers, so a
// retune keeps them green and a broken derivation does not.

const stat = (over: Partial<VanyagotchiStat> = {}): VanyagotchiStat => ({
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
  ...over,
});

/**
 * A lifetime tally, with the shape the catalogue actually gives one: a rate of
 * nought, a bound a million away that exists only so the clamp has something to
 * clamp to, and a `warn_at` sitting on the floor because no total is ever
 * trouble.
 */
const counter = (over: Partial<VanyagotchiStat> = {}): VanyagotchiStat =>
  stat({
    key: 'beers_drunk',
    label: 'выпито пива',
    emoji: '🍻',
    max: 1_000_000,
    start: 0,
    decay_per_hour: 0,
    warn_at: 0,
    counter: true,
    fatal: false,
    ...over,
  });

const action = (over: Partial<VanyagotchiAction> = {}): VanyagotchiAction => ({
  key: 'drink',
  label: 'выпить пива',
  emoji: '🍺',
  effects: [{ stat_key: 'beer', delta: 40 }],
  done: 'выпил',
  revives_fatal: false,
  starts_over: false,
  ...over,
});

/**
 * A durable thing a verb can leave standing in the yard, shaped as the shipped
 * one is: unnamed, because a caption over a deposit would be one more thing to
 * draw, and gone after ten minutes.
 */
const objectKind = (over: Partial<VanyagotchiObjectKind> = {}): VanyagotchiObjectKind => ({
  key: 'relief',
  art: 'obj_relief',
  lifetime_seconds: 600,
  ...over,
});

/** The verb that leaves one behind: «покакать», with the catalogue's own key. */
const relieve = (over: Partial<VanyagotchiAction> = {}): VanyagotchiAction =>
  action({
    key: 'relieve',
    label: 'покакать',
    emoji: '💩',
    effects: [{ stat_key: 'bladder', delta: -100 }],
    done: 'полегчало',
    leaves: 'relief',
    ...over,
  });

/** The verb that is the way back from a death: no effects at all, just a reset. */
const revive = (over: Partial<VanyagotchiAction> = {}): VanyagotchiAction =>
  action({
    key: 'revive',
    label: 'восстать из мертвых',
    emoji: '🧟',
    effects: [],
    done: 'воскрес',
    revives_fatal: true,
    starts_over: true,
    ...over,
  });

const config = (over: Partial<VanyagotchiConfig> = {}): VanyagotchiConfig =>
  ({
    game_key: 'vanyagotchi',
    title: 'Ванягоччи',
    stats: [],
    actions: [],
    skins: [],
    npcs: [],
    locations: [],
    default_skin: 'vanya',
    default_location: 'yard',
    ...over,
  }) as VanyagotchiConfig;

describe('buildRules — the drift line', () => {
  // THE one piece of arithmetic here that can be wrong while reading perfectly:
  // the catalogue's rate is a DRAIN, so the number the player watches the bar do
  // is its negation. Printed straight through, the cheatsheet would claim the
  // bladder empties itself.
  it('negates the drain, so a draining stat reads as falling', () => {
    const [row] = buildRules(config({ stats: [stat({ decay_per_hour: 1 })] })).stats;
    expect(row?.drift).toBe('старт 65, −1 в час');
  });

  it('negates a filling stat too, so the bladder reads as rising', () => {
    const [row] = buildRules(
      config({
        stats: [stat({ key: 'bladder', label: 'мочевой пузырь', start: 0, decay_per_hour: -5 })],
      }),
    ).stats;
    expect(row?.drift).toBe('старт 0, +5 в час');
  });

  it('says so when nothing happens on its own', () => {
    const [row] = buildRules(config({ stats: [stat({ decay_per_hour: 0 })] })).stats;
    expect(row?.drift).toBe('старт 65, сам не меняется');
  });

  it('uses a real minus sign rather than a hyphen', () => {
    const [row] = buildRules(config({ stats: [stat({ decay_per_hour: 1 })] })).stats;
    expect(row?.drift).toContain('−');
    expect(row?.drift).not.toContain('-');
  });
});

describe('buildRules — the notes under a stat', () => {
  it('names the DRIVING stat by its label, not its key', () => {
    const rules = buildRules(
      config({
        stats: [
          stat({ penalties: [{ when_key: 'beer', threshold: 20, above: false, rate_per_hour: 6 }] }),
          stat({ key: 'beer', label: 'пиво', emoji: '🍺', fatal: false }),
        ],
      }),
    );
    expect(rules.stats[0]?.notes).toContain('ещё −6 в час, пока пиво ≤ 20');
  });

  it('flips the comparison for an above-threshold penalty', () => {
    const rules = buildRules(
      config({
        stats: [
          stat({
            penalties: [{ when_key: 'bladder', threshold: 80, above: true, rate_per_hour: 6 }],
          }),
          stat({ key: 'bladder', label: 'мочевой пузырь', fatal: false }),
        ],
      }),
    );
    expect(rules.stats[0]?.notes).toContain('ещё −6 в час, пока мочевой пузырь ≥ 80');
  });

  it('falls back to the raw key when the driver is not in the catalogue', () => {
    const rules = buildRules(
      config({
        stats: [
          stat({ penalties: [{ when_key: 'ghost', threshold: 5, above: true, rate_per_hour: 2 }] }),
        ],
      }),
    );
    expect(rules.stats[0]?.notes.some((n) => n.includes('ghost'))).toBe(true);
  });

  it('takes which end is the bad end from good_high, as the bar colour does', () => {
    const high = buildRules(config({ stats: [stat({ good_high: true, warn_at: 30 })] }));
    const low = buildRules(config({ stats: [stat({ good_high: false, warn_at: 80 })] }));
    expect(high.stats[0]?.notes).toContain('бар желтеет ниже 30');
    expect(low.stats[0]?.notes).toContain('бар желтеет выше 80');
  });

  it('warns that a fatal stat is fatal, and stays quiet when it is not', () => {
    const fatal = buildRules(config({ stats: [stat({ fatal: true, min: 0 })] }));
    const harmless = buildRules(config({ stats: [stat({ fatal: false })] }));
    expect(fatal.stats[0]?.notes).toContain('дойдёт до 0 — всё, помер');
    expect(harmless.stats[0]?.notes.some((n) => n.includes('помер'))).toBe(false);
  });

  it('drops a penalty that does nothing rather than printing «ещё 0 в час»', () => {
    const rules = buildRules(
      config({
        stats: [
          stat({ penalties: [{ when_key: 'beer', threshold: 20, above: false, rate_per_hour: 0 }] }),
        ],
      }),
    );
    expect(rules.stats[0]?.notes.some((n) => n.startsWith('ещё'))).toBe(false);
  });
});

describe('buildRules — actions', () => {
  it('lists every effect against the stat it moves', () => {
    const rules = buildRules(
      config({
        stats: [
          stat({ key: 'beer', label: 'пиво' }),
          stat({ key: 'hp', label: 'здоровье' }),
        ],
        actions: [
          action({
            effects: [
              { stat_key: 'beer', delta: 40 },
              { stat_key: 'hp', delta: 15 },
            ],
          }),
        ],
      }),
    );
    expect(rules.actions[0]?.effects).toBe('пиво +40 · здоровье +15');
  });

  // A delta at least as wide as the scale is the catalogue's idiom for "reset",
  // and the clamp lands it exactly on the bound. «−100» against a bar that only
  // goes to 100 is arithmetic the player should not have to do.
  it('spells a full-scale delta as the bound it lands on', () => {
    const rules = buildRules(
      config({
        stats: [stat({ key: 'bladder', label: 'мочевой пузырь', min: 0, max: 100 })],
        actions: [
          action({ key: 'relieve', effects: [{ stat_key: 'bladder', delta: -100 }] }),
        ],
      }),
    );
    expect(rules.actions[0]?.effects).toBe('мочевой пузырь → 0');
  });

  // Reviving used to be a side effect of drinking, which made dying almost
  // invisible; it is now one verb of its own, and «единственный» is the half of
  // that rule the player has to know at the one moment the game asks him to
  // press a particular button. It is COUNTED off the catalogue rather than
  // asserted, so a second way back would soften the wording by itself instead of
  // leaving the screen quietly claiming exclusivity it no longer has.
  it('calls the one reviving action the only way back, and refuses the rest', () => {
    const rules = buildRules(
      config({
        actions: [
          action({ key: 'drink' }),
          action({ key: 'relieve' }),
          revive(),
        ],
      }),
    );
    expect(rules.actions[0]?.notes).toEqual(['мёртвому нельзя']);
    expect(rules.actions[1]?.notes).toEqual(['мёртвому нельзя']);
    expect(rules.actions[2]?.notes).toEqual(['единственный способ поднять мёртвого']);
  });

  it('drops the exclusivity claim as soon as a second action revives', () => {
    const rules = buildRules(
      config({ actions: [revive(), revive({ key: 'defibrillate', starts_over: false })] }),
    );
    expect(rules.actions[0]?.notes).toEqual(['поднимает мёртвого']);
    expect(rules.actions[1]?.notes).toEqual(['поднимает мёртвого']);
  });
});

describe('buildRules — a verb that leaves something standing in the yard', () => {
  // «Покакать» stopped being a private matter: it puts a deposit on the ground
  // where the server thinks you are, everybody in the yard walks past it, and it
  // is gone ten minutes later. That is a rule a player has to be told, and it is
  // derived rather than typed — the verb carries the kind key and the kind
  // carries the lifetime, both out of internal/gamevanyagotchi/content.go.
  const withRelief = (over: Partial<VanyagotchiObjectKind> = {}) =>
    config({ actions: [relieve()], object_kinds: [objectKind(over)] });

  it('says what the verb leaves and how long it lasts', () => {
    const [row] = buildRules(withRelief()).actions;
    expect(row?.notes[0]).toBe('оставляет кое-что на земле: видно всем 10 минут');
  });

  // THE assertion this derivation exists for. The ten minutes is a constant in
  // content.go, so shortening it is a backend deploy — and the screen that
  // teaches the rule has to move with it rather than keep promising ten.
  it('follows a retuned lifetime, so the sentence cannot go stale', () => {
    expect(buildRules(withRelief({ lifetime_seconds: 120 })).actions[0]?.notes[0]).toBe(
      'оставляет кое-что на земле: видно всем 2 минуты',
    );
    expect(buildRules(withRelief({ lifetime_seconds: 3600 })).actions[0]?.notes[0]).toBe(
      'оставляет кое-что на земле: видно всем 60 минут',
    );
    expect(buildRules(withRelief({ lifetime_seconds: 600 })).actions[0]?.notes[0]).not.toContain(
      '2 минуты',
    );
  });

  // Russian agrees the noun with the numeral, so a lifetime retuned to a minute
  // or two is the case a naive `${n} минут` gets visibly wrong — «1 минут» —
  // exactly when the derivation is doing its job.
  it.each<[number, string]>([
    [60, 'видно всем 1 минуту'],
    [120, 'видно всем 2 минуты'],
    [660, 'видно всем 11 минут'],
    [1260, 'видно всем 21 минуту'],
    [30, 'видно всем 30 секунд'],
    [1, 'видно всем 1 секунду'],
    [3, 'видно всем 3 секунды'],
  ])('agrees the unit with the number: %d seconds', (seconds, expected) => {
    expect(buildRules(withRelief({ lifetime_seconds: seconds })).actions[0]?.notes[0]).toContain(
      expected,
    );
  });

  // A deposit is deliberately unnamed and a lost key would not be, so the noun
  // is the one part of the sentence the catalogue may or may not supply.
  it('names the thing when the catalogue names it', () => {
    const [row] = buildRules(withRelief({ label: 'связка ключей' })).actions;
    expect(row?.notes[0]).toBe('оставляет «связка ключей» на земле: видно всем 10 минут');
  });

  // Zero is the catalogue's word for forever — such a row is never filtered out
  // on read — so the line must not print «видно всем 0 секунд», which says the
  // opposite of what the server does.
  it('reads a lifetime of nought as forever rather than as no time at all', () => {
    const [row] = buildRules(withRelief({ lifetime_seconds: 0 })).actions;
    expect(row?.notes[0]).toBe('оставляет кое-что на земле: видно всем, и оно уже никуда не денется');
  });

  // The warning about a corpse is still the last thing said, and the two notes
  // are ordered consequence-then-condition.
  it('keeps the death warning underneath it', () => {
    const [row] = buildRules(withRelief()).actions;
    expect(row?.notes).toEqual([
      'оставляет кое-что на земле: видно всем 10 минут',
      'мёртвому нельзя',
    ]);
  });

  it('says nothing about leavings for a verb that leaves none', () => {
    const rules = buildRules(
      config({ actions: [action({ key: 'drink' })], object_kinds: [objectKind()] }),
    );
    expect(rules.actions[0]?.notes).toEqual(['мёртвому нельзя']);
  });

  // A client older than the server can be handed a verb that leaves a kind it
  // has never heard of. Saying nothing is honest; «оставляет что-то» would be a
  // guess dressed up as a rule, and a player would go looking for it.
  it('stays quiet about a kind the catalogue does not describe', () => {
    const rules = buildRules(config({ actions: [relieve({ leaves: 'ghost' })], object_kinds: [] }));
    expect(rules.actions[0]?.notes).toEqual(['мёртвому нельзя']);
  });

  it('survives a catalogue with no object kinds at all', () => {
    const rules = buildRules(config({ actions: [relieve()] }));
    expect(rules.actions[0]?.notes).toEqual(['мёртвому нельзя']);
  });

  // Same tolerance the rest of the derivation has: a config fetch is allowed to
  // fail or to arrive from a server halfway through a deploy, and none of those
  // shapes may throw on the one screen a player has to get past to play.
  it.each<[string, VanyagotchiObjectKind[]]>([
    ['a list that is not an array', 'nope' as unknown as VanyagotchiObjectKind[]],
    ['an entry with no key', [{} as VanyagotchiObjectKind]],
    ['a null entry', [null as unknown as VanyagotchiObjectKind]],
    ['a kind with no lifetime at all', [{ key: 'relief', art: 'x' } as VanyagotchiObjectKind]],
  ])('degrades rather than throwing on %s', (_name, kinds) => {
    const rules = buildRules(config({ actions: [relieve()], object_kinds: kinds }));
    expect(rules.actions[0]?.notes.at(-1)).toBe('мёртвому нельзя');
    // Whatever it says, it never claims a duration it was not given.
    expect(rules.actions[0]?.notes.join(' ')).not.toMatch(/\d/);
  });
});

describe('buildRules — a verb the pet is not always allowed to use', () => {
  // «Покакать» is gated: the server refuses it with «рано ещё» unless the
  // bladder is at least 15, and applies nothing. That is a rule the player meets
  // as a button that appears to do nothing, so the cheatsheet has to state it —
  // and state it DERIVED, because the 15 is a constant in content.go and a
  // hand-typed copy would be wrong the first afternoon somebody retuned it.
  //
  // The gate is deliberately NOT on the shared `relieve()` fixture above. The
  // block about leavings asserts exact note lists, and adding a second note to
  // every one of them would make those tests about two rules at once; the last
  // test here is what covers the two appearing together on the real verb.
  const gated = (over: Partial<VanyagotchiAction> = {}) =>
    relieve({ needs_stat: 'bladder', needs_at_least: 15, ...over });
  const bladder = stat({ key: 'bladder', label: 'мочевой пузырь', emoji: '🚽', start: 0 });

  it('says what has to be accumulated before the verb will do anything', () => {
    const rules = buildRules(config({ stats: [bladder], actions: [gated()] }));
    expect(rules.actions[0]?.notes[0]).toBe('нужно накопить: мочевой пузырь от 15');
  });

  // THE assertion this derivation exists for: the threshold is a backend
  // constant, and the screen that teaches the rule has to move with it.
  it('follows a retuned threshold, so the sentence cannot go stale', () => {
    const rules = buildRules(
      config({ stats: [bladder], actions: [gated({ needs_at_least: 40 })] }),
    );
    expect(rules.actions[0]?.notes[0]).toBe('нужно накопить: мочевой пузырь от 40');
  });

  it('names the stat as the catalogue names it, never by its wire key', () => {
    const rules = buildRules(
      config({ stats: [stat({ key: 'bladder', label: 'терпение' })], actions: [gated()] }),
    );
    expect(rules.actions[0]?.notes[0]).toBe('нужно накопить: терпение от 15');
  });

  it('says nothing for a verb that can be pressed whenever', () => {
    const rules = buildRules(config({ stats: [bladder], actions: [action({ key: 'drink' })] }));
    expect(rules.actions[0]?.notes).toEqual(['мёртвому нельзя']);
  });

  // A client older than the server can be handed a gate on a stat it has never
  // heard of. Silence is honest; «нужно накопить: bladder от 15» would put a
  // wire key in front of a player as though it were a word.
  it('stays quiet about a stat the catalogue does not describe', () => {
    const rules = buildRules(config({ stats: [], actions: [gated()] }));
    expect(rules.actions[0]?.notes).toEqual(['мёртвому нельзя']);
  });

  // A gate at nought is satisfied by every value the stat can hold — the server
  // compares `>=` — so it is not a rule at all, and printing it would describe a
  // condition that can never fail.
  it.each<[string, Partial<VanyagotchiAction>]>([
    ['a threshold of nought', { needs_at_least: 0 }],
    ['a negative threshold', { needs_at_least: -5 }],
    ['no threshold at all', { needs_at_least: undefined }],
    ['a threshold that is not a number', { needs_at_least: 'много' as unknown as number }],
    ['an empty stat key', { needs_stat: '' }],
  ])('says nothing for %s', (_name, over) => {
    const rules = buildRules(config({ stats: [bladder], actions: [gated(over)] }));
    expect(rules.actions[0]?.notes).toEqual(['мёртвому нельзя']);
  });

  it('states the condition before what the verb does with it, on the real verb', () => {
    // The shipped «покакать», with both of its rules and in the order a player
    // needs them: what it needs first, because that is the one that decides
    // whether pressing the button does anything at all.
    const rules = buildRules(
      config({
        stats: [bladder],
        actions: [gated()],
        object_kinds: [objectKind()],
      }),
    );
    expect(rules.actions[0]?.notes).toEqual([
      'нужно накопить: мочевой пузырь от 15',
      'оставляет кое-что на земле: видно всем 10 минут',
      'мёртвому нельзя',
    ]);
  });
});

describe('buildRules — an action that starts him over', () => {
  // The server IGNORES `effects` when `starts_over` is set, which is why the
  // reviving verb ships with an empty list. Rendered by the ordinary path that
  // comes out as an empty string — a cheatsheet claiming the one verb a dead
  // player needs moves nothing at all.
  it('says what a reset lands on rather than that the verb moves nothing', () => {
    const rules = buildRules(
      config({
        stats: [
          stat({ key: 'hp', label: 'здоровье', start: 65 }),
          stat({ key: 'beer', label: 'пиво', start: 60, fatal: false }),
          stat({ key: 'bladder', label: 'мочевой пузырь', start: 0, fatal: false }),
        ],
        actions: [revive()],
      }),
    );
    expect(rules.actions[0]?.effects).toBe(
      'всё заново: здоровье → 65 · пиво → 60 · мочевой пузырь → 0',
    );
  });

  // The numbers come from the catalogue's own `start` values, which is the whole
  // reason the line is built rather than written: retuning what дядя Ваня comes
  // back with is a backend change, and this screen has to follow it.
  it('takes the values from the catalogue, so a retuned start retunes the line', () => {
    const rules = buildRules(
      config({ stats: [stat({ key: 'hp', label: 'здоровье', start: 12 })], actions: [revive()] }),
    );
    expect(rules.actions[0]?.effects).toBe('всё заново: здоровье → 12');
  });

  // A total that death reset would not be a lifetime total, so the reset spares
  // the counters — and a line that listed «выпито пива → 0» would promise the
  // opposite of what the server does.
  it('leaves the lifetime tallies out of the reset', () => {
    const rules = buildRules(
      config({
        stats: [stat({ key: 'hp', label: 'здоровье', start: 65 }), counter()],
        actions: [revive()],
      }),
    );
    expect(rules.actions[0]?.effects).toBe('всё заново: здоровье → 65');
    expect(rules.actions[0]?.effects).not.toContain('выпито пива');
  });

  // Whatever deltas such an action happens to carry are dead weight on the
  // server, so printing them would be describing an outcome that does not
  // happen.
  it('ignores the effects list entirely, exactly as the server does', () => {
    const rules = buildRules(
      config({
        stats: [stat({ key: 'hp', label: 'здоровье', start: 65 })],
        actions: [revive({ effects: [{ stat_key: 'hp', delta: 99 }] })],
      }),
    );
    expect(rules.actions[0]?.effects).toBe('всё заново: здоровье → 65');
    expect(rules.actions[0]?.effects).not.toContain('99');
  });
});

describe('buildRules — the lifetime tallies', () => {
  // «Тикает само» is a promise that everything under it drifts on its own, and a
  // counter is exactly the stat that does not. Listed there it would read as
  // «выпито пива — старт 0, сам не меняется», which is a drift line saying there
  // is no drift: noise at best, and at worst a player wondering why his beer
  // total is in the section about things that kill him.
  it('keeps a counter out of the stats that drift', () => {
    const rules = buildRules(
      config({ stats: [stat({ key: 'hp' }), counter()] }),
    );
    expect(rules.stats.map((row) => row.key)).toEqual(['hp']);
    expect(rules.counters.map((row) => row.key)).toEqual(['beers_drunk']);
  });

  it('gives a tally its name and no numbers at all', () => {
    const [row] = buildRules(config({ stats: [counter()] })).counters;
    expect(row?.label).toBe('выпито пива');
    expect(row?.emoji).toBe('🍻');
    // The bound is a million and the start is nought; both are plumbing rather
    // than rules, and printing either would teach the player an implementation
    // detail as if it were something to aim at.
    expect(row?.note).not.toMatch(/\d/);
  });

  // Surviving the one thing that wipes everything else is what makes a tally a
  // lifetime tally, and nothing else on the splash says so.
  it('promises a tally survives a reset when the catalogue has one', () => {
    const [row] = buildRules(config({ stats: [counter()], actions: [revive()] })).counters;
    expect(row?.note).toBe('только растёт: даже начав заново, его не обнулишь');
  });

  it('claims nothing about resets in a catalogue that has none', () => {
    const [row] = buildRules(
      config({ stats: [counter()], actions: [action({ key: 'drink' })] }),
    ).counters;
    expect(row?.note).toBe('только растёт');
  });

  // A counter is still a stat an action moves, so the verb that bumps it has to
  // name it in words: «beers_drunk +1» would be the catalogue leaking through.
  it('still labels a counter when an action increments it', () => {
    const rules = buildRules(
      config({
        stats: [stat({ key: 'beer', label: 'пиво' }), counter()],
        actions: [
          action({
            effects: [
              { stat_key: 'beer', delta: 40 },
              { stat_key: 'beers_drunk', delta: 1 },
            ],
          }),
        ],
      }),
    );
    expect(rules.actions[0]?.effects).toBe('пиво +40 · выпито пива +1');
  });

  // A counter's scale is 0..1000000, and «+1» against a span that wide is not
  // the catalogue's reset idiom. It would be, at a delta of a million — which is
  // why the guard is a comparison against the span rather than against 100.
  it('does not mistake a counter increment for the reset idiom', () => {
    const rules = buildRules(
      config({
        stats: [counter()],
        actions: [action({ effects: [{ stat_key: 'beers_drunk', delta: 1 }] })],
      }),
    );
    expect(rules.actions[0]?.effects).toBe('выпито пива +1');
  });
});

describe('buildRules — degrades instead of throwing', () => {
  // The config fetch is deliberately allowed to fail: the yard runs on the
  // socket, so a catalogue that never arrives must cost the derived half of the
  // cheatsheet and nothing else. Every one of these is a real shape the client
  // can be handed.
  it.each([
    ['null', null],
    ['undefined', undefined],
    ['an empty object', {} as VanyagotchiConfig],
    ['lists that are not arrays', { stats: 'nope', actions: 7 } as unknown as VanyagotchiConfig],
    ['entries with no key', config({ stats: [{} as VanyagotchiStat] })],
    ['a null entry', config({ actions: [null as unknown as VanyagotchiAction] })],
  ])('survives %s', (_name, given) => {
    const rules = buildRules(given as VanyagotchiConfig | null | undefined);
    expect(rules.stats).toEqual([]);
    expect(rules.counters).toEqual([]);
    expect(rules.actions).toEqual([]);
  });

  it('keeps a stat whose optional fields are missing', () => {
    const rules = buildRules(
      config({ stats: [{ key: 'mood' } as unknown as VanyagotchiStat] }),
    );
    // Labelled by its key rather than blank, and carrying no claims it cannot
    // support — a row that says nothing false is better than no row.
    expect(rules.stats[0]?.label).toBe('mood');
    expect(rules.stats[0]?.notes).toEqual([]);
  });

  it('skips an effect whose delta is not a number', () => {
    const rules = buildRules(
      config({
        actions: [
          action({
            effects: [
              { stat_key: 'beer', delta: Number.NaN },
              { stat_key: 'hp', delta: 15 },
            ],
          }),
        ],
      }),
    );
    expect(rules.actions[0]?.effects).toBe('hp +15');
  });
});

describe('YARD_PROSE — the hardcoded half', () => {
  // It exists precisely because the server does not put walking, muttering,
  // sleepers or offline decay on the wire. The comment above it in the source
  // is what a rules change has to come back and read, and this test is what
  // stops the array being quietly emptied.
  it('is non-empty, so the splash still teaches the yard with no config at all', () => {
    expect(YARD_PROSE.length).toBeGreaterThan(0);
    for (const line of YARD_PROSE) expect(line.trim().length).toBeGreaterThan(0);
  });
});

describe('buildRules — a verb you have to walk to something to use', () => {
  /** The crate, as the catalogue actually ships it: named, forever, six in it. */
  const crate = (over: Partial<VanyagotchiObjectKind> = {}): VanyagotchiObjectKind =>
    objectKind({
      key: 'beer_crate',
      art: 'obj_crate',
      label: 'ящик пива',
      lifetime_seconds: 0,
      stock: 6,
      ...over,
    });

  /** «выпить пива», gated on standing at the crate and drawn from its stock. */
  const drink = (over: Partial<VanyagotchiAction> = {}): VanyagotchiAction =>
    action({ needs_near: 'beer_crate', contests: 'beer_crate', ...over });

  const notesFor = (cfg: VanyagotchiConfig): string[] => buildRules(cfg).actions[0].notes;

  it('says where he has to stand, naming the thing from the catalogue', () => {
    const notes = notesFor(config({ actions: [drink()], object_kinds: [crate()] }));
    expect(notes).toContain('нужно стоять рядом: ящик пива');
  });

  it('says how many the thing holds, derived from its stock', () => {
    // The six is `crateStock` in content.go — retuned by feel one evening, which
    // is exactly why this sentence is derived and not typed.
    const notes = notesFor(config({ actions: [drink()], object_kinds: [crate()] }));
    expect(notes).toContain('ящик пива — 6 порций на всех');
  });

  it('follows a retune rather than pinning the number', () => {
    const notes = notesFor(config({ actions: [drink()], object_kinds: [crate({ stock: 2 })] }));
    expect(notes).toContain('ящик пива — 2 порции на всех');
  });

  it('agrees the Russian numeral, which a naive template gets visibly wrong', () => {
    const stockOf = (n: number) =>
      notesFor(config({ actions: [drink()], object_kinds: [crate({ stock: n })] })).find((note) =>
        note.includes('на всех'),
      );
    expect(stockOf(1)).toBe('ящик пива — 1 порция на всех');
    expect(stockOf(3)).toBe('ящик пива — 3 порции на всех');
    expect(stockOf(6)).toBe('ящик пива — 6 порций на всех');
    expect(stockOf(11)).toBe('ящик пива — 11 порций на всех');
    expect(stockOf(21)).toBe('ящик пива — 21 порция на всех');
  });

  it('says neither for a verb that is not gated on a place and races nobody', () => {
    const notes = notesFor(config({ actions: [action()], object_kinds: [crate()] }));
    expect(notes.some((note) => note.startsWith('нужно стоять рядом'))).toBe(false);
    expect(notes.some((note) => note.includes('на всех'))).toBe(false);
  });

  it('stays silent about a kind the catalogue does not describe', () => {
    // A client older than the server can be told about a verb gated on a kind it
    // has never heard of, and «нужно стоять рядом: кое-что» is a fetch quest
    // rather than a rule.
    const notes = notesFor(config({ actions: [drink()], object_kinds: [] }));
    expect(notes.some((note) => note.startsWith('нужно стоять рядом'))).toBe(false);
    expect(notes.some((note) => note.includes('на всех'))).toBe(false);
  });

  it('stays silent about a kind the catalogue does not NAME', () => {
    // Stricter than `leavesNote`, which falls back to «кое-что»: the whole
    // content of these two sentences is WHICH thing to walk to.
    const notes = notesFor(
      config({ actions: [drink()], object_kinds: [crate({ label: undefined })] }),
    );
    expect(notes.some((note) => note.startsWith('нужно стоять рядом'))).toBe(false);
    expect(notes.some((note) => note.includes('на всех'))).toBe(false);
  });

  it('says nothing about stock for a contested thing that is won outright', () => {
    // The lost key is contested too, and carries no stock — it is taken whole
    // rather than drawn down, so there is no count to state.
    const key = objectKind({ key: 'key', art: 'obj_key', label: 'ключи', lifetime_seconds: 0 });
    const notes = notesFor(
      config({ actions: [action({ contests: 'key' })], object_kinds: [key] }),
    );
    expect(notes.some((note) => note.includes('на всех'))).toBe(false);
  });

  it('does not confuse the two gates: a place is not a stat', () => {
    const notes = notesFor(
      config({
        stats: [stat({ key: 'bladder', label: 'мочевой пузырь' })],
        actions: [drink({ needs_stat: 'bladder', needs_at_least: 15 })],
        object_kinds: [crate()],
      }),
    );
    expect(notes).toContain('нужно накопить: мочевой пузырь от 15');
    expect(notes).toContain('нужно стоять рядом: ящик пива');
  });
});
