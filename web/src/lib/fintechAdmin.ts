/**
 * «СИМУЛЯТОР ФИНТЕХА» — reading the floor an admin is looking at.
 *
 * THE PLAN IS THE SAME ROOM THE GAME DRAWS, so the placement helpers are the
 * game's own: `solidBox`, `windowBand`, `toPlane` and `floorStripe` all live in
 * `fintechPlane` and are reused here rather than re-derived. A second
 * implementation of "where does this desk go" is how the plan and the office
 * start disagreeing about the room they both claim to describe — and the plan's
 * whole job is to be believable about a floor somebody is about to change.
 *
 * WHAT IS NEW HERE IS EVERYTHING THAT IS NOT GEOMETRY: the Russian for a source,
 * for a spot, the way an installation time is written, and the roll-up the legend
 * draws. All of it is pure and unit-tested, for the reason the rest of this
 * client's derivations are: the template renders rows and never builds a sentence.
 *
 * THE PLAN IS NOT THE GAME. It shows the room from directly above with nothing
 * standing in it — no figures, no depth bands, no HUD — so none of the play view's
 * apparatus (the wall band a feet-anchored figure hangs into, the depth ramp, the
 * `--unit` scale) is wanted, and none of it is imported. The plan's box is the
 * room's own `w × h` and nothing else, which is what makes "the plan keeps the
 * served ratio" a claim a test can make.
 */

import { floorStripe, solidBox, toPlane, windowBand } from './fintechPlane';
import type { FintechAdminSpot, FintechRect, FintechWindow } from '../api/types';

/**
 * Where a floor came from, in Russian.
 *
 * EXHAUSTIVE TODAY AND STILL TOTAL TOMORROW. `source` is a plain string on the
 * wire for the same reason a solid's `kind` is: the server may learn a fourth
 * value, and a client already sitting in somebody's browser must not answer that
 * with a blank. `starting` is the only one the server sends today; `generated`
 * and `edited` arrive with the generator and the editor, and are answered here
 * now so that landing them is a server change rather than a two-repository one.
 *
 * The fallback shows the raw key rather than a guess or an empty string: an admin
 * seeing `сгенерированный` for a source this build has never heard of would be
 * told something false, and seeing nothing would be told nothing at all.
 */
export function sourceLabel(source: string): string {
  switch (source) {
    case 'starting':
      return 'стартовый';
    case 'generated':
      return 'сгенерированный';
    case 'edited':
      return 'изменённый вручную';
    default:
      return source.trim() === '' ? 'неизвестно' : source;
  }
}

/**
 * The one clock this page is read against.
 *
 * MOSCOW, ALWAYS, AND SAID SO. The audience is in one time zone and the server
 * writes RFC3339 in UTC, so a browser-local rendering would quietly read three
 * hours early for anybody travelling — and «когда встал этот этаж» is a fact
 * people compare against their own memory of the afternoon. Naming the zone in
 * the string is what makes that comparison safe.
 *
 * It also makes the tests deterministic: a fixed zone means the assertion does
 * not depend on the `TZ` of whichever runner picked the job up.
 */
export const OFFICE_TIME_ZONE = 'Europe/Moscow';

/**
 * When the floor was installed, as the status card writes it.
 *
 * An unparseable timestamp answers with the raw string rather than with
 * `Invalid Date`: it is a value off the wire, and showing it is what lets
 * somebody say what the server actually sent.
 */
export function formatInstalled(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return iso;
  const text = new Intl.DateTimeFormat('ru-RU', {
    timeZone: OFFICE_TIME_ZONE,
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(at);
  return `${text} МСК`;
}

/**
 * What a fixed catalogue point is, in Russian.
 *
 * The server sends `what` and no label, unlike `kinds` — deliberately, because a
 * kind is a taxonomy the editor will offer as a palette while a spot is a fact
 * about the game's own cast. Total for `sourceLabel`'s reason: an unknown marker
 * is drawn and named by its raw key rather than dropped, because a point the
 * validator protects and the plan hides is worse than one nobody has a word for.
 */
export function spotLabel(what: string): string {
  switch (what) {
    case 'player':
      return 'старт игрока';
    case 'boss':
      return 'старт лысого';
    case 'chaser':
      return 'старт клода';
    case 'npc':
      return 'Серега и Тёма';
    case 'bottle':
      return 'бутылка';
    case 'hookah':
      return 'кальян';
    default:
      return what.trim() === '' ? 'точка' : what;
  }
}

/** One row of the plan's legend: a marker, its name, and how many are drawn. */
export interface SpotLegendRow {
  what: string;
  label: string;
  count: number;
}

/**
 * The spot legend, rolled up by kind.
 *
 * IN FIRST-APPEARANCE ORDER rather than sorted, because the server assembles the
 * list in the order the game itself thinks about the cast — both spawns, then
 * Claude's, then the colleagues', then the props — and that order is more
 * meaningful than the alphabet. Sorting would also make the legend reshuffle
 * itself the day somebody renames a marker.
 *
 * The count is there because the six bottle spots are one entry with a six on it
 * rather than six identical lines, and because «сколько их вообще» is exactly
 * what an admin about to move furniture wants to know.
 */
export function spotLegend(spots: readonly FintechAdminSpot[]): SpotLegendRow[] {
  const rows: SpotLegendRow[] = [];
  const seen = new Map<string, SpotLegendRow>();
  for (const spot of spots) {
    const row = seen.get(spot.what);
    if (row) {
      row.count += 1;
      continue;
    }
    const fresh = { what: spot.what, label: spotLabel(spot.what), count: 1 };
    seen.set(spot.what, fresh);
    rows.push(fresh);
  }
  return rows;
}

/** A style object the template spreads onto an element. */
export type PlanStyle = Record<string, string>;

/**
 * One solid's box on the plan, as percentages.
 *
 * `solidBox` does the arithmetic and this only spells it as CSS — the same two
 * steps the play view takes, and deliberately the same first one. It is a
 * function rather than a template expression so that the mapping is testable
 * without mounting anything.
 */
export function planSolidStyle(rect: FintechRect, w: number, h: number): PlanStyle {
  const box = solidBox(rect, w, h);
  return {
    left: `${box.left * 100}%`,
    top: `${box.top * 100}%`,
    width: `${box.width * 100}%`,
    height: `${box.height * 100}%`,
  };
}

/**
 * One pane, as an offset and a span along the wall it is on.
 *
 * THE PLAN HAS NO WALL BAND OUTSIDE THE ROOM, which is what makes this simpler
 * than the play view's version of the same thing. The office draws the room
 * inside a taller plane because a figure is feet-anchored and hangs above its
 * coordinate; nothing stands on a plan, so the plan's box IS the room and a pane
 * is a strip along the inside of its edge. `windowBand` already answers in
 * fractions of the pane's own wall, so those fractions land directly on the box
 * with no share to subtract.
 *
 * A pane the helper could not place (an unknown wall, a zero length, a room with
 * no size) answers `undefined`, and the caller draws nothing: a strip of no
 * length in the corner would be worse than an absent one.
 */
export function planWindowStyle(win: FintechWindow, w: number, h: number): PlanStyle | undefined {
  const band = windowBand(win, w, h);
  if (band.span <= 0) return undefined;
  return { '--at': String(band.start), '--len': String(band.span) };
}

/**
 * A fixed catalogue point, as a position on the plan.
 *
 * The marker is centred on the coordinate by the stylesheet, so this writes the
 * point itself and nothing about the marker's size — which is a fixed pixel
 * count rather than a fraction of the room, because a spot is a POSITION and not
 * an area, and drawing it to scale would make the six bottles invisible.
 */
export function planSpotStyle(spot: { x: number; y: number }, w: number, h: number): PlanStyle {
  const at = toPlane(spot.x, spot.y, w, h);
  return { left: `${at.u * 100}%`, top: `${at.v * 100}%` };
}

/**
 * The plan's own box: the served ratio, and a metre grid in both directions.
 *
 * THE RATIO IS THE CLAIM THIS PAGE MAKES. A plan drawn at any other shape is a
 * plan that lies about how far apart two desks are, which is the one thing it
 * exists to show — so `w / h` goes onto the element as a custom property and the
 * stylesheet's `aspect-ratio` reads it, and the desktop test asserts the box
 * that comes out.
 *
 * BOTH GRID LINES COME FROM `floorStripe`, given each dimension in turn. It
 * answers "one metre, as a percentage of this dimension", which is what the
 * office's floorboards are — and reusing it for the columns as well is what
 * keeps the plan's metre and the game's metre the same number. A room it cannot
 * read answers a single full-size cell, which draws as a plain floor rather than
 * as a division by zero.
 */
export interface PlanBox {
  ratio: number;
  rowStripe: string;
  columnStripe: string;
}

export function planBoxFor(w: number, h: number): PlanBox {
  const usable = w > 0 && h > 0 && Number.isFinite(w) && Number.isFinite(h);
  return {
    // The starting office's own shape, for a payload that has not arrived yet:
    // an element with no ratio at all collapses, and a collapsed box measured by
    // a test is a pass that means nothing.
    ratio: usable ? w / h : 16 / 22,
    rowStripe: floorStripe(h),
    columnStripe: floorStripe(w),
  };
}

/** The room's size as the status card writes it: «16 × 22 м». */
export function formatRoom(w: number, h: number): string {
  const one = (v: number) => (Number.isFinite(v) ? String(Number(v.toFixed(2))).replace('.', ',') : '?');
  return `${one(w)} × ${one(h)} м`;
}
