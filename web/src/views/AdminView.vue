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
            <a :href="acc.vk_url || undefined" target="_blank" rel="noopener noreferrer" class="name-link ps-wrap">
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
        </div>
      </div>
    </v-card>
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
