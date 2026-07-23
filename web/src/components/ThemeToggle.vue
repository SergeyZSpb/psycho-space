<template>
  <v-btn
    :icon="themeStore.isDark() ? 'mdi-weather-night' : 'mdi-white-balance-sunny'"
    variant="text"
    :aria-label="themeStore.isDark() ? 'Светлая тема' : 'Тёмная тема'"
    :title="themeStore.isDark() ? 'Светлая тема' : 'Тёмная тема'"
    @click="themeStore.toggle()"
  />
</template>

<script setup lang="ts">
import { useThemeStore } from '../stores/theme';

const themeStore = useThemeStore();
</script>
