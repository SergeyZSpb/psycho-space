<template>
  <v-app>
    <router-view />
    <!-- Mounted once, driven by the error store — every unexpected failure lands here. -->
    <ErrorModal />
  </v-app>
</template>

<script setup lang="ts">
import { watch } from 'vue';
import { useTheme } from 'vuetify';
import { useThemeStore } from './stores/theme';
import ErrorModal from './components/ErrorModal.vue';

// Keep the Vuetify active theme in sync with the persisted theme store.
const theme = useTheme();
const themeStore = useThemeStore();

function apply(name: string) {
  theme.global.name.value = name;
}

apply(themeStore.current);
watch(() => themeStore.current, apply);
</script>
