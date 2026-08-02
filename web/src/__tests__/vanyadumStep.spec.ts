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

/**
 * One step of the trace: where he ended up, and what state his gun is in.
 *
 * The gun's three fields are OPTIONAL because the artefact omits them at rest —
 * `cd`, `rl` and `ammo` are absent far more often than they are present, on a
 * file that already lands in every diff touching movement. Absent means zero,
 * and reading it as anything else is how a vacuous pass starts.
 */
interface GoldenPoint {
  x: number;
  y: number;
  s: number;
  /** Barrels loaded. Always present: a resting gun is full rather than empty. */
  b: number;
  /** Seconds, unquantised — the wire's millisecond rounding happens elsewhere. */
  cd?: number;
  rl?: number;
  ammo?: number;
}

interface GoldenCase {
  name: string;
  level: VanyadumLevel;
  start: { x: number; y: number };
  sector: number;
  /** How much ammunition the player begins with; absent means none. */
  start_ammo?: number;
  commands: {
    dt: number;
    mx?: number;
    my?: number;
    yaw?: number;
    pitch?: number;
    f?: boolean;
  }[];
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
    barrels: number;
    fire_cooldown_seconds: number;
    reload_seconds: number;
    reload_cost: number;
    ammo: string;
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
  barrels: golden.constants.barrels,
  fireCooldownSeconds: golden.constants.fire_cooldown_seconds,
  reloadSeconds: golden.constants.reload_seconds,
  reloadCost: golden.constants.reload_cost,
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

/**
 * How far the gun's countdowns may differ from the original: not at all.
 *
 * THE POSITION'S TOLERANCE DOES NOT APPLY HERE, and the reason is worth being
 * precise about rather than copying the looser number across. That tolerance
 * exists because `math.Sincos` and `Math.sin` are not required to be correctly
 * rounded, so two runtimes may disagree in the last bit. A timer is a
 * subtraction and a `max` against zero — both exactly specified by IEEE-754 in
 * both languages — so the accumulated value is not approximately equal across
 * the port, it is bit-for-bit equal, and a tolerance here would hide precisely
 * the kind of drift this artefact exists to catch. The vectors deliberately mix
 * sub-steps so the countdowns land on values with no short binary expansion:
 * `1.1449174941446927e-15` of reload left is in the file, and it is there
 * because it is exactly what a port that rounded would get wrong.
 */
const EXACT = 0;

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
    expect(K.barrels).toBeGreaterThan(0);
    expect(K.fireCooldownSeconds).toBeGreaterThan(0);
    expect(K.reloadSeconds).toBeGreaterThan(0);
  });

  it('has a case in which the trigger is actually pulled', () => {
    // THE GUARD AGAINST THE FAILURE THIS SUITE ALREADY HAD ONCE. Every gun
    // assertion below is a loop over the vectors, so a file whose commands never
    // set `f` would make all of them pass while pinning nothing at all about the
    // weapon — which is exactly the state this spec was in before it read
    // anything but x, y and the sector.
    const pulls = golden.cases.flatMap((c) => c.commands.filter((g) => g.f));
    expect(pulls.length).toBeGreaterThan(0);
    // And the transcript has to reach every branch, or a port could refuse every
    // pull and still reproduce the trace. A barrel spent, a reload begun, and a
    // gun that came back loaded are the three that matter.
    const points = golden.cases.flatMap((c) => c.trace);
    expect(points.some((p) => p.b === K.barrels - 1)).toBe(true);
    expect(points.some((p) => p.b === 0)).toBe(true);
    expect(points.some((p) => (p.rl ?? 0) > 0)).toBe(true);
    expect(points.some((p) => (p.cd ?? 0) > 0)).toBe(true);
  });

  for (const c of golden.cases) {
    it(`reproduces the trace: ${c.name} (${c.trace.length} steps)`, () => {
      let p: StepPlayer = {
        x: c.start.x,
        y: c.start.y,
        yaw: 0,
        pitch: 0,
        sector: c.sector,
        // The gun always starts full, exactly as the server's NewPlayer leaves
        // it, and the ammunition is the case's own — a reload cannot be
        // exercised by somebody with empty pockets.
        loaded: K.barrels,
        cooldown: 0,
        reload: 0,
        ammo: c.start_ammo ?? 0,
      };
      for (let i = 0; i < c.commands.length; i++) {
        const g = c.commands[i];
        p = step(
          c.level,
          p,
          {
            dt: g.dt,
            mx: g.mx ?? 0,
            my: g.my ?? 0,
            yaw: g.yaw ?? 0,
            pitch: g.pitch ?? 0,
            fire: g.f ?? false,
          },
          K,
        );
        const want = c.trace[i];
        // Reported with the step index, because a divergence almost always
        // starts at one identifiable moment — a doorway, a corner, the frame a
        // cadence runs out — and the first one that differs is the only one
        // worth reading.
        expect(
          Math.hypot(p.x - want.x, p.y - want.y),
          `${c.name} step ${i}: got (${p.x}, ${p.y}) want (${want.x}, ${want.y})`,
        ).toBeLessThan(TOLERANCE);
        expect(p.sector, `${c.name} step ${i}: sector`).toBe(want.s);
        expect(p.loaded, `${c.name} step ${i}: barrels`).toBe(want.b);
        expect(p.ammo, `${c.name} step ${i}: ammunition`).toBe(want.ammo ?? 0);
        expect(
          Math.abs(p.cooldown - (want.cd ?? 0)),
          `${c.name} step ${i}: cooldown got ${p.cooldown} want ${want.cd ?? 0}`,
        ).toBeLessThanOrEqual(EXACT);
        expect(
          Math.abs(p.reload - (want.rl ?? 0)),
          `${c.name} step ${i}: reload got ${p.reload} want ${want.rl ?? 0}`,
        ).toBeLessThanOrEqual(EXACT);
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

  it('carries the trigger through, because this one builds a fresh object', () => {
    // The Go copies a struct and cannot lose a field; this returns a literal and
    // can. A dropped `fire` would be a client that predicted every shot as a
    // refusal while sending the pull anyway — the muzzle flash gone, the shell
    // count still falling, and nothing on screen to explain it.
    expect(sanitise({ dt: 0.025, mx: 0, my: 0, yaw: 0, pitch: 0, fire: true }, K).fire).toBe(true);
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

describe('the gun, where the vectors cannot reach', () => {
  /** The room the gun case is walked around in, with nothing in the way. */
  const level = golden.cases[golden.cases.length - 1].level;
  const standing = { dt: 0.025, mx: 0, my: 0, yaw: 0, pitch: 0 };
  const still = (): StepPlayer => ({
    x: 5,
    y: 5,
    yaw: 0,
    pitch: 0,
    sector: 0,
    loaded: K.barrels,
    cooldown: 0,
    reload: 0,
    ammo: 1,
  });

  it('fires while standing perfectly still', () => {
    // THE ONE CLAIM THE GOLDEN VECTORS CANNOT MAKE, and the reason it is stated
    // separately rather than trusted to them. Every command in the `gun` case is
    // walking, so a port that folded the gun in AFTER `step`'s standing-still
    // return would reproduce the whole transcript and still ship a weapon that
    // only works while its owner is moving — which is the opposite of how
    // anybody shoots at anything.
    const after = step(level, still(), { ...standing, fire: true }, K);
    expect(after.loaded).toBe(K.barrels - 1);
    expect(after.cooldown).toBe(K.fireCooldownSeconds);
  });

  it('cools down while standing perfectly still', () => {
    // The same ordering, seen from the other side: a cadence that only ran while
    // walking would leave somebody who fired and then stopped unable to fire
    // again until he moved.
    const hot: StepPlayer = { ...still(), cooldown: 0.1 };
    expect(step(level, hot, standing, K).cooldown).toBeCloseTo(0.075, 12);
  });

  it('leaves the player it was given alone', () => {
    // `step` is called twice on the same object by construction — the predictor
    // replays every pending command on top of each snapshot it reconciles
    // against — so an in-place decrement would spend a bottle per replay rather
    // than per command, and drain a bag at the round-trip rate.
    const before = still();
    const snapshot = { ...before };
    step(level, before, { ...standing, fire: true }, K);
    expect(before).toEqual(snapshot);
  });
});
