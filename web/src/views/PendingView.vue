<template>
  <PublicLayout>
    <v-container class="py-12">
      <v-row justify="center">
        <v-col cols="12" sm="10" md="7" lg="6">
          <v-card class="pa-6 text-center">
            <v-icon
              :icon="isBlocked ? 'mdi-lock-alert' : 'mdi-account-clock'"
              :color="isBlocked ? 'error' : 'primary'"
              size="56"
              class="mb-4"
            />

            <template v-if="isBlocked">
              <h1 class="text-h5 mb-3">Доступ отозван</h1>
              <p class="text-body-1 text-medium-emphasis">
                Доступ отозван. Напиши админам, чтобы вернуть доступ.
              </p>
            </template>

            <template v-else>
              <h1 class="text-h5 mb-3">Ждём одобрения</h1>
              <p class="text-body-1 text-medium-emphasis mb-4">
                Попроси Сергея добавить тебя в allowlist. Покажи ему свой код:
              </p>

              <div v-if="handle" class="d-flex align-center justify-center ga-2 mb-2">
                <code class="handle-code">{{ handle }}</code>
                <v-btn
                  :icon="copied ? 'mdi-check' : 'mdi-content-copy'"
                  variant="text"
                  size="small"
                  :title="copied ? 'Скопировано' : 'Скопировать код'"
                  @click="copyHandle"
                />
              </div>
              <p v-else class="text-caption text-disabled">
                (код не передан — вернись на главную и войди заново)
              </p>
            </template>

            <v-divider class="my-5" />
            <v-btn variant="text" color="primary" :to="{ name: 'landing' }">
              На главную
            </v-btn>
          </v-card>
        </v-col>
      </v-row>
    </v-container>
  </PublicLayout>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import { useRoute } from 'vue-router';
import PublicLayout from '../components/layout/PublicLayout.vue';

const route = useRoute();

const handle = computed(() => {
  const h = route.query.handle;
  return Array.isArray(h) ? (h[0] ?? '') : (h ?? '');
});
const isBlocked = computed(() => route.query.status === 'blocked');

const copied = ref(false);
async function copyHandle() {
  if (!handle.value) return;
  try {
    await navigator.clipboard.writeText(handle.value);
    copied.value = true;
    setTimeout(() => (copied.value = false), 1500);
  } catch {
    /* clipboard blocked — the code is visible to copy by hand */
  }
}
</script>

<style scoped>
.handle-code {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 1.5rem;
  font-weight: 700;
  letter-spacing: 2px;
  padding: 8px 16px;
  border-radius: 10px;
  background: rgba(138, 92, 246, 0.14);
  color: rgb(var(--v-theme-primary));
  user-select: all;
}
</style>
