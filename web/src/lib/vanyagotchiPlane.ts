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

/** One entity in a roster frame, as the server sends it. */
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
