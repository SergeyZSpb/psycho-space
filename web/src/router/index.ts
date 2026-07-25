import { createRouter, createWebHistory } from 'vue-router';
import type { RouteRecordRaw } from 'vue-router';
import { useAuthStore } from '../stores/auth';

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'landing',
    component: () => import('../views/LandingView.vue'),
  },
  {
    // VK redirect-mode fallback. The primary flow is the OneTap Callback (no
    // navigation); this route only matters if VK falls back to a redirect.
    path: '/auth/redirect',
    name: 'auth-redirect',
    component: () => import('../views/AuthRedirectView.vue'),
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
      { path: '', redirect: { name: 'game-khimki' } },
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
        path: 'admin',
        name: 'admin',
        component: () => import('../views/AdminView.vue'),
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
    return { name: 'game-khimki' };
  }

  // /pending — must have a session (pending users now have one). Approved users
  // don't belong here; a signed-out user has no handle to show.
  if (to.name === 'pending') {
    if (!auth.isAuthed) return { name: 'landing' };
    if (auth.isApproved) return { name: 'game-khimki' };
  }

  // Landing — route a signed-in user by status: approved -> app, else pending.
  if (to.name === 'landing' && auth.isAuthed) {
    return auth.isApproved ? { name: 'game-khimki' } : { name: 'pending' };
  }

  return true;
});

export default router;
