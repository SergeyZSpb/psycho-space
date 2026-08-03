/**
 * «ВАНЯДУМ» — the movement simulation and the gun, in the browser.
 *
 * THIS IS A SECOND IMPLEMENTATION OF ONE RULE, and this project's own rules say
 * not to build one. It exists because client-side prediction is impossible
 * without it: to apply your own input the instant you press it, the browser has
 * to compute the same thing the server would have (ADR-052).
 *
 * WHAT MAKES IT SAFE. `internal/gamevanyadum/sim.go` is the authority; this is
 * the port. They are pinned together by golden vectors — the Go test emits a
 * level, an input transcript and the resulting position trace into
 * `internal/gamevanyadum/testdata/step_vectors.json`, and
 * `vanyadumStep.spec.ts` asserts this file reproduces it — the positions, the
 * shell count, both of the gun's timers, the ammunition, and the spawn
 * protection a man gets up with.
 * Change either on one side and the gate goes red on the other. **Do not edit
 * this file without regenerating the vectors and reading them.**
 *
 * It is line-for-line the Go, deliberately, down to the order of the clamps and
 * the shape of the collision loop. Where it would be more idiomatic in
 * TypeScript to do something else, it does the Go thing instead — a port that
 * reads as a translation can be checked against its original by eye, and one
 * that reads as a rewrite cannot.
 *
 * THE SERVER REMAINS THE ONLY AUTHORITY. Nothing here decides anything. When
 * this disagrees with a snapshot, the snapshot wins, unconditionally.
 */

import type { VanyadumLevel, VanyadumSector, VanyadumWall } from './vanyadumLevel';

/** The constants the simulation runs on, as the catalogue serves them. */
export interface StepConstants {
  walkSpeed: number;
  radius: number;
  maxStep: number;
  maxPitch: number;
  maxStepSeconds: number;
  collisionPasses: number;
  /** Barrels a full gun holds. */
  barrels: number;
  /** Seconds the gun is busy after a shot. */
  fireCooldownSeconds: number;
  /** Seconds a reload takes. */
  reloadSeconds: number;
  /** How much ammunition one reload spends. */
  reloadCost: number;
  /**
   * The most health a man can hold. Here because it is the CAP AN AMPOULE IS
   * CLAMPED TO, and clamping is the whole of why injecting at nearly full health
   * is a bad trade rather than a free top-up.
   */
  maxHealth: number;
  /**
   * How much health one ampoule holds, and how long it takes to empty.
   *
   * Both come from the catalogue's medicine entry — `heals` and
   * `inject_seconds` — rather than being constants here, so retuning either on
   * the server moves what the browser predicts with no frontend change.
   */
  syringeHeal: number;
  syringeSeconds: number;
}

/** One sub-step of input. Mirrors the server's Command. */
export interface StepCommand {
  seq?: number;
  dt: number;
  mx: number;
  my: number;
  yaw: number;
  pitch: number;
  /**
   * A REQUEST to pull the trigger, and never a claim to have fired.
   *
   * Optional here and omitted on the wire, because almost no command is a
   * trigger pull: forty sub-steps a second go out, and even a player firing as
   * fast as the gun allows asks three times in that second.
   */
  fire?: boolean;
}

/**
 * The simulated player. Mirrors the server's Player, minus what we never predict.
 *
 * EVERY FIELD HERE IS EITHER PREDICTED OR READ, and the two are worth telling
 * apart. ADR-058's question decides which: must the CLIENT simulate this?
 *
 *   * PREDICTED — the position, and the gun's shell count, both timers and the
 *     ammunition a reload spends. The gun has to be, for a reason movement never
 *     raised: the muzzle flash is drawn the instant a thumb lands, and a flash
 *     can only be honest if the browser has already run the same refusal the
 *     server is about to run.
 *   * BOTH — `protectedLeft` and `injectLeft`. Each is SET by the server (a
 *     respawn grants the first, walking over an ampoule the second) and COUNTED
 *     DOWN here, because each refuses the trigger and that refusal has to run in
 *     the same frame as the thumb. The ampoule also refuses every STEP, so a
 *     client that did not run it would draw a man walking out of a room the
 *     server has him rooted in.
 *   * BOTH, AND IT USED TO BE NEITHER — `health`. Nothing the player does moved
 *     it while damage was the world's alone, so it was only ever read off the
 *     snapshot. The шприц ended that: an injection is advanced INSIDE `step` and
 *     the health it delivers with it, so the browser now produces the number too.
 *     Damage is still a correction like any other, and `step` still READS the
 *     value besides — a man on the floor does not walk, and a client that went on
 *     walking him would have his corpse dragged down the corridor and corrected
 *     twenty times a second.
 *
 * `ammo` IS ONE NUMBER WHERE THE SERVER KEEPS A WHOLE BAG. `Step` reads exactly
 * one key of `Player.Counters` — the one a reload spends — and the golden vectors
 * trace exactly that one integer, so a map here would be a second copy of the
 * HUD's bag pretending to be part of the simulation. The HUD reads the whole bag
 * straight off the snapshot; this is only the number the trigger consults.
 *
 * `loaded`, `cooldown` and `ammo` ARE READ-MODIFY-WRITE, which nothing on this
 * type was before. A countdown is decremented rather than replaced, so replaying
 * a pending command on top of a state that already contains it decrements it
 * twice — see the reconcile in vanyadumPredict.ts for what that obliges.
 */
export interface StepPlayer {
  x: number;
  y: number;
  yaw: number;
  pitch: number;
  sector: number;
  /**
   * What is left of him. ZERO IS THE WHOLE OF BEING DEAD — there is no separate
   * flag, on the wire or here.
   *
   * PREDICTED ONLY WHERE AN AMPOULE IS RUNNING. `stepInject` below is the one
   * thing in this file that writes it; every other movement of the number is
   * damage, which belongs to the world and arrives as a correction.
   */
  health: number;
  /**
   * Seconds of spawn protection left: he cannot be hurt and he cannot fire.
   *
   * Counted down by the command's own dt rather than held as a deadline, because
   * `step` is pure, reads no clock and knows no tick — seconds are the only
   * representation both ends can agree on.
   */
  protectedLeft: number;
  /** Barrels ready to fire, 0..barrels. */
  loaded: number;
  /** Seconds until the gun fires again. */
  cooldown: number;
  /** Seconds until a reload completes; zero when none is running. */
  reload: number;
  /** How much of the reload's ammunition is in the player's pockets. */
  ammo: number;
  /**
   * Seconds until the ampoule in his forearm is empty; zero when there is none.
   *
   * While it runs he CANNOT WALK and CANNOT FIRE, and `health` climbs as it
   * empties. Both refusals are why it is predicted rather than merely drawn: the
   * browser decides whether to draw a muzzle flash the instant a thumb lands, and
   * it predicts its own position, which while this runs does not move however
   * hard the stick is held.
   *
   * Counted down by the command's own dt, exactly as the gun's timers are,
   * because `step` is pure and knows no tick.
   */
  injectLeft: number;
}

/** Squared epsilon used where the Go uses 1e-9. Kept identical on purpose. */
const EPS = 1e-9;

function clampFinite(v: number, lo: number, hi: number): number {
  if (Number.isNaN(v)) return 0;
  return Math.max(lo, Math.min(hi, v));
}

function clamp(v: number, lo: number, hi: number): number {
  return Math.max(lo, Math.min(hi, v));
}

/**
 * Clamps a command into the range the server is willing to simulate.
 *
 * Applied here as well as on the server, and identically, because a client that
 * predicted from an unclamped command would disagree with the server about
 * every hostile or merely unlucky input — and a correction that arrives on
 * every frame is indistinguishable from a broken game.
 */
export function sanitise(c: StepCommand, k: StepConstants): StepCommand {
  let yaw = c.yaw;
  if (Number.isNaN(yaw) || !Number.isFinite(yaw)) yaw = 0;
  yaw = yaw % (Math.PI * 2);
  return {
    seq: c.seq,
    dt: clampFinite(c.dt, 0, k.maxStepSeconds),
    mx: clampFinite(c.mx, -1, 1),
    my: clampFinite(c.my, -1, 1),
    yaw,
    pitch: clampFinite(c.pitch, -k.maxPitch, k.maxPitch),
    // Carried through untouched, exactly as the server's own Sanitise carries it
    // — a bool has no range to clamp into. It is here at all because this
    // function builds a fresh object where the Go copies a struct, so a field
    // left out is a field silently dropped rather than a compile error.
    fire: c.fire,
  };
}

/** The sector containing a point, or −1. Mirrors the server's SectorAt exactly. */
export function sectorAt(level: VanyadumLevel, x: number, y: number): number {
  for (const s of level.sectors) {
    if (x >= s.x0 && x <= s.x1 && y >= s.y0 && y <= s.y1) return s.id;
  }
  return -1;
}

function sectorById(level: VanyadumLevel, id: number): VanyadumSector | undefined {
  return level.sectors.find((s) => s.id === id);
}

/**
 * Moves a disc clear of one wall if it is inside it.
 *
 * `from` is where the player was before the step, used only to break the
 * degenerate tie when the centre lands exactly on the segment — anything but a
 * division by zero, which would put him at infinity.
 */
function pushOut(
  w: VanyadumWall,
  px: number,
  py: number,
  fromX: number,
  fromY: number,
  radius: number,
): { x: number; y: number; moved: boolean } {
  let cx: number;
  let cy: number;
  if (w.v) {
    cx = w.a;
    cy = clamp(py, w.lo, w.hi);
  } else {
    cx = clamp(px, w.lo, w.hi);
    cy = w.a;
  }
  let dx = px - cx;
  let dy = py - cy;
  let d = Math.hypot(dx, dy);
  if (d >= radius) return { x: px, y: py, moved: false };
  if (d < EPS) {
    if (w.v) {
      dx = fromX - w.a;
      dy = 0;
      if (dx === 0) dx = 1;
    } else {
      dx = 0;
      dy = fromY - w.a;
      if (dy === 0) dy = 1;
    }
    d = Math.hypot(dx, dy);
  }
  const scale = radius / d;
  return { x: cx + dx * scale, y: cy + dy * scale, moved: true };
}

/**
 * Moves a disc from one point towards another, sliding along whatever it meets.
 *
 * The whole collision model is "push the circle out of every solid segment it
 * overlaps, a few times" — enough here because every wall is axis-aligned, and
 * it gives wall-sliding, doorway jambs and inside corners with no special cases.
 */
function resolve(
  level: VanyadumLevel,
  fromX: number,
  fromY: number,
  fromSector: number,
  toX: number,
  toY: number,
  k: StepConstants,
): { x: number; y: number; sector: number } {
  let px = toX;
  let py = toY;
  for (let pass = 0; pass < k.collisionPasses; pass++) {
    let moved = false;
    for (const w of level.walls) {
      const out = pushOut(w, px, py, fromX, fromY, k.radius);
      if (out.moved) {
        px = out.x;
        py = out.y;
        moved = true;
      }
    }
    if (!moved) break;
  }

  // Two ways the move is refused outright rather than adjusted, both mirroring
  // the server: leaving the level at all means the resolver has been defeated,
  // and standing still is always a safe answer where teleporting into the void
  // is not; and a rise taller than MaxStep is the ordinary rule.
  const id = sectorAt(level, px, py);
  if (id < 0) return { x: fromX, y: fromY, sector: fromSector };
  const to = sectorById(level, id);
  const from = sectorById(level, fromSector);
  if (to && from && Math.abs(to.fz - from.fz) > k.maxStep + EPS) {
    return { x: fromX, y: fromY, sector: fromSector };
  }
  return { x: px, y: py, sector: id };
}

/**
 * Advances one player by one command. Pure — no clock, no randomness, no state.
 *
 * Mirrors `Step` in `internal/gamevanyadum/sim.go`. The command is sanitised
 * here rather than by the caller, so there is no path into the simulation that
 * skips it — the same promise the server makes.
 */
export function step(
  level: VanyadumLevel,
  p: StepPlayer,
  cmd: StepCommand,
  k: StepConstants,
): StepPlayer {
  const c = sanitise(cmd, k);
  const out: StepPlayer = { ...p, yaw: c.yaw, pitch: c.pitch };

  // A MAN ON THE FLOOR DOES NOTHING BUT LOOK AROUND. He does not walk, he does
  // not shoot, and neither of his timers runs — what gets him up again is the
  // server's own deadline rather than anything he sends.
  //
  // IT IS A RULE OF `step` AND NOT OF THE WORLD, deliberately, and this port is
  // exactly why: a server that quietly ignored a dead player's commands while
  // the browser went on applying them would drag his corpse down the corridor
  // and correct it twenty times a second. Here, both ends refuse the same input
  // and there is nothing to reconcile.
  //
  // The angles are still assigned, above, because the camera belongs to the
  // client — a death you cannot look around from is a black screen with a timer
  // on it.
  if (out.health <= 0) return out;

  // Spawn protection runs on the command's own dt, exactly as the gun's timers
  // do, and it is what stops the trigger below.
  out.protectedLeft = Math.max(0, out.protectedLeft - c.dt);

  // The ampoule first, because the gun below asks whether one is running. Both
  // obey the same ordering rule for the same reason: the timer is advanced and
  // THEN the trigger is read, so a shot asked for on the very step an injection
  // finishes is honoured rather than refused by a state the arm has just left.
  stepInject(out, c, k);

  // BEFORE the standing-still return below, and that ordering is the whole of
  // whether the gun works: firing while standing perfectly still is not an edge
  // case, it is how anybody shoots at anything. A gun folded in after that
  // return would cool down only while its owner was walking — and the golden
  // vectors' `gun` case walks throughout precisely so that a port which got this
  // wrong would still pass a stationary trace.
  stepGun(out, c, k);

  // A MAN WITH A NEEDLE IN HIS ARM DOES NOT WALK, and this is the whole of the
  // vulnerability the ampoule buys. It is refused HERE rather than by zeroing the
  // axes, so his angles still land — the camera belongs to the client, and a
  // first-person game that takes the view away from somebody is worse than one
  // that takes his feet.
  //
  // AFTER the gun and after the injection's own countdown, so that the step an
  // injection ends on is a step he can move in — the same rule the server applies
  // to a man getting up off the floor.
  if (out.injectLeft > 0) return out;

  // The movement axes are in the player's own frame, so yaw turns them into the
  // world. Yaw zero looks along +Y; the renderer's camera uses the same
  // convention and the golden vectors pin it, because the two disagreeing looks
  // like broken controls rather than like a broken transform.
  const sin = Math.sin(out.yaw);
  const cos = Math.cos(out.yaw);
  let dx = c.my * sin + c.mx * cos;
  let dy = c.my * cos - c.mx * sin;

  // A diagonal must not be faster than a straight line. Normalising only when
  // the vector is longer than one keeps a half-pressed stick analogue.
  const mag = Math.hypot(dx, dy);
  if (mag > 1) {
    dx /= mag;
    dy /= mag;
  }
  if (dx === 0 && dy === 0) return out;

  const dist = k.walkSpeed * c.dt;
  const r = resolve(level, p.x, p.y, p.sector, p.x + dx * dist, p.y + dy * dist, k);
  out.x = r.x;
  out.y = r.y;
  out.sector = r.sector;
  return out;
}

/**
 * Advances the ampoule by one command: the countdown by the command's own dt,
 * and the health that went in while it ran. Mirrors `stepInject` in
 * `internal/gamevanyadum/sim.go`.
 *
 * THE HEALTH IS DERIVED FROM THE CLOCK AND NEVER ACCUMULATED, and on this side
 * of the port that is what makes reconciliation work at all. How much has gone in
 * is a pure function of how much is left (`injectDelivered`), so the step grants
 * the DIFFERENCE between the two readings either side of the decrement — and a
 * player replayed from a snapshot therefore arrives at exactly the health the
 * server has, because both ends computed it from the same remaining time rather
 * than from a sum of their own histories. A fractional accumulator carried on the
 * player would be a second piece of state to reconcile, and the sum of
 * heal×dt/seconds over commands a client chose the lengths of is not the same
 * number twice.
 *
 * IT DOES NOT OVERHEAL. The cap is applied to the grant rather than to the
 * ampoule, so a man who fills up halfway through is still rooted for the rest of
 * it — the ampoule is spent on the clock, not on the health.
 *
 * WRITES INTO ITS ARGUMENT, exactly as `stepGun` below does and for the same
 * reason: `step` has already made the copy.
 */
function stepInject(p: StepPlayer, c: StepCommand, k: StepConstants): void {
  if (p.injectLeft <= 0) return;
  const before = injectDelivered(p.injectLeft, k);
  p.injectLeft = Math.max(0, p.injectLeft - c.dt);
  const gain = injectDelivered(p.injectLeft, k) - before;
  if (gain > 0) p.health = Math.min(p.health + gain, k.maxHealth);
}

/**
 * How much of one ampoule has gone in by the time `left` seconds of it remain:
 * nothing at the start, the whole of `syringeHeal` at the end, and a straight
 * line between the two.
 *
 * ROUNDED RATHER THAN TRUNCATED, and this is the one line in the game where two
 * languages could disagree. Go's `math.Round` and JavaScript's `Math.round` part
 * company on exact halves of NEGATIVE numbers, and nothing here is ever negative:
 * `left` is clamped to zero at the low end and to `syringeSeconds` at the high
 * one, so the argument is bounded by 0 and `syringeHeal` and the two agree bit
 * for bit.
 *
 * THE GUARD IS FOR A CATALOGUE WITH NO MEDICINE IN IT, which is a config the
 * server would never start an injection from — but the constants are served
 * rather than compiled in, and a zero duration here would divide and put a NaN
 * into the health the HUD reads. Cheap insurance, not a case anybody expects.
 */
function injectDelivered(left: number, k: StepConstants): number {
  if (!(k.syringeSeconds > 0)) return 0;
  return Math.round((k.syringeHeal * (k.syringeSeconds - left)) / k.syringeSeconds);
}

/**
 * Whether the trigger would be refused right now — every reason at once.
 *
 * ONE PREDICATE WITH THREE CALLERS, and the third is what made it worth
 * extracting. `stepGun` runs it because it is the simulation's own rule; the
 * view runs it to decide whether a pull is worth the uplink at all, since asking
 * a question you have already answered spends the worse half of a mobile
 * connection to be told no; and the view runs it AGAIN over the numbers the
 * snapshot carries, to mark the trigger control as busy — because a pull refused
 * by the cadence and a pull that vanished look identical on a button that never
 * changes, and this project counts an invisible refusal as an unfinished action.
 * Three copies of a five-way condition is three places to forget the ampoule the
 * next time one is added.
 *
 * `health` IS IN IT although `stepGun` is only ever reached with a living man —
 * `step` returns early for a corpse — because the other two callers are not,
 * and a man on the floor is the most obvious refusal of the five.
 */
export function gunBusy(
  p: Pick<StepPlayer, 'health' | 'protectedLeft' | 'cooldown' | 'reload' | 'injectLeft'>,
): boolean {
  return (
    p.health <= 0 ||
    p.cooldown > 0 ||
    p.reload > 0 ||
    p.protectedLeft > 0 ||
    p.injectLeft > 0
  );
}

/**
 * Advances the обрез by one command: the timers by the command's own dt, and
 * then the trigger. Mirrors `stepGun` in `internal/gamevanyadum/sim.go`.
 *
 * TIMERS FIRST AND THE TRIGGER SECOND. A shot asked for on the very step a
 * cooldown runs out is honoured, rather than being refused by a state the gun has
 * just left — which at forty commands a second is the difference between a weapon
 * that fires when you ask and one that occasionally eats a tap for no reason a
 * player can see.
 *
 * THE TRIGGER IS THE ONLY THING THAT STARTS A RELOAD, and nothing reloads by
 * itself, so pulling on an empty gun is always answered: with a shot, or with a
 * reload, or with nothing at all when there is no beer.
 *
 * NOTHING HERE HITS ANYTHING, AND THAT IS PERMANENT RATHER THAN UNFINISHED. A
 * granted shot spends a barrel and starts a cooldown, and that is the complete
 * list. Where the ray went and who it landed on is the SERVER's — it needs every
 * other body in the building and a rewind buffer of where they used to be — and
 * the browser learns the answer a round trip later, as a mark on the man who was
 * shot. A client is never allowed to predict a question about somebody else.
 *
 * WRITES INTO ITS ARGUMENT, unlike everything else in this file, because `step`
 * has already made the copy: `out` is a fresh object nobody else holds, so a
 * second one would be an allocation per command per player at forty a second for
 * no property the caller can observe. The Go returns a value because its Player
 * is a value; the shapes differ, the arithmetic does not.
 */
function stepGun(p: StepPlayer, c: StepCommand, k: StepConstants): void {
  if (p.reload > 0) {
    p.reload = Math.max(0, p.reload - c.dt);
    if (p.reload === 0) p.loaded = k.barrels;
  }
  p.cooldown = Math.max(0, p.cooldown - c.dt);

  // PROTECTION IS PART OF THE REFUSAL AND NOT A SEPARATE CHECK, so a protected
  // man cannot start a reload either — otherwise the window would be spent
  // filling the gun he is about to be untouchable with.
  //
  // AND SO IS THE AMPOULE, WHICH IS HOW THE TRIGGER AND THE INJECTION AGREE
  // ABOUT WHO WINS: the injection does, always, for as long as it lasts. The
  // other way round — a trigger pull that cancels the needle — reads as the
  // generous rule and is in fact the rule that deletes the feature, because a
  // window you can leave by tapping the thing you were already holding is not a
  // window. The same refusal keeps a reload from starting mid-injection, since
  // the trigger is the only thing that starts one.
  if (!c.fire || gunBusy(p)) {
    return;
  }
  if (p.loaded > 0) {
    p.loaded -= 1;
    p.cooldown = k.fireCooldownSeconds;
    return;
  }
  // An empty gun. The ammunition is spent NOW rather than when the reload
  // finishes, so that a reload interrupted by anything at all cannot be a free
  // one. There is no in-place hazard to guard against here the way the server has
  // one — its bag is a map shared with the caller, and this is a plain number on
  // an object `step` has just copied.
  if (p.ammo >= k.reloadCost) {
    p.ammo -= k.reloadCost;
    p.reload = k.reloadSeconds;
  }
}

/**
 * Where an eye sits for somebody standing in this sector. Mirrors `EyeZ`.
 *
 * Takes the sector rather than a whole player because it has two callers that
 * hold different things: the predictor knows the player it is simulating, and
 * the peer decode knows only the sector index the wire named — a peer's height
 * is no longer sent, precisely so the client can look it up here (see
 * vanyadumRoster). Both of them must land on the same number as the server, and
 * one function is how that stays true.
 *
 * Predicted rather than taken from the snapshot for the same reason the
 * position is: a camera height that only changed twenty times a second would
 * make every step visibly jolt.
 *
 * A sector nothing matches is the eye height alone, exactly as the server's own
 * bounds check answers.
 */
export function eyeZ(level: VanyadumLevel, sector: number, eyeHeight: number): number {
  const s = sectorById(level, sector);
  return (s ? s.fz : 0) + eyeHeight;
}
