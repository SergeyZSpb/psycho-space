import { describe, expect, it } from 'vitest';
import {
  LOOK_SENSITIVITY,
  STICK_DEADZONE,
  applyLook,
  axesFromKeys,
  buildInputFrame,
  createEmitter,
  stickVector,
  wrapAngle,
  type VanyadumCommand,
} from '../lib/vanyadumInput';

describe('stickVector', () => {
  const origin = { x: 100, y: 100 };

  it('walks forward when the thumb goes up the screen', () => {
    // Screen +y is downwards, so forward is a negative dy. Getting this
    // backwards is the classic inverted-controls bug and it is not obvious from
    // reading the code, so it is pinned.
    const v = stickVector(origin, { x: 100, y: 50 }, 50);
    expect(v.my).toBeCloseTo(1, 6);
    expect(v.mx).toBeCloseTo(0, 6);
  });

  it('strafes right when the thumb goes right', () => {
    const v = stickVector(origin, { x: 150, y: 100 }, 50);
    expect(v.mx).toBeCloseTo(1, 6);
    expect(v.my).toBeCloseTo(0, 6);
  });

  it('ignores a thumb resting inside the dead zone', () => {
    // Without this the player drifts while standing still, which reads as the
    // game being possessed rather than as a sensitivity problem.
    const nudge = STICK_DEADZONE * 0.5 * 50;
    expect(stickVector(origin, { x: 100 + nudge, y: 100 }, 50)).toEqual({ mx: 0, my: 0 });
  });

  it('clamps rather than normalises, so half a push is half speed', () => {
    const half = stickVector(origin, { x: 100, y: 75 }, 50);
    expect(half.my).toBeCloseTo(0.5, 6);

    const beyond = stickVector(origin, { x: 100, y: -400 }, 50);
    expect(beyond.my).toBeCloseTo(1, 6);
  });

  it('answers zero for a zero radius instead of dividing by it', () => {
    expect(stickVector(origin, { x: 200, y: 200 }, 0)).toEqual({ mx: 0, my: 0 });
  });
});

describe('applyLook', () => {
  it('turns right when you drag right', () => {
    // The server's convention read back: yaw zero faces world +Y and increasing
    // yaw swings towards +X, which is a right turn seen from above.
    const next = applyLook({ yaw: 0, pitch: 0 }, 100, 0, 1.5);
    expect(next.yaw).toBeCloseTo(100 * LOOK_SENSITIVITY, 6);
  });

  it('looks down when you drag down', () => {
    const next = applyLook({ yaw: 0, pitch: 0 }, 0, 100, 1.5);
    expect(next.pitch).toBeLessThan(0);
  });

  it('clamps pitch, because beyond straight up the horizon rolls', () => {
    expect(applyLook({ yaw: 0, pitch: 0 }, 0, -100000, 1.5).pitch).toBe(1.5);
    expect(applyLook({ yaw: 0, pitch: 0 }, 0, 100000, 1.5).pitch).toBe(-1.5);
  });

  it('wraps yaw, so a long run of turning never grows it without bound', () => {
    let state = { yaw: 0, pitch: 0 };
    for (let i = 0; i < 500; i++) state = applyLook(state, 100, 0, 1.5);
    expect(Math.abs(state.yaw)).toBeLessThanOrEqual(Math.PI + 1e-9);
  });
});

describe('wrapAngle', () => {
  it('brings any angle into −π..π', () => {
    expect(wrapAngle(0)).toBe(0);
    expect(wrapAngle(Math.PI * 3)).toBeCloseTo(Math.PI, 6);
    expect(wrapAngle(-Math.PI * 3)).toBeCloseTo(-Math.PI, 6);
    expect(wrapAngle(Math.PI * 100.5)).toBeGreaterThanOrEqual(-Math.PI);
  });
});

describe('axesFromKeys', () => {
  it('maps WASD and the arrows the same way', () => {
    expect(axesFromKeys(new Set(['KeyW']))).toEqual({ mx: 0, my: 1 });
    expect(axesFromKeys(new Set(['ArrowUp']))).toEqual({ mx: 0, my: 1 });
    expect(axesFromKeys(new Set(['KeyA']))).toEqual({ mx: -1, my: 0 });
    expect(axesFromKeys(new Set(['KeyS', 'KeyD']))).not.toEqual({ mx: 1, my: -1 });
  });

  it('cancels opposite keys instead of preferring one', () => {
    expect(axesFromKeys(new Set(['KeyW', 'KeyS']))).toEqual({ mx: 0, my: 0 });
  });

  it('does not make a diagonal faster than a straight line', () => {
    const d = axesFromKeys(new Set(['KeyW', 'KeyD']));
    expect(Math.hypot(d.mx, d.my)).toBeCloseTo(1, 6);
  });

  it('ignores keys it does not know', () => {
    expect(axesFromKeys(new Set(['Space', 'ShiftLeft']))).toEqual({ mx: 0, my: 0 });
  });
});

describe('createEmitter', () => {
  const opts = { hz: 40, maxStepSeconds: 0.2, maxPerWake: 4 };
  const walking = { mx: 0, my: 1, yaw: 0.5, pitch: 0 };
  const still = { mx: 0, my: 0, yaw: 0.5, pitch: 0 };

  it('produces nothing from a single reading, because dt needs two', () => {
    const e = createEmitter(opts);
    expect(e.due(1000, walking)).toEqual([]);
  });

  it('emits at its own fixed rate, not at the frame rate', () => {
    // THE reason this replaced a per-frame sampler: whatever is predicted must
    // be exactly what is sent, so the number of commands in a send window has
    // to be a property of the emitter rather than of the phone's refresh rate.
    const fast = createEmitter(opts);
    fast.due(0, walking);
    let n = 0;
    for (let t = 8; t <= 1000; t += 8) n += fast.due(t, walking).length; // 120 Hz

    const slow = createEmitter(opts);
    slow.due(0, walking);
    let m = 0;
    for (let t = 33; t <= 1000; t += 33) m += slow.due(t, walking).length; // 30 Hz

    expect(n).toBeGreaterThan(30);
    expect(Math.abs(n - m)).toBeLessThanOrEqual(2);
  });

  it('gives every command the same sub-step, so replay is exact', () => {
    const e = createEmitter(opts);
    e.due(0, walking);
    const cmds = e.due(200, walking);
    expect(cmds.length).toBeGreaterThan(0);
    for (const c of cmds) expect(c.dt).toBeCloseTo(1 / opts.hz, 9);
  });

  it('emits nothing at all while nobody is touching anything', () => {
    // A player at rest costs the network nothing. The naive version ships ten
    // frames a second of "dt of nothing" forever, to a phone on mobile data.
    const e = createEmitter(opts);
    e.due(0, still);
    let n = 0;
    for (let t = 25; t <= 2000; t += 25) n += e.due(t, still).length;
    expect(n).toBe(0);
  });

  it('but emits a turn, because aim is state the server has to be told about', () => {
    const e = createEmitter(opts);
    e.due(0, still);
    expect(e.due(30, { ...still, yaw: 0.9 }).length).toBe(1);
  });

  it('caps what one wake may produce, so a stalled tab cannot flood', () => {
    // The server's time budget would refuse the surplus anyway; not creating it
    // is what keeps the client's own prediction agreeing with that refusal.
    const e = createEmitter(opts);
    e.due(0, walking);
    expect(e.due(60_000, walking).length).toBeLessThanOrEqual(opts.maxPerWake);
  });

  it('carries fractional leftovers rather than dropping them', () => {
    // Dropping them makes the client's simulated time drift behind the
    // server's, which shows up as a correction always in the same direction.
    const e = createEmitter(opts);
    e.due(0, walking);
    let n = 0;
    for (let t = 30; t <= 3000; t += 30) n += e.due(t, walking).length;
    // ~3 s at 40 Hz is ~120 commands; a dropped remainder would give ~100.
    expect(n).toBeGreaterThan(110);
  });

  it('forgets everything on reset, so a new run starts clean', () => {
    const e = createEmitter(opts);
    e.due(0, walking);
    e.due(200, walking);
    e.reset();
    expect(e.due(5000, walking)).toEqual([]);
  });
});

describe('buildInputFrame', () => {
  const cmd = (seq: number): VanyadumCommand => ({ seq, dt: 0.025, mx: 0, my: 1, yaw: 0, pitch: 0 });

  it('carries the tick this client last drew, so the server can derive the round trip', () => {
    // Derived rather than reported: lag compensation rewinds by exactly that
    // number, so a client choosing its own would be choosing an advantage.
    expect(buildInputFrame(42, [cmd(1)], []).k).toBe(42);
  });

  it('puts the unacknowledged tail first and the fresh commands last', () => {
    // The server applies them in order, so the resend has to come before the
    // new input or a replayed command would land after work that followed it.
    const frame = buildInputFrame(0, [cmd(5), cmd(6)], [cmd(3), cmd(4)]);
    expect(frame.cmds.map((c) => c.q)).toEqual([3, 4, 5, 6]);
  });

  it('never sends the same sequence twice in one frame', () => {
    // The unacknowledged list still contains what was just applied, so without
    // the de-duplication every frame would carry its own fresh commands twice.
    const frame = buildInputFrame(0, [cmd(5)], [cmd(4), cmd(5)]);
    expect(frame.cmds.map((c) => c.q)).toEqual([4, 5]);
  });

  it('sends intent and never a fact', () => {
    // The rule the whole design rests on: a prediction is something the client
    // draws, never something it asserts. No position, no health, no hit claim.
    const frame = buildInputFrame(7, [cmd(1)], []) as unknown as Record<string, unknown>;
    expect(Object.keys(frame).sort()).toEqual(['cmds', 'k', 't']);
    expect(Object.keys(frame.cmds as object[])).toHaveLength(1);
    for (const c of (frame.cmds as Record<string, unknown>[])) {
      expect(Object.keys(c).sort()).toEqual(['dt', 'mx', 'my', 'pitch', 'q', 'yaw']);
    }
  });

  it('is happy with nothing to resend', () => {
    expect(buildInputFrame(0, [cmd(1)], []).cmds.map((c) => c.q)).toEqual([1]);
  });
});
