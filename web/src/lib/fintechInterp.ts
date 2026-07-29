/**
 * «СИМУЛЯТОР ФИНТЕХА» — entity interpolation, which is the third Gambetta rung
 * and the one this game shipped without.
 *
 * WHAT IT IS FOR. Your own Карен is predicted, so he responds instantly and
 * moves at frame rate. The лысый cannot be: his intent is not yours to guess, so
 * all the client ever knows about him is a position ten times a second. The
 * first build handed each of those straight to a `transition: transform 100ms
 * linear` and let CSS smooth it, which is the obvious thing and is wrong in
 * three ways that all read as twitch:
 *
 *   * **Jitter becomes stop-start.** The transition runs for a fixed 100 ms from
 *     when a frame ARRIVES. Arrive at 130 ms and he glides for 100 and then
 *     stands still for 30 — right at the moment you are judging the dodge.
 *   * **A dropped frame becomes a jump.** The hub deliberately discards a slow
 *     client's backlog, because the snapshot is idempotent full state and that
 *     is safe. He then stalls a whole period and covers the gap in one glide.
 *   * **Velocity is discontinuous** at every frame boundary, and he changes
 *     direction constantly, because he is steering at you.
 *
 * WHAT IT DOES INSTEAD. It buffers the samples and draws him in the RECENT PAST
 * — far enough back that the two samples bracketing the drawn instant have both
 * already arrived — and interpolates between them. Jitter and a dropped frame
 * then cost nothing at all, because the renderer is never waiting on anything:
 * it is always working from two samples it already holds.
 *
 * THE COST IS THE DELAY, AND IT IS DELIBERATE. He is drawn about a sixth of a
 * second behind where the server has him. That is the standard trade and it is
 * the right one here: you are dodging his position on YOUR screen, the server
 * resolves the catch against its own, and the catch radius is far larger than
 * the distance he covers in that time. Nothing in this game shoots, so there is
 * nothing to lag-compensate and no rung four to build.
 *
 * It takes its clock as an argument everywhere, so a test drives a second of
 * jitter and packet loss without waiting for one.
 */

/**
 * A sampled entity: where it was, and — if it is the лысый — how pleased it was
 * about it.
 *
 * `grin` is OPTIONAL because this interpolator now runs for peers too, and
 * another Карен has no grin: the widening smile is the bald man's readout of how
 * much trouble you are in, and nobody else draws one. Absent reads as 0, which
 * is a figure with a straight face rather than a missing custom property.
 */
export interface InterpSample {
  x: number;
  y: number;
  grin?: number;
}

/**
 * What comes back out: the same thing with the grin RESOLVED.
 *
 * In and out are different types on purpose. A caller may push a peer with no
 * grin at all, but every read has to answer with a number the renderer can write
 * into a custom property — `grinOf` settles it once, here, rather than at each
 * of the four places a sample is returned or each of the callers.
 */
export interface InterpPoint {
  x: number;
  y: number;
  grin: number;
}

interface Timed extends InterpSample {
  /** Local arrival time. The server's tick is NOT used — see below. */
  at: number;
}

/**
 * How far behind the newest sample to draw, as a multiple of the snapshot
 * period.
 *
 * One period would be exactly enough if frames were perfectly spaced, which is
 * the one thing they are not. A half period of slack absorbs ordinary jitter and
 * a single dropped frame, and costs 150 ms at the 10 Hz this game publishes at.
 */
export const DELAY_PERIODS = 1.5;

/** How many samples to keep. Two are needed; the rest are slack for a stall. */
const BUFFER = 8;

function lerp(a: number, b: number, t: number): number {
  return a + (b - a) * t;
}

/** A missing grin is a straight face, not a missing value. */
function grinOf(s: InterpSample): number {
  return s.grin ?? 0;
}

/**
 * Builds an interpolator for one entity.
 *
 * `periodMs` is the snapshot period, taken from the served catalogue rather than
 * hardcoded, so changing the publish rate on the server changes the delay here
 * with no client edit.
 */
export function createInterpolator(periodMs: number) {
  const delay = Math.max(0, periodMs) * DELAY_PERIODS;
  let buf: Timed[] = [];

  return {
    /**
     * Records a sample as having arrived now.
     *
     * ARRIVAL TIME RATHER THAN THE SERVER'S TICK NUMBER, deliberately. The tick
     * is a perfectly good timeline and is on the wire, but using it means
     * mapping the server's clock onto this one and keeping that mapping honest
     * across a stall, a sleep and a reconnect. Arrival time needs no mapping and
     * is wrong only in the way that does not matter here: it inherits the
     * network's jitter, which is exactly what the delay above is for.
     */
    push(s: InterpSample, nowMs: number): void {
      // Out-of-order or duplicate arrivals are ignored rather than sorted in:
      // the transport delivers in order, so this is belt and braces.
      const last = buf[buf.length - 1];
      if (last && nowMs < last.at) return;
      buf.push({ ...s, at: nowMs });
      if (buf.length > BUFFER) buf = buf.slice(buf.length - BUFFER);
    },

    /**
     * Where to draw the entity now, or null while nothing has arrived yet.
     *
     * A caller that gets null draws nothing at all, which is correct: an entity
     * whose position is unknown must not be guessed at a default and then
     * snapped, and one frame of absence at the start of a shift is invisible.
     */
    at(nowMs: number): InterpPoint | null {
      if (buf.length === 0) return null;
      if (buf.length === 1) return { x: buf[0].x, y: buf[0].y, grin: grinOf(buf[0]) };

      const target = nowMs - delay;

      // Older than anything held — a long stall. Hold the oldest rather than
      // extrapolating backwards into a past nobody sampled.
      if (target <= buf[0].at) return { x: buf[0].x, y: buf[0].y, grin: grinOf(buf[0]) };

      for (let i = buf.length - 1; i > 0; i--) {
        const b = buf[i];
        const a = buf[i - 1];
        if (target >= a.at && target <= b.at) {
          const span = b.at - a.at;
          const t = span > 0 ? (target - a.at) / span : 1;
          return {
            x: lerp(a.x, b.x, t),
            y: lerp(a.y, b.y, t),
            grin: lerp(grinOf(a), grinOf(b), t),
          };
        }
      }

      // Newer than the newest: nothing has arrived for longer than the delay, so
      // the sender has gone quiet. HOLD rather than extrapolate — a boss who
      // keeps walking on a guess and is then corrected is worse than one who
      // pauses, because the guess is what the player is dodging.
      const newest = buf[buf.length - 1];
      return { x: newest.x, y: newest.y, grin: grinOf(newest) };
    },

    /** Drops everything, for when a shift ends. */
    reset(): void {
      buf = [];
    },

    /** For tests. */
    size(): number {
      return buf.length;
    },
  };
}

export type Interpolator = ReturnType<typeof createInterpolator>;
