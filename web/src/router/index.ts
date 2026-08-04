import { createRouter, createWebHistory } from 'vue-router';
import type { RouteRecordRaw } from 'vue-router';
import { useAuthStore } from '../stores/auth';
import {
  HOME_ROUTE_NAME,
  VK_REDIRECT_PATH,
  YANDEX_REDIRECT_PATH,
  type OAuthProvider,
} from '../constants';

/**
 * What a route may carry in `meta`.
 *
 * Declared rather than left implicit because `provider` is read to decide which
 * backend endpoint an authorization code is posted to: a value typed `unknown`
 * would be one cast away from being whatever a caller felt like.
 */
declare module 'vue-router' {
  interface RouteMeta {
    requiresApproved?: boolean;
    requiresAdmin?: boolean;
    /** Set on the two OAuth landing pages; the provider they finish a login for. */
    provider?: OAuthProvider;
  }
}

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'landing',
    component: () => import('../views/LandingView.vue'),
  },
  {
    // VK redirect-mode fallback. The primary flow is the OneTap Callback (no
    // navigation); this route only matters if VK falls back to a redirect.
    //
    // The path cannot move: it is registered on the VK application, and the
    // backend echoes it byte for byte at the token exchange. Which is why the
    // SECOND provider got the explicit path rather than this one.
    path: VK_REDIRECT_PATH,
    name: 'auth-redirect',
    component: () => import('../views/AuthRedirectView.vue'),
    meta: { provider: 'vk' },
  },
  {
    // Yandex's landing page. Not a fallback — it is how EVERY Yandex login
    // finishes, because there is no in-page SDK callback for one to fall back
    // from.
    //
    // THE PROVIDER COMES FROM THE ROUTE AND NEVER FROM THE QUERY. A
    // `?provider=` parameter would be attacker-controlled, and this page's
    // whole job is to post an authorization code to a backend endpoint —
    // choosing that endpoint from the same string the code arrived in is
    // choosing it from the attacker. Two routes, one view, and the provider is
    // a fact about which URL was matched.
    path: YANDEX_REDIRECT_PATH,
    name: 'auth-redirect-yandex',
    component: () => import('../views/AuthRedirectView.vue'),
    meta: { provider: 'yandex' },
  },
  {
    path: '/pending',
    name: 'pending',
    component: () => import('../views/PendingView.vue'),
  },
  {
    path: '/privacy',
    name: 'privacy',
    component: () => import('../views/PrivacyView.vue'),
  },
  {
    path: '/consent',
    name: 'consent',
    component: () => import('../views/ConsentView.vue'),
  },
  {
    path: '/app',
    component: () => import('../views/AppShell.vue'),
    meta: { requiresApproved: true },
    children: [
      { path: '', redirect: { name: HOME_ROUTE_NAME } },
      {
        path: 'wishlist',
        name: 'wishlist',
        component: () => import('../views/WishlistView.vue'),
      },
      {
        path: 'game-khimki',
        name: 'game-khimki',
        component: () => import('../views/GameKhimkiView.vue'),
      },
      // The game lived at /app/game until it was renamed for the per-game
      // naming rule. This redirect is permanent, not a migration aid: the old
      // path is in people's history and on their home screens.
      { path: 'game', redirect: { name: 'game-khimki' } },
      {
        path: 'game-vanyagotchi',
        name: 'game-vanyagotchi',
        component: () => import('../views/GameVanyagotchiView.vue'),
      },
      {
        // «ВАНЯДУМ». Lazy like every route, and it matters more here than
        // anywhere else in the app: this view is the only thing that pulls
        // three.js in, so nobody who never opens it pays for the engine.
        path: 'game-vanyadum',
        name: 'game-vanyadum',
        component: () => import('../views/GameVanyadumView.vue'),
      },
      {
        // «СИМУЛЯТОР ФИНТЕХА». A child of the /app shell, which already carries
        // `meta.requiresApproved` — so the game route needs no meta and no
        // guard of its own. Lazy like every route, though this one pulls in
        // nothing heavier than the rest of the SPA: the office is DOM and CSS.
        path: 'game-fintech',
        name: 'game-fintech',
        component: () => import('../views/GameFintechView.vue'),
      },
      {
        path: 'admin',
        name: 'admin',
        component: () => import('../views/AdminView.vue'),
        meta: { requiresAdmin: true },
      },
      {
        // «АДМИН ФИНТЕХА» — the office's own control room.
        //
        // A CHILD OF /app LIKE EVERY OTHER SECTION, and admin-gated by the same
        // `meta.requiresAdmin` the account admin page uses: the guard above
        // already turns a non-admin back to the front door, so there is nothing
        // game-specific to add. It is a route of the GAME rather than of the
        // admin section (ADR-028), which is why it sits here under its own name
        // instead of becoming a tab inside AdminView.
        path: 'game-fintech-admin',
        name: 'game-fintech-admin',
        component: () => import('../views/GameFintechAdminView.vue'),
        meta: { requiresAdmin: true },
      },
    ],
  },
  // Unknown paths bounce to the landing.
  { path: '/:pathMatch(.*)*', redirect: { name: 'landing' } },
];

export const router = createRouter({
  history: createWebHistory('/'),
  routes,
  scrollBehavior() {
    return { top: 0 };
  },
});

// Global guard: resolve the session once, then enforce access.
router.beforeEach(async (to) => {
  const auth = useAuthStore();
  await auth.ensureLoaded();

  const requiresApproved = to.matched.some((r) => r.meta.requiresApproved);
  const requiresAdmin = to.matched.some((r) => r.meta.requiresAdmin);

  // /app/* — approved users only.
  if (requiresApproved) {
    if (!auth.isAuthed) return { name: 'landing' };
    if (!auth.isApproved) return { name: 'pending' };
  }
  if (requiresAdmin && !auth.isAdmin) {
    return { name: HOME_ROUTE_NAME };
  }

  // /pending — must have a session (pending users now have one). Approved users
  // don't belong here; a signed-out user has no handle to show.
  if (to.name === 'pending') {
    if (!auth.isAuthed) return { name: 'landing' };
    if (auth.isApproved) return { name: HOME_ROUTE_NAME };
  }

  // Landing — route a signed-in user by status: approved -> app, else pending.
  if (to.name === 'landing' && auth.isAuthed) {
    return auth.isApproved ? { name: HOME_ROUTE_NAME } : { name: 'pending' };
  }

  return true;
});

export default router;
