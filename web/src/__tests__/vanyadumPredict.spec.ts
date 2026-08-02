import { describe, expect, it } from 'vitest';
import {
  CORRECTION_SECONDS,
  SILENT_CORRECTION,
  SNAP_CORRECTION,
  createPredictor,
  type Authoritative,
} from '../lib/vanyadumPredict';
import type { VanyadumAxes } from '../lib/vanyadumInput';
import type { StepConstants } from '../lib/vanyadumStep';
import type { VanyadumLevel } from '../lib/vanyadumLevel';

/**
 * Client-side prediction and server reconciliation.
 *
 * These are the tests that say prediction is a *rendering* technique and not a
 * transfer of authority: every one of them ends with the client agreeing with
 * the server, and the interesting question is only how it got there.
 */

const K: StepConstants = {
  walkSpeed: 5,
  radius: 0.35,
  maxStep: 0.6,
  maxPitch: 1.5,
  maxStepSeconds: 0.2,
  collisionPasses: 3,
  barrels: 2,
  fireCooldownSeconds: 0.35,
  reloadSeconds: 1.5,
  reloadCost: 1,
  maxHealth: 100,
  syringeHeal: 50,
  syringeSeconds: 2.5,
};

/**
 * A snapshot that says nothing has changed but the position.
 *
 * Every reconcile below spreads over this rather than writing the gun out, so
 * the tests that are about walking stay about walking — and the ones that are
 * about the gun name only the field they are making a claim about.
 */
function snap(over: Partial<Authoritative> & { ack: number }): Authoritative {
  return {
    x: 50,
    y: 50,
    sector: 0,
    health: 100,
    loaded: K.barrels,
    cooldown: 0,
    reload: 0,
    protect: 0,
    ammo: 0,
    inject: 0,
    ...over,
  };
}

/** One big empty room, so nothing below is about collision. */
const ROOM: VanyadumLevel = {
  seed: 1,
  sectors: [
    { id: 0, x0: 0, y0: 0, x1: 100, y1: 100, fz: 0, cz: 3, w: 'c', f: 'f', c: 'c', l: 1 },
  ],
  portals: [],
  walls: [],
  pickups: [],
  spawn: { x: 50, y: 50 },
  spawn_sector: 0,
  spawn_yaw: 0,
};

function predictor() {
  return createPredictor({
    level: ROOM,
    constants: K,
    eyeHeight: 1.65,
    start: { x: 50, y: 50, sector: 0, yaw: 0, health: 100 },
  });
}

const walk = { dt: 0.025, mx: 0, my: 1, yaw: 0, pitch: 0 };

/**
 * The axes a frame is drawn with.
 *
 * `view` takes them because a carry has to be stepped with the same axes the
 * command it anticipates will be built from. With nothing to carry they are
 * never read, so every call below that only asks "where is he being drawn now"
 * passes a carry of zero and `STILL`.
 */
const STILL: VanyadumAxes = { mx: 0, my: 0, yaw: 0, pitch: 0 };
const WALKING: VanyadumAxes = { mx: 0, my: 1, yaw: 0, pitch: 0 };

describe('prediction', () => {
  it('moves the instant a command is applied, with no server in sight', () => {
    // The whole point: iteration 1 waited a round trip and then twenty hertz.
    const p = predictor();
    const before = p.view(0, STILL).y;
    p.apply(walk);
    expect(p.view(0, STILL).y).toBeGreaterThan(before);
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

  it('computes the eye height from the sector, not from a snapshot', () => {
    // Otherwise a camera that only rose twenty times a second would make every
    // step visibly jolt.
    expect(predictor().view(0, STILL).z).toBeCloseTo(1.65, 9);
  });
});

describe('reconciliation', () => {
  it('drops acknowledged commands and keeps the rest', () => {
    const p = predictor();
    for (let i = 0; i < 5; i++) p.apply(walk);
    p.reconcile(snap({ x: 50, y: 50.25, ack: 3 }));
    expect(p.pendingCount()).toBe(2);
  });

  it('replays what is still pending on top of the authoritative position', () => {
    // The half that is easy to omit. Without the replay the client snaps back
    // to where the server was a round trip ago and re-runs forward next frame,
    // which is the classic rubber-band.
    const p = predictor();
    for (let i = 0; i < 4; i++) p.apply(walk);
    // The server has only seen the first two, so it is two steps behind.
    p.reconcile(snap({ x: 50, y: 50 + 2 * walk.dt * K.walkSpeed, ack: 2 }));
    // Four steps of movement must still be visible, not two.
    expect(p.raw().y).toBeCloseTo(50 + 4 * walk.dt * K.walkSpeed, 6);
  });

  it('lands exactly where the server says once everything is acknowledged', () => {
    const p = predictor();
    for (let i = 0; i < 6; i++) p.apply(walk);
    p.reconcile(snap({ x: 42, y: 17, ack: 6 }));
    expect(p.raw().x).toBeCloseTo(42, 9);
    expect(p.raw().y).toBeCloseTo(17, 9);
  });

  it('applies a tiny disagreement silently, so the camera never shivers', () => {
    // Quantisation to the centimetre and floating-point drift mean the two ends
    // are never bit-identical. Without a floor, every snapshot would start a
    // correction and the view would tremble permanently.
    const p = predictor();
    p.apply(walk);
    const y = p.raw().y;
    p.reconcile(snap({ x: 50, y: y + SILENT_CORRECTION / 2, ack: 1 }));
    expect(p.correction()).toBe(0);
  });

  it('eases a visible disagreement out instead of jumping', () => {
    const p = predictor();
    p.apply(walk);
    const drawn = p.view(0, STILL);
    p.reconcile(snap({ x: 50, y: p.raw().y + 0.5, ack: 1 }));
    // Still drawn where it was, not teleported to the new truth.
    expect(p.view(0, STILL).y).toBeCloseTo(drawn.y, 6);
    expect(p.correction()).toBeGreaterThan(0);
  });

  it('and the correction actually goes away', () => {
    const p = predictor();
    p.apply(walk);
    p.reconcile(snap({ x: 50, y: p.raw().y + 0.5, ack: 1 }));
    // Exponential decay never reaches zero by arithmetic, so it is cut off at
    // a tenth of a millimetre. A second and a bit of ticks is comfortably past
    // that; the point of the assertion is that it ENDS, not how fast.
    for (let i = 0; i < 150; i++) p.tick(CORRECTION_SECONDS / 10);
    expect(p.correction()).toBe(0);
    expect(p.view(0, STILL).y).toBeCloseTo(p.raw().y, 9);
  });

  it('snaps a disagreement too large to glide', () => {
    // Beyond a couple of metres the client is not slightly wrong, it is
    // somewhere else — a refused move, a resync — and sliding a player across a
    // room to arrive at the truth is slower and stranger than putting them there.
    const p = predictor();
    p.apply(walk);
    p.reconcile(snap({ x: 50, y: p.raw().y + SNAP_CORRECTION * 2, ack: 1 }));
    expect(p.correction()).toBe(0);
  });

  it('never argues with the server, however wrong the server looks', () => {
    // The claim that matters: prediction is a rendering technique, not
    // authority. Whatever the client thought, the server's answer is where it
    // ends up.
    const p = predictor();
    for (let i = 0; i < 20; i++) p.apply(walk);
    p.reconcile(snap({ x: 1, y: 1, ack: 20 }));
    for (let i = 0; i < 60; i++) p.tick(0.016);
    expect(p.view(0, STILL).x).toBeCloseTo(1, 6);
    expect(p.view(0, STILL).y).toBeCloseTo(1, 6);
  });
});

describe('the gun, which is the first thing here that is decremented rather than replaced', () => {
  /** Walking forward with the trigger pulled. */
  const shoot = { ...walk, fire: true };

  it('predicts a shot the instant the trigger is pulled', () => {
    // The whole reason the gun is predicted at all: the muzzle flash is drawn
    // from this, so it has to be true before any snapshot has been anywhere.
    const p = predictor();
    p.apply(shoot);
    expect(p.raw().loaded).toBe(K.barrels - 1);
    expect(p.raw().cooldown).toBe(K.fireCooldownSeconds);
  });

  it('refuses a second pull inside the cadence, exactly as the server will', () => {
    // A refusal predicted correctly is what stops a held trigger drawing a flash
    // per frame for shots that never happened.
    const p = predictor();
    p.apply(shoot);
    p.apply(shoot);
    expect(p.raw().loaded).toBe(K.barrels - 1);
  });

  it('does not decrement a running cooldown twice when a snapshot lands', () => {
    // THE DEFECT THIS ITERATION EXISTS TO PREVENT, and it could not have been
    // written before it: until the обрез there was no field here that a replay
    // could apply twice, because everything was overwritten by the snapshot or
    // by the thumb. A cadence is DECREMENTED, so a base that already contained
    // the pending commands would take each command's dt off the clock a second
    // time — a gun that cooled at twice real speed for as long as anything was
    // in flight.
    const p = predictor();
    p.apply(shoot);
    // Three more commands the server has not seen: the trigger is still down and
    // every one of them is refused by the cadence, which is the ordinary case.
    for (let i = 0; i < 3; i++) p.apply(shoot);
    expect(p.pendingCount()).toBe(4);

    // The server has folded in the shot and nothing after it, so its cadence
    // still reads full.
    p.reconcile(snap({ y: p.raw().y, ack: 1, loaded: K.barrels - 1, cooldown: K.fireCooldownSeconds }));

    // Three replays, three decrements. Six would be the bug.
    expect(p.raw().cooldown).toBeCloseTo(K.fireCooldownSeconds - 3 * walk.dt, 12);
    expect(p.raw().loaded).toBe(K.barrels - 1);
  });

  it('adopts the server’s cadence rather than the one it stopped running', () => {
    // ADR-058's sharp edge, and this game has it worse than the office does. A
    // predicted timer only advances inside `apply`, and `apply` only runs when a
    // command is emitted — so a player who has fired and is standing perfectly
    // still to aim emits nothing and his local cadence stops dead. The server
    // keeps it running through those ticks, which is why the base is the
    // snapshot's gun and not this client's memory of it: without that he taps,
    // the browser refuses, and no flash is drawn for a shell that was spent.
    const p = predictor();
    p.apply(shoot);
    expect(p.raw().cooldown).toBe(K.fireCooldownSeconds);
    // Three hundred milliseconds later on the server's clock, with nothing sent.
    p.reconcile(snap({ y: p.raw().y, ack: 1, loaded: K.barrels - 1, cooldown: 0.05 }));
    expect(p.raw().cooldown).toBeCloseTo(0.05, 12);
  });

  it('takes the ammunition from the snapshot, because it never sees a pickup', () => {
    // Walking over a bottle is the server's to decide and this client predicts
    // none of it, so a locally held count would only ever fall — and an empty
    // gun would refuse the reload the server was granting.
    const p = predictor();
    expect(p.raw().ammo).toBe(0);
    p.reconcile(snap({ ack: 0, ammo: 3 }));
    expect(p.raw().ammo).toBe(3);
  });

  it('spends one bottle per reload however many times the command is replayed', () => {
    // The other half of the same hazard. A reload spends ammunition, and a
    // replay that spent it again would drain a bag at the round-trip rate.
    const p = predictor();
    // An empty gun with something to load it: the pull starts a reload.
    p.reconcile(snap({ ack: 0, loaded: 0, ammo: 2 }));
    p.apply(shoot);
    expect(p.raw().reload).toBe(K.reloadSeconds);
    expect(p.raw().ammo).toBe(1);

    // The same command, still unacknowledged, replayed on top of a snapshot
    // taken before it.
    p.reconcile(snap({ y: p.raw().y, ack: 0, loaded: 0, ammo: 2 }));
    expect(p.raw().ammo).toBe(1);
    expect(p.raw().reload).toBe(K.reloadSeconds);
  });

  it('is corrected by the server when it predicted a shot that never happened', () => {
    // Prediction is a rendering technique here exactly as it is for the
    // position: the browser may draw a flash the server then refuses, and the
    // next frame is simply the truth again.
    const p = predictor();
    p.apply(shoot);
    expect(p.raw().loaded).toBe(K.barrels - 1);
    p.reconcile(snap({ ack: 1, loaded: K.barrels }));
    expect(p.raw().loaded).toBe(K.barrels);
  });
});

describe('being killed, and coming back', () => {
  const shoot = { ...walk, fire: true };

  it('stops walking a man the snapshot says is on the floor', () => {
    // HEALTH IS NOT PREDICTED AND IS STILL ON THE REPLAY BASE, and this is what
    // that buys. A player is shot while walking; the pending commands are
    // replayed on top of the frame that says so, and `step` refuses every one of
    // them — so the browser stops him in the same frame the server did, instead
    // of walking his corpse down the corridor for a round trip and then snapping
    // it back.
    const p = predictor();
    for (let i = 0; i < 4; i++) p.apply(walk);
    const where = p.raw();
    p.reconcile(snap({ x: where.x, y: where.y, ack: 0, health: 0, loaded: 0 }));
    // Four commands still pending, every one of them a step forward, and he has
    // not moved a centimetre.
    expect(p.pendingCount()).toBe(4);
    expect(p.raw().y).toBeCloseTo(where.y, 9);
  });

  it('refuses the trigger while he is down, so no flash is drawn for a corpse', () => {
    const p = predictor();
    p.reconcile(snap({ ack: 0, health: 0 }));
    p.apply(shoot);
    expect(p.raw().loaded).toBe(K.barrels);
    expect(p.raw().cooldown).toBe(0);
  });

  it('refuses it through spawn protection too, and counts the protection down', () => {
    // The muzzle flash is drawn from this prediction, so a client that did not
    // run the refusal would light the barrels for every shot the server threw
    // away — during exactly the two seconds a player is most likely to be
    // holding the trigger, having just been killed.
    const p = predictor();
    p.reconcile(snap({ ack: 0, protect: 2 }));
    p.apply(shoot);
    expect(p.raw().loaded).toBe(K.barrels);
    expect(p.raw().protectedLeft).toBeCloseTo(2 - walk.dt, 12);
  });

  it('fires the moment the protection has run out', () => {
    // And it must not run out LATE, or the first shot of a fight is eaten. The
    // predictor counts it down through its own commands, exactly as it does the
    // cadence.
    const p = predictor();
    p.reconcile(snap({ ack: 0, protect: 2 * walk.dt }));
    p.apply(shoot);
    p.apply(shoot);
    expect(p.raw().protectedLeft).toBe(0);
    p.apply(shoot);
    expect(p.raw().loaded).toBe(K.barrels - 1);
  });

  it('does not decrement the protection twice when a snapshot lands', () => {
    // ADR-058'S SHARP EDGE, FOR THE THIRD COUNTDOWN ON THIS TYPE. Take the base
    // from the client's own predicted player and every pending command takes its
    // dt off the clock a second time — so a walking player would come out of his
    // protection at twice real speed, and be killable while his own screen still
    // said he was safe.
    const p = predictor();
    p.reconcile(snap({ ack: 0, protect: 2 }));
    for (let i = 0; i < 3; i++) p.apply(walk);
    // The server has folded in the first of those three and says so.
    p.reconcile(snap({ y: p.raw().y, ack: 1, protect: 2 - walk.dt }));
    // Two still pending, so exactly three commands' worth is off the clock.
    expect(p.raw().protectedLeft).toBeCloseTo(2 - 3 * walk.dt, 12);
  });

  it('takes the server’s word for coming back, including where', () => {
    // A respawn is a teleport to the building's spawn with a full gun, and
    // nothing about it is the client's to predict — it is simply the next
    // snapshot, and it wins.
    const p = predictor();
    p.reconcile(snap({ ack: 0, health: 0, loaded: 0 }));
    p.reconcile(snap({ x: 50, y: 50, ack: 0, health: 100, loaded: K.barrels, protect: 2 }));
    const back = p.raw();
    expect(back.health).toBe(100);
    expect(back.loaded).toBe(K.barrels);
    expect(back.protectedLeft).toBe(2);
    // And he can walk again, on the very next command.
    p.apply(walk);
    expect(p.raw().y).toBeGreaterThan(50);
  });
});

describe('the ampoule, which the frame both starts and re-bases', () => {
  // THE ONE FIELD ON THIS TYPE THE CALLER HAS TO DISCRIMINATE. The wire spends
  // `dn` on the respawn countdown AND on the ampoule, with `hp` saying which — so
  // by the time it reaches `Authoritative.inject` the split has already been made
  // (GameVanyadumView, applySnapshot). These tests are about what happens after
  // it: a number that is DECREMENTED rather than replaced, replayed against.

  it('is taken from the frame, so the client never starts one for itself', () => {
    // Walking over a шприц is the server's to decide, exactly as a bottle is —
    // the client predicts no pickups at all. What it predicts is the countdown
    // that follows, because the refusals hang off it.
    const p = predictor();
    p.apply(walk);
    expect(p.raw().injectLeft).toBe(0);
    p.reconcile(snap({ ack: 1, health: 40, inject: 2.5 }));
    expect(p.raw().injectLeft).toBe(2.5);
  });

  it('counts down through this client’s own commands, and roots it while it does', () => {
    const p = predictor();
    p.reconcile(snap({ ack: 0, health: 40, inject: 1 }));
    const before = p.raw().y;
    p.apply(walk);
    expect(p.raw().injectLeft).toBeCloseTo(1 - walk.dt, 12);
    // And he has not moved, however much walking was asked for.
    expect(p.raw().y).toBe(before);
  });

  it('re-bases on every frame, so a replay cannot spend it twice', () => {
    // ADR-058's sharp edge, and the reason this is the base rather than the
    // client's own memory. Two commands are pending when the frame lands; the
    // server folded neither in, so the replay has to take exactly their dt off
    // the SERVER'S number — and a base taken from `predicted` would take it off a
    // value that already contained them, bringing him out of the injection early
    // and drawing him walking while the server still has him rooted.
    const p = predictor();
    p.reconcile(snap({ ack: 0, health: 40, inject: 2 }));
    p.apply(walk);
    p.apply(walk);
    expect(p.raw().injectLeft).toBeCloseTo(2 - 2 * walk.dt, 12);

    p.reconcile(snap({ ack: 0, health: 40, inject: 2 }));
    expect(p.raw().injectLeft).toBeCloseTo(2 - 2 * walk.dt, 12);
  });

  it('the server can end it early, and the client walks on the next command', () => {
    // Being shot is what takes the needle out, and that is a correction like any
    // other: the frame simply stops carrying the ampoule. Nothing here has to
    // know WHY — the base is the frame, so the client is walking again on the
    // very next command it applies.
    const p = predictor();
    p.reconcile(snap({ ack: 0, health: 40, inject: 2 }));
    p.apply(walk);
    expect(p.raw().y).toBe(50);

    p.reconcile(snap({ ack: 1, health: 20, inject: 0 }));
    p.apply(walk);
    expect(p.raw().injectLeft).toBe(0);
    expect(p.raw().y).toBeGreaterThan(50);
  });

  it('lands on the server’s health rather than on a sum of its own', () => {
    // The health an ampoule delivers is DERIVED from the countdown on both ends,
    // so replaying pending commands on top of a frame re-derives exactly what the
    // server derived. An accumulator would drift by a hit point per replay, and
    // the HUD reads that number.
    const p = predictor();
    p.reconcile(snap({ ack: 0, health: 50, inject: K.syringeSeconds }));
    for (let i = 0; i < 4; i++) p.apply(walk);
    const replayed = p.raw();

    // The same remaining time reached in one command instead of four.
    const q = predictor();
    q.reconcile(snap({ ack: 0, health: 50, inject: K.syringeSeconds }));
    q.apply({ ...walk, dt: 4 * walk.dt });
    expect(q.raw().injectLeft).toBeCloseTo(replayed.injectLeft, 12);
    expect(q.raw().health).toBe(replayed.health);
  });
});

describe('a predictor rebuilt under an occupant that is still standing there', () => {
  // The defect this pins was a permanent freeze, not a glitch. A rebuilt
  // predictor counts from one, the server has long since accepted far higher
  // sequences FROM THAT SAME OCCUPANT, so it drops command 1 as stale and then
  // every command that follows it — with the socket up, the snapshots arriving
  // and the HUD alive, so nothing on screen says why the player has stopped
  // moving. And the occupant outlives the predictor by design: a reload inside
  // the abandon grace comes back to it, and so does the заброшка being
  // regenerated, which rebuilds the predictor against new geometry without the
  // socket ever dropping.

  it('counts on from the ack, so its first command is not dropped as stale', () => {
    const p = predictor();
    p.reconcile(snap({ x: 50, y: 50, ack: 400 }));
    expect(p.apply(walk).seq).toBeGreaterThan(400);
  });

  it('but never moves the count when the ack is one of its own', () => {
    // The floor is one-directional on purpose. Rewinding onto sequences that are
    // still pending would put two different commands under one number, and
    // skipping ahead on an ack it has already passed would be the client
    // inventing a number rather than agreeing with the server's.
    const p = predictor();
    p.apply(walk);
    p.apply(walk);
    p.apply(walk);
    // Below the count: three sent, one folded in so far.
    p.reconcile(snap({ x: 50, y: p.raw().y, ack: 1 }));
    expect(p.apply(walk).seq).toBe(4);
    // Level with it: everything sent has been folded in, and the next command is
    // still simply the next one.
    p.reconcile(snap({ x: 50, y: p.raw().y, ack: 4 }));
    expect(p.apply(walk).seq).toBe(5);
  });
});

describe('input redundancy', () => {
  it('offers the tail of what is unacknowledged', () => {
    const p = predictor();
    for (let i = 0; i < 10; i++) p.apply(walk);
    const tail = p.unacknowledged(3);
    expect(tail.map((c) => c.seq)).toEqual([8, 9, 10]);
  });

  it('offers nothing once everything has been acknowledged', () => {
    const p = predictor();
    for (let i = 0; i < 4; i++) p.apply(walk);
    p.reconcile(snap({ x: 50, y: p.raw().y, ack: 4 }));
    expect(p.unacknowledged(6)).toEqual([]);
  });

  it('offers nothing when asked for nothing', () => {
    const p = predictor();
    p.apply(walk);
    expect(p.unacknowledged(0)).toEqual([]);
  });
});

describe('the drawn eye advances every frame, not every command', () => {
  // Regression. `predicted` only moves inside apply(), which happens forty
  // times a second, while the screen refreshes at sixty to a hundred and
  // forty-four — so drawing the prediction alone held the camera still for one
  // to three frames and then jumped it a whole sub-step, 12.5 cm at a walk.
  // Turning was smooth throughout, because look() already ran every frame, and
  // that asymmetry is how the fault announces itself.
  /** Where one command of walking forward from the spawn lands. */
  const LANDING = 50 + K.walkSpeed * walk.dt;
  /** How far one sub-step of walking goes — what the largest carry is worth. */
  const SUB_STEP = K.walkSpeed * walk.dt;

  it('draws exactly the prediction when there is nothing to carry', () => {
    // The invariant that lets one method serve both jobs: with the stick pushed
    // and no leftover, the frame is the prediction untouched. There is no
    // second, carry-free method for the application to call and the tests to
    // leave uncovered — a resting frame is this same call with a zero.
    const p = predictor();
    p.apply(walk);
    const v = p.view(0, WALKING);
    expect(v.x).toBeCloseTo(p.raw().x, 9);
    expect(v.y).toBeCloseTo(p.raw().y, 9);
  });

  it('advances the drawn position monotonically over the carry', () => {
    const p = predictor();
    let last = p.view(0, STILL).y;
    for (const dt of [0.005, 0.01, 0.015, 0.02, 0.025]) {
      const y = p.view(dt, WALKING).y;
      expect(y).toBeGreaterThan(last);
      last = y;
    }
  });

  it('and never past where the next command puts him — it lands exactly there', () => {
    // The claim that makes this not extrapolation: the time has already passed,
    // so the real command starts from the untouched prediction and arrives at
    // the position that was already on screen. Exact BECAUSE the axes are the
    // same in both frames here, which is the condition the claim carries; the
    // test below is what happens when they are not.
    const p = predictor();
    const carried = p.view(walk.dt, WALKING);
    expect(carried.y).toBeCloseTo(LANDING, 9);
    p.apply(walk);
    expect(p.view(0, STILL).x).toBeCloseTo(carried.x, 9);
    expect(p.view(0, STILL).y).toBeCloseTo(carried.y, 9);
  });

  it('steps back by at most one sub-step when the stick is let go', () => {
    // The carry's one honest cost, and the point of pinning it is the BOUND
    // rather than the absence: a carry that was showing part of a command which
    // then never came has to be taken off the screen, and letting go is how
    // that happens constantly. Emptying the leftover after a long stall draws
    // the identical frame, so this covers both. What must never happen is the
    // back-step growing past the sub-step the carry could have been showing —
    // that would mean the carry had drawn something the next command was not
    // about to do.
    const p = predictor();
    for (const carry of [0.005, 0.015, walk.dt]) {
      const carried = p.view(carry, WALKING).y;
      const released = p.view(carry, STILL).y;
      expect(released).toBeLessThan(carried);
      expect(carried - released).toBeLessThanOrEqual(SUB_STEP + 1e-9);
      // And it steps back to the prediction itself rather than past it: where
      // the last command actually left him, which is the only place the next
      // one can start from.
      expect(released).toBeCloseTo(p.raw().y, 9);
    }
    expect(p.view(0, WALKING).y).toBeCloseTo(p.raw().y, 9);
  });

  it('draws against a copy, leaving the prediction untouched', () => {
    const p = predictor();
    const before = p.raw();
    p.view(0.02, WALKING);
    p.view(0.02, WALKING);
    expect(p.raw()).toEqual(before);
    expect(p.pendingCount()).toBe(0);
  });

  it('takes the aim from the axes, not from the prediction it is carrying', () => {
    // Both of these matter. The aim has to be carried at all because step()
    // writes the command's yaw and pitch straight onto the player, so a carry
    // built without them would face yaw zero and swing the horizon round to
    // north on every frame between two commands. And it has to come from the
    // AXES because those are what emitter.due puts into the real command: read
    // off the prediction it would agree only while look() happened to run first
    // in the same frame, which is agreement by call ordering rather than by
    // construction. So look() is left holding yesterday's aim here.
    const p = predictor();
    p.look(0, 0);
    const v = p.view(walk.dt, { mx: 0, my: 1, yaw: Math.PI / 2, pitch: -0.3 });
    expect(v.yaw).toBeCloseTo(Math.PI / 2, 9);
    expect(v.pitch).toBeCloseTo(-0.3, 9);
    // Yaw π/2 faces +X, so walking forward has to carry him along x and not y —
    // exactly where the command about to be emitted will put him.
    expect(v.x).toBeCloseTo(50 + SUB_STEP, 9);
    expect(v.y).toBeCloseTo(50, 9);
  });
});

describe('aim', () => {
  it('is the client’s own state — the server clamps it but never sets it', () => {
    // A view that snapped back to a snapshot's angle would fight the thumb
    // turning it, which is far worse than a position doing the same.
    const p = predictor();
    p.look(1.2, -0.3);
    p.reconcile(snap({ x: 50, y: 50, ack: 0 }));
    expect(p.view(0, STILL).yaw).toBeCloseTo(1.2, 9);
    expect(p.view(0, STILL).pitch).toBeCloseTo(-0.3, 9);
  });
});
