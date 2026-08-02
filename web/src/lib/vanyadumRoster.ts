/**
 * «ВАНЯДУМ» — the slot directory: which place in the building each figure
 * holds, and who is holding it.
 *
 * WHY THERE ARE TWO FRAMES AND NOT ONE. A snapshot repeats twenty times a
 * second forever, to a phone on mobile data, so the only things allowed on it
 * are the ones that change at that rate — a position and an angle. A name does
 * not: it is constant for as long as somebody is in the building. So a peer is
 * addressed by a SLOT, a small integer, and the mapping from slot to pseudonym
 * rides the standings frame once a second instead. That swap is most of what
 * took a peer from 71 bytes to 49, and it is the whole of what bought the fifth
 * place in the заброшка.
 *
 * THE TWO FRAMES SAY DIFFERENT THINGS, AND THAT IS THE POINT.
 *
 *   * A snapshot is what you can SEE. It is filtered to the reader's own room
 *     and the rooms through its doorways, so somebody two rooms away is simply
 *     absent from it — there is no departure event, and absence is the whole of
 *     leaving the set.
 *   * The standings are what is TRUE OF THE BUILDING. Unfiltered, identical for
 *     every reader, and including the reader himself — which is why the reader
 *     is told his own slot once, on the ready frame, rather than being expected
 *     to find himself in a list that names everybody.
 *
 * THE ONE HAZARD, AND IT IS AN ORDERING ONE. A slot is REUSED once its holder
 * leaves, so the same number can mean two different people a second apart. The
 * server publishes a standings frame on the tick a roster changes, ahead of that
 * tick's snapshots, so a client is told whose slot it is before it is ever asked
 * to draw him. A dropped board frame breaks that guarantee for at most a second,
 * and the client's answer must be SAFE rather than clever: draw the figure with
 * no name attached, and never invent one. `changedHands` is the other half —
 * the moment a slot's holder is seen to differ, that slot's interpolation
 * history is thrown away, because blending across a hand-over draws one man
 * sliding across the building into another man's position.
 *
 * Everything here is pure and node-testable, which is the same split the rest of
 * this game keeps: the wire is decoded and reasoned about in `lib/`, and only
 * the drawing needs a browser.
 */

import type { PeerState } from './vanyadumInterp';
import type { VanyadumLevel } from './vanyadumLevel';
import { eyeZ } from './vanyadumStep';

/** A wire field that has to be a finite number, or it is nothing. */
function num(v: unknown): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : 0;
}

/**
 * One row of the standings: a place in the building and who is in it.
 *
 * `slot` is the same number a snapshot's peer entry carries, and that
 * correspondence is the entire reason both frames exist in the shape they do.
 */
export interface BoardRow {
  slot: number;
  /** The per-process pseudonym — never an account id, and never durable. */
  name: string;
  /** How long they have been in the building. */
  seconds: number;
  /** What they are carrying, keyed by the catalogue's `grants`. */
  bag: Record<string, number>;
}

/**
 * Reads the peer array off a snapshot.
 *
 * THE HEIGHT IS DERIVED RATHER THAN SENT, and both halves of that are
 * deliberate. The wire carries the peer's SECTOR, three characters instead of
 * the six an eye height cost — and the sector is the more useful of the two,
 * because this client already holds the level and can look a floor up in it.
 * Deriving the height from the peer's POSITION instead would have cost nothing
 * on the wire and been wrong at every doorway: a shared boundary belongs to both
 * rooms, so two ends resolving it independently can disagree about which room a
 * man in a doorway is in, and he would bob by a whole step while standing still.
 *
 * Derived HERE, at ingest, rather than when the figure is drawn, so the
 * interpolator blends the height exactly as it blends the position and a peer
 * stepping between two rooms glides up instead of snapping.
 *
 * A sector index the level has no room for falls back to the bare eye height,
 * which is what the server's own `EyeZ` answers for the same question.
 */
export function decodePeers(
  raw: unknown,
  level: VanyadumLevel,
  eyeHeight: number,
): PeerState[] {
  if (!Array.isArray(raw)) return [];
  const out: PeerState[] = [];
  for (const entry of raw) {
    if (!entry || typeof entry !== 'object') continue;
    const p = entry as Record<string, unknown>;
    // Positions are centimetres and angles thousandths of a radian, because
    // this frame repeats twenty times a second forever.
    out.push({
      slot: num(p.n),
      x: num(p.x) / 100,
      y: num(p.y) / 100,
      z: eyeZ(level, num(p.s), eyeHeight),
      yaw: num(p.yaw) / 1000,
    });
  }
  return out;
}

/**
 * Reads the standings frame's rows.
 *
 * FULL STATE, exactly as a snapshot is: the newest board is the truth, and it is
 * replaced rather than merged. There is no departure row and no "he left" field,
 * because a person who is gone is simply not in the next one.
 *
 * A row carrying nothing has no `c` at all — omitting beats sending an empty
 * object on a frame that goes to everybody — so an absent bag reads as an empty
 * one here rather than as a missing field.
 */
export function decodeBoard(raw: unknown): BoardRow[] {
  if (!Array.isArray(raw)) return [];
  const out: BoardRow[] = [];
  for (const entry of raw) {
    if (!entry || typeof entry !== 'object') continue;
    const r = entry as Record<string, unknown>;
    const bag: Record<string, number> = {};
    if (r.c && typeof r.c === 'object') {
      for (const [k, v] of Object.entries(r.c as Record<string, unknown>)) bag[k] = num(v);
    }
    out.push({
      slot: num(r.n),
      name: typeof r.i === 'string' ? r.i : '',
      seconds: num(r.s),
      bag,
    });
  }
  return out;
}

/**
 * Which slots have changed hands between two standings frames.
 *
 * A slot is a place and not a person, so the only evidence that the person in it
 * has changed is the NAME against it changing. A slot named in the new board
 * that the old one named differently — or did not name at all, because its
 * previous holder had already left — has a new occupant standing in it, and
 * whatever this client remembers about where that place was a moment ago belongs
 * to somebody else.
 *
 * A slot that has merely gone quiet is deliberately NOT reported: nobody is
 * standing in it, so there is nothing to draw wrongly, and its stale positions
 * age out of the interpolation buffer on their own. It is reported the moment
 * somebody takes it.
 */
export function changedHands(before: BoardRow[], after: BoardRow[]): number[] {
  const was = new Map<number, string>();
  for (const row of before) was.set(row.slot, row.name);
  const out: number[] = [];
  for (const row of after) {
    if ((was.get(row.slot) ?? '') !== row.name) out.push(row.slot);
  }
  return out;
}

/**
 * How long somebody has been in the building, as `M:SS`.
 *
 * Minutes rather than hours all the way up, because the readout sits beside a
 * twelve-character pseudonym on a 360 px screen and a third field would push the
 * name off it. Somebody who has been in the заброшка for two hours reads 120:00,
 * which is longer but never ambiguous.
 */
export function clock(seconds: number): string {
  const total = Number.isFinite(seconds) && seconds > 0 ? Math.floor(seconds) : 0;
  const secs = total % 60;
  return `${Math.floor(total / 60)}:${secs < 10 ? '0' : ''}${secs}`;
}
