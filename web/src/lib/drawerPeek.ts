// The navigation drawer's one-off "peek" on app-shell load: the drawer opens by
// itself, holds for a moment, then closes again, so people notice that the nav
// exists and that new sections have appeared in it.
//
// The *whether* of the peek lives here as pure functions, so the rules (only an
// overlay drawer, never under reduced motion) are unit-testable without mounting
// the shell. The *when* — the timer, and cancelling it — belongs to the component
// that owns the drawer, because only it knows when it is being unmounted or
// overruled by the user.

/**
 * How long the drawer stays open before it closes itself, in milliseconds.
 *
 * Vuetify's drawer slide is 200 ms at each end, so this leaves roughly half a
 * second of fully-open drawer: long enough to register as a deliberate reveal
 * and read the section list, short enough to be gone before anyone reaches for
 * it.
 */
export const DRAWER_PEEK_MS = 900;

/** The conditions the peek decision is made from. */
export interface DrawerPeekConditions {
  /**
   * Is the drawer an overlay at the current breakpoint? Where it is permanent
   * the nav is already on screen, so there is nothing to reveal.
   */
  temporary: boolean;
  /** Has the user asked for reduced motion? */
  reducedMotion: boolean;
}

/**
 * Should the app shell peek the drawer on this page load?
 *
 * Only when the drawer is temporary, and never for a user who has asked for
 * reduced motion — an unrequested slide-in is exactly what that preference is
 * about.
 */
export function shouldPeekDrawer(conditions: DrawerPeekConditions): boolean {
  return conditions.temporary && !conditions.reducedMotion;
}

/**
 * Does the browser report `prefers-reduced-motion: reduce`?
 *
 * Guarded so it is safe under jsdom / SSR, where `window.matchMedia` may be
 * missing; an environment that cannot answer is treated as "no preference",
 * which matches how the theme store reads `prefers-color-scheme`.
 */
export function prefersReducedMotion(): boolean {
  return (
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches
  );
}
