/**
 * «АДМИН ФИНТЕХА» — the field constructor: what happens between a thumb and a floor.
 *
 * WHAT THIS FILE IS AND IS NOT. It is the pure half of an editor: pixels into
 * metres, metres onto a lattice, a rectangle put back inside the room, which
 * object a tap meant, and the Russian for what the server said was wrong. It is
 * NOT a validator, and the distinction is the whole design of the feature.
 *
 * THE LOCAL ARITHMETIC IS A CONTROL AFFORDANCE, NOT A JUDGEMENT. A control that
 * lets you drag a desk into the car park is broken, so dragging clamps to the
 * room and lands on the grid — and that is the entire extent of what this
 * client decides. Everything past «stays in the box, lands on the grid» is the
 * server's word, asked for over `POST …/layout/check` after every change and
 * again over `PUT …/layout` when it is saved. There is deliberately no second
 * copy here of the separation rule, the spot rule or the flood fill: two
 * implementations of «is this office playable» drift the moment one of them is
 * retuned, and the one that would be wrong is the one nobody runs the game
 * against.
 *
 * THE PLACEMENT SEARCH IS THE ONE THING THAT LOOKS LIKE AN EXCEPTION AND IS NOT.
 * `freeSpot` picks where a newly added object LANDS. Its answer is a starting
 * position, never a verdict: a spot it chooses is put to the server exactly like
 * one somebody dragged, and can be refused. It exists because a palette that
 * drops every new desk on top of the last one is a palette nobody can use.
 *
 * NOT ONE SENTENCE OF RUSSIAN CROSSES THE WIRE. The server answers in stable
 * snake_case codes plus an index, because this project never returns an error's
 * text to a client — so `problemText` below is where the words live, and it is
 * the hand-written part of this file in exactly the sense `FINTECH_PROSE` is of
 * `fintechRules.ts`. It is total: a code this build has never heard of is quoted
 * rather than swallowed, so a newer server cannot make the panel go blank.
 */

import { plural } from './fintechRules';
import type {
  FintechAdminKind,
  FintechLayoutDraft,
  FintechLayoutProblem,
  FintechRect,
  FintechSolid,
  FintechWindow,
} from '../api/types';

// ---------------------------------------------------------------------------
// The lattice, and the bounds the server enforces without publishing.
// ---------------------------------------------------------------------------

/**
 * The lattice every number this editor produces lands on, in metres.
 *
 * A QUARTER OF A METRE BECAUSE THE GENERATOR USES ONE. `placeStep` in
 * `internal/gamefintech/generate.go` draws every generated office on exactly
 * this grid — it is exact in binary, half the navigation grid's cell, and finer
 * than any gap the validator permits — so an office dragged into shape by hand
 * is made of the same numbers as one drawn by the machine. It is also what makes
 * a thumb usable at 360 px: a quarter of a metre is about five pixels there, so
 * a drag lands somewhere definite instead of somewhere with fourteen decimals.
 */
export const GRID = 0.25;

/**
 * THE BOUNDS BELOW ARE HAND-MIRRORED FROM THE SERVER, and that is a defect in
 * the contract rather than a choice made here.
 *
 * `GET /api/game-fintech/admin/layout` publishes the room (`w`, `h`), both body
 * radii and `min_gap` — but not `MinSolidSide`, `MaxSolidSide`, `MaxSolids`,
 * `MaxWindows`, `WindowMin`, `WindowEdge` or `WindowMullion`, all of which
 * `ValidateLayout` enforces. So a control that stops you dragging an object down
 * to nothing has no served number to stop at, and these are copied from
 * `internal/gamefintech/layout.go` by hand.
 *
 * WHAT THAT COSTS IF THEY DRIFT IS BOUNDED AND VISIBLE: this file is on the
 * affordance side of the line, so a stale copy makes a control stop a little
 * early or a little late, and the server still refuses what it always refused —
 * with a problem the panel names. It can never let an illegal floor through.
 * The fix, when somebody is next in `gamefintech`, is to put them on the admin
 * payload beside `min_gap` and delete this block.
 */
const SERVER_MIN_SIDE = 0.6;
const SERVER_MAX_SIDE = 6.0;
const SERVER_WINDOW_MIN = 1.2;
const SERVER_WINDOW_EDGE = 0.6;
const SERVER_WINDOW_MULLION = 0.6;

/** How many solids and how many panes may be saved at all. */
export const MAX_SOLIDS = 40;
export const MAX_WINDOWS = 12;

/**
 * The shortest and longest side this editor will produce, ON THE LATTICE.
 *
 * DERIVED FROM THE SERVER'S OWN BOUNDS RATHER THAN TYPED BESIDE THEM, and
 * rounded INWARDS: the server's floor is 0.6 m, which is not a multiple of a
 * quarter, so the smallest object a drag can make is the first lattice step at
 * or above it. Rounding the other way would let a drag produce a side the server
 * refuses, which is a control that offers an illegal move — the one failure this
 * clamp exists to prevent.
 */
export const MIN_SIDE = Math.ceil(SERVER_MIN_SIDE / GRID) * GRID;
export const MAX_SIDE = Math.floor(SERVER_MAX_SIDE / GRID) * GRID;

/** The same rounding, for the three numbers that bound a pane. */
export const WINDOW_MIN_LEN = Math.ceil(SERVER_WINDOW_MIN / GRID) * GRID;
export const WINDOW_EDGE = Math.ceil(SERVER_WINDOW_EDGE / GRID) * GRID;
export const WINDOW_MULLION = Math.ceil(SERVER_WINDOW_MULLION / GRID) * GRID;

/**
 * How much of a wall a freshly added pane covers, and how big a freshly added
 * object is.
 *
 * HAND-WRITTEN, because nothing publishes a default size — the catalogue
 * describes the office that exists rather than the one somebody is about to
 * draw. They are starting points a drag immediately overrides, so being
 * approximately right is the whole requirement: a desk is desk-shaped, a
 * flowerpot is small and a ficus is between them.
 */
export const NEW_WINDOW_LEN = 3;
const NEW_SIZES: Record<string, { w: number; h: number }> = {
  desk: { w: 2.5, h: 1 },
  flower: { w: 0.75, h: 0.75 },
  tree: { w: 1, h: 1 },
};
const NEW_SIZE_FALLBACK = { w: 1, h: 1 };

/** How big a newly added object of this kind starts out. */
export function newSize(kind: string): { w: number; h: number } {
  return NEW_SIZES[kind] ?? NEW_SIZE_FALLBACK;
}

// ---------------------------------------------------------------------------
// Numbers.
// ---------------------------------------------------------------------------

/**
 * A metre value put on the lattice.
 *
 * HALVES ROUND TOWARDS +∞, which is `Math.round`'s own rule and is stated here
 * because it is the one thing about this function a test has to pin: 0.125
 * becomes 0.25 and −0.125 becomes 0, so the two are not mirror images. Nothing
 * in the editor depends on which way a halfway drag falls — it is a fifth of a
 * pixel at 360 px — but a rule nobody wrote down is a rule that changes by
 * accident.
 *
 * NEGATIVE ZERO IS NORMALISED AWAY. `Math.round(-0.4)` is `-0`, which formats as
 * «-0,00» and compares equal to zero everywhere except `Object.is` — a value
 * that is invisible in every test that would catch it and visible in the one
 * place a person reads.
 */
export function snap(v: number): number {
  if (!Number.isFinite(v)) return 0;
  // The `+ 0` is what turns -0 back into 0; it is not arithmetic.
  return Math.round(v / GRID) * GRID + 0;
}

/** Bounds a number, tolerating a range whose ends have crossed over. */
function clamp(v: number, lo: number, hi: number): number {
  if (!Number.isFinite(v)) return lo;
  return Math.min(Math.max(v, lo), Math.max(lo, hi));
}

/** A length in metres, as the readout writes it: «4,50». */
export function metres(v: number): string {
  if (!Number.isFinite(v)) return '?';
  return v.toFixed(2).replace('.', ',');
}

// ---------------------------------------------------------------------------
// Pixels and metres.
// ---------------------------------------------------------------------------

/** The room the draft is being drawn in. */
export interface Room {
  w: number;
  h: number;
}

/**
 * The plan's box in CSS pixels — `getBoundingClientRect()`, narrowed to the four
 * numbers this file reads.
 *
 * IT IS THE ROOM AND NOTHING ELSE, which is a property the stylesheet has to
 * keep: the plan draws its edge with an INSET ring rather than a border, so the
 * bounding box is exactly the rectangle the solids' percentages are resolved
 * against. A one-pixel border would put the two half a grid step out of step at
 * 360 px, which is invisible until a drag lands on the wrong quarter-metre.
 */
export interface PlanBox {
  left: number;
  top: number;
  width: number;
  height: number;
}

/** Whether a box is usable — a plan that has not been laid out yet is not. */
function measurable(box: PlanBox, room: Room): boolean {
  return box.width > 0 && box.height > 0 && room.w > 0 && room.h > 0;
}

/** A point in viewport pixels, as a point on the floor in metres. */
export function pointToMetres(
  clientX: number,
  clientY: number,
  box: PlanBox,
  room: Room,
): { x: number; y: number } {
  if (!measurable(box, room)) return { x: 0, y: 0 };
  return {
    x: ((clientX - box.left) / box.width) * room.w,
    y: ((clientY - box.top) / box.height) * room.h,
  };
}

/**
 * A distance in pixels, as a distance in metres.
 *
 * THE LARGER OF THE TWO AXES, because the answer is used as a tolerance and a
 * tolerance should be forgiving. The plan keeps the room's own aspect ratio, so
 * the two scales are equal in practice and this only matters while a layout pass
 * is in flight.
 */
export function toleranceMetres(px: number, box: PlanBox, room: Room): number {
  if (!measurable(box, room)) return 0;
  return Math.max((px * room.w) / box.width, (px * room.h) / box.height);
}

/**
 * How near a tap has to be to count as aiming at something, in pixels.
 *
 * A THUMB, ROUNDED DOWN. The tap-target floor this project enforces is 44 px, so
 * half of one is the radius within which somebody meant to hit a thing they
 * missed. It is deliberately used ONLY as a fallback — see `pickAt`.
 */
export const PICK_TOLERANCE_PX = 22;

// ---------------------------------------------------------------------------
// Which object a tap meant.
// ---------------------------------------------------------------------------

/** How far a point is from a rectangle, zero inside it. */
function pointGap(r: FintechRect, x: number, y: number): number {
  const dx = Math.max(0, Math.max(r.x - x, x - (r.x + r.w)));
  const dy = Math.max(0, Math.max(r.y - y, y - (r.y + r.h)));
  return Math.hypot(dx, dy);
}

/**
 * Which solid a tap at this point meant, or −1 for none.
 *
 * TWO PASSES, AND THE ORDER IS THE WHOLE POINT.
 *
 * FIRST, EXACT CONTAINMENT, topmost first — later solids paint over earlier ones,
 * so the last one in the list is the one the eye sees. A point inside something
 * always selects that something, whatever else is nearby.
 *
 * ONLY THEN, A TOLERANCE, and only when the tap landed on bare floor. A single
 * pass that took the nearest object within a thumb's width is broken by
 * arithmetic rather than by taste: at 360 px the plan is under 300 px wide for a
 * 16 m room, so 22 px is well over a metre — while the validator's own minimum
 * separation is 1.5 m. A flowerpot standing the legal distance from a desk would
 * therefore be inside the DESK's tolerance, and tapping the flowerpot dead
 * centre would select the desk instead, for ever, with no way to reach the
 * flowerpot at all.
 *
 * THE SMALLER OBJECT WINS A TIE, because a flowerpot is what somebody was
 * plausibly aiming at and a desk is not something you miss.
 */
export function pickAt(
  solids: readonly FintechRect[],
  at: { x: number; y: number },
  tolerance: number,
): number {
  for (let i = solids.length - 1; i >= 0; i--) {
    const r = solids[i];
    if (at.x >= r.x && at.x <= r.x + r.w && at.y >= r.y && at.y <= r.y + r.h) return i;
  }
  if (!(tolerance > 0)) return -1;
  let best = -1;
  let bestGap = Infinity;
  let bestArea = Infinity;
  for (let i = 0; i < solids.length; i++) {
    const gap = pointGap(solids[i], at.x, at.y);
    if (gap > tolerance) continue;
    const area = solids[i].w * solids[i].h;
    // A hair of slack, so two objects at the same distance are separated by size
    // rather than by floating-point noise in the subtraction that produced it.
    if (gap < bestGap - 1e-9 || (gap < bestGap + 1e-9 && area < bestArea)) {
      best = i;
      bestGap = gap;
      bestArea = area;
    }
  }
  return best;
}

// ---------------------------------------------------------------------------
// Moving and resizing, which is the whole of what this client decides.
// ---------------------------------------------------------------------------

/** A rectangle put on the lattice and back inside the room, keeping its size. */
export function moveSolidTo<T extends FintechRect>(rect: T, x: number, y: number, room: Room): T {
  return {
    ...rect,
    x: clamp(snap(x), 0, room.w - rect.w),
    y: clamp(snap(y), 0, room.h - rect.h),
  };
}

/**
 * A rectangle resized from its bottom-right corner, keeping its top-left one.
 *
 * The far corner staying put is what makes a handle feel like a handle: the
 * thing you are not touching does not move. Both sides land on the lattice, are
 * held between the shortest and longest the server will accept, and are cut
 * short by the wall — so a drag can never produce a box that hangs out of the
 * room or a side the validator refuses outright.
 */
export function resizeSolidTo<T extends FintechRect>(rect: T, w: number, h: number, room: Room): T {
  return {
    ...rect,
    w: clamp(snap(w), MIN_SIDE, Math.min(MAX_SIDE, room.w - rect.x)),
    h: clamp(snap(h), MIN_SIDE, Math.min(MAX_SIDE, room.h - rect.y)),
  };
}

/** How long the wall a pane is on is, or zero for a wall this build cannot place. */
export function wallLength(wall: string, room: Room): number {
  if (wall === 'top') return room.w;
  if (wall === 'left' || wall === 'right') return room.h;
  return 0;
}

/** A pane put on the lattice, kept off both ends of its wall and inside it. */
export function clampWindow(win: FintechWindow, room: Room): FintechWindow {
  const along = wallLength(win.wall, room);
  if (!(along > 0)) return win;
  const len = clamp(snap(win.len), WINDOW_MIN_LEN, along - 2 * WINDOW_EDGE);
  return {
    ...win,
    len,
    at: clamp(snap(win.at), WINDOW_EDGE, along - WINDOW_EDGE - len),
  };
}

/** The four numbers of a solid a stepper or an arrow key can move. */
export type SolidField = 'x' | 'y' | 'w' | 'h';
/** The two of a pane. */
export type WindowField = 'at' | 'len';

/** One solid, nudged by whole lattice steps. */
export function stepSolid<T extends FintechRect>(
  rect: T,
  field: SolidField,
  steps: number,
  room: Room,
): T {
  const by = steps * GRID;
  switch (field) {
    case 'x':
      return moveSolidTo(rect, rect.x + by, rect.y, room);
    case 'y':
      return moveSolidTo(rect, rect.x, rect.y + by, room);
    case 'w':
      return resizeSolidTo(rect, rect.w + by, rect.h, room);
    case 'h':
      return resizeSolidTo(rect, rect.w, rect.h + by, room);
  }
}

/** One pane, nudged by whole lattice steps. */
export function stepWindow(
  win: FintechWindow,
  field: WindowField,
  steps: number,
  room: Room,
): FintechWindow {
  const by = steps * GRID;
  return clampWindow(
    field === 'at' ? { ...win, at: win.at + by } : { ...win, len: win.len + by },
    room,
  );
}

// ---------------------------------------------------------------------------
// Where a newly added thing lands.
// ---------------------------------------------------------------------------

/** The separation between two rectangles, per axis — the shape the resolver cares about. */
function axisGap(a: FintechRect, b: FintechRect): number {
  const dx = Math.max(0, Math.max(b.x - (a.x + a.w), a.x - (b.x + b.w)));
  const dy = Math.max(0, Math.max(b.y - (a.y + a.h), a.y - (b.y + b.h)));
  return Math.max(dx, dy);
}

/**
 * Somewhere roomy for a new object, on the lattice, nearest the middle of the
 * floor.
 *
 * IT PICKS A STARTING POSITION AND JUDGES NOTHING. Everything it lands on is put
 * to `POST …/layout/check` exactly like a rectangle somebody dragged, and can be
 * refused — this is why it is allowed to use a rule of thumb at all. What it
 * exists for is the alternative: a palette that drops every new desk on the same
 * square puts the second one inside the first, and the admin's first act is
 * always to drag it off again.
 *
 * `gap` IS THE SERVED `min_gap` and the only number involved. It is applied to
 * the walls, to the other objects and to the fixed catalogue points alike —
 * which is STRICTER than the server is about the points (they need less room
 * than furniture does), and being stricter is free here because a failure to
 * find anywhere falls through to the next attempt rather than refusing to place.
 *
 * Three attempts, narrowest first: clear of everything, then clear of the
 * furniture alone, then the middle of the room whatever is standing there.
 */
export function freeSpot(
  size: { w: number; h: number },
  room: Room,
  solids: readonly FintechRect[],
  points: readonly { x: number; y: number }[],
  gap: number,
): { x: number; y: number } {
  const middle = {
    x: clamp(snap((room.w - size.w) / 2), 0, room.w - size.w),
    y: clamp(snap((room.h - size.h) / 2), 0, room.h - size.h),
  };
  const search = (avoidPoints: boolean): { x: number; y: number } | null => {
    const lo = Math.max(0, gap);
    let best: { x: number; y: number } | null = null;
    let bestD = Infinity;
    for (let x = snap(lo); x <= room.w - size.w - lo + 1e-9; x += GRID) {
      for (let y = snap(lo); y <= room.h - size.h - lo + 1e-9; y += GRID) {
        const box = { x, y, w: size.w, h: size.h };
        if (solids.some((s) => axisGap(box, s) < gap)) continue;
        if (avoidPoints && points.some((p) => pointGap(box, p.x, p.y) < gap)) continue;
        const d = Math.hypot(x - middle.x, y - middle.y);
        if (d < bestD) {
          best = { x, y };
          bestD = d;
        }
      }
    }
    return best;
  };
  return search(true) ?? search(false) ?? middle;
}

/**
 * Somewhere on this wall a new pane fits, or the near end of it.
 *
 * The same shape as `freeSpot` and for the same reason: a second pane placed
 * exactly on top of the first is a control that appears not to have worked.
 */
export function freeWindowAt(
  wall: string,
  len: number,
  room: Room,
  windows: readonly FintechWindow[],
): number {
  const along = wallLength(wall, room);
  if (!(along > 0)) return WINDOW_EDGE;
  const last = along - WINDOW_EDGE - len;
  const taken = windows.filter((p) => p.wall === wall);
  for (let at = WINDOW_EDGE; at <= last + 1e-9; at += GRID) {
    const clear = taken.every(
      (p) => at >= p.at + p.len + WINDOW_MULLION || at + len + WINDOW_MULLION <= p.at,
    );
    if (clear) return snap(at);
  }
  return WINDOW_EDGE;
}

// ---------------------------------------------------------------------------
// What is selected, and what the readout says about it.
// ---------------------------------------------------------------------------

/** Which list a selection is in, and where. */
export interface Selection {
  list: 'solid' | 'window';
  index: number;
}

/**
 * The next thing round the cycle, or null when there is nothing to select.
 *
 * SOLIDS FIRST, THEN PANES, AND IT WRAPS. Tab is the only way a keyboard reaches
 * the bin and the steppers, so the cycle has to include every object rather than
 * only the ones a pointer can hit — a pane is 5 px of wall and is deliberately
 * not a tap target, which makes this its ONLY route to being selected.
 */
export function cycleSelection(
  current: Selection | null,
  counts: { solids: number; windows: number },
  by: number,
): Selection | null {
  const total = Math.max(0, counts.solids) + Math.max(0, counts.windows);
  if (total === 0) return null;
  const flat = current === null ? (by >= 0 ? -1 : 0) : indexOfSelection(current, counts);
  const next = ((flat + by) % total + total) % total;
  return next < counts.solids
    ? { list: 'solid', index: next }
    : { list: 'window', index: next - counts.solids };
}

/** Where a selection sits in the flat cycle above. */
function indexOfSelection(sel: Selection, counts: { solids: number; windows: number }): number {
  return sel.list === 'solid' ? sel.index : counts.solids + sel.index;
}

/** What the selection is called, in Russian — the server's own word where it has one. */
export function kindLabel(kind: string, kinds: readonly FintechAdminKind[]): string {
  const known = kinds.find((k) => k.key === kind);
  if (known && known.label.trim() !== '') return known.label;
  return kind.trim() === '' ? 'предмет' : kind;
}

/**
 * What a wall is called.
 *
 * HAND-WRITTEN, unlike a kind: the payload carries a label for every kind of
 * solid and none for a wall, so these three are the words. Total, because `wall`
 * is a plain string on the wire for the same reason `kind` is — a fourth wall
 * would be quoted rather than blanked.
 */
export function wallLabel(wall: string): string {
  switch (wall) {
    case 'top':
      return 'верх';
    case 'left':
      return 'лево';
    case 'right':
      return 'право';
    default:
      return wall.trim() === '' ? 'стена' : wall;
  }
}

/**
 * The selection, in metres, as one line — «стол · X 4,50 · Y 6,00 · Ш 2,50 · В 1,00».
 *
 * IT IS LOAD-BEARING RATHER THAN DECORATIVE. A drag on a plan cannot be asserted
 * on without comparing pixels, which this project does not do; this line is what
 * makes «the desk moved two and a half metres» a claim a test can read. It is
 * also the readout somebody editing on a phone actually works from, because a
 * quarter of a metre is five pixels there and the eye cannot tell four from
 * four-and-a-quarter.
 */
export function selectionReadout(
  sel: Selection | null,
  draft: FintechLayoutDraft,
  kinds: readonly FintechAdminKind[],
): string {
  if (sel === null) return 'ничего не выбрано';
  if (sel.list === 'solid') {
    const s = draft.solids[sel.index];
    if (!s) return 'ничего не выбрано';
    return `${kindLabel(s.kind, kinds)} · X ${metres(s.x)} · Y ${metres(s.y)} · Ш ${metres(s.w)} · В ${metres(s.h)}`;
  }
  const p = draft.windows[sel.index];
  if (!p) return 'ничего не выбрано';
  return `окно · ${wallLabel(p.wall)} · от ${metres(p.at)} · длина ${metres(p.len)}`;
}

// ---------------------------------------------------------------------------
// What the server said was wrong.
// ---------------------------------------------------------------------------

/**
 * Every problem code `ValidateLayout` can answer with.
 *
 * The list is here so the exhaustiveness of `problemText` is a thing a test can
 * walk rather than a thing a reviewer has to notice. The WIRE type stays a plain
 * string — a newer server must not break a browser somebody is holding — so this
 * bounds the switch and not the payload.
 */
export const LAYOUT_PROBLEM_CODES = [
  'bad_kind',
  'bad_size',
  'off_floor',
  'too_close',
  'spot_blocked',
  'split_floor',
  'too_many',
  'bad_window',
] as const;

export type LayoutProblemCode = (typeof LAYOUT_PROBLEM_CODES)[number];

/**
 * The Russian for a code this build knows.
 *
 * THE HAND-WRITTEN PART OF THIS FILE, in the sense `FINTECH_PROSE` is of
 * `fintechRules.ts`: nothing here is derived from anything, so a rule change on
 * the server is a change here as well. The `default` arm is an exhaustiveness
 * guard rather than a fallback — adding a code to the list above without adding
 * a sentence here fails the type-check.
 *
 * Each line says what to DO about it wherever there is anything to do, because
 * «too_close» on its own tells an admin what they can already see.
 */
function knownProblemText(code: LayoutProblemCode): string {
  switch (code) {
    case 'bad_kind':
      return 'неизвестный вид мебели';
    case 'bad_size':
      return 'слишком маленький или слишком большой — потяните за угол';
    case 'off_floor':
      return 'слишком близко к стене — отодвиньте вглубь комнаты';
    case 'too_close':
      return 'слишком близко к соседнему предмету — раздвиньте их';
    case 'spot_blocked':
      return 'стоит на месте, которое должно оставаться свободным';
    case 'split_floor':
      return 'мебель делит пол надвое — до части комнаты не дойти';
    case 'too_many':
      return 'слишком много предметов или окон';
    case 'bad_window':
      return 'окно не помещается на стене или налезает на соседнее';
    default: {
      const never: never = code;
      return String(never);
    }
  }
}

/** The Russian for any code, including one this build has never heard of. */
export function problemText(code: string): string {
  return (LAYOUT_PROBLEM_CODES as readonly string[]).includes(code)
    ? knownProblemText(code as LayoutProblemCode)
    : `непонятная претензия к этажу (${code})`;
}

/**
 * Which list a problem's index addresses.
 *
 * ONE INDEX AND TWO LISTS IS UNAMBIGUOUS because exactly one code is about a
 * window, and −1 belongs to the floor as a whole. Written down as its own
 * function because getting it wrong marks the wrong object, which is worse than
 * marking nothing: it sends somebody to fix a desk that is fine.
 */
export function problemTarget(problem: FintechLayoutProblem): Selection | null {
  if (problem.index < 0) return null;
  return problem.problem === 'bad_window'
    ? { list: 'window', index: problem.index }
    : { list: 'solid', index: problem.index };
}

/** One problem, as the panel lists it. */
export function problemLine(
  problem: FintechLayoutProblem,
  draft: FintechLayoutDraft,
  kinds: readonly FintechAdminKind[],
): string {
  const target = problemTarget(problem);
  const text = problemText(problem.problem);
  if (target === null) return `весь этаж — ${text}`;
  if (target.list === 'window') return `окно ${target.index + 1} — ${text}`;
  const solid = draft.solids[target.index];
  const what = solid ? kindLabel(solid.kind, kinds) : 'предмет';
  return `${what} ${target.index + 1} — ${text}`;
}

/**
 * The problems off a payload of unknown shape, or null when there are none to
 * read.
 *
 * A 422 CARRIES THEM IN THE ERROR BODY, which is the one place in this client
 * where a failure's payload is worth more than its code: «what is wrong with the
 * floor I just tried to save» is the whole answer. Null rather than an empty
 * array for an unreadable body, because the two mean opposite things — nothing
 * wrong, versus nothing known.
 */
export function problemsFrom(body: unknown): FintechLayoutProblem[] | null {
  if (typeof body !== 'object' || body === null) return null;
  const raw = (body as { problems?: unknown }).problems;
  if (!Array.isArray(raw)) return null;
  const out: FintechLayoutProblem[] = [];
  for (const entry of raw) {
    if (typeof entry !== 'object' || entry === null) continue;
    const { problem, index } = entry as { problem?: unknown; index?: unknown };
    if (typeof problem !== 'string' || typeof index !== 'number' || !Number.isFinite(index)) continue;
    out.push({ problem, index: Math.trunc(index) });
  }
  return out;
}

// ---------------------------------------------------------------------------
// The draft itself.
// ---------------------------------------------------------------------------

/** A copy of a layout's geometry, safe to drag about. */
export function draftFrom(layout: {
  solids: readonly FintechSolid[];
  windows: readonly FintechWindow[];
}): FintechLayoutDraft {
  return {
    solids: layout.solids.map((s) => ({ ...s })),
    windows: layout.windows.map((w) => ({ ...w })),
  };
}

/**
 * Whether two drafts describe the same floor.
 *
 * IT IS WHAT MAKES «ПРИМЕНИТЬ» A DECISION. Saving is destructive — it ends every
 * shift in progress — so an unchanged draft must not be savable: the button
 * would otherwise sit live and ready to throw the office out in exchange for
 * exactly nothing.
 */
export function sameLayout(a: FintechLayoutDraft, b: FintechLayoutDraft): boolean {
  if (a.solids.length !== b.solids.length || a.windows.length !== b.windows.length) return false;
  for (let i = 0; i < a.solids.length; i++) {
    const x = a.solids[i];
    const y = b.solids[i];
    if (x.kind !== y.kind || x.x !== y.x || x.y !== y.y || x.w !== y.w || x.h !== y.h) return false;
  }
  for (let i = 0; i < a.windows.length; i++) {
    const x = a.windows[i];
    const y = b.windows[i];
    if (x.wall !== y.wall || x.at !== y.at || x.len !== y.len) return false;
  }
  return true;
}

/** A count off the wire, made safe to put in a sentence. */
function count(v: number): number {
  return Number.isFinite(v) && v > 0 ? Math.round(v) : 0;
}

/**
 * What saving did, as the page reports it afterwards.
 *
 * The sibling of `rerollReport` in `fintechAdmin.ts` and deliberately its own
 * sentence: «Офис пересобран» is what the machine did, and this is what a person
 * did. Both say how many shifts went, because that is the half nobody can see —
 * the new floor is on the screen and the people thrown off the old one are
 * somewhere else entirely.
 */
export function installReport(ended: number): string {
  const n = count(ended);
  if (n === 0) return 'Этаж поставлен. В офисе никого не было.';
  const verb = plural(n, 'Закончилась', 'Закончились', 'Закончилось');
  const noun = plural(n, 'смена', 'смены', 'смен');
  return `Этаж поставлен. ${verb} ${n} ${noun}.`;
}
