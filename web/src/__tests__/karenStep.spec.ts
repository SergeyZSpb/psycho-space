import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import {
  multiplierFor,
  sanitise,
  step,
  type StepConstants,
  type StepPlayer,
} from '../lib/karenStep';
import type { KarenRect } from '../api/types';

/**
 * The conformance test that makes client-side prediction safe.
 *
 * `Step` exists twice — in Go and in TypeScript — because prediction cannot work
 * otherwise. This file is the thing that stops the two drifting: the Go test
 * emits a desk layout, an input transcript and the resulting player trace, and
 * the port must reproduce it. Regenerate the vectors with
 *
 *     ./dev.sh test -run TestGoldenVectors ./internal/gamekaren/ -update
 *
 * and this test goes red until the port is brought back into line. That is the
 * intended sequence, not an accident.
 *
 * IT COVERS THE MONEY AS WELL AS THE MOVEMENT, which is the difference from
 * «ВАНЯДУМ»'s equivalent: the streak, the grace window and the salary are all in
 * `Step`, so a retuned ramp or a mis-ordered dash rule shows up here rather than
 * in a screenshot somebody takes a week later.
 */

/** Every field of the simulated player, as the Go emits it. */
interface GoldenPlayer {
  x: number;
  y: number;
  salary: number;
  streak: number;
  move_grace: number;
  dash_left: number;
  dash_cooldown: number;
}

interface GoldenCase {
  name: string;
  desks: KarenRect[];
  start: GoldenPlayer;
  commands: { dt: number; mx?: number; my?: number; d?: boolean }[];
  trace: GoldenPlayer[];
}

interface GoldenFile {
  constants: {
    office_w: number;
    office_h: number;
    player_radius: number;
    walk_speed: number;
    dash_speed: number;
    dash_seconds: number;
    dash_cooldown: number;
    idle_threshold: number;
    base_per_second: number;
    ramp_seconds: number;
    max_multiplier: number;
    grace_seconds: number;
    max_step_seconds: number;
  };
  cases: GoldenCase[];
}

const VECTORS = resolve(__dirname, '../../../internal/gamekaren/testdata/step_vectors.json');

/**
 * The vectors, or an empty set with the catalogue's own numbers.
 *
 * READING IT DEFENSIVELY IS NOT THE SAME AS TOLERATING ITS ABSENCE. A missing or
 * unreadable file makes the first test below fail loudly — that is the point of
 * the emptiness — while the rule claims further down still run against the
 * production constants, so a broken port is reported as "the dash earns money"
 * rather than as one import-time exception that takes the whole file with it.
 */
function loadGolden(): GoldenFile {
  try {
    return JSON.parse(readFileSync(VECTORS, 'utf8')) as GoldenFile;
  } catch {
    return {
      constants: {
        office_w: 16,
        office_h: 22,
        player_radius: 0.35,
        walk_speed: 3.2,
        dash_speed: 9,
        dash_seconds: 0.22,
        dash_cooldown: 4,
        idle_threshold: 0.05,
        base_per_second: 120,
        ramp_seconds: 6,
        max_multiplier: 3,
        grace_seconds: 0.3,
        max_step_seconds: 0.2,
      },
      cases: [],
    };
  }
}

const golden: GoldenFile = loadGolden();

const K: StepConstants = {
  officeW: golden.constants.office_w,
  officeH: golden.constants.office_h,
  playerRadius: golden.constants.player_radius,
  walkSpeed: golden.constants.walk_speed,
  dashSpeed: golden.constants.dash_speed,
  dashSeconds: golden.constants.dash_seconds,
  dashCooldownSeconds: golden.constants.dash_cooldown,
  idleThreshold: golden.constants.idle_threshold,
  maxStepSeconds: golden.constants.max_step_seconds,
  basePerSecond: golden.constants.base_per_second,
  rampSeconds: golden.constants.ramp_seconds,
  maxMultiplier: golden.constants.max_multiplier,
  graceSeconds: golden.constants.grace_seconds,
};

/**
 * How far the port may differ from the original.
 *
 * There is no trigonometry in this simulation — it is addition, multiplication
 * and comparison on IEEE doubles — so the two runtimes should agree bit for bit
 * and this is slack rather than a budget. A micrometre is far below anything the
 * game can express (positions go on the wire quantised to the centimetre) and a
 * millionth of a rouble is below anything it can pay.
 */
const TOLERANCE = 1e-6;

function fromGolden(p: GoldenPlayer): StepPlayer {
  return {
    x: p.x,
    y: p.y,
    salary: p.salary,
    streak: p.streak,
    moveGrace: p.move_grace,
    dashLeft: p.dash_left,
    dashCooldown: p.dash_cooldown,
  };
}

describe('the TypeScript port of Step conforms to the Go original', () => {
  it('has vectors to check against at all', () => {
    // A guard against the file going missing or being emptied, which would
    // otherwise make every test below pass vacuously.
    expect(golden.cases.length, `no golden vectors at ${VECTORS}`).toBeGreaterThan(0);
    for (const c of golden.cases) {
      expect(c.trace.length).toBe(c.commands.length);
      expect(c.trace.length).toBeGreaterThan(0);
    }
  });

  it('agrees on the constants it is simulating with', () => {
    // Checked separately so a mismatch reports "your walk speed is wrong" rather
    // than a hundred failing positions.
    expect(K.walkSpeed).toBeGreaterThan(0);
    expect(K.dashSpeed).toBeGreaterThan(K.walkSpeed);
    expect(K.playerRadius).toBeGreaterThan(0);
    expect(K.rampSeconds).toBeGreaterThan(0);
    expect(K.maxMultiplier).toBeGreaterThan(1);
    // The client mirrors this one rather than being served it — see
    // IDLE_THRESHOLD in karenStep.ts. If the server retunes it, this fails.
    expect(K.idleThreshold).toBeGreaterThan(0);
  });

  for (const c of golden.cases) {
    it(`reproduces the trace: ${c.name} (${c.trace.length} steps)`, () => {
      let p = fromGolden(c.start);
      for (let i = 0; i < c.commands.length; i++) {
        const g = c.commands[i];
        p = step(c.desks, p, { dt: g.dt, mx: g.mx ?? 0, my: g.my ?? 0, dash: g.d ?? false }, K);
        const want = c.trace[i];
        // Reported with the step index, because a divergence almost always
        // starts at one identifiable moment — a desk, the instant a dash ends,
        // the tick the grace window runs out — and the first one that differs is
        // the only one worth reading.
        const where = `${c.name} step ${i}`;
        expect(
          Math.hypot(p.x - want.x, p.y - want.y),
          `${where}: at (${p.x}, ${p.y}), want (${want.x}, ${want.y})`,
        ).toBeLessThan(TOLERANCE);
        expect(Math.abs(p.salary - want.salary), `${where}: salary`).toBeLessThan(TOLERANCE);
        expect(Math.abs(p.streak - want.streak), `${where}: streak`).toBeLessThan(TOLERANCE);
        expect(Math.abs(p.moveGrace - want.move_grace), `${where}: moveGrace`).toBeLessThan(
          TOLERANCE,
        );
        expect(Math.abs(p.dashLeft - want.dash_left), `${where}: dashLeft`).toBeLessThan(TOLERANCE);
        expect(
          Math.abs(p.dashCooldown - want.dash_cooldown),
          `${where}: dashCooldown`,
        ).toBeLessThan(TOLERANCE);
      }
    });
  }
});

// ---------------------------------------------------------------------------
// The rules the vectors pin, stated again as claims — so a failure says WHICH
// rule broke rather than "step 47 of case 3 diverged".
// ---------------------------------------------------------------------------

const ROOM: readonly KarenRect[] = [];

function player(over: Partial<StepPlayer> = {}): StepPlayer {
  return {
    x: 8,
    y: 11,
    salary: 0,
    streak: 0,
    moveGrace: 0,
    dashLeft: 0,
    dashCooldown: 0,
    ...over,
  };
}

const still = { dt: 0.05, mx: 0, my: 0 };
const walking = { dt: 0.05, mx: 0, my: 1 };

describe('sanitise', () => {
  it('clamps every hostile field the way the server does', () => {
    // A dt of a thousand seconds, an axis of a million and a NaN are all one
    // JSON frame away. The port runs the same clamps for a different reason: it
    // has to PREDICT from the command the server will actually simulate.
    const got = sanitise({ dt: 1e9, mx: Number.POSITIVE_INFINITY, my: -500 }, K);
    expect(got.dt).toBe(K.maxStepSeconds);
    // Clamped to the unit square first, then normalised because (1,−1) is longer
    // than one — which is the diagonal rule, not a second clamp.
    expect(Math.hypot(got.mx, got.my)).toBeCloseTo(1, 9);
    expect(got.mx).toBeGreaterThan(0);
    expect(got.my).toBeLessThan(0);
  });

  it('turns a NaN into a standstill rather than into a NaN position', () => {
    const got = sanitise({ dt: Number.NaN, mx: Number.NaN, my: Number.NaN }, K);
    expect(got).toEqual({ dt: 0, mx: 0, my: 0 });
  });

  it('leaves a half-pushed stick alone, so it stays analogue', () => {
    const got = sanitise({ dt: 0.05, mx: 0.3, my: -0.4 }, K);
    expect(got.mx).toBeCloseTo(0.3, 9);
    expect(got.my).toBeCloseTo(-0.4, 9);
  });

  it('keeps the sequence and the dash, and omits the dash when there is none', () => {
    expect(sanitise({ seq: 9, dt: 0.05, mx: 0, my: 1, dash: true }, K)).toEqual({
      seq: 9,
      dt: 0.05,
      mx: 0,
      my: 1,
      dash: true,
    });
    expect('dash' in sanitise({ dt: 0.05, mx: 0, my: 1 }, K)).toBe(false);
  });

  it('is what makes a diagonal no faster than a straight line', () => {
    // Asserted through `step`, because that is where it matters: an unsanitised
    // client would predict itself 41% further than the server moved it, on every
    // diagonal push, forever.
    const straight = step(ROOM, player(), { dt: 0.05, mx: 0, my: 1 }, K);
    const diagonal = step(ROOM, player(), { dt: 0.05, mx: 1, my: 1 }, K);
    expect(Math.hypot(diagonal.x - 8, diagonal.y - 11)).toBeCloseTo(
      Math.hypot(straight.x - 8, straight.y - 11),
      9,
    );
  });
});

describe('the money', () => {
  it('accrues only while standing still', () => {
    const idle = step(ROOM, player(), still, K);
    const moving = step(ROOM, player(), walking, K);
    expect(idle.salary).toBeGreaterThan(0);
    expect(moving.salary).toBe(0);
  });

  it('reaches the cap after exactly the ramp, and goes no further', () => {
    let p = player();
    const steps = Math.round(K.rampSeconds / still.dt);
    for (let i = 0; i < steps; i++) p = step(ROOM, p, still, K);
    expect(p.streak).toBeCloseTo(K.rampSeconds, 9);
    expect(multiplierFor(p.streak, K.rampSeconds, K.maxMultiplier)).toBeCloseTo(K.maxMultiplier, 9);
    for (let i = 0; i < 20; i++) p = step(ROOM, p, still, K);
    expect(multiplierFor(p.streak, K.rampSeconds, K.maxMultiplier)).toBeCloseTo(K.maxMultiplier, 9);
  });

  it('forgives a movement shorter than the grace window', () => {
    let p = player({ streak: 4 });
    // Half the grace, spent walking. The streak survives it untouched.
    const steps = Math.floor(K.graceSeconds / 2 / walking.dt);
    for (let i = 0; i < steps; i++) p = step(ROOM, p, walking, K);
    expect(p.streak).toBe(4);
    expect(p.moveGrace).toBeGreaterThan(0);
  });

  it('and resets it once the movement outlasts the window', () => {
    let p = player({ streak: 4 });
    const steps = Math.ceil(K.graceSeconds / walking.dt) + 2;
    for (let i = 0; i < steps; i++) p = step(ROOM, p, walking, K);
    expect(p.streak).toBe(0);
    // Clamped rather than left to grow, so a long walk banks no debt.
    expect(p.moveGrace).toBeCloseTo(K.graceSeconds, 9);
  });

  it('starts the streak over the moment you stand still after being reset', () => {
    // The state a long walk leaves behind: nothing banked, and the grace window
    // spent. Standing still clears the window and begins again from zero.
    let p = step(ROOM, player({ streak: 0, moveGrace: K.graceSeconds }), still, K);
    expect(p.moveGrace).toBe(0);
    expect(p.streak).toBeCloseTo(still.dt, 9);
    p = step(ROOM, p, still, K);
    expect(p.streak).toBeCloseTo(still.dt * 2, 9);
  });

  it('and an unbroken streak simply carries on, rather than restarting', () => {
    // The counterpart, and worth pinning: a brief movement inside the grace
    // window leaves the streak where it was, so the next still sub-step adds to
    // it instead of beginning again.
    const p = step(ROOM, player({ streak: 4, moveGrace: 0.1 }), still, K);
    expect(p.streak).toBeCloseTo(4 + still.dt, 9);
  });
});

describe('the dash — the one movement that costs nothing', () => {
  it('preserves the streak and does not grow it', () => {
    const before = player({ streak: 4 });
    const after = step(ROOM, before, { ...walking, dash: true }, K);
    expect(after.streak).toBe(4);
    expect(after.moveGrace).toBe(0);
  });

  it('earns nothing, because you are not standing still', () => {
    const after = step(ROOM, player({ streak: 6 }), { ...walking, dash: true }, K);
    expect(after.salary).toBe(0);
  });

  it('moves at the dash speed from its very first sub-step', () => {
    const dashed = step(ROOM, player(), { ...walking, dash: true }, K);
    const walked = step(ROOM, player(), walking, K);
    expect(dashed.y - 11).toBeCloseTo(K.dashSpeed * walking.dt, 9);
    expect(walked.y - 11).toBeCloseTo(K.walkSpeed * walking.dt, 9);
  });

  it('cannot be re-triggered inside its cooldown', () => {
    let p = step(ROOM, player(), { ...walking, dash: true }, K);
    const cooldown = p.dashCooldown;
    expect(cooldown).toBeCloseTo(K.dashCooldownSeconds - walking.dt, 9);
    // Ask again immediately: refused, and the cooldown is not extended either.
    p = step(ROOM, p, { ...walking, dash: true }, K);
    expect(p.dashCooldown).toBeCloseTo(cooldown - walking.dt, 9);
  });

  it('and a dash that has expired stops being fast', () => {
    let p = step(ROOM, player(), { ...walking, dash: true }, K);
    const steps = Math.ceil(K.dashSeconds / walking.dt) + 1;
    for (let i = 0; i < steps; i++) p = step(ROOM, p, walking, K);
    expect(p.dashLeft).toBe(0);
    const before = p.y;
    p = step(ROOM, p, walking, K);
    expect(p.y - before).toBeCloseTo(K.walkSpeed * walking.dt, 9);
  });
});

describe('the office', () => {
  it('never lets anybody off the floor, however hard they push', () => {
    let p = player({ x: 1, y: 1 });
    for (let i = 0; i < 400; i++) p = step(ROOM, p, { dt: 0.2, mx: -1, my: -1 }, K);
    expect(p.x).toBeCloseTo(K.playerRadius, 9);
    expect(p.y).toBeCloseTo(K.playerRadius, 9);

    for (let i = 0; i < 400; i++) p = step(ROOM, p, { dt: 0.2, mx: 1, my: 1 }, K);
    expect(p.x).toBeCloseTo(K.officeW - K.playerRadius, 9);
    expect(p.y).toBeCloseTo(K.officeH - K.playerRadius, 9);
  });

  it('never leaves anybody standing inside a desk', () => {
    const desks: KarenRect[] = [{ x: 4, y: 8, w: 4, h: 2 }];
    let p = player({ x: 6, y: 4 });
    // Walk straight into it and keep pushing.
    for (let i = 0; i < 200; i++) p = step(desks, p, { dt: 0.05, mx: 0, my: 1 }, K);
    const d = desks[0];
    const inside =
      p.x > d.x - K.playerRadius &&
      p.x < d.x + d.w + K.playerRadius &&
      p.y > d.y - K.playerRadius &&
      p.y < d.y + d.h + K.playerRadius;
    expect(inside).toBe(false);
  });

  it('is deterministic: the same input twice is the same player twice', () => {
    const desks: KarenRect[] = [{ x: 4, y: 8, w: 4, h: 2 }];
    const run = () => {
      let p = player({ x: 5.5, y: 5 });
      for (let i = 0; i < 120; i++) {
        p = step(desks, p, { dt: 0.025, mx: i % 3 === 0 ? 1 : -0.4, my: 0.8, dash: i === 30 }, K);
      }
      return p;
    };
    expect(run()).toEqual(run());
  });
});
