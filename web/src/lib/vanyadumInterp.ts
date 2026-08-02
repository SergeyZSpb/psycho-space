/**
 * «ВАНЯДУМ» — entity interpolation, Gambetta's rung three.
 *
 * WHY PEERS ARE NOT PREDICTED. Your own movement can be predicted because you
 * know your own input. Another player's cannot: there is no way to know they
 * were about to turn left. So everything that is not you is drawn a fixed
 * distance **in the past** — far enough back that the two snapshots bracketing
 * that instant have both arrived — and its position is interpolated between
 * them.
 *
 * The cost is that peers are seen slightly late, and that is the trade the whole
 * industry makes: a peer a tenth of a second behind looks correct, and a peer
 * extrapolated forwards looks correct right up until they stop, at which point
 * they walk into a wall and snap back.
 *
 * The delay is **served, not chosen here** — lag compensation on the server
 * rewinds by exactly this number, so a client that picked its own would be
 * picking its own advantage.
 *
 * This module is pure and takes its clock as an argument, so a test can drive a
 * whole second of interpolation without waiting for one.
 */

/**
 * One peer as a snapshot describes it, already converted from the wire.
 *
 * IDENTIFIED BY A SLOT — a small integer naming a place in the building, not a
 * person. The wire stopped carrying a pseudonym per peer per tick when the
 * standings frame took over publishing who holds which place; see
 * vanyadumRoster for the decode and for what a slot changing hands costs this
 * buffer.
 */
export interface PeerState {
  slot: number;
  x: number;
  y: number;
  /**
   * Eye height, DERIVED from the sector the wire named rather than sent. See
   * `decodePeers` — the wire carries the room, and the room's floor is
   * something this client already holds.
   */
  z: number;
  yaw: number;
  /**
   * What the server says about this peer beyond where he is standing: his gun
   * going off, a shot landing on him, him lying on the floor, or him being
   * untouchable. See PEER_FIRED in vanyadumRoster for the four values.
   *
   * TWO OF THEM ARE LEVELS ON THE WIRE AND EVENTS ON THE SCREEN. Firing and
   * being hit are true for the one tick they happened on, which is fifty
   * milliseconds — several drawn frames, and longer while the buffer is holding
   * its newest frame — so whoever draws them has to mark the TRANSITION rather
   * than the value. See vanyadumFlash. The other two last their whole duration
   * and are drawn straight off this field, because a state with a duration is
   * not an event.
   *
   * NOT INTERPOLATED, and it could not be: an enumeration has no midpoint. It is
   * taken from the NEWER of the two frames being blended, so a mark lands on the
   * frame the drawn instant is moving towards rather than one it has already
   * passed.
   *
   * WHICH BOUNDS THE TWO INSTANTS, and the bound is worth knowing: a tick is
   * only ever the newer of a pair for one drawn frame if the phone is drawing at
   * the tick rate or faster. Below that — under twenty frames a second, where the
   * drawn instant advances by more than a tick per frame — a shot or a hit can
   * fall in a tick that is stepped straight over and never seen. That is left
   * alone rather than fixed: catching it would mean scanning the frames the draw
   * skipped, and a phone drawing ten frames a second has lost more than one
   * man's muzzle flash. The two STATES cannot be missed that way at all, because
   * they are true of every tick they last.
   */
  st?: number;
}

/** One received snapshot, placed on the server's own timeline. */
interface Frame {
  /** The simulation tick this frame describes. The timeline — see `push`. */
  tick: number;
  peers: Map<number, PeerState>;
}

/**
 * How many frames to keep.
 *
 * Enough to cover the interpolation delay several times over on a connection
 * that is behaving, so an ordinary late frame still has a bracketing pair to
 * work with — and bounded, so a tab left open overnight does not accumulate a
 * history of a world nobody is looking at.
 *
 * It is also the window inside which a lower tick counts as a LATE frame rather
 * than as a different building — see `push`.
 */
export const BUFFER_FRAMES = 32;

/**
 * How fast the estimated clock offset is allowed to creep UPWARDS, per frame.
 *
 * The estimator takes the minimum, and a minimum is a ratchet: without this the
 * two machines' clocks drifting apart would push the drawn instant further and
 * further past the newest frame the buffer holds, which shows as every peer
 * pausing more and more often. Creeping upwards costs about two per cent of the
 * error a second at twenty frames a second — far slower than the jitter it must
 * not chase, and far faster than any real crystal drifts.
 *
 * It is the answer to SLOW DRIFT and only to slow drift. A step in the one-way
 * delay is a minute away at this rate, which is why the estimate also carries a
 * ceiling; see `maxExcess`.
 */
const OFFSET_CREEP = 0.001;

/**
 * Builds the buffer every peer is drawn from.
 *
 * `delayMs` is how far into the past to draw — `sim.interp_delay_ms` from the
 * catalogue, never a number this client picks. `tickMs` is one simulation step,
 * `1000 / sim.hz`, and it is what puts a server tick on this machine's clock.
 */
export function createInterpolator(delayMs: number, tickMs: number) {
  const delay = Math.max(0, delayMs);
  const perTick = tickMs > 0 ? tickMs : 1;
  /**
   * How far above the estimated offset a frame's arrival may sit before the
   * ESTIMATE is pulled up to meet it.
   *
   * THE ARITHMETIC, BECAUSE IT IS THE LOAD-BEARING PART. A frame published on
   * `tick` and arriving at `now` contributes `seen = now − tick × period`, and
   * it is drawn at `tick × period + offset`, which is the same instant as
   * `now − (seen − offset)`. What is being drawn is `now − delay`. So the newest
   * frame is still ahead of the drawn instant only while `seen − offset` stays
   * under `delay`, and exactly at that boundary the drawn instant lands on it
   * and `sample` holds instead of interpolating. Leaving one period of headroom
   * — `seen − offset <= delay − period` — keeps the drawn instant a whole step
   * behind the newest, so a genuine PAIR brackets it rather than the newest
   * frame alone. ONE period and not two, because one period is also how long the
   * drawn instant then waits for the next frame: `now` advances between arrivals
   * while the newest frame does not, so the headroom is spent at exactly the rate
   * the next arrival replaces it.
   *
   * WITHOUT THE CEILING, ONE LUCKY PACKET COSTS A MINUTE. The estimator is a
   * minimum, which is right against jitter and wrong against a persistent shift:
   * a single unusually early frame pins the offset, every later frame's excess
   * is then the whole size of the shift, and the creep recovers at two per cent
   * of the error a second (OFFSET_CREEP at twenty frames a second) — a time
   * constant near fifty seconds. Driven through a render loop, one 20 ms packet
   * followed by a steady 200 ms one-way delay held EVERY sample at the newest
   * frame and drew two of every three consecutive pairs at an identical
   * position; the same steady delay without that one early packet never holds at
   * all. The lucky packet is the entire difference, and the render-loop test in
   * this module's spec is that transcript.
   *
   * So the minimum stays, the creep stays, and this bounds how stale the answer
   * either of them gives is allowed to be. It floors at zero rather than going
   * negative for a served delay shorter than one step — a configuration that
   * would leave no interpolation runway at all, and one this game does not
   * serve.
   */
  const maxExcess = Math.max(0, delay - perTick);
  let frames: Frame[] = [];
  // Where tick zero sits on THIS machine's clock, estimated from arrivals. Null
  // until the first frame; see `push`.
  let offset: number | null = null;

  /** A frame's place on the local clock. */
  const timeOf = (f: Frame) => f.tick * perTick + (offset ?? 0);

  return {
    /**
     * Records the peers the server published on `tick`, arriving now.
     *
     * THE TIMELINE IS THE SERVER'S TICK, NOT THE ARRIVAL TIME, and that is the
     * whole of this module's correctness. Arrival time is easier — it needs no
     * clock mapping — but it inherits the network's jitter DIRECTLY AS RENDERED
     * VELOCITY: two frames sent 50 ms apart that arrive 20 ms and 80 ms apart
     * make a peer appear to walk at two and a half times his speed and then at
     * three fifths of it, on precisely the stretch where a dodge is being judged
     * and a shot is being led. It collapses outright when a stalled client is
     * handed a burst of buffered frames at once, because on an arrival timeline
     * that is a world where everything happened in the same five milliseconds.
     *
     * AND IN THIS GAME IT IS NOT ONLY A SMOOTHNESS PROBLEM. Lag compensation
     * rewinds the building by exactly `InterpolationDelay`, on the assumption
     * that the client drew a peer at `serverTick − delay`. On an arrival
     * timeline the instant actually drawn drifts with the one-way delay, so the
     * server's rewind and this client's draw disagree by the jitter — and a
     * player is shot for standing where he was never displayed. Keying on the
     * tick makes that disagreement SMALLER RATHER THAN ZERO, and it is worth
     * being exact about which half goes. What disappears is the drift: both ends then count
     * the same fixed-rate steps, which is the assumption the compensation was
     * written against. What remains is everything neither end can measure — the
     * server rewinds by a round trip derived from the tick a client echoes back,
     * so it is quantised to whole simulation steps, smoothed by an exponential
     * average and inflated by however long that frame waited for the slower
     * input clock, while this side draws at an estimated offset carrying
     * whatever jitter sits above the running minimum. That residue is bounded
     * and known, instead of following the network wherever it goes.
     *
     * The mapping between the two clocks is one number and it is estimated
     * ROBUSTLY RATHER THAN EXACTLY: the local time of tick zero is the smallest
     * `arrival − tick × period` seen, because the least-delayed frame is the best
     * evidence of the true offset, with a slow upward creep so a minimum cannot
     * ratchet (see OFFSET_CREEP) and a hard ceiling so a stale minimum cannot
     * outlive the point at which it is still drawable (see `maxExcess`). Nothing
     * here needs the estimate to be right in absolute terms — only stable —
     * because everything drawn is a difference.
     */
    push(peers: PeerState[], tick: number, nowMs: number): void {
      // Out-of-order or duplicate ticks are ignored rather than sorted in: the
      // transport delivers in order, so this is belt and braces — and a repeat
      // would make a span of zero.
      //
      // A tick a long way BACKWARDS is a different building rather than a late
      // frame: the заброшка emptied and was regenerated, or the process
      // restarted, and either way the building that replaced it counts from
      // zero. Dropping those would leave every peer frozen for as long as this
      // client stayed connected, so the timeline is abandoned and started again
      // from what just arrived.
      const last = frames[frames.length - 1];
      if (last && tick <= last.tick) {
        if (last.tick - tick <= BUFFER_FRAMES) return;
        frames = [];
        offset = null;
      }

      const seen = nowMs - tick * perTick;
      if (offset === null || seen < offset) {
        // A faster frame than anything seen so far is better evidence of the
        // true offset than the estimate is, and it is taken whole.
        offset = seen;
      } else {
        // Otherwise two forces, and they answer different failures. The creep
        // handles ordinary drift between two crystals, slowly enough to sit
        // under the jitter. The ceiling handles a step: past `maxExcess` the
        // frame cannot be interpolated towards at all, so believing the old
        // minimum for another fifty seconds is choosing frozen peers over an
        // estimate that is merely less well evidenced.
        offset = Math.max(offset + (seen - offset) * OFFSET_CREEP, seen - maxExcess);
      }

      const m = new Map<number, PeerState>();
      for (const p of peers) m.set(p.slot, p);
      frames.push({ tick, peers: m });
      if (frames.length > BUFFER_FRAMES) frames = frames.slice(frames.length - BUFFER_FRAMES);
    },

    /**
     * The world as it should be drawn at this instant: `now − delay` on the
     * server's timeline, interpolated between the two frames that bracket it.
     *
     * ONLY WHOEVER THE NEWER FRAME NAMES IS DRAWN, and that is load-bearing
     * rather than incidental. A snapshot's peer array is idempotent full state
     * AND it is filtered to what the reader could plausibly see, so a peer
     * dropping out of it means one of two things — he left the building, or he
     * walked two rooms away — and neither is announced. Keeping a figure alive
     * because nothing said to remove it leaves a man standing in a doorway he
     * left a minute ago.
     *
     * NOTHING HERE DEBOUNCES THAT, AND THE SERVER IS WHY IT DOES NOT HAVE TO.
     * A sector is derived from a position, so somebody standing in a doorway
     * belongs to whichever room the last sub-centimetre of movement put him in
     * — and that flips at up to the tick rate without anybody walking anywhere.
     * It flips for the READER too, and that is the worse half: his own room
     * changing takes the whole visible set with it, so a peer in a third room
     * adjacent to one of the two would appear and vanish twenty times a second.
     *
     * The server holds the set instead of publishing it raw: a peer is sent
     * while he is visible now OR was visible within the last `visibleHold` (200
     * ms — see gamevanyadum's constant), so a flip has to stay flipped for four
     * consecutive ticks before anybody is dropped, and jitter at a boundary
     * never removes anybody at all. Two hundred milliseconds is also short
     * enough that a man who genuinely walked away is not left standing where he
     * was.
     *
     * That belongs THERE rather than here, and deliberately: a client smoothing
     * its own membership would be a second opinion about who is on screen, and
     * the day something shoots, the server's rewind and this client's draw have
     * to agree about exactly that. So this function keeps drawing whoever the
     * newer frame names, and the frame is what has already been made stable.
     *
     * Three honest degradations, none of which invents a position:
     *
     *   * **Nothing buffered** — draw nothing. A peer that has never been heard
     *     of has no last known position to hold.
     *   * **The target is older than anything buffered** (a long stall, then a
     *     reconnect) — show the oldest frame. It is stale, and saying so by
     *     being visibly behind is better than guessing.
     *   * **The target is newer than anything buffered** (the buffer ran dry) —
     *     **hold the newest frame rather than extrapolating**. Extrapolation is
     *     what makes a peer walk through a wall and snap back; holding makes
     *     them pause. A pause is a worse-looking correct answer, and correct
     *     wins.
     */
    sample(nowMs: number): PeerState[] {
      if (frames.length === 0) return [];
      const target = nowMs - delay;

      if (target <= timeOf(frames[0])) return [...frames[0].peers.values()];
      const newest = frames[frames.length - 1];
      if (target >= timeOf(newest)) return [...newest.peers.values()];

      let a = frames[0];
      let b = newest;
      for (let i = 1; i < frames.length; i++) {
        if (timeOf(frames[i]) >= target) {
          a = frames[i - 1];
          b = frames[i];
          break;
        }
      }
      const span = timeOf(b) - timeOf(a);
      const t = span > 0 ? (target - timeOf(a)) / span : 1;

      const out: PeerState[] = [];
      for (const [slot, bs] of b.peers) {
        const as = a.peers.get(slot);
        if (!as) {
          // Present only in the later frame — it appeared during the gap, so
          // there is nothing to interpolate from and its later position is the
          // only one that was ever true.
          out.push(bs);
          continue;
        }
        out.push({
          slot,
          x: as.x + (bs.x - as.x) * t,
          y: as.y + (bs.y - as.y) * t,
          // Interpolated exactly as x and y are, which is why the height is
          // derived at INGEST rather than at draw: a peer stepping through a
          // doorway between two rooms at different heights then glides up the
          // step instead of snapping to it.
          z: as.z + (bs.z - as.z) * t,
          yaw: as.yaw + shortestTurn(as.yaw, bs.yaw) * t,
          // From the NEWER frame, undiluted — a shot is a thing that happened on
          // one tick rather than a quantity with a midpoint. Taking it from `b`
          // means a mark appears as the drawn instant moves INTO the tick that
          // carried it, and stays set for as long as that frame is the newer of
          // the pair, which is what makes the transition rule in vanyadumFlash
          // necessary rather than optional.
          st: bs.st,
        });
      }
      return out;
    },

    /**
     * Throws away one slot's history, leaving everybody else's alone.
     *
     * A SLOT IS A PLACE, NOT A PERSON: it is handed back when its holder leaves
     * and given to the next arrival, so two people can occupy the same number
     * within a second of each other. Blending across that hand-over draws one
     * man sliding across the building into another man's position — the same
     * defect as interpolating between two different buildings, at the scale of
     * one figure.
     *
     * The caller is the standings frame, which is the only thing that ever says
     * a place has changed hands (see `changedHands`). It cannot be `reset`,
     * because that would pause every OTHER peer in the building every time
     * anybody walked in or out.
     */
    forget(slot: number): void {
      for (const f of frames) f.peers.delete(slot);
    },

    /** How many frames are buffered. For tests and for a diagnostic readout. */
    size(): number {
      return frames.length;
    },

    /**
     * Drops everything.
     *
     * Two callers, and the second is the interesting one: leaving the building,
     * and the building being REGENERATED under a client that is still standing
     * in it. Nothing ends in this game, so a buffer is never thrown away because
     * a match finished — it is thrown away because what it describes is gone.
     */
    reset(): void {
      frames = [];
      // The offset goes with it, because THE TIMELINE BELONGS TO ONE BUILDING:
      // the заброшка that replaces this one counts its ticks from zero again, so
      // an estimate made against the previous one would place every frame of the
      // new one minutes into the past.
      offset = null;
    },
  };
}

/**
 * The signed shortest way round from one angle to another.
 *
 * Without this, a peer turning through the wrap point — from just under π to
 * just over −π — is interpolated the long way and spins a full circle on the
 * spot. It is the single most visible bug in naive angle interpolation.
 */
export function shortestTurn(from: number, to: number): number {
  const twoPi = Math.PI * 2;
  let d = (to - from) % twoPi;
  if (d > Math.PI) d -= twoPi;
  if (d < -Math.PI) d += twoPi;
  return d;
}

export type Interpolator = ReturnType<typeof createInterpolator>;
