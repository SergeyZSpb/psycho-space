<template>
  <v-dialog v-model="errorStore.open" max-width="480" persistent>
    <v-card>
      <v-card-title class="d-flex align-center ga-2">
        <v-icon color="error" icon="mdi-alert-circle" />
        <span>Ой, ошибка</span>
      </v-card-title>

      <v-card-text>
        <p class="mb-3">
          Что-то пошло не так. Код ошибки:
          <strong>{{ errorStore.code }}</strong>
          <span v-if="errorStore.status"> (статус {{ errorStore.status }})</span>.
        </p>

        <p class="mb-2 text-body-2">
          Отправь этот код Сергею, чтобы он разобрался:
        </p>

        <v-text-field
          :model-value="errorStore.traceId || '—'"
          label="trace_id"
          readonly
          density="compact"
          variant="outlined"
          class="trace-field"
          hide-details
          @focus="selectAll"
        >
          <template #append-inner>
            <v-btn
              size="small"
              variant="text"
              :icon="copied ? 'mdi-check' : 'mdi-content-copy'"
              :title="copied ? 'Скопировано' : 'Скопировать'"
              @click="copyTrace"
            />
          </template>
        </v-text-field>
      </v-card-text>

      <v-card-actions>
        <v-spacer />
        <v-btn variant="text" @click="copyTrace">Скопировать</v-btn>
        <v-btn color="primary" variant="tonal" @click="errorStore.close()">Закрыть</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import { useErrorStore } from '../stores/error';

const errorStore = useErrorStore();
const copied = ref(false);

function selectAll(e: FocusEvent) {
  (e.target as HTMLInputElement | null)?.select();
}

async function copyTrace() {
  const value = errorStore.traceId;
  if (!value) return;
  try {
    await navigator.clipboard.writeText(value);
    copied.value = true;
    setTimeout(() => (copied.value = false), 1500);
  } catch {
    /* clipboard blocked — the field is selectable as a fallback */
  }
}
</script>

<style scoped>
.trace-field :deep(input) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 0.85rem;
}
</style>
