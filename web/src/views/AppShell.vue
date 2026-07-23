<template>
  <div>
    <v-navigation-drawer v-model="drawer" :permanent="mdAndUp" :temporary="!mdAndUp">
      <v-list nav density="comfortable">
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
      <v-app-bar-nav-icon v-if="!mdAndUp" @click="drawer = !drawer" />
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
import { computed, ref } from 'vue';
import { useDisplay } from 'vuetify';
import { useRouter } from 'vue-router';
import ThemeToggle from '../components/ThemeToggle.vue';
import { useAuthStore } from '../stores/auth';
import { useErrorStore } from '../stores/error';

const auth = useAuthStore();
const errorStore = useErrorStore();
const router = useRouter();
const { mdAndUp } = useDisplay();

const drawer = ref(true);

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
