/**
 * «ВАНЯДУМ» — the muzzle flash, counted in DRAWN FRAMES rather than in seconds.
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

/** A peer as the frame being drawn describes him, for flash purposes only. */
export interface FiringPeer {
  slot: number;
  /**
   * Whether the frame this peer was read from is one the server marked as
   * carrying a shot. Absent — which is nearly always — means no.
   */
  firing?: boolean;
}

/**
 * The marks for everybody else in the building.
 *
 * IT IS A LEVEL ON THE WIRE AND AN EVENT ON THE SCREEN, and converting between
 * the two is this thing's real job. The marker rides a peer entry for the tick a
 * shot happened, and a tick is fifty milliseconds — three frames at sixty hertz,
 * more while the interpolation buffer is holding its newest frame. Drawn from
 * the level, a single shot would light the peer for as long as that frame was
 * the newest one, and a peer firing on consecutive ticks would simply glow. So
 * what starts a mark is the TRANSITION: the marker being set in a frame where
 * this peer's previous frame did not have it, compared per slot and compared
 * BEFORE the previous value is overwritten.
 *
 * KEYED BY SLOT, which is a place in the building rather than a person, so the
 * map is bounded by the заброшка's capacity however many people pass through it.
 * A slot changing hands mid-flash can therefore cost the new holder his first
 * mark, if the previous holder's last observed frame also carried one — a missed
 * flash on one tick, which heals the next time he fires. It cannot produce a
 * mark on somebody who did not shoot, which is the direction that would matter.
 */
export function createPeerFlashes(frames: number = FLASH_FRAMES) {
  const marks = new Map<number, Flash>();
  const lit = new Map<number, boolean>();
  return {
    /**
     * Which peers' muzzles belong in the frame now being drawn.
     *
     * One call per drawn frame, given whatever the interpolator produced for it.
     * A peer the frame does not name is not drawn at all, so his mark simply
     * waits — there is nothing on screen for it to be shown on.
     */
    frame(peers: readonly FiringPeer[]): Set<number> {
      const out = new Set<number>();
      for (const p of peers) {
        const firing = p.firing === true;
        // The comparison, and it happens before the assignment below for the
        // reason this project states as a rule: a value overwritten first is a
        // transition nobody can see.
        if (firing && !lit.get(p.slot)) {
          let mark = marks.get(p.slot);
          if (!mark) {
            mark = createFlash(frames);
            marks.set(p.slot, mark);
          }
          mark.fire();
        }
        lit.set(p.slot, firing);
        if (marks.get(p.slot)?.frame()) out.add(p.slot);
      }
      return out;
    },
  };
}

export type PeerFlashes = ReturnType<typeof createPeerFlashes>;
