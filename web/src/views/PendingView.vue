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
                  aria-label="Скопировать код"
                  @click="copyHandle"
                />
              </div>
            </template>

            <p class="text-caption text-medium-emphasis mt-4 d-flex align-center justify-center ga-2">
              <v-progress-circular indeterminate size="14" width="2" />
              Страница обновляется автоматически
            </p>

            <v-divider class="my-5" />

            <div class="d-flex flex-wrap justify-center ga-2">
              <v-btn
                color="primary"
                variant="tonal"
                prepend-icon="mdi-refresh"
                :loading="checking"
                @click="check"
              >
                Проверить
              </v-btn>
              <v-btn variant="text" color="primary" @click="signOut">На главную</v-btn>
            </div>
          </v-card>
        </v-col>
      </v-row>
    </v-container>
  </PublicLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import PublicLayout from '../components/layout/PublicLayout.vue';
import { useAuthStore } from '../stores/auth';

const POLL_MS = 7000;

const auth = useAuthStore();
const router = useRouter();

const handle = computed(() => auth.account?.handle ?? '');
const isBlocked = computed(() => auth.account?.status === 'blocked');

const checking = ref(false);
const copied = ref(false);
let timer: ReturnType<typeof setInterval> | undefined;

// Re-check status: approved -> app, signed-out (401) -> landing, else stay.
async function check() {
  if (checking.value) return;
  checking.value = true;
  try {
    const acc = await auth.refresh();
    if (!acc) {
      await router.push({ name: 'landing' });
    } else if (acc.status === 'approved') {
      await router.push({ name: 'wishlist' });
    }
  } finally {
    checking.value = false;
  }
}

async function signOut() {
  await auth.logout();
  await router.push({ name: 'landing' });
}

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

onMounted(() => {
  timer = setInterval(check, POLL_MS);
});
onUnmounted(() => {
  if (timer) clearInterval(timer);
});
</script>

<style scoped>
.handle-code {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 1.5rem;
  font-weight: 700;
  letter-spacing: 2px;
  padding: 8px 16px;
  border-radius: 10px;
  background: rgba(45, 212, 191, 0.14);
  color: rgb(var(--v-theme-primary));
  user-select: all;
}
</style>
