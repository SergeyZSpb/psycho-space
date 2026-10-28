<template>
  <v-snackbar
    v-model="show"
    :timeout="-1"
    location="bottom"
    color="surface"
    class="cookie-snackbar"
    multi-line
  >
    <!-- A NOTICE, not a consent. Only strictly necessary cookies are set (the
         login's state and the session), which the law asks us to explain and
         not to ask permission for — so nothing here may claim that staying on
         the page is agreement to anything. The link says what the page it
         opens is called. -->
    <div class="text-body-2" data-testid="cookie-notice">
      Тут только необходимые куки — чтобы работали вход и сессия, никакой аналитики.
      Подробнее — в
      <router-link class="cookie-link" :to="{ name: 'privacy' }">Политике обработки персональных данных</router-link>.
    </div>
    <template #actions>
      <v-btn variant="tonal" color="primary" @click="dismiss">Понятно</v-btn>
    </template>
  </v-snackbar>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import { LS_COOKIE_NOTICE } from '../constants';

function alreadyDismissed(): boolean {
  try {
    return localStorage.getItem(LS_COOKIE_NOTICE) === '1';
  } catch {
    return false;
  }
}

const show = ref(!alreadyDismissed());

function dismiss() {
  show.value = false;
  try {
    localStorage.setItem(LS_COOKIE_NOTICE, '1');
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
