import { describe, expect, it } from 'vitest';
import {
  CORRECTION_SECONDS,
  SILENT_CORRECTION,
  SNAP_CORRECTION,
  STICK_DEADZONE,
  buildInputFrame,
  createEmitter,
  createPredictor,
  stickVector,
} from '../lib/karenPredict';
import type { StepConstants } from '../lib/karenStep';
import type { KarenRect } from '../api/types';

/**
 * Prediction, the commands that feed it, and the stick that produces those.
 *
 * These are the tests that say prediction is a *rendering* technique and not a
 * transfer of authority: every one of them ends with the client agreeing with
 * the server, and the interesting question is only how it got there.
 *
 * The input half is tested here rather than in Playwright on purpose. Commands
 * are emitted from the render loop, and a browser pauses `requestAnimationFrame`
 * outright for a backgrounded page — under parallel workers only one page is
 * ever visible, which made the equivalent Playwright assertion in «ВАНЯДУМ» fail
 * about one run in three for reasons unrelated to the payload.
 */

const K: StepConstants = {
  officeW: 16,
  officeH: 22,
  playerRadius: 0.35,
  walkSpeed: 4,
  dashSpeed: 10,
  dashSeconds: 0.2,
  dashCooldownSeconds: 4,
  idleThreshold: 0.05,
  maxStepSeconds: 0.2,
  basePerSecond: 100,
  rampSeconds: 5,
  maxMultiplier: 3,
  graceSeconds: 0.3,
};

/** An empty room, so nothing below is about collision. */
const NO_DESKS: readonly KarenRect[] = [];

function predictor() {
  return createPredictor({ desks: NO_DESKS, constants: K, start: { x: 8, y: 11 } });
}

const walk = { dt: 0.025, mx: 0, my: 1 };

describe('prediction', () => {
  it('moves the instant a command is applied, with no server in sight', () => {
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
});

describe('reconciliation', () => {
  it('drops acknowledged commands and keeps the rest', () => {
    const p = predictor();
    for (let i = 0; i < 5; i++) p.apply(walk);
    p.reconcile({ x: 8, y: 11.2, ack: 3 });
    expect(p.pendingCount()).toBe(2);
  });

  it('replays what is still pending on top of the authoritative position', () => {
    // The half that is easy to omit. Without the replay the client snaps back to
    // where the server was a round trip ago and re-runs forward next frame,
    // which is the classic rubber-band.
    const p = predictor();
    for (let i = 0; i < 4; i++) p.apply(walk);
    // The server has only seen the first two, so it is two steps behind.
    p.reconcile({ x: 8, y: 11 + 2 * walk.dt * K.walkSpeed, ack: 2 });
    expect(p.raw().y).toBeCloseTo(11 + 4 * walk.dt * K.walkSpeed, 6);
  });

  it('lands exactly where the server says once everything is acknowledged', () => {
    const p = predictor();
    for (let i = 0; i < 6; i++) p.apply(walk);
    p.reconcile({ x: 4.25, y: 17, ack: 6 });
    expect(p.raw().x).toBeCloseTo(4.25, 9);
    expect(p.raw().y).toBeCloseTo(17, 9);
  });

  it('applies a tiny disagreement silently, so the figure never shivers', () => {
    // Quantisation to the centimetre and floating-point drift mean the two ends
    // are never bit-identical. Without a floor, every snapshot would start a
    // correction and the office would tremble permanently.
    const p = predictor();
    p.apply(walk);
    const y = p.raw().y;
    p.reconcile({ x: 8, y: y + SILENT_CORRECTION / 2, ack: 1 });
    expect(p.correction()).toBe(0);
  });

  it('eases a visible disagreement out instead of jumping', () => {
    const p = predictor();
    p.apply(walk);
    const drawn = p.view();
    p.reconcile({ x: 8, y: p.raw().y + 0.5, ack: 1 });
    expect(p.view().y).toBeCloseTo(drawn.y, 6);
    expect(p.correction()).toBeGreaterThan(0);
  });

  it('and the correction actually goes away', () => {
    const p = predictor();
    p.apply(walk);
    p.reconcile({ x: 8, y: p.raw().y + 0.5, ack: 1 });
    // Exponential decay never reaches zero by arithmetic, so it is cut off at a
    // tenth of a millimetre. The point is that it ENDS, not how fast.
    for (let i = 0; i < 150; i++) p.tick(CORRECTION_SECONDS / 10);
    expect(p.correction()).toBe(0);
    expect(p.view().y).toBeCloseTo(p.raw().y, 9);
  });

  it('snaps a disagreement too large to glide', () => {
    const p = predictor();
    p.apply(walk);
    p.reconcile({ x: 8, y: p.raw().y + SNAP_CORRECTION * 2, ack: 1 });
    expect(p.correction()).toBe(0);
  });

  it('never argues with the server, however wrong the server looks', () => {
    const p = predictor();
    for (let i = 0; i < 20; i++) p.apply(walk);
    p.reconcile({ x: 1, y: 1, ack: 20 });
    for (let i = 0; i < 60; i++) p.tick(0.016);
    expect(p.view().x).toBeCloseTo(1, 6);
    expect(p.view().y).toBeCloseTo(1, 6);
  });
});

describe('input redundancy', () => {
  it('offers the tail of what is unacknowledged', () => {
    const p = predictor();
    for (let i = 0; i < 10; i++) p.apply(walk);
    expect(p.unacknowledged(3).map((c) => c.seq)).toEqual([8, 9, 10]);
  });

  it('offers nothing once everything has been acknowledged', () => {
    const p = predictor();
    for (let i = 0; i < 4; i++) p.apply(walk);
    p.reconcile({ x: 8, y: p.raw().y, ack: 4 });
    expect(p.unacknowledged(6)).toEqual([]);
  });

  it('offers nothing when asked for nothing', () => {
    const p = predictor();
    p.apply(walk);
    expect(p.unacknowledged(0)).toEqual([]);
  });
});

describe('the emitter', () => {
  const emitter = () =>
    createEmitter({ hz: 40, maxStepSeconds: 0.2, maxPerWake: 4, idleThreshold: K.idleThreshold });

  it('says nothing on its very first wake, because it has no interval yet', () => {
    expect(emitter().due(1000, { mx: 0, my: 1 }, false)).toEqual([]);
  });

  it('emits at a fixed rate rather than one per frame', () => {
    // The whole reason it exists: merging is fatal once prediction is on, so the
    // rate is fixed and a frame carries however many the window held. At 40 Hz a
    // 100 ms window is exactly four.
    const e = emitter();
    e.due(0, { mx: 0, my: 1 }, false);
    expect(e.due(100, { mx: 0, my: 1 }, false)).toHaveLength(4);
  });

  it('SENDS NOTHING WHILE SOMEBODY IS STANDING STILL', () => {
    // The rule this game is built on, and the defect «ВАНЯДУМ» shipped once:
    // standing perfectly still is the entire point here, and it must cost the
    // network nothing at all. The salary climbs because the SERVER advances the
    // shift, never because the client keeps talking.
    const e = emitter();
    e.due(0, { mx: 0, my: 0 }, false);
    expect(e.due(1000, { mx: 0, my: 0 }, false)).toEqual([]);
  });

  it('treats a push inside the dead zone as standing still', () => {
    const e = emitter();
    e.due(0, { mx: 0.01, my: -0.01 }, false);
    expect(e.due(200, { mx: 0.01, my: -0.01 }, false)).toEqual([]);
  });

  it('speaks up for a dash even when the feet are still', () => {
    const e = emitter();
    e.due(0, { mx: 0, my: 0 }, false);
    const out = e.due(100, { mx: 0, my: 0 }, true);
    expect(out).toHaveLength(1);
    expect(out[0].dash).toBe(true);
  });

  it('puts the dash on exactly one command, never on the whole window', () => {
    const e = emitter();
    e.due(0, { mx: 0, my: 1 }, false);
    const out = e.due(100, { mx: 0, my: 1 }, true);
    expect(out.filter((c) => c.dash)).toHaveLength(1);
    expect(out[0].dash).toBe(true);
  });

  it('never claims a longer sub-step than the server will simulate', () => {
    const e = createEmitter({
      hz: 2,
      maxStepSeconds: 0.2,
      maxPerWake: 4,
      idleThreshold: K.idleThreshold,
    });
    e.due(0, { mx: 0, my: 1 }, false);
    for (const c of e.due(2000, { mx: 0, my: 1 }, false)) expect(c.dt).toBeLessThanOrEqual(0.2);
  });

  it('does not empty a minute of stall into the office all at once', () => {
    // A tab backgrounded for a minute would otherwise wake up owing 2400
    // commands, every one of which the server's time budget would refuse — and
    // not creating them is what keeps the prediction agreeing with that refusal.
    const e = emitter();
    e.due(0, { mx: 0, my: 1 }, false);
    expect(e.due(60_000, { mx: 0, my: 1 }, false)).toHaveLength(4);
    // And the backlog is dropped rather than paid off over the next second.
    expect(e.due(60_025, { mx: 0, my: 1 }, false)).toHaveLength(1);
  });

  it('forgets everything on reset, so a new shift starts from zero', () => {
    const e = emitter();
    e.due(0, { mx: 0, my: 1 }, false);
    e.reset();
    expect(e.due(1000, { mx: 0, my: 1 }, false)).toEqual([]);
  });
});

describe('the input frame', () => {
  it('carries the redundant commands first, then the fresh ones', () => {
    const frame = buildInputFrame(
      812,
      [{ seq: 5, dt: 0.025, mx: 0, my: 1 }],
      [
        { seq: 3, dt: 0.025, mx: 0, my: 1 },
        { seq: 4, dt: 0.025, mx: 0, my: 1 },
      ],
    );
    expect(frame.t).toBe('karen_input');
    expect(frame.k).toBe(812);
    expect(frame.cmds.map((c) => c.q)).toEqual([3, 4, 5]);
  });

  it('does not send the same command twice in one frame', () => {
    const cmd = { seq: 7, dt: 0.025, mx: 1, my: 0 };
    const frame = buildInputFrame(1, [cmd], [cmd]);
    expect(frame.cmds).toHaveLength(1);
  });

  it('omits the dash flag rather than sending it false', () => {
    // This frame repeats ten times a second for as long as somebody is walking,
    // so a field that means "no" has no business on it.
    const frame = buildInputFrame(1, [{ seq: 1, dt: 0.025, mx: 0, my: 1 }], []);
    expect('d' in frame.cmds[0]).toBe(false);
    const dashing = buildInputFrame(1, [{ seq: 2, dt: 0.025, mx: 0, my: 1, dash: true }], []);
    expect(dashing.cmds[0].d).toBe(true);
  });

  it('claims nothing about where anybody is', () => {
    // Intent only: identity is the connection, and a position on an inbound
    // frame would be a position the client got to choose.
    const frame = buildInputFrame(1, [{ seq: 1, dt: 0.025, mx: 0, my: 1 }], []);
    expect(Object.keys(frame).sort()).toEqual(['cmds', 'k', 't']);
    expect(Object.keys(frame.cmds[0]).sort()).toEqual(['dt', 'mx', 'my', 'q']);
  });
});

describe('the stick', () => {
  const origin = { x: 100, y: 100 };

  it('ignores a thumb resting on glass', () => {
    // Drifting a hair while you believe you are standing still is the difference
    // between ×3 and a silent reset, with nothing on screen to explain it.
    const v = stickVector(origin, { x: 101, y: 101 }, 52);
    expect(v).toEqual({ mx: 0, my: 0 });
  });

  it('does not flip the vertical axis, because the office is a plan view', () => {
    // Screen +y is down and office +y is down, so dragging down walks down. A
    // flip here would be the one axis bug that looks like broken controls.
    const v = stickVector(origin, { x: 100, y: 152 }, 52);
    expect(v.my).toBeCloseTo(1, 9);
    expect(v.mx).toBeCloseTo(0, 9);
  });

  it('keeps a half-pushed stick at half speed', () => {
    const v = stickVector(origin, { x: 126, y: 100 }, 52);
    expect(v.mx).toBeCloseTo(0.5, 9);
  });

  it('clamps a push past the ring rather than running off', () => {
    const v = stickVector(origin, { x: 400, y: 100 }, 52);
    expect(Math.hypot(v.mx, v.my)).toBeCloseTo(1, 9);
  });

  it('refuses a degenerate ring instead of dividing by zero', () => {
    expect(stickVector(origin, { x: 400, y: 400 }, 0)).toEqual({ mx: 0, my: 0 });
  });

  it('has a dead zone wider than the simulation calls movement', () => {
    // Otherwise the stick could report a push the server counts as walking while
    // the player believes they are still — the streak would reset for no visible
    // reason at all.
    expect(STICK_DEADZONE).toBeGreaterThan(K.idleThreshold);
  });
});

describe('the drawn position advances every frame, not every command', () => {
  // Regression, and it is what a dash is made of. `predicted` only moves inside
  // apply(), which happens 40x a second, while the screen refreshes at 60-120 —
  // so drawing view() held the figure still for one to three frames and then
  // jumped him. At a walk that is 8 cm a step; at dash speed it is 22 cm, nine
  // times, which is the entire burst.
  const AXES = { mx: 0, my: -1 };

  const fresh = predictor;

  it('carries the player forward over time that has not become a command yet', () => {
    const p = fresh();
    const still = p.view();
    const drawn = p.viewAhead(0.02, AXES);
    expect(drawn.y).toBeLessThan(still.y);
    // Exactly a walk over that slice — not a guess, not a smoothing lag.
    expect(still.y - drawn.y).toBeCloseTo(K.walkSpeed * 0.02, 6);
  });

  it('does not drift: the real command lands where the carry had already drawn', () => {
    const p = fresh();
    const drawnAt = p.viewAhead(0.025, AXES);
    p.apply({ dt: 0.025, mx: AXES.mx, my: AXES.my });
    const after = p.view();
    expect(after.x).toBeCloseTo(drawnAt.x, 9);
    expect(after.y).toBeCloseTo(drawnAt.y, 9);
  });

  it('carries at DASH speed while a dash is running, which is the point', () => {
    const p = fresh();
    p.apply({ dt: 0.001, mx: AXES.mx, my: AXES.my, dash: true });
    const from = p.view();
    const drawn = p.viewAhead(0.02, AXES);
    expect(from.y - drawn.y).toBeCloseTo(K.dashSpeed * 0.02, 6);
  });

  it('has nothing to snap back from when the stick is released', () => {
    const p = fresh();
    const held = p.view();
    expect(p.viewAhead(0.02, { mx: 0, my: 0 })).toEqual(held);
  });

  it('leaves prediction untouched — it draws against a copy', () => {
    const p = fresh();
    const before = p.view();
    p.viewAhead(0.02, AXES);
    p.viewAhead(0.02, AXES);
    expect(p.view()).toEqual(before);
    expect(p.pendingCount()).toBe(0);
  });
});
