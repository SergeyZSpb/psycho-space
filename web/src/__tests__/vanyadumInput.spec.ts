import { describe, expect, it } from 'vitest';
import {
  LOOK_SENSITIVITY,
  STICK_DEADZONE,
  applyLook,
  axesFromKeys,
  coalesce,
  createSampler,
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

describe('coalesce', () => {
  const cmd = (dt: number, my: number): VanyadumCommand => ({ dt, mx: 0, my, yaw: 0, pitch: 0 });

  it('leaves a short list alone', () => {
    const list = [cmd(0.02, 1), cmd(0.02, 1)];
    expect(coalesce(list, 4)).toBe(list);
  });

  it('preserves total elapsed time when it merges', () => {
    // The point of merging rather than dropping: a stalled tab produces more
    // samples than a frame may carry, and dropping the surplus would silently
    // shorten the player's movement.
    const list = Array.from({ length: 20 }, () => cmd(0.01, 1));
    const merged = coalesce(list, 4);
    expect(merged.length).toBeLessThanOrEqual(4);
    const total = merged.reduce((s, c) => s + c.dt, 0);
    expect(total).toBeCloseTo(0.2, 6);
  });

  it('keeps the most recent intent in each bucket', () => {
    const list = [cmd(0.01, 1), cmd(0.01, -1), cmd(0.01, 1), cmd(0.01, -1)];
    const merged = coalesce(list, 2);
    expect(merged[0].my).toBe(-1);
    expect(merged[1].my).toBe(-1);
  });

  it('answers empty for a zero cap rather than throwing', () => {
    expect(coalesce([cmd(0.01, 1)], 0)).toEqual([]);
  });
});

describe('createSampler', () => {
  const axes = { mx: 0, my: 1, yaw: 0.5, pitch: 0 };
  const opts = { maxStepSeconds: 0.2, maxCommands: 4 };

  it('produces nothing from a single sample, because dt needs two', () => {
    const s = createSampler(opts);
    s.sample(1000, axes);
    expect(s.take()).toEqual([]);
  });

  it('measures dt between samples in seconds', () => {
    const s = createSampler(opts);
    s.sample(1000, axes);
    s.sample(1025, axes);
    const [c] = s.take();
    expect(c.dt).toBeCloseTo(0.025, 6);
    expect(c.yaw).toBe(0.5);
  });

  it('clamps a huge gap, which is what a backgrounded tab produces', () => {
    const s = createSampler(opts);
    s.sample(0, axes);
    s.sample(60_000, axes);
    expect(s.take()[0].dt).toBe(opts.maxStepSeconds);
  });

  it('never hands out more sub-steps than a frame may carry', () => {
    const s = createSampler(opts);
    for (let i = 0; i <= 40; i++) s.sample(i * 5, axes);
    expect(s.take().length).toBeLessThanOrEqual(opts.maxCommands);
  });

  it('empties itself when taken, so nothing is sent twice', () => {
    const s = createSampler(opts);
    s.sample(0, axes);
    s.sample(25, axes);
    expect(s.take()).toHaveLength(1);
    expect(s.take()).toEqual([]);
  });

  it('forgets everything on reset, so a new run starts clean', () => {
    const s = createSampler(opts);
    s.sample(0, axes);
    s.sample(25, axes);
    s.reset();
    expect(s.pendingCount()).toBe(0);
    s.sample(1000, axes);
    expect(s.take()).toEqual([]); // the clock restarted too
  });
});

describe('createSampler — the idle rule', () => {
  const opts = { maxStepSeconds: 0.2, maxCommands: 4 };
  const still = { mx: 0, my: 0, yaw: 0.5, pitch: 0 };

  it('records nothing at all while nobody is touching anything', () => {
    // Found by the layout suite rather than by reasoning: without this the
    // client ships ten frames a second of "dt of nothing" forever, to a phone on
    // mobile data, for a simulation that would do precisely nothing with them.
    const s = createSampler(opts);
    for (let i = 0; i <= 60; i++) s.sample(i * 16, still);
    expect(s.take()).toEqual([]);
  });

  it('but records a turn, because aim is state the server has to be told about', () => {
    const s = createSampler(opts);
    s.sample(0, still);
    s.sample(16, { ...still, yaw: 0.9 });
    expect(s.take()).toHaveLength(1);
  });

  it('and records movement even when the view is perfectly still', () => {
    const s = createSampler(opts);
    s.sample(0, still);
    s.sample(16, { ...still, my: 1 });
    expect(s.take()).toHaveLength(1);
  });

  it('stops recording the moment the stick is released', () => {
    const s = createSampler(opts);
    s.sample(0, still);
    s.sample(16, { ...still, my: 1 });
    s.sample(32, still);
    s.sample(48, still);
    // Only the walking sample survives; stopping needs no message, because the
    // server moves a player only when it drains a command.
    expect(s.take()).toHaveLength(1);
  });
});
