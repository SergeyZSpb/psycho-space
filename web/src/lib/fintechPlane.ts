/**
 * «СИМУЛЯТОР ФИНТЕХА» — placing the office on the screen, and reading its numbers.
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

import type { FintechRect } from '../api/types';

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
  // Whether his words have anywhere to go above his head. Written HERE for the
  // same reason the band is: derived at a second site it lags the coordinate it
  // is derived from by a frame, and a balloon that flips one frame late is worse
  // than one that never flips.
  el.style.setProperty(SAY_BELOW_PROPERTY, sayBelow(at.v) ? '1' : '0');
}

/**
 * How much clear wall is kept above the room, expressed in FIGURE HEIGHTS.
 *
 * A figure is `1.6 × --unit` tall and feet-anchored, so its box hangs entirely
 * above the coordinate it stands on. At the top wall — reachable, because the
 * simulation clamps only to `PlayerRadius` = 0.35 m of a 22 m room — that box is
 * almost entirely above the office rectangle, and the plane clips it. So the
 * plane is drawn taller than the room and the room sits at the bottom of it; the
 * strip above is wall.
 *
 * IN FIGURE HEIGHTS RATHER THAN IN METRES, and that is the whole point of the
 * number. Sized in metres it would have to be re-derived by hand every time the
 * figure's size changed — and the figure is about to shrink by a quarter. Tied
 * to the unit, the band follows automatically and the relation is testable:
 * `planeBoxFits` below is what asserts it, and `UNIT_CQW` is the one number a
 * scale change touches.
 *
 * EXACTLY ONE FIGURE, AND THE CEILING IS NOT TASTE — it is the full-bleed rule.
 * Every extra tenth of a figure makes the plane taller, and on a phone the plane's
 * HEIGHT is what binds, so a deeper wall is paid for directly in room width.
 * Measured on a 412 × 746 phone: at 1.0 the room is the whole 412; at 1.035 it
 * starts losing pixels; at 1.1 it is 410 and at 1.2 it is 408, drawing with dead
 * space down both sides — which is the exact defect the overlay layout was built
 * to remove («the office is the whole screen, edge to edge» in the layout suite).
 *
 * So there is no air above his head, and the consequence is worth stating: at the
 * wall the outermost pixel or two of the head's outline is clipped. That is
 * invisible at a glance and is the right way round — a man drawn a pixel short
 * beats a room drawn narrow. The avatar badge used to be on this list and is not
 * any more: it sits inside the figure's own box now, so the wall cannot reach it.
 */
export const HEADROOM_FIGURES = 1.0;

/**
 * The figure's height as a fraction of the office's WIDTH.
 *
 * `--unit` is `UNIT_CQW × 100cqw` of the office and a figure is `1.6 × --unit`
 * tall, so this is the one bridge between the stylesheet's sense of scale and
 * this module's. It is exported so the view can write `--unit-cqw` and the
 * stylesheet can read it: the coefficient then lives in exactly one place
 * instead of being spelled once in CSS and once here, which is the duplication
 * that makes a scale change a two-file guess.
 */
export const UNIT_CQW = 0.0825;

/** A figure's HEIGHT, as a fraction of the office's width. */
export const FIGURE_W = 1.6 * UNIT_CQW;

/**
 * A figure's WIDTH, as a fraction of the office's width.
 *
 * The same number `--unit` is, because a figure box is one unit wide and 1.6 tall.
 * Named separately because the two walls are derived from different halves of it and
 * confusing them costs the room a tenth of its width: the top wall needs a figure's
 * HEIGHT, the side walls need half its WIDTH, and using the height for both was the
 * first version of this.
 */
export const FIGURE_WIDE = UNIT_CQW;

/** The custom property saying a balloon must hang below the feet instead. */
export const SAY_BELOW_PROPERTY = '--say-below';

/**
 * How near the top wall a figure has to be before its words move below its feet.
 *
 * THE BAND MAKES THE MAN VISIBLE; IT DOES NOT MAKE ROOM FOR HIS WORDS. Covering
 * both would need a strip about half again as deep, which is floor space and
 * screen taken from the room for the sake of two lines of text — so the balloon
 * is moved instead of the wall being grown. Below this height there is a figure's
 * worth of wall above him and no more, which is where the balloon stops fitting.
 *
 * Deliberately looser than the derived threshold, following the yard's recorded
 * failure direction: too tight clips a balloon to nothing, too loose costs
 * nothing at all — the flip is invisible when there was room anyway.
 */
export const SAY_FLIP_V = 0.08;

/** Whether this figure's balloon has to hang below its feet. */
export function sayBelow(v: number): boolean {
  if (!Number.isFinite(v)) return false;
  return v < SAY_FLIP_V;
}

/** The plane's shape, and how much of it is wall rather than room. */
export interface PlaneBox {
  /** width ÷ height of the whole plane, room plus walls. */
  boxRatio: number;
  /** The top wall's share of the plane's height, 0..1. */
  headShare: number;
  /** Each side wall's share of the plane's width, 0..1. */
  sideShare: number;
}

/**
 * The plane's box for a room of this size.
 *
 * PURE, AND THE REASON THE SPLIT IS TWO ELEMENTS RATHER THAN ARITHMETIC. The
 * plane is the clipping box and holds the wall; the office rectangle inside it
 * keeps the catalogue's own shape and is the query container, so `toPlane`,
 * `deskBox` and every `100cqw` / `100cqh` consumer are untouched by the band's
 * existence — a coordinate is still a fraction of the room. Folding the band into
 * the four transform sites instead would be four independent edits that have to
 * agree forever.
 *
 * A degenerate room answers the plane's own ratio with no wall, so a catalogue
 * that has not arrived yet draws a plausible empty box rather than `NaN` — which
 * `toPlane` would clamp to zero and stack every figure in one corner, a failure
 * this game has shipped before.
 */
export function planeBox(officeW: number, officeH: number): PlaneBox {
  if (!(officeW > 0) || !(officeH > 0) || !Number.isFinite(officeW) || !Number.isFinite(officeH)) {
    return { boxRatio: 16 / 22, headShare: 0, sideShare: 0 };
  }
  const headroom = HEADROOM_FIGURES * FIGURE_W * officeW;
  // HALF A FIGURE ON EACH SIDE, and that is the whole of the side walls.
  //
  // A figure is centred on its coordinate and is FIGURE_W of the office's width
  // across, so at either side wall — reachable, since the simulation clamps only to
  // PlayerRadius — exactly half of it is outside the room. The top wall is a whole
  // figure HEIGHT deep because a figure hangs entirely above its feet; a side wall is
  // half its WIDTH, because sideways it is symmetric. Using the height for both, as
  // the first version did, gives away a tenth of the room for nothing.
  const side = 0.5 * FIGURE_WIDE * officeW;
  const boxW = officeW + 2 * side;
  const boxH = officeH + headroom;
  return {
    boxRatio: boxW / boxH,
    headShare: headroom / boxH,
    sideShare: side / boxW,
  };
}

/**
 * Whether the wall is deep enough to hold a whole figure standing at the wall.
 *
 * This is the relation the band exists to satisfy, written down so that changing
 * `UNIT_CQW` cannot silently break it — which is exactly what a hand-tuned metre
 * constant would have done the moment the figures were resized. Asserted by the
 * unit tests rather than trusted.
 */
export function planeBoxFits(officeW: number, officeH: number): boolean {
  const { headShare, sideShare, boxRatio } = planeBox(officeW, officeH);
  if (headShare <= 0 || sideShare <= 0) return false;
  // MEASURED AGAINST THE ROOM'S WIDTH, which is the basis `FIGURE_W` and
  // `FIGURE_WIDE` are in — and no longer the plane's, now that the room is inset on
  // the sides as well. Getting that basis wrong is what made this return false for a
  // box that was in fact correct: the plane is wider than the room, so a wall is a
  // smaller fraction of it.
  //
  // Derived from the SHARES the stylesheet actually consumes rather than recomputed
  // from the inputs, so this checks the arithmetic instead of restating it.
  const roomShare = 1 - 2 * sideShare; // the room's width, per unit of plane width
  const topWall = headShare / boxRatio / roomShare; // per unit of ROOM width
  const sideWall = sideShare / roomShare;
  const slack = 1e-9;
  return topWall + slack >= FIGURE_W && sideWall + slack >= FIGURE_WIDE / 2;
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

/**
 * The token the server leaves in a line for a name it will not send.
 *
 * The лысый's «where did he go» run contains one line that names whoever vanished.
 * The office knows who that is; putting the name on a frame that repeats ten times a
 * second to say something that changes once a shift is what ADR-037 refused. So the
 * server sends the templated line only to the person who actually vanished — the one
 * client that already knows its own persona's name — and this is what it replaces.
 */
export const NAME_PLACEHOLDER = '{}';

/**
 * Puts a name into a line that asked for one.
 *
 * `fallback` is for every screen that cannot know: a colleague's client is
 * deliberately never told another player's persona, so it says «ОН» rather than
 * guessing or rendering a `{}` at somebody.
 */
export function withName(line: string, name: string, fallback = 'ОН'): string {
  if (!line.includes(NAME_PLACEHOLDER)) return line;
  const use = name.trim() === '' ? fallback : name;
  return line.split(NAME_PLACEHOLDER).join(use.toUpperCase());
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
export function deskBox(d: FintechRect, officeW: number, officeH: number): DeskBox {
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
 * ALWAYS TWO DECIMALS, AND THAT IS THE WHOLE POINT — `×3,00` rather than `×3`.
 * This readout changes several times a second while the ramp climbs, and a
 * variable number of digits changes the cell's WIDTH with it, so everything to
 * its right jogs sideways as you stand still. Tabular figures fix the digits'
 * widths but not how many of them there are; only a fixed count does that.
 *
 * It used to strip the decimals on a whole value, on the reasoning that the
 * ceiling deserves to look like a round number when you reach it. It does — and
 * it reached it by shoving the rest of the strip half a centimetre to the left,
 * which is the one moment in the game when nobody should be looking at anything
 * but the лысый.
 */
export function formatMultiplier(hundredths: number): string {
  const v = Number.isFinite(hundredths) ? Math.max(0, hundredths) / 100 : 1;
  return `×${v.toFixed(2).replace('.', ',')}`;
}

/**
 * How long the shift has lasted, as the HUD shows it: `1:23`.
 *
 * A CLOCK RATHER THAN A DECIMAL, unlike `formatSeconds` right below — this one is
 * read as a length of time somebody is trying to beat, and «83 с.» is a number you
 * have to convert before it means anything. Minutes are not padded and seconds
 * always are, which is how every clock anybody has ever read is written.
 *
 * It takes SECONDS, because that is what both callers have: the HUD derives them
 * from two tick numbers and the leaderboard reads a `double precision` column.
 * Anything not finite, or negative, is 0:00 — a shift cannot have lasted less than
 * no time, and a readout is the wrong place to notice a bad number.
 */
export function formatClock(secs: number): string {
  const total = Number.isFinite(secs) ? Math.max(0, Math.floor(secs)) : 0;
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, '0')}`;
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

/**
 * A hue in 0..359 for a peer's handle, so two colleagues are told apart at a
 * glance without a name on the plane.
 *
 * IT IS DERIVED RATHER THAN SENT. The handle is already on every frame (ADR-037)
 * and a colour is a pure function of it, so the wire carries nothing extra and
 * both people see each other in the same colour without agreeing on one. The
 * yard does the same thing with the same `*31` hash — re-implemented here rather
 * than imported, because a game owns its own lib (ADR-028).
 */
export function hueFor(handle: string): number {
  let hash = 0;
  for (let i = 0; i < handle.length; i++) {
    hash = (hash * 31 + handle.charCodeAt(i)) >>> 0;
  }
  return hash % 360;
}

/** The shirt a peer is drawn in. */
export function peerColour(handle: string): string {
  return `hsl(${hueFor(handle)} 55% 42%)`;
}

/** One other Карен, as the plane needs him: who he is and what he is saying. */
export interface PeerLook {
  id: string;
  line: number;
  /**
   * Whether he is behind a cloud of hookah smoke.
   *
   * Part of the roster rather than written per frame because it is a MEMBERSHIP
   * fact in the same sense his line is: it toggles a class, which is a vdom patch,
   * and it changes twice in ten seconds rather than ten times a second. Compared
   * in `sameRoster`, so a frame where nothing changed still costs nothing.
   */
  cloud?: boolean;
  /**
   * Whether Claude Code has landed on him recently.
   *
   * On the roster for the cloud's reason: it toggles a class, which is a vdom
   * patch, and it changes twice in four seconds rather than ten times a second.
   */
  slow?: boolean;
}

/**
 * Whether two rosters describe the same people saying the same things.
 *
 * THIS IS WHAT KEEPS THE PEERS OUT OF THE FRAME LOOP. Membership and speech are
 * reactive — they are a `v-for` and a text node — but they arrive on a payload
 * that repeats ten times a second, and assigning a fresh array each time would
 * be a scheduler pass and a vdom patch per peer per frame to produce markup that
 * is usually identical. Positions never enter reactivity at all: they are CSS
 * custom properties written straight to the element (see applyFigure), which is
 * the yard's rule and the reason a plane full of figures costs the compositor
 * and not Vue.
 */
export function sameRoster(a: readonly PeerLook[], b: readonly PeerLook[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i].id !== b[i].id || a[i].line !== b[i].line) return false;
    // A cloud appearing or clearing on a colleague IS a roster change: it is a
    // class on his figure, and without this the office would keep drawing him as
    // catchable until something else about him happened to change.
    if (!!a[i].cloud !== !!b[i].cloud) return false;
    if (!!a[i].slow !== !!b[i].slow) return false;
  }
  return true;
}

/**
 * Where to ask for the picture to draw on a colleague.
 *
 * DERIVED FROM THE HANDLE RATHER THAN SENT WITH IT. A ~250-character URL beside
 * each peer would repeat ten times a second, per viewer, for the whole of a
 * shift, to say something that cannot change during one (ADR-037). Derived, it
 * costs the wire nothing and the browser one cached GET per face.
 *
 * There is no such thing as a colleague this client cannot ask about, so it asks
 * about every one it draws and lets the answer decide — a 404 is an ordinary
 * reply meaning "he has no picture", not a failure.
 *
 * The handle is escaped even though every one the server mints today is
 * base64url. It is a value off the wire being pasted into a URL path, and on the
 * day that stops being true an unescaped one would ask for a different route
 * entirely rather than fail in a way that looks like a bad handle.
 */
export function fintechAvatarEndpoint(handle: string): string {
  return `/api/game-fintech/avatar/${encodeURIComponent(handle)}`;
}

/**
 * Whether a figure is moving faster than anybody can walk — which, on this
 * plane, means dashing.
 *
 * A BUFF DERIVED RATHER THAN SENT. A colleague's dash used to be visible only to
 * the colleague, because the only thing that knew about it was his own
 * predictor. Nothing here exceeds the walk speed except a dash, so two
 * consecutive drawn positions and the walk speed the catalogue already publishes
 * are enough to know — for every peer, at no cost on a frame that repeats ten
 * times a second.
 *
 * The margin is what makes it usable rather than merely true: interpolation and
 * a recovered dropped frame both overstate a single step, while a dash is
 * several times a walk, so there is a wide gap to sit in. A non-positive or
 * absurd `dt` answers false rather than dividing by it.
 */
export function movingFast(
  from: { x: number; y: number },
  to: { x: number; y: number },
  dtSeconds: number,
  walkSpeed: number,
): boolean {
  if (!(dtSeconds > 0) || !Number.isFinite(dtSeconds) || dtSeconds > 1) return false;
  if (!(walkSpeed > 0)) return false;
  const speed = Math.hypot(to.x - from.x, to.y - from.y) / dtSeconds;
  return speed > walkSpeed * FAST_MARGIN;
}

/** How far past a walk counts as a dash. See movingFast. */
export const FAST_MARGIN = 1.6;
