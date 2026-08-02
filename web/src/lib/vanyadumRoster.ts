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
 * took a peer entry from 71 bytes to 49 — and `st` has since put 7 of them back,
 * which is what the building's capacity is derived from.
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

import type { PeerState, SlopState } from './vanyadumInterp';
import type { VanyadumLevel } from './vanyadumLevel';
import { eyeZ } from './vanyadumStep';

/** A wire field that has to be a finite number, or it is nothing. */
function num(v: unknown): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : 0;
}

/**
 * The values a peer's `st` takes — everything a viewer has to be told about
 * somebody beyond where he is standing.
 *
 * ONE FIELD FOR FOUR VALUES, because the server's rules make them mutually
 * exclusive: a man on the floor cannot fire and cannot be hit again, and a
 * protected man can do neither either. Its precedence is the server's — down
 * beats protected beats hit beats fired — so a killing blow arrives as DOWN
 * rather than as HIT, and the acknowledgement for it is the figure going over.
 *
 * TWO INSTANTS AND TWO STATES, and the difference decides how each is drawn.
 * `FIRED` and `HIT` are true for exactly the tick they happened on, so they are
 * marked on the TRANSITION and shown for a few frames (vanyadumFlash). `DOWN`
 * and `PROTECTED` last their whole duration, so they are drawn as properties of
 * the figure for as long as the field carries them — a mark that flashed once
 * would say nothing about the three seconds that follow.
 *
 * Zero — which is the omitted state, and almost every peer on almost every tick
 * — is a man who is alive, unprotected and did nothing.
 */
export const PEER_FIRED = 1;
export const PEER_HIT = 2;
export const PEER_DOWN = 3;
export const PEER_PROTECTED = 4;

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
  /**
   * How often the building has put them on the floor, how many нейрослопы they
   * have put down, and how many friends they have put there.
   *
   * THE KILL COLUMN AND THE BETRAYAL COLUMN ARE THE JOKE, and it became one the
   * day there was something in the building worth killing. Friendly fire is
   * still on and a friend shot still scores NOTHING: he is not added to the
   * kills, he is published on his own line under his own heading, and the two
   * columns therefore say in two numbers what the game thinks of what you have
   * been doing. All three ride the standings at 1 Hz rather than the snapshot at
   * 20, because all three move a few times a MINUTE — and all three are omitted
   * at zero, so a building where nobody has shot anything carries none of them.
   */
  deaths: number;
  kills: number;
  betrayals: number;
}

/**
 * The glyphs those three columns are labelled with.
 *
 * SHARED BECAUSE THEY HAVE TWO USES TODAY, which is this project's bar for a
 * seam: the standings draw them, and the splash cheatsheet names them when it
 * tells a player what he is looking at. Two copies could drift, and a cheatsheet
 * describing a column by a symbol that is no longer on it is worse than one that
 * says nothing.
 *
 * NOT FROM THE CATALOGUE, unlike a pickup's icon, because the server publishes
 * no icon for any of them — it publishes the WORD for two (`slop.kills_title`
 * and `world.betrayals_title`), which is the part of the joke it owns.
 *
 * THREE THAT HAVE TO READ AS THREE DIFFERENT NUMBERS on a 360 px screen, at a
 * glance, with no room for a heading over any of them. So they are three
 * different KINDS of picture rather than three shades of one: a skull for what
 * the building did to you, a creature for what you shot, a knife for who you
 * shot. Two numbers a player cannot tell apart are worse than one number.
 */
export const DEATHS_ICON = '💀';
export const KILLS_ICON = '👾';
export const BETRAYALS_ICON = '🔪';

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
 *
 * `st` IS THE ONE FIELD HERE THAT IS NOT GEOMETRY, and it is on the peer rather
 * than derivable from one. Your own gun needs no marker — a barrel count falling
 * IS the shot — but nothing about another man's gun is on the wire to fall, and
 * a HIT moves nobody at all, so there is no value already on the frame that
 * could imply either. See PEER_FIRED above for the four values and for which of
 * them are instants. Omitted in the resting state, which is almost every peer on
 * almost every tick, so absent reads as nothing happening.
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
      // Set on every peer rather than only on the ones that have something to
      // say, so the object this loop produces has one shape: it is built four
      // times a tick forever, and a field that comes and goes is the kind of
      // thing an engine deoptimises for. Zero is the resting value AND the
      // omitted one, which is what makes `num` the whole of the decode.
      st: num(p.st),
    });
  }
  return out;
}

/**
 * Reads the нейрослоп array off a snapshot.
 *
 * A SEPARATE ARRAY UNDER ITS OWN KEY, which is a byte decision on the server
 * before it is a tidiness one here: merged into the peers, every entry would
 * carry a discriminator to say something the array it is in already says — seven
 * bytes at the JSON floor, on every man as well as every creature, twenty times
 * a second. Separated, each kind carries only the fields its own kind needs, and
 * a слоп is four of them against a peer's six.
 *
 * THE KEY IS `f` AND NOT `z`, and it is worth knowing why that is written down:
 * `z` is the reader's own eye height, and Go's encoder emits NEITHER of two
 * fields that share a tag. A collision here would have silently deleted the
 * player's eye height and every creature in the building at once.
 *
 * THE HEIGHT IS THE ROOM'S FLOOR, not an eye — a слоп is drawn as a billboard
 * standing on the ground, so what it needs is the ground. Derived from the
 * sector the wire named for the same reason a peer's is: this client holds the
 * level, and resolving the room from the POSITION instead would disagree with
 * the server at every doorway, which is exactly where слопы live. Derived here
 * at ingest, so the interpolator blends it and a слоп crossing between two rooms
 * at different heights glides up the step instead of snapping to it.
 */
export function decodeSlops(raw: unknown, level: VanyadumLevel): SlopState[] {
  if (!Array.isArray(raw)) return [];
  const out: SlopState[] = [];
  for (const entry of raw) {
    if (!entry || typeof entry !== 'object') continue;
    const s = entry as Record<string, unknown>;
    out.push({
      id: num(s.n),
      x: num(s.x) / 100,
      y: num(s.y) / 100,
      // Zero eye height: `eyeZ` is "the floor of this room plus however much you
      // want", and what a thing standing on that floor wants is none.
      z: eyeZ(level, num(s.s), 0),
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
      // All three omitted at zero on the wire, so absent reads as none — which
      // is every row in a building where nobody has shot anything yet.
      deaths: num(r.d),
      kills: num(r.k),
      betrayals: num(r.br),
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
