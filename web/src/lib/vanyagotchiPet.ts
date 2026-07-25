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
// It is a deliberate second implementation of ONE LINE of arithmetic, and one
// line is the whole budget. The rule that keeps it honest is that every number
// behind it — the bounds, which end of the scale is good, and above all the RATE
// — arrives from the server, so this file knows *how* a stat decays and never
// *which* stats exist, how fast, or what makes them fall faster.

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
 *
 * THE RATE COMES FROM THE SERVER, NOT FROM THE CATALOGUE, and that is the point
 * of `rate_per_hour` existing at all. A stat's real drain is not its catalogue
 * rate: health rots at one point an hour with both needs met and at thirteen
 * with neither, because an empty beer and a full bladder each add six. Working
 * that out here would mean re-deriving the thresholds, the instant each penalty
 * switched on, and every driver's own trajectory — a transliteration of
 * decay.go into TypeScript, kept honest by nothing, and free to disagree with
 * the server about how ill he is. So the server sends the drain in force at the
 * moment it answered and this draws a straight line from it. That line is exact
 * until the next threshold is crossed, which is hours away; and the next fetch,
 * or any action at all, replaces the whole reading with the server's own.
 *
 * `decay_per_hour` is the fallback for a response that carries no rate — an
 * older server, or a fixture that predates the field. Drawing the uncoupled
 * slope is wrong slowly; drawing nothing is wrong immediately.
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
  // `??` rather than `||`: zero is a legitimate rate — a lifetime counter only
  // actions move — and treating it as "missing" would make such a stat drift.
  const rate = stat.rate_per_hour ?? def.decay_per_hour;
  return clampStat(def, stat.value - rate * (elapsed / HOUR_MS));
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

// How the pet LOOKS is deliberately not computed here any more.
//
// There was a `condition()` in this file that derived a pose from the fatal
// stat, and it was right about the arithmetic and wrong about the authority: it
// could only ever see this player's own pet, so the yard drew one Ваня with a
// face and everybody else as an anonymous dot. The pose now arrives per entity
// in the roster frame, worked out server-side from the same catalogue thresholds
// (internal/gamevanyagotchi/display.go), so every player sees the same world and
// a pose can be added without a client deploy. See `PeerPose` in
// lib/vanyagotchiPlane.ts.
//
// The bars kept their local arithmetic, and the difference is worth stating: a
// bar is YOUR pet's number redrawn between fetches, so a copy of the formula
// only ever disagrees with the server about your own screen. A pose is a fact
// several people look at at once, so it has exactly one author.
