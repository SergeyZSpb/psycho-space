/**
 * «СИМУЛЯТОР КАРЕНА» — placing the office on the screen, and reading its numbers.
 *
 * THE OFFICE IS DOM AND CSS, NOT A CANVAS. «ВАНЯДУМ» is WebGL and the yard is
 * DOM; each game decides for itself (ADR-028), and this one is a flat plan view
 * of a room, which is exactly what a stylesheet is good at. What does not vary
 * is where the line falls: every readout, control and word of text is real DOM,
 * because nothing inside a canvas can be asserted on without pixel comparison.
 *
 * POSITIONS DO NOT GO THROUGH VUE. The player and the boss move sixty times a
 * second; bound to a `:style`, each frame would become a scheduler pass plus a
 * vdom patch per figure to produce a transform the compositor could have been
 * handed straight away. So a frame is applied by writing a handful of CSS custom
 * properties per element and nothing else — the same split the yard settled on,
 * where membership is reactive and positions are not.
 *
 * CUSTOM PROPERTIES RATHER THAN A `transform` STRING, for one concrete reason:
 * the mapping from metres to pixels then lives once, in the stylesheet, against
 * the plane's own box. There is no measured size cached in JavaScript to
 * invalidate — which matters here because the plane resizes whenever mobile
 * browser chrome slides in or out.
 *
 * Everything in this file is pure, which is the point: the view writes
 * properties and never builds a sentence or works out a coordinate.
 */

import type { KarenRect } from '../api/types';

/** The custom properties the stylesheet reads. Normalised 0..1 across the plane. */
export const X_PROPERTY = '--x';
export const Y_PROPERTY = '--y';
/** Which depth band the figure is in — the stylesheet uses it as the z-index. */
export const BAND_PROPERTY = '--band';
/** That band's scale factor. */
export const DEPTH_PROPERTY = '--depth';
/** How pleased the bald man is to see you, 0..1. */
export const GRIN_PROPERTY = '--grin';

/**
 * Everything this module needs of an element: somewhere to set a custom
 * property. Narrowed to exactly that rather than typed as an `HTMLElement`, so
 * the placement path can be exercised without a DOM — and so it is obvious from
 * the signature that nothing here reads layout or measures a box.
 */
export interface StyleTarget {
  style: { setProperty(name: string, value: string): void };
}

/**
 * What each depth band multiplies a figure's size by, from the back of the
 * office to the front.
 *
 * THE FIRST ENTRY IS 1 ON PURPOSE: depth can then only ever make a figure
 * BIGGER than its unscaled size, so the legibility floor is the CSS size itself
 * rather than a product of two numbers that could drift apart.
 *
 * DISCRETE BANDS RATHER THAN A CONTINUOUS FUNCTION OF Y, and that is the whole
 * design. Size is a `transform`, which the compositor interpolates; stacking
 * order is a `z-index`, which can only jump. Scale continuously and the two
 * disagree on every frame — a figure is visibly larger part-way through the walk
 * that will eventually put it in front, so for that stretch it is a big shape
 * drawn behind a small one. With bands the disagreement is confined to the
 * instant a boundary is crossed.
 *
 * A narrower ramp than the yard's, deliberately: this office is 16 by 22 metres
 * seen from above with a shallow tilt, not a garden with a horizon in it, so a
 * strong ramp would read as the room being funnel-shaped.
 */
export const DEPTH_SCALES: readonly number[] = [1, 1.1, 1.22, 1.36];

/** Which band a figure at this normalised height belongs to. */
export function bandFor(v: number): number {
  if (!Number.isFinite(v)) return 0;
  const band = Math.floor(v * DEPTH_SCALES.length);
  return Math.min(DEPTH_SCALES.length - 1, Math.max(0, band));
}

/**
 * How much bigger a figure is at this height — CONTINUOUS, not banded.
 *
 * The band is still the right shape for anything that genuinely steps, and paint
 * order is the reason it exists: two figures either overlap one way or the other
 * and there is no half-way. **Scale is not that kind of quantity.** Reading it
 * off `DEPTH_SCALES[bandFor(v)]` made a figure walking from the back of the
 * office to the front change size in three visible jerks — the yard gets away
 * with exactly that because its people amble at 5 Hz behind a CSS transition,
 * and here you are staring at the one figure you are steering while it crosses a
 * band boundary mid-dash.
 *
 * The curve is the same one the bands describe, just not sampled: piecewise
 * linear through those knots, evenly spaced across 0..1. So the artist-chosen
 * shape — a gentle ramp that accelerates slightly, because a strong one reads as
 * the room being funnel-shaped — is preserved exactly, and only the stepping is
 * gone.
 */
export function depthScaleFor(v: number): number {
  if (!Number.isFinite(v)) return DEPTH_SCALES[0];
  const t = clamp01(v) * (DEPTH_SCALES.length - 1);
  const i = Math.min(DEPTH_SCALES.length - 2, Math.floor(t));
  return DEPTH_SCALES[i] + (DEPTH_SCALES[i + 1] - DEPTH_SCALES[i]) * (t - i);
}

/** A point on the plane, normalised to 0..1 on each axis. */
export interface PlanePoint {
  u: number;
  v: number;
}

function clamp01(v: number): number {
  if (!Number.isFinite(v)) return 0;
  return Math.min(1, Math.max(0, v));
}

/**
 * Metres to a fraction of the plane.
 *
 * The office's origin is top-left with +Y downwards, which is the same
 * convention the plane is drawn in — so there is no axis flip anywhere in this
 * client, and a coordinate read off a snapshot is a coordinate on the screen.
 *
 * Clamped, and degenerate dimensions answer the middle rather than a division by
 * zero: a config that has not arrived yet must not write `NaN` into a custom
 * property, which CSS resolves to nothing and leaves a figure stuck wherever it
 * last was with no error anywhere.
 */
export function toPlane(x: number, y: number, officeW: number, officeH: number): PlanePoint {
  const u = officeW > 0 ? clamp01(x / officeW) : 0.5;
  const v = officeH > 0 ? clamp01(y / officeH) : 0.5;
  return { u, v };
}

/**
 * Writes one figure's position onto its element.
 *
 * The depth band travels with the position rather than being written from
 * somewhere else: derived at a second site it could lag the coordinates by a
 * frame, and a figure drawn a band behind where it is standing is exactly the
 * artefact the discrete bands exist to avoid.
 */
export function applyFigure(el: StyleTarget, at: PlanePoint): void {
  const band = bandFor(at.v);
  el.style.setProperty(X_PROPERTY, String(at.u));
  el.style.setProperty(Y_PROPERTY, String(at.v));
  el.style.setProperty(BAND_PROPERTY, String(band));
  // The band picks the paint order, which steps; the scale is read from the
  // continuous ramp, which does not. See depthScaleFor.
  el.style.setProperty(DEPTH_PROPERTY, String(depthScaleFor(at.v)));
}

/** How pleased he is, as a state the stylesheet can key off. */
export type GrinState = 'far' | 'closing' | 'here';

/**
 * Reads the boss's grin, quantised into the three states the drawing has.
 *
 * A STATE AS WELL AS A NUMBER, because the two are drawn by different
 * mechanisms: the smile widens continuously from `--grin`, which the compositor
 * can interpolate, while the colour of the whole figure changes in steps, which
 * it cannot — and a colour interpolated from a value that arrives ten times a
 * second reads as flicker rather than as approach.
 *
 * Anything unreadable is `far`, which is the safe direction: a client that could
 * not parse a frame should not be painting the screen red.
 */
export function grinState(grin: number): GrinState {
  if (!Number.isFinite(grin)) return 'far';
  if (grin >= 0.7) return 'here';
  if (grin >= 0.35) return 'closing';
  return 'far';
}

/** Writes the boss's position and how pleased he is, in one pass. */
export function applyBoss(el: StyleTarget, at: PlanePoint, grin: number): void {
  applyFigure(el, at);
  el.style.setProperty(GRIN_PROPERTY, String(Number.isFinite(grin) ? clamp01(grin) : 0));
}

/**
 * The line a balloon shows, from a pool and the index the server sent.
 *
 * THE WIRE CARRIES AN INDEX AND NEVER THE WORDS (ADR-037), so this is where the
 * two are put back together. The catalogue was fetched once; the index costs a
 * digit on a frame that repeats ten times a second per viewer, forever.
 *
 * TOTAL, because the alternative is a blank rectangle over somebody's head. An
 * absent index is zero and zero is the default line, which is exactly what the
 * `omitempty` on the wire means; anything out of range — an older client against
 * a newer catalogue, or the reverse — falls back to the same place rather than
 * to nothing, because a figure with no balloon reads as broken while a figure
 * saying the wrong thing reads as this game.
 */
export function sayFor(lines: readonly string[] | undefined, index: number): string {
  if (!lines || lines.length === 0) return '';
  if (!Number.isFinite(index)) return lines[0];
  const i = Math.trunc(index);
  return i >= 0 && i < lines.length ? lines[i] : lines[0];
}

/** A desk's box on the plane, as fractions of it. */
export interface DeskBox {
  left: number;
  top: number;
  width: number;
  height: number;
}

/**
 * One desk, as a fraction of the plane.
 *
 * Desks are static — the office is in the catalogue and never generated — so
 * unlike the figures these go through Vue exactly once, as an inline style on a
 * `v-for`. Deriving the fractions here rather than in the template keeps the
 * arithmetic testable and the template a list of boxes.
 */
export function deskBox(d: KarenRect, officeW: number, officeH: number): DeskBox {
  if (!(officeW > 0) || !(officeH > 0)) return { left: 0, top: 0, width: 0, height: 0 };
  return {
    left: clamp01(d.x / officeW),
    top: clamp01(d.y / officeH),
    width: clamp01(d.w / officeW),
    height: clamp01(d.h / officeH),
  };
}

// ---------------------------------------------------------------------------
// Reading the numbers on the wire.
//
// The snapshot is deliberately terse — centimetres, whole roubles, hundredths of
// a multiplier, milliseconds — because it repeats ten times a second for as long
// as anybody is playing. Every conversion back into something a person can read
// lives here, so the HUD renders strings and computes nothing.
// ---------------------------------------------------------------------------

/** Groups thousands with a non-breaking space, the Russian convention. */
function group(n: number): string {
  return String(n).replace(/\B(?=(\d{3})+(?!\d))/g, '\u00a0');
}

/**
 * The salary, as the HUD shows it: whole roubles, grouped, with the sign.
 *
 * Non-breaking spaces throughout, so a five-figure salary never wraps between
 * its own digits on a 360 px phone — which it did, once, and read as two numbers.
 */
export function formatMoney(rubles: number): string {
  const n = Number.isFinite(rubles) ? Math.max(0, Math.round(rubles)) : 0;
  return `${group(n)}\u00a0₽`;
}

/** Formats a number in the Russian convention, without a trailing `,0`. */
export function decimal(v: number, digits = 1): string {
  if (!Number.isFinite(v)) return '0';
  return String(Number(v.toFixed(digits))).replace('.', ',');
}

/**
 * The multiplier, from the hundredths the wire carries.
 *
 * Whole values lose their decimals — `×3`, not `×3,00` — because the ceiling is
 * the thing a player is protecting and it deserves to look like a round number
 * when they reach it.
 */
export function formatMultiplier(hundredths: number): string {
  const v = Number.isFinite(hundredths) ? Math.max(0, hundredths) / 100 : 1;
  return `×${decimal(v, 2)}`;
}

/** A duration in milliseconds, as the HUD shows it: `1,8 с.` */
export function formatSeconds(ms: number): string {
  const v = Number.isFinite(ms) ? Math.max(0, ms) / 1000 : 0;
  return `${decimal(v, 1)}\u00a0с.`;
}

/**
 * How full the ramp bar is, 0..1.
 *
 * Derived from the streak the snapshot carries rather than from the multiplier,
 * because the bar is about PROGRESS and the multiplier is about the reward: they
 * agree today only because the ramp happens to be linear, and reading the streak
 * keeps the bar right on the day it stops being.
 */
export function rampFraction(streakMs: number, rampSeconds: number): number {
  if (!(rampSeconds > 0)) return 1;
  return clamp01(Math.max(0, streakMs) / 1000 / rampSeconds);
}
