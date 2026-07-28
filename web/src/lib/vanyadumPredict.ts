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

    /** What to draw this frame. */
    view(): PredictedView {
      const shown: StepPlayer = { ...predicted, x: predicted.x + errX, y: predicted.y + errY };
      return {
        x: shown.x,
        y: shown.y,
        z: eyeZ(level, shown, eyeHeight),
        yaw: shown.yaw,
        pitch: shown.pitch,
        sector: shown.sector,
      };
    },

    /**
     * The commands the server has not acknowledged, newest last.
     *
     * Sent again in each frame up to `max`, which costs a few bytes and means
     * one lost packet costs no input at all — the server drops any sequence it
     * has already applied, so a duplicate is free. This is only possible
     * because the pending list has to exist for reconciliation anyway.
     */
    unacknowledged(max: number): StepCommand[] {
      return max <= 0 ? [] : pending.slice(-max);
    },

    /** For tests and for the run ending. */
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
