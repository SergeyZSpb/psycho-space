import type { VanyagotchiSkin, VanyagotchiStore } from '../api/types';

// Applying positions to the plane, deliberately outside Vue.
//
// Positions arrive five times a second and must not enter reactivity: bound to
// a `:style`, each frame would become a scheduler pass plus one vdom patch per
// entity, which at twenty entities is on the order of a thousand patches a
// second to produce a transform the compositor could have been handed straight
// away. So a frame is applied by writing a handful of CSS custom properties per
// element and nothing else.
//
// Custom properties rather than writing `transform` directly, for one concrete
// reason: the mapping from normalised 0..1 coordinates to pixels then lives once,
// in the stylesheet, against the plane's own box. There is no measured size
// cached in JavaScript to invalidate — which matters here because the plane
// resizes whenever mobile browser chrome slides in or out. It also composes with
// anything else the stylesheet wants to do to the same element.
//
// Everything DERIVED from a position travels the same way and in the same write:
// the depth band, its scale, and which side of an entity a speech balloon has to
// hang on are all pure functions of x and y, so they are properties on the same
// element rather than anything Vue is told about. The rule is the rate of change,
// not the kind of fact — see the appearance half at the foot of this file for
// what does go through reactivity, and why.

/**
 * The positional half of one entity in a roster frame.
 *
 * Half, because a frame entry also says what the entity looks like — see
 * PeerAppearance at the foot of this file. The two are read separately and go to
 * different places on purpose: this half is written straight to CSS five times a
 * second, that half goes through Vue.
 */
export interface PeerPosition {
  id: string;
  x: number;
  y: number;
}

/** The custom properties the stylesheet reads. */
export const X_PROPERTY = '--x';
export const Y_PROPERTY = '--y';
/** Which depth band the entity is in — the stylesheet uses it as the z-index. */
export const BAND_PROPERTY = '--band';
/** That band's scale factor. */
export const DEPTH_PROPERTY = '--depth';
/** 1 when a speech balloon has to hang below its entity instead of above it. */
export const SAY_BELOW_PROPERTY = '--say-below';

/**
 * Everything this module needs of an element: somewhere to set a custom
 * property. Narrowed to exactly that rather than typed as an `HTMLElement`, so
 * the position path can be exercised without a DOM — and so it is obvious from
 * the signature that nothing here reads layout, measures a box, or touches an
 * attribute.
 */
export interface StyleTarget {
  style: { setProperty(name: string, value: string): void };
}

/**
 * Is this a position we can render?
 *
 * The server already clamps and rejects, so this is not a second line of
 * validation — it is a guard against writing `NaN` into a custom property,
 * which CSS resolves to nothing and leaves an entity stuck at its last position
 * with no error anywhere.
 */
export function isRenderablePosition(peer: unknown): peer is PeerPosition {
  if (typeof peer !== 'object' || peer === null) return false;
  const p = peer as Record<string, unknown>;
  return (
    typeof p.id === 'string' &&
    p.id.length > 0 &&
    typeof p.x === 'number' &&
    Number.isFinite(p.x) &&
    typeof p.y === 'number' &&
    Number.isFinite(p.y)
  );
}

// ---------------------------------------------------------------------------
// Depth. Further down the plane is nearer the viewer, so an entity there is
// drawn slightly larger and in front of the ones behind it.
//
// DISCRETE BANDS, NOT A CONTINUOUS FUNCTION OF Y, and that is the whole design.
// The two halves of "nearer" are drawn by different mechanisms: the size is a
// `transform`, which the compositor INTERPOLATES over the 220 ms a move takes,
// while the stacking order is a `z-index`, which can only JUMP. Scale one
// continuously and the two disagree on every single frame — an entity is already
// visibly bigger a third of the way through the walk that will eventually put it
// in front, so for that third it is a large shape drawn behind a small one. With
// bands the disagreement is confined to the instant a boundary is crossed, which
// happens a handful of times per journey rather than sixty times a second.
//
// Four of them because three reads as two-and-a-half — the middle band is most
// of the plane — and five puts the boundaries close enough together that a
// diagonal walk pops through two of them at once.
// ---------------------------------------------------------------------------

/**
 * What each band multiplies an entity's size by, from the back of the plane to
 * the front.
 *
 * THE FIRST ENTRY IS 1 ON PURPOSE, and it is what keeps the mobile suite's
 * legibility floor satisfied without anybody having to redo the arithmetic:
 * depth can only ever make an entity BIGGER than its unscaled size, so the floor
 * is the CSS size itself (`PEER_BASE_PX`, `.peer` in GameVanyagotchiView.vue)
 * rather than a product of two numbers that could drift apart. Scaling the far
 * band DOWN instead would have meant every future change to either number
 * re-deriving whether the smallest entity was still readable.
 *
 * 8% a band: enough that two entities a band apart read as at different
 * distances, small enough that the jump at a boundary is not a pop.
 */
export const DEPTH_SCALES: readonly number[] = [1, 1.08, 1.16, 1.24];

/**
 * The SMALLEST a WHOLE entity is ever drawn, in CSS pixels.
 *
 * A floor, not the size. `.peer` is `var(--unit)` and the unit is
 * `clamp(32px, 9cqw, 64px)`, so an entity grows with the yard and this is the
 * bottom of that clamp.
 *
 * IT USED TO BE 44, AND LOWERING IT WAS ALLOWED PRECISELY BECAUSE IT IS NOT A
 * TAP TARGET. `.peer` is `pointer-events: none` and the plane takes every tap
 * (see `.peer` in GameVanyagotchiView.vue), so no dot on this plane has ever
 * been tappable and the 44 that several assertions still name was never the
 * WCAG number it looks like — it is how small a face can be drawn and still be
 * read across a phone-sized yard. The owner, playing on a phone, reported the
 * whole yard as too big; a third off every length in it is a legibility
 * judgement, which is the only kind this number was ever making.
 *
 * It mirrors the stylesheet rather than driving it: the stylesheet owns the
 * size, and this exists so the floor can be asserted in a unit test. The e2e
 * suite measures the real box at mobile widths, so the two cannot drift apart
 * silently.
 *
 * It is the floor for an entity drawn at its full world unit. A world OBJECT is
 * drawn at half of it (see `propScale` and `.peer--prop`), deliberately and for
 * the same legibility reason inverted: a thing lying on the ground must not
 * compete with the people standing on it.
 *
 * It stays a floor at all only while `DEPTH_SCALES[0] === 1` keeps depth from
 * ever shrinking anybody; the day a far band draws smaller than a near one, this
 * becomes `base * minScale` and the assertions naming it have to be re-argued.
 */
export const PEER_BASE_PX = 32;

/**
 * Which band an entity at this height belongs to.
 *
 * Equal slices of the plane rather than a perspective curve: the plane is a
 * flat 3:4 rectangle with no horizon in it, so there is no vanishing point for a
 * curve to be right about, and equal slices are the thing a player can predict.
 */
export function bandFor(y: number): number {
  if (!Number.isFinite(y)) return 0;
  const band = Math.floor(y * DEPTH_SCALES.length);
  return Math.min(DEPTH_SCALES.length - 1, Math.max(0, band));
}

/**
 * How far up the plane a speech balloon stops fitting above its entity.
 *
 * The balloon hangs off the top of a dot that is centred on its own coordinates,
 * so it needs roughly a dot's height of plane above that point; on the shortest
 * plane we support (about 308 px tall, at 320x568) a 44 px dot made that 0.145 of
 * the height, and the number was chosen against it.
 *
 * The dot is 32 px now, which needs 0.107 — so this is LOOSER than it has to be
 * rather than wrong, and it is left alone: over-flipping costs a balloon nothing
 * (below the entity is a perfectly good place for it, and where it goes for the
 * whole bottom 85% anyway) whereas under-flipping clips it away to nothing.
 * Normalised rather than measured, deliberately — measuring would mean
 * caching the plane's box in JavaScript, which is the one thing this module is
 * built not to do. On a tall screen it therefore flips a balloon that would just
 * have fitted, which costs nothing: below the entity is a perfectly good place
 * for it, and it is where the balloon goes for the whole bottom 85% anyway.
 */
export const SAY_FLIP_Y = 0.15;

/** Does this entity's balloon have to hang below it to stay on the plane? */
export function sayBelow(y: number): boolean {
  return Number.isFinite(y) && y < SAY_FLIP_Y;
}

/**
 * Writes one entity's position onto its element.
 *
 * Everything here is a pure function of x and y, which is why the depth band
 * travels with the position rather than being written from somewhere else:
 * derived at a second site it could lag the coordinates by a frame, and an
 * entity drawn a band behind where it is standing is exactly the artefact the
 * discrete bands above exist to avoid.
 */
export function applyPosition(el: StyleTarget, x: number, y: number): void {
  const band = bandFor(y);
  el.style.setProperty(X_PROPERTY, String(x));
  el.style.setProperty(Y_PROPERTY, String(y));
  el.style.setProperty(BAND_PROPERTY, String(band));
  el.style.setProperty(DEPTH_PROPERTY, String(DEPTH_SCALES[band]));
  el.style.setProperty(SAY_BELOW_PROPERTY, sayBelow(y) ? '1' : '0');
}

/**
 * Applies a whole frame.
 *
 * Peers with no element yet are skipped rather than queued: the element appears
 * on the next render after the store notices the membership change, and the
 * frame after that positions it. One frame is 200 ms, and every frame is full
 * state, so nothing is lost by missing one.
 *
 * Returns the number of entities actually positioned, which is what the tests
 * assert on — "did this frame reach the DOM" is otherwise invisible.
 */
export function applyFrame(
  peers: readonly unknown[],
  elements: ReadonlyMap<string, StyleTarget>,
): number {
  let applied = 0;
  for (const peer of peers) {
    if (!isRenderablePosition(peer)) continue;
    const el = elements.get(peer.id);
    if (!el) continue;
    applyPosition(el, peer.x, peer.y);
    applied += 1;
  }
  return applied;
}

/**
 * Turns a tap into normalised plane coordinates.
 *
 * The client sends where it *believes* the tap was; the server clamps and may
 * refuse it, and the position only becomes real when it comes back in a roster.
 * Nothing here is trusted downstream, which is why this can be as simple as it
 * looks.
 *
 * Clamped anyway, so a tap on the plane's own border — where a rounded
 * `getBoundingClientRect` can put the pointer a fraction outside — does not
 * become a message the server has to reject.
 */
export function tapToPosition(
  rect: { left: number; top: number; width: number; height: number },
  clientX: number,
  clientY: number,
): PeerPosition | null {
  if (rect.width <= 0 || rect.height <= 0) return null;
  const x = clamp01((clientX - rect.left) / rect.width);
  const y = clamp01((clientY - rect.top) / rect.height);
  if (!Number.isFinite(x) || !Number.isFinite(y)) return null;
  return { id: '', x, y };
}

function clamp01(v: number): number {
  if (Number.isNaN(v)) return 0;
  return Math.min(1, Math.max(0, v));
}

// ---------------------------------------------------------------------------
// Appearance — the other half of a roster entry, and the half that IS reactive.
//
// A frame says two kinds of thing about an entity. WHERE it stands changes five
// times a second, and everything above this line exists so that never reaches
// Vue. WHAT it looks like — its art, its name, how it is doing — changes a few
// times an hour, so it can afford the keyed list, and belongs there: a face and
// a name are structure, and re-deriving them imperatively would mean owning the
// diff by hand.
//
// What that costs is one guard. The server sends full state five times a second,
// so a fresh array of fresh objects arrives every 200 ms and is almost always
// identical to the last one; assigned blindly it would re-render the whole yard
// at 5 Hz and undo the split the file above is built around. sameAppearance is
// to appearance exactly what sameIds is to membership.
//
// EVERYTHING HERE IS THE SERVER'S DECISION, resolved rather than derived. The
// wire carries a catalogue key, not a picture, and a pose name, not a rule — so
// adding a skin, or later an NPC that is not a Ваня at all, is a backend change
// with no client deploy. That is also why an unrecognised key has to draw
// something anyway: a client that refuses to render what it has not heard of is
// a client that must be deployed in lockstep with the server, which is the
// property this whole shape is bought to avoid.
// ---------------------------------------------------------------------------

/**
 * How an entity is doing, as the SERVER decided it.
 *
 * Not computed here, and deliberately not computed here even for your own Ваня:
 * the yard has to show everybody the same world, and a pose worked out locally
 * would be worked out from state only its owner can see.
 *
 * Two of them mean something different in kind from the other four, and it is
 * worth knowing which while reading the stylesheet. `fine`, `poorly`, `dead` and
 * `asleep` are CONDITIONS — the server derives each from the stats and the clock,
 * so one is true for as long as the numbers behind it are. `happy` and `sad` are
 * MOODS: the outcome of a contested claim, held in memory on the server for a few
 * seconds and then simply absent again. Nothing here has to know the difference —
 * a pose is a pose to this module and to the template — but the drawing does,
 * which is why neither mood animates: a state that lasts three seconds must be
 * legible in the first frame of it rather than at the top of a cycle.
 */
export type PeerPose = 'fine' | 'poorly' | 'dead' | 'asleep' | 'happy' | 'sad';

/** The poses this client knows how to draw. Anything else falls back to fine. */
const POSES: readonly string[] = ['fine', 'poorly', 'dead', 'asleep', 'happy', 'sad'];

/**
 * What one entity looks like this frame.
 *
 * No coordinates, and that omission is load-bearing: this is the shape that goes
 * through reactivity, so putting x and y in it would quietly drag positions back
 * into the vdom at 5 Hz.
 */
export interface PeerAppearance {
  id: string;
  /** A catalogue skin key, unresolved — see resolveArt. */
  art: string;
  /** The pet's name. Absent, not empty, until it has been given one. */
  label?: string;
  pose: PeerPose;
  /**
   * A line over this entity's head, absent almost always.
   *
   * A string rather than a second pose, because the server draws the same
   * distinction: a pose is how somebody LOOKS and this is something he SAID. It
   * arrives already decided — the walk he gave up on was rolled once, on the
   * server, so everybody watches him sit down in the same spot saying the same
   * thing, which a locally-derived line could not manage.
   */
  say?: string;
  /**
   * When this entity stops existing, as unix SECONDS, or absent for one that
   * does not stop.
   *
   * THE ONLY THING ON A FRAME THAT SAYS «THIS IS NOT A PERSON», and it says it
   * without naming a kind, which is the whole reason the drawing is allowed to
   * key off it. Only a world object is ever given an expiry — a pet, a sleeper
   * and an NPC are all open-ended — so its PRESENCE is an honest signal that
   * something is lying on the ground rather than standing on it, and reading it
   * teaches this client nothing about what the thing IS. A `kind` field, or a
   * lookup of the art key against the catalogue's object kinds, would teach it
   * exactly that, and would put the cast list in the browser where adding to it
   * becomes a deploy (ADR-028).
   *
   * What it costs is stated rather than discovered: an object with no expiry —
   * today, the key, which lasts until somebody finds it — is drawn as an
   * ordinary full-size dot. That is the right way round anyway. The thing this
   * distinction is FOR is litter, which the owner asked to stop dominating the
   * yard, and the one object that is meant to be spotted is the one that keeps
   * its circle.
   *
   * ABSOLUTE, which is what lets it sit on a frame that repeats five times a
   * second: an instant is CONSTANT for the life of the object, so it never
   * churns `sameAppearance` and never re-renders the yard, whereas a countdown
   * would change on every tick and re-render every deposit in it. The client
   * subtracts it from the server clock it already tracks and does the shrinking
   * itself — the same division of labour the stat bars use, where the server
   * sends a rate and the browser interpolates.
   */
  expires?: number;
  // THERE IS NO AVATAR FIELD HERE, and its absence is a decision rather than an
  // omission — the picture of the person behind an entity is fetched by ID, over
  // ordinary HTTP, from GET /api/game-vanyagotchi/avatar/{id}, and never travels
  // on a frame at all. Two reasons, and they point the same way.
  //
  // A URL on the wire would be re-sent for every entity five times a second for
  // as long as anybody is looking: a couple of hundred characters that change
  // perhaps once a year, multiplied by everybody standing in the yard and again
  // by everybody watching it, at an audience holding phones on mobile data. And
  // it would be the one DURABLE thing on a frame whose identity is deliberately
  // ephemeral — a VK URL comes out of Postgres and survives a restart, while the
  // `id` beside it is a per-process pseudonym that on purpose does not, so two
  // frames from either side of a deploy would be linkable by the picture even
  // though nothing else on them was.
  //
  // Fetching by id costs nothing on the wire and puts the picture behind the
  // same pseudonym everything else about a person is behind. It also keeps this
  // module kind-agnostic, which is the property the whole appearance half is
  // built around: the client asks for every entity it draws and lets the answer
  // decide, so a 404 — which is what every NPC and every player VK has no
  // picture for replies — is an ordinary fallback to the catalogue art rather
  // than a case anybody here has to recognise. Putting the field back would undo
  // all three at once.
}

/**
 * Where this client asks for the picture of the person behind an entity.
 *
 * DERIVED FROM THE ID RATHER THAN SENT WITH IT — the note in PeerAppearance
 * above says why a URL has no business on a frame that repeats five times a
 * second. What the derivation buys a caller is the useful half: there is no
 * such thing as an entity this client cannot ask about, so it asks about every
 * entity it draws and lets the answer decide, instead of reading a field whose
 * absence it would have to interpret. A 404 is the ordinary reply — every NPC
 * gets one, and so does every player VK has no picture of — and it means "draw
 * the catalogue art", not "something went wrong".
 *
 * The id is escaped even though every one this server mints today is hex out of
 * a hash. It is a value off the wire being pasted into a URL path, and on the
 * day that stops being true — a pseudonym scheme carrying a slash or a question
 * mark — an unescaped one would ask for a different route entirely rather than
 * fail in any way that looks like a bad id.
 */
export function avatarEndpoint(id: string): string {
  return `/api/game-vanyagotchi/avatar/${encodeURIComponent(id)}`;
}

/**
 * The longest name a dot will show, in characters.
 *
 * Two caps, deliberately, and this is the one a test can hold. The stylesheet
 * caps the WIDTH, which is what the eye sees and what stops a name covering its
 * neighbours; this caps the STRING, so nothing downstream — an aria label, a
 * tooltip, anything added later — has to re-derive the same limit, and so a
 * server that never validated a name cannot put a kilobyte of it in the DOM.
 * Sixteen because it comfortably fits «дядя Ваня» and every ordinary Russian
 * first name, which is what people will actually type.
 */
export const LABEL_MAX = 16;

/**
 * Reads a name off the wire, or reports that there isn't one.
 *
 * Returns undefined rather than an empty string so that "no name" is a single
 * state at every layer: the template asks `v-if="peer.label"` and there is no
 * second falsy value to remember, and nothing can render the string "undefined".
 */
export function capLabel(raw: unknown): string | undefined {
  return capText(raw, LABEL_MAX);
}

/**
 * The longest line a balloon will show, in characters.
 *
 * Longer than a name because a name is one word and a line is a sentence, short
 * enough that a balloon is still smaller than the entity it belongs to. What it
 * really guards is the same thing LABEL_MAX guards: the wire is trusted to be
 * short, and this is what makes it true rather than hoped for.
 */
export const SAY_MAX = 24;

/** Reads a spoken line off the wire, or reports that there isn't one. */
export function capSay(raw: unknown): string | undefined {
  return capText(raw, SAY_MAX);
}

/**
 * Trims a wire string to a maximum, or reports that there was nothing there.
 *
 * Cut by code point rather than by `slice`, because slicing a UTF-16 string in
 * the middle of a surrogate pair leaves a lone half that renders as a
 * replacement character — and both of the fields this caps are exactly the ones
 * somebody puts an emoji in.
 */
function capText(raw: unknown, max: number): string | undefined {
  if (typeof raw !== 'string') return undefined;
  const trimmed = raw.trim();
  if (!trimmed) return undefined;
  const chars = [...trimmed];
  if (chars.length <= max) return trimmed;
  return `${chars.slice(0, max - 1).join('')}…`;
}

/**
 * Reads an expiry off the wire, or reports that this entity does not have one.
 *
 * Everything that is not a positive finite number reads as "no expiry", which
 * collapses four cases that must not be told apart into one: a field the server
 * omitted, a 0 an older server sent instead of omitting it, a `null`, and
 * anything malformed. All four describe an entity that is not going anywhere,
 * and the failure mode of getting this wrong is not subtle — an entity wrongly
 * given an expiry is drawn as half-sized litter that shrinks away, and if the
 * value is in the past it is drawn at nothing at all, i.e. a player who has
 * become invisible.
 */
function readExpires(raw: unknown): number | undefined {
  if (typeof raw !== 'number' || !Number.isFinite(raw) || raw <= 0) return undefined;
  return raw;
}

/**
 * How long an ageing thing is drawn shrinking for, in milliseconds.
 *
 * A PRESENTATION WINDOW, NOT A COPY OF THE SERVER'S LIFETIME, and the difference
 * matters because nothing on the wire could make it a copy: a frame carries the
 * instant a thing expires and never the instant it appeared, so the client knows
 * how much life is LEFT and cannot know how much there was. Deriving the total
 * from the catalogue's object kinds is the one thing that is not allowed here —
 * it would mean matching an entity's art key against a list of kinds, which is
 * precisely the content knowledge `expires` exists to keep out of the browser.
 *
 * So this says how much remaining life is drawn as "full size", and everything
 * below it is drawn proportionally smaller. Ten minutes because that is what the
 * only expiring thing in the game happens to live for, which makes the drawing
 * exactly right today — but it is not READ from anywhere, and it does not have
 * to track a retune, because it degrades gracefully in both directions rather
 * than breaking: too long a window and a fresh deposit starts part-shrunk, too
 * short and it sits at full size for a while before it starts ageing. Both are
 * honest pictures of a thing that is on its way out.
 */
export const PROP_LIFE_MS = 10 * 60 * 1_000;

/**
 * How big to draw an entity that is on its way out, as a fraction of its size.
 *
 * 1 for anything with no expiry, which is every person, every sleeper and every
 * NPC — so this is safe to apply to the whole yard without first asking what
 * anything is, and there is no branch anywhere that has to know which entities
 * are things.
 *
 * INTERPOLATED HERE RATHER THAN SENT. The server could publish a size on every
 * frame and it would be strictly worse: a number that changes five times a
 * second for every deposit in the yard, on the one frame whose repetition is
 * what makes it cheap, to say something both ends can work out from one
 * timestamp. The same division of labour as the stat bars, where a rate crosses
 * the wire and the browser does the arithmetic.
 *
 * `now` is the SERVER's clock as the view tracks it (skew-corrected, see
 * `skewMs`), not `Date.now()`. A phone whose clock is a minute fast would
 * otherwise draw every deposit in the yard a minute nearer death than it is, and
 * one that is far enough out would draw them all at nothing — the yard would
 * simply have no litter in it, on that device alone.
 */
export function propScale(expires: number | undefined, now: number): number {
  if (expires === undefined || !Number.isFinite(now)) return 1;
  const remaining = expires * 1_000 - now;
  if (!(remaining > 0)) return 0;
  return Math.min(1, remaining / PROP_LIFE_MS);
}

/**
 * Reads the appearance of every entity in a frame.
 *
 * Filtered through the SAME renderability guard the positions use, so the keyed
 * list and the head count can never disagree about who is standing in the yard —
 * one frame, one notion of who is on it.
 *
 * Every field falls back rather than dropping the entity. An art key this client
 * has never heard of, a pose it cannot draw, a missing field from a server
 * halfway through a deploy: all of them are still somebody standing in the yard,
 * and making them invisible would be a worse answer than drawing a placeholder.
 */
export function readAppearances(peers: readonly unknown[]): readonly PeerAppearance[] {
  const out: PeerAppearance[] = [];
  for (const peer of peers) {
    if (!isRenderablePosition(peer)) continue;
    // The same object, read again untyped: the guard above narrowed it to the
    // positional shape, which deliberately does not describe these fields.
    const raw = peer as unknown as Record<string, unknown>;
    const label = capLabel(raw.label);
    const say = capSay(raw.say);
    const expiresAt = readExpires(raw.expires);
    const appearance: PeerAppearance = {
      id: peer.id,
      art: typeof raw.art === 'string' ? raw.art : '',
      pose:
        typeof raw.pose === 'string' && POSES.includes(raw.pose)
          ? (raw.pose as PeerPose)
          : 'fine',
    };
    if (label !== undefined) appearance.label = label;
    if (say !== undefined) appearance.say = say;
    // Read with the same "absent, never zero" discipline the two strings above
    // use, and for a sharper reason: absence is what tells a thing on the ground
    // from a person standing on it, so a 0 read as an expiry would draw every
    // Ваня in the yard as a deposit that had already run out. `omitempty` on the
    // Go side means an entity that does not expire sends no field at all, and a
    // server that sent one anyway would be sending 0 — so both shapes have to
    // mean the same thing here.
    if (expiresAt !== undefined) appearance.expires = expiresAt;
    out.push(appearance);
  }
  return Object.freeze(out);
}

/**
 * Do these two frames describe entities that LOOK the same?
 *
 * Order-insensitive for the same reason sameIds is: the server builds the roster
 * by iterating a Go map, so two frames describing an identical world arrive in
 * different orders, and an order-sensitive comparison would report a change five
 * times a second — which is precisely the re-render this guard exists to prevent.
 */
export function sameAppearance(
  a: readonly PeerAppearance[],
  b: readonly PeerAppearance[],
): boolean {
  if (a.length !== b.length) return false;
  const before = new Map(a.map((look) => [look.id, look]));
  for (const look of b) {
    const was = before.get(look.id);
    if (!was) return false;
    if (was.art !== look.art || was.label !== look.label || was.pose !== look.pose) return false;
    // Compared like the rest, and it has to be: a line is on the wire for a few
    // seconds and then gone, so it is the ONE appearance field that reliably
    // changes twice a minute. Left out of this comparison it would be read from
    // a frame that happened to change something else, which in a quiet yard is
    // never — the balloon would simply not appear.
    if (was.say !== look.say) return false;
    // Compared, and it costs nothing to compare, which is the entire reason an
    // ABSOLUTE instant is on the wire instead of a countdown. An expiry is fixed
    // for the life of the entity that has one, so this test is false on the
    // frame a deposit arrives and true on every one of the three thousand frames
    // after it; a "seconds left" field in its place would differ on every single
    // tick and re-render the whole yard five times a second, which is the exact
    // cost this guard exists to avoid.
    if (was.expires !== look.expires) return false;
    // There is nothing here about a picture of a person, and there must not be:
    // a face is fetched by id rather than sent, so it is not on either side of
    // this comparison and cannot change between two frames. What DOES change —
    // an entity arriving, leaving, or having its picture fail to load — is a
    // change of membership or of this screen's own state, and both of those
    // reach the DOM without a frame having to differ.
  }
  return true;
}

/**
 * How many PEOPLE are in the yard, as the server counted them.
 *
 * Not `peers.length`, which stopped being the answer the moment the roster
 * started carrying NPCs and sleeping Vanyas as well. The count is published
 * precisely so this client does not have to tell a person from a character:
 * doing that here would mean the browser holding a copy of who is real, and a
 * cast added on the server would silently start inflating the head count on
 * every client that had not been redeployed.
 *
 * Falls back to the number of entities when the field is missing, which is
 * exactly right for the server that omits it: one that does not send `here` is
 * one that has no NPCs and no sleepers either, so every entity in its frame IS
 * a person.
 */
export function readHere(raw: unknown, entities: number): number {
  if (typeof raw !== 'number' || !Number.isInteger(raw) || raw < 0) return entities;
  return raw;
}

/**
 * Which key hunt is running, as the frame states it — or the empty string when
 * none is.
 *
 * The field is STATE AND NOT AN ANNOUNCEMENT, which is the whole reason it rides
 * the roster rather than arriving as an event of its own: a one-shot «ключи
 * снова потерялись» is exactly what somebody who opened the app thirty seconds
 * too late never sees, whereas an id repeated five times a second means that
 * joining at any instant shows the world as it is and a hunt already in progress
 * can be taken part in.
 *
 * Anything that is not a string reads as no hunt — which is the honest reading of
 * a server too old to send the field, and of a frame that omits it because the
 * hunt has just been won and the replacement has not been published yet.
 */
export function readHunt(raw: unknown): string {
  return typeof raw === 'string' ? raw : '';
}

/**
 * Is there a fresh hunt to be pleased about — i.e. has the flavour line earned
 * its place on the screen?
 *
 * WHAT IT REFUSES TO SAY YES TO IS THE POINT. Seeing a hunt for the first time is
 * not news: a player who arrives after somebody else has found the keys is simply
 * standing in a world where a hunt is running, and telling him it has just
 * started would be announcing an event that happened before he was here. So an
 * announcement needs BOTH ends of a transition — a hunt this screen was already
 * watching, and a different one now — and the id changing is precisely that
 * transition and the only thing that is.
 *
 * A hunt ending to nothing is silent for the same reason, and it is worth stating
 * because it looks like a change: the winning claim exhausts the old key and
 * inserts its replacement in one statement, so an empty value is a gap between
 * two frames rather than a state a player ever sits in. Announcing it would put
 * the line up at the moment the keys were FOUND, which is the opposite of what it
 * says.
 */
export function huntRestarted(before: string, now: string): boolean {
  return !!before && !!now && before !== now;
}

// ---------------------------------------------------------------------------
// The beer store, and the one rule in this game that is about WHERE somebody is
// standing.
//
// It is the second thing on a roster frame that is state about the world rather
// than about an entity — the hunt id above is the first — and it is read the
// same way: through a guard, tolerant of every shape a server halfway through a
// deploy can send, and never trusted to be well-formed because it came over a
// socket.
//
// WHAT IS NOT HERE IS THE POINT. There is no kind key anywhere in this module,
// no list of what a crate looks like, and no way for the browser to tell which
// `obj-` entity is the store: it is TOLD where the store is, as a place and a
// count, rather than being asked to find it. Which is what keeps a new kind of
// thing on the ground a backend-only change (ADR-028), and it is why the gate
// below tests the PRESENCE of a verb's `needs_near` and never its value.
// ---------------------------------------------------------------------------

/** A place on the plane, in the same normalised 0..1 coordinates everything uses. */
interface Place {
  x: number;
  y: number;
}

/** Is this something we can measure a distance to? */
function isPlace(value: unknown): value is Place {
  if (typeof value !== 'object' || value === null) return false;
  const p = value as Record<string, unknown>;
  return typeof p.x === 'number' && Number.isFinite(p.x) && typeof p.y === 'number' && Number.isFinite(p.y);
}

/**
 * Reads the store block off a roster frame, or reports that the yard has none.
 *
 * A store with an unreadable PLACE is no store at all, because the place is what
 * the whole block is for: a count with nowhere to walk to could only grey a
 * button and never explain it.
 *
 * A store with an unreadable COUNT reads as EMPTY rather than as absent, and the
 * asymmetry is deliberate. Both greys the button, so the difference is only what
 * the player is told — and «в ящике: 0» beside a crate he can see is a truer
 * account of a client that cannot read the number than silently pretending the
 * yard has no crate in it. It is also the safe way round: the server refuses the
 * draw regardless, so the worst case is a drink that has to be waited for rather
 * than one that is offered and cannot be poured.
 */
export function readStore(raw: unknown): VanyagotchiStore | undefined {
  if (!isPlace(raw)) return undefined;
  const left = (raw as unknown as Record<string, unknown>).left;
  return {
    x: raw.x,
    y: raw.y,
    left: typeof left === 'number' && Number.isFinite(left) ? Math.max(0, Math.trunc(left)) : 0,
  };
}

/**
 * Do these two frames describe the same store?
 *
 * The guard that keeps the store out of the five-times-a-second re-render, and
 * it is the same discipline `sameIds` and `sameAppearance` follow — with a much
 * easier job, because a store is genuinely discrete state: the crate does not
 * move at all, and its count changes a few times an evening. So this is true on
 * essentially every frame, which is exactly what makes a `ref` the right place
 * to keep it.
 *
 * Two absent stores are the same store: a yard with no crate in it, frame after
 * frame, must not look like a change.
 */
export function sameStore(
  a: VanyagotchiStore | undefined,
  b: VanyagotchiStore | undefined,
): boolean {
  if (a === b) return true;
  if (!a || !b) return false;
  return a.x === b.x && a.y === b.y && a.left === b.left;
}

/**
 * Is he standing close enough to something to use a verb gated on it?
 *
 * THE SAME ARITHMETIC THE SERVER DOES, deliberately — plain Euclidean distance
 * in the plane's normalised coordinates, compared with `<=` — so that the button
 * this greys and the verb the server refuses turn on one number rather than two
 * that agree until they do not. See `beside` in internal/gamevanyagotchi/world.go;
 * `within` comes off the served catalogue for the same reason (`arrive_within`),
 * so a retune moves both ends at once.
 *
 * It is also why THIS gate is enforced in the browser at all when the pet's own
 * `needs_stat` gate deliberately is not: a stat is interpolated here and read
 * there, so the two ends hold different numbers between frames, whereas a
 * position is not interpolated by this client at all — the frame states it.
 *
 * FALSE FOR ANYTHING IT CANNOT READ, and each of those is a real shape rather
 * than a defensive flourish. No `here` is a client that has not had its hello
 * answered yet, so it does not know which entity it is: it cannot be beside
 * anything, and a fresh Ваня spawns across the yard from the crate anyway. No
 * `there` is a yard with no crate. An unusable `within` is a server that shipped
 * a gate without the threshold it turns on — impossible from the catalogue,
 * which carries both in one struct, and possible from a fixture that stubs one
 * of them. Guessing a threshold would be worse than greying: it would grey at a
 * distance nothing else in the system agrees with.
 */
export function beside(
  here: Place | undefined,
  there: Place | undefined,
  within: number | undefined,
): boolean {
  if (!isPlace(here) || !isPlace(there)) return false;
  if (typeof within !== 'number' || !Number.isFinite(within) || within < 0) return false;
  return Math.hypot(there.x - here.x, there.y - here.y) <= within;
}

/**
 * Is this verb unpressable right now because of where he is standing — or
 * because there is nothing left to press it on?
 *
 * THE PRESENCE OF `needs_near` IS TESTED AND ITS VALUE IS NEVER LOOKED AT, and
 * that is the load-bearing line in this function. The client holds no kind keys:
 * it does not know that a crate is called `beer_crate`, cannot tell which entity
 * in the yard is one, and must not learn either — the server publishes the store
 * as a PLACE precisely so that the browser can gate a button without holding any
 * content at all. Comparing `needs_near` to a string here would put the
 * catalogue in the SPA and make a new gated verb a client deploy (ADR-028).
 *
 * Three reasons, and the caller can tell them apart because it holds all three
 * inputs: no crate in the yard, a crate with nothing in it, and a crate he is
 * not standing at. They want opposite things from the player — wait, versus walk
 * over — which is why the server answers them with two different lines
 * («пиво кончилось» and «далековато») and why the screen says which one it is
 * rather than only greying the button.
 *
 * A COURTESY, NEVER A RULE. The server enforces all three regardless, so the
 * worst this can be is unkind: an ungreyed button that is refused, or a greyed
 * one that would have been accepted. That is what makes it safe for it to fail
 * closed on a threshold it cannot read.
 */
export function outOfReach(
  action: { needs_near?: string } | null | undefined,
  store: VanyagotchiStore | undefined,
  atStore: boolean,
): boolean {
  if (!action?.needs_near) return false;
  if (!store) return true;
  if (store.left <= 0) return true;
  return !atStore;
}

/**
 * The one line the status row says about the beer store, or nothing when the
 * yard has no crate.
 *
 * A GREYED BUTTON IS NOT AN EXPLANATION, and this is the explanation. The three
 * states `outOfReach` collapses into one boolean are three different
 * instructions to the player, and they are the same three the server answers
 * with when the button is pressed anyway: nothing left («пиво кончилось» — wait),
 * too far («далековато» — walk over), and ready. A screen that greyed the button
 * without saying which of them applied would leave him staring at a dead control
 * with no idea whether the problem was his feet or the crate.
 *
 * The count is stated even when he cannot reach it, because that is the half he
 * is deciding on: six left is worth crossing the yard for and one is a race he
 * has probably already lost.
 *
 * NOTHING HERE KNOWS WHAT IS IN THE CRATE. The emoji is the drink's, not the
 * kind's, because the client holds no object kinds — see the note at the head of
 * this section. If the store ever stops selling beer this line is wrong, and
 * that is the honest trade for a browser that cannot be told what a crate is.
 */
export function storeLabel(store: VanyagotchiStore | undefined, atStore: boolean): string | null {
  if (!store) return null;
  // Never reached from a well-formed frame — the draw that empties a crate
  // exhausts it and stands a fresh one up in the same transaction, so the server
  // publishes an absent store rather than a nought. It is here because
  // `readStore` deliberately reads an UNREADABLE count as nought, and because a
  // count on a frame is always a moment out of date.
  if (store.left <= 0) return '🍺 ящик пуст';
  // Deliberately terse. It is the THIRD item in a status row that must not wrap
  // and must not overflow at 320px — the width that has broken this screen
  // before — so the sentence is as short as it can be while still saying which
  // of the three states applies. «дойди» is the whole instruction: the yard is
  // already showing him the crate, and the button lighting up is how he learns
  // he has arrived.
  if (!atStore) return `🍺 ящик: ${store.left} — дойди`;
  return `🍺 ящик: ${store.left}`;
}

/**
 * What to draw for an art key the catalogue does not describe.
 *
 * A silhouette rather than a question mark or a blank: the entity is real and is
 * standing there, and the only thing missing is this client's idea of what it
 * looks like. Rendering nothing would make a player invisible to everybody who
 * had not reloaded since the skin was added, which is the deploy coupling the
 * key-on-the-wire design is bought to avoid.
 */
export const UNKNOWN_ART = '👤';

/** One entity's art, resolved against the catalogue and ready to draw. */
export interface ResolvedArt {
  /** The glyph to draw when there is no sprite. Never empty. */
  emoji: string;
  /** A sprite URL. Wins over the emoji whenever the catalogue has one. */
  image?: string;
}

/**
 * Resolves a skin key against the catalogue the screen already fetched.
 *
 * Falls back at every step — no catalogue yet (it is fetched over HTTP and the
 * plane runs on the socket, so the yard can be populated before it lands), no
 * such key, or a skin carrying neither picture nor emoji. Each of those is a
 * placeholder, never an empty dot.
 *
 * The skin's `gradient` is deliberately not used here: a 44px circle has room
 * for one background, and it already carries the entity's own colour, which is
 * the thing that differs between two players wearing the same skin. The gradient
 * is for a surface that shows ONE pet large.
 */
export function resolveArt(
  skins: readonly VanyagotchiSkin[] | undefined,
  art: string,
): ResolvedArt {
  const skin = skins?.find((s) => s.key === art);
  if (!skin) return { emoji: UNKNOWN_ART };
  const resolved: ResolvedArt = { emoji: skin.emoji || UNKNOWN_ART };
  if (skin.image) resolved.image = skin.image;
  return resolved;
}
