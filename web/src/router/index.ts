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
      { path: '', redirect: { name: 'wishlist' } },
      {
        path: 'wishlist',
        name: 'wishlist',
        component: () => import('../views/WishlistView.vue'),
      },
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

  if (requiresApproved) {
    if (!auth.isAuthed) return { name: 'landing' };
    if (!auth.isApproved) return { name: 'pending' };
  }
  if (requiresAdmin && !auth.isAdmin) {
    return { name: 'wishlist' };
  }

  // Already signed in + approved? Skip the landing, go straight to the app.
  if (to.name === 'landing' && auth.isAuthed && auth.isApproved) {
    return { name: 'wishlist' };
  }

  return true;
});

export default router;
