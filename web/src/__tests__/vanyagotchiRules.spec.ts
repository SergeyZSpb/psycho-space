import { describe, expect, it } from 'vitest';

import type {
  VanyagotchiAction,
  VanyagotchiConfig,
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
  fatal: true,
  ...over,
});

const action = (over: Partial<VanyagotchiAction> = {}): VanyagotchiAction => ({
  key: 'drink',
  label: 'выпить пива',
  emoji: '🍺',
  effects: [{ stat_key: 'beer', delta: 40 }],
  done: 'выпил',
  revives_fatal: true,
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

  it('says which actions work on a dead Ваня, and which are refused', () => {
    const rules = buildRules(
      config({
        actions: [
          action({ key: 'drink', revives_fatal: true }),
          action({ key: 'relieve', revives_fatal: false }),
        ],
      }),
    );
    expect(rules.actions[0]?.notes).toContain('поднимает мёртвого');
    expect(rules.actions[1]?.notes).toContain('мёртвому нельзя');
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
