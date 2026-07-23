<template>
  <v-footer color="surface" class="px-4 py-6">
    <v-container class="pa-0">
      <div class="d-flex flex-wrap align-center justify-space-between ga-2">
        <span class="text-caption text-medium-emphasis">
          © {{ year }} психоспасе · сделано на нейрослопе
        </span>
        <div class="d-flex flex-wrap ga-4">
          <router-link class="footer-link text-caption" :to="{ name: 'privacy' }">
            Политика конфиденциальности
          </router-link>
          <router-link class="footer-link text-caption" :to="{ name: 'consent' }">
            Согласие на обработку ПД
          </router-link>
        </div>
      </div>
    </v-container>
  </v-footer>
</template>

<script setup lang="ts">
const year = new Date().getFullYear();
</script>

<style scoped>
.footer-link {
  color: rgb(var(--v-theme-primary));
  text-decoration: none;
}
.footer-link:hover {
  text-decoration: underline;
}
</style>
