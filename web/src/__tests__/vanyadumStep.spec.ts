import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import { sanitise, sectorAt, step, type StepConstants, type StepPlayer } from '../lib/vanyadumStep';
import type { VanyadumLevel } from '../lib/vanyadumLevel';

/**
 * The conformance test that makes client-side prediction safe.
 *
 * `Step` exists twice — in Go and in TypeScript — because prediction cannot
 * work otherwise (ADR-052). This file is the thing that stops the two drifting:
 * the Go test emits a level, an input transcript and the resulting position
 * trace, and the port must reproduce it. Regenerate the vectors with
 *
 *     ./dev.sh test -run TestGoldenVectors ./internal/gamevanyadum/ -update
 *
 * and this test goes red until the port is brought back into line. That is the
 * intended sequence, not an accident.
 */

interface GoldenPoint {
  x: number;
  y: number;
  s: number;
}

interface GoldenCase {
  name: string;
  level: VanyadumLevel;
  start: { x: number; y: number };
  sector: number;
  commands: { dt: number; mx?: number; my?: number; yaw?: number; pitch?: number }[];
  trace: GoldenPoint[];
}

interface GoldenFile {
  constants: {
    walk_speed: number;
    radius: number;
    max_step: number;
    max_pitch: number;
    max_step_seconds: number;
    collision_passes: number;
  };
  cases: GoldenCase[];
}

const golden: GoldenFile = JSON.parse(
  readFileSync(
    resolve(__dirname, '../../../internal/gamevanyadum/testdata/step_vectors.json'),
    'utf8',
  ),
);

const K: StepConstants = {
  walkSpeed: golden.constants.walk_speed,
  radius: golden.constants.radius,
  maxStep: golden.constants.max_step,
  maxPitch: golden.constants.max_pitch,
  maxStepSeconds: golden.constants.max_step_seconds,
  collisionPasses: golden.constants.collision_passes,
};

/**
 * How far the port may differ from the original.
 *
 * NOT zero, and the reason is worth stating: Go's `math.Sincos` and JavaScript's
 * `Math.sin`/`Math.cos` are both IEEE-754 doubles but neither is required to be
 * correctly rounded, so the two runtimes may disagree in the last bit or two.
 * A micrometre is far below anything the game can express — positions go on the
 * wire quantised to the centimetre — and far below any logic error, which shows
 * up as centimetres or metres rather than as ulps. So this tolerance catches
 * every divergence that matters and none that does not.
 */
const TOLERANCE = 1e-6;

describe('the TypeScript port of Step conforms to the Go original', () => {
  it('has vectors to check against at all', () => {
    // A guard against the file going missing or being emptied, which would
    // otherwise make every test below pass vacuously.
    expect(golden.cases.length).toBeGreaterThan(0);
    for (const c of golden.cases) {
      expect(c.trace.length).toBe(c.commands.length);
      expect(c.trace.length).toBeGreaterThan(0);
    }
  });

  it('agrees on the constants it is simulating with', () => {
    // Checked separately so a mismatch reports "your walk speed is wrong"
    // rather than a hundred failing positions.
    expect(K.walkSpeed).toBeGreaterThan(0);
    expect(K.radius).toBeGreaterThan(0);
    expect(K.collisionPasses).toBeGreaterThanOrEqual(1);
  });

  for (const c of golden.cases) {
    it(`reproduces the trace: ${c.name} (${c.trace.length} steps)`, () => {
      let p: StepPlayer = {
        x: c.start.x,
        y: c.start.y,
        yaw: 0,
        pitch: 0,
        sector: c.sector,
      };
      for (let i = 0; i < c.commands.length; i++) {
        const g = c.commands[i];
        p = step(
          c.level,
          p,
          { dt: g.dt, mx: g.mx ?? 0, my: g.my ?? 0, yaw: g.yaw ?? 0, pitch: g.pitch ?? 0 },
          K,
        );
        const want = c.trace[i];
        // Reported with the step index, because a divergence almost always
        // starts at one identifiable moment — a doorway, a corner — and the
        // first one that differs is the only one worth reading.
        expect(
          Math.hypot(p.x - want.x, p.y - want.y),
          `${c.name} step ${i}: got (${p.x}, ${p.y}) want (${want.x}, ${want.y})`,
        ).toBeLessThan(TOLERANCE);
        expect(p.sector, `${c.name} step ${i}: sector`).toBe(want.s);
      }
    });
  }
});

describe('sanitise', () => {
  it('clamps every hostile field the way the server does', () => {
    const got = sanitise(
      { dt: 1e9, mx: Infinity, my: -500, yaw: Number.NaN, pitch: 99 },
      { ...K, maxPitch: 1.5, maxStepSeconds: 0.2 },
    );
    expect(got.dt).toBe(0.2);
    expect(got.mx).toBe(1);
    expect(got.my).toBe(-1);
    expect(got.yaw).toBe(0);
    expect(got.pitch).toBe(1.5);
  });

  it('wraps a huge but legal yaw instead of handing it to trigonometry', () => {
    const got = sanitise({ dt: 0.05, mx: 0, my: 0, yaw: 1e18, pitch: 0 }, K);
    expect(Math.abs(got.yaw)).toBeLessThanOrEqual(Math.PI * 2);
  });
});

describe('sectorAt', () => {
  it('answers −1 outside every room, like the server', () => {
    const level = golden.cases[golden.cases.length - 1].level;
    expect(sectorAt(level, -100, -100)).toBe(-1);
  });
});
