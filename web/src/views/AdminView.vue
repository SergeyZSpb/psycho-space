<template>
  <v-container class="py-6" style="max-width: 1000px">
    <h1 class="text-h5 mb-4">Админка</h1>

    <!-- Registration settings — superadmin only. -->
    <v-card v-if="auth.isSuperadmin" class="pa-4 mb-6">
      <h2 class="text-subtitle-1 font-weight-bold mb-1">Настройки</h2>
      <v-switch
        :model-value="openRegistration"
        color="primary"
        density="comfortable"
        hide-details
        :loading="settingsLoading"
        :disabled="settingsSaving || settingsLoading"
        label="Открытая регистрация — новые пользователи одобряются автоматически (роль обычного пользователя)"
        @update:model-value="onToggleOpenRegistration"
      />
    </v-card>

    <v-tabs v-model="status" color="primary" class="mb-4">
      <v-tab value="pending">Ожидают</v-tab>
      <v-tab value="approved">Одобрены</v-tab>
      <v-tab value="blocked">Заблокированы</v-tab>
    </v-tabs>

    <div v-if="loading" class="text-center py-8">
      <v-progress-circular indeterminate color="primary" />
    </div>

    <v-alert v-else-if="accounts.length === 0" type="info" variant="tonal">
      Никого нет в этом статусе.
    </v-alert>

    <v-card v-for="acc in accounts" :key="acc.id" class="pa-4 mb-3">
      <div class="d-flex flex-wrap align-center ga-4">
        <v-avatar size="44" color="secondary">
          <v-img v-if="acc.avatar_url" :src="acc.avatar_url" alt="" />
          <span v-else>{{ initial(acc.display_name) }}</span>
        </v-avatar>

        <div class="flex-grow-1" style="min-width: 160px">
          <div class="d-flex align-center ga-2 flex-wrap">
            <!-- '' for a Яндекс account and for a forgotten one: no link, just
                 the name. -->
            <a :href="acc.profile_url || undefined" target="_blank" rel="noopener noreferrer" class="name-link ps-wrap">
              {{ acc.display_name }}
            </a>
            <v-chip size="x-small" :color="roleColor(acc.role)" variant="tonal">{{ acc.role }}</v-chip>
            <v-chip size="x-small" :color="statusColor(acc.status)" variant="tonal">{{ acc.status }}</v-chip>
          </div>
          <div class="text-caption text-medium-emphasis mt-1">
            код: <code>{{ acc.handle }}</code> · создан {{ formatDate(acc.created_at) }}
          </div>
        </div>

        <div class="d-flex ga-2 flex-wrap">
          <v-btn
            v-if="acc.status !== 'approved'"
            color="success"
            variant="tonal"
            size="small"
            prepend-icon="mdi-check"
            :loading="busyId === acc.id"
            @click="act(acc, 'approve')"
          >
            принять
          </v-btn>
          <v-btn
            v-if="acc.status !== 'blocked'"
            color="error"
            variant="tonal"
            size="small"
            prepend-icon="mdi-cancel"
            :loading="busyId === acc.id"
            @click="act(acc, 'block')"
          >
            отозвать доступ
          </v-btn>
          <!-- Role control: approved accounts only, superadmin only. -->
          <template v-if="auth.isSuperadmin && acc.status === 'approved'">
            <v-btn
              v-if="acc.role === 'user'"
              color="primary"
              variant="tonal"
              size="small"
              prepend-icon="mdi-shield-plus"
              :loading="busyId === acc.id"
              @click="act(acc, 'promote')"
            >
              Сделать админом
            </v-btn>
            <v-btn
              v-else-if="acc.role === 'admin'"
              color="warning"
              variant="tonal"
              size="small"
              prepend-icon="mdi-shield-off-outline"
              :loading="busyId === acc.id"
              @click="act(acc, 'demote')"
            >
              Разжаловать
            </v-btn>
            <v-chip
              v-else
              size="small"
              color="primary"
              variant="tonal"
              prepend-icon="mdi-shield-crown-outline"
            >
              суперадмин
            </v-chip>
          </template>

          <!-- Irreversible, so it is superadmin-only, never offered for a
               superadmin, and goes through a confirmation. -->
          <v-btn
            v-if="auth.isSuperadmin && acc.role !== 'superadmin'"
            color="error"
            variant="text"
            size="small"
            prepend-icon="mdi-account-off-outline"
            data-testid="admin-forget"
            :loading="busyId === acc.id"
            @click="askForget(acc)"
          >
            Забыть
          </v-btn>
        </div>
      </div>
    </v-card>

    <!-- Forget confirmation. Spells out both halves — what goes and what stays
         — because "забыть" sounds gentler than it is and the half that stays is
         the half people do not expect. -->
    <v-dialog v-model="confirmForgetOpen" max-width="460">
      <v-card data-testid="admin-forget-dialog">
        <v-card-title>Забыть пользователя?</v-card-title>
        <v-card-text class="ps-wrap">
          <p class="mb-2">
            «{{ pendingForget?.display_name }}» будет обезличен: имя, фото и связь
            с VK стираются навсегда. Отменить нельзя.
          </p>
          <p class="mb-2">
            Всё, что он написал, останется — идеи, комментарии, голоса и рекорды.
            Автором станет анонимный «psycho-…».
          </p>
          <p class="text-caption">
            Если он войдёт через VK снова — это будет совершенно новый аккаунт,
            который придётся одобрять заново.
          </p>
        </v-card-text>
        <!-- Both buttons carry an inline min-height: Vuetify's default in a
             dialog is 41 px, under the 44 px floor the layout suite enforces.
             INLINE rather than a scoped class, because a v-dialog teleports its
             content to the body and scoped CSS never reaches it — and the
             `height` prop is overridden by the component's own density. The
             cancel button gets it too: on an irreversible action the easy
             target should be the way out.

             48 rather than exactly 44: Vuetify's own borders and rounding shave
             a pixel or two off the measured box, so asking for the floor lands
             just under it. A destructive action deserves the bigger target
             anyway. -->
        <v-card-actions>
          <v-spacer />
          <v-btn
            variant="text"
            :style="{ minHeight: '48px' }"
            @click="confirmForgetOpen = false"
          >
            Отмена
          </v-btn>
          <v-btn
            color="error"
            variant="tonal"
            :style="{ minHeight: '48px' }"
            data-testid="admin-forget-confirm"
            :loading="forgetting"
            @click="confirmForget"
          >
            Забыть
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </v-container>
</template>

<script setup lang="ts">
import { onMounted, ref, watch } from 'vue';
import { adminApi } from '../api/endpoints';
import { useAuthStore } from '../stores/auth';
import { useErrorStore } from '../stores/error';
import type { AdminAccount, AccountStatus, Role } from '../api/types';

const auth = useAuthStore();
const errorStore = useErrorStore();

const status = ref<AccountStatus>('pending');
const accounts = ref<AdminAccount[]>([]);
const loading = ref(false);
const busyId = ref<string | null>(null);

// Open-registration setting (read by any admin; toggled by superadmin only).
const openRegistration = ref(false);
const settingsLoading = ref(false);
const settingsSaving = ref(false);

async function loadSettings() {
  settingsLoading.value = true;
  try {
    const res = await adminApi.settings();
    openRegistration.value = res.open_registration;
  } catch (err) {
    errorStore.report(err);
  } finally {
    settingsLoading.value = false;
  }
}

async function onToggleOpenRegistration(value: boolean | null) {
  const enabled = value === true;
  settingsSaving.value = true;
  try {
    const res = await adminApi.setOpenRegistration(enabled);
    openRegistration.value = res.open_registration;
  } catch (err) {
    // On failure (e.g. 403) keep the switch on the last known server value.
    errorStore.report(err);
  } finally {
    settingsSaving.value = false;
  }
}

function initial(name: string): string {
  const n = name.trim();
  return n ? n.charAt(0).toUpperCase() : '?';
}

function roleColor(role: Role): string {
  if (role === 'superadmin') return 'primary';
  if (role === 'admin') return 'secondary';
  return 'grey';
}

function statusColor(s: AccountStatus): string {
  if (s === 'approved') return 'success';
  if (s === 'blocked') return 'error';
  return 'warning';
}

function formatDate(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleDateString('ru-RU');
}

async function load() {
  loading.value = true;
  try {
    const res = await adminApi.list(status.value);
    accounts.value = res.accounts;
  } catch (err) {
    errorStore.report(err);
  } finally {
    loading.value = false;
  }
}

const confirmForgetOpen = ref(false);
const pendingForget = ref<AdminAccount | null>(null);
const forgetting = ref(false);

function askForget(acc: AdminAccount) {
  pendingForget.value = acc;
  confirmForgetOpen.value = true;
}

/**
 * Anonymises the person and keeps their contributions.
 *
 * The dialog stays open when this fails — the global error modal surfaces the
 * code and the trace id, and closing would hide the fact that nothing happened.
 * It closes only on success, which is the same shape the wishlist's delete
 * confirmation uses.
 */
async function confirmForget() {
  const acc = pendingForget.value;
  if (!acc) return;
  forgetting.value = true;
  try {
    await adminApi.forget(acc.id);
    confirmForgetOpen.value = false;
    pendingForget.value = null;
    await load();
  } catch (err) {
    // 403 cannot_modify_self / cannot_modify_superadmin, 409 already_forgotten.
    errorStore.report(err);
  } finally {
    forgetting.value = false;
  }
}

async function act(acc: AdminAccount, action: 'approve' | 'block' | 'promote' | 'demote') {
  busyId.value = acc.id;
  try {
    if (action === 'approve') await adminApi.approve(acc.id);
    else if (action === 'block') await adminApi.block(acc.id);
    else if (action === 'promote') await adminApi.promote(acc.id);
    else await adminApi.demote(acc.id);
    await load();
  } catch (err) {
    // The backend returns 403 when an admin tries to act on an admin/superadmin;
    // the global modal surfaces the code + trace id so the reason is visible.
    errorStore.report(err);
  } finally {
    busyId.value = null;
  }
}

watch(status, load);
onMounted(() => {
  void load();
  void loadSettings(); // any admin may read the current setting
});
</script>

<style scoped>
.name-link {
  color: rgb(var(--v-theme-primary));
  text-decoration: none;
  font-weight: 600;
}
.name-link:hover {
  text-decoration: underline;
}
</style>
