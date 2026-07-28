/**
 * «ВАНЯДУМ» — turning thumbs and keys into the commands the server simulates.
 *
 * Pure, and deliberately so: not a listener, not a timer, not a ref. The view
 * feeds this module raw events and clock readings and gets back the exact
 * payload that goes on the wire, which is what lets every rule below — the dead
 * zone, the pitch clamp, the emit rate — be a unit test rather than something
 * you find out about on a phone.
 *
 * THE SEND RATE IS A PLATFORM BOUND, NOT A PREFERENCE. The socket allows ten
 * frames a second per connection (internal/realtime/conn.go), and that is a
 * security property this game fits inside rather than loosens. So commands are
 * emitted at four times the send rate and batched: a frame carries the
 * sub-steps that happened between sends, so a flick that starts and ends inside
 * one hundred-millisecond window still reaches the simulation.
 */

/** One sub-step, exactly as the server's Command reads it. */
export interface VanyadumCommand {
  /** Assigned by the predictor when it applies the command, not here. */
  seq?: number;
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
  const on = (...keys: string[]) => (keys.some((k) => held.has(k)) ? 1 : 0);
  const my = on('KeyW', 'ArrowUp') - on('KeyS', 'ArrowDown');
  const mx = on('KeyD', 'ArrowRight') - on('KeyA', 'ArrowLeft');
  if (mx === 0 || my === 0) return { mx, my };
  // Normalised so that holding two keys is not faster than holding one. The
  // server enforces this too, and now so does the client's own prediction —
  // all three agreeing is the point.
  const inv = 1 / Math.SQRT2;
  return { mx: mx * inv, my: my * inv };
}

export interface EmitterOptions {
  /** Commands a second: the send rate times the commands one frame may carry. */
  hz: number;
  /** Longest sub-step the server will simulate. */
  maxStepSeconds: number;
  /** Most commands one wake may produce, so a stalled tab cannot flood. */
  maxPerWake: number;
}

/**
 * Emits commands at a FIXED RATE, independent of the frame rate.
 *
 * This replaced a sampler that produced one command per animation frame and
 * merged the surplus before sending. **Merging is fatal once prediction
 * exists**: the client would predict from the individual commands while the
 * server simulated the merged ones, so the two would disagree by construction
 * and the correction would never settle. Whatever is predicted must be exactly
 * what is sent.
 *
 * So the rate is fixed instead — the send rate times the commands one frame may
 * carry, forty a second against ten frames of four. A send window then holds
 * exactly the number of commands it is allowed to, on a 144 Hz tablet and a
 * 30 Hz phone alike, and nothing ever has to be dropped or combined.
 *
 * It takes its clock as an argument rather than reading one, so a test drives a
 * whole second without waiting for one.
 */
export function createEmitter(opts: EmitterOptions) {
  const period = 1000 / opts.hz;
  let last: number | null = null;
  // Fractional leftovers are carried rather than discarded: dropping them makes
  // the client's simulated time drift slowly behind the server's, which shows
  // up as a correction that is always in the same direction.
  let owed = 0;
  let lastYaw = Number.NaN;
  let lastPitch = Number.NaN;

  return {
    /**
     * Returns the commands due at this instant — usually none, sometimes one,
     * more only after a stall.
     *
     * A command is emitted only when something HAPPENED: moving, or looking
     * somewhere new. Standing still with the screen untouched emits nothing, so
     * no frame is sent, so a player at rest costs the network nothing at all.
     * Turning counts as something, because aim is state the server must be told
     * about even when the feet did not move.
     */
    due(nowMs: number, axes: VanyadumAxes): VanyadumCommand[] {
      if (last === null) {
        last = nowMs;
        lastYaw = axes.yaw;
        lastPitch = axes.pitch;
        return [];
      }
      owed += nowMs - last;
      last = nowMs;

      const out: VanyadumCommand[] = [];
      // Bounded so a tab that was backgrounded for a minute does not emit a
      // minute of commands the moment it wakes. The server's own time budget
      // would refuse them anyway, and not creating them is what keeps the
      // client's prediction agreeing with that refusal.
      let budget = opts.maxPerWake;
      while (owed >= period && budget > 0) {
        owed -= period;
        budget -= 1;
        const idle =
          axes.mx === 0 && axes.my === 0 && axes.yaw === lastYaw && axes.pitch === lastPitch;
        if (idle) continue;
        lastYaw = axes.yaw;
        lastPitch = axes.pitch;
        out.push({
          dt: Math.min(opts.maxStepSeconds, period / 1000),
          mx: axes.mx,
          my: axes.my,
          yaw: axes.yaw,
          pitch: axes.pitch,
        });
      }
      // Whatever the wake budget could not cover is dropped rather than owed
      // forever, so a long stall costs one gap instead of a slow catch-up the
      // server would refuse anyway.
      if (owed > period * opts.maxPerWake) owed = 0;
      return out;
    },

    /** Drops everything, for when a run ends. */
    reset(): void {
      last = null;
      owed = 0;
      lastYaw = Number.NaN;
      lastPitch = Number.NaN;
    },
  };
}

export type Emitter = ReturnType<typeof createEmitter>;
