import { defineStore } from 'pinia';
import { computed, ref } from 'vue';
import { authApi } from '../api/endpoints';
import { ApiError } from '../api/client';
import type { Account } from '../api/types';

// Holds the current account and gates the router. `loaded` guarantees we only
// hit /api/auth/me once before the first navigation resolves.
export const useAuthStore = defineStore('auth', () => {
  const account = ref<Account | null>(null);
  const loaded = ref(false);

  const isAuthed = computed(() => account.value !== null);
  const isApproved = computed(() => account.value?.status === 'approved');
  const isAdmin = computed(
    () => account.value?.role === 'admin' || account.value?.role === 'superadmin',
  );
  const isSuperadmin = computed(() => account.value?.role === 'superadmin');

  function setAccount(acc: Account | null) {
    account.value = acc;
  }

  // Resolve the session once. A 401 simply means "not logged in" — expected, and
  // never surfaced as an error. Other failures leave the user logged-out too but
  // are swallowed here (the landing must still render if the API hiccups).
  async function ensureLoaded(): Promise<void> {
    if (loaded.value) return;
    try {
      const res = await authApi.me();
      account.value = res.account;
    } catch (err) {
      if (!(err instanceof ApiError) || err.status !== 401) {
        // Non-401: leave unauthenticated; don't spam the modal on first paint.
        console.warn('auth: /me failed', err);
      }
      account.value = null;
    } finally {
      loaded.value = true;
    }
  }

  // Force a re-fetch of /me (used by the pending-screen poll and after login).
  // Returns the current account (or null on 401). A 401 clears the session and
  // is treated as "logged out"; other errors leave the last-known account intact.
  async function refresh(): Promise<Account | null> {
    try {
      const res = await authApi.me();
      account.value = res.account;
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        account.value = null;
      } else {
        console.warn('auth: /me refresh failed', err);
      }
    } finally {
      loaded.value = true;
    }
    return account.value;
  }

  async function logout(): Promise<void> {
    try {
      await authApi.logout();
    } finally {
      account.value = null;
    }
  }

  return {
    account,
    loaded,
    isAuthed,
    isApproved,
    isAdmin,
    isSuperadmin,
    setAccount,
    ensureLoaded,
    refresh,
    logout,
  };
});
