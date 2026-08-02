/**
 * «ВАНЯДУМ» — the marks a shot leaves, counted in DRAWN FRAMES rather than in
 * seconds: the muzzle flash on whoever fired, and the blow on whoever it landed
 * on.
 *
 * WHY THIS IS NOT A TIMER, AND IT IS THE WHOLE POINT OF THE MODULE. A mark that
 * is cleared on a clock can expire inside the very frame that was going to draw
 * it: the loop asks for the elapsed time, subtracts it, finds nothing left, and
 * renders a muzzle that was lit a microsecond ago. That is not a hypothetical —
 * a 0.05 s flash decremented by a frame's own `dt` is drawn zero times at twenty
 * frames a second, which is exactly the cheap phone this game is played on, and
 * twenty is also the clamp the view puts on `dt`. Counting frames cannot fail
 * that way: the mark is spent by being DRAWN, so it always survives at least one
 * draw, whatever the phone is managing.
 *
 * WHAT IT COSTS, AND WHY THAT IS THE RIGHT WAY ROUND. A frame count makes the
 * flash's DURATION depend on the frame rate — about 21 ms on a 144 Hz screen,
 * 50 ms at sixty, 150 ms at twenty. That is the useful direction: a slow phone
 * draws few frames, so a mark it can actually catch has to last longer there,
 * and a fast one gets three crisp frames instead of a smear. Every one of those
 * numbers is well under the half-second this project's acknowledgement rule caps
 * a mark at.
 *
 * WHY IT SURVIVES `prefers-reduced-motion`. Nothing here animates: a mark is
 * either in a frame or it is not, and it is cleared by a count of frames rather
 * than by an animation ending. So a player who asked for less movement gets the
 * same flash — which is what that rule requires, because somebody who asked for
 * less motion still has to be told that a gun went off.
 *
 * Pure and node-testable, like everything else in `lib/`. The renderer holds the
 * GPU; this holds the decision, so the decision can be asserted on without one.
 */

// The wire's own values, imported rather than re-typed: which peer state counts
// as which kind of mark is a fact about the protocol, and there is one place it
// is written down.
import { PEER_FIRED, PEER_HIT } from './vanyadumRoster';

/**
 * How many drawn frames a mark lasts.
 *
 * Three, which is the smallest number that is unmistakably a flash rather than a
 * dropped frame on a screen that is not being stared at. It is also the exact
 * length of the flash this game shipped with at sixty hertz, so nothing about
 * how the gun looks on an ordinary phone has changed — only what happens on a
 * slow one, where it used to be nothing at all.
 */
export const FLASH_FRAMES = 3;

/** One mark: started by an event, spent by being drawn. */
export interface Flash {
  /**
   * Starts the mark, or restarts one already running.
   *
   * Restarting rather than extending, so the second barrel a cadence later is
   * the same flash and not a longer one.
   */
  fire(): void;
  /**
   * Whether the mark belongs in the frame NOW BEING DRAWN — and counts it.
   *
   * Exactly one call per drawn frame, and the query and the count are one call
   * on purpose: two of them is precisely how a mark ends up cleared by the frame
   * that was about to show it.
   */
  frame(): boolean;
}

/** Builds one mark. */
export function createFlash(frames: number = FLASH_FRAMES): Flash {
  let left = 0;
  return {
    fire(): void {
      left = frames;
    },
    frame(): boolean {
      if (left <= 0) return false;
      left--;
      return true;
    },
  };
}

/** A peer as the frame being drawn describes him, for mark purposes only. */
export interface MarkedPeer {
  slot: number;
  /**
   * What the server said about him on the frame he was read from — see
   * PEER_FIRED in vanyadumRoster. Absent, or zero, is a man who did nothing.
   */
  st?: number;
}

/** Which peers are carrying which mark in the frame now being drawn. */
export interface PeerMarks {
  /** His обрез went off. */
  fired: Set<number>;
  /** A shot landed on him. */
  hit: Set<number>;
}

/**
 * The marks for everybody else in the building.
 *
 * IT IS A LEVEL ON THE WIRE AND AN EVENT ON THE SCREEN, and converting between
 * the two is this thing's real job. `st` rides a peer entry for the tick a shot
 * happened, and a tick is fifty milliseconds — three frames at sixty hertz, more
 * while the interpolation buffer is holding its newest frame. Drawn from the
 * level, a single shot would light the peer for as long as that frame was the
 * newest one, and a peer firing on consecutive ticks would simply glow. So what
 * starts a mark is the TRANSITION: the value appearing in a frame where this
 * peer's previous frame did not carry it, compared per slot and compared BEFORE
 * the previous value is overwritten.
 *
 * ONLY THE TWO INSTANTS ARE IN HERE. The other three — being down, being
 * protected and injecting — last their whole duration, so they are properties of
 * the figure and are drawn straight off `st`. Running them through a mark that
 * expires after three frames would show a man on the floor for a fifth of the
 * three seconds he is lying there, and would do the same to the two and a half
 * seconds an ampoule takes to empty.
 *
 * TWO KINDS, COMPARED SEPARATELY, because they can follow one another on
 * consecutive ticks: a man who fires and is shot for it goes 1 → 2, and a rule
 * that only asked "did `st` change" would light the wrong mark. Each kind is
 * therefore its own transition against its own previous value.
 *
 * KEYED BY SLOT, which is a place in the building rather than a person, so the
 * map is bounded by the заброшка's capacity however many people pass through it.
 * A slot changing hands mid-mark can therefore cost the new holder his first
 * one, if the previous holder's last observed frame carried the same value — a
 * missed mark on one tick, which heals the next time it happens. It cannot
 * produce a mark on somebody nothing happened to, which is the direction that
 * would matter.
 */
export function createPeerFlashes(frames: number = FLASH_FRAMES) {
  /** One mark per slot per kind, created on first sight and reused after. */
  const marks = new Map<string, Flash>();
  /** What `st` was on this slot's previous frame. */
  const before = new Map<number, number>();

  function mark(slot: number, kind: number, started: boolean): boolean {
    const key = `${slot}:${kind}`;
    let flash = marks.get(key);
    if (!flash) {
      flash = createFlash(frames);
      marks.set(key, flash);
    }
    if (started) flash.fire();
    return flash.frame();
  }

  return {
    /**
     * Which peers are marked in the frame now being drawn.
     *
     * One call per drawn frame, given whatever the interpolator produced for it.
     * A peer the frame does not name is not drawn at all, so his marks simply
     * wait — there is nothing on screen for them to be shown on.
     */
    frame(peers: readonly MarkedPeer[]): PeerMarks {
      const out: PeerMarks = { fired: new Set<number>(), hit: new Set<number>() };
      for (const p of peers) {
        const st = p.st ?? 0;
        // READ, then overwrite, then compare — which is this project's rule
        // stated exactly: a previous value overwritten before anything has
        // compared against it is a transition nobody can see.
        const was = before.get(p.slot) ?? 0;
        before.set(p.slot, st);
        if (mark(p.slot, PEER_FIRED, st === PEER_FIRED && was !== PEER_FIRED)) {
          out.fired.add(p.slot);
        }
        if (mark(p.slot, PEER_HIT, st === PEER_HIT && was !== PEER_HIT)) out.hit.add(p.slot);
      }
      return out;
    },
  };
}

export type PeerFlashes = ReturnType<typeof createPeerFlashes>;

/** A нейрослоп as the frame being drawn describes it, for mark purposes only. */
export interface MarkedSlop {
  id: number;
  x: number;
  y: number;
  z: number;
}

/** Where a slop-sized mark belongs in the world this frame. */
export interface Spot {
  x: number;
  y: number;
  z: number;
}

/** Which marks the нейрослопы are leaving behind in the frame now being drawn. */
export interface SlopMarks {
  /** Keyed by the id that has just left the picture, at the place it left it. */
  gone: Map<number, Spot>;
}

/**
 * The mark a нейрослоп leaves by ceasing to exist.
 *
 * ABSENCE IS THE WHOLE OF DYING, and that is the server's design rather than a
 * gap in it. A слоп is worth exactly one barrel, so there is no wounded state to
 * report and no health on the wire to fall; the creature is simply not in the
 * next frame's array. That makes "it was here and now it is not" the ONLY thing
 * a client can mark on — and an unmarked kill is an unfinished action, because
 * the whole acknowledgement for emptying a barrel into something would be a
 * silence indistinguishable from a miss.
 *
 * IT IS DERIVED, NOT ANNOUNCED, which is what makes it affordable: the frame
 * already carries the array, so this costs nothing on the wire, and it is the
 * same mark for every viewer because everybody's array loses the creature on the
 * same tick.
 *
 * THE ONE THING IT CANNOT TELL APART, stated plainly because it is the price. A
 * слоп also leaves the array by walking out of what the reader can see — his own
 * room and the rooms through its doorways — so a departure two rooms away is
 * marked as a death. Three things keep that cheap rather than misleading. The
 * server holds a creature in the array for `visibleHold` after it stops being
 * visible, so the last position anybody is given is one it walked to AFTER
 * leaving, which is inside a room the reader cannot see into; the mark is drawn
 * in the world with depth testing, so the wall that hid the слоп hides the mark
 * for it; and the standings carry the kill count, which is what actually says
 * whether anything died. What is left is a puff of light behind a wall, and the
 * alternative — marking nothing — makes every kill in the game silent. The link
 * going down empties the buffer and therefore marks everything in it, which is
 * the same trade at its least useful: three frames of light beside a notice
 * already saying the connection has gone.
 *
 * COUNTED IN DRAWN FRAMES and not in seconds, and cleared by being drawn, for
 * the reasons at the top of this file: it survives `prefers-reduced-motion`
 * intact, and a phone managing twenty frames a second still gets one.
 *
 * KEYED BY ID, which is a place in the building rather than a creature, so the
 * map is bounded by the population however many are shot. An id is not reused
 * for a whole spawn interval, so a mark can never be carried over onto its
 * successor.
 */
export function createSlopMarks(frames: number = FLASH_FRAMES) {
  /** One mark per id, created on the first death there and reused after. */
  const marks = new Map<number, Flash>();
  /** Where each running mark is being drawn. */
  const where = new Map<number, Spot>();
  /** Where each нейрослоп was on the previous drawn frame. */
  const last = new Map<number, Spot>();

  return {
    /**
     * One call per drawn frame, given whatever the interpolator produced for it.
     */
    frame(slops: readonly MarkedSlop[]): SlopMarks {
      const here = new Set<number>();
      for (const s of slops) here.add(s.id);

      // READ, then overwrite — this project's rule stated exactly. What is being
      // compared is the previous frame's membership against this one's, so the
      // previous frame's has to still be here when the comparison happens.
      for (const [id, at] of last) {
        if (here.has(id)) continue;
        let flash = marks.get(id);
        if (!flash) {
          flash = createFlash(frames);
          marks.set(id, flash);
        }
        flash.fire();
        // The place it was LAST SEEN, not where it is — there is no longer a
        // where. A mark is about the spot something happened at, and that spot
        // stops moving the instant the creature does.
        where.set(id, at);
      }
      last.clear();
      for (const s of slops) last.set(s.id, { x: s.x, y: s.y, z: s.z });

      const gone = new Map<number, Spot>();
      for (const [id, flash] of marks) {
        // Spent by being drawn, exactly once per frame, for every mark that
        // exists — including the ones that have already burnt out, because
        // `frame` is what makes them burn out.
        if (!flash.frame()) continue;
        const at = where.get(id);
        if (at) gone.set(id, at);
      }
      return { gone };
    },
  };
}
