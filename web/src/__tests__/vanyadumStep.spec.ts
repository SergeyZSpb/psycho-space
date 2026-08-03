import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import {
  gunBusy,
  sanitise,
  sectorAt,
  step,
  type StepConstants,
  type StepPlayer,
} from '../lib/vanyadumStep';
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
 * One step of the trace: where he ended up, what state his gun is in, and what
 * is left of him.
 *
 * The gun's two timers are OPTIONAL because the artefact omits them at rest —
 * `cd` and `rl` are absent far more often than they are present, on a file that
 * already lands in every diff touching movement. Absent means zero, and reading
 * it as anything else is how a vacuous pass starts.
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
  /**
   * Seconds of spawn protection left, omitted at zero — which is every step of
   * every case except the one that is about it.
   *
   * It is on the point rather than left to be inferred from a refused trigger so
   * that a port whose arithmetic drifts reports "your protection is wrong"
   * instead of "your gun is wrong".
   */
  pr?: number;
  /** Seconds of ampoule left, on exactly those terms. */
  ij?: number;
  /**
   * What is left of him, and NOT omitted at zero: zero is a man on the floor,
   * which is what one whole case is about, and an absent field cannot say it.
   *
   * IT WAS NOT ON THE POINT AT ALL until the шприц arrived, because `step` could
   * not change health — damage belongs to the world, which the browser does not
   * run. An injection is advanced INSIDE `step` and the health it delivers with
   * it, so the number is now a product of the thing this file pins. Carrying it
   * on the cases that do NOT inject is what asserts that walking, firing and
   * dying still leave it alone.
   */
  hp: number;
}

interface GoldenCase {
  name: string;
  level: VanyadumLevel;
  start: { x: number; y: number };
  sector: number;
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
  /**
   * How much health has already been taken off him when the case begins, and how
   * many seconds of an ampoule are already running.
   *
   * HURT RATHER THAN A HEALTH VALUE, because zero has to mean something and
   * "unhurt" is the only reading that does not collide with `down` above — a
   * `start_health` of 0 would be indistinguishable from an absent field on a man
   * at full health, and would mean the opposite of it.
   */
  start_hurt?: number;
  start_inject?: number;
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
    /**
     * The ampoule, and the cap its heal is clamped to. A port healing by a
     * different amount or over a different time would fail every point of the
     * injection cases, and these are what make the report say which of the three
     * numbers is wrong instead.
     */
    max_health: number;
    syringe_heal: number;
    syringe_seconds: number;
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
  maxHealth: golden.constants.max_health,
  syringeHeal: golden.constants.syringe_heal,
  syringeSeconds: golden.constants.syringe_seconds,
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
    expect(K.maxHealth).toBeGreaterThan(0);
    expect(K.syringeHeal).toBeGreaterThan(0);
    expect(K.syringeSeconds).toBeGreaterThan(0);
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

    // AND IT HAS TO RELOAD MORE THAN ONCE, which is the guard infinite
    // ammunition needs and nothing above provides. A reload is free — the gun
    // fills itself out of empty pockets — so a port that kept any condition on
    // that branch fires the first barrels correctly, starts one reload if it
    // happens to have inherited a state that allows it, and then stands there
    // for ever. One reload in the file would let that ship; two cannot.
    const reloadStarts = golden.cases.flatMap((c) =>
      c.trace.filter((p, i) => (p.rl ?? 0) > 0 && (c.trace[i - 1]?.rl ?? 0) === 0),
    );
    expect(reloadStarts.length).toBeGreaterThanOrEqual(2);
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

  it('has a case about an ampoule, and it runs down and lets him go', () => {
    // THE SAME GUARD AGAIN, for the three rules C3 added — a rooted man, a
    // refused trigger, and health climbing inside `step`. All three are things
    // that DO NOT HAPPEN, so a vector file that never started an injection would
    // let a port ship without any of them and still reproduce every trace in it.
    const injecting = golden.cases.filter((c) => (c.start_inject ?? 0) > 0);
    expect(injecting.length).toBeGreaterThan(0);

    for (const c of injecting) {
      const left = c.trace.map((p) => p.ij ?? 0);
      // It has to RUN OUT inside the transcript, or the tail that proves the
      // root and the refusal end is not in the file at all.
      expect(left[0]).toBeGreaterThan(0);
      expect(left[left.length - 1]).toBe(0);
      // And the health has to MOVE, or the case would pass against a port whose
      // `stepInject` did nothing but count down.
      const health = c.trace.map((p) => p.hp);
      expect(health[health.length - 1]).toBeGreaterThan(health[0]);
    }

    // The overheal case is the one worth naming: it ends at the cap having spent
    // the whole ampoule anyway, which is the rule that makes injecting at nearly
    // full health a bad trade rather than a free top-up.
    const overheal = injecting.find((c) => (c.start_hurt ?? 0) < K.syringeHeal);
    expect(overheal).toBeDefined();
    expect(overheal!.trace[overheal!.trace.length - 1].hp).toBe(K.maxHealth);
  });

  for (const c of golden.cases) {
    it(`reproduces the trace: ${c.name} (${c.trace.length} steps)`, () => {
      let p: StepPlayer = {
        x: c.start.x,
        y: c.start.y,
        yaw: 0,
        pitch: 0,
        sector: c.sector,
        // Whole unless the case says otherwise, and the number now MATTERS: an
        // ampoule adds to it and stops at the cap, so a case that starts at the
        // wrong health overheals or fails to.
        //
        // `max_health` MINUS THE HURT, because that is the only expression the
        // artefact makes available — it carries the cap and not the starting
        // health, and the server pins the two as equal (a man is whole when he
        // walks in, which is also what stops a spawn ever carrying an ampoule and
        // a shield at once).
        health: c.down ? 0 : golden.constants.max_health - (c.start_hurt ?? 0),
        protectedLeft: c.start_protect ?? 0,
        injectLeft: c.start_inject ?? 0,
        // The gun always starts full, exactly as the server's NewPlayer leaves
        // it. The case used to carry a starting ammunition beside this, because
        // a reload could not be exercised by somebody with empty pockets; now it
        // is the only way one ever is.
        loaded: K.barrels,
        cooldown: 0,
        reload: 0,
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
        expect(
          Math.abs(p.injectLeft - (want.ij ?? 0)),
          `${c.name} step ${i}: ampoule got ${p.injectLeft} want ${want.ij ?? 0}`,
        ).toBeLessThanOrEqual(EXACT);
        // EXACT, and it is an integer either side: the health an ampoule delivers
        // is `Math.round` of a ratio of served constants, so a port that rounded
        // differently — or accumulated instead of deriving — drifts by a hit
        // point every few frames and this is what catches it.
        expect(p.health, `${c.name} step ${i}: health`).toBe(want.hp);
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
    injectLeft: 0,
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
    // обрез and none of the second and a half everybody else pays for it.
    const guarded: StepPlayer = { ...still(), protectedLeft: 1, loaded: 0 };
    const after = step(level, guarded, { ...standing, fire: true }, K);
    expect(after.reload).toBe(0);
    expect(after.loaded).toBe(0);
  });

  it('reloads an empty gun out of nothing, every time it is asked', () => {
    // THE WHOLE OF THE RULE THAT REPLACED THE PRICE. A reload costs the time and
    // nothing else, so the trigger's answer to an empty обрез does not depend on
    // anything a player is carrying — and it is the same answer the second time,
    // and the tenth. The golden vectors' `gun` case pins the sequence; this
    // states the claim on its own, because a port that kept a condition here is
    // a gun that stops for good after the shots it spawned with.
    let p: StepPlayer = { ...still(), loaded: 0 };
    for (let reloads = 0; reloads < 3; reloads++) {
      p = step(level, p, { ...standing, fire: true }, K);
      expect(p.reload).toBe(K.reloadSeconds);
      // Run it out, which is the only thing that fills the gun.
      for (let i = 0; i < 200 && p.reload > 0; i++) p = step(level, p, standing, K);
      expect(p.loaded).toBe(K.barrels);
      // And empty it again the honest way, through the cadence.
      for (let barrel = 0; barrel < K.barrels; barrel++) {
        p = step(level, p, { ...standing, fire: true }, K);
        for (let i = 0; i < 200 && p.cooldown > 0; i++) p = step(level, p, standing, K);
      }
      expect(p.loaded).toBe(0);
    }
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
    // against — so a step that wrote through to its argument would apply each
    // command once per replay rather than once per command.
    const before = still();
    const snapshot = { ...before };
    step(level, before, { ...standing, fire: true }, K);
    expect(before).toEqual(snapshot);
  });
});

describe('the шприц, where the vectors cannot reach', () => {
  /** The room the injection cases are run in, with nothing in the way. */
  const level = golden.cases[golden.cases.length - 1].level;
  const walking = { dt: 0.025, mx: 0, my: 1, yaw: 0, pitch: 0 };
  /** A man halfway through an ampoule, with a full gun and somewhere to walk. */
  const injecting = (over: Partial<StepPlayer> = {}): StepPlayer => ({
    x: 20,
    y: 20,
    yaw: 0,
    pitch: 0,
    sector: 0,
    health: 40,
    protectedLeft: 0,
    loaded: K.barrels,
    cooldown: 0,
    reload: 0,
    injectLeft: K.syringeSeconds,
    ...over,
  });

  it('roots him where he stands, however hard the stick is held', () => {
    // THE CLAIM THE GOLDEN VECTORS MAKE AND THIS ONE NAMES. It is stated
    // separately because it is the refusal the CAMERA depends on: a client that
    // kept walking would draw a man leaving a room the server has him standing
    // in, and would then be corrected twenty times a second for the whole
    // injection.
    const after = step(level, injecting(), { ...walking, mx: 1 }, K);
    expect(after.x).toBe(20);
    expect(after.y).toBe(20);
  });

  it('still lets him look around, because the camera is the client’s', () => {
    // The angles land even while the feet do not. A first-person game that takes
    // the view away from somebody is worse than one that takes his feet — and it
    // is also how he watches whatever is walking towards him.
    const after = step(level, injecting(), { ...walking, yaw: 1.1, pitch: -0.4 }, K);
    expect(after.yaw).toBeCloseTo(1.1, 12);
    expect(after.pitch).toBeCloseTo(-0.4, 12);
  });

  it('refuses the trigger and the reload for the whole of it', () => {
    // THE INJECTION WINS, ALWAYS. The kind rule — a trigger pull that cancels the
    // needle — is the rule that deletes the feature, because a window you can
    // leave by tapping the thing you are already holding is not a window. This is
    // predicted rather than merely drawn precisely so the browser refuses the
    // muzzle flash in the same frame as the thumb.
    const after = step(level, injecting(), { ...walking, fire: true }, K);
    expect(after.loaded).toBe(K.barrels);
    expect(after.cooldown).toBe(0);

    const empty = step(level, injecting({ loaded: 0 }), { ...walking, fire: true }, K);
    expect(empty.reload).toBe(0);
    expect(empty.loaded).toBe(0);
  });

  it('gives him the step it ends on, rather than one more of standing still', () => {
    // The same rule the server applies to a man getting up off the floor: the
    // countdown is advanced and THEN the refusals are read, so the step an
    // injection finishes on is a step he can move and shoot in. At 20 Hz the
    // other ordering is a whole tick of a game that ignores you for no reason a
    // player can see.
    const last = step(level, injecting({ injectLeft: 0.02 }), { ...walking, fire: true }, K);
    expect(last.injectLeft).toBe(0);
    expect(last.y).toBeGreaterThan(20);
    expect(last.loaded).toBe(K.barrels - 1);
  });

  it('delivers exactly one ampoule, and not a hit point more', () => {
    let p = injecting({ health: K.maxHealth - K.syringeHeal });
    for (let i = 0; i < 200 && p.injectLeft > 0; i++) p = step(level, p, walking, K);
    expect(p.injectLeft).toBe(0);
    expect(p.health).toBe(K.maxHealth);
  });

  it('does not overheal, and spends the time anyway', () => {
    // The cap is applied to the GRANT and not to the ampoule, so a man who fills
    // up a fifth of the way through is still rooted for the rest of it. That is
    // what makes injecting at nearly full health a bad trade rather than a free
    // top-up.
    let p = injecting({ health: K.maxHealth - 1 });
    const half = step(level, p, walking, K);
    expect(half.injectLeft).toBeLessThan(K.syringeSeconds);
    for (let i = 0; i < 200 && p.injectLeft > 0; i++) p = step(level, p, walking, K);
    expect(p.health).toBe(K.maxHealth);
  });

  it('replaying a command advances the ampoule again, which is why the base is the frame', () => {
    // ADR-058's property, stated from this side of the port. `injectLeft` is
    // DECREMENTED rather than replaced, so applying one command to one state
    // twice moves it twice — which is exactly what the predictor does on every
    // reconcile, and exactly why its replay base has to be the snapshot's number
    // rather than this client's own.
    const start = injecting();
    const once = step(level, start, walking, K);
    const twice = step(level, once, walking, K);
    expect(twice.injectLeft).toBeCloseTo(K.syringeSeconds - 2 * walking.dt, 12);

    // AND THE HEALTH IS DERIVED FROM THE CLOCK RATHER THAN ACCUMULATED, which is
    // what makes that survivable: two routes to the same remaining time land on
    // the same health, so a player replayed from a snapshot arrives where the
    // server is instead of on a sum of his own history.
    let long = start;
    long = step(level, long, { ...walking, dt: 2 * walking.dt }, K);
    expect(long.injectLeft).toBeCloseTo(twice.injectLeft, 12);
    expect(long.health).toBe(twice.health);
  });

  it('runs none of it for a man on the floor', () => {
    // A corpse does nothing at all, and that includes mending. The world clears
    // the ampoule when it kills him; this is the port refusing to run one that
    // somehow survived, so the two ends cannot disagree about a dead man.
    const dead = injecting({ health: 0 });
    const after = step(level, dead, { ...walking, fire: true }, K);
    expect(after.injectLeft).toBe(K.syringeSeconds);
    expect(after.health).toBe(0);
  });
});

describe('gunBusy — the one refusal three callers ask about', () => {
  /**
   * A man who could fire this instant. Every case below names only the field it
   * is making a claim about.
   */
  const ready = {
    health: 100,
    protectedLeft: 0,
    cooldown: 0,
    reload: 0,
    injectLeft: 0,
  };

  it('is false for a man who could fire', () => {
    expect(gunBusy(ready)).toBe(false);
  });

  it('is true for every reason the trigger is ever refused', () => {
    // All five, named, because the view draws the trigger control from this and
    // a refusal it did not know about would be a button that says «ready» while
    // the gun says no. The ampoule is the one most recently added, and the one a
    // sixth check would most likely forget.
    expect(gunBusy({ ...ready, health: 0 })).toBe(true);
    expect(gunBusy({ ...ready, cooldown: 0.2 })).toBe(true);
    expect(gunBusy({ ...ready, reload: 1.5 })).toBe(true);
    expect(gunBusy({ ...ready, protectedLeft: 3 })).toBe(true);
    expect(gunBusy({ ...ready, injectLeft: 2.5 })).toBe(true);
  });

  it('does not call an empty gun busy', () => {
    // An empty обрез is a reload waiting to be started rather than a refusal:
    // the trigger is the only thing that begins one, and it costs nothing, so a
    // pull on an empty gun is granted and the trigger control stays lit for it.
    const empty: StepPlayer = {
      x: 5,
      y: 5,
      yaw: 0,
      pitch: 0,
      sector: 0,
      ...ready,
      loaded: 0,
    };
    expect(gunBusy(empty)).toBe(false);
    // And it IS granted, which is the half `gunBusy` alone cannot say.
    const level = golden.cases[golden.cases.length - 1].level;
    const after = step(level, empty, { dt: 0.025, mx: 0, my: 0, yaw: 0, pitch: 0, fire: true }, K);
    expect(after.reload).toBe(K.reloadSeconds);
  });

  it('is exactly what step refuses on, which is why it is shared', () => {
    // The property that makes one predicate honest rather than convenient: the
    // simulation runs it, so the view's suppression and the view's button state
    // cannot drift from the rule both ends actually apply.
    const level = golden.cases[golden.cases.length - 1].level;
    const base: StepPlayer = {
      x: 5,
      y: 5,
      yaw: 0,
      pitch: 0,
      sector: 0,
      loaded: K.barrels,
      ...ready,
    };
    const pull = { dt: 0.025, mx: 0, my: 0, yaw: 0, pitch: 0, fire: true };
    for (const over of [
      { cooldown: 0.2 },
      { reload: 1.5 },
      { protectedLeft: 3 },
      { injectLeft: 2.5 },
    ]) {
      const p: StepPlayer = { ...base, ...over };
      expect(gunBusy(p)).toBe(true);
      // Refused: the barrels are exactly where they were.
      expect(step(level, p, pull, K).loaded).toBe(K.barrels);
    }
    expect(step(level, base, pull, K).loaded).toBe(K.barrels - 1);
  });
});
