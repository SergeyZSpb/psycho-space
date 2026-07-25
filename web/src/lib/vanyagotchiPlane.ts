import type { VanyagotchiSkin } from '../api/types';

// Applying positions to the plane, deliberately outside Vue.
//
// Positions arrive five times a second and must not enter reactivity: bound to
// a `:style`, each frame would become a scheduler pass plus one vdom patch per
// entity, which at twenty entities is on the order of a thousand patches a
// second to produce a transform the compositor could have been handed straight
// away. So a frame is applied by writing two CSS custom properties per element
// and nothing else.
//
// Custom properties rather than writing `transform` directly, for one concrete
// reason: the mapping from normalised 0..1 coordinates to pixels then lives once,
// in the stylesheet, against the plane's own box. There is no measured size
// cached in JavaScript to invalidate — which matters here because the plane
// resizes whenever mobile browser chrome slides in or out. It also composes with
// anything else the stylesheet wants to do to the same element.

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

/** Writes one entity's position onto its element. */
export function applyPosition(el: StyleTarget, x: number, y: number): void {
  el.style.setProperty(X_PROPERTY, String(x));
  el.style.setProperty(Y_PROPERTY, String(y));
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
export type PeerPose = 'fine' | 'poorly' | 'dead';

/** The poses this client knows how to draw. Anything else falls back to fine. */
const POSES: readonly string[] = ['fine', 'poorly', 'dead'];

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
 *
 * Cut by code point rather than by `slice`, because slicing a UTF-16 string in
 * the middle of a surrogate pair leaves a lone half that renders as a replacement
 * character — and a name is exactly the field somebody puts an emoji in.
 */
export function capLabel(raw: unknown): string | undefined {
  if (typeof raw !== 'string') return undefined;
  const trimmed = raw.trim();
  if (!trimmed) return undefined;
  const chars = [...trimmed];
  if (chars.length <= LABEL_MAX) return trimmed;
  return `${chars.slice(0, LABEL_MAX - 1).join('')}…`;
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
    const appearance: PeerAppearance = {
      id: peer.id,
      art: typeof raw.art === 'string' ? raw.art : '',
      pose:
        typeof raw.pose === 'string' && POSES.includes(raw.pose)
          ? (raw.pose as PeerPose)
          : 'fine',
    };
    if (label !== undefined) appearance.label = label;
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
  }
  return true;
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
