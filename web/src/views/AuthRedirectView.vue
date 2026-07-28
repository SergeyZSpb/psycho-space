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
import { useVkLogin } from '../composables/useVkLogin';
import { useErrorStore } from '../stores/error';

// The VK redirect target. Most logins never reach this page — the OneTap widget
// finishes in place — but every login that falls back to a full-page redirect
// lands here, and this is the only code that can finish it.
const route = useRoute();
const { completeRedirect } = useVkLogin();
const errorStore = useErrorStore();

const busy = ref(true);
const message = ref('заканчиваем вход…');

onMounted(async () => {
  try {
    // A returned sentence means the trip itself cannot be completed — cancelled
    // at VK, a truncated return URL, a verifier left in another tab. Those are
    // outcomes, not failures: show them and offer the way back, without the
    // trace-id modal that a real error deserves.
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
