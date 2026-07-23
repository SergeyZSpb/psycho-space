<template>
  <div class="d-flex flex-column min-h-screen">
    <v-app-bar flat color="surface" density="comfortable">
      <v-app-bar-title>
        <router-link to="/" class="brand">психоспасе</router-link>
      </v-app-bar-title>
      <template #append>
        <ThemeToggle />
      </template>
    </v-app-bar>

    <v-main class="flex-grow-1">
      <slot />
    </v-main>

    <AppFooter />
    <CookieConsent />
  </div>
</template>

<script setup lang="ts">
import ThemeToggle from '../ThemeToggle.vue';
import AppFooter from '../AppFooter.vue';
import CookieConsent from '../CookieConsent.vue';
</script>

<style scoped>
.min-h-screen {
  min-height: 100vh;
}
.brand {
  color: rgb(var(--v-theme-primary));
  font-weight: 700;
  text-decoration: none;
  letter-spacing: 0.5px;
}
</style>
