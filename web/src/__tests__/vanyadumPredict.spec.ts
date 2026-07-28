import { describe, expect, it } from 'vitest';
import {
  CORRECTION_SECONDS,
  SILENT_CORRECTION,
  SNAP_CORRECTION,
  createPredictor,
} from '../lib/vanyadumPredict';
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

describe('prediction', () => {
  it('moves the instant a command is applied, with no server in sight', () => {
    // The whole point: iteration 1 waited a round trip and then twenty hertz.
    const p = predictor();
    const before = p.view().y;
    p.apply(walk);
    expect(p.view().y).toBeGreaterThan(before);
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
    expect(predictor().view().z).toBeCloseTo(1.65, 9);
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
    const drawn = p.view();
    p.reconcile({ x: 50, y: p.raw().y + 0.5, sector: 0, ack: 1 });
    // Still drawn where it was, not teleported to the new truth.
    expect(p.view().y).toBeCloseTo(drawn.y, 6);
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
    expect(p.view().y).toBeCloseTo(p.raw().y, 9);
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
    expect(p.view().x).toBeCloseTo(1, 6);
    expect(p.view().y).toBeCloseTo(1, 6);
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

describe('aim', () => {
  it('is the client’s own state — the server clamps it but never sets it', () => {
    // A view that snapped back to a snapshot's angle would fight the thumb
    // turning it, which is far worse than a position doing the same.
    const p = predictor();
    p.look(1.2, -0.3);
    p.reconcile({ x: 50, y: 50, sector: 0, ack: 0 });
    expect(p.view().yaw).toBeCloseTo(1.2, 9);
    expect(p.view().pitch).toBeCloseTo(-0.3, 9);
  });
});
