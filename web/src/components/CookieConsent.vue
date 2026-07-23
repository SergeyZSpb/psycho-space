<template>
  <v-snackbar
    v-model="show"
    :timeout="-1"
    location="bottom"
    color="surface"
    class="cookie-snackbar"
    multi-line
  >
    <div class="text-body-2">
      Мы используем куки, чтобы приложулька работала (вход, сессия). Оставаясь тут,
      ты соглашаешься. Подробнее — в
      <router-link class="cookie-link" :to="{ name: 'privacy' }">Политике конфиденциальности</router-link>.
    </div>
    <template #actions>
      <v-btn variant="tonal" color="primary" @click="dismiss">Ок, понятно</v-btn>
    </template>
  </v-snackbar>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import { LS_COOKIE_CONSENT } from '../constants';

function alreadyDismissed(): boolean {
  try {
    return localStorage.getItem(LS_COOKIE_CONSENT) === '1';
  } catch {
    return false;
  }
}

const show = ref(!alreadyDismissed());

function dismiss() {
  show.value = false;
  try {
    localStorage.setItem(LS_COOKIE_CONSENT, '1');
  } catch {
    /* ignore */
  }
}
</script>

<style scoped>
.cookie-link {
  color: rgb(var(--v-theme-primary));
}
</style>
