<template>
  <PublicLayout>
    <v-container class="py-16 text-center">
      <v-progress-circular v-if="busy" indeterminate color="primary" size="48" />
      <p class="mt-4 text-medium-emphasis">{{ message }}</p>
    </v-container>
  </PublicLayout>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { useRoute } from 'vue-router';
import PublicLayout from '../components/layout/PublicLayout.vue';
import { useVkLogin } from '../composables/useVkLogin';
import { useErrorStore } from '../stores/error';

// VK redirect-mode fallback. The primary flow is the OneTap Callback (no
// navigation ever reaches here); this only runs if VK falls back to a redirect.
const route = useRoute();
const { completeRedirect } = useVkLogin();
const errorStore = useErrorStore();

const busy = ref(true);
const message = ref('заканчиваем вход…');

onMounted(async () => {
  try {
    await completeRedirect(route.query);
  } catch (err) {
    busy.value = false;
    message.value = 'не удалось завершить вход';
    errorStore.report(err);
  }
});
</script>
