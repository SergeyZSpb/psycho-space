import { describe, expect, it } from 'vitest';
import {
  CORRECTION_SECONDS,
  SILENT_CORRECTION,
  SNAP_CORRECTION,
  createPredictor,
} from '../lib/vanyadumPredict';
import type { VanyadumAxes } from '../lib/vanyadumInput';
import type { StepConstants } from '../lib/vanyadumStep';
import type { VanyadumLevel } from '../lib/vanyadumLevel';

/**
 * Client-side prediction and server reconciliation.
 *
 * These are the tests that say prediction is a *rendering* technique and not a
 * transfer of authority: every one of them ends with the client agreeing with
 * the server, and the interesting question is only how it got there.
 */

const K: StepConstants = {
  walkSpeed: 5,
  radius: 0.35,
  maxStep: 0.6,
  maxPitch: 1.5,
  maxStepSeconds: 0.2,
  collisionPasses: 3,
};

/** One big empty room, so nothing below is about collision. */
const ROOM: VanyadumLevel = {
  seed: 1,
  sectors: [
    { id: 0, x0: 0, y0: 0, x1: 100, y1: 100, fz: 0, cz: 3, w: 'c', f: 'f', c: 'c', l: 1 },
  ],
  portals: [],
  walls: [],
  pickups: [],
  spawn: { x: 50, y: 50 },
  spawn_sector: 0,
  spawn_yaw: 0,
};

function predictor() {
  return createPredictor({
    level: ROOM,
    constants: K,
    eyeHeight: 1.65,
    start: { x: 50, y: 50, sector: 0, yaw: 0 },
  });
}

const walk = { dt: 0.025, mx: 0, my: 1, yaw: 0, pitch: 0 };

/**
 * The axes a frame is drawn with.
 *
 * `view` takes them because a carry has to be stepped with the same axes the
 * command it anticipates will be built from. With nothing to carry they are
 * never read, so every call below that only asks "where is he being drawn now"
 * passes a carry of zero and `STILL`.
 */
const STILL: VanyadumAxes = { mx: 0, my: 0, yaw: 0, pitch: 0 };
const WALKING: VanyadumAxes = { mx: 0, my: 1, yaw: 0, pitch: 0 };

describe('prediction', () => {
  it('moves the instant a command is applied, with no server in sight', () => {
    // The whole point: iteration 1 waited a round trip and then twenty hertz.
    const p = predictor();
    const before = p.view(0, STILL).y;
    p.apply(walk);
    expect(p.view(0, STILL).y).toBeGreaterThan(before);
  });

  it('numbers commands from one, because zero means unset to the server', () => {
    const p = predictor();
    expect(p.apply(walk).seq).toBe(1);
    expect(p.apply(walk).seq).toBe(2);
  });

  it('keeps every command pending until it is acknowledged', () => {
    const p = predictor();
    for (let i = 0; i < 5; i++) p.apply(walk);
    expect(p.pendingCount()).toBe(5);
  });

  it('computes the eye height from the sector, not from a snapshot', () => {
    // Otherwise a camera that only rose twenty times a second would make every
    // step visibly jolt.
    expect(predictor().view(0, STILL).z).toBeCloseTo(1.65, 9);
  });
});

describe('reconciliation', () => {
  it('drops acknowledged commands and keeps the rest', () => {
    const p = predictor();
    for (let i = 0; i < 5; i++) p.apply(walk);
    p.reconcile({ x: 50, y: 50.25, sector: 0, ack: 3 });
    expect(p.pendingCount()).toBe(2);
  });

  it('replays what is still pending on top of the authoritative position', () => {
    // The half that is easy to omit. Without the replay the client snaps back
    // to where the server was a round trip ago and re-runs forward next frame,
    // which is the classic rubber-band.
    const p = predictor();
    for (let i = 0; i < 4; i++) p.apply(walk);
    // The server has only seen the first two, so it is two steps behind.
    p.reconcile({ x: 50, y: 50 + 2 * walk.dt * K.walkSpeed, sector: 0, ack: 2 });
    // Four steps of movement must still be visible, not two.
    expect(p.raw().y).toBeCloseTo(50 + 4 * walk.dt * K.walkSpeed, 6);
  });

  it('lands exactly where the server says once everything is acknowledged', () => {
    const p = predictor();
    for (let i = 0; i < 6; i++) p.apply(walk);
    p.reconcile({ x: 42, y: 17, sector: 0, ack: 6 });
    expect(p.raw().x).toBeCloseTo(42, 9);
    expect(p.raw().y).toBeCloseTo(17, 9);
  });

  it('applies a tiny disagreement silently, so the camera never shivers', () => {
    // Quantisation to the centimetre and floating-point drift mean the two ends
    // are never bit-identical. Without a floor, every snapshot would start a
    // correction and the view would tremble permanently.
    const p = predictor();
    p.apply(walk);
    const y = p.raw().y;
    p.reconcile({ x: 50, y: y + SILENT_CORRECTION / 2, sector: 0, ack: 1 });
    expect(p.correction()).toBe(0);
  });

  it('eases a visible disagreement out instead of jumping', () => {
    const p = predictor();
    p.apply(walk);
    const drawn = p.view(0, STILL);
    p.reconcile({ x: 50, y: p.raw().y + 0.5, sector: 0, ack: 1 });
    // Still drawn where it was, not teleported to the new truth.
    expect(p.view(0, STILL).y).toBeCloseTo(drawn.y, 6);
    expect(p.correction()).toBeGreaterThan(0);
  });

  it('and the correction actually goes away', () => {
    const p = predictor();
    p.apply(walk);
    p.reconcile({ x: 50, y: p.raw().y + 0.5, sector: 0, ack: 1 });
    // Exponential decay never reaches zero by arithmetic, so it is cut off at
    // a tenth of a millimetre. A second and a bit of ticks is comfortably past
    // that; the point of the assertion is that it ENDS, not how fast.
    for (let i = 0; i < 150; i++) p.tick(CORRECTION_SECONDS / 10);
    expect(p.correction()).toBe(0);
    expect(p.view(0, STILL).y).toBeCloseTo(p.raw().y, 9);
  });

  it('snaps a disagreement too large to glide', () => {
    // Beyond a couple of metres the client is not slightly wrong, it is
    // somewhere else — a refused move, a resync — and sliding a player across a
    // room to arrive at the truth is slower and stranger than putting them there.
    const p = predictor();
    p.apply(walk);
    p.reconcile({ x: 50, y: p.raw().y + SNAP_CORRECTION * 2, sector: 0, ack: 1 });
    expect(p.correction()).toBe(0);
  });

  it('never argues with the server, however wrong the server looks', () => {
    // The claim that matters: prediction is a rendering technique, not
    // authority. Whatever the client thought, the server's answer is where it
    // ends up.
    const p = predictor();
    for (let i = 0; i < 20; i++) p.apply(walk);
    p.reconcile({ x: 1, y: 1, sector: 0, ack: 20 });
    for (let i = 0; i < 60; i++) p.tick(0.016);
    expect(p.view(0, STILL).x).toBeCloseTo(1, 6);
    expect(p.view(0, STILL).y).toBeCloseTo(1, 6);
  });
});

describe('a predictor rebuilt in the middle of a run', () => {
  // The defect this pins was a permanent freeze, not a glitch. A rebuilt
  // predictor counts from one, the arena has long since accepted far higher, so
  // it drops command 1 as stale and then every command that follows it — with
  // the socket up, the snapshots arriving and the HUD alive, so nothing on
  // screen says why the player has stopped moving. A reload is enough to cause
  // it, and it lasts the rest of the run.

  it('counts on from the ack, so its first command is not dropped as stale', () => {
    const p = predictor();
    p.reconcile({ x: 50, y: 50, sector: 0, ack: 400 });
    expect(p.apply(walk).seq).toBeGreaterThan(400);
  });

  it('but never moves the count when the ack is one of its own', () => {
    // The floor is one-directional on purpose. Rewinding onto sequences that are
    // still pending would put two different commands under one number, and
    // skipping ahead on an ack it has already passed would be the client
    // inventing a number rather than agreeing with the server's.
    const p = predictor();
    p.apply(walk);
    p.apply(walk);
    p.apply(walk);
    // Below the count: three sent, one folded in so far.
    p.reconcile({ x: 50, y: p.raw().y, sector: 0, ack: 1 });
    expect(p.apply(walk).seq).toBe(4);
    // Level with it: everything sent has been folded in, and the next command is
    // still simply the next one.
    p.reconcile({ x: 50, y: p.raw().y, sector: 0, ack: 4 });
    expect(p.apply(walk).seq).toBe(5);
  });
});

describe('input redundancy', () => {
  it('offers the tail of what is unacknowledged', () => {
    const p = predictor();
    for (let i = 0; i < 10; i++) p.apply(walk);
    const tail = p.unacknowledged(3);
    expect(tail.map((c) => c.seq)).toEqual([8, 9, 10]);
  });

  it('offers nothing once everything has been acknowledged', () => {
    const p = predictor();
    for (let i = 0; i < 4; i++) p.apply(walk);
    p.reconcile({ x: 50, y: p.raw().y, sector: 0, ack: 4 });
    expect(p.unacknowledged(6)).toEqual([]);
  });

  it('offers nothing when asked for nothing', () => {
    const p = predictor();
    p.apply(walk);
    expect(p.unacknowledged(0)).toEqual([]);
  });
});

describe('the drawn eye advances every frame, not every command', () => {
  // Regression. `predicted` only moves inside apply(), which happens forty
  // times a second, while the screen refreshes at sixty to a hundred and
  // forty-four — so drawing the prediction alone held the camera still for one
  // to three frames and then jumped it a whole sub-step, 12.5 cm at a walk.
  // Turning was smooth throughout, because look() already ran every frame, and
  // that asymmetry is how the fault announces itself.
  /** Where one command of walking forward from the spawn lands. */
  const LANDING = 50 + K.walkSpeed * walk.dt;
  /** How far one sub-step of walking goes — what the largest carry is worth. */
  const SUB_STEP = K.walkSpeed * walk.dt;

  it('draws exactly the prediction when there is nothing to carry', () => {
    // The invariant that lets one method serve both jobs: with the stick pushed
    // and no leftover, the frame is the prediction untouched. There is no
    // second, carry-free method for the application to call and the tests to
    // leave uncovered — a resting frame is this same call with a zero.
    const p = predictor();
    p.apply(walk);
    const v = p.view(0, WALKING);
    expect(v.x).toBeCloseTo(p.raw().x, 9);
    expect(v.y).toBeCloseTo(p.raw().y, 9);
  });

  it('advances the drawn position monotonically over the carry', () => {
    const p = predictor();
    let last = p.view(0, STILL).y;
    for (const dt of [0.005, 0.01, 0.015, 0.02, 0.025]) {
      const y = p.view(dt, WALKING).y;
      expect(y).toBeGreaterThan(last);
      last = y;
    }
  });

  it('and never past where the next command puts him — it lands exactly there', () => {
    // The claim that makes this not extrapolation: the time has already passed,
    // so the real command starts from the untouched prediction and arrives at
    // the position that was already on screen. Exact BECAUSE the axes are the
    // same in both frames here, which is the condition the claim carries; the
    // test below is what happens when they are not.
    const p = predictor();
    const carried = p.view(walk.dt, WALKING);
    expect(carried.y).toBeCloseTo(LANDING, 9);
    p.apply(walk);
    expect(p.view(0, STILL).x).toBeCloseTo(carried.x, 9);
    expect(p.view(0, STILL).y).toBeCloseTo(carried.y, 9);
  });

  it('steps back by at most one sub-step when the stick is let go', () => {
    // The carry's one honest cost, and the point of pinning it is the BOUND
    // rather than the absence: a carry that was showing part of a command which
    // then never came has to be taken off the screen, and letting go is how
    // that happens constantly. Emptying the leftover after a long stall draws
    // the identical frame, so this covers both. What must never happen is the
    // back-step growing past the sub-step the carry could have been showing —
    // that would mean the carry had drawn something the next command was not
    // about to do.
    const p = predictor();
    for (const carry of [0.005, 0.015, walk.dt]) {
      const carried = p.view(carry, WALKING).y;
      const released = p.view(carry, STILL).y;
      expect(released).toBeLessThan(carried);
      expect(carried - released).toBeLessThanOrEqual(SUB_STEP + 1e-9);
      // And it steps back to the prediction itself rather than past it: where
      // the last command actually left him, which is the only place the next
      // one can start from.
      expect(released).toBeCloseTo(p.raw().y, 9);
    }
    expect(p.view(0, WALKING).y).toBeCloseTo(p.raw().y, 9);
  });

  it('draws against a copy, leaving the prediction untouched', () => {
    const p = predictor();
    const before = p.raw();
    p.view(0.02, WALKING);
    p.view(0.02, WALKING);
    expect(p.raw()).toEqual(before);
    expect(p.pendingCount()).toBe(0);
  });

  it('takes the aim from the axes, not from the prediction it is carrying', () => {
    // Both of these matter. The aim has to be carried at all because step()
    // writes the command's yaw and pitch straight onto the player, so a carry
    // built without them would face yaw zero and swing the horizon round to
    // north on every frame between two commands. And it has to come from the
    // AXES because those are what emitter.due puts into the real command: read
    // off the prediction it would agree only while look() happened to run first
    // in the same frame, which is agreement by call ordering rather than by
    // construction. So look() is left holding yesterday's aim here.
    const p = predictor();
    p.look(0, 0);
    const v = p.view(walk.dt, { mx: 0, my: 1, yaw: Math.PI / 2, pitch: -0.3 });
    expect(v.yaw).toBeCloseTo(Math.PI / 2, 9);
    expect(v.pitch).toBeCloseTo(-0.3, 9);
    // Yaw π/2 faces +X, so walking forward has to carry him along x and not y —
    // exactly where the command about to be emitted will put him.
    expect(v.x).toBeCloseTo(50 + SUB_STEP, 9);
    expect(v.y).toBeCloseTo(50, 9);
  });
});

describe('aim', () => {
  it('is the client’s own state — the server clamps it but never sets it', () => {
    // A view that snapped back to a snapshot's angle would fight the thumb
    // turning it, which is far worse than a position doing the same.
    const p = predictor();
    p.look(1.2, -0.3);
    p.reconcile({ x: 50, y: 50, sector: 0, ack: 0 });
    expect(p.view(0, STILL).yaw).toBeCloseTo(1.2, 9);
    expect(p.view(0, STILL).pitch).toBeCloseTo(-0.3, 9);
  });
});
