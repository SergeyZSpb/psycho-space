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
  /** The simulation tick this sample describes. The timeline, see below. */
  tick: number;
}

/**
 * How fast the estimated clock offset is allowed to creep UPWARDS, per sample.
 *
 * The estimator takes the minimum, and a minimum is a ratchet: without this a
 * single unusually-early frame would pin the offset for the rest of the shift,
 * and any drift between the two machines' clocks would slowly push the drawn
 * instant past the newest sample the buffer holds — which shows as the лысый
 * pausing more and more often. Creeping upwards costs about two per cent of the
 * error a second at twenty samples a second, which is far slower than jitter and
 * far faster than any real crystal drifts.
 */
const OFFSET_CREEP = 0.001;

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
 * `delayMs` is how far into the past to draw, and IT IS SERVED RATHER THAN
 * COMPUTED HERE — `sim.render_delay_ms` from the catalogue. Both ends need the
 * same number and only one of them can be authoritative: the office rewinds by
 * exactly this much to decide whether the лысый reached you, so a client picking
 * its own would be choosing how far behind the office believes it to be.
 *
 * The office derives it from its own publish rate (1.5 snapshot periods — one
 * would be exactly enough if frames were perfectly spaced, which is the one thing
 * they are not, and the extra half absorbs jitter and a single dropped frame), so
 * changing that rate moves this and the compensation together.
 */
export function createInterpolator(delayMs: number, tickMs: number) {
  const delay = Math.max(0, delayMs);
  const perTick = tickMs > 0 ? tickMs : 1;
  let buf: Timed[] = [];
  // Where tick zero sits on THIS machine's clock, estimated from arrivals. Null
  // until the first sample; see `push`.
  let offset: number | null = null;

  /** A sample's place on the local clock. */
  const timeOf = (t: Timed) => t.tick * perTick + (offset ?? 0);

  return {
    /**
     * Records the sample the office published on `tick`, arriving now.
     *
     * THE TIMELINE IS THE SERVER'S TICK, NOT THE ARRIVAL TIME, and that is the
     * whole of this module's correctness. Arrival time is easier — it needs no
     * clock mapping — but it inherits the network's jitter DIRECTLY AS RENDERED
     * VELOCITY: two frames sent 50 ms apart that arrive 20 ms and 80 ms apart
     * make the лысый appear to walk at two and a half times his speed and then at
     * three fifths of it, on the exact stretch where the dodge is being judged.
     * The tick is a perfect fixed-rate timeline and was already on every frame.
     *
     * It also fixes what a stall does. The hub drops a slow client's backlog but
     * keeps what fits, so a phone coming back from a tunnel receives several
     * frames within a few milliseconds of each other — on an arrival timeline
     * that is a world where everything happened at once, and the buffer's whole
     * span collapses. Keyed on the tick they simply carry their own spacing.
     *
     * The mapping between the two clocks is one number and it is estimated
     * ROBUSTLY RATHER THAN EXACTLY: the local time of tick zero is the smallest
     * `arrival − tick × period` seen, because the least-delayed frame is the best
     * evidence of the true offset, with a slow upward creep so a minimum cannot
     * ratchet (see OFFSET_CREEP). Nothing here needs the estimate to be right in
     * absolute terms — only stable — because everything drawn is a difference.
     */
    push(s: InterpSample, tick: number, nowMs: number): void {
      // Out-of-order or duplicate ticks are ignored rather than sorted in: the
      // transport delivers in order, so this is belt and braces — and a repeat
      // would make a span of zero.
      //
      // A tick a long way BACKWARDS is a different office rather than a late
      // frame: the process restarted, or a shift was picked up in a rebuilt one,
      // and its clock starts again from nothing. Dropping those would leave the
      // figure frozen for the rest of the shift, so the timeline is abandoned
      // and started again from what just arrived.
      const last = buf[buf.length - 1];
      if (last && tick <= last.tick) {
        if (last.tick - tick <= BUFFER) return;
        buf = [];
        offset = null;
      }

      const seen = nowMs - tick * perTick;
      if (offset === null || seen < offset) offset = seen;
      else offset += (seen - offset) * OFFSET_CREEP;

      buf.push({ ...s, tick });
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
      if (target <= timeOf(buf[0])) return { x: buf[0].x, y: buf[0].y, grin: grinOf(buf[0]) };

      for (let i = buf.length - 1; i > 0; i--) {
        const b = buf[i];
        const a = buf[i - 1];
        const ta = timeOf(a);
        const tb = timeOf(b);
        if (target >= ta && target <= tb) {
          const span = tb - ta;
          const t = span > 0 ? (target - ta) / span : 1;
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
      // The offset goes with it: a new shift is a new office, its ticks start
      // again from nothing, and an estimate made against the last one would put
      // every sample minutes into the past.
      offset = null;
    },

    /** For tests. */
    size(): number {
      return buf.length;
    },
  };
}

export type Interpolator = ReturnType<typeof createInterpolator>;
