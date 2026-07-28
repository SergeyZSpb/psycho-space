<template>
  <PublicLayout>
    <v-container class="py-16 text-center">
      <v-progress-circular v-if="busy" indeterminate color="primary" size="48" />
      <p class="mt-4 text-medium-emphasis" data-testid="auth-redirect-message">{{ message }}</p>
      <v-btn v-if="!busy" class="mt-6" color="primary" variant="tonal" :to="{ name: 'landing' }">
        на главную
      </v-btn>
    </v-container>
  </PublicLayout>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { useRoute } from 'vue-router';
import PublicLayout from '../components/layout/PublicLayout.vue';
import { useOAuthLogin } from '../composables/useOAuthLogin';
import { useErrorStore } from '../stores/error';

// Both providers' landing page. Yandex ALWAYS ends up here — a plain OAuth
// redirect is its only mode — while most VK logins never reach it at all,
// because the OneTap widget finishes in place. Either way this is the only code
// that can finish a login that navigated.
//
// THE PROVIDER COMES FROM THE ROUTE, deliberately: `/auth/redirect` is VK's and
// `/auth/yandex/redirect` is Yandex's, each pinning its provider in `meta`. A
// `?provider=` query parameter would let whoever wrote the URL choose which
// backend endpoint an authorization code is posted to, and the query is exactly
// the part of this URL that arrives from outside.
const route = useRoute();
const provider = route.meta.provider ?? 'vk';
const { completeRedirect } = useOAuthLogin(provider);
const errorStore = useErrorStore();

const busy = ref(true);
const message = ref('заканчиваем вход…');

onMounted(async () => {
  try {
    // A returned sentence means the trip itself cannot be completed — cancelled
    // at the provider, a truncated return URL, a verifier left in another tab.
    // Those are outcomes, not failures: show them and offer the way back,
    // without the trace-id modal that a real error deserves.
    const problem = await completeRedirect(route.query);
    if (problem) {
      busy.value = false;
      message.value = problem;
    }
  } catch (err) {
    busy.value = false;
    message.value = 'не удалось завершить вход';
    errorStore.report(err);
  }
});
</script>
