<template>
  <div>
    <v-navigation-drawer v-model="drawer" :permanent="mdAndUp" :temporary="!mdAndUp">
      <v-list nav density="comfortable">
        <v-list-item
          :to="{ name: 'game-vanyagotchi' }"
          prepend-icon="mdi-account-group-outline"
          title="Ванягоччи"
          value="game-vanyagotchi"
        />
        <v-list-item
          :to="{ name: 'game-khimki' }"
          prepend-icon="mdi-controller-classic-outline"
          title="Смолтолк в Химках"
          value="game-khimki"
        />
        <v-list-item
          :to="{ name: 'wishlist' }"
          prepend-icon="mdi-lightbulb-on-outline"
          title="Вишлист"
          value="wishlist"
        />
        <v-list-item
          v-if="auth.isAdmin"
          :to="{ name: 'admin' }"
          prepend-icon="mdi-shield-account-outline"
          title="Админка"
          value="admin"
        />

        <v-divider class="my-2" />

        <!-- Signals more sections are coming. -->
        <v-list-item
          disabled
          prepend-icon="mdi-dots-horizontal"
          title="скоро больше разделов"
          class="text-disabled"
        />
      </v-list>
    </v-navigation-drawer>

    <v-app-bar flat color="surface" density="comfortable">
      <v-app-bar-nav-icon v-if="!mdAndUp" @click="toggleDrawer" />
      <v-app-bar-title>
        <span class="brand">психоспасе</span>
      </v-app-bar-title>

      <template #append>
        <ThemeToggle />

        <div class="d-flex align-center ga-2 ml-2">
          <v-avatar size="32" color="secondary">
            <v-img v-if="auth.account?.avatar_url" :src="auth.account.avatar_url" alt="" />
            <span v-else class="text-caption">{{ initials }}</span>
          </v-avatar>
          <span class="text-body-2 d-none d-sm-inline">{{ auth.account?.display_name }}</span>
        </div>

        <v-btn
          icon="mdi-logout"
          variant="text"
          title="Выйти"
          aria-label="Выйти"
          class="ml-1"
          @click="doLogout"
        />
      </template>
    </v-app-bar>

    <v-main>
      <router-view />
    </v-main>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useDisplay } from 'vuetify';
import { useRouter } from 'vue-router';
import ThemeToggle from '../components/ThemeToggle.vue';
import { DRAWER_PEEK_MS, prefersReducedMotion, shouldPeekDrawer } from '../lib/drawerPeek';
import { useAuthStore } from '../stores/auth';
import { useErrorStore } from '../stores/error';

const auth = useAuthStore();
const errorStore = useErrorStore();
const router = useRouter();
const { mdAndUp } = useDisplay();

// Permanent + open on desktop; on mobile/tablet it's a temporary drawer that
// starts closed (opened via the app-bar nav icon) so it never overlays content.
const drawer = ref(mdAndUp.value);

// --- one-off drawer peek ------------------------------------------------------
// On a full page load of the shell the drawer opens itself, holds for
// DRAWER_PEEK_MS, then closes: people who arrive on a deep link would otherwise
// never learn that the nav — and the sections and games being added to it —
// exists behind the app-bar icon.
//
// This shell is the parent route component for every /app/* route, so it mounts
// once per page load and stays mounted across client-side navigation: hanging
// the peek off onMounted is what makes it fire exactly once, and never again on
// a route change.

// The pending auto-close, and whether the peek still owns the drawer.
let peekTimer: ReturnType<typeof setTimeout> | undefined;
let peeking = false;

// Drop the pending auto-close. Called on unmount and whenever the user takes the
// drawer over, so a stale timer can never close the drawer against them.
function endPeek() {
  peeking = false;
  if (peekTimer !== undefined) {
    clearTimeout(peekTimer);
    peekTimer = undefined;
  }
}

onMounted(() => {
  // Nothing to reveal where the drawer is permanent, and no unrequested motion
  // for a user who has asked for none.
  if (!shouldPeekDrawer({ temporary: !mdAndUp.value, reducedMotion: prefersReducedMotion() })) {
    return;
  }
  peeking = true;
  drawer.value = true;
  peekTimer = setTimeout(() => {
    peekTimer = undefined;
    if (!peeking) return;
    peeking = false;
    drawer.value = false;
  }, DRAWER_PEEK_MS);
});

// No leaked timers: the peek dies with the component.
onBeforeUnmount(endPeek);

// Any close while the peek is running came from someone else — the user tapping
// the scrim or pressing Escape, or Vuetify closing the drawer on a route change.
// Hand the drawer over and drop the pending auto-close rather than fighting it.
watch(drawer, (open) => {
  if (peeking && !open) endPeek();
});

// The app-bar icon: an explicit user toggle always wins over the peek.
function toggleDrawer() {
  endPeek();
  drawer.value = !drawer.value;
}
// ------------------------------------------------------------------------------

const initials = computed(() => {
  const name = auth.account?.display_name?.trim() ?? '';
  return name ? name.charAt(0).toUpperCase() : '?';
});

async function doLogout() {
  try {
    await auth.logout();
  } catch (err) {
    errorStore.report(err);
  } finally {
    await router.push({ name: 'landing' });
  }
}
</script>

<style scoped>
.brand {
  color: rgb(var(--v-theme-primary));
  font-weight: 700;
  letter-spacing: 0.5px;
}
</style>
