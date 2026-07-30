import { describe, expect, it } from 'vitest';
import {
  CORRECTION_SECONDS,
  SILENT_CORRECTION,
  SNAP_CORRECTION,
  STICK_DEADZONE,
  buildInputFrame,
  createEmitter,
  dashAxes,
  createPredictor,
  stickVector,
} from '../lib/fintechPredict';
import type { FintechAxes } from '../lib/fintechPredict';
import type { StepCommand, StepConstants } from '../lib/fintechStep';
import type { FintechRect } from '../api/types';

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
  slowFactor: 0.8,
};

/** An empty room, so nothing below is about collision. */
const NO_DESKS: readonly FintechRect[] = [];

/**
 * The direction a dash would resolve to if one were asked for.
 *
 * `dashAxes` has its own tests; everywhere else this is just "some direction",
 * and it is only read by the one command that carries a dash request.
 */
const FOR_DASH = { mx: 0, my: -1 };

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
    p.reconcile({ x: 8, y: 11.2, ack: 3, dashCooldown: 0, slowLeft: 0 });
    expect(p.pendingCount()).toBe(2);
  });

  it('replays what is still pending on top of the authoritative position', () => {
    // The half that is easy to omit. Without the replay the client snaps back to
    // where the server was a round trip ago and re-runs forward next frame,
    // which is the classic rubber-band.
    const p = predictor();
    for (let i = 0; i < 4; i++) p.apply(walk);
    // The server has only seen the first two, so it is two steps behind.
    p.reconcile({ x: 8, y: 11 + 2 * walk.dt * K.walkSpeed, ack: 2, dashCooldown: 0, slowLeft: 0 });
    expect(p.raw().y).toBeCloseTo(11 + 4 * walk.dt * K.walkSpeed, 6);
  });

  it('lands exactly where the server says once everything is acknowledged', () => {
    const p = predictor();
    for (let i = 0; i < 6; i++) p.apply(walk);
    p.reconcile({ x: 4.25, y: 17, ack: 6, dashCooldown: 0, slowLeft: 0 });
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
    p.reconcile({ x: 8, y: y + SILENT_CORRECTION / 2, ack: 1, dashCooldown: 0, slowLeft: 0 });
    expect(p.correction()).toBe(0);
  });

  it('eases a visible disagreement out instead of jumping', () => {
    const p = predictor();
    p.apply(walk);
    const drawn = p.view();
    p.reconcile({ x: 8, y: p.raw().y + 0.5, ack: 1, dashCooldown: 0, slowLeft: 0 });
    expect(p.view().y).toBeCloseTo(drawn.y, 6);
    expect(p.correction()).toBeGreaterThan(0);
  });

  it('and the correction actually goes away', () => {
    const p = predictor();
    p.apply(walk);
    p.reconcile({ x: 8, y: p.raw().y + 0.5, ack: 1, dashCooldown: 0, slowLeft: 0 });
    // Exponential decay never reaches zero by arithmetic, so it is cut off at a
    // tenth of a millimetre. The point is that it ENDS, not how fast.
    for (let i = 0; i < 150; i++) p.tick(CORRECTION_SECONDS / 10);
    expect(p.correction()).toBe(0);
    expect(p.view().y).toBeCloseTo(p.raw().y, 9);
  });

  it('snaps a disagreement too large to glide', () => {
    const p = predictor();
    p.apply(walk);
    p.reconcile({ x: 8, y: p.raw().y + SNAP_CORRECTION * 2, ack: 1, dashCooldown: 0, slowLeft: 0 });
    expect(p.correction()).toBe(0);
  });

  it('never argues with the server, however wrong the server looks', () => {
    const p = predictor();
    for (let i = 0; i < 20; i++) p.apply(walk);
    p.reconcile({ x: 1, y: 1, ack: 20, dashCooldown: 0, slowLeft: 0 });
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
    p.reconcile({ x: 8, y: p.raw().y, ack: 4, dashCooldown: 0, slowLeft: 0 });
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
    expect(emitter().due(1000, { mx: 0, my: 1 }, false, FOR_DASH, false)).toEqual([]);
  });

  it('emits at a fixed rate rather than one per frame', () => {
    // The whole reason it exists: merging is fatal once prediction is on, so the
    // rate is fixed and a frame carries however many the window held. At 40 Hz a
    // 100 ms window is exactly four.
    const e = emitter();
    e.due(0, { mx: 0, my: 1 }, false, FOR_DASH, false);
    expect(e.due(100, { mx: 0, my: 1 }, false, FOR_DASH, false)).toHaveLength(4);
  });

  it('SENDS NOTHING WHILE SOMEBODY IS STANDING STILL', () => {
    // The rule this game is built on, and the defect «ВАНЯДУМ» shipped once:
    // standing perfectly still is the entire point here, and it must cost the
    // network nothing at all. The salary climbs because the SERVER advances the
    // shift, never because the client keeps talking.
    const e = emitter();
    e.due(0, { mx: 0, my: 0 }, false, FOR_DASH, false);
    expect(e.due(1000, { mx: 0, my: 0 }, false, FOR_DASH, false)).toEqual([]);
  });

  it('treats a push inside the dead zone as standing still', () => {
    const e = emitter();
    e.due(0, { mx: 0.01, my: -0.01 }, false, FOR_DASH, false);
    expect(e.due(200, { mx: 0.01, my: -0.01 }, false, FOR_DASH, false)).toEqual([]);
  });

  it('speaks up for a dash even when the feet are still', () => {
    const e = emitter();
    e.due(0, { mx: 0, my: 0 }, false, FOR_DASH, false);
    const out = e.due(100, { mx: 0, my: 0 }, true, FOR_DASH, false);
    expect(out).toHaveLength(1);
    expect(out[0].dash).toBe(true);
  });

  it('puts the dash on exactly one command, never on the whole window', () => {
    const e = emitter();
    e.due(0, { mx: 0, my: 1 }, false, FOR_DASH, false);
    const out = e.due(100, { mx: 0, my: 1 }, true, FOR_DASH, false);
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
    e.due(0, { mx: 0, my: 1 }, false, FOR_DASH, false);
    for (const c of e.due(2000, { mx: 0, my: 1 }, false, FOR_DASH, false)) expect(c.dt).toBeLessThanOrEqual(0.2);
  });

  it('does not empty a minute of stall into the office all at once', () => {
    // A tab backgrounded for a minute would otherwise wake up owing 2400
    // commands, every one of which the server's time budget would refuse — and
    // not creating them is what keeps the prediction agreeing with that refusal.
    const e = emitter();
    e.due(0, { mx: 0, my: 1 }, false, FOR_DASH, false);
    expect(e.due(60_000, { mx: 0, my: 1 }, false, FOR_DASH, false)).toHaveLength(4);
    // And the backlog is dropped rather than paid off over the next second.
    expect(e.due(60_025, { mx: 0, my: 1 }, false, FOR_DASH, false)).toHaveLength(1);
  });

  it('forgets everything on reset, so a new shift starts from zero', () => {
    const e = emitter();
    e.due(0, { mx: 0, my: 1 }, false, FOR_DASH, false);
    e.reset();
    expect(e.due(1000, { mx: 0, my: 1 }, false, FOR_DASH, false)).toEqual([]);
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
    expect(frame.t).toBe('fintech_input');
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

describe('a dash always has a direction', () => {
  // THE WORST BUG THIS GAME HAS HAD. The whole game is played standing
  // perfectly still, so a neutral stick is the DEFAULT state and it is exactly
  // the state you dash out of. Sending the axes as they were meant sending
  // (0, 0): the dash started, burned its entire cooldown, and moved the player
  // nowhere. Measured at 0.000 m.
  const IDLE = 0.05;
  const still = { mx: 0, my: 0 };

  it('obeys the stick whenever the stick is saying anything', () => {
    const pushed = { mx: 0.6, my: -0.8 };
    expect(dashAxes(pushed, { mx: 1, my: 0 }, { dx: -1, dy: 0 }, IDLE)).toEqual(pushed);
  });

  it('falls back to the last way the player actually went', () => {
    expect(dashAxes(still, { mx: -1, my: 0 }, { dx: 0, dy: 5 }, IDLE)).toEqual({ mx: -1, my: 0 });
  });

  it('dashes away from the лысый when there is nothing else to go on', () => {
    // He is below-left, so away is up-and-right, normalised.
    const got = dashAxes(still, null, { dx: 3, dy: -4 }, IDLE);
    expect(got.mx).toBeCloseTo(0.6, 6);
    expect(got.my).toBeCloseTo(-0.8, 6);
    expect(Math.hypot(got.mx, got.my)).toBeCloseTo(1, 6);
  });

  it('never returns nothing, which is the whole point', () => {
    for (const last of [null, still]) {
      for (const away of [null, { dx: 0, dy: 0 }]) {
        const got = dashAxes(still, last, away, IDLE);
        expect(Math.hypot(got.mx, got.my)).toBeGreaterThan(IDLE);
      }
    }
  });

  it('a still player who taps РЫВОК actually moves', () => {
    const p = predictor();
    const from = p.view();
    const dir = dashAxes(still, { mx: 0, my: 1 }, null, IDLE);
    p.apply({ dt: 0.025, mx: dir.mx, my: dir.my, dash: true });
    expect(Math.hypot(p.view().x - from.x, p.view().y - from.y)).toBeCloseTo(K.dashSpeed * 0.025, 6);
  });
});

describe('a dash is predicted for the whole of its length', () => {
  /**
   * Regression, and it is the worst defect this game has had.
   *
   * The client's simulation only advances inside `apply`, and `apply` only runs
   * on a command the emitter produced. The emitter used to fall silent as soon
   * as the dash REQUEST had been carried — but a dash tapped from a standstill
   * leaves the stick neutral for the other 0.235 s of the burst, so exactly one
   * command was ever emitted for it. The server, meanwhile, simulates any part
   * of a tick nobody claimed as standing still, and a still step during a
   * committed dash travels at the full dash speed. So the server ran the whole
   * burst while the browser predicted a twentieth of it.
   *
   * Measured on production, at 20 m/s and a 2 m snap threshold: the client
   * predicted 0.500 m of 5.500 m, the disagreement cleared the snap threshold in
   * 0.1 s, and the figure sawed back and forth — teleports of 4.566 m, 3.197 m
   * and 2.340 m in a single dash — before arriving where the server had put it.
   * Three separate things go wrong and all three are pinned below: the position
   * freezes, `dashLeft` never reaches zero, and `dashCooldown` never reaches
   * zero either, so the NEXT dash is refused locally while the server grants it.
   */
  const FRAME_MS = 1000 / 60;
  const STILL: FintechAxes = { mx: 0, my: 0 };

  /**
   * The loop the view actually runs, with nothing stubbed out: an emitter at the
   * fixed command rate, the real predictor, and a 60 Hz frame that emits,
   * applies, decays the correction and reads the drawn position — `drawFrame`
   * and `placeMe`, in order.
   */
  function loop(opts: { frames: number; dashAt: number; axes?: (frame: number) => FintechAxes }) {
    const p = predictor();
    const e = createEmitter({
      hz: 40,
      maxStepSeconds: 0.2,
      maxPerWake: 4,
      idleThreshold: K.idleThreshold,
    });
    const axesFor = opts.axes ?? (() => STILL);
    let dashPending = false;
    const commands: StepCommand[] = [];
    const drawn: { x: number; y: number }[] = [];
    for (let f = 0; f < opts.frames; f++) {
      if (f === opts.dashAt) dashPending = true;
      const axes = axesFor(f);
      const due = e.due(f * FRAME_MS, axes, dashPending, FOR_DASH, p.dashing());
      for (const c of due) commands.push(p.apply(c));
      if (due.some((c) => c.dash)) dashPending = false;
      p.tick(FRAME_MS / 1000);
      drawn.push(p.viewAhead(e.residualSeconds(), axes));
    }
    return { p, commands, drawn };
  }

  /** How many sub-steps a dash is, at the fixed command rate. */
  const DASH_COMMANDS = K.dashSeconds / 0.025;

  it('keeps emitting for the rest of the burst, though the stick is neutral', () => {
    const { commands, p } = loop({ frames: 40, dashAt: 4 });
    // One command carries the request; the rest carry the TIME the prediction
    // would otherwise never simulate. BEFORE THE FIX THIS WAS EXACTLY 1.
    //
    // Two sub-steps of slack, and both are honest rather than sloppy. `step`
    // decides whether a sub-step is a dash BEFORE decrementing the timer, so a
    // dash that does not tile the command rate runs one partial sub-step at full
    // speed — and floating-point residue in the repeated subtraction can leave
    // that partial step a hair above zero, which the server does identically.
    // The caller also samples "is a dash running" once per drawn frame while a
    // frame may produce two commands, so the last still-step can be one late.
    expect(commands.length).toBeGreaterThanOrEqual(DASH_COMMANDS);
    expect(commands.length).toBeLessThanOrEqual(DASH_COMMANDS + 2);
    expect(commands.filter((c) => c.dash)).toHaveLength(1);
    expect(p.raw().dashLeft).toBe(0);
  });

  it('predicts the whole distance, not a twentieth of it', () => {
    const { p } = loop({ frames: 40, dashAt: 4 });
    // Straight up, from the spawn this suite uses, with no walls in reach.
    const travelled = 11 - p.raw().y;
    // The nominal burst, plus at most the one partial sub-step described above.
    // Before the fix this was a single sub-step: 0.25 m of 2.00 m.
    expect(travelled).toBeGreaterThanOrEqual(K.dashSpeed * K.dashSeconds);
    expect(travelled).toBeLessThanOrEqual(K.dashSpeed * (K.dashSeconds + 0.025));
  });

  it('draws it as one burst — the figure never goes backwards', () => {
    // THE SYMPTOM, and it is what the owner saw. With `predicted` frozen the
    // only thing moving the figure was the renderer's carry over elapsed-but-
    // uncommitted time, which sweeps a whole sub-step at dash speed and then
    // resets to nothing forty times a second.
    const { drawn } = loop({ frames: 40, dashAt: 4 });
    let backwards = 0;
    let worst = 0;
    for (let i = 1; i < drawn.length; i++) {
      const step = drawn[i].y - drawn[i - 1].y;
      if (step > 1e-9) {
        backwards += 1;
        worst = Math.max(worst, step);
      }
    }
    expect({ backwards, worst }).toEqual({ backwards: 0, worst: 0 });
  });

  it('and standing still afterwards draws the same place every frame', () => {
    // `dashLeft` used to be left permanently non-zero, so the carry kept running
    // at dash speed for the rest of the shift and the figure shivered by up to
    // half a metre while the player was doing nothing at all.
    const { p, drawn } = loop({ frames: 60, dashAt: 4 });
    const settled = drawn.slice(-20);
    for (const at of settled) expect(at.y).toBeCloseTo(p.raw().y, 9);
  });

  it('and says nothing at all once it is over, which is the whole design', () => {
    // The fix must not become "always talk". Standing perfectly still is what
    // this game is about and it still costs the network nothing.
    const before = loop({ frames: 40, dashAt: 4 }).commands.length;
    const after = loop({ frames: 200, dashAt: 4 }).commands.length;
    expect(after).toBe(before);
  });
});

describe('the dash cooldown comes from the server, because a still client cannot count it', () => {
  it('folds the authoritative cooldown in beside the position', () => {
    const p = predictor();
    p.apply({ dt: 0.025, mx: 0, my: -1, dash: true });
    expect(p.raw().dashCooldown).toBeCloseTo(K.dashCooldownSeconds - 0.025, 9);
    // Some seconds later the server says it is ready. The client emitted nothing
    // in between — it was standing still, which is the point of the game — so
    // this snapshot is the only way it can ever find out.
    p.reconcile({ x: p.raw().x, y: p.raw().y, ack: 1, dashCooldown: 0, slowLeft: 0 });
    expect(p.raw().dashCooldown).toBe(0);
  });

  it('so the second dash of a shift is granted locally, not refused', () => {
    // Before the fix the client's own cooldown stayed frozen at 2.175 s for the
    // rest of the shift, so `step` refused every later dash while the server
    // granted it — the client predicted a walk against a 20 m/s burst.
    const p = predictor();
    p.apply({ dt: 0.025, mx: 0, my: -1, dash: true });
    // Run the burst out, the way the emitter now does.
    while (p.raw().dashLeft > 0) p.apply({ dt: 0.025, mx: 0, my: 0 });
    // Seconds pass. The client is standing perfectly still, so it sends nothing
    // and its own cooldown stays frozen wherever the burst left it.
    expect(p.raw().dashCooldown).toBeGreaterThan(0);

    p.reconcile({ x: p.raw().x, y: p.raw().y, ack: 99, dashCooldown: 0, slowLeft: 0 });
    const before = p.raw().y;
    p.apply({ dt: 0.025, mx: 0, my: -1, dash: true });
    expect(p.raw().dashLeft).toBeCloseTo(K.dashSeconds - 0.025, 9);
    expect(before - p.raw().y).toBeCloseTo(K.dashSpeed * 0.025, 9);
  });
});

describe('a replay is a rewind, not a second application', () => {
  it('does not spend the dash timer twice on the commands still in flight', () => {
    // `predicted` already contains every pending command. Replaying them on top
    // of it reset the position — which the snapshot overwrites anyway — while
    // decrementing every timer a second time. Measured before the fix: a walking
    // client burned 1.8 s of cooldown in 0.9 s of wall time, and a dash whose
    // commands were still in flight ended in half its length.
    const p = predictor();
    p.apply({ dt: 0.025, mx: 0, my: -1, dash: true });
    for (let i = 0; i < 3; i++) p.apply({ dt: 0.025, mx: 0, my: 0 });
    const straight = p.raw();
    // The server has acknowledged only the dash. The other three are replayed.
    p.reconcile({ x: straight.x, y: straight.y, ack: 1, dashCooldown: straight.dashCooldown + 3 * 0.025, slowLeft: 0 });
    expect(p.raw().dashLeft).toBeCloseTo(straight.dashLeft, 9);
    expect(p.raw().dashCooldown).toBeCloseTo(straight.dashCooldown, 9);
    expect(p.raw().streak).toBeCloseTo(straight.streak, 9);
  });

  it('still lands exactly on the server once everything is acknowledged', () => {
    const p = predictor();
    for (let i = 0; i < 6; i++) p.apply({ dt: 0.025, mx: 0, my: 1 });
    p.reconcile({ x: 4.25, y: 17, ack: 6, dashCooldown: 0, slowLeft: 0 });
    expect(p.raw().x).toBeCloseTo(4.25, 9);
    expect(p.raw().y).toBeCloseTo(17, 9);
  });
});
