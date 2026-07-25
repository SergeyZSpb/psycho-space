import type { VanyagotchiStat, VanyagotchiStatValue } from '../api/types';

// Display maths for «Ванягоччи»'s pet — and every function here is DISPLAY ONLY.
//
// The server owns every value. It stores the pair (value, as_of) and computes
// `clamp(value − rate × hoursSince(as_of))` on read, and that answer is the
// truth; what this file does is redraw the same closed form between fetches so a
// bar creeps rather than jumping once a minute. The client never sends a value
// back, and every action is answered with the server's own recomputed state, so
// a screen that has drifted is corrected the moment the player does anything.
//
// It is a deliberate second implementation of one line of arithmetic. The rule
// that keeps that honest is that the numbers behind it — the rates, the bounds,
// which end of the scale is good — all arrive from the catalogue, so this file
// knows *how* a stat decays and never *which* stats exist or how fast.

/** Milliseconds in an hour, the unit every catalogue rate is expressed in. */
const HOUR_MS = 3_600_000;

/**
 * How far this device's clock is ahead of the server's, in milliseconds.
 *
 * Measured once per fetch and applied to every later reading, because the
 * elapsed time that matters is measured on the SERVER's clock: `as_of` is a
 * server timestamp, and subtracting it from a device clock that is three minutes
 * fast would draw three minutes of decay that has not happened. Phones are
 * routinely wrong by more than that.
 */
export function skewMs(serverNow: string, clientNowMs: number): number {
  const server = Date.parse(serverNow);
  if (Number.isNaN(server)) return 0;
  return clientNowMs - server;
}

/**
 * The stat's value now, decayed from the pair the server sent.
 *
 * `serverNowMs` is this instant expressed on the server's clock — i.e.
 * `Date.now() - skew`. Elapsed time is never allowed to run backwards: a
 * negative interval yields the stored value untouched, exactly as the server
 * does, rather than winding the value back up.
 */
export function decayedValue(
  def: VanyagotchiStat,
  stat: VanyagotchiStatValue,
  serverNowMs: number,
): number {
  const asOf = Date.parse(stat.as_of);
  if (Number.isNaN(asOf)) return clampStat(def, stat.value);
  const elapsed = serverNowMs - asOf;
  if (elapsed <= 0) return clampStat(def, stat.value);
  return clampStat(def, stat.value - def.decay_per_hour * (elapsed / HOUR_MS));
}

/** Forces a value inside the stat's catalogue bounds. */
export function clampStat(def: VanyagotchiStat, value: number): number {
  if (!Number.isFinite(value)) return def.start;
  return Math.min(def.max, Math.max(def.min, value));
}

/**
 * Where the value sits on its own scale, 0..1, for the width of a bar.
 *
 * Always measured from `min` towards `max` regardless of which end is the happy
 * one — a bladder filling up should visibly fill up. Which end is *good* is
 * `good_high`, and that drives colour, not length.
 */
export function statFraction(def: VanyagotchiStat, value: number): number {
  const span = def.max - def.min;
  if (!(span > 0)) return 0;
  return Math.min(1, Math.max(0, (clampStat(def, value) - def.min) / span));
}

/**
 * Is this stat in the range that should read as trouble?
 *
 * The threshold and its direction are both catalogue data, so the stylesheet
 * never learns that thirty is a bad amount of health and seventy is a worrying
 * amount of bladder.
 */
export function inTrouble(def: VanyagotchiStat, value: number): boolean {
  return def.good_high ? value < def.warn_at : value > def.warn_at;
}

/** How the pet should look, in the three states the yard can draw. */
export type PetCondition = 'dead' | 'poorly' | 'fine';

/**
 * The pet's overall condition, derived from the fatal stat rather than from a
 * mood column.
 *
 * There is no mood in the database and there should not be: it is a pure
 * function of values that already exist, so it cannot drift out of step with
 * them and it costs no storage. `alive` still comes from the server, because
 * death is a recorded fact — the instant it happened — and not merely "hp is
 * zero right now".
 */
export function condition(
  defs: readonly VanyagotchiStat[],
  values: ReadonlyMap<string, number>,
  alive: boolean,
): PetCondition {
  if (!alive) return 'dead';
  for (const def of defs) {
    if (!def.fatal) continue;
    const v = values.get(def.key);
    if (v !== undefined && inTrouble(def, v)) return 'poorly';
  }
  return 'fine';
}
