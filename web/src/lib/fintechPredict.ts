/**
 * «СИМУЛЯТОР ФИНТЕХА» — client-side prediction, and the commands that feed it.
 *
 * THE PROBLEM. Snapshots arrive ten times a second. Without prediction the man
 * on the screen only moves when one lands, so the world updates at ten hertz
 * while the phone redraws at sixty — which looks exactly like ten frames a
 * second — and every push of the stick waits out a round trip before anything
 * happens. «ВАНЯДУМ» already paid for that lesson; this game inherits the
 * answer rather than rediscovering it.
 *
 * THE MECHANISM. Every command is stamped with a sequence number, applied
 * locally through the same `step` the server runs, and kept pending. The server
 * echoes the last sequence it folded in. On each snapshot the client drops
 * everything acknowledged, resets to the server's authoritative position, and
 * replays what is still pending on top of it. Movement then responds in zero
 * milliseconds and updates at frame rate, and the server still decides
 * everything.
 *
 * WHAT IS PREDICTED AND WHAT IS NOT. Position, and the dash timers that decide
 * how fast the position moves. **The money is not predicted** — the salary, the
 * multiplier and the streak are read straight off the snapshot, because they are
 * the score and a score that flickered between a guess and the truth would be
 * worse than one that is simply ten hertz. The predictor keeps its own salary
 * only because `step` computes one; nothing reads it.
 *
 * WHAT THIS IS NOT. It is not authority. A prediction is a guess that is usually
 * right; when the server disagrees, the server wins, without negotiation.
 */

import { step, type StepCommand, type StepConstants, type StepPlayer } from './fintechStep';
import type { FintechRect } from '../api/types';

/**
 * How many already-sent commands ride along on each frame.
 *
 * Also mirrored rather than served. The server drops any sequence it has already
 * applied, so a repeat costs a few bytes and buys immunity to a single lost
 * packet — and the pending list this reads from has to exist for reconciliation
 * anyway, so the redundancy is very nearly free.
 */
export const REDUNDANT_COMMANDS = 6;

/**
 * A disagreement smaller than this is applied silently, in metres.
 *
 * Positions cross the wire quantised to the centimetre and the two runtimes'
 * arithmetic is never bit-identical, so without a floor every single snapshot
 * would start a correction and the figure would shiver permanently.
 */
export const SILENT_CORRECTION = 0.01;

/**
 * A disagreement larger than this is snapped rather than eased, in metres.
 *
 * Beyond a couple of metres the client is not slightly wrong, it is somewhere
 * else — a refused move, a resync after a stall — and gliding somebody across
 * the office to arrive at the truth is both slower and stranger than putting
 * them there.
 */
export const SNAP_CORRECTION = 2;

/** How long a correction takes to disappear, in seconds. */
export const CORRECTION_SECONDS = 0.12;

export interface PredictorOptions {
  desks: readonly FintechRect[];
  constants: StepConstants;
  /** Where the player starts, in metres. */
  start: { x: number; y: number };
}

/** Where the player is authoritatively, as a snapshot reports it. */
export interface Authoritative {
  /** Metres — the caller converts from the wire's centimetres. */
  x: number;
  y: number;
  /** The last command sequence the server folded in. */
  ack: number;
  /**
   * Seconds until the next dash — the caller converts from the wire's `dc`
   * milliseconds, reading an absent field as zero.
   *
   * IT IS FOLDED IN LIKE THE POSITION, AND FOR THE SAME REASON. The predicted
   * player's own cooldown only moves inside `apply`, and `apply` only runs when
   * a command is emitted — so a player standing perfectly still, which is this
   * game's default state, never runs their cooldown down at all. It would still
   * read 1.9 s when the server had long been ready, `step` would refuse the next
   * dash locally while the server granted it, and the client would predict a
   * walk against a 20 m/s burst: five and a half metres of divergence, on every
   * dash after the first.
   *
   * The server already publishes this on every snapshot, so agreeing with it
   * costs nothing on the wire — which is the whole argument for taking it from
   * here rather than keeping the client talking for the 2.2 s it would take to
   * run the timer down locally.
   */
  dashCooldown: number;
  /**
   * Seconds of Claude Code's slow remaining — the caller converts from the wire's
   * `sl` milliseconds, reading an absent field as zero.
   *
   * FOLDED IN FOR EXACTLY THE DASH COOLDOWN'S REASON, and it is the same bug
   * waiting to happen. The predicted player's own timer only moves inside `apply`,
   * and `apply` only runs when a command is emitted — so a player standing
   * perfectly still, which is this game's default state, never runs the slow down
   * locally at all. It would still read 4 s when the office had long since let it
   * expire, and the client would predict a 5.12 m/s walk against the server's 6.4.
   * The cooldown version of this cost 5.5 m of divergence per dash; this one costs
   * 1.28 m/s for as long as the two disagree.
   */
  slowLeft: number;
}

/**
 * Builds the predictor.
 *
 * It owns the pending list, the predicted state and the correction offset, and
 * takes its clock as an argument everywhere — so a test drives a whole second of
 * prediction, reconciliation and smoothing without waiting for one.
 */
export function createPredictor(opts: PredictorOptions) {
  const { desks, constants } = opts;

  let seq = 0;
  /**
   * The commands the server has not acknowledged, each with the predicted state
   * as it stood BEFORE that command was applied.
   *
   * The `before` is what makes a replay a replay rather than a second
   * application. `predicted` already contains every one of these commands, so
   * replaying them on top of it would decrement each timer twice — the position
   * is safe because it is overwritten from the snapshot, and everything else is
   * not. Measured: a walking client burned 1.8 s of dash cooldown in 0.9 s of
   * wall time, and a dash whose commands were still in flight ended in half its
   * length. The first unacknowledged command's `before` is exactly the state the
   * server's snapshot describes, so replaying from there is exact for every
   * field, not just the two the wire carries.
   */
  let pending: { cmd: StepCommand; before: StepPlayer }[] = [];
  let predicted: StepPlayer = {
    x: opts.start.x,
    y: opts.start.y,
    salary: 0,
    streak: 0,
    moveGrace: 0,
    dashLeft: 0,
    // Zero rather than a guess, and overwritten from the first snapshot like the
    // cooldown beside it: starting a shift unslowed is right, and starting it
    // wrongly slowed would predict 5.12 m/s against the office's 6.4.
    slowLeft: 0,
    dashCooldown: 0,
    dashDx: 0,
    dashDy: 0,
  };
  // The offset being eased out, in metres. Added to the predicted position when
  // drawing, and decayed towards zero every frame.
  let errX = 0;
  let errY = 0;

  return {
    /**
     * Applies one command locally and returns it, stamped with its sequence.
     *
     * The caller sends what comes back. Nothing is predicted that is not also
     * sent, and nothing is sent that is not also predicted — the two lists being
     * the same list is what makes reconciliation exact, and it is why the
     * emitter below produces commands at a fixed rate instead of merging them.
     */
    apply(cmd: Omit<StepCommand, 'seq'>): StepCommand {
      seq += 1;
      const stamped: StepCommand = { ...cmd, seq };
      // `step` returns a fresh object, so keeping the old one costs nothing and
      // nothing can mutate it afterwards.
      pending.push({ cmd: stamped, before: predicted });
      predicted = step(desks, predicted, stamped, constants);
      return stamped;
    },

    /**
     * Folds in an authoritative position: drop what it acknowledges, rewind to
     * it, replay the rest.
     *
     * The replay is the part that is easy to get wrong by omitting: without it
     * the client snaps back to where the server was a round trip ago and then
     * re-runs forwards from there on the next frame, which is the classic
     * rubber-band.
     *
     * THE REWIND IS THE PART THAT IS EASY TO GET WRONG BY HALVING. Replaying on
     * top of the CURRENT prediction resets the position — which the snapshot
     * overwrites anyway — while applying every pending command's effect on the
     * timers, the streak and the grace a second time. See `pending`.
     */
    reconcile(a: Authoritative): void {
      pending = pending.filter((p) => (p.cmd.seq ?? 0) > a.ack);

      // Where the figure is being DRAWN right now — predicted plus whatever
      // correction is still easing out. Measured against this rather than
      // against the raw prediction, which is what stops a second correction
      // arriving mid-glide from jumping.
      const drawnX = predicted.x + errX;
      const drawnY = predicted.y + errY;

      // The state this client believed in at the moment the server's number
      // describes, with the two fields the server is authoritative about
      // written over it. With nothing pending that moment is now.
      const base = pending.length ? pending[0].before : predicted;
      let replayed: StepPlayer = {
        ...base,
        x: a.x,
        y: a.y,
        dashCooldown: a.dashCooldown,
        slowLeft: a.slowLeft,
      };
      for (const p of pending) replayed = step(desks, replayed, p.cmd, constants);
      predicted = replayed;

      const dx = drawnX - predicted.x;
      const dy = drawnY - predicted.y;
      const off = Math.hypot(dx, dy);
      if (off < SILENT_CORRECTION || off > SNAP_CORRECTION) {
        // Too small to see, or too big to glide. Both end with the figure
        // exactly where the server says, which is the only outcome that matters.
        errX = 0;
        errY = 0;
      } else {
        errX = dx;
        errY = dy;
      }
    },

    /** Decays the correction. Called once per drawn frame with its own dt. */
    tick(dt: number): void {
      if (errX === 0 && errY === 0) return;
      // Exponential, not linear: it lands smoothly rather than stopping dead,
      // and it cannot overshoot however long or short the frame was.
      const k = Math.exp(-dt / CORRECTION_SECONDS);
      errX *= k;
      errY *= k;
      if (Math.hypot(errX, errY) < 1e-4) {
        errX = 0;
        errY = 0;
      }
    },

    /** Where to draw the player this frame, in metres. */
    view(): { x: number; y: number } {
      return { x: predicted.x + errX, y: predicted.y + errY };
    },

    /**
     * Where to DRAW the player, carried forward over the time that has elapsed
     * since the last command but has not yet become one.
     *
     * This is the difference between a figure that moves and a figure that
     * flickers. `predicted` only advances inside `apply`, which happens forty
     * times a second, while this is called every animation frame — so without
     * the carry the player is redrawn in the same place two or three times and
     * then teleported, forty times a second, for ever.
     *
     * **It is not extrapolation and it cannot drift.** The step is over time
     * that has already passed, using the axes the thumb is on *now*, and it runs
     * against a COPY — so when the real command arrives a moment later it starts
     * from the untouched `predicted` and lands exactly where this had already
     * drawn him.
     *
     * Releasing the stick is the one case where it steps BACK, by however much
     * of a sub-step the carry was showing — measured at a walk, 0.107 m on one
     * frame, which is two pixels on a phone. It is honest rather than wrong: the
     * carry was drawing a command that then never came. Worth knowing before
     * reading a small backwards step as this game's older and much larger one.
     *
     * A dash in progress carries at dash speed, because the copy still holds the
     * dash timer and `step` reads it. That is the whole point: the burst is ten
     * commands long, so it is the one move that was almost entirely stepping.
     */
    viewAhead(dt: number, axes: { mx: number; my: number }): { x: number; y: number } {
      if (!(dt > 0)) return this.view();
      const ahead = step(desks, predicted, { seq: 0, dt, mx: axes.mx, my: axes.my }, constants);
      return { x: ahead.x + errX, y: ahead.y + errY };
    },

    /**
     * The commands the server has not acknowledged, newest last.
     *
     * Sent again in each frame up to `max`; see REDUNDANT_COMMANDS.
     */
    unacknowledged(max: number): StepCommand[] {
      return max <= 0 ? [] : pending.slice(-max).map((p) => p.cmd);
    },

    /**
     * Whether a dash is still running in the predicted player.
     *
     * THE EMITTER NEEDS THIS AND NOTHING ELSE DOES. A dash is a committed
     * movement that outlives the one command which started it by nine more, and
     * the commonest dash in this game is tapped from a standstill — so without
     * this the emitter falls silent the instant the request has been carried,
     * the prediction freezes a fifth of the way through the burst, and the
     * server runs the other four fifths on its own. See `due`.
     */
    dashing(): boolean {
      return predicted.dashLeft > 0;
    },

    /** For tests and for the shift ending. */
    pendingCount(): number {
      return pending.length;
    },
    /** The raw prediction, without the correction offset. For tests. */
    raw(): StepPlayer {
      return { ...predicted };
    },
    /** How far the drawn position currently is from the predicted one. */
    correction(): number {
      return Math.hypot(errX, errY);
    },
  };
}

export type Predictor = ReturnType<typeof createPredictor>;

// ---------------------------------------------------------------------------
// The emitter: turning a thumb and a clock into the commands that get predicted
// and sent.
// ---------------------------------------------------------------------------

export interface EmitterOptions {
  /** Commands a second: the send rate times the commands one frame may carry. */
  hz: number;
  /** Longest sub-step the server will simulate. */
  maxStepSeconds: number;
  /** Most commands one wake may produce, so a stalled tab cannot flood. */
  maxPerWake: number;
  /** Below this magnitude of push, nothing happened. Mirrors the simulation. */
  idleThreshold: number;
}

/** Where the player is pushing, right now. */
export interface FintechAxes {
  mx: number;
  my: number;
}

/**
 * Emits commands at a FIXED RATE, independent of the frame rate.
 *
 * **Merging is fatal once prediction exists**: the client would predict from the
 * individual commands while the server simulated the merged ones, so the two
 * would disagree by construction and the correction would never settle. So the
 * rate is fixed instead — the send rate times the commands one frame may carry,
 * forty a second against ten frames of four. A send window then holds exactly
 * the number of commands it is allowed to, on a 144 Hz tablet and a 30 Hz phone
 * alike, and nothing ever has to be dropped or combined.
 *
 * A COMMAND IS EMITTED ONLY WHEN SOMETHING HAPPENED — a push past the idle
 * threshold, or a dash. Standing perfectly still with the screen untouched emits
 * nothing, so no frame is sent, so a player who is doing the thing this game is
 * about costs the network nothing at all. The salary keeps climbing because the
 * SERVER advances the shift; a client that had to send ten frames a second in
 * order to be paid would be the exact defect «ВАНЯДУМ» shipped once.
 *
 * It takes its clock as an argument rather than reading one, so a test drives a
 * whole second without waiting for one.
 */
/**
 * Which way a dash goes when the thumb is not saying.
 *
 * THE WHOLE GAME IS PLAYED STANDING PERFECTLY STILL, so a neutral stick is not
 * an edge case here — it is the default state, and it is exactly the state you
 * dash out of. The first build sent the dash with the axes as they were, which
 * for a still player is `(0, 0)`: dash speed times no direction is no movement,
 * so the button started the dash, burned the whole cooldown and moved the player
 * nowhere. Pressing the dodge and not moving is the worst thing a control can
 * do, and it did it in the commonest case there is.
 *
 * So a dash always has a direction, in this order:
 *
 *  1. **The stick**, whenever it is being pushed — the player asked, and the
 *     player is never overruled.
 *  2. **The last way they went**, which is what "dodge" means after any movement
 *     at all and needs no explanation on a control with one stick.
 *  3. **Directly away from the лысый**, for the one dash a shift where neither
 *     exists — you have not moved yet, and the only thing worth dashing from is
 *     walking at you.
 *
 * It stays on the client on purpose: the command carries `mx`/`my` like any
 * other, so the server sees an ordinary dash in a direction and needs to know
 * nothing about where it came from — and prediction stays exact, because what is
 * predicted is what is sent.
 */
export function dashAxes(
  axes: FintechAxes,
  last: FintechAxes | null,
  awayFrom: { dx: number; dy: number } | null,
  idleThreshold: number,
): FintechAxes {
  if (Math.hypot(axes.mx, axes.my) > idleThreshold) return axes;
  if (last && Math.hypot(last.mx, last.my) > idleThreshold) return last;
  if (awayFrom) {
    const mag = Math.hypot(awayFrom.dx, awayFrom.dy);
    if (mag > 1e-6) return { mx: awayFrom.dx / mag, my: awayFrom.dy / mag };
  }
  // Nothing to go on at all — up the plane, away from where he starts.
  return { mx: 0, my: -1 };
}

export function createEmitter(opts: EmitterOptions) {
  const period = 1000 / opts.hz;
  let last: number | null = null;
  // Fractional leftovers are carried rather than discarded: dropping them makes
  // the client's simulated time drift slowly behind the server's, which shows up
  // as a correction that is always in the same direction.
  let owed = 0;

  return {
    /**
     * The commands due at this instant — usually none, sometimes one, more only
     * after a stall.
     *
     * `dash` is consumed by the FIRST command this call emits. The caller keeps
     * asking until one comes back carrying it, so a tap during a frame that
     * produced no command is not silently lost.
     *
     * `dashing` is whether a dash STARTED EARLIER IS STILL RUNNING, and it is a
     * different question from `dash`, which is a request that has not been
     * carried yet. Keeping the two apart is the whole of this method's
     * correctness, because a dash outlives its request by nine commands:
     *
     *   * `dash` true, `dashing` false — the tap. One command carries it.
     *   * `dash` false, `dashing` true — the other 0.235 s of the burst. The
     *     stick is neutral (that is what you dash out of), so nothing else here
     *     would produce a command, and NOT producing one is the bug this
     *     parameter exists to prevent: the client's simulation only advances
     *     inside `apply`, so it would freeze a fifth of the way through while
     *     the server ran the burst to the end. Measured, before the fix: the
     *     client predicted 0.500 m of a 5.500 m dash, the gap reached the 2 m
     *     snap threshold in a tenth of a second, and the figure sawed back and
     *     forth by up to 2.8 m before arriving where the server had put it.
     *   * both false, stick neutral — standing perfectly still, which is the
     *     point of the game and still emits nothing at all.
     *
     * The commands emitted for a running dash carry the stick as it is, which
     * for a still player is `(0, 0)` — and that is correct rather than a
     * compromise, because `step` ignores a command's axes for the duration of a
     * dash on both ends of the port. What they carry is TIME, which is the thing
     * the prediction was missing.
     */
    due(nowMs: number, axes: FintechAxes, dash: boolean, forDash: FintechAxes, dashing: boolean): StepCommand[] {
      if (last === null) {
        last = nowMs;
        return [];
      }
      owed += nowMs - last;
      last = nowMs;

      const out: StepCommand[] = [];
      // Bounded so a tab that was backgrounded for a minute does not emit a
      // minute of commands the moment it wakes. The server's own time budget
      // would refuse them anyway, and not creating them is what keeps the
      // client's prediction agreeing with that refusal.
      let budget = opts.maxPerWake;
      let dashLeft = dash;
      while (owed >= period && budget > 0) {
        owed -= period;
        budget -= 1;
        const pushing = Math.hypot(axes.mx, axes.my) > opts.idleThreshold;
        if (!pushing && !dashLeft && !dashing) continue;
        // The command that CARRIES the dash takes the resolved direction, which
        // for a still player is not the stick — see dashAxes. Every other
        // command takes the stick as it is, because a neutral stick means stand
        // still and that is the point of the game.
        const use = dashLeft ? forDash : axes;
        const cmd: StepCommand = {
          dt: Math.min(opts.maxStepSeconds, period / 1000),
          mx: use.mx,
          my: use.my,
        };
        if (dashLeft) {
          cmd.dash = true;
          dashLeft = false;
        }
        out.push(cmd);
      }
      // Whatever the wake budget could not cover is dropped rather than owed
      // forever, so a long stall costs one gap instead of a slow catch-up the
      // server would refuse anyway.
      if (owed > period * opts.maxPerWake) owed = 0;
      return out;
    },

    /**
     * Time that has elapsed but has not yet become a command, in seconds.
     *
     * THE RENDERER NEEDS THIS AND NOTHING ELSE DOES. Commands exist at a fixed
     * forty a second because merging them would break prediction, but the screen
     * refreshes at sixty, ninety or a hundred and twenty — so drawing the player
     * at the last command's endpoint holds him still for one to three frames and
     * then jumps him. At a walk that is 8 cm a step; **during a dash it is 22 cm,
     * nine times, which is what a dash entirely consists of**, and it reads as
     * the burst stuttering rather than firing.
     *
     * Handing the leftover to the renderer closes the gap without inventing
     * anything: it is time that has genuinely passed and that the very next
     * command will claim, so the figure drawn here is exactly where `apply` is
     * about to put him. It is a rendering detail and never a simulated one —
     * nothing derived from it is sent, stored, or folded into prediction.
     */
    residualSeconds(): number {
      return Math.min(owed, period) / 1000;
    },

    /** Drops everything, for when a shift ends. */
    reset(): void {
      last = null;
      owed = 0;
    },
  };
}

export type Emitter = ReturnType<typeof createEmitter>;

/**
 * How far from the stick's centre counts as nothing.
 *
 * A thumb resting on glass is never perfectly still, and in this game that is
 * not a cosmetic problem: drifting a hair while you believe you are standing
 * still is the difference between the multiplier climbing to ×3 and being
 * silently reset, with nothing on screen to explain it. Comfortably above the
 * simulation's own idle threshold for that reason — the stick refuses to report
 * a push the server would count as movement.
 */
export const STICK_DEADZONE = 0.16;

/**
 * Turns a drag on the stick into movement axes.
 *
 * Screen coordinates have +y downwards and so does the office (origin top-left),
 * so — unlike a first-person game — there is no flip: dragging down walks down
 * the screen, which is what a plan view means.
 *
 * Clamped rather than normalised past full push: a half-pushed stick stays half
 * speed, which the server also relies on.
 */
export function stickVector(
  origin: { x: number; y: number },
  point: { x: number; y: number },
  radius: number,
): FintechAxes {
  if (!(radius > 0)) return { mx: 0, my: 0 };
  const dx = (point.x - origin.x) / radius;
  const dy = (point.y - origin.y) / radius;
  const mag = Math.hypot(dx, dy);
  if (!Number.isFinite(mag) || mag < STICK_DEADZONE) return { mx: 0, my: 0 };
  const scale = mag > 1 ? 1 / mag : 1;
  return { mx: dx * scale, my: dy * scale };
}

/**
 * Movement axes from the keys currently held — WASD, or the arrows.
 *
 * DESKTOP PARITY COSTS ONE FUNCTION and buys two things worth more: the game is
 * playable and developable without a touch emulator, and a test can drive it with
 * ordinary key events instead of synthesising pointer gestures on a stick.
 *
 * SCREEN AXES, LIKE THE STICK'S. `W` is −1 on Y because the office's +Y points DOWN
 * and so does the screen's, which is the convention the whole client is built on —
 * there is no axis flip anywhere in it, and this is not the place to introduce one.
 *
 * Re-implemented here rather than imported from «ВАНЯДУМ», which has the same
 * function: no game shares code with another (ADR-028), and that game's version has
 * the opposite Y sign because a shooter's forward is away from the camera.
 */
export function axesFromKeys(held: ReadonlySet<string>): FintechAxes {
  const on = (...keys: string[]) => (keys.some((k) => held.has(k)) ? 1 : 0);
  const mx = on('KeyD', 'ArrowRight') - on('KeyA', 'ArrowLeft');
  const my = on('KeyS', 'ArrowDown') - on('KeyW', 'ArrowUp');
  if (mx === 0 || my === 0) return { mx, my };
  // Normalised, so holding two keys is not faster than holding one. `Sanitise` on the
  // server does this too and so does the port — all three agreeing is the point, and
  // a diagonal that outran a straight line is a cheat the office would refuse.
  const inv = 1 / Math.SQRT2;
  return { mx: mx * inv, my: my * inv };
}

/** The keys this game reads, so a handler can ignore everything else. */
export const MOVE_KEYS: readonly string[] = [
  'KeyW',
  'KeyA',
  'KeyS',
  'KeyD',
  'ArrowUp',
  'ArrowDown',
  'ArrowLeft',
  'ArrowRight',
];

/** The key that dashes. */
export const DASH_KEY = 'Space';

/** One command exactly as it goes on the wire — short keys, `d` omitted. */
export interface WireCommand {
  q?: number;
  dt: number;
  mx: number;
  my: number;
  d?: true;
}

/** One input frame, exactly as it goes on the wire. */
export interface InputFrame {
  t: 'fintech_input';
  /** The last snapshot tick this client drew — the server derives RTT from it. */
  k: number;
  cmds: WireCommand[];
}

/**
 * Builds an input frame: the commands just applied, preceded by the tail of
 * whatever is still unacknowledged.
 *
 * PURE, AND TESTED HERE RATHER THAN IN PLAYWRIGHT. Input is emitted from the
 * render loop, and a browser pauses `requestAnimationFrame` outright for a
 * backgrounded page — under several parallel workers only one page is ever
 * visible, which made the equivalent assertion in «ВАНЯДУМ» fail about one run
 * in three for reasons that had nothing to do with the payload.
 *
 * Redundant commands come FIRST and are de-duplicated against the fresh ones:
 * the server applies them in order and drops any sequence it has already seen.
 * `d` is omitted rather than sent false, because this frame repeats ten times a
 * second for as long as somebody is walking.
 */
export function buildInputFrame(
  seenTick: number,
  fresh: readonly StepCommand[],
  unacknowledged: readonly StepCommand[],
): InputFrame {
  const seen = new Set(fresh.map((c) => c.seq));
  const cmds = [...unacknowledged.filter((c) => !seen.has(c.seq)), ...fresh];
  return {
    t: 'fintech_input',
    k: seenTick,
    cmds: cmds.map((c) => {
      const wire: WireCommand = { q: c.seq, dt: c.dt, mx: c.mx, my: c.my };
      if (c.dash) wire.d = true;
      return wire;
    }),
  };
}
