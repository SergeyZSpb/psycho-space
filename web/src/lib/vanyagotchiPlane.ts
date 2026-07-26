import type { VanyagotchiSkin } from '../api/types';

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
 * THE FIRST ENTRY IS 1 ON PURPOSE, and it is what keeps the mobile suite's 44 px
 * tap-target floor satisfied without anybody having to redo the arithmetic:
 * depth can only ever make an entity BIGGER than its unscaled size, so the floor
 * is the CSS size itself (44 px, `.peer` in GameVanyagotchiView.vue) rather than
 * a product of two numbers that could drift apart. Scaling the far band DOWN
 * instead would have meant every future change to either number re-deriving
 * whether the smallest entity was still tappable.
 *
 * 8% a band: enough that two entities a band apart read as at different
 * distances, small enough that the jump at a boundary is not a pop.
 */
export const DEPTH_SCALES: readonly number[] = [1, 1.08, 1.16, 1.24];

/**
 * The SMALLEST an entity is ever drawn, in CSS pixels.
 *
 * A floor, not the size. `.peer` is `var(--unit)` and the unit is
 * `clamp(44px, 13cqw, 88px)`, so an entity grows with the yard and this is the
 * bottom of that clamp — which is what a 360px phone draws and what every wider
 * screen used to draw as well, before the plane started scaling and the dot did
 * not.
 *
 * It mirrors the stylesheet rather than driving it: the stylesheet owns the
 * size, and this exists so the floor can be asserted in a unit test. The e2e
 * suite measures the real box at mobile widths, so the two cannot drift apart
 * silently.
 *
 * The floor is LEGIBILITY rather than accessibility, which is worth stating
 * because the tests around it are older than the distinction: `.peer` is
 * `pointer-events: none` and the plane takes every tap, so a dot has never been
 * a tap target. It stays 44 while `DEPTH_SCALES[0] === 1` keeps depth from ever
 * shrinking anybody; the day a far band draws smaller than a near one, this
 * becomes `base * minScale` and the assertions naming it have to be re-argued.
 */
export const PEER_BASE_PX = 44;

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
 * The balloon hangs off the top of a 44 px dot that is centred on its own
 * coordinates, so it needs roughly 45 px of plane above that point; on the
 * shortest plane we support (about 308 px tall, at 320x568) that is 0.145 of the
 * height. Normalised rather than measured, deliberately — measuring would mean
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
 */
export type PeerPose = 'fine' | 'poorly' | 'dead' | 'asleep';

/** The poses this client knows how to draw. Anything else falls back to fine. */
const POSES: readonly string[] = ['fine', 'poorly', 'dead', 'asleep'];

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
