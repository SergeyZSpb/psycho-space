/**
 * «ВАНЯДУМ» — where the thing in your hands is this frame.
 *
 * WHY THIS IS IN `lib/` AND NOT IN THE RENDERER. The renderer holds a GPU and
 * cannot be asserted on without a browser; the TIMING of an animation is
 * arithmetic, and arithmetic belongs where it can be unit-tested. So this module
 * answers "where is it, and how far has its moving part travelled" as a pure
 * function of the countdown, and the scene does nothing with the answer but
 * assign it to a transform. It is the same split the rest of this game keeps.
 *
 * WHAT A POSE IS. An OFFSET from the item's own resting pose in the hand, never
 * an absolute placement: the rest pose belongs to the mesh — it is a matter of
 * how the thing is held, which is the renderer's business — and an animation is
 * the departure from it. That is what lets a second animation be written without
 * either of them agreeing about where anything sits.
 *
 * WHY IT IS SHAPED FOR A SECOND ANIMATION AND IS STILL NOT A FRAMEWORK. The
 * reload and the melee this game will grow are the same shape as this one: a
 * countdown the client already predicts, a rigid item moving in the hand, and one
 * moving part travelling from one end of its stroke to the other. So there is a
 * pose type and a pure function per animation, and that is the whole of it — no
 * registry, no timeline, no easing library, and nothing that would have to be
 * generalised further to accept the second one. When the reload arrives it is a
 * function beside `syringePose`, and the only thing it shares is the type.
 */

/**
 * Where a held item is this frame, relative to how it rests in the hand.
 *
 * SIX RIGID DEGREES AND ONE SCALAR, and every one of them is used by the only
 * animation there is today — a field nothing moves would be a field the renderer
 * applies for nothing. Yaw is deliberately absent: it is where the player is
 * LOOKING, the camera owns it, and an item that yawed independently of the view
 * would swing across the screen.
 */
export interface ViewmodelPose {
  /**
   * Metres added to the rest position, in camera space: +x right, +y up, −z
   * away from the eye — the renderer's own convention, so the scene assigns
   * these without transforming them.
   */
  x: number;
  y: number;
  z: number;
  /** Radians added to the rest rotation. */
  pitch: number;
  roll: number;
  /**
   * How far the item's own moving part has travelled, 0..1 — the plunger here,
   * a break-open latch or a swing for whatever comes next.
   */
  action: number;
}

/**
 * How much of the injection is spent bringing the hand up, and how much pushing
 * the needle in. The rest of it is the plunger going down.
 *
 * FRACTIONS OF THE INJECTION RATHER THAN SECONDS, because the duration is served
 * (`inject_seconds` on the catalogue's medicine entry) and can be retuned without
 * a client deploy. Expressed in seconds, retuning it to something short would
 * leave a hand still rising when the ampoule was already empty.
 *
 * Together they are a bit over a quarter of it — long enough to read as a hand
 * doing something deliberate, short enough that most of the time on screen is the
 * plunger, which is the part that means anything.
 */
const RAISE = 0.16;
const INSERT = 0.12;

/**
 * How far below the resting pose the hand starts, and how much further from the
 * eye — so the syringe comes up and towards you rather than sliding up a plane.
 */
const RAISE_DROP = 0.34;
const RAISE_OUT = 0.05;
/** How far the syringe travels towards the forearm once it is up, in metres. */
const INSERT_TRAVEL = 0.07;
/** How far it is tipped down before it comes up, and rolled before it goes in. */
const RAISE_PITCH = -0.5;
const INSERT_ROLL = 0.35;

function clamp01(v: number): number {
  if (!(v > 0)) return 0;
  return v > 1 ? 1 : v;
}

/**
 * Fast, then settling — the shape a hand actually moves in.
 *
 * Only the raise uses it. The needle going in is a steady push and the plunger is
 * the health arriving, and easing either would be the picture disagreeing with
 * the arithmetic underneath it.
 */
function easeOut(t: number): number {
  const rest = 1 - t;
  return 1 - rest * rest;
}

/**
 * The syringe, from the countdown the client is already predicting.
 *
 * Null when nothing is running, so the caller has one question to ask rather than
 * a pose plus a flag — and so the renderer's "put the обрез back in his hands" is
 * the same branch as "there is no syringe".
 *
 * THE PLUNGER IS THE DELIVERED HEALTH AND NOT A DECORATION, which is the one
 * thing about this animation that had to be right. The server hands out the
 * ampoule as a straight line over the whole countdown (`injectDelivered` in
 * sim.go), so `action` is exactly that line: a plunger a quarter of the way down
 * is a quarter of the ampoule in the man. An eased plunger, or one that only
 * started moving after the needle was in, would be a picture that lies about the
 * only thing the player is waiting for.
 *
 * THE SAME QUANTITY AS THE HEALTH READOUT, FROM A DIFFERENT AUTHORITY, and that
 * distinction is the whole reason this takes a predicted countdown rather than a
 * snapshot one. `left` is the PREDICTED remainder, stepped every drawn frame, so
 * the hand comes up the instant the man walks onto the шприц and the plunger goes
 * on travelling between snapshots. The number written on the HUD is the SERVER'S
 * health at twenty hertz, because a number a player reads must not be a guess
 * taken back a frame later. Both ends compute the same straight line the same
 * way, so the two agree to within a round trip — which is precisely the round
 * trip the plunger exists in order not to wait for. They are one quantity from
 * two sources, not one number read twice.
 *
 * UNDER `prefers-reduced-motion` THE INFORMATION SURVIVES AND THE TRAVEL DOES
 * NOT. The hand does not sweep up and the needle does not slide in — the syringe
 * is simply already there, in the pose it would have settled into — and the
 * plunger still tracks the delivery, because that is what says an injection is
 * running and how far along it is. Somebody who asked for less movement still has
 * to be able to tell.
 */
export function syringePose(
  left: number,
  seconds: number,
  reducedMotion: boolean,
): ViewmodelPose | null {
  if (!(left > 0)) return null;
  // A duration of zero would divide. It cannot arrive from a catalogue that has
  // medicine in it, and a catalogue that has not cannot produce a countdown — so
  // this is the same cheap insurance `injectDelivered` carries, not a case
  // anybody expects.
  const spent = seconds > 0 ? clamp01((seconds - left) / seconds) : 1;

  // Both are 1 from the first frame under reduced motion, which is what "already
  // there" means: the settled pose, with only the plunger left to move.
  const rise = reducedMotion ? 1 : easeOut(clamp01(spent / RAISE));
  const insert = reducedMotion ? 1 : clamp01((spent - RAISE) / INSERT);

  return {
    x: -INSERT_TRAVEL * insert,
    y: -RAISE_DROP * (1 - rise),
    z: -RAISE_OUT * (1 - rise),
    pitch: RAISE_PITCH * (1 - rise),
    roll: INSERT_ROLL * insert,
    action: spent,
  };
}
