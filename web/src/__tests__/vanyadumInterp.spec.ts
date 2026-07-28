import { describe, expect, it } from 'vitest';
import { BUFFER_FRAMES, createInterpolator, shortestTurn } from '../lib/vanyadumInterp';

/**
 * Entity interpolation — how everything that is not you is drawn.
 *
 * The three degradations at the bottom are the interesting half: what this does
 * when the buffer is empty, stale or dry decides whether a bad connection looks
 * like a pause or like a peer teleporting through a wall.
 */

const DELAY = 100;

function peer(id: string, x: number, yaw = 0) {
  return { id, x, y: 0, z: 1.65, yaw, state: 0 };
}

describe('createInterpolator', () => {
  it('draws nothing before anything has arrived', () => {
    // A peer never heard of has no last known position to hold.
    expect(createInterpolator(DELAY).sample(1000)).toEqual([]);
  });

  it('interpolates between the two frames bracketing the render instant', () => {
    const i = createInterpolator(DELAY);
    i.push(1000, [peer('a', 0)]);
    i.push(1100, [peer('a', 10)]);
    // Rendering at 1150 means showing 1050 — halfway between the two.
    const [p] = i.sample(1150);
    expect(p.x).toBeCloseTo(5, 6);
  });

  it('draws the past, not the present — that is the whole idea', () => {
    const i = createInterpolator(DELAY);
    i.push(1000, [peer('a', 0)]);
    i.push(1100, [peer('a', 10)]);
    // At 1200 the newest frame says 10, but we are drawing 1100's world.
    expect(i.sample(1200)[0].x).toBeCloseTo(10, 6);
    // ...and a moment earlier we are genuinely behind it.
    expect(i.sample(1160)[0].x).toBeLessThan(10);
  });

  it('holds the newest frame rather than extrapolating when the buffer runs dry', () => {
    // Extrapolation is what makes a peer walk through a wall and snap back.
    // Holding makes them pause: a worse-looking correct answer, and correct
    // wins.
    const i = createInterpolator(DELAY);
    i.push(1000, [peer('a', 0)]);
    i.push(1100, [peer('a', 10)]);
    const late = i.sample(5000);
    expect(late[0].x).toBe(10);
  });

  it('shows the oldest frame when asked about a time before it', () => {
    // Stale, and saying so by being visibly behind beats guessing.
    const i = createInterpolator(DELAY);
    i.push(1000, [peer('a', 7)]);
    expect(i.sample(1000)[0].x).toBe(7);
  });

  it('shows a peer that appeared mid-gap at the position it appeared in', () => {
    const i = createInterpolator(DELAY);
    i.push(1000, [peer('a', 0)]);
    i.push(1100, [peer('a', 10), peer('b', 42)]);
    const out = i.sample(1150);
    expect(out.find((p) => p.id === 'b')?.x).toBe(42);
  });

  it('drops a peer that is no longer in the newer frame', () => {
    const i = createInterpolator(DELAY);
    i.push(1000, [peer('a', 0), peer('b', 1)]);
    i.push(1100, [peer('a', 10)]);
    expect(i.sample(1150).map((p) => p.id)).toEqual(['a']);
  });

  it('takes the short way round when a peer turns through the wrap point', () => {
    // The single most visible bug in naive angle interpolation: without this a
    // peer turning from just under π to just over −π spins a full circle on the
    // spot.
    const i = createInterpolator(DELAY);
    i.push(1000, [peer('a', 0, Math.PI - 0.1)]);
    i.push(1100, [peer('a', 0, -Math.PI + 0.1)]);
    const yaw = i.sample(1150)[0].yaw;
    expect(Math.abs(yaw)).toBeGreaterThan(Math.PI - 0.2);
  });

  it('does not interpolate a discrete state', () => {
    // Halfway between alive and dead is not something anything can draw.
    const i = createInterpolator(DELAY);
    i.push(1000, [{ ...peer('a', 0), state: 0 }]);
    i.push(1100, [{ ...peer('a', 10), state: 2 }]);
    expect(i.sample(1150)[0].state).toBe(2);
  });

  it('tolerates frames arriving out of order', () => {
    const i = createInterpolator(DELAY);
    i.push(1100, [peer('a', 10)]);
    i.push(1000, [peer('a', 0)]);
    expect(i.sample(1150)[0].x).toBeCloseTo(5, 6);
  });

  it('bounds the buffer, so a tab left open does not accumulate a world', () => {
    const i = createInterpolator(DELAY);
    for (let t = 0; t < 500; t += 50) i.push(t, [peer('a', t)]);
    expect(i.size()).toBeLessThanOrEqual(BUFFER_FRAMES);
  });

  it('forgets everything on reset', () => {
    const i = createInterpolator(DELAY);
    i.push(1000, [peer('a', 0)]);
    i.reset();
    expect(i.size()).toBe(0);
    expect(i.sample(2000)).toEqual([]);
  });
});

describe('shortestTurn', () => {
  it('answers the signed short way round', () => {
    expect(shortestTurn(0, 1)).toBeCloseTo(1, 9);
    expect(shortestTurn(0, -1)).toBeCloseTo(-1, 9);
    expect(shortestTurn(Math.PI - 0.1, -Math.PI + 0.1)).toBeCloseTo(0.2, 6);
    expect(shortestTurn(-Math.PI + 0.1, Math.PI - 0.1)).toBeCloseTo(-0.2, 6);
  });

  it('never returns more than half a turn', () => {
    for (let a = -10; a < 10; a += 0.37) {
      for (let b = -10; b < 10; b += 0.53) {
        expect(Math.abs(shortestTurn(a, b))).toBeLessThanOrEqual(Math.PI + 1e-9);
      }
    }
  });
});
