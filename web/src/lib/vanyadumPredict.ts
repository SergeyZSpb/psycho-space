/**
 * «ВАНЯДУМ» — client-side prediction and server reconciliation.
 *
 * THE PROBLEM. Without this the camera only moves when a snapshot lands, so the
 * world updates twenty times a second while the screen redraws sixty — which
 * looks exactly like twenty frames a second — and every input waits out a round
 * trip before it does anything. Both were measured on a phone and both were
 * bad.
 *
 * THE MECHANISM (Gambetta's rung two). Every frame the client builds a command,
 * gives it a sequence number, **applies it locally through the same `Step` the
 * server runs**, and keeps it pending. The server echoes the last sequence it
 * folded in. On each snapshot the client drops everything acknowledged, resets
 * to the server's authoritative position, and **replays what is still pending
 * on top of it**. Movement then responds in zero milliseconds and updates at
 * frame rate, and the server still decides everything.
 *
 * WHAT IS PREDICTED, AND WHY THE GUN IS. Position, and — since the обрез — the
 * shell count, both of its timers and the ammunition the reload spends.
 * ADR-058's question is the one that decides each of them, asked the way that
 * record's reasoning asks it rather than the way its one-line test does: must
 * the CLIENT simulate this? The gun must, because the muzzle flash is drawn the
 * instant a thumb lands and a flash is only honest if the browser has already
 * run the same refusal the server is about to run.
 *
 * HEALTH IS PREDICTED IN EXACTLY ONE DIRECTION, and the ampoule is what made it
 * so. Damage is still the world's — a barrel, a нейрослоп — and arrives as a
 * correction like any other; the health an injection delivers is produced INSIDE
 * `step`, from the countdown rather than from a running total, so both ends land
 * on the same number by computing it the same way instead of by agreeing on a sum.
 * `step` consults the value besides, because a man on the floor does not walk.
 *
 * SPAWN PROTECTION AND THE AMPOULE ARE THE SAME SHAPE: granted by the server,
 * counted down here, and part of the same trigger refusal the gun's timers are —
 * with the ampoule refusing every step as well, which is the one refusal that
 * moves the camera. All of them are on the replay base below, which is what makes
 * a death, a respawn and an injection cost nothing to reconcile.
 *
 * WHAT THIS IS NOT. It is not authority. A prediction is a guess that is usually
 * right; when the server disagrees, the server wins, without negotiation and
 * without the client getting a say. See ADR-052.
 */

import type { VanyadumAxes } from './vanyadumInput';
import type { VanyadumLevel } from './vanyadumLevel';
import { eyeZ, step, type StepCommand, type StepConstants, type StepPlayer } from './vanyadumStep';

/**
 * Where the player is authoritatively, as a snapshot reports it.
 *
 * EVERY GUN FIELD IS REQUIRED, and required rather than optional on purpose: the
 * replay base below is a complete object literal with no spread, so a field
 * added to `StepPlayer` and forgotten here is a compile error at every call
 * site. That is the property ADR-058 asks for, and it is worth keeping by
 * construction rather than by luck.
 */
export interface Authoritative {
  /** Metres — the client converts from the wire's centimetres. */
  x: number;
  y: number;
  sector: number;
  /** The last command sequence the server folded in. */
  ack: number;
  /**
   * What is left of the player, and ZERO IS THE WHOLE OF BEING DEAD.
   *
   * THE REPLAY BASE FOR A NUMBER THAT IS NOW PARTLY PREDICTED. Damage is the
   * world's and is never guessed; the health an ampoule delivers is produced by
   * `step`, so replaying the pending commands on top of THIS value re-derives
   * exactly what the server derived. Taking the base from the client's own
   * predicted player instead would count the same commands' delivery twice.
   * `step` consults it besides, because a man on the floor neither walks nor
   * shoots — replaying against a stale `health` would walk a corpse for a round
   * trip and then snap it back to the spawn.
   */
  health: number;
  /**
   * Seconds of spawn protection left; the caller converts the wire's `pr` ms.
   *
   * THE SAME RECONCILE BASE THE GUN'S TIMERS GET, and for the same reason: it is
   * decremented rather than replaced, so a base taken from this client's own
   * memory would take each pending command's dt off it twice. The server also
   * advances it through ticks the client sent nothing for, which is exactly the
   * state a man standing still on a spawn is in.
   */
  protect: number;
  /** Barrels ready to fire — the server's count, never the client's guess. */
  loaded: number;
  /** Seconds until the gun fires again; the caller converts the wire's `d` ms. */
  cooldown: number;
  /** Seconds until a reload finishes; the caller converts the wire's `r` ms. */
  reload: number;
  /**
   * How much ammunition the player is carrying, out of the snapshot's bag.
   *
   * ON THIS TYPE ALTHOUGH THE TRIGGER IS THE ONLY THING THAT SPENDS IT, because
   * the trigger is not the only thing that MOVES it: walking over a bottle is
   * the server's to decide and this client predicts no pickups at all. A locally
   * held count would therefore only ever fall, so an empty gun would refuse a
   * reload the server was granting — and the reload is the one branch of the
   * trigger a player waits a second and a half for.
   */
  ammo: number;
  /**
   * Seconds of ampoule left, and the CALLER reads the discriminator: the wire
   * spends one field on this and on the respawn countdown, with `hp` saying
   * which of the two it means. Above zero health it is the injection; at zero
   * health it is a man on the floor and nothing here is running.
   *
   * THE SAME RECONCILE BASE THE GUN'S TIMERS GET, and for the same reason — it
   * is decremented rather than replaced, so a base taken from this client's own
   * memory would take each pending command's dt off it twice and bring somebody
   * out of an injection early. The server also advances it through ticks the
   * client sent nothing for, which is exactly the state a rooted man is in: he
   * cannot walk, so the idle fill is the only thing advancing him at all.
   */
  inject: number;
}

/** What the renderer draws. */
export interface PredictedView {
  x: number;
  y: number;
  z: number;
  yaw: number;
  pitch: number;
  sector: number;
}

/**
 * A disagreement smaller than this is applied silently.
 *
 * Floating-point drift and the wire's centimetre quantisation mean the server
 * and the client are *never* bit-identical, so without a floor every single
 * snapshot would start a correction and the camera would shiver permanently.
 * One centimetre is below what a player can see and above what quantisation can
 * produce.
 */
export const SILENT_CORRECTION = 0.01;

/**
 * A disagreement larger than this is snapped rather than eased.
 *
 * Beyond a couple of metres the client is not slightly wrong, it is somewhere
 * else — a refused move, a teleport, a resync after a long stall — and gliding
 * a player across a room to arrive at the truth is both slower and stranger
 * than putting them there.
 */
export const SNAP_CORRECTION = 2;

/**
 * How long a correction takes to disappear.
 *
 * The whole trick of smoothing: the *rendered* position keeps its old offset
 * and that offset decays, so the camera never jumps and the player still ends
 * up exactly where the server says. Too fast and it reads as a twitch, too slow
 * and the client is visibly playing a different game from the server.
 */
export const CORRECTION_SECONDS = 0.12;

export interface PredictorOptions {
  level: VanyadumLevel;
  constants: StepConstants;
  eyeHeight: number;
  /**
   * Where the player starts, and with how much of him left.
   *
   * `health` is here rather than assumed because `step` refuses to move a dead
   * man, so a predictor built with a zero would be frozen for the fraction of a
   * second before the first snapshot arrives — the one moment nothing is
   * correcting it. It is the catalogue's own starting health, which is what the
   * server's `NewPlayer` gives somebody walking in.
   */
  start: { x: number; y: number; sector: number; yaw: number; health: number };
}

/**
 * Builds the predictor.
 *
 * It owns the pending list, the predicted state and the correction offset, and
 * takes its clock as an argument everywhere — so a test drives a whole second
 * of prediction, reconciliation and smoothing without waiting for one.
 */
export function createPredictor(opts: PredictorOptions) {
  const { level, constants, eyeHeight } = opts;

  let seq = 0;
  let pending: StepCommand[] = [];
  let predicted: StepPlayer = {
    x: opts.start.x,
    y: opts.start.y,
    yaw: opts.start.yaw,
    pitch: 0,
    sector: opts.start.sector,
    // Alive and unprotected, loaded and dry — which is how the server's
    // NewPlayer leaves somebody: two free shots and then a walk to a bottle.
    // Guessed here only for the fraction of a second before the first snapshot,
    // which overwrites every one of them.
    health: opts.start.health,
    protectedLeft: 0,
    loaded: constants.barrels,
    cooldown: 0,
    reload: 0,
    ammo: 0,
    injectLeft: 0,
  };
  // The offset being eased out, in metres. Added to the predicted position when
  // rendering and decayed towards zero every frame.
  let errX = 0;
  let errY = 0;

  /**
   * Turns a simulated player into what the renderer draws: the correction
   * offset applied, then the eye placed on whatever sector that lands in.
   *
   * Both branches of `view` end here — the resting frame and the carried one —
   * so the two cannot come to disagree about how the correction is applied or
   * where the eye sits, which is what they would do as two functions kept in
   * step by hand.
   */
  function drawnView(p: StepPlayer): PredictedView {
    const shown: StepPlayer = { ...p, x: p.x + errX, y: p.y + errY };
    return {
      x: shown.x,
      y: shown.y,
      z: eyeZ(level, shown.sector, eyeHeight),
      yaw: shown.yaw,
      pitch: shown.pitch,
      sector: shown.sector,
    };
  }

  return {
    /**
     * Applies one command locally and returns it, stamped with its sequence.
     *
     * The caller sends what comes back. Nothing is predicted that is not also
     * sent, and nothing is sent that is not also predicted — the two lists
     * being the same list is what makes reconciliation exact.
     */
    apply(cmd: Omit<StepCommand, 'seq'>): StepCommand {
      seq += 1;
      const stamped: StepCommand = { ...cmd, seq };
      predicted = step(level, predicted, stamped, constants);
      pending.push(stamped);
      return stamped;
    },

    /**
     * Folds in an authoritative position: drop what it acknowledges, reset to
     * it, replay the rest.
     *
     * The replay is the part that is easy to get wrong by omitting: without it
     * the client would snap back to where the server was *a round trip ago* and
     * then re-run forwards from there on the next frame, which is the classic
     * rubber-band.
     */
    reconcile(a: Authoritative): void {
      // A PREDICTOR REBUILT UNDER A LIVING OCCUPANT ADOPTS THE SERVER'S COUNT,
      // or it never moves again. `createPredictor` starts at zero, so its first
      // command is sequence 1 — and the server drops everything at or below the
      // highest sequence THAT OCCUPANT has already sent, which after a few
      // seconds of walking is in the hundreds. So it drops 1 as stale, then 2,
      // then every command after it, silently and for ever.
      //
      // This is not a corner case, because the occupant outlives the client's
      // predictor by design. A reload inside the abandon grace comes back to the
      // player it left standing there; so does a backgrounded tab whose socket
      // dropped; and so does the заброшка being regenerated, which rebuilds the
      // predictor against new geometry WITHOUT the socket or the occupant ever
      // going away — the one that needs no interruption at all.
      //
      // The ack is the right floor because THE SERVER IS THE ONE THAT SAID IT.
      // It is the last sequence the server folded in, so counting on from it is
      // the client agreeing with what it has just been told, not asserting a
      // number of its own — the same relationship the rest of this file has
      // with authority. And it only ever moves the counter FORWARDS: an ack at
      // or below `seq` is acknowledging commands this predictor itself sent, and
      // rewinding onto sequences that are still pending would put two different
      // commands under one number.
      //
      // It is a floor, not an exact resync. The server deduplicates against the
      // highest sequence it has ACCEPTED into that occupant's queue, which sits
      // at or above the last one it has STEPPED — the ack — by however much is
      // still waiting there. So a handful of the first resumed commands can land
      // below that mark and be dropped before the ack catches up. That is a
      // fraction of a second of unresponsiveness, against a freeze a reload
      // could not clear — the occupant, and the count it is stuck behind,
      // survive one.
      if (a.ack > seq) seq = a.ack;

      pending = pending.filter((c) => (c.seq ?? 0) > a.ack);

      // Where the player is being DRAWN right now — predicted plus whatever
      // correction is still easing out. Kept so the new offset can be measured
      // against it rather than against the raw prediction, which is what stops
      // a second correction arriving mid-glide from jumping.
      const drawnX = predicted.x + errX;
      const drawnY = predicted.y + errY;

      // THE REPLAY BASE, AND THE ONE THING THIS FUNCTION MUST NOT BECOME.
      //
      // Until the обрез arrived, every field on the predicted player was
      // REPLACED — by the snapshot, or by the thumb. The gun brought the first
      // three that are DECREMENTED, and a decremented field cannot be replayed
      // on top of a state that already contains it: write `cooldown:
      // predicted.cooldown` here and every command still pending takes its dt
      // off the clock a second time, so a walking client burns its cadence at
      // twice real speed and a reload finishes early. That is ADR-058's subject,
      // and this literal is where the record is either honoured or lost.
      //
      // The base is therefore the SERVER'S gun and not this client's memory of
      // it, and that choice is deliberate rather than the easier of two. The
      // server advances the gun through ticks the client sent nothing for
      // (world.go's idle fill) — and it must, because a player who has fired and
      // is standing perfectly still emits no commands at all, which is precisely
      // the state somebody aiming at something is in. A base rewound to what
      // this client believed before its oldest unacknowledged command would hold
      // a cooldown that stopped running the moment he stopped walking: he taps,
      // the browser refuses a shot the server grants, and no muzzle flash is
      // drawn for a shell that was really spent. Taking it from the snapshot is
      // what makes the client agree with a clock it is not running.
      //
      // What the snapshot costs by comparison is half a millisecond: the timers
      // cross the wire quantised to the millisecond, against a cadence of 350.
      // It does not accumulate — every frame restates the server's own exact
      // value rather than adding to the last one.
      //
      // A COMPLETE OBJECT LITERAL WITH NO SPREAD, and that is load-bearing. A
      // field added to `StepPlayer` and forgotten here stops this file
      // compiling, which is the only mechanism that reliably survives the next
      // person adding a timer. Do not "tidy" it into `{ ...predicted, ... }`.
      let replayed: StepPlayer = {
        x: a.x,
        y: a.y,
        yaw: predicted.yaw,
        pitch: predicted.pitch,
        sector: a.sector,
        health: a.health,
        protectedLeft: a.protect,
        loaded: a.loaded,
        cooldown: a.cooldown,
        reload: a.reload,
        ammo: a.ammo,
        injectLeft: a.inject,
      };
      for (const c of pending) replayed = step(level, replayed, c, constants);
      predicted = replayed;

      const dx = drawnX - predicted.x;
      const dy = drawnY - predicted.y;
      const off = Math.hypot(dx, dy);
      if (off < SILENT_CORRECTION || off > SNAP_CORRECTION) {
        // Too small to see, or too big to glide. Both end with the camera
        // exactly where the server says, which is the only outcome that matters.
        errX = 0;
        errY = 0;
      } else {
        errX = dx;
        errY = dy;
      }
    },

    /** Decays the correction. Called once per rendered frame with its own dt. */
    tick(dt: number): void {
      if (errX === 0 && errY === 0) return;
      // Exponential, not linear: it lands smoothly rather than stopping dead,
      // and it cannot overshoot however long or short the frame was.
      const k = Math.exp(-dt / CORRECTION_SECONDS);
      errX *= k;
      errY *= k;
      if (Math.hypot(errX, errY) < 1e-4) {
        errX = 0;
        errY = 0;
      }
    },

    /** Aim is the client's own state — the server clamps it but never sets it. */
    look(yaw: number, pitch: number): void {
      predicted.yaw = yaw;
      predicted.pitch = pitch;
    },

    /**
     * What to draw this frame: the prediction, carried forward over the time
     * that has already elapsed but has not yet become a command.
     *
     * This is the difference between an eye that moves and an eye that
     * flickers. `predicted` only advances inside `apply`, which happens forty
     * times a second, while this is called on every animation frame — so
     * without the carry the camera is redrawn in the same place for one to
     * three frames and then teleported 12.5 cm, forty times a second, for ever.
     * Only walking suffers, because `look` already runs every frame and turning
     * was therefore always smooth: that asymmetry is the fault's own signature.
     *
     * THERE IS ONE METHOD AND `carrySeconds` IS REQUIRED — not a carrying
     * method beside a resting one, and not an optional argument that leads back
     * to the resting shape. That buys exactly two things, and it is worth being
     * precise about which. First, there is a single drawing path through the
     * predictor, so there is no carry-free second path for the specs to leave
     * uncovered: a resting frame is this same call with a zero, so every spec in
     * `vanyadumPredict.spec.ts` drives the method the game draws with. Second, a
     * required argument makes removing the carry a DELIBERATE edit — the
     * compiler refuses a bare `view()` — rather than a line that can quietly go
     * missing.
     *
     * WHAT IT DOES NOT BUY, AND CANNOT. The ARGUMENT the view passes is
     * unpinned: change `emitter.residualSeconds()` to `0` at the call site and
     * the types are still satisfied, every suite is still green, and the judder
     * is back. There is nowhere to pin it: the eye lives inside the canvas, and
     * ADR-047 accepts that a canvas is opaque to both Playwright suites — what
     * it could offer is a pixel comparison or a test-only introspection API, and
     * this repository takes neither. So only a person looking at the game can
     * see that line go wrong. The gap is written down rather than papered over,
     * because a reviewer who believes the specs cover it will not look at it.
     *
     * THE CARRY COMMAND IS THE AXES PLUS THE ELAPSED TIME — the same two
     * ingredients `emitter.due` puts into the real command, so the carried
     * frame and the command it anticipates agree by construction rather than by
     * call ordering. Taking the aim off `predicted` instead would agree only
     * while `look` happened to have run earlier in the same frame, and would
     * quietly draw a stale horizon against a fresh command the day it did not.
     * The aim has to be carried at all because THIS GAME'S COMMAND IS ABSOLUTE
     * about where the player is looking rather than relative — `step` writes
     * `c.yaw` and `c.pitch` straight onto the player — so a carry command built
     * without them would step the copy facing yaw zero, and every frame between
     * two commands would swing the horizon round to north and back again.
     * «СИМУЛЯТОР ФИНТЕХА»'s command carries no aim at all, so the hazard is
     * this game's alone and the two carries are not the same shape.
     *
     * **It is not extrapolation and it cannot drift.** The step is over time
     * that has already passed, and it runs against a COPY — `predicted` is left
     * exactly as the real command will find it.
     *
     * WHERE IT IS EXACT, AND WHERE IT IS NOT. While the axes are unchanged
     * between the carried frame and the frame that emits, the real command
     * starts from the untouched `predicted` and lands precisely where this had
     * already drawn: nothing moves twice and nothing moves back. When the thumb
     * HAS moved, the command goes somewhere the carry was not pointing and the
     * drawn eye takes one small step across to it — sub-step scale, the same
     * size of disagreement the correction smoothing already glides out on every
     * snapshot. Releasing the stick is the case that happens constantly, and
     * there it steps BACK by whatever fraction of a sub-step the carry was
     * showing: at a walk, at most 12.5 cm on one frame, which the specs pin.
     * That is honest rather than wrong — the carry was drawing a command that
     * then never came.
     */
    view(carrySeconds: number, axes: VanyadumAxes): PredictedView {
      if (!(carrySeconds > 0)) return drawnView(predicted);
      return drawnView(step(level, predicted, { dt: carrySeconds, ...axes }, constants));
    },

    /**
     * The commands the server has not acknowledged, newest last.
     *
     * Sent again in each frame up to `max`, which costs a few bytes and means
     * one lost packet costs no input at all — the server drops any sequence it
     * has already ACCEPTED, queued as well as stepped, so a duplicate is free.
     * Filtering on what it had stepped would have re-admitted everything still
     * waiting in the queue and run it twice. This is only possible
     * because the pending list has to exist for reconciliation anyway.
     */
    unacknowledged(max: number): StepCommand[] {
      return max <= 0 ? [] : pending.slice(-max);
    },

    /** How much is still unacknowledged. For tests. */
    pendingCount(): number {
      return pending.length;
    },
    /**
     * The raw prediction, without the correction offset.
     *
     * The view reads THE GUN off this once a frame and compares it against the
     * frame before, which is how a shot becomes a muzzle flash: a barrel count
     * falling by one IS the shot, exactly as it is on the wire, so nothing has
     * to be published to say one happened. The position on it is for tests —
     * `view` is what the camera is placed from.
     */
    raw(): StepPlayer {
      return { ...predicted };
    },
    /** How far the drawn position currently is from the predicted one. */
    correction(): number {
      return Math.hypot(errX, errY);
    },
  };
}

export type Predictor = ReturnType<typeof createPredictor>;
