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

/** One peer as a snapshot describes it, already converted from the wire. */
export interface PeerState {
  id: string;
  x: number;
  y: number;
  z: number;
  yaw: number;
  state: number;
}

/** One received snapshot, kept only for as long as it can still be needed. */
interface Frame {
  /** Local receive time in milliseconds. */
  at: number;
  peers: Map<string, PeerState>;
}

/**
 * How many frames to keep.
 *
 * Enough to cover the interpolation delay several times over on a connection
 * that is behaving, so an ordinary late frame still has a bracketing pair to
 * work with — and bounded, so a tab left open overnight does not accumulate a
 * history of a world nobody is looking at.
 */
export const BUFFER_FRAMES = 32;

export function createInterpolator(delayMs: number) {
  const frames: Frame[] = [];

  return {
    /** Records a snapshot's peers at the instant it arrived. */
    push(atMs: number, peers: PeerState[]): void {
      const m = new Map<string, PeerState>();
      for (const p of peers) m.set(p.id, p);
      frames.push({ at: atMs, peers: m });
      // Ordered by arrival, and arrival can be out of order on a network that
      // reorders. Sorting on push keeps every read below simple, and the array
      // is tiny.
      frames.sort((a, b) => a.at - b.at);
      while (frames.length > BUFFER_FRAMES) frames.shift();
    },

    /**
     * The world as it should be drawn at this instant: `now − delay`,
     * interpolated between the two frames that bracket it.
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
      const target = nowMs - delayMs;

      if (target <= frames[0].at) return [...frames[0].peers.values()];
      const newest = frames[frames.length - 1];
      if (target >= newest.at) return [...newest.peers.values()];

      let a = frames[0];
      let b = newest;
      for (let i = 1; i < frames.length; i++) {
        if (frames[i].at >= target) {
          a = frames[i - 1];
          b = frames[i];
          break;
        }
      }
      const span = b.at - a.at;
      const t = span > 0 ? (target - a.at) / span : 1;

      const out: PeerState[] = [];
      for (const [id, bs] of b.peers) {
        const as = a.peers.get(id);
        if (!as) {
          // Present only in the later frame — it appeared during the gap, so
          // there is nothing to interpolate from and its later position is the
          // only one that was ever true.
          out.push(bs);
          continue;
        }
        out.push({
          id,
          x: as.x + (bs.x - as.x) * t,
          y: as.y + (bs.y - as.y) * t,
          z: as.z + (bs.z - as.z) * t,
          yaw: as.yaw + shortestTurn(as.yaw, bs.yaw) * t,
          // Discrete, so not interpolated: halfway between alive and dead is
          // not a state anything can draw.
          state: bs.state,
        });
      }
      return out;
    },

    /** How many frames are buffered. For tests and for a diagnostic readout. */
    size(): number {
      return frames.length;
    },

    /** Drops everything, for when a run ends. */
    reset(): void {
      frames.length = 0;
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
