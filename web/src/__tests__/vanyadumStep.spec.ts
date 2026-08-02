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
  /**
   * Seconds of spawn protection left, omitted at zero — which is every step of
   * every case except the one that is about it.
   *
   * It is on the point rather than left to be inferred from a refused trigger so
   * that a port whose arithmetic drifts reports "your protection is wrong"
   * instead of "your gun is wrong".
   */
  pr?: number;
}

interface GoldenCase {
  name: string;
  level: VanyadumLevel;
  start: { x: number; y: number };
  sector: number;
  /** How much ammunition the player begins with; absent means none. */
  start_ammo?: number;
  /**
   * `down` starts the case with the player on the floor, and `start_protect`
   * with that many seconds of spawn protection.
   *
   * A BOOL RATHER THAN A HEALTH VALUE, because zero is the interesting state and
   * an omitted number would have to mean the opposite of what it says. What the
   * port does with it is set health to nothing; what happens next is the whole
   * of the case.
   */
  down?: boolean;
  start_protect?: number;
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

  it('has a case about being dead and a case about being untouchable', () => {
    // THE SAME GUARD, FOR THE TWO RULES C1b ADDED. Both are refusals — a dead
    // man does not move, a protected one does not fire — so a vector file that
    // exercised neither would let a port ship without either and still reproduce
    // every trace in it. The transcripts have to REACH the states, not merely
    // declare them: a protection that never runs down is a countdown nobody
    // ported.
    expect(golden.cases.some((c) => c.down)).toBe(true);
    const protectedCase = golden.cases.find((c) => (c.start_protect ?? 0) > 0);
    expect(protectedCase).toBeDefined();
    const protection = protectedCase!.trace.map((p) => p.pr ?? 0);
    expect(protection[0]).toBeGreaterThan(0);
    expect(protection[protection.length - 1]).toBe(0);
  });

  for (const c of golden.cases) {
    it(`reproduces the trace: ${c.name} (${c.trace.length} steps)`, () => {
      let p: StepPlayer = {
        x: c.start.x,
        y: c.start.y,
        yaw: 0,
        pitch: 0,
        sector: c.sector,
        // Alive unless the case is about being dead, and the health value itself
        // does not matter beyond that: `step` only ever asks whether it is at or
        // below zero, so the vectors carry no health column at all.
        health: c.down ? 0 : 100,
        protectedLeft: c.start_protect ?? 0,
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
        expect(
          Math.abs(p.protectedLeft - (want.pr ?? 0)),
          `${c.name} step ${i}: protection got ${p.protectedLeft} want ${want.pr ?? 0}`,
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
    health: 100,
    protectedLeft: 0,
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

  it('refuses the trigger while spawn protection is running', () => {
    // BOTH HALVES OR IT IS A WEAPON. Protection that you can fire from hands the
    // spawn to whoever died last, which is worse than the grief it was fixing —
    // so the browser runs the same refusal, and it has to, because it is what
    // decides whether a muzzle flash is drawn the instant a thumb lands.
    const guarded: StepPlayer = { ...still(), protectedLeft: 1 };
    const after = step(level, guarded, { ...standing, fire: true }, K);
    expect(after.loaded).toBe(K.barrels);
    expect(after.cooldown).toBe(0);
    // And it counts down while he stands perfectly still, which is the state
    // somebody who has just got up and is looking around is in.
    expect(after.protectedLeft).toBeCloseTo(1 - standing.dt, 12);
  });

  it('refuses the reload too, so the window is not spent filling the gun', () => {
    // A protected man who could reload would come out of the window with a full
    // обрез he never had to walk for.
    const guarded: StepPlayer = { ...still(), protectedLeft: 1, loaded: 0, ammo: 2 };
    const after = step(level, guarded, { ...standing, fire: true }, K);
    expect(after.reload).toBe(0);
    expect(after.ammo).toBe(2);
  });

  it('lets a man on the floor look around and do nothing else', () => {
    // THE RULE LIVES IN `step` AND NOT IN THE WORLD, and this port is why: a
    // server that quietly ignored a dead player's commands while the browser
    // went on applying them would drag his corpse down the corridor and correct
    // it twenty times a second. The angles are still his, because a death you
    // cannot look around from is a black screen with a timer on it.
    const dead: StepPlayer = { ...still(), health: 0, cooldown: 0.2, protectedLeft: 0.4 };
    const after = step(level, dead, { dt: 0.025, mx: 0, my: 1, yaw: 1.2, pitch: -0.3, fire: true }, K);
    expect(after.x).toBe(dead.x);
    expect(after.y).toBe(dead.y);
    expect(after.yaw).toBeCloseTo(1.2, 12);
    expect(after.pitch).toBeCloseTo(-0.3, 12);
    // Nothing ran: not the cadence, not the protection, not the trigger.
    expect(after.loaded).toBe(K.barrels);
    expect(after.cooldown).toBe(0.2);
    expect(after.protectedLeft).toBe(0.4);
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
