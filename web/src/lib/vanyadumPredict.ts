/**
 * «ВАНЯДУМ» — client-side prediction and server reconciliation.
 *
 * THE PROBLEM. Without this the camera only moves when a snapshot lands, so the
 * world updates twenty times a second while the screen redraws sixty — which
 * looks exactly like twenty frames a second — and every input waits out a round
 * trip before it does anything. Both were measured on a phone and both were
 * bad.
 *
 * THE MECHANISM (Gambetta's rung two). Every frame the client builds a command,
 * gives it a sequence number, **applies it locally through the same `Step` the
 * server runs**, and keeps it pending. The server echoes the last sequence it
 * folded in. On each snapshot the client drops everything acknowledged, resets
 * to the server's authoritative position, and **replays what is still pending
 * on top of it**. Movement then responds in zero milliseconds and updates at
 * frame rate, and the server still decides everything.
 *
 * WHAT THIS IS NOT. It is not authority. A prediction is a guess that is usually
 * right; when the server disagrees, the server wins, without negotiation and
 * without the client getting a say. See ADR-052.
 */

import type { VanyadumAxes } from './vanyadumInput';
import type { VanyadumLevel } from './vanyadumLevel';
import { eyeZ, step, type StepCommand, type StepConstants, type StepPlayer } from './vanyadumStep';

/** Where the player is authoritatively, as a snapshot reports it. */
export interface Authoritative {
  /** Metres — the client converts from the wire's centimetres. */
  x: number;
  y: number;
  sector: number;
  /** The last command sequence the server folded in. */
  ack: number;
}

/** What the renderer draws. */
export interface PredictedView {
  x: number;
  y: number;
  z: number;
  yaw: number;
  pitch: number;
  sector: number;
}

/**
 * A disagreement smaller than this is applied silently.
 *
 * Floating-point drift and the wire's centimetre quantisation mean the server
 * and the client are *never* bit-identical, so without a floor every single
 * snapshot would start a correction and the camera would shiver permanently.
 * One centimetre is below what a player can see and above what quantisation can
 * produce.
 */
export const SILENT_CORRECTION = 0.01;

/**
 * A disagreement larger than this is snapped rather than eased.
 *
 * Beyond a couple of metres the client is not slightly wrong, it is somewhere
 * else — a refused move, a teleport, a resync after a long stall — and gliding
 * a player across a room to arrive at the truth is both slower and stranger
 * than putting them there.
 */
export const SNAP_CORRECTION = 2;

/**
 * How long a correction takes to disappear.
 *
 * The whole trick of smoothing: the *rendered* position keeps its old offset
 * and that offset decays, so the camera never jumps and the player still ends
 * up exactly where the server says. Too fast and it reads as a twitch, too slow
 * and the client is visibly playing a different game from the server.
 */
export const CORRECTION_SECONDS = 0.12;

export interface PredictorOptions {
  level: VanyadumLevel;
  constants: StepConstants;
  eyeHeight: number;
  /** Starting position, from the level's spawn. */
  start: { x: number; y: number; sector: number; yaw: number };
}

/**
 * Builds the predictor.
 *
 * It owns the pending list, the predicted state and the correction offset, and
 * takes its clock as an argument everywhere — so a test drives a whole second
 * of prediction, reconciliation and smoothing without waiting for one.
 */
export function createPredictor(opts: PredictorOptions) {
  const { level, constants, eyeHeight } = opts;

  let seq = 0;
  let pending: StepCommand[] = [];
  let predicted: StepPlayer = {
    x: opts.start.x,
    y: opts.start.y,
    yaw: opts.start.yaw,
    pitch: 0,
    sector: opts.start.sector,
  };
  // The offset being eased out, in metres. Added to the predicted position when
  // rendering and decayed towards zero every frame.
  let errX = 0;
  let errY = 0;

  /**
   * Turns a simulated player into what the renderer draws: the correction
   * offset applied, then the eye placed on whatever sector that lands in.
   *
   * Both branches of `view` end here — the resting frame and the carried one —
   * so the two cannot come to disagree about how the correction is applied or
   * where the eye sits, which is what they would do as two functions kept in
   * step by hand.
   */
  function drawnView(p: StepPlayer): PredictedView {
    const shown: StepPlayer = { ...p, x: p.x + errX, y: p.y + errY };
    return {
      x: shown.x,
      y: shown.y,
      z: eyeZ(level, shown, eyeHeight),
      yaw: shown.yaw,
      pitch: shown.pitch,
      sector: shown.sector,
    };
  }

  return {
    /**
     * Applies one command locally and returns it, stamped with its sequence.
     *
     * The caller sends what comes back. Nothing is predicted that is not also
     * sent, and nothing is sent that is not also predicted — the two lists
     * being the same list is what makes reconciliation exact.
     */
    apply(cmd: Omit<StepCommand, 'seq'>): StepCommand {
      seq += 1;
      const stamped: StepCommand = { ...cmd, seq };
      predicted = step(level, predicted, stamped, constants);
      pending.push(stamped);
      return stamped;
    },

    /**
     * Folds in an authoritative position: drop what it acknowledges, reset to
     * it, replay the rest.
     *
     * The replay is the part that is easy to get wrong by omitting: without it
     * the client would snap back to where the server was *a round trip ago* and
     * then re-run forwards from there on the next frame, which is the classic
     * rubber-band.
     */
    reconcile(a: Authoritative): void {
      // A PREDICTOR REBUILT UNDER A LIVING OCCUPANT ADOPTS THE SERVER'S COUNT,
      // or it never moves again. `createPredictor` starts at zero, so its first
      // command is sequence 1 — and the server drops everything at or below the
      // highest sequence THAT OCCUPANT has already sent, which after a few
      // seconds of walking is in the hundreds. So it drops 1 as stale, then 2,
      // then every command after it, silently and for ever.
      //
      // This is not a corner case, because the occupant outlives the client's
      // predictor by design. A reload inside the abandon grace comes back to the
      // player it left standing there; so does a backgrounded tab whose socket
      // dropped; and so does the заброшка being regenerated, which rebuilds the
      // predictor against new geometry WITHOUT the socket or the occupant ever
      // going away — the one that needs no interruption at all.
      //
      // The ack is the right floor because THE SERVER IS THE ONE THAT SAID IT.
      // It is the last sequence the server folded in, so counting on from it is
      // the client agreeing with what it has just been told, not asserting a
      // number of its own — the same relationship the rest of this file has
      // with authority. And it only ever moves the counter FORWARDS: an ack at
      // or below `seq` is acknowledging commands this predictor itself sent, and
      // rewinding onto sequences that are still pending would put two different
      // commands under one number.
      //
      // It is a floor, not an exact resync. The server deduplicates against the
      // highest sequence it has ACCEPTED into that occupant's queue, which sits
      // at or above the last one it has STEPPED — the ack — by however much is
      // still waiting there. So a handful of the first resumed commands can land
      // below that mark and be dropped before the ack catches up. That is a
      // fraction of a second of unresponsiveness, against a freeze a reload
      // could not clear — the occupant, and the count it is stuck behind,
      // survive one.
      if (a.ack > seq) seq = a.ack;

      pending = pending.filter((c) => (c.seq ?? 0) > a.ack);

      // Where the player is being DRAWN right now — predicted plus whatever
      // correction is still easing out. Kept so the new offset can be measured
      // against it rather than against the raw prediction, which is what stops
      // a second correction arriving mid-glide from jumping.
      const drawnX = predicted.x + errX;
      const drawnY = predicted.y + errY;

      let replayed: StepPlayer = {
        x: a.x,
        y: a.y,
        yaw: predicted.yaw,
        pitch: predicted.pitch,
        sector: a.sector,
      };
      for (const c of pending) replayed = step(level, replayed, c, constants);
      predicted = replayed;

      const dx = drawnX - predicted.x;
      const dy = drawnY - predicted.y;
      const off = Math.hypot(dx, dy);
      if (off < SILENT_CORRECTION || off > SNAP_CORRECTION) {
        // Too small to see, or too big to glide. Both end with the camera
        // exactly where the server says, which is the only outcome that matters.
        errX = 0;
        errY = 0;
      } else {
        errX = dx;
        errY = dy;
      }
    },

    /** Decays the correction. Called once per rendered frame with its own dt. */
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

    /** Aim is the client's own state — the server clamps it but never sets it. */
    look(yaw: number, pitch: number): void {
      predicted.yaw = yaw;
      predicted.pitch = pitch;
    },

    /**
     * What to draw this frame: the prediction, carried forward over the time
     * that has already elapsed but has not yet become a command.
     *
     * This is the difference between an eye that moves and an eye that
     * flickers. `predicted` only advances inside `apply`, which happens forty
     * times a second, while this is called on every animation frame — so
     * without the carry the camera is redrawn in the same place for one to
     * three frames and then teleported 12.5 cm, forty times a second, for ever.
     * Only walking suffers, because `look` already runs every frame and turning
     * was therefore always smooth: that asymmetry is the fault's own signature.
     *
     * THERE IS ONE METHOD AND `carrySeconds` IS REQUIRED — not a carrying
     * method beside a resting one, and not an optional argument that leads back
     * to the resting shape. That buys exactly two things, and it is worth being
     * precise about which. First, there is a single drawing path through the
     * predictor, so there is no carry-free second path for the specs to leave
     * uncovered: a resting frame is this same call with a zero, so every spec in
     * `vanyadumPredict.spec.ts` drives the method the game draws with. Second, a
     * required argument makes removing the carry a DELIBERATE edit — the
     * compiler refuses a bare `view()` — rather than a line that can quietly go
     * missing.
     *
     * WHAT IT DOES NOT BUY, AND CANNOT. The ARGUMENT the view passes is
     * unpinned: change `emitter.residualSeconds()` to `0` at the call site and
     * the types are still satisfied, every suite is still green, and the judder
     * is back. There is nowhere to pin it: the eye lives inside the canvas, and
     * ADR-047 accepts that a canvas is opaque to both Playwright suites — what
     * it could offer is a pixel comparison or a test-only introspection API, and
     * this repository takes neither. So only a person looking at the game can
     * see that line go wrong. The gap is written down rather than papered over,
     * because a reviewer who believes the specs cover it will not look at it.
     *
     * THE CARRY COMMAND IS THE AXES PLUS THE ELAPSED TIME — the same two
     * ingredients `emitter.due` puts into the real command, so the carried
     * frame and the command it anticipates agree by construction rather than by
     * call ordering. Taking the aim off `predicted` instead would agree only
     * while `look` happened to have run earlier in the same frame, and would
     * quietly draw a stale horizon against a fresh command the day it did not.
     * The aim has to be carried at all because THIS GAME'S COMMAND IS ABSOLUTE
     * about where the player is looking rather than relative — `step` writes
     * `c.yaw` and `c.pitch` straight onto the player — so a carry command built
     * without them would step the copy facing yaw zero, and every frame between
     * two commands would swing the horizon round to north and back again.
     * «СИМУЛЯТОР ФИНТЕХА»'s command carries no aim at all, so the hazard is
     * this game's alone and the two carries are not the same shape.
     *
     * **It is not extrapolation and it cannot drift.** The step is over time
     * that has already passed, and it runs against a COPY — `predicted` is left
     * exactly as the real command will find it.
     *
     * WHERE IT IS EXACT, AND WHERE IT IS NOT. While the axes are unchanged
     * between the carried frame and the frame that emits, the real command
     * starts from the untouched `predicted` and lands precisely where this had
     * already drawn: nothing moves twice and nothing moves back. When the thumb
     * HAS moved, the command goes somewhere the carry was not pointing and the
     * drawn eye takes one small step across to it — sub-step scale, the same
     * size of disagreement the correction smoothing already glides out on every
     * snapshot. Releasing the stick is the case that happens constantly, and
     * there it steps BACK by whatever fraction of a sub-step the carry was
     * showing: at a walk, at most 12.5 cm on one frame, which the specs pin.
     * That is honest rather than wrong — the carry was drawing a command that
     * then never came.
     */
    view(carrySeconds: number, axes: VanyadumAxes): PredictedView {
      if (!(carrySeconds > 0)) return drawnView(predicted);
      return drawnView(step(level, predicted, { dt: carrySeconds, ...axes }, constants));
    },

    /**
     * The commands the server has not acknowledged, newest last.
     *
     * Sent again in each frame up to `max`, which costs a few bytes and means
     * one lost packet costs no input at all — the server drops any sequence it
     * has already ACCEPTED, queued as well as stepped, so a duplicate is free.
     * Filtering on what it had stepped would have re-admitted everything still
     * waiting in the queue and run it twice. This is only possible
     * because the pending list has to exist for reconciliation anyway.
     */
    unacknowledged(max: number): StepCommand[] {
      return max <= 0 ? [] : pending.slice(-max);
    },

    /** How much is still unacknowledged. For tests. */
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
