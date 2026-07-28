/**
 * «ВАНЯДУМ» — turning thumbs and keys into the commands the server simulates.
 *
 * Pure, and deliberately so: not a listener, not a timer, not a ref. The view
 * feeds this module raw events and clock readings and gets back the exact
 * payload that goes on the wire, which is what lets every rule below — the dead
 * zone, the pitch clamp, the coalescing when a frame is late — be a unit test
 * rather than something you find out about on a phone.
 *
 * THE SEND RATE IS A PLATFORM BOUND, NOT A PREFERENCE. The socket allows ten
 * frames a second per connection (internal/realtime/conn.go), and that is a
 * security property this game fits inside rather than loosens. So input is
 * SAMPLED at four times the send rate and BATCHED: a frame carries the sub-steps
 * that happened between sends, so a flick that starts and ends inside one
 * hundred-millisecond window still reaches the simulation instead of being
 * rounded away.
 */

/** One sub-step, exactly as the server's Command reads it. */
export interface VanyadumCommand {
  dt: number;
  mx: number;
  my: number;
  yaw: number;
  pitch: number;
}

/** Where the player is pushing and looking, right now. */
export interface VanyadumAxes {
  /** Strafe, −1..1. Positive is right. */
  mx: number;
  /** Walk, −1..1. Positive is forward. */
  my: number;
  /** Absolute view angles in radians. */
  yaw: number;
  pitch: number;
}

/** A point in screen pixels. */
export interface Point {
  x: number;
  y: number;
}

/**
 * How far from the stick's origin counts as nothing.
 *
 * A thumb resting on glass is never perfectly still, and without this the player
 * drifts while standing — which reads as the game being possessed rather than as
 * a sensitivity problem.
 */
export const STICK_DEADZONE = 0.14;

/**
 * How many radians of turn one pixel of drag is worth.
 *
 * Tuned for a 360 px phone: dragging across the full width turns roughly three
 * quarters of a circle, which is enough to spin round in one gesture without
 * making a small correction impossible.
 */
export const LOOK_SENSITIVITY = 0.0055;

/**
 * Converts a touch on the movement stick into movement axes.
 *
 * The stick's ORIGIN is wherever the thumb first landed rather than a fixed spot
 * on the screen — the single thing that makes an on-screen stick usable, because
 * a thumb cannot find a painted circle without looking at it, and looking at it
 * means not looking at the game.
 *
 * Screen coordinates have +y downwards, so walking forward is a NEGATIVE dy.
 */
export function stickVector(origin: Point, point: Point, radius: number): {
  mx: number;
  my: number;
} {
  if (radius <= 0) return { mx: 0, my: 0 };
  const dx = (point.x - origin.x) / radius;
  const dy = (point.y - origin.y) / radius;
  const mag = Math.hypot(dx, dy);
  if (mag < STICK_DEADZONE) return { mx: 0, my: 0 };
  // Clamped rather than normalised: a half-pushed stick must stay half speed,
  // which the server also relies on (see its TestHalfPressedStickIsSlower).
  const scale = mag > 1 ? 1 / mag : 1;
  return { mx: dx * scale, my: -dy * scale };
}

/**
 * Applies a look drag to the current view angles.
 *
 * Dragging right turns right, which is the server's convention read back: yaw
 * zero faces world +Y and increasing yaw swings towards +X. Dragging down looks
 * down. Pitch is clamped just short of straight up, because beyond that the view
 * matrix degenerates and the horizon rolls.
 */
export function applyLook(
  current: { yaw: number; pitch: number },
  dx: number,
  dy: number,
  maxPitch: number,
  sensitivity = LOOK_SENSITIVITY,
): { yaw: number; pitch: number } {
  const yaw = wrapAngle(current.yaw + dx * sensitivity);
  const pitch = Math.max(-maxPitch, Math.min(maxPitch, current.pitch - dy * sensitivity));
  return { yaw, pitch };
}

/** Wraps an angle into −π..π, so it never grows without bound over a long run. */
export function wrapAngle(a: number): number {
  const twoPi = Math.PI * 2;
  let out = a % twoPi;
  if (out > Math.PI) out -= twoPi;
  if (out < -Math.PI) out += twoPi;
  return out;
}

/**
 * Movement axes from the keys currently held.
 *
 * Desktop parity costs one function and buys two things worth more than it: the
 * game is developable without a touch emulator, and the full-stack end-to-end
 * suite can drive it with ordinary key events.
 */
export function axesFromKeys(held: Set<string>): { mx: number; my: number } {
  const on = (...keys: string[]) => keys.some((k) => held.has(k)) ? 1 : 0;
  const my = on('KeyW', 'ArrowUp') - on('KeyS', 'ArrowDown');
  const mx = on('KeyD', 'ArrowRight') - on('KeyA', 'ArrowLeft');
  if (mx === 0 || my === 0) return { mx, my };
  // Normalised so that holding two keys is not faster than holding one. The
  // server enforces this too; doing it here as well keeps the client's own
  // prediction — if it ever gets one — agreeing with the simulation.
  const inv = 1 / Math.SQRT2;
  return { mx: mx * inv, my: my * inv };
}

/**
 * Collapses a list of sub-steps into at most `max` of them, preserving the total
 * elapsed time.
 *
 * A tab that stalls — a garbage collection, a phone waking up, a slow frame —
 * produces more samples than a frame is allowed to carry. Dropping the surplus
 * would silently shorten the player's movement; merging preserves it. Each
 * bucket keeps the LAST sample's axes, because the most recent intent is the one
 * worth simulating.
 */
export function coalesce(cmds: VanyadumCommand[], max: number): VanyadumCommand[] {
  if (max <= 0) return [];
  if (cmds.length <= max) return cmds;
  const out: VanyadumCommand[] = [];
  const per = Math.ceil(cmds.length / max);
  for (let i = 0; i < cmds.length; i += per) {
    const bucket = cmds.slice(i, i + per);
    const last = bucket[bucket.length - 1];
    out.push({
      dt: bucket.reduce((sum, c) => sum + c.dt, 0),
      mx: last.mx,
      my: last.my,
      yaw: last.yaw,
      pitch: last.pitch,
    });
  }
  return out;
}

export interface SamplerOptions {
  /** Longest sub-step the server will simulate. Anything longer is clamped. */
  maxStepSeconds: number;
  /** Most sub-steps one frame may carry. */
  maxCommands: number;
}

/**
 * Accumulates sub-steps between sends.
 *
 * It holds a little state — the time of the last sample and the commands not yet
 * sent — and takes its clock as an argument rather than reading one, so a test
 * drives it through a whole second without waiting for one.
 */
export function createSampler(opts: SamplerOptions) {
  let lastMs: number | null = null;
  let pending: VanyadumCommand[] = [];
  // The last angles actually recorded. Compared against, so that merely LOOKING
  // somewhere new is worth sending while standing perfectly still is not.
  let lastYaw = Number.NaN;
  let lastPitch = Number.NaN;

  return {
    /**
     * Records the axes held at this instant.
     *
     * A SAMPLE IN WHICH NOTHING HAPPENED IS DROPPED. Standing still with the
     * screen untouched produces no command, so no frame is sent — where the
     * obvious version records "dt of nothing" sixty times a second and ships
     * ten frames a second of it forever, to a phone on mobile data, for a
     * simulation that would have done precisely nothing with them. Turning
     * counts as something: the aim is the client's own state, and the server
     * has to be told when it moves even if the feet did not.
     */
    sample(nowMs: number, axes: VanyadumAxes): void {
      if (lastMs === null) {
        lastMs = nowMs;
        lastYaw = axes.yaw;
        lastPitch = axes.pitch;
        return;
      }
      const dt = Math.max(0, Math.min(opts.maxStepSeconds, (nowMs - lastMs) / 1000));
      lastMs = nowMs;
      if (dt === 0) return;
      const idle =
        axes.mx === 0 && axes.my === 0 && axes.yaw === lastYaw && axes.pitch === lastPitch;
      if (idle) return;
      lastYaw = axes.yaw;
      lastPitch = axes.pitch;
      pending.push({ dt, mx: axes.mx, my: axes.my, yaw: axes.yaw, pitch: axes.pitch });
    },

    /**
     * Takes everything accumulated, coalesced to fit one frame. Returns an empty
     * array when there is nothing to send — and the caller must then send
     * nothing at all rather than an empty frame, because an empty frame still
     * spends one of the socket's ten.
     */
    take(): VanyadumCommand[] {
      const out = coalesce(pending, opts.maxCommands);
      pending = [];
      return out;
    },

    /** Drops everything, for when a run ends. */
    reset(): void {
      lastMs = null;
      pending = [];
      lastYaw = Number.NaN;
      lastPitch = Number.NaN;
    },

    pendingCount(): number {
      return pending.length;
    },
  };
}

export type Sampler = ReturnType<typeof createSampler>;
